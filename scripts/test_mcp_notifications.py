#!/usr/bin/env python3
"""MCP Notifications Test - validates resources/subscribe + notifications/resources/updated.

The server may emit the notification *before* the tools/call response (the
mutation+notify happens inside handleToolsCall, before the result is returned).
So after a mutating call we drain messages until we see the notification.
"""
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

    def send(msg):
        data = json.dumps(msg, separators=(",", ":")).encode()
        proc.stdin.write(f"Content-Length: {len(data)}\r\n\r\n".encode() + data)
        proc.stdin.flush()

    def recv():
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
        return json.loads(proc.stdout.read(cl))

    def rpc(msg):
        send(msg)
        return recv()

    # 1. Initialize — verify subscribe capability is advertised.
    r = rpc({"jsonrpc": "2.0", "id": 1, "method": "initialize",
             "params": {"protocolVersion": "2024-11-05"}})
    assert r["result"]["capabilities"]["resources"].get("subscribe") is True, \
        "resources.subscribe capability not advertised"

    send({"jsonrpc": "2.0", "method": "notifications/initialized"})

    # 2. Subscribe to a list resource and a per-metric resource.
    r = rpc({"jsonrpc": "2.0", "id": 2, "method": "resources/subscribe",
             "params": {"uri": "promql://metrics/list"}})
    assert "result" in r, "subscribe failed"
    r = rpc({"jsonrpc": "2.0", "id": 3, "method": "resources/subscribe",
             "params": {"uri": "promql://metrics/http_requests_total"}})
    assert "result" in r, "subscribe to metric resource failed"

    # 3. Mutate storage via execute_line (.seed adds data).
    send({"jsonrpc": "2.0", "id": 4, "method": "tools/call",
          "params": {"name": "execute_line",
                     "arguments": {"line": ".seed http_requests_total 5 1m"}}})

    # 4. Drain messages until we see the notification (may precede the response).
    notif = None
    for _ in range(4):  # response + notification, at most a couple messages
        msg = recv()
        if not msg:
            break
        if msg.get("method") == "notifications/resources/updated":
            notif = msg
            break
        # otherwise it's the tools/call response — keep reading
        if msg.get("id") == 4:
            assert "result" in msg, f"execute_line failed: {msg}"

    assert notif is not None, "did not receive notifications/resources/updated"
    uris = notif["params"]["uris"]
    # The server notifies the list-level URIs so the client knows to re-list
    # resources; per-metric URIs are discovered via that re-list.
    assert "promql://metrics/list" in uris, f"promql://metrics/list not in {uris}"
    print(f"  Received notification for URIs: {uris}")

    # 5. Unsubscribe.
    r = rpc({"jsonrpc": "2.0", "id": 5, "method": "resources/unsubscribe",
             "params": {"uri": "promql://metrics/list"}})
    assert "result" in r, "unsubscribe failed"

    send({"jsonrpc": "2.0", "id": 99, "method": "shutdown"})
    proc.wait(timeout=3)

    print("✅ MCP notifications test passed")


if __name__ == "__main__":
    main()
