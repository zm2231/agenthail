export function applyStreamedRow(items, envelope) {
  const row = envelope.data;
  const rest = items.filter((value) => value.id !== row.itemId);
  if (row.op === "remove") return rest;
  const item = envelope.item || { id: row.itemId, kind: row.kind, role: row.role, origin: row.origin, sender: row.sender, title: row.title || row.kind, text: row.body || "", timestamp: row.ts, status: row.status, resetsAt: row.resetsAt, truncated: row.truncated, bodyRef: row.bodyRef, attachment: row.attachment, files: row.files, callId: row.callId, turnId: row.turnId };
  return [...rest, item];
}

function utf8Length(text) {
  let bytes = 0;
  for (let index = 0; index < text.length; index++) {
    const unit = text.charCodeAt(index);
    if (unit < 0x80) bytes += 1;
    else if (unit < 0x800) bytes += 2;
    else if (unit >= 0xd800 && unit < 0xdc00 && index + 1 < text.length) { bytes += 4; index++; }
    else bytes += 3;
  }
  return bytes;
}

// An append's from counts the UTF-8 bytes this connection was sent for the
// row, so a null result means the copy diverged and the stream must reopen.
export function streamRebuild() {
  const rows = new Map();
  return (envelope) => {
    if (envelope.type === "append") {
      const held = rows.get(envelope.itemId);
      if (!held || held.bytes !== envelope.from) return null;
      const body = (held.data.body || "") + envelope.text;
      const data = { ...held.data, body, version: envelope.version, ts: envelope.ts, final: Boolean(envelope.final), truncated: false };
      const item = held.item && { ...held.item, text: body, timestamp: envelope.ts, truncated: false };
      rows.set(envelope.itemId, { data, item, bytes: held.bytes + utf8Length(envelope.text) });
      return { stream: envelope.stream, sessionId: envelope.sessionId, seq: envelope.seq, type: "item", data, item };
    }
    const row = envelope.data || {};
    if (row.itemId && row.op === "remove") rows.delete(row.itemId);
    else if (row.itemId) rows.set(row.itemId, { data: row, item: envelope.item, bytes: utf8Length(row.body || "") });
    return envelope;
  };
}

export function promptOrigin(value) {
  if (value.role === "peer") return "peer";
  return value.origin || "";
}

export function exchangesFromItems(items) {
  const exchanges = [];
  let turn = "";
  let open = false;
  for (const value of items) {
    if (value.kind === "done") {
      if (open && (!value.turnId || value.turnId === turn)) open = false;
      continue;
    }
    if (!value.text) continue;
    const prompt = (["message", "text"].includes(value.kind) && ["user", "peer"].includes(value.role)) || (value.kind === "event" && Boolean(value.origin));
    if (prompt) {
      if (open && turn && value.turnId === turn) {
        const last = exchanges.at(-1);
        if (!last.user) Object.assign(last, { user: value.text, origin: promptOrigin(value), sender: value.sender || "" });
        else if (last.origin === promptOrigin(value) && last.sender === (value.sender || "")) last.user += `\n\n${value.text}`;
        continue;
      }
      exchanges.push({ user: value.text, origin: promptOrigin(value), sender: value.sender || "", timestamp: value.timestamp });
      turn = value.turnId || "";
      open = true;
      continue;
    }
    const reply = (["message", "assistant"].includes(value.kind) && value.role === "assistant") || (value.kind === "text" && (!value.role || value.role === "assistant"));
    if (!reply) continue;
    if (!open || (turn && value.turnId && value.turnId !== turn)) {
      exchanges.push({ timestamp: value.timestamp });
      turn = value.turnId || "";
      open = true;
    }
    if (!turn) turn = value.turnId || "";
    exchanges.at(-1).assistant = value.text;
  }
  return exchanges;
}
