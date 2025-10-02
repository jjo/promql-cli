package simple_storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql"
)

func TestSimpleStorage_LoadFromReader_ParsesMetrics(t *testing.T) {
	store := NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(SampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	// Expect keys for http_requests_total and temperature
	httpSamples, ok := store.Metrics["http_requests_total"]
	if !ok {
		t.Fatalf("expected metric family 'http_requests_total' to be present")
	}
	if len(httpSamples) != 2 {
		t.Fatalf("expected 2 samples for http_requests_total, got %d", len(httpSamples))
	}
	for _, s := range httpSamples {
		if s.Labels["__name__"] != "http_requests_total" {
			t.Errorf("expected __name__ label to be set, got %q", s.Labels["__name__"])
		}
		if s.Timestamp == 0 {
			t.Errorf("expected non-zero timestamp for http_requests_total")
		}
		// Verify key labels are present
		if s.Labels["method"] == "" || s.Labels["code"] == "" {
			t.Errorf("expected method and code labels to be set, got=%v", s.Labels)
		}
	}

	tempSamples, ok := store.Metrics["temperature"]
	if !ok {
		t.Fatalf("expected metric family 'temperature' to be present")
	}
	if len(tempSamples) != 1 {
		t.Fatalf("expected 1 sample for temperature, got %d", len(tempSamples))
	}
	if got := tempSamples[0].Value; got != 27.3 {
		t.Errorf("unexpected temperature value: got=%v want=27.3", got)
	}
}

func TestPromQL_SumByCode(t *testing.T) {
	store := NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(SampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	engine := promql.NewEngine(promql.EngineOpts{
		Logger:                   nil,
		Reg:                      nil,
		MaxSamples:               50_000_000,
		Timeout:                  30 * time.Second,
		LookbackDelta:            5 * time.Minute,
		NoStepSubqueryIntervalFn: func(rangeMillis int64) int64 { return 60 * 1000 },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	q, err := engine.NewInstantQuery(ctx, store, nil, "sum(http_requests_total) by (code)", time.Now())
	if err != nil {
		t.Fatalf("NewInstantQuery failed: %v", err)
	}
	res := q.Exec(ctx)
	if res.Err != nil {
		t.Fatalf("query failed: %v", res.Err)
	}

	vec, ok := res.Value.(promql.Vector)
	if !ok {
		t.Fatalf("expected Vector result, got %T", res.Value)
	}
	if len(vec) != 2 {
		t.Fatalf("expected 2 series in result, got %d", len(vec))
	}

	// Build map from code label to value for easy assertions
	got := map[string]float64{}
	for _, s := range vec {
		code := s.Metric.Get("code")
		got[code] = s.F
	}
	if got["200"] != 1027 {
		t.Errorf("unexpected sum for code=200: got=%v want=1027", got["200"])
	}
	if got["404"] != 3 {
		t.Errorf("unexpected sum for code=404: got=%v want=3", got["404"])
	}
}

func TestPromQL_SumByMethod(t *testing.T) {
	store := NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(SampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	engine := promql.NewEngine(promql.EngineOpts{
		Logger:                   nil,
		Reg:                      nil,
		MaxSamples:               50_000_000,
		Timeout:                  30 * time.Second,
		LookbackDelta:            5 * time.Minute,
		NoStepSubqueryIntervalFn: func(rangeMillis int64) int64 { return 60 * 1000 },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	q, err := engine.NewInstantQuery(ctx, store, nil, "sum(http_requests_total) by (method)", time.Now())
	if err != nil {
		t.Fatalf("NewInstantQuery failed: %v", err)
	}
	res := q.Exec(ctx)
	if res.Err != nil {
		t.Fatalf("query failed: %v", res.Err)
	}

	vec, ok := res.Value.(promql.Vector)
	if !ok {
		t.Fatalf("expected Vector result, got %T", res.Value)
	}
	if len(vec) != 1 {
		t.Fatalf("expected 1 series in result, got %d", len(vec))
	}
	if vec[0].Metric.Get("method") != "get" {
		t.Fatalf("expected method label 'get', got=%s", vec[0].Metric.Get("method"))
	}
	if vec[0].F != 1030 {
		t.Fatalf("unexpected sum for method=get: got=%v want=1030", vec[0].F)
	}
}

func TestPromQL_SelectorByLabel(t *testing.T) {
	store := NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(SampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	engine := promql.NewEngine(promql.EngineOpts{
		Logger:                   nil,
		Reg:                      nil,
		MaxSamples:               50_000_000,
		Timeout:                  30 * time.Second,
		LookbackDelta:            5 * time.Minute,
		NoStepSubqueryIntervalFn: func(rangeMillis int64) int64 { return 60 * 1000 },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	q, err := engine.NewInstantQuery(ctx, store, nil, "http_requests_total{code=\"404\"}", time.Now())
	if err != nil {
		t.Fatalf("NewInstantQuery failed: %v", err)
	}
	res := q.Exec(ctx)
	if res.Err != nil {
		t.Fatalf("query failed: %v", res.Err)
	}

	vec, ok := res.Value.(promql.Vector)
	if !ok {
		t.Fatalf("expected Vector result, got %T", res.Value)
	}
	if len(vec) != 1 {
		t.Fatalf("expected 1 series in result, got %d", len(vec))
	}
	if vec[0].F != 3 {
		t.Fatalf("unexpected value for code=404: got=%v want=3", vec[0].F)
	}
}

func TestRenameMetric(t *testing.T) {
	storage := NewSimpleStorage()

	// Add some test metrics
	storage.AddSample(map[string]string{
		"__name__": "http_requests_total",
		"method":   "GET",
		"code":     "200",
	}, 100, 1000)

	storage.AddSample(map[string]string{
		"__name__": "http_requests_total",
		"method":   "POST",
		"code":     "201",
	}, 50, 2000)

	storage.MetricsHelp["http_requests_total"] = "Total number of HTTP requests"

	// Test successful rename
	err := storage.RenameMetric("http_requests_total", "http_reqs")
	if err != nil {
		t.Fatalf("RenameMetric failed: %v", err)
	}

	// Check old metric is gone
	if _, exists := storage.Metrics["http_requests_total"]; exists {
		t.Error("Old metric name still exists after rename")
	}

	// Check new metric exists
	samples, exists := storage.Metrics["http_reqs"]
	if !exists {
		t.Fatal("New metric name doesn't exist after rename")
	}

	// Check sample count
	if len(samples) != 2 {
		t.Errorf("Expected 2 samples, got %d", len(samples))
	}

	// Check __name__ labels are updated
	for i, sample := range samples {
		if sample.Labels["__name__"] != "http_reqs" {
			t.Errorf("Sample %d has wrong __name__: %s", i, sample.Labels["__name__"])
		}
	}

	// Check help text was moved
	if help, ok := storage.MetricsHelp["http_reqs"]; !ok {
		t.Error("Help text wasn't moved to new metric name")
	} else if help != "Total number of HTTP requests" {
		t.Errorf("Help text is incorrect: %s", help)
	}

	if _, ok := storage.MetricsHelp["http_requests_total"]; ok {
		t.Error("Help text still exists for old metric name")
	}
}

func TestRenameMetricErrors(t *testing.T) {
	storage := NewSimpleStorage()

	// Test rename on empty storage
	err := storage.RenameMetric("nonexistent", "new_name")
	if err == nil {
		t.Error("Expected error for non-existent metric")
	}

	// Add a metric
	storage.AddSample(map[string]string{
		"__name__": "metric1",
		"label":    "value",
	}, 42, 1000)

	// Test rename to existing name
	storage.AddSample(map[string]string{
		"__name__": "metric2",
		"label":    "value",
	}, 84, 2000)

	err = storage.RenameMetric("metric1", "metric2")
	if err == nil {
		t.Error("Expected error when renaming to existing metric name")
	}
}
