import json
import os
import socketserver
import sys
import threading
import time
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs

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
ANSWER_REPLIES = {
    "stale": (409, {"ok": False, "error": {"code": "stale", "message": "this request was already answered or has expired"}}),
    "maybe": (502, {"ok": False, "error": {"code": "bad_gateway", "message": "the answer may have reached the session, so it is not sent again: delivery timed out"}}),
    "failed": (502, {"ok": False, "error": {"code": "bad_gateway", "message": "the session is not running"}}),
}
HELD_STREAMS = set()
STREAMS = {}
SCOPED = {}
ACTIONS_LOG = os.environ.get("AGENTHAIL_FIXTURE_ACTIONS_LOG", "")
REQUESTS_LOG = os.environ.get("AGENTHAIL_FIXTURE_REQUESTS_LOG", "")


class Server(ThreadingHTTPServer):
    # HTTPServer resolves the bound address's hostname, a reverse DNS lookup
    # that can stall for the resolver timeout; the fixture needs no name.
    def server_bind(self):
        socketserver.TCPServer.server_bind(self)
        host, self.server_port = self.server_address[:2]
        self.server_name = str(host)


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if REQUESTS_LOG:
            with open(REQUESTS_LOG, "a") as log:
                log.write(self.path + "\n")
        if self.path.split("?", 1)[0] in HELD_STREAMS:
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            self.wfile.write(b":" + b" " * 1024 + b"\n\n")
            self.wfile.flush()
            threading.Event().wait()
        if self.path.split("?", 1)[0] in STREAMS:
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            self.wfile.write(b":" + b" " * 1024 + b"\n\n")
            self.wfile.flush()
            try:
                STREAMS[self.path.split("?", 1)[0]](self.wfile)
            except (BrokenPipeError, ConnectionResetError):
                pass
            return
        path, _, query = self.path.partition("?")
        body = SCOPED.get(parse_qs(query).get("scope", [""])[0]) if path == "/api/v1/snapshot" else None
        body = body or RESPONSES.get(path)
        if body is None:
            if REQUESTS_LOG:
                with open(REQUESTS_LOG, "a") as log:
                    log.write("404 " + self.path + "\n")
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
        status, reply = 200, {"ok": True, "result": {"outcome": "answered"}}
        if SESSION.get("endTurn") and json.loads(body or b"{}").get("requestId") == SESSION["endTurn"].get("requestId"):
            status, reply = ANSWER_REPLIES.get(os.environ.get("AGENTHAIL_FIXTURE_ANSWER", ""), (status, reply))
            if status != 502 or "may have reached" in reply["error"]["message"]:
                SESSION["endTurn"]["answeredAt"] = NOW
                RESPONSES["/api/v1/requests"] = {"permissionMode": "", "requests": []}
                if "/api/v1/decisions" in RESPONSES:
                    RESPONSES["/api/v1/decisions"]["decisions"] = [item for item in RESPONSES["/api/v1/decisions"]["decisions"] if item["sessionId"] != SESSION["id"]]
        data = json.dumps(reply).encode()
        self.send_response(status)
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
    if sys.argv[1:2] in (["ask"], ["ask-several"], ["inbox"], ["tiles"]):
        # An idle session whose ended turn asks the person, from a provider that answers nothing through Agenthail.
        SESSION["status"] = "idle"
        SESSION["capabilities"] = {**CAPABILITIES, "respond": False}
        with open(os.path.join(os.path.dirname(__file__), ("ask" if sys.argv[1] in ("inbox", "tiles") else sys.argv[1]) + "-requests.json")) as requests:
            RESPONSES["/api/v1/requests"] = json.load(requests)
        ask = RESPONSES["/api/v1/requests"]["requests"][0]
        SESSION["endTurn"] = {"headline": ask["title"], "needs": "you", "endedAt": NOW, "requestId": ask["id"]}
        SESSION["attention"] = {"kind": "ask", "reason": ask["title"]}
        one = {"families": 1, "sessions": 1}
        RESPONSES["/api/v1/snapshot"]["view"] = {
            "counts": {"running": {"families": 0, "sessions": 0}, "current": one, "recent": one, "all": one, "archived": {"families": 0, "sessions": 0}},
            "surfaces": [],
            "projects": [{"id": SESSION["project"]["id"], "name": SESSION["project"]["displayName"], "counts": one, "collapsedFamilyIds": [SESSION["id"]]}],
            "families": [{"id": SESSION["id"], "rootId": SESSION["id"], "projectId": SESSION["project"]["id"], "checkoutId": SESSION["checkout"]["id"],
                          "members": [{"id": SESSION["id"], "depth": 0, "contextOnly": False, "pinned": False}], "pinned": False, "workingSubagents": 0}],
        }
        RESPONSES["/api/v1/history"] = {"items": [], "hasMore": False, "nextBefore": 0, "kinds": []}
        RESPONSES["/api/v1/projects/activity"] = {"projects": []}
        RESPONSES["/api/v1/session-options"] = {"surfaces": [{"id": "claude", "workspace": True}], "projects": [], "claudeConfigRoots": []}
        RESPONSES["/api/v1/usage"] = {"sets": []}
        RESPONSES["/api/v1/session-commands"] = {"commands": [], "unavailable": ""}
        RESPONSES["/api/v1/session-metadata"] = {}
        RESPONSES["/api/v1/prompts"] = {"prompts": [], "nextBefore": None}
        RESPONSES["/api/v1/models"] = {"models": []}
        HELD_STREAMS.update({"/api/v1/catalog-events", "/api/v1/session-stream"})
        RESPONSES["/api/v1/session"] = {
            "session": {k: SESSION[k] for k in ("id", "surface", "name", "status", "lastActive", "cwd")},
            "exchanges": [], "capabilities": SESSION["capabilities"], "readOnly": False, "readOnlyReason": "",
            "timeline": {"source": "claude", "truncated": False, "nextBefore": 0, "items": []},
        }
        theme = sys.argv[2] if len(sys.argv) > 2 else "system"
        RESPONSES["/api/v1/ui"] = {"rev": "", "problems": [], "chosen": {}, "overrides": {}, "values": {"theme": theme}}
        HELD_STREAMS.add("/api/v1/events")
    if sys.argv[1:2] == ["inbox"]:
        ask = RESPONSES["/api/v1/requests"]["requests"][0]
        RESPONSES["/api/v1/decisions"] = {
            "decisions": [
                {"group": "stopped", "sessionId": "fixture-stopped", "kind": "delivery", "reason": "A message to it was not delivered", "since": NOW, "answerable": False},
                {"group": "holding", "sessionId": SESSION["id"], "projectId": SESSION["project"]["id"], "kind": "ask", "reason": ask["title"], "since": NOW,
                 "answerable": True, "requests": RESPONSES["/api/v1/requests"]["requests"], "holdsUp": ["fixture-waiting"]},
            ],
            "leadHandling": [{"sessionId": "fixture-worker", "heldBy": SESSION["id"], "role": "worker", "reason": "Asks to run the migration", "since": NOW}],
        }
        local = {"id": "", "name": "this Mac", "state": "local"}
        RESPONSES["/api/v1/snapshot"]["projects"] = [
            {"id": SESSION["project"]["id"], "displayName": SESSION["project"]["displayName"], "kind": "git", "checkouts": [{"host": local, "projectId": SESSION["project"]["id"], "rootPath": SESSION["cwd"]}]},
            {"id": "release-notes", "displayName": "release-notes", "kind": "folder", "checkouts": [{"host": local, "projectId": "release-notes", "rootPath": "/tmp/release-notes"}],
             "coordinators": [{"name": "notes-lead", "surface": "claude"}]},
        ]
        RESPONSES["/api/v1/projects/activity"] = {"projects": [
            {"id": SESSION["project"]["id"], "lastActive": NOW, "sessionCount": 1, "checkouts": [{"projectId": SESSION["project"]["id"], "sessionCount": 1}]},
            {"id": "release-notes", "sessionCount": 0, "checkouts": []},
        ]}
    if sys.argv[1:2] == ["tiles"]:
        names = ["backend", "website", "monitor", "billing", "mobile1", "docs", "api", "search", "ledger", "android", "desktop", "payroll", "metrics", "support", "release"]
        count = int(sys.argv[2]) if len(sys.argv) > 2 else 3
        local = {"id": "", "name": "this Mac", "state": "local"}
        RESPONSES["/api/v1/snapshot"]["projects"] = [
            {"id": name, "displayName": name, "kind": "folder", "checkouts": [{"host": local, "projectId": name, "rootPath": "/tmp/" + name}]}
            for name in names[:count]
        ]
        RESPONSES["/api/v1/projects/activity"] = {"projects": [
            {"id": name, "lastActive": NOW, "sessionCount": count - index, "checkouts": [{"projectId": name, "sessionCount": count - index}]}
            for index, name in enumerate(names[:count])
        ]}
        theme = sys.argv[3] if len(sys.argv) > 3 else "system"
        RESPONSES["/api/v1/ui"] = {"rev": "", "problems": [], "chosen": {}, "overrides": {}, "values": {"theme": theme}}
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
    if sys.argv[1:2] == ["list"]:
        def listed(id, name, project, surface, status, minutes):
            return {**SESSION, "id": id, "name": name, "alias": None, "surface": surface, "status": status, "cwd": "/srv/" + project,
                    "lastActive": f"2026-01-01T{11 - minutes // 60:02d}:{59 - minutes % 60:02d}:00Z",
                    "project": {"id": project, "displayName": project, "rootPath": "/srv/" + project, "kind": "git"},
                    "checkout": {"id": project + "-main", "path": "/srv/" + project, "branch": "main", "dirty": False, "isMain": True}}

        def page(sessions):
            projects = list(dict.fromkeys(s["project"]["id"] for s in sessions))
            return {**RESPONSES["/api/v1/snapshot"], "sessions": sessions, "totalSessions": len(sessions), "view": {
                "counts": {scope: {"families": len(listing), "sessions": len(listing)} for scope, listing in scopes.items()}, "surfaces": [],
                "projects": [{"id": p, "name": p, "counts": {"families": len(ids), "sessions": len(ids)}, "collapsedFamilyIds": ids}
                             for p, ids in ((p, [s["id"] for s in sessions if s["project"]["id"] == p]) for p in projects)],
                "families": [{"id": s["id"], "rootId": s["id"], "projectId": s["project"]["id"], "checkoutId": s["checkout"]["id"],
                              "members": [{"id": s["id"], "depth": 0, "contextOnly": False, "pinned": False}], "pinned": False, "workingSubagents": 0}
                             for s in sessions]}}

        active = [listed("web-1", "Fix the login redirect", "web-app", "claude", "busy", 2), listed("docs-1", "Write the install guide", "docs-site", "codex", "idle", 9),
                  listed("web-2", "Add dark mode tokens", "web-app", "codex", "idle", 25), listed("docs-2", "Check broken links", "docs-site", "claude", "idle", 70)]
        archived = [listed("web-0", "Spike the old migration", "web-app", "codex", "idle", 300), listed("docs-0", "Draft the changelog", "docs-site", "claude", "idle", 400)]
        scopes = {"running": [s for s in active if s["status"] == "busy"], "current": active, "recent": active, "all": active, "archived": archived}
        local = {"id": "", "name": "this Mac", "state": "local"}
        RESPONSES["/api/v1/snapshot"]["sessions"] = active
        RESPONSES["/api/v1/snapshot"]["totalSessions"] = len(active)
        RESPONSES["/api/v1/snapshot"]["projects"] = [
            {"id": p, "displayName": p, "kind": "git", "checkouts": [{"host": local, "projectId": p, "rootPath": "/srv/" + p}]} for p in ("web-app", "docs-site")
        ]
        SCOPED.update({scope: page(listing) for scope, listing in scopes.items()})
        RESPONSES["/api/v1/projects/activity"] = {"projects": [
            {"id": p, "lastActive": max(s["lastActive"] for s in active if s["project"]["id"] == p), "sessionCount": 2, "checkouts": [{"projectId": p, "sessionCount": 2}]}
            for p in ("web-app", "docs-site")
        ]}
        RESPONSES["/api/v1/history"] = {"items": [], "hasMore": False, "nextBefore": 0, "kinds": []}
        RESPONSES["/api/v1/decisions"] = {"decisions": [], "leadHandling": []}
        RESPONSES["/api/v1/session-options"] = {"surfaces": [{"id": "claude", "workspace": True}], "projects": [], "claudeConfigRoots": []}
        RESPONSES["/api/v1/usage"] = {"sets": []}
        RESPONSES["/api/v1/models"] = {"models": []}
        theme = sys.argv[2] if len(sys.argv) > 2 else "system"
        RESPONSES["/api/v1/ui"] = {"rev": "", "problems": [], "chosen": {}, "overrides": {}, "values": {"theme": theme}}
        HELD_STREAMS.update({"/api/v1/events", "/api/v1/catalog-events"})
    if sys.argv[1:2] == ["labels"]:
        def labelled(id, name, **fields):
            return {**SESSION, "id": id, "name": name, "alias": None, "status": "idle", "cwd": "/srv/web-app",
                    "project": {"id": "web-app", "displayName": "web-app", "rootPath": "/srv/web-app", "kind": "git"},
                    "checkout": {"id": "web-app-main", "path": "/srv/web-app", "branch": "main", "dirty": False, "isMain": True}, **fields}

        sessions = [
            labelled("link", "https://github.com/acme/widgets/pull/12 review the change",
                     claudeConfigRoot={"name": "team", "root": "/srv/home/.team/.claude", "signedInAs": "dev@example.com"},
                     capabilities={**CAPABILITIES, "personMessage": "note", "remoteControl": True}),
            labelled("terminal", "Ship the release notes", status="busy", runtimeKind="claude-cli", waitingOn={"kind": "question", "id": "q1", "since": NOW}),
            labelled("limit", "Migrate the database", limitResume={"state": "waiting"}),
            labelled("stopped", "Fix the flaky test", turnStop={"turnId": "t1", "reason": "the model returned an error"}),
            labelled("background", "Watch the build", backgroundTasks={"running": True, "tasks": [{"id": "b1", "kind": "shell", "description": "npm run build"}], "source": "host"}),
            labelled("offline-host", "Sync the mirror", host={"id": "h0123456789ab", "name": "server", "state": "unreachable"}, unavailableReason="server is unreachable"),
            labelled("archived", "Spike the old migration", archived=True),
            labelled("plain", "Write the install guide"),
        ]
        count = {"families": len(sessions), "sessions": len(sessions)}
        scope_counts = {"running": sum(s["status"] == "busy" for s in sessions), "current": len(sessions), "recent": len(sessions), "all": len(sessions),
                        "archived": sum(bool(s.get("archived")) for s in sessions)}
        RESPONSES["/api/v1/snapshot"].update({"sessions": sessions, "totalSessions": len(sessions), "view": {
            "counts": {scope: {"families": n, "sessions": n} for scope, n in scope_counts.items()}, "surfaces": [],
            "projects": [{"id": "web-app", "name": "web-app", "counts": count, "collapsedFamilyIds": [s["id"] for s in sessions]}],
            "families": [{"id": s["id"], "rootId": s["id"], "projectId": "web-app", "checkoutId": "web-app-main",
                          "members": [{"id": s["id"], "depth": 0, "contextOnly": False, "pinned": False}], "pinned": False, "workingSubagents": 0} for s in sessions]}})
        RESPONSES["/api/v1/session"] = {
            "session": {k: sessions[0][k] for k in ("id", "surface", "name", "status", "lastActive", "cwd", "claudeConfigRoot")},
            "exchanges": [], "capabilities": CAPABILITIES, "readOnly": False, "readOnlyReason": "",
            "timeline": {"source": "claude", "truncated": False, "nextBefore": 0, "items": []},
        }
        for path, body in {"/api/v1/projects/activity": {"projects": []}, "/api/v1/history": {"items": [], "hasMore": False, "nextBefore": 0, "kinds": []},
                           "/api/v1/decisions": {"decisions": [
                               {"group": "stopped", "sessionId": "terminal", "projectId": "web-app", "kind": "request", "reason": "Answer in its terminal", "since": NOW,
                                "answerable": False, "requestError": "this session's requests are not answered through Agenthail"},
                               {"group": "stopped", "sessionId": "limit", "projectId": "web-app", "kind": "usageLimit", "reason": "Resumes when the limit lifts", "since": NOW, "answerable": False},
                               {"group": "stopped", "sessionId": "stopped", "projectId": "web-app", "kind": "stopped", "reason": "the model returned an error", "since": NOW, "answerable": False}],
                               "leadHandling": []}, "/api/v1/usage": {"sets": []}, "/api/v1/models": {"models": []},
                           "/api/v1/session-options": {"surfaces": [{"id": "claude", "workspace": True}], "projects": [], "claudeConfigRoots": []},
                           "/api/v1/session-commands": {"commands": [], "unavailable": ""}, "/api/v1/session-metadata": {},
                           "/api/v1/prompts": {"prompts": [], "nextBefore": None}, "/api/v1/requests": {"permissionMode": "", "requests": []}}.items():
            RESPONSES[path] = body
        RESPONSES["/api/v1/session"]["timeline"]["items"] = [
            {"id": "t1", "kind": "message", "role": "user", "title": "", "text": "Summarise the review", "timestamp": NOW, "truncated": False},
            {"id": "t2", "kind": "toolCall", "role": "assistant", "title": "Run git diff --stat", "text": "", "timestamp": NOW, "callId": "c1", "truncated": False},
            {"id": "t3", "kind": "review", "role": "assistant", "title": "Review summary", "text": "**2 findings**, both in `api/auth.ts`.", "timestamp": NOW, "truncated": False},
            {"id": "t4", "kind": "mystery", "role": "assistant", "title": "", "text": "", "timestamp": NOW, "truncated": False},
            {"id": "t5", "kind": "message", "role": "assistant", "title": "", "text": "The review found two issues in the auth handler.", "timestamp": NOW, "truncated": False},
        ]
        theme = sys.argv[2] if len(sys.argv) > 2 else "system"
        RESPONSES["/api/v1/ui"] = {"rev": "", "problems": [], "chosen": {}, "overrides": {}, "values": {"theme": theme}}
        HELD_STREAMS.update({"/api/v1/events", "/api/v1/catalog-events", "/api/v1/session-stream"})
    if sys.argv[1:2] == ["background"]:
        def ago(minutes):
            return (datetime.now(timezone.utc) - timedelta(minutes=minutes)).strftime("%Y-%m-%dT%H:%M:%SZ")

        def tasked(id, name, status, background):
            return {**SESSION, "id": id, "name": name, "alias": None, "status": status, "lastActive": ago(1), "backgroundTasks": background}

        sessions = [
            tasked("build", "Ship the release build", "idle", {"running": True, "source": "host", "tasks": [
                {"id": "b1", "kind": "shell", "description": "npm run build -- --production", "since": ago(12)},
                {"id": "a1", "kind": "agent", "description": "Review the migration diff", "since": ago(4)},
                {"id": "m1", "kind": "monitor", "description": "file watcher", "ambient": True, "since": ago(90)}]}),
            tasked("server", "Work on the landing page", "idle", {"running": False, "source": "host", "tasks": [
                {"id": "d1", "kind": "shell", "description": "npm run dev", "ambient": True, "since": ago(26 * 60)}]}),
        ]
        shown = next((s for s in sessions if s["id"] == (sys.argv[2] if len(sys.argv) > 2 else "build")), sessions[0])
        count = {"families": len(sessions), "sessions": len(sessions)}
        RESPONSES["/api/v1/snapshot"].update({"sessions": sessions, "totalSessions": len(sessions), "view": {
            "counts": {"running": {"families": 0, "sessions": 0}, "current": count, "recent": count, "all": count, "archived": {"families": 0, "sessions": 0}},
            "surfaces": [], "projects": [{"id": SESSION["project"]["id"], "name": SESSION["project"]["displayName"], "counts": count, "collapsedFamilyIds": [s["id"] for s in sessions]}],
            "families": [{"id": s["id"], "rootId": s["id"], "projectId": SESSION["project"]["id"], "checkoutId": SESSION["checkout"]["id"],
                          "members": [{"id": s["id"], "depth": 0, "contextOnly": False, "pinned": False}], "pinned": False, "workingSubagents": 0} for s in sessions]}})
        RESPONSES["/api/v1/session"] = {
            "session": {k: shown[k] for k in ("id", "surface", "name", "status", "lastActive", "cwd")},
            "exchanges": [], "capabilities": CAPABILITIES, "readOnly": False, "readOnlyReason": "",
            "timeline": {"source": "claude", "truncated": False, "nextBefore": 0, "items": []},
        }
        for path, body in {"/api/v1/projects/activity": {"projects": []}, "/api/v1/history": {"items": [], "hasMore": False, "nextBefore": 0, "kinds": []},
                           "/api/v1/decisions": {"decisions": [], "leadHandling": []}, "/api/v1/usage": {"sets": []}, "/api/v1/models": {"models": []},
                           "/api/v1/session-options": {"surfaces": [{"id": "claude", "workspace": True}], "projects": [], "claudeConfigRoots": []},
                           "/api/v1/session-commands": {"commands": [], "unavailable": ""}, "/api/v1/session-metadata": {},
                           "/api/v1/prompts": {"prompts": [], "nextBefore": None}, "/api/v1/requests": {"permissionMode": "", "requests": []}}.items():
            RESPONSES[path] = body
        theme = sys.argv[3] if len(sys.argv) > 3 else "system"
        RESPONSES["/api/v1/ui"] = {"rev": "", "problems": [], "chosen": {}, "overrides": {}, "values": {"theme": theme}}
        HELD_STREAMS.update({"/api/v1/events", "/api/v1/catalog-events", "/api/v1/session-stream"})
    if sys.argv[1:2] == ["upserts"]:
        count = int(sys.argv[2]) if len(sys.argv) > 2 else 40
        variant = sys.argv[3] if len(sys.argv) > 3 else "stable"
        interval = float(sys.argv[4]) if len(sys.argv) > 4 else 1.0
        transcript = int(sys.argv[5]) if len(sys.argv) > 5 else 0

        def row(id, status="idle"):
            return {**SESSION, "id": id, "name": f"Session {id}", "alias": None, "status": status, "lastActive": NOW, "placement": "p-" + id}

        rows = [row("shown"), row("hidden", "busy")] + [row(f"s{n:03d}") for n in range(count - 2)]
        listed = {"families": len(rows), "sessions": len(rows)}
        page = {**RESPONSES["/api/v1/snapshot"], "sessions": rows, "totalSessions": len(rows), "decisionsRev": "fixture-1", "view": {
            "counts": {scope: listed for scope in ("running", "current", "recent", "all")} | {"archived": {"families": 0, "sessions": 0}}, "surfaces": [],
            "projects": [{"id": SESSION["project"]["id"], "name": SESSION["project"]["displayName"], "counts": listed, "collapsedFamilyIds": [r["id"] for r in rows]}],
            "families": [{"id": r["id"], "rootId": r["id"], "projectId": SESSION["project"]["id"], "checkoutId": SESSION["checkout"]["id"],
                          "members": [{"id": r["id"], "depth": 0, "contextOnly": False, "pinned": False}], "pinned": False, "workingSubagents": 0} for r in rows]}}
        RESPONSES["/api/v1/snapshot"] = page
        SCOPED.update({scope: page for scope in ("current", "recent", "all", "running")})
        RESPONSES["/api/v1/session"] = {
            "session": {k: rows[0][k] for k in ("id", "surface", "name", "status", "lastActive", "cwd")},
            "exchanges": [], "capabilities": CAPABILITIES, "readOnly": False, "readOnlyReason": "",
            "timeline": {"source": "claude", "truncated": False, "nextBefore": 0, "items": [
                {"id": f"t{n}", "kind": kind, "role": role, "title": title, "text": text, "timestamp": NOW, "callId": f"c{n // 4}", "truncated": False}
                for n in range(transcript)
                for kind, role, title, text in [[
                    ("message", "user", "", f"Step {n}: check the parser handles nested lists and report what changed."),
                    ("toolCall", "assistant", f"Run go test ./internal/parse/... -run TestNested{n}", ""),
                    ("toolResult", "tool", "", "ok  \tgithub.com/example/parse\t0.42s\n" * 3),
                    ("message", "assistant", "", f"Done with step {n}. The **nested list** case now keeps its indentation, and `ParseBlock` returns early for empty input.\n\n- kept the old behaviour for flat lists\n- added a case for three levels"),
                ][n % 4]]]},
        }
        for path, body in {"/api/v1/projects/activity": {"projects": []}, "/api/v1/history": {"items": [], "hasMore": False, "nextBefore": 0, "kinds": []},
                           "/api/v1/decisions": {"decisions": [], "leadHandling": []}, "/api/v1/usage": {"sets": []}, "/api/v1/models": {"models": []},
                           "/api/v1/session-options": {"surfaces": [{"id": "claude", "workspace": True}], "projects": [], "claudeConfigRoots": []},
                           "/api/v1/session-commands": {"commands": [], "unavailable": ""}, "/api/v1/session-metadata": {},
                           "/api/v1/prompts": {"prompts": [], "nextBefore": None}, "/api/v1/requests": {"permissionMode": "", "requests": []}}.items():
            RESPONSES[path] = body
        RESPONSES["/api/v1/ui"] = {"rev": "", "problems": [], "chosen": {}, "overrides": {}, "values": {"theme": "dark"}}
        HELD_STREAMS.add("/api/v1/session-stream")
        upsert_log = os.environ.get("AGENTHAIL_FIXTURE_UPSERT_LOG", "")

        def catalog(out):
            seq = 1
            while True:
                time.sleep(interval)
                seq += 1
                hidden = {**rows[1], "lastActive": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")}
                if variant == "moved":
                    hidden["placement"] = f"p-hidden-{seq % 2}"
                event = {"stream": "catalog", "seq": seq, "type": "session.upserted", "data": {"session": hidden}}
                out.write(b"data: " + json.dumps(event).encode() + b"\n\n")
                out.flush()
                if upsert_log:
                    with open(upsert_log, "a") as log:
                        log.write(f"{time.time():.3f} {seq}\n")

        def events(out):
            seq = 1
            while True:
                time.sleep(interval)
                seq += 1
                event = {"id": seq, "type": "session.updated", "timestamp": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "entityId": "hidden",
                         "data": {"status": "busy", "activeTurnId": "turn-1"}}
                out.write(b"data: " + json.dumps(event).encode() + b"\n\n")
                out.flush()

        STREAMS.update({"/api/v1/catalog-events": catalog, "/api/v1/events": events})
    server = Server(("127.0.0.1", 0), Handler)
    print(server.server_address[1], flush=True)
    sys.stdout.close()
    server.serve_forever()
