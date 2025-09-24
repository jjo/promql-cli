package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// aiSuggestQueries produces PromQL query suggestions for a free-text intent using a selected AI provider.
// Provider selection via env PROMQL_CLI_AI_PROVIDER: ollama|openai|claude|grok (default: ollama)
// Models and endpoints via envs: see per-provider functions below.
// Global AI configuration (flags override env).
var (
	aiProviderFlag       string
	aiNumAnswersFlag     int
	aiOpenAIModelFlag    string
	aiOpenAIBaseFlag     string
	aiAnthropicModelFlag string
	aiAnthropicBaseFlag  string
	aiXAIModelFlag       string
	aiXAIBaseFlag        string
	aiOllamaModelFlag    string
	aiOllamaHostFlag     string
)

func ConfigureAIFromFlags(provider string, openaiModel, openaiBase, claudeModel, claudeBase, xaiModel, xaiBase, ollamaModel, ollamaHost string) {
	aiProviderFlag = strings.ToLower(strings.TrimSpace(provider))
	aiOpenAIModelFlag = strings.TrimSpace(openaiModel)
	aiOpenAIBaseFlag = strings.TrimSpace(openaiBase)
	aiAnthropicModelFlag = strings.TrimSpace(claudeModel)
	aiAnthropicBaseFlag = strings.TrimSpace(claudeBase)
	aiXAIModelFlag = strings.TrimSpace(xaiModel)
	aiXAIBaseFlag = strings.TrimSpace(xaiBase)
	aiOllamaModelFlag = strings.TrimSpace(ollamaModel)
	aiOllamaHostFlag = strings.TrimSpace(ollamaHost)
}

func aiSuggestQueries(storage *SimpleStorage, intent string) ([]AISuggestion, error) {
	return aiSuggestQueriesCtx(context.Background(), storage, intent)
}

// aiSuggestQueriesCtx is like aiSuggestQueries but allows cancellation via context.
func aiSuggestQueriesCtx(ctx context.Context, storage *SimpleStorage, intent string) ([]AISuggestion, error) {
	provider := aiProviderFlag
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(os.Getenv("PROMQL_CLI_AI_PROVIDER")))
	}
	if provider == "" {
		provider = "ollama"
	}
	pctx := buildAIPromptContext(storage)
	prompt := buildAIPrompt(pctx, intent)

	switch provider {
case "ollama":
		return aiOllama(ctx, prompt)
	case "openai":
		return aiOpenAI(ctx, prompt)
	case "claude":
		return aiClaude(ctx, prompt)
	case "grok":
		return aiGrok(ctx, prompt)
	default:
		return nil, fmt.Errorf("unknown AI provider: %s", provider)
	}
}

type promptContext struct {
	Metrics []metricInfo
	NowRFC  string
	NumAns  int
}

type AISuggestion struct {
	Query   string
	Explain string
}
type metricInfo struct {
	Name   string
	Help   string
	Labels []string
}

func buildAIPromptContext(storage *SimpleStorage) promptContext {
	// desired number of answers
	num := aiDesiredNum()
	var metrics []metricInfo
	for name, samples := range storage.metrics {
		// Collect label names (excluding __name__)
		labelSet := map[string]bool{}
		for _, s := range samples {
			for k := range s.Labels {
				if k != "__name__" {
					labelSet[k] = true
				}
			}
		}
		var labels []string
		for k := range labelSet {
			labels = append(labels, k)
		}
		sort.Strings(labels)
		help := ""
		if storage.metricsHelp != nil {
			help = storage.metricsHelp[name]
		}
		metrics = append(metrics, metricInfo{Name: name, Help: help, Labels: labels})
	}
	// Sort metrics by name and cap to 60 to keep prompt small
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Name < metrics[j].Name })
	if len(metrics) > 60 {
		metrics = metrics[:60]
	}
	return promptContext{Metrics: metrics, NowRFC: time.Now().UTC().Format(time.RFC3339), NumAns: num}
}

func buildAIPrompt(ctx promptContext, intent string) string {
	var b strings.Builder
	b.WriteString("You are an expert in monitoring and observability that writes PromQL queries which provide useful insights.\n")
	b.WriteString("Use only the listed metrics and their labels. Prefer rate() for *_total counters.\n")
	b.WriteString("Return valid PromQL. Output JSON as {\"answers\":[{\"query\":\"...\",\"explain\":\"one short sentence\"}, ...]}. Return up to ")
	b.WriteString(fmt.Sprintf("%d", ctx.NumAns))
	b.WriteString(" concise answers.\n\n")
	b.WriteString("Current time: ")
	b.WriteString(ctx.NowRFC)
	b.WriteString("\n\nMetrics:\n")
	for _, m := range ctx.Metrics {
		b.WriteString("- ")
		b.WriteString(m.Name)
		if m.Help != "" {
			b.WriteString(" (help: ")
			if len(m.Help) > 120 {
				b.WriteString(m.Help[:120])
				b.WriteString("...")
			} else {
				b.WriteString(m.Help)
			}
			b.WriteString(")")
		}
		if len(m.Labels) > 0 {
			b.WriteString(" labels: {")
			for i, l := range m.Labels {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(l)
			}
			b.WriteString("}")
		}
		b.WriteString("\n")
	}
	b.WriteString("\nTask: ")
	b.WriteString(intent)
	b.WriteString("\n")
	return b.String()
}

// Provider: Ollama (local)
func aiOllama(ctx context.Context, prompt string) ([]AISuggestion, error) {
	host := aiOllamaHostFlag
	if host == "" {
		host = os.Getenv("PROMQL_CLI_OLLAMA_HOST")
	}
	if host == "" {
		host = "http://localhost:11434"
	}
	model := aiOllamaModelFlag
	if model == "" {
		model = os.Getenv("PROMQL_CLI_OLLAMA_MODEL")
	}
	if model == "" {
		model = "llama3.1"
	}
	url := strings.TrimRight(host, "/") + "/api/chat"
	reqBody := map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "system", "content": "You write PromQL."}, {"role": "user", "content": prompt}},
		"stream":   false,
	}
return postAndExtractAISuggestions(ctx, url, "", reqBody, func(r io.Reader) (string, error) {
		var resp struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := json.NewDecoder(r).Decode(&resp); err != nil {
			return "", err
		}
		return resp.Message.Content, nil
	})
}

// Provider: OpenAI-compatible
func aiOpenAI(ctx context.Context, prompt string) ([]AISuggestion, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("missing OPENAI_API_KEY")
	}
	base := aiOpenAIBaseFlag
	if base == "" {
		base = os.Getenv("PROMQL_CLI_OPENAI_BASE")
	}
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	model := aiOpenAIModelFlag
	if model == "" {
		model = os.Getenv("PROMQL_CLI_OPENAI_MODEL")
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	url := strings.TrimRight(base, "/") + "/chat/completions"
	head := "Bearer " + apiKey
	reqBody := map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "system", "content": "You write PromQL."}, {"role": "user", "content": prompt}},
		"temperature": 0.2,
	}
return postAndExtractAISuggestions(ctx, url, head, reqBody, func(r io.Reader) (string, error) {
		var resp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(r).Decode(&resp); err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", errors.New("no choices")
		}
		return resp.Choices[0].Message.Content, nil
	})
}

// Provider: Claude (Anthropic)
func aiClaude(ctx context.Context, prompt string) ([]AISuggestion, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, errors.New("missing ANTHROPIC_API_KEY")
	}
	base := aiAnthropicBaseFlag
	if base == "" {
		base = os.Getenv("PROMQL_CLI_ANTHROPIC_BASE")
	}
	if base == "" {
		base = "https://api.anthropic.com/v1"
	}
	model := aiAnthropicModelFlag
	if model == "" {
		model = os.Getenv("PROMQL_CLI_ANTHROPIC_MODEL")
	}
	if model == "" {
		model = "claude-3-5-sonnet-20240620"
	}
	url := strings.TrimRight(base, "/") + "/messages"
	head := apiKey // special header form used below
	reqBody := map[string]any{
		"model":      model,
		"max_tokens": 800,
		"messages": []map[string]any{{
			"role":    "user",
			"content": []map[string]string{{"type": "text", "text": prompt}},
		}},
	}
return postAndExtractAISuggestionsAnthropic(ctx, url, head, reqBody)
}

// Provider: Grok (xAI) — OpenAI-compatible style
func aiGrok(ctx context.Context, prompt string) ([]AISuggestion, error) {
	apiKey := os.Getenv("XAI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("missing XAI_API_KEY")
	}
	base := aiXAIBaseFlag
	if base == "" {
		base = os.Getenv("PROMQL_CLI_XAI_BASE")
	}
	if base == "" {
		base = "https://api.x.ai/v1"
	}
	model := aiXAIModelFlag
	if model == "" {
		model = os.Getenv("PROMQL_CLI_XAI_MODEL")
	}
	if model == "" {
		model = "grok-2"
	}
	url := strings.TrimRight(base, "/") + "/chat/completions"
	head := "Bearer " + apiKey
	reqBody := map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "system", "content": "You write PromQL."}, {"role": "user", "content": prompt}},
		"temperature": 0.2,
	}
return postAndExtractAISuggestions(ctx, url, head, reqBody, func(r io.Reader) (string, error) {
		var resp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(r).Decode(&resp); err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", errors.New("no choices")
		}
		return resp.Choices[0].Message.Content, nil
	})
}

// Helpers
func postAndExtractAISuggestions(ctx context.Context, url, bearer string, body any, extract func(io.Reader) (string, error)) ([]AISuggestion, error) {
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		return nil, err
	}
req, err := http.NewRequestWithContext(ctx, "POST", url, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("AI HTTP %d: %s", resp.StatusCode, string(b))
	}
	text, err := extract(resp.Body)
	if err != nil {
		return nil, err
	}
	sug := parseAISuggestions(text)
	if len(sug) == 0 && os.Getenv("PROMQL_CLI_AI_DEBUG") == "true" {
		fmt.Fprintln(os.Stderr, "AI raw response:")
		fmt.Fprintln(os.Stderr, text)
	}
	return sug, nil
}

func postAndExtractAISuggestionsAnthropic(ctx context.Context, url, apiKey string, body any) ([]AISuggestion, error) {
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		return nil, err
	}
req, err := http.NewRequestWithContext(ctx, "POST", url, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("AI HTTP %d: %s", resp.StatusCode, string(b))
	}
	var ar struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return nil, err
	}
	if len(ar.Content) == 0 {
		return nil, errors.New("no content")
	}
	return parseAISuggestions(ar.Content[0].Text), nil
}

// parseAISuggestions tries JSON {answers:[{query,explain}]} first, then {queries:[...]}, then code/lines.
func parseAISuggestions(s string) []AISuggestion {
	s = strings.TrimSpace(s)

	// Try direct JSON: {"answers": [...]}
	var a struct {
		Answers []struct {
			Query   string `json:"query"`
			Explain string `json:"explain"`
		} `json:"answers"`
	}
	if json.Unmarshal([]byte(s), &a) == nil && len(a.Answers) > 0 {
		out := make([]AISuggestion, 0, len(a.Answers))
		for _, ans := range a.Answers {
			q := cleanCandidate(ans.Query)
			if q != "" {
				out = append(out, AISuggestion{Query: q, Explain: strings.TrimSpace(ans.Explain)})
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	// Try direct JSON: {"queries": ["..."]}
	var j struct {
		Queries []string `json:"queries"`
	}
	if json.Unmarshal([]byte(s), &j) == nil && len(j.Queries) > 0 {
		out := make([]AISuggestion, 0, len(j.Queries))
		for _, q := range j.Queries {
			if t := cleanCandidate(q); t != "" {
				out = append(out, AISuggestion{Query: t})
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	// Try to extract embedded JSON object if present in text
	if l := strings.IndexByte(s, '{'); l != -1 {
		if r := strings.LastIndexByte(s, '}'); r != -1 && r > l {
			candidate := strings.TrimSpace(s[l : r+1])
			var a3 struct {
				Answers []struct{ Query, Explain string } `json:"answers"`
			}
			if json.Unmarshal([]byte(candidate), &a3) == nil && len(a3.Answers) > 0 {
				out := make([]AISuggestion, 0, len(a3.Answers))
				for _, ans := range a3.Answers {
					q := cleanCandidate(ans.Query)
					if q != "" {
						out = append(out, AISuggestion{Query: q, Explain: strings.TrimSpace(ans.Explain)})
					}
				}
				if len(out) > 0 {
					return out
				}
			}
			var j3 struct {
				Queries []string `json:"queries"`
			}
			if json.Unmarshal([]byte(candidate), &j3) == nil && len(j3.Queries) > 0 {
				out := make([]AISuggestion, 0, len(j3.Queries))
				for _, q := range j3.Queries {
					if t := cleanCandidate(q); t != "" {
						out = append(out, AISuggestion{Query: t})
					}
				}
				if len(out) > 0 {
					return out
				}
			}
		}
	}

	// Handle fenced code blocks: try to parse JSON inside first; otherwise treat as lines
	if strings.HasPrefix(s, "```") {
		// Extract the first fenced block
		body := s
		if idx := strings.Index(body, "\n"); idx != -1 {
			// Header line may be ```json or ```promql; drop it
			body = body[idx+1:]
		}
		body = strings.TrimSuffix(body, "```")
		trimmed := strings.TrimSpace(body)
		// Attempt JSON parse of the fenced body
		var a2 struct {
			Answers []struct {
				Query   string `json:"query"`
				Explain string `json:"explain"`
			} `json:"answers"`
		}
		if json.Unmarshal([]byte(trimmed), &a2) == nil && len(a2.Answers) > 0 {
			out := make([]AISuggestion, 0, len(a2.Answers))
			for _, ans := range a2.Answers {
				q := cleanCandidate(ans.Query)
				if q != "" {
					out = append(out, AISuggestion{Query: q, Explain: strings.TrimSpace(ans.Explain)})
				}
			}
			if len(out) > 0 {
				return out
			}
		}
		var j2 struct {
			Queries []string `json:"queries"`
		}
		if json.Unmarshal([]byte(trimmed), &j2) == nil && len(j2.Queries) > 0 {
			out := make([]AISuggestion, 0, len(j2.Queries))
			for _, q := range j2.Queries {
				if t := cleanCandidate(q); t != "" {
					out = append(out, AISuggestion{Query: t})
				}
			}
			if len(out) > 0 {
				return out
			}
		}
		// Not JSON; fall through to line-by-line extraction
	}

	// Fallback: collect any lines that look like PromQL
	out := []AISuggestion{}
	var lines []string
	if strings.HasPrefix(s, "```") {
		body := s
		if idx := strings.Index(body, "\n"); idx != -1 {
			body = body[idx+1:]
		}
		body = strings.TrimSuffix(body, "```")
		lines = strings.Split(body, "\n")
	} else {
		lines = strings.Split(s, "\n")
	}
	for _, line := range lines {
		if t := cleanCandidate(line); t != "" && looksLikePromQL(t) {
			out = append(out, AISuggestion{Query: t})
		}
	}
	if len(out) == 0 {
		if t := cleanCandidate(s); t != "" {
			out = append(out, AISuggestion{Query: t})
		}
	}
	return out
}

func aiDesiredNum() int {
	// flag first
	if aiNumAnswersFlag > 0 {
		return aiNumAnswersFlag
	}
	// env fallback
	if v := strings.TrimSpace(os.Getenv("PROMQL_CLI_AI_NUM")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 3
}

// cleanCandidate removes surrounding quotes/backticks/fences and trims spaces.
func cleanCandidate(in string) string {
	s := strings.TrimSpace(in)
	if s == "" {
		return ""
	}
	// Remove triple backtick fences if line contains them inline
	if strings.HasPrefix(s, "```") && strings.HasSuffix(s, "```") && len(s) >= 6 {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	// Repeatedly strip matching wrapping quotes/backticks
	for {
		if len(s) >= 2 {
			first, last := s[0], s[len(s)-1]
			if (first == last) && (first == '"' || first == '\'' || first == '`') {
				s = strings.TrimSpace(s[1 : len(s)-1])
				continue
			}
		}
		break
	}
	return strings.TrimSpace(s)
}

func looksLikePromQL(line string) bool {
	// Heuristic: contains a metric-like token or a function(
	return strings.Contains(line, "(") || strings.Contains(line, "{") || strings.Contains(line, "[")
}
