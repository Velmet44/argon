package risk

import "regexp"

var secretRe = regexp.MustCompile(`(?i)(api[_-]?key|token|passwd|password|secret|bearer|aws_.{0,20}|client[_-]?secret)\s*[:=]\s*\S+`)

// Redact replaces secret-looking assignments before anything is sent to the LLM
// (prompts, history entries, command outputs).
func Redact(s string) string { return secretRe.ReplaceAllString(s, "$1=[REDACTED]") }
