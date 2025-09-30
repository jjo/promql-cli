//go:build noprompt

package repl

// getInMemoryHistory returns nil when the prompt backend is not built.
func getInMemoryHistory() []string { return nil }
