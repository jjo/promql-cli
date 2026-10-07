//go:build !noprompt

package repl

import (
	"testing"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// `.pinat <metric selector>` pins the evaluation time to the newest sample of
// the matching series, so its completion must offer metric names next to the
// time presets - in both REPL backends - and must degrade to the time presets
// alone when no metrics are loaded.

func pinatCompletionStore() *sstorage.SimpleStorage {
	s := sstorage.NewSimpleStorage()
	for _, m := range []string{"node_load1", "nowcast_total", "up"} {
		s.AddSample(map[string]string{"__name__": m}, 1, 1791401839901)
	}
	return s
}

func hasText(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestPinatCompletions covers the prompt-toolkit path (getCompletions in
// prompt_repl.go).
func TestPinatCompletions(t *testing.T) {
	prevMetrics, prevHelp, prevRule, prevStorage := metrics, metricsHelp, recordingRuleSet, globalStorage
	defer func() {
		metrics, metricsHelp, recordingRuleSet, globalStorage = prevMetrics, prevHelp, prevRule, prevStorage
	}()

	globalStorage = nil
	recordingRuleSet = nil
	metrics = []string{"node_load1", "nowcast_total", "up"}
	metricsHelp = map[string]string{"node_load1": "1m load average"}

	byText := func(prefix string) map[string]string {
		got := map[string]string{}
		for _, s := range pinatCompletions(prefix) {
			got[s.Text] = s.Description
		}
		return got
	}

	t.Run("metric prefix offers the metric and its help", func(t *testing.T) {
		got := byText("node")
		if _, ok := got["node_load1"]; !ok {
			t.Fatalf("node_load1 missing from completions: %v", got)
		}
		if got["node_load1"] != "1m load average" {
			t.Errorf("metric help lost: %q", got["node_load1"])
		}
	})

	t.Run("empty prefix keeps time presets and metrics", func(t *testing.T) {
		got := byText("")
		for _, want := range []string{"now", "now-1h", "remove", "node_load1", "up"} {
			if _, ok := got[want]; !ok {
				t.Errorf("%q missing from completions", want)
			}
		}
	})

	t.Run("a metric named like a preset survives the merge", func(t *testing.T) {
		got := byText("now")
		if _, ok := got["now"]; !ok {
			t.Error("time preset 'now' missing")
		}
		if _, ok := got["nowcast_total"]; !ok {
			t.Error("metric nowcast_total missing: metrics must merge with, not replace, the presets")
		}
	})

	t.Run("no metrics loaded: time presets only, no panic", func(t *testing.T) {
		metrics = nil
		metricsHelp = nil
		got := byText("")
		if _, ok := got["now"]; !ok {
			t.Error("time presets must survive an empty metric list")
		}
		for _, stale := range []string{"node_load1", "up"} {
			if _, ok := got[stale]; ok {
				t.Errorf("stale metric leaked into completions: %s", stale)
			}
		}
	})
}

// TestReadlinePinatCompletion covers the default (readline) path
// (PrometheusAutoCompleter.getCompletions in repl.go).
func TestReadlinePinatCompletion(t *testing.T) {
	pac := &PrometheusAutoCompleter{storage: pinatCompletionStore()}

	t.Run("metric prefix suggests the metric", func(t *testing.T) {
		got := pac.getCompletions(".pinat node", len(".pinat node"), "node")
		if !hasText(got, "node_load1") {
			t.Fatalf("node_load1 missing from readline completions: %v", got)
		}
	})

	t.Run("empty prefix suggests times and metrics", func(t *testing.T) {
		got := pac.getCompletions(".pinat ", len(".pinat "), "")
		for _, want := range []string{"now", "remove", "node_load1", "up"} {
			if !hasText(got, want) {
				t.Errorf("%q missing from readline completions: %v", want, got)
			}
		}
	})

	t.Run("a preset is never suggested twice", func(t *testing.T) {
		got := pac.getCompletions(".pinat now", len(".pinat now"), "now")
		n := 0
		for _, s := range got {
			if s == "now" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("'now' suggested %d times, want 1: %v", n, got)
		}
		if !hasText(got, "nowcast_total") {
			t.Errorf("nowcast_total missing: %v", got)
		}
	})
}
