package repl

import "strings"

// BuildFilteredHistory builds a newest-first filtered history slice based on the given prefix.
// - Preserves duplicates for 1:1 navigation
// - Returns entries from most recent to oldest
func BuildFilteredHistory(prefix string, history []string) []string {
	out := make([]string, 0, len(history))
	for i := len(history) - 1; i >= 0; i-- {
		entry := history[i]
		if prefix == "" || strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return out
}
