package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	promparser "github.com/prometheus/prometheus/promql/parser"
)

// pinnedEvalTime, when set, forces future query evaluation to use this timestamp.
// It is used by the REPL and can be controlled via the .pinat ad-hoc command.
var pinnedEvalTime *time.Time

// When true, the go-prompt completer will present an AI selection menu
var aiSelectionActive bool

// refreshMetricsCache is a function pointer to refresh the metrics cache for autocompletion
// It's set by the prompt backend when active
var refreshMetricsCache func(*SimpleStorage)

// handleAdHocFunction handles special ad-hoc functions that are not part of PromQL
// Returns true if the query was handled as an ad-hoc function, false otherwise
var lastAISuggestions []string
var lastAIExplanations []string
var pendingAISuggestion string
var aiClipboard string

// aiCancelRequest, when non-nil, cancels an in-flight AI request (e.g., on Ctrl-C)
var aiCancelRequest func()

// aiInProgress indicates an AI request is running asynchronously.
var aiInProgress bool

func handleAdHocFunction(query string, storage *SimpleStorage) bool {
	// .help: show ad-hoc commands usage
	if strings.HasPrefix(query, ".help") {
		fmt.Println("\nAd-hoc commands:")
		for _, cmd := range AdHocCommands {
			fmt.Printf("  %s\n", cmd.Usage)
			fmt.Printf("    %s\n", cmd.Description)
			if len(cmd.Examples) > 0 {
				if len(cmd.Examples) == 1 {
					fmt.Printf("    Example: %s\n", cmd.Examples[0])
				} else {
					fmt.Println("    Examples:")
					for _, ex := range cmd.Examples {
						fmt.Printf("      %s\n", ex)
					}
				}
			}
		}
		fmt.Println()
		return true
	}

	// .ai: AI-assisted query suggestions
	if strings.HasPrefix(strings.TrimSpace(query), ".ai") {
		args := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), ".ai"))
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
			suggestions, err := aiSuggestQueriesCtx(ctx, storage, intent)
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
				q = cleanCandidate(q)
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
			fmt.Println("Tips: Alt-1..Alt-9 to paste a suggestion; Ctrl-Y to paste the first suggestion; Tab to show the dropdown.")
		}(args)
		// Return immediately to keep the prompt interactive
		return true
	}

	// .metrics: list metric names
	if strings.TrimSpace(query) == ".metrics" {
		if len(storage.metrics) == 0 {
			fmt.Println("No metrics loaded")
			return true
		}
		var names []string
		for name := range storage.metrics {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Printf("Metrics (%d):\n", len(names))
		for _, n := range names {
			fmt.Printf("  - %s\n", n)
		}
		return true
	}

	// Handle .labels <metric> (preferred) and legacy .labels(metric)
	if strings.HasPrefix(query, ".labels ") || (strings.HasPrefix(query, ".labels(") && strings.HasSuffix(query, ")")) {
		var metricName string
		if strings.HasPrefix(query, ".labels ") {
			metricName = strings.TrimSpace(strings.TrimPrefix(query, ".labels "))
		} else {
			// legacy form
			metricName = strings.TrimSuffix(strings.TrimPrefix(query, ".labels("), ")")
		}
		metricName = strings.Trim(metricName, " \"'")

		if metricName == "" {
			fmt.Println("Usage: .labels <metric_name>")
			fmt.Println("Example: .labels cloudcost_azure_aks_storage_by_location_usd_per_gibyte_hour")
			return true
		}

		// Check if metric exists
		samples, exists := storage.metrics[metricName]
		if !exists {
			fmt.Printf("Metric '%s' not found\n", metricName)
			fmt.Println("Available metrics:")
			count := 0
			for name := range storage.metrics {
				if count >= 5 {
					fmt.Printf("... and %d more\n", len(storage.metrics)-5)
					break
				}
				fmt.Printf("  - %s\n", name)
				count++
			}
			return true
		}

		// Collect all unique labels for this metric
		labelNames := make(map[string]bool)
		labelValues := make(map[string]map[string]bool)

		for _, sample := range samples {
			for labelName, labelValue := range sample.Labels {
				if labelName != "__name__" { // Skip the metric name label
					labelNames[labelName] = true

					if labelValues[labelName] == nil {
						labelValues[labelName] = make(map[string]bool)
					}
					labelValues[labelName][labelValue] = true
				}
			}
		}

		// Display results
		fmt.Printf("Labels for metric '%s' (%d samples):\n", metricName, len(samples))
		if len(labelNames) == 0 {
			fmt.Println("  No labels found")
			return true
		}

		// Sort label names for consistent output
		var sortedLabels []string
		for labelName := range labelNames {
			sortedLabels = append(sortedLabels, labelName)
		}
		sort.Strings(sortedLabels)

		for _, labelName := range sortedLabels {
			values := labelValues[labelName]
			valueCount := len(values)

			fmt.Printf("  %s (%d values)", labelName, valueCount)

			// Show first few values as examples
			if valueCount > 0 {
				var sortedValues []string
				for value := range values {
					sortedValues = append(sortedValues, value)
				}
				sort.Strings(sortedValues)

				fmt.Printf(": ")
				if valueCount <= 3 {
					// Show all values if 3 or fewer
					for i, value := range sortedValues {
						if i > 0 {
							fmt.Printf(", ")
						}
						fmt.Printf("%q", value)
					}
				} else {
					// Show first 3 values and indicate there are more
					for i := 0; i < 3; i++ {
						if i > 0 {
							fmt.Printf(", ")
						}
						fmt.Printf("%q", sortedValues[i])
					}
					fmt.Printf(", ... and %d more", valueCount-3)
				}
			}
			fmt.Println()
		}

		return true
	}

	// Handle .timestamps <metric>
	if strings.HasPrefix(strings.TrimSpace(query), ".timestamps ") || strings.TrimSpace(query) == ".timestamps" {
		metric := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), ".timestamps"))
		metric = strings.Trim(metric, " \"'")
		if metric == "" {
			fmt.Println("Usage: .timestamps <metric>")
			fmt.Println("Example: .timestamps http_requests_total")
			return true
		}
		samples, exists := storage.metrics[metric]
		if !exists || len(samples) == 0 {
			fmt.Printf("Metric '%s' not found or has no samples\n", metric)
			return true
		}
		// Series count (group by labelset excluding __name__)
		seriesSet := make(map[string]struct{})
		for _, s := range samples {
			// build key excluding __name__
			keys := make([]string, 0, len(s.Labels))
			for k := range s.Labels {
				if k == "__name__" {
					continue
				}
				keys = append(keys, k)
			}
			sort.Strings(keys)
			b := strings.Builder{}
			for i, k := range keys {
				if i > 0 {
					b.WriteByte('\xff') // unlikely separator
				}
				b.WriteString(k)
				b.WriteByte('=')
				b.WriteString(s.Labels[k])
			}
			seriesSet[b.String()] = struct{}{}
		}
		seriesCount := len(seriesSet)

		// Unique timestamps and min/max
		uniq := make(map[int64]int)
		var minTs, maxTs int64
		first := true
		for _, s := range samples {
			uniq[s.Timestamp]++
			if first {
				minTs, maxTs = s.Timestamp, s.Timestamp
				first = false
			} else {
				if s.Timestamp < minTs {
					minTs = s.Timestamp
				}
				if s.Timestamp > maxTs {
					maxTs = s.Timestamp
				}
			}
		}
		uniqueCount := len(uniq)
		span := time.Duration(maxTs-minTs) * time.Millisecond
		// Prepare example timestamps
		ts := make([]int64, 0, uniqueCount)
		for t := range uniq {
			ts = append(ts, t)
		}
		sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
		exN := 5
		if len(ts) < exN {
			exN = len(ts)
		}
		fmt.Printf("Timestamp summary for metric '%s'\n", metric)
		fmt.Printf("  Series: %d\n", seriesCount)
		fmt.Printf("  Samples: %d\n", len(samples))
		fmt.Printf("  Unique timestamps: %d\n", uniqueCount)
		fmt.Printf("  Earliest: %s (unix_ms=%d)\n", time.UnixMilli(minTs).UTC().Format(time.RFC3339), minTs)
		fmt.Printf("  Latest:   %s (unix_ms=%d)\n", time.UnixMilli(maxTs).UTC().Format(time.RFC3339), maxTs)
		fmt.Printf("  Span:     %s\n", span)
		if exN > 0 {
			fmt.Printf("  Examples: ")
			for i := 0; i < exN; i++ {
				if i > 0 {
					fmt.Printf(", ")
				}
				fmt.Printf("%s", time.UnixMilli(ts[i]).UTC().Format(time.RFC3339))
			}
			fmt.Println()
		}
		return true
	}

	// Handle .drop <metric>
	if strings.HasPrefix(strings.TrimSpace(query), ".drop ") || strings.TrimSpace(query) == ".drop" {
		metric := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), ".drop"))
		metric = strings.TrimSpace(metric)
		if metric == "" {
			fmt.Println("Usage: .drop <metric>")
			fmt.Println("Example: .drop http_requests_total")
			return true
		}
		// Check existence
		samples, exists := storage.metrics[metric]
		if !exists {
			fmt.Printf("Metric '%s' not found\n", metric)
			return true
		}
		removed := len(samples)
		delete(storage.metrics, metric)
		// Report new totals
		totalMetrics := len(storage.metrics)
		totalSamples := 0
		for _, ss := range storage.metrics {
			totalSamples += len(ss)
		}
		fmt.Printf("Dropped '%s': -%d samples (now: %d metrics, %d samples)\n", metric, removed, totalMetrics, totalSamples)

		// Refresh metrics cache for autocompletion if using prompt backend
		if refreshMetricsCache != nil {
			refreshMetricsCache(storage)
		}

		return true
	}

	// Handle .save <file.prom>
	if strings.HasPrefix(strings.TrimSpace(query), ".save ") || strings.TrimSpace(query) == ".save" {
		path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), ".save"))
		path = strings.Trim(path, " \"'")
		if path == "" {
			fmt.Println("Usage: .save <file.prom>")
			return true
		}
		f, err := os.Create(path)
		if err != nil {
			fmt.Printf("Failed to open %s for writing: %v\n", path, err)
			return true
		}
		defer f.Close()
		if err := storage.SaveToWriter(f); err != nil {
			fmt.Printf("Failed to save metrics to %s: %v\n", path, err)
			return true
		}
		fmt.Printf("Saved store to %s\n", path)
		return true
	}

	// Handle .load <file.prom>
	if strings.HasPrefix(strings.TrimSpace(query), ".load ") || strings.TrimSpace(query) == ".load" {
		path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), ".load"))
		path = strings.Trim(path, " \"'")
		if path == "" {
			fmt.Println("Usage: .load <file.prom>")
			return true
		}
		f, err := os.Open(path)
		if err != nil {
			fmt.Printf("Failed to open %s: %v\n", path, err)
			return true
		}
		defer f.Close()
		beforeMetrics := len(storage.metrics)
		beforeSamples := 0
		for _, ss := range storage.metrics {
			beforeSamples += len(ss)
		}
		if err := storage.LoadFromReader(f); err != nil {
			fmt.Printf("Failed to load metrics from %s: %v\n", path, err)
			return true
		}
		afterMetrics := len(storage.metrics)
		afterSamples := 0
		for _, ss := range storage.metrics {
			afterSamples += len(ss)
		}
		fmt.Printf("Loaded %s: +%d metrics, +%d samples (total: %d metrics, %d samples)\n", path, afterMetrics-beforeMetrics, afterSamples-beforeSamples, afterMetrics, afterSamples)

		// Refresh metrics cache for autocompletion if using prompt backend
		if refreshMetricsCache != nil {
			refreshMetricsCache(storage)
		}

		return true
	}

	// Handle .pinat <time|now|remove>
	if strings.HasPrefix(strings.TrimSpace(query), ".pinat") {
		arg := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), ".pinat"))
		arg = strings.Trim(arg, " \"'")
		if arg == "" {
			if pinnedEvalTime == nil {
				fmt.Println("Pinned evaluation time: none")
			} else {
				fmt.Printf("Pinned evaluation time: %s\n", pinnedEvalTime.UTC().Format(time.RFC3339))
			}
			return true
		}
		if strings.EqualFold(arg, "remove") {
			pinnedEvalTime = nil
			fmt.Println("Pinned evaluation time: removed")
			return true
		}
		var t time.Time
		var err error
		if strings.EqualFold(arg, "now") {
			t = time.Now()
		} else {
			t, err = parseEvalTime(arg)
			if err != nil {
				fmt.Printf("Invalid time %q: %v\n", arg, err)
				return true
			}
		}
		pinnedEvalTime = &t
		fmt.Printf("Pinned evaluation time: %s\n", t.UTC().Format(time.RFC3339))
		return true
	}

	// Handle .scrape <URI> [metrics_regex] [count] [delay]
	if strings.HasPrefix(strings.TrimSpace(query), ".scrape ") {
		args := strings.Fields(strings.TrimSpace(query))
		if len(args) < 2 {
			fmt.Println("Usage: .scrape <URI> [metrics_regex] [count] [delay]")
			fmt.Println("Examples: .scrape http://localhost:9100/metrics | .scrape http://localhost:9100/metrics '^(up|process_.*)$' 3 5s")
			return true
		}
		uri := args[1]
		var regexStr string
		count := 1
		delay := 10 * time.Second
		countSet := false
		delaySet := false
		for _, tok := range args[2:] {
			if !countSet {
				if n, err := strconv.Atoi(tok); err == nil {
					count = n
					if count < 1 {
						count = 1
					}
					countSet = true
					continue
				}
			}
			if !delaySet {
				if d, err := time.ParseDuration(tok); err == nil {
					delay = d
					if delay < 0 {
						delay = 0
					}
					delaySet = true
					continue
				}
			}
			if regexStr == "" {
				regexStr = strings.Trim(tok, "\"'")
				continue
			}
		}
		var re *regexp.Regexp
		var reErr error
		if strings.TrimSpace(regexStr) != "" {
			re, reErr = regexp.Compile(regexStr)
			if reErr != nil {
				fmt.Printf("Invalid metrics_regex %q: %v\n", regexStr, reErr)
				return true
			}
		}

		client := &http.Client{Timeout: 60 * time.Second}
		for i := 0; i < count; i++ {
			beforeMetrics := len(storage.metrics)
			beforeSamples := 0
			for _, ss := range storage.metrics {
				beforeSamples += len(ss)
			}

			resp, err := client.Get(uri)
			if err != nil {
				fmt.Printf("Failed to scrape %s: %v\n", uri, err)
				return true
			}
			func() {
				defer resp.Body.Close()
				if resp.StatusCode < 200 || resp.StatusCode >= 300 {
					fmt.Printf("Failed to scrape %s: HTTP %d\n", uri, resp.StatusCode)
					return
				}
				if re != nil {
					if err := storage.LoadFromReaderWithFilter(resp.Body, func(name string) bool { return re.MatchString(name) }); err != nil {
						fmt.Printf("Failed to parse metrics from %s: %v\n", uri, err)
						return
					}
				} else {
					if err := storage.LoadFromReader(resp.Body); err != nil {
						fmt.Printf("Failed to parse metrics from %s: %v\n", uri, err)
						return
					}
				}
			}()

			afterMetrics := len(storage.metrics)
			afterSamples := 0
			for _, ss := range storage.metrics {
				afterSamples += len(ss)
			}
			fmt.Printf("Scraped %s (%d/%d): +%d metrics, +%d samples (total: %d metrics, %d samples)\n",
				uri, i+1, count, afterMetrics-beforeMetrics, afterSamples-beforeSamples, afterMetrics, afterSamples)

			if i < count-1 && delay > 0 {
				time.Sleep(delay)
			}
		}

		// Refresh metrics cache for autocompletion if using prompt backend
		if refreshMetricsCache != nil {
			refreshMetricsCache(storage)
		}

		return true
	}

	// Handle .seed <metric> [steps=N] [step=1m]
	if strings.HasPrefix(query, ".seed ") {
		args := strings.Fields(query)
		if len(args) < 2 {
			fmt.Println("Usage: .seed <metric> [steps=N] [step=1m]")
			return true
		}
		metric := args[1]
		steps := 5
		step := time.Minute
		posIdx := 0
		for _, a := range args[2:] {
			if strings.HasPrefix(a, "steps=") {
				fmt.Sscanf(a, "steps=%d", &steps)
				continue
			}
			if strings.HasPrefix(a, "step=") {
				v := strings.TrimPrefix(a, "step=")
				if d, err := time.ParseDuration(v); err == nil {
					step = d
				}
				continue
			}
			// Positional parsing: first bare token -> steps (int), second -> step (duration)
			if posIdx == 0 {
				if n, err := strconv.Atoi(a); err == nil {
					steps = n
					posIdx++
					continue
				}
			}
			if posIdx <= 1 { // allow step to be set even if steps was invalid
				if d, err := time.ParseDuration(a); err == nil {
					step = d
					posIdx = 2
					continue
				}
			}
		}
		seedHistory(storage, metric, steps, step)
		fmt.Printf("Seeded %d historical points (step %s) for metric '%s'\n", steps, step, metric)

		// Refresh metrics cache for autocompletion if using prompt backend
		if refreshMetricsCache != nil {
			refreshMetricsCache(storage)
		}

		return true
	}

	return false
}
