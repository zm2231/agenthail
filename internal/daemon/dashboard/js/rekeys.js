import { app } from "./state.js";
import { $ } from "./dom.js";

export function currentSessionID(id) {
  return app.rekeyed[id] || id;
}

export function applyRekeys(rekeyed) {
  app.rekeyed = rekeyed;
  for (const [retired, current] of Object.entries(rekeyed)) {
    if (!app.drafts.has(retired)) continue;
    if (!app.drafts.has(current)) app.drafts.set(current, app.drafts.get(retired));
    app.drafts.delete(retired);
  }
  if (app.voice.requestedTarget) app.voice.requestedTarget = currentSessionID(app.voice.requestedTarget);
  const target = $("#voice-target");
  if (target?.value.trim()) target.value = currentSessionID(target.value.trim());
}
