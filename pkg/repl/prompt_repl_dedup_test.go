//go:build !noprompt

package repl

import (
	"testing"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// countOccurrences counts how many times target appears in slice
func countOccurrences(xs []string, target string) int {
	n := 0
	for _, x := range xs {
		if x == target {
			n++
		}
	}
	return n
}

func TestFetchMetrics_DedupesStorageAndRecordingRules(t *testing.T) {
	// Prepare a storage with a metric that also appears as a recording rule
	st := sstorage.NewSimpleStorage()
	st.AddSample(map[string]string{"__name__": "node_count"}, 1, 0)
	st.AddSample(map[string]string{"__name__": "up"}, 1, 0)

	// Point global storage to our in-memory storage
	globalStorage = st

	// Configure active recording rule names overlapping with storage
	activeRecordingNames = []string{"node_count", "derived_rule_only"}

	// Reset globals used by fetch
	metrics = nil
	metricsHelp = map[string]string{}
	recordingRuleSet = nil

	// Execute fetch path
	fetchMetrics()

	// Ensure metrics have both storage and rule-only names but no duplicates
	if count := countOccurrences(metrics, "node_count"); count != 1 {
		t.Fatalf("expected exactly 1 occurrence of node_count, got %d (metrics=%v)", count, metrics)
	}
	if count := countOccurrences(metrics, "derived_rule_only"); count != 1 {
		t.Fatalf("expected exactly 1 occurrence of derived_rule_only, got %d (metrics=%v)", count, metrics)
	}
	if count := countOccurrences(metrics, "up"); count != 1 {
		t.Fatalf("expected exactly 1 occurrence of up, got %d (metrics=%v)", count, metrics)
	}

	// Ensure recording rule set marks both rule names
	if recordingRuleSet == nil || !recordingRuleSet["node_count"] || !recordingRuleSet["derived_rule_only"] {
		t.Fatalf("expected recordingRuleSet to mark node_count and derived_rule_only; got %#v", recordingRuleSet)
	}
}
