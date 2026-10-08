package repl

import (
	"io"
	"testing"

	prompt "github.com/c-bata/go-prompt"
	"github.com/chzyer/readline"
)

var editWordSamples = []string{
	"",
	"foo",
	"foo bar",
	"  foo   bar  ",
	"sum(rate(node_cpu_seconds_total{mode='idle'}[5m]))",
	"job:up:min",
	".scrape http://x:9100/metrics",
	"up{job='ñandú_é'} + 1",
	"((()))",
	"a",
	" a",
	"a ",
}

// readlineRB builds a non-interactive readline RuneBuffer holding text with the cursor at idx.
func readlineRB(text []rune, idx int) *readline.RuneBuffer {
	cfg := &readline.Config{FuncIsTerminal: func() bool { return false }}
	rb := readline.NewRuneBuffer(io.Discard, "", cfg, 80)
	rb.SetWithIdx(idx, append([]rune(nil), text...))
	return rb
}

// The prompt backend's word operations must match readline's RuneBuffer for
// every cursor position of every sample.
func TestEditWords_MatchReadlineRuneBuffer(t *testing.T) {
	for _, s := range editWordSamples {
		text := []rune(s)
		for idx := 0; idx <= len(text); idx++ {
			check := func(op string, run func(*readline.RuneBuffer), apply func(*prompt.Buffer)) {
				rb := readlineRB(text, idx)
				run(rb)

				pb := prompt.NewBuffer()
				pb.InsertText(s, false, true)
				moveCursorTo(pb, idx)
				apply(pb)

				wantText, wantIdx := rb.Runes(), rb.Pos()
				if op == "backEscapeWord" && len(wantText) == 0 && len(text) > 0 {
					// readline wipes the whole line when no word start is found;
					// the readline backend (repl.go) then restores it and deletes
					// just [0, idx). The prompt backend does the latter directly.
					wantText, wantIdx = text[idx:], 0
				}

				gotText, gotIdx := bufferRunes(pb)
				if string(gotText) != string(wantText) || gotIdx != wantIdx {
					t.Errorf("%s on %q @%d: prompt=(%q,%d) readline=(%q,%d)",
						op, s, idx, string(gotText), gotIdx, string(wantText), wantIdx)
				}
			}
			check("backEscapeWord", (*readline.RuneBuffer).BackEscapeWord, editBackEscapeWord)
			check("moveToPrevWord", func(rb *readline.RuneBuffer) { rb.MoveToPrevWord() }, editMoveToPrevWord)
			check("moveToNextWord", (*readline.RuneBuffer).MoveToNextWord, editMoveToNextWord)
			check("deleteWord", (*readline.RuneBuffer).DeleteWord, editDeleteWord)
		}
	}
}

func TestReplaceWordAtCursor(t *testing.T) {
	pb := prompt.NewBuffer()
	pb.InsertText("ñandú foo", false, true)
	moveCursorTo(pb, 6)
	replaceWordAtCursor(pb, capitalizeWord)
	if got := pb.Text(); got != "ñandú Foo" {
		t.Fatalf("got %q", got)
	}
}
