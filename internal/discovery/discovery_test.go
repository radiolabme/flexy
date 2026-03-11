package discovery

import "testing"

func TestRadioFromKV_Basic(t *testing.T) {
	kv := map[string]string{
		"serial":   "1234-5678",
		"nickname": "HamShack",
		"model":    "FLEX-6600M",
		"ip":       "10.10.0.100",
		"port":     "4992",
		"version":  "3.6.10.123",
		"status":   "Available",
	}
	r := radioFromKV(kv)
	if r.Serial != "1234-5678" {
		t.Errorf("Serial = %q", r.Serial)
	}
	if r.Nickname != "HamShack" {
		t.Errorf("Nickname = %q", r.Nickname)
	}
	if r.IP != "10.10.0.100" {
		t.Errorf("IP = %q", r.IP)
	}
	if r.Raw == nil {
		t.Fatal("Raw is nil")
	}
	if len(r.Stations) != 0 {
		t.Errorf("Stations = %v, want empty", r.Stations)
	}
}

func TestRadioFromKV_Stations(t *testing.T) {
	kv := map[string]string{
		"gui_client_stations": "WSJT-X, DXLab, Logger32",
	}
	r := radioFromKV(kv)
	if len(r.Stations) != 3 {
		t.Fatalf("got %d stations, want 3", len(r.Stations))
	}
	want := []string{"WSJT-X", "DXLab", "Logger32"}
	for i, w := range want {
		if r.Stations[i] != w {
			t.Errorf("Stations[%d] = %q, want %q", i, r.Stations[i], w)
		}
	}
}

func TestRadioFromKV_StationsWhitespace(t *testing.T) {
	kv := map[string]string{
		"gui_client_stations": "  , , WSJT-X , ",
	}
	r := radioFromKV(kv)
	if len(r.Stations) != 1 || r.Stations[0] != "WSJT-X" {
		t.Errorf("Stations = %v, want [WSJT-X]", r.Stations)
	}
}

func TestRadioFromKV_EmptyMap(t *testing.T) {
	r := radioFromKV(map[string]string{})
	if r.Raw == nil {
		t.Fatal("Raw is nil for empty map")
	}
	if r.Serial != "" || r.IP != "" {
		t.Errorf("expected zero values, got Serial=%q IP=%q", r.Serial, r.IP)
	}
}
