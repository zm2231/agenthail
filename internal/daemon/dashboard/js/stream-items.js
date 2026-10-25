export function applyStreamedRow(items, envelope) {
  const row = envelope.data;
  const rest = items.filter((value) => value.id !== row.itemId);
  if (row.op === "remove") return rest;
  const item = envelope.item || { id: row.itemId, kind: row.kind, role: row.role, origin: row.origin, sender: row.sender, title: row.title || row.kind, text: row.body || "", timestamp: row.ts, status: row.status, resetsAt: row.resetsAt, truncated: row.truncated, bodyRef: row.bodyRef, attachment: row.attachment, files: row.files, callId: row.callId, turnId: row.turnId };
  return [...rest, item];
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
