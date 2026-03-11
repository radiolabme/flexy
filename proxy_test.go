//go:build unix

package main

import (
	"testing"
)

// --- Discovery payload codec ---

func TestParseDiscoveryPayload_Basic(t *testing.T) {
	kv := parseDiscoveryPayload("model=FLEX-6600M serial=1234-5678-9012-3456")
	if kv["model"] != "FLEX-6600M" {
		t.Errorf("model = %q", kv["model"])
	}
	if kv["serial"] != "1234-5678-9012-3456" {
		t.Errorf("serial = %q", kv["serial"])
	}
}

func TestParseDiscoveryPayload_SpaceDecode(t *testing.T) {
	// 0x7f is the FlexRadio-encoded space character.
	kv := parseDiscoveryPayload("nickname=My\x7fRadio")
	if kv["nickname"] != "My Radio" {
		t.Errorf("nickname = %q, want %q", kv["nickname"], "My Radio")
	}
}

func TestParseDiscoveryPayload_NullTrim(t *testing.T) {
	kv := parseDiscoveryPayload("key=val\x00\x00\x00")
	if kv["key"] != "val" {
		t.Errorf("key = %q", kv["key"])
	}
}

func TestParseDiscoveryPayload_Empty(t *testing.T) {
	kv := parseDiscoveryPayload("")
	if len(kv) != 0 {
		t.Errorf("expected empty map, got %v", kv)
	}
}

func TestParseDiscoveryPayload_NoEquals(t *testing.T) {
	kv := parseDiscoveryPayload("bareword anotherword")
	if len(kv) != 0 {
		t.Errorf("expected empty map for no-equals input, got %v", kv)
	}
}

func TestBuildDiscoveryPacket_RoundTrip(t *testing.T) {
	original := map[string]string{
		"model":    "FLEX-6600M",
		"nickname": "Test Radio",
		"ip":       "10.10.0.100",
		"serial":   "1234-5678",
	}
	pkt := buildDiscoveryPacket(original)

	// VITA-49 header: 4 bytes, class ID: 8 bytes, then payload.
	if len(pkt) < 12 {
		t.Fatalf("packet too short: %d bytes", len(pkt))
	}

	// Verify header word structure.
	headerWord := uint32(pkt[0])<<24 | uint32(pkt[1])<<16 | uint32(pkt[2])<<8 | uint32(pkt[3])
	packetType := (headerWord >> 28) & 0xF
	classPresent := (headerWord >> 27) & 1
	if packetType != 5 {
		t.Errorf("PacketType = %d, want 5 (ExtContext)", packetType)
	}
	if classPresent != 1 {
		t.Error("C bit not set")
	}

	// Parse the payload back.
	decoded := parseDiscoveryPayload(string(pkt[12:]))

	for k, v := range original {
		if decoded[k] != v {
			t.Errorf("round-trip mismatch: %s = %q, want %q", k, decoded[k], v)
		}
	}
}

func TestBuildDiscoveryPacket_SpaceEncoding(t *testing.T) {
	pkt := buildDiscoveryPacket(map[string]string{"nickname": "My Radio"})
	payload := string(pkt[12:])
	// The space should be encoded as 0x7f in the wire format.
	if !containsByte(payload, 0x7f) {
		t.Error("space not encoded as 0x7f in payload")
	}
	// When parsed back, 0x7f should become space again.
	kv := parseDiscoveryPayload(payload)
	if kv["nickname"] != "My Radio" {
		t.Errorf("nickname = %q", kv["nickname"])
	}
}

func TestBuildDiscoveryPacket_PaddedTo4Bytes(t *testing.T) {
	pkt := buildDiscoveryPacket(map[string]string{"a": "b"})
	// Total packet length must be a multiple of 4 bytes.
	if len(pkt)%4 != 0 {
		t.Errorf("packet length %d not a multiple of 4", len(pkt))
	}
}

func containsByte(s string, b byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return true
		}
	}
	return false
}

// --- Protocol line regex tests ---
// These test the regexes against the SmartSDR TCP API protocol spec:
//   - Client→Radio: C<seq>|<command>
//   - Radio→Client: R<seq>|<status>|<payload>
//   - Handle assignment: H<hex>
//   - Status updates: S<handle>|<type> <params>

func TestClientIPCmdRe(t *testing.T) {
	tests := []struct {
		line    string
		match   bool
		wantSeq string
	}{
		{"C3|client ip", true, "3"},
		{"C12|client ip", true, "12"},

		{"C3|client program foo", false, ""},
		{"C3|client bind client_id=abc", false, ""},
		{"R3|0|10.10.0.3", false, ""},
		{"", false, ""},
	}
	for _, tt := range tests {
		m := clientIPCmdRe.FindStringSubmatch(tt.line)
		if tt.match {
			if m == nil {
				t.Errorf("clientIPCmdRe should match %q", tt.line)
			} else if m[1] != tt.wantSeq {
				t.Errorf("clientIPCmdRe seq = %q, want %q for %q", m[1], tt.wantSeq, tt.line)
			}
		} else if m != nil {
			t.Errorf("clientIPCmdRe should not match %q", tt.line)
		}
	}
}

func TestClientIPRespRe(t *testing.T) {
	tests := []struct {
		line   string
		match  bool
		wantIP string
	}{
		{"R3|0|10.10.0.3", true, "10.10.0.3"},
		{"R12|0|100.66.19.12", true, "100.66.19.12"},
		{"R3|1|error message", false, ""}, // non-zero status
		{"C3|client ip", false, ""},
		{"R3|0|", false, ""}, // no IP
	}
	for _, tt := range tests {
		m := clientIPRespRe.FindStringSubmatch(tt.line)
		if tt.match {
			if m == nil {
				t.Errorf("clientIPRespRe should match %q", tt.line)
				continue
			}
			// m[1]=R, m[2]=seq, m[3]=|0|, m[4]=ip
			if m[4] != tt.wantIP {
				t.Errorf("clientIPRespRe ip = %q, want %q for %q", m[4], tt.wantIP, tt.line)
			}
		} else if m != nil {
			t.Errorf("clientIPRespRe should not match %q", tt.line)
		}
	}
}

func TestClientProgramRe(t *testing.T) {
	tests := []struct {
		line     string
		match    bool
		wantProg string
	}{
		{"C1|client program SmartSDR-Mac", true, "SmartSDR-Mac"},
		{"C5|client program CAT", true, "CAT"},
		{"C1|client station MyStation", false, ""},
	}
	for _, tt := range tests {
		m := clientProgramRe.FindStringSubmatch(tt.line)
		if tt.match {
			if m == nil {
				t.Errorf("clientProgramRe should match %q", tt.line)
			} else if m[1] != tt.wantProg {
				t.Errorf("program = %q, want %q", m[1], tt.wantProg)
			}
		} else if m != nil {
			t.Errorf("clientProgramRe should not match %q", tt.line)
		}
	}
}

func TestClientStationRe(t *testing.T) {
	tests := []struct {
		line        string
		match       bool
		wantStation string
	}{
		{"C2|client station MyStation", true, "MyStation"},
		{"C2|client program SmartSDR-Mac", false, ""},
	}
	for _, tt := range tests {
		m := clientStationRe.FindStringSubmatch(tt.line)
		if tt.match {
			if m == nil {
				t.Errorf("clientStationRe should match %q", tt.line)
			} else if m[1] != tt.wantStation {
				t.Errorf("station = %q, want %q", m[1], tt.wantStation)
			}
		} else if m != nil {
			t.Errorf("clientStationRe should not match %q", tt.line)
		}
	}
}

func TestClientStatusRe(t *testing.T) {
	tests := []struct {
		line         string
		match        bool
		wantHandle   string
		wantClientID string
	}{
		// Standard radio status update for a connected GUI client.
		{"S66F305EF|client 0x66F305EF connected local_ptt=1 client_id=A1B2C3D4-E5F6-7890-ABCD-EF1234567890", true,
			"66F305EF", "A1B2C3D4-E5F6-7890-ABCD-EF1234567890"},
		// Lowercase hex.
		{"S0a273c01|client 0x0a273c01 connected local_ptt=0 client_id=deadbeef-1234-5678-9abc-def012345678", true,
			"0a273c01", "deadbeef-1234-5678-9abc-def012345678"},
		{"C3|client ip", false, "", ""},
	}
	for _, tt := range tests {
		m := clientStatusRe.FindStringSubmatch(tt.line)
		if tt.match {
			if m == nil {
				t.Errorf("clientStatusRe should match %q", tt.line)
				continue
			}
			if m[1] != tt.wantHandle {
				t.Errorf("handle = %q, want %q", m[1], tt.wantHandle)
			}
			if m[2] != tt.wantClientID {
				t.Errorf("clientID = %q, want %q", m[2], tt.wantClientID)
			}
		} else if m != nil {
			t.Errorf("clientStatusRe should not match %q", tt.line)
		}
	}
}

func TestEmptyBindRe(t *testing.T) {
	tests := []struct {
		line  string
		match bool
		seq   string // expected captured sequence number
	}{
		// Empty bind — suppressed by proxy, fake R-response injected.
		{"C5|client bind client_id=", true, "5"},
		{"C12|client bind client_id=", true, "12"},
		// Valid bind — must NOT match.
		{"C5|client bind client_id=A1B2C3D4-E5F6-7890-ABCD-EF1234567890", false, ""},
		// Other commands.
		{"C3|client ip", false, ""},
	}
	for _, tt := range tests {
		m := emptyBindRe.FindStringSubmatch(tt.line)
		got := m != nil
		if got != tt.match {
			t.Errorf("emptyBindRe on %q: got match=%v, want %v", tt.line, got, tt.match)
		}
		if got && m[1] != tt.seq {
			t.Errorf("emptyBindRe on %q: got seq=%q, want %q", tt.line, m[1], tt.seq)
		}
	}
}

func TestClientProgramRewrite(t *testing.T) {
	tests := []struct {
		line    string
		match   bool
		program string
	}{
		{"C4|client program CAT", true, "CAT"},
		{"C5|client program SmartSDR-Win", true, "SmartSDR-Win"},
		{"C6|client program DAX", true, "DAX"},
		{"C3|client ip", false, ""},
	}
	for _, tt := range tests {
		m := clientProgramRewriteRe.FindStringSubmatch(tt.line)
		got := m != nil
		if got != tt.match {
			t.Errorf("clientProgramRewriteRe on %q: got match=%v, want %v", tt.line, got, tt.match)
		}
		if got && m[2] != tt.program {
			t.Errorf("clientProgramRewriteRe on %q: got program=%q, want %q", tt.line, m[2], tt.program)
		}
	}
}

func TestUdpPortRe(t *testing.T) {
	tests := []struct {
		line     string
		match    bool
		wantPort string
	}{
		{"C4|client udpport 4993", true, "4993"},
		{"C4|client udpport 0", true, "0"},
		{"C4|client program SmartSDR", false, ""},
	}
	for _, tt := range tests {
		m := udpPortRe.FindStringSubmatch(tt.line)
		if tt.match {
			if m == nil {
				t.Errorf("udpPortRe should match %q", tt.line)
			} else if m[2] != tt.wantPort {
				t.Errorf("port = %q, want %q", m[2], tt.wantPort)
			}
		} else if m != nil {
			t.Errorf("udpPortRe should not match %q", tt.line)
		}
	}
}

func TestProxyHandleRe(t *testing.T) {
	tests := []struct {
		line       string
		match      bool
		wantHandle string
	}{
		// H-line: first message from radio on TCP connect.
		{"H0A273C01", true, "0A273C01"},
		{"H66F305EF", true, "66F305EF"},
		// Lowercase.
		{"Habcdef01", true, "abcdef01"},
		// Not an H-line.
		{"S0A273C01|client 0x0A273C01 connected", false, ""},
		{"R3|0|10.10.0.3", false, ""},
	}
	for _, tt := range tests {
		m := proxyHandleRe.FindStringSubmatch(tt.line)
		if tt.match {
			if m == nil {
				t.Errorf("proxyHandleRe should match %q", tt.line)
			} else if m[1] != tt.wantHandle {
				t.Errorf("handle = %q, want %q", m[1], tt.wantHandle)
			}
		} else if m != nil {
			t.Errorf("proxyHandleRe should not match %q", tt.line)
		}
	}
}

func TestPingLineRe(t *testing.T) {
	tests := []struct {
		line  string
		match bool
	}{
		{"C1|ping", true},
		{"C99|ping", true},
		{"R1|0|ping", true},
		{"R99|0|ping", true},
		{"C1|client ip", false},
		{"R1|0|10.10.0.3", false},
	}
	for _, tt := range tests {
		got := pingLineRe.MatchString(tt.line)
		if got != tt.match {
			t.Errorf("pingLineRe.MatchString(%q) = %v, want %v", tt.line, got, tt.match)
		}
	}
}

func TestProxyPanOwnerRe(t *testing.T) {
	line := "S66F305EF|display pan 0x40000001 x_pixels=800 y_pixels=600 client_handle=0x0A273C01"
	m := proxyPanOwnerRe.FindStringSubmatch(line)
	if m == nil {
		t.Fatal("proxyPanOwnerRe should match")
	}
	if m[1] != "0x40000001" {
		t.Errorf("pan = %q", m[1])
	}
	if m[2] != "0A273C01" {
		t.Errorf("owner handle = %q", m[2])
	}
}

func TestProxySliceOwnerRe(t *testing.T) {
	line := "S66F305EF|slice 0 RF_frequency=14.200 mode=USB client_handle=0x0A273C01"
	m := proxySliceOwnerRe.FindStringSubmatch(line)
	if m == nil {
		t.Fatal("proxySliceOwnerRe should match")
	}
	if m[1] != "0" {
		t.Errorf("slice = %q", m[1])
	}
	if m[2] != "0A273C01" {
		t.Errorf("owner handle = %q", m[2])
	}
}
