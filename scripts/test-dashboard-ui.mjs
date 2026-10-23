import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import * as contrast from "../internal/daemon/dashboard/js/ui-contrast.js";
import { applyTheme, parseHex, resolveTheme, start } from "../internal/daemon/dashboard/js/ui.js";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const vectors = JSON.parse(fs.readFileSync(path.join(root, "scripts/fixtures/contrast-vectors.json"), "utf8"));
const close = (a, b, message) => assert.ok(Math.abs(a - b) < 1e-5, `${message}: ${a} vs ${b}`);
const closeAll = (a, b, message) => a.forEach((v, i) => close(v, b[i], message));

close(contrast.MARGIN, vectors.margin, "margin");
close(contrast.TINT_DEPTH, vectors.tintDepth, "tint depth");
for (const { in: x, out } of vectors.linear) close(contrast.linear(x), out, `linear(${x})`);
for (const { in: x, out } of vectors.gamma) close(contrast.gamma(x), out, `gamma(${x})`);
for (const { in: c, out } of vectors.luminance) close(contrast.luminance(c), out, `luminance(${c})`);
for (const v of vectors.boundary) close(contrast.boundary(v.roles, v.polarity, v.target), v.out, `boundary ${JSON.stringify(v)}`);
for (const v of vectors.reachable) closeAll(contrast.reachable(v.colour, v.polarity, v.boundary), v.out, `reachable ${JSON.stringify(v)}`);
for (const v of vectors.accent) {
  const readable = contrast.readableAccent(v.accent, v.surfaces, v.dark);
  closeAll(readable, v.readable, `readable accent ${JSON.stringify(v.accent)} dark=${v.dark}`);
  assert.deepEqual(contrast.foregroundOn(readable), v.foreground, `foreground on ${JSON.stringify(v.accent)}`);
}

for (const [background, tint, polarity, target] of [
  [[0.9, 0.85, 0.7], [0.05, 0.05, 0.06], "dark", 4.5],
  [[0.5, 0.5, 0.5], [0, 0, 0], "dark", 7],
  [[0.1, 0.15, 0.3], [0.97, 0.97, 0.98], "light", 4.5],
]) {
  const limit = contrast.boundary(polarity === "dark" ? [0.85, 0.42] : [0.012, 0.05], polarity, target);
  const alpha = contrast.blendFloor(background, tint, polarity, limit);
  const shown = (a) => contrast.luminance(background.map((b, i) => b + (tint[i] - b) * a));
  assert.ok(contrast.passes(shown(alpha), limit, polarity), `a ${alpha} layer reaches the boundary`);
  assert.ok(alpha === 0 || !contrast.passes(shown(alpha - 2 / 255), limit, polarity), `${alpha} is the least layer that does`);
}
assert.equal(contrast.blendFloor([0.02, 0.02, 0.02], [0, 0, 0], "dark", 0.18), 0, "a background that already reads needs no layer");

function oklch(L, C, h) {
  const a = C * Math.cos((h * Math.PI) / 180);
  const b = C * Math.sin((h * Math.PI) / 180);
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
  return [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ].map((v) => Math.min(1, Math.max(0, contrast.gamma(v))));
}
const css = fs.readFileSync(path.join(root, "internal/daemon/dashboard/tokens.css"), "utf8");
function tokens(selector) {
  const start = css.indexOf(`${selector} {`);
  assert.ok(start >= 0, `${selector} block`);
  const block = css.slice(start, css.indexOf("}", start));
  const out = {};
  for (const m of block.matchAll(/--([a-z-]+):\s*oklch\(([\d.]+)%\s+([\d.]+)\s+([\d.]+)\)/g)) out[m[1]] = oklch(+m[2] / 100, +m[3], +m[4]);
  return out;
}
const dark = tokens(":root");
const light = tokens(':root[data-theme="light"]');
assert.deepEqual(Object.keys(light).sort(), Object.keys(dark).sort(), "the light theme sets every colour the dark theme does");
const textRatio = (a, b) => contrast.ratio(contrast.luminance(a), contrast.luminance(b));
for (const role of ["ink", "muted", "quiet", "accent", "good", "warn", "bad", "focus"]) {
  for (const surface of ["paper", "panel", "raised", "hover"]) {
    const r = textRatio(light[role], light[surface]);
    assert.ok(r >= 4.5, `light ${role} on ${surface} reads at ${r.toFixed(2)}`);
  }
}
assert.ok(textRatio(light.paper, light.accent) >= 4.5, "light button text on the accent reads");
assert.ok(textRatio(dark.paper, dark.accent) >= 4.5, "dark button text on the accent reads");

assert.equal(resolveTheme("dark", false), "dark");
assert.equal(resolveTheme("light", true), "light");
assert.equal(resolveTheme("system", true), "dark");
assert.equal(resolveTheme("system", false), "light");
assert.equal(resolveTheme(undefined, true), "dark", "until ui.conf loads, the system appearance decides");
assert.deepEqual(parseHex("#ff8000"), [1, 128 / 255, 0]);
assert.equal(parseHex("system"), null);

function fakeDocument() {
  const properties = new Map();
  const meta = { content: "dark", setAttribute(name, value) { this[name] = value; } };
  return {
    meta,
    documentElement: {
      dataset: {},
      style: {
        properties,
        setProperty: (name, value) => properties.set(name, value),
        removeProperty: (name) => properties.delete(name),
        set colorScheme(value) { properties.set("color-scheme", value); },
      },
    },
    querySelector: (selector) => (selector === 'meta[name="color-scheme"]' ? meta : null),
  };
}
const surfaces = {
  dark: { "var(--paper)": [0.09, 0.094, 0.102], "var(--panel)": [0.12, 0.12, 0.13], "var(--raised)": [0.16, 0.16, 0.17], "var(--hover)": [0.19, 0.19, 0.2] },
  light: { "var(--paper)": [0.97, 0.96, 0.95], "var(--panel)": [0.99, 0.99, 0.98], "var(--raised)": [0.94, 0.93, 0.92], "var(--hover)": [0.91, 0.9, 0.89] },
};
const colourOf = (doc) => (value) => surfaces[doc.documentElement.dataset.theme][value];

{
  const doc = fakeDocument();
  assert.equal(applyTheme(doc, { theme: "light", "theme.accent": "system" }, true, colourOf(doc)), "light");
  assert.equal(doc.documentElement.dataset.theme, "light", "theme = light switches the page to light even when the system is dark");
  assert.equal(doc.meta.content, "light", "form controls and scrollbars follow");
  assert.equal(doc.documentElement.style.properties.get("color-scheme"), "light");
  assert.equal(doc.documentElement.style.properties.has("--accent"), false, "the system accent keeps the dashboard's own");

  applyTheme(doc, { theme: "dark", "theme.accent": "#1b2a6b" }, false, colourOf(doc));
  const accent = doc.documentElement.style.properties.get("--accent");
  const rgb = accent.match(/\d+/g).slice(0, 3).map((v) => +v / 255);
  for (const surface of Object.values(surfaces.dark)) {
    assert.ok(contrast.accentContrast(rgb, surface) >= 4.4, `a dark navy accent is lifted until links read on dark surfaces (${accent})`);
  }
  assert.match(doc.documentElement.style.properties.get("--accent-soft"), /\/ 0\.12\)$/);
  assert.equal(doc.documentElement.style.properties.get("--on-accent"), "rgb(0 0 0)", "button text on a lifted accent picks the readable colour");

  applyTheme(doc, { theme: "dark", "theme.accent": "system" }, false, colourOf(doc));
  assert.equal(doc.documentElement.style.properties.has("--accent"), false, "going back to the system accent removes the override");
}

{
  const doc = fakeDocument();
  const requests = [];
  let config = { theme: "system", "theme.accent": "system" };
  const fetch = async (url) => {
    requests.push(url);
    return { ok: true, json: async () => ({ values: config }) };
  };
  const listeners = {};
  class FakeEventSource {
    constructor(url) {
      this.url = url;
      FakeEventSource.last = this;
    }
    addEventListener(type, listener) {
      (listeners[type] ||= []).push(listener);
    }
  }
  const media = { matches: true, handlers: [], addEventListener(type, handler) { this.handlers.push(handler); } };
  const stored = new Map();
  const storage = { getItem: (k) => stored.get(k) ?? null, setItem: (k, v) => stored.set(k, v) };
  const view = start({ document: doc, fetch, EventSource: FakeEventSource, matchMedia: () => media, storage, colourOf: colourOf(doc), debounce: 1 });
  await view.ready;
  assert.deepEqual(requests, ["/api/v1/ui?class=web"], "the page asks the daemon for the web device's settings");
  assert.equal(FakeEventSource.last.url, "/api/v1/events");
  assert.equal(doc.documentElement.dataset.theme, "dark", "system follows a dark system appearance");

  media.matches = false;
  media.handlers.forEach((handler) => handler());
  assert.equal(doc.documentElement.dataset.theme, "light", "and switches live when the system turns light");

  config = { theme: "dark", "theme.accent": "#3fb68b" };
  listeners["ui.changed"].forEach((listener) => listener());
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(requests.length, 2, "a ui.conf change reloads the settings");
  assert.equal(doc.documentElement.dataset.theme, "dark", "and applies the new theme");
  assert.ok(doc.documentElement.style.properties.get("--accent"), "and the new accent");

  listeners["stream.reset"].forEach((listener) => listener());
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(requests.length, 3, "a reset event stream reloads too");

  const next = fakeDocument();
  start({ document: next, fetch: () => new Promise(() => {}), EventSource: FakeEventSource, matchMedia: () => ({ matches: false }), storage, colourOf: colourOf(next) });
  assert.equal(next.documentElement.dataset.theme, "dark", "the next page load starts with the last theme before the daemon answers");
}

{
  const doc = fakeDocument();
  class Silent {
    addEventListener() {}
  }
  const view = start({ document: doc, fetch: async () => { throw new Error("offline"); }, EventSource: Silent, matchMedia: () => ({ matches: true }), storage: null, colourOf: colourOf(doc) });
  await view.ready;
  assert.equal(doc.documentElement.dataset.theme, "dark", "an unreachable daemon leaves the system appearance");
}

console.log("dashboard ui ok");
