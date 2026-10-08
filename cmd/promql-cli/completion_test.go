package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peterbourgon/ff/v3/ffcli"
)

func generate(t *testing.T, shell string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := writeCompletion(&buf, shell, newRootCommand()); err != nil {
		t.Fatalf("writeCompletion(%s): %v", shell, err)
	}
	return buf.String()
}

func TestWriteCompletionContent(t *testing.T) {
	want := []string{
		// subcommands
		"load", "query", "version", "mcp", "completion",
		// root, subcommand and logging flags
		"repl", "version", "query", "file", "output", "log.level", "log.format",
		// value lists
		"prompt", "readline", "json", "debug", "bash", "zsh", "fish",
	}
	for _, shell := range completionShells {
		t.Run(shell, func(t *testing.T) {
			out := generate(t, shell)
			for _, w := range want {
				if !strings.Contains(out, w) {
					t.Errorf("%s completion lacks %q", shell, w)
				}
			}
			for _, w := range []string{"--repl", "--query", "--log.level", "--version"} {
				if shell == "fish" {
					w = strings.TrimPrefix(w, "--")
					w = "-l " + w
				}
				if !strings.Contains(out, w) {
					t.Errorf("%s completion lacks %q", shell, w)
				}
			}
		})
	}
}

func TestWriteCompletionUnsupportedShell(t *testing.T) {
	var buf bytes.Buffer
	for _, shell := range []string{"pwsh", ""} {
		err := writeCompletion(&buf, shell, newRootCommand())
		if err == nil || !strings.Contains(err.Error(), "unsupported shell") {
			t.Errorf("shell %q: want unsupported shell error, got %v", shell, err)
		}
	}
	if buf.Len() != 0 {
		t.Errorf("unexpected output on error: %q", buf.String())
	}
}

func TestCompletionSyntax(t *testing.T) {
	checks := map[string][]string{
		"bash": {"bash", "-n"},
		"zsh":  {"zsh", "-n"},
		"fish": {"fish", "--no-execute"},
	}
	for shell, argv := range checks {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(argv[0]); err != nil {
				t.Skipf("%s not installed", argv[0])
			}
			path := filepath.Join(t.TempDir(), "completion."+shell)
			if err := os.WriteFile(path, []byte(generate(t, shell)), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(argv[0], append(argv[1:], path)...).CombinedOutput(); err != nil { //nolint:gosec // fixed argv
				t.Fatalf("%v: %v\n%s", argv, err, out)
			}
		})
	}
}

func TestBashCompletionBehaviour(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	path := filepath.Join(t.TempDir(), "completion.bash")
	if err := os.WriteFile(path, []byte(generate(t, "bash")), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		words string
		cword int
		want  []string
		deny  []string
	}{
		{"subcommand", `promql-cli qu`, 1, []string{"query"}, []string{"load"}},
		{"subcommand after root flag", `promql-cli --repl prompt lo`, 3, []string{"load"}, nil},
		{"query flags", `promql-cli query --`, 2, []string{"--query", "--file", "--output"}, []string{"--repl"}},
		{"root flags", `promql-cli --`, 1, []string{"--repl", "--log.level", "--version"}, []string{"--query"}},
		{"repl values", `promql-cli --repl ""`, 2, []string{"prompt", "readline"}, nil},
		{"output values", `promql-cli query -o ""`, 3, []string{"json"}, nil},
		{"log level values", `promql-cli --log.level d`, 2, []string{"debug"}, []string{"info"}},
		{"completion shells", `promql-cli completion ""`, 2, []string{"bash", "zsh", "fish"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := `source "$1"; COMP_WORDS=(` + tc.words + `); COMP_CWORD=` + string(rune('0'+tc.cword)) +
				`; _promql_cli; echo "${COMPREPLY[@]}"`
			out, err := exec.Command("bash", "-c", script, "bash", path).Output() //nolint:gosec // test-controlled input
			if err != nil {
				t.Fatalf("bash: %v", err)
			}
			got := strings.Fields(string(out))
			for _, w := range tc.want {
				if !contains(got, w) {
					t.Errorf("want %q in %v", w, got)
				}
			}
			for _, w := range tc.deny {
				if contains(got, w) {
					t.Errorf("unexpected %q in %v", w, got)
				}
			}
		})
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// zsh _describe collapses entries without a description onto one line, so every
// subcommand (including the ones that do not set ShortHelp) must carry one.
func TestZshSubcommandsHaveDescriptions(t *testing.T) {
	root := newRootCommand()
	out := generate(t, "zsh")
	for _, sub := range root.Subcommands {
		var found bool
		for line := range strings.SplitSeq(out, "\n") {
			if desc, ok := strings.CutPrefix(strings.TrimSpace(line), "'"+sub.Name+":"); ok {
				found = true
				if strings.Trim(desc, "'") == "" {
					t.Errorf("zsh completion for %q has an empty description", sub.Name)
				}
			}
		}
		if !found {
			t.Errorf("zsh completion lacks an entry for %q", sub.Name)
		}
	}
}

func TestSubcommandHelpFallback(t *testing.T) {
	tests := []struct {
		name string
		cmd  *ffcli.Command
		want string
	}{
		{"short help wins", &ffcli.Command{Name: "x", ShortUsage: "usage", ShortHelp: "help"}, "help"},
		{"falls back to usage", &ffcli.Command{Name: "x", ShortUsage: "usage"}, "usage"},
		{"falls back to name", &ffcli.Command{Name: "x"}, "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subcommandHelp(tt.cmd); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckFormatCompletion(t *testing.T) {
	out := generate(t, "bash")
	for _, v := range []string{"tap", "junit"} {
		if !strings.Contains(out, v) {
			t.Errorf("bash completion lacks --format value %q", v)
		}
	}
}
