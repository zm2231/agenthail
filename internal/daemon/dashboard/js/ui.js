import { foregroundOn, readableOnTint } from "./ui-contrast.js";

const ACCENT_TOKENS = ["--accent", "--accent-soft", "--on-accent"];
const SURFACES = ["--paper", "--panel", "--raised", "--hover"];
const STORED = "agenthail.ui";
const SOFT = 0.12;

export function resolveTheme(theme, prefersDark) {
  if (theme === "dark" || theme === "light") return theme;
  return prefersDark ? "dark" : "light";
}

export function parseHex(value) {
  const match = /^#([0-9a-f]{6})$/i.exec(value || "");
  if (!match) return null;
  const n = parseInt(match[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((v) => v / 255);
}

function css([r, g, b], alpha) {
  const channels = [r, g, b].map((v) => Math.round(v * 255)).join(" ");
  return alpha === undefined ? `rgb(${channels})` : `rgb(${channels} / ${alpha})`;
}

export function applyTheme(document, values, prefersDark, colourOf) {
  const root = document.documentElement;
  const theme = resolveTheme(values.theme, prefersDark);
  root.dataset.theme = theme;
  root.style.colorScheme = theme;
  document.querySelector('meta[name="color-scheme"]')?.setAttribute("content", theme);
  for (const token of ACCENT_TOKENS) root.style.removeProperty(token);
  const accent = parseHex(values["theme.accent"]);
  if (accent) {
    const surfaces = SURFACES.map((token) => colourOf(`var(${token})`)).filter(Boolean);
    const readable = readableOnTint(accent, surfaces, theme === "dark", SOFT);
    root.style.setProperty("--accent", css(readable));
    root.style.setProperty("--accent-soft", css(readable, SOFT));
    root.style.setProperty("--on-accent", css(foregroundOn(readable)));
  }
  return theme;
}

function browserColour(document) {
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = 1;
  const context = canvas.getContext("2d", { willReadFrequently: true });
  const probe = document.createElement("span");
  probe.style.display = "none";
  document.body.append(probe);
  return (value) => {
    probe.style.color = value;
    const resolved = getComputedStyle(probe).color;
    if (!resolved || !context) return null;
    context.clearRect(0, 0, 1, 1);
    context.fillStyle = resolved;
    context.fillRect(0, 0, 1, 1);
    const [r, g, b] = context.getImageData(0, 0, 1, 1).data;
    return [r / 255, g / 255, b / 255];
  };
}

export function start({ document, fetch, EventSource, matchMedia, storage, colourOf, debounce = 150 }) {
  const scheme = matchMedia("(prefers-color-scheme: dark)");
  let values = {};
  try {
    values = JSON.parse(storage?.getItem(STORED) || "{}");
  } catch {
    values = {};
  }
  const apply = () => applyTheme(document, values, scheme.matches, colourOf);
  apply();

  let generation = 0;
  const load = async () => {
    const mine = ++generation;
    try {
      const response = await fetch("/api/v1/ui?class=web", { headers: { accept: "application/json" } });
      if (!response.ok || mine !== generation) return;
      const body = await response.json();
      if (mine !== generation) return;
      values = body.values || {};
      storage?.setItem(STORED, JSON.stringify({ theme: values.theme, "theme.accent": values["theme.accent"] }));
      apply();
    } catch {}
  };

  let timer = null;
  const reload = () => {
    clearTimeout(timer);
    timer = setTimeout(load, debounce);
  };
  scheme.addEventListener?.("change", apply);
  const events = new EventSource("/api/v1/events");
  events.addEventListener("ui.changed", reload);
  events.addEventListener("stream.reset", reload);
  const ready = load();
  return { ready, reload, events, current: () => values };
}

if (typeof window !== "undefined" && window.document) {
  start({
    document: window.document,
    fetch: window.fetch.bind(window),
    EventSource: window.EventSource,
    matchMedia: window.matchMedia.bind(window),
    storage: window.localStorage,
    colourOf: browserColour(window.document),
  });
}
