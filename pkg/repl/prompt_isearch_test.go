//go:build !noprompt

package repl

import "testing"

func TestReverseSearch(t *testing.T) {
	hist := []string{"sum(rate(foo[5m]))", "up", "count(up)", "rate(bar[1m])"}
	tests := []struct {
		name   string
		query  string
		before int
		want   int
		wantOK bool
	}{
		{"newest first", "up", 4, 2, true},
		{"next older match", "up", 2, 1, true},
		{"stop at oldest", "up", 1, -1, false},
		{"no match", "zzz", 4, -1, false},
		{"empty query matches newest", "", 4, 3, true},
		{"empty query older", "", 3, 2, true},
		{"before beyond len is clamped", "foo", 99, 0, true},
		{"before zero", "up", 0, -1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := reverseSearch(hist, tt.query, tt.before)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("reverseSearch(%q, %d) = (%d, %v), want (%d, %v)", tt.query, tt.before, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestIsearchStateTransitions(t *testing.T) {
	hist := []string{"sum(rate(foo[5m]))", "up", "count(up)"}
	var s isearchState

	// Ctrl-R with empty buffer: empty query shows the newest entry.
	if text, ok := s.start("", hist); !ok || text != "count(up)" || !s.active {
		t.Fatalf("start = (%q, %v), active=%v", text, ok, s.active)
	}
	// Typing narrows the query and re-searches from the newest entry.
	for _, c := range []byte("cou") {
		s.extend(c, hist)
	}
	if p, _ := s.prompt(); p != "(reverse-i-search)`cou': " || s.line != "count(up)" {
		t.Fatalf("prompt=%q line=%q", p, s.line)
	}
	// Repeated Ctrl-R: no older "cou" match, prompt reports failure and buffer is kept.
	if _, ok := s.start("count(up)", hist); ok {
		t.Fatal("expected no older match")
	}
	if p, _ := s.prompt(); p != "(failed reverse-i-search)`cou': " {
		t.Fatalf("prompt=%q", p)
	}
	// Backspace shortens the query and finds a match again.
	if text, ok := s.backspace(hist); !ok || text != "count(up)" {
		t.Fatalf("backspace = (%q, %v)", text, ok)
	}
	s.backspace(hist) // "c"
	s.backspace(hist) // ""
	// Empty query, newest, then repeated Ctrl-R steps to older entries.
	if text, _ := s.start("", hist); text != "up" {
		t.Fatalf("older = %q, want up", text)
	}
	if text, _ := s.start("", hist); text != "sum(rate(foo[5m]))" {
		t.Fatalf("older = %q", text)
	}
	if _, ok := s.start("", hist); ok {
		t.Fatal("expected stop at oldest")
	}
	// Cancel restores the original text and leaves the mode.
	if got := s.cancel(); got != "" || s.active {
		t.Fatalf("cancel = %q active=%v", got, s.active)
	}
}

func TestIsearchInitialQueryFromBufferAndLeave(t *testing.T) {
	hist := []string{"up", "count(up)", "up"}
	var s isearchState
	text, ok := s.start("co", hist)
	if !ok || text != "count(up)" || s.original != "co" {
		t.Fatalf("start = (%q, %v) original=%q", text, ok, s.original)
	}
	s.leave()
	if s.active {
		t.Fatal("leave should deactivate")
	}
	if _, ok := s.prompt(); ok {
		t.Fatal("no search prompt when inactive")
	}
}

func TestIsearchSkipsDuplicateOfCurrentLine(t *testing.T) {
	hist := []string{"up", "up", "count(up)", "up"}
	var s isearchState
	s.start("up", hist) // newest "up" (idx 3)
	if text, ok := s.start("up", hist); !ok || text != "count(up)" {
		t.Fatalf("next = (%q, %v)", text, ok)
	}
	if text, ok := s.start("up", hist); !ok || text != "up" {
		t.Fatalf("next = (%q, %v)", text, ok)
	}
}
