// +build !prompt

package main

import (
	"fmt"
	"os"

	"github.com/prometheus/prometheus/promql"
)

// runInteractiveQueriesDispatch determines which REPL backend to use
func runInteractiveQueriesDispatch(engine *promql.Engine, storage *SimpleStorage, silent bool) {
	// Check if user wants to use go-prompt
	if os.Getenv("REPL_BACKEND") == "prompt" {
		fmt.Println("Error: go-prompt backend requested but not compiled in.")
		fmt.Println("To use go-prompt, build with: go build -tags prompt")
		os.Exit(1)
	}

	// Default to readline
	runInteractiveQueries(engine, storage, silent)
}