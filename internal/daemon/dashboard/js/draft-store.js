const storagePrefix = "agenthail.draft.";

function keyFor(sessionId) {
  return storagePrefix + JSON.stringify(sessionId);
}

export function persistentDrafts() {
  const unsaved = new Map();
  return {
    get(sessionId) {
      if (unsaved.has(sessionId)) return unsaved.get(sessionId);
      try {
        const record = JSON.parse(globalThis.localStorage.getItem(keyFor(sessionId)));
        return typeof record?.text === "string" ? record.text : undefined;
      } catch {
        return undefined;
      }
    },
    has(sessionId) {
      return this.get(sessionId) !== undefined;
    },
    set(sessionId, text) {
      if (!text) return this.delete(sessionId);
      try {
        globalThis.localStorage.setItem(keyFor(sessionId), JSON.stringify({ sessionId, text, updatedAt: Date.now() }));
        unsaved.delete(sessionId);
        return true;
      } catch {
        unsaved.set(sessionId, text);
        return false;
      }
    },
    delete(sessionId) {
      unsaved.delete(sessionId);
      try {
        globalThis.localStorage.removeItem(keyFor(sessionId));
      } catch {}
      return true;
    },
  };
}
