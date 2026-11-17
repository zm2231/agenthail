import { applyTileChange } from "./project-tiles.js";

const storageKey = "agenthail.tileOrder";
const empty = { order: [], pinned: [], revision: 0, unsent: false };
let pending = [];
let saved = read();
let heard = false;
let sending = false;
let arrange = (arrangement) => arrangement.order;
let changed = () => {};

function read() {
  try {
    const value = JSON.parse(globalThis.localStorage?.getItem(storageKey) ?? "null");
    if (value && Array.isArray(value.order) && Array.isArray(value.pinned)) {
      pending = Array.isArray(value.pending) ? value.pending : [];
      return { order: value.order, pinned: value.pinned, revision: Number(value.revision) || 0, unsent: Boolean(value.unsent) };
    }
  } catch {}
  return { ...empty };
}

function persist() {
  try { globalThis.localStorage?.setItem(storageKey, JSON.stringify({ ...saved, pending })); } catch {}
}

function save(next) {
  const moved = JSON.stringify([next.order, next.pinned]) !== JSON.stringify([saved.order, saved.pinned]);
  saved = next;
  persist();
  if (moved) changed();
}

async function put({ order, pinned, revision }) {
  const response = await globalThis.fetch("/api/v1/projects/tiles", {
    method: "PUT",
    headers: { "content-type": "application/json", accept: "application/json" },
    body: JSON.stringify({ order, pinned, revision }),
  });
  const body = await response.json().catch(() => ({}));
  if (response.status === 409 && body.tileOrder) return { stale: body.tileOrder };
  if (!response.ok || !body.tileOrder) throw new Error(body.error?.message || `The tile order was not saved (${response.status}).`);
  return { saved: body.tileOrder };
}

const same = (a, b) => JSON.stringify([a.order, [...a.pinned].sort()]) === JSON.stringify([b.order, [...b.pinned].sort()]);

function replayed(base) {
  return pending.reduce((arrangement, change) => ({ ...arrangement, ...applyTileChange(arrangement, change, arrange(arrangement)) }), base);
}

async function send() {
  if (!heard || sending) return;
  sending = true;
  let retried = false;
  try {
    for (;;) {
      const sent = saved;
      const included = pending.length;
      const reply = await put(sent);
      if (reply.saved) {
        pending = pending.slice(included);
        saved = { ...saved, revision: reply.saved.revision };
        persist();
        if (!same(saved, sent)) continue;
        save({ ...saved, unsent: false });
        return;
      }
      const current = reply.stale;
      if (retried || !saved.unsent) {
        pending = [];
        save({ order: current.order, pinned: current.pinned, revision: current.revision, unsent: false });
        return;
      }
      retried = true;
      const base = { order: current.order, pinned: current.pinned };
      save({ ...(pending.length ? replayed(base) : saved), revision: current.revision, unsent: true });
    }
  } catch {
  } finally {
    sending = false;
  }
}

export function tileOrder() {
  return saved;
}

export function onTileOrderChange(callback) {
  changed = callback;
}

export function tilesArrangedBy(callback) {
  arrange = callback;
}

export function adoptTileOrder(remote) {
  if (!remote) return;
  heard = true;
  if (sending || (!saved.unsent && remote.revision < saved.revision)) return;
  if (saved.unsent || (remote.revision === 0 && saved.order.length)) {
    saved = { ...saved, revision: remote.revision };
    send();
  } else if (remote.revision !== saved.revision || !same(remote, saved)) {
    save({ order: remote.order || [], pinned: remote.pinned || [], revision: remote.revision, unsent: false });
  }
}

export function seedTileOrder(ids) {
  if (saved.order.length || !ids.length) return;
  save({ ...saved, order: ids });
  if (heard && saved.revision === 0) send();
}

export function changeTiles(change, arranged) {
  const next = applyTileChange(saved, change, arranged);
  if (same(next, saved)) return Promise.resolve();
  pending.push(change);
  save({ ...next, revision: saved.revision, unsent: true });
  return send();
}
