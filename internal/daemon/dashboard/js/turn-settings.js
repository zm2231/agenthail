import { $ } from "./dom.js";
import { app } from "./state.js";
import { escape } from "./format.js";
import { action } from "./actions.js";
import { friendlyError, toast } from "./feedback.js";
import { loadSessionMetadata } from "./session-metadata.js";
import { compactSettingsOf } from "./compact-settings.js";

const shown = [["model", "Model"], ["effort", "Effort"], ["mode", "Mode"]];

function settingsOf(history) {
  return Array.isArray(history?.turnSettings) ? history.turnSettings : [];
}

function changeableOf(history) {
  return [...settingsOf(history), ...compactSettingsOf(history)];
}

function renderSetting(setting, name) {
  const known = setting.options.some((option) => option.id === setting.value);
  const current = known ? "" : `<option value="${escape(setting.value)}" selected disabled>${escape(setting.value || "Default")}</option>`;
  const options = setting.options
    .map((option) => `<option value="${escape(option.id)}"${option.id === setting.value ? " selected" : ""}>${escape(option.label)}</option>`)
    .join("");
  const hint = setting.applies === "nextTurn" ? `<span class="turn-setting-hint">next turn</span>` : "";
  return `<label class="turn-setting"><span>${name}</span><select data-turn-setting="${escape(setting.key)}" aria-label="${name}">${current}${options}</select>${hint}</label>`;
}

export function renderTurnSettings() {
  const box = $("#turn-settings");
  const settings = app.selected && !app.history?.readOnly ? settingsOf(app.history) : [];
  box.innerHTML = shown
    .map(([key, name]) => {
      const setting = settings.find((candidate) => candidate.key === key);
      return setting ? renderSetting(setting, name) : "";
    })
    .join("");
  box.hidden = box.innerHTML === "";
}

export async function changeTurnSetting(key, value) {
  const detail = app.history;
  const sessionId = app.selected?.id;
  const setting = changeableOf(detail).find((candidate) => candidate.key === key);
  const option = setting?.options.find((candidate) => candidate.id === value);
  if (!sessionId || !option || setting.value === value) return false;
  const restore = () => {
    renderTurnSettings();
    for (const select of $("#chat-body")?.querySelectorAll(`[data-turn-setting="${key}"]`) ?? []) select.value = setting.value;
  };
  if (option.confirm && !window.confirm(`Switch to ${option.label}? The agent will run tools without asking you first.`)) {
    restore();
    return false;
  }
  try {
    await action("turn-setting", { setting: key, value });
  } catch (error) {
    toast(friendlyError(error));
    restore();
    return false;
  }
  if (app.selected?.id === sessionId && app.history === detail) loadSessionMetadata(sessionId, detail);
  return true;
}

export function bindTurnSettingEvents() {
  for (const box of [$("#turn-settings"), $("#chat-body")].filter(Boolean)) {
    box.addEventListener("change", (event) => {
      const select = event.target.closest("[data-turn-setting]");
      if (select) changeTurnSetting(select.dataset.turnSetting, select.value);
    });
  }
}
