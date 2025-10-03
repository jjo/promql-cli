package repl

import (
	"testing"
)

func TestParseQueriesFromContent_SingleLine(t *testing.T) {
	content := `up`
	queries := parseQueriesFromContent(content)

	if len(queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queries))
	}
	if queries[0].query != "up" {
		t.Errorf("expected query 'up', got '%s'", queries[0].query)
	}
	if queries[0].startLine != 1 {
		t.Errorf("expected startLine 1, got %d", queries[0].startLine)
	}
}

func TestParseQueriesFromContent_MultipleQueriesBlankLineSeparated(t *testing.T) {
	content := `up

sum(rate(cpu[5m]))

count(memory)`

	queries := parseQueriesFromContent(content)

	if len(queries) != 3 {
		t.Fatalf("expected 3 queries, got %d", len(queries))
	}

	expected := []struct {
		query     string
		startLine int
	}{
		{"up", 1},
		{"sum(rate(cpu[5m]))", 3},
		{"count(memory)", 5},
	}

	for i, exp := range expected {
		if queries[i].query != exp.query {
			t.Errorf("query %d: expected '%s', got '%s'", i, exp.query, queries[i].query)
		}
		if queries[i].startLine != exp.startLine {
			t.Errorf("query %d: expected startLine %d, got %d", i, exp.startLine, queries[i].startLine)
		}
	}
}

func TestParseQueriesFromContent_BackslashContinuation(t *testing.T) {
	content := `up{job="prometheus"} \
  + \
  up{job="node"}`

	queries := parseQueriesFromContent(content)

	if len(queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queries))
	}

	expected := `up{job="prometheus"}    +  up{job="node"}`
	if queries[0].query != expected {
		t.Errorf("expected '%s', got '%s'", expected, queries[0].query)
	}
	if queries[0].startLine != 1 {
		t.Errorf("expected startLine 1, got %d", queries[0].startLine)
	}
}

func TestParseQueriesFromContent_NaturalMultiLine(t *testing.T) {
	content := `sum(
  rate(cpu[5m])
) by (instance)`

	queries := parseQueriesFromContent(content)

	if len(queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queries))
	}

	expected := `sum( rate(cpu[5m]) ) by (instance)`
	if queries[0].query != expected {
		t.Errorf("expected '%s', got '%s'", expected, queries[0].query)
	}
	if queries[0].startLine != 1 {
		t.Errorf("expected startLine 1, got %d", queries[0].startLine)
	}
}

func TestParseQueriesFromContent_EOFTerminator(t *testing.T) {
	// No trailing newline
	content := `up

sum(rate(cpu[5m]))`

	queries := parseQueriesFromContent(content)

	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}

	if queries[0].query != "up" {
		t.Errorf("query 0: expected 'up', got '%s'", queries[0].query)
	}
	if queries[1].query != "sum(rate(cpu[5m]))" {
		t.Errorf("query 1: expected 'sum(rate(cpu[5m]))', got '%s'", queries[1].query)
	}
}

func TestParseQueriesFromContent_CommentsIgnored(t *testing.T) {
	content := `# This is a comment
up

# Another comment
sum(rate(cpu[5m]))

# Final comment`

	queries := parseQueriesFromContent(content)

	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}

	if queries[0].query != "up" {
		t.Errorf("query 0: expected 'up', got '%s'", queries[0].query)
	}
	if queries[1].query != "sum(rate(cpu[5m]))" {
		t.Errorf("query 1: expected 'sum(rate(cpu[5m]))', got '%s'", queries[1].query)
	}
}

func TestParseQueriesFromContent_CommentsPreserveLineNumbers(t *testing.T) {
	content := `# Comment line 1
# Comment line 2
up

# Comment line 5
sum(rate(cpu[5m]))`

	queries := parseQueriesFromContent(content)

	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}

	if queries[0].startLine != 3 {
		t.Errorf("query 0: expected startLine 3, got %d", queries[0].startLine)
	}
	if queries[1].startLine != 6 {
		t.Errorf("query 1: expected startLine 6, got %d", queries[1].startLine)
	}
}

func TestParseQueriesFromContent_MultipleBlankLines(t *testing.T) {
	content := `up



sum(rate(cpu[5m]))`

	queries := parseQueriesFromContent(content)

	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}
}

func TestParseQueriesFromContent_EmptyFile(t *testing.T) {
	content := ``
	queries := parseQueriesFromContent(content)

	if len(queries) != 0 {
		t.Fatalf("expected 0 queries, got %d", len(queries))
	}
}

func TestParseQueriesFromContent_OnlyComments(t *testing.T) {
	content := `# Just comments
# Nothing else`

	queries := parseQueriesFromContent(content)

	if len(queries) != 0 {
		t.Fatalf("expected 0 queries, got %d", len(queries))
	}
}

func TestParseQueriesFromContent_OnlyBlankLines(t *testing.T) {
	content := `


`

	queries := parseQueriesFromContent(content)

	if len(queries) != 0 {
		t.Fatalf("expected 0 queries, got %d", len(queries))
	}
}

func TestParseQueriesFromContent_BackslashWithBlankLineAfter(t *testing.T) {
	// Backslash followed by blank line should end the query
	content := `sum(up) \

count(down)`

	queries := parseQueriesFromContent(content)

	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}

	// First query has trailing space before the backslash was removed
	if queries[0].query != "sum(up) " {
		t.Errorf("query 0: expected 'sum(up) ', got '%s'", queries[0].query)
	}
	if queries[1].query != "count(down)" {
		t.Errorf("query 1: expected 'count(down)', got '%s'", queries[1].query)
	}
}

func TestParseQueriesFromContent_TrailingBackslashAtEOF(t *testing.T) {
	// Trailing backslash at EOF should be handled gracefully
	content := `sum(up) \`

	queries := parseQueriesFromContent(content)

	if len(queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queries))
	}

	// Trailing space before backslash is preserved
	if queries[0].query != "sum(up) " {
		t.Errorf("expected 'sum(up) ', got '%s'", queries[0].query)
	}
}

func TestParseQueriesFromContent_DoubleBackslashNotContinuation(t *testing.T) {
	// Double backslash should not be treated as continuation
	content := `label_replace(up, "foo", "\\", "bar", ".*")

count(down)`

	queries := parseQueriesFromContent(content)

	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}

	expected := `label_replace(up, "foo", "\\", "bar", ".*")`
	if queries[0].query != expected {
		t.Errorf("expected '%s', got '%s'", expected, queries[0].query)
	}
}

func TestParseQueriesFromContent_MixedBackslashAndNatural(t *testing.T) {
	content := `sum(
  rate(cpu[5m])
) \
  by (instance)

count(memory)`

	queries := parseQueriesFromContent(content)

	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}

	// Lines are joined with single space
	expected := `sum( rate(cpu[5m]) )  by (instance)`
	if queries[0].query != expected {
		t.Errorf("expected '%s', got '%s'", expected, queries[0].query)
	}
	if queries[1].query != "count(memory)" {
		t.Errorf("expected 'count(memory)', got '%s'", queries[1].query)
	}
}

func TestParseQueriesFromContent_WindowsLineEndings(t *testing.T) {
	content := "up\r\n\r\nsum(rate(cpu[5m]))\r\n"

	queries := parseQueriesFromContent(content)

	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}

	if queries[0].query != "up" {
		t.Errorf("query 0: expected 'up', got '%s'", queries[0].query)
	}
	if queries[1].query != "sum(rate(cpu[5m]))" {
		t.Errorf("query 1: expected 'sum(rate(cpu[5m]))', got '%s'", queries[1].query)
	}
}

func TestParseQueriesFromContent_WhitespaceOnlyLines(t *testing.T) {
	content := `up


sum(rate(cpu[5m]))`

	queries := parseQueriesFromContent(content)

	// Whitespace-only lines should be treated as blank lines
	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}
}

func TestParseQueriesFromContent_ComplexRealWorld(t *testing.T) {
	content := `# Real-world example file
# with multiple queries

# Query 1: Simple selector
node_memory_MemTotal_bytes

# Query 2: Rate calculation with aggregation
sum(
  rate(
    http_requests_total[5m]
  )
) by (job, instance)

# Query 3: Complex query with backslash continuation
avg(
  rate(cpu_seconds_total[5m])
) by (instance) \
  / \
  avg(
    node_load1
  ) by (instance)

# Query 4: Final query without trailing newline
count(up == 1)`

	queries := parseQueriesFromContent(content)

	if len(queries) != 4 {
		t.Fatalf("expected 4 queries, got %d", len(queries))
	}

	// Verify query contents
	if queries[0].query != "node_memory_MemTotal_bytes" {
		t.Errorf("query 0: expected 'node_memory_MemTotal_bytes', got '%s'", queries[0].query)
	}

	expectedQ2 := "sum( rate( http_requests_total[5m] ) ) by (job, instance)"
	if queries[1].query != expectedQ2 {
		t.Errorf("query 1: expected '%s', got '%s'", expectedQ2, queries[1].query)
	}

	expectedQ3 := "avg( rate(cpu_seconds_total[5m]) ) by (instance)    /  avg( node_load1 ) by (instance)"
	if queries[2].query != expectedQ3 {
		t.Errorf("query 2: expected '%s', got '%s'", expectedQ3, queries[2].query)
	}

	if queries[3].query != "count(up == 1)" {
		t.Errorf("query 3: expected 'count(up == 1)', got '%s'", queries[3].query)
	}

	// Verify line numbers
	if queries[0].startLine != 5 {
		t.Errorf("query 0: expected startLine 5, got %d", queries[0].startLine)
	}
	if queries[1].startLine != 8 {
		t.Errorf("query 1: expected startLine 8, got %d", queries[1].startLine)
	}
}

func TestParseQueriesFromContent_EmptyLinesAfterBackslash(t *testing.T) {
	// Line with only backslash continues to next line
	content := `sum(up) \
\
count(down)`

	queries := parseQueriesFromContent(content)

	// All three lines are joined into one query
	// Line 1: "sum(up) " (backslash removed)
	// Line 2: "" (just backslash, removed, empty string not added)
	// Line 3: "count(down)"
	if len(queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queries))
	}

	expected := "sum(up)  count(down)"
	if queries[0].query != expected {
		t.Errorf("expected '%s', got '%s'", expected, queries[0].query)
	}
}

func TestParseQueriesFromContent_InlineComments(t *testing.T) {
	// PromQL doesn't support inline comments, but # in strings shouldn't be treated as comments
	content := `label_replace(up, "foo", "#bar", "baz", ".*")

count(down)`

	queries := parseQueriesFromContent(content)

	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}

	// The # inside the string should be preserved
	expected := `label_replace(up, "foo", "#bar", "baz", ".*")`
	if queries[0].query != expected {
		t.Errorf("expected '%s', got '%s'", expected, queries[0].query)
	}
}

func TestParseQueriesFromContent_AdhocCommandsNoBlankLine(t *testing.T) {
	// Adhoc commands (starting with .) should be processed immediately
	// without requiring blank lines
	content := `.load /tmp/test.prom
.metrics
sum(test_metric)

test_metric * 2

.quit`

	queries := parseQueriesFromContent(content)

	if len(queries) != 5 {
		t.Fatalf("expected 5 queries, got %d: %v", len(queries), queries)
	}

	expected := []string{
		".load /tmp/test.prom",
		".metrics",
		"sum(test_metric)",
		"test_metric * 2",
		".quit",
	}

	for i, exp := range expected {
		if queries[i].query != exp {
			t.Errorf("query %d: expected '%s', got '%s'", i, exp, queries[i].query)
		}
	}
}

func TestParseQueriesFromContent_AdhocCommandsConsecutive(t *testing.T) {
	// Multiple consecutive adhoc commands without blank lines
	content := `.load /tmp/test.prom
.metrics
.pinat now
.quit`

	queries := parseQueriesFromContent(content)

	if len(queries) != 4 {
		t.Fatalf("expected 4 queries, got %d: %v", len(queries), queries)
	}

	expected := []string{
		".load /tmp/test.prom",
		".metrics",
		".pinat now",
		".quit",
	}

	for i, exp := range expected {
		if queries[i].query != exp {
			t.Errorf("query %d: expected '%s', got '%s'", i, exp, queries[i].query)
		}
	}
}

func TestParseQueriesFromContent_AdhocAfterMultilineQuery(t *testing.T) {
	// Adhoc command immediately after multiline query should flush the query first
	content := `sum(
  test_metric
)
.metrics
.quit`

	queries := parseQueriesFromContent(content)

	if len(queries) != 3 {
		t.Fatalf("expected 3 queries, got %d: %v", len(queries), queries)
	}

	expected := []string{
		"sum( test_metric )",
		".metrics",
		".quit",
	}

	for i, exp := range expected {
		if queries[i].query != exp {
			t.Errorf("query %d: expected '%s', got '%s'", i, exp, queries[i].query)
		}
	}
}

func TestParseQueriesFromContent_MixedAdhocAndQueries(t *testing.T) {
	// Mixed adhoc commands and queries
	content := `.load /tmp/test.prom
test_metric
.metrics
sum(test_metric)
.quit`

	queries := parseQueriesFromContent(content)

	if len(queries) != 5 {
		t.Fatalf("expected 5 queries, got %d: %v", len(queries), queries)
	}

	expected := []string{
		".load /tmp/test.prom",
		"test_metric",
		".metrics",
		"sum(test_metric)",
		".quit",
	}

	for i, exp := range expected {
		if queries[i].query != exp {
			t.Errorf("query %d: expected '%s', got '%s'", i, exp, queries[i].query)
		}
	}
}
