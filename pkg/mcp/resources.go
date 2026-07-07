package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Resource types
// ---------------------------------------------------------------------------

// resourceDefinition describes one resource in the resources/list response.
type resourceDefinition struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// resourceReadResult is the result object for resources/read responses.
type resourceReadResult struct {
	Contents []resourceContent `json:"contents"`
}

type resourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// Resource URI scheme.
const (
	uriMetrics     = "promql://metrics"      // human-readable list with help text
	uriMetricsList = "promql://metrics/list" // bare names, one per line
	uriLabels      = "promql://labels"       // all label names + sample values
	uriMetricPfx   = "promql://metrics/"     // + <name> → JSON samples for one metric
	uriLabelPfx    = "promql://labels/"      // + <name> → JSON values for one label
)

const (
	mimeText = "text/plain"
	mimeJSON = "application/json"
)

// staticResources are the fixed resources always advertised, independent of
// what data is loaded. Per-metric resources (promql://metrics/<name>) are
// generated dynamically in handleResourcesList.
var staticResources = []resourceDefinition{
	{URI: uriMetrics, Name: "All Metrics", Description: "All loaded metric names with help text", MimeType: mimeText},
	{URI: uriMetricsList, Name: "Metric Names", Description: "Metric names, one per line", MimeType: mimeText},
	{URI: uriLabels, Name: "All Labels", Description: "All label names across metrics with sample values", MimeType: mimeText},
}

// ---------------------------------------------------------------------------
// resources/list
// ---------------------------------------------------------------------------

type resourcesListResult struct {
	Resources []resourceDefinition `json:"resources"`
}

func (s *Server) handleResourcesList() (json.RawMessage, *rpcError) {
	labelNames := s.sortedLabelNames()
	resources := make([]resourceDefinition, 0, len(staticResources)+len(s.storage.Metrics)+len(labelNames))
	resources = append(resources, staticResources...)

	// One resource per loaded metric, in stable (sorted) order.
	for _, name := range s.sortedMetricNames() {
		resources = append(resources, resourceDefinition{
			URI:         uriMetricPfx + name,
			Name:        name,
			Description: "Samples for metric " + name,
			MimeType:    mimeJSON,
		})
	}

	// One resource per label name, listing all observed values.
	for _, name := range labelNames {
		resources = append(resources, resourceDefinition{
			URI:         uriLabelPfx + name,
			Name:        "label:" + name,
			Description: "All observed values for label \"" + name + "\"",
			MimeType:    mimeJSON,
		})
	}

	data, err := json.Marshal(resourcesListResult{Resources: resources})
	if err != nil {
		return nil, internalError("marshal resources", err)
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// resources/read
// ---------------------------------------------------------------------------

type resourcesReadArgs struct {
	URI string `json:"uri"`
}

func (s *Server) handleResourcesRead(params json.RawMessage) (json.RawMessage, *rpcError) {
	var args resourcesReadArgs
	if err := json.Unmarshal(params, &args); err != nil || args.URI == "" {
		return nil, errParams
	}

	var content resourceContent
	switch args.URI {
	case uriMetrics:
		content = s.resourceAllMetrics()
	case uriMetricsList:
		content = s.resourceMetricList()
	case uriLabels:
		content = s.resourceAllLabels()
	default:
		if name, ok := strings.CutPrefix(args.URI, uriMetricPfx); ok {
			content = s.resourceMetricSamples(name)
		} else if name, ok := strings.CutPrefix(args.URI, uriLabelPfx); ok {
			content = s.resourceLabelValues(name)
		} else {
			return nil, invalidParams("unknown resource URI: " + args.URI)
		}
	}

	data, err := json.Marshal(resourceReadResult{Contents: []resourceContent{content}})
	if err != nil {
		return nil, internalError("marshal resource", err)
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// Resource content builders
// ---------------------------------------------------------------------------

func (s *Server) resourceAllMetrics() resourceContent {
	names := s.sortedMetricNames()
	var b strings.Builder
	fmt.Fprintf(&b, "Metrics (%d total):\n", len(names))
	for _, n := range names {
		if help := s.storage.MetricsHelp[n]; help != "" {
			fmt.Fprintf(&b, "  %s  # %s\n", n, help)
		} else {
			fmt.Fprintf(&b, "  %s\n", n)
		}
	}
	return resourceContent{URI: uriMetrics, MimeType: mimeText, Text: b.String()}
}

func (s *Server) resourceMetricList() resourceContent {
	return resourceContent{
		URI:      uriMetricsList,
		MimeType: mimeText,
		Text:     strings.Join(s.sortedMetricNames(), "\n"),
	}
}

func (s *Server) resourceAllLabels() resourceContent {
	// labelName -> set of observed values
	labelValues := make(map[string]map[string]bool)
	for _, samples := range s.storage.Metrics {
		for _, smpl := range samples {
			for k, v := range smpl.Labels {
				if k == "__name__" {
					continue
				}
				if labelValues[k] == nil {
					labelValues[k] = make(map[string]bool)
				}
				labelValues[k][v] = true
			}
		}
	}

	names := make([]string, 0, len(labelValues))
	for n := range labelValues {
		names = append(names, n)
	}
	sort.Strings(names)

	const maxSampleValues = 10
	var b strings.Builder
	for _, n := range names {
		vals := sortedKeys(labelValues[n])
		shown := vals
		if len(shown) > maxSampleValues {
			shown = shown[:maxSampleValues]
		}
		fmt.Fprintf(&b, "  %s [%d unique values] e.g.: %s\n", n, len(vals), strings.Join(shown, ", "))
	}
	return resourceContent{URI: uriLabels, MimeType: mimeText, Text: b.String()}
}

func (s *Server) resourceLabelValues(name string) resourceContent {
	uri := uriLabelPfx + name
	values := s.labelValues(name)
	if values == nil {
		return resourceContent{URI: uri, MimeType: mimeJSON, Text: fmt.Sprintf("Label %q not found", name)}
	}
	type labelValueJSON struct {
		Value string `json:"value"`
		Count int    `json:"series_count"`
	}
	out := make([]labelValueJSON, 0, len(values))
	for _, v := range values {
		out = append(out, labelValueJSON{Value: v, Count: s.seriesCountForLabel(name, v)})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return resourceContent{URI: uri, MimeType: mimeJSON, Text: fmt.Sprintf("marshal error: %v", err)}
	}
	return resourceContent{URI: uri, MimeType: mimeJSON, Text: string(data)}
}

func (s *Server) resourceMetricSamples(name string) resourceContent {
	uri := uriMetricPfx + name
	samples, ok := s.storage.Metrics[name]
	if !ok {
		return resourceContent{URI: uri, MimeType: mimeJSON, Text: fmt.Sprintf("Metric %q not found", name)}
	}

	type sampleJSON struct {
		Labels       map[string]string `json:"labels"`
		Value        float64           `json:"value"`
		Timestamp    int64             `json:"timestamp"`
		TimestampRFC string            `json:"timestamp_rfc"`
	}
	out := make([]sampleJSON, 0, len(samples))
	for _, smpl := range samples {
		out = append(out, sampleJSON{
			Labels:       smpl.Labels,
			Value:        smpl.Value,
			Timestamp:    smpl.Timestamp,
			TimestampRFC: time.UnixMilli(smpl.Timestamp).UTC().Format(time.RFC3339),
		})
	}
	data, err := json.Marshal(out)
	if err != nil {
		return resourceContent{URI: uri, MimeType: mimeJSON, Text: fmt.Sprintf("marshal error: %v", err)}
	}
	return resourceContent{URI: uri, MimeType: mimeJSON, Text: string(data)}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// sortedMetricNames returns the loaded metric names in sorted order.
func (s *Server) sortedMetricNames() []string {
	names := make([]string, 0, len(s.storage.Metrics))
	for n := range s.storage.Metrics {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// sortedLabelNames returns all label names (excluding __name__) in sorted order.
func (s *Server) sortedLabelNames() []string {
	return sortedKeys(s.allLabelNames())
}

// labelValues returns the sorted set of observed values for a given label name.
// Returns nil if the label name is not present in any series.
func (s *Server) labelValues(name string) []string {
	values := make(map[string]bool)
	for _, samples := range s.storage.Metrics {
		for _, smpl := range samples {
			if v, ok := smpl.Labels[name]; ok {
				values[v] = true
			}
		}
	}
	if len(values) == 0 {
		return nil
	}
	return sortedKeys(values)
}

// seriesCountForLabel returns the number of series that have label name=<value>.
func (s *Server) seriesCountForLabel(name, value string) int {
	count := 0
	for _, samples := range s.storage.Metrics {
		seen := false
		for _, smpl := range samples {
			if smpl.Labels[name] == value {
				seen = true
				break
			}
		}
		if seen {
			count++
		}
	}
	return count
}

// allLabelNames returns the set of all label names (excluding __name__).
func (s *Server) allLabelNames() map[string]bool {
	names := make(map[string]bool)
	for _, samples := range s.storage.Metrics {
		for _, smpl := range samples {
			for k := range smpl.Labels {
				if k != "__name__" {
					names[k] = true
				}
			}
		}
	}
	return names
}

// sortedKeys returns the keys of a set in sorted order.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
