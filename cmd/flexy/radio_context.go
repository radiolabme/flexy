package main

import (
	"net"
	"strings"
	"sync"
	"time"

	log "github.com/rs/zerolog/log"
)

// ProxyClient represents a single SmartSDR TCP client connection proxied
// through Flexy. The Handle is assigned by the radio (from the H-line);
// RemoteIP is the real client IP taken from the accepted socket.
type ProxyClient struct {
	Handle      string // radio-assigned hex handle, e.g. "0A273C01"
	RemoteIP    net.IP // real client IP (from clientConn.RemoteAddr)
	RemoteAddr  string // full "ip:port" of the client
	Program     string // e.g. "SmartSDR-Mac", "CAT", "DAX"
	Station     string // station name once assigned
	ClientID    string // GUI UUID (from client status updates)
	ConnectedAt time.Time
	Relay       *UDPRelayCounter
	TCP         TCPCounter

	// Owned resources for cleanup on disconnect.
	OwnedPans   map[string]struct{}
	OwnedSlices map[string]struct{}
}

// RadioContext tracks all proxy state for one target radio. This is the
// central structure the discovery relay, TCP proxy, and web UI all share.
type RadioContext struct {
	mu sync.RWMutex

	RadioIP    string // target radio IP
	ProxyLanIP string // our IP as seen by this radio (from outbound socket)
	ProxyIP    string // advertised proxy IP (what clients connect to)

	// Clients keyed by handle (uppercase hex, no "0x" prefix).
	Clients map[string]*ProxyClient

	// Discovery state — last received from this radio.
	DiscoveryKV    map[string]string
	BroadcastAddrs string   // comma-joined broadcast targets
	UnicastAddrs   []string // peer descriptions
}

// NewRadioContext creates a RadioContext for the given radio.
func NewRadioContext(radioIP, proxyLanIP, proxyIP string) *RadioContext {
	return &RadioContext{
		RadioIP:    radioIP,
		ProxyLanIP: proxyLanIP,
		ProxyIP:    proxyIP,
		Clients:    make(map[string]*ProxyClient),
	}
}

// RegisterClient adds a proxy client after the radio assigns its handle.
func (rc *RadioContext) RegisterClient(c *ProxyClient) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.Clients[c.Handle] = c
}

// UnregisterClient removes a proxy client by handle.
func (rc *RadioContext) UnregisterClient(handle string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	delete(rc.Clients, handle)
}

// ClientByHandle returns the proxy client for a given handle (nil if not found).
func (rc *RadioContext) ClientByHandle(handle string) *ProxyClient {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.Clients[handle]
}

// RewriteDiscoveryIPs rewrites gui_client_ips in the discovery key-value map
// using the handle→remoteIP mapping from registered proxy clients. Handles
// and IPs are positionally correlated comma-separated lists.
func (rc *RadioContext) RewriteDiscoveryIPs(kv map[string]string) {
	handlesStr, ok := kv["gui_client_handles"]
	if !ok {
		return
	}
	ipsStr, ok := kv["gui_client_ips"]
	if !ok {
		return
	}

	handles := strings.Split(handlesStr, ",")
	ips := strings.Split(ipsStr, ",")
	if len(handles) != len(ips) {
		log.Warn().Str("ctx", "proxy").Str("handles", handlesStr).Str("ips", ipsStr).
			Msg("Discovery rewrite: handles/ips length mismatch")
		return // malformed, leave untouched
	}

	rc.mu.RLock()
	defer rc.mu.RUnlock()

	changed := false
	for i, h := range handles {
		// Normalise: strip "0x" prefix for lookup.
		key := strings.TrimPrefix(strings.TrimSpace(h), "0x")
		key = strings.ToUpper(key)
		if c, ok := rc.Clients[key]; ok && c.RemoteIP != nil {
			newIP := c.RemoteIP.String()
			if ips[i] != newIP {
				log.Debug().Str("ctx", "proxy").Str("handle", key).
					Str("old_ip", ips[i]).Str("new_ip", newIP).
					Msg("Discovery rewrite: gui_client_ips")
				ips[i] = newIP
				changed = true
			}
		} else {
			log.Debug().Str("ctx", "proxy").Str("handle", key).
				Bool("in_map", ok).Str("ip", ips[i]).
				Msg("Discovery rewrite: handle not in proxy client map")
		}
	}
	if changed {
		kv["gui_client_ips"] = strings.Join(ips, ",")
	}
}

// SetDiscovery stores the latest discovery state for this radio.
func (rc *RadioContext) SetDiscovery(kv map[string]string, bcast string, unicast []string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.DiscoveryKV = kv
	rc.BroadcastAddrs = bcast
	rc.UnicastAddrs = unicast
}

// --- Snapshots for web API ---

type ProxyClientSnapshot struct {
	Handle      string            `json:"handle"`
	RemoteAddr  string            `json:"remoteAddr"`
	RemoteIP    string            `json:"remoteIP"`
	Program     string            `json:"program,omitempty"`
	Station     string            `json:"station,omitempty"`
	ClientID    string            `json:"clientId,omitempty"`
	ConnectedAt time.Time         `json:"connectedAt"`
	UDPRelay    *UDPRelaySnapshot `json:"udpRelay,omitempty"`
	TCP         TCPSnapshot       `json:"tcp"`
}

type RadioContextSnapshot struct {
	RadioIP    string                `json:"radioIP"`
	ProxyLanIP string                `json:"proxyLanIP"`
	ProxyIP    string                `json:"proxyIP"`
	Clients    []ProxyClientSnapshot `json:"clients"`
	Discovery  map[string]string     `json:"discovery,omitempty"`
	BcastAddrs string                `json:"broadcastAddrs,omitempty"`
	Unicast    []string              `json:"unicastAddrs,omitempty"`
}

func (rc *RadioContext) Snapshot() RadioContextSnapshot {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	s := RadioContextSnapshot{
		RadioIP:    rc.RadioIP,
		ProxyLanIP: rc.ProxyLanIP,
		ProxyIP:    rc.ProxyIP,
		Discovery:  rc.DiscoveryKV,
		BcastAddrs: rc.BroadcastAddrs,
		Unicast:    rc.UnicastAddrs,
	}
	for _, c := range rc.Clients {
		cs := ProxyClientSnapshot{
			Handle:      c.Handle,
			RemoteAddr:  c.RemoteAddr,
			RemoteIP:    c.RemoteIP.String(),
			Program:     c.Program,
			Station:     c.Station,
			ClientID:    c.ClientID,
			ConnectedAt: c.ConnectedAt,
			TCP:         c.TCP.snapshot(),
		}
		if c.Relay != nil {
			rs := c.Relay.snapshot()
			cs.UDPRelay = &rs
		}
		s.Clients = append(s.Clients, cs)
	}
	return s
}

// --- Global registry ---

var (
	radioCtxMu sync.RWMutex
	radioCtx   *RadioContext // single radio for now; becomes a map for multi-radio
)

func setRadioContext(rc *RadioContext) {
	radioCtxMu.Lock()
	defer radioCtxMu.Unlock()
	radioCtx = rc
}

func getRadioContext() *RadioContext {
	radioCtxMu.RLock()
	defer radioCtxMu.RUnlock()
	return radioCtx
}
