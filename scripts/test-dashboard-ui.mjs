import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import * as contrast from "../internal/daemon/dashboard/js/ui-contrast.js";
import * as backdrop from "../internal/daemon/dashboard/js/ui-backdrop.js";
import * as solve from "../internal/daemon/dashboard/js/ui-pane-solve.js";
import { paneAreas } from "../internal/daemon/dashboard/js/ui-areas.js";
import { createSampler, sampleSize } from "../internal/daemon/dashboard/js/ui-sampler.js";
import { applyTheme, backdropLook, effectiveTarget, parseHex, resolveTheme, start, workerMeasure } from "../internal/daemon/dashboard/js/ui.js";

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
  for (const m of block.matchAll(/--([a-z-]+):\s*oklch\(([\d.]+)%\s+([\d.]+)\s+([\d.]+)(?:\s*\/\s*([\d.]+))?\)/g)) {
    out[m[1]] = { colour: oklch(+m[2] / 100, +m[3], +m[4]), alpha: m[5] === undefined ? 1 : +m[5] };
  }
  return out;
}
const dark = tokens(":root");
const light = tokens(':root[data-theme="light"]');
assert.deepEqual(Object.keys(light).sort(), Object.keys(dark).sort(), "the light theme sets every colour the dark theme does");
const textRatio = (a, b) => contrast.ratio(contrast.luminance(a), contrast.luminance(b));
const over = (token, under) => token.colour.map((v, i) => v * token.alpha + under[i] * (1 - token.alpha));
for (const [name, set] of [["dark", dark], ["light", light]]) {
  const composer = over(set.composer, set.paper.colour);
  const surfaces = {
    paper: set.paper.colour,
    panel: set.panel.colour,
    raised: set.raised.colour,
    hover: set.hover.colour,
    code: set.code.colour,
    "the composer": composer,
    "the top bar": over(set.glass, set.paper.colour),
    "a selected row": over(set["accent-soft"], set.paper.colour),
    "a selected row on a panel": over(set["accent-soft"], set.panel.colour),
    "a request card": over(set["accent-soft"], composer),
  };
  for (const role of ["ink", "muted", "quiet", "accent", "good", "warn", "bad", "focus"]) {
    for (const [surface, colour] of Object.entries(surfaces)) {
      const r = textRatio(set[role].colour, colour);
      assert.ok(r >= 4.5, `${name} ${role} on ${surface} reads at ${r.toFixed(2)}`);
    }
  }
  assert.ok(textRatio(set.paper.colour, set.accent.colour) >= 4.5, `${name} button text on the accent reads`);
}

for (const [name, set] of [["dark", dark], ["light", light]]) {
  const solids = ["paper", "panel", "raised", "hover"].map((token) => set[token].colour);
  for (const hex of ["#0a84ff", "#1b2a6b", "#e9a23b", "#f2f2f2", "#3fb68b", "#101010", "#ff0000", "#808080", "#ffff00"]) {
    const accent = contrast.readableOnTint(parseHex(hex), solids, name === "dark", 0.12);
    for (const solid of solids) {
      const tint = solid.map((v, i) => accent[i] * 0.12 + v * 0.88);
      for (const [where, surface] of [["a surface", solid], ["its own tint", tint]]) {
        const r = contrast.accentContrast(accent, surface);
        assert.ok(r >= 4.5, `${name} accent ${hex} reads at ${r.toFixed(2)} on ${where}`);
      }
    }
  }
}

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

{
  const grain = backdrop.grainPixels();
  assert.deepEqual([...grain.slice(0, 32)], vectors.backdrop.grain.first, "grain is the Mac's tile, byte for byte");
  assert.equal(grain.reduce((a, b) => a + b, 0), vectors.backdrop.grain.sum);

  const mesh = vectors.backdrop.mesh;
  assert.deepEqual(backdrop.meshSize(mesh.aspect), { width: mesh.width, height: mesh.height });
  const pixels = backdrop.meshPixels({ columns: mesh.columns, rows: mesh.rows, colours: mesh.colours }, mesh.width, mesh.height);
  for (const { x, y, rgb } of mesh.samples) {
    const i = (y * mesh.width + x) * 4;
    rgb.forEach((v, k) => assert.ok(Math.abs(pixels[i + k] - v) <= 1, `mesh pixel ${x},${y} matches the Mac: ${[...pixels.slice(i, i + 3)]} vs ${rgb}`));
  }
}

{
  assert.deepEqual(backdrop.parseBackground("none"), { kind: "none" });
  assert.deepEqual(backdrop.parseBackground("image: art/tide-dark.jpg"), { kind: "image", path: "art/tide-dark.jpg" });
  assert.deepEqual(backdrop.parseBackground("gradient: 90deg, #000000 0%, #ffffff 100%"), {
    kind: "linear",
    angle: 90,
    stops: [{ colour: [0, 0, 0], location: 0 }, { colour: [1, 1, 1], location: 1 }],
  });
  assert.equal(backdrop.parseBackground("mesh: 2x2, #000000, #ffffff, #000000"), null, "a mesh with too few colours is refused");
  assert.equal(backdrop.parseBackground("gradient: sunrise"), null, "the daemon expands presets; the page only draws what it resolved");
  assert.equal(backdrop.parseBackground(undefined), null);

  assert.equal(backdrop.pictureStyle({ kind: "none" }), null);
  assert.equal(backdrop.pictureStyle({ kind: "desktop" }), null, "a browser cannot show the desktop");
  const image = backdrop.pictureStyle({ kind: "image", path: "art/tide dark.jpg" }, "center", "top");
  assert.equal(image.backgroundImage, 'url("/api/v1/ui/background?path=art%2Ftide%20dark.jpg")', "pictures load from the daemon");
  assert.deepEqual([image.backgroundSize, image.backgroundRepeat, image.backgroundPosition], ["auto", "no-repeat", "center top"], "center keeps natural size; top pins it to the top, centred, as the Mac draws it");
  assert.deepEqual(
    Object.fromEntries(["cover", "contain", "tile"].map((fit) => [fit, backdrop.pictureStyle({ kind: "image", path: "a.png" }, fit, "left")]).map(([fit, s]) => [fit, [s.backgroundSize, s.backgroundRepeat, s.backgroundPosition]])),
    { cover: ["cover", "no-repeat", "left center"], contain: ["contain", "no-repeat", "left center"], tile: ["auto", "repeat", "left top"] },
  );
  assert.equal(
    backdrop.pictureStyle({ kind: "linear", angle: 90, stops: [{ colour: [1, 0, 0], location: 0 }, { colour: [0, 0, 1], location: 0.5 }] }).backgroundImage,
    "linear-gradient(90deg, #ff0000 0%, #0000ff 50%)",
    "ui.conf angles are CSS angles: 0deg starts at the bottom, 90deg on the left, as the Mac draws them",
  );
  assert.deepEqual(backdrop.imageRect(400, 300, 1000, 800, "center", "top"), { x: 300, y: 0, width: 400, height: 300 });
  const cover = backdrop.imageRect(400, 300, 1000, 800, "cover", "bottom");
  closeAll([cover.x, cover.y, cover.width, cover.height], [-100 / 3, 0, 3200 / 3, 800], "cover fills the window, centred across it");
  const ninety = backdrop.gradientLine(90, 200, 100);
  closeAll([ninety.x0, ninety.y0, ninety.x1, ninety.y1], [0, 50, 200, 50], "90deg runs left to right");
  const zero = backdrop.gradientLine(0, 200, 100);
  close(zero.y0, 100, "0deg starts at the bottom");
  close(zero.y1, 0, "and ends at the top");

  const flower = new Uint8ClampedArray([10, 10, 10, 255, 10, 10, 10, 255, 250, 240, 230, 255, 10, 10, 10, 255]);
  assert.deepEqual([...backdrop.distinctColours(flower)], [0x0a0a0a, 0xfaf0e6], "every distinct pixel is measured, so a one-pixel highlight counts");
  assert.equal(backdrop.textureStyle("scanlines", 0), null);
  assert.match(backdrop.textureStyle("scanlines", 0.2, 4).backgroundImage, /rgb\(0 0 0 \/ 0\.2\) 0 1px, transparent 1px 4px/);
  assert.deepEqual(backdrop.textureStyle("grain", 0.4), { opacity: "0.2", mixBlendMode: "overlay" });
}

{
  const ink = contrast.luminance([0.93, 0.92, 0.9]);
  const quiet = contrast.luminance([0.75, 0.73, 0.7]);
  const ground = [0.09, 0.094, 0.102];
  for (const [polarity, roles, base, samples] of [
    ["dark", [ink, quiet], ground, [[0.05, 0.05, 0.05], [0.3, 0.6, 0.9], [0.95, 0.9, 0.8]]],
    ["light", [contrast.luminance([0.2, 0.2, 0.2]), contrast.luminance([0.3, 0.3, 0.3])], [0.97, 0.96, 0.95], [[0.8, 0.2, 0.2], [0.1, 0.1, 0.3]]],
  ]) {
    for (const target of [4.5, 7]) {
      const alpha = solve.alphaOver(samples, roles, polarity, target, base);
      const under = (a) => samples.map((c) => c.map((v, i) => v + (base[i] - v) * a));
      const worst = (a) => Math.min(...under(a).flatMap((c) => roles.map((role) => contrast.ratio(role, contrast.luminance(c)))));
      assert.ok(worst(alpha) >= target, `the tint brings every text role to ${target}:1 over every sample (${polarity}: ${worst(alpha)})`);
      assert.ok(alpha === 0 || worst(alpha - 1 / 255) < target + contrast.MARGIN, `and is the least that does (${polarity} ${target})`);
    }
  }
  assert.equal(solve.alphaOver([[0.02, 0.02, 0.02]], [ink], "dark", 4.5, ground), 0, "a dark enough picture needs no tint");
  assert.equal(solve.alphaOver([[0.02, 0.02, 0.02]], [ink], "dark", 4.5, ground, 0.3), 0.3, "and a pane keeps its own opacity as a floor");

  const pack = ([r, g, b]) => (r << 16) | (g << 8) | b;
  const look = { strength: 1, dim: 0, ground, polarity: "dark", texture: "none", textureStrength: 0, roles: [ink, quiet], target: 4.5 };
  const same = (colours, overrides, expected, message) =>
    assert.equal(solve.alphaForColours(Uint32Array.from(colours.map(pack)), { ...look, ...overrides }, overrides.ground || ground), solve.alphaOver(expected, overrides.roles || look.roles, overrides.polarity || look.polarity, look.target, overrides.ground || ground), message);
  same([[255, 255, 255]], { strength: 0.5, dim: 0.25 }, [ground.map((g) => (g + (1 - g) * 0.5) * 0.75)], "strength blends over the ground, then dim darkens");
  const lightRoles = [contrast.luminance([0.2, 0.2, 0.2])];
  same([[0, 0, 0]], { dim: 0.5, polarity: "light", ground: [1, 1, 1], roles: lightRoles }, [[0.5, 0.5, 0.5]], "dim lightens in light");
  const c = [51, 153, 204].map((v) => v / 255);
  same([[51, 153, 204]], { texture: "grain", textureStrength: 0.4 }, [c, c.map((v) => v + (Math.min(2 * v, 1) - v) * 0.2)], "grain's brightest overlay pixel is measured for light text");
  same([[51, 153, 204]], { texture: "grain", textureStrength: 0.4, polarity: "light", ground: [1, 1, 1], roles: lightRoles }, [c, c.map((v) => v + (Math.max(2 * v - 1, 0) - v) * 0.2)], "and its darkest for dark text");
  same([[128, 128, 128]], { texture: "scanlines", textureStrength: 0.2 }, [[128 / 255, 128 / 255, 128 / 255]], "scanlines only darken, which never hurts light text");
  same([[128, 128, 128]], { texture: "scanlines", textureStrength: 0.2, polarity: "light", ground: [1, 1, 1], roles: lightRoles }, [[128 / 255, 128 / 255, 128 / 255], [0.8 * 128 / 255, 0.8 * 128 / 255, 0.8 * 128 / 255]], "but they are measured for dark text");

  const many = new Uint32Array(1 << 20);
  for (let i = 0; i < many.length; i++) many[i] = (i * 16 + (i >> 4)) & 0xffffff;
  const before = process.memoryUsage().heapUsed;
  const started = performance.now();
  const alpha = solve.alphaForColours(many, look, ground);
  const elapsed = performance.now() - started;
  const grown = process.memoryUsage().heapUsed - before;
  let expected = 0;
  for (let i = 0; i < many.length; i += 8192) {
    const chunk = [...many.subarray(i, i + 8192)].map((n) => [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((v) => v / 255));
    expected = Math.max(expected, solve.alphaOver(chunk, look.roles, "dark", 4.5, ground));
  }
  assert.equal(alpha, expected, "a million distinct colours give the same tint as measuring each one separately");
  assert.ok(elapsed < 2000, `in bounded time (${elapsed.toFixed(0)} ms)`);
  assert.ok(grown < 16 * 1024 * 1024, `without a heap object per colour (${(grown / 1048576).toFixed(1)} MiB)`);
}

{
  const colours = { "var(--paper)": [0.09, 0.094, 0.102], "var(--ink)": [0.93, 0.92, 0.9], "var(--muted)": [0.75, 0.73, 0.7], "var(--quiet)": [0.6, 0.58, 0.56] };
  const values = {
    background: "image: art/tide-dark.jpg",
    "background.light": "image: art/tide-light.jpg",
    "background.opacity": 1,
    "background.session-opacity": 0.4,
    "background.dim": 0.2,
    "background.fit": "cover",
    "background.position": "center",
    texture: "grain",
    "texture.strength": 0.3,
    "texture.spacing": 3,
    "contrast.target": 4.5,
    "glass.readability": "auto",
  };
  const base = { sessionOpen: false, highContrast: false, colourOf: (v) => colours[v], width: 1440, height: 900 };
  const look = backdropLook(values, "dark", base);
  assert.deepEqual(look.spec, { kind: "image", path: "art/tide-dark.jpg" });
  assert.equal(look.strength, 1, "empty views use background.opacity");
  assert.equal(look.scale, 1);
  assert.equal(backdropLook(values, "dark", { ...base, scale: 2 }).scale, 2, "a high-density display is sampled at its own pixels");
  assert.equal(look.roles.length, 3, "every text role is measured");
  assert.equal(backdropLook(values, "dark", { ...base, sessionOpen: true }).strength, 0.4, "an open session uses background.session-opacity");
  assert.equal(backdropLook(values, "light", base).spec.path, "art/tide-light.jpg", "light draws background.light");
  assert.equal(backdropLook({ ...values, "glass.readability": "off" }, "dark", base).readable, false, "readability off uses the settings exactly");
  const more = backdropLook({ ...values, "glass.readability": "off" }, "dark", { ...base, highContrast: true });
  assert.deepEqual([more.readable, more.target], [true, 7], "increased contrast forces readability at 7:1");
}

{
  const doc = fakeDocument();
  const attributes = new Set();
  doc.documentElement.hasAttribute = (name) => attributes.has(name);
  doc.documentElement.dataset = {};
  const renders = [];
  let observer = null;
  class FakeObserver {
    constructor(callback) {
      this.callback = callback;
      observer = this;
    }
    observe(target, options) {
      this.options = options;
    }
  }
  const resizes = [];
  class Silent {
    addEventListener() {}
  }
  const values = { theme: "dark", "theme.accent": "system", background: "gradient: 90deg, #000000 0%, #ffffff 100%", "background.opacity": 1, "background.session-opacity": 0.3 };
  const view = start({
    document: doc,
    fetch: async () => ({ ok: true, json: async () => ({ values }) }),
    EventSource: Silent,
    matchMedia: (query) => ({ matches: query.includes("dark") }),
    storage: null,
    colourOf: colourOf(doc),
    backdrop: { render: async (look) => renders.push(look) },
    viewport: () => ({ width: 800, height: 600 }),
    MutationObserver: FakeObserver,
    addEventListener: (type, handler) => resizes.push([type, handler]),
    debounce: 1,
  });
  await view.ready;
  const last = renders.at(-1);
  assert.equal(last.spec.kind, "linear", "the page draws the background the daemon resolved");
  assert.deepEqual([last.width, last.height, last.strength, last.target], [800, 600, 1, 4.5]);
  assert.deepEqual(observer.options.attributeFilter, ["data-session-open"], "opening a session redraws");
  attributes.add("data-session-open");
  observer.callback();
  assert.equal(renders.at(-1).strength, 0.3, "at the session strength");
  assert.equal(resizes[0][0], "resize", "and resizing the window remeasures");
}

{
  const quiet = [0.6, 0.58, 0.56];
  const paper = [0.09, 0.094, 0.102];
  assert.equal(contrast.readableText(quiet, [paper], true, 4.5), quiet, "text that already reads keeps its colour");
  const lifted = contrast.readableText(quiet, [paper], true, 7);
  assert.ok(textRatio(lifted, paper) >= 7, "text that misses the target is lifted until it reads");
  assert.ok(lifted.every((v) => Math.round(v * 255) === v * 255), "in whole 8-bit steps, so the page draws what was measured");
  const less = lifted.map((v, i) => Math.max(v - 2 / 255, quiet[i]));
  assert.ok(textRatio(less, paper) < 7, "and no further than it needs");
}

for (const [name, set] of [["dark", dark], ["light", light]]) {
  const over = (token, under) => token.colour.map((v, i) => Math.round((v * token.alpha + under[i] * (1 - token.alpha)) * 255) / 255);
  const parse = (value) => {
    const rgb = /^rgb\((\d+) (\d+) (\d+)/.exec(value);
    if (rgb) return { colour: rgb.slice(1, 4).map((v) => +v / 255), alpha: 1 };
    const token = /^var\(--([a-z-]+)\)$/.exec(value);
    return token ? set[token[1]] : null;
  };
  const doc = fakeDocument();
  const colourOf = (value, under) => {
    const name = /^var\((--[a-z-]+)\)$/.exec(value)?.[1];
    const top = parse(doc.documentElement.style.properties.get(name) ?? value);
    if (!top) return null;
    const ground = under ? parse(under) : null;
    return ground ? over(top, ground.colour) : top.colour;
  };
  for (const [why, values, highContrast] of [
    ["contrast.target = 7", { theme: name, "contrast.target": 7 }, false],
    ["the browser's increased contrast", { theme: name, "contrast.target": 4.5 }, true],
  ]) {
    const target = effectiveTarget(values, highContrast);
    applyTheme(doc, values, name === "dark", colourOf, target);
    const roles = ["--ink", "--muted", "--quiet"].map((token) => colourOf(`var(${token})`));
    const composer = over(set.composer, set.paper.colour);
    const surfaces = [set.paper, set.panel, set.raised, set.hover, set.code].map((t) => t.colour).concat([
      composer,
      over(set.glass, set.paper.colour),
      over(set["accent-soft"], set.paper.colour),
      over(set["accent-soft"], set.panel.colour),
      over(set["accent-soft"], composer),
    ]);
    for (const role of roles) for (const surface of surfaces) {
      assert.ok(textRatio(role, surface) >= 7, `${name}, ${why}: every text role reads at 7:1 on every surface (${textRatio(role, surface).toFixed(2)})`);
    }
    const look = backdropLook(values, name, { sessionOpen: false, highContrast, colourOf, width: 1440, height: 900 });
    assert.equal(look.target, 7);
    const samples = [[1, 1, 1], [0, 0, 0], [0.9, 0.3, 0.2], [0.2, 0.4, 0.9]];
    const alpha = solve.alphaOver(samples, look.roles, look.polarity, look.target, look.ground);
    assert.ok(alpha < 1, `${name}, ${why}: a ground tint reaches 7:1 before it is solid`);
    for (const sample of samples) {
      const shown = sample.map((v, i) => v + (look.ground[i] - v) * alpha);
      for (const role of look.roles) {
        const r = contrast.ratio(role, contrast.luminance(shown));
        assert.ok(r >= 7, `${name}, ${why}: every role reads at 7:1 over the background (${r.toFixed(2)})`);
      }
    }
  }
  applyTheme(doc, { theme: name }, name === "dark", colourOf, 4.5);
  assert.equal(doc.documentElement.style.properties.has("--quiet"), false, `${name}: at 4.5 the palette is used as it is`);
}

{
  const made = [];
  let reads = 0;
  let bright = [255, 255, 255];
  const makeCanvas = (width, height) => {
    made.push([width, height]);
    return {
      getContext: () => ({
        fillRect() {},
        drawImage() {},
        putImageData() {},
        createLinearGradient: () => ({ addColorStop() {} }),
        createImageData: (w, h) => ({ data: new Uint8ClampedArray(w * h * 4) }),
        getImageData: (x, y, w, h) => {
          reads++;
          const data = new Uint8ClampedArray(w * h * 4).fill(20);
          data.set([...bright, 255], 0);
          return { data };
        },
      }),
    };
  };
  let loads = 0;
  const sampler = createSampler({ makeCanvas, loadImage: async (url) => (loads++, url.includes("missing") ? null : { width: 400, height: 300 }) });
  const ink = contrast.luminance([0.93, 0.92, 0.9]);
  const look = {
    spec: { kind: "image", path: "art/field-dark.jpg" },
    fit: "cover",
    position: "center",
    width: 800,
    height: 600,
    scale: 2,
    strength: 1,
    dim: 0,
    ground: [0.09, 0.094, 0.102],
    polarity: "dark",
    texture: "none",
    textureStrength: 0,
    roles: [ink],
    target: 4.5,
    glass: { tint: [0.09, 0.094, 0.102], panes: { content: 0, chrome: 0 } },
  };
  const full = (await sampler.measure(look)).panes.content;
  assert.ok(full > 0, "a bright pixel needs a tint");
  assert.deepEqual(made[0], [1600, 1200], "a high-density display is sampled at its own pixels");
  assert.deepEqual(sampleSize({ width: 390, height: 844, scale: 3 }), { width: 1170, height: 2532 });
  assert.equal((await sampler.measure(look)).panes.content, full);
  assert.deepEqual([reads, loads], [1, 1], "the same background, size and ground are drawn once");
  const softer = (await sampler.measure({ ...look, strength: 0.3, texture: "grain", textureStrength: 0.2, target: 7 })).panes.content;
  assert.ok(softer < full, "strength, texture and target re-solve from the cached pixels");
  assert.equal(reads, 1, "without drawing again");
  const placed = (await sampler.measure({ ...look, areas: { content: [[0, 0, 800, 600]], chrome: [[100, 100, 50, 50]] } })).panes;
  assert.deepEqual(placed, { content: full, chrome: 0 }, "each kind of pane is solved over the art under its own areas only");
  assert.deepEqual((await sampler.measure({ ...look, areas: { content: [], chrome: [] } })).panes, { content: 0, chrome: 0 }, "a kind with nothing on screen keeps its setting");
  assert.equal(reads, 1, "areas re-solve from the cached pixels");
  bright = [10, 10, 10];
  assert.equal((await sampler.measure({ ...look, width: 801 })).panes.content, 0, "a new size draws again");
  assert.equal(reads, 2);
  assert.equal(await sampler.measure({ ...look, spec: { kind: "image", path: "missing.png" } }), null, "a picture that fails to load gives no measurement");
  assert.equal(await sampler.measure({ ...look, spec: { kind: "none" } }), null);
}

{
  const listeners = {};
  const posted = [];
  const worker = { addEventListener: (type, fn) => (listeners[type] = fn), postMessage: (message) => posted.push(message) };
  const measure = workerMeasure(worker);
  const first = measure({ n: 1 });
  const second = measure({ n: 2 });
  listeners.message({ data: { id: posted[1].id, alpha: 0.4 } });
  listeners.message({ data: { id: posted[0].id, alpha: 0.7 } });
  assert.deepEqual([await first, await second], [0.7, 0.4], "answers are matched to their requests");
  const pending = measure({ n: 3 });
  listeners.error(new Event("error"));
  assert.equal(await pending, null, "a worker that fails answers nothing");
  assert.equal(await measure({ n: 4 }), null, "and later requests do not wait on it");
}

{
  const element = () => {
    const node = { style: {}, children: [], className: "", setAttribute() {}, append: (child) => node.children.push(child) };
    return node;
  };
  const attributes = new Map();
  const properties = {};
  const document = {
    createElement: element,
    body: { prepend() {} },
    documentElement: {
      style: { setProperty: (name, value) => (properties[name] = value), removeProperty: (name) => delete properties[name] },
      toggleAttribute: (name, on) => (on ? attributes.set(name, "") : attributes.delete(name)),
      setAttribute: (name, value) => attributes.set(name, value),
      removeAttribute: (name) => attributes.delete(name),
    },
  };
  const answers = [];
  const view = backdrop.createBackdrop({ document, measure: () => new Promise((resolve, reject) => answers.push({ resolve, reject })) });
  const glass = { on: true, tint: [0.1, 0.1, 0.1], edge: [1, 1, 1], panes: { content: 0.18, sidebar: 0.45, chrome: 0, float: 0.5 }, blur: 30, border: 0.08 };
  const look = { spec: { kind: "linear", angle: 90, stops: [{ colour: [0, 0, 0], location: 0 }, { colour: [1, 1, 1], location: 1 }] }, fit: "cover", position: "center", strength: 1, dim: 0, texture: "none", textureStrength: 0, textureSpacing: 3, polarity: "dark", ground: [0.1, 0.1, 0.1], readable: true, gutter: 12, width: 800, height: 600, glass };
  assert.deepEqual(Object.keys(view.layers), ["picture", "dim", "texture"], "no layer covers the whole background: panes carry the text");
  const older = view.render(look);
  const newer = view.render(look);
  assert.equal(properties["--glass-sidebar"], "0.45", "configured opacities apply at once");
  assert.deepEqual([properties["--glass-tint"], properties["--glass-blur"], properties["--pane-gutter"]], ["26 26 26", "30px", "12px"]);
  assert.equal(attributes.get("data-panes"), "glass");
  answers[1].resolve({ panes: { content: 0.6, sidebar: 0.45, chrome: 0.2, float: 0.5 } });
  answers[0].resolve({ panes: { content: 0.9, sidebar: 0.9, chrome: 0.9, float: 0.9 } });
  assert.deepEqual([await older, (await newer).panes.content], [null, 0.6]);
  assert.deepEqual([properties["--glass-content"], properties["--glass-chrome"]], ["0.6", "0.2"], "panes rise where text needs them, and a slower, older measurement never replaces a newer one");
  const failed = view.render(look);
  answers[2].resolve(null);
  await failed;
  assert.deepEqual(["content", "sidebar", "chrome", "float"].map((kind) => properties[`--glass-${kind}`]), ["1", "1", "1", "1"], "with no measurement, panes are drawn solid so text stays readable");
  const thrown = view.render(look);
  answers[3].reject(new Error("gone"));
  await thrown;
  assert.equal(properties["--glass-content"], "1");
  assert.equal(attributes.has("data-backdrop"), true);

  const noGlass = () => Object.keys(properties).every((name) => !name.startsWith("--glass-"));
  await view.render({ ...look, polarity: "light", glass: { ...glass, on: false } });
  assert.equal(answers.length, 4, "solid panes need no measurement");
  assert.ok(attributes.get("data-panes") === "solid" && properties["--pane-gutter"] === "12px" && noGlass(), "solid panes keep the gutter and leave no glass state behind");
  view.render(look);
  view.render({ ...look, spec: { kind: "none" } });
  assert.ok(!attributes.has("data-panes") && !attributes.has("data-backdrop") && !("--pane-gutter" in properties) && noGlass(), "removing the background removes the panes");
}

{
  const elements = [
    { pane: "chrome", rect: { left: 12, right: 1428, top: 12, bottom: 88, width: 1416, height: 76 }, position: "sticky" },
    { pane: "content", rect: { left: 12, right: 700, top: 400, bottom: 2400, width: 688, height: 2000 }, position: "static" },
    { pane: "content", rect: { left: 712, right: 1600, top: -50, bottom: 300, width: 888, height: 350 }, position: "static" },
    { pane: "float", rect: { left: 0, right: 0, top: 0, bottom: 0, width: 0, height: 0 }, position: "static" },
    { pane: "sidebar", rect: { left: 12, right: 352, top: 100, bottom: 888, width: 340, height: 788 }, position: "static", parent: "sticky" },
  ].map(({ pane, rect, position, parent }) => {
    const parentElement = parent ? { nodeType: 1, position: parent, parentElement: null } : null;
    return { nodeType: 1, dataset: { pane }, position, parentElement, getBoundingClientRect: () => rect };
  });
  const document = { querySelectorAll: () => elements };
  const view = { innerWidth: 1440, innerHeight: 900, getComputedStyle: (node) => ({ position: node.position }) };
  assert.deepEqual(paneAreas(document, view), {
    chrome: [[12, 12, 1416, 76]],
    sidebar: [[12, 100, 340, 788]],
    content: [[12, 0, 688, 900], [712, 0, 728, 900]],
    float: [],
  }, "a pinned pane covers its own box; a scrolling pane covers its whole column, clipped to the window; an empty one covers nothing");
}

{
  const doc = fakeDocument();
  doc.documentElement.hasAttribute = () => false;
  const windowListeners = [];
  const renders = [];
  const settled = new Set();
  doc.documentElement.toggleAttribute = (name, on) => (on ? settled.add(name) : settled.delete(name));
  let layout = { content: [[0, 0, 800, 600]] };
  let resized = null;
  class Silent {
    addEventListener() {}
  }
  start({
    document: doc,
    fetch: async () => ({ ok: true, json: async () => ({ values: { theme: "dark", background: "gradient: 90deg, #000000 0%, #ffffff 100%" } }) }),
    EventSource: Silent,
    matchMedia: () => ({ matches: false }),
    storage: null,
    colourOf: colourOf(doc),
    backdrop: { render: async (look) => renders.push(look) },
    viewport: () => ({ width: 800, height: 600, scale: 1 }),
    areas: () => layout,
    MutationObserver: class {
      observe() {}
    },
    ResizeObserver: class {
      constructor(callback) {
        resized = callback;
      }
      observe() {}
    },
    addEventListener: (type, handler) => windowListeners.push([type, handler]),
    debounce: 1,
  });
  await new Promise((resolve) => setTimeout(resolve, 10));
  const count = renders.length;
  assert.deepEqual(renders.at(-1).areas, layout, "the look carries where each kind of pane sits");
  assert.ok(settled.has("data-ui-settled"), "the page marks itself settled once loading and measuring are done");
  assert.deepEqual(windowListeners.map(([type]) => type), ["resize", "hashchange"], "the page listens for resizing and view changes, never scrolling, so scrolling the chat never remeasures");
  windowListeners[0][1]();
  windowListeners[0][1]();
  assert.ok(!settled.has("data-ui-settled"), "and is not settled while a remeasure is waiting");
  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(renders.length, count + 1, "a burst of resizes remeasures once");
  resized();
  windowListeners[1][1]();
  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(renders.length, count + 1, "content that grows without moving a pane remeasures nothing");
  layout = { content: [[0, 0, 400, 600]] };
  resized();
  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(renders.length, count + 2, "a pane that moves remeasures");
  assert.ok(settled.has("data-ui-settled"));
}

{
  const ground = [0.97, 0.96, 0.95];
  const texts = [[0.2, 0.2, 0.2], [0.35, 0.34, 0.33], [0.42, 0.41, 0.4]];
  const navy = [0.1, 0.15, 0.4];
  const lifted = contrast.readableSurface(navy, texts, ground, 4.5);
  for (const text of texts) assert.ok(textRatio(text, lifted) >= 4.5, "a tint too dark for dark text is moved toward the ground until every role reads on it");
  assert.equal(contrast.readableSurface(ground, texts, ground, 4.5), ground, "a tint that already carries the text is kept");

  const roles = texts.map(contrast.luminance);
  const colours = Uint32Array.from([0xffffff, 0xeeddcc, 0x203040, 0x808080]);
  const look = { strength: 1, dim: 0, ground, polarity: "light", texture: "none", textureStrength: 0, roles, target: 4.5 };
  const floors = { content: 0.18, sidebar: 0.45, chrome: 0, float: 0.5 };
  const readable = Uint32Array.from([0xffffff, 0xf8f8f8]);
  const everywhere = { content: colours, sidebar: colours, chrome: colours, float: colours };
  assert.deepEqual(solve.paneAlphas({ content: readable, sidebar: readable, chrome: readable, float: readable }, { ...look, glass: { tint: ground, panes: floors } }), floors, "a ground tint over a readable background needs nothing beyond its setting");
  const tint = contrast.readableSurface([0.75, 0.55, 0.5], texts, ground, 4.5 + contrast.MARGIN);
  const panes = solve.paneAlphas(everywhere, { ...look, glass: { tint, panes: floors } });
  assert.ok(panes.chrome > 0 && panes.content > floors.content, "over a background that misses the target, a pane's tint rises above its setting");
  assert.equal(solve.paneAlphas({ content: colours }, { ...look, glass: { tint, panes: floors } }).chrome, floors.chrome, "a kind not on screen keeps its setting");
  for (const [pane, alpha] of Object.entries(panes)) {
    assert.ok(alpha >= floors[pane], `${pane} never drops below its setting`);
    const shown = (a) => [...colours].map((n) => [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((v, i) => v / 255 + (tint[i] - v / 255) * a));
    const worst = (a) => Math.min(...shown(a).flatMap((c) => roles.map((role) => contrast.ratio(role, contrast.luminance(c)))));
    assert.ok(worst(alpha) >= 4.5, `${pane}: every role reads on the tinted pane (${worst(alpha).toFixed(2)})`);
    assert.ok(alpha === floors[pane] || worst(alpha - 1 / 255) < 4.5 + contrast.MARGIN, `${pane}: and the tint rises no further than it needs`);
  }
}

{
  const colours = { "var(--paper)": [0.09, 0.094, 0.102], "var(--ink)": [0.93, 0.92, 0.9], "var(--muted)": [0.75, 0.73, 0.7], "var(--quiet)": [0.6, 0.58, 0.56], "var(--accent)": [0.95, 0.45, 0.3] };
  const base = { sessionOpen: false, highContrast: false, reducedTransparency: false, colourOf: (v) => colours[v], width: 1440, height: 900 };
  const values = { background: "none", glass: "on", "glass.tint": "ground", "glass.opacity": 0.18, "glass.sidebar.opacity": 0.45, "glass.chrome.opacity": 0, "glass.composer.opacity": 0.1, "glass.blur": 30, "glass.border": 0.08, "glass.gutter": 8 };
  const look = backdropLook(values, "dark", base).glass;
  assert.equal(look.on, true);
  assert.equal(backdropLook(values, "light", base).glass.on, false, "light draws solid panes, as on the Mac");
  assert.equal(backdropLook(values, "light", base).gutter, 8, "with the same gutter around them");
  assert.deepEqual(look.tint, colours["var(--paper)"], "the ground tint is the page's own ground");
  assert.deepEqual(look.panes, { content: 0.18, sidebar: 0.45, chrome: 0, float: 0.2 }, "floating glass keeps at least 0.2 while readability is on");
  assert.equal(backdropLook({ ...values, "glass.readability": "off" }, "dark", base).glass.panes.float, 0.1, "readability off uses the settings exactly");
  assert.equal(backdropLook({ ...values, glass: "off" }, "dark", base).glass.on, false);
  assert.equal(backdropLook(values, "dark", { ...base, reducedTransparency: true }).glass.on, false, "reduced transparency draws solid panes");
  const accent = backdropLook({ ...values, "glass.tint": "accent" }, "dark", base).glass.tint;
  for (const token of ["var(--ink)", "var(--muted)", "var(--quiet)"]) assert.ok(textRatio(colours[token], accent) >= 4.5 + contrast.MARGIN, "an accent tint is darkened toward the ground until light text reads on it, with the solver's margin");
  assert.deepEqual(backdropLook({ ...values, "glass.tint": "#000000" }, "dark", base).glass.tint, [0, 0, 0], "a colour that carries the text is used as it is");
}

console.log("dashboard ui ok");
