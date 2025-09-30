//go:build noprompt
// +build noprompt

package repl

import (
	"fmt"
	"os"

	"github.com/prometheus/prometheus/promql"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// runInteractiveQueriesDispatch determines which REPL backend to use
func RunInteractiveQueriesDispatch(engine *promql.Engine, storage *sstorage.SimpleStorage, silent bool, replBackend string) {
	// This build excludes go-prompt (built with -tags noprompt). Use readline.
	if !silent {
		fmt.Println("Using readline backend (built without go-prompt)")
	}
	SetEvalEngine(engine)
	runInteractiveQueries(engine, storage, silent)
}
