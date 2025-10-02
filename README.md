# promql-cli

[![Tests](https://github.com/jjo/promql-cli/actions/workflows/test.yml/badge.svg)](https://github.com/jjo/promql-cli/actions/workflows/test.yml)
[![Docker build and publish](https://github.com/jjo/promql-cli/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/jjo/promql-cli/actions/workflows/docker-publish.yml)
[![Docker Hub](https://img.shields.io/docker/pulls/xjjo/promql-cli?logo=docker&label=Docker%20Hub)](https://hub.docker.com/r/xjjo/promql-cli)
[![Docker Image Size](https://img.shields.io/docker/image-size/xjjo/promql-cli/latest?logo=docker)](https://hub.docker.com/r/xjjo/promql-cli/tags)

> **A lightweight PromQL playground and REPL for rapid Prometheus metric exploration**

TL;DR: prometheus-less promQL CLI that can load metrics files, scrape
live endpoints (including prometheus itself), and basic get AI help.

Load Prometheus text-format metrics, query them with the compiled-in
upstream Prometheus engine, and iterate quickly with intelligent
autocompletion and AI assistance. Perfect for developing exporters,
debugging metrics, and learning PromQL.

## ✨ Key Features

- 🚀 **Interactive REPL** with rich PromQL-aware autocompletion
- 📊 **Querying** with the upstream Prometheus engine
- 🚨 **Rules support** with alerting and recording rules
- 🤖 **AI assistance** for query suggestions (OpenAI, Claude, Grok, Ollama)
- 📊 **Live metric scraping** from HTTP endpoints with filtering
- 🕒 **Time manipulation** with pinned evaluation times
- 💾 **Data persistence** with load/save functionality
- 🎯 **Developer-friendly** with prefix-based history and multi-line editing
- 📦 **Zero-config** - works with any Prometheus text-format metrics

## 🏃‍♂️ Quick Start

### Installation

```bash
# Go
go install github.com/jjo/promql-cli@latest

# Docker
docker pull xjjo/promql-cli:latest
# or
docker pull ghcr.io/jjo/promql-cli:latest
```

### Try it in 30 seconds

```bash
# Load a metrics file and start exploring
promql-cli query ./example.prom

# Or scrape live metrics and start querying
promql-cli query -c ".scrape http://localhost:9100/metrics; .metrics"

# Run a single query and get JSON output
promql-cli query -q 'up' -o json ./example.prom
```

## 📋 Command Reference

### CLI Commands

| Command | Description |
|---------|-------------|
| `promql-cli query [file.prom]` | Start interactive REPL (optionally load metrics file) |
| `promql-cli load <file.prom>` | Parse and load metrics file (shows summary) |
| `promql-cli version` | Show version information |

### CLI Options

| Option | Description | Example |
|--------|-------------|---------|
| `-q, --query "<expr>"` | Run single query and exit | `-q 'up'` |
| `-f, --file <file>` | Execute PromQL queries from file | `-f queries.promql` |
| `-o, --output json` | Output JSON format (with `-q`) | `-q 'up' -o json` |
| `-c, --command "cmds"` | Run commands before REPL/query | `-c ".scrape http://localhost:9100/metrics"` |
| `-s, --silent` | Suppress startup output | `-s -c ".load data.prom"` |
| `--rules {dir/,fileglob.yml}` | Load alerting/recording rules file | `--rules example-rules.yml` |
| `--repl {prompt\|readline}` | Choose REPL backend (default: readline) | `--repl prompt` |
| `--ai "key=value,..."` | Configure AI settings in one flag | `--ai "provider=claude,model=opus,answers=5"` |

### 🤖 REPL Commands

Commands you can use inside the interactive session:

#### **Data Management**

| Command | Purpose | Example |
|---------|---------|---------|
| `.load <file> [timestamp={now,remove,<timespec>}] [regex='<series regex>']` | Load metrics (override ts and/or filter by series) | `.load metrics.prom timestamp=now regex='^up\{.*\}$'` |
| `.save <file> [timestamp={now,remove,<timespec>}] [regex='<series regex>']` | Save metrics (override ts and/or filter by series) | `.save snapshot.prom timestamp=remove regex='http_requests_total\{.*code="5..".*\}'` |
| `.source <file>` | Execute PromQL queries from file | `.source queries.promql` |
| `.scrape <url> [regex] [count] [delay]` | Fetch live metrics (text exposition) | `.scrape http://localhost:9100/metrics` |
| `.prom_scrape <api> 'query' [count] [delay] [auth=...]` | Import from Prometheus API (instant) | `.prom_scrape http://prom:9090 'up'` |
| `.prom_scrape_range <api> 'query' <start> <end> <step> [count] [delay] [auth={basic\|mimir}] [user=... pass=...] [org_id=... api_key=...]` | Import from Prometheus API (range) | `.prom_scrape_range http://prom:9090 'rate(http_requests_total[5m])' now-1h now 30s` |
| `.drop <regex>` | Remove metrics by regex from memory | `.drop http_requests_.*` |
| `.keep <regex>` | Keep only metrics by regex from memory | `.keep http_requests_.*` |
| `.rename <old> <new>` | Rename a metric (all series with that name) | `.rename http_requests_total http_reqs_total` |

#### **Exploration**

| Command | Purpose | Example |
|---------|---------|---------|
| `.metrics` | List all loaded metrics | `.metrics` |
| `.labels <metric>` | Show labels for a metric | `.labels http_requests_total` |
| `.timestamps <metric>` | Show timestamp info | `.timestamps http_requests_total` |

#### **Time Manipulation**

| Command | Purpose | Example |
|---------|---------|---------|
| `.at <time> <query>` | Query at specific time | `.at now-5m rate(cpu_usage[1m])` |
| `.pinat <time>` | Pin evaluation time | `.pinat now` |
| `.seed <metric> [steps] [interval]` | Generate historical data | `.seed http_requests_total 10 1m` |

#### **AI Assistance**

| Command | Purpose | Example |
|---------|---------|---------|
| `.ai ask <question>` | Get AI query suggestions | `.ai ask show me error rates by service` |
| `.ai edit <N>` | Copy AI answer#<N> to clipboard | `.ai edit 1` |
| `.ai run <N>` | Run AI answer#<N> as query | `.ai run 2` |
| `.ai show` | Show all AI answers | `.ai show` |
| `.help` | Show all commands | `.help` |

## ⚡ Advanced Features

### Smart Autocompletion

The go-prompt backend provides context-aware PromQL suggestions (enable with `--repl=prompt`):

- **🎯 Context-aware**: Suggests metrics, functions, and labels based on what you're typing
- **📚 Documentation**: Shows help text and function signatures
- **🔄 Dynamic updates**: Refreshes automatically after loading new data
- **⌨️ Multi-line support**: Backslash continuation

```promql
# Examples of smart completion:
http_req<Tab>                    # → http_requests_total
rate(http<Tab>                   # → rate(http_requests_total
http_requests_total{<Tab>        # → shows actual labels
http_requests_total{code="<Tab>  # → shows real label values
sum by (<Tab>                    # → suggests relevant grouping labels
```

### ⌨️ Key Bindings

**Navigation**

- `Ctrl-A/E` - Jump to line start/end
- `Alt-B/F` - Move backward/forward by word
- `↑/↓` - Prefix-filtered history search
- `Alt-.` - Insert/cycle last argument from history (like bash yank-last-arg)

**Editing**

- `Ctrl-K/U` - Delete to end/start of line
- `Ctrl-W`, `Ctrl-Backspace` - Delete previous word (PromQL-aware: respects `(){},.` as boundaries)
- `Alt-D` - Delete word forward
- `Alt-Backspace` - Delete word backward

**Multi-line**: `Alt-Enter` or `\` (line continuation)
**AI**: `Ctrl-Y` (paste AI suggestion)
**External Editor**: `Ctrl-X Ctrl-E` (open line in $EDITOR)

### 🤖 AI Configuration

#### Quick Setup

```bash
# Method 1: Use the composite --ai flag (recommended)
promql-cli query --ai "provider=claude,model=opus,answers=5" ./metrics.prom

# Method 2: Set environment variables
export PROMQL_CLI_AI_PROVIDER=claude
export ANTHROPIC_API_KEY=your_key_here
promql-cli query ./metrics.prom

# Method 3: Create a profile file ~/.config/promql-cli/ai.toml
```

#### Composite AI Flag

The `--ai` flag lets you configure all AI settings in one place:

```bash
# Basic provider selection
--ai "provider=claude"

# Full configuration
--ai "provider=openai,model=gpt-4,base=https://custom.api/v1,answers=3"

# Multiple values (comma or space separated)
--ai "provider=claude model=opus answers=5"
--ai "provider=grok,model=grok-beta,answers=2"
```

**Supported keys:**

- `provider` - AI provider (openai|claude|grok|ollama)
- `model` - Model name to use
- `base` - Custom API base URL
- `answers` - Number of suggestions to generate
- `profile` - Load settings from profile file

#### Provider Details

| Provider | API Key Variable | Default Model | Base URL |
|----------|------------------|---------------|----------|
| **OpenAI** | `OPENAI_API_KEY` | gpt-4o-mini | https://api.openai.com/v1 |
| **Claude** | `ANTHROPIC_API_KEY` | claude-3-5-sonnet-20240620 | https://api.anthropic.com/v1 |
| **Grok** | `XAI_API_KEY` | grok-2 | https://api.x.ai/v1 |
| **Ollama** | (none - local) | llama3.1 | http://localhost:11434 |

#### Configuration Priority

Settings are applied in this order (later overrides earlier):

1. Profile file (`~/.config/promql-cli/ai.toml`)
2. Environment variables (`PROMQL_CLI_AI` or individual vars)
3. Command line `--ai` flag

#### Profile Files

Create `~/.config/promql-cli/ai.toml` for persistent settings:

```toml
[profiles.default]
provider = "claude"
model = "claude-3-5-sonnet-20240620"
answers = 3

[profiles.work]
provider = "openai"
model = "gpt-4"
base = "https://company-proxy.internal/v1"
answers = 5

[profiles.local]
provider = "ollama"
model = "llama3.1"
host = "http://localhost:11434"
```

Use with: `--ai "profile=work"` or `export PROMQL_CLI_AI_PROFILE=work`

### 🌐 Remote Prometheus API Import (.prom_scrape / .prom_scrape_range)

Import series from a remote Prometheus-compatible API directly into the in-memory store.

- Instant import (vector/matrix/scalar via /api/v1/query):

```bash
.prom_scrape <PROM_API_URI> 'query' [count] [delay] [auth={basic|mimir}] [user=... pass=...] [org_id=... api_key=...]
```

- Range import (matrix via /api/v1/query_range):

```bash
.prom_scrape_range <PROM_API_URI> 'query' <start> <end> <step> [count] [delay] [auth={basic|mimir}] [user=... pass=...] [org_id=... api_key=...]
```

Notes:

- PROM_API_URI can be the root (http://host:9090), the API root (`/api/v1`), or full endpoint (`/api/v1/query[_range]`).
- count repeats the import N times; delay waits between repeats (e.g., 10s).
- If auth is omitted, it will be inferred from provided credentials (user/pass => basic, org_id/api_key => mimir).
- HTTP errors now display detailed error messages from the Prometheus API (e.g., "parse error: unexpected character")

Auth options:

- Basic auth: sets Authorization: Basic base64(user:pass)

```bash
.prom_scrape http://prom:9090 'up' auth=basic user=alice pass=s3cr3t
```

- Grafana Mimir/Tenant headers: sets X-Scope-OrgID and Authorization: Bearer <api_key>

```bash
.prom_scrape_range http://mimir.example 'rate(http_requests_total[5m])' now-1h now 30s auth=mimir org_id=acme api_key=$MY_API_KEY
```

After importing, use `.metrics`, `.labels <metric>`, and run PromQL normally on the imported data.

### 📄 Executing Queries from Files (.source and -f flag)

Run multiple PromQL queries from a file, displaying each expression and its result:

```bash
# From the CLI
promql-cli query -f queries.promql metrics.prom

# From within the REPL
.source queries.promql
```

**File format:**

- One PromQL expression per line
- Lines starting with `#` are treated as comments
- Empty lines are ignored

**Example file (queries.promql):**

```promql
# Check service availability
up

# Calculate error rate
rate(http_requests_total{code=~"5.."}[5m])

# Top 5 memory consumers
topk(5, process_resident_memory_bytes)
```

**Output format:**

```promql
> up
Vector (3 samples):
  [1] {instance="localhost:9090", job="prometheus"} => 1 @ ...
  ...

> rate(http_requests_total{code=~"5.."}[5m])
Vector (2 samples):
  ...
```

This feature is perfect for:

- Running query suites for testing
- Documenting and sharing query collections
- Automated metric validation in CI/CD pipelines

### 🕒 Timestamp and regex options for .save and .load

Both `.save` and `.load` accept an optional timestamp argument to control timestamps:

- Syntax: `timestamp={now|remove|<timespec>}`
- `<timespec>` supports the same formats as `.pinat`/`.at`:
  - `now`, `now-<duration>` (e.g., `now-5m`, `now+1h`)
  - RFC3339 (e.g., `2025-10-01T12:00:00Z`)
  - Unix seconds or milliseconds

Examples:

```bash
.save snapshot.prom timestamp=remove                # write without timestamps
.save snapshot.prom timestamp=now-10m               # write with a fixed timestamp
.load metrics.prom timestamp=now                    # force a fixed timestamp on newly loaded samples
.load "metrics with spaces.prom" timestamp=2025-10-01T12:00:00Z
```

Notes:

- For `.load`, the timestamp override applies only to the samples loaded by that command; existing samples are unchanged.
- For `.save`, the timestamp override affects how timestamps are written to the output file; it does not modify in-memory data.

#### Series regex filter

Both `.save` and `.load` accept an optional `regex='<series regex>'` that filters time series by their identity string:

- Matching is performed against the canonical series signature: `name{labels}`, where labels are sorted alphabetically and values are quoted/escaped (e.g., `http_requests_total{code="200",method="GET"}`)
- Quote your regex if it contains spaces, braces, or shell metacharacters
- Examples:

```bash
# Save only 5xx http series, without timestamps
.save snapshot.prom timestamp=remove regex='http_requests_total\{.*code="5..".*\}'

# Load only 'up{...}' series and stamp them to a fixed time
.load metrics.prom timestamp=2025-10-01T12:00:00Z regex='^up\{.*\}$'

# Load node_cpu_seconds_total with mode="idle" and set timestamps to now-5m
.load node.prom regex='node_cpu_seconds_total\{.*mode="idle".*\}' timestamp=now-5m
```

## 🎯 Use Cases

### 🚀 Exporter Development

**Problem**: You're developing a Prometheus exporter and need to quickly test metrics output.

**Solution**: Use promql-cli for rapid iteration during development:

```bash
# Start your exporter development loop
promql-cli query -c ".scrape http://localhost:9123/metrics ^awesome_metric 3 10s; .pinat now"
> .labels awesome_metric_total    # Explore your metric's labels
> rate(awesome_metric_total[30s]) # Test rate calculations
```

**Why it's better**: No need to set up Prometheus + Grafana just to test your metrics during development.

### 📊 Kubernetes Metric Investigation

**Problem**: You need to debug metrics from a pod in your K8s cluster.

**Solution**: Port-forward and capture metrics for offline analysis:

```bash
# Port-forward the service
kubectl -n production port-forward svc/my-app 9090:9090 &

# Capture and explore
promql-cli query -c ".scrape http://localhost:9090/metrics; .save debug-snapshot.prom"
> .labels http_requests_total{job="my-app"}  # Find problematic series
> histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))
```

**Why it's better**: Work offline, share snapshots with your team, and avoid hitting production systems repeatedly.

### 🎓 PromQL Learning & Validation

**Problem**: Learning PromQL syntax and testing alert rules.

**Solution**: Use synthetic data and AI assistance:

```bash
# Create realistic test data
promql-cli query ./sample-metrics.prom
> .seed http_requests_total steps=20 step=1m  # Generate historical data
> .ai show me error rate over time            # Get AI suggestions
> rate(http_requests_total{code=~"5.."}[5m]) / rate(http_requests_total[5m])
```

**Why it's better**: Learn with real-looking data, get AI help, and validate queries before deploying alerts.

### 🔍 System Performance Analysis

**Problem**: You want to quickly check system metrics without setting up monitoring.

**Solution**: One-liner performance analysis:

```bash
# Quick system overview
docker run -d --name=node-exporter --net="host" --pid="host" \
  -v "/:/host:ro,rslave" prom/node-exporter:latest --path.rootfs=/host

promql-cli query -c ".scrape http://localhost:9100/metrics ^node 5 2s; .pinat now" \
  -q 'topk(5, rate(node_network_transmit_bytes_total[10s]))'

# Clean up
docker rm -f node-exporter
```

**Why it's better**: No permanent monitoring setup required, perfect for quick investigations.

### ⚡ Additional Use Cases

<details>
<summary><strong>CI/CD Integration</strong></summary>

Validate exporter metrics in your CI pipeline:

```bash
# In your CI script
docker run --rm promql-cli:latest query \
  -c ".scrape http://test-exporter:8080/metrics" \
  -q "up{job='test-exporter'} == 1" \
  -o json | jq '.data.result | length > 0'
```

</details>

<details>
<summary><strong>Alert Rule Testing</strong></summary>

Test Prometheus alert rules against historical data:

```bash
promql-cli query -c ".load historical-data.prom; .pinat 2023-12-01T10:00:00Z"
> rate(errors_total[5m]) > 0.1  # Test your alert condition
```

</details>

<details>
<summary><strong>Metric Comparison</strong></summary>

Compare metrics between different time periods or environments:

```bash
# Compare two snapshots
promql-cli query snapshot-before.prom
> .save /tmp/before-analysis.prom

promql-cli query snapshot-after.prom
> .load /tmp/before-analysis.prom     # Load both for comparison
```

</details>

## 🐳 Docker Usage

```bash
# Mount local directory and explore metrics file
docker run --rm -it -v "$PWD":/data xjjo/promql-cli:latest query /data/metrics.prom

# Connect to host network and scrape local services
docker run --rm -it --net=host xjjo/promql-cli:latest query \
  -c ".scrape http://localhost:9100/metrics; .metrics"

# One-shot query with JSON output
docker run --rm -v "$PWD":/data xjjo/promql-cli:latest query \
  -q 'up' -o json /data/metrics.prom
```

## 🛠️ Building from Source

```bash
# Build
make build

# See all options
make help

# Run tests
make test
```

**Runtime options:**

- Both REPL backends are built into the binary.
- Default backend: readline (portable, minimal dependencies).
- For advanced, rich UI with autocompletion, run with `--repl=prompt`.
