package repl

import (
	"bytes"
	"reflect"
	"strings"
	"unsafe"

	"github.com/c-bata/go-prompt"
)

// continuationPrompt is shown while a multi-line input is being accumulated.
const continuationPrompt = "...> "

// multiLineDiscardedMsg is printed when an in-progress multi-line input is aborted.
const multiLineDiscardedMsg = "(multi-line input discarded)"

// scanOutsideQuotes walks s calling fn for every rune that is outside quoted
// strings and outside '#' comments. It mirrors the quote handling of
// splitQueryAndPipe: "..." and '...' honour backslash escapes, `...` is raw.
// It returns true when s ends inside an open string.
func scanOutsideQuotes(s string, fn func(i int, r rune)) (openString bool) {
	var quote rune
	esc := false
	inComment := false
	for i, r := range s {
		if inComment {
			if r == '\n' {
				inComment = false
			}
			continue
		}
		if quote != 0 {
			if esc {
				esc = false
				continue
			}
			if r == '\\' && quote != '`' {
				esc = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '"', '\'', '`':
			quote = r
		case '#':
			inComment = true
		default:
			fn(i, r)
		}
	}
	return quote != 0
}

// endsWithContinuation reports whether the last line of s ends with a
// continuation backslash (an odd number of trailing backslashes).
func endsWithContinuation(s string) bool {
	last := strings.TrimRight(s[strings.LastIndexByte(s, '\n')+1:], " \t")
	n := len(last) - len(strings.TrimRight(last, `\`))
	return n%2 == 1
}

// inputIncomplete reports whether s is a PromQL expression that is still
// open: an unclosed ( [ { or an unterminated string, or a last line ending
// with a continuation backslash. Extra closers are not "incomplete" (the
// parser reports them). Shell lines (!cmd) never continue.
func inputIncomplete(s string) bool {
	if strings.HasPrefix(strings.TrimSpace(s), "!") {
		return false
	}
	if endsWithContinuation(s) {
		return true
	}
	depth := 0
	open := scanOutsideQuotes(s, func(_ int, r rune) {
		switch r {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		}
	})
	return open || depth > 0
}

// stripComment removes a trailing '#' comment (outside quotes) from a single line.
func stripComment(line string) string {
	cut := -1
	var quote rune
	esc := false
	for i, r := range line {
		if quote != 0 {
			if esc {
				esc = false
				continue
			}
			if r == '\\' && quote != '`' {
				esc = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '"', '\'', '`':
			quote = r
		case '#':
			cut = i
		}
		if cut >= 0 {
			break
		}
	}
	if cut < 0 {
		return line
	}
	return line[:cut]
}

// joinContinuation joins the accumulated lines into a single line: comments
// are stripped (so one cannot swallow the rest of the query), then the lines
// are joined as joinLines does.
func joinContinuation(lines []string) string {
	stripped := make([]string, len(lines))
	for i, l := range lines {
		stripped[i] = stripComment(l)
	}
	return joinLines(stripped)
}

// joinLines trims each line and joins them with single spaces. A line ending
// with a continuation backslash inside an open string is joined to the next
// one with no space, keeping everything before the backslash verbatim (only the
// backslash and the next line's indentation go), so a long string or regex can
// be split across indented lines; outside strings the backslash is dropped and
// a space is kept, so tokens never merge.
func joinLines(lines []string) string {
	var b strings.Builder
	glue := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if b.Len() > 0 && !glue {
			b.WriteByte(' ')
		}
		glue = false
		if endsWithContinuation(t) {
			t = t[:len(t)-1]
			glue = scanOutsideQuotes(b.String()+t, func(int, rune) {})
			if !glue {
				t = strings.TrimRight(t, " \t")
			}
		}
		b.WriteString(t)
	}
	return b.String()
}

// lineSplitParser wraps a go-prompt ConsoleParser so a pasted chunk containing
// line terminators is delivered piecewise: text segments and a lone "\r"
// (Enter) per Read call. go-prompt would otherwise insert the whole chunk, with
// embedded newlines, into its single-line buffer and redraw it on every keystroke.
type lineSplitParser struct {
	prompt.ConsoleParser
	queue [][]byte
}

func newLineSplitParser(p prompt.ConsoleParser) *lineSplitParser {
	return &lineSplitParser{ConsoleParser: p}
}

// Read implements prompt.ConsoleParser.
func (l *lineSplitParser) Read() ([]byte, error) {
	if len(l.queue) == 0 {
		b, err := l.ConsoleParser.Read()
		if err != nil || len(b) == 0 {
			return b, err
		}
		l.queue = splitChunkLines(b)
	}
	out := l.queue[0]
	l.queue = l.queue[1:]
	return out, nil
}

// splitChunkLines splits a raw input chunk into text segments and "\r"
// Enter keys. Chunks that are escape sequences or contain no newline, and a
// lone Enter, are returned unchanged.
func splitChunkLines(b []byte) [][]byte {
	if len(b) == 0 || b[0] == 0x1b || !bytes.ContainsAny(b, "\r\n") || len(b) == 1 {
		return [][]byte{b}
	}
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] != '\r' && b[i] != '\n' {
			continue
		}
		if i > start {
			out = append(out, append([]byte(nil), b[start:i]...))
		}
		out = append(out, []byte{'\r'})
		if b[i] == '\r' && i+1 < len(b) && b[i+1] == '\n' {
			i++
		}
		start = i + 1
	}
	if start < len(b) {
		out = append(out, append([]byte(nil), b[start:]...))
	}
	return out
}

// clearPromptHistory empties go-prompt's internal history (an unexported
// field). go-prompt records every Enter (including continuation fragments)
// there and applies Up/Down to it before our own key binds run, which would
// replace the buffer with a fragment and break our prefix-based history
// navigation. Our own replHistory is the single source of truth.
func clearPromptHistory(p *prompt.Prompt) {
	if p == nil {
		return
	}
	f := reflect.ValueOf(p).Elem().FieldByName("history")
	if !f.IsValid() || f.Kind() != reflect.Pointer || f.IsNil() || f.Elem().Kind() != reflect.Struct {
		return
	}
	hv := f.Elem()
	set := func(name string, v reflect.Value) {
		// Silently skip fields whose layout differs from go-prompt v0.2.6.
		if fld := hv.FieldByName(name); fld.IsValid() && fld.Type() == v.Type() {
			reflect.NewAt(fld.Type(), unsafe.Pointer(fld.UnsafeAddr())).Elem().Set(v)
		}
	}
	set("histories", reflect.ValueOf([]string{}))
	set("tmp", reflect.ValueOf([]string{""}))
	set("selected", reflect.ValueOf(0))
}
