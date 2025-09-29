//go:build prompt

package repl

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/c-bata/go-prompt"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/prometheus/promql/parser"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

const suggestionLimit = 100

// Global variables needed for prompt completions
var (
	client      v1.API
	ctx         = context.Background()
	metrics     []string
	metricsHelp map[string]string // metric name -> help text
	// recordingRuleSet marks names that come from recording rules so we can label them
	recordingRuleSet  map[string]bool
	replHistory       []string
	executeOneFunc    func(string) // Function pointer to executeOne
	globalStorage     interface{}  // Storage for accessing metrics metadata
	inGoPromptSession bool         // true while running go-prompt REPL
	dropdownActive    bool         // true if user opened completion with Tab
)

// promptCompleter provides completions for go-prompt
func promptCompleter(d prompt.Document) []prompt.Suggest {
	text := d.TextBeforeCursor()
	trimmedText := strings.TrimSpace(text)
	emptySuggestions := []prompt.Suggest{}

	// Reset history navigation if the text has changed from what's in filtered history
	resetHistoryNavigationIfNeeded(d.Text, historyPrefix)

	// If we are in AI selection mode, present the AI suggestions menu unless user is typing a .ai command
	if aiSelectionActive && !strings.HasPrefix(strings.TrimSpace(text), ".ai") {
		return getAISuggestionMenu()
	}

	// Check if we should suppress completions based on context
	if shouldSuppressCompletions(text, trimmedText) {
		return emptySuggestions
	}

	// Handle eager completion mode - show suggestions at start
	eagerCompletion := os.Getenv("PROMQL_CLI_EAGER_COMPLETION") == "true"
	if eagerCompletion && text == "" {
		return getMixedSuggests("")
	}

	// Use go-prompt's word detection with our custom separators
	wordBefore := d.GetWordBeforeCursorUntilSeparator("(){}[]\" \t\n,=")

	// Check if we're in ANY ad-hoc command context first
	// This prevents range duration suggestions from appearing in ad-hoc commands
	isAdHocCommand := isInAdHocCommandContext(trimmedText)

	// Check if we're typing an ad-hoc command itself
	if strings.HasPrefix(wordBefore, ".") && !strings.Contains(text, " ") {
		// Special-case .ai to require a subcommand even before the space
		if strings.HasPrefix(wordBefore, ".ai") {
			return []prompt.Suggest{
				{Text: ".ai ask ", Description: "ask free text to ask the AI"},
				{Text: ".ai edit ", Description: "prepare a suggestion for editing (Ctrl-Y to paste)"},
				{Text: ".ai run ", Description: "run a suggestion number"},
				{Text: ".ai show", Description: "show last AI suggestions"},
			}
		}
		return getAdHocCommandSuggests(wordBefore)
	}

	// If we're in an ad-hoc command context, handle it specifically
	if isAdHocCommand {
		// Handle .ai completions (subcommands and indices)
		if strings.HasPrefix(trimmedText, ".ai") {
			// Find the position after ".ai" token in the current line
			pos := strings.Index(text, ".ai")
			if pos == -1 {
				return emptySuggestions
			}
			after := strings.TrimSpace(text[pos+3:])
			// If no argument yet, suggest subcommands (no prefix)
			if after == "" {
				return []prompt.Suggest{
					{Text: "ask ", Description: "ask free text to ask the AI"},
					{Text: "edit ", Description: "prepare a suggestion for editing (Ctrl-Y to paste)"},
					{Text: "run ", Description: "run a suggestion number"},
					{Text: "show", Description: "show last AI suggestions"},
				}
			}
			// If typing the subcommand token, provide filtered suggestions
			low := strings.ToLower(after)
			subs := []prompt.Suggest{
				{Text: "ask ", Description: "ask free text to ask the AI"},
				{Text: "edit ", Description: "prepare a suggestion for editing (Ctrl-Y to paste)"},
				{Text: "run ", Description: "run a suggestion number"},
				{Text: "show", Description: "show last AI suggestions"},
			}
			// Special handling when a subcommand is already chosen
			if strings.HasPrefix(low, "run ") || strings.HasPrefix(low, "edit ") {
				// Determine the number prefix after the subcommand
				rest := ""
				if strings.HasPrefix(after, "run ") {
					rest = strings.TrimSpace(after[len("run "):])
				} else if strings.HasPrefix(after, "edit ") {
					rest = strings.TrimSpace(after[len("edit "):])
				}
				prefixNum := rest // may be empty; show all indices if empty
				var sugg []prompt.Suggest
				max := len(lastAISuggestions)
				if max > 20 {
					max = 20
				}
				for i := 1; i <= max; i++ {
					num := fmt.Sprintf("%d", i)
					if prefixNum == "" || strings.HasPrefix(num, prefixNum) {
						desc := "suggestion"
						if i-1 < len(lastAISuggestions) {
							s := strings.TrimSpace(lastAISuggestions[i-1])
							if len(s) > 0 {
								if len(s) > suggestionLimit {
									desc = s[:suggestionLimit] + "..."
								} else {
									desc = s
								}
							}
						}
						sugg = append(sugg, prompt.Suggest{Text: num, Description: desc})
					}
				}
				return sugg
			}
			// If typing partial token (e.g., "ru", "ed", "sh", "as")
			var filtered []prompt.Suggest
			for _, s := range subs {
				if strings.HasPrefix(s.Text, after) || strings.HasPrefix(strings.ToLower(s.Text), low) {
					filtered = append(filtered, s)
				}
			}
			return filtered
		}

		// Handle .scrape, .prom_scrape and .prom_scrape_range URL completions FIRST
		if strings.HasPrefix(trimmedText, ".scrape") || strings.HasPrefix(trimmedText, ".prom_scrape") || strings.HasPrefix(trimmedText, ".prom_scrape_range") {
			if strings.Contains(text, ".scrape ") {
				cmdEnd := strings.Index(text, ".scrape ") + 8
				afterCmd := strings.TrimSpace(text[cmdEnd:])

				// Only show URL examples if we haven't typed a space after the URL yet
				if !strings.Contains(afterCmd, " ") {
					return getScrapeURLCompletions(afterCmd)
				}
			}
			// After the URL, no more completions (regex, count, delay are too specific)
			return emptySuggestions
		}

		// Check if we're in a special ad-hoc command context for metric completion
		if strings.Contains(text, ".labels ") || strings.Contains(text, ".timestamps ") ||
			strings.Contains(text, ".drop ") || strings.Contains(text, ".seed ") {
			// We're after .labels/.timestamps/.drop/.seed, show metric completions
			if lastSpace := strings.LastIndex(text, " "); lastSpace != -1 {
				wordAfterSpace := text[lastSpace+1:]
				return getMetricSuggests(wordAfterSpace)
			}
			return emptySuggestions
		}

		// Check if we're after .load or .save for file completions
		if strings.Contains(text, ".load ") || strings.Contains(text, ".save ") {
			if lastSpace := strings.LastIndex(text, " "); lastSpace != -1 {
				pathPrefix := text[lastSpace+1:]
				return getFileCompletions(pathPrefix)
			}
			return emptySuggestions
		}

		// Handle .at and .pinat time completions
		if strings.HasPrefix(trimmedText, ".at") || strings.HasPrefix(trimmedText, ".pinat") {
			if strings.Contains(text, ".at ") || strings.Contains(text, ".pinat ") {
				var cmdEnd int
				if strings.Contains(text, ".at ") {
					cmdEnd = strings.Index(text, ".at ") + 4
				} else {
					cmdEnd = strings.Index(text, ".pinat ") + 7
				}

				afterCmd := text[cmdEnd:]
				// Check if we already have a time token and a space (ready for query in .at case)
				if strings.HasPrefix(trimmedText, ".at ") {
					if spaceIdx := strings.Index(strings.TrimSpace(afterCmd), " "); spaceIdx != -1 {
						// We're after the time, now show PromQL completions
						return getMixedSuggests(wordBefore)
					}
				}

				// Show time completions
				return getTimeCompletions(strings.TrimSpace(afterCmd))
			}
			return emptySuggestions
		}

		// No further completions for .help, .metrics, .quit
		if trimmedText == ".help" || trimmedText == ".metrics" || trimmedText == ".quit" ||
			strings.HasPrefix(trimmedText, ".help ") || strings.HasPrefix(trimmedText, ".metrics ") ||
			strings.HasPrefix(trimmedText, ".quit ") {
			return emptySuggestions
		}

		// For any other ad-hoc command we don't have specific handling for
		return emptySuggestions
	}

	// Use the PromQL parser to understand context better
	context := analyzePromQLContext(text)

	// Based on parser context, return appropriate suggestions
	switch context.Type {
	case "range_duration":
		return getRangeDurationSuggests(wordBefore, context.MetricName)
	case "label_name":
		return getLabelNameSuggests(wordBefore, context.MetricName)
	case "label_value":
		return getLabelValueSuggests(wordBefore, context.MetricName, context.LabelName)
	case "function_arg":
		// Inside function args, offer both functions and metrics (functions like rate() are common)
		return getMixedSuggests(wordBefore)
	case "after_operator":
		// After operator, show metrics
		return getMetricSuggests(wordBefore)
	case "aggregation":
		return getAggregationSuggests(wordBefore)
	default:
		// Default to mixed completions
		return getMixedSuggests(wordBefore)
	}
}

// getAdHocCommandSuggests returns completions for ad-hoc commands
func getAdHocCommandSuggests(prefix string) []prompt.Suggest {
	// Build suggestions from centralized command list
	var s []prompt.Suggest
	for _, cmd := range AdHocCommands {
		s = append(s, prompt.Suggest{
			Text:        cmd.Command,
			Description: cmd.Description,
		})
	}

	// Filter based on prefix
	filtered := []prompt.Suggest{}
	for _, cmd := range s {
		if strings.HasPrefix(cmd.Text, prefix) {
			filtered = append(filtered, cmd)
		}
	}

	// Always return at least empty slice, never nil
	if filtered == nil {
		return []prompt.Suggest{}
	}
	return filtered
}

// getScrapeURLCompletions returns URL examples for .scrape command
func getScrapeURLCompletions(prefix string) []prompt.Suggest {
	// Common scraping endpoints
	urlOptions := []prompt.Suggest{
		{Text: "http://localhost:9090/metrics", Description: "Prometheus metrics"},
		{Text: "http://localhost:9100/metrics", Description: "Node Exporter metrics"},
		{Text: "http://localhost:8080/metrics", Description: "Application metrics"},
		{Text: "http://localhost:3000/metrics", Description: "Grafana metrics"},
		{Text: "http://localhost:9093/metrics", Description: "Alertmanager metrics"},
		{Text: "http://localhost:9091/metrics", Description: "Pushgateway metrics"},
		{Text: "http://localhost:9115/metrics", Description: "Blackbox Exporter metrics"},
		{Text: "http://localhost:2112/metrics", Description: "Common exporter port"},
	}

	// Filter based on prefix
	if prefix == "" {
		return urlOptions
	}

	filtered := []prompt.Suggest{}
	for _, opt := range urlOptions {
		if strings.HasPrefix(opt.Text, prefix) {
			filtered = append(filtered, opt)
		}
	}

	// Always return at least empty slice, never nil
	if filtered == nil {
		return []prompt.Suggest{}
	}
	return filtered
}

// getTimeCompletions returns time format completions for .at and .pinat commands
func getTimeCompletions(prefix string) []prompt.Suggest {
	// Common time formats and examples
	timeOptions := []prompt.Suggest{
		{Text: "now", Description: "current time"},
		{Text: "now-5m", Description: "5 minutes ago"},
		{Text: "now-15m", Description: "15 minutes ago"},
		{Text: "now-30m", Description: "30 minutes ago"},
		{Text: "now-1h", Description: "1 hour ago"},
		{Text: "now-2h", Description: "2 hours ago"},
		{Text: "now-6h", Description: "6 hours ago"},
		{Text: "now-12h", Description: "12 hours ago"},
		{Text: "now-24h", Description: "1 day ago"},
		{Text: "now-7d", Description: "1 week ago"},
		{Text: "now+5m", Description: "5 minutes from now"},
		{Text: "now+1h", Description: "1 hour from now"},
	}

	// Add current timestamp in RFC3339 format as an example
	now := time.Now().UTC()
	timeOptions = append(timeOptions, prompt.Suggest{
		Text:        now.Format(time.RFC3339),
		Description: "RFC3339 format example",
	})

	// Add unix timestamp examples
	unixSec := now.Unix()
	unixMs := now.UnixMilli()
	timeOptions = append(timeOptions,
		prompt.Suggest{Text: fmt.Sprintf("%d", unixSec), Description: "unix seconds"},
		prompt.Suggest{Text: fmt.Sprintf("%d", unixMs), Description: "unix milliseconds"},
	)

	// For .pinat specifically, add "remove" option
	// We detect this by checking the caller context (a bit hacky but works)
	// Since we're called from the completer, we can't directly know if it's .pinat
	// but "remove" is only valid for .pinat, so we always add it and it won't hurt .at
	timeOptions = append(timeOptions, prompt.Suggest{
		Text:        "remove",
		Description: "remove pinned time (pinat only)",
	})

	// Filter based on prefix
	if prefix == "" {
		return timeOptions
	}

	filtered := []prompt.Suggest{}
	for _, opt := range timeOptions {
		if strings.HasPrefix(strings.ToLower(opt.Text), strings.ToLower(prefix)) {
			filtered = append(filtered, opt)
		}
	}

	// Always return at least empty slice, never nil
	if filtered == nil {
		return []prompt.Suggest{}
	}
	return filtered
}

// getFileCompletions returns file/directory completions
func getFileCompletions(prefix string) []prompt.Suggest {
	// Handle empty prefix or just "./"
	if prefix == "" {
		prefix = "."
	}

	dir := filepath.Dir(prefix)
	if dir == "" {
		dir = "."
	}

	base := filepath.Base(prefix)
	if prefix == "." || prefix == "./" {
		base = ""
	}

	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return []prompt.Suggest{}
	}

	var suggestions []prompt.Suggest
	for _, f := range files {
		name := f.Name()
		// Skip hidden files unless explicitly searching for them
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		if base == "" || strings.HasPrefix(name, base) {
			path := filepath.Join(dir, name)
			if f.IsDir() {
				suggestions = append(suggestions, prompt.Suggest{
					Text:        path + "/",
					Description: "directory",
				})
			} else {
				// Show file extension as description
				ext := filepath.Ext(name)
				desc := "file"
				if ext != "" {
					desc = ext[1:] + " file"
				}
				suggestions = append(suggestions, prompt.Suggest{
					Text:        path,
					Description: desc,
				})
			}
		}
	}

	// Sort suggestions with directories first
	sort.Slice(suggestions, func(i, j int) bool {
		iIsDir := strings.HasSuffix(suggestions[i].Text, "/")
		jIsDir := strings.HasSuffix(suggestions[j].Text, "/")
		if iIsDir != jIsDir {
			return iIsDir
		}
		return suggestions[i].Text < suggestions[j].Text
	})

	return suggestions
}

// getFunctionSuggests returns PromQL function completions based on prefix
func getFunctionSuggests(prefix string) []prompt.Suggest {
	functions := []prompt.Suggest{
		{Text: "abs(", Description: "absolute value"},
		{Text: "absent(", Description: "check if metrics are absent"},
		{Text: "absent_over_time(", Description: "check if absent over time range"},
		{Text: "avg(", Description: "average value"},
		{Text: "avg_over_time(", Description: "average over time range"},
		{Text: "ceil(", Description: "round up to nearest integer"},
		{Text: "changes(", Description: "number of value changes"},
		{Text: "clamp(", Description: "clamp values to range"},
		{Text: "clamp_max(", Description: "clamp to maximum value"},
		{Text: "clamp_min(", Description: "clamp to minimum value"},
		{Text: "count(", Description: "count number of series"},
		{Text: "count_over_time(", Description: "count samples over time"},
		{Text: "day_of_month(", Description: "day of the month"},
		{Text: "day_of_week(", Description: "day of the week"},
		{Text: "days_in_month(", Description: "number of days in month"},
		{Text: "delta(", Description: "difference between first and last value"},
		{Text: "deriv(", Description: "derivative using linear regression"},
		{Text: "exp(", Description: "exponential function"},
		{Text: "floor(", Description: "round down to nearest integer"},
		{Text: "histogram_quantile(", Description: "calculate quantile from histogram"},
		{Text: "holt_winters(", Description: "Holt-Winters double exponential smoothing"},
		{Text: "hour(", Description: "hour of the day"},
		{Text: "idelta(", Description: "instant delta"},
		{Text: "increase(", Description: "increase in value over time range"},
		{Text: "irate(", Description: "instant rate of increase"},
		{Text: "label_join(", Description: "join label values"},
		{Text: "label_replace(", Description: "replace label values"},
		{Text: "ln(", Description: "natural logarithm"},
		{Text: "log10(", Description: "base-10 logarithm"},
		{Text: "log2(", Description: "base-2 logarithm"},
		{Text: "max(", Description: "maximum value"},
		{Text: "max_over_time(", Description: "maximum over time range"},
		{Text: "min(", Description: "minimum value"},
		{Text: "min_over_time(", Description: "minimum over time range"},
		{Text: "minute(", Description: "minute of the hour"},
		{Text: "month(", Description: "month of the year"},
		{Text: "predict_linear(", Description: "predict value using linear regression"},
		{Text: "quantile(", Description: "calculate quantile"},
		{Text: "quantile_over_time(", Description: "quantile over time range"},
		{Text: "rate(", Description: "per-second rate of increase"},
		{Text: "resets(", Description: "number of counter resets"},
		{Text: "round(", Description: "round to nearest integer"},
		{Text: "scalar(", Description: "convert single-element vector to scalar"},
		{Text: "sgn(", Description: "sign of value (-1, 0, or 1)"},
		{Text: "sort(", Description: "sort values in ascending order"},
		{Text: "sort_desc(", Description: "sort values in descending order"},
		{Text: "sqrt(", Description: "square root"},
		{Text: "stddev(", Description: "standard deviation"},
		{Text: "stddev_over_time(", Description: "standard deviation over time"},
		{Text: "stdvar(", Description: "standard variance"},
		{Text: "stdvar_over_time(", Description: "standard variance over time"},
		{Text: "sum(", Description: "sum of values"},
		{Text: "sum_over_time(", Description: "sum over time range"},
		{Text: "time(", Description: "current evaluation timestamp"},
		{Text: "timestamp(", Description: "timestamp of each sample"},
		{Text: "topk(", Description: "top k elements"},
		{Text: "vector(", Description: "create vector from scalar"},
		{Text: "year(", Description: "year"},
	}

	// Filter based on the prefix
	filtered := []prompt.Suggest{}
	for _, f := range functions {
		if strings.HasPrefix(f.Text, prefix) {
			filtered = append(filtered, f)
		}
	}

	return filtered
}

// getMetricSuggests returns metric suggestions based on prefix
func getMetricSuggests(prefix string) []prompt.Suggest {
	if metrics == nil || len(metrics) == 0 {
		// Try to fetch metrics if not already loaded
		fetchMetrics()
	}

	var suggestions []prompt.Suggest
	count := 0

	// Sort metrics to ensure consistent ordering
	sortedMetrics := make([]string, len(metrics))
	copy(sortedMetrics, metrics)
	sort.Strings(sortedMetrics)

	for _, m := range sortedMetrics {
		if count >= 100 { // Limit suggestions
			break
		}
		if prefix == "" || strings.HasPrefix(m, prefix) || strings.HasPrefix(strings.ToLower(m), strings.ToLower(prefix)) {
			// Get help text if available - prioritize showing the metric's documentation
			description := ""
			if metricsHelp != nil && metricsHelp[m] != "" {
				description = metricsHelp[m]
			} else {
				// Check for related metrics with help (e.g., base metric for _total, _bucket, _count)
				baseName := m
				for _, suffix := range []string{"_total", "_bucket", "_count", "_sum"} {
					if strings.HasSuffix(m, suffix) {
						baseName = strings.TrimSuffix(m, suffix)
						if help, ok := metricsHelp[baseName]; ok && help != "" {
							description = help
							break
						}
					}
				}
				if description == "" {
					// Default label depends on whether this name is a recording rule
					if recordingRuleSet != nil && recordingRuleSet[m] {
						description = "(rule)"
					} else {
						description = "(metric)"
					}
				}
			}

			// Ensure description fits nicely in the completion display
			if len(description) > 100 {
				description = description[:(suggestionLimit-3)] + "..."
			}

			suggestions = append(suggestions, prompt.Suggest{
				Text:        m,
				Description: description,
			})
			count++
		}
	}

	return suggestions
}

// getRangeDurationSuggests returns range duration completions
func getRangeDurationSuggests(prefix string, metricName string) []prompt.Suggest {
	// Common duration suggestions
	durations := []prompt.Suggest{
		{Text: "5s]", Description: "5 seconds"},
		{Text: "10s]", Description: "10 seconds"},
		{Text: "30s]", Description: "30 seconds"},
		{Text: "1m]", Description: "1 minute"},
		{Text: "5m]", Description: "5 minutes"},
		{Text: "10m]", Description: "10 minutes"},
		{Text: "15m]", Description: "15 minutes"},
		{Text: "30m]", Description: "30 minutes"},
		{Text: "1h]", Description: "1 hour"},
		{Text: "2h]", Description: "2 hours"},
		{Text: "6h]", Description: "6 hours"},
		{Text: "12h]", Description: "12 hours"},
		{Text: "1d]", Description: "1 day"},
		{Text: "7d]", Description: "7 days"},
	}

	// Filter based on what's already typed
	filtered := []prompt.Suggest{}
	for _, d := range durations {
		if strings.HasPrefix(d.Text, prefix) {
			filtered = append(filtered, d)
		}
	}

	return filtered
}

// Global variable to store the original terminal state for restoration
var globalOriginalState string

// Global variable to track the last executed command for Alt+. functionality
var lastExecutedCommand string

// Global variables for prefix-based history navigation
var (
	historyIndex    int      // Current position in filtered history
	historyPrefix   string   // Prefix to filter history by (captured at activation)
	filteredHistory []string // History entries matching the prefix

	// State for active prefix-search navigation on arrows
	historyActive   bool   // true while navigating with Up/Down
	historySeed     string // full line captured at activation (to restore on Down at end)
	historyLastLine string // last line inserted by history navigation
)

// Global variables for multi-line editing
var (
	multiLineBuffer []string // Accumulates lines for multi-line input
	inMultiLine     bool     // Whether we're in multi-line mode
)

// promptExecutor handles command execution
func promptExecutor(s string) {
	// If AI selection was active, clear it upon any command submission
	aiSelectionActive = false
	// Any command submission exits dropdown mode
	dropdownActive = false
	// Reset history navigation state when a command is executed
	historyIndex = 0
	historyPrefix = ""
	filteredHistory = nil

	// Handle multi-line input (commands with embedded newlines)
	if strings.Contains(s, "\n") {
		// Process multi-line PromQL query
		// Replace newlines with spaces for PromQL parsing
		s = strings.ReplaceAll(s, "\n", " ")
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
	} else {
		s = strings.TrimSpace(s)
		if s == "" && !inMultiLine {
			return
		}

		// Check for line continuation (backslash at end)
		if strings.HasSuffix(s, "\\") && !strings.HasSuffix(s, "\\\\") {
			// Remove the backslash and store the line
			s = strings.TrimSuffix(s, "\\")
			multiLineBuffer = append(multiLineBuffer, s)
			inMultiLine = true
			// The prompt will show again for the next line
			return
		}

		// If we're in multi-line mode from backslash continuation, combine all lines
		if inMultiLine {
			multiLineBuffer = append(multiLineBuffer, s)
			s = strings.Join(multiLineBuffer, " ")
			multiLineBuffer = nil
			inMultiLine = false
			s = strings.TrimSpace(s)
			if s == "" {
				return
			}
		}
	}

	// Handle quit (but not .exit which isn't implemented)
	if s == "quit" || s == ".quit" {
		// Save history before exiting
		saveHistory()
		fmt.Println("\nExiting...")
		// Restore terminal state before exiting
		if globalOriginalState != "" {
			restoreTerminalState(globalOriginalState)
		}
		os.Exit(0)
	}

	// Add to history
	if s != "" && !strings.HasPrefix(s, " ") { // Don't save empty or space-prefixed commands
		// Avoid adding consecutive duplicates
		if len(replHistory) == 0 || replHistory[len(replHistory)-1] != s {
			replHistory = append(replHistory, s)
			// Save to file immediately for persistence
			appendToHistoryFile(s)
		}
		// Track for Alt+. functionality
		lastExecutedCommand = s
	}

	// Execute the command - executeOne is defined elsewhere and will be called via function pointer
	if executeOneFunc != nil {
		executeOneFunc(s)
	}
}

// createPromptREPL creates a go-prompt based REPL
func createPromptREPL() *promptREPL {
	return &promptREPL{}
}

// getAISuggestionMenu builds the dropdown items for AI selection (edit first, then run)
func getAISuggestionMenu() []prompt.Suggest {
	var items []prompt.Suggest
	if len(lastAISuggestions) == 0 {
		return items
	}
	// First: edit
	for i, q := range lastAISuggestions {
		preview := strings.TrimSpace(q)
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		if i < len(lastAIExplanations) && strings.TrimSpace(lastAIExplanations[i]) != "" {
			ex := strings.TrimSpace(lastAIExplanations[i])
			if len(ex) > 80 {
				ex = ex[:80] + "..."
			}
			preview = preview + " — " + ex
		}
		items = append(items, prompt.Suggest{Text: fmt.Sprintf(".ai edit %d", i+1), Description: preview})
	}
	// Then: run
	for i, q := range lastAISuggestions {
		preview := strings.TrimSpace(q)
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		if i < len(lastAIExplanations) && strings.TrimSpace(lastAIExplanations[i]) != "" {
			ex := strings.TrimSpace(lastAIExplanations[i])
			if len(ex) > 80 {
				ex = ex[:80] + "..."
			}
			preview = preview + " — " + ex
		}
		items = append(items, prompt.Suggest{Text: fmt.Sprintf(".ai run %d", i+1), Description: preview})
	}
	return items
}

type promptREPL struct {
	prompt *prompt.Prompt
}

func pasteAISuggestionN(buf *prompt.Buffer, n int) {
	if n <= 0 || n > len(lastAISuggestions) {
		return
	}
	s := lastAISuggestions[n-1]
	if s == "" {
		return
	}
	// Append helper text if available
	if n-1 < len(lastAIExplanations) {
		if ex := strings.TrimSpace(lastAIExplanations[n-1]); ex != "" {
			s = s + " # " + ex
		}
	}
	// Replace entire line with the chosen suggestion
	doc := buf.Document()
	buf.CursorLeft(len([]rune(doc.TextBeforeCursor())))
	buf.Delete(len(doc.Text))
	buf.InsertText(s, false, true)
}

// runAIPicker opens a temporary go-prompt to pick an AI suggestion.
// Returns true if a selection was made (either run or edit), false if canceled.
func runAIPicker(validQ []string, validE []string) bool {
	if len(validQ) == 0 {
		return false
	}
	if !inGoPromptSession {
		return false
	}

	// Build suggestion list: "run N" and "edit N" with query preview
	var items []prompt.Suggest
	// First, all edit options
	for i, q := range validQ {
		preview := strings.TrimSpace(q)
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		if i < len(validE) && strings.TrimSpace(validE[i]) != "" {
			ex := strings.TrimSpace(validE[i])
			if len(ex) > 80 {
				ex = ex[:80] + "..."
			}
			preview = preview + " — " + ex
		}
		items = append(items,
			prompt.Suggest{Text: fmt.Sprintf("edit %d", i+1), Description: preview},
		)
	}
	// Then, all run options
	for i, q := range validQ {
		preview := strings.TrimSpace(q)
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		if i < len(validE) && strings.TrimSpace(validE[i]) != "" {
			ex := strings.TrimSpace(validE[i])
			if len(ex) > 80 {
				ex = ex[:80] + "..."
			}
			preview = preview + " — " + ex
		}
		items = append(items,
			prompt.Suggest{Text: fmt.Sprintf("run %d", i+1), Description: preview},
		)
	}

	selected := false
	pickerExec := func(in string) {
		line := strings.TrimSpace(in)
		if line == "" || line == "q" || line == "quit" || line == "exit" {
			return
		}
		// Accept just a number => run N
		if n, err := strconv.Atoi(line); err == nil && n > 0 && n <= len(validQ) {
			pendingAISuggestion = validQ[n-1]
			fmt.Printf("Running suggestion [%d]: %s\n", n, pendingAISuggestion)
			selected = true
			return
		}
		// Accept explicit run/edit forms
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "run ") {
			idxStr := strings.TrimSpace(strings.TrimPrefix(lower, "run "))
			if n, err := strconv.Atoi(idxStr); err == nil && n > 0 && n <= len(validQ) {
				pendingAISuggestion = validQ[n-1]
				fmt.Printf("Running suggestion [%d]: %s\n", n, pendingAISuggestion)
				selected = true
			}
			return
		}
		if strings.HasPrefix(lower, "edit ") {
			idxStr := strings.TrimSpace(strings.TrimPrefix(lower, "edit "))
			if n, err := strconv.Atoi(idxStr); err == nil && n > 0 && n <= len(validQ) {
				aiClipboard = validQ[n-1]
				fmt.Printf("Prepared suggestion [%d] for editing. Press Ctrl-Y to paste.\n", n)
				selected = true
			}
			return
		}
	}
	pickerComp := func(d prompt.Document) []prompt.Suggest {
		w := strings.ToLower(strings.TrimSpace(d.GetWordBeforeCursor()))
		if w == "" {
			return items
		}
		var out []prompt.Suggest
		for _, it := range items {
			if strings.HasPrefix(strings.ToLower(it.Text), w) {
				out = append(out, it)
			}
		}
		return out
	}

	fmt.Println("Use number to run (e.g., 1), or \"run N\" / \"edit N\". Enter with empty input to cancel.")
	p := prompt.New(
		pickerExec,
		pickerComp,
		prompt.OptionPrefix("AI> "),
		prompt.OptionTitle("AI Suggestions"),
		prompt.OptionMaxSuggestion(uint16(len(items))),
		prompt.OptionShowCompletionAtStart(),
		prompt.OptionSelectedSuggestionBGColor(prompt.LightGray),
		prompt.OptionSuggestionBGColor(prompt.DarkGray),
		prompt.OptionDescriptionBGColor(prompt.DarkGray),
		prompt.OptionDescriptionTextColor(prompt.White),
	)
	p.Run()
	return selected
}

func (r *promptREPL) Run() error {
	// Load history from file
	loadHistory()

	// Save terminal state before starting
	originalState := saveTerminalState()
	globalOriginalState = originalState // Store globally for .quit handler

	// Set up signal handling to restore terminal on interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		for {
			<-sigChan
			// If an AI request is in-flight, cancel it instead of exiting
			if aiCancelRequest != nil {
				aiCancelRequest()
				continue
			}
			// Otherwise exit cleanly
			saveHistory()
			fmt.Println("\nInterrupted. Exiting...")
			restoreTerminalState(originalState)
			if r.prompt != nil {
				os.Exit(0)
			}
		}
	}()

	// Check if eager completion is enabled
	eagerCompletion := os.Getenv("PROMQL_CLI_EAGER_COMPLETION") == "true"

	// Create the prompt with proper options
	inGoPromptSession = true
	opts := []prompt.Option{
		prompt.OptionPrefix("PromQL> "),
		prompt.OptionTitle("PromQL CLI"),
		// We implement our own prefix-based history on arrow keys
		prompt.OptionPrefixTextColor(prompt.Blue),
		// Use a live prefix that updates based on state
		prompt.OptionLivePrefix(func() (string, bool) {
			if aiInProgress {
				return "AI...> ", true
			}
			if inMultiLine {
				return "      > ", true // Continuation prompt
			}
			if pinnedEvalTime != nil {
				return "PromQL(pinat)> ", true
			}
			return "PromQL> ", true
		}),
		prompt.OptionPreviewSuggestionTextColor(prompt.DarkGray),
		prompt.OptionSelectedSuggestionBGColor(prompt.LightGray),
		prompt.OptionSuggestionBGColor(prompt.DarkGray),
		prompt.OptionDescriptionBGColor(prompt.DarkGray),
		prompt.OptionDescriptionTextColor(prompt.White), // Make descriptions more visible
		prompt.OptionMaxSuggestion(20),
		// Use PromQL-specific word separators for word detection
		prompt.OptionCompletionWordSeparator("(){}[]\" \t\n,="), // PromQL-specific word separators
		// Track when user explicitly opens the dropdown via Tab
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.Tab,
			Fn: func(buf *prompt.Buffer) {
				dropdownActive = true
			},
		}),
		// Arrow Up: prefix-search history navigation
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.Up,
			Fn: func(buf *prompt.Buffer) {
				// If user has edited since last insertion from history, restart nav
				if historyActive && buf.Text() != historyLastLine {
					resetHistoryState()
				}
				// If dropdown is active and suggestions exist, let go-prompt handle arrow keys
				if dropdownActive {
					if comps := promptCompleter(*buf.Document()); len(comps) > 0 {
						return
					}
					// dropdown was likely closed (no suggestions now)
					dropdownActive = false
				}
				// If we previously activated but found no matches, allow reactivation to rebuild
				if historyActive && len(filteredHistory) == 0 {
					historyActive = false
				}
				// Activate if not active
				if !historyActive {
					historyActive = true
					historySeed = buf.Text()
					historyPrefix = buf.Document().TextBeforeCursor()
					// Build filtered history based on captured prefix (keep duplicates for 1-1 navigation)
					filteredHistory = BuildFilteredHistory(historyPrefix, replHistory)
					historyIndex = 0
				}
				if len(filteredHistory) == 0 {
					return
				}
				if historyIndex < len(filteredHistory) {
					// Insert next older match
					buf.DeleteBeforeCursor(len([]rune(buf.Document().CurrentLineBeforeCursor())))
					buf.Delete(len([]rune(buf.Document().CurrentLineAfterCursor())))
					line := filteredHistory[historyIndex]
					buf.InsertText(line, false, true)
					historyLastLine = line
					historyIndex++
				}
			},
		}),
		// Arrow Down: prefix-search history navigation forward
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.Down,
			Fn: func(buf *prompt.Buffer) {
				// If dropdown is active and suggestions exist, let go-prompt handle arrow keys
				if dropdownActive {
					if comps := promptCompleter(*buf.Document()); len(comps) > 0 {
						return
					}
					// dropdown was likely closed
					dropdownActive = false
				}
				if !historyActive || len(filteredHistory) == 0 {
					return
				}
				if historyIndex > 1 {
					historyIndex--
					buf.DeleteBeforeCursor(len([]rune(buf.Document().CurrentLineBeforeCursor())))
					buf.Delete(len([]rune(buf.Document().CurrentLineAfterCursor())))
					line := filteredHistory[historyIndex-1]
					buf.InsertText(line, false, true)
					historyLastLine = line
					return
				}
				if historyIndex == 1 {
					// Restore the original seed line and exit navigation
					buf.DeleteBeforeCursor(len([]rune(buf.Document().CurrentLineBeforeCursor())))
					buf.Delete(len([]rune(buf.Document().CurrentLineAfterCursor())))
					buf.InsertText(historySeed, false, true)
					resetHistoryState()
				}
			},
		}),
		// Ctrl-C: cancel in-flight AI or clear current line
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlC,
			Fn: func(buf *prompt.Buffer) {
				if aiCancelRequest != nil {
					aiCancelRequest()
					return
				}
				// Clear line (do not submit 0x03 as input)
				doc := buf.Document()
				buf.CursorLeft(len([]rune(doc.TextBeforeCursor())))
				buf.Delete(len(doc.Text))
				// Reset history navigation state when line is cleared
				resetHistoryState()
			},
		}),

		// Emacs-style key bindings
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlA,
			Fn: func(buf *prompt.Buffer) {
				// Move to beginning of line
				x := []rune(buf.Document().CurrentLineBeforeCursor())
				buf.CursorLeft(len(x))
			},
		}),
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlE,
			Fn: func(buf *prompt.Buffer) {
				// Move to end of line
				x := []rune(buf.Document().CurrentLineAfterCursor())
				buf.CursorRight(len(x))
			},
		}),
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlK,
			Fn: func(buf *prompt.Buffer) {
				// Kill line from cursor to end
				x := []rune(buf.Document().CurrentLineAfterCursor())
				buf.Delete(len(x))
				// If we just cleared the entire line, reset history navigation
				if buf.Text() == "" {
					resetHistoryState()
				}
			},
		}),
		// Alt-F: Forward one word (ESC+f)
		prompt.OptionAddASCIICodeBind(
			prompt.ASCIICodeBind{
				ASCIICode: []byte{0x1b, 0x66}, // ESC + f
				Fn: func(buf *prompt.Buffer) {
					// Move forward one word
					doc := buf.Document()
					separators := "(){}[]\" \t\n,="
					text := doc.Text
					pos := len(doc.TextBeforeCursor())

					// Skip current word
					for pos < len(text) && !strings.ContainsRune(separators, rune(text[pos])) {
						pos++
					}
					// Skip separators
					for pos < len(text) && strings.ContainsRune(separators, rune(text[pos])) {
						pos++
					}
					// Move cursor
					moveCount := pos - len(doc.TextBeforeCursor())
					if moveCount > 0 {
						buf.CursorRight(moveCount)
					}
				},
			},
		),
		// Alt-B: Backward one word (ESC+b)
		prompt.OptionAddASCIICodeBind(
			prompt.ASCIICodeBind{
				ASCIICode: []byte{0x1b, 0x62}, // ESC + b
				Fn: func(buf *prompt.Buffer) {
					// Move backward one word
					doc := buf.Document()
					separators := "(){}[]\" \t\n,="
					text := doc.Text
					pos := len(doc.TextBeforeCursor())

					if pos == 0 {
						return
					}

					// Skip separators backward
					for pos > 0 && strings.ContainsRune(separators, rune(text[pos-1])) {
						pos--
					}
					// Skip word backward
					for pos > 0 && !strings.ContainsRune(separators, rune(text[pos-1])) {
						pos--
					}
					// Move cursor
					moveCount := len(doc.TextBeforeCursor()) - pos
					if moveCount > 0 {
						buf.CursorLeft(moveCount)
					}
				},
			},
		),
		// Ctrl-W: Delete word before cursor (with PromQL word boundaries)
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlW,
			Fn: func(buf *prompt.Buffer) {
				// Delete word before cursor using PromQL separators
				text := buf.Text()
				pos := len(buf.Document().TextBeforeCursor())
				if pos == 0 {
					return
				}

				// Find word boundary with PromQL-specific separators
				separators := "(){}[]\" \t\n,="
				start := pos - 1
				// Skip trailing separators
				for start >= 0 && strings.ContainsRune(separators, rune(text[start])) {
					start--
				}
				// Find beginning of word
				for start >= 0 && !strings.ContainsRune(separators, rune(text[start])) {
					start--
				}
				start++ // Move to first char of word

				// Delete from start to current position
				if start < pos {
					buf.DeleteBeforeCursor(pos - start)
				}
			},
		}),
		// Ctrl-U: Delete from cursor to beginning of line
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlU,
			Fn: func(buf *prompt.Buffer) {
				x := []rune(buf.Document().CurrentLineBeforeCursor())
				buf.DeleteBeforeCursor(len(x))
				// If we just cleared the entire line, reset history navigation
				if buf.Text() == "" {
					resetHistoryState()
				}
			},
		}),
		// Ctrl-D: Delete character under cursor (or exit if line is empty)
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlD,
			Fn: func(buf *prompt.Buffer) {
				if buf.Text() == "" {
					// Exit on empty line
					saveHistory()
					fmt.Println("\nExiting...")
					os.Exit(0)
				} else {
					// Delete character under cursor
					buf.Delete(1)
				}
			},
		}),
		// Alt-D: Delete word to the right (ESC+d)
		prompt.OptionAddASCIICodeBind(
			prompt.ASCIICodeBind{
				ASCIICode: []byte{0x1b, 0x64}, // ESC + d
				Fn: func(buf *prompt.Buffer) {
					// Delete word forward
					doc := buf.Document()
					separators := "(){}[]\" \t\n,="
					text := doc.Text
					pos := len(doc.TextBeforeCursor())
					end := pos

					// Advance to end of current word but do NOT consume following separators
					for end < len(text) && !strings.ContainsRune(separators, rune(text[end])) {
						end++
					}
					// Delete only the word; keep the next separator (e.g., '(', '{', space) intact
					deleteCount := end - pos
					if deleteCount > 0 {
						buf.Delete(deleteCount)
					}
				},
			},
		),
		// Alt-Backspace: Delete word backward (ESC+Backspace)
		prompt.OptionAddASCIICodeBind(
			prompt.ASCIICodeBind{
				ASCIICode: []byte{0x1b, 0x7f}, // ESC + DEL/Backspace
				Fn: func(buf *prompt.Buffer) {
					// Delete word before cursor (same as Ctrl-W)
					text := buf.Text()
					pos := len(buf.Document().TextBeforeCursor())
					if pos == 0 {
						return
					}

					separators := "(){}[]\" \t\n,="
					start := pos - 1
					// Skip trailing separators
					for start >= 0 && strings.ContainsRune(separators, rune(text[start])) {
						start--
					}
					// Find beginning of word
					for start >= 0 && !strings.ContainsRune(separators, rune(text[start])) {
						start--
					}
					start++

					if start < pos {
						buf.DeleteBeforeCursor(pos - start)
					}
				},
			},
		),
		// Ctrl-T: Transpose characters (swap current with previous)
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlT,
			Fn: func(buf *prompt.Buffer) {
				doc := buf.Document()
				text := doc.Text
				pos := len(doc.TextBeforeCursor())

				// Need at least 2 characters to transpose
				if pos > 0 && len(text) > 1 {
					if pos == len(text) {
						// At end of line, swap last two chars
						if pos >= 2 {
							buf.CursorLeft(2)
							buf.Delete(2)
							buf.InsertText(string(text[pos-1])+string(text[pos-2]), false, true)
						}
					} else if pos >= 1 {
						// In middle, swap current with previous
						buf.CursorLeft(1)
						buf.Delete(2)
						buf.InsertText(string(text[pos])+string(text[pos-1]), false, true)
					}
				}
			},
		}),
		// Ctrl-Y: Paste AI clipboard (first suggestion or last edited)
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlY,
			Fn: func(buf *prompt.Buffer) {
				if aiClipboard == "" {
					// If clipboard is empty but we have suggestions, default to first
					if len(lastAISuggestions) > 0 {
						aiClipboard = lastAISuggestions[0]
					} else {
						return
					}
				}
				// Replace entire line with clipboard content, appending helper text when available
				doc := buf.Document()
				buf.CursorLeft(len([]rune(doc.TextBeforeCursor())))
				buf.Delete(len(doc.Text))
				text := aiClipboard
				// Try to find matching explanation
				idx := -1
				for i, q := range lastAISuggestions {
					if strings.TrimSpace(q) == strings.TrimSpace(aiClipboard) {
						idx = i
						break
					}
				}
				if idx >= 0 && idx < len(lastAIExplanations) {
					if ex := strings.TrimSpace(lastAIExplanations[idx]); ex != "" {
						text = text + " # " + ex
					}
				}
				buf.InsertText(text, false, true)
			},
		}),
		// Alt-1..Alt-9: Paste corresponding AI suggestion directly
		// Note: ESC+<digit> sequences

		// Ctrl-L: Clear screen
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlL,
			Fn: func(buf *prompt.Buffer) {
				// Clear screen by printing ANSI escape codes
				fmt.Print("\033[2J\033[H") // Clear screen and move cursor to home
			},
		}),
		// Alt-Uppercase: Convert word to uppercase (ESC+u)
		prompt.OptionAddASCIICodeBind(
			prompt.ASCIICodeBind{
				ASCIICode: []byte{0x1b, 0x75}, // ESC + u
				Fn: func(buf *prompt.Buffer) {
					doc := buf.Document()
					separators := "(){}[]\" \t\n,="
					text := doc.Text
					pos := len(doc.TextBeforeCursor())
					end := pos

					// Find end of current word
					for end < len(text) && !strings.ContainsRune(separators, rune(text[end])) {
						end++
					}

					if end > pos {
						wordLen := end - pos
						buf.Delete(wordLen)
						buf.InsertText(strings.ToUpper(text[pos:end]), false, true)
					}
				},
			},
		),
		// Alt-Lowercase: Convert word to lowercase (ESC+l)
		prompt.OptionAddASCIICodeBind(
			prompt.ASCIICodeBind{
				ASCIICode: []byte{0x1b, 0x6c}, // ESC + l
				Fn: func(buf *prompt.Buffer) {
					doc := buf.Document()
					separators := "(){}[]\" \t\n,="
					text := doc.Text
					pos := len(doc.TextBeforeCursor())
					end := pos

					// Find end of current word
					for end < len(text) && !strings.ContainsRune(separators, rune(text[end])) {
						end++
					}

					if end > pos {
						wordLen := end - pos
						buf.Delete(wordLen)
						buf.InsertText(strings.ToLower(text[pos:end]), false, true)
					}
				},
			},
		),
		// Alt-Capitalize: Capitalize current word (ESC+c)
		prompt.OptionAddASCIICodeBind(
			prompt.ASCIICodeBind{
				ASCIICode: []byte{0x1b, 0x63}, // ESC + c
				Fn: func(buf *prompt.Buffer) {
					doc := buf.Document()
					separators := "(){}[]\" \t\n,="
					text := doc.Text
					pos := len(doc.TextBeforeCursor())
					end := pos

					// Find end of current word
					for end < len(text) && !strings.ContainsRune(separators, rune(text[end])) {
						end++
					}

					if end > pos {
						word := text[pos:end]
						if len(word) > 0 {
							var capitalized string
							if len(word) > 1 {
								capitalized = strings.ToUpper(string(word[0])) + strings.ToLower(word[1:])
							} else {
								capitalized = strings.ToUpper(word)
							}
							buf.Delete(len(word))
							buf.InsertText(capitalized, false, true)
						}
					}
				},
			},
		),
		// Alt+. (ESC+.): Insert last argument from previous command
		prompt.OptionAddASCIICodeBind(
			prompt.ASCIICodeBind{
				ASCIICode: []byte{0x1b, 0x2e}, // ESC + .
				Fn: func(buf *prompt.Buffer) {
					// Extract last argument from previous command
					if lastExecutedCommand == "" {
						return
					}

					lastArg := extractLastArgument(lastExecutedCommand)
					if lastArg != "" {
						buf.InsertText(lastArg, false, true)
					}
				},
			},
		),
		// Alt+Enter (ESC+Enter): Insert a literal newline for multi-line editing
		prompt.OptionAddASCIICodeBind(
			prompt.ASCIICodeBind{
				ASCIICode: []byte{0x1b, 0x0d}, // ESC + Enter (CR)
				Fn: func(buf *prompt.Buffer) {
					// Insert a newline character
					buf.InsertText("\n", false, true)
				},
			},
		),
		// Also try Alt+J (ESC+j) as an alternative for newline
		prompt.OptionAddASCIICodeBind(
			prompt.ASCIICodeBind{
				ASCIICode: []byte{0x1b, 0x6a}, // ESC + j
				Fn: func(buf *prompt.Buffer) {
					// Insert a newline character
					buf.InsertText("\n", false, true)
				},
			},
		),
	}

	// Add option to show completions at start only if eager completion is enabled
	if eagerCompletion {
		opts = append(opts, prompt.OptionShowCompletionAtStart())
	}

	// Initialize metrics for completion
	fetchMetrics()

	r.prompt = prompt.New(
		promptExecutor,
		promptCompleter,
		opts...,
	)

	// Run the prompt - this will handle terminal restoration on exit
	r.prompt.Run()
	inGoPromptSession = false

	// Save history when exiting normally
	saveHistory()

	// Restore terminal state when exiting normally
	if originalState != "" {
		restoreTerminalState(originalState)
	}

	return nil
}

func (r *promptREPL) Close() error {
	// go-prompt handles terminal restoration automatically
	return nil
}

// fetchMetrics fetches available metrics for completion
func fetchMetrics() {
	// Try to get metrics from globalStorage if available
	if globalStorage != nil {
		if storage, ok := globalStorage.(*sstorage.SimpleStorage); ok && storage != nil {
			// Build a de-duplicated metrics list and track recording rule names
			seen := make(map[string]bool)
			metrics = make([]string, 0, len(storage.Metrics))
			metricsHelp = storage.MetricsHelp // Use the help text from storage
			recordingRuleSet = make(map[string]bool)
			for name := range storage.Metrics {
				if !seen[name] {
					metrics = append(metrics, name)
					seen[name] = true
				}
			}
			// Include configured recording rule names as suggestions (without duplicates)
			for _, rn := range GetRecordingRuleNames() {
				recordingRuleSet[rn] = true
				if !seen[rn] {
					metrics = append(metrics, rn)
					seen[rn] = true
				}
			}
			return
		}
	}

	// Fallback to client if available
	if client == nil {
		return
	}

	// Use the pinned time if set, otherwise use current time
	evalTime := time.Now()
	if pinnedEvalTime != nil {
		evalTime = *pinnedEvalTime
	}

	lbls, _, err := client.LabelValues(ctx, "__name__", []string{}, evalTime.Add(-time.Hour), evalTime)
	if err != nil {
		return
	}

	metrics = make([]string, 0, len(lbls))
	for _, lbl := range lbls {
		metrics = append(metrics, string(lbl))
	}
}

// getMixedSuggests returns both metrics and functions (metrics prioritized)
func getMixedSuggests(prefix string) []prompt.Suggest {
	var suggestions []prompt.Suggest

	// Add metric suggestions FIRST (prioritized)
	metricSuggests := getMetricSuggests(prefix)
	for i := range metricSuggests {
		if len(suggestions) >= 50 {
			break
		}
		suggestions = append(suggestions, metricSuggests[i])
	}

	// Then add function suggestions
	funcSuggests := getFunctionSuggests(prefix)
	for i := range funcSuggests {
		if len(suggestions) >= 100 {
			break
		}
		suggestions = append(suggestions, funcSuggests[i])
	}

	return suggestions
}

// saveTerminalState saves the current terminal state using stty
func saveTerminalState() string {
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// restoreTerminalState restores terminal state using stty
func restoreTerminalState(state string) {
	if state == "" {
		return
	}
	cmd := exec.Command("stty", state)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

// PromQLContext represents the current context in a PromQL expression
type PromQLContext struct {
	Type         string // "function_arg", "label_name", "label_value", "range_duration", "after_operator", etc.
	MetricName   string
	LabelName    string
	FunctionName string
}

// analyzePromQLContext uses the PromQL parser to understand the current context
func analyzePromQLContext(text string) PromQLContext {
	context := PromQLContext{Type: "unknown"}

	// Try to parse what we have so far
	// Even if incomplete, the parser can give us useful information
	_, err := parser.ParseExpr(text)

	// Check for specific patterns that indicate context
	// Look for unclosed brackets (range selector)
	lastOpenBracket := strings.LastIndex(text, "[")
	lastCloseBracket := strings.LastIndex(text, "]")
	if lastOpenBracket > lastCloseBracket && lastOpenBracket != -1 {
		context.Type = "range_duration"
		// Extract metric name before bracket
		metricEnd := lastOpenBracket
		metricStart := lastOpenBracket - 1
		for metricStart >= 0 && !strings.ContainsRune("(){}\" \t\n,=", rune(text[metricStart])) {
			metricStart--
		}
		metricStart++
		if metricStart < metricEnd {
			context.MetricName = text[metricStart:metricEnd]
		}
		return context
	}

	// Check for label selector context
	lastOpenBrace := strings.LastIndex(text, "{")
	lastCloseBrace := strings.LastIndex(text, "}")
	if lastOpenBrace > lastCloseBrace && lastOpenBrace != -1 {
		// We're in a label selector
		labelPart := text[lastOpenBrace+1:]

		// Check if we're typing a label value (after =, !=, =~, !~)
		if matches := regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*(!?[=~])\s*"?([^"]*)$`).FindStringSubmatch(labelPart); len(matches) > 1 {
			context.Type = "label_value"
			context.LabelName = matches[1]
		} else {
			context.Type = "label_name"
		}

		// Extract metric name before brace
		metricEnd := lastOpenBrace
		metricStart := lastOpenBrace - 1
		for metricStart >= 0 && !strings.ContainsRune("()\" \t\n,=", rune(text[metricStart])) {
			metricStart--
		}
		metricStart++
		if metricStart < metricEnd {
			context.MetricName = strings.TrimSpace(text[metricStart:metricEnd])
		}
		return context
	}

	// Check if we're inside a function
	lastOpenParen := strings.LastIndex(text, "(")
	lastCloseParen := strings.LastIndex(text, ")")
	if lastOpenParen > lastCloseParen && lastOpenParen != -1 {
		// Extract function name
		funcStart := lastOpenParen - 1
		for funcStart >= 0 && (unicode.IsLetter(rune(text[funcStart])) || text[funcStart] == '_') {
			funcStart--
		}
		funcStart++
		if funcStart < lastOpenParen {
			context.FunctionName = text[funcStart:lastOpenParen]
			context.Type = "function_arg"
		}
		return context
	}

	// Check if we're after an operator
	operators := []string{"+", "-", "*", "/", "^", "%", "and", "or", "unless", ">", "<", ">=", "<=", "==", "!=", "by", "without"}
	for _, op := range operators {
		if strings.HasSuffix(strings.TrimSpace(text), op) ||
			strings.HasSuffix(strings.TrimSpace(text), op+" ") {
			context.Type = "after_operator"
			break
		}
	}

	// Check for aggregation context
	if err != nil && strings.Contains(err.Error(), "aggregation") {
		context.Type = "aggregation"
	}

	return context
}

// getLabelNameSuggests returns label name suggestions for a metric
func getLabelNameSuggests(prefix string, metricName string) []prompt.Suggest {
	if globalStorage == nil {
		return []prompt.Suggest{}
	}

	storage, ok := globalStorage.(*sstorage.SimpleStorage)
	if !ok || storage == nil {
		return []prompt.Suggest{}
	}

	// Collect unique label names from the metric
	labelNames := make(map[string]bool)
	if samples, exists := storage.Metrics[metricName]; exists {
		for _, sample := range samples {
			for labelName := range sample.Labels {
				if labelName != "__name__" {
					labelNames[labelName] = true
				}
			}
		}
	}

	var suggestions []prompt.Suggest
	for labelName := range labelNames {
		if prefix == "" || strings.HasPrefix(labelName, prefix) {
			suggestions = append(suggestions, prompt.Suggest{
				Text:        labelName,
				Description: "label",
			})
		}
	}

	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Text < suggestions[j].Text
	})

	return suggestions
}

// getLabelValueSuggests returns label value suggestions for a specific label
func getLabelValueSuggests(prefix string, metricName string, labelName string) []prompt.Suggest {
	if globalStorage == nil {
		return []prompt.Suggest{}
	}

	storage, ok := globalStorage.(*sstorage.SimpleStorage)
	if !ok || storage == nil {
		return []prompt.Suggest{}
	}

	// Collect unique label values
	labelValues := make(map[string]bool)
	if samples, exists := storage.Metrics[metricName]; exists {
		for _, sample := range samples {
			if value, hasLabel := sample.Labels[labelName]; hasLabel {
				labelValues[value] = true
			}
		}
	}

	// Check if prefix already has quotes
	prefixHasOpenQuote := strings.HasPrefix(prefix, "\"")
	prefixToMatch := prefix
	if prefixHasOpenQuote {
		// Remove the opening quote for matching
		prefixToMatch = strings.TrimPrefix(prefix, "\"")
	}

	var suggestions []prompt.Suggest
	for labelValue := range labelValues {
		if prefixToMatch == "" || strings.HasPrefix(labelValue, prefixToMatch) {
			// Add quotes around the value
			quotedValue := "\"" + labelValue + "\""
			if prefixHasOpenQuote {
				// If we already have an opening quote, don't add another
				quotedValue = labelValue + "\""
			}
			suggestions = append(suggestions, prompt.Suggest{
				Text:        quotedValue,
				Description: "value",
			})
		}
	}

	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Text < suggestions[j].Text
	})

	return suggestions
}

// getAggregationSuggests returns aggregation operator suggestions
func getAggregationSuggests(prefix string) []prompt.Suggest {
	aggregations := []prompt.Suggest{
		{Text: "sum", Description: "calculate sum"},
		{Text: "min", Description: "select minimum"},
		{Text: "max", Description: "select maximum"},
		{Text: "avg", Description: "calculate average"},
		{Text: "group", Description: "group series"},
		{Text: "stddev", Description: "calculate standard deviation"},
		{Text: "stdvar", Description: "calculate standard variance"},
		{Text: "count", Description: "count series"},
		{Text: "count_values", Description: "count by value"},
		{Text: "bottomk", Description: "smallest k elements"},
		{Text: "topk", Description: "largest k elements"},
		{Text: "quantile", Description: "calculate quantile"},
	}

	var filtered []prompt.Suggest
	for _, agg := range aggregations {
		if strings.HasPrefix(agg.Text, prefix) {
			filtered = append(filtered, agg)
		}
	}

	return filtered
}

// getHistoryPath returns the path to the history file
func getHistoryPath() string {
	// First check if PROMQL_CLI_HISTORY env var is set
	if histPath := os.Getenv("PROMQL_CLI_HISTORY"); histPath != "" {
		return histPath
	}

	// Prefer the user's home directory
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".promql-cli_history")
	}
	// As a safer fallback than /tmp, use current working directory
	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		return filepath.Join(cwd, ".promql-cli_history")
	}
	// Last resort: relative path in current process dir
	return ".promql-cli_history"
}

// loadHistory loads command history from file
func loadHistory() {
	histPath := getHistoryPath()
	data, err := os.ReadFile(histPath)
	if err != nil {
		// File doesn't exist or can't be read, start with empty history
		replHistory = []string{}
		return
	}

	replHistory = []string{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			replHistory = append(replHistory, line)
		}
	}

	// Limit history size to last 1000 entries
	if len(replHistory) > 1000 {
		replHistory = replHistory[len(replHistory)-1000:]
	}
}

// saveHistory saves command history to file
func saveHistory() {
	if len(replHistory) == 0 {
		return
	}

	histPath := getHistoryPath()

	// Create directory if it doesn't exist
	dir := filepath.Dir(histPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return // Silently fail if we can't create directory
	}

	// Write history to file
	file, err := os.Create(histPath)
	if err != nil {
		return // Silently fail if we can't create file
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	// Keep only last 1000 entries
	start := 0
	if len(replHistory) > 1000 {
		start = len(replHistory) - 1000
	}

	for i := start; i < len(replHistory); i++ {
		_, _ = writer.WriteString(replHistory[i] + "\n")
	}
	_ = writer.Flush()
}

// appendToHistoryFile appends a single command to the history file
func appendToHistoryFile(cmd string) {
	histPath := getHistoryPath()

	// Create directory if it doesn't exist
	dir := filepath.Dir(histPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return // Silently fail
	}

	// Append to file
	file, err := os.OpenFile(histPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return // Silently fail
	}
	defer file.Close()

	_, _ = file.WriteString(cmd + "\n")
}

// Helper function to check if a string is a valid PromQL function
func isPromQLFunction(s string) bool {
	// Use the parser's function list
	_, exists := parser.Functions[s]
	return exists
}

// extractLastArgument extracts the last meaningful argument from a command
// For PromQL queries, this typically means the last metric name or significant value
func extractLastArgument(cmd string) string {
	if cmd == "" {
		return ""
	}

	// Handle ad-hoc commands first
	if strings.HasPrefix(cmd, ".") {
		parts := strings.Fields(cmd)
		if len(parts) > 1 {
			// Return the last argument of an ad-hoc command
			return parts[len(parts)-1]
		}
		return ""
	}

	// For PromQL expressions, we need to find the last meaningful token
	// This is typically a metric name, label value, or duration
	separators := "(){}[]\" \t\n,="
	tokens := []string{}
	currentToken := ""

	for _, ch := range cmd {
		if strings.ContainsRune(separators, ch) {
			if currentToken != "" {
				tokens = append(tokens, currentToken)
				currentToken = ""
			}
		} else {
			currentToken += string(ch)
		}
	}
	if currentToken != "" {
		tokens = append(tokens, currentToken)
	}

	// Find the last meaningful token (skip operators and keywords)
	operators := map[string]bool{
		"and": true, "or": true, "unless": true,
		"by": true, "without": true, "on": true, "ignoring": true,
		"group_left": true, "group_right": true,
		"offset": true, "bool": true,
	}

	for i := len(tokens) - 1; i >= 0; i-- {
		token := tokens[i]
		// Skip operators and common keywords
		if operators[strings.ToLower(token)] {
			continue
		}
		// Skip pure numbers (likely to be constants, not metrics)
		if _, err := strconv.ParseFloat(token, 64); err == nil {
			continue
		}
		// Skip time durations ending with time units
		if regexp.MustCompile(`^\d+[smhdwy]$`).MatchString(token) {
			continue
		}
		// This looks like a metric name or label value - return it
		return token
	}

	return ""
}

// resetHistoryNavigationIfNeeded resets history navigation state if user has typed something different
func resetHistoryNavigationIfNeeded(currentFullText, previousHistoryPrefix string) {
	if historyIndex > 0 && len(filteredHistory) > 0 {
		// Check if current text matches any history entry we've navigated to
		isHistoryEntry := false
		for i := 0; i < historyIndex && i < len(filteredHistory); i++ {
			if currentFullText == filteredHistory[i] {
				isHistoryEntry = true
				break
			}
		}
		// If user has typed something different, reset history navigation
		if !isHistoryEntry && currentFullText != previousHistoryPrefix {
			resetHistoryState()
		}
	}
}

// shouldSuppressCompletions determines if completions should be suppressed based on context
func shouldSuppressCompletions(text, trimmedText string) bool {
	// Suppress suggestions immediately after closing delimiters ) ] }
	trimRight := strings.TrimRight(text, " \t")
	if trimRight != "" {
		last := trimRight[len(trimRight)-1]
		if last == ')' || last == ']' || last == '}' {
			return true
		}
	}

	// Check eager completion setting
	eagerCompletion := os.Getenv("PROMQL_CLI_EAGER_COMPLETION") == "true"

	if !eagerCompletion {
		// Don't show suggestions at the start of a new line - wait for Tab or typing
		if text == "" {
			return true
		}

		// Don't show completions immediately after space unless it's an ad-hoc command
		if strings.HasSuffix(text, " ") {
			// Check if we're in an ad-hoc command that expects completions after space
			isAdHocWithCompletion := false
			adHocCompletionCommands := make([]string, len(AdHocCommands))
			for i, cmd := range AdHocCommands {
				adHocCompletionCommands[i] = cmd.Command + " "
			}
			for _, cmd := range adHocCompletionCommands {
				if strings.Contains(text, cmd) {
					isAdHocWithCompletion = true
					break
				}
			}
			// Special case for .ai - it should show completions after space
			if strings.HasPrefix(trimmedText, ".ai ") {
				isAdHocWithCompletion = true
			}
			if !isAdHocWithCompletion {
				return true
			}
		}
	}

	return false
}

// isInAdHocCommandContext checks if we're currently typing within an ad-hoc command
func isInAdHocCommandContext(trimmedText string) bool {
	for _, cmd := range AdHocCommands {
		if strings.HasPrefix(trimmedText, cmd.Command) {
			return true
		}
	}
	return false
}

// resetHistoryState fully resets all history navigation state
func resetHistoryState() {
	historyActive = false
	historyIndex = 0
	historyPrefix = ""
	filteredHistory = nil
	historyLastLine = ""
}
