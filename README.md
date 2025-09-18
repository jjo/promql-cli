# promql-cli

A lightweight PromQL playground and REPL. Load Prometheus text-format metrics, query them with the upstream Prometheus engine, and iterate quickly with interactive auto-completion.

## Install

- Go:
  - `go install github.com/jjo/promql-cli@latest`
- Docker:
  - Docker Hub: `docker pull xjjo/promql-cli:latest`
  - GHCR: `docker pull ghcr.io/jjo/promql-cli:latest`

## Quick start

- Load a metrics file and open the REPL:
  - `promql-cli query ./example.prom`
- Scrape metrics before starting and list metric names:
  - `promql-cli query -c ".scrape http://localhost:9100/metrics; .metrics"`
- Run a single query and print JSON:
  - `promql-cli query -q 'up' -o json ./example.prom`

## Commands

- `promql-cli load <file.prom>` — parse and load a text-format metrics file (prints a short summary)
- `promql-cli query [flags] [<file.prom>]` — start the REPL or run a one-off query
- `promql-cli version` — print version, commit, and build date

### Query flags
- `-q, --query "<expr>"` — run a single expression and exit
- `-o, --output json` — with `-q`, print JSON result
- `-c, --command "cmds"` — run semicolon-separated commands before the session or one-off query
  - Example: `-c ".scrape http://localhost:9100/metrics; .metrics"`
- `-s, --silent` — suppress startup output and `-c` command output

## Ad-hoc commands (in the REPL)

- `.help`
  - Show usage for ad-hoc commands
- `.labels <metric>`
  - Show the set of labels and example values for a metric present in the loaded dataset
  - Example: `.labels http_requests_total`
- `.metrics`
  - List metric names present in the loaded dataset
- `.timestamps <metric>`
  - Summarize timestamps found across the metric's time series (unique count, earliest, latest, span)
  - Example: `.timestamps http_requests_total`
- `.load <file.prom>`
  - Load metrics from a Prometheus text-format file into the store
- `.save <file.prom>`
  - Save current store to a Prometheus text-format file
- `.seed <metric> [steps=N] [step=1m]`
  - Backfill N historical points per series for a metric, spaced by step (enables rate()/increase())
  - Also supports positional form: `.seed <metric> <steps> [<step>]`
  - Examples: `.seed http_requests_total steps=10 step=30s` or `.seed http_requests_total 10 30s`
- `.scrape <URI> [metrics_regex] [count] [delay]`
  - Fetch metrics from an HTTP(S) endpoint and load them into the store, optionally filtering by metric name regex, repeating count times with delay between scrapes
  - Examples:
    - `.scrape http://localhost:9100/metrics`
    - `.scrape http://localhost:9100/metrics '^(up|process_.*)$'`
    - `.scrape http://localhost:9100/metrics 3 5s`
    - `.scrape http://localhost:9100/metrics 'http_.*' 5 2s`
- `.drop <metric>`
  - Remove a metric (all its series) from the in-memory store
  - Example: `.drop http_requests_total`
- `.at <time> <query>`
  - Evaluate a query at a specific time
  - Time formats: now, now-5m, now+1h, RFC3339 (2025-09-16T20:40:00Z), unix seconds/millis
  - Example: `.at now-10m sum by (path) (rate(http_requests_total[5m]))`
- `.pinat [time|now|remove]`
  - Without args, show current pinned evaluation time
  - With an argument, pin all future queries to a specific evaluation time until removed
  - Examples: `.pinat` (show), `.pinat now`, `.pinat 2025-09-16T20:40:00Z`, `.pinat remove`

## Notes
- Input files must be in Prometheus text exposition format.
- The REPL supports tab-completion and keeps history in `/tmp/.promql-cli_history`.

## Use cases

### Developing an exporter
Use promql-cli to iterate quickly on metrics emitted by your exporter during development.
- Repeatedly scrape your local exporter while you code.
- Pin evaluation time to "now" for stable rate/increase windows during quick loops.
- Explore labels with `.labels` and use Tab completion to discover series.

Example:

```
./bin/promql-cli query -c ".scrape http://localhost:9123/metrics ^awesome_metric 3 10s; .pinat now"
> .labels awesome_metric<TAB>
> rate(awesome_metric_foo_total[30s])
```

<details>
<summary>Flow</summary>

```
Dev edits code
     │
     ▼
Exporter (localhost:9123/metrics) ──▶ promql-cli .scrape (repeat count/delay)
                                      │
                                      ▼
                               In-memory store
                                      │
                                      ▼
                         REPL (completion, .labels, .pinat)
                                      │
                                      ▼
                             Queries (rate/increase)
                                      │
                                      ▼
                                 Insights/iterate ↺
```

</details>

### Grabbing exported metrics from a Kubernetes pod
Port-forward the pod or service locally, scrape a few times, explore, and save for offline analysis later.

```
# Port-forward a service or pod (adjust namespace/name)
kubectl -n <namespace> port-forward svc/<service> 9123:9123 &

./bin/promql-cli query -c ".scrape http://localhost:9123/metrics ^awesome_metric 3 10s; .pinat now"
> .labels awesome_metric<TAB>
> rate(awesome_metric_foo_total[30s])
> .save exported.prom
```

Later reload and continue exploring:

```
./bin/promql-cli query -c ".load exported.prom"
> .timestamps awesome_metric_foo_total
> .pinat <last_timestamp_from_above>
> rate(awesome_metric_foo_total[30s])
```

<details>
<summary>Flow</summary>

```
K8s Pod/Service ──(port-forward 9123)──▶ localhost:9123/metrics
                                         │
                                         ▼
                                   promql-cli .scrape (×N)
                                         │
                                         ▼
                                   In-memory store
                                         │            ┌───────────────┐
                                         ├──────────▶ │ .save snapshot │───▶ exported.prom
                                         │            └───────────────┘
                                         ▼
                                      Explore
                                         │
                                         ▼
                                   Later: .load file
                                         │
                                         ▼
                                      Explore again
```

</details>

### Other ideas
- Validate alerts and recording rules locally: run PromQL expressions against a saved snapshot to check thresholds.

<details>
<summary>Flow</summary>

```
exported.prom ──▶ promql-cli query -c ".load exported.prom" ──▶ run expressions ──▶ validate thresholds
```

</details>

- Teach/learn PromQL: use completion, `.seed` to create history for `rate()`/`increase()`, and `.labels` to discover dimensions.

<details>
<summary>Flow</summary>

```
minimal dataset ──▶ promql-cli (.seed to synthesize history) ──▶ try functions (rate/increase) ──▶ iterate
```

</details>

- CI smoke tests for exporters: in CI, run the Docker image, scrape a test exporter, and run a set of queries (via `-q`) to assert presence/shape of metrics.

<details>
<summary>Flow</summary>

```
CI runner ──▶ docker run promql-cli query -c ".scrape http://exporter:metrics" -q "required_query"
         └─▶ exit code + logs enforce expectations
```

</details>

- Compare snapshots over time: save multiple `.prom` files and load the one you need to analyze regressions or label churn.

<details>
<summary>Flow</summary>

```
exported_1.prom   exported_2.prom
       │                 │
       └──▶ promql-cli (load one at a time) ──▶ run diff-like queries (by labels/values)
```

</details>

## Example PromQL queries

- Try the bundled example dataset:
  - `./bin/promql-cli load ./example.prom`
  - `./bin/promql-cli query ./example.prom`

- Summarize your own metrics:
  - `./bin/promql-cli load ./metrics.prom`
- Open interactive REPL and run PromQL like sum(rate(http_requests_total[5m])) by (method):
  - `./bin/promql-cli query ./metrics.prom`

### Example queries (with example.prom)

- Sum requests by service:
  - `sum by (service) (http_requests_total)`
- Error rate by path (last 5m):
  - `sum by (path) (rate(http_requests_total{code=~"5.."}[5m])) / sum by (path) (rate(http_requests_total[5m]))`
- 95th percentile latency on homepage:
  - `histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{path="/"}[5m])))`
- Memory per service:
  - `process_resident_memory_bytes`
- Active sessions by region:
  - `active_sessions`

## Docker
- Run with a local file mounted:
  - `docker run --rm -it -v "$PWD":/data xjjo/promql-cli:latest query /data/metrics.prom`
- Initialize via scrape and enter REPL:
  - `docker run --rm -it xjjo/promql-cli:latest query -c ".scrape http://localhost:9100/metrics; .metrics"`
