import { gamma, linear } from "./ui-contrast.js";

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

const GLASS_PROPERTIES = ["--glass-tint", "--glass-edge", "--glass-blur", "--glass-border", "--glass-content", "--glass-sidebar", "--glass-chrome", "--glass-float"];

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

export function distinctColours(data, width = data.length >> 2, rects = [[0, 0, width, (data.length >> 2) / width]]) {
  const seen = new Uint8Array(1 << 21);
  const out = new Uint32Array(Math.min(data.length >> 2, 1 << 24));
  const height = (data.length >> 2) / width;
  let count = 0;
  for (const [x, y, w, h] of rects) {
    const left = Math.max(x, 0);
    const right = Math.min(x + w, width);
    for (let row = Math.max(y, 0); row < Math.min(y + h, height); row++) {
      for (let i = (row * width + left) * 4, end = (row * width + right) * 4; i < end; i += 4) {
        const n = (data[i] << 16) | (data[i + 1] << 8) | data[i + 2];
        const bit = 1 << (n & 7);
        if (seen[n >> 3] & bit) continue;
        seen[n >> 3] |= bit;
        out[count++] = n;
      }
    }
  }
  return out.slice(0, count);
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
  const setPanes = (panes) => {
    for (const [pane, alpha] of Object.entries(panes)) html.style.setProperty(`--glass-${pane}`, String(alpha));
  };

  const render = async (look) => {
    const mine = ++generation;
    const { spec, fit, position, strength, dim, texture, textureStrength, textureSpacing, polarity, readable, width, height } = look;
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
    if (style) html.setAttribute("data-panes", glass ? "glass" : "solid");
    else html.removeAttribute("data-panes");
    if (style) html.style.setProperty("--pane-gutter", `${look.gutter}px`);
    else html.style.removeProperty("--pane-gutter");
    if (!glass) for (const name of GLASS_PROPERTIES) html.style.removeProperty(name);
    if (!glass) return { panes: {} };
    html.style.setProperty("--glass-tint", channels(glass.tint));
    html.style.setProperty("--glass-edge", channels(glass.edge));
    html.style.setProperty("--glass-blur", `${glass.blur}px`);
    html.style.setProperty("--glass-border", String(glass.border));
    setPanes(glass.panes);
    if (!readable) return { panes: glass.panes };
    let measured = null;
    try {
      measured = await measure(look);
    } catch {
      measured = null;
    }
    if (mine !== generation) return null;
    const panes = measured?.panes || Object.fromEntries(Object.keys(glass.panes).map((kind) => [kind, 1]));
    setPanes(panes);
    return { panes };
  };

  return { root, layers, render };
}
