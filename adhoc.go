package main

import (
	"fmt"
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
		fmt.Println("  .seed <metric> [steps=N] [step=1m]")
		fmt.Println("    Backfill N historical points per series for a metric, spaced by step (enables rate()/increase())")
		fmt.Println("    Also supports positional form: .seed <metric> <steps> [<step>]")
		fmt.Println("    Examples: .seed http_requests_total steps=10 step=30s | .seed http_requests_total 10 30s")
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
