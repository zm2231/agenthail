import importlib.util
import json
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

spec = importlib.util.spec_from_file_location("layout_daemon", os.path.join(os.path.dirname(__file__), "layout-daemon.py"))
if spec is None or spec.loader is None:
    raise SystemExit("layout-daemon.py fixture is missing")
layout_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(layout_module)
layout = vars(layout_module)
RESPONSES = layout["RESPONSES"]
SESSION = layout["SESSION"]
TOKEN = "fixture-token"
OPERATOR = {"id": "fixture-operator", "surface": "codex", "name": "Agenthail orchestrator", "status": "idle", "lastActive": layout["NOW"]}
TARGETS = {"fixture": SESSION["id"], SESSION["id"]: SESSION["id"]}
PEER = """<!doctype html><html><body><script>
const notify = (type, value = "") => window.webkit.messageHandlers.voice.postMessage({type, value});
window.agenthailVoice = {
  start: async () => notify("offer", "v=0 fixture-offer"),
  end: () => {},
  mute: () => {},
  answer: async () => { notify("connection", "connected"); notify("channel", "open"); }
};
notify("ready");
</script></body></html>"""

lock = threading.Lock()
state = {"protocol": 1, "phase": "ready", "events": [], "truncated": False, "occupied": False}


def target(requested):
    if not requested:
        return None
    if requested not in TARGETS:
        raise ValueError(f'resolve voice target "{requested}" by alias or exact session ID: no such session')
    return {**SESSION, "id": TARGETS[requested], "name": SESSION["name"]}


def apply(action):
    name = action.get("action")
    if name == "select":
        if state["phase"] in ("starting", "negotiating", "connected"):
            raise ValueError("end the current audio call before selecting a different voice target")
        state["target"] = target(action.get("targetId", "").strip())
    elif name == "prepare":
        state["session"] = OPERATOR
    elif name == "start":
        state.update(phase="negotiating", attemptId=action["attemptId"], sdp="v=0 fixture-answer", audioProvider="openai-realtime-via-codex")
    elif name == "connected":
        state.update(phase="connected", sdp=None)
        state["events"] = [
            {"sequence": 1, "method": "thread/realtime/transcript/done", "params": {"role": "user", "text": "What is the builder working on?"}},
            {"sequence": 2, "method": "thread/realtime/transcript/done", "params": {"role": "assistant", "text": "The builder is running the fixture test suite."}},
        ]
    elif name == "transfer":
        if state["phase"] != "connected":
            raise ValueError("voice transfer requires the current connected call")
        state["target"] = target(action.get("targetId", "").strip())
    elif name == "target-interrupt":
        if not state.get("target"):
            raise ValueError("no selected session")
    elif name == "heartbeat":
        if state["phase"] not in ("starting", "negotiating", "connected", "stopping") or action.get("attemptId") != state.get("attemptId"):
            raise ValueError("heartbeat requires the current active voice call")
    elif name == "stop":
        state.update(phase="ended", sdp=None)
    else:
        raise ValueError(f"unsupported fixture action {name}")


class Handler(BaseHTTPRequestHandler):
    def authorized(self):
        if self.headers.get("Authorization") == f"Bearer {TOKEN}":
            return True
        self.reply(401, {"error": {"code": "device_required", "message": "Voice requires a paired device token with control permission."}})
        return False

    def reply(self, status, body, content_type="application/json"):
        data = body.encode() if isinstance(body, str) else json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path == "/api/v1/voice/peer":
            if self.authorized():
                self.reply(200, PEER, "text/html; charset=utf-8")
            return
        if path == "/api/v1/voice":
            if self.authorized():
                with lock:
                    self.reply(200, state)
            return
        body = RESPONSES.get(path)
        if body is None:
            self.send_error(404)
            return
        self.reply(200, body)

    def do_POST(self):
        if self.path.split("?", 1)[0] != "/api/v1/voice":
            self.send_error(404)
            return
        if not self.authorized():
            return
        action = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))) or b"{}")
        with lock:
            try:
                apply(action)
                self.reply(200, state)
            except ValueError as error:
                self.reply(409, {"error": {"code": "voice_blocked", "message": str(error)}, "state": state})

    def log_message(self, format, *args):
        print(format % args, file=sys.stderr, flush=True)


server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
print(server.server_address[1], flush=True)
sys.stdout.close()
server.serve_forever()
