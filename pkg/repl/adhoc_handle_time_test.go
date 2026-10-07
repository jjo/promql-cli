package repl

import (
	"strings"
	"testing"
	"time"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

func pinatStore() *sstorage.SimpleStorage {
	s := sstorage.NewSimpleStorage()
	// Two scrapes ~5s apart, millisecond timestamps like a real .scrape.
	for _, inst := range []string{"a", "b"} {
		s.AddSample(map[string]string{"__name__": "node_load1", "instance": inst}, 1, 1791401839901)
	}
	s.AddSample(map[string]string{"__name__": "node_load1", "instance": "a"}, 2, 1791401844972)
	s.AddSample(map[string]string{"__name__": "node_load1", "instance": "b"}, 2, 1791401843000)
	s.AddSample(map[string]string{"__name__": "other_metric"}, 1, 1791409999000)
	s.AddSample(map[string]string{"__name__": "nowcast_total"}, 1, 1791400000123)
	s.AddSample(map[string]string{"__name__": "now_5m"}, 1, 1791400000456)
	return s
}

func TestAdhoc_Pinat_MetricSelector(t *testing.T) {
	cases := []struct {
		name      string
		arg       string
		wantMilli int64
		wantOut   string
	}{
		{"latest across series", "node_load1", 1791401844972, "latest sample of node_load1, 2 series"},
		{"label selector", `node_load1{instance="b"}`, 1791401843000, "1 series"},
		{"quoted selector", `'node_load1{instance="b"}'`, 1791401843000, "1 series"},
		{"bare matcher selector", `{__name__=~"node_.+"}`, 1791401844972, "2 series"},
		{"metric named like now", "nowcast_total", 1791400000123, "nowcast_total"},
		{"metric named like now-offset", "now_5m", 1791400000456, "now_5m"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pinnedEvalTime = nil
			defer func() { pinnedEvalTime = nil }()
			out := captureStdout(t, func() { _ = handleAdHocFunction(".pinat "+c.arg, pinatStore()) })
			if pinnedEvalTime == nil {
				t.Fatalf("expected pin to be set, got output: %s", out)
			}
			if got := pinnedEvalTime.UnixMilli(); got != c.wantMilli {
				t.Fatalf("pinned %d, want %d (output: %s)", got, c.wantMilli, out)
			}
			if !strings.Contains(out, c.wantOut) {
				t.Fatalf("expected output to contain %q, got: %s", c.wantOut, out)
			}
		})
	}
}

func TestAdhoc_Pinat_MetricSelectorErrors(t *testing.T) {
	cases := []struct {
		name    string
		arg     string
		store   *sstorage.SimpleStorage
		wantOut string
	}{
		{"unknown metric", "does_not_exist", pinatStore(), "no series match does_not_exist"},
		{"no match for labels", `node_load1{instance="zzz"}`, pinatStore(), "no series match"},
		{"invalid selector", "node_load1{", pinatStore(), "Invalid .pinat argument"},
		{"empty store", "node_load1", sstorage.NewSimpleStorage(), "no series match"},
		{"nil store", "node_load1", nil, "no storage loaded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prev := time.Unix(1700000000, 0)
			pinnedEvalTime = &prev
			defer func() { pinnedEvalTime = nil }()
			out := captureStdout(t, func() { _ = handleAdHocFunction(".pinat "+c.arg, c.store) })
			if !strings.Contains(out, c.wantOut) || !strings.Contains(out, "Invalid .pinat argument") {
				t.Fatalf("expected error containing %q, got: %s", c.wantOut, out)
			}
			if pinnedEvalTime == nil || !pinnedEvalTime.Equal(prev) {
				t.Fatalf("a failed .pinat must keep the previous pin, got %v", pinnedEvalTime)
			}
		})
	}
}

func TestAdhoc_Pinat_TimeFormatsTakePrecedence(t *testing.T) {
	pinnedEvalTime = nil
	defer func() { pinnedEvalTime = nil }()
	store := pinatStore()

	_ = captureStdout(t, func() { _ = handleAdHocFunction(".pinat 1700000000", store) })
	if pinnedEvalTime == nil || pinnedEvalTime.Unix() != 1700000000 {
		t.Fatalf("unix seconds must still parse as a time, got %v", pinnedEvalTime)
	}
	_ = captureStdout(t, func() { _ = handleAdHocFunction(".pinat 2025-09-16T20:40:00Z", store) })
	if want := time.Date(2025, 9, 16, 20, 40, 0, 0, time.UTC); pinnedEvalTime == nil || !pinnedEvalTime.Equal(want) {
		t.Fatalf("RFC3339 must still parse as a time, got %v", pinnedEvalTime)
	}
}

func TestParseEvalTime_NowOffsets(t *testing.T) {
	for _, tok := range []string{"now+5m", "now-5m"} {
		if _, err := parseEvalTime(tok); err != nil {
			t.Fatalf("parseEvalTime(%q) failed: %v", tok, err)
		}
	}
	for _, tok := range []string{"now_5m", "nowx1h", "now 5m"} {
		if _, err := parseEvalTime(tok); err == nil {
			t.Fatalf("parseEvalTime(%q) must fail: only + and - are offsets", tok)
		}
	}
}
