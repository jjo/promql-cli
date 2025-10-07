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

## 🎬 Quick Examples

```bash
# Scrape live metrics and explore
promql-cli query -c ".scrape http://localhost:9100/metrics; .metrics"

# Test an exporter with repeated scraping (3 times, 10s apart)
promql-cli query -c ".scrape http://localhost:9123/metrics ^my_metric 3 10s"

# Analyze a metrics file and get AI query suggestions
promql-cli query ./metrics.prom
> .ai ask show me error rates by service

# Run queries from a file and save results
promql-cli query -f queries.promql metrics.prom > results.txt

# Quick one-shot query with JSON output
promql-cli query -q 'rate(http_requests_total[5m])' -o json metrics.prom | jq
```

## 🏃‍♂️ Quick Start

### Try Without Installing (Docker)

No installation needed! Try it immediately with Docker:

```bash
# Try it instantly - scrape node_exporter metrics
docker run --rm -it --net=host xjjo/promql-cli:latest query \
  -c ".scrape http://localhost:9100/metrics; .metrics"

# Or with your own metrics file
docker run --rm -it -v "$PWD":/data xjjo/promql-cli:latest query /data/metrics.prom

# One-shot query with JSON output
docker run --rm -v "$PWD":/data xjjo/promql-cli:latest query \
  -q 'rate(http_requests_total[5m])' -o json /data/metrics.prom | jq
```

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
promql-cli query ./examples/example.prom

# Or scrape live metrics and start querying
promql-cli query -c ".scrape http://localhost:9100/metrics; .metrics"

# Run a single query and get JSON output
promql-cli query -q 'up' -o json ./examples/example.prom
```

## 🎯 Why Use promql-cli?

### 🚀 Exporter Development

**Problem**: You're developing a Prometheus exporter and need to quickly test metrics output.

**Solution**: Use promql-cli for rapid iteration during development:

```bash
# Start your exporter development loop
promql-cli query -c ".scrape http://localhost:9123/metrics ^awesome_metric 3 10s; .pinat now"
> .labels awesome_metric_total    # Explore your metric's labels
> rate(awesome_metric_total[30s]) # Test rate calculations
```

**Why it's better**: No need to set up Prometheus just to test your metrics during development.

### 📊 Kubernetes Metric Investigation

**Problem**: You need to debug metrics from a pod in your K8s cluster.

**Solution**: Port-forward and capture metrics for offline analysis:

```bash
# Port-forward the service
kubectl -n production port-forward svc/my-app 9090:9090 &

# Capture and explore
promql-cli query -c ".scrape http://localhost:9090/metrics 6 10s; .save debug-snapshot.prom"
> .labels http_requests_total{job="my-app"}  # Find problematic series
> histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[1m]))
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

<details>
<summary><strong>⚡ More Use Cases (click to expand)</strong></summary>

**CI/CD Integration** - Validate exporter metrics in your CI pipeline:

```bash
docker run --rm promql-cli:latest query \
  -c ".scrape http://test-exporter:8080/metrics" \
  -q "up{job='test-exporter'} == 1" \
  -o json | jq '.data.result | length > 0'
```

**Alert Rule Testing** - Test Prometheus alert rules against historical data:

```bash
promql-cli query -c ".load historical-data.prom; .pinat 2023-12-01T10:00:00Z"
> rate(errors_total[5m]) > 0.1  # Test your alert condition
```

**Metric Comparison** - Compare metrics between different time periods or environments:

```bash
promql-cli query snapshot-before.prom
> .save /tmp/before-analysis.prom
promql-cli query snapshot-after.prom
> .load /tmp/before-analysis.prom     # Load both for comparison
```

</details>

## 🎓 5-Minute Tutorial

Want to get hands-on? Follow this quick tutorial:

**Step 1: Get sample metrics**
```bash
# Create a sample metrics file with realistic data
cat > tutorial.prom <<'EOF'
# HELP http_requests_total Total HTTP requests
# TYPE http_requests_total counter
http_requests_total{method="GET",code="200",service="api"} 1543 1704067200000
http_requests_total{method="GET",code="404",service="api"} 23 1704067200000
http_requests_total{method="GET",code="500",service="api"} 5 1704067200000
http_requests_total{method="POST",code="200",service="api"} 892 1704067200000
http_requests_total{method="POST",code="500",service="api"} 12 1704067200000

# HELP process_cpu_seconds_total Total CPU time
# TYPE process_cpu_seconds_total counter
process_cpu_seconds_total{service="api"} 45.3 1704067200000
process_cpu_seconds_total{service="db"} 123.7 1704067200000
EOF
```

**Step 2: Start exploring**
```bash
promql-cli query --repl=prompt tutorial.prom
```

**Step 3: Try these commands in the REPL**
```promql
# See what metrics are available
> .metrics

# Explore labels for a specific metric
> .labels http_requests_total

# Run your first query - see all HTTP requests
> http_requests_total

# Calculate error rate (5xx errors)
> rate(http_requests_total{code="500"}[5m])

# Get total requests by method
> sum by (method) (http_requests_total)

# Find error percentage
> sum(http_requests_total{code=~"5.."}) / sum(http_requests_total) * 100

# Generate historical data to test rate() properly
> .seed http_requests_total 20 30s

# Now calculate actual rate
> rate(http_requests_total[2m])
```

**Step 4: Try AI assistance** (requires API key)
```bash
# Set your API key first
export ANTHROPIC_API_KEY="your-key-here"
# or
export OPENAI_API_KEY="your-key-here"

# Start with AI enabled
promql-cli query --repl=prompt --ai "provider=claude" tutorial.prom
```

```promql
# Ask AI for help
> .ai ask show me the error rate by service

# Run one of the AI suggestions
> .ai run 1

# Get more creative queries
> .ai ask which service has the highest CPU usage
```

**Step 5: Save your work**
```bash
# Save modified metrics (with generated history)
> .save tutorial-with-history.prom

# Exit
> .quit
```

🎉 **Congratulations!** You now know the basics. Check out the sections below for advanced features.

## 🔄 promql-cli vs Alternatives

| Feature / Task | promql-cli | Prometheus + Grafana | promtool |
|----------------|------------|----------------------|----------|
| **Setup time** | < 1 minute | 15-30 minutes | < 1 minute |
| **Query metrics file** | ✅ Native | ❌ Need import | ✅ Limited |
| **Interactive REPL** | ✅ Rich w/ autocomplete | ➖ Web UI only | ❌ No |
| **AI query assistance** | ✅ Built-in | ❌ No | ❌ No |
| **Offline usage** | ✅ Yes | ✅ Yes | ✅ Yes |
| **Generate test data** | ✅ `.seed` command | ➖ Manual | ❌ No |
| **Scrape live endpoints** | ✅ `.scrape` command | ✅ Config required | ❌ No |
| **Test alert rules** | ✅ `--rules` flag | ✅ Full featured | ✅ Limited |
| **JSON output** | ✅ `-o json` | ✅ API | ✅ Yes |
| **Multi-line queries** | ✅ Native | ✅ Yes | ❌ No |
| **Visualization** | ❌ No | ✅ Graphs/dashboards | ❌ No |
| **Time-series storage** | ➖ In-memory only | ✅ Persistent | ❌ No |
| **Best for** | Dev/debug/learn | Production monitoring | CI/CD validation |

**When to use promql-cli:**
- 🚀 Developing/testing Prometheus exporters
- 🐛 Debugging metrics offline
- 📚 Learning PromQL interactively
- 🧪 Testing queries before deploying to production
- 💻 Quick metric analysis without infrastructure

**When to use Prometheus + Grafana:**
- 📈 Production monitoring with alerts
- 📊 Visual dashboards and graphs
- 🗄️ Long-term metric storage
- 👥 Team collaboration and sharing

**When to use promtool:**
- ✅ CI/CD pipeline validation
- 🔍 Rule syntax checking
- 📋 Non-interactive testing

## 📋 Command Reference

### CLI Commands

| Command | Description |
|---------|-------------|
| `promql-cli query [file.prom]` | Start interactive REPL (optionally load metrics file) |
| `promql-cli load <file.prom>` | Parse and load metrics file (shows summary) |
| `promql-cli version` | Show version information |

### CLI Options

| Option | Description | When to Use | Example |
|--------|-------------|-------------|---------|
| `-q, --query "<expr>"` | Run single query and exit | Scripting, CI/CD, quick checks | `-q 'up'` |
| `-f, --file <file>` | Execute PromQL queries from file | Batch query execution, testing suites | `-f queries.promql` |
| `-o, --output json` | Output JSON format (with `-q`) | Piping to jq, programmatic parsing | `-q 'up' -o json` |
| `-c, --command "cmds"` | Run commands before REPL/query | Automating data loading, setup | `-c ".scrape http://localhost:9100/metrics"` |
| `-s, --silent` | Suppress startup output | Scripts, clean output | `-s -c ".load data.prom"` |
| `--rules {dir/,fileglob.yml}` | Load alerting/recording rules | Testing alert rules | `--rules example-rules.yml` |
| `--repl {prompt\|readline}` | Choose REPL backend | Use `prompt` for autocompletion | `--repl prompt` |
| `--ai "key=value,..."` | Configure AI settings in one flag | Query suggestions, learning PromQL | `--ai "provider=claude,model=opus"` |

### 🤖 REPL Commands (Grouped by Workflow)

#### **Getting Started with Data**

| Command | What it does | Example |
|---------|--------------|---------|
| `.load <file> [timestamp=...] [regex='...']` | Load metrics from file | `.load metrics.prom` |
| `.scrape <url> [regex] [count] [delay]` | Fetch live metrics from HTTP endpoint | `.scrape http://localhost:9100/metrics` |
| `.prom_scrape <api> 'query' [...]` | Import instant data from Prometheus API | `.prom_scrape http://prom:9090 'up'` |
| `.source <file>` | Run queries from a file | `.source queries.promql` |

#### **Exploring Your Metrics**

| Command | What it does | Example |
|---------|--------------|---------|
| `.metrics` | List all available metrics | `.metrics` |
| `.labels <metric>` | Show what labels a metric has | `.labels http_requests_total` |
| `.timestamps <metric>` | Check timestamp information | `.timestamps http_requests_total` |

#### **Testing & Debugging Queries**

| Command | What it does | Example |
|---------|--------------|---------|
| `.seed <metric> [steps] [interval]` | Generate test data history | `.seed http_requests_total 20 30s` |
| `.pinat <time>` | Lock evaluation time (for testing) | `.pinat now-1h` |
| `.at <time> <query>` | Run query at specific time | `.at now-5m rate(cpu[1m])` |

#### **Managing Metrics**

| Command | What it does | Example |
|---------|--------------|---------|
| `.save <file> [timestamp=...] [regex='...']` | Export metrics to file | `.save snapshot.prom timestamp=remove` |
| `.rename <old> <new>` | Rename a metric | `.rename old_name new_name` |
| `.drop <regex>` | Delete metrics matching regex | `.drop test_.*` |
| `.keep <regex>` | Keep only matching metrics | `.keep important_.*` |

#### **AI-Powered Query Help**

| Command | What it does | Example |
|---------|--------------|---------|
| `.ai ask <question>` | Get query suggestions from AI | `.ai ask show me error rates` |
| `.ai run <N>` | Execute AI suggestion #N | `.ai run 1` |
| `.ai edit <N>` | Copy AI suggestion #N to clipboard | `.ai edit 2` |
| `.ai show` | Show all previous AI answers | `.ai show` |

#### **Advanced Data Import**

<details>
<summary>Click to expand: Prometheus API range queries and authentication</summary>

| Command | What it does | Example |
|---------|--------------|---------|
| `.prom_scrape_range <api> 'query' <start> <end> <step> [auth=...] [...]` | Import time-range data from Prometheus | `.prom_scrape_range http://prom:9090 'rate(http[5m])' now-1h now 30s` |

**Authentication options:**
- Basic auth: `auth=basic user=alice pass=secret`
- Mimir/tenant: `auth=mimir org_id=tenant1 api_key=$KEY`

</details>

**💡 Tip:** Type `.help` in the REPL to see all commands with descriptions.

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

### ⌨️ Keyboard Shortcuts Cheat Sheet

Enable with `--repl=prompt` for full keyboard support.

| Action | Shortcut | Notes |
|--------|----------|-------|
| **Navigation** |
| Jump to start/end of line | `Ctrl-A` / `Ctrl-E` | Like bash/emacs |
| Move by word | `Alt-B` / `Alt-F` | Backward/Forward |
| Search history (prefix) | `↑` / `↓` | Type prefix first, then arrow keys |
| Insert last argument | `Alt-.` | Cycles through previous args (bash-style) |
| **Editing** |
| Delete to line end/start | `Ctrl-K` / `Ctrl-U` | Kill to end/beginning |
| Delete previous word | `Ctrl-W` or `Ctrl-Backspace` | PromQL-aware (respects `(){},.`) |
| Delete forward word | `Alt-D` | |
| Delete backward word | `Alt-Backspace` | |
| **Multi-line Queries** |
| Line continuation | `\` (backslash at end) | Continue query on next line |
| Literal newline | `Alt-Enter` | Insert actual newline |
| **AI & External Tools** |
| Paste AI suggestion | `Ctrl-Y` | After `.ai edit N` |
| Open in external editor | `Ctrl-X Ctrl-E` | Uses `$EDITOR` (vim, nano, etc.) |
| **Completion** |
| Trigger completion | `Tab` | Context-aware PromQL completion |
| Accept completion | `Enter` or `→` | |

💡 **Pro tips:**
- Type a metric name prefix + `↑` to search history for queries with that metric
- Use `Alt-.` repeatedly to cycle through arguments from previous commands
- `Ctrl-W` understands PromQL syntax (e.g., stops at `{` when deleting in `metric_name{label="value"}`)

### 🤖 AI Configuration

![AI Demo](demo/demo-ai.gif)

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

## 📖 Query Recipes

Common PromQL patterns you can use with your metrics:

### Finding Top Consumers

```promql
# Top 10 memory consumers
topk(10, process_resident_memory_bytes)

# Top 5 error-generating services
topk(5, sum by (service) (rate(http_requests_total{code=~"5.."}[5m])))

# Bottom 5 by CPU usage
bottomk(5, rate(process_cpu_seconds_total[5m]))
```

### Error Rates & Percentages

```promql
# Overall error rate (errors per second)
sum(rate(http_requests_total{code=~"5.."}[5m]))

# Error percentage by service
sum by (service) (rate(http_requests_total{code=~"5.."}[5m]))
  / sum by (service) (rate(http_requests_total[5m])) * 100

# Request success rate (as percentage)
sum(rate(http_requests_total{code=~"2.."}[5m]))
  / sum(rate(http_requests_total[5m])) * 100
```

### Aggregations & Grouping

```promql
# Total requests per minute across all services
sum(rate(http_requests_total[1m])) * 60

# Requests by HTTP method
sum by (method) (http_requests_total)

# Average response time per endpoint
avg by (endpoint) (http_request_duration_seconds)

# Count of unique services reporting metrics
count(count by (service) (up))
```

### Latency & Percentiles

```promql
# 95th percentile latency
histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))

# 99th percentile by service
histogram_quantile(0.99,
  sum by (service, le) (rate(http_request_duration_seconds_bucket[5m])))

# Median (50th percentile) response time
histogram_quantile(0.5, rate(http_request_duration_seconds_bucket[5m]))
```

### Time Comparisons

```promql
# Current value vs 1 hour ago
http_requests_total - http_requests_total offset 1h

# Percentage change in last hour
((http_requests_total - http_requests_total offset 1h)
  / http_requests_total offset 1h) * 100

# Rate of change (derivative)
deriv(http_requests_total[10m])
```

### Predictions & Trends

```promql
# Predict value in 4 hours based on current trend
predict_linear(http_requests_total[1h], 4*3600)

# How fast is disk filling up (hours until full)
predict_linear(node_filesystem_free_bytes[1h], 3600) /
  node_filesystem_free_bytes < 0
```

### Testing with Generated Data

```bash
# Generate realistic test data first
> .seed http_requests_total 50 1m
> .pinat now

# Then test your queries
> rate(http_requests_total[5m])
> increase(http_requests_total[10m])
```

💡 **Pro tip:** Use `.ai ask` to generate custom queries for your specific metrics!

## 🔧 Common Workflows

Real-world step-by-step workflows you can follow:

### Workflow 1: Debugging a Production Issue

```bash
# 1. Capture current state from production
promql-cli query -c ".scrape http://prod-server:9090/metrics; .save prod-snapshot.prom timestamp=now"

# 2. Work offline with the snapshot
promql-cli query --repl=prompt prod-snapshot.prom

# 3. Investigate in the REPL
> .metrics | grep error
> .labels http_errors_total
> rate(http_errors_total[5m])
> sum by (service, code) (http_errors_total)

# 4. Share findings - save filtered metrics
> .save error-metrics-only.prom regex='.*error.*'
```

### Workflow 2: Developing a New Exporter

```bash
# 1. Start your exporter on localhost:9123
./my-exporter --port=9123 &

# 2. Test with live reloading (scrape every 5 seconds, 100 times)
promql-cli query --repl=prompt -c ".scrape http://localhost:9123/metrics 100 5s; .pinat now"

# 3. In the REPL: verify metrics structure
> .metrics
> .labels my_custom_metric
> .timestamps my_custom_metric

# 4. Test rate calculations work correctly
> rate(my_custom_metric[30s])

# 5. Generate historical data to test range queries
> .seed my_custom_metric 50 10s
> rate(my_custom_metric[2m])
> increase(my_custom_metric[5m])
```

### Workflow 3: Testing Alert Rules Before Deployment

```bash
# 1. Get sample data from production
promql-cli query -c ".prom_scrape http://prod:9090 'up or http_requests_total'; .save test-data.prom"

# 2. Test your alert expression locally
promql-cli query test-data.prom

# 3. Test the alert condition
> rate(http_requests_total{code="500"}[5m]) > 0.1
> absent(up{job="critical-service"})

# 4. Use AI to refine the query
> .ai ask improve this alert to detect sustained high error rates
> .ai run 1

# 5. Save validated query for deployment
> .save validated-metrics.prom
```

### Workflow 4: Learning PromQL with Real Data

```bash
# 1. Get node_exporter running
docker run -d --name=node-exporter --net="host" --pid="host" \
  -v "/:/host:ro,rslave" prom/node-exporter:latest --path.rootfs=/host

# 2. Start learning session with AI help
promql-cli query --ai "provider=claude" \
  -c ".scrape http://localhost:9100/metrics 3 10s; .metrics"

# 3. Explore and learn
> .ai ask show me CPU usage patterns
> .ai run 1
> .ai ask how do I calculate memory percentage
> .ai run 2

# 4. Practice with generated data
> .seed node_cpu_seconds_total 100 30s
> rate(node_cpu_seconds_total[5m])
```

### Workflow 5: Batch Query Validation in CI/CD

```bash
# Create test query suite
cat > queries-to-validate.promql <<EOF
# Health checks
up{job="api"} == 1
up{job="database"} == 1

# Performance checks
rate(http_requests_total[5m]) > 0
histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m])) < 1
EOF

# Run in CI pipeline
promql-cli query -f queries-to-validate.promql test-metrics.prom > results.txt
if [ $? -eq 0 ]; then
  echo "✅ All queries validated successfully"
else
  echo "❌ Query validation failed"
  exit 1
fi
```

### Workflow 6: Comparing Metrics Across Environments

```bash
# 1. Capture from both environments
promql-cli query -c ".scrape http://staging:9090/metrics; .save staging.prom"
promql-cli query -c ".scrape http://prod:9090/metrics; .save prod.prom"

# 2. Load both and compare
promql-cli query --repl=prompt staging.prom
> .load prod.prom  # Load second file into same session
> .metrics  # See all metrics from both

# 3. Compare specific metrics
> http_requests_total{env="staging"}
> http_requests_total{env="prod"}

# 4. Calculate differences
> sum by (service) (http_requests_total{env="prod"})
  - sum by (service) (http_requests_total{env="staging"})
```

## 🔧 Troubleshooting

### Autocompletion Not Working

**Problem:** Tab completion doesn't suggest metrics or labels.

**Solution:**
```bash
# Make sure you're using the prompt backend
promql-cli query --repl=prompt metrics.prom

# Verify metrics are loaded
> .metrics
```

**Common causes:**
- Using default `readline` backend (no advanced completion)
- No metrics loaded yet (use `.load` or `.scrape` first)
- Metrics file is empty or malformed

### AI Not Responding

**Problem:** `.ai ask` returns errors or no response.

**Solution:**
```bash
# 1. Check API key is set
echo $OPENAI_API_KEY        # For OpenAI
echo $ANTHROPIC_API_KEY     # For Claude
echo $XAI_API_KEY           # For Grok

# 2. Verify provider configuration
promql-cli query --ai "provider=claude" metrics.prom

# 3. Test with debug mode
export PROMQL_CLI_AI_DEBUG=1
promql-cli query --ai "provider=claude" metrics.prom
```

**Common causes:**
- Missing or invalid API key
- Wrong provider name (use: `openai`, `claude`, `grok`, `ollama`)
- Network connectivity issues
- API rate limits exceeded

### Query Returns "No Data" or Empty Results

**Problem:** Queries return no results even though metrics are loaded.

**Solution:**
```bash
# 1. Verify metrics exist, and their labels
> .metrics
> .labels some_metric

# 2. Check metric name exactly (case-sensitive)
> http_requests_total    # Not HTTP_Requests_Total

# 3. Verify timestamp alignment
> .timestamps http_requests_total
> .pinat now              # Pin evaluation time to now

# 4. Check if metrics have history for rate() queries
> .seed http_requests_total 20 30s    # Generate test history
> rate(http_requests_total[2m])
```

**Common causes:**
- Evaluation time outside metric timestamp range, see `.pinat`
- Typo in metric name
- Using `rate()` on single-point counter (needs history)
- Label selectors don't match any series

### History/Arrow Keys Not Working

**Problem:** Up/down arrows show `^[[A` instead of history.

**Solution:**

```bash
# Try the (experimental) prompt backend
promql-cli query --repl=prompt metrics.prom

# If still broken, check terminal compatibility
echo $TERM    # Should be xterm-256color or similar
```

### Ctrl-W Deletes Entire Metric Name

**Problem:** `Ctrl-W` deletes too much when editing queries.

**Solution:**
This is an issue that has been worked-around in the (default) readline backend. Try the prompt backend:

```bash
promql-cli query --repl=prompt metrics.prom
```

### Docker Container Can't Access Host Services

**Problem:** `.scrape http://localhost:9100/metrics` fails in Docker.

**Solution:**

```bash
# Use --net=host to access host network
docker run --rm -it --net=host xjjo/promql-cli:latest query \
  -c ".scrape http://localhost:9100/metrics"

# Or use host.docker.internal on Mac/Windows
docker run --rm -it xjjo/promql-cli:latest query \
  -c ".scrape http://host.docker.internal:9100/metrics"
```

### Performance Issues with Large Metric Files

**Problem:** Slow loading or query performance.

**Solution:**

```bash
# 1. Filter metrics during load
> .load huge.prom regex='critical_metrics_.*'

# 2. Drop unused metrics after loading
> .keep important_.*

# 3. For very large files, use .prom_scrape with filters instead
> .prom_scrape http://prom:9090 'important_metric' 1
```

**💡 Still having issues?** Report bugs at https://github.com/jjo/promql-cli/issues

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

# Run tests
make test
```

**Runtime options:**

- Both REPL backends (`readline` and `prompt`) are built into the binary.
- Default backend: `readline` (UI less invasive)
- Experimental: for advanced, rich UI with autocompletion, run with `--repl=prompt`.
