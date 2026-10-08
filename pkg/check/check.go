// Package check evaluates PromQL "contracts": lists of expressions that must
// each return a non-empty result. It is shared by the `promql-cli check`
// subcommand and the REPL's `.assert` command.
package check

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/prometheus/prometheus/promql"
	promparser "github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/storage"
)

// Status is the outcome of a single check.
type Status string

const (
	// StatusPass means the expression returned a non-empty result.
	StatusPass Status = "pass"
	// StatusFail means the expression evaluated but returned an empty result.
	StatusFail Status = "fail"
	// StatusError means the expression could not be parsed or evaluated.
	StatusError Status = "error"
)

// Check is one expression of a contract.
type Check struct {
	Name string `json:"name"` // preceding comment line, or the expression itself
	Expr string `json:"expr"`
	Line int    `json:"line"` // 1-based line in the contract file; 0 when ad hoc
}

// Result is the outcome of running a Check.
type Result struct {
	Check
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"` // why a check failed or errored
}

// Report is the outcome of running a whole contract.
type Report struct {
	Contract    string
	EvaluatedAt time.Time
	Results     []Result
}

// Counts returns how many results passed, failed and errored.
func (r Report) Counts() (passed, failed, errored int) {
	for _, res := range r.Results {
		switch res.Status {
		case StatusPass:
			passed++
		case StatusFail:
			failed++
		case StatusError:
			errored++
		}
	}
	return passed, failed, errored
}

// ExitCode maps the results to a process exit code: 2 when any check errored,
// else 1 when any failed, else 0.
func (r Report) ExitCode() int {
	_, failed, errored := r.Counts()
	switch {
	case errored > 0:
		return 2
	case failed > 0:
		return 1
	}
	return 0
}

// ParseContract reads a contract: one PromQL expression per line, blank lines
// and '#' comment lines ignored. The '#' comment line immediately preceding an
// expression (no blank line in between) becomes its name; without one the name
// is the expression itself.
func ParseContract(r io.Reader) ([]Check, error) {
	var (
		checks  []Check
		comment string
		lineNo  int
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lineNo++
		text := sc.Text()
		if lineNo == 1 {
			text = strings.TrimPrefix(text, "\ufeff") // UTF-8 byte order mark
		}
		line := strings.TrimSpace(text)
		switch {
		case line == "":
			comment = ""
		case strings.HasPrefix(line, "#"):
			comment = strings.TrimSpace(strings.TrimLeft(line, "#"))
		default:
			name := comment
			if name == "" {
				name = line
			}
			checks = append(checks, Check{Name: name, Expr: line, Line: lineNo})
			comment = ""
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return checks, nil
}

// ParseContractFile is ParseContract on the file at path.
func ParseContractFile(path string) ([]Check, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ParseContract(f)
}

// Runner evaluates checks against a queryable at a fixed instant.
type Runner struct {
	Engine    *promql.Engine
	Queryable storage.Queryable
	// Parser must match the engine's parser options (experimental functions).
	// Nil means a parser with experimental functions enabled.
	Parser promparser.Parser
	At     time.Time
	// Timeout bounds each evaluation; zero means 30s.
	Timeout time.Duration
}

func (r *Runner) parser() promparser.Parser {
	if r.Parser != nil {
		return r.Parser
	}
	return promparser.NewParser(promparser.Options{EnableExperimentalFunctions: true})
}

func (r *Runner) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 30 * time.Second
}

// eval runs expr as an instant query at r.At.
func (r *Runner) eval(ctx context.Context, expr string) (promparser.Value, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	q, err := r.Engine.NewInstantQuery(ctx, r.Queryable, nil, expr, r.At)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	res := q.Exec(ctx)
	if res.Err != nil {
		return nil, res.Err
	}
	return res.Value, nil
}

// Run evaluates one check: non-empty result passes, empty fails (with an
// explanation), parse and evaluation problems are errors.
func (r *Runner) Run(ctx context.Context, c Check) Result {
	res := Result{Check: c}
	ast, err := r.parser().ParseExpr(c.Expr)
	if err != nil {
		res.Status, res.Detail = StatusError, err.Error()
		return res
	}
	val, err := r.eval(ctx, c.Expr)
	if err != nil {
		res.Status, res.Detail = StatusError, err.Error()
		return res
	}
	if !isEmpty(val) {
		res.Status = StatusPass
		return res
	}
	res.Status, res.Detail = StatusFail, r.explain(ctx, ast)
	return res
}

// RunAll runs every check in order; a failing check never stops the others.
func (r *Runner) RunAll(ctx context.Context, checks []Check) []Result {
	out := make([]Result, 0, len(checks))
	for _, c := range checks {
		out = append(out, r.Run(ctx, c))
	}
	return out
}

// Assert runs an ad hoc expression, named after itself.
func (r *Runner) Assert(ctx context.Context, expr string) Result {
	expr = strings.TrimSpace(expr)
	return r.Run(ctx, Check{Name: expr, Expr: expr})
}

func isEmpty(v promparser.Value) bool {
	switch t := v.(type) {
	case promql.Vector:
		return len(t) == 0
	case promql.Matrix:
		return len(t) == 0
	}
	return false // scalars and strings are always a result
}
