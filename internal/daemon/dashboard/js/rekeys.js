import { app } from "./state.js";
import { $ } from "./dom.js";
import { movePendingSends } from "./pending-send.js";
import { moveHeldAttachments } from "./composer-attachments.js";

export function currentSessionID(id) {
  return app.rekeyed[id] || id;
}

export function applyRekeys(rekeyed) {
  app.rekeyed = rekeyed;
  movePendingSends(rekeyed);
  moveHeldAttachments(rekeyed);
  for (const [retired, current] of Object.entries(rekeyed)) {
    if (!app.drafts.has(retired)) continue;
    const moving = app.drafts.get(retired);
    const existing = app.drafts.get(current);
    const text = existing && existing !== moving ? `${existing}\n\n${moving}` : moving;
    if (app.drafts.set(current, text) !== false) app.drafts.delete(retired);
  }
  if (app.voice.requestedTarget) app.voice.requestedTarget = currentSessionID(app.voice.requestedTarget);
  const target = $("#voice-target");
  if (target?.value.trim()) target.value = currentSessionID(target.value.trim());
}
