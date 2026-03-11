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

func run() error {
	listenAddr := flag.String("listen", ":4993", "UDP address to listen on")
	allowFrom := flag.String("allow-from", "100.64.0.0/10", "CIDR of trusted source IPs (default: Tailscale CGNAT range)")
	flag.Parse()

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

	bcastDest := &net.UDPAddr{IP: net.IPv4bcast, Port: 4992}

	log.Info().Str("listen", *listenAddr).Str("allow", *allowFrom).Msg("started")

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
			continue
		}

		if n < 28 {
			dropped++
			continue
		}

		if _, err := sendConn.WriteToUDP(buf[:n], bcastDest); err != nil {
			log.Error().Err(err).Msg("broadcast failed")
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
