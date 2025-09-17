package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"sort"
	"strconv"
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

// getLabelKey creates a stable unique key for a label set by sorting labels
func (q *SimpleQuerier) getLabelKey(lbls map[string]string) string {
	l := labels.FromMap(lbls)
	return l.String()
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
		fmt.Println("  Query:        go run main.go query [flags] <file.prom>")
		fmt.Println("")
		fmt.Println("Common flags:")
		fmt.Println("  -s, --silent           Suppress startup output (banners, summaries)")
		fmt.Println("")
		fmt.Println("Flags (query mode):")
		fmt.Println("  -q, --query \"<expr>\"   Execute a single PromQL expression and exit (no REPL)")
		fmt.Println("  -o, --output json       When used with -q, output JSON (default is text)")
		fmt.Println("")
		fmt.Println("Features:")
		fmt.Println("  - Dynamic auto-completion for metric names, labels, and values")
		fmt.Println("  - Context-aware suggestions based on query position")
		fmt.Println("  - Full PromQL function and operator completion")
		fmt.Println("  - Tab completion similar to Prometheus UI")
		fmt.Println("  - Ad-hoc commands: .help, .labels, .metrics, .seed, .at")
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
		// Flags: -s/--silent
		args := os.Args[2:]
		var (
			metricsFile string
			silent      bool
		)
		for i := 0; i < len(args); i++ {
			a := args[i]
			if a == "-s" || a == "--silent" {
				silent = true
				continue
			}
			if strings.HasPrefix(a, "-") {
				log.Fatalf("Unknown flag: %s", a)
			}
			if metricsFile == "" {
				metricsFile = a
			} else {
				log.Fatalf("Unexpected extra argument: %s", a)
			}
		}
		if metricsFile == "" {
			log.Fatal("Please specify a metrics file")
		}

		if err := loadMetricsFromFile(storage, metricsFile); err != nil {
			log.Fatalf("Failed to load metrics: %v", err)
		}

		if !silent {
			fmt.Printf("Successfully loaded metrics from %s\n", metricsFile)
			printStorageInfo(storage)
		}

	case "query":
		// Parse flags: -q/--query, -o/--output, and metrics file path
		args := os.Args[2:]
		var (
			metricsFile string
			oneOffQuery string
			outputJSON  bool
			silent      bool
		)
		for i := 0; i < len(args); i++ {
			a := args[i]
			if a == "-q" || a == "--query" {
				i++
				if i >= len(args) {
					log.Fatal("--query requires an argument")
				}
				oneOffQuery = args[i]
				continue
			}
			if strings.HasPrefix(a, "--query=") {
				oneOffQuery = strings.TrimPrefix(a, "--query=")
				continue
			}
			if a == "-o" || a == "--output" {
				i++
				if i >= len(args) {
					log.Fatal("--output requires an argument (e.g., json)")
				}
				if strings.EqualFold(args[i], "json") {
					outputJSON = true
				}
				continue
			}
			if strings.HasPrefix(a, "--output=") {
				val := strings.TrimPrefix(a, "--output=")
				if strings.EqualFold(val, "json") {
					outputJSON = true
				}
				continue
			}
			if a == "-s" || a == "--silent" {
				silent = true
				continue
			}
			if strings.HasPrefix(a, "-") {
				log.Fatalf("Unknown flag: %s", a)
			}
			// positional -> metrics file
			if metricsFile == "" {
				metricsFile = a
			} else {
				log.Fatalf("Unexpected extra argument: %s", a)
			}
		}
		if metricsFile == "" {
			log.Fatal("Please specify a metrics file")
		}

		if err := loadMetricsFromFile(storage, metricsFile); err != nil {
			log.Fatalf("Failed to load metrics: %v", err)
		}

		if !silent {
			fmt.Printf("Loaded metrics from %s\n", metricsFile)
			printStorageInfo(storage)
			fmt.Println()
		}

		if oneOffQuery != "" {
			// Execute a single expression and exit
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			q, err := engine.NewInstantQuery(ctx, storage, nil, oneOffQuery, time.Now())
			if err != nil {
				cancel()
				log.Fatalf("Error creating query: %v", err)
			}
			res := q.Exec(ctx)
			cancel()
			if res.Err != nil {
				log.Fatalf("Error: %v", res.Err)
			}
			if outputJSON {
				if err := printResultJSON(res); err != nil {
					log.Fatalf("Failed to render JSON: %v", err)
				}
			} else {
				printUpstreamQueryResult(res)
			}
			return
		}

		// Interactive REPL
		runInteractiveQueries(engine, storage, silent)

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
func runInteractiveQueries(engine *promql.Engine, storage *SimpleStorage, silent bool) {
	if !silent {
		fmt.Println("Enter PromQL queries (or 'quit' to exit):")
		fmt.Println("Supported queries:")
		fmt.Println("  - Basic selectors: metric_name, metric_name{label=\"value\"}")
		fmt.Println("  - Aggregations: sum(metric_name), avg(metric_name), count(metric_name), min(metric_name), max(metric_name)")
		fmt.Println("  - Group by: sum(metric_name) by (label)")
		fmt.Println("  - Binary operations: metric_name + 10, metric_name1 * metric_name2")
		fmt.Println("  - Functions: rate(metric_name), increase(metric_name), abs(metric_name)")
		fmt.Println("  - Comparisons: metric_name > 100, metric_name == 0")
		fmt.Println("  - Ad-hoc commands: .help, .labels <metric>, .metrics, .seed <metric> [steps=N] [step=1m], .at <time> <query>")
		fmt.Println()
	}

	// Configure readline
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "> ",
		HistoryFile:     "/tmp/.promql-cli_history",
		AutoComplete:    createAutoCompleter(storage), // Dynamic tab completion
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		fmt.Printf("Warning: Could not initialize readline, falling back to basic input: %v\n", err)
		runBasicInteractiveQueries(engine, storage, silent)
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

		// Handle ad-hoc functions before normal PromQL execution
		if handleAdHocFunction(query, storage) {
			continue
		}

		// Support ".at <time> <query>" to set evaluation time
		evalTime := time.Now()
		if strings.HasPrefix(query, ".at ") {
			parts := strings.Fields(query)
			if len(parts) >= 3 {
				if ts, err := parseEvalTime(parts[1]); err == nil {
					evalTime = ts
					query = strings.TrimPrefix(query, ".at "+parts[1]+" ")
				}
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		// Execute query using upstream Prometheus engine
		q, err := engine.NewInstantQuery(ctx, storage, nil, query, evalTime)
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
// AutoCompleteOptions controls optional completion behaviors, configurable via env vars.
type AutoCompleteOptions struct {
	AutoBrace       bool // when completing a metric name uniquely, append '{'
	LabelNameEquals bool // when completing a label name, append '="'
	AutoCloseQuote  bool // when completing a label value, append closing '"'
}

type PrometheusAutoCompleter struct {
	storage *SimpleStorage
	opts    AutoCompleteOptions
}

// NewPrometheusAutoCompleter creates a new auto-completer with access to metric data.
func NewPrometheusAutoCompleter(storage *SimpleStorage) *PrometheusAutoCompleter {
	return &PrometheusAutoCompleter{storage: storage, opts: loadAutoCompleteOptions()}
}

// Do implements the readline.AutoCompleter interface to provide dynamic completions.
func (pac *PrometheusAutoCompleter) Do(line []rune, pos int) (newLine [][]rune, length int) {
	lineStr := string(line)
	cursorPos := pos

	// Determine current word at cursor and context
	currentWord, _ := pac.getCurrentWord(lineStr, cursorPos)
	ctx := pac.analyzeContext(lineStr, cursorPos)

	// Fetch context-aware completions (full candidates)
	completions := pac.getCompletions(lineStr, cursorPos, currentWord)

	// Dedupe and filter to those that extend currentWord
	uniq := make(map[string]struct{}, len(completions))
	suffixes := make([][]rune, 0, len(completions))
	for _, cand := range completions {
		if _, ok := uniq[cand]; ok {
			continue
		}
		uniq[cand] = struct{}{}
		// Only consider candidates that start with currentWord
		if strings.HasPrefix(cand, currentWord) {
			cw := []rune(currentWord)
			cr := []rune(cand)
			if len(cr) >= len(cw) {
				// base suffix beyond current word
				suf := make([]rune, len(cr[len(cw):]))
				copy(suf, cr[len(cw):])

				// Optional tweaks based on context
				switch ctx.Type {
				case "metric_name":
					if pac.opts.AutoBrace && len(completions) == 1 {
						suf = append(suf, '{')
					}
case "label_name":
					if pac.opts.LabelNameEquals {
						suf = append(suf, '=', '"')
					}
				case "label_value":
					if pac.opts.AutoCloseQuote {
						suf = append(suf, '"')
					}
				}

				suffixes = append(suffixes, suf)
			}
		}
	}

	if len(suffixes) == 0 {
		return nil, 0
	}

	// Return suffixes and replacement length = len(currentWord) in runes.
	// The upstream readline completer will aggregate LCP and enter select-mode
	// with arrow-key navigation automatically when multiple remain.
	return suffixes, runeLen(currentWord)
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
	// Special handling for ad-hoc commands starting with '.'
	beforeCursor := line[:pos]
	trimmed := strings.TrimLeft(beforeCursor, " \t")
		if strings.HasPrefix(trimmed, ".") {
			// If typing the command token, suggest available ad-hoc commands
			if strings.HasPrefix(currentWord, ".") || strings.TrimSpace(trimmed) == "." {
				cmds := []string{".help", ".labels", ".metrics", ".seed", ".at"}
				var out []string
				for _, c := range cmds {
					if strings.HasPrefix(strings.ToLower(c), strings.ToLower(currentWord)) {
						out = append(out, c)
					}
				}
				return out
			}
		// If after ".labels " or ".seed ", complete metric names
		if strings.HasPrefix(trimmed, ".labels ") || strings.HasPrefix(trimmed, ".seed ") {
			return pac.getMetricNameCompletions(currentWord)
		}
		// If after ".at ", offer some time presets
		if strings.HasPrefix(trimmed, ".at ") {
			presets := []string{"now", "now-5m", "now-1h", time.Now().UTC().Format(time.RFC3339)}
			var out []string
			for _, p := range presets {
				if strings.HasPrefix(strings.ToLower(p), strings.ToLower(currentWord)) {
					out = append(out, p)
				}
			}
			return out
		}
	}

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
					labelValues[value] = true // raw value, no quotes; quotes handled in Do
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

// longestCommonPrefix finds the common prefix among a slice of strings (case-sensitive)
func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		// Trim prefix until it is a prefix of s
		for !strings.HasPrefix(s, prefix) {
			if len(prefix) == 0 {
				return ""
			}
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

// runeLen returns the rune length of a string (readline uses rune positions)
func runeLen(s string) int {
	return len([]rune(s))
}

// parseEvalTime parses time tokens like RFC3339, unix seconds/millis, or now+/-duration.
func parseEvalTime(tok string) (time.Time, error) {
	// now+/-duration
	if strings.HasPrefix(tok, "now") {
		if tok == "now" {
			return time.Now(), nil
		}
		op := tok[3]
		durStr := strings.TrimSpace(tok[4:])
		d, err := time.ParseDuration(durStr)
		if err != nil {
			return time.Time{}, err
		}
		if op == '+' {
			return time.Now().Add(d), nil
		}
		return time.Now().Add(-d), nil
	}
	// RFC3339
	if t, err := time.Parse(time.RFC3339, tok); err == nil {
		return t, nil
	}
	// unix seconds or millis
	if n, err := strconv.ParseInt(tok, 10, 64); err == nil {
		if n > 1_000_000_000_000 { // ms
			return time.UnixMilli(n), nil
		}
		return time.Unix(n, 0), nil
	}
	return time.Time{}, fmt.Errorf("unsupported time format: %s", tok)
}

// seedHistory synthesizes historical samples for a metric to enable rate() queries.
func seedHistory(storage *SimpleStorage, metric string, steps int, step time.Duration) {
	samples, ok := storage.metrics[metric]
	if !ok || len(samples) == 0 {
		fmt.Printf("Metric '%s' not found or has no samples\n", metric)
		return
	}
	isCounter := strings.HasSuffix(metric, "_total") || strings.Contains(metric, "_total_")
	for idx := range samples {
		base := samples[idx]
		for i := 1; i <= steps; i++ {
			copyLabels := make(map[string]string, len(base.Labels))
			for k, v := range base.Labels {
				copyLabels[k] = v
			}
			newTs := base.Timestamp - int64((steps-i+1))*step.Milliseconds()
			newVal := base.Value
			if isCounter {
				dec := base.Value * 0.001
				if dec < 1 {
					dec = 1
				}
				newVal = base.Value - float64(i)*dec
				if newVal < 0 {
					newVal = 0
				}
			} else {
				// Gauges: small drift
				newVal = base.Value * (1 - 0.001*float64(i))
			}
			// Avoid appending duplicate timestamp points for the same labelset
			existing := storage.metrics[metric]
			dup := false
			for _, s := range existing {
				if s.Timestamp == newTs && qEqualLabels(s.Labels, copyLabels) {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
			storage.metrics[metric] = append(storage.metrics[metric], MetricSample{
				Labels:    copyLabels,
				Value:     newVal,
				Timestamp: newTs,
			})
		}
	}
}

// qEqualLabels compares two label maps for equality
func qEqualLabels(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		if b[k] != va {
			return false
		}
	}
	return true
}

// getEnvBool reads an environment variable and parses it as boolean.
// Accepts 1/0, true/false (case-insensitive). Falls back to defVal when unset/invalid.
func getEnvBool(name string, defVal bool) bool {
	v := os.Getenv(name)
	if v == "" {
		return defVal
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return defVal
	}
}

// loadAutoCompleteOptions reads options from environment variables with sane defaults.
func loadAutoCompleteOptions() AutoCompleteOptions {
	return AutoCompleteOptions{
		AutoBrace:       getEnvBool("PROMQL_CLI_COMPLETION_AUTO_BRACE", true),
		LabelNameEquals: getEnvBool("PROMQL_CLI_COMPLETION_LABEL_EQUALS", true),
		AutoCloseQuote:  getEnvBool("PROMQL_CLI_COMPLETION_AUTO_CLOSE_QUOTE", true),
	}
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
func runBasicInteractiveQueries(engine *promql.Engine, storage *SimpleStorage, silent bool) {
	if !silent {
		fmt.Println("Using basic input mode (readline unavailable)")
	}

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

// printResultJSON renders the result as JSON similar to Prometheus API shapes.
func printResultJSON(result *promql.Result) error {
	type sampleJSON struct {
		Metric map[string]string `json:"metric"`
		Value  [2]interface{}    `json:"value"` // [timestamp(sec), value]
	}
	type seriesJSON struct {
		Metric map[string]string  `json:"metric"`
		Values [][2]interface{}   `json:"values"`
	}
	type dataJSON struct {
		ResultType string        `json:"resultType"`
		Result     interface{}   `json:"result"`
	}
	type respJSON struct {
		Status string   `json:"status"`
		Data   dataJSON `json:"data"`
	}

	switch v := result.Value.(type) {
	case promql.Vector:
		out := respJSON{Status: "success", Data: dataJSON{ResultType: "vector"}}
		var arr []sampleJSON
		for _, s := range v {
			arr = append(arr, sampleJSON{
				Metric: labelsToMap(s.Metric),
				Value:  [2]interface{}{float64(s.T) / 1000.0, s.F},
			})
		}
		out.Data.Result = arr
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	case promql.Scalar:
		out := respJSON{Status: "success", Data: dataJSON{ResultType: "scalar"}}
		out.Data.Result = [2]interface{}{float64(v.T) / 1000.0, v.V}
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	case promql.Matrix:
		out := respJSON{Status: "success", Data: dataJSON{ResultType: "matrix"}}
		var arr []seriesJSON
		for _, series := range v {
			var values [][2]interface{}
			for _, p := range series.Floats {
				values = append(values, [2]interface{}{float64(p.T) / 1000.0, p.F})
			}
			arr = append(arr, seriesJSON{
				Metric: labelsToMap(series.Metric),
				Values: values,
			})
		}
		out.Data.Result = arr
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	default:
		// Unknown type; just marshal empty
		out := respJSON{Status: "success", Data: dataJSON{ResultType: fmt.Sprintf("%T", result.Value), Result: nil}}
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
}

func labelsToMap(l labels.Labels) map[string]string {
	return l.Map()
}

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
