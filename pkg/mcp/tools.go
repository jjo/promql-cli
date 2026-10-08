package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"

	repl "github.com/jjo/promql-cli/pkg/repl"
	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// ---------------------------------------------------------------------------
// Tool protocol types
// ---------------------------------------------------------------------------

// toolDefinition describes one tool in the tools/list response.
//
// Annotations tell MCP clients how to surface the tool:
//
//	readOnlyHint=true      — no mutations
//	destructiveHint=true   — may mutate state; clients confirm before calling
//	idempotentHint=true    — repeating the same call has no extra effect
//	openWorldHint=true     — may reach external systems (network, files)
type toolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations *toolAnnotation `json:"annotations,omitempty"`
}

type toolAnnotation struct {
	DestructiveHint bool `json:"destructiveHint,omitempty"`
	IdempotentHint  bool `json:"idempotentHint,omitempty"`
	OpenWorldHint   bool `json:"openWorldHint,omitempty"`
	ReadOnlyHint    bool `json:"readOnlyHint,omitempty"`
}

// toolCallResult is the result object for tools/call responses.
type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError"`
}

type toolContent struct {
	Type string `json:"type"` // always "text"
	Text string `json:"text"`
}

// per-query execution settings.
var (
	// mcpParser validates PromQL expressions before execution.
	mcpParser parser.Parser = parser.NewParser(parser.Options{EnableExperimentalFunctions: true})
	// promqlTimeout is the per-query timeout (overridden in tests).
	promqlTimeout = 30 * time.Second
)

// ---------------------------------------------------------------------------
// Tool registry
// ---------------------------------------------------------------------------

// toolHandler is the signature every tool handler implements.
type toolHandlerFunc func(*Server, json.RawMessage) (json.RawMessage, error)

// toolEntry couples a tool's advertised definition with its handler.
type toolEntry struct {
	def     toolDefinition
	handler toolHandlerFunc
}

// tools is the ordered registry of MCP tools. Order is preserved in
// tools/list responses.
var tools = []toolEntry{
	{
		def: toolDefinition{
			Name:        "query_instant",
			Description: "Execute a PromQL instant query against the loaded metrics",
			InputSchema: rawJSON(`{
				"type": "object",
				"properties": {
					"promql":    {"type": "string", "description": "PromQL expression to evaluate"},
					"timestamp": {"type": "string", "description": "Evaluation timestamp: RFC3339, Unix seconds/millis, or 'now' (default)"}
				},
				"required": ["promql"]
			}`),
			Annotations: &toolAnnotation{ReadOnlyHint: true, IdempotentHint: true},
		},
		handler: handleQueryInstant,
	},
	{
		def: toolDefinition{
			Name:        "query_range",
			Description: "Execute a PromQL range query against the loaded metrics",
			InputSchema: rawJSON(`{
				"type": "object",
				"properties": {
					"promql": {"type": "string", "description": "PromQL expression to evaluate"},
					"start":  {"type": "number", "description": "Start timestamp (Unix seconds)"},
					"end":    {"type": "number", "description": "End timestamp (Unix seconds)"},
					"step":   {"type": "number", "description": "Step in seconds (default: 15)"}
				},
				"required": ["promql", "start", "end"]
			}`),
			Annotations: &toolAnnotation{ReadOnlyHint: true, IdempotentHint: true},
		},
		handler: handleQueryRange,
	},
	{
		def: toolDefinition{
			Name:        "load_metrics",
			Description: "Load Prometheus exposition-format metrics into the in-memory store",
			InputSchema: rawJSON(`{
				"type": "object",
				"properties": {
					"data": {"type": "string", "description": "Prometheus exposition-format metrics data"}
				},
				"required": ["data"]
			}`),
			// Additive (never deletes), but not idempotent (repeat loads add samples).
			Annotations: &toolAnnotation{OpenWorldHint: true},
		},
		handler: handleLoadMetrics,
	},
	{
		def: toolDefinition{
			Name:        "list_metrics",
			Description: "List metric names in the loaded dataset",
			InputSchema: rawJSON(`{
				"type": "object",
				"properties": {
					"prefix": {"type": "string", "description": "Optional prefix to filter metric names"}
				}
			}`),
			Annotations: &toolAnnotation{ReadOnlyHint: true, IdempotentHint: true},
		},
		handler: handleListMetrics,
	},
	{
		def: toolDefinition{
			Name:        "list_labels",
			Description: "List label names (and sample values) for a metric, or all metrics",
			InputSchema: rawJSON(`{
				"type": "object",
				"properties": {
					"metric_name": {"type": "string", "description": "Optional metric name to scope label listing"},
					"prefix":      {"type": "string", "description": "Optional prefix to filter label names"}
				}
			}`),
			Annotations: &toolAnnotation{ReadOnlyHint: true, IdempotentHint: true},
		},
		handler: handleListLabels,
	},
	{
		def: toolDefinition{
			Name:        "execute_line",
			Description: "Execute any REPL command line — PromQL queries, ad-hoc commands (.scrape, .load, .seed, .save, .drop, .keep, .rename, etc.), or queries. Full REPL engine Blocked: shell !commands, shell pipes (|) outside quoted strings, and the .ai, .edit and .source commands. Note .save/.drop/.scrape can mutate state or reach external systems.",
			InputSchema: rawJSON(`{
				"type": "object",
				"properties": {
					"line": {"type": "string", "description": "Command line: PromQL expression, ad-hoc command (.scrape, .load, .seed, .save, .pinat, .at, .drop, .keep, .rename, .labels, .metrics, .timestamps, .ai, .rules, .alerts, .help, .history), or piped query (query | cmd). Shell ! commands are blocked."},
					"stream": {"type": "boolean", "description": "If true, stream output incrementally via notifications/execute_line_output. Requires executionStreaming capability."}
				},
				"required": ["line"]
			}`),
			Annotations: &toolAnnotation{DestructiveHint: true, OpenWorldHint: true},
		},
		handler: handleExecuteLine,
	},
}

// toolByName indexes tools for O(1) handler lookup, built once at init.
var toolByName = func() map[string]toolEntry {
	m := make(map[string]toolEntry, len(tools))
	for _, t := range tools {
		m[t.def.Name] = t
	}
	return m
}()

// rawJSON embeds a JSON literal as a json.RawMessage at init time.
func rawJSON(s string) json.RawMessage { return json.RawMessage(s) }

// ---------------------------------------------------------------------------
// tools/list
// ---------------------------------------------------------------------------

type toolsListResult struct {
	Tools []toolDefinition `json:"tools"`
}

func (s *Server) handleToolsList() (json.RawMessage, *rpcError) {
	defs := make([]toolDefinition, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, t.def)
	}
	data, err := json.Marshal(toolsListResult{Tools: defs})
	if err != nil {
		return nil, internalError("marshal tools", err)
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// tools/call
// ---------------------------------------------------------------------------

func (s *Server) handleToolsCall(params json.RawMessage) (json.RawMessage, *rpcError) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, errParams
	}

	entry, ok := toolByName[call.Name]
	if !ok {
		return nil, errMethod
	}

	result, err := entry.handler(s, call.Arguments)
	if err != nil {
		// Tool execution errors are reported as isError content, not JSON-RPC
		// errors — the call itself succeeded, the tool reported a failure.
		return marshalToolResultRPC(err.Error(), true)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Tool handler: query_instant
// ---------------------------------------------------------------------------

type queryInstantArgs struct {
	PromQL    string `json:"promql"`
	Timestamp string `json:"timestamp,omitempty"`
}

func handleQueryInstant(srv *Server, params json.RawMessage) (json.RawMessage, error) {
	var args queryInstantArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.PromQL == "" {
		return nil, fmt.Errorf("promql is required")
	}

	// Parse timestamp.
	ts := time.Now().UnixMilli()
	if args.Timestamp != "" && args.Timestamp != "now" {
		parsed, err := parseTimestamp(args.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("invalid timestamp: %w", err)
		}
		ts = parsed.UnixMilli()
	}

	if srv.storage == nil || len(srv.storage.Metrics) == 0 {
		return nil, fmt.Errorf("no metrics loaded; use load_metrics or start with a data file")
	}

	text, err := runInstantQuery(srv.engine, srv.storage, args.PromQL, ts)
	if err != nil {
		return nil, err
	}
	return marshalToolResult(text, false)
}

// ---------------------------------------------------------------------------
// Tool handler: query_range
// ---------------------------------------------------------------------------

type queryRangeArgs struct {
	PromQL string  `json:"promql"`
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Step   float64 `json:"step,omitempty"`
}

func handleQueryRange(srv *Server, params json.RawMessage) (json.RawMessage, error) {
	var args queryRangeArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.PromQL == "" {
		return nil, fmt.Errorf("promql is required")
	}
	if args.Step <= 0 {
		args.Step = 15
	}

	startMs := int64(args.Start * 1000)
	endMs := int64(args.End * 1000)
	stepMs := int64(args.Step * 1000)

	if srv.storage == nil || len(srv.storage.Metrics) == 0 {
		return nil, fmt.Errorf("no metrics loaded; use load_metrics or start with a data file")
	}

	text, err := runRangeQuery(srv.engine, srv.storage, args.PromQL, startMs, endMs, stepMs)
	if err != nil {
		return nil, err
	}
	return marshalToolResult(text, false)
}

// ---------------------------------------------------------------------------
// Tool handler: load_metrics
// ---------------------------------------------------------------------------

type loadMetricsArgs struct {
	Data string `json:"data"`
}

func handleLoadMetrics(srv *Server, params json.RawMessage) (json.RawMessage, error) {
	var args loadMetricsArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Data == "" {
		return nil, fmt.Errorf("data is required")
	}

	if srv.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	before := totalSamples(srv.storage)
	beforeCounts := repl.SampleCounts(srv.storage)
	if err := srv.storage.LoadFromReader(strings.NewReader(args.Data)); err != nil {
		return nil, fmt.Errorf("load failed: %w", err)
	}
	// stdout carries the MCP protocol: the unpin note goes to stderr
	repl.DropHeaderPinIfNewer(os.Stderr, srv.storage, beforeCounts)
	after := totalSamples(srv.storage)
	added := after - before

	text := fmt.Sprintf("Loaded %d samples from %d metrics", added, len(srv.storage.Metrics))
	slog.Debug("load_metrics", "added", added, "total_metrics", len(srv.storage.Metrics))
	if added != 0 {
		srv.notifyResourcesUpdated(uriMetrics, uriMetricsList, uriLabels)
	}
	return marshalToolResult(text, false)
}

// ---------------------------------------------------------------------------
// Tool handler: list_metrics
// ---------------------------------------------------------------------------

type listMetricsArgs struct {
	Prefix string `json:"prefix,omitempty"`
}

func handleListMetrics(srv *Server, params json.RawMessage) (json.RawMessage, error) {
	var args listMetricsArgs
	_ = json.Unmarshal(params, &args) // prefix is optional, ignore unmarshal errors

	if srv.storage == nil {
		return marshalToolResult("No metrics loaded.", false)
	}

	var names []string
	for name := range srv.storage.Metrics {
		if args.Prefix == "" || strings.HasPrefix(name, args.Prefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	if len(names) == 0 {
		return marshalToolResult("No metrics match.", false)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Metrics (%d total):\n", len(names))
	for _, n := range names {
		help := srv.storage.MetricsHelp[n]
		if help != "" {
			fmt.Fprintf(&b, "  %s  # %s\n", n, help)
		} else {
			fmt.Fprintf(&b, "  %s\n", n)
		}
	}
	return marshalToolResult(b.String(), false)
}

// ---------------------------------------------------------------------------
// Tool handler: list_labels
// ---------------------------------------------------------------------------

type listLabelsArgs struct {
	MetricName string `json:"metric_name,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
}

func handleListLabels(srv *Server, params json.RawMessage) (json.RawMessage, error) {
	var args listLabelsArgs
	_ = json.Unmarshal(params, &args)

	if srv.storage == nil {
		return marshalToolResult("No metrics loaded.", false)
	}

	// Collect label names and sample values.
	labelInfo := make(map[string]map[string]bool) // labelName -> set of values

	collect := func(samples []sstorage.MetricSample) {
		for _, smpl := range samples {
			for k, v := range smpl.Labels {
				if k == "__name__" {
					continue
				}
				if args.Prefix != "" && !strings.HasPrefix(k, args.Prefix) {
					continue
				}
				if labelInfo[k] == nil {
					labelInfo[k] = make(map[string]bool)
				}
				labelInfo[k][v] = true
			}
		}
	}

	if args.MetricName != "" {
		// Specific metric only.
		if samples, ok := srv.storage.Metrics[args.MetricName]; ok {
			collect(samples)
		} else {
			return marshalToolResult(fmt.Sprintf("Metric %q not found", args.MetricName), false)
		}
	} else {
		// All metrics.
		for _, samples := range srv.storage.Metrics {
			collect(samples)
		}
	}

	if len(labelInfo) == 0 {
		return marshalToolResult("No labels found.", false)
	}

	// Sort label names.
	names := make([]string, 0, len(labelInfo))
	for n := range labelInfo {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, n := range names {
		vals := labelInfo[n]
		// Show a few sample values.
		valList := make([]string, 0, len(vals))
		for v := range vals {
			valList = append(valList, v)
		}
		sort.Strings(valList)
		sampleVals := valList
		if len(sampleVals) > 5 {
			sampleVals = sampleVals[:5]
		}
		fmt.Fprintf(&b, "  %s [%d unique values] e.g.: %s\n", n, len(vals), strings.Join(sampleVals, ", "))
	}
	return marshalToolResult(b.String(), false)
}

// ---------------------------------------------------------------------------
// Tool handler: execute_line
// ---------------------------------------------------------------------------

type executeLineArgs struct {
	Line   string `json:"line"`
	Stream bool   `json:"stream,omitempty"`
}

// checkExecuteLine rejects lines that would run shell commands, spawn async
// AI work, open an editor, or source arbitrary files — MCP clients are
// remote/code-originated.
func checkExecuteLine(line string) error {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "!") {
		return fmt.Errorf("shell commands (!) are not allowed via MCP")
	}
	if repl.HasShellPipe(trimmed) {
		return fmt.Errorf("shell pipes (|) are not allowed via MCP")
	}
	// Match by prefix, like the REPL dispatcher (strings.HasPrefix(line, ".ai")),
	// so variants such as ".aifoo" can't slip past the guard.
	lower := strings.ToLower(trimmed)
	for _, cmd := range []string{".ai", ".edit", ".source"} {
		if strings.HasPrefix(lower, cmd) {
			return fmt.Errorf("%s is not allowed via MCP", cmd)
		}
	}
	return nil
}

func handleExecuteLine(srv *Server, params json.RawMessage) (json.RawMessage, error) {
	var args executeLineArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Line == "" {
		return nil, fmt.Errorf("line is required")
	}

	if err := checkExecuteLine(args.Line); err != nil {
		return nil, err
	}

	// Handle streaming mode
	if args.Stream {
		return handleExecuteLineStream(srv, args)
	}

	// Non-streaming path (original behavior)
	beforeMetrics := len(srv.storage.Metrics)
	beforeSamples := totalSamples(srv.storage)
	text := repl.CaptureQueryLine(srv.engine, srv.storage, args.Line)
	if len(srv.storage.Metrics) != beforeMetrics || totalSamples(srv.storage) != beforeSamples {
		srv.notifyResourcesUpdated(uriMetrics, uriMetricsList, uriLabels)
	}
	return marshalToolResult(text, false)
}

// handleExecuteLineStream handles streaming execution of a command line.
// For REPL commands (PromQL queries, .scrape, .load, etc.), it uses the REPL
// engine and sends the result via notifications/execute_line_output.
func handleExecuteLineStream(srv *Server, args executeLineArgs) (json.RawMessage, error) {
	if err := checkExecuteLine(args.Line); err != nil {
		return nil, err
	}
	// Always use the REPL engine for command execution. This handles PromQL
	// queries, ad-hoc commands, and piped queries correctly.
	beforeMetrics := len(srv.storage.Metrics)
	beforeSamples := totalSamples(srv.storage)

	// Execute the command via REPL and capture output
	text := repl.CaptureQueryLine(srv.engine, srv.storage, args.Line)

	// Send the output as a streaming notification
	srv.sendNotification("notifications/execute_line_output", map[string]any{
		"event": "execute_line_output",
		"chunks": []any{map[string]any{
			"text":        text,
			"is_final":    true,
			"is_complete": true,
		}},
		"is_final": true,
	})

	// Check for resource changes
	if len(srv.storage.Metrics) != beforeMetrics || totalSamples(srv.storage) != beforeSamples {
		srv.notifyResourcesUpdated(uriMetrics, uriMetricsList, uriLabels)
	}

	// Return empty result (client gets output via notifications)
	return emptyResult, nil
}

// ---------------------------------------------------------------------------
// Result formatting
// ---------------------------------------------------------------------------

// formatResult converts a PromQL query result to human-readable text.
func formatResult(res *promql.Result) string {
	if res == nil || res.Value == nil {
		return "<nil>"
	}
	switch v := res.Value.(type) {
	case promql.Vector:
		if len(v) == 0 {
			return "Vector (no series)"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Vector (%d series):\n", len(v))
		for _, s := range v {
			lblStr := formatLabels(s.Metric)
			if s.H != nil {
				fmt.Fprintf(&b, "  %s => %s %s\n", lblStr, formatFloat(s.H.Sum), s.H.String())
			} else {
				fmt.Fprintf(&b, "  %s => %s @[%s]\n", lblStr, formatFloat(s.F), formatTimestampMillis(s.T))
			}
		}
		return b.String()
	case promql.Scalar:
		return fmt.Sprintf("Scalar: %s @[%s]", formatFloat(v.V), formatTimestampMillis(v.T))
	case promql.Matrix:
		if len(v) == 0 {
			return "Matrix (no series)"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Matrix (%d series):\n", len(v))
		for _, s := range v {
			lblStr := formatLabels(s.Metric)
			fmt.Fprintf(&b, "  %s [%d points]:\n", lblStr, len(s.Floats)+len(s.Histograms))
			// Show first and last few float points.
			maxPts := 10
			if len(s.Floats) <= maxPts {
				for _, p := range s.Floats {
					fmt.Fprintf(&b, "    %s %s\n", formatTimestampMillis(p.T), formatFloat(p.F))
				}
			} else {
				// First 5, ..., last 5.
				for _, p := range s.Floats[:5] {
					fmt.Fprintf(&b, "    %s %s\n", formatTimestampMillis(p.T), formatFloat(p.F))
				}
				fmt.Fprintf(&b, "    ... (%d more)\n", len(s.Floats)-10)
				for _, p := range s.Floats[len(s.Floats)-5:] {
					fmt.Fprintf(&b, "    %s %s\n", formatTimestampMillis(p.T), formatFloat(p.F))
				}
			}
		}
		return b.String()
	case *parser.NumberLiteral:
		return fmt.Sprintf("Number: %s", formatFloat(v.Val))
	case *parser.StringLiteral:
		return fmt.Sprintf("String: %q", v.Val)
	default:
		return fmt.Sprintf("Result type: %T, value: %v", v, v)
	}
}

func formatLabels(lbls labels.Labels) string {
	if lbls.Len() == 0 {
		return "{}"
	}
	name := lbls.Get("__name__")
	if name == "" {
		return lbls.String()
	}
	// Drop __name__ for cleaner display (we show it at the start).
	rest := dropMetricName(lbls)
	if rest.Len() == 0 {
		return name
	}
	return name + rest.String()
}

// dropMetricName returns labels without the __name__ label.
func dropMetricName(lbls labels.Labels) labels.Labels {
	m := lbls.Map()
	delete(m, "__name__")
	return labels.FromMap(m)
}

func formatFloat(v float64) string {
	if math.IsNaN(v) {
		return "NaN"
	}
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	if math.IsInf(v, -1) {
		return "-Inf"
	}
	return fmt.Sprintf("%g", v)
}

func formatTimestampMillis(ts int64) string {
	t := time.UnixMilli(ts)
	return t.UTC().Format(time.RFC3339)
}

// ---------------------------------------------------------------------------
// Query execution
// ---------------------------------------------------------------------------

// runInstantQuery runs a PromQL instant query at the given eval time (Unix
// millis) and returns a text representation of the result.
func runInstantQuery(engine *promql.Engine, storage *sstorage.SimpleStorage, expr string, ts int64) (string, error) {
	if engine == nil || storage == nil {
		return "", fmt.Errorf("no metrics loaded")
	}
	if _, err := mcpParser.ParseExpr(expr); err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), promqlTimeout)
	defer cancel()

	q, err := engine.NewInstantQuery(ctx, storage, nil, expr, time.UnixMilli(ts))
	if err != nil {
		return "", fmt.Errorf("create query: %w", err)
	}
	res := q.Exec(ctx)
	if res.Err != nil {
		return "", fmt.Errorf("execute: %w", res.Err)
	}
	return formatResult(res), nil
}

// runRangeQuery runs a PromQL range query over [start, end] (Unix millis) with
// the given step (millis) and returns a text representation of the result.
func runRangeQuery(engine *promql.Engine, storage *sstorage.SimpleStorage, expr string, start, end, step int64) (string, error) {
	if engine == nil || storage == nil {
		return "", fmt.Errorf("no metrics loaded")
	}
	if _, err := mcpParser.ParseExpr(expr); err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), promqlTimeout)
	defer cancel()

	q, err := engine.NewRangeQuery(ctx, storage, nil, expr,
		time.UnixMilli(start), time.UnixMilli(end), time.Duration(step)*time.Millisecond)
	if err != nil {
		return "", fmt.Errorf("create range query: %w", err)
	}
	res := q.Exec(ctx)
	if res.Err != nil {
		return "", fmt.Errorf("execute: %w", res.Err)
	}
	return formatResult(res), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func totalSamples(s *sstorage.SimpleStorage) int {
	n := 0
	for _, samples := range s.Metrics {
		n += len(samples)
	}
	return n
}

// marshalToolResult marshals a text tool result. Returns a Go error only on
// marshal failure (which the caller converts to a JSON-RPC error).
func marshalToolResult(text string, isError bool) (json.RawMessage, error) {
	res := toolCallResult{
		Content: []toolContent{{Type: "text", Text: text}},
		IsError: isError,
	}
	data, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("marshal tool result: %w", err)
	}
	return data, nil
}

// marshalToolResultRPC is like marshalToolResult but returns an *rpcError,
// for use directly from dispatch-level handlers (tools/call).
func marshalToolResultRPC(text string, isError bool) (json.RawMessage, *rpcError) {
	data, err := marshalToolResult(text, isError)
	if err != nil {
		return nil, internalError("marshal tool result", err)
	}
	return data, nil
}

// parseTimestamp parses a timestamp string (RFC3339, Unix seconds, Unix millis, or "now").
func parseTimestamp(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "now" {
		return time.Now(), nil
	}
	// Try RFC3339
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try Unix seconds or millis
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		if i > 1e12 { // likely milliseconds
			return time.UnixMilli(i), nil
		}
		return time.Unix(i, 0), nil
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp format: %s", s)
}
