// Package llm: OpenAI-compatible client (streaming SSE) + presets.
// v0.1 implements openai_compat only. Anthropic native is deferred;
// use OpenRouter/custom baseURL for Claude.
package llm

type Preset struct {
	Key     string
	BaseURL string
	KeyEnv  string
	Model   string // docs-only initial value; model is free-text
}

func Presets() []Preset {
	return []Preset{
		{"openai", "https://api.openai.com/v1", "OPENAI_API_KEY", "gpt-4o-mini"},
		{"gemini", "https://generativelanguage.googleapis.com/v1beta/openai/", "GEMINI_API_KEY", "gemini-2.0-flash"},
		{"groq", "https://api.groq.com/openai/v1", "GROQ_API_KEY", "llama-3.3-70b-versatile"},
		{"openrouter", "https://api.openrouter.ai/api/v1", "OPENROUTER_API_KEY", "openrouter/auto"},
		{"nvidia", "https://integrate.api.nvidia.com/v1", "NVIDIA_API_KEY", "meta/llama-3.3-70b-instruct"},
		{"cerebras", "https://api.cerebras.ai/v1", "CEREBRAS_API_KEY", "llama-3.3-70b"},
		{"mistral", "https://api.mistral.ai/v1", "MISTRAL_API_KEY", "mistral-small-latest"},
		{"deepseek", "https://api.deepseek.com/v1", "DEEPSEEK_API_KEY", "deepseek-chat"},
		{"together", "https://api.together.xyz/v1", "TOGETHER_API_KEY", "meta-llama/Llama-3.3-70B-Instruct-Turbo"},
		{"fireworks", "https://api.fireworks.ai/inference/v1", "FIREWORKS_API_KEY", "accounts/fireworks/models/llama-v3p3-70b-instruct"},
		{"xai", "https://api.x.ai/v1", "XAI_API_KEY", "grok-3-mini"},
		{"ollama", "http://localhost:11434/v1", "", "qwen2.5-coder:7b"},
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
