package repl

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
)

// PrintUpstreamQueryResult formats and displays query results from the upstream PromQL engine.
// It handles different result types (Vector, Scalar, Matrix) with appropriate formatting.
func PrintUpstreamQueryResult(result *promql.Result) {
	PrintUpstreamQueryResultToWriter(result, os.Stdout)
}

func PrintUpstreamQueryResultToWriter(result *promql.Result, w io.Writer) {
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

// PrintResultJSON renders the result as JSON similar to Prometheus API shapes.
func PrintResultJSON(result *promql.Result) error {
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
