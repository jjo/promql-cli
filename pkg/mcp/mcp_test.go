package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// mcpTestHarness creates a Server with piped I/O for testing.
type mcpTestHarness struct {
	t          *testing.T
	srv        *Server
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	recvReader *bufio.Reader
	cancel     context.CancelFunc
	errCh      chan error
}

func newMCPTestHarness(t *testing.T, engine *promql.Engine, storage *sstorage.SimpleStorage) *mcpTestHarness {
	t.Helper()
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	srv := &Server{
		engine:  engine,
		storage: storage,
		reader:  bufio.NewReader(stdinR),
		writer:  stdoutW,
	}
	promqlTimeout = 5 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	h := &mcpTestHarness{
		t:          t,
		srv:        srv,
		stdin:      stdinW,
		stdout:     stdoutR,
		recvReader: bufio.NewReader(stdoutR),
		cancel:     cancel,
		errCh:      errCh,
	}

	// Start server in background.
	go func() {
		errCh <- srv.Run(ctx)
	}()

	return h
}

func (h *mcpTestHarness) closeStdin() {
	h.t.Helper()
	_ = h.stdin.Close()
}

func (h *mcpTestHarness) waitDone() {
	h.t.Helper()
	// Close stdin first to unblock readMessage(), then cancel context as backup.
	h.closeStdin()
	h.cancel()
	select {
	case <-h.errCh:
	case <-time.After(3 * time.Second):
		h.t.Log("server exit timeout")
	}
	_ = h.stdout.Close()
}

func (h *mcpTestHarness) send(msg map[string]any) {
	h.t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		h.t.Fatalf("marshal request: %v", err)
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := io.WriteString(h.stdin, header); err != nil {
		h.t.Fatalf("write header: %v", err)
	}
	if _, err := h.stdin.Write(data); err != nil {
		h.t.Fatalf("write body: %v", err)
	}
}

func (h *mcpTestHarness) recv() map[string]any {
	h.t.Helper()
	rd := h.recvReader
	// Read Content-Length header.
	var contentLength int64
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			h.t.Fatalf("read header: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if contentLength > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "Content-Length:") || strings.HasPrefix(line, "content-length:") {
			n, err := fmt.Sscanf(line, "Content-Length: %d", &contentLength)
			if err != nil || n != 1 {
				h.t.Fatalf("parse Content-Length from %q: %v", line, err)
			}
		}
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(rd, body); err != nil {
		h.t.Fatalf("read body: %v (len=%d)", err, contentLength)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		h.t.Fatalf("unmarshal response: %v (body=%s)", err, string(body))
	}
	return resp
}

func newTestEngine() *promql.Engine {
	return promql.NewEngine(promql.EngineOpts{
		Logger:                   nil,
		Reg:                      nil,
		MaxSamples:               50_000_000,
		Timeout:                  10 * time.Second,
		LookbackDelta:            5 * time.Minute,
		EnableAtModifier:         true,
		EnableNegativeOffset:     true,
		NoStepSubqueryIntervalFn: func(_ int64) int64 { return 60 * 1000 },
	})
}

func newTestStorage(t *testing.T) *sstorage.SimpleStorage {
	t.Helper()
	s := sstorage.NewSimpleStorage()
	if err := s.LoadFromReader(strings.NewReader(sstorage.SampleMetrics)); err != nil {
		t.Fatalf("LoadFromReader: %v", err)
	}
	return s
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestInitialize(t *testing.T) {
	engine := newTestEngine()
	store := sstorage.NewSimpleStorage()
	h := newMCPTestHarness(t, engine, store)

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
		},
	})
	resp := h.recv()

	if resp["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %v", resp["jsonrpc"])
	}
	if id := resp["id"]; id != float64(1) {
		t.Errorf("expected id 1, got %v", id)
	}
	if e := resp["error"]; e != nil {
		t.Fatalf("unexpected error: %v", e)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %T", resp["result"])
	}
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("expected protocolVersion 2024-11-05, got %v", result["protocolVersion"])
	}
	info, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("expected serverInfo object")
	}
	if info["name"] != "promql-cli" {
		t.Errorf("expected server name promql-cli, got %v", info["name"])
	}
	if v, _ := info["version"].(string); v == "" {
		t.Errorf("expected non-empty server version, got %v", info["version"])
	}
	// All four capabilities must be advertised so spec-compliant clients
	// know to call resources/*, prompts/*, and logging/*.
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected capabilities object, got %T", result["capabilities"])
	}
	for _, want := range []string{"tools", "resources", "prompts", "logging", "executionStreaming"} {
		if _, ok := caps[want]; !ok {
			t.Errorf("initialize did not advertise %q capability; got %v", want, caps)
		}
	}
	// resources capability must announce subscribe=true so clients call
	// resources/subscribe to opt into update notifications.
	res, ok := caps["resources"].(map[string]any)
	if !ok {
		t.Fatalf("expected resources capability object, got %T", caps["resources"])
	}
	if res["subscribe"] != true {
		t.Errorf("resources.subscribe should be true, got %v", res["subscribe"])
	}
	h.waitDone()
}

func TestResourcesSubscribe(t *testing.T) {
	engine := newTestEngine()
	store := sstorage.NewSimpleStorage()
	h := newMCPTestHarness(t, engine, store)

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05"},
	})
	h.recv()
	h.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	// Subscribe.
	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "resources/subscribe",
		"params": map[string]any{"uri": "promql://metrics/list"},
	})
	resp := h.recv()
	if e := resp["error"]; e != nil {
		t.Fatalf("subscribe returned error: %v", e)
	}
	if _, ok := subscribedResources.Load("promql://metrics/list"); !ok {
		t.Error("subscribe did not register the URI")
	}

	// Unsubscribe.
	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "resources/unsubscribe",
		"params": map[string]any{"uri": "promql://metrics/list"},
	})
	resp = h.recv()
	if e := resp["error"]; e != nil {
		t.Fatalf("unsubscribe returned error: %v", e)
	}
	if _, ok := subscribedResources.Load("promql://metrics/list"); ok {
		t.Error("unsubscribe did not remove the URI")
	}
	h.waitDone()
}

func TestToolsList(t *testing.T) {
	engine := newTestEngine()
	store := sstorage.NewSimpleStorage()
	h := newMCPTestHarness(t, engine, store)

	// Initialize.
	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05"},
	})
	h.recv()

	// Notify initialized.
	h.send(map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	})

	// List tools.
	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	resp := h.recv()

	if e := resp["error"]; e != nil {
		t.Fatalf("unexpected error: %v", e)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got %T", resp["result"])
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %T", result["tools"])
	}

	expected := []string{"query_instant", "query_range", "load_metrics", "list_metrics", "list_labels", "execute_line"}
	found := make(map[string]bool)
	for _, tdef := range tools {
		tool := tdef.(map[string]any)
		name, _ := tool["name"].(string)
		found[name] = true
	}
	for _, name := range expected {
		if !found[name] {
			t.Errorf("missing tool: %s", name)
		}
	}

	// Verify every tool has inputSchema.
	for _, tdef := range tools {
		tool := tdef.(map[string]any)
		if _, ok := tool["inputSchema"]; !ok {
			t.Errorf("tool %q missing inputSchema", tool["name"])
		}
	}
	h.waitDone()
}

func TestListMetricsWithData(t *testing.T) {
	store := newTestStorage(t)
	engine := newTestEngine()
	h := newMCPTestHarness(t, engine, store)

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05"},
	})
	h.recv()

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "list_metrics",
			"arguments": map[string]any{},
		},
	})
	resp := h.recv()
	assertNoError(t, resp)

	text := extractText(t, resp)
	if !strings.Contains(text, "http_requests_total") {
		t.Errorf("expected http_requests_total in output, got: %s", text)
	}
	if !strings.Contains(text, "temperature") {
		t.Errorf("expected temperature in output, got: %s", text)
	}
	h.waitDone()
}

func TestResourcesPerLabel(t *testing.T) {
	store := newTestStorage(t)
	engine := newTestEngine()
	h := newMCPTestHarness(t, engine, store)

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05"},
	})
	h.recv()

	// resources/list should include per-label URIs (promql://labels/<name>).
	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "resources/list",
	})
	resp := h.recv()
	assertNoError(t, resp)

	resultRaw, err := json.Marshal(resp["result"])
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var listResult struct {
		Resources []struct {
			URI  string `json:"uri"`
			Name string `json:"name"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(resultRaw, &listResult); err != nil {
		t.Fatalf("unmarshal resources/list result: %v", err)
	}

	// With sample data we expect at least "code" and "method" label resources.
	var codeURI string
	labelCount := 0
	for _, res := range listResult.Resources {
		if strings.HasPrefix(res.URI, "promql://labels/") {
			labelCount++
			if res.URI == "promql://labels/code" {
				codeURI = res.URI
			}
		}
	}
	if labelCount == 0 {
		t.Error("expected per-label resources in resources/list, got none")
	}
	if codeURI == "" {
		t.Error("expected promql://labels/code in resources/list")
	}

	// resources/read promql://labels/code should return JSON with value + series_count.
	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "resources/read",
		"params": map[string]any{"uri": "promql://labels/code"},
	})
	resp = h.recv()
	assertNoError(t, resp)

	var readResult struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	readRaw, _ := json.Marshal(resp["result"])
	if err := json.Unmarshal(readRaw, &readResult); err != nil {
		t.Fatalf("unmarshal resources/read result: %v", err)
	}
	if len(readResult.Contents) == 0 {
		t.Fatal("expected contents in resources/read response")
	}

	var labelVals []struct {
		Value       string `json:"value"`
		SeriesCount int    `json:"series_count"`
	}
	if err := json.Unmarshal([]byte(readResult.Contents[0].Text), &labelVals); err != nil {
		t.Fatalf("unmarshal label values JSON: %v\nraw: %s", err, readResult.Contents[0].Text)
	}
	if len(labelVals) == 0 {
		t.Error("expected label values for 'job'")
	}
	for _, lv := range labelVals {
		if lv.SeriesCount <= 0 {
			t.Errorf("expected series_count > 0 for value %q, got %d", lv.Value, lv.SeriesCount)
		}
	}

	// Non-existent label should not crash — returns a descriptive text error.
	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "resources/read",
		"params": map[string]any{"uri": "promql://labels/nonexistent_label"},
	})
	resp = h.recv()
	assertNoError(t, resp)
	readRaw2, _ := json.Marshal(resp["result"])
	if err := json.Unmarshal(readRaw2, &readResult); err != nil {
		t.Fatalf("unmarshal non-existent label read result: %v", err)
	}
	if !strings.Contains(strings.ToLower(readResult.Contents[0].Text), "not found") {
		t.Errorf("expected 'not found' for non-existent label, got: %s", readResult.Contents[0].Text)
	}

	h.waitDone()
}

func TestQueryInstant(t *testing.T) {
	store := newTestStorage(t)
	engine := newTestEngine()
	h := newMCPTestHarness(t, engine, store)

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05"},
	})
	h.recv()

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name": "query_instant",
			"arguments": map[string]any{
				"promql": "http_requests_total{code=\"200\"}",
			},
		},
	})
	resp := h.recv()
	assertNoError(t, resp)
	assertNotError(t, resp)

	text := extractText(t, resp)
	if !strings.Contains(text, "http_requests_total") {
		t.Errorf("expected http_requests_total in result, got: %s", text)
	}
	if !strings.Contains(text, "1027") {
		t.Errorf("expected value 1027 in result, got: %s", text)
	}
	h.waitDone()
}

func TestQueryInstantNoData(t *testing.T) {
	store := sstorage.NewSimpleStorage()
	engine := newTestEngine()
	h := newMCPTestHarness(t, engine, store)

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05"},
	})
	h.recv()

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "query_instant",
			"arguments": map[string]any{"promql": "up"},
		},
	})
	resp := h.recv()
	assertNoError(t, resp)
	assertIsError(t, resp)
	h.waitDone()
}

func TestLoadMetrics(t *testing.T) {
	store := sstorage.NewSimpleStorage()
	engine := newTestEngine()
	h := newMCPTestHarness(t, engine, store)

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05"},
	})
	h.recv()

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name": "load_metrics",
			"arguments": map[string]any{
				"data": sstorage.SampleMetrics,
			},
		},
	})
	resp := h.recv()
	assertNoError(t, resp)
	assertNotError(t, resp)

	text := extractText(t, resp)
	if !strings.Contains(text, "Loaded") {
		t.Errorf("expected 'Loaded' in response, got: %s", text)
	}

	// Now query the loaded data.
	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{
			"name":      "query_instant",
			"arguments": map[string]any{"promql": "temperature"},
		},
	})
	resp = h.recv()
	assertNoError(t, resp)
	assertNotError(t, resp)

	text = extractText(t, resp)
	if !strings.Contains(text, "27.3") {
		t.Errorf("expected value 27.3 in result, got: %s", text)
	}
	h.waitDone()
}

func TestListLabels(t *testing.T) {
	store := newTestStorage(t)
	engine := newTestEngine()
	h := newMCPTestHarness(t, engine, store)

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05"},
	})
	h.recv()

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name": "list_labels",
			"arguments": map[string]any{
				"metric_name": "http_requests_total",
			},
		},
	})
	resp := h.recv()
	assertNoError(t, resp)
	assertNotError(t, resp)

	text := extractText(t, resp)
	if !strings.Contains(text, "method") {
		t.Errorf("expected 'method' label, got: %s", text)
	}
	if !strings.Contains(text, "code") {
		t.Errorf("expected 'code' label, got: %s", text)
	}
	h.waitDone()
}

func TestShutdown(t *testing.T) {
	store := sstorage.NewSimpleStorage()
	engine := newTestEngine()
	h := newMCPTestHarness(t, engine, store)

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05"},
	})
	h.recv()

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "shutdown",
	})
	h.recv()
	// On shutdown, the server should exit the loop. Closing stdin lets us wait cleanly.
	h.waitDone()
}

func TestUnknownMethod(t *testing.T) {
	store := sstorage.NewSimpleStorage()
	engine := newTestEngine()
	h := newMCPTestHarness(t, engine, store)

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "foobar",
	})
	resp := h.recv()
	if resp["error"] == nil {
		t.Fatal("expected error for unknown method")
	}
	h.waitDone()
}

func TestNoArgumentTools(t *testing.T) {
	store := newTestStorage(t)
	engine := newTestEngine()
	h := newMCPTestHarness(t, engine, store)

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05"},
	})
	h.recv()

	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name": "list_metrics",
		},
	})
	resp := h.recv()
	assertNoError(t, resp)
	h.waitDone()
}

func TestToolRegistryConsistency(t *testing.T) {
	if len(toolByName) != len(tools) {
		t.Fatalf("toolByName has %d entries, tools has %d — duplicate tool name?", len(toolByName), len(tools))
	}
	for _, entry := range tools {
		if entry.handler == nil {
			t.Errorf("tool %q has nil handler", entry.def.Name)
		}
		if _, ok := toolByName[entry.def.Name]; !ok {
			t.Errorf("tool %q missing from toolByName index", entry.def.Name)
		}
	}
}

func TestExecuteLineStream(t *testing.T) {
	store := newTestStorage(t)
	engine := newTestEngine()
	h := newMCPTestHarness(t, engine, store)

	// Initialize
	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05"},
	})
	h.recv()

	// Send notifications/initialized so the server can emit notifications.
	h.send(map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	})

	// Verify tools/list includes stream parameter on execute_line
	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	resp := h.recv()
	assertNoError(t, resp)

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %T", resp["result"])
	}
	toolsList, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %T", result["tools"])
	}

	var executeLineDef map[string]any
	for _, tool := range toolsList {
		toolMap := tool.(map[string]any)
		if toolMap["name"] == "execute_line" {
			executeLineDef = toolMap
			break
		}
	}
	if executeLineDef == nil {
		t.Fatal("execute_line tool not found in tools/list")
	}
	schema, ok := executeLineDef["inputSchema"].(map[string]any)
	if !ok {
		t.Fatal("expected inputSchema on execute_line")
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in inputSchema")
	}
	if _, ok := props["stream"]; !ok {
		t.Error("expected stream property in execute_line inputSchema")
	}

	// Call execute_line with stream=true for a simple REPL command
	h.send(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{
			"name": "execute_line",
			"arguments": map[string]any{
				"line":   "up",
				"stream": true,
			},
		},
	})

	// The streaming handler sends a notification BEFORE the response.
	// Read the notification first.
	notif := h.recv()
	notifMethod, ok := notif["method"].(string)
	if !ok {
		t.Fatalf("expected notification method, got %T", notif["method"])
	}
	if notifMethod != "notifications/execute_line_output" {
		t.Errorf("expected notifications/execute_line_output, got %s", notifMethod)
	}

	// Then read the response from tools/call.
	resp = h.recv()
	assertNoError(t, resp)

	// The response should be empty JSON (no content).
	resultRaw, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %T", resp["result"])
	}
	// The streaming handler returns `{}` — empty JSON.
	if _, ok := resultRaw["content"]; ok {
		t.Error("expected no content in response for streaming mode")
	}

	h.waitDone()
}

// ---------------------------------------------------------------------------
// Assertion helpers
// ---------------------------------------------------------------------------

func assertNoError(t *testing.T, resp map[string]any) {
	t.Helper()
	if e := resp["error"]; e != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", e)
	}
}

func assertNotError(t *testing.T, resp map[string]any) {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %T", resp["result"])
	}
	if isErr, _ := result["isError"].(bool); isErr {
		text, _ := result["content"].([]any)[0].(map[string]any)["text"].(string)
		t.Fatalf("tool returned isError: %s", text)
	}
}

func assertIsError(t *testing.T, resp map[string]any) {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %T", resp["result"])
	}
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Error("expected isError=true")
	}
}

func extractText(t *testing.T, resp map[string]any) string {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object")
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected content array")
	}
	text, ok := content[0].(map[string]any)["text"].(string)
	if !ok {
		t.Fatalf("expected text in content")
	}
	return text
}

// ---------------------------------------------------------------------------
// Benchmark
// ---------------------------------------------------------------------------

func BenchmarkDispatch(b *testing.B) {
	store := sstorage.NewSimpleStorage()
	_ = store.LoadFromReader(strings.NewReader(sstorage.SampleMetrics))
	engine := newTestEngine()
	srv := NewServer(engine, store)

	req := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		srv.dispatch(req)
	}
}

// ---------------------------------------------------------------------------
// Transport framing
// ---------------------------------------------------------------------------

const initializeLine = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`

func TestNewlineDelimitedInitialize(t *testing.T) {
	h := newMCPTestHarness(t, newTestEngine(), newTestStorage(t))
	defer h.waitDone()

	if _, err := io.WriteString(h.stdin, initializeLine+"\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	line, err := h.recvReader.ReadString('\n')
	if err != nil {
		t.Fatalf("read response line: %v", err)
	}
	if strings.HasPrefix(line, "Content-Length") {
		t.Fatalf("expected newline-delimited response, got %q", line)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	assertNoError(t, resp)
	if resp["result"] == nil {
		t.Fatalf("missing result: %v", resp)
	}
}

func TestContentLengthInitializeKeepsContentLengthResponse(t *testing.T) {
	h := newMCPTestHarness(t, newTestEngine(), newTestStorage(t))
	defer h.waitDone()

	msg := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(initializeLine), initializeLine)
	if _, err := io.WriteString(h.stdin, msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	first, err := h.recvReader.Peek(len("Content-Length:"))
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if string(first) != "Content-Length:" {
		t.Fatalf("expected Content-Length framed response, got %q", first)
	}
	resp := h.recv()
	assertNoError(t, resp)
}

func TestReadMessageRejectsBadContentLength(t *testing.T) {
	for _, hdr := range []string{
		"Content-Length: -5\r\n\r\n",
		"Content-Length: 0\r\n\r\n",
		"Content-Length: 99999999999\r\n\r\n",
		"Content-Length: abc\r\n\r\n",
	} {
		s := NewServerWithIO(newTestEngine(), nil, strings.NewReader(hdr), io.Discard)
		if _, err := s.readMessage(); err == nil {
			t.Errorf("expected error for %q", hdr)
		}
	}
}

func TestReadMessageRejectsOversizedLine(t *testing.T) {
	line := strings.Repeat("x", maxMessageSize+1) // never a newline
	s := NewServerWithIO(newTestEngine(), nil, strings.NewReader(line), io.Discard)
	if _, err := s.readMessage(); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected an oversize error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// execute_line guard
// ---------------------------------------------------------------------------

func TestExecuteLineGuard(t *testing.T) {
	for _, stream := range []bool{false, true} {
		h := newMCPTestHarness(t, newTestEngine(), newTestStorage(t))
		id := 1
		call := func(line string) map[string]any {
			id++
			h.send(map[string]any{
				"jsonrpc": "2.0", "id": id, "method": "tools/call",
				"params": map[string]any{
					"name":      "execute_line",
					"arguments": map[string]any{"line": line, "stream": stream},
				},
			})
			for {
				resp := h.recv()
				// Skip streaming notifications (no id).
				if resp["id"] != nil {
					return resp
				}
			}
		}

		for _, tc := range []struct{ line, want string }{
			{"vector(1) | id -un", "pipes"},
			{".ai hi", ".ai"},
			{".aifoo", ".ai"},
			{".source x", ".source"},
			{".edit", ".edit"},
			{"!id", "shell commands"},
		} {
			resp := call(tc.line)
			assertIsError(t, resp)
			if text := extractText(t, resp); !strings.Contains(text, tc.want) {
				t.Errorf("stream=%v line=%q: error %q does not mention %q", stream, tc.line, text, tc.want)
			}
		}

		// A '|' inside a double-quoted string is not a shell pipe.
		resp := call(`up{job=~"a|b"}`)
		assertNotError(t, resp)

		h.waitDone()
	}
}

// ---------------------------------------------------------------------------
// Stdout hygiene
// ---------------------------------------------------------------------------

// TestProtocolWriterIgnoresStdoutSwap verifies protocol frames go to the
// injected writer even while os.Stdout is swapped (as captureOutput does).
func TestProtocolWriterIgnoresStdoutSwap(t *testing.T) {
	var buf strings.Builder
	s := NewServerWithIO(newTestEngine(), newTestStorage(t), strings.NewReader(""), &buf)

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	werr := s.writeMessage([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	os.Stdout = orig
	_ = w.Close()
	_ = r.Close()
	if werr != nil {
		t.Fatalf("writeMessage: %v", werr)
	}
	if got := buf.String(); got != `{"jsonrpc":"2.0","id":1,"result":{}}`+"\n" {
		t.Fatalf("unexpected protocol output: %q", got)
	}
}
