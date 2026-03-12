// flexy-discovery receives unicast SmartSDR discovery packets (e.g. from a
// Flexy proxy over Tailscale) and re-broadcasts them on the local network so
// that SmartSDR, Smart CAT, and Smart DAX can discover the radio.
//
// Future: add -auth-key flag for HMAC verification of incoming packets.
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strings"

	"github.com/rs/zerolog"
	log "github.com/rs/zerolog/log"
)

// waitOnWindows pauses for a keypress so double-clicked .exe windows
// don't vanish before the user can read the output.
func waitOnWindows() {
	if runtime.GOOS != "windows" {
		return
	}
	fmt.Fprintln(os.Stderr, "\nPress Enter to exit...")
	buf := make([]byte, 1)
	os.Stdin.Read(buf) //nolint:errcheck
}

func main() {
	defer waitOnWindows()

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()

	if err := run(); err != nil {
		log.Error().Err(err).Msg("fatal")
		return
	}
}

// lanBroadcastAddrs returns the directed broadcast address for every non-loopback
// IPv4 interface that is up.
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
			if ip4 == nil {
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

func run() error {
	listenAddr := flag.String("listen", ":4993", "UDP address to listen on")
	allowFrom := flag.String("allow-from", "100.64.0.0/10", "CIDR of trusted source IPs (default: Tailscale CGNAT range)")
	logLevel := flag.String("log-level", "info", "minimum log level (debug, info, warn, error)")
	flag.Parse()

	lvl, err := zerolog.ParseLevel(*logLevel)
	if err != nil {
		return fmt.Errorf("bad -log-level %q: %w", *logLevel, err)
	}
	zerolog.SetGlobalLevel(lvl)

	_, allowNet, err := net.ParseCIDR(*allowFrom)
	if err != nil {
		return fmt.Errorf("bad -allow-from CIDR: %w", err)
	}

	addr, err := net.ResolveUDPAddr("udp4", *listenAddr)
	if err != nil {
		return fmt.Errorf("resolve listen address: %w", err)
	}

	recvConn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	defer recvConn.Close()

	sendConn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return fmt.Errorf("open send socket: %w", err)
	}
	defer sendConn.Close()

	// Enable SO_BROADCAST so we can send to 255.255.255.255 and subnet broadcasts.
	if err := setSockBroadcast(sendConn); err != nil {
		return fmt.Errorf("set SO_BROADCAST: %w", err)
	}

	bcastAddrs := lanBroadcastAddrs()
	if len(bcastAddrs) == 0 {
		bcastAddrs = []net.IP{net.IPv4bcast}
	}

	var bcastStrs []string
	for _, b := range bcastAddrs {
		bcastStrs = append(bcastStrs, (&net.UDPAddr{IP: b, Port: 4992}).String())
	}

	log.Info().
		Str("listen", *listenAddr).
		Str("allow", *allowFrom).
		Strs("broadcast", bcastStrs).
		Str("log_level", *logLevel).
		Msg("started")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		log.Info().Msg("shutting down")
		recvConn.Close()
	}()

	buf := make([]byte, 65536)
	var relayed, dropped uint64
	for {
		n, src, err := recvConn.ReadFromUDP(buf)
		if err != nil {
			if strings.Contains(err.Error(), "use of closed") {
				break
			}
			log.Error().Err(err).Msg("read error")
			continue
		}

		if !allowNet.Contains(src.IP) {
			dropped++
			log.Debug().Str("src", src.String()).Int("bytes", n).Msg("dropped: source outside allow range")
			continue
		}

		if n < 28 {
			dropped++
			log.Debug().Str("src", src.String()).Int("bytes", n).Msg("dropped: packet too small")
			continue
		}

		sent := false
		for _, bcastIP := range bcastAddrs {
			dest := &net.UDPAddr{IP: bcastIP, Port: 4992}
			if _, err := sendConn.WriteToUDP(buf[:n], dest); err != nil {
				log.Error().Err(err).Str("src", src.String()).Int("bytes", n).Str("dest", dest.String()).Msg("broadcast failed")
			} else {
				sent = true
				log.Debug().Str("src", src.String()).Int("bytes", n).Str("dest", dest.String()).Msg("relayed")
			}
		}
		if !sent {
			continue
		}

		relayed++
		if relayed == 1 || relayed%100 == 0 {
			log.Info().Uint64("relayed", relayed).Uint64("dropped", dropped).Msg("progress")
		}
	}

	log.Info().Uint64("relayed", relayed).Uint64("dropped", dropped).Msg("exiting")
	return nil
}
