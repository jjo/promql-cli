# promql-cli

> **A lightweight PromQL playground and REPL for rapid Prometheus metric exploration**

Load Prometheus text-format metrics, query them with the upstream Prometheus engine, and iterate quickly with intelligent autocompletion and AI assistance. Perfect for developing exporters, debugging metrics, and learning PromQL.

## ✨ Key Features

- 🚀 **Interactive REPL** with rich PromQL-aware autocompletion
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
| `-o, --output json` | Output JSON format (with `-q`) | `-q 'up' -o json` |
| `-c, --command "cmds"` | Run commands before REPL/query | `-c ".scrape http://localhost:9100/metrics"` |
| `-s, --silent` | Suppress startup output | `-s -c ".load data.prom"` |
| `--repl {prompt\|readline}` | Choose REPL backend | `--repl readline` |

### 🤖 REPL Commands

Commands you can use inside the interactive session:

#### **Data Management**
| Command | Purpose | Example |
|---------|---------|---------|
| `.load <file>` | Load metrics from file | `.load metrics.prom` |
| `.save <file>` | Save current metrics | `.save snapshot.prom` |
| `.scrape <url> [regex] [count] [delay]` | Fetch live metrics | `.scrape http://localhost:9100/metrics` |
| `.drop <metric>` | Remove metric from memory | `.drop http_requests_total` |

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
| `.pinat [time]` | Pin evaluation time | `.pinat now` |
| `.seed <metric> [steps] [interval]` | Generate historical data | `.seed http_requests_total 10 1m` |

#### **AI Assistance**
| Command | Purpose | Example |
|---------|---------|---------|
| `.ai <question>` | Get AI query suggestions | `.ai show me error rates by service` |
| `.help` | Show all commands | `.help` |

## ⚡ Advanced Features

### Smart Autocompletion

The default go-prompt backend provides context-aware PromQL suggestions:

- **🎯 Context-aware**: Suggests metrics, functions, and labels based on what you're typing
- **📚 Documentation**: Shows help text and function signatures
- **🔄 Dynamic updates**: Refreshes automatically after loading new data
- **⌨️ Multi-line support**: Alt+Enter or backslash continuation

```promql
# Examples of smart completion:
http_req<Tab>                    # → http_requests_total
rate(http<Tab>                   # → rate(http_requests_total
http_requests_total{<Tab>        # → shows actual labels
http_requests_total{code="<Tab>  # → shows real label values
sum by (<Tab>                    # → suggests relevant grouping labels
```

### ⌨️ Key Bindings

**Navigation**: `Ctrl-A/E` (line start/end), `Alt-B/F` (word movement), `↑/↓` (prefix-filtered history)
**Editing**: `Ctrl-K/U/W` (delete to end/start/word), `Alt-D/Backspace` (delete word forward/back)
**Multi-line**: `Alt-Enter` or `\` (line continuation)
**AI**: `Ctrl-Y` (paste AI suggestion)

### 🤖 AI Configuration

| Provider | Environment Variables | Default Model |
|----------|----------------------|---------------|
| **OpenAI** | `OPENAI_API_KEY` | gpt-4o-mini |
| **Claude** | `ANTHROPIC_API_KEY` | claude-3-5-sonnet-20240620 |
| **Grok** | `XAI_API_KEY` | grok-2 |
| **Ollama** | `PROMQL_CLI_OLLAMA_HOST` | llama3.1 |

Set provider: `export PROMQL_CLI_AI_PROVIDER=claude`

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

## 📚 PromQL Examples

Common patterns you can try with your metrics:

```promql
# Request rate by service
sum by (service) (http_requests_total)

# Error rate percentage
sum(rate(http_requests_total{code=~"5.."}[5m])) / sum(rate(http_requests_total[5m])) * 100

# 95th percentile latency
histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[5m])))

# Top 5 memory consumers
topk(5, process_resident_memory_bytes)

# Resource usage trends
rate(cpu_usage_seconds_total[5m])
rate(memory_usage_bytes[5m])
```

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
# Full-featured build (default)
make build

# Minimal build (smaller binary)
make build-no-prompt

# See all options
make help

# Run tests
make test
```

**Build options:**
- `prompt` tag: Advanced REPL with autocompletion (default)
- No tags: Basic readline interface for minimal deployments
