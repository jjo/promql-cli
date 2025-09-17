package main

import (
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *SimpleStorage {
	t.Helper()
	store := NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}
	return store
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
