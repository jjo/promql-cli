package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"
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

// normalizeLongOpts converts GNU-style "--long" options to stdlib-flag style "-long".
// It leaves the "--" end-of-flags marker intact and doesn't touch single-dash or positional args.
func normalizeLongOpts(args []string) []string {
	out := make([]string, 0, len(args))
	seenTerminator := false
	for _, a := range args {
		if seenTerminator {
			out = append(out, a)
			continue
		}
		if a == "--" {
			seenTerminator = true
			out = append(out, a)
			continue
		}
		if strings.HasPrefix(a, "--") && len(a) > 2 {
			// Convert --flag and --flag=value to -flag and -flag=value
			out = append(out, "-"+a[2:])
			continue
		}
		out = append(out, a)
	}
	return out
}

// main is the entry point of the application.
// It provides a command-line interface for loading metrics and executing PromQL queries.
func main() {
// Root (global) flags
rootFlags := flag.NewFlagSet("promql-cli", flag.ContinueOnError)
replBackend := rootFlags.String("repl", "prompt", "REPL backend: prompt|readline")
silent := rootFlags.Bool("silent", false, "suppress startup output")
rootFlags.BoolVar(silent, "s", *silent, "shorthand for --silent")

// Composite AI flag (preferred)
var aikv aiKV
rootFlags.Var(&aikv, "ai", "AI options as key=value pairs (comma/space separated). Example: --ai 'provider=claude model=opus answers=3' (env PROMQL_CLI_AI)")

	// Prepare shared state
	storage := NewSimpleStorage()
	engine := promql.NewEngine(promql.EngineOpts{
		Logger:               nil,
		Reg:                  nil,
		MaxSamples:           50000000,
		Timeout:              30 * time.Second,
		LookbackDelta:        5 * time.Minute,
		EnableAtModifier:     true,
		EnableNegativeOffset: true,
		NoStepSubqueryIntervalFn: func(rangeMillis int64) int64 { return 60 * 1000 },
	})

// load subcommand
	loadFlags := flag.NewFlagSet("load", flag.ContinueOnError)
	var loadCmd *ffcli.Command
loadCmd = &ffcli.Command{
		Name:       "load",
		ShortUsage: "promql-cli [--repl=...] load <file.prom>",
		FlagSet:    loadFlags,
Exec: func(ctx context.Context, args []string) error {
			// Apply AI configuration (composite/env/profile)
			ConfigureAIComposite(map[string]string(aikv))
			if len(args) != 1 {
				return fmt.Errorf("load requires <file.prom>")
			}
			metricsFile := args[0]
			if err := loadMetricsFromFile(storage, metricsFile); err != nil {
				return fmt.Errorf("failed to load metrics: %w", err)
			}
			if !*silent {
				fmt.Printf("Successfully loaded metrics from %s\n", metricsFile)
				printStorageInfo(storage)
			}
			return nil
		},
	}

	// query subcommand
	queryFlags := flag.NewFlagSet("query", flag.ContinueOnError)
	oneOffQuery := queryFlags.String("query", "", "one-off query expr; exit")
	queryFlags.StringVar(oneOffQuery, "q", "", "shorthand for --query")
	output := queryFlags.String("output", "", "output format for -q (json)")
	initCommands := queryFlags.String("command", "", "semicolon-separated pre-commands")
	queryFlags.StringVar(initCommands, "c", "", "shorthand for --command")

queryCmd := &ffcli.Command{
		Name:       "query",
		ShortUsage: "promql-cli [--repl=...] query [flags] [<file.prom>]",
		FlagSet:    queryFlags,
Exec: func(ctx context.Context, args []string) error {
			// Apply AI configuration (composite/env/profile)
			ConfigureAIComposite(map[string]string(aikv))

			// Optional positional metrics file
			var metricsFile string
			if len(args) > 0 {
				metricsFile = args[0]
			}
			if metricsFile != "" {
				if err := loadMetricsFromFile(storage, metricsFile); err != nil {
					return fmt.Errorf("failed to load metrics: %w", err)
				}
				if !*silent {
					fmt.Printf("Loaded metrics from %s\n", metricsFile)
					printStorageInfo(storage)
					fmt.Println()
				}
			}

			if *initCommands != "" {
				runInitCommands(engine, storage, *initCommands, *silent)
			}

			if *oneOffQuery != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				q, err := engine.NewInstantQuery(ctx, storage, nil, *oneOffQuery, time.Now())
				if err != nil { cancel(); return fmt.Errorf("error creating query: %w", err) }
				res := q.Exec(ctx)
				cancel()
				if res.Err != nil { return fmt.Errorf("error: %w", res.Err) }
				if strings.EqualFold(*output, "json") {
					if err := printResultJSON(res); err != nil { return fmt.Errorf("failed to render JSON: %w", err) }
				} else {
					printUpstreamQueryResult(res)
				}
				return nil
			}

			// Interactive REPL
			runInteractiveQueriesDispatch(engine, storage, *silent, *replBackend)
			return nil
		},
	}

	// version subcommand
	versionCmd := &ffcli.Command{
		Name:    "version",
		Exec: func(ctx context.Context, _ []string) error { printVersion(); return nil },
	}

	root := &ffcli.Command{
		Name:       "promql-cli",
		ShortUsage: "promql-cli [--repl=prompt|readline] <subcommand> [flags]",
		FlagSet:    rootFlags,
		Subcommands: []*ffcli.Command{
			loadCmd, queryCmd, versionCmd,
		},
		Exec: func(ctx context.Context, _ []string) error { return flag.ErrHelp },
	}

	// Normalize GNU-style long options ("--long") to stdlib format ("-long")
	norm := normalizeLongOpts(os.Args[1:])
	// Parse args and run
	if err := root.ParseAndRun(context.Background(), norm); err != nil {
		if err == flag.ErrHelp {
			root.FlagSet.Usage()
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, err)
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
	printUpstreamQueryResultToWriter(result, os.Stdout)
}

func printUpstreamQueryResultToWriter(result *promql.Result, w io.Writer) {
	switch v := result.Value.(type) {
	case promql.Vector:
		if len(v) == 0 {
			fmt.Fprintln(w, "No results found")
			return
		}
		fmt.Fprintf(w, "Vector (%d samples):\n", len(v))
		for i, sample := range v {
			fmt.Fprintf(w, "  [%d] %s => %g @ %s\n",
				i+1,
				sample.Metric,
				sample.F,
				model.Time(sample.T).Time().Format(time.RFC3339))
		}
	case promql.Scalar:
		fmt.Fprintf(w, "Scalar: %g @ %s\n", v.V, model.Time(v.T).Time().Format(time.RFC3339))
	case promql.String:
		fmt.Fprintf(w, "String: %s\n", v.V)
	case promql.Matrix:
		if len(v) == 0 {
			fmt.Println("No results found")
			return
		}
		fmt.Fprintf(w, "Matrix (%d series):\n", len(v))
		for i, series := range v {
			fmt.Fprintf(w, "  [%d] %s:\n", i+1, series.Metric)
			for _, point := range series.Floats {
				fmt.Fprintf(w, "    %g @ %s\n", point.F, model.Time(point.T).Time().Format(time.RFC3339))
			}
		}
	default:
		fmt.Fprintf(w, "Unsupported result type: %T\n", result.Value)
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
