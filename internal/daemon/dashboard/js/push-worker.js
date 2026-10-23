// The dashboard's service worker shows the notifications the daemon pushes
// and closes them again when their question is answered elsewhere. It holds
// no state: every message carries what it needs.

const icon = "/logo.png";

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));

self.addEventListener("push", (event) => {
  let message = {};
  try {
    message = event.data ? event.data.json() : {};
  } catch {
    message = {};
  }
  event.waitUntil(message.type === "withdraw" ? withdraw(message) : show(message));
});

async function show(message) {
  const tag = message.collapseId || undefined;
  await self.registration.showNotification(message.title || "Agenthail", {
    body: message.message || "",
    icon,
    badge: icon,
    tag,
    renotify: false,
    data: { sessionId: message.sessionId || "", requestId: message.requestId || "" },
  });
}

// withdraw closes what the question showed. A push that leaves nothing shown
// counts as silent, and browsers punish those: Chrome posts its own "updated
// in the background" notice. So an answered marker is shown silently and
// closed at once, which the system removes before presenting it. The marker
// has its own tag: under the question's tag it would replace a notification
// still on screen instead of arriving new. A browser whose close leaves the
// question shown, as Safari's does, is reported so it gets no more withdraws:
// each would leave one more notification.
async function withdraw(message) {
  if (!message.collapseId) return;
  const shown = await self.registration.getNotifications({ tag: message.collapseId });
  for (const notification of shown) notification.close();
  const marker = `${message.collapseId}:answered`;
  await self.registration.showNotification("Agenthail", { body: "Answered", icon, badge: icon, tag: marker, silent: true });
  for (const notification of await self.registration.getNotifications({ tag: marker })) notification.close();
  if (shown.length > 0 && (await self.registration.getNotifications({ tag: message.collapseId })).length > 0) await reportCannotClose();
}

async function reportCannotClose() {
  const subscription = await self.registration.pushManager.getSubscription();
  if (!subscription) return;
  await fetch("/api/v1/browser/push", {
    method: "PATCH",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ endpoint: subscription.endpoint, withdraw: false }),
  }).catch(() => {});
}

// A click opens the session in a dashboard window. The worker's scope is
// /js/, so it controls no page: an open window is told by message, and a new
// one is opened at the session.
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(openSession(event.notification.data?.sessionId || ""));
});

async function openSession(sessionId) {
  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  const open = windows.find((client) => new URL(client.url).origin === self.location.origin);
  if (open) {
    await open.focus();
    if (sessionId) open.postMessage({ type: "open-session", sessionId });
    return;
  }
  const target = new URL("/", self.location.origin);
  if (sessionId) target.searchParams.set("session", sessionId);
  target.hash = sessionId ? "conversations" : "overview";
  await self.clients.openWindow(target.href);
}
