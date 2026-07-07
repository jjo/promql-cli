// Package mcp implements a Model Context Protocol (MCP) server that exposes
// promql-cli over stdio transport (JSON-RPC 2.0, protocol 2024-11-05).
//
// Capabilities:
//
//	tools      — query_instant, query_range, list_metrics, list_labels,
//	             load_metrics, execute_line (with optional streaming)
//	resources  — promql://metrics, promql://metrics/list, promql://labels,
//	             and one promql://metrics/<name> per loaded metric
//	prompts    — parameterized PromQL templates (error-rate-by-service, etc.)
//	logging    — logging/setLevel to adjust slog verbosity at runtime
//	executionStreaming — execute_line stream=true support
//
// Lifecycle methods: initialize, ping, shutdown, notifications/initialized.
//
// Source layout:
//
//	mcp.go        — Server, transport framing, dispatch, lifecycle, logging
//	tools.go      — tool definitions, handlers, query execution, formatting
//	resources.go  — resources/list + resources/read
//	prompts.go    — prompts/list + prompts/get
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/prometheus/prometheus/promql"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// MCP protocol version implemented by this server.
const protocolVersion = "2024-11-05"

// Server identity. serverVersion is overridable at build time via
// -ldflags "-X github.com/jjo/promql-cli/pkg/mcp.serverVersion=...".
var (
	serverName    = "promql-cli"
	serverVersion = "0.1.0"
)

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 types
// ---------------------------------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC error codes (per the JSON-RPC 2.0 spec).
const (
	codeParse    = -32700
	codeInvalid  = -32600
	codeMethod   = -32601
	codeParams   = -32602
	codeInternal = -32603
)

var (
	errParse   = &rpcError{Code: codeParse, Message: "Parse error"}
	errInvalid = &rpcError{Code: codeInvalid, Message: "Invalid request"}
	errMethod  = &rpcError{Code: codeMethod, Message: "Method not found"}
	errParams  = &rpcError{Code: codeParams, Message: "Invalid params"}
)

// internalError wraps a Go error as a JSON-RPC internal error (-32603).
func internalError(context string, err error) *rpcError {
	return &rpcError{Code: codeInternal, Message: fmt.Sprintf("%s: %v", context, err)}
}

// invalidParams returns an Invalid params (-32602) error with a custom message.
func invalidParams(msg string) *rpcError {
	return &rpcError{Code: codeParams, Message: msg}
}

// ---------------------------------------------------------------------------
// Capabilities (initialize response)
// ---------------------------------------------------------------------------

// serverCapabilities advertises every feature the server actually implements
// so spec-compliant clients know to call resources/*, prompts/*, logging/*.
type serverCapabilities struct {
	Tools              *toolsCapability              `json:"tools,omitempty"`
	Resources          *resourcesCapability          `json:"resources,omitempty"`
	Prompts            *promptsCapability            `json:"prompts,omitempty"`
	Logging            *loggingCapability            `json:"logging,omitempty"`
	ExecutionStreaming *executionStreamingCapability `json:"executionStreaming,omitempty"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type resourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type promptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// loggingCapability is an empty object per the MCP spec — its presence alone
// signals that logging/setLevel is supported.
type loggingCapability struct{}

// executionStreamingCapability advertises support for streaming tool execution.
// When present, clients may call execute_line with "stream": true to receive
// incremental output via notifications/execute_line_output.
type executionStreamingCapability struct{}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// Server implements the MCP protocol over stdin/stdout.
type Server struct {
	engine  *promql.Engine
	storage *sstorage.SimpleStorage

	reader      *bufio.Reader
	writer      io.Writer
	initialized atomic.Bool
	shutdown    atomic.Bool
}

// NewServer creates an MCP server over stdin/stdout.
func NewServer(engine *promql.Engine, storage *sstorage.SimpleStorage) *Server {
	return &Server{
		engine:  engine,
		storage: storage,
		reader:  bufio.NewReader(os.Stdin),
		writer:  os.Stdout,
	}
}

// Run reads JSON-RPC messages from stdin, dispatches them, and writes
// responses to stdout. It returns when stdin is closed or ctx is done.
func (s *Server) Run(ctx context.Context) error {
	slog.Debug("MCP server starting")
	defer slog.Debug("MCP server exited")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if s.shutdown.Load() {
			return nil
		}

		msg, err := s.readMessage()
		if err != nil {
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "closed pipe") {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}

		s.dispatch(msg)
	}
}

// ---------------------------------------------------------------------------
// Transport: Content-Length framing
// ---------------------------------------------------------------------------

const contentLengthHeader = "Content-Length:"

func (s *Server) readMessage() (json.RawMessage, error) {
	var contentLength int64
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			// Empty line ends the header block.
			if contentLength > 0 {
				break
			}
			// No Content-Length yet; tolerate leading/trailing blank lines.
			continue
		}
		if v, ok := cutHeaderCI(line, contentLengthHeader); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %s", line)
			}
			contentLength = n
		}
	}
	if contentLength == 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(s.reader, body); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return json.RawMessage(body), nil
}

// cutHeaderCI does a case-insensitive prefix match on an HTTP-style header
// line and returns the value portion after the prefix.
func cutHeaderCI(line, prefix string) (string, bool) {
	if len(line) < len(prefix) {
		return "", false
	}
	if !strings.EqualFold(line[:len(prefix)], prefix) {
		return "", false
	}
	return line[len(prefix):], true
}

func (s *Server) writeMessage(data []byte) error {
	if _, err := fmt.Fprintf(s.writer, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	if _, err := s.writer.Write(data); err != nil {
		return err
	}
	// Flush so the client receives it immediately.
	if f, ok := s.writer.(*os.File); ok {
		_ = f.Sync()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

func (s *Server) dispatch(raw json.RawMessage) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.sendError(nil, errParse)
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		s.sendError(req.ID, errInvalid)
		return
	}

	isNotification := req.ID == nil || string(req.ID) == "null"
	slog.Debug("MCP dispatch", "method", req.Method, "notification", isNotification)

	// Notifications carry no id and get no response.
	if isNotification {
		s.handleNotification(req.Method, req.Params)
		return
	}

	result, rpcErr := s.handleRequest(req.Method, req.Params)
	if rpcErr != nil {
		s.sendError(req.ID, rpcErr)
		return
	}
	s.sendResult(req.ID, result)
}

func (s *Server) handleNotification(method string, _ json.RawMessage) {
	switch method {
	case "notifications/initialized":
		s.initialized.Store(true)
		slog.Debug("MCP: client initialized")
	case "exit":
		s.shutdown.Store(true)
	default:
		slog.Debug("MCP: unknown notification", "method", method)
	}
}

// emptyResult is the canonical empty JSON-RPC result object.
var emptyResult = json.RawMessage(`{}`)

func (s *Server) handleRequest(method string, params json.RawMessage) (json.RawMessage, *rpcError) {
	switch method {
	case "initialize":
		return s.handleInitialize(params)
	case "ping":
		return emptyResult, nil
	case "shutdown":
		s.shutdown.Store(true)
		return emptyResult, nil
	case "tools/list":
		return s.handleToolsList()
	case "tools/call":
		return s.handleToolsCall(params)
	case "resources/list":
		return s.handleResourcesList()
	case "resources/read":
		return s.handleResourcesRead(params)
	case "resources/subscribe":
		return s.handleResourcesSubscribe(params)
	case "resources/unsubscribe":
		return s.handleResourcesUnsubscribe(params)
	case "prompts/list":
		return s.handlePromptsList()
	case "prompts/get":
		return s.handlePromptsGet(params)
	case "logging/setLevel":
		return s.handleSetLogLevel(params)
	default:
		return nil, errMethod
	}
}

// ---------------------------------------------------------------------------
// Lifecycle: initialize
// ---------------------------------------------------------------------------

type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

func (s *Server) handleInitialize(_ json.RawMessage) (json.RawMessage, *rpcError) {
	res := initializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities: serverCapabilities{
			Tools:              &toolsCapability{},
			Resources:          &resourcesCapability{Subscribe: true, ListChanged: true},
			Prompts:            &promptsCapability{},
			Logging:            &loggingCapability{},
			ExecutionStreaming: &executionStreamingCapability{},
		},
	}
	res.ServerInfo.Name = serverName
	res.ServerInfo.Version = serverVersion
	data, err := json.Marshal(res)
	if err != nil {
		return nil, internalError("marshal initialize result", err)
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// Send helpers
// ---------------------------------------------------------------------------

func (s *Server) sendResult(id, result json.RawMessage) {
	data, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
	_ = s.writeMessage(data)
}

func (s *Server) sendError(id json.RawMessage, rpcErr *rpcError) {
	data, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Error: rpcErr})
	_ = s.writeMessage(data)
}

// sendNotification writes a JSON-RPC notification (no id, no response expected).
// Used for server-initiated messages like notifications/resources/updated.
func (s *Server) sendNotification(method string, params any) {
	data, err := json.Marshal(rpcNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		slog.Debug("MCP: failed to marshal notification", "method", method, "err", err)
		return
	}
	_ = s.writeMessage(data)
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// ---------------------------------------------------------------------------
// Subscriptions: resources/subscribe + notifications/resources/updated
// ---------------------------------------------------------------------------

// subscribedResources tracks which resource URIs the client is watching.
// When the stored dataset changes (load, scrape, drop, seed), we emit
// notifications/resources/updated for each subscribed URI.
var subscribedResources sync.Map // map[string]struct{}

func init() {
	// Always subscribe to the list-level resources so clients refresh after
	// any mutation, even if they only subscribed to a single metric.
	for _, uri := range []string{uriMetrics, uriMetricsList, uriLabels} {
		subscribedResources.Store(uri, struct{}{})
	}
}

type resourcesSubscribeArgs struct {
	URI string `json:"uri"`
}

// handleResourcesSubscribe is called by clients to register interest in a
// resource URI. We accept any URI — the capability is advertised, so
// spec-compliant clients will use this to opt into update notifications.
func (s *Server) handleResourcesSubscribe(params json.RawMessage) (json.RawMessage, *rpcError) {
	var args resourcesSubscribeArgs
	if err := json.Unmarshal(params, &args); err != nil || args.URI == "" {
		return nil, errParams
	}
	subscribedResources.Store(args.URI, struct{}{})
	slog.Debug("MCP: resource subscribed", "uri", args.URI)
	return emptyResult, nil
}

// handleResourcesUnsubscribe removes a resource URI from the watch set.
func (s *Server) handleResourcesUnsubscribe(params json.RawMessage) (json.RawMessage, *rpcError) {
	var args resourcesSubscribeArgs
	if err := json.Unmarshal(params, &args); err != nil || args.URI == "" {
		return nil, errParams
	}
	subscribedResources.Delete(args.URI)
	slog.Debug("MCP: resource unsubscribed", "uri", args.URI)
	return emptyResult, nil
}

// notifyResourcesUpdated emits a notifications/resources/updated for the
// given URIs to any client that subscribed to them. Call this after any
// storage mutation (load_metrics, execute_line with .scrape/.load/.drop/...).
//
// In addition to the explicitly-passed URIs, we also notify any subscribed
// per-label resources (promql://labels/<name>) when uriLabels is in the set,
// and any subscribed per-metric resources (promql://metrics/<name>) when
// uriMetrics/uriMetricsList are in the set. This lets clients that subscribed
// to a specific label or metric receive update notifications without the
// server needing to track exactly which label/metric changed.
func (s *Server) notifyResourcesUpdated(uris ...string) {
	if !s.initialized.Load() {
		return // client hasn't completed initialize; don't send notifications
	}
	// Build the set of URIs to check subscriptions for.
	notifySet := make(map[string]bool, len(uris))
	notifyLabels := false
	notifyMetrics := false
	for _, uri := range uris {
		notifySet[uri] = true
		if uri == uriLabels {
			notifyLabels = true
		}
		if uri == uriMetrics || uri == uriMetricsList {
			notifyMetrics = true
		}
	}
	// Scan subscribed resources: include exact matches plus any per-label or
	// per-metric subscriptions that are children of the changed list URIs.
	var changed []string
	subscribedResources.Range(func(key, _ any) bool {
		uri := key.(string)
		if notifySet[uri] {
			changed = append(changed, uri)
			return true
		}
		if notifyLabels && strings.HasPrefix(uri, uriLabelPfx) {
			changed = append(changed, uri)
			return true
		}
		if notifyMetrics && strings.HasPrefix(uri, uriMetricPfx) {
			changed = append(changed, uri)
			return true
		}
		return true
	})
	if len(changed) == 0 {
		return
	}
	s.sendNotification("notifications/resources/updated", map[string]any{
		"uris": changed,
	})
	slog.Debug("MCP: notified resources updated", "uris", changed)
}

// ---------------------------------------------------------------------------
// logging/setLevel
// ---------------------------------------------------------------------------

type setLogLevelArgs struct {
	Level string `json:"level"`
}

// handleSetLogLevel lets the MCP client adjust the global slog level at
// runtime. Levels follow slog conventions: debug, info, warn, error.
func (s *Server) handleSetLogLevel(params json.RawMessage) (json.RawMessage, *rpcError) {
	var args setLogLevelArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, errParams
	}
	level, ok := parseLogLevel(args.Level)
	if !ok {
		return nil, invalidParams("invalid level: " + args.Level)
	}
	// Logs go to stderr (stdout is reserved for the JSON-RPC protocol stream).
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
	slog.Info("MCP logging level changed", "level", level.String())
	return emptyResult, nil
}

func parseLogLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, true
	case "info", "":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return 0, false
	}
}
