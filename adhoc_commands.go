package main

// AdHocCommand represents an ad-hoc command with its description
type AdHocCommand struct {
	Command     string
	Description string
	Usage       string
	Examples    []string
}

// AdHocCommands is the centralized list of all ad-hoc commands
// This serves as the source of truth for both the help text and autocompletion
var AdHocCommands = []AdHocCommand{
	{
		Command:     ".help",
		Description: "Show usage for ad-hoc commands",
		Usage:       ".help",
	},
	{
		Command:     ".ai",
		Description: "Use AI to propose PromQL queries for your loaded metrics",
		Usage:       ".ai <intent> | .ai ask <intent> | .ai show | .ai run <N> | .ai edit <N>",
		Examples: []string{
			".ai top 5 pods by http error rate over last hour",
			".ai cpu usage by mode per instance in 30m",
		},
	},
	{
		Command:     ".labels",
		Description: "Show labels and example values for a metric",
		Usage:       ".labels <metric>",
		Examples:    []string{".labels http_requests_total"},
	},
	{
		Command:     ".metrics",
		Description: "List metric names in the loaded dataset",
		Usage:       ".metrics",
	},
	{
		Command:     ".timestamps",
		Description: "Summarize timestamps for a metric",
		Usage:       ".timestamps <metric>",
		Examples:    []string{".timestamps http_requests_total"},
	},
	{
		Command:     ".load",
		Description: "Load metrics from a Prometheus text-format file",
		Usage:       ".load <file.prom>",
		Examples:    []string{".load metrics.prom"},
	},
	{
		Command:     ".save",
		Description: "Save current store to a Prometheus text-format file",
		Usage:       ".save <file.prom>",
		Examples:    []string{".save snapshot.prom"},
	},
	{
		Command:     ".seed",
		Description: "Backfill historical points for rate/increase",
		Usage:       ".seed <metric> [steps=N] [step=1m] OR .seed <metric> <steps> [<step>]",
		Examples: []string{
			".seed http_requests_total steps=10 step=30s",
			".seed http_requests_total 10 30s",
		},
	},
	{
		Command:     ".scrape",
		Description: "Fetch metrics from HTTP(S) endpoint",
		Usage:       ".scrape <URI> [metrics_regex] [count] [delay]",
		Examples: []string{
			".scrape http://localhost:9100/metrics",
			".scrape http://localhost:9100/metrics '^(up|process_.*)$'",
			".scrape http://localhost:9100/metrics 3 5s",
			".scrape http://localhost:9100/metrics 'http_.*' 5 2s",
		},
	},
	{
		Command:     ".prom_scrape",
		Description: "Query a remote Prometheus API and import the results",
		Usage:       ".prom_scrape <PROM_API_URI> 'query' [count] [delay]",
		Examples: []string{
			".prom_scrape http://localhost:9090/api/v1 'up'",
			".prom_scrape http://localhost:9090 'rate(http_requests_total[5m])' 3 10s",
		},
	},
	{
		Command:     ".prom_scrape_range",
		Description: "Query a remote Prometheus API over a time range and import the results",
		Usage:       ".prom_scrape_range <PROM_API_URI> 'query' <start> <end> <step> [count] [delay]",
		Examples: []string{
			".prom_scrape_range http://localhost:9090 'up' now-15m now 30s",
			".prom_scrape_range http://localhost:9090 'rate(http_requests_total[5m])' 2025-09-27T00:00:00Z 2025-09-27T00:30:00Z 15s",
		},
	},
	{
		Command:     ".drop",
		Description: "Remove a metric from the in-memory store",
		Usage:       ".drop <metric>",
		Examples:    []string{".drop http_requests_total"},
	},
	{
		Command:     ".at",
		Description: "Evaluate a query at a specific time",
		Usage:       ".at <time> <query>",
		Examples:    []string{".at now-10m sum by (path) (rate(http_requests_total[5m]))"},
	},
	{
		Command:     ".pinat",
		Description: "Pin evaluation time for all future queries",
		Usage:       ".pinat [time|now|remove]",
		Examples: []string{
			".pinat",
			".pinat now",
			".pinat 2025-09-16T20:40:00Z",
			".pinat remove",
		},
	},
	{
		Command:     ".quit",
		Description: "Exit the REPL",
		Usage:       ".quit",
	},
	{
		Command:     ".history",
		Description: "Show REPL history (all or last N entries)",
		Usage:       ".history [N]",
		Examples: []string{
			".history",
			".history 20",
		},
	},
}

// GetAdHocCommandNames returns just the command names for autocompletion
func GetAdHocCommandNames() []string {
	names := make([]string, len(AdHocCommands))
	for i, cmd := range AdHocCommands {
		names[i] = cmd.Command
	}
	return names
}

// GetAdHocCommandByName returns a command by its name
func GetAdHocCommandByName(name string) *AdHocCommand {
	for _, cmd := range AdHocCommands {
		if cmd.Command == name {
			return &cmd
		}
	}
	return nil
}
