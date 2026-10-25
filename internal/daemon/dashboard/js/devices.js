import { signinRequest } from "./http.js";
import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape } from "./format.js";
import { toast } from "./feedback.js";

export async function loadDevices() {
  try {
    const { devices } = await signinRequest("/api/v1/devices");
    app.devices = (devices || []).filter((device) => !(device.scopes || []).includes("host"));
  } catch (error) {
    app.devices = null;
    app.devicesError = error.message;
  }
  renderDevices();
}

export function renderDevices() {
  const pairing = app.devicePairing;
  $("#device-pairing").innerHTML = pairing
    ? `<div class="remote-ready"><div class="remote-qr"><img src="${escape(pairing.qr)}" alt="Pairing code for the Agenthail iPhone app"></div><div><span class="status-pill busy">Waiting for the iPhone</span><p>Scan this with the iPhone's camera, or with Scan pairing code in Agenthail, before ${escape(new Date(pairing.expiresAt).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }))}.</p><div class="remote-actions"><a class="primary-button" href="${escape(pairing.pairingURL)}">Open in Agenthail</a><button class="soft-button" data-device-pair-cancel type="button">Cancel</button></div><small>On this iPhone, tap Open in Agenthail instead.</small></div></div>`
    : `<div class="remote-actions"><button class="primary-button" data-device-pair type="button">Pair an iPhone</button></div>${app.devicePairingError ? `<p class="signin-note">${escape(app.devicePairingError)}</p>` : ""}`;
  if (!app.devices) {
    $("#devices-list").innerHTML = `<div class="empty-card">${escape(app.devicesError || "Loading devices.")}</div>`;
    return;
  }
  $("#devices-list").innerHTML = app.devices.map((device) =>
    `<article class="operation-item"><div class="operation-main"><div class="operation-title">${escape(device.name)}</div><div class="operation-detail">${device.lastSeenAt ? `Last seen ${escape(new Date(device.lastSeenAt).toLocaleString())}` : "Not seen yet"} · Notifications ${device.pushEnabled ? "on" : "off"}</div></div><button class="button quiet" data-device-remove="${escape(device.id)}" type="button">Remove</button></article>`,
  ).join("") || '<div class="empty-card">No iPhone is paired with this computer.</div>';
}

export async function startDevicePairing() {
  if (app.devicePairingStarting || app.devicePairing) return;
  app.devicePairingStarting = true;
  app.devicePairingError = "";
  try {
    const pairing = await signinRequest("/api/v1/pairings", { method: "POST", body: JSON.stringify({ name: "", scopes: ["read", "control"], qr: true }) });
    app.devicePairing = pairing;
    renderDevices();
    watchDevicePairing(pairing.id);
  } catch (error) {
    app.devicePairingError = error.message;
    renderDevices();
  } finally {
    app.devicePairingStarting = false;
  }
}

export function cancelDevicePairing() {
  app.devicePairing = null;
  renderDevices();
}

export function expireDevicePairing() {
  app.devicePairing = null;
  app.devicePairingError = "The pairing code expired. Pair again to show a new one.";
  renderDevices();
}

export async function watchDevicePairing(id) {
  while (app.devicePairing?.id === id) {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    if (app.devicePairing?.id !== id) return;
    let status;
    try {
      status = await signinRequest(`/api/v1/pairings?id=${encodeURIComponent(id)}`);
    } catch {
      if (app.devicePairing?.id === id && Date.now() >= new Date(app.devicePairing.expiresAt).getTime()) expireDevicePairing();
      continue;
    }
    if (app.devicePairing?.id !== id) return;
    if (status.state === "paired") {
      app.devicePairing = null;
      toast(`Paired ${status.device?.name || "the iPhone"}.`);
      await loadDevices();
      return;
    }
    if (status.state === "expired") {
      expireDevicePairing();
      return;
    }
  }
}

export async function removeDevice(button) {
  const device = (app.devices || []).find((item) => item.id === button.dataset.deviceRemove);
  if (!device || !confirm(`Disconnect ${device.name} from this computer?`)) return;
  button.disabled = true;
  try {
    await signinRequest("/api/v1/devices", { method: "DELETE", body: JSON.stringify({ id: device.id }) });
    toast(`Removed ${device.name}.`);
    await loadDevices();
  } catch (error) {
    toast(error.message);
    button.disabled = false;
  }
}

export function bindDeviceEvents() {
  $("#devices-card").addEventListener("click", (event) => {
    if (event.target.closest("[data-device-pair]")) {
      startDevicePairing();
      return;
    }
    if (event.target.closest("[data-device-pair-cancel]")) {
      cancelDevicePairing();
      return;
    }
    const remove = event.target.closest("[data-device-remove]");
    if (remove) removeDevice(remove);
  });
}
