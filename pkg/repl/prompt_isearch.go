//go:build !noprompt

package repl

import (
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/c-bata/go-prompt"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// commandRunning is true while the prompt executor runs a command (the
// terminal is in cooked mode then, so Ctrl-C/Ctrl-Z generate signals).
var commandRunning atomic.Bool

// cookedTermState is the terminal state of stdin captured before go-prompt's
// first Setup. go-prompt v0.2.6 saves a pointer to its "original" termios and
// later mutates it to raw in place, so its TearDown() restores raw mode and
// cannot be used to get a usable terminal while a command runs.
var cookedTermState *term.State

// captureCookedTerminal records the current (cooked) state of stdin. It must be
// called before go-prompt's first Setup. It is a no-op when stdin is not a TTY.
func captureCookedTerminal() {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return
	}
	if st, err := term.GetState(fd); err == nil {
		cookedTermState = st
	}
}

// restoreCookedTerminal puts stdin back into the captured cooked state.
func restoreCookedTerminal() {
	if cookedTermState != nil {
		_ = term.Restore(int(os.Stdin.Fd()), cookedTermState)
	}
}

// runPromptCommand runs the executor with the cooked terminal state restored,
// so that Ctrl-C (SIGINT) and Ctrl-Z (SIGTSTP) work while a command runs.
// go-prompt re-enters raw mode itself once the executor returns.
func runPromptCommand(s string) {
	commandRunning.Store(true)
	defer commandRunning.Store(false)
	restoreCookedTerminal()
	promptExecutor(s)
}

// suspendPrompt handles Ctrl-Z at the idle prompt (raw mode, so it arrives as
// a plain byte): restore the cooked state, stop the process like a shell job,
// and re-apply the raw state once resumed with SIGCONT.
func suspendPrompt() {
	fd := int(os.Stdin.Fd())
	var rawState *term.State
	if term.IsTerminal(fd) {
		rawState, _ = term.GetState(fd)
	}
	restoreCookedTerminal()

	cont := make(chan os.Signal, 1)
	signal.Notify(cont, syscall.SIGCONT)
	defer signal.Stop(cont)

	if err := unix.Kill(0, unix.SIGTSTP); err == nil {
		// Kill returns before the stop is necessarily delivered; wait for the
		// resume. The timeout covers an orphaned process group, where the
		// kernel discards SIGTSTP.
		select {
		case <-cont:
		case <-time.After(300 * time.Millisecond):
		}
	}

	if rawState != nil {
		_ = term.Restore(fd, rawState)
	}
}

// reverseSearch returns the index of the most recent history entry older than
// index `before` (exclusive) that contains query. history is oldest first.
func reverseSearch(history []string, query string, before int) (int, bool) {
	if before > len(history) {
		before = len(history)
	}
	for i := before - 1; i >= 0; i-- {
		if strings.Contains(history[i], query) {
			return i, true
		}
	}
	return -1, false
}

// isearchState is the state of an active Ctrl-R incremental history search.
type isearchState struct {
	active   bool
	query    string
	original string // buffer text when the search started
	match    int    // index into history of the current match
	found    bool   // whether the last search found a match
	line     string // text currently shown as the match
}

// isearch is the global search state of the prompt REPL.
var isearch isearchState

// prompt returns the live prompt prefix while searching.
func (s *isearchState) prompt() (string, bool) {
	if !s.active {
		return "", false
	}
	if s.found {
		return "(reverse-i-search)`" + s.query + "': ", true
	}
	return "(failed reverse-i-search)`" + s.query + "': ", true
}

// searchFrom searches older than `before`, skipping lines identical to the
// one currently shown. It returns the new buffer text and whether it changed.
func (s *isearchState) searchFrom(history []string, before int, skipCurrent bool) (string, bool) {
	for {
		idx, ok := reverseSearch(history, s.query, before)
		if !ok {
			s.found = false
			return "", false
		}
		if skipCurrent && s.found && history[idx] == s.line {
			before = idx
			continue
		}
		s.found, s.match, s.line = true, idx, history[idx]
		return s.line, true
	}
}

// start enters search mode (or advances to the next older match when already
// searching). It returns the text to put in the buffer, if it changed.
func (s *isearchState) start(bufText string, history []string) (string, bool) {
	if s.active {
		return s.searchFrom(history, s.match, true)
	}
	*s = isearchState{active: true, query: bufText, original: bufText}
	s.match = len(history)
	return s.searchFrom(history, len(history), false)
}

// extend appends c to the query and re-searches from the newest entry.
func (s *isearchState) extend(c byte, history []string) (string, bool) {
	s.query += string(c)
	s.found = false
	return s.searchFrom(history, len(history), false)
}

// backspace shortens the query and re-searches from the newest entry.
func (s *isearchState) backspace(history []string) (string, bool) {
	if s.query == "" {
		return "", false
	}
	s.query = s.query[:len(s.query)-1]
	s.found = false
	return s.searchFrom(history, len(history), false)
}

// cancel leaves search mode and returns the original buffer text.
func (s *isearchState) cancel() string {
	orig := s.original
	*s = isearchState{}
	return orig
}

// leave exits search mode keeping whatever is in the buffer.
func (s *isearchState) leave() {
	*s = isearchState{}
}

// setBufferText replaces the whole buffer content.
func setBufferText(buf *prompt.Buffer, text string) {
	doc := buf.Document()
	buf.DeleteBeforeCursor(len([]rune(doc.TextBeforeCursor())))
	buf.Delete(len([]rune(doc.TextAfterCursor())))
	buf.InsertText(text, false, true)
}

// isearchOptions returns the go-prompt key bindings implementing Ctrl-R.
func isearchOptions() []prompt.Option {
	leave := func(_ *prompt.Buffer) { isearch.leave() }
	opts := []prompt.Option{
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlR,
			Fn: func(buf *prompt.Buffer) {
				if !isearch.active {
					resetHistoryState()
				}
				if text, ok := isearch.start(buf.Text(), replHistory); ok {
					setBufferText(buf, text)
				}
			},
		}),
		// Enter executes the buffer (the match) as usual; just leave the mode.
		prompt.OptionAddKeyBind(prompt.KeyBind{Key: prompt.ControlM, Fn: leave}),
		prompt.OptionAddKeyBind(prompt.KeyBind{Key: prompt.ControlJ, Fn: leave}),
		prompt.OptionAddKeyBind(prompt.KeyBind{Key: prompt.Left, Fn: leave}),
		prompt.OptionAddKeyBind(prompt.KeyBind{Key: prompt.Right, Fn: leave}),
	}
	cancel := func(buf *prompt.Buffer) {
		if isearch.active {
			setBufferText(buf, isearch.cancel())
		}
	}
	for _, k := range []prompt.Key{prompt.Escape, prompt.ControlG} {
		opts = append(opts, prompt.OptionAddKeyBind(prompt.KeyBind{Key: k, Fn: cancel}))
	}
	back := func(buf *prompt.Buffer) {
		if !isearch.active {
			return // the default binding already deleted the char
		}
		if text, ok := isearch.backspace(replHistory); ok {
			setBufferText(buf, text)
		}
	}
	for _, k := range []prompt.Key{prompt.Backspace, prompt.ControlH} {
		opts = append(opts, prompt.OptionAddKeyBind(prompt.KeyBind{Key: k, Fn: back}))
	}
	// Typed printable characters: extend the query while searching, otherwise
	// behave exactly like normal input. go-prompt matches ASCII binds against
	// the whole read chunk, so pasted multi-byte chunks are inserted normally.
	for c := byte(0x20); c <= 0x7e; c++ {
		opts = append(opts, prompt.OptionAddASCIICodeBind(prompt.ASCIICodeBind{
			ASCIICode: []byte{c},
			Fn: func(buf *prompt.Buffer) {
				if !isearch.active {
					if c == '/' && shouldSwallowSlash(buf.Document().TextBeforeCursor()) {
						return
					}
					buf.InsertText(string(c), false, true)
					return
				}
				if text, ok := isearch.extend(c, replHistory); ok {
					setBufferText(buf, text)
				}
			},
		}))
	}
	return opts
}
