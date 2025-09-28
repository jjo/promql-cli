package ai

import (
	"os"
	"reflect"
	"strings"
	"testing"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

func TestFieldsRespectQuotes(t *testing.T) {
	in := `provider=claude model="sonnet 3.5" base='https://api.example/v1' answers=3`
	got := fieldsRespectQuotes(in)
	want := []string{
		"provider=claude",
		"model=\"sonnet 3.5\"",
		"base='https://api.example/v1'",
		"answers=3",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fieldsRespectQuotes diff:\n got=%v\nwant=%v", got, want)
	}
}

func TestMergeKV(t *testing.T) {
	dst := map[string]string{}
	mergeKV(dst, "provider=openai, model=gpt-4o-mini answers=2")
	if dst["provider"] != "openai" || dst["model"] != "gpt-4o-mini" || dst["answers"] != "2" {
		t.Fatalf("mergeKV failed, got=%v", dst)
	}
}

func TestConfigureAIComposite_OpenAI(t *testing.T) {
	backupEnv := os.Environ()
	defer func() {
		// restore env by clearing and resetting minimal for safety
		for _, e := range os.Environ() {
			k := strings.SplitN(e, "=", 2)[0]
			_ = os.Unsetenv(k)
		}
		for _, e := range backupEnv {
			kv := strings.SplitN(e, "=", 2)
			if len(kv) == 2 {
				_ = os.Setenv(kv[0], kv[1])
			}
		}
	}()
	// Clear globals relevant to provider
	aiProviderFlag, aiOpenAIModelFlag, aiOpenAIBaseFlag = "", "", ""
	os.Setenv("PROMQL_CLI_AI", "")

	ConfigureAIComposite(map[string]string{
		"provider": "openai",
		"model":    "gpt-4o-mini",
		"base":     "https://api.openai.com/v1",
		"answers":  "4",
	})
	if aiProviderFlag != "openai" {
		t.Fatalf("expected provider openai, got %s", aiProviderFlag)
	}
	if aiOpenAIModelFlag != "gpt-4o-mini" {
		t.Fatalf("expected model gpt-4o-mini, got %s", aiOpenAIModelFlag)
	}
	if !strings.Contains(aiOpenAIBaseFlag, "openai.com") {
		t.Fatalf("expected base to contain openai.com, got %s", aiOpenAIBaseFlag)
	}
}

func TestBuildAIPromptContextAndPrompt(t *testing.T) {
	st := sstorage.NewSimpleStorage()
	// Create two series for metric http_requests_total with labels method/code
	st.Metrics["http_requests_total"] = []sstorage.MetricSample{
		{Labels: map[string]string{"__name__": "http_requests_total", "method": "GET", "code": "200"}, Value: 1},
		{Labels: map[string]string{"__name__": "http_requests_total", "method": "POST", "code": "500"}, Value: 2},
	}
	st.MetricsHelp["http_requests_total"] = "HTTP requests"

	ctx := buildAIPromptContext(st)
	if len(ctx.Metrics) == 0 || ctx.Metrics[0].Name != "http_requests_total" {
		t.Fatalf("expected metric in context, got=%v", ctx.Metrics)
	}
	// Labels sorted
	if got := strings.Join(ctx.Metrics[0].Labels, ","); got != "code,method" {
		t.Fatalf("expected labels sorted code,method; got %s", got)
	}

	p := buildAIPrompt(ctx, "errors over time")
	if !strings.Contains(p, "Task: errors over time") {
		t.Fatalf("prompt missing task, got: %s", p)
	}
	if !strings.Contains(p, "http_requests_total") || !strings.Contains(p, "labels:") {
		t.Fatalf("prompt missing metric/labels: %s", p)
	}
}

func TestParseAISuggestions_Variants(t *testing.T) {
	// JSON answers
	jsonAnswers := `{"answers":[{"query":"rate(http_requests_total[5m])","explain":"request rate"}]}`
	s := parseAISuggestions(jsonAnswers)
	if len(s) != 1 || !strings.Contains(s[0].Query, "http_requests_total") || s[0].Explain == "" {
		t.Fatalf("parseAISuggestions answers failed: %v", s)
	}
	// JSON queries
	jsonQueries := `{"queries":["up","sum(rate(x[5m]))"]}`
	s = parseAISuggestions(jsonQueries)
	if len(s) != 2 || s[0].Query != "up" {
		t.Fatalf("parseAISuggestions queries failed: %v", s)
	}
	// Embedded JSON in text
	emb := "foo bar {\"answers\":[{\"query\":\"up\",\"explain\":\"is up\"}]} baz"
	s = parseAISuggestions(emb)
	if len(s) != 1 || s[0].Query != "up" || s[0].Explain == "" {
		t.Fatalf("parseAISuggestions embedded failed: %v", s)
	}
	// Fenced JSON
	fenced := "```\n{\"queries\":[\"up\"]}\n```"
	s = parseAISuggestions(fenced)
	if len(s) != 1 || s[0].Query != "up" {
		t.Fatalf("parseAISuggestions fenced json failed: %v", s)
	}
	// Plain lines
	plain := "rate(foo_total[5m])\nnot-promql"
	s = parseAISuggestions(plain)
	if len(s) == 0 || !strings.Contains(s[0].Query, "rate(") {
		t.Fatalf("parseAISuggestions plain failed: %v", s)
	}
}

func TestCleanCandidate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"`up`", "up"},
		{"\"up\"", "up"},
		{"'up'", "up"},
		{"````code````", "code"},
	}
	for _, c := range cases {
		if got := CleanCandidate(c.in); got != c.want {
			t.Fatalf("CleanCandidate(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}
