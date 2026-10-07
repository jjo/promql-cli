package repl

import (
	"encoding/json"
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
