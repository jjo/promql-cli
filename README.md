# promql-cli

An in-memory PromQL playground/REPL. It loads Prometheus text exposition format metrics into a simple in-memory store and executes PromQL using the upstream Prometheus engine. Includes interactive querying with readline-based history and dynamic auto-completion for metric names, labels, values, functions, and operators derived from the loaded dataset.

## Build

- Local build:
  - make build
  - Binary: ./bin/promql-cli

- Run without building:
  - go run . <command> <file.prom>

## CLI usage

Commands (from the program):
- Load metrics (parse + summarize):
  - go run . load <file.prom>
- Query (REPL with autocomplete):
  - go run . query [flags] <file.prom>
- Test auto-completion (prints suggestions without REPL):
  - go run . test-completion <file.prom>

Flags (query mode):
- -q, --query "<expr>"  Execute a single PromQL expression and exit (no REPL)
- -o, --output json      With -q, output results as JSON (vector/scalar/matrix)

Common flags:
- -s, --silent           Suppress startup output (banners, summaries)

Ad-hoc commands (REPL only):
- .help
  - Show usage for ad-hoc commands
- .labels <metric>
  - Show the set of labels and example values for a metric present in the loaded dataset
  - Example: `.labels http_requests_total`
- .metrics
  - List metric names present in the loaded dataset
- .seed <metric> [steps=N] [step=1m]
  - Backfill N historical points per series for a metric, spaced by step (enables rate()/increase())
  - Also supports positional form: `.seed <metric> <steps> [<step>]`
  - Examples: `.seed http_requests_total steps=10 step=30s` or `.seed http_requests_total 10 30s`
- .at <time> <query>
  - Evaluate a query at a specific time
  - Time formats: now, now-5m, now+1h, RFC3339 (2025-09-16T20:40:00Z), unix seconds/millis
  - Example: `.at now-10m sum by (path) (rate(http_requests_total[5m]))`

With the built binary:
- ./bin/promql-cli load <file.prom>
- ./bin/promql-cli query [flags] <file.prom>
- ./bin/promql-cli test-completion <file.prom>

Examples (non-interactive and JSON):
- One-off query and exit:
  - ./bin/promql-cli query -q 'sum(rate(http_requests_total[5m]))' metrics.prom
- JSON output (Prometheus-like shape):
  - ./bin/promql-cli query -q 'up' -o json metrics.prom
- Suppress startup output:
  - ./bin/promql-cli query -s metrics.prom

Notes:
- <file.prom> must be in Prometheus text exposition format.
- The REPL supports tab-completion and keeps history in /tmp/.promql-cli_history.

## Examples

- Try the bundled example dataset:
  - ./bin/promql-cli load ./example.prom
  - ./bin/promql-cli query ./example.prom

- Summarize your own metrics:
  - ./bin/promql-cli load ./metrics.prom
- Open interactive REPL and run PromQL like sum(rate(http_requests_total[5m])) by (method):
  - ./bin/promql-cli query ./metrics.prom

### Example queries (with example.prom)
- Sum requests by service:
  - sum by (service) (http_requests_total)
- Error rate by path (last 5m):
  - sum by (path) (rate(http_requests_total{code=~"5.."}[5m])) / sum by (path) (rate(http_requests_total[5m]))
- 95th percentile latency on homepage:
  - histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{path="/"}[5m])))
- Memory per service:
  - process_resident_memory_bytes
- Active sessions by region:
  - active_sessions

## Docker

Image name (default): xjjo/promql-cli

- Build:
  - make docker-build TAG=latest
- Run (mount metrics):
  - docker run --rm -it -v "$PWD":/data xjjo/promql-cli:latest query /data/metrics.prom
- Push (to Docker Hub):
  - make docker-push TAG=latest

### GitHub Actions (CI) Docker build & push
This repository includes a GitHub Actions workflow that builds and pushes the Docker image to Docker Hub:
- Triggers: on push to main, and on tags (v*).
- Tags pushed:
  - On main: latest and sha-<shortsha>
  - On tags: the tag name (e.g., v1.2.3) and latest

Setup required in GitHub repo settings (Secrets and variables > Actions):
- DOCKERHUB_USERNAME: your Docker Hub username (Secret)
- DOCKERHUB_TOKEN: a Docker Hub Access Token (Secret)
- IMAGE_NAME: optional Docker Hub image override (Variable) — default: xjjo/promql-cli
- GHCR_IMAGE_NAME: optional GHCR image override (Variable) — default: ghcr.io/<org-or-user>/promql-cli

Notes:
- The workflow also logs in to ghcr.io using the built-in GITHUB_TOKEN to push ghcr.io images alongside Docker Hub.
- On main/master, both registries receive latest and sha- tags. On tags, both receive the tag name and latest.

## Make targets

- make build: Build ./bin/promql-cli
- make run ARGS="query ./metrics.prom": Build then run with ARGS passed to the binary
- make test: Run go test ./...
- make fmt: go fmt ./...
- make vet: go vet ./...
- make tidy: go mod tidy
- make clean: Remove ./bin
- make docker-build [TAG=latest]: Build Docker image xjjo/promql-cli:TAG
- make docker-run ARGS="query /data/metrics.prom" [TAG=latest]: Run the image with ARGS
- make docker-push [TAG=latest]: Push xjjo/promql-cli:TAG

