#!/usr/bin/env python3
import datetime
import json
import os
import sys
import time
import urllib.parse
import urllib.request

KEEP = {"id", "surface", "name", "alias", "status", "lastActive", "open", "current", "archived", "display", "attention", "turnState", "turnCompletion", "endTurn", "project", "host", "checkout", "heldWork", "plan", "prs", "queueCount", "startedBy", "subagent", "delegated"}

BASE = os.environ.get("AGENTHAIL_API_URL", "http://127.0.0.1:7412").rstrip("/")


def token():
    if os.environ.get("AGENTHAIL_API_TOKEN"):
        return os.environ["AGENTHAIL_API_TOKEN"]
    with open(os.path.expanduser("~/.agenthail/dashboard.token")) as f:
        return f.read().strip()


TOKEN = token()
OUT = os.path.join(os.path.dirname(__file__), "..", "fixture", "data.json")


def get(path, timeout=30):
    req = urllib.request.Request(BASE + path, headers={"Authorization": f"Bearer {TOKEN}", "Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.load(r)


def enc(v):
    return urllib.parse.quote(v, safe="")


def transcript(sid, seconds=6.0):
    req = urllib.request.Request(
        f"{BASE}/api/v1/session-stream?id={enc(sid)}&after=0",
        headers={"Authorization": f"Bearer {TOKEN}", "Accept": "text/event-stream"},
    )
    rows, order, data = {}, [], []
    deadline = time.time() + seconds
    try:
        with urllib.request.urlopen(req, timeout=seconds) as r:
            for raw in r:
                if time.time() > deadline:
                    break
                line = raw.decode("utf-8", "replace").rstrip("\n")
                if line.startswith("data:"):
                    data.append(line[5:].lstrip(" "))
                    continue
                if line or not data:
                    continue
                try:
                    w = json.loads("\n".join(data))
                except (OSError, ValueError):
                    data = []
                    continue
                data = []
                if w.get("type") == "append":
                    it = rows.get(w.get("itemId"))
                    if it:
                        it["text"] = it.get("text", "") + (w.get("text") or "")
                    continue
                d = w.get("data") or {}
                if d.get("op") == "remove":
                    rows.pop(d.get("itemId"), None)
                elif w.get("item"):
                    it = w["item"]
                    if it["id"] not in rows:
                        order.append(it["id"])
                    rows[it["id"]] = it
    except (OSError, ValueError):
        pass
    return [rows[i] for i in order if i in rows]


def main():
    snap = get("/api/v1/snapshot")
    now = datetime.datetime.fromisoformat(snap["updatedAt"].replace("Z", "+00:00")[:26] + "+00:00")
    sessions = [s for s in snap["sessions"] if not s.get("archived")]

    def age_h(s):
        try:
            t = datetime.datetime.fromisoformat(s.get("lastActive", "")[:26] + "+00:00")
            return (now - t).total_seconds() / 3600
        except (OSError, ValueError):
            return 1e9

    projects = [p for p in snap.get("projects", []) if not p.get("archived")]
    live = {s["project"]["id"] for s in sessions if s.get("project") and age_h(s) < 72}
    ledgers = {}
    for p in projects:
        if p["id"] not in live:
            continue
        rows, cursor = [], None
        for _ in range(20):
            path = f"/api/v1/ledger?projectKey={enc(p['id'])}&view=work&limit=100"
            if cursor:
                path += f"&cursor={enc(cursor)}"
            try:
                v = get(path)
            except (OSError, ValueError):
                break
            rows += v.get("work") or []
            cursor = v.get("nextCursor")
            if not cursor:
                break
        ledgers[p["id"]] = rows
    requests = {}
    for s in sessions:
        if age_h(s) < 24:
            try:
                r = get(f"/api/v1/requests?sessionId={enc(s['id'])}").get("requests") or []
            except (OSError, ValueError):
                r = []
            if r:
                requests[s["id"]] = r
    recent = sorted([s for s in sessions if age_h(s) < 24], key=age_h)[:14]
    transcripts = {}
    for s in recent:
        items = transcript(s["id"])
        if items:
            transcripts[s["id"]] = items[-160:]
    cutoff = (now - datetime.timedelta(days=4)).isoformat()[:19]
    for key, rows in ledgers.items():
        recent_done = sorted((r for r in rows if r["state"] == "done"), key=lambda r: r["timeline"][-1]["at"] if r.get("timeline") else "")[-40:]
        ids = {r["id"] for r in recent_done}
        ledgers[key] = [r for r in rows if r["state"] not in ("done", "deleted") or r["id"] in ids or (r.get("timeline") and r["timeline"][-1]["at"] >= cutoff)]
    for items in transcripts.values():
        for it in items:
            if it.get("kind") in ("toolResult", "reasoning") and len(it.get("text", "")) > 1200:
                it["text"] = it["text"][:1200]
    keep = {k: snap[k] for k in ("updatedAt", "projects", "coordinators", "person", "counts")}
    keep["sessions"] = [{k: v for k, v in x.items() if k in KEEP} for x in sessions]
    keep["ledgers"] = ledgers
    keep["requests"] = requests
    keep["transcripts"] = transcripts
    keep["usage"] = [
        {"host": st.get("host"), "provider": m.get("provider"), "label": m.get("label"), "percent": m.get("percent"), "resetsAt": m.get("resetsAt"), "severity": m.get("severity"), "primary": st.get("primary", False), "name": st.get("name")}
        for st in get("/api/v1/usage").get("sets", [])
        for m in st.get("meters", [])
    ]
    hv = get("/api/v1/hosts")
    keep["hosts"] = [{"name": hv["host"]["name"], "state": hv["host"]["state"]}] + [{"name": x["name"], "state": x["state"], "sessionCount": x.get("sessionCount")} for x in hv.get("hosts", [])]
    os.makedirs(os.path.dirname(os.path.abspath(OUT)), exist_ok=True)
    with open(OUT, "w") as f:
        json.dump(keep, f)
    print(OUT, len(sessions), "sessions", len(ledgers), "ledgers", sum(map(len, ledgers.values())), "items", len(requests), "with requests", len(transcripts), "transcripts")



def add_transcripts(ids):
    with open(OUT) as f:
        data = json.load(f)
    for sid in ids:
        items = transcript(sid)
        for it in items:
            if it.get("kind") in ("toolResult", "reasoning") and len(it.get("text", "")) > 1200:
                it["text"] = it["text"][:1200]
        data.setdefault("transcripts", {})[sid] = items[-160:]
        print(sid, len(items), "items")
    with open(OUT, "w") as f:
        json.dump(data, f)


if len(sys.argv) > 2 and sys.argv[1] == "--add-transcript":
    add_transcripts(sys.argv[2:])
else:
    main()
