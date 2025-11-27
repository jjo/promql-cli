// Package main provides a tool to generate model information for AI providers.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type ModelInfo struct {
	ID          string `json:"id"`
	Display     string `json:"display,omitempty"`
	Family      string `json:"family,omitempty"`
	ContextK    int    `json:"context_k,omitempty"`
	Description string `json:"description,omitempty"`
}

type Catalog map[string][]ModelInfo // provider -> models

func main() {
	out := flag.String("o", "pkg/ai/ai_models_generated.go", "output file")
	timeout := flag.Duration("timeout", 6*time.Second, "http timeout")
	flag.Parse()

	client := &http.Client{Timeout: *timeout}
	cat := Catalog{}

	// OpenAI (optional; needs OPENAI_API_KEY); fallback to curated list
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		if ms, err := fetchOpenAI(client, key, os.Getenv("PROMQL_CLI_OPENAI_BASE")); err == nil && len(ms) > 0 {
			cat["openai"] = ms
		} else {
			cat["openai"] = curatedOpenAI()
		}
	} else {
		cat["openai"] = curatedOpenAI()
	}
	// xAI (optional; needs XAI_API_KEY); fallback to curated list
	if key := os.Getenv("XAI_API_KEY"); key != "" {
		if ms, err := fetchOpenAICompat(client, key, orDefault(os.Getenv("PROMQL_CLI_XAI_BASE"), "https://api.x.ai/v1")); err == nil && len(ms) > 0 {
			cat["grok"] = ms
		} else {
			cat["grok"] = curatedGrok()
		}
	} else {
		cat["grok"] = curatedGrok()
	}
	// Anthropic: fetch when ANTHROPIC_API_KEY present; otherwise curated fallback
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		if ms, err := fetchAnthropic(client, key, orDefault(os.Getenv("PROMQL_CLI_ANTHROPIC_BASE"), "https://api.anthropic.com/v1")); err == nil && len(ms) > 0 {
			cat["claude"] = ms
		} else {
			cat["claude"] = curatedAnthropic()
		}
	} else {
		cat["claude"] = curatedAnthropic()
	}

	// Ollama local
	if host := orDefault(os.Getenv("PROMQL_CLI_OLLAMA_HOST"), "http://localhost:11434"); host != "" {
		if ms, err := fetchOllama(host, client); err == nil && len(ms) > 0 {
			cat["ollama"] = ms
		} else if cat["ollama"] == nil {
			cat["ollama"] = curatedOllama()
		}
	}

	// Order entries for stable diffs
	for k := range cat {
		sort.Slice(cat[k], func(i, j int) bool { return cat[k][i].ID < cat[k][j].ID })
	}

	if err := writeFile(*out, cat); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
}

func fetchOpenAI(c *http.Client, key, base string) ([]ModelInfo, error) {
	base = strings.TrimRight(orDefault(base, "https://api.openai.com/v1"), "/")
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Data []struct{ ID string } `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	var out []ModelInfo
	for _, d := range r.Data {
		id := d.ID
		// Heuristic: keep common chat LLMs
		if strings.HasPrefix(id, "gpt-4") || strings.HasPrefix(id, "gpt-4o") || strings.Contains(id, "omni") || strings.HasPrefix(id, "o3") {
			out = append(out, ModelInfo{ID: id, Display: id})
		}
	}
	return out, nil
}

func fetchOpenAICompat(c *http.Client, key, base string) ([]ModelInfo, error) {
	base = strings.TrimRight(base, "/")
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Data []struct{ ID string } `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	var out []ModelInfo
	for _, d := range r.Data {
		out = append(out, ModelInfo{ID: d.ID, Display: d.ID})
	}
	return out, nil
}

func fetchOllama(host string, c *http.Client) ([]ModelInfo, error) {
	url := strings.TrimRight(host, "/") + "/api/tags"
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Models []struct {
			Name    string `json:"name"`
			Details struct {
				Family        string `json:"family"`
				ParameterSize string `json:"parameter_size"`
				ContextLength int    `json:"context_length"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	var out []ModelInfo
	for _, m := range r.Models {
		out = append(out, ModelInfo{
			ID:       m.Name,
			Display:  m.Name,
			Family:   m.Details.Family,
			ContextK: m.Details.ContextLength,
		})
	}
	return out, nil
}

func fetchAnthropic(c *http.Client, apiKey, base string) ([]ModelInfo, error) {
	base = strings.TrimRight(base, "/")
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/models", nil)
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	var out []ModelInfo
	for _, d := range r.Data {
		disp := d.DisplayName
		if strings.TrimSpace(disp) == "" {
			disp = d.ID
		}
		out = append(out, ModelInfo{ID: d.ID, Display: disp})
	}
	return out, nil
}

func curatedAnthropic() []ModelInfo {
	return []ModelInfo{
		{ID: "claude-3-5-haiku-20241022", Display: "Claude Haiku 3.5"},
		{ID: "claude-3-5-sonnet-20240620", Display: "Claude Sonnet 3.5 (Old)"},
		{ID: "claude-3-5-sonnet-20241022", Display: "Claude Sonnet 3.5 (New)"},
		{ID: "claude-3-7-sonnet-20250219", Display: "Claude Sonnet 3.7"},
		{ID: "claude-3-haiku-20240307", Display: "Claude Haiku 3"},
		{ID: "claude-3-opus-20240229", Display: "Claude Opus 3"},
		{ID: "claude-opus-4-1-20250805", Display: "Claude Opus 4.1"},
		{ID: "claude-opus-4-20250514", Display: "Claude Opus 4"},
		{ID: "claude-sonnet-4-20250514", Display: "Claude Sonnet 4"},
	}
}

func curatedOpenAI() []ModelInfo {
	return []ModelInfo{
		{ID: "gpt-4o-mini", Display: "gpt-4o-mini"},
		{ID: "gpt-4o", Display: "gpt-4o"},
		{ID: "gpt-4.1-mini", Display: "gpt-4.1-mini"},
		{ID: "gpt-4.1", Display: "gpt-4.1"},
		{ID: "o3-mini", Display: "o3-mini"},
		{ID: "o4-mini", Display: "o4-mini"},
	}
}

func curatedGrok() []ModelInfo {
	return []ModelInfo{
		{ID: "grok-2-1212", Display: "grok-2-1212"},
		{ID: "grok-2-image-1212", Display: "grok-2-image-1212"},
		{ID: "grok-2-vision-1212", Display: "grok-2-vision-1212"},
		{ID: "grok-3", Display: "grok-3"},
		{ID: "grok-3-mini", Display: "grok-3-mini"},
		{ID: "grok-4-0709", Display: "grok-4-0709"},
		{ID: "grok-4-fast-non-reasoning", Display: "grok-4-fast-non-reasoning"},
		{ID: "grok-4-fast-reasoning", Display: "grok-4-fast-reasoning"},
		{ID: "grok-code-fast-1", Display: "grok-code-fast-1"},
	}
}

func curatedOllama() []ModelInfo {
	return []ModelInfo{
		{ID: "llama3.1", Display: "Llama 3.1"},
		{ID: "qwen2.5", Display: "Qwen 2.5"},
	}
}

func writeFile(path string, cat Catalog) error {
	var b strings.Builder
	now := time.Now().UTC().Format(time.RFC3339)
	fmt.Fprintf(&b, "// Code generated by tools/genmodels at %s; DO NOT EDIT.\n", now)
	b.WriteString("package ai\n\n")
	b.WriteString("func init() {\n")
	b.WriteString("modelCatalogHolder = map[string][]ModelInfo{\n")
	for prov, list := range cat {
		fmt.Fprintf(&b, "\t%q: {\n", prov)
		for _, m := range list {
			fmt.Fprintf(&b, "\t\t{ID:%q, Display:%q, Family:%q, ContextK:%d, Description:%q},\n",
				m.ID, m.Display, m.Family, m.ContextK, m.Description)
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n}\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}
