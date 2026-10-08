package repl

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"testing"
)

func TestJSONFloatNonFinite(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{1.5, `1.5`},
		{0, `0`},
		{math.NaN(), `"NaN"`},
		{math.Inf(1), `"+Inf"`},
		{math.Inf(-1), `"-Inf"`},
	}
	for _, c := range cases {
		b, err := json.Marshal(jsonFloat(c.in))
		if err != nil {
			t.Fatalf("marshal %v: %v", c.in, err)
		}
		if string(b) != c.want {
			t.Errorf("jsonFloat(%v) = %s, want %s", c.in, b, c.want)
		}
	}
}

func TestValueFormatterBoldOnlyOnTerminal(t *testing.T) {
	orig := isTerminal
	t.Cleanup(func() { isTerminal = orig })
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	isTerminal = func(io.Writer) bool { return false }
	if got := valueFormatter(io.Discard)(3.55); got != "3.55" {
		t.Errorf("not a terminal: got %q, want plain 3.55", got)
	}

	isTerminal = func(io.Writer) bool { return true }
	if got := valueFormatter(io.Discard)(3.55); got != valueStyle+"3.55\x1b[0m" {
		t.Errorf("terminal: got %q, want highlighted 3.55", got)
	}

	t.Setenv("NO_COLOR", "1")
	if got := valueFormatter(io.Discard)(3.55); got != "3.55" {
		t.Errorf("NO_COLOR: got %q, want plain 3.55", got)
	}

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if got := valueFormatter(io.Discard)(3.55); got != "3.55" {
		t.Errorf("TERM=dumb: got %q, want plain 3.55", got)
	}
}

func TestValueFormatterMatchesPercentG(t *testing.T) {
	orig := isTerminal
	t.Cleanup(func() { isTerminal = orig })
	isTerminal = func(io.Writer) bool { return false }
	for _, f := range []float64{0, 1, 3.55, 13.44814814814815, 6.178675614230592e+06, 1e21, 1e-7, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got, want := valueFormatter(io.Discard)(f), fmt.Sprintf("%g", f); got != want {
			t.Errorf("valueFormatter(%v) = %q, want %q (same as the previous %%g output)", f, got, want)
		}
	}
}
