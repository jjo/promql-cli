package repl

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fileCompletionFixture creates ./rules/ and ./rules.yaml in a temp dir and
// makes it the working directory for the duration of the test.
func fileCompletionFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules.yaml"), []byte("groups: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules", "a.yaml"), []byte("groups: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// suffixes runs the readline completer and returns the full candidates
// (typed length + suffix) so they can be compared with expected names.
func completeLine(t *testing.T, line string) []string {
	t.Helper()
	ac := NewPrometheusAutoCompleter(newTestStore(t))
	suff, length := ac.Do([]rune(line), len([]rune(line)))
	typed := []rune(line)
	word := string(typed[len(typed)-length:])
	var out []string
	for _, s := range suff {
		out = append(out, word+string(s))
	}
	sort.Strings(out)
	return out
}

func TestAutoCompleter_FileArgCommands(t *testing.T) {
	fileCompletionFixture(t)

	tests := []struct {
		name string
		line string
		want []string
	}{
		{"rules empty arg lists entries", ".rules ", []string{"rules.yaml", "rules/"}},
		{"rules partial name", ".rules ru", []string{"rules.yaml", "rules/"}},
		{"rules relative dir prefix", ".rules ./ru", []string{"rules.yaml", "rules/"}},
		{"rules inside dir", ".rules rules/", []string{"a.yaml"}},
		{"load partial name", ".load rules.", []string{"rules.yaml"}},
		{"source partial name", ".source ru", []string{"rules.yaml", "rules/"}},
		{"save partial name", ".save rules.y", []string{"rules.yaml"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := completeLine(t, tt.line)
			for _, g := range got {
				if strings.HasPrefix(g, "http_") || strings.HasPrefix(g, "up") {
					t.Fatalf("%q offered metric name %q instead of paths: %v", tt.line, g, got)
				}
			}
			if len(got) != len(tt.want) {
				t.Fatalf("%q: got %v, want entries %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestAutoCompleter_RulesAbsolutePath(t *testing.T) {
	dir := fileCompletionFixture(t)
	ac := NewPrometheusAutoCompleter(newTestStore(t))
	got := ac.getFilePathCompletions(dir+"/ru", "ru")
	want := []string{"rules.yaml", "rules/"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFileArgAfterCommand(t *testing.T) {
	tests := []struct {
		line string
		arg  string
		ok   bool
	}{
		{".rules ./ru", "./ru", true},
		{".rules ", "", true},
		{".load a", "a", true},
		{".rules", "", false},
		{".labels x", "", false},
	}
	for _, tt := range tests {
		arg, ok := fileArgAfterCommand(tt.line)
		if arg != tt.arg || ok != tt.ok {
			t.Errorf("fileArgAfterCommand(%q) = (%q,%v), want (%q,%v)", tt.line, arg, ok, tt.arg, tt.ok)
		}
	}
}

func TestPromptFileCompletions_Rules(t *testing.T) {
	fileCompletionFixture(t)
	got := getFileCompletions("ru")
	var texts []string
	for _, s := range got {
		texts = append(texts, s.Text)
	}
	want := []string{"rules/", "rules.yaml"}
	if strings.Join(texts, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", texts, want)
	}
}

// nestedTree creates talks/lightning/rules/{disk.rules.yaml,.hidden} and
// talks/lightning/demo.sh under a fresh temp cwd.
func nestedTree(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	rules := filepath.Join(dir, "talks", "lightning", "rules")
	if err := os.MkdirAll(rules, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		filepath.Join(rules, "disk.rules.yaml"),
		filepath.Join(rules, ".hidden"),
		filepath.Join(dir, "talks", "lightning", "demo.sh"),
	} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
}

func TestListPathEntries_NestedDescent(t *testing.T) {
	nestedTree(t)
	names := func(arg string) string {
		var n []string
		for _, e := range listPathEntries(arg) {
			n = append(n, e.Name)
		}
		return strings.Join(n, ",")
	}
	tests := []struct{ arg, want string }{
		{"", "talks/"},
		{"./", "talks/"},
		{"./ta", "talks/"},
		{"talks/", "lightning/"},
		{"talks/li", "lightning/"},
		{"talks/lightning/", "demo.sh,rules/"},
		{"talks/lightning/rules/", "disk.rules.yaml"},
		{"talks/lightning/rules/.", ".hidden"},
		{"talks/lightning/rules/d", "disk.rules.yaml"},
		{"talks/nope/", ""},
	}
	for _, tt := range tests {
		if got := names(tt.arg); got != tt.want {
			t.Errorf("listPathEntries(%q) = %q, want %q", tt.arg, got, tt.want)
		}
	}
}

func TestListPathEntries_HomeAndAbsolute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "d1"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"~/", "~/d", home + "/d"} {
		got := listPathEntries(arg)
		if len(got) != 1 || got[0].Name != "d1/" {
			t.Errorf("listPathEntries(%q) = %v, want [d1/]", arg, got)
		}
	}
}

func TestPromptFileCompletions_KeepTypedDir(t *testing.T) {
	nestedTree(t)
	texts := func(prefix string) string {
		var n []string
		for _, s := range getFileCompletions(prefix) {
			n = append(n, s.Text)
		}
		return strings.Join(n, ",")
	}
	tests := []struct{ prefix, want string }{
		{"talks/", "talks/lightning/"},
		{"./talks/li", "./talks/lightning/"},
		{"talks/lightning/", "talks/lightning/rules/,talks/lightning/demo.sh"},
		{"talks/lightning/rules/", "talks/lightning/rules/disk.rules.yaml"},
	}
	for _, tt := range tests {
		if got := texts(tt.prefix); got != tt.want {
			t.Errorf("getFileCompletions(%q) = %q, want %q", tt.prefix, got, tt.want)
		}
	}
}

func TestAutoCompleter_NestedReadline(t *testing.T) {
	nestedTree(t)
	for _, cmd := range fileArgCommands {
		got := completeLine(t, cmd+"talks/lightning/rules/d")
		if len(got) != 1 || got[0] != "disk.rules.yaml" {
			t.Errorf("%q: got %v", cmd, got)
		}
	}
}

func TestCollapseSlashes(t *testing.T) {
	tests := map[string]string{
		"":                 "",
		"a/b":              "a/b",
		"talks//":          "talks/",
		"talks//x///y":     "talks/x/y",
		"//abs":            "/abs",
		"http://x":         "http:/x", // only ever applied to file path arguments
		"~//d":             "~/d",
		"a/////////////b/": "a/b/",
	}
	for in, want := range tests {
		if got := collapseSlashes(in); got != want {
			t.Errorf("collapseSlashes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShouldSwallowSlash(t *testing.T) {
	tests := []struct {
		before string
		want   bool
	}{
		{".rules talks/", true},
		{"  .load a/b/", true},
		{".source ./", true},
		{".save out/", true},
		{".rules talks", false},
		{".rules ", false},
		{"rate(x[5m]) /", false},
		{"up/", false},
		{".scrape http:/", false},
		{".prom_scrape http://", false},
		{".labels foo/", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := shouldSwallowSlash(tt.before); got != tt.want {
			t.Errorf("shouldSwallowSlash(%q) = %v, want %v", tt.before, got, tt.want)
		}
	}
}

func TestPathCompletions_DoubleSlashTolerated(t *testing.T) {
	nestedTree(t)
	var all []string
	for _, e := range listPathEntries("talks//lightning//rules//") {
		all = append(all, e.Name)
	}
	if strings.Join(all, ",") != "disk.rules.yaml" {
		t.Fatalf("got %v", all)
	}
	for _, in := range []string{"talks//", "talks/lightning//", ".//talks//li", "talks/"} {
		for _, s := range getFileCompletions(in) {
			if strings.Contains(s.Text, "//") {
				t.Errorf("getFileCompletions(%q) produced %q", in, s.Text)
			}
		}
		for _, n := range listPathEntries(in) {
			if strings.Contains(n.Name, "//") {
				t.Errorf("listPathEntries(%q) produced %q", in, n.Name)
			}
		}
	}
}
