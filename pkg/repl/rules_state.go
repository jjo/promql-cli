package repl

import (
	"fmt"
	"time"

	"github.com/prometheus/prometheus/promql"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

var (
	evalEngine           *promql.Engine
	activeRuleSpec       string
	activeRuleFiles      []string
	activeRecordingNames []string
	activeAlertingRules  []AlertRule
)

// AlertRule represents an alerting rule with its name and expression
type AlertRule struct {
	Name string
	Expr string
}

// SetEvalEngine stores a reference to the promql.Engine for rule evaluations in the REPL.
func SetEvalEngine(e *promql.Engine) { evalEngine = e }

// SetActiveRules sets the active rule files and spec used for auto-evaluation.
func SetActiveRules(files []string, spec string) {
	activeRuleFiles = append([]string{}, files...)
	activeRuleSpec = spec
	activeRecordingNames = collectRecordingRuleNames(files)
	activeAlertingRules = collectAlertingRules(files)
}

// GetActiveRules returns the current spec and files.
func GetActiveRules() (string, []string) {
	return activeRuleSpec, append([]string{}, activeRuleFiles...)
}

// GetRecordingRuleNames returns configured recording rule metric names.
func GetRecordingRuleNames() []string { return append([]string{}, activeRecordingNames...) }

// GetAlertingRules returns configured alerting rules with their names and expressions.
func GetAlertingRules() []AlertRule { return append([]AlertRule{}, activeAlertingRules...) }

// GetAlertExpr returns the expression for a given alert name, or empty string if not found.
func GetAlertExpr(alertName string) string {
	for _, ar := range activeAlertingRules {
		if ar.Name == alertName {
			return ar.Expr
		}
	}
	return ""
}

// EvaluateActiveRules evaluates currently active rule files (if any) over the provided storage.
// Uses pinnedEvalTime when set, else time.Now(). Prints a brief summary.
func EvaluateActiveRules(storage *sstorage.SimpleStorage) (added int, alerts int, err error) {
	if evalEngine == nil || len(activeRuleFiles) == 0 {
		return 0, 0, nil
	}
	t := time.Now()
	if pinnedEvalTime != nil {
		t = *pinnedEvalTime
	}
	return EvaluateRulesOnStorage(evalEngine, storage, activeRuleFiles, t, func(s string) { fmt.Println(s) })
}

// collectRecordingRuleNames parses the files and returns all recording rule names.
func collectRecordingRuleNames(files []string) []string {
	groups, err := loadRuleGroups(files)
	if err != nil {
		return nil
	}
	m := map[string]struct{}{}
	for _, g := range groups {
		for _, r := range g.Rules {
			if r.Record != "" {
				m[r.Record] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// collectAlertingRules parses the files and returns all alerting rules with their expressions.
func collectAlertingRules(files []string) []AlertRule {
	groups, err := loadRuleGroups(files)
	if err != nil {
		return nil
	}
	var out []AlertRule
	for _, g := range groups {
		for _, r := range g.Rules {
			if r.Alert != "" {
				out = append(out, AlertRule{
					Name: r.Alert,
					Expr: r.Expr,
				})
			}
		}
	}
	return out
}
