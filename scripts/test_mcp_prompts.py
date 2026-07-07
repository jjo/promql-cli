#!/usr/bin/env python3
"""MCP Prompts Test - validates prompts/list, prompts/get with arguments"""
import json
import subprocess
import sys


def main():
    proc = subprocess.Popen(
        ["./bin/promql-cli", "mcp", "examples/example.prom"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
    )

    def rpc(msg):
        data = json.dumps(msg, separators=(",", ":")).encode()
        proc.stdin.write(f"Content-Length: {len(data)}\r\n\r\n".encode() + data)
        proc.stdin.flush()
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

    rpc({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2024-11-05"}})

    # List prompts
    r = rpc({"jsonrpc": "2.0", "id": 2, "method": "prompts/list"})
    prompt_names = [p["name"] for p in r["result"]["prompts"]]
    assert "error-rate-by-service" in prompt_names, "error-rate-by-service missing"
    assert "latency-percentiles" in prompt_names, "latency-percentiles missing"

    # Get prompt with arguments
    r = rpc({"jsonrpc": "2.0", "id": 3, "method": "prompts/get", "params": {"name": "error-rate-by-service", "arguments": {"window": "10m"}}})
    messages = r["result"]["messages"]
    assert 'rate(http_requests_total{code=~"5.."}[10m])' in messages[0]["content"]["text"], "error-rate prompt failed"

    # Get latency prompt
    r = rpc({"jsonrpc": "2.0", "id": 4, "method": "prompts/get", "params": {"name": "latency-percentiles", "arguments": {"metric": "http_request_duration_seconds", "window": "5m"}}})
    messages = r["result"]["messages"]
    assert len(messages) == 3, f"expected 3 messages, got {len(messages)}"
    assert "histogram_quantile(0.50" in messages[0]["content"]["text"], "p50 missing"
    assert "histogram_quantile(0.99" in messages[2]["content"]["text"], "p99 missing"

    rpc({"jsonrpc": "2.0", "id": 99, "method": "shutdown"})
    proc.wait(timeout=3)
    print("✅ MCP prompts test passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())