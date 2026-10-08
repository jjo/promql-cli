package repl

import (
	"os"
	"strings"
)

// PromQLSeparators defines PromQL-aware word separators used for word boundary detection.
const PromQLSeparators = " (){},=!~\"\t\n+-*/^%"

// getEditorCommand returns the user's preferred editor from environment variables,
// falling back to nano as default.
// Checks PROMQL_EDITOR, VISUAL, EDITOR in that order.
func getEditorCommand() string {
	for _, envVar := range []string{"PROMQL_EDITOR", "VISUAL", "EDITOR"} {
		if editor := strings.TrimSpace(os.Getenv(envVar)); editor != "" {
			return editor
		}
	}
	return "nano"
}

// isWordBoundaryRune checks if a rune is a word boundary using PromQL separators
func isWordBoundaryRune(r rune) bool {
	return strings.ContainsRune(PromQLSeparators, r)
}

// isEditWordBreak reports whether r ends a word for the word-wise editing keys
// (Ctrl-W, Alt-Backspace, Alt-B/F/D, Alt-U/L/C). It mirrors chzyer/readline's
// IsWordBreak so --repl=prompt edits words exactly like the default backend:
// only [A-Za-z0-9] are word characters, so "_", ":", ".", "[" and quotes stop.
// Completion keeps using isWordBoundaryRune, where metric names stay one word.
func isEditWordBreak(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return true
}

// shellQuote safely quotes a string for use in shell commands using POSIX single-quote escaping
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
