package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	promparser "github.com/prometheus/prometheus/promql/parser"
)

// Version info. Overridden at build time via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func init() {
	// Initialize validation scheme to avoid panics
	model.NameValidationScheme = model.UTF8Validation
	// Enable experimental PromQL functions (equivalent to --enable-feature=promql-experimental-functions)
	promparser.EnableExperimentalFunctions = true
}

// main is the entry point of the application.
// It provides a command-line interface for loading metrics and executing PromQL queries.
func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage:")
		fmt.Println("  Load metrics: go run main.go load <file.prom>")
		fmt.Println("  Query:        go run main.go query [flags] <file.prom>")
		fmt.Println("  Version:      go run main.go version")
		fmt.Println("")
		fmt.Println("Common flags:")
		fmt.Println("  -s, --silent           Suppress startup output (banners, summaries)")
		fmt.Println("")
		fmt.Println("Flags (query mode):")
		fmt.Println("  -q, --query \"<expr>\"   Execute a single PromQL expression and exit (no REPL)")
		fmt.Println("  -o, --output json       When used with -q, output JSON (default is text)")
		fmt.Println("  -c, --command \"cmds\"  Run semicolon-separated commands before the session (e.g., \".scrape URL; .metrics\")")
		fmt.Println("")
		fmt.Println("Features:")
		fmt.Println("  - Dynamic auto-completion for metric names, labels, and values")
		fmt.Println("  - Context-aware suggestions based on query position")
		fmt.Println("  - Full PromQL function and operator completion")
		fmt.Println("  - Tab completion similar to Prometheus UI")
		fmt.Println("  - Ad-hoc commands: .help, .labels, .metrics, .seed, .at")
		fmt.Println("Tip: use -c to run startup commands before the session, e.g. -c \".scrape http://localhost:9100/metrics; .metrics\"")
		os.Exit(1)
	}

	// Fast-path for version banner
	if os.Args[1] == "version" {
		printVersion()
		return
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
			metricsFile  string
			oneOffQuery  string
			outputJSON   bool
			silent       bool
			initCommands string
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
			if a == "-c" || a == "--command" {
				i++
				if i >= len(args) {
					log.Fatal("--command requires an argument")
				}
				if initCommands == "" {
					initCommands = args[i]
				} else {
					initCommands = initCommands + "; " + args[i]
				}
				continue
			}
			if strings.HasPrefix(a, "--command=") {
				val := strings.TrimPrefix(a, "--command=")
				if initCommands == "" {
					initCommands = val
				} else {
					initCommands = initCommands + "; " + val
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
		if metricsFile != "" {
			if err := loadMetricsFromFile(storage, metricsFile); err != nil {
				log.Fatalf("Failed to load metrics: %v", err)
			}
			if !silent {
				fmt.Printf("Loaded metrics from %s\n", metricsFile)
				printStorageInfo(storage)
				fmt.Println()
			}
		}

		// Run initialization commands if provided (before one-off or REPL)
		if initCommands != "" {
			runInitCommands(engine, storage, initCommands, silent)
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

// printVersion prints a human-readable version string.
func printVersion() {
	fmt.Printf("promql-cli %s\n", version)
	fmt.Printf("  commit: %s\n", commit)
	fmt.Printf("  date:   %s\n", date)
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
		Metric map[string]string `json:"metric"`
		Values [][2]interface{}  `json:"values"`
	}
	type dataJSON struct {
		ResultType string      `json:"resultType"`
		Result     interface{} `json:"result"`
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
