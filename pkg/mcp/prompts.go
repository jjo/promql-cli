package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ---------------------------------------------------------------------------
// Prompt types
// ---------------------------------------------------------------------------

// promptDefinition describes one prompt in the prompts/list response.
type promptDefinition struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []promptArgument `json:"arguments,omitempty"`
}

type promptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// promptGetResult is the result object for prompts/get responses.
type promptGetResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []promptMessage `json:"messages"`
}

type promptMessage struct {
	Role    string        `json:"role"`
	Content promptContent `json:"content"`
}

type promptContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ---------------------------------------------------------------------------
// Prompt registry
// ---------------------------------------------------------------------------

// promptTemplate couples a prompt's advertised definition with a render
// function that turns validated arguments into PromQL query messages.
type promptTemplate struct {
	def    promptDefinition
	render func(args map[string]string) []promptMessage
}

// argOr returns args[key] or a default when absent/empty.
func argOr(args map[string]string, key, def string) string {
	if v := args[key]; v != "" {
		return v
	}
	return def
}

// userMsg builds a single user-role text prompt message.
func userMsg(text string) promptMessage {
	return promptMessage{Role: "user", Content: promptContent{Type: "text", Text: text}}
}

// promptTemplates is the static registry of prompt templates, keyed by name.
var promptTemplates = map[string]promptTemplate{
	"error-rate-by-service": {
		def: promptDefinition{
			Name:        "error-rate-by-service",
			Description: "Calculate error rate (5xx) grouped by service over a time window",
			Arguments: []promptArgument{
				{Name: "window", Description: "Time window for rate calculation (e.g., 5m, 1h)", Required: true},
			},
		},
		render: func(args map[string]string) []promptMessage {
			w := argOr(args, "window", "5m")
			return []promptMessage{userMsg(fmt.Sprintf(
				`sum(rate(http_requests_total{code=~"5.."}[%s])) by (service) / sum(rate(http_requests_total[%s])) by (service)`,
				w, w))}
		},
	},
	"top-errors": {
		def: promptDefinition{
			Name:        "top-errors",
			Description: "Find top N endpoints by error rate",
			Arguments: []promptArgument{
				{Name: "n", Description: "Number of top results (default 10)", Required: false},
				{Name: "window", Description: "Time window", Required: true},
			},
		},
		render: func(args map[string]string) []promptMessage {
			n := argOr(args, "n", "10")
			w := argOr(args, "window", "5m")
			return []promptMessage{userMsg(fmt.Sprintf(
				`topk(%s, sum(rate(http_requests_total{code=~"5.."}[%s])) by (service, path))`,
				n, w))}
		},
	},
	"latency-percentiles": {
		def: promptDefinition{
			Name:        "latency-percentiles",
			Description: "Calculate latency percentiles (p50, p90, p99) for a histogram metric",
			Arguments: []promptArgument{
				{Name: "metric", Description: "Histogram metric base name (e.g., http_request_duration_seconds)", Required: true},
				{Name: "window", Description: "Time window", Required: true},
			},
		},
		render: func(args map[string]string) []promptMessage {
			m := argOr(args, "metric", "http_request_duration_seconds")
			w := argOr(args, "window", "5m")
			mk := func(q float64) promptMessage {
				return userMsg(fmt.Sprintf(
					"histogram_quantile(%.2f, sum(rate(%s_bucket[%s])) by (le, service))", q, m, w))
			}
			return []promptMessage{mk(0.50), mk(0.90), mk(0.99)}
		},
	},
	"saturation-analysis": {
		def: promptDefinition{
			Name:        "saturation-analysis",
			Description: "Analyze resource saturation (CPU, memory, disk, network)",
			Arguments: []promptArgument{
				{Name: "resource", Description: "Resource type: cpu, memory, disk, network", Required: true},
				{Name: "window", Description: "Time window", Required: true},
			},
		},
		render: func(args map[string]string) []promptMessage {
			w := argOr(args, "window", "5m")
			queries := map[string]string{
				"cpu":     fmt.Sprintf("avg(rate(container_cpu_usage_seconds_total[%s])) by (container)", w),
				"memory":  "container_memory_usage_bytes / container_spec_memory_limit_bytes * 100",
				"disk":    "disk_usage_bytes / disk_total_bytes * 100",
				"network": fmt.Sprintf("rate(container_network_receive_bytes_total[%s] + container_network_transmit_bytes_total[%s]) by (container)", w, w),
			}
			q, ok := queries[argOr(args, "resource", "cpu")]
			if !ok {
				q = queries["cpu"]
			}
			return []promptMessage{userMsg(q)}
		},
	},
}

// ---------------------------------------------------------------------------
// prompts/list
// ---------------------------------------------------------------------------

type promptsListResult struct {
	Prompts []promptDefinition `json:"prompts"`
}

func (s *Server) handlePromptsList() (json.RawMessage, *rpcError) {
	names := make([]string, 0, len(promptTemplates))
	for name := range promptTemplates {
		names = append(names, name)
	}
	sort.Strings(names)

	prompts := make([]promptDefinition, 0, len(names))
	for _, name := range names {
		prompts = append(prompts, promptTemplates[name].def)
	}

	data, err := json.Marshal(promptsListResult{Prompts: prompts})
	if err != nil {
		return nil, internalError("marshal prompts", err)
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// prompts/get
// ---------------------------------------------------------------------------

type promptsGetArgs struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

func (s *Server) handlePromptsGet(params json.RawMessage) (json.RawMessage, *rpcError) {
	var args promptsGetArgs
	if err := json.Unmarshal(params, &args); err != nil || args.Name == "" {
		return nil, errParams
	}

	tmpl, ok := promptTemplates[args.Name]
	if !ok {
		return nil, invalidParams("unknown prompt: " + args.Name)
	}

	// Validate required arguments.
	for _, arg := range tmpl.def.Arguments {
		if arg.Required && args.Arguments[arg.Name] == "" {
			return nil, invalidParams("missing required argument: " + arg.Name)
		}
	}

	result := promptGetResult{
		Description: tmpl.def.Description,
		Messages:    tmpl.render(args.Arguments),
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, internalError("marshal prompt", err)
	}
	return data, nil
}
