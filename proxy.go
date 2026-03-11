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

// clientIPCmdRe matches a "client ip" command so we can track the sequence
// number and rewrite the response with the real client IP.
var clientIPCmdRe = regexp.MustCompile(`^C(\d+)\|client ip\s*$`)

// clientIPRespRe matches the radio's response to "client ip".
var clientIPRespRe = regexp.MustCompile(`^(R)(\d+)(\|0\|)(\S+)\s*$`)

// clientProgramRe extracts the program name from "client program <name>".
var clientProgramRe = regexp.MustCompile(`^C\d+\|client program (.+)$`)

// clientStationRe extracts the station name from "client station <name>".
var clientStationRe = regexp.MustCompile(`^C\d+\|client station (.+)$`)

// clientStatusRe extracts fields from radio client status updates.
var clientStatusRe = regexp.MustCompile(`\|client 0x([0-9A-Fa-f]+) connected .* client_id=([0-9A-Fa-f-]+)`)

// parseDiscoveryPayload parses VITA-49 discovery packet key=value payload.
// 0x7f is the FlexRadio-encoded space character and is decoded to a real space.
func parseDiscoveryPayload(s string) map[string]string {
	result := map[string]string{}
	s = strings.Trim(s, " \x00")
	for _, part := range strings.Fields(s) {
		if idx := strings.IndexByte(part, '='); idx >= 0 {
			v := strings.ReplaceAll(part[idx+1:], "\x7f", " ")
			result[part[:idx]] = v
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
	binary.BigEndian.PutUint32(buf[4:8], flexOUI)                     // OUI in low 24 bits
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

// tailscalePeer holds the machine name and all IPv4 addresses for one online
// Tailscale peer.
type tailscalePeer struct {
	Name string
	IPs  []net.IP
}

var (
	tsPeerCacheMu  sync.Mutex
	tsPeerCache    []tailscalePeer
	tsPeerCacheAt  time.Time
	tsPeerCacheTTL = 10 * time.Second
)

// tailscaleOnlinePeers returns all currently-online Tailscale peers with their
// machine name and IPv4 addresses. Results are cached for 10 s to avoid
// spawning a subprocess on every radio broadcast.
func tailscaleOnlinePeers() []tailscalePeer {
	tsPeerCacheMu.Lock()
	defer tsPeerCacheMu.Unlock()
	if time.Since(tsPeerCacheAt) < tsPeerCacheTTL {
		return tsPeerCache
	}
	tsPeerCache = tailscaleStatusPeers()
	tsPeerCacheAt = time.Now()
	return tsPeerCache
}

func tailscaleStatusPeers() []tailscalePeer {
	out, err := exec.Command("tailscale", "status", "--json").Output()
	if err != nil {
		return nil
	}
	var status struct {
		Peer map[string]struct {
			HostName     string   `json:"HostName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
			Addrs        []string `json:"Addrs"` // WireGuard endpoints, "ip:port"
			Online       bool     `json:"Online"`
		} `json:"Peer"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return nil
	}
	var peers []tailscalePeer
	for _, peer := range status.Peer {
		if !peer.Online {
			continue
		}
		seen := map[string]bool{}
		addIPv4 := func(ipStr string) {
			if seen[ipStr] {
				return
			}
			ip := net.ParseIP(ipStr)
			if ip == nil || ip.To4() == nil {
				return
			}
			seen[ipStr] = true
			peers[len(peers)-1].IPs = append(peers[len(peers)-1].IPs, ip)
		}
		peers = append(peers, tailscalePeer{Name: peer.HostName})
		for _, ipStr := range peer.TailscaleIPs {
			addIPv4(ipStr)
		}
		for _, addr := range peer.Addrs {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				continue
			}
			addIPv4(host)
		}
		if len(peers[len(peers)-1].IPs) == 0 {
			peers = peers[:len(peers)-1]
		}
	}
	return peers
}

// startDiscoveryRelay listens for VITA-49 discovery broadcasts from the radio,
// rewrites the radio's IP to the proxy IP, and re-broadcasts to all LAN
// interfaces and unicasts to all online Tailscale peers. This preserves every
// field in the radio's original packet (including gui_client_handles etc.)
// so that SmartSDR DAX/CAT can discover connected stations.
func startDiscoveryRelay(ctx context.Context, proxyIP string) {
	recvConn, err := discoveryListenReusePort()
	if err != nil {
		log.Error().Err(err).Msg("Discovery relay: failed to listen on :4992")
		return
	}
	defer recvConn.Close()

	sendConn, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		log.Error().Err(err).Msg("Discovery relay: failed to open send socket")
		return
	}
	defer sendConn.Close()

	go func() {
		<-ctx.Done()
		recvConn.Close()
	}()

	log.Info().Str("ctx", "proxy").Str("proto", "UDP").Str("proxy_ip", proxyIP).Msg("Discovery relay active")

	logged := false
	buf := make([]byte, 65536)
	for {
		n, addr, err := recvConn.ReadFrom(buf)
		if err != nil {
			return
		}
		udpAddr, ok := addr.(*net.UDPAddr)
		if !ok || udpAddr.IP.String() != cfg.RadioIP {
			continue // only relay the radio's own discovery broadcasts
		}
		if n < 12 {
			continue
		}

		// Parse the radio's payload (bytes 12+), rewrite ip= and annotate nickname.
		kv := parseDiscoveryPayload(string(buf[12:n]))
		if len(kv) == 0 {
			continue
		}

		// Log full discovery fields once on first packet from radio.
		if !logged {
			logged = true
			ev := log.Info().Str("ctx", "proxy").Int("fields", len(kv))
			for k, v := range kv {
				ev = ev.Str(k, v)
			}
			ev.Msg("Discovery relay: first packet from radio")
		}

		kv["ip"] = proxyIP
		if nick := kv["nickname"]; nick != "" {
			kv["nickname"] = nick + " [Flexy]"
		}

		// Rewrite gui_client_ips so companion apps (Smart CAT, Smart DAX)
		// can match their own IP against the discovery data and find the
		// correct station to bind to.
		if rc := getRadioContext(); rc != nil {
			rc.RewriteDiscoveryIPs(kv)
		}

		if log.Debug().Enabled() {
			if h, ok := kv["gui_client_handles"]; ok {
				log.Debug().Str("ctx", "proxy").
					Str("gui_client_handles", h).
					Str("gui_client_ips", kv["gui_client_ips"]).
					Msg("Discovery relay: client fields after rewrite")
			}
		}

		pkt := buildDiscoveryPacket(kv)

		bcastAddrs := lanBroadcastAddrs()
		var bcastStrs []string
		for _, b := range bcastAddrs {
			bcastStrs = append(bcastStrs, b.String())
		}

		peers := tailscaleOnlinePeers()
		var unicastStrs []string
		for _, peer := range peers {
			var ipStrs []string
			for _, ip := range peer.IPs {
				ipStrs = append(ipStrs, ip.String())
			}
			unicastStrs = append(unicastStrs, peer.Name+" ("+strings.Join(ipStrs, ", ")+")")
		}

		if rc := getRadioContext(); rc != nil {
			rc.SetDiscovery(kv, strings.Join(bcastStrs, ", "), unicastStrs)
		}

		for _, bcastIP := range bcastAddrs {
			if _, err := sendConn.WriteTo(pkt, &net.UDPAddr{IP: bcastIP, Port: 4992}); err != nil {
				log.Debug().Err(err).Str("broadcast", bcastIP.String()).Msg("Discovery relay: broadcast failed")
			}
		}
		for _, peer := range peers {
			for _, peerIP := range peer.IPs {
				if _, err := sendConn.WriteTo(pkt, &net.UDPAddr{IP: peerIP, Port: 4992}); err != nil {
					log.Debug().Err(err).Str("peer", peerIP.String()).Msg("Discovery relay: unicast failed")
				}
			}
		}
	}
}

// udpPortRe matches FlexRadio "client udpport N" commands in the TCP stream.
var udpPortRe = regexp.MustCompile(`^(C\d+\|client udpport )(\d+)\s*$`)

// emptyBindRe matches "client bind client_id=" with nothing after the equals.
var emptyBindRe = regexp.MustCompile(`^C\d+\|client bind client_id=\s*$`)

// pingLineRe matches FlexRadio ping commands and their responses so they can
// be excluded from debug logging (they fire every few seconds and are noisy).
var pingLineRe = regexp.MustCompile(`^C\d+\|ping$|^R\d+\|0\|ping$`)

// regexes for tracking client-owned pans and slices for cleanup on disconnect.
var (
	proxyHandleRe     = regexp.MustCompile(`^H([0-9A-Fa-f]+)$`)
	proxyPanOwnerRe   = regexp.MustCompile(`\|display pan (0x[0-9A-Fa-f]+) .*client_handle=0x([0-9A-Fa-f]+)`)
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
// intercepting client udpport commands to set up a VITA-49 UDP relay, and
// rewriting IP references so companion apps can resolve stations correctly.
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

	done := make(chan struct{})
	defer close(done)

	// ProxyClient will be registered in RadioContext once we learn our handle.
	pc := &ProxyClient{
		RemoteIP:    clientAddr.IP,
		RemoteAddr:  clientAddr.String(),
		ConnectedAt: time.Now(),
		OwnedPans:   make(map[string]struct{}),
		OwnedSlices: make(map[string]struct{}),
	}

	defer func() {
		// Unregister from RadioContext.
		if pc.Handle != "" {
			if rc := getRadioContext(); rc != nil {
				rc.UnregisterClient(pc.Handle)
			}
		}
		// Cleanup owned resources.
		if pc.Handle == "" || (len(pc.OwnedPans) == 0 && len(pc.OwnedSlices) == 0) {
			return
		}
		// Skip cleanup if other proxy clients are still connected to this radio.
		if rc := getRadioContext(); rc != nil {
			rc.mu.RLock()
			otherConns := len(rc.Clients) > 0
			rc.mu.RUnlock()
			if otherConns {
				log.Info().Str("ctx", "proxy").Str("handle", pc.Handle).
					Msg("Proxy cleanup skipped: other clients still connected")
				return
			}
		}
		log.Info().Str("ctx", "proxy").Str("handle", pc.Handle).
			Int("pans", len(pc.OwnedPans)).Int("slices", len(pc.OwnedSlices)).
			Msg("Proxy cleanup: removing client pans and slices on disconnect")
		_ = radioConn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		for pan := range pc.OwnedPans {
			fmt.Fprintf(radioConn, "display pan remove %s\n", pan)
		}
		for slice := range pc.OwnedSlices {
			fmt.Fprintf(radioConn, "slice remove %s\n", slice)
		}
		_ = radioConn.SetWriteDeadline(time.Time{})
	}()

	// Track "client ip" command sequence numbers so we can rewrite the
	// response with the real client IP instead of the proxy LAN IP.
	var clientIPSeqMu sync.Mutex
	clientIPSeqs := map[string]bool{}

	// client → radio: scan line by line, intercept udpport and track metadata.
	go func() {
		scanner := bufio.NewScanner(clientConn)
		for scanner.Scan() {
			line := scanner.Text()
			if cfg.LogPings || !pingLineRe.MatchString(line) {
				log.Debug().Str("ctx", "proxy").Str("proto", "TCP").Str("dir", "→").Str("line", line).Msg("proxy cmd")
			}
			if emptyBindRe.MatchString(line) {
				log.Info().Str("ctx", "proxy").Str("line", line).Msg("Empty client bind (passing through)")
			}
			// Track "client ip" command seq for response rewrite.
			if m := clientIPCmdRe.FindStringSubmatch(line); m != nil {
				clientIPSeqMu.Lock()
				clientIPSeqs[m[1]] = true
				clientIPSeqMu.Unlock()
			}
			if m := clientProgramRe.FindStringSubmatch(line); m != nil {
				pc.Program = strings.ReplaceAll(m[1], "\x7f", " ")
			}
			if m := clientStationRe.FindStringSubmatch(line); m != nil {
				pc.Station = strings.ReplaceAll(m[1], "\x7f", " ")
			}
			if m := udpPortRe.FindStringSubmatch(line); m != nil {
				clientPort, _ := strconv.Atoi(m[2])
				if clientPort == 0 {
					log.Debug().Str("ctx", "proxy").Str("proto", "UDP").Msg("UDP deregister (port 0): passing through")
				} else {
					localPort, counter, err := startUDPRelay(bindIP, clientAddr.IP.String(), clientPort, done)
					if err != nil {
						log.Error().Err(err).Msg("SmartSDR proxy: UDP relay setup failed")
					} else {
						pc.Relay = counter
						log.Info().
							Str("ctx", "proxy").Str("proto", "UDP").
							Str("client", fmt.Sprintf("%s:%d", clientAddr.IP, clientPort)).
							Int("relay_port", localPort).
							Msg("UDP relay: radio→Flexy:relay_port→client")
						line = m[1] + strconv.Itoa(localPort)
					}
				}
			}
			if _, err := fmt.Fprintf(radioConn, "%s\n", line); err != nil {
				return
			}
		}
		radioConn.SetReadDeadline(time.Now())
	}()

	// radio → client: rewrite IPs so SmartSDR and companion apps see
	// consistent addresses matching what they connected to.
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

		// Rewrite "client ip" response: replace proxy LAN IP with real client IP.
		if m := clientIPRespRe.FindStringSubmatch(line); m != nil {
			seq := m[2]
			clientIPSeqMu.Lock()
			isClientIP := clientIPSeqs[seq]
			delete(clientIPSeqs, seq)
			clientIPSeqMu.Unlock()
			if isClientIP {
				oldIP := m[4]
				newIP := clientAddr.IP.String()
				if oldIP != newIP {
					line = m[1] + m[2] + m[3] + newIP
					log.Debug().Str("ctx", "proxy").
						Str("old_ip", oldIP).Str("new_ip", newIP).
						Msg("Rewrote client ip response")
				}
			}
		}

		// Track handle from the H-line and register in RadioContext.
		if pc.Handle == "" {
			if m := proxyHandleRe.FindStringSubmatch(line); m != nil {
				pc.Handle = strings.ToUpper(m[1])
				if rc := getRadioContext(); rc != nil {
					rc.RegisterClient(pc)
				}
				log.Info().Str("ctx", "proxy").
					Str("handle", pc.Handle).
					Str("client", clientAddr.String()).
					Msg("Proxy client handle assigned")
			}
		}

		// Track client_id from radio status updates.
		if pc.Handle != "" {
			if m := clientStatusRe.FindStringSubmatch(line); m != nil && strings.EqualFold(m[1], pc.Handle) {
				pc.ClientID = m[2]
			}
			if m := proxyPanOwnerRe.FindStringSubmatch(line); m != nil && strings.EqualFold(m[2], pc.Handle) {
				pc.OwnedPans[m[1]] = struct{}{}
			}
			if m := proxySliceOwnerRe.FindStringSubmatch(line); m != nil && strings.EqualFold(m[2], pc.Handle) {
				pc.OwnedSlices[m[1]] = struct{}{}
			}
		}

		if cfg.LogPings || !pingLineRe.MatchString(line) {
			log.Debug().Str("ctx", "proxy").Str("proto", "TCP").Str("dir", "←").Str("line", line).Msg("proxy resp")
		}
		if _, err := fmt.Fprintf(clientConn, "%s\n", line); err != nil {
			break
		}
	}

	log.Info().Str("ctx", "proxy").Str("proto", "TCP").Str("client", clientAddr.String()).Msg("SmartSDR client disconnected")
}

// startSmartSDRProxy listens for SmartSDR TCP connections and proxies them.
func startSmartSDRProxy(ctx context.Context, listen string) {
	proxyIP := cfg.ProxyIP
	if proxyIP == "" {
		proxyIP = getLocalIP()
	}
	rc := NewRadioContext(cfg.RadioIP, getLocalIP(), proxyIP)
	setRadioContext(rc)

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
