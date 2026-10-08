package repl

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
		Command:     ".rules",
		Description: "Show or set active Prometheus rule files (dir, glob, or file)",
		Usage:       ".rules [<dir|glob|file>]",
		Examples: []string{
			".rules",
			".rules ./example-rules.yaml",
			".rules ./rules/",
			".rules 'rules/*.yaml'",
		},
	},
	{
		Command:     ".alerts",
		Description: "Show alerting rules from active rule files",
		Usage:       ".alerts",
	},
	{
		Command:     ".timestamps",
		Description: "Summarize timestamps for a metric",
		Usage:       ".timestamps <metric>",
		Examples:    []string{".timestamps http_requests_total"},
	},
	{
		Command:     ".stats",
		Description: "Show current store totals (metrics and samples)",
		Usage:       ".stats",
	},
	{
		Command:     ".load",
		Description: "Load metrics from a Prometheus text-format file",
		Usage:       ".load <file.prom> [timestamp={now|remove|<timespec>}] [regex='<series regex>'] [pinat={last|first|none|<time>|<selector>}]",
		Examples: []string{
			".load metrics.prom",
			".load incident.prom                 # restores the '# promql-cli: pinat=' time saved by .save",
			".load metrics.prom pinat=last       # pin evaluation time to the newest loaded sample",
			".load metrics.prom pinat=node_load1 # pin to the newest sample of a selector",
			".load metrics.prom timestamp=now",
			".load metrics.prom timestamp=2025-09-28T12:00:00Z",
			".load metrics.prom timestamp=remove",
			".load metrics.prom regex='^up\\{.*\\}$'",
		},
	},
	{
		Command:     ".source",
		Description: "Execute PromQL expressions from a file (one per line)",
		Usage:       ".source <file>",
		Examples: []string{
			".source queries.promql",
			".source /path/to/expressions.txt",
		},
	},
	{
		Command:     ".save",
		Description: "Save current store to a Prometheus text-format file (records the pinned .pinat time, or the newest saved sample, in a '# promql-cli: pinat=' header)",
		Usage:       ".save <file.prom> [timestamp={now|remove|<timespec>}] [regex='<series regex>']",
		Examples: []string{
			".save snapshot.prom",
			".save snapshot.prom timestamp=now",
			".save snapshot.prom timestamp=remove",
			".save snapshot.prom regex='http_requests_total\\{.*code=\"5..\".*\\}'",
		},
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
		Description: "Drop all series matching a regex (by series signature name{labels})",
		Usage:       ".drop <series regex>",
		Examples: []string{
			".drop '^up\\{.*instance=\"db-.*\".*\\}$'",
			".drop 'http_requests_total\\{.*code=\"5..\".*\\}'",
		},
	},
	{
		Command:     ".keep",
		Description: "Keep only series matching a regex (drop the rest)",
		Usage:       ".keep <series regex>",
		Examples: []string{
			".keep '^up\\{.*job=\"node-exporter\".*\\}$'",
			".keep 'node_cpu_seconds_total\\{.*mode=\"idle\".*\\}'",
		},
	},
	{
		Command:     ".at",
		Description: "Evaluate a query at a specific time",
		Usage:       ".at <time> <query>",
		Examples:    []string{".at now-10m sum by (path) (rate(http_requests_total[5m]))"},
	},
	{
		Command:     ".assert",
		Description: "Check that a query returns a non-empty result: PASS, or FAIL with why (evaluated at the .pinat time)",
		Usage:       ".assert <expr>",
		Examples: []string{
			".assert up",
			".assert count({__name__=~\"node_.+\"}) < 1000",
			".assert absent(rate(errors_total[5m]) > 10)",
		},
	},
	{
		Command:     ".common_labels",
		Description: "Print labels and timestamps shared by every series once, as # common_labels / # common_timestamp headers (interactive terminal only; on by default)",
		Usage:       ".common_labels [on|off]",
		Examples: []string{
			".common_labels       # show the current setting",
			".common_labels off   # full labels on every line",
		},
	},
	{
		Command:     ".pinat",
		Description: "Pin evaluation time for all future queries (a metric selector pins to its latest sample)",
		Usage:       ".pinat [time|now|<metric selector>|remove]",
		Examples: []string{
			".pinat",
			".pinat now",
			".pinat 2025-09-16T20:40:00Z",
			".pinat node_load1",
			`.pinat up{job="node"}`,
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
	{
		Command:     ".rename",
		Description: "Rename a metric (all series with that metric name)",
		Usage:       ".rename <old_metric> <new_metric>",
		Examples: []string{
			".rename http_requests_total http_requests",
			".rename old_metric_name new_metric_name",
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
