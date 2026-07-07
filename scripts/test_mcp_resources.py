#!/usr/bin/env python3
"""MCP Resources Test - validates resources/list, resources/read"""
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

    # List resources
    r = rpc({"jsonrpc": "2.0", "id": 2, "method": "resources/list"})
    resources = [res["uri"] for res in r["result"]["resources"]]
    assert "promql://metrics" in resources, "promql://metrics missing"
    assert any(r.startswith("promql://metrics/") for r in resources), "dynamic metrics missing"
    assert any(r.startswith("promql://labels/") for r in resources), "per-label resources missing"
    assert "promql://labels" in resources, "promql://labels (list) missing"

    # Read all metrics
    r = rpc({"jsonrpc": "2.0", "id": 3, "method": "resources/read", "params": {"uri": "promql://metrics"}})
    assert "Metrics (38 total)" in r["result"]["contents"][0]["text"], "read all failed"

    # Read specific metric
    r = rpc({"jsonrpc": "2.0", "id": 4, "method": "resources/read", "params": {"uri": "promql://metrics/http_requests_total"}})
    samples = json.loads(r["result"]["contents"][0]["text"])
    assert len(samples) == 6, f"expected 6 samples, got {len(samples)}"
    assert all("labels" in s and "value" in s for s in samples), "sample format invalid"

    # Read per-label resource (job label is always present in example.prom)
    r = rpc({"jsonrpc": "2.0", "id": 5, "method": "resources/read", "params": {"uri": "promql://labels/job"}})
    label_vals = json.loads(r["result"]["contents"][0]["text"])
    assert len(label_vals) > 0, "expected label values for 'job'"
    assert all("value" in v and "series_count" in v for v in label_vals), "label value format invalid"
    # Verify the values are sorted
    vals = [v["value"] for v in label_vals]
    assert vals == sorted(vals), f"label values not sorted: {vals}"

    # Read non-existent label — should return a text error, not crash
    r = rpc({"jsonrpc": "2.0", "id": 6, "method": "resources/read", "params": {"uri": "promql://labels/nonexistent_label"}})
    text = r["result"]["contents"][0]["text"]
    assert "not found" in text.lower(), f"expected 'not found', got: {text}"

    rpc({"jsonrpc": "2.0", "id": 99, "method": "shutdown"})
    proc.wait(timeout=3)
    print("✅ MCP resources test passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())