package ai

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AIConfig implements flag.Value to parse key=value pairs for --ai.
// Example: --ai "provider=claude model=opus base=https://... answers=3 profile=work"
// Multiple --ai flags merge; values later override earlier ones.
type AIConfig map[string]string

func (a *AIConfig) String() string {
	if a == nil || *a == nil {
		return ""
	}
	var parts []string
	for k, v := range *a {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, " ")
}

func (a *AIConfig) Set(s string) error {
	if *a == nil {
		*a = make(map[string]string)
	}
	mergeKV(*a, s)
	return nil
}

func mergeKV(dst map[string]string, s string) {
	// Split on commas first, then allow spaces; preserve quoted values.
	for _, chunk := range strings.Split(s, ",") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		for _, tok := range fieldsRespectQuotes(chunk) {
			k, v, ok := strings.Cut(tok, "=")
			if !ok {
				continue
			}
			k = strings.ToLower(strings.TrimSpace(k))
			v = strings.TrimSpace(v)
			v = strings.Trim(v, `"'`)
			dst[k] = v
		}
	}
}

func fieldsRespectQuotes(s string) []string {
	var out []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch r {
		case ' ':
			if inSingle || inDouble {
				cur.WriteRune(r)
			} else {
				flush()
			}
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
			cur.WriteRune(r)
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// ConfigureAIComposite merges AI configuration from, in precedence order:
// 1) Composite --ai key=val pairs (CLI)
// 2) PROMQL_CLI_AI env (key=val pairs)
// 3) Profile file (~/.config/promql-cli/ai.toml) selected by --ai profile= or PROMQL_CLI_AI_PROFILE
// 4) Provider defaults
// The result populates global ai*Flag variables used by providers.
func ConfigureAIComposite(kv map[string]string) {
	cfg := map[string]string{}

	// 2) merge env composite first (lower precedence than CLI)
	if env := strings.TrimSpace(os.Getenv("PROMQL_CLI_AI")); env != "" {
		mergeKV(cfg, env)
	}
	// 1) merge CLI composite
	for k, v := range kv {
		cfg[strings.ToLower(k)] = v
	}

	// 3) profiles (load and merge selected)
	profile := cfg["profile"]
	if profile == "" {
		profile = os.Getenv("PROMQL_CLI_AI_PROFILE")
	}
	if profMap := loadAIProfile(profile); len(profMap) > 0 {
		for k, v := range profMap {
			if _, exists := cfg[k]; !exists { // profile provides defaults unless overridden by CLI/env
				cfg[k] = v
			}
		}
	}

	// Normalize common keys
	prov := strings.ToLower(strings.TrimSpace(firstNonEmpty(cfg["provider"], cfg["prov"])))

	// Apply provider-specific defaults and bind to global ai*Flag vars.
	switch prov {
	case "openai":
		aiProviderFlag = "openai"
		aiOpenAIModelFlag = firstNonEmpty(cfg["model"], cfg["openai_model"], os.Getenv("PROMQL_CLI_OPENAI_MODEL"), "gpt-4o-mini")
		aiOpenAIBaseFlag = firstNonEmpty(cfg["base"], cfg["openai_base"], os.Getenv("PROMQL_CLI_OPENAI_BASE"), "https://api.openai.com/v1")
		// Clear others to avoid accidental carry-over
		aiAnthropicModelFlag, aiAnthropicBaseFlag = "", ""
		aiXAIModelFlag, aiXAIBaseFlag = "", ""
		aiOllamaModelFlag, aiOllamaHostFlag = "", ""
	case "claude", "anthropic":
		aiProviderFlag = "claude"
		aiAnthropicModelFlag = firstNonEmpty(cfg["model"], cfg["claude_model"], cfg["anthropic_model"], os.Getenv("PROMQL_CLI_ANTHROPIC_MODEL"), "claude-3-5-sonnet-20240620")
		aiAnthropicBaseFlag = firstNonEmpty(cfg["base"], cfg["claude_base"], cfg["anthropic_base"], os.Getenv("PROMQL_CLI_ANTHROPIC_BASE"), "https://api.anthropic.com/v1")
		aiOpenAIModelFlag, aiOpenAIBaseFlag = "", ""
		aiXAIModelFlag, aiXAIBaseFlag = "", ""
		aiOllamaModelFlag, aiOllamaHostFlag = "", ""
	case "grok", "xai":
		aiProviderFlag = "grok"
		aiXAIModelFlag = firstNonEmpty(cfg["model"], cfg["xai_model"], os.Getenv("PROMQL_CLI_XAI_MODEL"), "grok-2")
		aiXAIBaseFlag = firstNonEmpty(cfg["base"], cfg["xai_base"], os.Getenv("PROMQL_CLI_XAI_BASE"), "https://api.x.ai/v1")
		aiOpenAIModelFlag, aiOpenAIBaseFlag = "", ""
		aiAnthropicModelFlag, aiAnthropicBaseFlag = "", ""
		aiOllamaModelFlag, aiOllamaHostFlag = "", ""
	case "ollama":
		// default provider if unspecified
		aiProviderFlag = "ollama"
		aiOllamaModelFlag = firstNonEmpty(cfg["model"], cfg["ollama_model"], os.Getenv("PROMQL_CLI_OLLAMA_MODEL"), "llama3.1")
		aiOllamaHostFlag = firstNonEmpty(cfg["base"], cfg["host"], cfg["ollama_host"], os.Getenv("PROMQL_CLI_OLLAMA_HOST"), "http://localhost:11434")
		aiOpenAIModelFlag, aiOpenAIBaseFlag = "", ""
		aiAnthropicModelFlag, aiAnthropicBaseFlag = "", ""
		aiXAIModelFlag, aiXAIBaseFlag = "", ""
	default:
		// Unknown provider; set as-is but don't crash. Fall back to ollama defaults if fields missing.
		aiProviderFlag = prov
	}

	// answers override if provided via composite/profile
	if v := firstNonEmpty(cfg["answers"], cfg["num"], cfg["count"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			aiNumAnswersFlag = n
		}
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// loadAIProfile loads the selected profile from ~/.config/promql-cli/ai.toml.
// Returns a flat map of keys (provider, model, base, answers, host, etc.).
// A very small TOML subset is supported.
func loadAIProfile(profile string) map[string]string {
	if profile == "" {
		// If no explicit profile, still try default if present
		profile = "default"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	path := filepath.Join(home, ".config", "promql-cli", "ai.toml")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	target := "profiles." + profile
	current := ""
	vals := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		// Section header
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sec := strings.TrimSpace(line[1 : len(line)-1])
			current = sec
			continue
		}
		if current != target {
			continue
		}
		// key = value
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		// strip comments at end
		if i := strings.IndexAny(v, "#;"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		v = strings.Trim(v, `"'`)
		if k != "" && v != "" {
			vals[k] = v
		}
	}
	return vals
}
