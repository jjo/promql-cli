# promql-cli Examples Guide

This guide demonstrates how to use the example metrics and queries included in the `examples/` directory. These examples showcase different use cases and help you understand promql-cli capabilities.

## 📁 Example Files Overview

| File | Description | Use Case |
|------|-------------|----------|
| `example.prom` | Comprehensive snapshot metrics from a web/api service | Instant queries, aggregations, exploring metrics |
| `example.promql` | Ready-to-run queries for `example.prom` | Learning PromQL basics, instant metric analysis |
| `example_range.prom` | Time-series data with multiple timestamps | Rate calculations, histogram quantiles, trends |
| `example_range.promql` | Queries demonstrating rate() and histogram operations | Testing time-series queries |

## 🚀 Quick Start with Examples

### Example 1: Exploring Instant Metrics

Load the snapshot metrics file and explore available data:

```bash
# Start interactive session with example metrics
promql-cli query examples/example.prom

# Or use the prompt backend for better autocompletion
promql-cli --repl=prompt query examples/example.prom
```

**In the REPL, try:**

```promql
# See all available metrics
> .metrics

# Explore labels for HTTP requests
> .labels http_requests_total

# Check service health
> up

# View memory usage in GB
> process_resident_memory_bytes / 2^30

# Find disk usage percentage
> 100 * disk_usage_bytes / disk_total_bytes

# Check which dependencies are down
> service_dependencies_up == 0
```

### Example 2: Running Pre-written Queries

Execute all example queries from the `.promql` file:

```bash
# Run all queries from the file
promql-cli query -f examples/example.promql examples/example.prom

# Or source them interactively
promql-cli query examples/example.prom
> .source examples/example.promql
```

**Expected output:** You'll see each query executed with its results, demonstrating:
- Basic metric selection and filtering
- Instant value calculations (disk usage %, memory in GB)
- Aggregations (total requests by service, sessions by region)
- Summary metric quantiles (database latency, GC pauses)
- Multi-line queries

### Example 3: Time-Series and Rate Calculations

The `example_range.prom` file contains time-series data perfect for testing rate() calculations:

```bash
# Load with timestamp pinning for consistent results
promql-cli query --timestamp=now examples/example_range.prom -c ".pinat now"

# Or let promql-cli load it automatically
promql-cli --repl=prompt query --timestamp=now examples/example_range.prom
```

**Important:** When working with time-series data, pin the evaluation time, so that all queries are pinned to a timestamp with time-series data.

```promql
# Pin evaluation time to now
> .pinat now

# Check timestamps to understand the data range
> .timestamps http_requests_total

# Now try rate calculations
> rate(http_requests_total[5m])

# Calculate error rate
> sum by (code) (rate(http_requests_total[5m]))

# Get 95th percentile latency from histogram
> histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))
```

### Example 4: Complete Time-Series Query Suite

Run all time-series example queries:

```bash
# Execute all queries from example_range.promql
promql-cli query --timestamp=now -f examples/example_range.promql examples/example_range.prom
```

**What you'll see:**
- HTTP request rates per second
- Error ratios and percentages
- Histogram quantile calculations (p50, p95, p99)
- CPU usage rates
- Cache hit ratios over time
- Complex multi-line aggregations

## 🎓 Learning Workflows

### Workflow 1: Understanding Your Metrics

**Goal:** Explore what metrics are available and their structure.

```bash
promql-cli query examples/example.prom
```

```promql
# 1. List all metrics
> .metrics

# 2. Pick a metric and see its labels
> .labels http_requests_total

# 3. View the raw metric values
> http_requests_total

# 4. Filter by specific labels
> http_requests_total{code="500"}

# 5. Check summary metrics
> db_query_duration_seconds
```

### Workflow 2: Testing Aggregation Queries

**Goal:** Practice PromQL aggregation functions.

```bash
promql-cli query examples/example.prom
```

```promql
# Count unique services
> count(count by (service) (up))

# Average memory by container
> avg by (namespace) (container_memory_usage_bytes)

# Find high disk usage systems
> 100 * disk_usage_bytes / disk_total_bytes > 80
```

### Workflow 3: Mastering Rate Calculations

**Goal:** Understand how rate() works with counter metrics.

```bash
promql-cli query --timestamp=now examples/example_range.prom -c ".pinat now"
```

```promql
# Basic rate (requests per second)
> rate(http_requests_total[5m])

# Group by dimension
> sum by (service, code) (rate(http_requests_total[5m]))

# Calculate error percentage
> 100 * sum(rate(http_requests_total{code=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))

# CPU usage in cores
> sum by (service) (rate(process_cpu_seconds_total[5m]))

# Cache hit ratio
> sum(rate(cache_hits_total[5m])) / (sum(rate(cache_hits_total[5m])) + sum(rate(cache_misses_total[5m])))
```

### Workflow 4: Working with Histograms

**Goal:** Extract latency percentiles from histogram metrics.

```bash
promql-cli query --timestamp=now examples/example_range.prom -c ".pinat now"
```

```promql
# 95th percentile latency
> histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))

# 99th percentile (tail latency)
> histogram_quantile(0.99, rate(http_request_duration_seconds_bucket[5m]))

# Median (50th percentile)
> histogram_quantile(0.50, rate(http_request_duration_seconds_bucket[5m]))

# Average from histogram _sum and _count
> rate(http_request_duration_seconds_sum[5m]) / rate(http_request_duration_seconds_count[5m])

# Quantile grouped by service
> histogram_quantile(0.95, sum by (service, le) (rate(http_request_duration_seconds_bucket[5m])))
```

### Workflow 5: Testing Alert and Recording Rules

**Goal:** Load and test Prometheus alert rules with time-series data.

```bash
# Load rules with time-series data
promql-cli query --rules ./examples/example-rules.yaml --timestamp=now ./examples/example_range.prom
```

**In the REPL:**

```promql
# 1. Show loaded recording and alerting rules
> .rules

# 2. List all alerting rules
> .alerts

# 3. Execute alert by name (autocompletes with TAB)
> HighErrorRatioRate

# 4. Recording rules are automatically available as metrics
> http_requests_total_by_code
> node_count
```

**Expected output:** Rules are evaluated at load time, recording rules add metrics to storage, and alert expressions can be executed directly by name.

### Workflow 6: Building Complex Multi-line Queries

**Goal:** Practice writing readable, complex PromQL expressions.

```bash
promql-cli query examples/example.prom
```

```promql
# Multi-line disk usage check
> 100 * ( \
  disk_total_bytes - disk_usage_bytes \
) / disk_total_bytes

# Service health (combining conditions)
> ( \
  up == 1 \
  and \
  service_dependencies_up == 1 \
)

# Container resource usage by namespace
> sum by (pod, namespace) ( \
  container_memory_usage_bytes \
)
```

## 🤖 Using Examples with AI

Add AI assistance to learn faster:

```bash
# Start with AI enabled (requires API key)
export ANTHROPIC_API_KEY="your-key"
promql-cli --ai provider=claude,model=claude-opus-4-1-20250805,answers=3 query --timestamp=now examples/example_range.prom
```

```promql
# Ask AI about the metrics
> .ai ask what metrics are related to errors?

# Get query suggestions
> .ai ask show me how to calculate cache hit rate

# Run AI-generated queries
> .ai run 1

# Ask about specific patterns
> .ai ask how do I find services with high cpu usage?
```

## 🧪 Testing and Experimentation

### Generate More Test Data

The example range data has limited history. Generate more:

```bash
promql-cli query examples/example.prom
```

```promql
# Generate 50 data points at 1-minute intervals
> .seed http_requests_total 50 1m

# Pin evaluation time to now
> .pinat now

# Now rate() will actually work (simulated range time-series)
> rate(http_requests_total[5m])

# Save extended data for later
> .save foo.prom
```

### Modify and Save Filtered Data

Extract specific metrics for focused testing:

```bash
promql-cli query examples/example.prom
```

```promql
# Keep only HTTP metrics
> .keep http_

# Save filtered metrics
> .save /tmp/http-only.prom
```

## 📊 Use Case Examples

### Use Case 1: API Error Rate Dashboard Query

**Goal:** Calculate error rate percentage for dashboards

```bash
promql-cli query --timestamp=now examples/example_range.prom -c ".pinat now"
```

```promql
# Error rate as percentage
> ( \
  sum(rate(http_requests_total{code=~"5.."}[5m])) \
  / \
  sum(rate(http_requests_total[5m])) \
) * 100
```

### Use Case 2: Latency SLA Validation

**Goal:** Check if 95th percentile latency meets SLA (<500ms)

```bash
promql-cli query --timestamp=now examples/example_range.prom -c ".pinat now"
```

```promql
# Check p95 latency
> histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))

> histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m])) < 0.5
```

### Use Case 3: Resource Usage Report

**Goal:** Generate resource usage summary

```bash
promql-cli query examples/example.prom -f - <<EOF
# Memory usage by service (in GB)
process_resident_memory_bytes / 2^30

# Disk usage percentage
100 * disk_usage_bytes / disk_total_bytes

# Active connections by service
sum by (service) (active_sessions)

# Worker success rate
sum by (worker, status) (worker_tasks_processed_total)
EOF
```

### Use Case 4: Cache Performance Analysis

**Goal:** Evaluate cache effectiveness

```bash
promql-cli query --timestamp=now examples/example_range.prom -c ".pinat now"
```

```promql
# Cache hit ratio
> sum(rate(cache_hits_total[5m])) / (sum(rate(cache_hits_total[5m])) + sum(rate(cache_misses_total[5m])))

```

### Use Case 5: Service Health Check

**Goal:** Validate all services and dependencies are healthy

```bash
promql-cli query examples/example.prom
```

```promql
# Services that are down
> up == 0

# Dependencies that are down
> service_dependencies_up == 0

# Combined health check
> (up == 1) and (service_dependencies_up == 1)
```

## 🔧 Advanced Techniques

### JSON Output for Scripts

Process query results programmatically:

```bash
# Get results as JSON
promql-cli query -q 'up' -o json examples/example.prom | jq

# Extract specific values
promql-cli query -q 'sum by (service) (http_requests_total)' -o json examples/example.prom | jq '.data.result[].metric.service'

# Check if any service is down
promql-cli query -q 'up == 0' -o json examples/example.prom | jq '.data.result | length'
```

### Batch Query Validation

Test multiple queries at once:

```bash
# Validate all example queries work
promql-cli query -f examples/example.promql examples/example.prom > /dev/null && echo "✅ All queries passed"

# Same for time-series queries
promql-cli query -f examples/example_range.promql examples/example_range.prom -c ".pinat now" > /dev/null && echo "✅ All rate queries passed"
```

### Combining Example Files

Load both examples for comprehensive testing:

```bash
promql-cli query examples/example.prom
```

```promql
# Load time-series data too
> .load examples/example_range.prom timestamp=now

# Now you have both instant and time-series metrics
> .metrics

# Query across both
> http_requests_total
```

## 💡 Tips and Best Practices

1. **Always use `.pinat now`** when working with `example_range.prom` to align evaluation time with the data
2. **Use `.timestamps <metric>`** to understand the time range of your data
3. **Generate more history** with `.seed` when testing rate() on snapshot data
4. **Start with `example.promql`** to learn basic queries, then move to `example_range.promql` for advanced patterns
5. **Use `--repl=prompt`** for the experimental UX experience with autocompletion
6. **Combine with AI** (`--ai "provider=..."`) to get query suggestions and learn faster
7. **Save filtered data** with `.save ... regex='...'` to create focused test datasets
8. **Use `-f` flag** to run query suites in CI/CD or for regression testing

For more information, see the main [README.md](README.md).
