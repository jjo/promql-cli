#!/usr/bin/env python3
"""MCP Streaming Test - validates execute_line with stream=true."""
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

    def send_msg(msg):
        """Send a JSON-RPC message."""
        data = json.dumps(msg, separators=(",", ":")).encode()
        proc.stdin.write(f"Content-Length: {len(data)}\r\n\r\n".encode() + data)
        proc.stdin.flush()

    def recv_msg():
        """Read a single JSON-RPC message from stdout."""
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
        if cl == 0:
            return {}
        raw = proc.stdout.read(cl)
        return json.loads(raw)

    def rpc(msg):
        """Send a request and return the response."""
        send_msg(msg)
        return recv_msg()

    # Initialize
    init = rpc({"jsonrpc": "2.0", "id": 1, "method": "initialize",
                "params": {"protocolVersion": "2024-11-05"}})
    caps = init["result"]["capabilities"]
    assert "executionStreaming" in caps, \
        f"executionStreaming capability missing from initialize: {caps}"
    print("✅ executionStreaming capability advertised")

    # Send notifications/initialized
    send_msg({"jsonrpc": "2.0", "method": "notifications/initialized"})

    # Test streaming mode: execute a simple line query with stream=true
    send_msg({"jsonrpc": "2.0", "id": 2, "method": "tools/call",
              "params": {"name": "execute_line",
                         "arguments": {"line": "up", "stream": True}}})

    # Read the notification first (server sends it before the response)
    notif = recv_msg()
    assert notif.get("method") == "notifications/execute_line_output", \
        f"expected notifications/execute_line_output, got {notif}"
    params = notif.get("params", {})
    assert params.get("is_final"), "expected is_final=true in notification"
    chunks = params.get("chunks", [])
    assert len(chunks) > 0, "expected at least one chunk"
    assert "text" in chunks[0], "chunk missing text field"
    print(f"✅ Received streaming notification: {chunks[0].get('text', '')[:100]}")

    # Read the tools/call response (should be empty)
    resp = recv_msg()
    assert resp.get("id") == 2, f"expected id 2, got {resp}"
    assert "result" in resp, "expected result in response"
    assert "content" not in resp.get("result", {}), \
        f"expected no content in streaming response, got {resp}"
    print("✅ Streaming response has no content (result delivered via notification)")

    # Test non-streaming fallback still works
    r = rpc({"jsonrpc": "2.0", "id": 3, "method": "tools/call",
             "params": {"name": "execute_line",
                        "arguments": {"line": "up"}}})
    assert r.get("id") == 3, f"expected id 3, got {r}"
    result = r.get("result", {})
    assert "content" in result, "expected content for non-streaming mode"
    print("✅ Non-streaming fallback works correctly")

    # Test tools/list shows stream parameter
    r = rpc({"jsonrpc": "2.0", "id": 4, "method": "tools/list"})
    tools = r["result"]["tools"]
    execute_line = None
    for t in tools:
        if t["name"] == "execute_line":
            execute_line = t
            break
    assert execute_line is not None, "execute_line tool not found"
    props = execute_line.get("inputSchema", {}).get("properties", {})
    assert "stream" in props, f"stream property missing from execute_line: {props}"
    print("✅ stream parameter present in execute_line input schema")

    # Shutdown
    send_msg({"jsonrpc": "2.0", "id": 99, "method": "shutdown"})
    proc.wait(timeout=3)
    print("✅ MCP streaming test passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
