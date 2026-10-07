// Package main provides the promql-cli command-line tool for executing PromQL queries.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"
	"github.com/prometheus/prometheus/promql"
	promparser "github.com/prometheus/prometheus/promql/parser"

	ai "github.com/jjo/promql-cli/pkg/ai"
	mcp "github.com/jjo/promql-cli/pkg/mcp"
	repl "github.com/jjo/promql-cli/pkg/repl"
	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// Version info. Overridden at build time via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func init() {
	// Enable experimental PromQL functions (equivalent to --enable-feature=promql-experimental-functions)
	repl.InitParser(promparser.Options{EnableExperimentalFunctions: true})
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

// extractLogFlags returns -log.level and -log.format values from os.Args
// before the flag set is parsed, so we can initialise logging early.
func extractLogFlags() (level, format string) {
	level = "info"
	format = "text"
	for _, a := range os.Args[1:] {
		if a == "--" {
			break
		}
		switch {
		case a == "-log.level" || a == "--log.level":
			// no value given, ignore
		case strings.HasPrefix(a, "-log.level=") || strings.HasPrefix(a, "--log.level="):
			level = a[strings.IndexByte(a, '=')+1:]
		case a == "-log.format" || a == "--log.format":
		case strings.HasPrefix(a, "-log.format=") || strings.HasPrefix(a, "--log.format="):
			format = a[strings.IndexByte(a, '=')+1:]
		}
	}
	return level, format
}

// initLogging configures the global slog logger from the extracted flags.
func initLogging(level, format string) {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "info":
		l = slog.LevelInfo
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: l}

	var h slog.Handler
	switch strings.ToLower(format) {
	case "json":
		h = slog.NewJSONHandler(os.Stderr, opts)
	default:
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

// main is the entry point of the application.
// It provides a command-line interface for loading metrics and executing PromQL queries.
func main() {
	// Initialise logging before any output
	initLogging(extractLogFlags())

	// Root (global) flags
	rootFlags := flag.NewFlagSet("promql-cli", flag.ContinueOnError)
	replBackend := rootFlags.String("repl", "readline", "REPL backend: prompt|readline")
	silent := rootFlags.Bool("silent", false, "suppress startup output")
	rootFlags.BoolVar(silent, "s", *silent, "shorthand for --silent")

	// Logging flags — consumed by initLogging above; registered here so flag parser doesn't reject them
	logLevel := rootFlags.String("log.level", "info", "log level (debug|info|warn|error)")
	logFormat := rootFlags.String("log.format", "text", "log format (text|json)")
	_ = logLevel
	_ = logFormat

	// Composite AI flag (preferred)
	var aiConfig ai.AIConfig
	rootFlags.Var(&aiConfig, "ai", "AI options as key=value pairs (comma/space separated). Example: --ai 'provider=claude model=opus answers=3' (env PROMQL_CLI_AI)")

	// Prepare shared state
	storage := sstorage.NewSimpleStorage()
	engine := promql.NewEngine(promql.EngineOpts{
		Logger:                   nil,
		Reg:                      nil,
		MaxSamples:               50000000,
		Timeout:                  30 * time.Second,
		LookbackDelta:            5 * time.Minute,
		EnableAtModifier:         true,
		EnableNegativeOffset:     true,
		NoStepSubqueryIntervalFn: func(_ int64) int64 { return 60 * 1000 },
		// Without an explicit parser the engine defaults to one with experimental functions disabled.
		Parser: promparser.NewParser(promparser.Options{EnableExperimentalFunctions: true}),
	})

	// load subcommand
	loadFlags := flag.NewFlagSet("load", flag.ContinueOnError)
	loadCmd := &ffcli.Command{
		Name:       "load",
		ShortUsage: "promql-cli [--repl=...] load <file.prom>",
		FlagSet:    loadFlags,
		Exec: func(_ context.Context, args []string) error {
			// Apply AI configuration (composite/env/profile)
			ai.ConfigureAIComposite(map[string]string(aiConfig))
			if len(args) != 1 {
				return fmt.Errorf("load requires <file.prom>")
			}
			metricsFile := args[0]
			if err := loadMetricsFromFile(storage, metricsFile, "", ""); err != nil {
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
	querySilent := queryFlags.Bool("silent", false, "suppress startup output")
	queryFlags.BoolVar(querySilent, "s", false, "shorthand for --silent")
	oneOffQuery := queryFlags.String("query", "", "one-off query expr; exit")
	queryFlags.StringVar(oneOffQuery, "q", "", "shorthand for --query")
	queryFile := queryFlags.String("file", "", "file containing PromQL expressions (one per line)")
	queryFlags.StringVar(queryFile, "f", "", "shorthand for --file")
	rulesSpec := queryFlags.String("rules", "", "Prometheus rules: directory of .yml/.yaml or a glob (e.g., /path/*.yaml)")
	output := queryFlags.String("output", "", "output format for -q (json)")
	queryFlags.StringVar(output, "o", "", "shorthand for --output")
	initCommands := queryFlags.String("command", "", "semicolon-separated pre-commands")
	queryFlags.StringVar(initCommands, "c", "", "shorthand for --command")
	timestamp := queryFlags.String("timestamp", "", "timestamp override for metrics file: now|remove|<timespec>")
	regex := queryFlags.String("regex", "", "regex filter for series when loading metrics file")

	queryCmd := &ffcli.Command{
		Name:       "query",
		ShortUsage: "promql-cli [--repl=...] query [flags] [<file.prom>]",
		FlagSet:    queryFlags,
		Exec: func(_ context.Context, args []string) error {
			// Apply AI configuration (composite/env/profile)
			ai.ConfigureAIComposite(map[string]string(aiConfig))

			// Optional positional metrics file
			var metricsFile string
			if len(args) > 0 {
				metricsFile = args[0]
			}
			if metricsFile != "" {
				if err := loadMetricsFromFile(storage, metricsFile, *timestamp, *regex); err != nil {
					return fmt.Errorf("failed to load metrics: %w", err)
				}
				if !*querySilent {
					fmt.Printf("Loaded metrics from %s\n", metricsFile)
					printStorageInfo(storage)
					fmt.Println()
				}
			}

			if *initCommands != "" {
				repl.RunInitCommands(engine, storage, *initCommands, *querySilent)
			}

			// Load and evaluate rules if provided
			if *rulesSpec != "" {
				files, err := repl.ResolveRuleSpec(*rulesSpec)
				if err != nil {
					return fmt.Errorf("rules: %w", err)
				}
				if len(files) == 0 {
					if !*querySilent {
						fmt.Printf("No rule files matched %q\n", *rulesSpec)
					}
				} else {
					if !*querySilent {
						fmt.Printf("Evaluating %d rule file(s)\n", len(files))
					}
					now := time.Now()
					// Store active rules for REPL auto-evaluation on updates
					repl.SetActiveRules(files, *rulesSpec)
					added, alerts, err := repl.EvaluateRulesOnStorage(engine, storage, files, now, func(s string) { fmt.Println(s) })
					if err != nil {
						return fmt.Errorf("rules evaluation failed: %w", err)
					}
					if !*querySilent {
						fmt.Printf("Rules evaluated at %s: added %d samples; %d alerts\n", now.UTC().Format(time.RFC3339), added, alerts)
						// Show store totals
						tm := len(storage.Metrics)
						ts := 0
						for _, ss := range storage.Metrics {
							ts += len(ss)
						}
						fmt.Printf("Total: %d metrics, %d samples\n\n", tm, ts)
					}
				}
			}

			if *queryFile != "" {
				if err := repl.ExecuteQueriesFromFile(engine, storage, *queryFile); err != nil {
					return fmt.Errorf("error executing queries from file: %w", err)
				}
				return nil
			}

			if *oneOffQuery != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				q, err := engine.NewInstantQuery(ctx, storage, nil, *oneOffQuery, time.Now())
				if err != nil {
					cancel()
					return fmt.Errorf("error creating query: %w", err)
				}
				res := q.Exec(ctx)
				cancel()
				if res.Err != nil {
					return fmt.Errorf("error: %w", res.Err)
				}
				if strings.EqualFold(*output, "json") {
					if err := repl.PrintResultJSON(res); err != nil {
						return fmt.Errorf("failed to render JSON: %w", err)
					}
				} else {
					repl.PrintUpstreamQueryResult(res)
				}
				return nil
			}

			// Interactive REPL
			repl.RunInteractiveQueriesDispatch(engine, storage, *querySilent, *replBackend)
			return nil
		},
	}

	// version subcommand
	versionCmd := &ffcli.Command{
		Name: "version",
		Exec: func(_ context.Context, _ []string) error { printVersion(); return nil },
	}

	// mcp subcommand: MCP server over stdio
	mcpCmd := &ffcli.Command{
		Name:       "mcp",
		ShortUsage: "promql-cli mcp [<file.prom>]",
		ShortHelp:  "Start an MCP server over stdio (JSON-RPC) for AI agent integration",
		Exec: func(_ context.Context, args []string) error {
			// Keep the real stdout exclusively for protocol frames: anything else
			// printed from here on (storage info, init command output, stray
			// prints) goes to stderr.
			protoOut := os.Stdout
			os.Stdout = os.Stderr

			// Apply AI configuration (composite/env/profile)
			ai.ConfigureAIComposite(map[string]string(aiConfig))

			// Optional positional metrics file
			if len(args) > 0 {
				metricsFile := args[0]
				if err := loadMetricsFromFile(storage, metricsFile, "", ""); err != nil {
					return fmt.Errorf("failed to load metrics: %w", err)
				}
				if !*silent {
					fmt.Fprintf(os.Stderr, "Loaded metrics from %s\n", metricsFile)
					printStorageInfo(storage)
				}
			}

			// PROMQL_CLI_INIT_COMMANDS: semicolon-separated REPL init commands
			// (e.g. ".scrape http://...", ".prom_scrape http://prom:9090 'up'")
			// run at startup as if passed via -c. Works regardless of file arg.
			initCmds := strings.TrimSpace(os.Getenv("PROMQL_CLI_INIT_COMMANDS"))
			if initCmds != "" {
				slog.Debug("PROMQL_CLI_INIT_COMMANDS", "commands", initCmds)
				repl.RunInitCommands(engine, storage, initCmds, *silent)
			}

			// Create and run the MCP server on stdin/stdout.
			server := mcp.NewServerWithIO(engine, storage, os.Stdin, protoOut)
			return server.Run(context.Background())
		},
	}

	root := &ffcli.Command{
		Name:       "promql-cli",
		ShortUsage: "promql-cli [--repl=prompt|readline] <subcommand> [flags]",
		FlagSet:    rootFlags,
		Subcommands: []*ffcli.Command{
			loadCmd, queryCmd, versionCmd, mcpCmd,
		},
		Exec: func(_ context.Context, _ []string) error { return flag.ErrHelp },
	}

	// Normalize GNU-style long options ("--long") to stdlib format ("-long")
	norm := normalizeLongOpts(os.Args[1:])
	// Parse args and run
	if err := root.ParseAndRun(context.Background(), norm); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			root.FlagSet.Usage()
			os.Exit(1)
		}
		slog.Error("fatal", "err", err)
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
// Options like timestamp and regex can be provided to filter/transform the loaded data.
func loadMetricsFromFile(storage *sstorage.SimpleStorage, filename string, timestampSpec string, regexSpec string) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Capture before-load counts for timestamp override
	beforeCounts := make(map[string]int)
	for name, ss := range storage.Metrics {
		beforeCounts[name] = len(ss)
	}

	// Parse timestamp specification
	var tsMode string
	var tsFixed int64
	if timestampSpec == "" {
		tsMode = "keep"
	} else {
		args := []string{"timestamp=" + timestampSpec}
		var ok bool
		tsMode, tsFixed, ok = repl.ParseTimestampArg(args)
		if !ok {
			return fmt.Errorf("invalid timestamp specification: %s", timestampSpec)
		}
	}

	// Parse regex filter
	var re *regexp.Regexp
	if regexSpec != "" {
		args := []string{"regex=" + regexSpec}
		var ok bool
		re, ok = repl.ParseRegexArg(args)
		if !ok {
			return fmt.Errorf("invalid regex specification: %s", regexSpec)
		}
	}

	// Load metrics
	if re == nil {
		if err := storage.LoadFromReader(file); err != nil {
			return err
		}
		if tsMode != "keep" {
			repl.ApplyTimestampOverride(storage, beforeCounts, tsMode, tsFixed)
		}
	} else {
		// Load with regex filtering (same logic as .load command)
		tmp := sstorage.NewSimpleStorage()
		if err := tmp.LoadFromReader(file); err != nil {
			return err
		}
		repl.ApplyFilteredLoad(storage, tmp, re, tsMode, tsFixed)
	}

	return nil
}

// printStorageInfo displays a summary of the loaded metrics.
// It shows the total number of metrics and samples, plus examples.
func printStorageInfo(storage *sstorage.SimpleStorage) {
	totalSamples := 0
	for _, samples := range storage.Metrics {
		totalSamples += len(samples)
	}

	fmt.Printf("Storage contains %d metrics with %d total samples\n", len(storage.Metrics), totalSamples)

	// Print some example metrics
	count := 0
	for name, samples := range storage.Metrics {
		if count >= 5 {
			fmt.Println("...")
			break
		}
		fmt.Printf("  %s (%d samples)\n", name, len(samples))
		count++
	}
}
