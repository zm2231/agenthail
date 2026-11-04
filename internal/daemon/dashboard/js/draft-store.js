const storagePrefix = "agenthail.draft.";

function keyFor(sessionId) {
  return storagePrefix + JSON.stringify(sessionId);
}

function sessionOf(key) {
  if (!key?.startsWith(storagePrefix)) return undefined;
  try {
    return JSON.parse(key.slice(storagePrefix.length));
  } catch {
    return undefined;
  }
}

function textOf(raw) {
  try {
    const record = JSON.parse(raw);
    return typeof record?.text === "string" ? record.text : undefined;
  } catch {
    return undefined;
  }
}

function stored(sessionId) {
  try {
    return textOf(globalThis.localStorage.getItem(keyFor(sessionId)));
  } catch {
    return undefined;
  }
}

export function persistentDrafts() {
  const unsaved = new Map();
  const synced = new Map();
  return {
    get(sessionId) {
      if (unsaved.has(sessionId)) return unsaved.get(sessionId);
      const text = stored(sessionId);
      synced.set(sessionId, text || "");
      return text;
    },
    has(sessionId) {
      return this.get(sessionId) !== undefined;
    },
    set(sessionId, text) {
      text ||= "";
      const current = stored(sessionId) || "";
      if (synced.has(sessionId) && text === synced.get(sessionId) && current !== text && !unsaved.has(sessionId)) return true;
      synced.set(sessionId, text);
      unsaved.delete(sessionId);
      try {
        if (text) globalThis.localStorage.setItem(keyFor(sessionId), JSON.stringify({ sessionId, text, updatedAt: Date.now() }));
        else globalThis.localStorage.removeItem(keyFor(sessionId));
        return true;
      } catch {
        if (text) unsaved.set(sessionId, text);
        return false;
      }
    },
    delete(sessionId) {
      synced.set(sessionId, "");
      unsaved.delete(sessionId);
      try {
        globalThis.localStorage.removeItem(keyFor(sessionId));
      } catch {}
      return true;
    },
    watch(adopt) {
      globalThis.window?.addEventListener?.("storage", (event) => {
        const sessionId = sessionOf(event.key);
        if (sessionId === undefined) return;
        const text = stored(sessionId) || "";
        const previous = synced.get(sessionId) ?? "";
        if (text !== previous && adopt(sessionId, text, previous)) synced.set(sessionId, text);
      });
    },
  };
}
