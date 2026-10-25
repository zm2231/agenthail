import { $ } from "./dom.js";
import { app } from "./state.js";
import { signinRequest } from "./http.js";
import { toast } from "./feedback.js";

export const pushWorkerScope = "/js/";

export function browserPushUnavailable() {
  if (!window.isSecureContext) return "Notifications need a secure address. Open the dashboard at localhost on this computer, or through agenthail dashboard remote.";
  const appleTouch = /iPhone|iPad/.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  if (appleTouch && !navigator.standalone) return "On iPhone and iPad, add Agenthail to your Home Screen with Share, then turn this on from there.";
  if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) return "This browser cannot show notifications from a web page.";
  return "";
}

export async function pushWorker() {
  const registration = await navigator.serviceWorker.register("/js/push-worker.js", { scope: pushWorkerScope });
  if (registration.active) return registration;
  const worker = registration.installing || registration.waiting;
  await new Promise((resolve, reject) => {
    worker.addEventListener("statechange", () => {
      if (worker.state === "activated") resolve();
      if (worker.state === "redundant") reject(Error("The notification worker could not start."));
    });
  });
  return registration;
}

export function pushKeyBytes(key) {
  const base64 = key.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(key.length / 4) * 4, "=");
  return Uint8Array.from(atob(base64), (character) => character.charCodeAt(0));
}

export function sameKey(subscription, key) {
  const current = subscription.options?.applicationServerKey;
  if (!current) return true;
  const left = new Uint8Array(current);
  return left.length === key.length && left.every((byte, index) => byte === key[index]);
}

export function renderBrowserPush(on, note) {
  const toggle = $("#browser-push-toggle");
  toggle.checked = on;
  toggle.disabled = Boolean(app.browserPush.unavailable) || app.browserPush.busy;
  $("#browser-push-note").textContent = note;
}

export async function loadBrowserPush() {
  app.browserPush = { unavailable: browserPushUnavailable(), busy: false };
  if (app.browserPush.unavailable) return renderBrowserPush(false, app.browserPush.unavailable);
  try {
    const status = await signinRequest("/api/v1/browser/push");
    const registration = await navigator.serviceWorker.getRegistration(pushWorkerScope);
    const subscription = await registration?.pushManager.getSubscription();
    const key = pushKeyBytes(status.publicKey);
    if (status.subscribed && subscription && sameKey(subscription, key)) {
      await signinRequest("/api/v1/browser/push", { method: "PUT", body: JSON.stringify(subscription.toJSON()) });
      registration.update().catch(() => {});
      return renderBrowserPush(true, "On. This browser is notified while it is signed in.");
    }
    if (status.subscribed) await signinRequest("/api/v1/browser/push", { method: "DELETE" });
    if (Notification.permission === "denied") return renderBrowserPush(false, "Notifications are blocked for this site in the browser's settings.");
    renderBrowserPush(false, "Off.");
  } catch (error) {
    renderBrowserPush(false, error.message);
  }
}

export async function setBrowserPush(on) {
  app.browserPush.busy = true;
  renderBrowserPush(on, on ? "Turning on." : "Turning off.");
  try {
    if (on) {
      if ((await Notification.requestPermission()) !== "granted") throw Error("Notifications are blocked for this site in the browser's settings.");
      const { publicKey } = await signinRequest("/api/v1/browser/push");
      const key = pushKeyBytes(publicKey);
      const registration = await pushWorker();
      let subscription = await registration.pushManager.getSubscription();
      if (subscription && !sameKey(subscription, key)) {
        await subscription.unsubscribe();
        subscription = null;
      }
      subscription ||= await registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
      await signinRequest("/api/v1/browser/push", { method: "PUT", body: JSON.stringify(subscription.toJSON()) });
    } else {
      await signinRequest("/api/v1/browser/push", { method: "DELETE" });
      const registration = await navigator.serviceWorker.getRegistration(pushWorkerScope);
      await (await registration?.pushManager.getSubscription())?.unsubscribe();
    }
  } catch (error) {
    toast(error.message);
  } finally {
    app.browserPush.busy = false;
    await loadBrowserPush();
  }
}

export function bindPushEvents() {
  $("#browser-push-toggle").addEventListener("change", (event) => setBrowserPush(event.target.checked));
}
