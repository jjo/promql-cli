//go:build prompt
// +build prompt

package main

import (
	"fmt"

	"github.com/prometheus/prometheus/promql"
)

// runInteractiveQueriesDispatch determines which REPL backend to use
func runInteractiveQueriesDispatch(engine *promql.Engine, storage *SimpleStorage, silent bool, replBackend string) {
	if replBackend == "readline" {
		if !silent {
			fmt.Println("Using readline backend (--repl=readline)")
		}
		runInteractiveQueries(engine, storage, silent)
		return
	}
	// Default to go-prompt
	if !silent {
		fmt.Println("Using go-prompt backend (default)")
	}
	runPromptREPL(engine, storage, silent)
}

// runPromptREPL runs the go-prompt based REPL
func runPromptREPL(engine *promql.Engine, storage *SimpleStorage, silent bool) {
	if !silent {
		fmt.Println("Enter PromQL queries (or 'quit' to exit):")
		fmt.Println("Supported queries:")
		fmt.Println("  - Basic selectors: metric_name, metric_name{label=\"value\"}")
		fmt.Println("  - Aggregations: sum(metric_name), avg(metric_name), count(metric_name), min(metric_name), max(metric_name)")
		fmt.Println("  - Group by: sum(metric_name) by (label)")
		fmt.Println("  - Binary operations: metric_name + 10, metric_name1 * metric_name2")
		fmt.Println("  - Functions: rate(metric_name), increase(metric_name), abs(metric_name)")
		fmt.Println("  - Comparisons: metric_name > 100, metric_name == 0")
		fmt.Println("  - Ad-hoc commands: .help, .labels <metric>, .metrics, .seed <metric> [steps=N] [step=1m], .at <time> <query>")
		fmt.Println()
	}

	// Set up the executeOne function pointer for prompt_repl.go
	executeOneFunc = func(s string) {
		executeOne(engine, storage, s)
	}

	// Set global storage for metric help text access
	globalStorage = storage

	// Set up the refresh function for adhoc.go to call after loading metrics
	refreshMetricsCache = func(s *SimpleStorage) {
		if s != nil {
			// Update the global storage reference
			globalStorage = s

			// Rebuild metrics list
			var metricNames []string
			for name := range s.metrics {
				metricNames = append(metricNames, name)
			}
			metrics = metricNames
			metricsHelp = s.metricsHelp

			// Clear the cached metrics in fetchMetrics to force re-fetch
			// This ensures the next completion request gets fresh data
			if !silent {
				fmt.Printf("[Autocompletion cache updated: %d metrics]\n", len(metrics))
			}
		}
	}

	// Initialize metrics from storage for completions
	if storage != nil {
		var metricNames []string
		for name := range storage.metrics {
			metricNames = append(metricNames, name)
		}
		metrics = metricNames
		metricsHelp = storage.metricsHelp
	}

	// Create and run the prompt REPL
	repl := createPromptREPL()
	if err := repl.Run(); err != nil {
		fmt.Printf("Error running prompt REPL: %v\n", err)
	}

	// Clean up when exiting
	refreshMetricsCache = nil
}
