package repl

import (
	"testing"

	"github.com/chzyer/readline"
)

// --repl=prompt word-wise editing must stop at the same characters as the
// default readline backend.
func TestIsEditWordBreak_MatchesReadline(t *testing.T) {
	for r := rune(0); r < 0x3000; r++ {
		if got, want := isEditWordBreak(r), readline.IsWordBreak(r); got != want {
			t.Fatalf("isEditWordBreak(%q) = %v, readline.IsWordBreak = %v", r, got, want)
		}
	}
}
