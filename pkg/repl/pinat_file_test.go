package repl

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

const pinTestData = `a{i="1"} 1 1000000
a{i="2"} 2 2000000
b 3 3000000
`

func pinTestStore(t *testing.T) *sstorage.SimpleStorage {
	t.Helper()
	st := sstorage.NewSimpleStorage()
	if err := st.LoadFromReader(strings.NewReader(pinTestData)); err != nil {
		t.Fatal(err)
	}
	return st
}

func setPin(t *testing.T, p *time.Time) {
	t.Helper()
	old, oldSrc := pinnedEvalTime, pinFromHeader
	pinnedEvalTime, pinFromHeader = p, ""
	t.Cleanup(func() { pinnedEvalTime, pinFromHeader = old, oldSrc })
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSaveHeader(t *testing.T) {
	st := pinTestStore(t)
	dir := t.TempDir()
	firstLine := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return strings.SplitN(string(b), "\n", 2)[0]
	}

	// without a pin: the newest sample, where the data ends
	setPin(t, nil)
	p1 := filepath.Join(dir, "a.prom")
	handleAdhocSave(".save "+p1, st)
	if got := firstLine(p1); got != "# promql-cli: pinat=1970-01-01T00:50:00.000Z" {
		t.Fatalf("unpinned header = %q, want the newest sample time", got)
	}

	// without a pin and with regex=: the newest sample among the saved series only
	p0 := filepath.Join(dir, "a-only.prom")
	handleAdhocSave(".save "+p0+" regex='^a'", st)
	if got := firstLine(p0); got != "# promql-cli: pinat=1970-01-01T00:33:20.000Z" {
		t.Fatalf("unpinned regex header = %q, want the newest saved sample time", got)
	}

	// empty store: no header
	pe := filepath.Join(dir, "empty.prom")
	handleAdhocSave(".save "+pe, sstorage.NewSimpleStorage())
	if b, _ := os.ReadFile(pe); strings.Contains(string(b), "promql-cli:") {
		t.Fatalf("unexpected header for an empty store:\n%s", b)
	}

	pin := time.UnixMilli(1700000000123)
	setPin(t, &pin)
	p2 := filepath.Join(dir, "b.prom")
	handleAdhocSave(".save "+p2, st)
	b, _ := os.ReadFile(p2)
	if first := strings.SplitN(string(b), "\n", 2)[0]; first != "# promql-cli: pinat=2023-11-14T22:13:20.123Z" {
		t.Fatalf("bad header %q", first)
	}
	// still loads with the existing parser
	if err := sstorage.NewSimpleStorage().LoadFromReader(bytes.NewReader(b)); err != nil {
		t.Fatalf("file with header does not load: %v", err)
	}

	// timestamp= rewrite: no header
	p3 := filepath.Join(dir, "c.prom")
	handleAdhocSave(".save "+p3+" timestamp=remove", st)
	b, _ = os.ReadFile(p3)
	if strings.Contains(string(b), "promql-cli:") {
		t.Fatalf("unexpected header with timestamp=remove:\n%s", b)
	}
}

func TestPinRoundTrip(t *testing.T) {
	st := pinTestStore(t)
	pin := time.UnixMilli(1700000000123)
	setPin(t, &pin)
	p := filepath.Join(t.TempDir(), "rt.prom")
	handleAdhocSave(".save "+p, st)

	pinnedEvalTime = nil
	handleAdhocLoad(".load "+p, sstorage.NewSimpleStorage())
	if pinnedEvalTime == nil || pinnedEvalTime.UnixMilli() != 1700000000123 {
		t.Fatalf("pin not restored exactly: %v", pinnedEvalTime)
	}
}

func TestLoadPinatOptions(t *testing.T) {
	prev := time.UnixMilli(42)
	tests := []struct {
		name    string
		header  bool
		args    string
		wantMs  int64 // 0 => expect previous pin unchanged
		wantMsg string
	}{
		{"last", false, "pinat=last", 3000000, "from pinat=last"},
		{"first", false, "pinat=first", 1000000, "from pinat=first"},
		{"none ignores header", true, "pinat=none", 0, ""},
		{"timespec", false, "pinat=2026-01-02T03:04:05Z", 1767323045000, "from pinat="},
		{"selector", false, "pinat=a", 2000000, "from pinat=a"},
		{"explicit overrides header", true, "pinat=first", 1000000, "from pinat=first"},
		{"header restored", true, "", 1700000000123, "restored from"},
		{"timestamp skips header", true, "timestamp=2025-01-01T00:00:00Z", 0, "not restored"},
		{"bad pinat keeps pin", false, "pinat=nosuchmetric", 0, "unchanged"},
		{"timestamp plus pinat=last", true, "timestamp=2025-01-01T00:00:00Z pinat=last", 1735689600000, "from pinat=last"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := prev
			setPin(t, &p)
			content := pinTestData
			if tt.header {
				content = "# promql-cli: pinat=2023-11-14T22:13:20.123Z\n" + content
			}
			path := writeFile(t, "x.prom", content)
			st := sstorage.NewSimpleStorage()
			r, w, _ := os.Pipe()
			old := os.Stdout
			os.Stdout = w
			handleAdhocLoad(".load "+path+" "+tt.args, st)
			_ = w.Close()
			os.Stdout = old
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r)
			out := buf.String()

			if len(st.Metrics) == 0 {
				t.Fatalf("data did not load: %s", out)
			}
			want := tt.wantMs
			if want == 0 {
				want = 42
			}
			if pinnedEvalTime == nil || pinnedEvalTime.UnixMilli() != want {
				t.Fatalf("pin = %v, want %d\n%s", pinnedEvalTime, want, out)
			}
			if !strings.Contains(out, tt.wantMsg) {
				t.Fatalf("output missing %q:\n%s", tt.wantMsg, out)
			}
		})
	}
}

func TestLoadPinatLastOnlyThisCommand(t *testing.T) {
	setPin(t, nil)
	st := sstorage.NewSimpleStorage()
	_ = st.LoadFromReader(strings.NewReader("old 1 9000000\n"))
	path := writeFile(t, "x.prom", pinTestData)
	handleAdhocLoad(".load "+path+" pinat=last", st)
	if pinnedEvalTime == nil || pinnedEvalTime.UnixMilli() != 3000000 {
		t.Fatalf("pin = %v, want samples of this load only", pinnedEvalTime)
	}
}

func TestApplyLoadPinPositionalFile(t *testing.T) {
	setPin(t, nil)
	path := writeFile(t, "x.prom", "# promql-cli: pinat=2023-11-14T22:13:20.123Z\n"+pinTestData)
	var out bytes.Buffer
	ApplyLoadPin(&out, pinTestStore(t), map[string]int{}, path, "", false, false)
	if pinnedEvalTime == nil || pinnedEvalTime.UnixMilli() != 1700000000123 {
		t.Fatalf("pin = %v", pinnedEvalTime)
	}
	setPin(t, nil)
	out.Reset()
	ApplyLoadPin(&out, pinTestStore(t), map[string]int{}, path, "", false, true)
	if pinnedEvalTime != nil || !strings.Contains(out.String(), "not restored") {
		t.Fatalf("expected skip with note, pin=%v out=%q", pinnedEvalTime, out.String())
	}
}

// loadHeaderPin loads a saved-style file (header pinat=1000s, one sample at 1000s) and
// returns its path and the store, leaving a header-derived pin behind.
func loadHeaderPin(t *testing.T) (string, *sstorage.SimpleStorage) {
	t.Helper()
	setPin(t, nil)
	path := writeFile(t, "snap.prom", "# promql-cli: pinat=1970-01-01T00:16:40.000Z\nold 1 1000000\n")
	st := sstorage.NewSimpleStorage()
	var out bytes.Buffer
	before := sampleCounts(st)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := st.LoadFromReader(f); err != nil {
		t.Fatal(err)
	}
	ApplyLoadPin(&out, st, before, path, "", false, false)
	if pinnedEvalTime == nil || pinFromHeader != path {
		t.Fatalf("header pin not restored: pin=%v src=%q", pinnedEvalTime, pinFromHeader)
	}
	return path, st
}

func TestHeaderPinDroppedWhenNewerDataIsAdded(t *testing.T) {
	t.Run("scrape", func(t *testing.T) {
		path, st := loadHeaderPin(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, "fresh 1\n")
		}))
		defer srv.Close()
		out, _ := captureOutput(func() { handleAdhocScrape(".scrape "+srv.URL, st) })
		if pinnedEvalTime != nil {
			t.Fatalf("header pin must be dropped, still %v", pinnedEvalTime)
		}
		want := "Unpinned evaluation time (it was restored from " + path + "; newer data was added)"
		if strings.Count(out, want) != 1 {
			t.Errorf("want exactly one note %q in:\n%s", want, out)
		}
	})
	t.Run("load of a file without header", func(t *testing.T) {
		_, st := loadHeaderPin(t)
		more := writeFile(t, "more.prom", "newer 1 2000000\n")
		out, _ := captureOutput(func() { handleAdhocLoad(".load "+more, st) })
		if pinnedEvalTime != nil || !strings.Contains(out, "Unpinned evaluation time") {
			t.Errorf("pin=%v out=%q", pinnedEvalTime, out)
		}
	})
	t.Run("load of older data keeps the pin", func(t *testing.T) {
		_, st := loadHeaderPin(t)
		older := writeFile(t, "older.prom", "older 1 500000\n")
		_, _ = captureOutput(func() { handleAdhocLoad(".load "+older, st) })
		if pinnedEvalTime == nil {
			t.Error("older samples must not drop the pin")
		}
	})
	t.Run("explicit pin is never dropped", func(t *testing.T) {
		_, st := loadHeaderPin(t)
		_, _ = captureOutput(func() { handleAdhocPinAt(".pinat 1970-01-01T00:16:40Z", st) })
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, "fresh 1\n")
		}))
		defer srv.Close()
		out, _ := captureOutput(func() { handleAdhocScrape(".scrape "+srv.URL, st) })
		if pinnedEvalTime == nil || strings.Contains(out, "Unpinned") {
			t.Errorf("explicit pin dropped: pin=%v out=%q", pinnedEvalTime, out)
		}
	})
	t.Run("pinat= on load is explicit", func(t *testing.T) {
		_, st := loadHeaderPin(t)
		more := writeFile(t, "more.prom", "newer 1 2000000\n")
		_, _ = captureOutput(func() { handleAdhocLoad(".load "+more+" pinat=first", st) })
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, "fresh 1\n")
		}))
		defer srv.Close()
		_, _ = captureOutput(func() { handleAdhocScrape(".scrape "+srv.URL, st) })
		if pinnedEvalTime == nil {
			t.Error("pin set by pinat= must survive a scrape")
		}
	})
}

func TestScrapeRefreshesCompletionCacheOnError(t *testing.T) {
	calls := 0
	old := refreshMetricsCache
	refreshMetricsCache = func(*sstorage.SimpleStorage) { calls++ }
	t.Cleanup(func() { refreshMetricsCache = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	out, _ := captureOutput(func() { handleAdhocScrape(".scrape "+srv.URL, sstorage.NewSimpleStorage()) })
	if calls != 1 || !strings.Contains(out, "HTTP 500") {
		t.Errorf("refresh calls = %d (want 1), out=%q", calls, out)
	}
}

func TestSaveLoadCompressedRoundTrip(t *testing.T) {
	pin := time.UnixMilli(1700000000123)
	for _, ext := range []string{".prom", ".prom.gz", ".prom.zst", ".prom.zstd"} {
		t.Run(ext, func(t *testing.T) {
			st := pinTestStore(t)
			setPin(t, &pin)
			p := filepath.Join(t.TempDir(), "snap"+ext)
			out := captureStdout(t, func() { handleAdhocSave(".save "+p, st) })
			if !strings.Contains(out, "Saved store to") {
				t.Fatalf("save failed: %s", out)
			}

			// the pin header is readable through the compression layer
			if at, ok := ReadPinHeader(p); !ok || at.UnixMilli() != pin.UnixMilli() {
				t.Fatalf("ReadPinHeader = %v, %v", at, ok)
			}

			got := sstorage.NewSimpleStorage()
			pinnedEvalTime = nil
			handleAdhocLoad(".load "+p, got)
			if pinnedEvalTime == nil || pinnedEvalTime.UnixMilli() != pin.UnixMilli() {
				t.Fatalf("pin not restored: %v", pinnedEvalTime)
			}
			if n, want := countSamples(got), countSamples(st); n != want || n != 3 {
				t.Fatalf("loaded %d samples, want %d", n, want)
			}
		})
	}
}

func TestLoadCorruptCompressedFile(t *testing.T) {
	good := filepath.Join(t.TempDir(), "good.prom.zst")
	captureStdout(t, func() { handleAdhocSave(".save "+good, pinTestStore(t)) })
	raw, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad.prom.zst")
	if err := os.WriteFile(bad, raw[:len(raw)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	st := sstorage.NewSimpleStorage()
	out := captureStdout(t, func() { handleAdhocLoad(".load "+bad, st) })
	if !strings.Contains(out, "Failed to load metrics from") {
		t.Fatalf("expected a load failure message, got %q", out)
	}
	if countSamples(st) != 0 {
		t.Fatalf("truncated file must not load samples, got %d", countSamples(st))
	}
}

func countSamples(st *sstorage.SimpleStorage) int {
	n := 0
	for _, ss := range st.Metrics {
		n += len(ss)
	}
	return n
}
