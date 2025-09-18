package main

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql"
)

func newTestStore(t *testing.T) *SimpleStorage {
	t.Helper()
	store := NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}
	return store
}

// Ensure that queries with PromQL @ modifier using milliseconds are normalized and do not error.
func TestExecuteOne_AtModifierWithMillis_Works(t *testing.T) {
	store := NewSimpleStorage()
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
		NoStepSubqueryIntervalFn: func(rangeMillis int64) int64 { return 60 * 1000 },
	})

	q := "cloudcost_azure_aks_storage_by_location_usd_per_gibyte_hour @1700000000000"
	out := captureStdout(t, func() {
		executeOne(engine, store, q)
	})
	if strings.Contains(out, "Error creating query:") || strings.Contains(out, "Error:") {
		t.Fatalf("unexpected error output executing query with @ millis: %s", out)
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
