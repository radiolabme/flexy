package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// UDPRelayCounter tracks live packet statistics for one VITA-49 UDP relay.
type UDPRelayCounter struct {
	ClientAddr string
	RelayPort  int
	packetsRx  atomic.Int64
	lastPktNs  atomic.Int64
}

func (c *UDPRelayCounter) record() {
	c.packetsRx.Add(1)
	c.lastPktNs.Store(time.Now().UnixNano())
}

type UDPRelaySnapshot struct {
	ClientAddr string     `json:"clientAddr"`
	RelayPort  int        `json:"relayPort"`
	PacketsRx  int64      `json:"packetsRx"`
	LastPacket *time.Time `json:"lastPacket,omitempty"`
}

func (c *UDPRelayCounter) snapshot() UDPRelaySnapshot {
	ns := c.lastPktNs.Load()
	s := UDPRelaySnapshot{
		ClientAddr: c.ClientAddr,
		RelayPort:  c.RelayPort,
		PacketsRx:  c.packetsRx.Load(),
	}
	if ns != 0 {
		t := time.Unix(0, ns)
		s.LastPacket = &t
	}
	return s
}

// ProxyConn represents one active SmartSDR TCP proxy connection.
type ProxyConn struct {
	id          string
	ClientAddr  string
	RadioAddr   string
	ConnectedAt time.Time
	relay       atomic.Pointer[UDPRelayCounter]
}

func (c *ProxyConn) setRelay(r *UDPRelayCounter) { c.relay.Store(r) }
func (c *ProxyConn) getRelay() *UDPRelayCounter  { return c.relay.Load() }

type ProxyConnSnapshot struct {
	ID          string            `json:"id"`
	ClientAddr  string            `json:"clientAddr"`
	RadioAddr   string            `json:"radioAddr"`
	ConnectedAt time.Time         `json:"connectedAt"`
	UDPRelay    *UDPRelaySnapshot `json:"udpRelay,omitempty"`
}

func (c *ProxyConn) snapshot() ProxyConnSnapshot {
	s := ProxyConnSnapshot{
		ID:          c.id,
		ClientAddr:  c.ClientAddr,
		RadioAddr:   c.RadioAddr,
		ConnectedAt: c.ConnectedAt,
	}
	if r := c.getRelay(); r != nil {
		rs := r.snapshot()
		s.UDPRelay = &rs
	}
	return s
}

var (
	proxyConnsMu sync.RWMutex
	proxyConns   = map[string]*ProxyConn{}
	proxyConnSeq int64
)

func registerProxyConn(clientAddr, radioAddr string) *ProxyConn {
	id := fmt.Sprintf("%d", atomic.AddInt64(&proxyConnSeq, 1))
	c := &ProxyConn{
		id:          id,
		ClientAddr:  clientAddr,
		RadioAddr:   radioAddr,
		ConnectedAt: time.Now(),
	}
	proxyConnsMu.Lock()
	proxyConns[id] = c
	proxyConnsMu.Unlock()
	return c
}

func unregisterProxyConn(c *ProxyConn) {
	proxyConnsMu.Lock()
	delete(proxyConns, c.id)
	proxyConnsMu.Unlock()
}

func snapshotProxyConns() []ProxyConnSnapshot {
	proxyConnsMu.RLock()
	defer proxyConnsMu.RUnlock()
	out := make([]ProxyConnSnapshot, 0, len(proxyConns))
	for _, c := range proxyConns {
		out = append(out, c.snapshot())
	}
	return out
}

// HamlibClientConn tracks one active hamlib rigctld TCP client connection.
type HamlibClientConn struct {
	id          string
	Addr        string
	ConnectedAt time.Time
}

type HamlibClientSnapshot struct {
	ID          string    `json:"id"`
	Addr        string    `json:"addr"`
	ConnectedAt time.Time `json:"connectedAt"`
}

var (
	hamlibClientsMu sync.RWMutex
	hamlibClients   = map[string]*HamlibClientConn{}
	hamlibClientSeq int64
)

func registerHamlibClient(addr string) *HamlibClientConn {
	id := fmt.Sprintf("%d", atomic.AddInt64(&hamlibClientSeq, 1))
	c := &HamlibClientConn{id: id, Addr: addr, ConnectedAt: time.Now()}
	hamlibClientsMu.Lock()
	hamlibClients[id] = c
	hamlibClientsMu.Unlock()
	return c
}

func unregisterHamlibClient(c *HamlibClientConn) {
	hamlibClientsMu.Lock()
	delete(hamlibClients, c.id)
	hamlibClientsMu.Unlock()
}

func snapshotHamlibClients() []HamlibClientSnapshot {
	hamlibClientsMu.RLock()
	defer hamlibClientsMu.RUnlock()
	out := make([]HamlibClientSnapshot, 0, len(hamlibClients))
	for _, c := range hamlibClients {
		out = append(out, HamlibClientSnapshot{ID: c.id, Addr: c.Addr, ConnectedAt: c.ConnectedAt})
	}
	return out
}
