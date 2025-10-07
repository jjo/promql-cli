package repl

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
	"github.com/prometheus/prometheus/promql"
)

func handleAdhocSave(query string, storage *sstorage.SimpleStorage) bool {
	rest := strings.TrimSpace(strings.TrimPrefix(query, ".save"))
	usage := GetAdHocCommandByName(".save").Usage
	if rest == "" {
		fmt.Println(usage)
		return true
	}
	// Parse path (quoted or unquoted) and optional key=value tokens
	path, args := parsePathAndArgs(rest)
	if path == "" {
		fmt.Println(usage)
		return true
	}
	// Parse optional timestamp and regex
	tsMode, tsFixed, ok := ParseTimestampArg(args)
	if !ok {
		fmt.Println("Invalid timestamp specification. Use: timestamp={now|remove|<timespec>}")
		return true
	}
	re, ok := ParseRegexArg(args)
	if !ok {
		fmt.Println("Invalid regex specification. Use: regex='timeseries regex' (quote if it contains spaces)")
		return true
	}

	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("Failed to open %s for writing: %v\n", path, err)
		return true
	}
	defer func() { _ = f.Close() }()
	opts := sstorage.SaveOptions{TimestampMode: tsMode, FixedTimestamp: tsFixed}
	if re != nil {
		opts.SeriesRegex = re
	}
	if err := storage.SaveToWriterWithOptions(f, opts); err != nil {
		fmt.Printf("Failed to save metrics to %s: %v\n", path, err)
		return true
	}
	fmt.Printf("Saved store to %s\n", path)
	return true
}

// parsePathAndArgs splits first path token (quoted or unquoted) and returns the rest tokens for key=value options.
func parsePathAndArgs(rest string) (string, []string) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", nil
	}
	if rest[0] == '\'' || rest[0] == '"' {
		quote := rest[0]
		i := 1
		for i < len(rest) && rest[i] != quote {
			i++
		}
		if i >= len(rest) {
			// unterminated
			return strings.Trim(rest[1:], " \t\n\r"), nil
		}
		path := rest[1:i]
		remainder := strings.TrimSpace(rest[i+1:])
		var args []string
		if remainder != "" {
			args = strings.Fields(remainder)
		}
		return path, args
	}
	// unquoted
	i := 0
	for i < len(rest) && rest[i] != ' ' && rest[i] != '\t' {
		i++
	}
	path := rest[:i]
	remainder := strings.TrimSpace(rest[i:])
	var args []string
	if remainder != "" {
		args = strings.Fields(remainder)
	}
	return path, args
}

// ParseTimestampArg scans args for timestamp=... and returns (mode, fixedMs, ok)
// mode is one of: keep (default), remove, set
func ParseTimestampArg(args []string) (string, int64, bool) {
	mode := "keep"
	if len(args) == 0 {
		return mode, 0, true
	}
	for _, a := range args {
		if !strings.HasPrefix(strings.ToLower(a), "timestamp=") {
			continue
		}
		val := strings.TrimSpace(a[len("timestamp="):])
		val = strings.Trim(val, " \"'")
		if strings.EqualFold(val, "remove") {
			return "remove", 0, true
		}
		if strings.EqualFold(val, "now") {
			return "set", time.Now().UnixMilli(), true
		}
		// timespec parsed via parseEvalTime (supports now+/-dur, rfc3339, unix)
		t, err := parseEvalTime(val)
		if err != nil {
			return "keep", 0, false
		}
		return "set", t.UnixMilli(), true
	}
	return mode, 0, true
}

// ParseRegexArg finds regex=... and returns a compiled regexp (or nil if absent).
func ParseRegexArg(args []string) (*regexp.Regexp, bool) {
	for _, a := range args {
		if !strings.HasPrefix(strings.ToLower(a), "regex=") {
			continue
		}
		val := strings.TrimSpace(a[len("regex="):])
		val = strings.Trim(val, " \"'")
		if val == "" {
			return nil, false
		}
		re, err := regexp.Compile(val)
		if err != nil {
			return nil, false
		}
		return re, true
	}
	return nil, true
}

// calculateTimestampOffset calculates the offset needed to align the latest timestamp to the target.
// It examines samples in the given range and returns (offset, hasData).
// For "set" mode: offset = target - latest_timestamp
// For other modes: offset = 0
func calculateTimestampOffset(storage *sstorage.SimpleStorage, beforeCounts map[string]int, mode string, target int64) (int64, bool) {
	if mode != "set" {
		return 0, false
	}

	// Find the latest timestamp among samples in range
	var latestTimestamp int64
	hasSamples := false
	for name, samples := range storage.Metrics {
		start := beforeCounts[name]
		if start < 0 || start > len(samples) {
			start = 0
		}
		for i := start; i < len(samples); i++ {
			if !hasSamples || storage.Metrics[name][i].Timestamp > latestTimestamp {
				latestTimestamp = storage.Metrics[name][i].Timestamp
				hasSamples = true
			}
		}
	}

	if !hasSamples {
		return 0, false
	}

	return target - latestTimestamp, true
}

// ApplyTimestampOverride updates only newly loaded samples to the given mode
func ApplyTimestampOverride(storage *sstorage.SimpleStorage, beforeCounts map[string]int, mode string, fixed int64) {
	// Calculate offset if needed
	offset, _ := calculateTimestampOffset(storage, beforeCounts, mode, fixed)

	// Apply timestamp updates
	for name, samples := range storage.Metrics {
		start := beforeCounts[name]
		if start < 0 || start > len(samples) {
			start = 0
		}
		for i := start; i < len(samples); i++ {
			switch mode {
			case "remove":
				// set a uniform timestamp (current time) for all samples when 'remove' mode is used
				storage.Metrics[name][i].Timestamp = time.Now().UnixMilli()
			case "set":
				// offset all timestamps so that the latest one aligns with the target
				storage.Metrics[name][i].Timestamp += offset
			}
		}
	}
}

// ApplyFilteredLoad loads samples from tmp storage into target storage, applying regex filter and timestamp overrides.
// This is used when loading metrics with a regex filter from CLI or REPL commands.
func ApplyFilteredLoad(storage *sstorage.SimpleStorage, tmp *sstorage.SimpleStorage, re *regexp.Regexp, tsMode string, tsFixed int64) {
	if re == nil {
		return
	}

	// Find the latest timestamp in temp storage (for offset calculation)
	var latestTimestamp int64
	hasMatchingSamples := false
	for name, samples := range tmp.Metrics {
		for _, s := range samples {
			seriesSig := seriesSignature(name, s.Labels)
			if re.MatchString(seriesSig) {
				if !hasMatchingSamples || s.Timestamp > latestTimestamp {
					latestTimestamp = s.Timestamp
					hasMatchingSamples = true
				}
			}
		}
	}

	// Calculate offset if in "set" mode
	var offset int64
	if tsMode == "set" && hasMatchingSamples {
		offset = tsFixed - latestTimestamp
	}

	// Apply samples with timestamp adjustments
	for name, samples := range tmp.Metrics {
		for _, s := range samples {
			seriesSig := seriesSignature(name, s.Labels)
			if re.MatchString(seriesSig) {
				ts := s.Timestamp
				switch tsMode {
				case "remove":
					ts = time.Now().UnixMilli()
				case "set":
					// offset all timestamps so that the latest one aligns with the target
					ts += offset
				}
				storage.AddSample(s.Labels, s.Value, ts)
			}
		}
	}
}

// seriesSignature builds name{labels} (labels sorted, quoted) signature for regex matching.
func seriesSignature(name string, lbls map[string]string) string {
	// Exclude __name__
	keys := make([]string, 0, len(lbls))
	for k := range lbls {
		if k == "__name__" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := lbls[k]
		v = strings.ReplaceAll(v, "\\", "\\\\")
		v = strings.ReplaceAll(v, "\n", "\\n")
		v = strings.ReplaceAll(v, "\t", "\\t")
		v = strings.ReplaceAll(v, "\"", "\\\"")
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", k, v))
	}
	if len(parts) == 0 {
		return name
	}
	return fmt.Sprintf("%s{%s}", name, strings.Join(parts, ","))
}

// ExecuteQueriesFromFile reads and executes PromQL expressions from a file
// This is exported for use by the CLI -f flag
// Queries are separated by blank lines, EOF is treated as query terminator
// Supports backslash continuation within queries
func ExecuteQueriesFromFile(engine *promql.Engine, storage *sstorage.SimpleStorage, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	queries := parseQueriesFromContent(string(data))

	if len(queries) == 0 {
		fmt.Printf("No expressions found in %s\n", path)
		return nil
	}

	// Execute each query
	for _, q := range queries {
		fmt.Printf("> %s\n", q.query)
		ExecuteQueryLine(engine, storage, q.query)
	}

	return nil
}

// queryWithLineNum tracks a query and its starting line number for error reporting
type queryWithLineNum struct {
	query     string
	startLine int
}

// parseQueriesFromContent parses multi-line queries from file content
// Adhoc commands (starting with .) are processed on first newline
// PromQL queries are separated by blank lines, backslash continues lines
func parseQueriesFromContent(content string) []queryWithLineNum {
	var queries []queryWithLineNum
	var currentLines []string
	var startLine int
	lineNum := 0
	inContinuation := false

	for _, rawLine := range strings.Split(content, "\n") {
		lineNum++
		line := strings.TrimRight(rawLine, "\r")

		// Start new query if this is the first non-comment, non-empty line
		if len(currentLines) == 0 && !inContinuation {
			startLine = lineNum
		}

		// Handle comments - skip but don't break query accumulation
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}

		// Check for backslash continuation (before trimming)
		trimmedRight := strings.TrimRight(line, " \t")
		hasBackslash := strings.HasSuffix(trimmedRight, "\\") && !strings.HasSuffix(trimmedRight, "\\\\")

		if hasBackslash {
			// Remove backslash and accumulate
			part := strings.TrimSuffix(trimmedRight, "\\")
			if part != "" {
				currentLines = append(currentLines, part)
			}
			inContinuation = true
			continue
		}

		// Not a continuation line
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			// Blank line - end current query if any
			if len(currentLines) > 0 {
				query := strings.Join(currentLines, " ")
				queries = append(queries, queryWithLineNum{query: query, startLine: startLine})
				currentLines = nil
				inContinuation = false
			}
			continue
		}

		// Check if this is an adhoc command (starts with .)
		// Adhoc commands should be processed immediately, not accumulated
		if strings.HasPrefix(trimmed, ".") {
			// First, flush any accumulated query
			if len(currentLines) > 0 {
				query := strings.Join(currentLines, " ")
				queries = append(queries, queryWithLineNum{query: query, startLine: startLine})
				currentLines = nil
			}
			// Then add the adhoc command immediately without requiring blank line
			queries = append(queries, queryWithLineNum{query: trimmed, startLine: lineNum})
			inContinuation = false
			continue
		}

		// Regular line - add to current query
		currentLines = append(currentLines, trimmed)
		inContinuation = false
	}

	// Handle EOF - treat as query terminator if we have accumulated lines
	if len(currentLines) > 0 {
		query := strings.Join(currentLines, " ")
		queries = append(queries, queryWithLineNum{query: query, startLine: startLine})
	}

	return queries
}

func handleAdhocSource(query string, storage *sstorage.SimpleStorage) bool {
	rest := strings.TrimSpace(strings.TrimPrefix(query, ".source"))
	usage := GetAdHocCommandByName(".source").Usage
	if rest == "" {
		fmt.Println(usage)
		return true
	}
	path, _ := parsePathAndArgs(rest)
	if path == "" {
		fmt.Println(usage)
		return true
	}

	// Check if replEngine is set
	if replEngine == nil {
		fmt.Println("Error: PromQL engine not available")
		return true
	}

	// Use the exported function
	if err := ExecuteQueriesFromFile(replEngine, storage, path); err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	return true
}

func handleAdhocLoad(query string, storage *sstorage.SimpleStorage) bool {
	rest := strings.TrimSpace(strings.TrimPrefix(query, ".load"))
	usage := GetAdHocCommandByName(".load").Usage
	if rest == "" {
		fmt.Println(usage)
		return true
	}
	path, args := parsePathAndArgs(rest)
	if path == "" {
		fmt.Println(usage)
		return true
	}
	// capture per-metric counts to adjust only newly loaded samples when overriding timestamps
	beforeCounts := make(map[string]int)
	for name, ss := range storage.Metrics {
		beforeCounts[name] = len(ss)
	}

	f, err := os.Open(path)
	if err != nil {
		fmt.Printf("Failed to open %s: %v\n", path, err)
		return true
	}
	defer func() { _ = f.Close() }()
	beforeMetrics := len(storage.Metrics)
	beforeSamples := 0
	for _, ss := range storage.Metrics {
		beforeSamples += len(ss)
	}
	// Parse optional timestamp and regex
	tsMode, tsFixed, ok := ParseTimestampArg(args)
	if !ok {
		fmt.Println("Invalid timestamp specification. Use: timestamp={now|remove|<timespec>}")
		return true
	}
	re, ok := ParseRegexArg(args)
	if !ok {
		fmt.Println("Invalid regex specification. Use: regex='timeseries regex' (quote if it contains spaces)")
		return true
	}
	if re == nil {
		if err := storage.LoadFromReader(f); err != nil {
			fmt.Printf("Failed to load metrics from %s: %v\n", path, err)
			return true
		}
		if tsMode != "keep" {
			ApplyTimestampOverride(storage, beforeCounts, tsMode, tsFixed)
		}
	} else {
		// Load into temp storage and merge matching series only
		tmp := sstorage.NewSimpleStorage()
		if err := tmp.LoadFromReader(f); err != nil {
			fmt.Printf("Failed to load metrics from %s: %v\n", path, err)
			return true
		}

		// Find the latest timestamp in temp storage (for offset calculation)
		var latestTimestamp int64
		hasMatchingSamples := false
		for name, samples := range tmp.Metrics {
			for _, s := range samples {
				seriesSig := seriesSignature(name, s.Labels)
				if re.MatchString(seriesSig) {
					if !hasMatchingSamples || s.Timestamp > latestTimestamp {
						latestTimestamp = s.Timestamp
						hasMatchingSamples = true
					}
				}
			}
		}

		// Calculate offset if in "set" mode
		var offset int64
		if tsMode == "set" && hasMatchingSamples {
			offset = tsFixed - latestTimestamp
		}

		// Apply samples with timestamp adjustments
		for name, samples := range tmp.Metrics {
			for _, s := range samples {
				seriesSig := seriesSignature(name, s.Labels)
				if re.MatchString(seriesSig) {
					ts := s.Timestamp
					switch tsMode {
					case "remove":
						ts = time.Now().UnixMilli()
					case "set":
						// offset all timestamps so that the latest one aligns with the target
						ts += offset
					}
					storage.AddSample(s.Labels, s.Value, ts)
				}
			}
		}
	}

	afterMetrics, afterSamples := storeTotals(storage)
	fmt.Printf("Loaded %s: +%d metrics, +%d samples (total: %d metrics, %d samples)\n", path, afterMetrics-beforeMetrics, afterSamples-beforeSamples, afterMetrics, afterSamples)

	// Evaluate active rules after TSDB update
	if added, alerts, err := EvaluateActiveRules(storage); err != nil {
		fmt.Printf("Rules evaluation failed: %v\n", err)
	} else if added > 0 || alerts > 0 {
		fmt.Printf("Rules: added %d samples; %d alerts\n", added, alerts)
	}

	// Refresh metrics cache for autocompletion if using prompt backend (again, to include recorded series)
	if refreshMetricsCache != nil {
		refreshMetricsCache(storage)
	}

	return true
}
