package repl

import (
	"os"
	"strings"
)

// PromQL-aware word separators used for word boundary detection
const PromQLSeparators = "(){}[]\" \t\n,="

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

// shellQuote safely quotes a string for use in shell commands using POSIX single-quote escaping
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
