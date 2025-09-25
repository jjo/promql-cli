//go:build prompt

package main

// getInMemoryHistory returns the current in-memory history used by the prompt backend.
func getInMemoryHistory() []string { return replHistory }
