import json
import os
import socketserver
import sys
import threading
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
HELD_STREAMS = set()
ACTIONS_LOG = os.environ.get("AGENTHAIL_FIXTURE_ACTIONS_LOG", "")


class Server(ThreadingHTTPServer):
    # HTTPServer resolves the bound address's hostname, a reverse DNS lookup
    # that can stall for the resolver timeout; the fixture needs no name.
    def server_bind(self):
        socketserver.TCPServer.server_bind(self)
        host, self.server_port = self.server_address[:2]
        self.server_name = str(host)


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.split("?", 1)[0] in HELD_STREAMS:
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            self.wfile.write(b":" + b" " * 1024 + b"\n\n")
            self.wfile.flush()
            threading.Event().wait()
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

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        if ACTIONS_LOG and self.path.split("?", 1)[0] == "/api/v1/actions":
            with open(ACTIONS_LOG, "ab") as log:
                log.write(body + b"\n")
        data = json.dumps({"ok": True, "result": {"outcome": "answered"}}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, format, *args):
        pass


if __name__ == "__main__":
    if sys.argv[1:] in (["waiting"], ["waiting-queued"]):
        # A terminal session waiting on a question Agenthail can't answer.
        SESSION["waitingOn"] = {"kind": "question", "id": "fixture-session:question:1", "since": NOW}
        SESSION["runtime"] = {"focusable": True, "launcher": "cmux", "hostName": "cmux"}
    if sys.argv[1:] == ["waiting-queued"]:
        # Messages sent to a waiting session hold until it is answered, and
        # fill the composer's docks above the card.
        message = "Once you have an answer, rebase the release branch, rerun the full suite and post what changed. " * 3
        RESPONSES["/api/v1/snapshot"]["queue"] = [
            {"id": n, "sessionId": SESSION["id"], "target": "@fixture", "message": message, "status": "pending", "attempts": 0, "queuedAt": NOW, "evidence": "queued", "operation": "message"}
            for n in range(1, 9)
        ]
        RESPONSES["/api/v1/snapshot"]["deliveryProblems"] = [
            {"deliveryId": 1, "sessionId": SESSION["id"], "message": message, "reason": "session_waiting_on_you", "status": "failed", "at": NOW}
        ]
    if sys.argv[1:2] == ["questions"]:
        SESSION["waitingOn"] = {"kind": "question", "id": "fixture-questions", "since": NOW, "answerable": True}
        SESSION["capabilities"] = {**CAPABILITIES, "respond": True}
        with open(os.path.join(os.path.dirname(__file__), "question-pager-requests.json")) as requests:
            RESPONSES["/api/v1/requests"] = json.load(requests)
        RESPONSES["/api/v1/session"] = {
            "session": {k: SESSION[k] for k in ("id", "surface", "name", "status", "lastActive", "cwd", "waitingOn")},
            "exchanges": [], "capabilities": SESSION["capabilities"], "readOnly": False, "readOnlyReason": "",
            "timeline": {"source": "claude", "truncated": False, "nextBefore": 0, "items": []},
        }
        theme = sys.argv[2] if len(sys.argv) > 2 else "system"
        RESPONSES["/api/v1/ui"] = {"rev": "", "problems": [], "chosen": {}, "overrides": {}, "values": {"theme": theme}}
        HELD_STREAMS.add("/api/v1/events")
    if sys.argv[1:2] == ["agents"]:
        # An idle session whose transcript holds agent messages, sends and a fold, in the given theme.
        SESSION["status"] = "idle"
        with open(os.path.join(os.path.dirname(__file__), "agent-messages-timeline.json")) as timeline:
            items = json.load(timeline)
        RESPONSES["/api/v1/session"] = {
            "session": {k: SESSION[k] for k in ("id", "surface", "name", "status", "lastActive", "cwd")},
            "exchanges": [], "capabilities": CAPABILITIES, "readOnly": False, "readOnlyReason": "",
            "timeline": {"source": "claude", "truncated": False, "nextBefore": 0, "items": items},
        }
        theme = sys.argv[2] if len(sys.argv) > 2 else "system"
        RESPONSES["/api/v1/ui"] = {"rev": "", "problems": [], "chosen": {}, "overrides": {}, "values": {"theme": theme}}
        HELD_STREAMS.add("/api/v1/events")
    server = Server(("127.0.0.1", 0), Handler)
    print(server.server_address[1], flush=True)
    sys.stdout.close()
    server.serve_forever()
