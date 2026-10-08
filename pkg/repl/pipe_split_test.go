package repl

import "testing"

func TestSplitQueryAndPipe_Quotes(t *testing.T) {
	cases := []struct {
		line, left, right string
		pipe              bool
	}{
		// '|' inside any PromQL string style is part of the string, not a pipe.
		{`.prom_scrape_range http://x:9090/ 'node_load1|up' now-6h now 1m`, "", "", false},
		{`up{job=~"a|b"}`, "", "", false},
		{"up{job=~`a|b`}", "", "", false},
		{`up{job=~'a|b'}`, "", "", false},
		// Escapes inside "..." and '...' don't end the string; raw `...` has none.
		{`x{a="q\"|"} | wc -l`, `x{a="q\"|"}`, "wc -l", true},
		{`x{a='it\'s|x'} | cat`, `x{a='it\'s|x'}`, "cat", true},
		{"x{a=`\\`} | cat", "x{a=`\\`}", "cat", true},
		// Real pipes.
		{`up | grep x`, "up", "grep x", true},
		{`.metrics | grep -c node_`, ".metrics", "grep -c node_", true},
		{`up{job=~'a|b'} | head -1`, `up{job=~'a|b'}`, "head -1", true},
		// An unterminated quote swallows the rest of the line: no pipe.
		{`up{job=~'a|b`, "", "", false},
	}
	for _, c := range cases {
		left, right, pipe := splitQueryAndPipe(c.line)
		if pipe != c.pipe {
			t.Errorf("%q: pipe=%v, want %v", c.line, pipe, c.pipe)
			continue
		}
		if pipe && (left != c.left || right != c.right) {
			t.Errorf("%q: got (%q, %q), want (%q, %q)", c.line, left, right, c.left, c.right)
		}
		if HasShellPipe(c.line) != c.pipe {
			t.Errorf("%q: HasShellPipe must agree with splitQueryAndPipe", c.line)
		}
	}
}
