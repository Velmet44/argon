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
	c.History.RememberLimit = 7
	c.History.RememberLimit = 7
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
	if loaded.History.RememberLimit != 7 || !loaded.History.RememberTurns || !loaded.History.ShellHistory {
		t.Fatalf("history roundtrip: %+v", loaded.History)
	}
	_ = os.Getenv
}

func TestLegacyHistoryMigration(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "config.toml")
	// old file: no [history] section, explicit old shell-history choice
	old := "[llm]\npreset = \"groq\"\n[defaults]\nhistory = true\nhistory_limit = 42\n"
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.History.ShellHistory || loaded.History.ShellHistoryLimit != 42 {
		t.Fatalf("migration: %+v", loaded.History)
	}
	if !loaded.History.RememberTurns {
		t.Fatalf("new defaults should be on: %+v", loaded.History)
	}
	// pristine old file: new defaults win
	p2 := filepath.Join(d, "c2.toml")
	if err := os.WriteFile(p2, []byte("[llm]\npreset = \"groq\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l2, err := LoadFrom(p2)
	if err != nil {
		t.Fatal(err)
	}
	if !l2.History.ShellHistory || !l2.History.RememberTurns {
		t.Fatalf("pristine should get new defaults: %+v", l2.History)
	}
}
