// flexy-discovery receives unicast SmartSDR discovery packets (e.g. from a
// Flexy proxy over Tailscale) and re-broadcasts them on the local network so
// that SmartSDR, Smart CAT, and Smart DAX can discover the radio.
//
// Future: add -auth-key flag for HMAC verification of incoming packets.
package main

import (
	"flag"
	"net"
	"os"
	"os/signal"
	"strings"

	"github.com/rs/zerolog"
	log "github.com/rs/zerolog/log"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()

	listenAddr := flag.String("listen", ":4992", "UDP address to listen on")
	allowFrom := flag.String("allow-from", "100.64.0.0/10", "CIDR of trusted source IPs (default: Tailscale CGNAT range)")
	flag.Parse()

	_, allowNet, err := net.ParseCIDR(*allowFrom)
	if err != nil {
		log.Fatal().Err(err).Msg("bad -allow-from CIDR")
	}

	addr, err := net.ResolveUDPAddr("udp4", *listenAddr)
	if err != nil {
		log.Fatal().Err(err).Msg("resolve listen address")
	}

	recvConn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		log.Fatal().Err(err).Str("addr", addr.String()).Msg("listen failed")
	}
	defer recvConn.Close()

	sendConn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		log.Fatal().Err(err).Msg("open send socket")
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
}
