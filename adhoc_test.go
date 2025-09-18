package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
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

func TestAdhoc_Timestamps_Summary(t *testing.T) {
	store := NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	out := captureStdout(t, func() {
		_ = handleAdHocFunction(".timestamps http_requests_total", store)
	})
	if !strings.Contains(out, "Timestamp summary for metric 'http_requests_total'") {
		t.Fatalf("missing header in .timestamps output: %s", out)
	}
	if !strings.Contains(out, "Series: 2") {
		t.Fatalf("expected Series: 2, got: %s", out)
	}
	if !strings.Contains(out, "Samples: 2") {
		t.Fatalf("expected Samples: 2, got: %s", out)
	}
	// With per-sample timestamp support, this metric may have >1 unique timestamps; just assert presence of the line
	if !strings.Contains(out, "Unique timestamps:") {
		t.Fatalf("expected Unique timestamps line, got: %s", out)
	}
	if !strings.Contains(out, "Earliest:") || !strings.Contains(out, "Latest:") || !strings.Contains(out, "Span:") {
		t.Fatalf("expected Earliest/Latest/Span lines, got: %s", out)
	}
}

func TestAdhoc_Pinat_ShowSetRemove(t *testing.T) {
	// ensure clean state
	pinnedEvalTime = nil
	defer func() { pinnedEvalTime = nil }()

	store := NewSimpleStorage()

	// Show when none
	out := captureStdout(t, func() { _ = handleAdHocFunction(".pinat", store) })
	if !strings.Contains(out, "Pinned evaluation time: none") {
		t.Fatalf("expected none status, got: %s", out)
	}

	// Set to now
	out = captureStdout(t, func() { _ = handleAdHocFunction(".pinat now", store) })
	if pinnedEvalTime == nil {
		t.Fatalf("expected pinnedEvalTime to be set after .pinat now")
	}
	if !strings.Contains(out, "Pinned evaluation time:") {
		t.Fatalf("expected confirmation output, got: %s", out)
	}

	// Show current
	out = captureStdout(t, func() { _ = handleAdHocFunction(".pinat", store) })
	if !strings.Contains(out, "Pinned evaluation time:") || strings.Contains(out, "none") {
		t.Fatalf("expected current pin shown, got: %s", out)
	}

	// Remove
	out = captureStdout(t, func() { _ = handleAdHocFunction(".pinat remove", store) })
	if pinnedEvalTime != nil {
		t.Fatalf("expected pinnedEvalTime to be nil after remove")
	}
	if !strings.Contains(out, "Pinned evaluation time: removed") {
		t.Fatalf("expected removed message, got: %s", out)
	}
}

func TestTimestamps_WithExplicitTimestamps(t *testing.T) {
	// Create a temporary prom file with two samples ~10s apart
	dir := t.TempDir()
	path := filepath.Join(dir, "foo.prom")
	// Two samples with explicit unix_ms
	content := "cloudcost_azure_aks_storage_by_location_usd_per_gibyte_hour{location=\"eastus\"} 1 1700000000000\n" +
		"cloudcost_azure_aks_storage_by_location_usd_per_gibyte_hour{location=\"eastus\"} 2 1700000010000\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store := NewSimpleStorage()
	// Use the .load handler to exercise the same code path
	out := captureStdout(t, func() { _ = handleAdHocFunction(".load "+path, store) })
	if !strings.Contains(out, "Loaded ") {
		t.Fatalf("expected load output, got: %s", out)
	}

	// Now request timestamps summary
	out = captureStdout(t, func() {
		_ = handleAdHocFunction(".timestamps cloudcost_azure_aks_storage_by_location_usd_per_gibyte_hour", store)
	})
	if !strings.Contains(out, "Earliest: 2023-11-14T22:13:20Z") {
		t.Fatalf("expected earliest 2023-11-14T22:13:20Z, got: %s", out)
	}
	if !strings.Contains(out, "Latest:   2023-11-14T22:13:30Z") {
		t.Fatalf("expected latest 2023-11-14T22:13:30Z, got: %s", out)
	}
	if !strings.Contains(out, "Span:     10s") {
		t.Fatalf("expected span 10s, got: %s", out)
	}
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
