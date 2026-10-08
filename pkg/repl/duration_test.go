package repl

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	ok := map[string]time.Duration{
		// stdlib forms keep working exactly as before
		"30s": 30 * time.Second, "1.5s": 1500 * time.Millisecond, "300us": 300 * time.Microsecond, "1h30m": 90 * time.Minute,
		// Prometheus units
		"7d": 7 * 24 * time.Hour, "2w": 14 * 24 * time.Hour, "1y": 365 * 24 * time.Hour, "1d12h": 36 * time.Hour,
	}
	for in, want := range ok {
		if got, err := parseDuration(in); err != nil || got != want {
			t.Errorf("parseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "7", "d", "abc", "7days"} {
		if _, err := parseDuration(in); err == nil {
			t.Errorf("parseDuration(%q) should fail", in)
		}
	}
}

func TestParseEvalTime_PrometheusUnits(t *testing.T) {
	before := time.Now()
	got, err := parseEvalTime("now-7d")
	if err != nil {
		t.Fatalf("now-7d: %v", err)
	}
	if d := before.Sub(got); d < 7*24*time.Hour-time.Minute || d > 7*24*time.Hour+time.Minute {
		t.Fatalf("now-7d is %v before now, want ~168h", d)
	}
}
