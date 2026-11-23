import json
import sys

out = sys.argv[1]
turns = int(sys.argv[2]) if len(sys.argv) > 2 else 600
t0 = 1_000_000.0
seq = 0
lines = []


def emit(ts, payload):
    global seq
    seq += 1
    payload["seq"] = seq
    lines.append(f"{ts:.3f}\tdata: {json.dumps(payload)}")
    lines.append(f"{ts:.3f}\t")


def item(ts, iid, kind, role=None, title="", text="", call=None, when=None):
    body = {"id": iid, "kind": kind, "title": title, "text": text, "timestamp": when, "callId": call, "turnId": None, "truncated": False}
    if role:
        body["role"] = role
    emit(ts, {"type": "item", "data": {"itemId": iid, "op": "upsert", "kind": kind}, "item": body})


def iso(seconds):
    m, s = divmod(int(seconds), 60)
    h, m = divmod(m, 60)
    return f"2026-11-23T{10 + h % 12:02d}:{m:02d}:{s:02d}Z"


para = (
    "The daemon replays the journal from the last committed cursor, and a session that streams faster than the renderer keeps its tail in a growth row. "
    "Wrapped paragraphs must reflow when the window narrows without losing the reader's place."
)
for n in range(turns):
    base = n * 40
    item(t0, f"u{n}", "message", "user", text=f"Prompt {n}: check the transcript behaviour for case {n}.", when=iso(base))
    for k in range(3):
        item(t0, f"c{n}-{k}", "toolCall", "assistant", "Bash" if k != 1 else "Read", f"rg -n 'case {n}' src/file{k}.rs", f"call{n}-{k}", iso(base + 5 + k * 7))
        item(t0, f"r{n}-{k}", "toolResult", "user", "Bash", f"src/file{k}.rs:{n}: match", f"call{n}-{k}", iso(base + 8 + k * 7))
    reply = f"**Turn {n}.** " + " ".join([para] * (1 + n % 3))
    if n % 7 == 0:
        reply += f"\n\n```rust\nfn case_{n}() -> usize {{\n    {n}\n}}\n```\n\n- first point\n- second point"
    item(t0, f"a{n}", "message", "assistant", text=reply, when=iso(base + 30))
    emit(t0, {"type": "item", "data": {"itemId": f"d{n}", "op": "phase", "kind": "done"}})

live = turns * 40
t = t0 + 1.0
item(t, "u-live", "message", "user", text="Live prompt: stream a long answer while working.", when=iso(live))
for k in range(8):
    t = t0 + 1.0 + k * 1.0
    item(t, f"lc{k}", "toolCall", "assistant", "Bash", f"cargo test --package feed case_{k}", f"live{k}", iso(live + 5 + k * 10))
    item(t + 0.3, f"lr{k}", "toolResult", "user", "Bash", f"test result: ok. {k} passed", f"live{k}", iso(live + 8 + k * 10))
    if k == 4:
        item(t + 0.5, f"a{turns - 5}", "message", "assistant", text=f"**Turn {turns - 5}, edited.** " + " ".join([para] * 6), when=iso((turns - 5) * 40 + 30))
t = t0 + 10.0
item(t, "a-live", "message", "assistant", text="Streaming answer:", when=iso(live + 100))
held = len(b"Streaming answer:")
words = (para + " ").split(" ")
for i in range(400):
    ts = t + 0.05 * (i + 1)
    chunk = " " + words[i % len(words)]
    if i % 40 == 39:
        chunk = "\n\n" + words[i % len(words)]
    emit(ts, {"type": "append", "itemId": "a-live", "from": held, "text": chunk, "ts": iso(live + 100 + i // 20)})
    held += len(chunk.encode())
emit(t + 21.0, {"type": "item", "data": {"itemId": "d-live", "op": "phase", "kind": "done"}})

with open(out, "w") as f:
    f.write("\n".join(lines) + "\n")
print(f"{out}: {turns} turns, {seq} events, live phase 0..{21 + 10}s")
