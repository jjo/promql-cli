package check

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	promparser "github.com/prometheus/prometheus/promql/parser"
)

// maxShown caps how many series an explanation lists.
const maxShown = 3

// explain says why the (empty-result) expression failed, by looking at its
// top-level shape:
//   - a comparison (a > b): the values on each side, since the comparison
//     filtered everything out;
//   - absent(x): the series of x that make absent() empty;
//   - anything else: just "got: empty".
func (r *Runner) explain(ctx context.Context, expr promparser.Expr) string {
	const fallback = "got: empty"
	switch e := unparen(expr).(type) {
	case *promparser.BinaryExpr:
		if !e.Op.IsComparisonOperator() || e.ReturnBool {
			return fallback
		}
		lhs, err := r.eval(ctx, e.LHS.String())
		if err != nil {
			return fallback
		}
		out := "got: " + render(e.LHS.String(), lhs) + ", want " + e.Op.String() + " "
		if isLiteral(e.RHS) {
			return out + e.RHS.String()
		}
		rhs, err := r.eval(ctx, e.RHS.String())
		if err != nil {
			return out + e.RHS.String()
		}
		return out + render(e.RHS.String(), rhs)
	case *promparser.Call:
		if e.Func.Name != "absent" || len(e.Args) != 1 {
			return fallback
		}
		v, err := r.eval(ctx, e.Args[0].String())
		if err != nil {
			return fallback
		}
		vec, ok := v.(promql.Vector)
		if !ok || len(vec) == 0 {
			return fallback
		}
		return fmt.Sprintf("found %d offending series: %s", len(vec), seriesList(vec))
	}
	return fallback
}

func unparen(e promparser.Expr) promparser.Expr {
	for {
		p, ok := e.(*promparser.ParenExpr)
		if !ok {
			return e
		}
		e = p.Expr
	}
}

// isLiteral reports whether e is a constant (number, possibly negated, or string).
func isLiteral(e promparser.Expr) bool {
	switch t := unparen(e).(type) {
	case *promparser.NumberLiteral, *promparser.StringLiteral:
		return true
	case *promparser.UnaryExpr:
		return isLiteral(t.Expr)
	}
	return false
}

// render shows "<expr> = <value>" for a plain result (a scalar or a single
// unlabeled sample, or nothing), and "<expr> => {labels} = v, ..." for series.
func render(expr string, v promparser.Value) string {
	switch t := v.(type) {
	case promql.Scalar:
		return expr + " = " + formatValue(t.V)
	case promql.String:
		return expr + " = " + strconv.Quote(t.V)
	case promql.Vector:
		switch {
		case len(t) == 0:
			return expr + " = empty"
		case len(t) == 1 && t[0].Metric.IsEmpty():
			return expr + " = " + sampleValue(t[0])
		}
		return expr + " => " + seriesList(t)
	case promql.Matrix:
		if len(t) == 0 {
			return expr + " = empty"
		}
		return fmt.Sprintf("%s = %d range series", expr, len(t))
	}
	return expr + " = " + fmt.Sprint(v)
}

// seriesList lists up to maxShown samples as `{labels} = value`, sorted by
// labels so the output is deterministic, followed by "(+N more)".
func seriesList(vec promql.Vector) string {
	sorted := append(promql.Vector(nil), vec...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return metricString(sorted[i].Metric) < metricString(sorted[j].Metric)
	})
	var parts []string
	for i, s := range sorted {
		if i == maxShown {
			break
		}
		parts = append(parts, metricString(s.Metric)+" = "+sampleValue(s))
	}
	out := strings.Join(parts, ", ")
	if more := len(sorted) - maxShown; more > 0 {
		out += fmt.Sprintf(" (+%d more)", more)
	}
	return out
}

// metricString renders labels as name{k="v",...} (or {k="v"} without a name),
// the way PromQL selectors are written.
func metricString(l labels.Labels) string {
	var parts []string
	l.Range(func(lb labels.Label) {
		if lb.Name != model.MetricNameLabel {
			parts = append(parts, lb.Name+"="+strconv.Quote(lb.Value))
		}
	})
	return l.Get(model.MetricNameLabel) + "{" + strings.Join(parts, ",") + "}"
}

func sampleValue(s promql.Sample) string {
	if s.H != nil {
		return fmt.Sprintf("histogram(count=%s, sum=%s)", formatValue(s.H.Count), formatValue(s.H.Sum))
	}
	return formatValue(s.F)
}

// formatValue prints integers and ordinary magnitudes without exponents, so a
// cardinality shows as 1116 rather than 1.116e+03.
func formatValue(f float64) string {
	if a := math.Abs(f); f == 0 || (a >= 1e-4 && a < 1e15) {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
