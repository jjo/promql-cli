//go:build prompt

package repl

import (
	"testing"
)

func TestRecordingRuleLabelShownInCompletions(t *testing.T) {
	// Prepare globals
	metrics = []string{"node_count"}
	metricsHelp = map[string]string{}
	recordingRuleSet = map[string]bool{"node_count": true}

	// When prefix matches
	sugg := getMetricSuggests("node_co")
	if len(sugg) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(sugg))
	}
	if sugg[0].Text != "node_count" {
		t.Fatalf("unexpected suggestion text: %s", sugg[0].Text)
	}
	if got, want := sugg[0].Description, "(rule)"; got != want {
		t.Fatalf("expected description %q, got %q", want, got)
	}
}

func TestRecordingRuleKeepsHelpDescription(t *testing.T) {
	// If a recording rule also has HELP text (materialized metric), prefer help text
	metrics = []string{"node_count"}
	metricsHelp = map[string]string{"node_count": "Number of nodes"}
	recordingRuleSet = map[string]bool{"node_count": true}

	sugg := getMetricSuggests("node_co")
	if len(sugg) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(sugg))
	}
	if got, want := sugg[0].Description, "Number of nodes"; got != want {
		t.Fatalf("expected description %q, got %q", want, got)
	}
}
