package repl

import (
	"fmt"

	"github.com/prometheus/prometheus/promql"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// RunInteractiveQueriesDispatch determines which REPL backend to use
func RunInteractiveQueriesDispatch(engine *promql.Engine, storage *sstorage.SimpleStorage, silent bool, replBackend string) {
	// Always set the eval engine for rule evaluations regardless of backend
	SetEvalEngine(engine)

	if replBackend == "prompt" {
		if !silent {
			fmt.Println("Using go-prompt backend (--repl=prompt)")
		}
		runPromptREPL(engine, storage, silent)
		return
	}
	// Default to readline
	if !silent {
		fmt.Println("Using readline backend (default)")
	}
	runInteractiveQueries(engine, storage, silent)
}

// runPromptREPL runs the go-prompt based REPL
func runPromptREPL(engine *promql.Engine, storage *sstorage.SimpleStorage, silent bool) {
	if !silent {
		fmt.Println("Enter PromQL queries (or 'quit' to exit):")
		fmt.Println()
	}

	// Set up the executeOne function pointer for prompt_repl.go
	executeOneFunc = func(s string) {
		executeOne(engine, storage, s)
	}

	// Set global storage for metric help text access
	globalStorage = storage

	// Set up the refresh function for adhoc.go to call after loading metrics
	refreshMetricsCache = func(s *sstorage.SimpleStorage) {
		if s != nil {
			// Update the global storage reference
			globalStorage = s

			// Rebuild metrics list (de-duplicated) and track recording rule names
			seen := make(map[string]bool)
			var metricNames []string
			for name := range s.Metrics {
				if !seen[name] {
					metricNames = append(metricNames, name)
					seen[name] = true
				}
			}
			metrics = metricNames
			// Add recording rule names for completion even if not present yet (without duplicates)
			recordingRuleSet = make(map[string]bool)
			for _, rn := range GetRecordingRuleNames() {
				recordingRuleSet[rn] = true
				if !seen[rn] {
					metrics = append(metrics, rn)
					seen[rn] = true
				}
			}
			// Add alert names for completion (without duplicates)
			for _, ar := range GetAlertingRules() {
				if !seen[ar.Name] {
					metrics = append(metrics, ar.Name)
					seen[ar.Name] = true
				}
			}
			metricsHelp = s.MetricsHelp

			// Clear the cached metrics in fetchMetrics to force re-fetch
			// This ensures the next completion request gets fresh data
			if !silent {
				fmt.Printf("[Autocompletion cache updated: %d metrics]\n", len(metrics))
			}
		}
	}

	// Initialize metrics from storage for completions
	if storage != nil {
		seen := make(map[string]bool)
		var metricNames []string
		for name := range storage.Metrics {
			if !seen[name] {
				metricNames = append(metricNames, name)
				seen[name] = true
			}
		}
		metrics = metricNames
		metricsHelp = storage.MetricsHelp
		// Seed recording rule set and append rule names (without duplicates)
		recordingRuleSet = make(map[string]bool)
		for _, rn := range GetRecordingRuleNames() {
			recordingRuleSet[rn] = true
			if !seen[rn] {
				metrics = append(metrics, rn)
				seen[rn] = true
			}
		}
		// Add alert names for completion (without duplicates)
		for _, ar := range GetAlertingRules() {
			if !seen[ar.Name] {
				metrics = append(metrics, ar.Name)
				seen[ar.Name] = true
			}
		}
	}

	// Create and run the prompt REPL
	repl := createPromptREPL()
	if err := repl.Run(); err != nil {
		fmt.Printf("Error running prompt REPL: %v\n", err)
	}

	// Clean up when exiting
	refreshMetricsCache = nil
}
