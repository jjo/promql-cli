package repl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chzyer/readline"
	"github.com/prometheus/prometheus/promql"
	promparser "github.com/prometheus/prometheus/promql/parser"
	"golang.org/x/sys/unix"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// lastExecutedCommand is shared across REPL backends to enable features like Alt+.
var lastExecutedCommand string

var replTimeout = 60 * time.Second

// rlInputGate gates stdin to readline so we can pause input while running an external editor.
var rlInputGate *inputGate

// inputGate proxies bytes from a real source (os.Stdin) to a pipe that readline consumes.
// When paused, it stops reading from the source so the editor can read directly from the TTY.
type inputGate struct {
	src    *os.File
	r      *io.PipeReader
	w      *io.PipeWriter
	paused uint32 // atomic 0/1
	stop   chan struct{}
}

func newInputGate(src *os.File) *inputGate {
	pr, pw := io.Pipe()
	g := &inputGate{src: src, r: pr, w: pw, stop: make(chan struct{})}
	go g.loop()
	return g
}

func (g *inputGate) Reader() io.ReadCloser { return g.r }
func (g *inputGate) Pause()                { atomic.StoreUint32(&g.paused, 1) }
func (g *inputGate) Resume()               { atomic.StoreUint32(&g.paused, 0) }
func (g *inputGate) Closed() bool          { return atomic.LoadUint32(&g.paused) == 2 }
func (g *inputGate) Close() {
	select {
	case <-g.stop:
		// already closed
	default:
		close(g.stop)
	}
	_ = g.r.Close()
	_ = g.w.Close()
	atomic.StoreUint32(&g.paused, 2)
}

func (g *inputGate) loop() {
	buf := make([]byte, 4096)
	for {
		if atomic.LoadUint32(&g.paused) == 1 {
			select {
			case <-g.stop:
				return
			case <-time.After(10 * time.Millisecond):
				continue
			}
		}
		select {
		case <-g.stop:
			return
		default:
		}
		n, err := g.src.Read(buf)
		if n > 0 {
			_, _ = g.w.Write(buf[:n])
		}
		if err != nil {
			_ = g.w.CloseWithError(err)
			return
		}
	}
}

// Flush drains any immediately available bytes from the real stdin (TTY) without forwarding them.
func (g *inputGate) Flush() {
	if g == nil || g.src == nil {
		return
	}
	fd := int(g.src.Fd())
	_ = unix.SetNonblock(fd, true)
	defer func() { _ = unix.SetNonblock(fd, false) }()
	buf := make([]byte, 8192)
	for {
		n, err := unix.Read(fd, buf)
		if n <= 0 {
			if err == nil || err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				break
			}
			break
		}
		// Continue until empty
	}
}

// runInteractiveQueries starts an interactive query session using readline for enhanced UX.
// It allows users to execute PromQL queries against the loaded metrics with history and completion.
func runInteractiveQueries(engine *promql.Engine, storage *sstorage.SimpleStorage, silent bool) {
	if !silent {
		fmt.Println("Enter PromQL queries (or 'quit' to exit):")
		fmt.Println()
	}

	// Configure readline
	// History prefix-search on Up/Down is implemented via a custom Listener that
	// replaces the default Prev/Next history behavior when a non-empty prefix is present.
	historyPath := getHistoryFilePath()
	userHistory := loadHistoryFromFile(historyPath)

	// State for prefix-based history navigation
	type histState struct {
		lastPrefix string // prefix captured at activation (stable during nav)
		seedLine   []rune // editing line before entering navigation for lastPrefix
		matches    []int  // indices into userHistory (most recent first)
		idx        int    // current selection index in matches; len(matches) means seedLine
		active     bool   // true while navigating with Up/Down
	}
	state := &histState{idx: 0, active: false}

	// State for chords and ESC sequences
	var lastCtrlX time.Time
	var escPending bool
	var escAt time.Time

	// State for Alt+. (yank-last-arg) cycling
	var yankLastArgActive bool
	var yankLastArgIndex int       // Index into history for cycling
	var yankLastArgInserted string // What was inserted last time

	// Optional custom Alt+. rune codepoint (for terminals that send non-ESC meta)
	var altDotRune rune
	if v := strings.TrimSpace(os.Getenv("PROMQL_CLI_ALT_DOT_KEY")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			altDotRune = rune(n)
		} else if r := []rune(v); len(r) == 1 {
			altDotRune = r[0]
		}
	}

	// Build matches for a given prefix, ordered from most recent to oldest
	buildMatches := func(prefix string) {
		state.matches = state.matches[:0]
		if prefix == "" {
			state.idx = 0
			return
		}
		low := prefix
		for i := len(userHistory) - 1; i >= 0; i-- {
			cand := userHistory[i]
			if strings.HasPrefix(cand, low) {
				state.matches = append(state.matches, i)
			}
		}
		state.idx = len(state.matches) // start from seed position (no selection yet)
	}

	// Track previous line state to detect if Ctrl-W cleared the whole line
	var prevLine []rune
	var prevPos int

	// PromQL-aware previous-word deletion for Ctrl-W
	deletePrevWord := func(line []rune, pos int) ([]rune, int) {
		if pos == 0 {
			return line, pos
		}
		// Skip any separators immediately before the cursor
		i := pos
		for i > 0 {
			c := byte(line[i-1])
			if isWordBoundary(c) {
				i--
				continue
			}
			break
		}
		// If we only found separators and reached start, delete just the separators
		if i == 0 {
			newLine := append([]rune(nil), line[pos:]...)
			return newLine, 0
		}
		// Then delete the previous word
		for i > 0 {
			c := byte(line[i-1])
			if isWordBoundary(c) {
				break
			}
			i--
		}
		newLine := append([]rune(nil), line[:i]...)
		newLine = append(newLine, line[pos:]...)
		return newLine, i
	}

	listener := func(line []rune, pos int, key rune) (newLine []rune, newPos int, ok bool) {
		// Optional key debug
		if os.Getenv("PROMQL_CLI_DEBUG_KEYS") == "1" {
			fmt.Printf("\n[key=%#U code=%d]\n", key, key)
		}

		// Special handling for Ctrl-W and Ctrl-Backspace: readline processed them first (buggy), we fix it here
		// Ctrl-W = rune(23), but other word-delete keys might also trigger this
		if len(line) == 0 && len(prevLine) > 0 && prevPos > 0 {
			// A word-delete key was pressed and readline cleared the whole line (bug)
			// Apply our correct deletion logic using the previous state
			// This handles Ctrl-W (23) and potentially Ctrl-Backspace which might not even reach us as a distinct key
			nl, np := deletePrevWord(prevLine, prevPos)
			// Save state for next iteration
			prevLine = append(prevLine[:0], nl...)
			prevPos = np
			return nl, np, true
		}

		const (
			keyDown  = rune(14) // readline CharNext (Ctrl-N)
			keyUp    = rune(16) // readline CharPrev (Ctrl-P)
			keyCtrlY = rune(25) // Ctrl-Y (Yank/paste AI clipboard)
			keyCtrlW = rune(23) // Ctrl-W (delete previous word with PromQL boundaries)
			keyCtrlX = rune(24) // Ctrl-X (start chord Ctrl-X Ctrl-E)
			keyCtrlE = rune(5)  // Ctrl-E (end-of-line or chord with Ctrl-X)
			keyESC   = rune(27) // ESC (start of Alt- bindings)
		)

		// Helper: strip certain control runes (e.g., ^X, ^E) from a rune slice
		stripCtrl := func(rs []rune) []rune {
			if len(rs) == 0 {
				return rs
			}
			out := rs[:0]
			for _, r := range rs {
				if r == rune(0x18) || r == rune(0x05) { // ^X, ^E
					continue
				}
				out = append(out, r)
			}
			return out
		}

		// Ctrl-Y: paste AI clipboard (from .ai edit N)
		if key == keyCtrlY {
			if strings.TrimSpace(aiClipboard) == "" {
				cur := append([]rune(nil), line...)
				return cur, pos, true
			}
			newLine := []rune(aiClipboard)
			return newLine, len(newLine), true
		}

		// Ctrl-W: PromQL-aware delete previous word
		if key == keyCtrlW {
			if os.Getenv("PROMQL_CLI_DEBUG_KEYS") == "1" {
				fmt.Printf("\n[Ctrl-W] line=%q pos=%d\n", string(line), pos)
			}
			nl, np := deletePrevWord(line, pos)
			if os.Getenv("PROMQL_CLI_DEBUG_KEYS") == "1" {
				fmt.Printf("[Ctrl-W] newLine=%q newPos=%d\n", string(nl), np)
			}
			return nl, np, true
		}

		// Ctrl-X: begin chord (for Ctrl-X Ctrl-E external editor)
		if key == keyCtrlX {
			lastCtrlX = time.Now()
			// Consume the keystroke and actively remove any literal ^X that might have been inserted
			nl := append([]rune(nil), line...)
			// Remove ^X at or near cursor if present
			const ctrlXRune = rune(0x18)
			if pos > 0 && pos-1 < len(nl) && nl[pos-1] == ctrlXRune {
				nl = append(nl[:pos-1], nl[pos:]...)
				pos--
			} else if pos < len(nl) && nl[pos] == ctrlXRune {
				nl = append(nl[:pos], nl[pos+1:]...)
			} else if len(nl) > 0 && nl[len(nl)-1] == ctrlXRune {
				// Sometimes control rune lands at end
				nl = nl[:len(nl)-1]
				if pos > len(nl) {
					pos = len(nl)
				}
			}
			return nl, pos, true
		}
		// Ctrl-E: if recently after Ctrl-X, launch external editor
		if key == keyCtrlE {
			if !lastCtrlX.IsZero() && time.Since(lastCtrlX) <= 1500*time.Millisecond {
				lastCtrlX = time.Time{}
				// Sanitize current line to avoid passing literal control chars to the editor
				clean := stripCtrl(append([]rune(nil), line...))
				edited := rlLaunchExternalEditorForReadline(string(clean))
				if edited != "" {
					new := []rune(edited)
					return new, len(new), true
				}
				// If editor failed or empty, keep sanitized current line and consume
				cur := clean
				if pos > len(cur) {
					pos = len(cur)
				}
				return cur, pos, true
			}
			// otherwise let default Ctrl-E behavior (move to end) proceed
			return nil, 0, false
		}

		// Simple support for Alt-F / Alt-B via ESC sequences
		// Some terminals may deliver ESC as NUL (0). Treat both as Meta prefix.
		if key == keyESC || key == rune(0) {
			escPending = true
			escAt = time.Now()
			// Consume the ESC/NUL so readline doesn't interfere with subsequent key detection
			// Return current line unchanged
			return append([]rune(nil), line...), pos, true
		}
		if escPending {
			within := time.Since(escAt) <= 1500*time.Millisecond
			escPending = false
			// Only handle the next key as part of ESC- sequence if within window
			if within {
				if key == 'f' {
					// move forward to start of next word
					i := pos
					// skip current word
					for i < len(line) && !isWordBoundary(byte(line[i])) {
						i++
					}
					// skip separators
					for i < len(line) && isWordBoundary(byte(line[i])) {
						i++
					}
					return append([]rune(nil), line...), i, true
				}
				if key == 'b' {
					// move backward to start of previous word
					i := pos
					// skip separators
					for i > 0 && isWordBoundary(byte(line[i-1])) {
						i--
					}
					// skip word
					for i > 0 && !isWordBoundary(byte(line[i-1])) {
						i--
					}
					return append([]rune(nil), line...), i, true
				}
				// ESC+Backspace/DEL: Delete word backward (Ctrl-Backspace in some terminals)
				if key == 127 || key == 8 { // DEL or Backspace
					nl, np := deletePrevWord(line, pos)
					return nl, np, true
				}
				if key == '.' || key == '>' || key == ',' || (altDotRune != 0 && key == altDotRune) {
					// Alt+. (or Alt+> / Alt+, fallbacks): insert/cycle last argument from history
					cleanLine := line
					cleanPos := pos

					// Remove the trigger character (., >, or ,) that readline already inserted
					if pos > 0 && (line[pos-1] == '.' || line[pos-1] == '>' || line[pos-1] == ',') {
						cleanLine = append([]rune(nil), line[:pos-1]...)
						cleanLine = append(cleanLine, line[pos:]...)
						cleanPos = pos - 1
					}

					// If we're cycling (successive Alt+.), remove the previously inserted text
					if yankLastArgActive && yankLastArgInserted != "" {
						// Check if the previously inserted text is at the cursor position
						insLen := len([]rune(yankLastArgInserted))
						if cleanPos >= insLen {
							// Check if the text before cursor matches what we inserted
							prevText := string(cleanLine[cleanPos-insLen : cleanPos])
							if prevText == yankLastArgInserted {
								// Remove the previously inserted text
								cleanLine = append([]rune(nil), cleanLine[:cleanPos-insLen]...)
								cleanLine = append(cleanLine, cleanLine[cleanPos:]...) // Keep text after cursor
								cleanPos -= insLen
								// Move to older history entry for next iteration
								yankLastArgIndex--
							}
						}
					} else {
						// First time: start from most recent history (will be used in search below)
						yankLastArgActive = true
						yankLastArgIndex = len(userHistory)
					}

					// Get the last argument from history at the current index
					// Search backward from yankLastArgIndex for a non-empty last arg
					var lastArg string
					// Start search from the next older entry
					for i := yankLastArgIndex - 1; i >= 0; i-- {
						lastArg = rlExtractLastArgument(userHistory[i])
						if lastArg != "" {
							// Found one, update our position
							yankLastArgIndex = i
							break
						}
					}

					if lastArg == "" {
						// No more history, reset state
						yankLastArgActive = false
						return append([]rune(nil), cleanLine...), cleanPos, true
					}

					// Insert the argument
					yankLastArgInserted = lastArg
					ins := []rune(lastArg)
					newLine := make([]rune, 0, len(cleanLine)+len(ins))
					newLine = append(newLine, cleanLine[:cleanPos]...)
					newLine = append(newLine, ins...)
					newLine = append(newLine, cleanLine[cleanPos:]...)
					return newLine, cleanPos + len(ins), true
				}
			}
			// Not a recognized ESC sequence; treat this key normally (do not consume)
			// Keep escPending false so we don't chain indefinitely.
		}

		// Ctrl-X followed by '.' chord: insert last argument (fallback for terminals without Meta)
		if !lastCtrlX.IsZero() && time.Since(lastCtrlX) <= 1500*time.Millisecond && key == '.' {
			lastCtrlX = time.Time{}
			lastArg := rlExtractLastArgument(lastExecutedCommand)
			if lastArg != "" {
				ins := []rune(lastArg)
				newLine := make([]rune, 0, len(line)+len(ins))
				newLine = append(newLine, line[:pos]...)
				newLine = append(newLine, ins...)
				newLine = append(newLine, line[pos:]...)
				return newLine, pos + len(ins), true
			}
		}

		// Helper to start or refresh a navigation session for the given prefix
		startOrRefresh := func(prefix string, currentLine []rune) {
			if !state.active || prefix != state.lastPrefix {
				state.active = true
				state.lastPrefix = prefix
				state.seedLine = append(state.seedLine[:0], currentLine...)
				buildMatches(prefix)
				state.idx = len(state.matches) // start from seed position (no selection yet)
			}
		}

		// Up: previous matching history (older)
		if key == keyUp {
			prefix := string(line[:pos])
			// If no prefix, allow default readline history behavior
			if strings.TrimSpace(prefix) == "" {
				state.active = false
				state.lastPrefix = ""
				return nil, 0, false
			}
			startOrRefresh(prefix, line)
			if len(state.matches) == 0 {
				// No matches; end session and fall back to default behavior
				state.active = false
				state.lastPrefix = ""
				return nil, 0, false
			}
			if state.idx > 0 && state.idx <= len(state.matches) {
				state.idx--
			} else if state.idx == len(state.matches) {
				// first time pressing Up with this prefix
				state.idx = 0
			}
			idx := state.matches[state.idx]
			candidate := []rune(userHistory[idx])
			return candidate, len(candidate), true
		}
		// Down: next matching history (newer), eventually back to seedLine
		if key == keyDown {
			if !state.active {
				return nil, 0, false
			}
			if len(state.matches) == 0 {
				state.active = false
				state.lastPrefix = ""
				return nil, 0, false
			}
			if state.idx < len(state.matches)-1 {
				state.idx++
				idx := state.matches[state.idx]
				candidate := []rune(userHistory[idx])
				return candidate, len(candidate), true
			}
			// Move beyond the newest match back to the original editing seed line and exit nav
			state.idx = len(state.matches)
			seed := append([]rune(nil), state.seedLine...)
			state.active = false
			state.lastPrefix = ""
			return seed, len(seed), true
		}

		// Handle standalone . key when yank-last-arg is active
		// This allows cycling even if readline eats the ESC prefix on subsequent presses
		if yankLastArgActive && (key == '.' || key == '>' || key == ',') {
			// Treat this as if it were Alt+. (readline already inserted the character)
			// Remove the trigger character that was just inserted
			cleanLine := line
			cleanPos := pos
			if pos > 0 && (line[pos-1] == '.' || line[pos-1] == '>' || line[pos-1] == ',') {
				cleanLine = append([]rune(nil), line[:pos-1]...)
				cleanLine = append(cleanLine, line[pos:]...)
				cleanPos = pos - 1
			}

			// Check if the previously inserted text is at the cursor position
			if yankLastArgInserted != "" {
				insLen := len([]rune(yankLastArgInserted))
				if cleanPos >= insLen {
					prevText := string(cleanLine[cleanPos-insLen : cleanPos])
					if prevText == yankLastArgInserted {
						// Remove the previously inserted text
						startIdx := cleanPos - insLen
						if startIdx < 0 {
							startIdx = 0
						}
						if startIdx > len(cleanLine) {
							startIdx = len(cleanLine)
						}
						if cleanPos > len(cleanLine) {
							cleanPos = len(cleanLine)
						}
						// Reconstruct: [before_insertion] + [after_cursor]
						newCleanLine := make([]rune, 0, len(cleanLine))
						newCleanLine = append(newCleanLine, cleanLine[:startIdx]...)
						newCleanLine = append(newCleanLine, cleanLine[cleanPos:]...)
						cleanLine = newCleanLine
						cleanPos = startIdx
						// Move to older history entry
						yankLastArgIndex--
					}
				}
			}

			// Search for next last arg in history
			var lastArg string
			for i := yankLastArgIndex - 1; i >= 0; i-- {
				lastArg = rlExtractLastArgument(userHistory[i])
				if lastArg != "" {
					yankLastArgIndex = i
					break
				}
			}

			if lastArg == "" {
				// No more history, resetting
				yankLastArgActive = false
				return append([]rune(nil), cleanLine...), cleanPos, true
			}

			// Insert the new argument
			yankLastArgInserted = lastArg
			ins := []rune(lastArg)
			newLine := make([]rune, 0, len(cleanLine)+len(ins))
			newLine = append(newLine, cleanLine[:cleanPos]...)
			newLine = append(newLine, ins...)
			newLine = append(newLine, cleanLine[cleanPos:]...)
			return newLine, cleanPos + len(ins), true
		}

		// Any other key: end any active navigation and yank state, let readline proceed
		if state.active {
			state.active = false
			state.lastPrefix = ""
		}
		// Reset yank-last-arg state on any key that's not ESC or part of the Alt+. sequence
		// Note: We can't check processedEscSequence here because it's out of scope
		// So we check if we're not in an ESC sequence context
		if key != keyESC && key != rune(0) {
			// Only reset if we're not currently processing an ESC sequence
			// The escPending check happens BEFORE this, so we check if the key itself is a trigger
			if key != '.' && key != '>' && key != ',' && (altDotRune == 0 || key != altDotRune) {
				yankLastArgActive = false
				yankLastArgInserted = ""
			}
		}

		// Fallback key: Ctrl-] inserts last argument (portable), or custom altDotRune if supplied
		if key == rune(29) || (altDotRune != 0 && key == altDotRune) {
			lastArg := rlExtractLastArgument(lastExecutedCommand)
			if lastArg == "" {
				return append([]rune(nil), line...), pos, true
			}
			ins := []rune(lastArg)
			newLine := make([]rune, 0, len(line)+len(ins))
			newLine = append(newLine, line[:pos]...)
			newLine = append(newLine, ins...)
			newLine = append(newLine, line[pos:]...)
			return newLine, pos + len(ins), true
		}

		// Save current state for next keystroke (for Ctrl-W fix)
		// Make a copy to avoid shared slice issues
		if len(line) > 0 {
			prevLine = append(prevLine[:0], line...)
			prevPos = pos
		}

		return nil, 0, false
	}

	// Build a gated stdin so we can pause input while the external editor is active
	rlInputGate = newInputGate(os.Stdin)
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "> ",
		HistoryFile:     historyPath,
		AutoComplete:    createAutoCompleter(storage), // Dynamic tab completion
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
		Listener:        readline.FuncListener(listener),
		Stdin:           rlInputGate.Reader(),
	})
	if err != nil {
		fmt.Printf("Warning: Could not initialize readline, falling back to basic input: %v\n", err)
		runBasicInteractiveQueries(engine, storage, silent)
		return
	}
	defer func() { _ = rl.Close() }()
	// Ensure we stop proxying input when leaving the REPL
	defer func() {
		if rlInputGate != nil {
			rlInputGate.Close()
			rlInputGate = nil
		}
	}()

	// Multi-line continuation state (using backslash at EOL)
	var mlActive bool
	var mlParts []string

	getPrompt := func() string {
		if aiInProgress {
			return "AI...> "
		}
		if mlActive {
			return "      > "
		}
		if pinnedEvalTime != nil {
			return "PromQL(pinat)> "
		}
		return "PromQL> "
	}

	for {
		// Update prompt dynamically
		rl.SetPrompt(getPrompt())

		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				// On Ctrl-C during multi-line, cancel accumulation
				mlActive = false
				mlParts = nil
				continue
			} else if err == io.EOF {
				break
			}
			fmt.Printf("Error reading input: %v\n", err)
			break
		}

		// Multi-line continuation if line ends with a single backslash
		trimmedRight := strings.TrimRight(line, " \t")
		if strings.HasSuffix(trimmedRight, "\\") && !strings.HasSuffix(trimmedRight, "\\\\") {
			part := strings.TrimSuffix(trimmedRight, "\\")
			part = strings.TrimSpace(part)
			if part != "" {
				mlParts = append(mlParts, part)
			}
			mlActive = true
			// Continue reading next line with continuation prompt
			continue
		}

		// Keep our in-memory history in sync (readline persists to file separately)
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			userHistory = append(userHistory, trimmed)
		}

		query := strings.TrimSpace(line)
		if query == "" && !mlActive {
			continue
		}

		if mlActive {
			if s := strings.TrimSpace(query); s != "" {
				mlParts = append(mlParts, s)
			}
			query = strings.TrimSpace(strings.Join(mlParts, " "))
			mlActive = false
			mlParts = nil
			if query == "" {
				continue
			}
		}

		if query == "quit" || query == ".quit" {
			break
		}

		// Track last executed command for Alt+.
		lastExecutedCommand = query

		// Delegate full-line execution (ad-hoc, !cmd, query, pipes) to executeOne
		executeOne(engine, storage, query)
	}
}

// loadHistoryFromFile reads non-empty lines from the given history file path.
func loadHistoryFromFile(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimRight(ln, "\r")
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

// PrometheusAutoCompleter provides dynamic auto-completion for PromQL queries
// based on the loaded metrics data, similar to the Prometheus UI experience.
// AutoCompleteOptions controls optional completion behaviors, configurable via env vars.
type AutoCompleteOptions struct {
	AutoBrace       bool // when completing a metric name uniquely, append '{'
	LabelNameEquals bool // when completing a label name, append '="'
	AutoCloseQuote  bool // when completing a label value, append closing '"'
}

type PrometheusAutoCompleter struct {
	storage *sstorage.SimpleStorage
	opts    AutoCompleteOptions
}

// getFilePathCompletions returns filesystem path candidates for a given path string and current last-segment word.
func (pac *PrometheusAutoCompleter) getFilePathCompletions(pathSoFar, currentWord string) []string {
	// Expand ~ to home
	expandTilde := func(p string) string {
		if strings.HasPrefix(p, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				return filepath.Join(home, strings.TrimPrefix(p, "~"))
			}
		}
		return p
	}
	p := expandTilde(pathSoFar)
	dir, base := filepath.Split(p)
	if dir == "" {
		dir = "."
	}
	// List directory entries
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	low := strings.ToLower(base)
	for _, e := range ents {
		name := e.Name()
		if !strings.HasPrefix(strings.ToLower(name), low) {
			continue
		}
		if e.IsDir() {
			name = name + "/"
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// NewPrometheusAutoCompleter creates a new auto-completer with access to metric data.
func NewPrometheusAutoCompleter(storage *sstorage.SimpleStorage) *PrometheusAutoCompleter {
	return &PrometheusAutoCompleter{storage: storage, opts: loadAutoCompleteOptions()}
}

// Do implements the readline.AutoCompleter interface to provide dynamic completions.
func (pac *PrometheusAutoCompleter) Do(line []rune, pos int) (newLine [][]rune, length int) {
	lineStr := string(line)
	cursorPos := pos

	// Determine current word at cursor and context
	currentWord, _ := pac.getCurrentWord(lineStr, cursorPos)
	ctx := pac.analyzeContext(lineStr, cursorPos)

	// Fetch context-aware completions (full candidates)
	completions := pac.getCompletions(lineStr, cursorPos, currentWord)

	// Dedupe and filter to those that extend currentWord
	uniq := make(map[string]struct{}, len(completions))
	suffixes := make([][]rune, 0, len(completions))
	// Detect if we are completing an ad-hoc dot-command (like .labels, .metrics)
	dotCmdMode := func() bool {
		trim := strings.TrimLeft(lineStr[:cursorPos], " \t")
		return strings.HasPrefix(trim, ".")
	}()
	for _, cand := range completions {
		if _, ok := uniq[cand]; ok {
			continue
		}
		uniq[cand] = struct{}{}
		// Only consider candidates that start with currentWord
		if strings.HasPrefix(cand, currentWord) {
			cw := []rune(currentWord)
			cr := []rune(cand)
			if len(cr) >= len(cw) {
				// base suffix beyond current word
				suf := make([]rune, len(cr[len(cw):]))
				copy(suf, cr[len(cw):])

				if !dotCmdMode {
					// Optional tweaks based on context (disabled for dot-commands)
					switch ctx.Type {
					case "metric_name":
						if pac.opts.AutoBrace && len(completions) == 1 {
							suf = append(suf, '{')
						}
						// Also suggest a range-vector scaffold directly if candidate is a metric and unique
						if len(completions) == 1 {
							suffixes = append(suffixes, []rune("[5m]"))
						}
					case "label_name":
						if pac.opts.LabelNameEquals {
							// Provide multiple operator choices for label matching
							ops := [][]rune{{'=', '"'}, {'!', '=', '"'}, {'=', '~', '"'}, {'!', '~', '"'}}
							for _, op := range ops {
								// clone base remainder
								base := make([]rune, len(cr[len(cw):]))
								copy(base, cr[len(cw):])
								cand := append(base, op...)
								suffixes = append(suffixes, cand)
							}
						}
					case "label_value":
						// Check if we already have an opening quote
						hasOpenQuote := false
						// Look back from cursor to find the operator
						for i := cursorPos - 1; i >= 0; i-- {
							if lineStr[i] == '"' {
								hasOpenQuote = true
								break
							}
							if lineStr[i] == '=' || lineStr[i] == '~' {
								break
							}
						}

						// If no opening quote, add it at the beginning
						if !hasOpenQuote {
							suf = append([]rune{'"'}, suf...)
						}

						// Add closing quote if option is enabled
						if pac.opts.AutoCloseQuote {
							suf = append(suf, '"')
						}
					}
				}

				suffixes = append(suffixes, suf)
			}
		}
	}

	if len(suffixes) == 0 {
		return nil, 0
	}

	// Return suffixes and replacement length = len(currentWord) in runes.
	// The upstream readline completer will aggregate LCP and enter select-mode
	// with arrow-key navigation automatically when multiple remain.
	return suffixes, runeLen(currentWord)
}

// getCurrentWord extracts the word currently being typed at the cursor position.
func (pac *PrometheusAutoCompleter) getCurrentWord(line string, pos int) (string, int) {
	if pos > len(line) {
		pos = len(line)
	}

	// Find the start of the current word
	start := pos
	for start > 0 {
		c := line[start-1]
		// More comprehensive word boundary detection for PromQL
		if isWordBoundary(c) {
			break
		}
		start--
	}

	// Extract the word from start to cursor position
	currentWord := line[start:pos]
	return currentWord, start
}

// isWordBoundary checks if a character is a word boundary for PromQL
func isWordBoundary(c byte) bool {
	return c == ' ' || c == '(' || c == ')' || c == '{' || c == '}' ||
		c == ',' || c == '=' || c == '!' || c == '~' || c == '"' ||
		c == '\t' || c == '\n' || c == '+' || c == '-' || c == '*' ||
		c == '/' || c == '^' || c == '%'
}

// getCompletions returns appropriate completions based on the query context.
func (pac *PrometheusAutoCompleter) getCompletions(line string, pos int, currentWord string) []string {
	// Special handling for ad-hoc commands starting with '.'
	beforeCursor := line[:pos]
	trimmed := strings.TrimLeft(beforeCursor, " \t")

	// Range-vector scaffold suggestions when inside '[' ...
	if strings.HasSuffix(strings.TrimRight(beforeCursor, " \t"), "[") || strings.HasPrefix(currentWord, "[") {
		return getRangeDurationCompletions(currentWord)
	}
	// Do NOT offer ad-hoc dot-commands while inside label selectors {...}
	lastOpenBrace := strings.LastIndex(beforeCursor, "{")
	lastCloseBrace := strings.LastIndex(beforeCursor, "}")
	inLabels := lastOpenBrace > lastCloseBrace && lastOpenBrace != -1

	if !inLabels && strings.HasPrefix(trimmed, ".") {
		// If typing the command token, suggest available ad-hoc commands
		if strings.HasPrefix(currentWord, ".") || strings.TrimSpace(trimmed) == "." {
			cmds := GetAdHocCommandNames()
			var out []string
			for _, c := range cmds {
				if strings.HasPrefix(strings.ToLower(c), strings.ToLower(currentWord)) {
					out = append(out, c)
				}
			}
			return out
		}
		// If after ".ai ", offer subcommands or indices
		if strings.HasPrefix(strings.ToLower(trimmed), ".ai ") {
			after := strings.TrimSpace(trimmed[4:])
			low := strings.ToLower(after)
			if after == "" {
				return []string{"ask ", "run ", "edit ", "show"}
			}
			if strings.HasPrefix(low, "run ") || strings.HasPrefix(low, "edit ") {
				// suggest indices
				rest := after
				if strings.HasPrefix(low, "run ") {
					rest = strings.TrimSpace(after[len("run "):])
				}
				if strings.HasPrefix(low, "edit ") {
					rest = strings.TrimSpace(after[len("edit "):])
				}
				prefixNum := rest
				max := len(lastAISuggestions)
				if max > 20 {
					max = 20
				}
				var out []string
				for i := 1; i <= max; i++ {
					n := fmt.Sprintf("%d", i)
					if prefixNum == "" || strings.HasPrefix(n, prefixNum) {
						out = append(out, n)
					}
				}
				return out
			}
			// Otherwise, when typing "ask" or "show" we don't complete beyond token
			return []string{}
		}
		// If after ".labels ", ".seed ", ".drop ", or ".timestamps ", complete metric names
		if strings.HasPrefix(trimmed, ".labels ") || strings.HasPrefix(trimmed, ".seed ") ||
			strings.HasPrefix(trimmed, ".drop ") || strings.HasPrefix(trimmed, ".timestamps ") {
			return pac.getMetricNameCompletions(currentWord)
		}
		// No further completions for .help and .metrics
		if trimmed == ".help" || trimmed == ".metrics" || strings.HasPrefix(trimmed, ".help ") || strings.HasPrefix(trimmed, ".metrics ") {
			return []string{}
		}
		// If after ".load " or ".save ", complete filesystem paths (current word = base name)
		if strings.HasPrefix(trimmed, ".load ") || strings.HasPrefix(trimmed, ".save ") {
			// Extract the path substring after the command token
			var pathSoFar string
			if strings.HasPrefix(trimmed, ".load ") {
				pathSoFar = trimmed[len(".load "):]
			} else {
				pathSoFar = trimmed[len(".save "):]
			}
			return pac.getFilePathCompletions(pathSoFar, currentWord)
		}
		// If after ".scrape ", ".prom_scrape ", or ".prom_scrape_range ", offer URL examples
		if strings.HasPrefix(trimmed, ".scrape ") || strings.HasPrefix(trimmed, ".prom_scrape ") || strings.HasPrefix(trimmed, ".prom_scrape_range ") {
			after := trimmed[len(".scrape "):]
			// Only offer suggestions if no space yet (still typing URL)
			if !strings.Contains(after, " ") {
				urlExamples := []string{
					"http://localhost:9090/metrics",
					"http://localhost:9100/metrics",
					"http://localhost:8080/metrics",
					"http://localhost:3000/metrics",
					"http://localhost:9093/metrics",
					"http://localhost:9091/metrics",
					"http://localhost:2112/metrics",
					"http://localhost:9115/metrics",
				}
				var out []string
				for _, url := range urlExamples {
					// Show all URLs when currentWord is empty or filter if typing
					if currentWord == "" || strings.HasPrefix(url, currentWord) {
						out = append(out, url)
					}
				}
				return out
			}
			return []string{}
		}
		// If after ".pinat ", offer time presets similar to .at
		if strings.HasPrefix(trimmed, ".pinat ") {
			cmdIdx := strings.LastIndex(line[:pos], ".pinat ")
			if cmdIdx >= 0 {
				after := line[cmdIdx+7 : pos]
				// If still typing time token (no space yet), suggest presets
				if sp := strings.IndexAny(after, " \t"); sp == -1 {
					presets := []string{
						"now", "now-5m", "now-15m", "now-30m", "now-1h", "now-2h",
						"now-6h", "now-12h", "now-24h", "now-7d",
						"now+5m", "now+1h",
						"remove", // For .pinat
						time.Now().UTC().Format(time.RFC3339),
					}
					var out []string
					for _, p := range presets {
						// Show all presets when currentWord is empty or filter if typing
						if currentWord == "" || strings.HasPrefix(strings.ToLower(p), strings.ToLower(currentWord)) {
							out = append(out, p)
						}
					}
					return out
				}
			}
		}
		// If after ".at ", either offer time presets or transition into query completions
		if strings.HasPrefix(trimmed, ".at ") {
			cmdIdx := strings.LastIndex(line[:pos], ".at ")
			if cmdIdx >= 0 {
				after := line[cmdIdx+4 : pos]
				// If still typing time token (no space yet), suggest presets or a space once token is valid
				if sp := strings.IndexAny(after, " \t"); sp == -1 {
					tok := strings.TrimSpace(after)
					if tok != "" {
						if _, err := parseEvalTime(tok); err == nil || strings.EqualFold(tok, "now") {
							// insert a space to move into query context
							return []string{" "}
						}
					}
					presets := []string{
						"now", "now-5m", "now-15m", "now-30m", "now-1h", "now-2h",
						"now-6h", "now-12h", "now-24h", "now-7d",
						"now+5m", "now+1h",
						time.Now().UTC().Format(time.RFC3339),
					}
					var out []string
					for _, p := range presets {
						// Show all presets when currentWord is empty or filter if typing
						if currentWord == "" || strings.HasPrefix(strings.ToLower(p), strings.ToLower(currentWord)) {
							out = append(out, p)
						}
					}
					return out
				}
				// We have a space after time; delegate to query completions for the remainder
				queryStart := cmdIdx + 4 + strings.IndexAny(line[cmdIdx+4:], " \t") + 1
				if queryStart <= len(line) {
					subline := line[queryStart:]
					subpos := pos - queryStart
					subWord, _ := pac.getCurrentWord(subline, subpos)
					return pac.getCompletions(subline, subpos, subWord)
				}
			}
		}
	}

	// Analyze the context to determine what type of completion to provide
	context := pac.analyzeContext(line, pos)

	switch context.Type {
	case "metric_name":
		// Suggest metrics, range templates, aggregators, and functions when starting an expression
		var out []string
		out = append(out, pac.getMetricNameCompletions(currentWord)...)
		// Include range-vector scaffolds as standalone tokens
		out = append(out, getBracketedRangeTemplates()...)
		// Aggregators like sum, avg, min, max, topk, bottomk, quantile, etc.
		out = append(out, getAggregatorCompletions(currentWord)...)
		// Functions from upstream parser (e.g., sum_over_time)
		out = append(out, pac.getFunctionCompletions(currentWord)...)
		return out
	case "label_name":
		return pac.getLabelNameCompletions(context.MetricName, currentWord)
	case "label_value":
		return pac.getLabelValueCompletions(context.MetricName, context.LabelName, currentWord)
	case "function":
		return pac.getFunctionCompletions(currentWord)
	case "operator":
		return pac.getOperatorCompletions(currentWord)
	default:
		// Provide mixed completions when context is unclear
		return pac.getMixedCompletions(currentWord)
	}
}

// QueryContext represents the context of the current query position.
type QueryContext struct {
	Type       string // "metric_name", "label_name", "label_value", "function", "operator"
	MetricName string // The metric name if we're inside label selectors
	LabelName  string // The label name if we're typing a label value
}

// analyzeContext determines what type of completion should be provided based on cursor position.
func (pac *PrometheusAutoCompleter) analyzeContext(line string, pos int) QueryContext {
	if pos > len(line) {
		pos = len(line)
	}

	// Look at the characters around the cursor to determine context
	beforeCursor := line[:pos]
	_ = line[pos:] // afterCursor for future use

	// Check if we're inside label selectors {}
	lastOpenBrace := strings.LastIndex(beforeCursor, "{")
	lastCloseBrace := strings.LastIndex(beforeCursor, "}")

	if lastOpenBrace > lastCloseBrace && lastOpenBrace != -1 {
		// We're inside label selectors
		metricName := pac.extractMetricName(beforeCursor[:lastOpenBrace])

		// Check if we're typing a label value (after =, !=, =~, !~)
		labelValuePattern := regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*(!?[=~])\s*"?[^"]*$`)
		if matches := labelValuePattern.FindStringSubmatch(beforeCursor[lastOpenBrace+1:]); len(matches) > 1 {
			return QueryContext{
				Type:       "label_value",
				MetricName: metricName,
				LabelName:  matches[1],
			}
		}

		// Otherwise, we're typing a label name
		return QueryContext{
			Type:       "label_name",
			MetricName: metricName,
		}
	}

	// Check if we're typing a function
	if strings.HasSuffix(strings.TrimSpace(beforeCursor), "(") {
		return QueryContext{Type: "function"}
	}

	// Check for operators
	operatorPattern := regexp.MustCompile(`[+\-*/^%]\s*$`)
	if operatorPattern.MatchString(beforeCursor) {
		return QueryContext{Type: "operator"}
	}

	// Default to metric name completion
	return QueryContext{Type: "metric_name"}
}

// extractMetricName extracts the metric name from the text before label selectors.
func (pac *PrometheusAutoCompleter) extractMetricName(text string) string {
	// Simple extraction - look for the last word that could be a metric name
	text = strings.TrimSpace(text)
	words := strings.Fields(text)
	if len(words) > 0 {
		lastWord := words[len(words)-1]
		// Remove any function calls or operators
		metricPattern := regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_:]*)$`)
		if matches := metricPattern.FindStringSubmatch(lastWord); len(matches) > 1 {
			return matches[1]
		}
	}
	return ""
}

// getMetricNameCompletions returns metric names that match the current input.
func (pac *PrometheusAutoCompleter) getMetricNameCompletions(prefix string) []string {
	var completions []string

	for metricName := range pac.storage.Metrics {
		if strings.HasPrefix(strings.ToLower(metricName), strings.ToLower(prefix)) {
			completions = append(completions, metricName)
		}
	}

	sort.Strings(completions)
	return completions
}

// getLabelNameCompletions returns label names for a specific metric.
func (pac *PrometheusAutoCompleter) getLabelNameCompletions(metricName, prefix string) []string {
	labelNames := make(map[string]bool)

	// If no specific metric, get labels from all metrics
	metricsToCheck := make(map[string][]sstorage.MetricSample)
	if metricName != "" && pac.storage.Metrics[metricName] != nil {
		metricsToCheck[metricName] = pac.storage.Metrics[metricName]
	} else {
		metricsToCheck = pac.storage.Metrics
	}

	for _, samples := range metricsToCheck {
		for _, sample := range samples {
			for labelName := range sample.Labels {
				if labelName != "__name__" && strings.HasPrefix(strings.ToLower(labelName), strings.ToLower(prefix)) {
					labelNames[labelName] = true
				}
			}
		}
	}

	var completions []string
	for labelName := range labelNames {
		completions = append(completions, labelName)
	}

	sort.Strings(completions)
	return completions
}

// getLabelValueCompletions returns label values for a specific metric and label name.
func (pac *PrometheusAutoCompleter) getLabelValueCompletions(metricName, labelName, prefix string) []string {
	labelValues := make(map[string]bool)

	// If no specific metric, get values from all metrics
	metricsToCheck := make(map[string][]sstorage.MetricSample)
	if metricName != "" && pac.storage.Metrics[metricName] != nil {
		metricsToCheck[metricName] = pac.storage.Metrics[metricName]
	} else {
		metricsToCheck = pac.storage.Metrics
	}

	for _, samples := range metricsToCheck {
		for _, sample := range samples {
			if value, exists := sample.Labels[labelName]; exists {
				if strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
					labelValues[value] = true // raw value, no quotes; quotes handled in Do
				}
			}
		}
	}

	var completions []string
	for labelValue := range labelValues {
		completions = append(completions, labelValue)
	}

	sort.Strings(completions)
	return completions
}

// getFunctionCompletions returns PromQL function names.
func (pac *PrometheusAutoCompleter) getFunctionCompletions(prefix string) []string {
	var names []string
	for name, fn := range promparser.Functions {
		// Skip experimental functions if not enabled.
		if fn.Experimental && !promparser.EnableExperimentalFunctions {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var completions []string
	for _, name := range names {
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			// Base suggestion: name(
			completions = append(completions, name+"(")
			// Signature scaffold suggestion: name(args)
			if sig := buildFunctionSignature(name); sig != "" {
				completions = append(completions, name+"("+sig+")")
			}
		}
	}
	return completions
}

// getOperatorCompletions returns PromQL operators.
func (pac *PrometheusAutoCompleter) getOperatorCompletions(prefix string) []string {
	lowPref := strings.ToLower(prefix)
	seen := make(map[string]struct{})
	var out []string

	// 1) Arithmetic, comparison, and regex operators from parser's exported map.
	for typ, str := range promparser.ItemTypeStr {
		if typ.IsOperator() || typ.IsComparisonOperator() {
			candidate := str
			if strings.HasPrefix(strings.ToLower(candidate), lowPref) {
				if _, ok := seen[candidate]; !ok {
					seen[candidate] = struct{}{}
					out = append(out, candidate)
				}
			}
		}
	}

	// 2) Set operators and clause keywords (not present in ItemTypeStr with strings).
	keywords := []string{
		// set operators
		"and", "or", "unless",
		// join/label matching and grouping modifiers
		"by", "without", "on", "ignoring", "group_left", "group_right",
		// others
		"bool", "offset",
	}
	for _, kw := range keywords {
		if strings.HasPrefix(strings.ToLower(kw), lowPref) {
			if _, ok := seen[kw]; !ok {
				seen[kw] = struct{}{}
				out = append(out, kw)
			}
		}
	}

	sort.Strings(out)
	return out
}

// buildFunctionSignature builds a call signature hint from upstream function metadata.
func buildFunctionSignature(name string) string {
	fn, ok := promparser.Functions[name]
	if !ok {
		return ""
	}
	var parts []string
	for i, t := range fn.ArgTypes {
		parts = append(parts, placeholderForValueType(t, i))
	}
	if fn.Variadic >= 0 {
		parts = append(parts, "...")
	}
	return strings.Join(parts, ", ")
}

func placeholderForValueType(vt promparser.ValueType, _ int) string {
	switch vt {
	case promparser.ValueTypeVector:
		return "expr"
	case promparser.ValueTypeMatrix:
		return "expr[5m]"
	case promparser.ValueTypeScalar:
		return "scalar"
	case promparser.ValueTypeString:
		return "str"
	default:
		return "arg"
	}
}

// getAggregatorCompletions suggests aggregator keywords (not functions)
func getAggregatorCompletions(prefix string) []string {
	base := []string{
		"sum", "avg", "min", "max", "count", "group", "stddev", "stdvar",
		"topk", "bottomk", "quantile", "count_values",
	}
	// Experimental aggregators
	if promparser.EnableExperimentalFunctions {
		base = append(base, "limitk", "limit_ratio")
	}
	var out []string
	low := strings.ToLower(prefix)
	for _, name := range base {
		if strings.HasPrefix(strings.ToLower(name), low) {
			// Add with opening paren to hint call/aggregate form
			out = append(out, name+"(")
		}
	}
	return out
}

// splitQueryAndPipe splits a line into query and pipe command on a '|' that is outside double-quoted strings.
// Returns (query, cmd, true) when a top-level pipe is found; otherwise (line, "", false).
func splitQueryAndPipe(line string) (string, string, bool) {
	inStr := false
	esc := false
	for i, r := range line {
		if inStr {
			if esc {
				esc = false
				continue
			}
			if r == '\\' {
				esc = true
				continue
			}
			if r == '"' {
				inStr = false
			}
			continue
		}
		if r == '"' {
			inStr = true
			continue
		}
		if r == '|' {
			left := strings.TrimSpace(line[:i])
			right := strings.TrimSpace(line[i+1:])
			return left, right, true
		}
	}
	return line, "", false
}

func getBracketedRangeTemplates() []string {
	return []string{"[30s]", "[1m]", "[5m]", "[10m]", "[1h]", "[6h]", "[24h]"}
}

func getRangeDurationCompletions(currentWord string) []string {
	templates := getBracketedRangeTemplates()
	var out []string
	low := strings.ToLower(currentWord)
	for _, t := range templates {
		if strings.HasPrefix(strings.ToLower(t), low) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		// If nothing matches, still offer common ones
		out = append(out, "[5m]", "[1h]")
	}
	return out
}

// runeLen returns the rune length of a string (readline uses rune positions)
func runeLen(s string) int {
	return len([]rune(s))
}

// --- Readline helpers: external editor and terminal state ---

// rlFlattenEditorText converts multi-line editor content to a single line suitable for the REPL buffer.
// It normalizes CRLF/CR to LF, replaces newlines and tabs with spaces, and trims spaces.
func rlFlattenEditorText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	// Collapse multiple spaces and trim
	parts := strings.Fields(s)
	return strings.Join(parts, " ")
}

// rlExtractLastArgument extracts the last meaningful argument from a command line.
// It avoids operators, durations, and pure numbers.
func rlExtractLastArgument(cmd string) string {
	if strings.TrimSpace(cmd) == "" {
		return ""
	}
	// Ad-hoc command: return last field
	if strings.HasPrefix(cmd, ".") {
		parts := strings.Fields(cmd)
		if len(parts) > 1 {
			return parts[len(parts)-1]
		}
		return ""
	}
	seps := "(){}[]\" \t\n,="
	tokens := []string{}
	cur := ""
	for _, ch := range cmd {
		if strings.ContainsRune(seps, ch) {
			if cur != "" {
				tokens = append(tokens, cur)
				cur = ""
			}
		} else {
			cur += string(ch)
		}
	}
	if cur != "" {
		tokens = append(tokens, cur)
	}
	ops := map[string]bool{
		"and": true, "or": true, "unless": true,
		"by": true, "without": true, "on": true, "ignoring": true,
		"group_left": true, "group_right": true,
		"offset": true, "bool": true,
	}
	durRe := regexp.MustCompile(`^\d+[smhdwy]$`)
	for i := len(tokens) - 1; i >= 0; i-- {
		t := tokens[i]
		if ops[strings.ToLower(t)] {
			continue
		}
		if _, err := strconv.ParseFloat(t, 64); err == nil {
			continue
		}
		if durRe.MatchString(t) {
			continue
		}
		return t
	}
	return ""
}

// rlSaveTerminalState saves the current terminal state using stty
func rlSaveTerminalState() string {
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// rlRestoreTerminalState restores a saved terminal state using stty
func rlRestoreTerminalState(state string) {
	if state == "" {
		return
	}
	cmd := exec.Command("stty", state)
	cmd.Stdin = os.Stdin
	_ = cmd.Run()
}

// rlLaunchExternalEditorForReadline opens the current line in the user's editor and returns
// the edited text (flattened to a single line). If the editor fails, returns empty string.
func rlLaunchExternalEditorForReadline(current string) string {
	// Prepare temp file with .promql extension
	tf, err := os.CreateTemp("", "promql-*.promql")
	if err != nil {
		fmt.Printf("Failed to create temp file: %v\n", err)
		return ""
	}
	path := tf.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := tf.WriteString(current); err != nil {
		_ = tf.Close()
		fmt.Printf("Failed to write temp file: %v\n", err)
		return ""
	}
	_ = tf.Close()

	// Pick editor: PROMQL_EDITOR > VISUAL > EDITOR > nano
	editor := os.Getenv("PROMQL_EDITOR")
	if strings.TrimSpace(editor) == "" {
		editor = os.Getenv("VISUAL")
	}
	if strings.TrimSpace(editor) == "" {
		editor = os.Getenv("EDITOR")
	}
	if strings.TrimSpace(editor) == "" {
		editor = "nano"
	}

	// Safely quote path
	shQuote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

	// Pause readline input so it won't intercept keys intended for the editor
	if rlInputGate != nil {
		rlInputGate.Pause()
	}
	// Save current (raw) tty state and switch to a sane cooked mode for the editor
	raw := rlSaveTerminalState()
	_ = exec.Command("stty", "sane").Run()

	cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("%s %s", editor, shQuote(path)))
	// Use the real TTY for the editor
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()

	// Return terminal to prior raw state
	rlRestoreTerminalState(raw)
	// Drain any pending bytes that may have been queued during editor exit
	if rlInputGate != nil {
		// Drain multiple times with small sleeps to capture late-arriving bytes
		for i := 0; i < 3; i++ {
			rlInputGate.Flush()
			time.Sleep(15 * time.Millisecond)
		}
		// Resume forwarding input to readline
		rlInputGate.Resume()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("Failed to read edited file: %v\n", err)
		return ""
	}
	text := strings.TrimRight(string(data), "\r\n")
	if text != "" {
		text = rlFlattenEditorText(text)
	}
	return text
}

// normalizeAtModifierTimestamps converts PromQL @ timestamps provided in ms/us/ns to seconds with decimals.
// E.g., metric @1758201240105 -> metric @ 1758201240.105
func normalizeAtModifierTimestamps(q string) string {
	re := regexp.MustCompile(`@\s*(\d{13,19})`)
	return re.ReplaceAllStringFunc(q, func(m string) string {
		// Extract digits
		digits := regexp.MustCompile(`\d+`).FindString(m)
		l := len(digits)
		var sec string
		switch l {
		case 13, 16, 19:
			sec = digits[:10] + "." + digits[10:]
		default:
			// Not a ms/us/ns value; keep as is
			return m
		}
		return "@ " + sec
	})
}

// parseEvalTime parses time tokens like RFC3339, unix seconds/millis, or now+/-duration.
func parseEvalTime(tok string) (time.Time, error) {
	// now+/-duration
	if strings.HasPrefix(tok, "now") {
		if tok == "now" {
			return time.Now(), nil
		}
		op := tok[3]
		durStr := strings.TrimSpace(tok[4:])
		d, err := time.ParseDuration(durStr)
		if err != nil {
			return time.Time{}, err
		}
		if op == '+' {
			return time.Now().Add(d), nil
		}
		return time.Now().Add(-d), nil
	}
	// RFC3339
	if t, err := time.Parse(time.RFC3339, tok); err == nil {
		return t, nil
	}
	// unix seconds or millis
	if n, err := strconv.ParseInt(tok, 10, 64); err == nil {
		if n > 1_000_000_000_000 { // ms
			return time.UnixMilli(n), nil
		}
		return time.Unix(n, 0), nil
	}
	return time.Time{}, fmt.Errorf("unsupported time format: %s", tok)
}

// seedHistory synthesizes historical samples for a metric to enable rate() queries.
func seedHistory(storage *sstorage.SimpleStorage, metric string, steps int, step time.Duration) {
	samples, ok := storage.Metrics[metric]
	if !ok || len(samples) == 0 {
		fmt.Printf("Metric '%s' not found or has no samples\n", metric)
		return
	}
	isCounter := strings.HasSuffix(metric, "_total") || strings.Contains(metric, "_total_")
	for idx := range samples {
		base := samples[idx]
		for i := 1; i <= steps; i++ {
			copyLabels := make(map[string]string, len(base.Labels))
			for k, v := range base.Labels {
				copyLabels[k] = v
			}
			newTs := base.Timestamp - int64((steps-i+1))*step.Milliseconds()
			var newVal float64
			if isCounter {
				dec := base.Value * 0.001
				if dec < 1 {
					dec = 1
				}
				newVal = base.Value - float64(i)*dec
				if newVal < 0 {
					newVal = 0
				}
			} else {
				// Gauges: small drift
				newVal = base.Value * (1 - 0.001*float64(i))
			}
			// Avoid appending duplicate timestamp points for the same labelset
			existing := storage.Metrics[metric]
			dup := false
			for _, s := range existing {
				if s.Timestamp == newTs && qEqualLabels(s.Labels, copyLabels) {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
			storage.Metrics[metric] = append(storage.Metrics[metric], sstorage.MetricSample{
				Labels:    copyLabels,
				Value:     newVal,
				Timestamp: newTs,
			})
		}
	}
}

// qEqualLabels compares two label maps for equality
func qEqualLabels(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		if b[k] != va {
			return false
		}
	}
	return true
}

// getEnvBool reads an environment variable and parses it as boolean.
// Accepts 1/0, true/false (case-insensitive). Falls back to defVal when unset/invalid.
func getEnvBool(name string, defVal bool) bool {
	v := os.Getenv(name)
	if v == "" {
		return defVal
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return defVal
	}
}

// loadAutoCompleteOptions reads options from environment variables with sane defaults.
func loadAutoCompleteOptions() AutoCompleteOptions {
	return AutoCompleteOptions{
		AutoBrace:       getEnvBool("PROMQL_CLI_COMPLETION_AUTO_BRACE", true),
		LabelNameEquals: getEnvBool("PROMQL_CLI_COMPLETION_LABEL_EQUALS", true),
		AutoCloseQuote:  getEnvBool("PROMQL_CLI_COMPLETION_AUTO_CLOSE_QUOTE", true),
	}
}

// getMixedCompletions provides a mix of all completion types when context is unclear.
func (pac *PrometheusAutoCompleter) getMixedCompletions(prefix string) []string {
	var completions []string

	// Add metric names, range templates, aggregators, and functions
	completions = append(completions, pac.getMetricNameCompletions(prefix)...)
	completions = append(completions, getBracketedRangeTemplates()...)
	completions = append(completions, getAggregatorCompletions(prefix)...)
	completions = append(completions, pac.getFunctionCompletions(prefix)...)

	// Add operators
	completions = append(completions, pac.getOperatorCompletions(prefix)...)

	// Add common keywords
	keywords := []string{"quit", "offset 5m"}
	for _, keyword := range keywords {
		if strings.HasPrefix(strings.ToLower(keyword), strings.ToLower(prefix)) {
			completions = append(completions, keyword)
		}
	}

	sort.Strings(completions)
	return completions
}

// createAutoCompleter creates the enhanced auto-completer with metric awareness.
// This provides a Prometheus UI-like experience with dynamic completions.
func createAutoCompleter(storage *sstorage.SimpleStorage) readline.AutoCompleter {
	return NewPrometheusAutoCompleter(storage)
}

// runBasicInteractiveQueries provides a fallback when readline is unavailable
func runBasicInteractiveQueries(engine *promql.Engine, storage *sstorage.SimpleStorage, silent bool) {
	if !silent {
		fmt.Println("Using basic input mode (readline unavailable)")
	}

	for {
		if pinnedEvalTime != nil {
			fmt.Print("PromQL(pinat)> ")
		} else {
			fmt.Print("PromQL> ")
		}
		var query string
		_, err := fmt.Scanln(&query)
		if err != nil {
			if err.Error() == "unexpected newline" {
				continue
			}
			break
		}

		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}

		if query == "quit" {
			break
		}

		executeOne(engine, storage, query)
	}
}

// executeOne runs a single command line. Supports ad-hoc dot-commands and PromQL (including .at <time> <query>).
func executeOne(engine *promql.Engine, storage *sstorage.SimpleStorage, line string) {
	orig := strings.TrimSpace(line)
	if orig == "" {
		return
	}

	// Shell bang: execute external command and show stdout/stderr
	if strings.HasPrefix(orig, "!") {
		cmdStr := strings.TrimSpace(orig[1:])
		if cmdStr == "" {
			fmt.Println("Usage: !<command>")
			return
		}
		cmd := exec.Command("/bin/sh", "-c", cmdStr)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			fmt.Printf("Command failed: %v\n", err)
		}
		return
	}

	// Split potential pipeline: <query> | <command> where '|' is outside double-quoted strings
	queryPart, pipeCmd, hasPipe := splitQueryAndPipe(orig)
	query := strings.TrimSpace(queryPart)

	// Ad-hoc commands (support piping for their printed output)
	if strings.HasPrefix(query, ".") {
		if hasPipe {
			captured, _ := captureOutput(func() {
				_ = handleAdHocFunction(query, storage)
			})
			if aiInProgress {
				fmt.Println("[note] AI request started asynchronously; subsequent output will not be piped")
			}
			if pendingAISuggestion != "" {
				next := pendingAISuggestion
				pendingAISuggestion = ""
				// Execute the suggested PromQL line while preserving the original pipe
				executeOne(engine, storage, next+" | "+pipeCmd)
				return
			}
			cmd := exec.Command("/bin/sh", "-c", pipeCmd)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			stdin, err := cmd.StdinPipe()
			if err != nil {
				fmt.Printf("Pipe setup failed: %v\n", err)
				return
			}
			if err := cmd.Start(); err != nil {
				fmt.Printf("Command start failed: %v\n", err)
				_ = stdin.Close()
				return
			}
			_, _ = io.WriteString(stdin, captured)
			_ = stdin.Close()
			if err := cmd.Wait(); err != nil {
				fmt.Printf("Command failed: %v\n", err)
			}
			return
		}
		if handleAdHocFunction(query, storage) {
			if pendingAISuggestion != "" {
				next := pendingAISuggestion
				pendingAISuggestion = ""
				executeOne(engine, storage, next)
			}
			return
		}
	}

	// Support pinned evaluation time set via .pinat, unless overridden by .at
	evalTime := time.Now()
	if pinnedEvalTime != nil {
		evalTime = *pinnedEvalTime
	}
	// Support ".at <time> <query>" (overrides pinned time)
	if strings.HasPrefix(query, ".at ") {
		parts := strings.Fields(query)
		if len(parts) >= 3 {
			if ts, err := parseEvalTime(parts[1]); err == nil {
				evalTime = ts
				query = strings.TrimPrefix(query, ".at "+parts[1]+" ")
			}
		}
	}
	// Normalize @<unix_ms> to seconds with decimals for PromQL @ modifier
	query = normalizeAtModifierTimestamps(query)

	ctx, cancel := context.WithTimeout(context.Background(), replTimeout)
	defer cancel()

	q, err := engine.NewInstantQuery(ctx, storage, nil, query, evalTime)
	if err != nil {
		fmt.Printf("Error creating query: %v\n", err)
		return
	}

	result := q.Exec(ctx)
	if result.Err != nil {
		fmt.Printf("Error: %v\n", result.Err)
		return
	}

	if hasPipe {
		// Capture the normal printed output and feed it to the pipe command
		captured, _ := captureOutput(func() { PrintUpstreamQueryResult(result) })
		cmd := exec.Command("/bin/sh", "-c", pipeCmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		stdin, err := cmd.StdinPipe()
		if err != nil {
			fmt.Printf("Pipe setup failed: %v\n", err)
			return
		}
		if err := cmd.Start(); err != nil {
			fmt.Printf("Command start failed: %v\n", err)
			_ = stdin.Close()
			return
		}
		_, _ = io.WriteString(stdin, captured)
		_ = stdin.Close()
		if err := cmd.Wait(); err != nil {
			fmt.Printf("Command failed: %v\n", err)
		}
		return
	}

	PrintUpstreamQueryResult(result)
}

// captureOutput captures stdout produced by fn and returns it as a string.
func captureOutput(fn func()) (string, error) {
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	outCh := make(chan []byte)
	go func() {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			// ignore copy error; capture best-effort
			_ = err
		}
		outCh <- buf.Bytes()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	b := <-outCh
	_ = r.Close()
	return string(b), nil
}

// getHistoryFilePath returns the path to the history file for readline
func getHistoryFilePath() string {
	// First check if PROMQL_CLI_HISTORY env var is set
	if histPath := os.Getenv("PROMQL_CLI_HISTORY"); histPath != "" {
		return histPath
	}

	// Prefer the user's home directory
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".promql-cli_history")
	}
	// As a safer fallback than /tmp, use current working directory
	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		return filepath.Join(cwd, ".promql-cli_history")
	}
	// Last resort: relative path in current process dir
	return ".promql-cli_history"
}

// RunInitCommands executes semicolon-separated commands before interactive session or one-off query.
// When silent is true, outputs produced by these commands are suppressed.
func RunInitCommands(engine *promql.Engine, storage *sstorage.SimpleStorage, commands string, silent bool) {
	if strings.TrimSpace(commands) == "" {
		return
	}

	var restore func()
	if silent {
		// Temporarily redirect stdout to /dev/null so ad-hoc and query prints are suppressed
		old := os.Stdout
		devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err == nil {
			os.Stdout = devnull
			restore = func() {
				_ = devnull.Close()
				os.Stdout = old
			}
		} else {
			restore = func() {}
		}
		defer restore()
	}

	// Split by ';' and newlines to allow multi-line input
	seps := strings.NewReplacer("\n", ";", "\r", ";")
	flat := seps.Replace(commands)
	parts := strings.Split(flat, ";")
	for _, p := range parts {
		cmd := strings.TrimSpace(p)
		if cmd == "" {
			continue
		}
		executeOne(engine, storage, cmd)
	}
}

func handleAdhocHistory(query string, storage *sstorage.SimpleStorage) bool {
	fields := strings.Fields(query)
	n := -1
	if len(fields) == 2 {
		if v, err := strconv.Atoi(fields[1]); err == nil && v > 0 {
			n = v
		} else {
			fmt.Println("Usage: .history [N]")
			return true
		}
	} else if len(fields) > 2 {
		fmt.Println("Usage: .history [N]")
		return true
	}
	// Prefer in-memory history when available (prompt backend)
	entries := getInMemoryHistory()
	if len(entries) == 0 {
		// Fallback to file
		path := getHistoryFilePath()
		entries = loadHistoryFromFile(path)
	}
	if len(entries) == 0 {
		fmt.Println("No history available")
		return true
	}
	start := 0
	if n > 0 && n < len(entries) {
		start = len(entries) - n
	}
	for i := start; i < len(entries); i++ {
		fmt.Println(entries[i])
	}
	return true
}
