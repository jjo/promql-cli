package repl

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

func handleAdhocSave(query string, storage *sstorage.SimpleStorage) bool {
	rest := strings.TrimSpace(strings.TrimPrefix(query, ".save"))
	if rest == "" {
fmt.Println("Usage: .save <file.prom> [timestamp={now|remove|<timespec>}] [regex='<series regex>']")
		return true
	}
	// Parse path (quoted or unquoted) and optional key=value tokens
	path, args := parsePathAndArgs(rest)
	if path == "" {
fmt.Println("Usage: .save <file.prom> [timestamp={now|remove|<timespec>}] [regex='<series regex>']")
		return true
	}
	// Parse optional timestamp and regex
	tsMode, tsFixed, ok := parseTimestampArg(args)
	if !ok {
		fmt.Println("Invalid timestamp specification. Use: timestamp={now|remove|<timespec>}")
		return true
	}
	re, ok := parseRegexArg(args)
	if !ok {
		fmt.Println("Invalid regex specification. Use: regex='timeseries regex' (quote if it contains spaces)")
		return true
	}

	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("Failed to open %s for writing: %v\n", path, err)
		return true
	}
	defer f.Close()
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

// parseTimestampArg scans args for timestamp=... and returns (mode, fixedMs, ok)
// mode is one of: keep (default), remove, set
func parseTimestampArg(args []string) (string, int64, bool) {
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

// parseRegexArg finds regex=... and returns a compiled regexp (or nil if absent).
func parseRegexArg(args []string) (*regexp.Regexp, bool) {
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

// applyTimestampOverride updates only newly loaded samples to the given mode
func applyTimestampOverride(storage *sstorage.SimpleStorage, beforeCounts map[string]int, mode string, fixed int64) {
	for name, samples := range storage.Metrics {
		start := beforeCounts[name]
		if start < 0 || start > len(samples) {
			start = 0
		}
		for i := start; i < len(samples); i++ {
			if mode == "remove" {
				// set a uniform timestamp (current time) for all samples when 'remove' mode is used
				storage.Metrics[name][i].Timestamp = time.Now().UnixMilli()
			} else if mode == "set" {
				storage.Metrics[name][i].Timestamp = fixed
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

func handleAdhocLoad(query string, storage *sstorage.SimpleStorage) bool {
	rest := strings.TrimSpace(strings.TrimPrefix(query, ".load"))
	if rest == "" {
fmt.Println("Usage: .load <file.prom> [timestamp={now|remove|<timespec>}] [regex='<series regex>']")
		return true
	}
	path, args := parsePathAndArgs(rest)
	if path == "" {
fmt.Println("Usage: .load <file.prom> [timestamp={now|remove|<timespec>}] [regex='<series regex>']")
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
	defer f.Close()
	beforeMetrics := len(storage.Metrics)
	beforeSamples := 0
	for _, ss := range storage.Metrics {
		beforeSamples += len(ss)
	}
	// Parse optional timestamp and regex
	tsMode, tsFixed, ok := parseTimestampArg(args)
	if !ok {
		fmt.Println("Invalid timestamp specification. Use: timestamp={now|remove|<timespec>}")
		return true
	}
	re, ok := parseRegexArg(args)
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
			applyTimestampOverride(storage, beforeCounts, tsMode, tsFixed)
		}
	} else {
		// Load into temp storage and merge matching series only
		tmp := sstorage.NewSimpleStorage()
		if err := tmp.LoadFromReader(f); err != nil {
			fmt.Printf("Failed to load metrics from %s: %v\n", path, err)
			return true
		}
		for name, samples := range tmp.Metrics {
			for _, s := range samples {
				seriesSig := seriesSignature(name, s.Labels)
				if re.MatchString(seriesSig) {
					ts := s.Timestamp
					if tsMode == "remove" {
						ts = time.Now().UnixMilli()
					} else if tsMode == "set" {
						ts = tsFixed
					}
					storage.AddSample(s.Labels, s.Value, ts)
				}
			}
		}
	}

	afterMetrics, afterSamples := storeTotals(storage)
	fmt.Printf("Loaded %s: +%d metrics, +%d samples (total: %d metrics, %d samples)\n", path, afterMetrics-beforeMetrics, afterSamples-beforeSamples, afterMetrics, afterSamples)

	// Refresh metrics cache for autocompletion if using prompt backend
	if refreshMetricsCache != nil {
		refreshMetricsCache(storage)
	}

	return true
}
