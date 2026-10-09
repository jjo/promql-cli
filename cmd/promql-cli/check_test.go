package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	repl "github.com/jjo/promql-cli/pkg/repl"
	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

const exampleProm = "../../examples/example.prom"

type checkRun struct {
	code           int
	stdout, stderr string
}

// runCheckArgs runs the check command logic on a fresh engine and store.
func runCheckArgs(t *testing.T, opts checkOptions) checkRun {
	t.Helper()
	engine, store := newEngine(), sstorage.NewSimpleStorage()
	t.Cleanup(func() { repl.RunInitCommands(engine, store, ".pinat remove", true) })
	var stdout, stderr bytes.Buffer
	err := runCheck(context.Background(), opts, engine, store, &stdout, &stderr)
	code := 0
	if err != nil {
		var ok bool
		if code, ok = exitCodeOf(err); !ok {
			t.Fatalf("error without exit code: %v", err)
		}
		if ee, ok := err.(*exitError); ok && ee.err != nil { //nolint:errorlint // test inspects the concrete type
			stderr.WriteString(ee.err.Error())
		}
	}
	return checkRun{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func TestRunCheckExitCodesAndOutput(t *testing.T) {
	tests := []struct {
		name       string
		contract   string
		wantCode   int
		wantStdout []string
	}{
		{"all checks pass", "pass.contract.promql", 0, []string{"PASS  services are up", "PASS  traffic exists", "2 passed, 0 failed, 0 errors"}},
		{
			name: "failures exit 1 and say why", contract: "fail.contract.promql", wantCode: 1,
			wantStdout: []string{
				"PASS  exporter is alive",
				"FAIL  cardinality budget",
				"count(up) > 10",
				"got: count(up) = 2, want > 10",
				"FAIL  no 500s at all",
				`found 1 offending series: http_requests_total{`,
				"1 passed, 2 failed, 0 errors",
			},
		},
		{"parse errors exit 2 but the rest still runs", "error.contract.promql", 2, []string{"PASS  fine", "ERROR typo", "line 5: "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCheckArgs(t, checkOptions{format: "text", count: 1, args: []string{filepath.Join("testdata", tt.contract), exampleProm}})
			if got.code != tt.wantCode {
				t.Errorf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", got.code, tt.wantCode, got.stdout, got.stderr)
			}
			for _, w := range tt.wantStdout {
				if !strings.Contains(got.stdout, w) {
					t.Errorf("stdout lacks %q:\n%s", w, got.stdout)
				}
			}
			if strings.Contains(got.stdout, "Loaded") || strings.Contains(got.stdout, "Storage contains") {
				t.Errorf("startup noise on stdout:\n%s", got.stdout)
			}
		})
	}
}

func TestRunCheckFormatsKeepStdoutClean(t *testing.T) {
	contract := filepath.Join("testdata", "fail.contract.promql")
	t.Run("json", func(t *testing.T) {
		got := runCheckArgs(t, checkOptions{format: "json", count: 1, args: []string{contract, exampleProm}})
		var rep struct {
			Passed, Failed, Errors int
			Checks                 []struct{ Status string }
		}
		if err := json.Unmarshal([]byte(got.stdout), &rep); err != nil {
			t.Fatalf("stdout is not pure JSON: %v\n%s", err, got.stdout)
		}
		if rep.Passed != 1 || rep.Failed != 2 || len(rep.Checks) != 3 || got.code != 1 {
			t.Errorf("unexpected report %+v, code %d", rep, got.code)
		}
	})
	t.Run("tap", func(t *testing.T) {
		got := runCheckArgs(t, checkOptions{format: "tap", count: 1, args: []string{contract, exampleProm}})
		if !strings.HasPrefix(got.stdout, "TAP version 13\n1..3\nok 1 - exporter is alive\nnot ok 2 - cardinality budget\n") {
			t.Errorf("unexpected TAP:\n%s", got.stdout)
		}
	})
	t.Run("junit", func(t *testing.T) {
		got := runCheckArgs(t, checkOptions{format: "junit", count: 1, args: []string{contract, exampleProm}})
		if !strings.HasPrefix(got.stdout, "<?xml") || !strings.Contains(got.stdout, `<testsuite name="testdata/fail.contract.promql"`) {
			t.Errorf("unexpected JUnit:\n%s", got.stdout)
		}
	})
}

func TestRunCheckUsageErrors(t *testing.T) {
	pass := filepath.Join("testdata", "pass.contract.promql")
	tests := []struct {
		name string
		opts checkOptions
	}{
		{"no arguments", checkOptions{format: "text", count: 1}},
		{"no data source", checkOptions{format: "text", count: 1, args: []string{pass}}},
		{"unknown format", checkOptions{format: "yaml", count: 1, args: []string{pass, exampleProm}}},
		{"missing contract", checkOptions{format: "text", count: 1, args: []string{"testdata/nope.promql", exampleProm}}},
		{"missing data file", checkOptions{format: "text", count: 1, args: []string{pass, "testdata/nope.prom"}}},
		{"bad --at", checkOptions{format: "text", count: 1, at: "yesterday-ish", args: []string{pass, exampleProm}}},
		{"unreachable scrape", checkOptions{format: "text", count: 1, scrape: []string{"http://127.0.0.1:1/metrics"}, args: []string{pass}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCheckArgs(t, tt.opts)
			if got.code != 2 {
				t.Errorf("exit code %d, want 2 (stderr %q)", got.code, got.stderr)
			}
			if got.stderr == "" {
				t.Error("no message for the user")
			}
		})
	}
}

func TestRunCheckEvaluationTime(t *testing.T) {
	// Samples far in the past: "now" cannot see them, the newest sample can.
	data := filepath.Join(t.TempDir(), "old.prom")
	if err := os.WriteFile(data, []byte("up{job=\"x\"} 1 1700000000000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	contract := filepath.Join(t.TempDir(), "c.promql")
	if err := os.WriteFile(contract, []byte("up\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("defaults to the newest sample", func(t *testing.T) {
		got := runCheckArgs(t, checkOptions{format: "text", count: 1, args: []string{contract, data}})
		if got.code != 0 || !strings.Contains(got.stdout, "evaluated at 2023-11-14T22:13:20Z") {
			t.Errorf("code %d:\n%s", got.code, got.stdout)
		}
	})
	t.Run("--at overrides", func(t *testing.T) {
		got := runCheckArgs(t, checkOptions{format: "text", count: 1, at: "now", args: []string{contract, data}})
		if got.code != 1 {
			t.Errorf("code %d, want 1:\n%s", got.code, got.stdout)
		}
	})
	t.Run("restores the pin saved in the file header", func(t *testing.T) {
		pinned := filepath.Join(t.TempDir(), "pinned.prom")
		body := "# promql-cli: pinat=2023-11-14T22:13:20.000Z\nup{job=\"x\"} 1 1700000000000\nup{job=\"x\"} 1 1700000500000\n"
		if err := os.WriteFile(pinned, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got := runCheckArgs(t, checkOptions{format: "text", count: 1, args: []string{contract, pinned}})
		if got.code != 0 || !strings.Contains(got.stdout, "evaluated at 2023-11-14T22:13:20Z") {
			t.Errorf("code %d:\n%s", got.code, got.stdout)
		}
	})
}

func TestQueryFileExitsNonZeroOnFailedQuery(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.promql")
	bad := filepath.Join(dir, "bad.promql")
	if err := os.WriteFile(good, []byte("up\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("up\n\nup{\n\nup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		file    string
		wantErr bool
	}{{"all queries fine", good, false}, {"a query fails to parse", bad, true}} {
		t.Run(tt.name, func(t *testing.T) {
			root := newRootCommand()
			err := root.ParseAndRun(context.Background(), []string{"query", "-s", "-f", tt.file, exampleProm})
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestQueryFileAssertExitStatus(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tt := range []struct {
		name    string
		file    string
		wantErr bool
	}{
		{"failing assert", write("fail.promql", ".assert vector(1) > 5\n"), true},
		{"passing assert", write("pass.promql", ".assert vector(1)\n"), false},
		{"bare assert", write("bare.promql", ".assert\n"), true},
		{"unreadable .source", write("src.promql", ".source "+filepath.Join(dir, "missing.promql")+"\n"), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := newRootCommand()
			err := root.ParseAndRun(context.Background(), []string{"query", "-s", "-f", tt.file, exampleProm})
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckFlagErrorsExitTwo(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int // 0: not an exitError
	}{
		{"unknown flag", []string{"check", "--bogus", "c.promql", "d.prom"}, 2},
		{"bad value", []string{"check", "--count", "many", "c.promql", "d.prom"}, 2},
		{"missing value", []string{"check", "--at"}, 2},
		{"other subcommands keep the generic failure", []string{"query", "--bogus"}, 0},
		{"a root flag error before check keeps the generic failure", []string{"--bogus", "check", "c.promql"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRootCommand()
			err := root.Parse(normalizeLongOpts(tt.args))
			if err == nil {
				t.Fatal("expected a parse error")
			}
			code, ok := exitCodeOf(parseFailure(root, err))
			if tt.wantCode == 0 && ok || tt.wantCode != 0 && code != tt.wantCode {
				t.Errorf("exit code = %d (exitError=%v), want %d", code, ok, tt.wantCode)
			}
		})
	}
	t.Run("help is not an error", func(t *testing.T) {
		root := newRootCommand()
		if err := root.Parse([]string{"check", "-h"}); !errors.Is(err, flag.ErrHelp) {
			t.Errorf("err = %v, want flag.ErrHelp", err)
		}
	})
}

func TestCheckScrapeEvaluatesAtNewestSampleNotHeaderPin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "fresh_metric 1\n")
	}))
	defer srv.Close()
	dir := t.TempDir()
	saved := filepath.Join(dir, "old.prom")
	if err := os.WriteFile(saved, []byte("# promql-cli: pinat=2020-01-01T00:00:00.000Z\nold_metric 1 1577836800000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	contract := filepath.Join(dir, "c.promql")
	if err := os.WriteFile(contract, []byte("fresh_metric\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runCheckArgs(t, checkOptions{format: "text", count: 1, scrape: []string{srv.URL}, args: []string{contract, saved}})
	if got.code != 0 || strings.Contains(got.stdout, "evaluated at 2020-01-01") {
		t.Errorf("code %d, evaluated at the header pin instead of the newest sample:\n%s", got.code, got.stdout)
	}
}

func TestQueryPinMessageGoesToStderr(t *testing.T) {
	saved := filepath.Join(t.TempDir(), "snap.prom")
	if err := os.WriteFile(saved, []byte("# promql-cli: pinat=2020-01-01T00:00:00.000Z\nold_metric 1 1577836800000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repl.RunInitCommands(newEngine(), sstorage.NewSimpleStorage(), ".pinat remove", true) })

	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	os.Stdout, os.Stderr = outW, errW
	runErr := newRootCommand().ParseAndRun(context.Background(), []string{"query", "-s", "-q", "count(old_metric)", saved})
	os.Stdout, os.Stderr = oldOut, oldErr
	_ = outW.Close()
	_ = errW.Close()
	stdout, _ := io.ReadAll(outR)
	stderr, _ := io.ReadAll(errR)
	if runErr != nil {
		t.Fatal(runErr)
	}
	if strings.Contains(string(stdout), "Pinned evaluation time") || !strings.Contains(string(stdout), "1") {
		t.Errorf("stdout must carry only the result, got %q", stdout)
	}
	if !strings.Contains(string(stderr), "Pinned evaluation time: 2020-01-01T00:00:00.000Z (restored from "+saved+")") {
		t.Errorf("stderr lacks the pin message: %q", stderr)
	}
}

func TestCompressedDataFiles(t *testing.T) {
	data, err := os.ReadFile(exampleProm)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	zst := filepath.Join(dir, "example.prom.zst")
	gz := filepath.Join(dir, "example.prom.gz")
	for _, p := range []string{zst, gz} {
		w, err := sstorage.CreateMaybeCompressed(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	truncated := filepath.Join(dir, "trunc.prom.zst")
	raw, _ := os.ReadFile(zst)
	if err := os.WriteFile(truncated, raw[:len(raw)/2], 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("check passes on compressed data", func(t *testing.T) {
		for _, p := range []string{zst, gz} {
			got := runCheckArgs(t, checkOptions{format: "text", count: 1, args: []string{filepath.Join("testdata", "pass.contract.promql"), p}})
			if got.code != 0 || !strings.Contains(got.stdout, "2 passed, 0 failed, 0 errors") {
				t.Errorf("%s: exit %d\nstdout:\n%s\nstderr:\n%s", p, got.code, got.stdout, got.stderr)
			}
		}
	})
	t.Run("check reports a truncated file", func(t *testing.T) {
		got := runCheckArgs(t, checkOptions{format: "text", count: 1, args: []string{filepath.Join("testdata", "pass.contract.promql"), truncated}})
		if got.code == 0 {
			t.Errorf("expected a non-zero exit for a truncated file\nstdout:\n%s", got.stdout)
		}
	})
	t.Run("positional query load", func(t *testing.T) {
		t.Cleanup(func() { repl.RunInitCommands(newEngine(), sstorage.NewSimpleStorage(), ".pinat remove", true) })
		for _, p := range []string{zst, gz} {
			oldOut := os.Stdout
			outR, outW, _ := os.Pipe()
			os.Stdout = outW
			runErr := newRootCommand().ParseAndRun(context.Background(), []string{"query", "-s", "-q", "count(up)", p})
			os.Stdout = oldOut
			_ = outW.Close()
			stdout, _ := io.ReadAll(outR)
			if runErr != nil || !strings.Contains(string(stdout), "2") {
				t.Errorf("%s: err=%v stdout=%q", p, runErr, stdout)
			}
		}
		if err := newRootCommand().ParseAndRun(context.Background(), []string{"query", "-s", "-q", "count(up)", truncated}); err == nil {
			t.Error("expected an error loading a truncated file")
		}
	})
}
