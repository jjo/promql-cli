package check

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Formats lists the supported report formats.
var Formats = []string{"text", "tap", "junit", "json"}

// Write renders rep in the given format ("text" when empty). color only
// applies to the text format.
func Write(w io.Writer, format string, rep Report, color bool) error {
	switch format {
	case "", "text":
		WriteText(w, rep, color)
		return nil
	case "tap":
		WriteTAP(w, rep)
		return nil
	case "junit":
		return WriteJUnit(w, rep)
	case "json":
		return WriteJSON(w, rep)
	}
	return fmt.Errorf("unknown format %q (want %s)", format, strings.Join(Formats, "|"))
}

const (
	styleReset = "\x1b[0m"
	stylePass  = "\x1b[32m"
	styleFail  = "\x1b[31m"
	styleError = "\x1b[1;33m"
)

// WriteResult prints one check as a text block: a status line, then, for
// failures and errors, the expression (when it differs from the name) and why.
func WriteResult(w io.Writer, r Result, color bool) {
	label, style := "PASS", stylePass
	switch r.Status {
	case StatusFail:
		label, style = "FAIL", styleFail
	case StatusError:
		label, style = "ERROR", styleError
	}
	styled := label
	if color {
		styled = style + label + styleReset
	}
	const indent = "      "
	_, _ = fmt.Fprintf(w, "%s%s%s\n", styled, strings.Repeat(" ", 6-len(label)), r.Name)
	if r.Status == StatusPass {
		return
	}
	if r.Expr != r.Name {
		_, _ = fmt.Fprintf(w, "%s%s\n", indent, r.Expr)
	}
	if r.Status == StatusError && r.Line > 0 {
		_, _ = fmt.Fprintf(w, "%sline %d: %s\n", indent, r.Line, r.Detail)
		return
	}
	_, _ = fmt.Fprintf(w, "%s%s\n", indent, r.Detail)
}

// WriteText prints every result followed by a one-line summary.
func WriteText(w io.Writer, rep Report, color bool) {
	for _, r := range rep.Results {
		WriteResult(w, r, color)
	}
	p, f, e := rep.Counts()
	_, _ = fmt.Fprintf(w, "%d passed, %d failed, %d errors (evaluated at %s)\n", p, f, e, rep.EvaluatedAt.UTC().Format(time.RFC3339))
}

// WriteTAP prints the report as TAP version 13; failures carry a YAML block.
func WriteTAP(w io.Writer, rep Report) {
	_, _ = fmt.Fprintf(w, "TAP version 13\n1..%d\n", len(rep.Results))
	for i, r := range rep.Results {
		name := strings.ReplaceAll(r.Name, "#", `\#`)
		if r.Status == StatusPass {
			_, _ = fmt.Fprintf(w, "ok %d - %s\n", i+1, name)
			continue
		}
		_, _ = fmt.Fprintf(w, "not ok %d - %s\n  ---\n  status: %s\n  expr: %s\n  line: %d\n  detail: %s\n  ...\n",
			i+1, name, r.Status, strconv.Quote(r.Expr), r.Line, strconv.Quote(r.Detail))
	}
}

type junitSuite struct {
	XMLName   xml.Name    `xml:"testsuite"`
	Name      string      `xml:"name,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Errors    int         `xml:"errors,attr"`
	Timestamp string      `xml:"timestamp,attr"`
	Cases     []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Failure   *junitProblem `xml:"failure,omitempty"`
	Error     *junitProblem `xml:"error,omitempty"`
}

type junitProblem struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

// WriteJUnit prints the report as a single JUnit <testsuite>.
func WriteJUnit(w io.Writer, rep Report) error {
	_, failed, errored := rep.Counts()
	suite := junitSuite{
		Name:      rep.Contract,
		Tests:     len(rep.Results),
		Failures:  failed,
		Errors:    errored,
		Timestamp: rep.EvaluatedAt.UTC().Format(time.RFC3339),
	}
	for _, r := range rep.Results {
		c := junitCase{Name: r.Name, ClassName: rep.Contract}
		switch r.Status {
		case StatusFail:
			c.Failure = &junitProblem{Message: r.Detail, Body: r.Expr}
		case StatusError:
			c.Error = &junitProblem{Message: r.Detail, Body: r.Expr}
		}
		suite.Cases = append(suite.Cases, c)
	}
	out, err := xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s%s\n", xml.Header, out)
	return err
}

// WriteJSON prints the report as a JSON document.
func WriteJSON(w io.Writer, rep Report) error {
	p, f, e := rep.Counts()
	checks := rep.Results
	if checks == nil {
		checks = []Result{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		EvaluatedAt string   `json:"evaluated_at"`
		Passed      int      `json:"passed"`
		Failed      int      `json:"failed"`
		Errors      int      `json:"errors"`
		Checks      []Result `json:"checks"`
	}{rep.EvaluatedAt.UTC().Format(time.RFC3339), p, f, e, checks})
}
