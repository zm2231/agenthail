import { createBackdrop, parseBackground } from "./ui-backdrop.js";
import { foregroundOn, luminance, readableOnTint, readableText } from "./ui-contrast.js";

const ACCENT_TOKENS = ["--accent", "--accent-soft", "--on-accent"];
const SURFACES = ["--paper", "--panel", "--raised", "--hover"];
const STORED = "agenthail.ui";
const SOFT = 0.12;
const ROLES = ["--ink", "--muted", "--quiet"];

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

export function effectiveTarget(values, highContrast) {
  if (highContrast) return 7;
  return Number.isFinite(values["contrast.target"]) ? values["contrast.target"] : 4.5;
}

function textSurfaces(colourOf) {
  const composer = colourOf("var(--composer)", "var(--paper)");
  const solid = [...SURFACES, "--code"].map((token) => colourOf(`var(${token})`));
  const layered = [
    composer,
    colourOf("var(--glass)", "var(--paper)"),
    colourOf("var(--accent-soft)", "var(--paper)"),
    colourOf("var(--accent-soft)", "var(--panel)"),
    composer && colourOf("var(--accent-soft)", css(composer)),
  ];
  return [...solid, ...layered].filter(Boolean);
}

export function applyTheme(document, values, prefersDark, colourOf, target = 4.5) {
  const root = document.documentElement;
  const theme = resolveTheme(values.theme, prefersDark);
  root.dataset.theme = theme;
  root.style.colorScheme = theme;
  document.querySelector('meta[name="color-scheme"]')?.setAttribute("content", theme);
  for (const token of [...ACCENT_TOKENS, ...ROLES]) root.style.removeProperty(token);
  const accent = parseHex(values["theme.accent"]);
  if (accent) {
    const surfaces = SURFACES.map((token) => colourOf(`var(${token})`)).filter(Boolean);
    const readable = readableOnTint(accent, surfaces, theme === "dark", SOFT);
    root.style.setProperty("--accent", css(readable));
    root.style.setProperty("--accent-soft", css(readable, SOFT));
    root.style.setProperty("--on-accent", css(foregroundOn(readable)));
  }
  const surfaces = textSurfaces(colourOf);
  for (const token of ROLES) {
    const colour = colourOf(`var(${token})`);
    if (!colour) continue;
    const readable = readableText(colour, surfaces, theme === "dark", target);
    if (readable !== colour) root.style.setProperty(token, css(readable));
  }
  return theme;
}

export function backdropLook(values, theme, { sessionOpen, highContrast, colourOf, width, height, scale = 1 }) {
  const number = (key, fallback) => (Number.isFinite(values[key]) ? values[key] : fallback);
  const ground = colourOf("var(--paper)");
  return {
    spec: parseBackground(theme === "light" ? values["background.light"] : values.background),
    fit: values["background.fit"],
    position: values["background.position"],
    strength: number(sessionOpen ? "background.session-opacity" : "background.opacity", 1),
    dim: number("background.dim", 0),
    texture: values.texture,
    textureStrength: number("texture.strength", 0),
    textureSpacing: number("texture.spacing", 3),
    polarity: theme,
    ground: ground || (theme === "dark" ? [0, 0, 0] : [1, 1, 1]),
    roles: ROLES.map((token) => colourOf(`var(${token})`)).filter(Boolean).map(luminance),
    target: effectiveTarget(values, highContrast),
    readable: highContrast || values["glass.readability"] !== "off",
    width,
    height,
    scale,
  };
}

function browserColour(document) {
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = 1;
  const context = canvas.getContext("2d", { willReadFrequently: true });
  const probe = document.createElement("span");
  probe.style.display = "none";
  document.body.append(probe);
  const resolve = (value) => {
    probe.style.color = value;
    return getComputedStyle(probe).color;
  };
  return (value, under) => {
    const resolved = resolve(value);
    const ground = under ? resolve(under) : null;
    if (!resolved || !context || (under && !ground)) return null;
    context.clearRect(0, 0, 1, 1);
    if (ground) {
      context.fillStyle = ground;
      context.fillRect(0, 0, 1, 1);
    }
    context.fillStyle = resolved;
    context.fillRect(0, 0, 1, 1);
    const [r, g, b] = context.getImageData(0, 0, 1, 1).data;
    return [r / 255, g / 255, b / 255];
  };
}

export function workerMeasure(worker) {
  const waiting = new Map();
  let next = 0;
  let broken = false;
  const fail = () => {
    broken = true;
    for (const resolve of waiting.values()) resolve(null);
    waiting.clear();
  };
  worker.addEventListener("message", ({ data }) => {
    waiting.get(data.id)?.(data.alpha);
    waiting.delete(data.id);
  });
  worker.addEventListener("error", fail);
  worker.addEventListener("messageerror", fail);
  return (look) =>
    new Promise((resolve) => {
      if (broken) return resolve(null);
      const id = ++next;
      waiting.set(id, resolve);
      worker.postMessage({ id, look });
    });
}

export function start({ document, fetch, EventSource, matchMedia, storage, colourOf, backdrop, viewport, MutationObserver, addEventListener, debounce = 150 }) {
  const scheme = matchMedia("(prefers-color-scheme: dark)");
  const contrast = matchMedia("(prefers-contrast: more)");
  let values = {};
  try {
    values = JSON.parse(storage?.getItem(STORED) || "{}");
  } catch {
    values = {};
  }
  let painting = Promise.resolve();
  const apply = () => {
    const theme = applyTheme(document, values, scheme.matches, colourOf, effectiveTarget(values, contrast.matches));
    if (!backdrop) return;
    const size = viewport();
    const look = backdropLook(values, theme, {
      sessionOpen: document.documentElement.hasAttribute("data-session-open"),
      highContrast: contrast.matches,
      colourOf,
      ...size,
    });
    painting = backdrop.render(look);
  };
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
  let resizeTimer = null;
  scheme.addEventListener?.("change", apply);
  contrast.addEventListener?.("change", apply);
  if (backdrop) {
    if (MutationObserver) new MutationObserver(apply).observe(document.documentElement, { attributes: true, attributeFilter: ["data-session-open"] });
    addEventListener?.("resize", () => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(apply, debounce);
    });
  }
  const events = new EventSource("/api/v1/events");
  events.addEventListener("ui.changed", reload);
  events.addEventListener("stream.reset", reload);
  const ready = load();
  return { ready, reload, events, current: () => values, painted: () => painting };
}

if (typeof window !== "undefined" && window.document) {
  start({
    document: window.document,
    fetch: window.fetch.bind(window),
    EventSource: window.EventSource,
    matchMedia: window.matchMedia.bind(window),
    storage: window.localStorage,
    colourOf: browserColour(window.document),
    backdrop: createBackdrop({ document: window.document, measure: workerMeasure(new Worker("/js/ui-sampler-worker.js", { type: "module" })) }),
    viewport: () => ({ width: window.innerWidth, height: window.innerHeight, scale: window.devicePixelRatio || 1 }),
    MutationObserver: window.MutationObserver,
    addEventListener: window.addEventListener.bind(window),
  });
}
