package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql"
	promparser "github.com/prometheus/prometheus/promql/parser"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

func assertTestEngine() *promql.Engine {
	return promql.NewEngine(promql.EngineOpts{
		MaxSamples:    1000000,
		Timeout:       10 * time.Second,
		LookbackDelta: 5 * time.Minute,
		Parser:        promparser.NewParser(promparser.Options{EnableExperimentalFunctions: true}),
	})
}

func TestHandleAdhocAssert(t *testing.T) {
	store := sstorage.NewSimpleStorage()
	for i, inst := range []string{"a", "b"} {
		store.AddSample(map[string]string{"__name__": "load", "instance": inst}, float64(i+1), 1700000000000)
	}
	prevEngine, prevPin := replEngine, pinnedEvalTime
	t.Cleanup(func() { replEngine, pinnedEvalTime = prevEngine, prevPin })
	replEngine = assertTestEngine()
	// Samples are in the past: only a pinned evaluation time can see them.
	pin := time.UnixMilli(1700000000000)
	pinnedEvalTime = &pin

	tests := []struct {
		name string
		line string
		want string
	}{
		{"pass", ".assert load", "PASS  load\n"},
		{"fail with explanation", ".assert count(load) > 5", "FAIL  count(load) > 5\n      got: count(load) = 2, want > 5\n"},
		{"fail without explanation", ".assert nope", "FAIL  nope\n      got: empty\n"},
		{"error", ".assert load{", "ERROR load{\n      "},
		{"usage", ".assert", ".assert <expr>\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := captureOutput(func() { handleAdHocFunction(tt.line, store) })
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(out, tt.want) {
				t.Errorf("got %q, want prefix %q", out, tt.want)
			}
		})
	}

	t.Run("without a pin the past samples are out of reach", func(t *testing.T) {
		pinnedEvalTime = nil
		out, _ := captureOutput(func() { handleAdHocFunction(".assert load", store) })
		if !strings.HasPrefix(out, "FAIL  load") {
			t.Errorf("got %q", out)
		}
	})
}

func TestAssertIsRegistered(t *testing.T) {
	cmd := GetAdHocCommandByName(".assert")
	if cmd == nil || cmd.Usage == "" || cmd.Description == "" || len(cmd.Examples) == 0 {
		t.Fatalf("incomplete .assert registration: %+v", cmd)
	}
}

func TestExecuteQueriesFromFileReportsFailures(t *testing.T) {
	store := sstorage.NewSimpleStorage()
	store.AddSample(map[string]string{"__name__": "up"}, 1, time.Now().UnixMilli())
	engine := assertTestEngine()

	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{"all queries fine", "up\n\nup\n", ""},
		{"parse error", "up\n\nup{\n\nup\n", "1 of 3 queries"},
		{"evaluation error", "up + on(\n", "1 of 1 queries"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "q.promql")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			var err error
			_, _ = captureOutput(func() { err = ExecuteQueriesFromFile(engine, store, path) })
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("error %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestAssertEngineSetByFileExecution(t *testing.T) {
	prevEngine, prevPin := replEngine, pinnedEvalTime
	t.Cleanup(func() { replEngine, pinnedEvalTime = prevEngine, prevPin })
	store := sstorage.NewSimpleStorage()
	store.AddSample(map[string]string{"__name__": "up"}, 1, time.Now().UnixMilli())

	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{"failing assert", ".assert vector(1) > 5\n", true},
		{"passing assert", ".assert vector(1)\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replEngine = nil // as on the query -f path: nothing but the file entry point sets it
			path := filepath.Join(t.TempDir(), "a.promql")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			var err error
			out, _ := captureOutput(func() { err = ExecuteQueriesFromFile(assertTestEngine(), store, path) })
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v\n%s", err, tt.wantErr, out)
			}
			if strings.Contains(out, "engine not available") {
				t.Errorf("engine was not set:\n%s", out)
			}
		})
	}
}

func TestAssertWithoutEngineCountsAsFailure(t *testing.T) {
	prev := replEngine
	t.Cleanup(func() { replEngine = prev })
	replEngine = nil
	before := queryFailures.Load()
	out, _ := captureOutput(func() { handleAdhocAssert(".assert up", sstorage.NewSimpleStorage()) })
	if !strings.Contains(out, "engine not available") || queryFailures.Load() != before+1 {
		t.Errorf("failures %d -> %d, out=%q", before, queryFailures.Load(), out)
	}
}

func TestAssertStaleEvalHint(t *testing.T) {
	prevEngine, prevPin := replEngine, pinnedEvalTime
	t.Cleanup(func() { replEngine, pinnedEvalTime = prevEngine, prevPin })
	replEngine = assertTestEngine()
	store := sstorage.NewSimpleStorage()
	store.AddSample(map[string]string{"__name__": "load"}, 1, 1700000000000)

	t.Run("evaluating long after the newest sample hints at .pinat", func(t *testing.T) {
		pinnedEvalTime = nil
		out, _ := captureOutput(func() { handleAdHocFunction(".assert load", store) })
		want := "hint: evaluated at "
		if !strings.Contains(out, want) || !strings.Contains(out, "but the newest sample is at 2023-11-14T22:13:20Z; try .pinat <metric> (or .pinat 2023-11-14T22:13:20Z)") {
			t.Errorf("missing hint:\n%s", out)
		}
	})
	t.Run("pinned at the data there is no hint", func(t *testing.T) {
		pin := time.UnixMilli(1700000000000)
		pinnedEvalTime = &pin
		out, _ := captureOutput(func() { handleAdHocFunction(".assert nope", store) })
		if !strings.HasPrefix(out, "FAIL  nope") || strings.Contains(out, "hint:") {
			t.Errorf("unexpected hint:\n%s", out)
		}
	})
	t.Run("within the lookback window there is no hint", func(t *testing.T) {
		pin := time.UnixMilli(1700000000000).Add(4 * time.Minute)
		pinnedEvalTime = &pin
		out, _ := captureOutput(func() { handleAdHocFunction(".assert nope", store) })
		if strings.Contains(out, "hint:") {
			t.Errorf("unexpected hint:\n%s", out)
		}
	})
}
