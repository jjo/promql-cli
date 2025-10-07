package repl

import (
	"testing"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
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

// TestApplyTimestampOverride_SetMode tests the timestamp offsetting logic
func TestApplyTimestampOverride_SetMode(t *testing.T) {
	storage := sstorage.NewSimpleStorage()

	// Add initial samples with known timestamps
	storage.AddSample(map[string]string{"__name__": "test_metric", "job": "a"}, 100, 1000)
	storage.AddSample(map[string]string{"__name__": "test_metric", "job": "b"}, 200, 2000)
	storage.AddSample(map[string]string{"__name__": "test_metric", "job": "c"}, 300, 3000)

	// beforeCounts tracks initial state (empty in this case)
	beforeCounts := make(map[string]int)

	// Apply timestamp override to set latest to 10000
	// Latest is 3000, so offset should be 10000 - 3000 = 7000
	ApplyTimestampOverride(storage, beforeCounts, "set", 10000)

	// Verify all timestamps were offset by +7000
	samples := storage.Metrics["test_metric"]
	if len(samples) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(samples))
	}

	expected := []int64{8000, 9000, 10000}
	for i, expectedTs := range expected {
		if samples[i].Timestamp != expectedTs {
			t.Errorf("sample %d: expected timestamp %d, got %d", i, expectedTs, samples[i].Timestamp)
		}
	}
}

// TestApplyTimestampOverride_SetModeWithExistingSamples tests offset calculation with existing samples
func TestApplyTimestampOverride_SetModeWithExistingSamples(t *testing.T) {
	storage := sstorage.NewSimpleStorage()

	// Add initial samples
	storage.AddSample(map[string]string{"__name__": "existing", "job": "a"}, 100, 5000)
	storage.AddSample(map[string]string{"__name__": "existing", "job": "b"}, 200, 6000)

	// Track existing counts
	beforeCounts := map[string]int{
		"existing":   2,
		"new_metric": 0,
	}

	// Add new samples with different timestamps
	storage.AddSample(map[string]string{"__name__": "new_metric", "job": "x"}, 10, 1000)
	storage.AddSample(map[string]string{"__name__": "new_metric", "job": "y"}, 20, 2500)
	storage.AddSample(map[string]string{"__name__": "new_metric", "job": "z"}, 30, 3000)

	// Apply offset only to newly loaded samples (after beforeCounts)
	// Latest new sample is 3000, target is 8000, offset = 8000 - 3000 = 5000
	ApplyTimestampOverride(storage, beforeCounts, "set", 8000)

	// Verify existing samples were NOT modified
	existingSamples := storage.Metrics["existing"]
	if existingSamples[0].Timestamp != 5000 {
		t.Errorf("existing sample 0: expected timestamp 5000, got %d", existingSamples[0].Timestamp)
	}
	if existingSamples[1].Timestamp != 6000 {
		t.Errorf("existing sample 1: expected timestamp 6000, got %d", existingSamples[1].Timestamp)
	}

	// Verify new samples were offset by +5000
	newSamples := storage.Metrics["new_metric"]
	expectedNew := []int64{6000, 7500, 8000}
	for i, expectedTs := range expectedNew {
		if newSamples[i].Timestamp != expectedTs {
			t.Errorf("new sample %d: expected timestamp %d, got %d", i, expectedTs, newSamples[i].Timestamp)
		}
	}
}

// TestApplyTimestampOverride_RemoveMode tests the remove mode
func TestApplyTimestampOverride_RemoveMode(t *testing.T) {
	storage := sstorage.NewSimpleStorage()

	// Add samples with various timestamps
	storage.AddSample(map[string]string{"__name__": "test_metric", "job": "a"}, 100, 1000)
	storage.AddSample(map[string]string{"__name__": "test_metric", "job": "b"}, 200, 2000)

	beforeCounts := make(map[string]int)

	// Apply remove mode - should set all to "now" (current time)
	ApplyTimestampOverride(storage, beforeCounts, "remove", 0)

	// Verify all timestamps are set to approximately now (within 1 second)
	samples := storage.Metrics["test_metric"]
	for i, sample := range samples {
		// Timestamps should be very recent (within last second)
		if sample.Timestamp < 1000000000000 {
			t.Errorf("sample %d: timestamp %d is too old (not set to 'now')", i, sample.Timestamp)
		}
	}
}

// TestApplyTimestampOverride_KeepMode tests that keep mode doesn't modify timestamps
func TestApplyTimestampOverride_KeepMode(t *testing.T) {
	storage := sstorage.NewSimpleStorage()

	originalTimestamps := []int64{1000, 2000, 3000}
	storage.AddSample(map[string]string{"__name__": "test_metric", "job": "a"}, 100, originalTimestamps[0])
	storage.AddSample(map[string]string{"__name__": "test_metric", "job": "b"}, 200, originalTimestamps[1])
	storage.AddSample(map[string]string{"__name__": "test_metric", "job": "c"}, 300, originalTimestamps[2])

	beforeCounts := make(map[string]int)

	// Apply keep mode - should not modify timestamps
	ApplyTimestampOverride(storage, beforeCounts, "keep", 99999)

	// Verify timestamps are unchanged
	samples := storage.Metrics["test_metric"]
	for i, expectedTs := range originalTimestamps {
		if samples[i].Timestamp != expectedTs {
			t.Errorf("sample %d: expected timestamp %d (unchanged), got %d", i, expectedTs, samples[i].Timestamp)
		}
	}
}

// TestApplyTimestampOverride_MultipleMetrics tests offset with multiple metrics
func TestApplyTimestampOverride_MultipleMetrics(t *testing.T) {
	storage := sstorage.NewSimpleStorage()

	// Add samples for multiple metrics with different timestamps
	storage.AddSample(map[string]string{"__name__": "metric_a", "job": "1"}, 10, 1000)
	storage.AddSample(map[string]string{"__name__": "metric_a", "job": "2"}, 20, 2000)
	storage.AddSample(map[string]string{"__name__": "metric_b", "job": "1"}, 30, 1500)
	storage.AddSample(map[string]string{"__name__": "metric_b", "job": "2"}, 40, 3500) // Latest

	beforeCounts := make(map[string]int)

	// Target is 10000, latest is 3500, offset = 10000 - 3500 = 6500
	ApplyTimestampOverride(storage, beforeCounts, "set", 10000)

	// Verify metric_a timestamps
	samplesA := storage.Metrics["metric_a"]
	expectedA := []int64{7500, 8500}
	for i, expectedTs := range expectedA {
		if samplesA[i].Timestamp != expectedTs {
			t.Errorf("metric_a sample %d: expected timestamp %d, got %d", i, expectedTs, samplesA[i].Timestamp)
		}
	}

	// Verify metric_b timestamps
	samplesB := storage.Metrics["metric_b"]
	expectedB := []int64{8000, 10000}
	for i, expectedTs := range expectedB {
		if samplesB[i].Timestamp != expectedTs {
			t.Errorf("metric_b sample %d: expected timestamp %d, got %d", i, expectedTs, samplesB[i].Timestamp)
		}
	}
}

// TestApplyTimestampOverride_PreservesRelativeSpacing tests that relative time spacing is preserved
func TestApplyTimestampOverride_PreservesRelativeSpacing(t *testing.T) {
	storage := sstorage.NewSimpleStorage()

	// Create samples with specific spacing: 1s, 3s, 2s gaps
	storage.AddSample(map[string]string{"__name__": "test", "job": "a"}, 1, 1000)
	storage.AddSample(map[string]string{"__name__": "test", "job": "b"}, 2, 2000) // +1000ms
	storage.AddSample(map[string]string{"__name__": "test", "job": "c"}, 3, 5000) // +3000ms
	storage.AddSample(map[string]string{"__name__": "test", "job": "d"}, 4, 7000) // +2000ms

	beforeCounts := make(map[string]int)

	// Apply offset
	ApplyTimestampOverride(storage, beforeCounts, "set", 20000)

	// Verify spacing is preserved
	samples := storage.Metrics["test"]
	if len(samples) != 4 {
		t.Fatalf("expected 4 samples, got %d", len(samples))
	}

	// Calculate actual spacing
	spacing := []int64{
		samples[1].Timestamp - samples[0].Timestamp,
		samples[2].Timestamp - samples[1].Timestamp,
		samples[3].Timestamp - samples[2].Timestamp,
	}

	expectedSpacing := []int64{1000, 3000, 2000}
	for i, expected := range expectedSpacing {
		if spacing[i] != expected {
			t.Errorf("spacing %d: expected %d, got %d", i, expected, spacing[i])
		}
	}

	// Verify latest is at target
	if samples[3].Timestamp != 20000 {
		t.Errorf("latest timestamp: expected 20000, got %d", samples[3].Timestamp)
	}
}

// TestApplyTimestampOverride_EmptyStorage tests behavior with no samples
func TestApplyTimestampOverride_EmptyStorage(t *testing.T) {
	storage := sstorage.NewSimpleStorage()
	beforeCounts := make(map[string]int)

	// Should not panic with empty storage
	ApplyTimestampOverride(storage, beforeCounts, "set", 10000)

	// Verify storage is still empty
	if len(storage.Metrics) != 0 {
		t.Errorf("expected empty storage, got %d metrics", len(storage.Metrics))
	}
}

// TestParseTimestampArg tests timestamp argument parsing
func TestParseTimestampArg(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		expectedMode  string
		expectedFixed int64
		expectedOk    bool
	}{
		{
			name:          "no args returns keep mode",
			args:          []string{},
			expectedMode:  "keep",
			expectedFixed: 0,
			expectedOk:    true,
		},
		{
			name:          "timestamp=remove",
			args:          []string{"timestamp=remove"},
			expectedMode:  "remove",
			expectedFixed: 0,
			expectedOk:    true,
		},
		{
			name:          "timestamp=now",
			args:          []string{"timestamp=now"},
			expectedMode:  "set",
			expectedFixed: -1, // Special marker: we'll just check it's > 0
			expectedOk:    true,
		},
		{
			name:          "timestamp with unix seconds",
			args:          []string{"timestamp=1735732800"},
			expectedMode:  "set",
			expectedFixed: 1735732800000, // Converted to milliseconds
			expectedOk:    true,
		},
		{
			name:          "timestamp with quotes",
			args:          []string{"timestamp='now'"},
			expectedMode:  "set",
			expectedFixed: -1, // Special marker
			expectedOk:    true,
		},
		{
			name:          "timestamp with double quotes",
			args:          []string{`timestamp="remove"`},
			expectedMode:  "remove",
			expectedFixed: 0,
			expectedOk:    true,
		},
		{
			name:          "invalid timestamp format",
			args:          []string{"timestamp=invalid"},
			expectedMode:  "keep",
			expectedFixed: 0,
			expectedOk:    false,
		},
		{
			name:          "other args ignored",
			args:          []string{"regex=test", "timestamp=remove", "other=value"},
			expectedMode:  "remove",
			expectedFixed: 0,
			expectedOk:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, fixed, ok := ParseTimestampArg(tt.args)

			if ok != tt.expectedOk {
				t.Errorf("expected ok=%v, got ok=%v", tt.expectedOk, ok)
			}

			if mode != tt.expectedMode {
				t.Errorf("expected mode=%s, got mode=%s", tt.expectedMode, mode)
			}

			// For "now" tests, just verify it's a reasonable timestamp
			if tt.expectedFixed == -1 {
				if fixed < 1000000000000 {
					t.Errorf("expected timestamp > 1000000000000 (now), got %d", fixed)
				}
			} else if fixed != tt.expectedFixed {
				t.Errorf("expected fixed=%d, got fixed=%d", tt.expectedFixed, fixed)
			}
		})
	}
}
