package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

func setConfigHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", dir)
	xdg.Reload()
}

func TestDefaultsHasSaneValues(t *testing.T) {
	d := Defaults()
	if d.Radio != ":discover:" {
		t.Errorf("Radio = %q, want :discover:", d.Radio)
	}
	if d.Station != "Flex" {
		t.Errorf("Station = %q, want Flex", d.Station)
	}
	if !d.MeteringEnabled() {
		t.Error("Metering should default to true")
	}
}

func TestLoadMissing(t *testing.T) {
	setConfigHome(t, t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Radio != ":discover:" {
		t.Errorf("Radio = %q, want :discover:", cfg.Radio)
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmp := t.TempDir()
	setConfigHome(t, tmp)

	cfg := Defaults()
	cfg.Radio = "10.10.0.10"
	cfg.Web = ":8080"
	cfg.SetMetering(false)

	if err := Save(&cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	p := filepath.Join(tmp, "flexy", "config.json")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", p, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 600", perm)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Radio != "10.10.0.10" {
		t.Errorf("Radio = %q, want 10.10.0.10", loaded.Radio)
	}
	if loaded.Web != ":8080" {
		t.Errorf("Web = %q, want :8080", loaded.Web)
	}
	if loaded.MeteringEnabled() {
		t.Error("Metering should be false after save/load")
	}
	if loaded.Station != "Flex" {
		t.Errorf("Station = %q, want Flex (default)", loaded.Station)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	setConfigHome(t, tmp)

	dir := filepath.Join(tmp, "flexy")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{bad json"), 0o600)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
