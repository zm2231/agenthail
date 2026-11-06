import assert from "node:assert/strict";
import { dashboardModules } from "./dashboard-modules.mjs";

let clock = 0;
let timers = [];
const setTimer = (fn, ms, every = 0) => { const timer = { fn, at: clock + ms, every, live: true }; timers.push(timer); return timer; };
const clearTimer = (timer) => { if (timer) timer.live = false; };
const advance = async (ms) => {
  const end = clock + ms;
  for (;;) {
    const next = timers.filter((timer) => timer.live && timer.at <= end).sort((a, b) => a.at - b.at)[0];
    if (!next) break;
    clock = next.at;
    if (next.every) next.at += next.every;
    else next.live = false;
    next.fn();
    await settle();
  }
  clock = end;
  await settle();
};
const settle = async () => { for (let i = 0; i < 20; i++) await new Promise(setImmediate); };

class Locks {
  holder = null;
  queue = [];
  request(_name, options, callback) {
    return new Promise((resolve, reject) => {
      const entry = { callback, resolve, reject };
      if (options.steal) {
        if (this.holder) this.holder.reject(Object.assign(new Error("stolen"), { name: "AbortError" }));
        this.grant(entry);
        return;
      }
      options.signal?.addEventListener("abort", () => {
        const index = this.queue.indexOf(entry);
        if (index < 0) return;
        this.queue.splice(index, 1);
        reject(Object.assign(new Error("aborted"), { name: "AbortError" }));
      });
      if (this.holder) this.queue.push(entry);
      else this.grant(entry);
    });
  }
  grant(entry) {
    this.holder = entry;
    Promise.resolve(entry.callback()).then((value) => {
      if (this.holder === entry) {
        this.holder = null;
        const next = this.queue.shift();
        if (next) this.grant(next);
      }
      entry.resolve(value);
    });
  }
}

const hub = new Set();
class Channel {
  constructor() { hub.add(this); }
  postMessage(data) { for (const peer of hub) if (peer !== this && !peer.frozen) setImmediate(() => peer.onmessage?.({ data: structuredClone(data) })); }
}

const opened = [];
const read = (path, { signal, onOpen, onEvent }) => new Promise((resolve) => {
  const stream = { path, onEvent, signal, end: (status) => { stream.ended = true; resolve({ status }); } };
  opened.push(stream);
  onOpen();
  signal.addEventListener("abort", () => resolve({ status: 0 }), { once: true });
});
const live = (name) => opened.filter((stream) => stream.path.startsWith(name) && !stream.signal.aborted && !stream.ended);

const locks = new Locks();
async function tab(label) {
  const own = [];
  const timer = (fn, ms, every) => { const made = setTimer(fn, ms, every); own.push(made); return made; };
  const context = { URLSearchParams, AbortController, setTimeout: (fn, ms) => timer(fn, ms), clearTimeout: clearTimer, navigator: { locks }, BroadcastChannel: Channel };
  await dashboardModules(["shared-stream"], context);
  const got = [];
  context.configureStream("catalog", { ready: () => true, path: () => `/api/v1/catalog-events?after=${label}`, gap: () => got.push("gap") });
  context.subscribe("catalog", (message) => got.push(message.kind === "event" ? message.event.data : message.kind));
  const channel = new Channel();
  const visible = { value: true };
  let changed = () => {};
  const show = async (value) => { visible.value = value; changed(); await settle(); };
  const handle = context.startSharedStreams({ locks, channel, read, visible: () => visible.value, watchVisibility: (fn) => { changed = fn; }, now: () => clock, random: () => 0, setTimeout: (fn, ms) => timer(fn, ms), setInterval: (fn, ms) => timer(fn, ms, ms), clearInterval: clearTimer });
  const freeze = () => {
    channel.frozen = true;
    visible.value = false;
    for (const made of own) if (made.live) { made.live = false; made.frozen = true; }
  };
  const thaw = () => {
    channel.frozen = false;
    for (const made of own) if (made.frozen) { made.frozen = false; made.live = true; made.at = Math.max(made.at, clock); }
  };
  return { context, got, channel, visible, show, freeze, thaw, ...handle };
}

const support = {};
await dashboardModules(["shared-stream"], support);
assert.equal(support.streamsSupported({}), false, "a browser without Web Locks or BroadcastChannel is unsupported");
assert.equal(support.streamsSupported({ navigator: { locks: {} }, BroadcastChannel: class {} }), true);
const first = await tab("1");
await settle();
const second = await tab("2");
const third = await tab("3");
await settle();
assert.equal(first.leading(), true, "the first tab leads");
assert(!second.leading() && !third.leading());
assert.equal(live("/api/v1/catalog-events").length, 1, "three tabs hold one catalog stream");
assert.equal(live("/api/v1/events").length, 1, "and one events stream");

live("/api/v1/catalog-events")[0].onEvent({ id: "7", type: "session.upserted", data: "seven" });
await settle();
for (const each of [first, second, third]) assert.equal(each.got.at(-1), "seven", "every tab receives the leader's events");

await advance(25_000);
assert.equal(first.leading(), true, "a leader that keeps beating keeps leading");

await first.show(false);
assert.equal(first.leading(), false, "a hidden tab gives up leading, so a frozen background tab holds no stream");
assert.equal([second, third].filter((each) => each.leading()).length, 1, "a visible tab takes over at once");
assert.equal(live("/api/v1/catalog-events").length, 1);
await first.show(true);
assert.equal(first.leading(), false, "returning to view queues behind the current leader");
const handedTo = second.leading() ? second : third;
await handedTo.show(false);
assert.equal(first.leading() || (handedTo === second ? third : second).leading(), true, "the lock passes on again when that tab is hidden");
await handedTo.show(true);
await advance(1_000);
const holders = [first, second, third].filter((each) => each.leading());
assert.equal(holders.length, 1);
const [current] = holders;
const [watcher, sleeper] = [first, second, third].filter((each) => each !== current);

current.freeze();
await sleeper.show(false);
watcher.got.length = 0;
await advance(40_000);
assert.equal(watcher.leading(), true, "a visible follower takes over from a leader that stopped beating");
assert.equal(sleeper.leading(), false, "a hidden tab does not take over");
assert.equal(current.leading(), false, "the silent leader loses its lock");
assert.equal(live("/api/v1/catalog-events").length, 1, "the takeover leaves one catalog stream");
assert(watcher.got.includes("reset"), "a new leader makes every tab reread");
current.thaw();
current.visible.value = true;
await advance(60_000);
assert.equal(watcher.leading(), true, "a thawed tab hears the new leader and does not take the lock back");
assert.equal(current.leading(), false);
assert.equal(live("/api/v1/catalog-events").length, 1);

const stream = live("/api/v1/catalog-events")[0];
stream.end(409);
await settle();
assert(watcher.got.includes("gap"), "a retained-cursor gap reaches the leader's stream config");
await advance(1_000);
assert.equal(live("/api/v1/catalog-events").length, 1, "the stream reopens after a gap");

const parse = { AbortController, TextDecoder, TextEncoder, setTimeout, clearTimeout };
await dashboardModules(["sse-reader"], parse);
const chunks = [": connected\n\n", "id: 4\nevent: session.upserted\nda", "ta: {\"a\":1}\n\n: keepalive\n\nid: 5\r\nevent: x\r\ndata: one\r\ndata: two\r\n\r\n"];
const events = [];
let index = 0;
const body = { getReader: () => ({ read: async () => index < chunks.length ? { value: new TextEncoder().encode(chunks[index++]), done: false } : { done: true } }) };
const result = await parse.readStream("/s", { fetch: async () => ({ ok: true, body }), onOpen() {}, onEvent: (event) => events.push(event) });
assert.equal(result.status, 200);
assert.deepEqual(events.map((event) => [event.id, event.type, event.data]), [["4", "session.upserted", "{\"a\":1}"], ["5", "x", "one\ntwo"]], "frames split across chunks, comments and CRLF parse");
assert.equal((await parse.readStream("/s", { fetch: async () => ({ ok: false, status: 409 }), onEvent() {} })).status, 409, "a refused stream reports its status");
let aborted = false;
const silent = { getReader: () => ({ read: () => new Promise(() => {}) }) };
const stalled = parse.readStream("/s", { idle: 20, fetch: async (_path, { signal }) => { signal.addEventListener("abort", () => { aborted = true; }); return { ok: true, body: silent }; }, onEvent() {} });
await new Promise((resolve) => setTimeout(resolve, 60));
assert.equal(aborted, true, "a stream silent past the idle limit is dropped, as after sleep");
stalled.catch(() => {});
console.log("dashboard shared stream tests passed");
