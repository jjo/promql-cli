#!/usr/bin/env python3
"""MCP Tools Test - validates tools/list, tools/call, ping"""
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

    # Initialize
    r = rpc({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2024-11-05"}})
    assert r.get("result", {}).get("protocolVersion") == "2024-11-05", "initialize failed"

    # Ping
    r = rpc({"jsonrpc": "2.0", "id": 2, "method": "ping"})
    assert "result" in r, "ping failed"

    # List tools
    r = rpc({"jsonrpc": "2.0", "id": 3, "method": "tools/list"})
    tools = [t["name"] for t in r["result"]["tools"]]
    assert "query_instant" in tools and "execute_line" in tools, "tools missing"

    # Execute query
    r = rpc({"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": "query_instant", "arguments": {"promql": "up"}}})
    assert "up" in r["result"]["content"][0]["text"], "query_instant failed"

    # Shutdown
    rpc({"jsonrpc": "2.0", "id": 99, "method": "shutdown"})
    proc.wait(timeout=3)
    print("✅ MCP tools test passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())