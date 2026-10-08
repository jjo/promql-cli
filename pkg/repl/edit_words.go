package repl

import (
	"strings"
	"unicode"

	prompt "github.com/c-bata/go-prompt"
)

// Pure ports of chzyer/readline's RuneBuffer word operations, so that
// --repl=prompt edits words exactly like the default --repl=readline backend.
// All indexes are rune indexes; word breaks come from isEditWordBreak.

// moveToPrevWord returns the cursor index after Alt-B.
func moveToPrevWord(buf []rune, idx int) int {
	if idx == 0 {
		return 0
	}
	for i := idx - 1; i > 0; i-- {
		if !isEditWordBreak(buf[i]) && isEditWordBreak(buf[i-1]) {
			return i
		}
	}
	return 0
}

// moveToNextWord returns the cursor index after Alt-F.
func moveToNextWord(buf []rune, idx int) int {
	for i := idx + 1; i < len(buf); i++ {
		if !isEditWordBreak(buf[i]) && isEditWordBreak(buf[i-1]) {
			return i
		}
	}
	return len(buf)
}

// backEscapeWordStart returns the index where Ctrl-W / Alt-Backspace stop:
// the range [start, idx) is the text to delete. readline itself wipes the whole
// line when no word start is found; the readline backend works around that by
// deleting only [0, idx), which is what returning 0 means here.
func backEscapeWordStart(buf []rune, idx int) int {
	if idx == 0 {
		return 0
	}
	for i := idx - 1; i > 0; i-- {
		if !isEditWordBreak(buf[i]) && isEditWordBreak(buf[i-1]) {
			return i
		}
	}
	return 0
}

// deleteWordEnd returns the end (exclusive) of the range [idx, end) deleted by
// Alt-D. Like readline it also removes the break characters after the word,
// keeping the one just before the next word.
func deleteWordEnd(buf []rune, idx int) int {
	if idx >= len(buf) {
		return idx
	}
	init := idx
	for init < len(buf) && isEditWordBreak(buf[init]) {
		init++
	}
	for i := init + 1; i < len(buf); i++ {
		if !isEditWordBreak(buf[i]) && isEditWordBreak(buf[i-1]) {
			return i - 1
		}
	}
	return len(buf)
}

// wordEnd returns the end of the word starting at idx (for Alt-U/L/C).
func wordEnd(buf []rune, idx int) int {
	end := idx
	for end < len(buf) && !isEditWordBreak(buf[end]) {
		end++
	}
	return end
}

// bufferRunes returns the buffer text and the cursor position as rune index.
func bufferRunes(b *prompt.Buffer) ([]rune, int) {
	return []rune(b.Text()), len([]rune(b.Document().TextBeforeCursor()))
}

// moveCursorTo moves the cursor to rune index target. go-prompt's CursorLeft
// and CursorRight are limited to the current line, so stop when stuck.
func moveCursorTo(b *prompt.Buffer, target int) {
	for {
		_, cur := bufferRunes(b)
		switch {
		case cur < target:
			b.CursorRight(target - cur)
		case cur > target:
			b.CursorLeft(cur - target)
		default:
			return
		}
		if _, now := bufferRunes(b); now == cur {
			return
		}
	}
}

// deleteRange removes the runes [from, to) from the buffer, leaving the cursor
// at from. It only uses rune-based Buffer operations (Buffer.Delete counts
// bytes and breaks on non-ASCII text).
func deleteRange(b *prompt.Buffer, from, to int) {
	if to <= from {
		return
	}
	moveCursorTo(b, to)
	if _, cur := bufferRunes(b); cur > from {
		b.DeleteBeforeCursor(cur - from)
	}
}

// replaceWordAtCursor rewrites the word from the cursor to its end with
// fn(word); the cursor ends after the rewritten word.
func replaceWordAtCursor(b *prompt.Buffer, fn func(string) string) {
	text, pos := bufferRunes(b)
	end := wordEnd(text, pos)
	if end <= pos {
		return
	}
	word := string(text[pos:end])
	deleteRange(b, pos, end)
	b.InsertText(fn(word), false, true)
}

func capitalizeWord(w string) string {
	r := []rune(w)
	return string(unicode.ToUpper(r[0])) + strings.ToLower(string(r[1:]))
}

func editMoveToNextWord(b *prompt.Buffer) {
	text, pos := bufferRunes(b)
	moveCursorTo(b, moveToNextWord(text, pos))
}

func editMoveToPrevWord(b *prompt.Buffer) {
	text, pos := bufferRunes(b)
	moveCursorTo(b, moveToPrevWord(text, pos))
}

func editBackEscapeWord(b *prompt.Buffer) {
	text, pos := bufferRunes(b)
	deleteRange(b, backEscapeWordStart(text, pos), pos)
}

func editDeleteWord(b *prompt.Buffer) {
	text, pos := bufferRunes(b)
	deleteRange(b, pos, deleteWordEnd(text, pos))
}
