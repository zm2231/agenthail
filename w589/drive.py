import json, os, random, subprocess, sys, time, urllib.request, uuid, glob, datetime

control = os.environ["AGENTHAIL_LIVE_CONTROL"].rstrip("/")
binary, app, out, bundle = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
seconds = float(os.environ.get("W589_SECONDS", "60"))
rng = random.Random(589)
log = open(os.path.join(out, "drive.log"), "a")

def note(*parts):
    print(time.strftime("%H:%M:%S"), *parts, file=log, flush=True)

def call(method, path, body=None):
    request = urllib.request.Request(control + path, method=method, data=json.dumps(body).encode() if body is not None else None, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)

host = call("GET", "/hosts")[0]
endpoint = host["endpoint"]
port = endpoint.rsplit(":", 1)[1]
root = None
for config in glob.glob(os.path.join(os.environ.get("W589_BASE", "/tmp"), "h0.*", "home", ".agenthail", "dashboard.json")):
    if json.load(open(config)).get("listen", "").endswith(":" + port):
        root = config.split("/home/.agenthail/")[0]
if root is None:
    sys.exit("could not find host 0's scratch root")
home = os.path.join(root, "home")
token = open(os.path.join(home, ".agenthail", "dashboard.token")).read().strip()
session = call("POST", "/hosts/0/turn/start", {"text": "Work through the W528 roles build and keep me posted."})["session"]
transcript = glob.glob(os.path.join(home, ".claude", "projects", "*", session + ".jsonl"))[0]
cwd = json.loads(open(transcript).readline())["cwd"]
note("session", session, "transcript", transcript)
clock = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(hours=6)

def stamp(step=1.0):
    global clock
    clock += datetime.timedelta(seconds=step)
    return clock.isoformat().replace("+00:00", "Z")

def write(*records):
    with open(transcript, "a") as f:
        for r in records:
            r.setdefault("uuid", str(uuid.uuid4()))
            r.setdefault("sessionId", session)
            r.setdefault("cwd", cwd)
            r.setdefault("timestamp", stamp())
            f.write(json.dumps(r) + "\n")

WORDS = "the worker held the run while the machine was busy and the gate came back green on the second pass after the flaky check was fixed".split()

def prose(sentences):
    return " ".join(" ".join(rng.choice(WORDS) for _ in range(rng.randint(8, 22))).capitalize() + "." for _ in range(sentences))

def assistant(text):
    write({"type": "assistant", "message": {"id": "msg-" + str(uuid.uuid4()), "role": "assistant", "model": "claude-sonnet-5-5", "content": [{"type": "text", "text": text}]}})

def tool(name, payload, output):
    use = "toolu_" + uuid.uuid4().hex[:20]
    write({"type": "assistant", "message": {"id": "msg-" + str(uuid.uuid4()), "role": "assistant", "model": "claude-sonnet-5-5", "content": [{"type": "tool_use", "id": use, "name": name, "input": payload}]}},
          {"type": "user", "message": {"role": "user", "content": [{"type": "tool_result", "tool_use_id": use, "content": output}]}})

def bash():
    lines = "\n".join("line %d of output: %s" % (i, prose(1)) for i in range(rng.randint(1, 18)))
    tool("Bash", {"command": "go test ./internal/... -run Test%d" % rng.randint(1, 999), "description": "Run tests"}, lines)

def peer_in():
    write({"type": "user", "message": {"role": "user", "content": '<cross-session-message from="uds:/tmp/cc-socks/1.sock" from-session="%s" from-name="agenthail/claude: forge-agents::@roles">\n%s\n</cross-session-message>' % (uuid.uuid4(), prose(rng.randint(1, 3)))}})

def peer_out():
    tool("mcp__agenthail__agenthail_send", {"target": "@roles", "message": prose(rng.randint(1, 2))}, '{"ok":true,"result":{"status":"sent"}}')

def person():
    write({"type": "user", "message": {"role": "user", "content": prose(rng.randint(1, 4))}})

def duration():
    write({"type": "system", "subtype": "turn_duration", "durationMs": rng.randint(4000, 400000)})

turns = int(os.environ.get("W589_HISTORY_TURNS", "160"))
for t in range(turns):
    person()
    for _ in range(rng.randint(0, 6)):
        bash()
    if rng.random() < 0.3:
        peer_out()
    assistant(prose(rng.randint(1, 8)))
    duration()
    if rng.random() < 0.3:
        peer_in()
note("history", turns, "turns")
open(os.path.join(out, "session.txt"), "w").write(session + "\n" + endpoint + "\n")
# Signal run.sh that the fixture is ready, then run the live turn.
open(os.path.join(out, "fixture.ready"), "w").write("ready\n")
while not os.path.exists(os.path.join(out, "capture.started")):
    time.sleep(0.2)

def cli(*args):
    env = {"HOME": home, "CFFIXED_USER_HOME": home, "PATH": "/usr/bin:/bin", "TMPDIR": os.path.join(root, "tmp") + "/", "XDG_RUNTIME_DIR": os.path.join(root, "run"),
           "CLAUDE_CONFIG_DIR": os.path.join(home, ".claude"), "CODEX_HOME": os.path.join(home, ".codex"), "AGENTHAIL_EXTENSION_PORTS": "off"}
    result = subprocess.run([binary, *args], env=env, capture_output=True, text=True, timeout=60)
    note("cli", args[0], result.returncode, (result.stdout + result.stderr).strip()[:300])

start = time.time()
queued = [10, 35]
while time.time() - start < seconds:
    choice = rng.randrange(12)
    if choice < 4:
        bash()
    elif choice == 4:
        peer_in()
    elif choice == 5:
        peer_out()
    elif choice in (6, 7):
        assistant(prose(rng.randint(1, 5)))
    elif choice == 8:
        tool("Read", {"file_path": "/work/native/macOS/TranscriptFollow.swift"}, prose(3))
    else:
        bash()
    if queued and time.time() - start > queued[0]:
        queued.pop(0)
        cli("send", session, "W528 step 1 design (queued while working): " + prose(1))
    time.sleep(rng.uniform(0.1, 0.4))
note("live turn done")
open(os.path.join(out, "drive.done"), "w").write("done\n")
