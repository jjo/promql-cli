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
