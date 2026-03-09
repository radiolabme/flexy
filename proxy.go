//go:build unix

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	log "github.com/rs/zerolog/log"
	"golang.org/x/sys/unix"
)

const (
	flexOUI            = uint32(0x001c2d)
	discoveryClassCode = uint16(0xffff)
	smartsdrTCPPort    = "4992"
)

// tailscaleCGNAT is the CGNAT range used by Tailscale for its peer addresses.
var tailscaleCGNAT = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n
}()

// isTailscaleIP reports whether ip is a Tailscale peer address (100.64.0.0/10).
func isTailscaleIP(ip net.IP) bool {
	return tailscaleCGNAT != nil && tailscaleCGNAT.Contains(ip)
}

// proxyBindIP returns the IP to bind when dialing the radio (RadioBindIP),
// falling back to nil (OS chooses) if unset.
func proxyBindIP() net.IP {
	if cfg.RadioBindIP == "" {
		return nil
	}
	return net.ParseIP(cfg.RadioBindIP)
}

var (
	discoveryMu     sync.RWMutex
	lastDiscoveryKV map[string]string
	lastBcastAddr   string
)

// parseDiscoveryPayload parses VITA-49 discovery packet key=value payload.
func parseDiscoveryPayload(s string) map[string]string {
	result := map[string]string{}
	s = strings.Trim(s, " \x00")
	for _, part := range strings.Fields(s) {
		if idx := strings.IndexByte(part, '='); idx >= 0 {
			result[part[:idx]] = part[idx+1:]
		}
	}
	return result
}

// buildDiscoveryPacket encodes a VITA-49 discovery packet from key-value pairs.
func buildDiscoveryPacket(kv map[string]string) []byte {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(kv))
	for _, k := range keys {
		// Spaces in values are encoded as 0x7f in the FlexRadio protocol.
		v := strings.ReplaceAll(kv[k], " ", "\x7f")
		parts = append(parts, k+"="+v)
	}
	payload := []byte(strings.Join(parts, " "))

	// Pad payload to 4-byte boundary.
	for len(payload)%4 != 0 {
		payload = append(payload, 0)
	}

	totalWords := 1 + 2 + len(payload)/4 // header + class ID (2 words) + payload

	// Header: PacketType=ExtContext(5), C=1, T=0, TSI=0, TSF=0, count=0
	headerWord := uint32(5<<28) | (1 << 27) | uint32(totalWords)

	buf := make([]byte, 4+8+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], headerWord)
	binary.BigEndian.PutUint32(buf[4:8], flexOUI)                  // OUI in low 24 bits
	binary.BigEndian.PutUint32(buf[8:12], uint32(discoveryClassCode)) // PCC in low 16 bits
	copy(buf[12:], payload)
	return buf
}

// discoveryListenReusePort opens UDP :4992 with SO_REUSEPORT so it can coexist
// with the flexclient listener on the same port.
func discoveryListenReusePort() (*net.UDPConn, error) {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var opErr error
			c.Control(func(fd uintptr) {
				opErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
			})
			return opErr
		},
	}
	conn, err := lc.ListenPacket(context.Background(), "udp", ":4992")
	if err != nil {
		return nil, err
	}
	return conn.(*net.UDPConn), nil
}

// getLocalIP returns the local IP address used to reach the radio.
func getLocalIP() string {
	conn, err := net.Dial("udp", cfg.RadioIP+":4992")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// getBroadcastAddr returns the subnet broadcast address for the interface
// that holds the given IP, falling back to 255.255.255.255.
func getBroadcastAddr(ip string) net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return net.IPv4bcast
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.IP.String() == ip {
				ip4 := ipNet.IP.To4()
				mask := ipNet.Mask
				if ip4 == nil || len(mask) != 4 {
					continue
				}
				bcast := make(net.IP, 4)
				for i := range bcast {
					bcast[i] = ip4[i] | ^mask[i]
				}
				return bcast
			}
		}
	}
	return net.IPv4bcast
}

// parseInfoResponse parses the comma-separated key=value (with quoted values)
// response from the radio's "info" command into a map.
func parseInfoResponse(s string) map[string]string {
	result := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		idx := strings.IndexByte(part, '=')
		if idx < 0 {
			continue
		}
		k := part[:idx]
		v := strings.Trim(part[idx+1:], `"`)
		result[k] = v
	}
	return result
}

// buildDiscoveryKV constructs the key-value map for the discovery packet
// using info from the radio's TCP API.
func buildDiscoveryKV(proxyIP string) map[string]string {
	kv := map[string]string{
		"ip":     proxyIP,
		"port":   "4992",
		"status": "Available",
	}

	if fc != nil {
		res := fc.SendAndWait("info")
		if res.Error == 0 && res.Message != "" {
			info := parseInfoResponse(res.Message)
			for src, dst := range map[string]string{
				"model":        "model",
				"chassis_serial": "serial",
				"name":         "nickname",
				"callsign":     "callsign",
				"software_ver": "version",
			} {
				if v := info[src]; v != "" {
					kv[dst] = v
				}
			}
		}
	}

	if nick := kv["nickname"]; nick != "" {
		kv["nickname"] = nick + " [Flexy]"
	}

	return kv
}

// lanBroadcastAddrs returns the broadcast address for every non-Tailscale,
// non-loopback, up IPv4 interface. Used so discovery reaches LAN clients
// regardless of which interface holds the proxy IP.
func lanBroadcastAddrs() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil || isTailscaleIP(ip4) {
				continue
			}
			mask := ipNet.Mask
			if len(mask) != 4 {
				continue
			}
			bcast := make(net.IP, 4)
			for i := range bcast {
				bcast[i] = ip4[i] | ^mask[i]
			}
			out = append(out, bcast)
		}
	}
	return out
}

// tailscaleOnlinePeers returns the Tailscale IPs of all currently-online peers
// by running `tailscale status --json`. Returns nil on any error.
func tailscaleOnlinePeers() []net.IP {
	out, err := exec.Command("tailscale", "status", "--json").Output()
	if err != nil {
		return nil
	}
	var status struct {
		Peer map[string]struct {
			TailscaleIPs []string `json:"TailscaleIPs"`
			Online       bool     `json:"Online"`
		} `json:"Peer"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return nil
	}
	var peers []net.IP
	for _, peer := range status.Peer {
		if !peer.Online {
			continue
		}
		for _, ipStr := range peer.TailscaleIPs {
			if ip := net.ParseIP(ipStr); ip != nil {
				peers = append(peers, ip)
			}
		}
	}
	return peers
}

// startDiscoveryRelay periodically broadcasts a VITA-49 discovery packet
// advertising Flexy as a proxy for the connected radio. Broadcasts on all
// non-Tailscale LAN interfaces so clients on the local network can discover it.
func startDiscoveryRelay(ctx context.Context, proxyIP string) {
	sendConn, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		log.Error().Err(err).Msg("Discovery relay: failed to open send socket")
		return
	}
	defer sendConn.Close()

	log.Info().Str("ctx", "proxy").Str("proto", "UDP").Str("proxy_ip", proxyIP).Msg("Discovery relay active")

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state, _ := getConnState()
			if state != ConnStateConnected {
				continue
			}
			kv := buildDiscoveryKV(proxyIP)
			bcastAddrs := lanBroadcastAddrs()
			var bcastStrs []string
			for _, b := range bcastAddrs {
				bcastStrs = append(bcastStrs, b.String())
			}
			discoveryMu.Lock()
			lastDiscoveryKV = kv
			lastBcastAddr = strings.Join(bcastStrs, ", ")
			discoveryMu.Unlock()
			pkt := buildDiscoveryPacket(kv)
			for _, bcastIP := range bcastAddrs {
				bcastAddr := &net.UDPAddr{IP: bcastIP, Port: 4992}
				if _, err := sendConn.WriteTo(pkt, bcastAddr); err != nil {
					log.Debug().Err(err).Str("broadcast", bcastIP.String()).Msg("Discovery relay: broadcast failed")
				}
			}
			// Unicast to all online Tailscale peers so remote SmartSDR clients
			// can discover Flexy without needing to receive the LAN broadcast.
			for _, peerIP := range tailscaleOnlinePeers() {
				peerAddr := &net.UDPAddr{IP: peerIP, Port: 4992}
				if _, err := sendConn.WriteTo(pkt, peerAddr); err != nil {
					log.Debug().Err(err).Str("peer", peerIP.String()).Msg("Discovery relay: unicast failed")
				}
			}
		}
	}
}

// udpPortRe matches FlexRadio "client udpport N" commands in the TCP stream.
var udpPortRe = regexp.MustCompile(`^(C\d+\|client udpport )(\d+)\s*$`)

// regexes for tracking client-owned pans and slices for cleanup on disconnect.
var (
	proxyHandleRe    = regexp.MustCompile(`^H([0-9A-Fa-f]+)$`)
	proxyPanOwnerRe  = regexp.MustCompile(`\|display pan (0x[0-9A-Fa-f]+) .*client_handle=0x([0-9A-Fa-f]+)`)
	proxySliceOwnerRe = regexp.MustCompile(`\|slice (\d+) .*client_handle=0x([0-9A-Fa-f]+)`)
)

// startUDPRelay opens a local UDP port bound to bindIP (nil = all interfaces),
// forwards received packets to destIP:destPort, and closes when done is closed.
// Returns the local port, a packet counter, and any error.
func startUDPRelay(bindIP net.IP, destIP string, destPort int, done <-chan struct{}) (int, *UDPRelayCounter, error) {
	localConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: bindIP, Port: 0})
	if err != nil {
		return 0, nil, err
	}

	localPort := localConn.LocalAddr().(*net.UDPAddr).Port
	destAddr := &net.UDPAddr{IP: net.ParseIP(destIP), Port: destPort}
	counter := &UDPRelayCounter{
		ClientAddr: fmt.Sprintf("%s:%d", destIP, destPort),
		RelayPort:  localPort,
	}

	// Punch a hole in any intermediate NAT by sending a keepalive to the
	// radio's VITA-49 port so the radio can send UDP back through NAT.
	if radioIP := net.ParseIP(cfg.RadioIP); radioIP != nil {
		radioVITAAddr := &net.UDPAddr{IP: radioIP, Port: 4991}
		localConn.WriteTo([]byte{0}, radioVITAAddr)
		go func() {
			ticker := time.NewTicker(25 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					localConn.WriteTo([]byte{0}, radioVITAAddr)
				}
			}
		}()
	}

	go func() {
		defer localConn.Close()
		buf := make([]byte, 65536)
		for {
			n, _, err := localConn.ReadFrom(buf)
			if err != nil {
				return
			}
			localConn.WriteTo(buf[:n], destAddr)
			counter.record()
			if counter.packetsRx.Load() == 1 {
				log.Info().Str("ctx", "proxy").Str("proto", "UDP").Str("dir", "→").Str("dest", destAddr.String()).Msg("UDP relay: packets flowing")
			}
		}
	}()

	go func() {
		<-done
		localConn.Close()
	}()

	return localPort, counter, nil
}

// handleSmartSDRClient proxies one SmartSDR TCP connection to the real radio,
// intercepting client udpport commands to set up a VITA-49 UDP relay.
func handleSmartSDRClient(clientConn net.Conn) {
	defer clientConn.Close()

	clientAddr := clientConn.RemoteAddr().(*net.TCPAddr)
	radioAddr := cfg.RadioIP + ":" + smartsdrTCPPort
	log.Info().Str("ctx", "proxy").Str("proto", "TCP").Str("dir", "←").Str("client", clientAddr.String()).Msg("SmartSDR client connected")

	bindIP := proxyBindIP()
	dialer := &net.Dialer{}
	if bindIP != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: bindIP}
	}
	radioConn, err := dialer.Dial("tcp", radioAddr)
	if err != nil {
		log.Error().Err(err).Msg("SmartSDR proxy: failed to connect to radio")
		return
	}
	defer radioConn.Close()

	localTCPAddr := radioConn.LocalAddr().(*net.TCPAddr)
	log.Info().Str("ctx", "proxy").Str("proto", "TCP").
		Str("local", localTCPAddr.String()).
		Str("radio", radioAddr).
		Bool("tailscale", isTailscaleIP(localTCPAddr.IP)).
		Msg("Proxy TCP local address (radio will send UDP here)")

	conn := registerProxyConn(clientAddr.String(), radioAddr)
	defer unregisterProxyConn(conn)

	done := make(chan struct{})
	defer close(done)

	// Track client-owned pans and slices so we can remove them on disconnect.
	// This forces SmartSDR to do a full re-init (including client udpport) on
	// the next connection instead of silently restoring a stale session.
	var myHandle string
	ownedPans := map[string]struct{}{}
	ownedSlices := map[string]struct{}{}
	defer func() {
		if myHandle == "" || (len(ownedPans) == 0 && len(ownedSlices) == 0) {
			return
		}
		log.Info().Str("ctx", "proxy").Str("handle", myHandle).
			Int("pans", len(ownedPans)).Int("slices", len(ownedSlices)).
			Msg("Proxy cleanup: removing client pans and slices on disconnect")
		_ = radioConn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		for pan := range ownedPans {
			fmt.Fprintf(radioConn, "display pan remove %s\n", pan)
		}
		for slice := range ownedSlices {
			fmt.Fprintf(radioConn, "slice remove %s\n", slice)
		}
		_ = radioConn.SetWriteDeadline(time.Time{})
	}()

	// client → radio: scan line by line, intercept udpport commands.
	go func() {
		scanner := bufio.NewScanner(clientConn)
		for scanner.Scan() {
			line := scanner.Text()
			log.Debug().Str("ctx", "proxy").Str("proto", "TCP").Str("dir", "→").Str("line", line).Msg("proxy cmd")
			if m := udpPortRe.FindStringSubmatch(line); m != nil {
				clientPort, _ := strconv.Atoi(m[2])
				localPort, counter, err := startUDPRelay(bindIP, clientAddr.IP.String(), clientPort, done)
				if err != nil {
					log.Error().Err(err).Msg("SmartSDR proxy: UDP relay setup failed")
				} else {
					conn.setRelay(counter)
					log.Info().
						Str("ctx", "proxy").Str("proto", "UDP").
						Str("client", fmt.Sprintf("%s:%d", clientAddr.IP, clientPort)).
						Int("relay_port", localPort).
						Msg("UDP relay: radio→Flexy:relay_port→client")
					line = m[1] + strconv.Itoa(localPort)
				}
			}
			if _, err := fmt.Fprintf(radioConn, "%s\n", line); err != nil {
				return
			}
		}
		radioConn.Close()
	}()

	// radio → client: rewrite the radio's own IP in info responses so SmartSDR
	// sees an IP consistent with what it connected to and doesn't drop the session.
	proxyIPStr := cfg.ProxyIP
	if proxyIPStr == "" {
		proxyIPStr = getLocalIP()
	}
	radioScanner := bufio.NewScanner(radioConn)
	radioScanner.Buffer(make([]byte, 256*1024), 256*1024)
	for radioScanner.Scan() {
		line := radioScanner.Text()
		if proxyIPStr != "" && cfg.RadioIP != "" {
			line = strings.ReplaceAll(line, "ip="+cfg.RadioIP, "ip="+proxyIPStr)
		}
		// Track handle and owned pans/slices for cleanup on disconnect.
		if myHandle == "" {
			if m := proxyHandleRe.FindStringSubmatch(line); m != nil {
				myHandle = strings.ToUpper(m[1])
			}
		}
		if myHandle != "" {
			if m := proxyPanOwnerRe.FindStringSubmatch(line); m != nil && strings.EqualFold(m[2], myHandle) {
				ownedPans[m[1]] = struct{}{}
			}
			if m := proxySliceOwnerRe.FindStringSubmatch(line); m != nil && strings.EqualFold(m[2], myHandle) {
				ownedSlices[m[1]] = struct{}{}
			}
		}
		log.Debug().Str("ctx", "proxy").Str("proto", "TCP").Str("dir", "←").Str("line", line).Msg("proxy resp")
		if _, err := fmt.Fprintf(clientConn, "%s\n", line); err != nil {
			break
		}
	}

	log.Info().Str("ctx", "proxy").Str("proto", "TCP").Str("client", clientAddr.String()).Msg("SmartSDR client disconnected")
}

// startSmartSDRProxy listens for SmartSDR TCP connections and proxies them.
func startSmartSDRProxy(ctx context.Context, listen string) {
	l, err := net.Listen("tcp", listen)
	if err != nil {
		log.Error().Err(err).Str("addr", listen).Msg("SmartSDR proxy: failed to listen")
		return
	}

	go func() {
		<-ctx.Done()
		l.Close()
	}()

	log.Info().Str("addr", listen).Msg("SmartSDR proxy listening")

	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go handleSmartSDRClient(conn)
	}
}
