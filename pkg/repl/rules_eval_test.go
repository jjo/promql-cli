package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

func TestEvaluateRulesOnStorage_RecordingRule(t *testing.T) {
	// Prepare storage with sample metrics
	store := sstorage.NewSimpleStorage()
	if err := store.LoadFromReader(strings.NewReader(sstorage.SampleMetrics)); err != nil {
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

	// Create a temp rules file
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.yaml")
	yaml := `groups:
- name: test
  rules:
  - record: http_requests_total_copy
    expr: sum by (code) (http_requests_total)
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	files, err := ResolveRuleSpec(path)
	if err != nil {
		t.Fatalf("ResolveRuleSpec: %v", err)
	}

	added, alerts, err := EvaluateRulesOnStorage(engine, store, files, time.Now(), nil)
	if err != nil {
		t.Fatalf("EvaluateRulesOnStorage: %v", err)
	}
	if alerts != 0 {
		t.Fatalf("expected 0 alerts, got %d", alerts)
	}
	if added < 2 {
		t.Fatalf("expected at least 2 recorded samples, got %d", added)
	}
	if _, ok := store.Metrics["http_requests_total_copy"]; !ok {
		t.Fatalf("expected recorded metric present in storage")
	}
}
