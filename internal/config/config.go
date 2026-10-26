// Package config owns ~/.argon/config.toml loading, saving, and precedence:
// env var > config file > built-in default. Secrets are never printed
// unless explicitly requested with --show.
package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type LLMConfig struct {
	Preset  string `toml:"preset"`
	Model   string `toml:"model"`
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"` // empty = use env var
}

type DefaultsConfig struct {
	Shell         string `toml:"shell"` // auto|powershell|pwsh|bash|zsh
	MaxTurns      int    `toml:"max_turns"`
	LLMTimeoutSec int    `toml:"llm_timeout_sec"`
	ExecTimeoutSec int   `toml:"exec_timeout_sec"`
	History       bool   `toml:"history"`
	HistoryLimit  int    `toml:"history_limit"`
	ConfirmWrite  bool   `toml:"confirm_write"`
}

type UIConfig struct {
	Color   bool `toml:"color"`
	Verbose bool `toml:"verbose"`
	Stream  bool `toml:"stream"`
}

type RiskConfig struct {
	// User-customizable policy. Default deny/destructive lists live in
	// internal/risk/policy.go. These fields ADD to or REMOVE from them.
	ExtraDeny        []string `toml:"extra_deny"`        // extra deny regexes
	ExtraDestructive []string `toml:"extra_destructive"` // extra double-confirm regexes
	Disabled         []string `toml:"disabled"`          // rule IDs from defaults to turn off
}

type Config struct {
	LLM      LLMConfig      `toml:"llm"`
	Defaults DefaultsConfig `toml:"defaults"`
	UI       UIConfig       `toml:"ui"`
	Risk     RiskConfig     `toml:"risk"`
}

func Default() Config {
	return Config{
		LLM: LLMConfig{Preset: "groq", Model: "openai/gpt-oss-120b", BaseURL: ""},
		Defaults: DefaultsConfig{
			Shell: "auto", MaxTurns: 8,
			LLMTimeoutSec: 90, ExecTimeoutSec: 60,
			History: false, HistoryLimit: 50, ConfirmWrite: true,
		},
		UI: UIConfig{Color: true, Verbose: false, Stream: true},
	}
}

func Dir() (string, error) {
	if d := os.Getenv("ARGON_HOME"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	// Windows: %APPDATA%\argon, Unix: ~/.argon (both resolve sensibly here;
	// APPDATA is preferred on Windows when set).
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		return filepath.Join(appdata, "argon"), nil
	}
	return filepath.Join(home, ".argon"), nil
}

func Path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.toml"), nil
}

func Load() (Config, error) {
	cfg := Default()
	p, err := Path()
	if err != nil {
		return cfg, err
	}
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(p, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Defaults.MaxTurns <= 0 {
		cfg.Defaults.MaxTurns = 8
	}
	if cfg.Defaults.MaxTurns > 20 {
		cfg.Defaults.MaxTurns = 20
	}
	return cfg, nil
}

func Save(cfg Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	return SaveTo(cfg, p)
}

// SaveTo writes cfg to an explicit path (export).
func SaveTo(cfg Config, p string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// LoadFrom reads cfg from an explicit path (import).
func LoadFrom(p string) (Config, error) {
	cfg := Default()
	if _, err := toml.DecodeFile(p, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// APIKey resolves env-var first, then config file.
func (c Config) APIKey() string {
	if k := os.Getenv("ARGON_API_KEY"); k != "" {
		return k
	}
	if env := presetKeyEnv(c.LLM.Preset); env != "" {
		if k := os.Getenv(env); k != "" {
			return k
		}
	}
	return c.LLM.APIKey
}

func presetKeyEnv(preset string) string {
	switch preset {
	case "groq":
		return "GROQ_API_KEY"
	case "custom":
		return "ARGON_API_KEY"
	}
	return ""
}
