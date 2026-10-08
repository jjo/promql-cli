package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql"
	promparser "github.com/prometheus/prometheus/promql/parser"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// resetRulesState clears the package-level rules/pin state between tests.
func resetRulesState(t *testing.T) {
	t.Helper()
	reset := func() {
		evalEngine = nil
		SetActiveRules(nil, "")
		pinnedEvalTime = nil
	}
	reset()
	t.Cleanup(reset)
}

func writeAlertRule(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "alerts.yaml")
	yaml := `groups:
- name: test
  rules:
  - record: job:up:min
    expr: min by (job) (up)
  - alert: JobDown
    expr: job:up:min == 0
    labels:
      severity: page
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// Rules set through -c pre-commands must be evaluated: RunInitCommands runs
// before the interactive REPL wires the rules engine.
func TestRunInitCommands_RulesAreEvaluated(t *testing.T) {
	resetRulesState(t)
	store := sstorage.NewSimpleStorage()
	ts := time.Date(2026, 10, 7, 19, 38, 47, 0, time.UTC)
	store.AddSample(map[string]string{"__name__": "up", "job": "db", "instance": "a"}, 0, ts.UnixMilli())
	store.AddSample(map[string]string{"__name__": "up", "job": "web", "instance": "b"}, 1, ts.UnixMilli())
	engine := promql.NewEngine(promql.EngineOpts{
		MaxSamples:    50_000_000,
		Timeout:       30 * time.Second,
		LookbackDelta: 5 * time.Minute,
		Parser:        promparser.NewParser(promparser.Options{EnableExperimentalFunctions: true}),
	})

	out := captureStdout(t, func() {
		RunInitCommands(engine, store, ".pinat up; .rules "+writeAlertRule(t), true)
	})

	alerts := store.Metrics["ALERTS"]
	if len(alerts) != 1 {
		t.Fatalf("expected 1 ALERTS sample from pre-command rules, got %d (output: %s)", len(alerts), out)
	}
	got := alerts[0]
	if got.Labels["alertname"] != "JobDown" || got.Labels["job"] != "db" || got.Labels["severity"] != "page" {
		t.Fatalf("unexpected ALERTS labels: %v", got.Labels)
	}
	if got.Timestamp != ts.UnixMilli() {
		t.Fatalf("ALERTS at %d, want the pinned time %d", got.Timestamp, ts.UnixMilli())
	}
	if len(store.Metrics["job:up:min"]) != 2 {
		t.Fatalf("expected the recording rule to feed the alert, got %d job:up:min samples", len(store.Metrics["job:up:min"]))
	}
}

func TestEvaluateActiveRules_NoEngineIsAnError(t *testing.T) {
	resetRulesState(t)
	SetActiveRules([]string{writeAlertRule(t)}, "alerts.yaml")

	_, _, err := EvaluateActiveRules(sstorage.NewSimpleStorage())
	if err == nil || !strings.Contains(err.Error(), "no query engine") {
		t.Fatalf("expected a no-engine error, got %v", err)
	}
}

func TestEvaluateActiveRules_NoRulesIsANoop(t *testing.T) {
	resetRulesState(t)
	if added, alerts, err := EvaluateActiveRules(sstorage.NewSimpleStorage()); err != nil || added != 0 || alerts != 0 {
		t.Fatalf("expected a silent no-op without rules, got added=%d alerts=%d err=%v", added, alerts, err)
	}
}

func TestAdhocRules_NoEngineReportsError(t *testing.T) {
	resetRulesState(t)
	out := captureStdout(t, func() { _ = handleAdHocFunction(".rules "+writeAlertRule(t), sstorage.NewSimpleStorage()) })
	if !strings.Contains(out, "Rules evaluation failed") || !strings.Contains(out, "no query engine") {
		t.Fatalf("expected .rules to report the missing engine, got: %s", out)
	}
}

func TestFormatAlertLine(t *testing.T) {
	got := formatAlertLine("JobDown", map[string]string{"__name__": "ALERTS", "alertname": "JobDown", "alertstate": "firing", "job": "db", "severity": "page"}, 0)
	if want := `ALERT JobDown firing {job="db", severity="page"} value=0`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Alert lines must come after the command's status output: EvaluateActiveRules
// buffers them and FlushRuleAlerts (deferred by executeOne) prints them.
func TestEvaluateActiveRules_DefersAlertOutput(t *testing.T) {
	resetRulesState(t)
	pendingRuleAlerts = nil
	t.Cleanup(func() { pendingRuleAlerts = nil })
	store := sstorage.NewSimpleStorage()
	ts := time.Date(2026, 10, 7, 19, 38, 47, 0, time.UTC)
	store.AddSample(map[string]string{"__name__": "up", "job": "db"}, 0, ts.UnixMilli())
	SetEvalEngine(promql.NewEngine(promql.EngineOpts{MaxSamples: 1000, Timeout: 10 * time.Second, LookbackDelta: 5 * time.Minute}))
	SetActiveRules([]string{writeAlertRule(t)}, "alerts.yaml")
	pinnedEvalTime = &ts

	during := captureStdout(t, func() { _, _, _ = EvaluateActiveRules(store) })
	if strings.Contains(during, "ALERT ") {
		t.Fatalf("alert printed during evaluation: %q", during)
	}
	after := captureStdout(t, FlushRuleAlerts)
	if !strings.Contains(after, `ALERT JobDown firing {job="db", severity="page"}`) {
		t.Fatalf("flush did not print the alert: %q", after)
	}
	if again := captureStdout(t, FlushRuleAlerts); again != "" {
		t.Fatalf("flush must clear the buffer, got %q", again)
	}
}
