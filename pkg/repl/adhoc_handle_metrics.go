package repl

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// storeTotals returns (#metrics, #samples) for the current store
func storeTotals(storage *sstorage.SimpleStorage) (int, int) {
	totalMetrics := len(storage.Metrics)
	totalSamples := 0
	for _, ss := range storage.Metrics {
		totalSamples += len(ss)
	}
	return totalMetrics, totalSamples
}

func handleAdhocStats(_ string, storage *sstorage.SimpleStorage) bool {
	tm, ts := storeTotals(storage)
	fmt.Printf("Total: %d metrics, %d samples\n", tm, ts)
	return true
}

func handleAdhocMetrics(_ string, storage *sstorage.SimpleStorage) bool {
	if len(storage.Metrics) == 0 {
		fmt.Println("No metrics loaded")
		return true
	}
	var names []string
	for name := range storage.Metrics {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Printf("Metrics (%d):\n", len(names))
	for _, n := range names {
		fmt.Printf("  - %s\n", n)
	}
	return true
}

func handleAdhocLabels(query string, storage *sstorage.SimpleStorage) bool {
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
	samples, exists := storage.Metrics[metricName]
	if !exists {
		fmt.Printf("Metric '%s' not found\n", metricName)
		fmt.Println("Available metrics:")
		count := 0
		for name := range storage.Metrics {
			if count >= 5 {
				fmt.Printf("... and %d more\n", len(storage.Metrics)-5)
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

func handleAdhocTimestamps(query string, storage *sstorage.SimpleStorage) bool {
	metric := strings.TrimSpace(strings.TrimPrefix(query, ".timestamps"))
	metric = strings.Trim(metric, " \"'")
	if metric == "" {
		fmt.Println("Usage: .timestamps <metric>")
		fmt.Println("Example: .timestamps http_requests_total")
		return true
	}
	samples, exists := storage.Metrics[metric]
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

func handleAdhocDrop(query string, storage *sstorage.SimpleStorage) bool {
	arg := strings.TrimSpace(strings.TrimPrefix(query, ".drop"))
	arg = strings.Trim(arg, " \"'")
	if arg == "" {
		fmt.Println("Usage: .drop <series regex>")
		fmt.Println("Example: .drop '^up\\{.*instance=\"db-.*\".*\\}$'")
		return true
	}
	re, err := regexp.Compile(arg)
	if err != nil {
		fmt.Printf("Invalid regex: %v\n", err)
		return true
	}
	removed := 0
	for name, samples := range storage.Metrics {
		kept := samples[:0]
		for _, s := range samples {
			if re.MatchString(seriesSignature(name, s.Labels)) {
				removed++
				continue
			}
			kept = append(kept, s)
		}
		if len(kept) == 0 {
			delete(storage.Metrics, name)
		} else {
			storage.Metrics[name] = kept
		}
	}
	// Report new totals
	totalMetrics, totalSamples := storeTotals(storage)
	fmt.Printf("Dropped %d samples (now: %d metrics, %d samples)\n", removed, totalMetrics, totalSamples)
	if refreshMetricsCache != nil {
		refreshMetricsCache(storage)
	}
	return true
}

func handleAdhocKeep(query string, storage *sstorage.SimpleStorage) bool {
	arg := strings.TrimSpace(strings.TrimPrefix(query, ".keep"))
	arg = strings.Trim(arg, " \"'")
	if arg == "" {
		fmt.Println("Usage: .keep <series regex>")
		fmt.Println("Example: .keep 'node_cpu_seconds_total\\{.*mode=\"idle\".*\\}'")
		return true
	}
	re, err := regexp.Compile(arg)
	if err != nil {
		fmt.Printf("Invalid regex: %v\n", err)
		return true
	}
	removed := 0
	for name, samples := range storage.Metrics {
		kept := samples[:0]
		for _, s := range samples {
			if re.MatchString(seriesSignature(name, s.Labels)) {
				kept = append(kept, s)
			} else {
				removed++
			}
		}
		if len(kept) == 0 {
			delete(storage.Metrics, name)
		} else {
			storage.Metrics[name] = kept
		}
	}
	totalMetrics, totalSamples := storeTotals(storage)
	fmt.Printf("Kept regex %q (removed %d samples; now: %d metrics, %d samples)\n", arg, removed, totalMetrics, totalSamples)
	if refreshMetricsCache != nil {
		refreshMetricsCache(storage)
	}
	return true
}

// handleAdhocRename handles the .rename command
func handleAdhocRename(query string, storage *sstorage.SimpleStorage) bool {
	args := strings.Fields(strings.TrimSpace(strings.TrimPrefix(query, ".rename")))
	if len(args) != 2 {
		fmt.Println("Usage: .rename <old_metric> <new_metric>")
		fmt.Println("Example: .rename http_requests_total http_requests")
		return true
	}
	oldName := args[0]
	newName := args[1]
	if err := storage.RenameMetric(oldName, newName); err != nil {
		fmt.Printf("Error: %v\n", err)
		return true
	}
	count := len(storage.Metrics[newName])
	fmt.Printf("Renamed %q to %q (%d samples)\n", oldName, newName, count)
	if refreshMetricsCache != nil {
		refreshMetricsCache(storage)
	}
	return true
}

// .rules command: shows or sets active rules (directory, glob, or single file)
func handleAdhocRules(query string, storage *sstorage.SimpleStorage) bool {
	trim := strings.TrimSpace(query)
	args := strings.Fields(trim)
	if len(args) == 1 { // show
		spec, files := GetActiveRules()
		if len(files) == 0 {
			fmt.Println("Rules: none")
			return true
		}
		fmt.Printf("Rules (%d): spec=%q\n", len(files), spec)
		for _, f := range files {
			fmt.Printf("  - %s\n", f)
		}
		// List recording rule names
		recs := GetRecordingRuleNames()
		if len(recs) > 0 {
			fmt.Printf("Recording rules (%d):\n", len(recs))
			sort.Strings(recs)
			for _, n := range recs {
				fmt.Printf("  - %s\n", n)
			}
		}
		return true
	}
	// set
	spec := strings.TrimSpace(strings.Trim(args[1], "\"'"))
	files, err := ResolveRuleSpec(spec)
	if err != nil {
		fmt.Printf(".rules: %v\n", err)
		return true
	}
	SetActiveRules(files, spec)
	fmt.Printf("Rules set: %d file(s) from %q\n", len(files), spec)
	// Optionally evaluate immediately to populate store with recordings
	if added, alerts, err := EvaluateActiveRules(storage); err == nil && (added > 0 || alerts > 0) {
		fmt.Printf("Rules: added %d samples; %d alerts\n", added, alerts)
		if refreshMetricsCache != nil {
			refreshMetricsCache(storage)
		}
	}
	return true
}

// .alerts command: shows alerting rules from active rule files
func handleAdhocAlerts(_ string, storage *sstorage.SimpleStorage) bool {
	alerts := GetAlertingRules()
	if len(alerts) == 0 {
		fmt.Println("Alerts: none")
		return true
	}
	fmt.Printf("Alerting rules (%d):\n", len(alerts))
	for _, a := range alerts {
		fmt.Printf("  %s: %s\n", a.Name, a.Expr)
	}
	return true
}

// Seed historical samples for a metric
func handleAdhocSeed(query string, storage *sstorage.SimpleStorage) bool {
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
			if _, err := fmt.Sscanf(a, "steps=%d", &steps); err != nil {
				// ignore invalid steps value; keep default
				_ = err
			}
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

	if refreshMetricsCache != nil {
		refreshMetricsCache(storage)
	}
	return true
}
