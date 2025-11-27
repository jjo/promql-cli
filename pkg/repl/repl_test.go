package repl

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

func newTestStore(t *testing.T) *sstorage.SimpleStorage {
	t.Helper()
	store := sstorage.NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sstorage.SampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}
	return store
}

func newTestEngine() *promql.Engine {
	return promql.NewEngine(promql.EngineOpts{
		Logger:                   nil,
		Reg:                      nil,
		MaxSamples:               50_000_000,
		Timeout:                  30 * time.Second,
		LookbackDelta:            5 * time.Minute,
		EnableAtModifier:         true,
		EnableNegativeOffset:     true,
		NoStepSubqueryIntervalFn: func(_ int64) int64 { return 60 * 1000 },
	})
}

// Ensure that queries with PromQL @ modifier using milliseconds are normalized and do not error.
func TestExecuteOne_AtModifierWithMillis_Works(t *testing.T) {
	store := sstorage.NewSimpleStorage()
	content := "cloudcost_azure_aks_storage_by_location_usd_per_gibyte_hour{location=\"eastus\"} 1 1700000000000\n" +
		"cloudcost_azure_aks_storage_by_location_usd_per_gibyte_hour{location=\"eastus\"} 2 1700000010000\n"
	if err := store.LoadFromReader(strings.NewReader(content)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	engine := promql.NewEngine(promql.EngineOpts{
		Logger:                   nil,
		Reg:                      nil,
		MaxSamples:               50_000_000,
		Timeout:                  30 * time.Second,
		LookbackDelta:            5 * time.Minute,
		EnableAtModifier:         true,
		EnableNegativeOffset:     true,
		NoStepSubqueryIntervalFn: func(_ int64) int64 { return 60 * 1000 },
	})

	q := "cloudcost_azure_aks_storage_by_location_usd_per_gibyte_hour @1700000000000"
	out := captureStdout(t, func() {
		executeOne(engine, store, q)
	})
	if strings.Contains(out, "Error creating query:") || strings.Contains(out, "Error:") {
		t.Fatalf("unexpected error output executing query with @ millis: %s", out)
	}
}

func TestBangExec_Echo(t *testing.T) {
	store := sstorage.NewSimpleStorage()
	engine := newTestEngine()
	out := captureStdout(t, func() {
		executeOne(engine, store, "!echo HELLO")
	})
	if !strings.Contains(out, "HELLO") {
		t.Fatalf("expected HELLO from !echo, got: %s", out)
	}
}

func TestPipeExec_CatReceivesOutput(t *testing.T) {
	store := newTestStore(t)
	engine := newTestEngine()
	out := captureStdout(t, func() {
		executeOne(engine, store, "sum(http_requests_total) | cat")
	})
	if !strings.Contains(out, "Vector (") {
		t.Fatalf("expected Vector output piped through cat, got: %s", out)
	}
}

func TestPipeSplit_IgnoresPipeInsideQuotes(t *testing.T) {
	store := newTestStore(t)
	engine := newTestEngine()
	// The '|' appears inside the quoted regex and must not be treated as a pipeline
	out := captureStdout(t, func() {
		executeOne(engine, store, "http_requests_total{code=~\"200|404\"}")
	})
	if strings.Contains(out, "parse error") || strings.Contains(out, "Error:") {
		t.Fatalf("unexpected error executing query with '|' inside quotes: %s", out)
	}
	// Now verify that an external pipeline still works when a '|' exists inside quotes
	out = captureStdout(t, func() {
		executeOne(engine, store, "http_requests_total{code=~\"200|404\"} | grep 404")
	})
	if !strings.Contains(out, "404") {
		t.Fatalf("expected piped output to contain 404 label line, got: %s", out)
	}
}

func TestAutoCompleter_MetricNameCompletion(t *testing.T) {
	store := newTestStore(t)
	ac := NewPrometheusAutoCompleter(store)

	completions := ac.getMetricNameCompletions("http")
	found := false
	for _, c := range completions {
		if c == "http_requests_total" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'http_requests_total' in completions for prefix 'http' (got: %v)", completions)
	}
}

func TestAutoCompleter_LabelNameAndValueCompletion(t *testing.T) {
	store := newTestStore(t)
	ac := NewPrometheusAutoCompleter(store)

	// Label names for a specific metric
	ln := ac.getLabelNameCompletions("http_requests_total", "")
	if !contains(ln, "method") || !contains(ln, "code") {
		t.Fatalf("expected label names 'method' and 'code' for http_requests_total, got=%v", ln)
	}
	if contains(ln, "__name__") {
		t.Fatalf("did not expect __name__ in label names, got=%v", ln)
	}

	// Label values for a specific metric and label name
	lv := ac.getLabelValueCompletions("http_requests_total", "code", "4")
	if !contains(lv, "404") {
		t.Fatalf("expected label value '404' for prefix '4', got=%v", lv)
	}
}

func TestAutoCompleter_FunctionAndOperatorCompletions(t *testing.T) {
	store := newTestStore(t)
	ac := NewPrometheusAutoCompleter(store)

	fc := ac.getFunctionCompletions("ra")
	if !containsPrefix(fc, "rate(") {
		t.Fatalf("expected function completion including 'rate(', got=%v", fc)
	}

	op := ac.getOperatorCompletions("")
	for _, want := range []string{"and", "or", "unless", "by", "without"} {
		if !contains(op, want) {
			t.Fatalf("expected operator/keyword %q in completions, got=%v", want, op)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func containsPrefix(ss []string, prefix string) bool {
	for _, s := range ss {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func TestDeletePrevWord(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		pos      int
		wantLine string
		wantPos  int
	}{
		{
			name:     "delete word with separator after cursor (OK case from bug report)",
			line:     "sum by (foo)(foobar)",
			pos:      8, // cursor after "sum by (" at position of 'f'
			wantLine: "sum foo)(foobar)",
			wantPos:  4, // should delete "by (", leaving "sum "
		},
		{
			name:     "delete word when cursor is at beginning of word after separator (BUG case)",
			line:     "sum foo)(foobar)",
			pos:      4, // cursor after "sum "
			wantLine: "foo)(foobar)",
			wantPos:  0, // should delete "sum " not entire line
		},
		{
			name:     "delete word in middle",
			line:     "sum by job",
			pos:      10, // cursor at end
			wantLine: "sum by ",
			wantPos:  7,
		},
		{
			name:     "delete word with parenthesis",
			line:     "sum(rate(foo))",
			pos:      9, // cursor after "sum(rate("
			wantLine: "sum(foo))",
			wantPos:  4,
		},
		{
			name:     "cursor at beginning",
			line:     "sum by job",
			pos:      0,
			wantLine: "sum by job",
			wantPos:  0,
		},
		{
			name:     "only separators before cursor",
			line:     "  foo",
			pos:      2, // cursor after "  "
			wantLine: "foo",
			wantPos:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLine, gotPos := deletePrevWord([]rune(tt.line), tt.pos)
			gotLineStr := string(gotLine)
			if gotLineStr != tt.wantLine {
				t.Errorf("deletePrevWord() line = %q, want %q", gotLineStr, tt.wantLine)
			}
			if gotPos != tt.wantPos {
				t.Errorf("deletePrevWord() pos = %d, want %d", gotPos, tt.wantPos)
			}
		})
	}
}
