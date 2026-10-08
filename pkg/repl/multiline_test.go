package repl

import "testing"

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
