package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/Velmet44/argon/internal/config"
	"github.com/Velmet44/argon/internal/llm"
)

func newSettingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "settings",
		Short: "Open the full settings TUI (provider, safety, defaults, appearance)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isTTY() {
				return fmt.Errorf("argon settings needs a TTY — use `argon config list|set` instead")
			}
			return runSettingsTUI()
		},
	}
}

func runSettingsTUI() error {
	working := cfg // struct copy; slices alias — re-slice on edit below
	for {
		var section string
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Argon settings — pick a section").
				Description("Enter to edit, then Save & exit writes ~/.argon/config.toml").
				Options(
					huh.NewOption("1 · Provider & Auth", "provider"),
					huh.NewOption("2 · Execution & Safety", "safety"),
					huh.NewOption("3 · Defaults", "defaults"),
					huh.NewOption("4 · Appearance", "appearance"),
					huh.NewOption("5 · Advanced (paths, clear, export/import, reset)", "advanced"),
					huh.NewOption("Save & exit", "save"),
					huh.NewOption("Discard & exit", "quit"),
				).
				Value(&section),
		)).Run(); err != nil {
			return err
		}
		var err error
		switch section {
		case "provider":
			err = providerForm(&working)
		case "safety":
			err = safetyForm(&working)
		case "defaults":
			err = defaultsForm(&working)
		case "appearance":
			err = appearanceForm(&working)
		case "advanced":
			err = advancedForm(&working)
		case "save":
			cfg = working
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Println("saved.")
			return nil
		default:
			fmt.Println("discarded.")
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func presetKeys() []string {
	var out []string
	for _, p := range llm.Presets() {
		out = append(out, p.Key)
	}
	return out
}

func providerForm(w *config.Config) error {
	// Simple screen: Groq key + model selector. Advanced: full groq/custom editor.
	model := w.LLM.Model
	if model == "" {
		model = llm.DefaultModel
	}
	apiKey := w.LLM.APIKey
	simple := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Groq model").
			Description("Free tier: 30 req/min. Verified IDs from console.groq.com").
			Options(huh.NewOptions(llm.GroqModels...)...).
			Value(&model),
		huh.NewInput().
			Title("Groq API key").
			Description("Get a free key at "+llm.GroqKeyURL+" (or set GROQ_API_KEY)").
			Password(true).Placeholder("(env/empty = use env var)").
			Value(&apiKey),
	))
	if err := simple.Run(); err != nil {
		return err
	}
	w.LLM.Preset = "groq"
	w.LLM.Model = strings.TrimSpace(model)
	if w.LLM.Model == "" {
		w.LLM.Model = llm.DefaultModel
	}
	w.LLM.APIKey = strings.TrimSpace(apiKey)
	var advanced bool
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title("Advanced options (custom base URL / model)?").Value(&advanced),
	)).Run(); err != nil {
		return err
	}
	if !advanced {
		w.LLM.BaseURL = ""
		return testPrompt(w)
	}
	// Advanced: current-style editor, groq|custom only.
	preset, baseURL, freeModel, key := w.LLM.Preset, w.LLM.BaseURL, w.LLM.Model, w.LLM.APIKey
	if preset != "groq" && preset != "custom" {
		preset = "groq"
	}
	adv := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Provider").Options(huh.NewOptions("groq", "custom")...).Value(&preset),
		huh.NewInput().Title("Base URL (empty = preset default)").Placeholder(llm.GroqBaseURL).Value(&baseURL),
		huh.NewInput().Title("Model (free-text, overrides selector)").Placeholder(llm.DefaultModel).Value(&freeModel),
		huh.NewInput().Title("API key").Password(true).Placeholder("(env/empty = use env var)").Value(&key),
	))
	if err := adv.Run(); err != nil {
		return err
	}
	w.LLM.Preset = preset
	w.LLM.BaseURL = strings.TrimSpace(baseURL)
	w.LLM.Model = strings.TrimSpace(freeModel)
	if w.LLM.Model == "" {
		w.LLM.Model = llm.DefaultModel
	}
	w.LLM.APIKey = strings.TrimSpace(key)
	return testPrompt(w)
}

func testPrompt(w *config.Config) error {
	var test bool
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title("Test connection now?").Value(&test),
	)).Run(); err != nil {
		return err
	}
	if test {
		return testConnection(*w)
	}
	return nil
}

func testConnection(w config.Config) error {
	p := llm.Lookup(w.LLM.Preset)
	baseURL := w.LLM.BaseURL
	if baseURL == "" {
		baseURL = p.BaseURL
	}
	model := w.LLM.Model
	if model == "" {
		model = p.Model
	}
	key := w.APIKey()
	if key == "" {
		fmt.Printf("no key — set %s or ARGON_API_KEY first\n", p.KeyEnv)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := llm.CompleteOnce(ctx, llm.TurnRequest{
		BaseURL: baseURL, APIKey: key, Model: model,
		System: "Reply with exactly: ok", User: "ok?", Timeout: 30 * time.Second,
	})
	if err != nil {
		fmt.Printf("connection FAILED: %v\n", err)
		return nil
	}
	fmt.Printf("connection OK (%.120s)\n", strings.ReplaceAll(raw, "\n", " "))
	return nil
}

func intField(title string, v *int, min, max int) *huh.Input {
	s := strconv.Itoa(*v)
	return huh.NewInput().Title(title).Value(&s).Validate(func(s string) error {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < min || n > max {
			return fmt.Errorf("enter %d–%d", min, max)
		}
		*v = n
		return nil
	})
}

func safetyForm(w *config.Config) error {
	llmT, execT := w.Defaults.LLMTimeoutSec, w.Defaults.ExecTimeoutSec
	confirmWrite := w.Defaults.ConfirmWrite
	deny := strings.Join(w.Risk.ExtraDeny, ", ")
	destr := strings.Join(w.Risk.ExtraDestructive, ", ")
	disabled := strings.Join(w.Risk.Disabled, ", ")
	form := huh.NewForm(huh.NewGroup(
		intField("LLM timeout (sec)", &llmT, 10, 300),
		intField("Exec timeout (sec)", &execT, 5, 600),
		huh.NewConfirm().Title("Confirm before WRITE commands?").Value(&confirmWrite),
		huh.NewInput().Title("Extra deny regexes (comma-sep)").Placeholder("deploy-prod").Value(&deny),
		huh.NewInput().Title("Extra destructive regexes (comma-sep)").Value(&destr),
		huh.NewInput().Title("Disable default rule IDs (comma-sep)").Placeholder("read-ls").Value(&disabled),
	))
	if err := form.Run(); err != nil {
		return err
	}
	w.Defaults.LLMTimeoutSec, w.Defaults.ExecTimeoutSec, w.Defaults.ConfirmWrite = llmT, execT, confirmWrite
	w.Risk.ExtraDeny = splitList(deny)
	w.Risk.ExtraDestructive = splitList(destr)
	w.Risk.Disabled = splitList(disabled)
	fmt.Println("destructive stays double-confirm (locked in v0.1).")
	return nil
}

func defaultsForm(w *config.Config) error {
	shell, maxTurns := w.Defaults.Shell, w.Defaults.MaxTurns
	remember, rememberN := w.History.RememberTurns, w.History.RememberLimit
	rememberChars, shellHist, shellLimit := w.History.RememberOutputChars, w.History.ShellHistory, w.History.ShellHistoryLimit
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Default shell").Options(huh.NewOptions("auto", "powershell", "pwsh", "bash", "zsh")...).Value(&shell),
		intField("Max turns (1–20)", &maxTurns, 1, 20),
		huh.NewConfirm().Title("Remember past tasks across runs?").Description("Last tasks are re-sent as context so follow-ups work.").Value(&remember),
		intField("Past tasks to remember (1–20)", &rememberN, 1, 20),
		intField("Output chars kept per past task (100–8000)", &rememberChars, 100, 8000),
		huh.NewConfirm().Title("Include shell history by default?").Value(&shellHist),
		intField("Shell history entries (1–200)", &shellLimit, 1, 200),
	))
	if err := form.Run(); err != nil {
		return err
	}
	w.Defaults.Shell, w.Defaults.MaxTurns = shell, maxTurns
	w.History.RememberTurns, w.History.RememberLimit = remember, rememberN
	w.History.RememberOutputChars, w.History.ShellHistory, w.History.ShellHistoryLimit = rememberChars, shellHist, shellLimit
	return nil
}

func appearanceForm(w *config.Config) error {
	color, verbose, stream := w.UI.Color, w.UI.Verbose, w.UI.Stream
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title("Color output?").Value(&color),
		huh.NewConfirm().Title("Verbose by default?").Value(&verbose),
		huh.NewConfirm().Title("Stream tokens by default?").Value(&stream),
	)).Run(); err != nil {
		return err
	}
	w.UI.Color, w.UI.Verbose, w.UI.Stream = color, verbose, stream
	return nil
}

func advancedForm(w *config.Config) error {
	dir, _ := config.Dir()
	hist := filepath.Join(dir, "history.jsonl")
	var action string
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Advanced — config: "+dir).
			Options(
				huh.NewOption("Show paths", "paths"),
				huh.NewOption("Clear history log", "clear"),
				huh.NewOption("Export config to file", "export"),
				huh.NewOption("Import config from file", "import"),
				huh.NewOption("Reset to defaults", "reset"),
				huh.NewOption("Back", "back"),
			).Value(&action),
	)).Run(); err != nil {
		return err
	}
	switch action {
	case "paths":
		p, _ := config.Path()
		fmt.Printf("config: %s\nhistory: %s\n", p, hist)
	case "clear":
		var yes bool
		if err := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("Delete "+hist+"?").Value(&yes))).Run(); err != nil {
			return err
		}
		if yes {
			_ = os.Remove(hist)
			fmt.Println("cleared.")
		}
	case "export":
		var dest string
		if err := huh.NewForm(huh.NewGroup(huh.NewInput().Title("Export to path").Value(&dest))).Run(); err != nil {
			return err
		}
		if strings.TrimSpace(dest) != "" {
			if err := config.SaveTo(*w, dest); err != nil {
				fmt.Println("export failed:", err)
			} else {
				fmt.Println("exported.")
			}
		}
	case "import":
		var src string
		if err := huh.NewForm(huh.NewGroup(huh.NewInput().Title("Import from path").Value(&src))).Run(); err != nil {
			return err
		}
		if strings.TrimSpace(src) != "" {
			loaded, err := config.LoadFrom(src)
			if err != nil {
				fmt.Println("import failed:", err)
			} else {
				*w = loaded
				fmt.Println("imported (save & exit to persist).")
			}
		}
	case "reset":
		var yes bool
		if err := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("Reset all settings to defaults?").Value(&yes))).Run(); err != nil {
			return err
		}
		if yes {
			*w = config.Default()
			fmt.Println("reset (save & exit to persist).")
		}
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
