package check

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql"
	promparser "github.com/prometheus/prometheus/promql/parser"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

const testTS = int64(1700000000000)

func testRunner(t *testing.T) *Runner {
	t.Helper()
	parser := promparser.NewParser(promparser.Options{EnableExperimentalFunctions: true})
	engine := promql.NewEngine(promql.EngineOpts{
		MaxSamples:    1000000,
		Timeout:       10 * time.Second,
		LookbackDelta: 5 * time.Minute,
		Parser:        parser,
	})
	store := sstorage.NewSimpleStorage()
	for i, inst := range []string{"a", "b", "c", "d", "e"} {
		store.AddSample(map[string]string{"__name__": "node_load1", "instance": inst}, float64(i+1), testTS)
	}
	return &Runner{Engine: engine, Queryable: store, Parser: parser, At: time.UnixMilli(testTS)}
}

func TestParseContract(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Check
	}{
		{
			name: "UTF-8 BOM on the first line is ignored",
			in:   "\ufeff# is alive\nup\n",
			want: []Check{{Name: "is alive", Expr: "up", Line: 2}},
		},
		{
			name: "UTF-8 BOM before an expression",
			in:   "\ufeffup\n",
			want: []Check{{Name: "up", Expr: "up", Line: 1}},
		},
		{
			name: "comment names the next expression",
			in:   "# is alive\nup\n",
			want: []Check{{Name: "is alive", Expr: "up", Line: 2}},
		},
		{
			name: "no comment: name is the expression",
			in:   "up\n",
			want: []Check{{Name: "up", Expr: "up", Line: 1}},
		},
		{
			name: "blank line resets the comment",
			in:   "# header\n\nup\n",
			want: []Check{{Name: "up", Expr: "up", Line: 3}},
		},
		{
			name: "last of several comment lines wins",
			in:   "# one\n# two\nup\n",
			want: []Check{{Name: "two", Expr: "up", Line: 3}},
		},
		{
			name: "comment is consumed by the first expression",
			in:   "# named\na\nb\n",
			want: []Check{{Name: "named", Expr: "a", Line: 2}, {Name: "b", Expr: "b", Line: 3}},
		},
		{
			name: "windows line endings and indentation",
			in:   "  # crlf name\r\n  up  \r\n",
			want: []Check{{Name: "crlf name", Expr: "up", Line: 2}},
		},
		{name: "only comments and blanks", in: "# a\n\n# b\n", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseContract(strings.NewReader(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRunStatusAndExplanation(t *testing.T) {
	tests := []struct {
		name       string
		expr       string
		wantStatus Status
		wantDetail string
	}{
		{"non-empty vector passes", "node_load1", StatusPass, ""},
		{"scalar passes", "vector(1) > 0", StatusPass, ""},
		{
			name: "comparison with literal and aggregated lhs",
			expr: "count(node_load1) < 1", wantStatus: StatusFail,
			wantDetail: "got: count(node_load1) = 5, want < 1",
		},
		{
			name: "parenthesized comparison is unwrapped",
			expr: "(count(node_load1) < 1)", wantStatus: StatusFail,
			wantDetail: "got: count(node_load1) = 5, want < 1",
		},
		{
			name: "comparison lists up to three series",
			expr: "node_load1 > 100", wantStatus: StatusFail,
			wantDetail: `got: node_load1 => node_load1{instance="a"} = 1, node_load1{instance="b"} = 2, node_load1{instance="c"} = 3 (+2 more), want > 100`,
		},
		{
			name: "non literal rhs is evaluated too",
			expr: "sum(node_load1) > sum(node_load1) * 2", wantStatus: StatusFail,
			wantDetail: "got: sum(node_load1) = 15, want > sum(node_load1) * 2 = 30",
		},
		{
			name: "empty lhs",
			expr: "nothing_here > 5", wantStatus: StatusFail,
			wantDetail: "got: nothing_here = empty, want > 5",
		},
		{
			name: "absent lists offending series",
			expr: "absent(node_load1 > 3)", wantStatus: StatusFail,
			wantDetail: `found 2 offending series: node_load1{instance="d"} = 4, node_load1{instance="e"} = 5`,
		},
		{
			name: "absent truncates offending series",
			expr: "absent(node_load1)", wantStatus: StatusFail,
			wantDetail: `found 5 offending series: node_load1{instance="a"} = 1, node_load1{instance="b"} = 2, node_load1{instance="c"} = 3 (+2 more)`,
		},
		{"absent of nothing passes", "absent(nothing_here)", StatusPass, ""},
		{"other expressions", "nothing_here", StatusFail, "got: empty"},
		{"bool comparison is never explained as a comparison", "nothing_here > bool 5", StatusFail, "got: empty"},
		{"parse error", "node_load1{", StatusError, ""},
		{"evaluation error", "node_load1 + on(", StatusError, ""},
	}
	r := testRunner(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Assert(context.Background(), tt.expr)
			if got.Status != tt.wantStatus {
				t.Fatalf("status %q (detail %q), want %q", got.Status, got.Detail, tt.wantStatus)
			}
			switch tt.wantStatus {
			case StatusError:
				if got.Detail == "" {
					t.Error("error without detail")
				}
			default:
				if got.Detail != tt.wantDetail {
					t.Errorf("detail %q, want %q", got.Detail, tt.wantDetail)
				}
			}
		})
	}
}

func TestExitCode(t *testing.T) {
	mk := func(s ...Status) Report {
		var rep Report
		for _, st := range s {
			rep.Results = append(rep.Results, Result{Status: st})
		}
		return rep
	}
	tests := []struct {
		name string
		rep  Report
		want int
	}{
		{"all pass", mk(StatusPass, StatusPass), 0},
		{"fail", mk(StatusPass, StatusFail), 1},
		{"error", mk(StatusPass, StatusError), 2},
		{"error wins over fail", mk(StatusFail, StatusError), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rep.ExitCode(); got != tt.want {
				t.Errorf("exit code %d, want %d", got, tt.want)
			}
		})
	}
}

func sampleReport() Report {
	return Report{
		Contract:    "node.contract.promql",
		EvaluatedAt: time.Date(2026, 10, 8, 9, 30, 0, 0, time.UTC),
		Results: []Result{
			{Check: Check{Name: "it is alive", Expr: "up", Line: 2}, Status: StatusPass},
			{Check: Check{Name: "budget #1", Expr: "count(x) < 1000", Line: 5}, Status: StatusFail, Detail: "got: count(x) = 1116, want < 1000"},
			{Check: Check{Name: "bad < line", Expr: "up{", Line: 7}, Status: StatusError, Detail: "1:4: parse error"},
		},
	}
}

func TestWriteText(t *testing.T) {
	var b bytes.Buffer
	WriteText(&b, sampleReport(), false)
	want := `PASS  it is alive
FAIL  budget #1
      count(x) < 1000
      got: count(x) = 1116, want < 1000
ERROR bad < line
      up{
      line 7: 1:4: parse error
1 passed, 1 failed, 1 errors (evaluated at 2026-10-08T09:30:00Z)
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}

	t.Run("expression equal to the name is not repeated", func(t *testing.T) {
		var b bytes.Buffer
		WriteResult(&b, Result{Check: Check{Name: "x > 1", Expr: "x > 1"}, Status: StatusFail, Detail: "got: empty"}, false)
		if want := "FAIL  x > 1\n      got: empty\n"; b.String() != want {
			t.Errorf("got %q, want %q", b.String(), want)
		}
	})
	t.Run("color styles only the status word", func(t *testing.T) {
		var b bytes.Buffer
		WriteResult(&b, Result{Check: Check{Name: "n", Expr: "n"}, Status: StatusPass}, true)
		if want := "\x1b[32mPASS\x1b[0m  n\n"; b.String() != want {
			t.Errorf("got %q, want %q", b.String(), want)
		}
	})
}

func TestWriteTAP(t *testing.T) {
	var b bytes.Buffer
	WriteTAP(&b, sampleReport())
	out := b.String()
	for _, want := range []string{
		"TAP version 13\n1..3\n",
		"ok 1 - it is alive\n",
		"not ok 2 - budget \\#1\n  ---\n  status: fail\n  expr: \"count(x) < 1000\"\n  line: 5\n  detail: \"got: count(x) = 1116, want < 1000\"\n  ...\n",
		"not ok 3 - bad < line\n",
		"  status: error\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("TAP output lacks %q:\n%s", want, out)
		}
	}
}

func TestWriteJUnit(t *testing.T) {
	var b bytes.Buffer
	if err := WriteJUnit(&b, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Name     string `xml:"name,attr"`
		Tests    int    `xml:"tests,attr"`
		Failures int    `xml:"failures,attr"`
		Errors   int    `xml:"errors,attr"`
		Cases    []struct {
			Name    string `xml:"name,attr"`
			Failure *struct {
				Message string `xml:"message,attr"`
				Body    string `xml:",chardata"`
			} `xml:"failure"`
			Error *struct {
				Message string `xml:"message,attr"`
			} `xml:"error"`
		} `xml:"testcase"`
	}
	if err := xml.Unmarshal(b.Bytes(), &suite); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, b.String())
	}
	if suite.Name != "node.contract.promql" || suite.Tests != 3 || suite.Failures != 1 || suite.Errors != 1 || len(suite.Cases) != 3 {
		t.Fatalf("unexpected suite: %+v", suite)
	}
	if f := suite.Cases[1].Failure; f == nil || f.Message != "got: count(x) = 1116, want < 1000" || f.Body != "count(x) < 1000" {
		t.Errorf("failure not rendered: %+v", f)
	}
	if e := suite.Cases[2].Error; e == nil || e.Message != "1:4: parse error" {
		t.Errorf("error not rendered: %+v", e)
	}
	if suite.Cases[0].Failure != nil || suite.Cases[0].Error != nil {
		t.Error("passing case has a problem element")
	}
}

func TestWriteJSON(t *testing.T) {
	var b bytes.Buffer
	if err := WriteJSON(&b, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var got struct {
		EvaluatedAt string `json:"evaluated_at"`
		Passed      int    `json:"passed"`
		Failed      int    `json:"failed"`
		Errors      int    `json:"errors"`
		Checks      []struct {
			Name, Expr, Status, Detail string
			Line                       int
		} `json:"checks"`
	}
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.EvaluatedAt != "2026-10-08T09:30:00Z" || got.Passed != 1 || got.Failed != 1 || got.Errors != 1 || len(got.Checks) != 3 {
		t.Fatalf("unexpected report: %+v", got)
	}
	if c := got.Checks[1]; c.Name != "budget #1" || c.Status != "fail" || c.Line != 5 || c.Expr != "count(x) < 1000" {
		t.Errorf("unexpected check: %+v", c)
	}

	t.Run("empty report has an empty checks array", func(t *testing.T) {
		var b bytes.Buffer
		if err := WriteJSON(&b, Report{}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), `"checks": []`) {
			t.Errorf("checks must be [], got %s", b.String())
		}
	})
}

func TestWriteUnknownFormat(t *testing.T) {
	if err := Write(&bytes.Buffer{}, "yaml", Report{}, false); err == nil {
		t.Error("expected an error for an unknown format")
	}
}
