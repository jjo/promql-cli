# AI assistance for promql-cli

This document explains how to enable and use the AI assistant in promql-cli to help you craft PromQL queries against your loaded metrics.

The AI assistant can:
- Propose one or more valid PromQL queries from a natural-language intent
- Let you run a selected suggestion immediately
- Let you paste a suggestion into the line to edit before running

Providers supported
- ollama (local, default)
- openai
- claude (Anthropic)
- grok (xAI)

Current default models
- ollama: llama3.1
- openai: gpt-4o-mini
- claude: claude-3-5-sonnet-20240620
- grok (xAI): grok-2

You can override these with flags (preferred) or environment variables.


## Quick start

### 1) Use the composite --ai flag

Pass key=value pairs (comma or space separated). Unknown keys are ignored.

Examples:
- OpenAI mini (requires OPENAI_API_KEY):
  ```shell
  promql-cli --ai 'provider=openai model=gpt-4o-mini' query
  ```
- Claude Sonnet (requires ANTHROPIC_API_KEY):
  ```shell
  promql-cli --ai 'provider=claude model=claude-3-5-sonnet-20240620' query
  ```
- Grok 2 (requires XAI_API_KEY):
  ```shell
  promql-cli --ai 'provider=grok model=grok-2' query
  ```
- Ollama llama3.1 (local):
  ```shell
  promql-cli --ai 'provider=ollama model=llama3.1 host=http://localhost:11434' query
  ```
- Ask for more answers (default 3):
  ```shell
  promql-cli --ai 'provider=claude model=claude-3-5-sonnet-20240620 answers=5' query
  ```

Supported keys:
- provider: ollama | openai | claude | grok (xAI)
- model: model name for the provider
- base: API base URL (openai/claude/grok) or host (ollama)
- host: alias for base for ollama
- answers: number of AI suggestions to request
- profile: named profile to load from ~/.config/promql-cli/ai.toml

### 2) Or use environment variables

- Provider: `PROMQL_CLI_AI_PROVIDER=ollama|openai|claude|grok`
- OpenAI: `OPENAI_API_KEY`, optional `PROMQL_CLI_OPENAI_MODEL`, `PROMQL_CLI_OPENAI_BASE`
- Claude: `ANTHROPIC_API_KEY`, optional `PROMQL_CLI_ANTHROPIC_MODEL`, `PROMQL_CLI_ANTHROPIC_BASE`
- Grok: `XAI_API_KEY`, optional `PROMQL_CLI_XAI_MODEL`, `PROMQL_CLI_XAI_BASE`
- Ollama: optional `PROMQL_CLI_OLLAMA_MODEL`, `PROMQL_CLI_OLLAMA_HOST`

Examples:
```shell
# OpenAI (mini)
export PROMQL_CLI_AI_PROVIDER=openai
export OPENAI_API_KEY=$YOUR_OPENAI_KEY
export PROMQL_CLI_OPENAI_MODEL=gpt-4o-mini
promql-cli query

# OpenAI (GPT-5)
export PROMQL_CLI_AI_PROVIDER=openai
export OPENAI_API_KEY=$YOUR_OPENAI_KEY
export PROMQL_CLI_OPENAI_MODEL=gpt-5
promql-cli query

# Claude (Sonnet)
export PROMQL_CLI_AI_PROVIDER=claude
export ANTHROPIC_API_KEY=$YOUR_ANTHROPIC_KEY
export PROMQL_CLI_ANTHROPIC_MODEL=claude-3-5-sonnet-20240620
promql-cli query

# Claude (Opus-4)
export PROMQL_CLI_AI_PROVIDER=claude
export ANTHROPIC_API_KEY=$YOUR_ANTHROPIC_KEY
export PROMQL_CLI_ANTHROPIC_MODEL=opus-4
promql-cli query

# Grok (xAI)
export PROMQL_CLI_AI_PROVIDER=grok
export XAI_API_KEY=$YOUR_XAI_KEY
export PROMQL_CLI_XAI_MODEL=grok-2
promql-cli query

# Grok (xAI latest)
export PROMQL_CLI_AI_PROVIDER=grok
export XAI_API_KEY=$YOUR_XAI_KEY
export PROMQL_CLI_XAI_MODEL=grok-2-latest
promql-cli query

# Ollama (local)
export PROMQL_CLI_AI_PROVIDER=ollama
export PROMQL_CLI_OLLAMA_MODEL=llama3.1
export PROMQL_CLI_OLLAMA_HOST=http://localhost:11434
promql-cli query
```


## Using the AI in the REPL

Once you are in the `query` REPL (with metrics loaded), use the `.ai` commands:

- `.ai ask <intent>`  (alias: `.ai <intent>`)
  - Ask the AI to propose 1–3 valid PromQL queries for your free-text intent.
- `.ai run <N>`
  - Run suggestion number `N` (1-based) immediately.
- `.ai edit <N>`
  - Prepare suggestion `N` for editing; then press Ctrl-Y to paste it into the line and edit freely.
- `.ai show`
  - Reprint the last list of suggestions.

### Example session

```
PromQL> .ai ask cpu usage per instance over last 30m
AI suggestions (valid PromQL):
  [1] sum by (instance) (rate(node_cpu_seconds_total{mode!="idle"}[30m]))
  [2] sum by (instance) (1 - avg(rate(node_cpu_seconds_total{mode="idle"}[30m])))
Choose with: .ai run <N>  or  .ai edit <N>  (1-based)

# Run the first suggestion directly
PromQL> .ai run 1
Vector (3 samples):
  [1] {instance="host-a"} => 0.12 @ 2025-09-20T...
  ...

# Or edit the second one before running
PromQL> .ai edit 2
Prepared suggestion [2] for editing. Press Ctrl-Y to paste.
PromQL> <Ctrl-Y>
PromQL> sum by (instance) (1 - avg(rate(node_cpu_seconds_total{mode="idle"}[30m])))
... edit as needed, then press Enter
```


## go-prompt autocompletion

- `.ai` requires a subcommand and completes:
  - `.ai ask `
  - `.ai run `
  - `.ai edit `
  - `.ai show`
- After `.ai run ` or `.ai edit `, indices 1..N are suggested from the last result set.

## readline completion

- `.ai` suggests subcommands `ask`, `run`, `edit`, `show`.
- After `.ai run ` or `.ai edit `, indices 1..N (up to 20) are suggested.


## Security notes

- For hosted providers (OpenAI/Claude/Grok), never print or paste your API keys in the terminal.
- Use environment variables to set keys, and avoid storing them in shell history if possible.
- The assistant only sends schema/context (metric names, labels, and optional help text). It does not send raw samples.


## Troubleshooting

- "No AI suggestions yet" when trying `.ai run <N>` or `.ai edit <N>`:
  - First generate suggestions via `.ai ask <intent>`.
- Unexpected quoted suggestions:
  - The CLI sanitizes suggestions (removes accidental quotes/blocks). If you still see issues, try `.ai show`, copy the query, and paste manually.
- Provider/model flags "not applied":
  - Flags apply at the start of the `load` and `query` subcommands. Ensure you pass them before running the REPL.


## Reference: configuration

- Composite flag (preferred):
  - `--ai 'provider=<p> model=<m> [base=<url>|host=<url>] [answers=<n>] [profile=<name>]'
- Environment variables:
  - Generic composite: `PROMQL_CLI_AI='provider=... model=... base=... answers=...'`
  - Profile selection: `PROMQL_CLI_AI_PROFILE=<name>`
  - API keys: `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `XAI_API_KEY`
  - Provider-specific defaults (optional):
    - OpenAI: `PROMQL_CLI_OPENAI_MODEL`, `PROMQL_CLI_OPENAI_BASE`
    - Claude: `PROMQL_CLI_ANTHROPIC_MODEL`, `PROMQL_CLI_ANTHROPIC_BASE`
    - Grok: `PROMQL_CLI_XAI_MODEL`, `PROMQL_CLI_XAI_BASE`
    - Ollama: `PROMQL_CLI_OLLAMA_MODEL`, `PROMQL_CLI_OLLAMA_HOST`
