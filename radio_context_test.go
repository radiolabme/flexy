package main

import (
	"net"
	"testing"
)

func TestNewRadioContext(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	if rc.RadioIP != "10.10.0.100" {
		t.Errorf("RadioIP = %q, want %q", rc.RadioIP, "10.10.0.100")
	}
	if rc.ProxyLanIP != "10.10.0.3" {
		t.Errorf("ProxyLanIP = %q, want %q", rc.ProxyLanIP, "10.10.0.3")
	}
	if rc.ProxyIP != "100.66.19.5" {
		t.Errorf("ProxyIP = %q, want %q", rc.ProxyIP, "100.66.19.5")
	}
	if rc.Clients == nil {
		t.Fatal("Clients map is nil")
	}
	if len(rc.Clients) != 0 {
		t.Errorf("Clients should be empty, got %d", len(rc.Clients))
	}
}

func TestRegisterUnregisterClient(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")

	c := &ProxyClient{
		Handle:   "0A273C01",
		RemoteIP: net.ParseIP("100.66.19.12"),
	}
	rc.RegisterClient(c)

	got := rc.ClientByHandle("0A273C01")
	if got != c {
		t.Fatal("ClientByHandle returned wrong client after Register")
	}
	if got := rc.ClientByHandle("DEADBEEF"); got != nil {
		t.Fatal("ClientByHandle should return nil for unknown handle")
	}

	rc.UnregisterClient("0A273C01")
	if got := rc.ClientByHandle("0A273C01"); got != nil {
		t.Fatal("ClientByHandle should return nil after Unregister")
	}
}

func TestRewriteDiscoveryIPs_SingleClient(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	rc.RegisterClient(&ProxyClient{
		Handle:   "0A273C01",
		RemoteIP: net.ParseIP("100.66.19.12"),
	})

	// Discovery kv as the radio would send it: handle prefixed with 0x,
	// IP is the proxy LAN IP (what the radio sees).
	kv := map[string]string{
		"gui_client_handles": "0x0A273C01",
		"gui_client_ips":     "10.10.0.3",
	}
	rc.RewriteDiscoveryIPs(kv)

	if kv["gui_client_ips"] != "100.66.19.12" {
		t.Errorf("gui_client_ips = %q, want %q", kv["gui_client_ips"], "100.66.19.12")
	}
}

func TestRewriteDiscoveryIPs_MultipleClients(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	rc.RegisterClient(&ProxyClient{
		Handle:   "0A273C01",
		RemoteIP: net.ParseIP("100.66.19.12"),
	})
	rc.RegisterClient(&ProxyClient{
		Handle:   "0B384D02",
		RemoteIP: net.ParseIP("100.66.19.20"),
	})

	kv := map[string]string{
		"gui_client_handles": "0x0A273C01,0x0B384D02",
		"gui_client_ips":     "10.10.0.3,10.10.0.3",
	}
	rc.RewriteDiscoveryIPs(kv)

	if kv["gui_client_ips"] != "100.66.19.12,100.66.19.20" {
		t.Errorf("gui_client_ips = %q, want %q", kv["gui_client_ips"], "100.66.19.12,100.66.19.20")
	}
}

func TestRewriteDiscoveryIPs_MixedProxyAndLocal(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	// Only one of two clients is proxied.
	rc.RegisterClient(&ProxyClient{
		Handle:   "0A273C01",
		RemoteIP: net.ParseIP("100.66.19.12"),
	})

	kv := map[string]string{
		"gui_client_handles": "0x0A273C01,0xDEADBEEF",
		"gui_client_ips":     "10.10.0.3,10.10.0.50",
	}
	rc.RewriteDiscoveryIPs(kv)

	// First IP rewritten, second (non-proxied local client) left alone.
	if kv["gui_client_ips"] != "100.66.19.12,10.10.0.50" {
		t.Errorf("gui_client_ips = %q, want %q", kv["gui_client_ips"], "100.66.19.12,10.10.0.50")
	}
}

func TestRewriteDiscoveryIPs_CaseInsensitive(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	rc.RegisterClient(&ProxyClient{
		Handle:   "0A273C01", // stored uppercase
		RemoteIP: net.ParseIP("100.66.19.12"),
	})

	kv := map[string]string{
		"gui_client_handles": "0x0a273c01", // lowercase in discovery
		"gui_client_ips":     "10.10.0.3",
	}
	rc.RewriteDiscoveryIPs(kv)

	if kv["gui_client_ips"] != "100.66.19.12" {
		t.Errorf("case-insensitive lookup failed: gui_client_ips = %q", kv["gui_client_ips"])
	}
}

func TestRewriteDiscoveryIPs_NoHandlesKey(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	kv := map[string]string{
		"gui_client_ips": "10.10.0.3",
	}
	rc.RewriteDiscoveryIPs(kv) // should not panic
	if kv["gui_client_ips"] != "10.10.0.3" {
		t.Error("Should be unchanged when gui_client_handles missing")
	}
}

func TestRewriteDiscoveryIPs_NoIPsKey(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	kv := map[string]string{
		"gui_client_handles": "0x0A273C01",
	}
	rc.RewriteDiscoveryIPs(kv) // should not panic
	if _, ok := kv["gui_client_ips"]; ok {
		t.Error("gui_client_ips should not be created")
	}
}

func TestRewriteDiscoveryIPs_LengthMismatch(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	rc.RegisterClient(&ProxyClient{
		Handle:   "0A273C01",
		RemoteIP: net.ParseIP("100.66.19.12"),
	})

	// Two handles but only one IP — malformed; should leave untouched.
	kv := map[string]string{
		"gui_client_handles": "0x0A273C01,0xDEADBEEF",
		"gui_client_ips":     "10.10.0.3",
	}
	rc.RewriteDiscoveryIPs(kv)
	if kv["gui_client_ips"] != "10.10.0.3" {
		t.Error("Should be unchanged on length mismatch")
	}
}

func TestRewriteDiscoveryIPs_NilRemoteIP(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	rc.RegisterClient(&ProxyClient{
		Handle:   "0A273C01",
		RemoteIP: nil, // not yet resolved
	})

	kv := map[string]string{
		"gui_client_handles": "0x0A273C01",
		"gui_client_ips":     "10.10.0.3",
	}
	rc.RewriteDiscoveryIPs(kv)
	// Should not rewrite when RemoteIP is nil.
	if kv["gui_client_ips"] != "10.10.0.3" {
		t.Error("Should not rewrite when RemoteIP is nil")
	}
}

func TestRewriteDiscoveryIPs_AlreadyCorrect(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	rc.RegisterClient(&ProxyClient{
		Handle:   "0A273C01",
		RemoteIP: net.ParseIP("10.10.0.3"),
	})

	kv := map[string]string{
		"gui_client_handles": "0x0A273C01",
		"gui_client_ips":     "10.10.0.3",
	}
	rc.RewriteDiscoveryIPs(kv)
	// IP already matches — no change needed.
	if kv["gui_client_ips"] != "10.10.0.3" {
		t.Error("Should be unchanged when already correct")
	}
}

func TestSetDiscovery(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	kv := map[string]string{"model": "FLEX-6600M", "nickname": "Test Radio"}
	rc.SetDiscovery(kv, "10.10.0.255", []string{"peer1 (100.66.19.1)"})

	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if rc.DiscoveryKV["model"] != "FLEX-6600M" {
		t.Errorf("DiscoveryKV[model] = %q", rc.DiscoveryKV["model"])
	}
	if rc.BroadcastAddrs != "10.10.0.255" {
		t.Errorf("BroadcastAddrs = %q", rc.BroadcastAddrs)
	}
	if len(rc.UnicastAddrs) != 1 || rc.UnicastAddrs[0] != "peer1 (100.66.19.1)" {
		t.Errorf("UnicastAddrs = %v", rc.UnicastAddrs)
	}
}

func TestSnapshot(t *testing.T) {
	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	rc.RegisterClient(&ProxyClient{
		Handle:     "0A273C01",
		RemoteIP:   net.ParseIP("100.66.19.12"),
		RemoteAddr: "100.66.19.12:54321",
		Program:    "SmartSDR-Mac",
		Station:    "MyStation",
		ClientID:   "abcd-1234",
	})
	rc.SetDiscovery(map[string]string{"model": "FLEX-6600M"}, "10.10.0.255", nil)

	snap := rc.Snapshot()
	if snap.RadioIP != "10.10.0.100" {
		t.Errorf("snap.RadioIP = %q", snap.RadioIP)
	}
	if len(snap.Clients) != 1 {
		t.Fatalf("snap.Clients len = %d, want 1", len(snap.Clients))
	}
	c := snap.Clients[0]
	if c.Handle != "0A273C01" {
		t.Errorf("client.Handle = %q", c.Handle)
	}
	if c.RemoteIP != "100.66.19.12" {
		t.Errorf("client.RemoteIP = %q", c.RemoteIP)
	}
	if c.Program != "SmartSDR-Mac" {
		t.Errorf("client.Program = %q", c.Program)
	}
	if c.Station != "MyStation" {
		t.Errorf("client.Station = %q", c.Station)
	}
	if c.ClientID != "abcd-1234" {
		t.Errorf("client.ClientID = %q", c.ClientID)
	}
	if snap.Discovery["model"] != "FLEX-6600M" {
		t.Errorf("snap.Discovery[model] = %q", snap.Discovery["model"])
	}
}

func TestGlobalRadioContextGetSet(t *testing.T) {
	// Verify the global get/set works with nil.
	setRadioContext(nil)
	if rc := getRadioContext(); rc != nil {
		t.Error("getRadioContext should return nil after setRadioContext(nil)")
	}

	rc := NewRadioContext("10.10.0.100", "10.10.0.3", "100.66.19.5")
	setRadioContext(rc)
	if got := getRadioContext(); got != rc {
		t.Error("getRadioContext should return the set context")
	}
	// Cleanup.
	setRadioContext(nil)
}
