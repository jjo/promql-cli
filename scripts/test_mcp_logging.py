#!/usr/bin/env python3
"""MCP Logging Test - validates logging/setLevel"""
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

    # Set log level to debug
    r = rpc({"jsonrpc": "2.0", "id": 2, "method": "logging/setLevel", "params": {"level": "debug"}})
    assert "result" in r, "logging/setLevel failed"

    rpc({"jsonrpc": "2.0", "id": 99, "method": "shutdown"})
    proc.wait(timeout=3)
    print("✅ MCP logging test passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())