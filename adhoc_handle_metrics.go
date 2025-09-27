package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func handleAdhocMetrics(query string, storage *SimpleStorage) bool {
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

func handleAdhocLabels(query string, storage *SimpleStorage) bool {
	var metricName string
	if strings.HasPrefix(query, ".labels ") {
		metricName = strings.TrimSpace(strings.TrimPrefix(query, ".labels "))
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

func handleAdhocTimestamps(query string, storage *SimpleStorage) bool {
	metric := strings.TrimSpace(strings.TrimPrefix(query, ".timestamps"))
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

func handleAdhocDrop(query string, storage *SimpleStorage) bool {
	metric := strings.TrimSpace(strings.TrimPrefix(query, ".drop"))
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

func handleAdhocSeed(query string, storage *SimpleStorage) bool {
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
