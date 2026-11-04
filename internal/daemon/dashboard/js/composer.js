import { $ } from "./dom.js";
import { app } from "./state.js";
import { chosenHolder, renderHolderPicker, sharedHolders, withHolder } from "./holders.js";
import { action, logicalAction, sessionTarget } from "./actions.js";
import { configurePendingSends, enqueueSend } from "./pending-send.js";
import { bindSendStatus, renderSendStatus, returnToComposer } from "./send-status.js";
import { toast } from "./feedback.js";
import { load } from "./dashboard-state.js";
import { selectSession } from "./session-selection.js";
import { deliveryStatusLabel } from "./delivery-labels.js";
import { escape } from "./format.js";
import { rememberToolRow } from "./tool-view.js";

export function resizeComposer() {
  const input = $("#message");
  if (!input) return;
  const maxHeight = 160;
  input.style.height = "auto";
  const height = Math.min(input.scrollHeight, maxHeight);
  input.style.height = `${height}px`;
  input.style.overflowY = input.scrollHeight > maxHeight ? "auto" : "hidden";
}

export function syncComposerAction() {
  const input = $("#message");
  const sendButton = $("#send");
  const steerButton = $("#steer-message");
  const capabilities = app.history?.capabilities || {};
  const busy = app.selected?.status === "busy";
  const hasMessage = Boolean(input.value.trim());
  const readOnly = Boolean(app.history?.readOnly || app.selected?.readOnly);
  const turnNotice = (app.history ? capabilities.turnNotice : app.selected?.capabilities?.turnNotice) || "";
  const stopping = busy && capabilities.interrupt && !hasMessage && !readOnly;
  sendButton.dataset.mode = stopping ? "interrupt" : "send";
  sendButton.querySelector("span:first-child").textContent = stopping ? "Stop" : "Send";
  sendButton.querySelector("span:last-child").textContent = stopping ? "■" : "↑";
  sendButton.disabled = readOnly || (!hasMessage && !stopping);
  steerButton.hidden = !(busy && !app.selected?.waitingOn && capabilities.steer && hasMessage && !readOnly);
  steerButton.disabled = steerButton.hidden;
  $("#composer").classList.toggle("is-stopping", stopping);
  $("#composer").classList.toggle("has-steer", !steerButton.hidden);
  renderHolderPicker();
  renderSendStatus();
  if (readOnly) return;
  $("#composer-note").textContent = app.selected?.waitingOn
    ? "This agent is waiting on you. Messages queue until you answer it."
    : turnNotice
    ? turnNotice
    : busy
    ? hasMessage
      ? "Send queues this for next. Steer now changes the turn in progress."
      : "This agent is working. Stop ends the current turn."
    : "Ready to send.";
}

export async function send(requestedAction = "send") {
  const message = $("#message").value.trim();
  const composerAction = requestedAction === "send" ? $("#send").dataset.mode : requestedAction;
  if (composerAction === "interrupt") {
    $("#send").disabled = true;
    try {
      await action("interrupt");
      toast("Current turn stopped.");
      await load(true);
      await selectSession(app.selected.id);
    } catch (error) {
      toast(error.message);
    } finally {
      syncComposerAction();
    }
    return;
  }
  if (!message) return;
  $("#send").disabled = true;
  $("#steer-message").disabled = true;
  const sessionId = app.selected?.id;
  try {
    const [command, ...rest] = message.split(/\s+/);
    const argument = rest.join(" ");
    const commandActions = {
      "/compact": ["compact", {}],
      "/stop": ["interrupt", {}],
      "/steer": ["steer", { message: argument, author: "person" }],
      "/model": ["model", { model: argument }],
      "/goal": ["goal-set", { message: argument }],
      "/name": ["alias", { alias: argument }],
    };
    const commandAction = commandActions[command.toLowerCase()];
    if (commandAction && ["/steer", "/model", "/goal", "/name"].includes(command.toLowerCase()) && !argument)
      throw Error(`${command} needs a value`);
    const steering = composerAction === "steer" || command.toLowerCase() === "/steer";
    if (steering || !commandAction) {
      const payload = steering
        ? { message: composerAction === "steer" ? message : argument, author: "person" }
        : { message, author: "person", ...selectedTurnOptions() };
      if (!sessionId) throw Error("Choose a conversation first");
      const wire = { action: steering ? "steer" : "send", sessionId, ...withHolder(sessionId, payload) };
      if (enqueueSend(sessionId, wire, message)) {
        clearComposer(sessionId, message);
        return;
      }
      const unstored = await logicalAction(wire.action, wire.action, payload);
      clearComposer(sessionId, message);
      await sendAnswered({ sessionId, wire }, unstored);
      return;
    }
    const result = await action(commandAction[0], commandAction[1]);
    clearComposer(sessionId, message);
    toast(
      result?.result?.waitingOn || result?.result?.evidence === "transport_accepted" || result?.result?.evidence === "queued"
        ? result.result.detail
        : command?.toLowerCase() === "/model" && result?.result?.output
        ? result.result.output
        : `${command} requested.`,
    );
    await load();
    await selectSession(app.selected.id);
  } catch (error) {
    toast(refusalNotice(sessionId, error));
  } finally {
    syncComposerAction();
  }
}

function clearComposer(sessionId, message) {
  if (app.selected?.id === sessionId) {
    if ($("#message").value.trim() === message) $("#message").value = "";
    app.drafts.delete(sessionId);
  } else if (app.drafts.get(sessionId)?.trim() === message) {
    app.drafts.delete(sessionId);
  }
  resizeComposer();
  renderSlashMenu();
}

function refusalNotice(sessionId, error) {
  if (app.selected?.id !== sessionId) return error.message;
  const holders = sharedHolders(app.selected);
  return holders.length && !chosenHolder(sessionId) ? `Open in ${holders.length} terminals. Choose one above, then send.` : error.message;
}

async function sendAnswered(item, result) {
  const session = app.state?.sessions?.find((candidate) => candidate.id === item.sessionId) || app.selected;
  const target = sessionTarget(session);
  const status = deliveryStatusLabel(result);
  toast(
    result?.result?.waitingOn || result?.result?.evidence === "transport_accepted"
      ? result.result.detail
      : item.wire.action === "steer"
      ? "Current turn redirected."
      : result?.result?.notice
      ? `${status} to ${target}. ${result.result.notice}`
      : `${status} to ${target}.`,
  );
  await load();
  if (app.selected?.id === item.sessionId) await selectSession(item.sessionId);
}

function sendRefused(item, error) {
  returnToComposer(item);
  toast(refusalNotice(item.sessionId, error));
  syncComposerAction();
}

export function renderSlashMenu() {
  const menu = $("#slash-menu");
  const value = $("#message")?.value.trimStart() || "";
  const query = value.split(/\s/, 1)[0].toLowerCase();
  const choosingModel = value.toLowerCase().startsWith("/model ");
  const visible = value.startsWith("/") && !value.includes(" ");
  const commands = choosingModel
    ? (app.history?.models || []).map((model) => [model.id, model.displayName || model.id, model.id === (app.history?.pendingModel || app.selected?.pendingModel) ? "From next turn" : model.description || "", model.default])
    : visible
    ? app.slashCommands.filter(([command]) => command.startsWith(query))
    : [];
  menu.hidden = commands.length === 0;
  menu.innerHTML = commands
    .map(([command, description, detail, isDefault]) => choosingModel
      ? `<button type="button" data-model-option="${escape(command)}"><strong>${escape(description)}</strong><span>${escape(detail || (isDefault ? "Default" : command))}</span></button>`
      : `<button type="button" data-slash="${escape(command)}"><strong>${escape(command)}</strong><span>${escape(description)}</span></button>`)
    .join("");
}

export function selectedTurnOptions() {
  if (app.selected?.surface !== "codex") return {};
  const options = { effort: $("#turn-effort").value.trim(), mode: $("#turn-mode").value, serviceTier: $("#turn-tier").value };
  if ($("#turn-schema").value.trim()) options.outputSchema = JSON.parse($("#turn-schema").value);
  return options;
}

export function bindComposerEvents() {
  configurePendingSends({ answered: sendAnswered, refused: sendRefused, changed: renderSendStatus });
  bindSendStatus();
  $("#message").addEventListener("input", () => {
    resizeComposer();
    renderSlashMenu();
    syncComposerAction();
  });
  $("#steer-message").addEventListener("click", () => send("steer"));
  $("#chat-body").addEventListener("toggle", rememberToolRow, true);
  $("#composer").addEventListener("submit", (event) => {
    event.preventDefault();
    send();
  });
}
