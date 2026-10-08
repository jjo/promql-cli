package repl

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
)

func TestCommonLabels(t *testing.T) {
	a := labels.FromStrings("job", "node", "namespace", "monitoring", "instance", "a:9100")
	b := labels.FromStrings("job", "node", "namespace", "monitoring", "instance", "b:9100")
	c := labels.FromStrings("job", "other", "namespace", "monitoring", "instance", "c:9100")
	tests := []struct {
		name string
		in   []labels.Labels
		want string
	}{
		{"single series", []labels.Labels{a}, "{}"},
		{"two series", []labels.Labels{a, b}, `{job="node", namespace="monitoring"}`},
		{"value differs", []labels.Labels{a, b, c}, `{namespace="monitoring"}`},
		{"label missing on one", []labels.Labels{a, labels.FromStrings("instance", "x")}, "{}"},
		{"nothing shared", []labels.Labels{labels.FromStrings("a", "1"), labels.FromStrings("b", "2")}, "{}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commonLabels(tt.in).String(); got != tt.want {
				t.Fatalf("commonLabels() = %s, want %s", got, tt.want)
			}
		})
	}
}

func withTerminal(t *testing.T, tty bool) {
	t.Helper()
	orig, origEnabled, origRaw := isTerminal, commonLabelsEnabled, rawOutput
	t.Cleanup(func() { isTerminal, commonLabelsEnabled, rawOutput = orig, origEnabled, origRaw })
	isTerminal = func(io.Writer) bool { return tty }
	t.Setenv("NO_COLOR", "1") // keep value highlighting out of the assertions
}

func twoSeriesVector() promql.Vector {
	return promql.Vector{
		{Metric: labels.FromStrings("job", "node", "namespace", "monitoring", "instance", "a:9100"), F: 1, T: 0},
		{Metric: labels.FromStrings("job", "node", "namespace", "monitoring", "instance", "b:9100"), F: 2, T: 0},
	}
}

func TestPrintVectorCommonLabelsOnTerminal(t *testing.T) {
	withTerminal(t, true)
	var buf bytes.Buffer
	PrintUpstreamQueryResultToWriter(&promql.Result{Value: twoSeriesVector()}, &buf)
	out := buf.String()
	for _, want := range []string{
		`  # common_labels: {job="node", namespace="monitoring"}`,
		"  # common_timestamp: 1970-01-01T",
		"  [1] {…, instance=\"a:9100\"} => 1\n",
		"  [2] {…, instance=\"b:9100\"} => 2\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintVectorFullLabelsWhenPipedOrDisabled(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tty     bool
		enabled bool
		raw     bool
	}{
		{"piped", false, true, false},
		{"disabled on terminal", true, false, false},
		{"--repl-raw on terminal", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTerminal(t, tc.tty)
			commonLabelsEnabled = tc.enabled
			rawOutput = tc.raw
			var buf bytes.Buffer
			PrintUpstreamQueryResultToWriter(&promql.Result{Value: twoSeriesVector()}, &buf)
			out := buf.String()
			if strings.Contains(out, "common_") || strings.Contains(out, "…") {
				t.Fatalf("expected full labels, got:\n%s", out)
			}
			if !strings.Contains(out, `{instance="a:9100", job="node", namespace="monitoring"}`) {
				t.Fatalf("expected full label set, got:\n%s", out)
			}
		})
	}
}

func TestPrintVectorSingleSeriesUnchanged(t *testing.T) {
	withTerminal(t, true)
	var buf bytes.Buffer
	v := twoSeriesVector()[:1]
	PrintUpstreamQueryResultToWriter(&promql.Result{Value: v}, &buf)
	if out := buf.String(); strings.Contains(out, "common_") {
		t.Fatalf("single series should not be factored:\n%s", out)
	}
}

func TestPrintMatrixCommonLabels(t *testing.T) {
	withTerminal(t, true)
	m := promql.Matrix{
		{Metric: labels.FromStrings("job", "node", "instance", "a"), Floats: []promql.FPoint{{T: 0, F: 1}}},
		{Metric: labels.FromStrings("job", "node", "instance", "b"), Floats: []promql.FPoint{{T: 0, F: 2}}},
	}
	var buf bytes.Buffer
	PrintUpstreamQueryResultToWriter(&promql.Result{Value: m}, &buf)
	out := buf.String()
	if !strings.Contains(out, `# common_labels: {job="node"}`) || !strings.Contains(out, `[1] {…, instance="a"}:`) {
		t.Fatalf("unexpected matrix output:\n%s", out)
	}
}

func TestHandleAdhocCommonLabelsToggle(t *testing.T) {
	orig := commonLabelsEnabled
	t.Cleanup(func() { commonLabelsEnabled = orig })
	handleAdhocCommonLabels(".common_labels off")
	if commonLabelsEnabled {
		t.Fatal("expected off")
	}
	handleAdhocCommonLabels(".common_labels on")
	if !commonLabelsEnabled {
		t.Fatal("expected on")
	}
	handleAdhocCommonLabels(".common_labels bogus")
	if !commonLabelsEnabled {
		t.Fatal("invalid argument must not change the setting")
	}
}

func TestReplRawDisablesValueHighlight(t *testing.T) {
	withTerminal(t, true)
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if !ColorEnabled(io.Discard) {
		t.Fatal("expected color on a terminal without --repl-raw")
	}
	rawOutput = true
	if ColorEnabled(io.Discard) {
		t.Fatal("--repl-raw must disable color")
	}
	if got := valueFormatter(io.Discard)(3.55); got != "3.55" {
		t.Fatalf("--repl-raw: got %q, want plain 3.55", got)
	}
}

func TestDifferingAffixes(t *testing.T) {
	sets := []labels.Labels{
		labels.FromStrings("instance", "172.16.16.75:9100", "pod", "exporter-jshxb", "job", "node"),
		labels.FromStrings("instance", "172.16.16.76:9100", "pod", "exporter-nwqjx", "job", "node"),
		labels.FromStrings("instance", "172.16.16.79:9100", "pod", "exporter-wvpk8", "job", "node"),
	}
	got := differingAffixes(sets)
	want := map[string]affix{"instance": {prefix: 11, suffix: 5}, "pod": {prefix: 9, suffix: 0}}
	if len(got) != len(want) {
		t.Fatalf("differingAffixes() = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %+v, want %+v", k, got[k], v)
		}
	}
	if d := differingAffixes(sets[:1]); d != nil {
		t.Errorf("single series: got %v, want nil", d)
	}
	if d := differingAffixes([]labels.Labels{labels.FromStrings("a", "x"), labels.FromStrings("b", "y")}); len(d) != 0 {
		t.Errorf("label missing on a series: got %v, want none", d)
	}
}

func TestPrintVectorUnderlinesDifferences(t *testing.T) {
	withTerminal(t, true)
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	var buf bytes.Buffer
	PrintUpstreamQueryResultToWriter(&promql.Result{Value: twoSeriesVector()}, &buf)
	out := buf.String()
	for _, want := range []string{
		`[1] {…, instance="` + underlineOn + `a` + underlineOff + `:9100"} => `,
		`[2] {…, instance="` + underlineOn + `b` + underlineOff + `:9100"} => `,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%q", want, out)
		}
	}
}

func TestRenderLabelsQuotingAndUnicode(t *testing.T) {
	sets := []labels.Labels{
		labels.FromStrings("msg", `say "hi" ñandú-1`),
		labels.FromStrings("msg", `say "hi" ñandú-2`),
	}
	got := renderLabels(sets[0], nil, differingAffixes(sets))
	want := `{msg="say \"hi\" ñandú-` + underlineOn + `1` + underlineOff + `"}`
	if got != want {
		t.Fatalf("renderLabels() = %q, want %q", got, want)
	}
	// without the underline it is exactly labels.Labels.String
	if plain := renderLabels(sets[0], nil, nil); plain != sets[0].String() {
		t.Fatalf("renderLabels() without diffs = %q, want %q", plain, sets[0].String())
	}
}

func TestUnderlineOffWhenNoColorOrRaw(t *testing.T) {
	for _, tc := range []struct {
		name, noColor string
		raw           bool
	}{
		{"NO_COLOR", "1", false},
		{"--repl-raw", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTerminal(t, true)
			t.Setenv("NO_COLOR", tc.noColor)
			t.Setenv("TERM", "xterm-256color")
			rawOutput = tc.raw
			var buf bytes.Buffer
			PrintUpstreamQueryResultToWriter(&promql.Result{Value: twoSeriesVector()}, &buf)
			if strings.Contains(buf.String(), underlineOn) {
				t.Fatalf("unexpected underline:\n%q", buf.String())
			}
		})
	}
}

func TestPrintVectorTimestampsKeptWhenTheyDiffer(t *testing.T) {
	withTerminal(t, true)
	v := twoSeriesVector()
	v[1].T = 1000
	var buf bytes.Buffer
	PrintUpstreamQueryResultToWriter(&promql.Result{Value: v}, &buf)
	out := buf.String()
	if strings.Contains(out, "common_timestamp") || strings.Count(out, " @ ") != 2 {
		t.Fatalf("differing timestamps must stay on each line:\n%s", out)
	}
}
