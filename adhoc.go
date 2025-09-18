package main

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// handleAdHocFunction handles special ad-hoc functions that are not part of PromQL
// Returns true if the query was handled as an ad-hoc function, false otherwise
func handleAdHocFunction(query string, storage *SimpleStorage) bool {
	// .help: show ad-hoc commands usage
	if strings.HasPrefix(query, ".help") {
		fmt.Println("\nAd-hoc commands:")
		fmt.Println("  .help")
		fmt.Println("    Show this help")
		fmt.Println("  .labels <metric>")
		fmt.Println("    Show the set of labels and example values for a metric present in the loaded dataset")
		fmt.Println("    Example: .labels http_requests_total")
		fmt.Println("  .metrics")
		fmt.Println("    List metric names available in the loaded dataset")
		fmt.Println("  .timestamps <metric>")
		fmt.Println("    Summarize timestamps found across the metric's time series (unique count, earliest, latest, span)")
		fmt.Println("    Example: .timestamps http_requests_total")
		fmt.Println("  .seed <metric> [steps=N] [step=1m]")
		fmt.Println("    Backfill N historical points per series for a metric, spaced by step (enables rate()/increase())")
		fmt.Println("    Also supports positional form: .seed <metric> <steps> [<step>]")
		fmt.Println("    Examples: .seed http_requests_total steps=10 step=30s | .seed http_requests_total 10 30s")
		fmt.Println("  .scrape <URI> [metrics_regex] [count] [delay]")
		fmt.Println("    Fetch metrics from an HTTP(S) endpoint and load them. Optional:")
		fmt.Println("      - metrics_regex: only metric names matching this regexp are loaded")
		fmt.Println("      - count: number of scrapes (default 1)")
		fmt.Println("      - delay: delay between scrapes as Go duration (default 10s)")
		fmt.Println("    Examples:")
		fmt.Println("      .scrape http://localhost:9100/metrics")
		fmt.Println("      .scrape http://localhost:9100/metrics '^(up|process_.*)$'")
		fmt.Println("      .scrape http://localhost:9100/metrics 3 5s")
		fmt.Println("      .scrape http://localhost:9100/metrics 'http_.*' 5 2s")
		fmt.Println("  .drop <metric>")
		fmt.Println("    Remove a metric (all its series) from the in-memory store")
		fmt.Println("    Example: .drop http_requests_total")
		fmt.Println("  .at <time> <query>")
		fmt.Println("    Evaluate a query at a specific time. Time formats: now, now-5m, now+1h, RFC3339, unix secs/millis")
		fmt.Println("    Example: .at now-10m sum by (path) (rate(http_requests_total[5m]))")
		fmt.Println()
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

		client := &http.Client{Timeout: 20 * time.Second}
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
		return true
	}

	return false
}
