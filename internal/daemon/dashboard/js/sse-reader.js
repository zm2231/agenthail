export const streamIdle = 35_000;

function frame(block) {
  const event = { id: "", type: "message", data: [] };
  for (const line of block.split("\n")) {
    if (!line || line.startsWith(":")) continue;
    const colon = line.indexOf(":");
    const field = colon < 0 ? line : line.slice(0, colon);
    const value = colon < 0 ? "" : line.slice(colon + 1).replace(/^ /, "");
    if (field === "id") event.id = value;
    else if (field === "event") event.type = value;
    else if (field === "data") event.data.push(value);
  }
  return event.data.length ? { ...event, data: event.data.join("\n") } : null;
}

export async function readStream(path, { signal, onOpen, onEvent, idle = streamIdle, fetch = globalThis.fetch, setTimeout = globalThis.setTimeout, clearTimeout = globalThis.clearTimeout }) {
  const controller = new AbortController();
  const stop = () => controller.abort();
  signal?.addEventListener("abort", stop);
  let watchdog;
  const quiet = () => {
    clearTimeout(watchdog);
    watchdog = setTimeout(stop, idle);
  };
  try {
    quiet();
    const response = await fetch(path, { signal: controller.signal, headers: { Accept: "text/event-stream" }, cache: "no-store" });
    if (!response.ok) return { status: response.status };
    onOpen?.();
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return { status: 200 };
      quiet();
      buffer = (buffer + decoder.decode(value, { stream: true })).replace(/\r\n?/g, "\n");
      let end;
      while ((end = buffer.indexOf("\n\n")) >= 0) {
        const event = frame(buffer.slice(0, end));
        buffer = buffer.slice(end + 2);
        if (event) onEvent(event);
      }
    }
  } finally {
    clearTimeout(watchdog);
    signal?.removeEventListener("abort", stop);
  }
}
