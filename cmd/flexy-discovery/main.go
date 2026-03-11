// flexy-discovery receives unicast SmartSDR discovery packets (e.g. from a
// Flexy proxy over Tailscale) and re-broadcasts them on the local network so
// that SmartSDR, Smart CAT, and Smart DAX can discover the radio.
//
// Future: add -auth-key flag for HMAC verification of incoming packets.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
)

func main() {
	listenAddr := flag.String("listen", ":4992", "UDP address to listen on")
	allowFrom := flag.String("allow-from", "100.64.0.0/10", "CIDR of trusted source IPs (default: Tailscale CGNAT range)")
	flag.Parse()

	_, allowNet, err := net.ParseCIDR(*allowFrom)
	if err != nil {
		log.Fatalf("bad -allow-from CIDR: %v", err)
	}

	// Resolve listen address.
	addr, err := net.ResolveUDPAddr("udp4", *listenAddr)
	if err != nil {
		log.Fatalf("resolve listen: %v", err)
	}

	// Listen for incoming packets (unicast or broadcast).
	recvConn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		log.Fatalf("listen %s: %v", addr, err)
	}
	defer recvConn.Close()

	// Separate socket for sending broadcasts (needs SO_BROADCAST).
	sendConn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		log.Fatalf("open send socket: %v", err)
	}
	defer sendConn.Close()

	bcastDest := &net.UDPAddr{IP: net.IPv4bcast, Port: 4992}

	log.Printf("flexy-discovery: listening on %s, re-broadcasting from %s", *listenAddr, *allowFrom)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		log.Println("shutting down")
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
			log.Printf("read error: %v", err)
			continue
		}

		// Only relay packets from trusted sources.
		if !allowNet.Contains(src.IP) {
			dropped++
			continue
		}

		// Ignore tiny packets (VITA-49 header alone is 28 bytes).
		if n < 28 {
			dropped++
			continue
		}

		if _, err := sendConn.WriteToUDP(buf[:n], bcastDest); err != nil {
			log.Printf("broadcast failed: %v", err)
			continue
		}

		relayed++
		if relayed == 1 || relayed%100 == 0 {
			fmt.Fprintf(os.Stderr, "relayed %d packets (%d dropped)\n", relayed, dropped)
		}
	}

	log.Printf("flexy-discovery: exiting (relayed %d, dropped %d)", relayed, dropped)
}
