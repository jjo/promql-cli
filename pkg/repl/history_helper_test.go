package repl

import "testing"

func TestBuildFilteredHistory_PrefixAndDuplicates(t *testing.T) {
	h := []string{
		"sum(a)",
		"sum(b)",
		"sum(a)",
		"rate(a)",
	}
	// No prefix => all entries, newest first
	all := BuildFilteredHistory("", h)
	if len(all) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(all))
	}
	if all[0] != "rate(a)" || all[1] != "sum(a)" || all[2] != "sum(b)" || all[3] != "sum(a)" {
		t.Fatalf("unexpected order for no prefix: %#v", all)
	}

	// Prefix "sum" => only sum entries, duplicates preserved, newest first
	sums := BuildFilteredHistory("sum", h)
	if len(sums) != 3 {
		t.Fatalf("expected 3 entries for prefix 'sum', got %d", len(sums))
	}
	if sums[0] != "sum(a)" || sums[1] != "sum(b)" || sums[2] != "sum(a)" {
		t.Fatalf("unexpected order for prefix 'sum': %#v", sums)
	}

	// Prefix "sum(a)" => two duplicates
	sa := BuildFilteredHistory("sum(a)", h)
	if len(sa) != 2 || sa[0] != "sum(a)" || sa[1] != "sum(a)" {
		t.Fatalf("expected two 'sum(a)' entries, got %#v", sa)
	}

	// Unmatched prefix => empty
	none := BuildFilteredHistory("foo", h)
	if len(none) != 0 {
		t.Fatalf("expected 0 entries for unmatched prefix, got %d (%#v)", len(none), none)
	}
}
