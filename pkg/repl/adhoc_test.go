package repl

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

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
	if _, err := io.Copy(&buf, r); err != nil {
		// ignore in tests
		_ = err
	}
	_ = r.Close()
	return buf.String()
}

func TestAdhoc_Timestamps_Summary(t *testing.T) {
	store := sstorage.NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sstorage.SampleMetrics)); err != nil {
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

	store := sstorage.NewSimpleStorage()

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
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store := sstorage.NewSimpleStorage()
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

func TestAdhoc_Drop_RemovesMetricAndReports(t *testing.T) {
	store := sstorage.NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sstorage.SampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}
	if _, ok := store.Metrics["http_requests_total"]; !ok {
		t.Fatalf("expected metric present before drop")
	}

	// Drop by series-signature regex matching all series of http_requests_total
	out := captureStdout(t, func() {
		_ = handleAdHocFunction(".drop ^http_requests_total\\{", store)
	})
	if _, ok := store.Metrics["http_requests_total"]; ok {
		t.Fatalf("expected metric removed")
	}
	if !strings.Contains(out, "Dropped 2 samples") {
		t.Fatalf("unexpected .drop output: %s", out)
	}

	// Dropping with a regex that matches nothing
	out = captureStdout(t, func() {
		_ = handleAdHocFunction(".drop ^not_a_metric\\{", store)
	})
	if !strings.Contains(out, "Dropped 0 samples") {
		t.Fatalf("expected zero-dropped message, got: %s", out)
	}

	// Usage without argument
	out = captureStdout(t, func() {
		_ = handleAdHocFunction(".drop", store)
	})
	if !strings.Contains(out, "Usage: .drop <series regex>") {
		t.Fatalf("expected usage message, got: %s", out)
	}
}

func TestAdhoc_Scrape_FetchesAndLoads(t *testing.T) {
	// Prepare a small exposition endpoint
	payload := `# HELP up 1 if up
# TYPE up gauge
up 1
# HELP foo_total a counter
# TYPE foo_total counter
foo_total{code="200"} 5
foo_total{code="500"} 1
`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		if _, err := io.Copy(w, strings.NewReader(payload)); err != nil {
			return
		}
	}))
	defer ts.Close()

	store := sstorage.NewSimpleStorage()
	// Ensure store empty before
	if len(store.Metrics) != 0 {
		t.Fatalf("expected empty store initially")
	}

	out := captureStdout(t, func() {
		_ = handleAdHocFunction(".scrape "+ts.URL, store)
	})
	if !strings.Contains(out, "Scraped ") {
		t.Fatalf("expected scrape output, got: %s", out)
	}
	if _, ok := store.Metrics["up"]; !ok {
		t.Fatalf("expected 'up' metric to be loaded")
	}
	if samples := store.Metrics["foo_total"]; len(samples) < 2 {
		t.Fatalf("expected counter samples loaded, got %d", len(samples))
	}
}

func TestAdhoc_PromScrape_ImportsVector(t *testing.T) {
	// Minimal Prometheus-like API server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up","job":"test"},"value":[1738000000,"1"]}]}}`
		if _, err := io.WriteString(w, resp); err != nil {
			return
		}
	}))
	defer ts.Close()

	store := sstorage.NewSimpleStorage()
	out := captureStdout(t, func() { _ = handleAdHocFunction(".prom_scrape "+ts.URL+" 'up'", store) })
	if !strings.Contains(out, "Imported") {
		t.Fatalf("expected Imported output, got: %s", out)
	}
	if _, ok := store.Metrics["up"]; !ok {
		t.Fatalf("expected 'up' metric imported")
	}
}

func TestAdhoc_PromScrape_BasicAuth(t *testing.T) {
	user := "alice"
	pass := "secret"
	expected := "Basic " + basicAuth(user, pass)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != expected {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`); err != nil {
			return
		}
	}))
	defer ts.Close()
	store := sstorage.NewSimpleStorage()
	out := captureStdout(t, func() {
		_ = handleAdHocFunction(".prom_scrape "+ts.URL+" 'up' auth=basic user="+user+" pass="+pass, store)
	})
	if strings.Contains(strings.ToLower(out), "error") {
		t.Fatalf("unexpected error output: %s", out)
	}
}

func TestAdhoc_PromScrape_MimirAuth(t *testing.T) {
	orgID := "12345"
	apiKey := "API_KEY_XYZ"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Scope-OrgID") != orgID {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`); err != nil {
			return
		}
	}))
	defer ts.Close()
	store := sstorage.NewSimpleStorage()
	out := captureStdout(t, func() {
		_ = handleAdHocFunction(".prom_scrape "+ts.URL+" 'up' auth=mimir org_id="+orgID+" api_key="+apiKey, store)
	})
	if strings.Contains(strings.ToLower(out), "error") {
		t.Fatalf("unexpected error output: %s", out)
	}
}

func TestAdhoc_PromScrapeRange_ImportsMatrix(t *testing.T) {
	// Minimal Prometheus-like API server for query_range
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"foo_total","job":"test"},"values":[[1738000000,"5"],[1738000015,"7"]]}]}}`
		if _, err := io.WriteString(w, resp); err != nil {
			return
		}
	}))
	defer ts.Close()

	store := sstorage.NewSimpleStorage()
	out := captureStdout(t, func() { _ = handleAdHocFunction(".prom_scrape_range "+ts.URL+" 'foo_total' now-30m now 15s", store) })
	if !strings.Contains(out, "Imported range") {
		t.Fatalf("expected Imported range output, got: %s", out)
	}
	if samples := store.Metrics["foo_total"]; len(samples) < 2 {
		t.Fatalf("expected at least 2 samples imported, got %d", len(samples))
	}
}

func TestAdhoc_Help_PrintsAndReturnsTrue(t *testing.T) {
	store := sstorage.NewSimpleStorage()
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
	empty := sstorage.NewSimpleStorage()
	out := captureStdout(t, func() { _ = handleAdHocFunction(".metrics", empty) })
	if !strings.Contains(out, "No metrics loaded") {
		t.Fatalf("expected 'No metrics loaded' on empty store, got: %s", out)
	}

	// Non-empty store
	store := sstorage.NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sstorage.SampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}
	out = captureStdout(t, func() { _ = handleAdHocFunction(".metrics", store) })
	if !strings.Contains(out, "Metrics (") || !strings.Contains(out, "http_requests_total") || !strings.Contains(out, "temperature") {
		t.Fatalf("unexpected .metrics output: %s", out)
	}
}

func TestAdhoc_Labels_ExistingMissingAndUsage(t *testing.T) {
	store := sstorage.NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sstorage.SampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	// Usage when missing metric name (requires trailing space to match handler)
	usage := captureStdout(t, func() { _ = handleAdHocFunction(".labels ", store) })
	if !strings.Contains(usage, "Usage: .labels <metric_name>") {
		t.Fatalf("expected usage text for .labels with no args, got: %q", usage)
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

func TestAdhoc_Save_WithTimestamp_ParsesPathAndArgs(t *testing.T) {
	dir := t.TempDir()
	store := sstorage.NewSimpleStorage()
	path := filepath.Join(dir, "foo.prom")
	out := captureStdout(t, func() { _ = handleAdhocSave(".save "+path+" timestamp=remove", store) })
	if !strings.Contains(out, "Saved store to "+path) {
		t.Fatalf("expected saved message with path only, got: %s", out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file %s to be created: %v", path, err)
	}
	if _, err := os.Stat(path + " timestamp=remove"); err == nil {
		t.Fatalf("unexpected file created with args in name: %s", path+" timestamp=remove")
	}
}

func TestAdhoc_Seed_KVAndPositional(t *testing.T) {
	store := sstorage.NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sstorage.SampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}
	metric := "http_requests_total"
	orig := len(store.Metrics[metric])
	if orig == 0 {
		t.Fatalf("expected samples for %s", metric)
	}

	// KV-style seeding
	out := captureStdout(t, func() { _ = handleAdHocFunction(".seed "+metric+" steps=2 step=30s", store) })
	if !strings.Contains(out, "Seeded 2 historical points (step 30s) for metric") {
		t.Fatalf("unexpected .seed KV output: %s", out)
	}
	kvCount := len(store.Metrics[metric])
	if kvCount < orig+2*2 { // two series, 2 steps each
		t.Fatalf("expected at least %d samples after KV seeding, got %d", orig+4, kvCount)
	}

	// Positional seeding (additionally)
	out = captureStdout(t, func() { _ = handleAdHocFunction(".seed "+metric+" 1 1m", store) })
	if !strings.Contains(out, "Seeded 1 historical points (step 1m0s) for metric") {
		t.Fatalf("unexpected .seed positional output: %s", out)
	}
	posCount := len(store.Metrics[metric])
	if posCount < kvCount+2*1 { // two series, 1 additional step each
		t.Fatalf("expected at least %d samples after positional seeding, got %d", kvCount+2, posCount)
	}
}

func TestAdhoc_Rename_SuccessfulRename(t *testing.T) {
	storage := sstorage.NewSimpleStorage()

	// Add test metrics
	storage.AddSample(map[string]string{
		"__name__": "old_metric",
		"label":    "value1",
	}, 100, 1000)

	storage.AddSample(map[string]string{
		"__name__": "old_metric",
		"label":    "value2",
	}, 200, 2000)

	// Test successful rename
	out := captureStdout(t, func() {
		if !handleAdhocRename(".rename old_metric new_metric", storage) {
			t.Fatal("handleAdhocRename should have returned true")
		}
	})

	if !strings.Contains(out, "Renamed") {
		t.Errorf("Expected success message, got: %s", out)
	}

	// Verify rename worked
	if _, exists := storage.Metrics["old_metric"]; exists {
		t.Error("Old metric still exists")
	}

	if samples, exists := storage.Metrics["new_metric"]; !exists {
		t.Error("New metric doesn't exist")
	} else if len(samples) != 2 {
		t.Errorf("Expected 2 samples, got %d", len(samples))
	}
}

func TestAdhoc_Rename_Usage(t *testing.T) {
	storage := sstorage.NewSimpleStorage()

	testCases := []struct {
		cmd string
	}{
		{".rename"},
		{".rename only_one_arg"},
	}

	for _, tc := range testCases {
		t.Run(tc.cmd, func(t *testing.T) {
			out := captureStdout(t, func() {
				if !handleAdhocRename(tc.cmd, storage) {
					t.Fatal("handleAdhocRename should have returned true")
				}
			})

			if !strings.Contains(out, "Usage:") {
				t.Errorf("Expected usage message, got: %s", out)
			}
		})
	}
}
