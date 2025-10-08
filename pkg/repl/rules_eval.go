package repl

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/rulefmt"
	"github.com/prometheus/prometheus/promql"
	promparser "github.com/prometheus/prometheus/promql/parser"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// ResolveRuleSpec returns the list of rule files from a directory or glob or single file.
// - If spec contains shell wildcards (*?[...]), it's treated as a glob.
// - If spec is a directory, all .yml/.yaml files (non-recursive) are used.
// - Otherwise, spec is treated as a single file path.
func ResolveRuleSpec(spec string) ([]string, error) {
	// Glob pattern?
	if strings.ContainsAny(spec, "*?[]") {
		matches, err := filepath.Glob(spec)
		if err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", spec, err)
		}
		// Keep only .yml/.yaml
		var out []string
		for _, m := range matches {
			if isYAML(m) {
				out = append(out, m)
			}
		}
		return out, nil
	}
	// Directory?
	fi, err := os.Stat(spec)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", spec, err)
	}
	if fi.IsDir() {
		entries, err := os.ReadDir(spec)
		if err != nil {
			return nil, fmt.Errorf("readdir %q: %w", spec, err)
		}
		var out []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if isYAML(name) {
				out = append(out, filepath.Join(spec, name))
			}
		}
		return out, nil
	}
	// Single file
	if !isYAML(spec) {
		return nil, fmt.Errorf("rules file must be .yml or .yaml: %s", spec)
	}
	return []string{spec}, nil
}

func isYAML(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yml" || ext == ".yaml"
}

// EvaluateRulesOnStorage parses and evaluates Prometheus rules and applies recording results into storage.
// Alerts are printed via the provided printFn (or collected if nil).
// Returns number of recording samples added and the number of alert instances detected.
func EvaluateRulesOnStorage(engine *promql.Engine, storage *sstorage.SimpleStorage, files []string, evalTime time.Time, printFn func(string)) (int, int, error) {
	groups, err := loadRuleGroups(files)
	if err != nil {
		return 0, 0, err
	}
	added := 0
	alerts := 0
	for _, g := range groups {
		for _, r := range g.Rules {
			if r.Record != "" {
				n, err := evalRecordingRule(engine, storage, r, evalTime)
				if err != nil {
					return added, alerts, err
				}
				added += n
				continue
			}
			if r.Alert != "" {
				n, err := evalAlertingRule(engine, storage, r, evalTime, printFn)
				if err != nil {
					return added, alerts, err
				}
				alerts += n
				continue
			}
		}
	}
	return added, alerts, nil
}

func loadRuleGroups(files []string) ([]rulefmt.RuleGroup, error) {
	var groups []rulefmt.RuleGroup
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		rgs, errs := rulefmt.Parse(b, false)
		if len(errs) > 0 {
			return nil, fmt.Errorf("%s: %v", file, errs)
		}
		groups = append(groups, rgs.Groups...)
	}
	return groups, nil
}

func evalRecordingRule(engine *promql.Engine, storage *sstorage.SimpleStorage, r rulefmt.Rule, t time.Time) (int, error) {
	if r.Record == "" {
		return 0, nil
	}
	expr := r.Expr
	// Parse expression to ensure it's valid (promql.Engine will also parse, but this provides early error)
	if _, err := promparser.ParseExpr(expr); err != nil {
		return 0, fmt.Errorf("recording rule %q: parse error: %w", r.Record, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), replTimeout)
	defer cancel()
	q, err := engine.NewInstantQuery(ctx, storage, nil, expr, t)
	if err != nil {
		return 0, fmt.Errorf("recording rule %q: %w", r.Record, err)
	}
	res := q.Exec(ctx)
	if res.Err != nil {
		return 0, fmt.Errorf("recording rule %q: %w", r.Record, res.Err)
	}
	// Expect Vector or Scalar; for Scalar, create a single sample without extra labels.
	recorded := 0
	switch v := res.Value.(type) {
	case promql.Vector:
		for _, smpl := range v {
			lbls := make(map[string]string)
			smpl.Metric.Range(func(l labels.Label) {
				lbls[l.Name] = l.Value
			})
			// Remove original metric name and apply rule labels
			delete(lbls, "__name__")
			for k, v := range r.Labels {
				lbls[k] = v
			}
			lbls["__name__"] = r.Record
			// Use the engine's computed value; timestamp from evaluation time passed in
			storage.AddSample(lbls, smpl.F, t.UnixMilli())
			recorded++
		}
	case promql.Scalar:
		lbls := map[string]string{"__name__": r.Record}
		for k, v := range r.Labels {
			lbls[k] = v
		}
		storage.AddSample(lbls, v.V, t.UnixMilli())
		recorded++
	default:
		return 0, fmt.Errorf("recording rule %q: unsupported result type %T", r.Record, res.Value)
	}
	return recorded, nil
}

func evalAlertingRule(engine *promql.Engine, storage *sstorage.SimpleStorage, r rulefmt.Rule, t time.Time, printFn func(string)) (int, error) {
	if r.Alert == "" {
		return 0, nil
	}
	expr := r.Expr
	if _, err := promparser.ParseExpr(expr); err != nil {
		return 0, fmt.Errorf("alerting rule %q: parse error: %w", r.Alert, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), replTimeout)
	defer cancel()
	q, err := engine.NewInstantQuery(ctx, storage, nil, expr, t)
	if err != nil {
		return 0, fmt.Errorf("alerting rule %q: %w", r.Alert, err)
	}
	res := q.Exec(ctx)
	if res.Err != nil {
		return 0, fmt.Errorf("alerting rule %q: %w", r.Alert, res.Err)
	}
	fires := 0
	switch v := res.Value.(type) {
	case promql.Vector:
		for _, smpl := range v {
			if smpl.F == 0 || math.IsNaN(smpl.F) {
				continue
			}
			fires++
			// Build labels with alert metadata
			lbls := make(map[string]string)
			smpl.Metric.Range(func(l labels.Label) {
				if l.Name != "__name__" {
					lbls[l.Name] = l.Value
				}
			})
			// Add rule labels
			for k, v := range r.Labels {
				lbls[k] = v
			}
			// Add alert metadata
			lbls["alertname"] = r.Alert
			lbls["alertstate"] = "firing"
			lbls["__name__"] = "ALERTS"

			// Write ALERTS metric to storage
			storage.AddSample(lbls, smpl.F, t.UnixMilli())

			if printFn != nil {
				printFn(fmt.Sprintf("ALERT %s firing labels=%v value=%v", r.Alert, lbls, smpl.F))
			}
		}
	case promql.Scalar:
		if v.V != 0 && !math.IsNaN(v.V) {
			fires++
			// Create ALERTS metric for scalar alert
			lbls := map[string]string{
				"__name__":   "ALERTS",
				"alertname":  r.Alert,
				"alertstate": "firing",
			}
			// Add rule labels
			for k, v := range r.Labels {
				lbls[k] = v
			}
			storage.AddSample(lbls, v.V, t.UnixMilli())

			if printFn != nil {
				printFn(fmt.Sprintf("ALERT %s firing (scalar) value=%v", r.Alert, v.V))
			}
		}
	}
	return fires, nil
}
