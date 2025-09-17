package main

import (
	"strings"
	"testing"
)

func TestAutoCompleter_MetricNameCompletion(t *testing.T) {
	store := NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}
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
