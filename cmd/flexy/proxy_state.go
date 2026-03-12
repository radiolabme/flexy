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
	packetsTx  atomic.Int64
	lastPktNs  atomic.Int64
}

func (c *UDPRelayCounter) recordRx() {
	c.packetsRx.Add(1)
	c.lastPktNs.Store(time.Now().UnixNano())
}

func (c *UDPRelayCounter) recordTx() {
	c.packetsTx.Add(1)
	c.lastPktNs.Store(time.Now().UnixNano())
}

type UDPRelaySnapshot struct {
	ClientAddr string     `json:"clientAddr"`
	RelayPort  int        `json:"relayPort"`
	PacketsRx  int64      `json:"packetsRx"`
	PacketsTx  int64      `json:"packetsTx"`
	LastPacket *time.Time `json:"lastPacket,omitempty"`
}

func (c *UDPRelayCounter) snapshot() UDPRelaySnapshot {
	ns := c.lastPktNs.Load()
	s := UDPRelaySnapshot{
		ClientAddr: c.ClientAddr,
		RelayPort:  c.RelayPort,
		PacketsRx:  c.packetsRx.Load(),
		PacketsTx:  c.packetsTx.Load(),
	}
	if ns != 0 {
		t := time.Unix(0, ns)
		s.LastPacket = &t
	}
	return s
}

// HamlibClientConn tracks one active hamlib rigctld TCP client connection.
type HamlibClientConn struct {
	id          string
	Addr        string
	ConnectedAt time.Time
}

// TCPCounter tracks TCP line counts in each direction for a proxy client.
type TCPCounter struct {
	linesRx atomic.Int64 // radio → client
	linesTx atomic.Int64 // client → radio
}

type TCPSnapshot struct {
	LinesRx int64 `json:"linesRx"`
	LinesTx int64 `json:"linesTx"`
}

func (c *TCPCounter) snapshot() TCPSnapshot {
	return TCPSnapshot{
		LinesRx: c.linesRx.Load(),
		LinesTx: c.linesTx.Load(),
	}
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
