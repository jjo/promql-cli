package repl

import (
	"bytes"
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
	old := pinnedEvalTime
	pinnedEvalTime = p
	t.Cleanup(func() { pinnedEvalTime = old })
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSaveHeaderOnlyWhenPinned(t *testing.T) {
	st := pinTestStore(t)
	dir := t.TempDir()

	setPin(t, nil)
	p1 := filepath.Join(dir, "a.prom")
	handleAdhocSave(".save "+p1, st)
	b, _ := os.ReadFile(p1)
	if strings.Contains(string(b), "promql-cli:") {
		t.Fatalf("unexpected header without pin:\n%s", b)
	}

	pin := time.UnixMilli(1700000000123)
	setPin(t, &pin)
	p2 := filepath.Join(dir, "b.prom")
	handleAdhocSave(".save "+p2, st)
	b, _ = os.ReadFile(p2)
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
