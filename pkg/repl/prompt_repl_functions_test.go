//go:build !noprompt

package repl

import (
	"strings"
	"testing"

	promparser "github.com/prometheus/prometheus/promql/parser"
)

// The go-prompt backend must offer every parser function, experimental ones
// included, so it never drifts from what the engine actually accepts.
func TestGetFunctionSuggests_FromParserTable(t *testing.T) {
	got := map[string]string{}
	for _, s := range getFunctionSuggests("") {
		got[strings.TrimSuffix(s.Text, "(")] = s.Description
	}
	for name, fn := range promparser.Functions {
		desc, ok := got[name]
		if !ok {
			t.Errorf("function %q missing from completions", name)
			continue
		}
		if fn.Experimental && !strings.Contains(desc, "[experimental]") {
			t.Errorf("experimental function %q not tagged: %q", name, desc)
		}
	}
	for _, agg := range []string{"sum", "topk", "bottomk", "count_values", "limitk"} {
		if _, ok := got[agg]; !ok {
			t.Errorf("aggregator %q missing from completions", agg)
		}
	}
	if _, ok := got["holt_winters"]; ok {
		t.Errorf("holt_winters was renamed upstream and must not be offered")
	}
}

func TestGetFunctionSuggests_PrefixAndSignature(t *testing.T) {
	sugg := getFunctionSuggests("robust_")
	if len(sugg) == 0 {
		t.Fatalf("expected robust_* completions")
	}
	for _, s := range sugg {
		if !strings.HasPrefix(s.Text, "robust_") {
			t.Errorf("suggestion %q does not match prefix", s.Text)
		}
	}
	if got := functionSignature(promparser.Functions["lm_over_time"]); !strings.Contains(got, "range-vector") || !strings.Contains(got, "?") {
		t.Errorf("unexpected lm_over_time signature: %q", got)
	}
}
