package repl

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	promparser "github.com/prometheus/prometheus/promql/parser"

	ai "github.com/jjo/promql-cli/pkg/ai"
	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// When true, the go-prompt completer will present an AI selection menu
var aiSelectionActive bool

// aiInProgress indicates an AI request is running asynchronously.
var aiInProgress bool

// aiCancelRequest, when non-nil, cancels an in-flight AI request (e.g., on Ctrl-C)
var aiCancelRequest func()

var (
	lastAISuggestions   []string
	lastAIExplanations  []string
	pendingAISuggestion string
	aiClipboard         string
)

// Returns true if the query was handled as an ad-hoc function, false otherwise
func handleAdhocAI(query string, storage *sstorage.SimpleStorage) bool {
	args := strings.TrimSpace(strings.TrimPrefix(query, ".ai"))
	if args == "" || args == "help" { // help
		fmt.Println("Usage: .ai <intent> | .ai ask <intent> | .ai show | .ai <N> | .ai run <N> | .ai edit <N>")
		fmt.Println("Examples:")
		fmt.Println("  .ai top 5 pods by http error rate over last hour")
		fmt.Println("  .ai 1        # run suggestion [1] if available")
		fmt.Println("  .ai show     # reprint last suggestions")
		return true
	}
	// Selection: .ai show
	if args == "show" {
		if len(lastAISuggestions) == 0 {
			fmt.Println("No AI suggestions yet. Try: .ai <intent>")
			return true
		}
		// Activate the inline AI selection workflow (same as post-ask)
		aiSelectionActive = true
		fmt.Println("AI suggestions (valid PromQL):")
		for i, s := range lastAISuggestions {
			fmt.Printf("  [%d] %s\n", i+1, s)
			if i < len(lastAIExplanations) {
				if ex := strings.TrimSpace(lastAIExplanations[i]); ex != "" {
					fmt.Printf("      - %s\n", ex)
				}
			}
		}
		fmt.Println("Choose with: .ai edit <N>  or  .ai run <N>  (1-based)")
		fmt.Println("Tips: use Tab to open the dropdown and pick an item.")
		return true
	}
	// Selection: .ai run N or .ai N
	if strings.HasPrefix(args, "run ") || regexp.MustCompile(`^\d+$`).MatchString(args) {
		var idxStr string
		if strings.HasPrefix(args, "run ") {
			idxStr = strings.TrimSpace(strings.TrimPrefix(args, "run "))
		} else {
			idxStr = args
		}
		n, err := strconv.Atoi(idxStr)
		if err != nil || n <= 0 {
			fmt.Println("Usage: .ai run <N>  (N is 1-based)")
			return true
		}
		if len(lastAISuggestions) == 0 {
			fmt.Println("No AI suggestions yet. Try: .ai <intent>")
			return true
		}
		if n > len(lastAISuggestions) {
			fmt.Printf("Only %d suggestions available\n", len(lastAISuggestions))
			return true
		}
		pendingAISuggestion = lastAISuggestions[n-1]
		fmt.Printf("Running suggestion [%d]: %s\n", n, pendingAISuggestion)
		return true
	}
	// Selection: .ai edit N
	if strings.HasPrefix(args, "edit ") {
		idxStr := strings.TrimSpace(strings.TrimPrefix(args, "edit "))
		n, err := strconv.Atoi(idxStr)
		if err != nil || n <= 0 {
			fmt.Println("Usage: .ai edit <N>  (N is 1-based)")
			return true
		}
		if len(lastAISuggestions) == 0 {
			fmt.Println("No AI suggestions yet. Try: .ai <intent>")
			return true
		}
		if n > len(lastAISuggestions) {
			fmt.Printf("Only %d suggestions available\n", len(lastAISuggestions))
			return true
		}
		aiClipboard = lastAISuggestions[n-1]
		fmt.Printf("Prepared suggestion [%d] for editing. Press Ctrl-Y to paste.\n", n)
		return true
	}
	// Guard: ".ai run" or ".ai edit" without index should not call AI, show usage instead
	if args == "run" || strings.HasPrefix(args, "run\t") || strings.HasSuffix(args, "run ") {
		fmt.Println("Usage: .ai run <N>  (N is 1-based)")
		return true
	}
	if args == "edit" || strings.HasPrefix(args, "edit\t") || strings.HasSuffix(args, "edit ") {
		fmt.Println("Usage: .ai edit <N>  (N is 1-based)")
		return true
	}
	// Support alias: .ai ask <intent>
	if strings.HasPrefix(args, "ask ") {
		args = strings.TrimSpace(strings.TrimPrefix(args, "ask "))
	}
	// Start AI request asynchronously so Ctrl-C can cancel it while the prompt remains responsive
	if aiInProgress || aiCancelRequest != nil {
		fmt.Println("AI request already in progress. Press Ctrl-C to cancel it.")
		return true
	}
	ctx, cancel := context.WithCancel(context.Background())
	aiCancelRequest = cancel
	aiInProgress = true
	fmt.Println("Asking AI... (press Ctrl-C to cancel)")
	go func(intent string) {
		defer func() {
			aiInProgress = false
			aiCancelRequest = nil
			cancel()
		}()
		suggestions, err := ai.AISuggestQueriesCtx(ctx, storage, intent)
		if err != nil {
			if errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "context canceled") || strings.Contains(strings.ToLower(err.Error()), "request canceled") {
				fmt.Println("AI request canceled")
				return
			}
			fmt.Printf("AI error: %v\n", err)
			return
		}
		var validQ []string
		var validE []string
		for _, sug := range suggestions {
			q := strings.TrimSpace(sug.Query)
			if q == "" {
				continue
			}
			q = ai.CleanCandidate(q)
			if q == "" {
				continue
			}
			if _, err := promparser.ParseExpr(q); err == nil {
				validQ = append(validQ, q)
				validE = append(validE, strings.TrimSpace(sug.Explain))
			}
		}
		if len(validQ) == 0 {
			fmt.Println("AI returned no valid PromQL suggestions.")
			return
		}
		lastAISuggestions = validQ
		lastAIExplanations = validE
		aiSelectionActive = true
		fmt.Println("AI suggestions (valid PromQL):")
		for i := range validQ {
			fmt.Printf("  [%d] %s\n", i+1, validQ[i])
			if ex := strings.TrimSpace(validE[i]); ex != "" {
				fmt.Printf("      - %s\n", ex)
			}
		}
		fmt.Println("Choose with: .ai edit <N>  or  .ai run <N>  (1-based)")
		fmt.Println("Tips: Alt-1..Alt-9 to paste a suggestion; Ctrl-Y to paste the first suggestion.")
	}(args)
	// Return immediately to keep the prompt interactive
	return true
}
