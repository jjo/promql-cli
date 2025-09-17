package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout captures stdout during the execution of fn and returns the captured output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	// Close writer to finish the reader
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	return buf.String()
}

func TestAdhoc_Help_PrintsAndReturnsTrue(t *testing.T) {
	store := NewSimpleStorage()
	out := captureStdout(t, func() {
		if !handleAdHocFunction(".help", store) {
			t.Fatalf("expected .help to be handled")
		}
	})
	if !strings.Contains(out, "Ad-hoc commands:") || !strings.Contains(out, ".labels <metric>") {
		t.Fatalf("unexpected .help output: %s", out)
	}
}

func TestAdhoc_Metrics_EmptyAndNonEmpty(t *testing.T) {
	// Empty store
	empty := NewSimpleStorage()
	out := captureStdout(t, func() { _ = handleAdHocFunction(".metrics", empty) })
	if !strings.Contains(out, "No metrics loaded") {
		t.Fatalf("expected 'No metrics loaded' on empty store, got: %s", out)
	}

	// Non-empty store
	store := NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}
	out = captureStdout(t, func() { _ = handleAdHocFunction(".metrics", store) })
	if !strings.Contains(out, "Metrics (") || !strings.Contains(out, "http_requests_total") || !strings.Contains(out, "temperature") {
		t.Fatalf("unexpected .metrics output: %s", out)
	}
}

func TestAdhoc_Labels_ExistingMissingAndUsage(t *testing.T) {
	store := NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	// Usage when missing metric name (requires trailing space to match handler)
	usage := captureStdout(t, func() { _ = handleAdHocFunction(".labels ", store) })
	if !strings.Contains(usage, "Usage: .labels <metric_name>") {
		t.Fatalf("expected usage text for .labels with no args, got: %s", usage)
	}

	// Existing metric
	out := captureStdout(t, func() { _ = handleAdHocFunction(".labels http_requests_total", store) })
	if !strings.Contains(out, "Labels for metric 'http_requests_total'") {
		t.Fatalf("missing header in .labels output: %s", out)
	}
	if !strings.Contains(out, "code") || !strings.Contains(out, "method") {
		t.Fatalf("expected code and method labels listed, got: %s", out)
	}

	// Missing metric
	miss := captureStdout(t, func() { _ = handleAdHocFunction(".labels not_a_metric", store) })
	if !strings.Contains(miss, "Metric 'not_a_metric' not found") || !strings.Contains(miss, "Available metrics:") {
		t.Fatalf("unexpected .labels missing-metric output: %s", miss)
	}
}

func TestAdhoc_Seed_KVAndPositional(t *testing.T) {
	store := NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}
	metric := "http_requests_total"
	orig := len(store.metrics[metric])
	if orig == 0 {
		t.Fatalf("expected samples for %s", metric)
	}

	// KV-style seeding
	out := captureStdout(t, func() { _ = handleAdHocFunction(".seed "+metric+" steps=2 step=30s", store) })
	if !strings.Contains(out, "Seeded 2 historical points (step 30s) for metric") {
		t.Fatalf("unexpected .seed KV output: %s", out)
	}
	kvCount := len(store.metrics[metric])
	if kvCount < orig+2*2 { // two series, 2 steps each
		t.Fatalf("expected at least %d samples after KV seeding, got %d", orig+4, kvCount)
	}

	// Positional seeding (additionally)
	out = captureStdout(t, func() { _ = handleAdHocFunction(".seed "+metric+" 1 1m", store) })
	if !strings.Contains(out, "Seeded 1 historical points (step 1m0s) for metric") {
		t.Fatalf("unexpected .seed positional output: %s", out)
	}
	posCount := len(store.metrics[metric])
	if posCount < kvCount+2*1 { // two series, 1 additional step each
		t.Fatalf("expected at least %d samples after positional seeding, got %d", kvCount+2, posCount)
	}
}
