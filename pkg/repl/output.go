package repl

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"golang.org/x/term"
)

// mustFprintf and mustFprintln intentionally ignore write errors, e.g. when piping to a closed consumer.
// They keep the call sites free of errcheck noise while making the intent explicit.
func mustFprintf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
func mustFprintln(w io.Writer, a ...any)               { _, _ = fmt.Fprintln(w, a...) }

// PrintUpstreamQueryResult formats and displays query results from the upstream PromQL engine.
// It handles different result types (Vector, Scalar, Matrix) with appropriate formatting.
func PrintUpstreamQueryResult(result *promql.Result) {
	PrintUpstreamQueryResultToWriter(result, os.Stdout)
}

// isTerminal reports whether w is an interactive terminal; a variable so tests can override it.
var isTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// valueStyle highlights values: bold bright white on a 256-color grey, readable on dark and light themes.
const valueStyle = "\x1b[1;97;48;5;240m"

// rawOutput disables every terminal-only output nicety (value highlighting, the
// common-labels header), so a terminal shows exactly what a pipe would get.
var rawOutput bool

// SetRawOutput sets raw output mode (the --repl-raw flag).
func SetRawOutput(raw bool) { rawOutput = raw }

// ColorEnabled reports whether ANSI styling is appropriate for w: an interactive terminal,
// without --repl-raw, NO_COLOR (https://no-color.org) or TERM=dumb set.
func ColorEnabled(w io.Writer) bool {
	return !rawOutput && isTerminal(w) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

// valueFormatter returns how sample values are rendered: highlighted when ColorEnabled(w),
// plain when piped or captured.
func valueFormatter(w io.Writer) func(float64) string {
	plain := func(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
	if !ColorEnabled(w) {
		return plain
	}
	return func(f float64) string { return valueStyle + plain(f) + "\x1b[0m" }
}

func PrintUpstreamQueryResultToWriter(result *promql.Result, w io.Writer) {
	val := valueFormatter(w)
	switch v := result.Value.(type) {
	case promql.Vector:
		if len(v) == 0 {
			mustFprintln(w, "No results found")
			return
		}
		mustFprintf(w, "Vector (%d samples):\n", len(v))
		sets := make([]labels.Labels, len(v))
		for i, sample := range v {
			sets[i] = sample.Metric
		}
		header, lbls := labelFormatter(w, sets)
		if header != "" {
			mustFprintf(w, "  %s\n", header)
		}
		commonTS := commonTimestamp(w, v)
		if commonTS != "" {
			mustFprintf(w, "  # common_timestamp: %s\n", commonTS)
		}
		for i, sample := range v {
			if commonTS != "" {
				mustFprintf(w, "  [%d] %s => %s\n", i+1, lbls(sample.Metric), val(sample.F))
				continue
			}
			mustFprintf(w, "  [%d] %s => %s @ %s\n",
				i+1,
				lbls(sample.Metric),
				val(sample.F),
				model.Time(sample.T).Time().Format(time.RFC3339))
		}
	case promql.Scalar:
		mustFprintf(w, "Scalar: %s @ %s\n", val(v.V), model.Time(v.T).Time().Format(time.RFC3339))
	case promql.String:
		mustFprintf(w, "String: %s\n", v.V)
	case promql.Matrix:
		if len(v) == 0 {
			mustFprintln(w, "No results found")
			return
		}
		mustFprintf(w, "Matrix (%d series):\n", len(v))
		sets := make([]labels.Labels, len(v))
		for i, series := range v {
			sets[i] = series.Metric
		}
		header, lbls := labelFormatter(w, sets)
		if header != "" {
			mustFprintf(w, "  %s\n", header)
		}
		for i, series := range v {
			mustFprintf(w, "  [%d] %s:\n", i+1, lbls(series.Metric))
			for _, point := range series.Floats {
				mustFprintf(w, "    %s @ %s\n", val(point.F), model.Time(point.T).Time().Format(time.RFC3339))
			}
		}
	default:
		mustFprintf(w, "Unsupported result type: %T\n", result.Value)
	}
}

// PrintResultJSON renders the result as JSON similar to Prometheus API shapes.
func PrintResultJSON(result *promql.Result) error {
	type sampleJSON struct {
		Metric map[string]string `json:"metric"`
		Value  [2]any            `json:"value"` // [timestamp(sec), value]
	}
	type seriesJSON struct {
		Metric map[string]string `json:"metric"`
		Values [][2]any          `json:"values"`
	}
	type dataJSON struct {
		ResultType string `json:"resultType"`
		Result     any    `json:"result"`
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
				Value:  [2]any{float64(s.T) / 1000.0, jsonFloat(s.F)},
			})
		}
		out.Data.Result = arr
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(append(b, '\n')); err != nil {
			return err
		}
		return nil
	case promql.Scalar:
		out := respJSON{Status: "success", Data: dataJSON{ResultType: "scalar"}}
		out.Data.Result = [2]any{float64(v.T) / 1000.0, jsonFloat(v.V)}
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(append(b, '\n')); err != nil {
			return err
		}
		return nil
	case promql.Matrix:
		out := respJSON{Status: "success", Data: dataJSON{ResultType: "matrix"}}
		var arr []seriesJSON
		for _, series := range v {
			var values [][2]any
			for _, p := range series.Floats {
				values = append(values, [2]any{float64(p.T) / 1000.0, jsonFloat(p.F)})
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
		if _, err := os.Stdout.Write(append(b, '\n')); err != nil {
			return err
		}
		return nil
	default:
		// Unknown type; just marshal empty
		out := respJSON{Status: "success", Data: dataJSON{ResultType: fmt.Sprintf("%T", result.Value), Result: nil}}
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(append(b, '\n')); err != nil {
			return err
		}
		return nil
	}
}

// jsonFloat keeps finite values as JSON numbers; NaN and ±Inf, which encoding/json
// rejects, are rendered as strings the way the Prometheus HTTP API does ("NaN", "+Inf", "-Inf").
func jsonFloat(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return f
}

func labelsToMap(l labels.Labels) map[string]string {
	return l.Map()
}
