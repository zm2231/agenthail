import { escape } from "./format.js";

const names = { autoCompact: "Auto-compact", compactWindow: "Compact at", sessionCompactWindow: "This session" };

export function compactSettingsOf(history) {
  return Array.isArray(history?.compactSettings) ? history.compactSettings : [];
}

export function compactSettingNote(setting) {
  if (typeof setting.pending === "string") {
    const label = setting.options.find((option) => option.id === setting.pending)?.label ?? setting.pending;
    return `${label} from the next message`;
  }
  if (setting.applies === "newSessions") return setting.scope ? `For new sessions on ${setting.scope}` : "For new sessions";
  if (setting.applies === "nextTurn") return "From the next turn";
  if (setting.applies === "nextMessage") return "Restarts the session when idle";
  if (setting.applies === "nextLoad") return "Codex applies it the next time it loads this thread with no other app attached";
  return "";
}

function renderSetting(setting) {
  const name = names[setting.key] || setting.key;
  const known = setting.options.some((option) => option.id === setting.value);
  const current = known ? "" : `<option value="" selected disabled>Default</option>`;
  const options = setting.options
    .map((option) => `<option value="${escape(option.id)}"${option.id === setting.value ? " selected" : ""}>${escape(option.label)}</option>`)
    .join("");
  const note = compactSettingNote(setting);
  return `<label><span>${name}</span><select data-turn-setting="${escape(setting.key)}" aria-label="${name}">${current}${options}</select>${note ? `<small class="session-tool-note">${escape(note)}</small>` : ""}</label>`;
}

export function renderCompactSettings(history) {
  if (history?.readOnly) return "";
  const settings = compactSettingsOf(history);
  return settings.length ? `<div class="session-tools session-compact" data-tool="compact">${settings.map(renderSetting).join("")}</div>` : "";
}
