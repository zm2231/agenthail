import { blendFloor, boundary, gamma, linear, passes, reachable } from "./ui-contrast.js";

const MESH_WIDTH = 192;

function hexColour(value) {
  const match = /^#([0-9a-f]{6})$/i.exec(value || "");
  if (!match) return null;
  const n = parseInt(match[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((v) => v / 255);
}

export function parseBackground(value) {
  if (value === "none") return { kind: "none" };
  if (value === "desktop") return { kind: "desktop" };
  const colon = (value || "").indexOf(":");
  if (colon < 0) return null;
  const kind = value.slice(0, colon).trim();
  const spec = value.slice(colon + 1).trim();
  const parts = spec.split(",").map((part) => part.trim()).filter(Boolean);
  if (kind === "image") return spec ? { kind: "image", path: spec } : null;
  if (kind === "gradient") {
    if (parts.length < 3 || !parts[0].endsWith("deg")) return null;
    const angle = Number(parts[0].slice(0, -3));
    if (!Number.isFinite(angle)) return null;
    const stops = [];
    for (const part of parts.slice(1)) {
      const fields = part.split(" ").filter(Boolean);
      const colour = hexColour(fields[0]);
      if (fields.length !== 2 || !colour || !fields[1].endsWith("%")) return null;
      const location = Number(fields[1].slice(0, -1));
      if (!Number.isFinite(location)) return null;
      stops.push({ colour, location: location / 100 });
    }
    return { kind: "linear", angle, stops };
  }
  if (kind === "mesh") {
    const dims = (parts[0] || "").split("x").map(Number);
    if (dims.length !== 2 || !dims.every(Number.isInteger) || dims[0] < 2 || dims[1] < 2) return null;
    const colours = parts.slice(1).map(hexColour);
    if (colours.length !== dims[0] * dims[1] || colours.some((c) => !c)) return null;
    return { kind: "mesh", columns: dims[0], rows: dims[1], colours };
  }
  return null;
}

export function backgroundURL(path) {
  return `/api/v1/ui/background?path=${encodeURIComponent(path)}`;
}

const FIT = {
  cover: ["cover", "no-repeat"],
  contain: ["contain", "no-repeat"],
  center: ["auto", "no-repeat"],
  tile: ["auto", "repeat"],
};
const POSITION = { center: "center", top: "center top", bottom: "center bottom", left: "left center", right: "right center" };

const REGION_PROPERTIES = ["--glass-alpha", "--scrim-tint", "--scrim-alpha"];
const GLASS_PROPERTIES = ["--glass-tint", "--glass-edge", "--glass-blur", "--glass-border", "--glass-gutter", "--glass-content", "--glass-sidebar", "--glass-chrome", "--glass-float"];

function channels(colour) {
  return colour.map((v) => Math.round(v * 255)).join(" ");
}

export function hex(colour) {
  return `#${colour.map((v) => Math.round(v * 255).toString(16).padStart(2, "0")).join("")}`;
}

export function pictureStyle(spec, fit, position, meshURL) {
  if (!spec || spec.kind === "none" || spec.kind === "desktop") return null;
  if (spec.kind === "image") {
    const [size, repeat] = FIT[fit] || FIT.cover;
    return {
      backgroundImage: `url("${backgroundURL(spec.path)}")`,
      backgroundSize: size,
      backgroundRepeat: repeat,
      backgroundPosition: fit === "tile" ? "left top" : POSITION[position] || "center",
    };
  }
  if (spec.kind === "linear") {
    const stops = spec.stops.map((stop) => `${hex(stop.colour)} ${+(stop.location * 100).toFixed(4)}%`).join(", ");
    return { backgroundImage: `linear-gradient(${spec.angle}deg, ${stops})`, backgroundSize: "100% 100%", backgroundRepeat: "no-repeat", backgroundPosition: "center" };
  }
  return { backgroundImage: `url("${meshURL}")`, backgroundSize: "100% 100%", backgroundRepeat: "no-repeat", backgroundPosition: "center" };
}

export function meshSize(aspect) {
  return { width: MESH_WIDTH, height: Math.max(Math.floor(MESH_WIDTH / Math.max(aspect, 0.1)), 2) };
}

export function meshPixels({ columns, rows, colours }, width, height) {
  const out = new Uint8ClampedArray(width * height * 4);
  const smooth = (t) => t * t * (3 - 2 * t);
  const lin = colours.map((c) => c.map(linear));
  for (let y = 0; y < height; y++) {
    const fy = (y / (height - 1)) * (rows - 1);
    const r0 = Math.min(Math.floor(fy), rows - 2);
    const ty = smooth(fy - r0);
    for (let x = 0; x < width; x++) {
      const fx = (x / (width - 1)) * (columns - 1);
      const c0 = Math.min(Math.floor(fx), columns - 2);
      const tx = smooth(fx - c0);
      const at = (c, r) => lin[r * columns + c];
      const i = (y * width + x) * 4;
      for (let k = 0; k < 3; k++) {
        const top = at(c0, r0)[k] * (1 - tx) + at(c0 + 1, r0)[k] * tx;
        const bottom = at(c0, r0 + 1)[k] * (1 - tx) + at(c0 + 1, r0 + 1)[k] * tx;
        out[i + k] = Math.round(gamma(top * (1 - ty) + bottom * ty) * 255);
      }
      out[i + 3] = 255;
    }
  }
  return out;
}

export function grainPixels() {
  const size = 256;
  const out = new Uint8Array(size * size);
  let seed = 0x9e3779b9;
  for (let i = 0; i < size * size; i++) {
    seed = (seed ^ (seed << 13)) >>> 0;
    seed = (seed ^ (seed >>> 17)) >>> 0;
    seed = (seed ^ (seed << 5)) >>> 0;
    out[i] = seed & 255;
  }
  return out;
}

export function textureStyle(texture, strength, spacing) {
  if (!(strength > 0)) return null;
  if (texture === "scanlines") {
    return { backgroundImage: `repeating-linear-gradient(to bottom, rgb(0 0 0 / ${strength}) 0 1px, transparent 1px ${spacing}px)`, opacity: "1", mixBlendMode: "normal" };
  }
  if (texture === "grain") return { opacity: String(strength * 0.5), mixBlendMode: "overlay" };
  return null;
}

function solveScrim(visit, roles, polarity, target, tint, start = 0) {
  const limit = boundary(roles, polarity, target);
  const [tr, tg, tb] = tint;
  const colour = [0, 0, 0];
  let alpha = start;
  if (alpha >= 1) return 1;
  visit((r, g, b) => {
    const shown = 0.2126 * linear(r + (tr - r) * alpha) + 0.7152 * linear(g + (tg - g) * alpha) + 0.0722 * linear(b + (tb - b) * alpha);
    if (passes(shown, limit, polarity)) return true;
    colour[0] = r;
    colour[1] = g;
    colour[2] = b;
    alpha = Math.max(alpha, blendFloor(colour, tint, polarity, limit));
    return alpha < 1;
  });
  return alpha;
}

export function scrimOver(samples, roles, polarity, target, tint, start = 0) {
  return solveScrim((check) => {
    for (const [r, g, b] of samples) if (!check(r, g, b)) return;
  }, roles, polarity, target, tint, start);
}

function shownChannel({ strength, dim, ground, polarity }) {
  const veil = polarity === "dark" ? 0 : 1;
  return (v, i) => {
    const over = ground[i] + (v - ground[i]) * strength;
    return over + (veil - over) * dim;
  };
}

function visitShown(colours, look) {
  const { polarity, texture, textureStrength } = look;
  const shown = shownChannel(look);
  const grain = texture === "grain" && textureStrength > 0 ? textureStrength * 0.5 : 0;
  const lines = texture === "scanlines" && textureStrength > 0 && polarity === "light" ? textureStrength : 0;
  const lift = polarity === "light" ? (v) => v + (Math.max(2 * v - 1, 0) - v) * grain : (v) => v + (Math.min(2 * v, 1) - v) * grain;
  return (check) => {
    for (let k = 0; k < colours.length; k++) {
      const n = colours[k];
      const r = shown(((n >> 16) & 255) / 255, 0);
      const g = shown(((n >> 8) & 255) / 255, 1);
      const b = shown((n & 255) / 255, 2);
      if (!check(r, g, b)) return;
      if (grain && !check(lift(r), lift(g), lift(b))) return;
      if (lines && !check(r * (1 - lines), g * (1 - lines), b * (1 - lines))) return;
    }
  };
}

export function regionColours(data, width, [x, y, w, h], seen) {
  const out = new Uint32Array(Math.min(w * h, 1 << 24));
  let count = 0;
  let r = 0;
  let g = 0;
  let b = 0;
  for (let row = y; row < y + h; row++) {
    for (let i = (row * width + x) * 4, end = i + w * 4; i < end; i += 4) {
      r += data[i];
      g += data[i + 1];
      b += data[i + 2];
      const n = (data[i] << 16) | (data[i + 1] << 8) | data[i + 2];
      const bit = 1 << (n & 7);
      if (seen[n >> 3] & bit) continue;
      seen[n >> 3] |= bit;
      out[count++] = n;
    }
  }
  for (let k = 0; k < count; k++) seen[out[k] >> 3] = 0;
  const pixels = Math.max(w * h, 1) * 255;
  return { colours: out.slice(0, count), mean: [r / pixels, g / pixels, b / pixels] };
}

export function solveRegions(regions, look) {
  const { roles, polarity, target, glass } = look;
  const limit = boundary(roles, polarity, target);
  const shown = shownChannel(look);
  const out = {};
  for (const region of regions) {
    const visit = visitShown(region.colours, look);
    if (region.kind === "pane") {
      if (glass?.on) out[region.id] = { alpha: solveScrim(visit, roles, polarity, target, glass.tint, glass.panes[region.pane] ?? 0) };
      continue;
    }
    const base = Array.isArray(look.scrimTint) ? look.scrimTint : region.mean.map(shown);
    const tint = reachable(base, polarity, limit);
    out[region.id] = { tint, alpha: solveScrim(visit, roles, polarity, target, tint) };
  }
  return out;
}

export function imageRect(imageWidth, imageHeight, width, height, fit, position) {
  const scale = fit === "contain" ? Math.min(width / imageWidth, height / imageHeight) : fit === "center" ? 1 : Math.max(width / imageWidth, height / imageHeight);
  const w = imageWidth * scale;
  const h = imageHeight * scale;
  const ax = position === "left" ? 0 : position === "right" ? 1 : 0.5;
  const ay = position === "top" ? 0 : position === "bottom" ? 1 : 0.5;
  return { x: (width - w) * ax, y: (height - h) * ay, width: w, height: h };
}

export function gradientLine(angle, width, height) {
  const radians = (angle * Math.PI) / 180;
  const dx = Math.sin(radians);
  const dy = -Math.cos(radians);
  const length = Math.abs(width * dx) + Math.abs(height * dy);
  const cx = width / 2;
  const cy = height / 2;
  return { x0: cx - (dx * length) / 2, y0: cy - (dy * length) / 2, x1: cx + (dx * length) / 2, y1: cy + (dy * length) / 2 };
}

function canvas(document, width, height) {
  const element = document.createElement("canvas");
  element.width = width;
  element.height = height;
  return element;
}

export function createBackdrop({ document, measure }) {
  const root = document.createElement("div");
  root.className = "ui-backdrop";
  root.setAttribute("aria-hidden", "true");
  const layers = {};
  for (const name of ["picture", "dim", "texture"]) {
    layers[name] = document.createElement("div");
    layers[name].className = `ui-backdrop-${name}`;
    root.append(layers[name]);
  }
  document.body.prepend(root);
  let grainURL = null;
  let generation = 0;

  const grain = () => {
    if (grainURL) return grainURL;
    const element = canvas(document, 256, 256);
    const context = element.getContext("2d");
    const image = context.createImageData(256, 256);
    grainPixels().forEach((v, i) => image.data.set([v, v, v, 255], i * 4));
    context.putImageData(image, 0, 0);
    grainURL = element.toDataURL();
    return grainURL;
  };

  const meshURL = (spec, width, height) => {
    const size = meshSize(width / Math.max(height, 1));
    const element = canvas(document, size.width, size.height);
    const context = element.getContext("2d");
    const image = context.createImageData(size.width, size.height);
    image.data.set(meshPixels(spec, size.width, size.height));
    context.putImageData(image, 0, 0);
    return element.toDataURL();
  };

  const html = document.documentElement;
  const styled = new Set();
  const keepOnly = (keep) => {
    for (const element of styled) {
      if (keep.has(element)) continue;
      for (const name of REGION_PROPERTIES) element.style.removeProperty(name);
      styled.delete(element);
    }
  };
  const fallback = (look) =>
    Object.fromEntries(look.regions.filter((region) => region.kind === "text" || look.glass?.on).map((region) => [region.id, region.kind === "pane" ? { alpha: 1 } : { tint: look.ground, alpha: 1 }]));

  const setPanes = (panes) => {
    for (const [pane, alpha] of Object.entries(panes)) html.style.setProperty(`--glass-${pane}`, String(alpha));
  };

  const render = async (look, elements = new Map()) => {
    const mine = ++generation;
    const { spec, fit, position, strength, dim, texture, textureStrength, textureSpacing, polarity, ground, readable, width, height } = look;
    const style = pictureStyle(spec, fit, position, spec?.kind === "mesh" ? meshURL(spec, width, height) : null);
    html.toggleAttribute("data-backdrop", Boolean(style));
    Object.assign(layers.picture.style, { backgroundImage: "", backgroundSize: "", backgroundRepeat: "", backgroundPosition: "" }, style || {});
    layers.picture.style.opacity = String(strength);
    layers.dim.style.background = polarity === "dark" ? "black" : "white";
    layers.dim.style.opacity = style ? String(dim) : "0";
    const textureLook = style ? textureStyle(texture, textureStrength, textureSpacing) : null;
    Object.assign(layers.texture.style, { backgroundImage: "", backgroundSize: "", opacity: "0", mixBlendMode: "normal" }, textureLook || {});
    if (textureLook && texture === "grain") Object.assign(layers.texture.style, { backgroundImage: `url("${grain()}")`, backgroundSize: "256px 256px" });
    const glass = style && look.glass?.on ? look.glass : null;
    html.toggleAttribute("data-panes", Boolean(glass));
    html.toggleAttribute("data-glass-gap", Boolean(glass && glass.gutter > 0));
    if (!glass) for (const name of GLASS_PROPERTIES) html.style.removeProperty(name);
    if (glass) {
      html.style.setProperty("--glass-tint", channels(glass.tint));
      html.style.setProperty("--glass-edge", channels(glass.edge));
      html.style.setProperty("--glass-blur", `${glass.blur}px`);
      html.style.setProperty("--glass-border", String(glass.border));
      html.style.setProperty("--glass-gutter", `${glass.gutter}px`);
      setPanes(glass.panes);
    }
    const reading = Boolean(style && readable);
    html.toggleAttribute("data-readable-on", reading);
    if (reading) html.style.setProperty("--scrim-feather", `${look.feather}px`);
    else html.style.removeProperty("--scrim-feather");
    if (!reading) {
      keepOnly(new Set());
      return { regions: {} };
    }
    let measured = null;
    try {
      measured = await measure(look);
    } catch {
      measured = null;
    }
    if (mine !== generation) return null;
    const results = measured ?? fallback(look);
    const keep = new Set();
    for (const region of look.regions) {
      const element = elements.get(region.id);
      const result = results[region.id];
      if (!element || !result) continue;
      keep.add(element);
      styled.add(element);
      if (region.kind === "pane") {
        element.style.setProperty("--glass-alpha", String(result.alpha));
      } else {
        element.style.setProperty("--scrim-tint", channels(result.tint));
        element.style.setProperty("--scrim-alpha", String(result.alpha));
      }
    }
    keepOnly(keep);
    return { regions: results };
  };

  return { root, layers, render };
}
