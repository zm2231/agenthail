import { readStream, streamIdle } from "./sse-reader.js";

export const beatEvery = 10_000;
export const leaderSilence = 30_000;
const lockName = "agenthail-streams";
const subscribers = new Map();
const streams = new Map();

export function streamsSupported(scope = globalThis) {
  return Boolean(scope.navigator?.locks && scope.BroadcastChannel);
}

export function subscribe(stream, receive) {
  if (!subscribers.has(stream)) subscribers.set(stream, new Set());
  subscribers.get(stream).add(receive);
  return () => subscribers.get(stream).delete(receive);
}

export function configureStream(stream, config) {
  streams.set(stream, config);
}

const deliver = (message) => subscribers.get(message.stream)?.forEach((receive) => receive(message));

function eventsStream() {
  let last = "";
  return {
    ready: () => true,
    path: () => `/api/v1/events${last ? `?after=${encodeURIComponent(last)}` : ""}`,
    seen: (event) => { if (event.id) last = event.id; },
  };
}

export function startSharedStreams({ locks = navigator.locks, channel = new BroadcastChannel(lockName), read = readStream, visible = () => document.visibilityState === "visible", watchVisibility = (changed) => document.addEventListener("visibilitychange", changed), now = Date.now, random = Math.random, setTimeout = globalThis.setTimeout, setInterval = globalThis.setInterval, clearInterval = globalThis.clearInterval } = {}) {
  if (!streams.has("events")) configureStream("events", eventsStream());
  let leading = null;
  let queued = null;
  let heard = now();
  let patience = leaderSilence + random() * 5_000;
  const post = (message) => {
    channel.postMessage(message);
    deliver(message);
  };
  const sleep = (ms, signal) => new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener("abort", () => { clearTimeout(timer); resolve(); }, { once: true });
  });

  async function follow(name, config, signal) {
    for (let attempt = 0; !signal.aborted; attempt++) {
      if (!config.ready()) {
        await sleep(250, signal);
        attempt = -1;
        continue;
      }
      const result = await read(config.path(), {
        signal,
        onOpen: () => { attempt = 0; },
        onEvent: (event) => { config.seen?.(event); post({ stream: name, kind: "event", event }); },
      }).catch(() => null);
      if (signal.aborted) return;
      if (result?.status === 409) config.gap?.();
      post({ stream: name, kind: "reset", status: result?.status ?? 0 });
      await sleep(Math.min(20_000, 1_000 * 2 ** Math.max(0, attempt)), signal);
    }
  }

  function lead() {
    const controller = new AbortController();
    leading = controller;
    for (const name of new Set([...streams.keys(), ...subscribers.keys()])) post({ stream: name, kind: "reset", status: 0 });
    const beat = setInterval(() => channel.postMessage({ kind: "beat" }), beatEvery);
    controller.signal.addEventListener("abort", () => clearInterval(beat), { once: true });
    for (const [name, config] of streams) follow(name, config, controller.signal);
    return { controller, done: new Promise((resolve) => controller.signal.addEventListener("abort", resolve, { once: true })) };
  }

  function stepDown(controller) {
    if (leading !== controller) return;
    controller.abort();
    leading = null;
  }

  function join(steal = false) {
    queued?.abort();
    const abort = steal ? null : new AbortController();
    queued = abort;
    let mine = null;
    const granted = () => {
      if (queued === abort) queued = null;
      mine = lead();
      return mine.done;
    };
    locks.request(lockName, abort ? { signal: abort.signal } : { steal: true }, granted).then(
      () => mine && stepDown(mine.controller),
      () => {
        if (mine && leading === mine.controller) {
          stepDown(mine.controller);
          join();
        } else if (!mine && !abort?.signal.aborted) join();
      },
    );
  }

  channel.onmessage = ({ data }) => {
    heard = now();
    if (data?.stream) deliver(data);
  };
  let checked = now();
  setInterval(() => {
    const woke = now() - checked > 10_000;
    checked = now();
    if (woke) heard = checked;
    if (leading || !visible() || now() - heard < patience) return;
    heard = now();
    patience = leaderSilence + random() * 5_000;
    join(true);
  }, 5_000);
  watchVisibility(() => {
    if (!visible()) {
      queued?.abort();
      queued = null;
      if (leading) stepDown(leading);
    } else if (!leading && !queued) join();
  });
  if (visible()) join();
  return { leading: () => Boolean(leading), channel };
}

export class SharedEventSource {
  constructor() {
    this.listeners = new Map();
    subscribe("events", (message) => {
      if (message.kind === "event") this.dispatch(message.event.type, message.event);
      else this.dispatch("stream.reset", { data: "{}" });
    });
  }

  addEventListener(type, listener) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type).add(listener);
  }

  dispatch(type, event) {
    this.listeners.get(type)?.forEach((listener) => listener(event));
  }
}

export { streamIdle };
