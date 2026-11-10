import "./test-home.mjs";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

const source = readFileSync(new URL("../internal/daemon/dashboard/js/push-worker.js", import.meta.url), "utf8");

function worker(fetchReply) {
  const listeners = {};
  const shown = [];
  const posts = [];
  const opened = [];
  const self = {
    addEventListener: (type, handler) => { listeners[type] = handler; },
    skipWaiting: () => {},
    location: { origin: "https://host.example" },
    Notification: { maxActions: 2 },
    registration: {
      showNotification: async (title, options) => { shown.push({ title, options }); },
      getNotifications: async () => [],
    },
    clients: {
      claim: async () => {},
      matchAll: async () => [],
      openWindow: async (href) => { opened.push(href); },
    },
  };
  const fetch = async (url, init) => {
    posts.push({ url, init, body: JSON.parse(init.body) });
    return fetchReply();
  };
  vm.runInNewContext(source, { self, fetch, URL, JSON });
  const dispatch = async (type, event) => {
    let done;
    listeners[type]({ ...event, waitUntil: (promise) => { done = promise; } });
    await done;
  };
  return { dispatch, shown, posts, opened };
}

const askPush = {
  type: "notify", title: "Agenthail", message: "builder needs you: Ship it?", sessionId: "host-b::S1", requestId: "ask-0b5e1c2d3a4f", collapseId: "ask-key",
  actions: [{ id: "o0", title: "Ship" }, { id: "o1", title: "Wait" }, { id: "o2", title: "Cancel" }],
};

{
  const { dispatch, shown } = worker(() => ({ ok: true }));
  await dispatch("push", { data: { json: () => askPush } });
  assert.deepEqual(JSON.parse(JSON.stringify(shown[0].options.actions)), [{ action: "o0", title: "Ship" }, { action: "o1", title: "Wait" }], "an ask shows its first options, as many as the browser allows");
  await dispatch("push", { data: { json: () => ({ ...askPush, requestId: "" }) } });
  assert.deepEqual(JSON.parse(JSON.stringify(shown[1].options.actions)), [], "a notification about no request has no buttons");
}

const click = (action) => ({ action, notification: { close: () => {}, data: { sessionId: "host-b::S1", requestId: "ask-0b5e1c2d3a4f" } } });

{
  const { dispatch, posts, opened } = worker(() => ({ ok: true }));
  await dispatch("notificationclick", click("o1"));
  assert.equal(posts.length, 1);
  assert.equal(posts[0].url, "/api/v1/actions");
  assert.equal(posts[0].init.credentials, "same-origin");
  assert.ok(posts[0].init.headers["Idempotency-Key"], "a retried tap answers once");
  assert.deepEqual(posts[0].body, { action: "respond", sessionId: "host-b::S1", requestId: "ask-0b5e1c2d3a4f", decision: { choices: ["o1"] } });
  assert.deepEqual(opened, [], "an answered ask opens nothing");
}

{
  const { dispatch, opened } = worker(() => ({ ok: false, status: 409 }));
  await dispatch("notificationclick", click("o0"));
  assert.equal(opened.length, 1, "a refused answer opens the session instead");
  assert.match(opened[0], /session=host-b%3A%3AS1/);
}

{
  const { dispatch, opened } = worker(() => { throw new TypeError("offline"); });
  await dispatch("notificationclick", click("o0"));
  assert.equal(opened.length, 1, "an answer that cannot be sent opens the session instead");
}

{
  const { dispatch, posts, opened } = worker(() => ({ ok: true }));
  await dispatch("notificationclick", click(""));
  assert.equal(posts.length, 0, "a tap on the notification itself answers nothing");
  assert.equal(opened.length, 1);
}

console.log("push worker ok");
