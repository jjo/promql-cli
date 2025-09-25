//go:build !prompt

package main

// runAIPicker is not available without the go-prompt backend; return false.
func runAIPicker(validQ []string, validE []string) bool { return false }
