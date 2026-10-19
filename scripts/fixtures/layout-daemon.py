import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

NOW = "2026-01-01T12:00:00Z"
CAPABILITIES = {"send": True, "stream": True, "reply": True, "goal": False, "compact": True, "model": True, "interrupt": True, "steer": True}
SESSION = {
    "id": "fixture-session",
    "surface": "claude",
    "name": "fixture-layout-session",
    "cwd": "/tmp/fixture-project",
    "alias": "fixture",
    "status": "busy",
    "lastActive": NOW,
    "queueCount": 0,
    "open": True,
    "current": True,
    "capabilities": CAPABILITIES,
    "transport": "uds",
    "project": {"id": "fixture-project", "displayName": "fixture-project", "rootPath": "/tmp/fixture-project", "kind": "git"},
    "recent": True,
    "checkout": {"id": "fixture-checkout", "path": "/tmp/fixture-project", "branch": "main", "dirty": False, "isMain": True},
    "observedAt": NOW,
    "runtime": {"focusable": False, "launcher": "terminal"},
    "freshness": {"generation": 1, "observedAt": NOW, "stale": False},
}
RESPONSES = {
    "/api/v1/version": {"protocol": 1, "minimumProtocol": 1, "maximumProtocol": 1, "authentication": "bearer", "eventTransport": "sse", "pushRelayUrl": ""},
    "/api/v1/snapshot": {
        "updatedAt": NOW, "eventCursor": 1, "hostEpoch": "fixture", "catalogSeq": 1,
        "daemon": {"pid": 1, "running": True},
        "surfaces": [{"name": "claude", "connected": True, "health": "healthy", "capabilities": CAPABILITIES}],
        "sessions": [SESSION], "totalSessions": 1,
        "queue": [], "channels": [], "relays": [], "history": [], "attention": [], "deliveryProblems": [],
        "codexRecentHours": 24, "busyDelivery": "queue",
    },
}


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = RESPONSES.get(self.path.split("?", 1)[0])
        if body is None:
            self.send_error(404)
            return
        data = json.dumps(body).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, format, *args):
        pass


if __name__ == "__main__":
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    print(server.server_address[1], flush=True)
    sys.stdout.close()
    server.serve_forever()
