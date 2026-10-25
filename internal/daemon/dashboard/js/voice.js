import { app } from "./state.js";
import { $ } from "./dom.js";
import { ensureVoicePeer, voiceCallActive, voiceRequest } from "./voice-transport.js";
import { toast } from "./feedback.js";
import { currentSessionID } from "./rekeys.js";

export function renderVoice() {
  const state = app.voice.state;
  const call = $("#voice-call"), transfer = $("#voice-transfer"), stopTarget = $("#voice-stop-target"), hangup = $("#voice-hangup"), status = $("#voice-status"), target = $("#voice-target"), help = $("#voice-help");
  if (!call || !state) return;
  const routed = state.target?.name || "Agenthail orchestrator";
  const active = voiceCallActive();
  status.textContent = state.message || (active ? `Call active with ${routed}.` : `Ready to talk to ${routed}.`);
  if (app.voice.requestedTarget !== null) target.value = app.voice.requestedTarget;
  else if (state.target) target.value = state.target.id;
  call.hidden = active;
  transfer.hidden = !active;
  stopTarget.hidden = !active || !state.target;
  hangup.hidden = !active;
  transfer.textContent = target.value.trim() ? "Transfer to session" : "Return to orchestrator";
  help.textContent = target.value.trim() ? "This routes work only to that exact existing session." : "Leave blank to use Agenthail’s normal orchestration workflow.";
  $("#voice-title").textContent = state.target ? "Session voice" : "Talk to orchestrator";
}

export async function openVoice(targetID = "") {
  const dialog = $("#voice-dialog");
  app.voice.requestedTarget = targetID;
  $("#voice-target").value = targetID;
  if (!dialog.open) dialog.showModal();
  if (!app.voice.poll) app.voice.poll = setInterval(() => voiceRequest().catch(() => {}), 1000);
  try {
    await voiceRequest();
    if (targetID && !voiceCallActive()) renderVoice();
  } catch (error) { toast(error.message); }
}

export async function startVoice() {
  const targetID = currentSessionID($("#voice-target").value.trim());
  try {
    await voiceRequest("select", { targetId: targetID });
    app.voice.requestedTarget = null;
    await voiceRequest("prepare");
    await ensureVoicePeer();
    app.voice.attemptId = crypto.randomUUID();
    app.voice.localAudio = true;
    app.voice.connection = false;
    app.voice.channel = false;
    app.voice.appliedSDP = "";
    await window.agenthailVoice.start();
  } catch (error) {
    app.voice.localAudio = false;
    toast(error.message);
  }
}

export async function transferVoice() {
  try { await voiceRequest("transfer", { targetId: currentSessionID($("#voice-target").value.trim()) }); }
  catch (error) { toast(error.message); }
}

export async function interruptVoiceTarget() {
  const state = app.voice.state;
  if (!state?.target || !voiceCallActive()) return;
  if (!window.confirm(`Stop the confirmed active turn in ${state.target.name}? Audio stays connected.`)) return;
  try { await voiceRequest("target-interrupt", { attemptId: app.voice.attemptId || state.attemptId }); }
  catch (error) { toast(error.message); }
}

export async function hangupVoice() {
  const attemptId = app.voice.attemptId || app.voice.state?.attemptId;
  window.agenthailVoice?.end();
  app.voice.localAudio = false;
  if (!attemptId) return;
  try { await voiceRequest("stop", { attemptId }); }
  catch (error) { toast(error.message); }
}

export function bindVoiceEvents() {
  window.agenthailVoiceNotify = async ({ type, value }) => {
    if (type === "ready") return;
    if (type === "offer") {
      try {
        await voiceRequest("start", { attemptId: app.voice.attemptId, sdp: value });
      } catch (error) { toast(error.message); await hangupVoice(); }
      return;
    }
    if (type === "connection") app.voice.connection = value === "connected";
    if (type === "channel") app.voice.channel = value === "open";
    if (app.voice.connection && app.voice.channel && app.voice.attemptId) {
      try { await voiceRequest("connected", { attemptId: app.voice.attemptId }); }
      catch (error) { toast(error.message); }
      app.voice.channel = false;
    }
    if (type === "error") { toast(value || "Voice audio failed."); await hangupVoice(); }
  };
  $("#voice-orchestrator").addEventListener("click", () => openVoice());
  $("#voice-call").addEventListener("click", startVoice);
  $("#voice-transfer").addEventListener("click", transferVoice);
  $("#voice-stop-target").addEventListener("click", interruptVoiceTarget);
  $("#voice-hangup").addEventListener("click", hangupVoice);
  $("#voice-dialog").addEventListener("close", () => {
    if (app.voice.localAudio) hangupVoice();
    clearInterval(app.voice.poll);
    app.voice.poll = null;
  });
}
