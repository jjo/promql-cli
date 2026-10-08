package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"
	"github.com/prometheus/prometheus/promql"
	promparser "github.com/prometheus/prometheus/promql/parser"

	"github.com/jjo/promql-cli/pkg/check"
	repl "github.com/jjo/promql-cli/pkg/repl"
	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// Exit codes of `promql-cli check`.
const (
	exitCheckFailed = 1 // at least one check failed
	exitCheckError  = 2 // usage, I/O, scrape, parse or evaluation error
)

// exitError makes main exit with a specific status; err, when set, is printed to stderr first.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

func usageError(format string, a ...any) error {
	return &exitError{code: exitCheckError, err: fmt.Errorf(format, a...)}
}

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// checkOptions are the parsed flags and arguments of the check subcommand.
type checkOptions struct {
	scrape   []string
	count    int
	interval time.Duration
	at       string
	format   string
	commands string
	args     []string // <contract.promql> [<file.prom> ...]
}

func newCheckCommand(engine *promql.Engine, storage *sstorage.SimpleStorage) *ffcli.Command {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	var opts checkOptions
	var scrapes stringList
	fs.Var(&scrapes, "scrape", "scrape this Prometheus/OpenMetrics endpoint into the store (repeatable)")
	fs.IntVar(&opts.count, "count", 1, "number of scrapes per --scrape URL (rate() needs >= 2)")
	fs.DurationVar(&opts.interval, "interval", 5*time.Second, "delay between scrapes")
	fs.StringVar(&opts.at, "at", "", "evaluation time: now|now-5m|<RFC3339>|<unix> (default: pinned time of the loaded file, else the newest sample, else now)")
	fs.StringVar(&opts.format, "format", "text", "report format: text|tap|junit|json")
	fs.StringVar(&opts.commands, "command", "", "semicolon-separated pre-commands (e.g. \".rules rules.yaml\")")
	fs.StringVar(&opts.commands, "c", "", "shorthand for --command")

	return &ffcli.Command{
		Name:       "check",
		ShortUsage: "promql-cli check [flags] <contract.promql> [<file.prom> ...]",
		ShortHelp:  "Check that every expression of a contract file returns a result (CI for exporters)",
		LongHelp: `Evaluate a contract: one PromQL expression per line, blank lines and '#'
comments ignored. A non-empty result passes, an empty one fails. The comment
line right above an expression names the check.

Exit status: 0 all passed, 1 at least one failed, 2 usage, I/O, scrape or
expression errors (the remaining checks still run).

Example:
  promql-cli check --scrape http://localhost:9100/metrics --count 2 --interval 5s node.contract.promql`,
		FlagSet: fs,
		Exec: func(ctx context.Context, args []string) error {
			opts.scrape = scrapes
			opts.args = args
			return runCheck(ctx, opts, engine, storage, os.Stdout, os.Stderr)
		},
	}
}

// runCheck loads the data sources, runs the contract and writes the report to stdout.
// Progress and diagnostics go to stderr so stdout stays machine readable.
func runCheck(ctx context.Context, opts checkOptions, engine *promql.Engine, storage *sstorage.SimpleStorage, stdout, stderr io.Writer) error {
	if len(opts.args) == 0 {
		return usageError("check requires <contract.promql>")
	}
	contract, files := opts.args[0], opts.args[1:]
	if len(files) == 0 && len(opts.scrape) == 0 && strings.TrimSpace(opts.commands) == "" {
		return usageError("check needs a data source: <file.prom>, --scrape <url> or -c <commands>")
	}
	if !slices.Contains(check.Formats, opts.format) {
		return usageError("unknown --format %q (want %s)", opts.format, strings.Join(check.Formats, "|"))
	}
	if opts.count < 1 {
		return usageError("--count must be >= 1")
	}
	var at time.Time
	if opts.at != "" {
		t, err := repl.ParseEvalTime(opts.at)
		if err != nil {
			return usageError("invalid --at %q: %v", opts.at, err)
		}
		at = t
	}
	checks, err := check.ParseContractFile(contract)
	if err != nil {
		return usageError("contract: %v", err)
	}
	if len(checks) == 0 {
		return usageError("contract %s has no expressions", contract)
	}

	repl.SetEvalEngine(engine)
	for _, f := range files {
		if err := loadMetricsFromFile(storage, f, "", ""); err != nil {
			return usageError("failed to load %s: %v", f, err)
		}
	}
	if strings.TrimSpace(opts.commands) != "" {
		// Command output is diagnostics here, never part of the report.
		runWithStdout(stderr, func() { repl.RunInitCommands(engine, storage, opts.commands, false) })
	}
	for _, u := range opts.scrape {
		if err := repl.ScrapeEndpoint(io.Discard, storage, u, nil, opts.count, opts.interval); err != nil {
			return usageError("%v", err)
		}
		if opts.format == "text" {
			_, _ = fmt.Fprintf(stderr, "scraped %s %dx (%d series)\n", u, opts.count, countSeries(storage))
		}
	}

	if at.IsZero() {
		at = defaultEvalTime(storage)
	}
	runner := &check.Runner{
		Engine:    engine,
		Queryable: storage,
		Parser:    promparser.NewParser(promparser.Options{EnableExperimentalFunctions: true}),
		At:        at,
	}
	rep := check.Report{Contract: contract, EvaluatedAt: at, Results: runner.RunAll(ctx, checks)}
	if err := check.Write(stdout, opts.format, rep, repl.ColorEnabled(stdout)); err != nil {
		return usageError("writing report: %v", err)
	}
	if code := rep.ExitCode(); code != 0 {
		return &exitError{code: code}
	}
	return nil
}

// defaultEvalTime is the pinned time (restored from a saved file's header, unless a
// scrape added newer data, which drops it), else the newest sample in the store, else
// now. Saved files live in the past, where "now" is empty.
func defaultEvalTime(storage *sstorage.SimpleStorage) time.Time {
	if t, ok := repl.PinnedEvalTime(); ok {
		return t
	}
	latest := int64(math.MinInt64)
	for _, ss := range storage.Metrics {
		for _, s := range ss {
			latest = max(latest, s.Timestamp)
		}
	}
	if latest == math.MinInt64 {
		return time.Now()
	}
	return time.UnixMilli(latest)
}

// countSeries returns the number of distinct series in the store.
func countSeries(storage *sstorage.SimpleStorage) int {
	n := 0
	for _, ss := range storage.Metrics {
		seen := make(map[string]struct{}, len(ss))
		for _, s := range ss {
			seen[labelsKey(s.Labels)] = struct{}{}
		}
		n += len(seen)
	}
	return n
}

func labelsKey(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0)
		b.WriteString(m[k])
		b.WriteByte(0)
	}
	return b.String()
}

// runWithStdout runs fn with os.Stdout temporarily pointing at w when w is a file
// (stderr); for other writers it just runs fn.
func runWithStdout(w io.Writer, fn func()) {
	f, ok := w.(*os.File)
	if !ok {
		fn()
		return
	}
	old := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = old }()
	fn()
}

// exitCodeOf extracts the status carried by err, if it is an exitError.
func exitCodeOf(err error) (int, bool) {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code, true
	}
	return 0, false
}
