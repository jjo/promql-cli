package repl

import (
	"reflect"
	"testing"
	"unsafe"

	prompt "github.com/c-bata/go-prompt"
)

func TestInputIncomplete(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"complete", "up", false},
		{"example line 1", "sort(", true},
		{"example line 2", "sort(\n  sum by (persistentvolumeclaim) (", true},
		{"example line 3", "sort(\n  sum by (persistentvolumeclaim) (\n    time_to_threshold(kubelet_volume_stats_available_bytes[6h], 0)", true},
		{"example line 4", "sort(\n  sum by (persistentvolumeclaim) (\n    time_to_threshold(kubelet_volume_stats_available_bytes[6h], 0)\n  ) / 86400 > 0", true},
		{"example complete", "sort(\n  sum by (persistentvolumeclaim) (\n    time_to_threshold(kubelet_volume_stats_available_bytes[6h], 0)\n  ) / 86400 > 0\n)", false},
		{"open bracket", "up[5m", true},
		{"open brace", "up{job=\"a\"", true},
		{"paren in double quotes", `up{a="(x"}`, false},
		{"unclosed paren after quoted paren", `sum(up{a="(x"}`, true},
		{"open double quote", `up{a="x`, true},
		{"open single quote", `up{a='x`, true},
		{"escaped quote stays open", `up{a="x\"`, true},
		{"escaped quote closed", `up{a="x\""}`, false},
		{"backtick raw backslash", "up{a=`x\\`}", false},
		{"backtick open", "up{a=`x", true},
		{"comment with paren", "up # (", false},
		{"comment after open paren", "sum( # note (", true},
		{"hash inside quotes", `up{a="#("}`, false},
		{"extra closer", "up)", false},
		{"closer then opener", ")(", true},
		{"shell line", "!echo (", false},
		{"shell line indented", "  !echo \"", false},
		{"trailing backslash", `.prom_scrape_range $PROM \`, true},
		{"trailing backslash inside quotes", "up{a=\"x|\\", true},
		{"trailing backslash then spaces", "up \\  ", true},
		{"escaped backslash is not a continuation", `up{a="x\\"}`, false},
		{"continued then complete", "up \\\n  + 1", false},
		{"shell line with backslash", `!echo a \`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inputIncomplete(tt.in); got != tt.want {
				t.Fatalf("inputIncomplete(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestJoinContinuation(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"simple", []string{"sort(", "  up", ")"}, "sort( up )"},
		{"comment stripped", []string{"sum( # note (", "up", ")"}, "sum( up )"},
		{"comment only line", []string{"sum(", "# nothing", "up)"}, "sum( up)"},
		{"hash in quotes kept", []string{`up{a="#x"}`, "# c"}, `up{a="#x"}`},
		{"escaped quote then hash", []string{`up{a="\"#"} # c`}, `up{a="\"#"}`},
		{"empty", nil, ""},
		{
			"backslash joins without space",
			[]string{`.prom_scrape_range $PROM '{__name__=~"node_load1|\`, `    node_netstat_Tcp_RetransSegs|\`, `    kubelet_volume_stats_available_bytes"}' now-6h now 30s`},
			`.prom_scrape_range $PROM '{__name__=~"node_load1|node_netstat_Tcp_RetransSegs|kubelet_volume_stats_available_bytes"}' now-6h now 30s`,
		},
		{"space before backslash inside a string is kept", []string{`{__name__=~"foo \`, `    bar"}`}, `{__name__=~"foo bar"}`},
		{"space before backslash outside a string is dropped", []string{`up  \`, "or vector(0)"}, "up or vector(0)"},
		{"backslash outside strings keeps a space", []string{`.prom_scrape_range $PROM \`, `  'up' now-1h now 30s`}, `.prom_scrape_range $PROM 'up' now-1h now 30s`},
		{"backslash outside strings never merges tokens", []string{`up\`, "or vector(0)"}, "up or vector(0)"},
		{"backslash then comment line", []string{`sum(\`, "# c", "up)"}, "sum( up)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinContinuation(tt.in); got != tt.want {
				t.Fatalf("joinContinuation(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSplitChunkLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"plain text", "abc", []string{"abc"}},
		{"lone enter", "\r", []string{"\r"}},
		{"escape seq", "\x1b[A", []string{"\x1b[A"}},
		{"paste cr", "a(\r  b\r)", []string{"a(", "\r", "  b", "\r", ")"}},
		{"paste crlf trailing", "a\r\nb\r\n", []string{"a", "\r", "b", "\r"}},
		{"paste lf", "a\nb", []string{"a", "\r", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitChunkLines([]byte(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			for i := range got {
				if string(got[i]) != tt.want[i] {
					t.Fatalf("got %q, want %q", got, tt.want)
				}
			}
		})
	}
}

// fakeParser returns the queued chunks one per Read.
type fakeParser struct {
	prompt.ConsoleParser
	chunks [][]byte
}

func (f *fakeParser) Read() ([]byte, error) {
	if len(f.chunks) == 0 {
		return nil, nil
	}
	b := f.chunks[0]
	f.chunks = f.chunks[1:]
	return b, nil
}

func TestLineSplitParserEnterOnSelectedSuggestion(t *testing.T) {
	if acceptKey == nil || prompt.GetKey(acceptKey) != prompt.F12 {
		t.Fatalf("acceptKey %q must decode to go-prompt's F12", acceptKey)
	}
	tests := []struct {
		name       string
		chunk      string
		completing func() bool
		want       []string // successive Read results
	}{
		{"Enter with a selection", "\r", func() bool { return true }, []string{string(acceptKey)}},
		{"Ctrl-J with a selection", "\n", func() bool { return true }, []string{string(acceptKey)}},
		{"Enter without a selection", "\r", func() bool { return false }, []string{"\r"}},
		{"no completing callback", "\r", nil, []string{"\r"}},
		{"Alt+Enter is untouched", "\x1b\r", func() bool { return true }, []string{"\x1b\r"}},
		{"other input is untouched", "x", func() bool { return true }, []string{"x"}},
		{"paste chunk with a selection", "a\rb", func() bool { return true }, []string{"a", string(acceptKey), "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLineSplitParser(&fakeParser{chunks: [][]byte{[]byte(tt.chunk)}})
			l.completing = tt.completing
			for i, want := range tt.want {
				if got, _ := l.Read(); string(got) != want {
					t.Fatalf("Read #%d = %q, want %q", i+1, got, want)
				}
			}
		})
	}
}

// TestPromptCompletingSeesGoPromptInternals fails if a go-prompt upgrade changes the
// unexported fields promptCompleting reads, instead of the fix silently turning off.
// prompt.New needs /dev/tty, so the Prompt is built by hand around a real
// CompletionManager.
func TestPromptCompletingSeesGoPromptInternals(t *testing.T) {
	cm := prompt.NewCompletionManager(func(prompt.Document) []prompt.Suggest {
		return []prompt.Suggest{{Text: "5m]"}, {Text: "1h]"}}
	}, 5)
	p := &prompt.Prompt{}
	f := reflect.ValueOf(p).Elem().FieldByName("completion")
	if !f.IsValid() || f.Type() != reflect.TypeOf(cm) {
		t.Fatal("go-prompt Prompt has no *CompletionManager field named completion")
	}
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(cm))

	cm.Update(*prompt.NewDocument())
	if promptCompleting(p) {
		t.Fatal("no suggestion selected yet: want false")
	}
	cm.Next() // what Tab does
	if !promptCompleting(p) {
		t.Fatal("a suggestion is selected: want true (go-prompt's completion.selected changed?)")
	}
	cm.Reset()
	if promptCompleting(p) {
		t.Fatal("after Reset: want false")
	}
	if promptCompleting(nil) || promptCompleting(&prompt.Prompt{}) {
		t.Fatal("nil prompt or nil completion manager must report false")
	}
}
