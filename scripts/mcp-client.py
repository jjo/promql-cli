#!/usr/bin/env python3
"""
MCP CLI client for promql-cli.

Usage:
  ./mcp-client.py                    # quick demo with auto-generated data
  ./mcp-client.py metrics.prom       # quick demo with your file
  ./mcp-client.py metrics.prom batch # batch: read JSON-RPC commands from stdin
"""
import json
import subprocess
import sys
import tempfile
import os

DEFAULT_METRICS = """\
# HELP http_requests_total Total number of HTTP requests
# TYPE http_requests_total counter
http_requests_total{method="get",code="200"} 1027
http_requests_total{method="get",code="404"} 3
http_requests_total{method="post",code="200"} 523
http_requests_total{method="post",code="500"} 12
"""


def send(proc, msg):
    data = json.dumps(msg, separators=(",", ":")).encode()
    proc.stdin.write(f"Content-Length: {len(data)}\r\n\r\n".encode() + data)
    proc.stdin.flush()


def recv(proc):
    cl = 0
    while True:
        line = proc.stdout.readline()
        if not line:
            return {}
        ls = line.decode().strip()
        if ls == "" and cl > 0:
            break
        if ls.lower().startswith("content-length:"):
            cl = int(ls.split(":")[1].strip())
    return json.loads(proc.stdout.read(cl))


def rpc(proc, msg):
    send(proc, msg)
    return recv(proc)


def start_mcp(metrics_path=None):
    """Start promql-cli mcp with an optional metrics file."""
    if metrics_path and not os.path.exists(metrics_path):
        print(f"File not found: {metrics_path}", file=sys.stderr)
        sys.exit(1)

    if not metrics_path:
        # Write default metrics to a stable location
        metrics_path = "/tmp/promql-cli-mcp-default.prom"
        with open(metrics_path, "w") as f:
            f.write(DEFAULT_METRICS)

    # Determine binary path relative to this script
    script_dir = os.path.dirname(os.path.abspath(__file__))
    repo_root = os.path.dirname(script_dir)
    binary_path = os.path.join(repo_root, "bin", "promql-cli")

    return subprocess.Popen(
        [binary_path, "mcp", metrics_path],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
    )


def demo(proc):
    """Run a quick demo of MCP capabilities."""
    help_text = """
Available MCP tools — call via tools/call with JSON-RPC:
  execute_line   {"line": "<command>"}    any REPL command
  query_instant  {"promql": "<expr>"}     instant query
  query_range    {"promql": "...", "start": ..., "end": ..., "step": ...}
  list_metrics   {"prefix": "..."}        metric names
  list_labels    {"metric_name": "...", "prefix": "..."}
  load_metrics   {"data": "..."}          expose-format data

execute_line unlocks the full REPL:
  .scrape http://localhost:9100/metrics  — live scrape
  .seed metric 20 1m                     — backfill history
  .pinat now-5m; rate(cpu[5m])           — time travel
  .load /path/to/file.prom               — load more data
  .save snapshot.prom                    — save state
  !command                               — ❌ BLOCKED via MCP
"""

    # Initialize
    resp = rpc(proc, {
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {"protocolVersion": "2024-11-05", "clientInfo": {"name": "cli", "version": "0.1"}},
    })
    assert resp.get("error") is None, f"init error: {resp['error']}"
    print(f"✅ initialized  protocolVersion={resp['result']['protocolVersion']}")
    print(help_text)

    # tools/list
    resp = rpc(proc, {"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
    tools = [t["name"] for t in resp.get("result", {}).get("tools", [])]
    print(f"🔧 tools ({len(tools)}): {', '.join(tools)}\n")

    # Demo each tool
    demos = [
        (".metrics", "execute_line", {"line": ".metrics"}),
        ("PromQL query", "execute_line", {"line": "http_requests_total"}),
        ("sum by method", "query_instant", {"promql": "sum(http_requests_total) by (method)"}),
        ("!whoami blocked", "execute_line", {"line": "!whoami"}),
    ]

    for label, tool, args in demos:
        resp = rpc(proc, {
            "jsonrpc": "2.0", "id": 10 + demos.index((label, tool, args)),
            "method": "tools/call",
            "params": {"name": tool, "arguments": args},
        })
        result = resp.get("result", {})
        text = result.get("content", [{}])[0].get("text", "")
        is_err = result.get("isError", False)
        status = "❌" if is_err else "✅"
        print(f"  {status} {label}:")
        for line in text.strip().split("\n"):
            print(f"     {line}")
        print()

    # Shutdown
    rpc(proc, {"jsonrpc": "2.0", "id": 99, "method": "shutdown"})
    proc.wait(timeout=3)
    print("✅ done")


def batch(proc):
    """Read JSON-RPC messages from stdin, one per line."""
    print("Reading JSON-RPC commands from stdin (one JSON object per line)...", file=sys.stderr)
    for line in sys.stdin:
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError as e:
            print(f"⚠️  invalid JSON: {e}", file=sys.stderr)
            continue
        resp = rpc(proc, msg)
        result = resp.get("result")
        err = resp.get("error")
        if err:
            print(f"❌ {msg.get('method', '?')}: {err['message']}")
            if err.get("data"):
                print(f"   data: {err['data']}")
        else:
            if msg.get("method") == "tools/call":
                content = result.get("content", [])
                for c in content:
                    if c.get("isError"):
                        print(f"❌ {c['text']}")
                    else:
                        print(c["text"])
            else:
                print(json.dumps(result, indent=2))
        print()

    rpc(proc, {"jsonrpc": "2.0", "id": 999, "method": "shutdown"})
    proc.wait(timeout=3)


if __name__ == "__main__":
    args = sys.argv[1:]

    if len(args) >= 1 and args[0] not in ("-b", "--batch"):
        # Specific metrics file
        mcp_file = args[0]
        proc = start_mcp(mcp_file)
    else:
        # Use default embedded metrics
        proc = start_mcp(None)

    if "-b" in args or "--batch" in args or "batch" in args:
        batch(proc)
    else:
        demo(proc)
