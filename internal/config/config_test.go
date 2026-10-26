package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "config.toml")
	c := Default()
	c.LLM.Preset = "nvidia"
	c.LLM.Model = "x"
	c.Defaults.MaxTurns = 5
	c.Risk.ExtraDeny = []string{"deploy-prod"}
	if err := SaveTo(c, p); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LLM.Preset != "nvidia" || loaded.Defaults.MaxTurns != 5 || len(loaded.Risk.ExtraDeny) != 1 {
		t.Fatalf("roundtrip: %+v", loaded)
	}
	_ = os.Getenv
}
