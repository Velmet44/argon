// Package llm: OpenAI-compatible client (streaming SSE).
// Providers: groq (default, free) + custom OpenAI-compatible baseURL.
// All wire traffic is openai_compat; model is free-text (selector offers
// presets, never validates — IDs rot).
package llm

type Preset struct {
	Key     string
	BaseURL string
	KeyEnv  string
	Model   string // docs-only initial value; model is free-text
}

// GroqModels are the selector presets (user-verified on console.groq.com).
// Free-text override always allowed via advanced screen / --model.
var GroqModels = []string{
	"openai/gpt-oss-120b",
	"openai/gpt-oss-20b",
	"qwen/qwen3.8-27b",
}

// DefaultModel is the v0.1 default.
const DefaultModel = "openai/gpt-oss-120b"

const GroqBaseURL = "https://api.groq.com/openai/v1"
const GroqKeyEnv = "GROQ_API_KEY"
const GroqKeyURL = "https://console.groq.com/keys"

func Presets() []Preset {
	return []Preset{
		{"groq", GroqBaseURL, GroqKeyEnv, DefaultModel},
		{"custom", "", "ARGON_API_KEY", ""},
	}
}

func Lookup(key string) Preset {
	for _, p := range Presets() {
		if p.Key == key {
			return p
		}
	}
	return Preset{Key: "custom"}
}
