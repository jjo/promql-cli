//go:build !prompt
// +build !prompt

package repl

import (
	"fmt"
	"os"

	"github.com/prometheus/prometheus/promql"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// runInteractiveQueriesDispatch determines which REPL backend to use
func RunInteractiveQueriesDispatch(engine *promql.Engine, storage *sstorage.SimpleStorage, silent bool, replBackend string) {
	// This build does not include go-prompt. If prompt was requested, error out.
	if replBackend == "prompt" || replBackend == "" {
		fmt.Println("Error: --repl=prompt requested but not compiled in.")
		fmt.Println("To use go-prompt, build with: go build -tags prompt")
		os.Exit(1)
	}

	// Default to readline
	runInteractiveQueries(engine, storage, silent)
}
