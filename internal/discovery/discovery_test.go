package discovery

import "testing"

func TestRadioFromKV(t *testing.T) {
	kv := map[string]string{
		"serial":              "1234-5678",
		"nickname":            "HamShack",
		"model":               "FLEX-6600M",
		"ip":                  "10.10.0.100",
		"port":                "4992",
		"version":             "3.6.10.123",
		"status":              "Available",
		"gui_client_stations": "WSJT-X, DXLab, Logger32",
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
	if len(r.Stations) != 3 || r.Stations[0] != "WSJT-X" {
		t.Errorf("Stations = %v", r.Stations)
	}

	// whitespace-only stations get filtered
	r2 := radioFromKV(map[string]string{"gui_client_stations": "  , , WSJT-X , "})
	if len(r2.Stations) != 1 || r2.Stations[0] != "WSJT-X" {
		t.Errorf("whitespace filter: Stations = %v", r2.Stations)
	}

	// empty map shouldn't panic
	r3 := radioFromKV(map[string]string{})
	if r3.Raw == nil {
		t.Fatal("Raw is nil for empty map")
	}
}
