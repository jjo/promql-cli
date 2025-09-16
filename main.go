package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/chzyer/readline"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/prometheus/prometheus/util/annotations"
)

func init() {
	// Initialize validation scheme to avoid panics
	model.NameValidationScheme = model.UTF8Validation
}

// SimpleStorage holds metrics in a simple format for querying
type SimpleStorage struct {
	metrics map[string][]MetricSample
}

// MetricSample represents a single metric sample
type MetricSample struct {
	Labels    map[string]string
	Value     float64
	Timestamp int64
}

// NewSimpleStorage creates a new simple storage
func NewSimpleStorage() *SimpleStorage {
	return &SimpleStorage{
		metrics: make(map[string][]MetricSample),
	}
}

// LoadFromReader loads Prometheus exposition format data using the official Prometheus parser
func (s *SimpleStorage) LoadFromReader(reader io.Reader) error {
	// Use the standard Prometheus exposition format parser
	parser := expfmt.NewTextParser(model.UTF8Validation)
	metricFamilies, err := parser.TextToMetricFamilies(reader)
	if err != nil {
		return fmt.Errorf("failed to parse metrics with Prometheus parser: %w", err)
	}

	// Process the parsed metric families
	return s.processMetricFamilies(metricFamilies)
}

// processMetricFamilies processes the parsed metric families (extracted from original LoadFromReader)
func (s *SimpleStorage) processMetricFamilies(metricFamilies map[string]*dto.MetricFamily) error {
	// Use a consistent base timestamp for all samples loaded in this call
	baseTimestamp := time.Now().UnixMilli()

	// Convert each metric family to individual samples
	for _, mf := range metricFamilies {
		metricName := mf.GetName()

		// Process each metric within the family
		for _, metric := range mf.GetMetric() {
			// Create labels map starting with the metric name
			lbls := make(map[string]string)
			lbls["__name__"] = metricName

			// Add all labels from the metric to our labels map
			for _, labelPair := range metric.GetLabel() {
				lbls[labelPair.GetName()] = labelPair.GetValue()
			}

			// Get value based on metric type
			var value float64
			// Always use the consistent base timestamp to keep samples within lookback
			timestamp := baseTimestamp

			switch mf.GetType() {
			case dto.MetricType_COUNTER:
				if metric.Counter != nil {
					value = metric.Counter.GetValue()
				}
			case dto.MetricType_GAUGE:
				if metric.Gauge != nil {
					value = metric.Gauge.GetValue()
				}
			case dto.MetricType_UNTYPED:
				// Handle untyped metrics - treat as gauge-like
				if metric.Untyped != nil {
					value = metric.Untyped.GetValue()
				}
			case dto.MetricType_HISTOGRAM:
				if metric.Histogram != nil {
					// Store histogram buckets as separate metrics
					for _, bucket := range metric.Histogram.GetBucket() {
						bucketLabels := make(map[string]string)
						for k, v := range lbls {
							bucketLabels[k] = v
						}
						bucketLabels["le"] = fmt.Sprintf("%g", bucket.GetUpperBound())
						bucketLabels["__name__"] = metricName + "_bucket"

						bucketSample := MetricSample{
							Labels:    bucketLabels,
							Value:     float64(bucket.GetCumulativeCount()),
							Timestamp: timestamp,
						}
						s.metrics[metricName+"_bucket"] = append(s.metrics[metricName+"_bucket"], bucketSample)
					}

					// Store histogram sum
					sumLabels := make(map[string]string)
					for k, v := range lbls {
						sumLabels[k] = v
					}
					sumLabels["__name__"] = metricName + "_sum"
					sumSample := MetricSample{
						Labels:    sumLabels,
						Value:     metric.Histogram.GetSampleSum(),
						Timestamp: timestamp,
					}
					s.metrics[metricName+"_sum"] = append(s.metrics[metricName+"_sum"], sumSample)

					// Store histogram count
					countLabels := make(map[string]string)
					for k, v := range lbls {
						countLabels[k] = v
					}
					countLabels["__name__"] = metricName + "_count"
					countSample := MetricSample{
						Labels:    countLabels,
						Value:     float64(metric.Histogram.GetSampleCount()),
						Timestamp: timestamp,
					}
					s.metrics[metricName+"_count"] = append(s.metrics[metricName+"_count"], countSample)
					continue
				}
			case dto.MetricType_SUMMARY:
				if metric.Summary != nil {
					// Store summary quantiles
					for _, quantile := range metric.Summary.GetQuantile() {
						quantileLabels := make(map[string]string)
						for k, v := range lbls {
							quantileLabels[k] = v
						}
						quantileLabels["quantile"] = fmt.Sprintf("%g", quantile.GetQuantile())

						quantileSample := MetricSample{
							Labels:    quantileLabels,
							Value:     quantile.GetValue(),
							Timestamp: timestamp,
						}
						s.metrics[metricName] = append(s.metrics[metricName], quantileSample)
					}

					// Store summary sum
					sumLabels := make(map[string]string)
					for k, v := range lbls {
						sumLabels[k] = v
					}
					sumLabels["__name__"] = metricName + "_sum"
					sumSample := MetricSample{
						Labels:    sumLabels,
						Value:     metric.Summary.GetSampleSum(),
						Timestamp: timestamp,
					}
					s.metrics[metricName+"_sum"] = append(s.metrics[metricName+"_sum"], sumSample)

					// Store summary count
					countLabels := make(map[string]string)
					for k, v := range lbls {
						countLabels[k] = v
					}
					countLabels["__name__"] = metricName + "_count"
					countSample := MetricSample{
						Labels:    countLabels,
						Value:     float64(metric.Summary.GetSampleCount()),
						Timestamp: timestamp,
					}
					s.metrics[metricName+"_count"] = append(s.metrics[metricName+"_count"], countSample)
					continue
				}
			default:
				continue
			}

			// Create and store the primary sample
			sample := MetricSample{
				Labels:    lbls,
				Value:     value,
				Timestamp: timestamp,
			}

			s.metrics[metricName] = append(s.metrics[metricName], sample)
		}
	}

	return nil
}

// Queryable implementation for SimpleStorage
func (s *SimpleStorage) Querier(mint, maxt int64) (storage.Querier, error) {
	return &SimpleQuerier{storage: s, mint: mint, maxt: maxt}, nil
}

// SimpleQuerier implements storage.Querier
type SimpleQuerier struct {
	storage    *SimpleStorage
	mint, maxt int64
}

func (q *SimpleQuerier) Select(ctx context.Context, sortSeries bool, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	var series []storage.Series

	// Find matching metrics
	for _, samples := range q.storage.metrics {
		// Check if any samples match the matchers and time range
		var matchingSamples []MetricSample
		for _, sample := range samples {
			// Check time range
			if sample.Timestamp < q.mint || sample.Timestamp > q.maxt {
				continue
			}

			// Check label matchers
			if q.matchesLabelMatchers(sample.Labels, matchers) {
				matchingSamples = append(matchingSamples, sample)
			}
		}

		// Group samples by unique label sets
		labelGroups := make(map[string][]MetricSample)
		for _, sample := range matchingSamples {
			key := q.getLabelKey(sample.Labels)
			labelGroups[key] = append(labelGroups[key], sample)
		}

		// Create a series for each unique label set
		for _, groupSamples := range labelGroups {
			if len(groupSamples) > 0 {
				lbls := labels.FromMap(groupSamples[0].Labels)
				series = append(series, &SimpleSeries{
					labels:  lbls,
					samples: groupSamples,
				})
			}
		}
	}

	return &SimpleSeriesSet{series: series, index: -1}
}

func (q *SimpleQuerier) LabelValues(ctx context.Context, name string, hints *storage.LabelHints, matchers ...*labels.Matcher) ([]string, annotations.Annotations, error) {
	values := make(map[string]struct{})
	for _, samples := range q.storage.metrics {
		for _, sample := range samples {
			if q.matchesLabelMatchers(sample.Labels, matchers) {
				if value, ok := sample.Labels[name]; ok {
					values[value] = struct{}{}
				}
			}
		}
	}

	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return result, nil, nil
}

func (q *SimpleQuerier) LabelNames(ctx context.Context, hints *storage.LabelHints, matchers ...*labels.Matcher) ([]string, annotations.Annotations, error) {
	names := make(map[string]struct{})
	for _, samples := range q.storage.metrics {
		for _, sample := range samples {
			if q.matchesLabelMatchers(sample.Labels, matchers) {
				for name := range sample.Labels {
					names[name] = struct{}{}
				}
			}
		}
	}

	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	return result, nil, nil
}

func (q *SimpleQuerier) Close() error {
	return nil
}

// matchesLabelMatchers checks if labels match the given matchers
func (q *SimpleQuerier) matchesLabelMatchers(sampleLabels map[string]string, matchers []*labels.Matcher) bool {
	for _, matcher := range matchers {
		value, exists := sampleLabels[matcher.Name]
		if !exists {
			value = ""
		}
		if !matcher.Matches(value) {
			return false
		}
	}
	return true
}

// getLabelKey creates a unique key for a label set
func (q *SimpleQuerier) getLabelKey(labels map[string]string) string {
	var parts []string
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ",")
}

// SimpleSeries implements storage.Series
type SimpleSeries struct {
	labels  labels.Labels
	samples []MetricSample
}

func (s *SimpleSeries) Labels() labels.Labels {
	return s.labels
}

func (s *SimpleSeries) Iterator(iterator chunkenc.Iterator) chunkenc.Iterator {
	return &SimpleIterator{samples: s.samples, index: -1}
}

// SimpleIterator implements chunkenc.Iterator
type SimpleIterator struct {
	samples []MetricSample
	index   int
}

func (it *SimpleIterator) Next() chunkenc.ValueType {
	it.index++
	if it.index >= len(it.samples) {
		return chunkenc.ValNone
	}
	return chunkenc.ValFloat
}

func (it *SimpleIterator) Seek(t int64) chunkenc.ValueType {
	for i, sample := range it.samples {
		if sample.Timestamp >= t {
			it.index = i
			return chunkenc.ValFloat
		}
	}
	it.index = len(it.samples)
	return chunkenc.ValNone
}

func (it *SimpleIterator) At() (int64, float64) {
	if it.index < 0 || it.index >= len(it.samples) {
		return 0, 0
	}
	sample := it.samples[it.index]
	return sample.Timestamp, sample.Value
}

func (it *SimpleIterator) AtHistogram(*histogram.Histogram) (int64, *histogram.Histogram) {
	return 0, nil
}

func (it *SimpleIterator) AtFloatHistogram(*histogram.FloatHistogram) (int64, *histogram.FloatHistogram) {
	return 0, nil
}

func (it *SimpleIterator) AtT() int64 {
	if it.index < 0 || it.index >= len(it.samples) {
		return 0
	}
	return it.samples[it.index].Timestamp
}

func (it *SimpleIterator) Err() error {
	return nil
}

// SimpleSeriesSet implements storage.SeriesSet
type SimpleSeriesSet struct {
	series []storage.Series
	index  int
	err    error
}

func (s *SimpleSeriesSet) Next() bool {
	s.index++
	return s.index < len(s.series)
}

func (s *SimpleSeriesSet) At() storage.Series {
	if s.index < 0 || s.index >= len(s.series) {
		return nil
	}
	return s.series[s.index]
}

func (s *SimpleSeriesSet) Err() error {
	return s.err
}

func (s *SimpleSeriesSet) Warnings() annotations.Annotations {
	return nil
}

// main is the entry point of the application.
// It provides a command-line interface for loading metrics and executing PromQL queries.
func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage:")
		fmt.Println("  Load metrics: go run main.go load <file.prom>")
		fmt.Println("  Interactive query: go run main.go query <file.prom>")
		fmt.Println("  Test auto-completion: go run main.go test-completion <file.prom>")
		fmt.Println("")
		fmt.Println("Features:")
		fmt.Println("  - Dynamic auto-completion for metric names, labels, and values")
		fmt.Println("  - Context-aware suggestions based on query position")
		fmt.Println("  - Full PromQL function and operator completion")
		fmt.Println("  - Tab completion similar to Prometheus UI")
		os.Exit(1)
	}

	storage := NewSimpleStorage()

	// Create upstream Prometheus PromQL engine
	engine := promql.NewEngine(promql.EngineOpts{
		Logger:        nil,
		Reg:           nil,
		MaxSamples:    50000000,
		Timeout:       30 * time.Second,
		LookbackDelta: 5 * time.Minute,
		NoStepSubqueryIntervalFn: func(rangeMillis int64) int64 {
			return 60 * 1000 // 60 seconds
		},
	})

	switch os.Args[1] {
	case "load":
		if len(os.Args) < 3 {
			log.Fatal("Please specify a metrics file")
		}

		if err := loadMetricsFromFile(storage, os.Args[2]); err != nil {
			log.Fatalf("Failed to load metrics: %v", err)
		}

		fmt.Printf("Successfully loaded metrics from %s\n", os.Args[2])
		printStorageInfo(storage)

	case "query":
		if len(os.Args) < 3 {
			log.Fatal("Please specify a metrics file")
		}

		if err := loadMetricsFromFile(storage, os.Args[2]); err != nil {
			log.Fatalf("Failed to load metrics: %v", err)
		}

		fmt.Printf("Loaded metrics from %s\n", os.Args[2])
		printStorageInfo(storage)
		fmt.Println()

		runInteractiveQueries(engine, storage)

	case "test-completion":
		if len(os.Args) < 3 {
			log.Fatal("Please specify a metrics file")
		}

		if err := loadMetricsFromFile(storage, os.Args[2]); err != nil {
			log.Fatalf("Failed to load metrics: %v", err)
		}

		testAutoCompletion(storage)

	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

// loadMetricsFromFile loads metrics from a file into the provided storage.
// It handles file opening, reading, and error reporting.
func loadMetricsFromFile(storage *SimpleStorage, filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	return storage.LoadFromReader(file)
}

// printStorageInfo displays a summary of the loaded metrics.
// It shows the total number of metrics and samples, plus examples.
func printStorageInfo(storage *SimpleStorage) {
	totalSamples := 0
	for _, samples := range storage.metrics {
		totalSamples += len(samples)
	}

	fmt.Printf("Storage contains %d metrics with %d total samples\n", len(storage.metrics), totalSamples)

	// Print some example metrics
	count := 0
	for name, samples := range storage.metrics {
		if count >= 5 {
			fmt.Println("...")
			break
		}
		fmt.Printf("  %s (%d samples)\n", name, len(samples))
		count++
	}
}

// runInteractiveQueries starts an interactive query session using readline for enhanced UX.
// It allows users to execute PromQL queries against the loaded metrics with history and completion.
func runInteractiveQueries(engine *promql.Engine, storage *SimpleStorage) {
	fmt.Println("Enter PromQL queries (or 'quit' to exit):")
	fmt.Println("Supported queries:")
	fmt.Println("  - Basic selectors: metric_name, metric_name{label=\"value\"}")
	fmt.Println("  - Aggregations: sum(metric_name), avg(metric_name), count(metric_name), min(metric_name), max(metric_name)")
	fmt.Println("  - Group by: sum(metric_name) by (label)")
	fmt.Println("  - Binary operations: metric_name + 10, metric_name1 * metric_name2")
	fmt.Println("  - Functions: rate(metric_name), increase(metric_name), abs(metric_name)")
	fmt.Println("  - Comparisons: metric_name > 100, metric_name == 0")
	fmt.Println()

	// Configure readline
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "> ",
		HistoryFile:     "/tmp/.inmem-promql_history",
		AutoComplete:    createAutoCompleter(storage),        // Dynamic tab completion
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		fmt.Printf("Warning: Could not initialize readline, falling back to basic input: %v\n", err)
		runBasicInteractiveQueries(engine, storage)
		return
	}
	defer rl.Close()

	for {
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				continue
			} else if err == io.EOF {
				break
			}
			fmt.Printf("Error reading input: %v\n", err)
			break
		}

		query := strings.TrimSpace(line)
		if query == "" {
			continue
		}

		if query == "quit" || query == "exit" {
			break
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		// Execute query using upstream Prometheus engine
		q, err := engine.NewInstantQuery(ctx, storage, nil, query, time.Now())
		if err != nil {
			fmt.Printf("Error creating query: %v\n", err)
			cancel()
			continue
		}

		result := q.Exec(ctx)
		cancel()

		if result.Err != nil {
			fmt.Printf("Error: %v\n", result.Err)
			continue
		}

		// Print results
		printUpstreamQueryResult(result)
	}
}

// PrometheusAutoCompleter provides dynamic auto-completion for PromQL queries
// based on the loaded metrics data, similar to the Prometheus UI experience.
type PrometheusAutoCompleter struct {
	storage *SimpleStorage
}

// NewPrometheusAutoCompleter creates a new auto-completer with access to metric data.
func NewPrometheusAutoCompleter(storage *SimpleStorage) *PrometheusAutoCompleter {
	return &PrometheusAutoCompleter{storage: storage}
}

// Do implements the readline.AutoCompleter interface to provide dynamic completions.
func (pac *PrometheusAutoCompleter) Do(line []rune, pos int) (newLine [][]rune, length int) {
	lineStr := string(line)
	cursorPos := pos

	// Find the current word being typed
	currentWord, _ := pac.getCurrentWord(lineStr, cursorPos)

	// Get completions based on context
	completions := pac.getCompletions(lineStr, cursorPos, currentWord)

	// Convert completions to readline format
	var suggestions [][]rune

	// Check if current word is an exact match to avoid duplication
	hasExactMatch := false
	hasPartialMatches := len(completions) > 0
	for _, completion := range completions {
		if strings.EqualFold(completion, currentWord) {
			hasExactMatch = true
			break
		}
	}
	// If we have an exact match, suggest label selectors
	if hasExactMatch {
		suggestions = append(suggestions, []rune("{"))
		return suggestions, 0
	}

	// If we have multiple partial matches but no exact match, and the current word is long,
	// this likely means we're in a second TAB situation where we should show options instead of duplicating
	if hasPartialMatches && len(completions) > 1 && len(currentWord) > 10 {
		// Return the full completions but use replacement mode to avoid readline panics
		for _, completion := range completions {
			if strings.HasPrefix(strings.ToLower(completion), strings.ToLower(currentWord)) {
				suggestions = append(suggestions, []rune(completion))
			}
		}
		// Use replacement mode with the current word length
		return suggestions, len(currentWord)
	}

	// Use different approaches based on current word length to avoid readline bugs
	if len(currentWord) <= 3 {
		// For short prefixes, use append mode to avoid duplication
		for _, completion := range completions {
			if strings.HasPrefix(strings.ToLower(completion), strings.ToLower(currentWord)) {
				suffix := completion[len(currentWord):]
				suggestions = append(suggestions, []rune(suffix))
			}
		}
		return suggestions, 0
	} else {
		// For longer prefixes, use replacement mode to avoid readline panics
		for _, completion := range completions {
			if strings.HasPrefix(strings.ToLower(completion), strings.ToLower(currentWord)) {
				suggestions = append(suggestions, []rune(completion))
			}
		}
		return suggestions, len(currentWord)
	}
}

// getCurrentWord extracts the word currently being typed at the cursor position.
func (pac *PrometheusAutoCompleter) getCurrentWord(line string, pos int) (string, int) {
	if pos > len(line) {
		pos = len(line)
	}

	// Find the start of the current word
	start := pos
	for start > 0 {
		c := line[start-1]
		// More comprehensive word boundary detection for PromQL
		if isWordBoundary(c) {
			break
		}
		start--
	}

	// Extract the word from start to cursor position
	currentWord := line[start:pos]

	// Debug logging (uncomment for troubleshooting)
	// fmt.Fprintf(os.Stderr, "[DEBUG] Line: '%s', Pos: %d, Start: %d, CurrentWord: '%s'\n",
	//     line, pos, start, currentWord)

	return currentWord, start
}

// isWordBoundary checks if a character is a word boundary for PromQL
func isWordBoundary(c byte) bool {
	return c == ' ' || c == '(' || c == ')' || c == '{' || c == '}' ||
		   c == ',' || c == '=' || c == '!' || c == '~' || c == '"' ||
		   c == '\t' || c == '\n' || c == '+' || c == '-' || c == '*' ||
		   c == '/' || c == '^' || c == '%'
}

// getCompletions returns appropriate completions based on the query context.
func (pac *PrometheusAutoCompleter) getCompletions(line string, pos int, currentWord string) []string {
	// Analyze the context to determine what type of completion to provide
	context := pac.analyzeContext(line, pos)

	switch context.Type {
	case "metric_name":
		return pac.getMetricNameCompletions(currentWord)
	case "label_name":
		return pac.getLabelNameCompletions(context.MetricName, currentWord)
	case "label_value":
		return pac.getLabelValueCompletions(context.MetricName, context.LabelName, currentWord)
	case "function":
		return pac.getFunctionCompletions(currentWord)
	case "operator":
		return pac.getOperatorCompletions(currentWord)
	default:
		// Provide mixed completions when context is unclear
		return pac.getMixedCompletions(currentWord)
	}
}

// QueryContext represents the context of the current query position.
type QueryContext struct {
	Type       string // "metric_name", "label_name", "label_value", "function", "operator"
	MetricName string // The metric name if we're inside label selectors
	LabelName  string // The label name if we're typing a label value
}

// analyzeContext determines what type of completion should be provided based on cursor position.
func (pac *PrometheusAutoCompleter) analyzeContext(line string, pos int) QueryContext {
	if pos > len(line) {
		pos = len(line)
	}

	// Look at the characters around the cursor to determine context
	beforeCursor := line[:pos]
	_ = line[pos:] // afterCursor for future use

	// Check if we're inside label selectors {}
	lastOpenBrace := strings.LastIndex(beforeCursor, "{")
	lastCloseBrace := strings.LastIndex(beforeCursor, "}")

	if lastOpenBrace > lastCloseBrace && lastOpenBrace != -1 {
		// We're inside label selectors
		metricName := pac.extractMetricName(beforeCursor[:lastOpenBrace])

		// Check if we're typing a label value (after =, !=, =~, !~)
		labelValuePattern := regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*(!?[=~])\s*"?[^"]*$`)
		if matches := labelValuePattern.FindStringSubmatch(beforeCursor[lastOpenBrace+1:]); len(matches) > 1 {
			return QueryContext{
				Type:       "label_value",
				MetricName: metricName,
				LabelName:  matches[1],
			}
		}

		// Otherwise, we're typing a label name
		return QueryContext{
			Type:       "label_name",
			MetricName: metricName,
		}
	}

	// Check if we're typing a function
	if strings.HasSuffix(strings.TrimSpace(beforeCursor), "(") {
		return QueryContext{Type: "function"}
	}

	// Check for operators
	operatorPattern := regexp.MustCompile(`[+\-*/^%]\s*$`)
	if operatorPattern.MatchString(beforeCursor) {
		return QueryContext{Type: "operator"}
	}

	// Default to metric name completion
	return QueryContext{Type: "metric_name"}
}

// extractMetricName extracts the metric name from the text before label selectors.
func (pac *PrometheusAutoCompleter) extractMetricName(text string) string {
	// Simple extraction - look for the last word that could be a metric name
	text = strings.TrimSpace(text)
	words := strings.Fields(text)
	if len(words) > 0 {
		lastWord := words[len(words)-1]
		// Remove any function calls or operators
		metricPattern := regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_:]*)$`)
		if matches := metricPattern.FindStringSubmatch(lastWord); len(matches) > 1 {
			return matches[1]
		}
	}
	return ""
}

// getMetricNameCompletions returns metric names that match the current input.
func (pac *PrometheusAutoCompleter) getMetricNameCompletions(prefix string) []string {
	var completions []string

	for metricName := range pac.storage.metrics {
		if strings.HasPrefix(strings.ToLower(metricName), strings.ToLower(prefix)) {
			completions = append(completions, metricName)
		}
	}

	sort.Strings(completions)
	return completions
}

// getLabelNameCompletions returns label names for a specific metric.
func (pac *PrometheusAutoCompleter) getLabelNameCompletions(metricName, prefix string) []string {
	labelNames := make(map[string]bool)

	// If no specific metric, get labels from all metrics
	metricsToCheck := make(map[string][]MetricSample)
	if metricName != "" && pac.storage.metrics[metricName] != nil {
		metricsToCheck[metricName] = pac.storage.metrics[metricName]
	} else {
		metricsToCheck = pac.storage.metrics
	}

	for _, samples := range metricsToCheck {
		for _, sample := range samples {
			for labelName := range sample.Labels {
				if labelName != "__name__" && strings.HasPrefix(strings.ToLower(labelName), strings.ToLower(prefix)) {
					labelNames[labelName] = true
				}
			}
		}
	}

	var completions []string
	for labelName := range labelNames {
		completions = append(completions, labelName)
	}

	sort.Strings(completions)
	return completions
}

// getLabelValueCompletions returns label values for a specific metric and label name.
func (pac *PrometheusAutoCompleter) getLabelValueCompletions(metricName, labelName, prefix string) []string {
	labelValues := make(map[string]bool)

	// If no specific metric, get values from all metrics
	metricsToCheck := make(map[string][]MetricSample)
	if metricName != "" && pac.storage.metrics[metricName] != nil {
		metricsToCheck[metricName] = pac.storage.metrics[metricName]
	} else {
		metricsToCheck = pac.storage.metrics
	}

	for _, samples := range metricsToCheck {
		for _, sample := range samples {
			if value, exists := sample.Labels[labelName]; exists {
				if strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
					labelValues[`"`+value+`"`] = true // Add quotes for label values
				}
			}
		}
	}

	var completions []string
	for labelValue := range labelValues {
		completions = append(completions, labelValue)
	}

	sort.Strings(completions)
	return completions
}

// getFunctionCompletions returns PromQL function names.
func (pac *PrometheusAutoCompleter) getFunctionCompletions(prefix string) []string {
	functions := []string{
		// Aggregation functions
		"sum", "min", "max", "avg", "group", "stddev", "stdvar", "count", "count_values",
		"bottomk", "topk", "quantile",
		// Math functions
		"abs", "absent", "absent_over_time", "ceil", "clamp", "clamp_max", "clamp_min",
		"day_of_month", "day_of_week", "days_in_month", "exp", "floor", "hour",
		"idelta", "increase", "irate", "ln", "log10", "log2", "minute", "month",
		"predict_linear", "rate", "round", "scalar", "sgn", "sort", "sort_desc",
		"sqrt", "time", "timestamp", "vector", "year",
		// String functions
		"label_join", "label_replace",
		// Time functions
		"deriv", "holt_winters", "delta", "changes", "resets",
	}

	var completions []string
	for _, fn := range functions {
		if strings.HasPrefix(strings.ToLower(fn), strings.ToLower(prefix)) {
			completions = append(completions, fn+"(")
		}
	}

	return completions
}

// getOperatorCompletions returns PromQL operators.
func (pac *PrometheusAutoCompleter) getOperatorCompletions(prefix string) []string {
	operators := []string{
		"and", "or", "unless", "by", "without", "on", "ignoring", "group_left", "group_right",
		"bool", "offset",
	}

	var completions []string
	for _, op := range operators {
		if strings.HasPrefix(strings.ToLower(op), strings.ToLower(prefix)) {
			completions = append(completions, op)
		}
	}

	return completions
}

// getMixedCompletions provides a mix of all completion types when context is unclear.
func (pac *PrometheusAutoCompleter) getMixedCompletions(prefix string) []string {
	var completions []string

	// Add metric names
	completions = append(completions, pac.getMetricNameCompletions(prefix)...)

	// Add functions
	completions = append(completions, pac.getFunctionCompletions(prefix)...)

	// Add operators
	completions = append(completions, pac.getOperatorCompletions(prefix)...)

	// Add common keywords
	keywords := []string{"quit", "exit"}
	for _, keyword := range keywords {
		if strings.HasPrefix(strings.ToLower(keyword), strings.ToLower(prefix)) {
			completions = append(completions, keyword)
		}
	}

	sort.Strings(completions)
	return completions
}

// createAutoCompleter creates the enhanced auto-completer with metric awareness.
// This provides a Prometheus UI-like experience with dynamic completions.
func createAutoCompleter(storage *SimpleStorage) readline.AutoCompleter {
	return NewPrometheusAutoCompleter(storage)
}

// runBasicInteractiveQueries provides a fallback when readline is unavailable
func runBasicInteractiveQueries(engine *promql.Engine, storage *SimpleStorage) {
	fmt.Println("Using basic input mode (readline unavailable)")

	for {
		fmt.Print("> ")
		var query string
		_, err := fmt.Scanln(&query)
		if err != nil {
			if err.Error() == "unexpected newline" {
				continue
			}
			break
		}

		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}

		if query == "quit" || query == "exit" {
			break
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		// Execute query using upstream Prometheus engine
		q, err := engine.NewInstantQuery(ctx, storage, nil, query, time.Now())
		if err != nil {
			fmt.Printf("Error creating query: %v\n", err)
			cancel()
			continue
		}

		result := q.Exec(ctx)
		cancel()

		if result.Err != nil {
			fmt.Printf("Error: %v\n", result.Err)
			continue
		}

		// Print results
		printUpstreamQueryResult(result)
	}
}

// printUpstreamQueryResult formats and displays query results from the upstream PromQL engine.
// It handles different result types (Vector, Scalar, Matrix) with appropriate formatting.
func printUpstreamQueryResult(result *promql.Result) {
	switch v := result.Value.(type) {
	case promql.Vector:
		if len(v) == 0 {
			fmt.Println("No results found")
			return
		}
		fmt.Printf("Vector (%d samples):\n", len(v))
		for i, sample := range v {
			fmt.Printf("  [%d] %s => %g @ %s\n",
				i+1,
				sample.Metric,
				sample.F,
				model.Time(sample.T).Time().Format(time.RFC3339))
		}
	case promql.Scalar:
		fmt.Printf("Scalar: %g @ %s\n", v.V, model.Time(v.T).Time().Format(time.RFC3339))
	case promql.Matrix:
		if len(v) == 0 {
			fmt.Println("No results found")
			return
		}
		fmt.Printf("Matrix (%d series):\n", len(v))
		for i, series := range v {
			fmt.Printf("  [%d] %s:\n", i+1, series.Metric)
			for _, point := range series.Floats {
				fmt.Printf("    %g @ %s\n", point.F, model.Time(point.T).Time().Format(time.RFC3339))
			}
		}
	default:
		fmt.Printf("Unsupported result type: %T\n", result.Value)
	}
}

// testAutoCompletion demonstrates the auto-completion functionality
// This function shows how the enhanced auto-completer works with loaded metrics
func testAutoCompletion(storage *SimpleStorage) {
	fmt.Println("\n=== Auto-Completion Test ===")
fmt.Println("Testing enhanced auto-completion with loaded metrics...")

	// Create the auto-completer
	completer := NewPrometheusAutoCompleter(storage)

	// Test 1: Metric name completion
	fmt.Println("1. Metric name completion for 'cl':")
	completions := completer.getCompletions("cl", 2, "cl")
	for _, completion := range completions {
		fmt.Printf("   - %s\n", completion)
	}

	// Test 2: Metric name completion for 'cloudcost'
	fmt.Println("\n2. Metric name completion for 'cloudcost':")
	completions = completer.getCompletions("cloudcost", 9, "cloudcost")
	for _, completion := range completions {
		fmt.Printf("   - %s\n", completion)
	}

	// Test 3: Label name completion inside selectors
	fmt.Println("\n3. Label name completion for 'cloudcost_azure_aks_storage_by_location_usd_per_gibyte_hour{':")
	completions = completer.getCompletions("cloudcost_azure_aks_storage_by_location_usd_per_gibyte_hour{", 61, "")
	for _, completion := range completions {
		fmt.Printf("   - %s\n", completion)
	}

	// Test 4: Label value completion
	fmt.Println("\n4. Label value completion for 'cloudcost_azure_aks_storage_by_location_usd_per_gibyte_hour{region=':")
	completions = completer.getCompletions("cloudcost_azure_aks_storage_by_location_usd_per_gibyte_hour{region=", 68, "")
	for _, completion := range completions {
		fmt.Printf("   - %s\n", completion)
	}

	// Test 5: Function completion
	fmt.Println("\n5. Function completion for 'su':")
	completions = completer.getFunctionCompletions("su")
	for _, completion := range completions {
		fmt.Printf("   - %s\n", completion)
	}

	// Test 6: Mixed completion (when context is unclear)
	fmt.Println("\n6. Mixed completion for 'm':")
	completions = completer.getCompletions("m", 1, "m")
	limit := 10 // Limit output for readability
	for i, completion := range completions {
		if i >= limit {
			fmt.Printf("   ... and %d more\n", len(completions)-limit)
			break
		}
		fmt.Printf("   - %s\n", completion)
	}

	fmt.Println("\n=== Test completed ===")
	fmt.Println("The auto-completion system is now active. Try the 'query' mode to experience it interactively!")
}
