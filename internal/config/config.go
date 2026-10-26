// Package config owns ~/.argon/config.toml loading, saving, and precedence:
// env var > config file > built-in default. Secrets are never printed
// unless explicitly requested with --show.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

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

// HistoryConfig controls what past context enters each prompt.
// All of it stays on this machine (~/.argon/history.jsonl, 0600);
// history is only ever uploaded as (redacted) prompt context.
type HistoryConfig struct {
	RememberTurns       bool `toml:"remember_turns"`        // include own past tasks
	RememberLimit       int  `toml:"remember_limit"`        // last N finished tasks
	RememberOutputChars int  `toml:"remember_output_chars"` // stored/sent output chars per past turn
	ShellHistory        bool `toml:"shell_history"`         // include PSReadLine/bash history
	ShellHistoryLimit   int  `toml:"shell_history_limit"`   // entries, newest last
	FileMax             int  `toml:"file_max"`              // prune history file to N lines
}

type Config struct {
	LLM      LLMConfig      `toml:"llm"`
	Defaults DefaultsConfig `toml:"defaults"`
	UI       UIConfig       `toml:"ui"`
	Risk     RiskConfig     `toml:"risk"`
	History  HistoryConfig  `toml:"history"`
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
		History: HistoryConfig{
			RememberTurns: true, RememberLimit: 5, RememberOutputChars: 800,
			ShellHistory: true, ShellHistoryLimit: 50, FileMax: 200,
		},
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
	p, err := Path()
	if err != nil {
		return Default(), err
	}
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return Default(), err
	}
	return decode(raw)
}

// LoadFrom reads cfg from an explicit path (import). Same migration and
// clamps as Load so imports behave identically.
func LoadFrom(p string) (Config, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return Default(), err
	}
	return decode(raw)
}

func decode(raw []byte) (Config, error) {
	cfg := Default()
	if _, err := toml.Decode(string(raw), &cfg); err != nil {
		return cfg, err
	}
	// Migrate pre-[history] configs: an explicit old shell-history choice
	// wins over the new default; pristine files get the new defaults.
	if !strings.Contains(string(raw), "[history") {
		if cfg.Defaults.History || cfg.Defaults.HistoryLimit != 50 {
			cfg.History.ShellHistory = cfg.Defaults.History
			if cfg.Defaults.HistoryLimit > 0 {
				cfg.History.ShellHistoryLimit = cfg.Defaults.HistoryLimit
			}
		}
	}
	if cfg.Defaults.MaxTurns <= 0 {
		cfg.Defaults.MaxTurns = 8
	}
	if cfg.Defaults.MaxTurns > 20 {
		cfg.Defaults.MaxTurns = 20
	}
	if cfg.History.RememberLimit <= 0 {
		cfg.History.RememberLimit = 5
	}
	if cfg.History.RememberOutputChars <= 0 {
		cfg.History.RememberOutputChars = 800
	}
	if cfg.History.ShellHistoryLimit <= 0 {
		cfg.History.ShellHistoryLimit = 50
	}
	if cfg.History.FileMax < 10 {
		cfg.History.FileMax = 200
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
