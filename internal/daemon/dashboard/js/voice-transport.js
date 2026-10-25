import { app } from "./state.js";
import { renderVoice } from "./voice.js";

export async function voiceRequest(action, extra = {}) {
  const response = await fetch("/api/voice", {
    method: action ? "POST" : "GET",
    headers: action ? { "content-type": "application/json" } : undefined,
    body: action ? JSON.stringify({ action, ...extra }) : undefined,
  });
  const body = await response.json();
  if (!response.ok) {
    app.voice.state = body.state || app.voice.state;
    throw Error(body?.error?.message || "Voice action was blocked.");
  }
  app.voice.state = body;
  renderVoice();
  if (app.voice.localAudio && body.sdp && body.sdp !== app.voice.appliedSDP && window.agenthailVoice) {
    app.voice.appliedSDP = body.sdp;
    await window.agenthailVoice.answer(body.sdp);
  }
  return body;
}

export function voiceCallActive() {
  return ["starting", "negotiating", "connected", "stopping", "unknown"].includes(app.voice.state?.phase);
}

export async function ensureVoicePeer() {
  if (window.agenthailVoice) return;
  if (!app.voice.script) {
    app.voice.script = new Promise((resolve, reject) => {
      const script = document.createElement("script");
      script.src = "/voice-peer.js";
      script.onload = resolve;
      script.onerror = () => reject(Error("Could not load browser voice controls."));
      document.head.append(script);
    });
  }
  await app.voice.script;
}
