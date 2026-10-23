import { blendFloor, boundary, gamma, linear, luminance, passes } from "./ui-contrast.js";

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

function hex(colour) {
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

export function pageScrim(samples, roles, polarity, target, ground) {
  const limit = boundary(roles, polarity, target);
  let alpha = 0;
  for (const colour of samples) {
    if (passes(luminance(colour.map((v, i) => v + (ground[i] - v) * alpha)), limit, polarity)) continue;
    alpha = Math.max(alpha, blendFloor(colour, ground, polarity, limit));
    if (alpha >= 1) break;
  }
  return alpha;
}

export function composite(picture, strength, ground, dim, polarity) {
  const veil = polarity === "dark" ? 0 : 1;
  return picture.map((v, i) => {
    const shown = ground[i] + (v - ground[i]) * strength;
    return shown + (veil - shown) * dim;
  });
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

export function textureExtremes(colour, texture, strength, polarity) {
  if (!(strength > 0)) return [];
  if (texture === "scanlines") return polarity === "light" ? [colour.map((v) => v * (1 - strength))] : [];
  if (texture === "grain") {
    const a = strength * 0.5;
    const extreme = polarity === "light" ? (v) => Math.max(2 * v - 1, 0) : (v) => Math.min(2 * v, 1);
    return [colour.map((v) => v + (extreme(v) - v) * a)];
  }
  return [];
}

export function distinctColours(data) {
  const seen = new Uint8Array(1 << 21);
  const out = [];
  for (let i = 0; i < data.length; i += 4) {
    const n = (data[i] << 16) | (data[i + 1] << 8) | data[i + 2];
    const bit = 1 << (n & 7);
    if (seen[n >> 3] & bit) continue;
    seen[n >> 3] |= bit;
    out.push([data[i] / 255, data[i + 1] / 255, data[i + 2] / 255]);
  }
  return out;
}

function canvas(document, width, height) {
  const element = document.createElement("canvas");
  element.width = width;
  element.height = height;
  return element;
}

export function createBackdrop({ document, loadImage }) {
  const root = document.createElement("div");
  root.className = "ui-backdrop";
  root.setAttribute("aria-hidden", "true");
  const layers = {};
  for (const name of ["picture", "dim", "texture", "scrim"]) {
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

  const sample = async (spec, fit, position, width, height, scale, ground) => {
    const w = Math.max(Math.round(width * scale), 1);
    const h = Math.max(Math.round(height * scale), 1);
    const element = canvas(document, w, h);
    const context = element.getContext("2d", { willReadFrequently: true });
    context.fillStyle = hex(ground);
    context.fillRect(0, 0, w, h);
    if (spec.kind === "image") {
      const image = await loadImage(backgroundURL(spec.path));
      if (!image) return null;
      const iw = image.naturalWidth * scale;
      const ih = image.naturalHeight * scale;
      if (fit === "tile") {
        for (let y = 0; y < h; y += ih) for (let x = 0; x < w; x += iw) context.drawImage(image, x, y, iw, ih);
      } else {
        const rect = imageRect(iw, ih, w, h, fit, position);
        context.drawImage(image, rect.x, rect.y, rect.width, rect.height);
      }
    } else if (spec.kind === "linear") {
      const line = gradientLine(spec.angle, w, h);
      const gradient = context.createLinearGradient(line.x0, line.y0, line.x1, line.y1);
      for (const stop of spec.stops) gradient.addColorStop(Math.min(Math.max(stop.location, 0), 1), hex(stop.colour));
      context.fillStyle = gradient;
      context.fillRect(0, 0, w, h);
    } else if (spec.kind === "mesh") {
      const size = meshSize(width / Math.max(height, 1));
      const small = canvas(document, size.width, size.height);
      const smallContext = small.getContext("2d");
      const image = smallContext.createImageData(size.width, size.height);
      image.data.set(meshPixels(spec, size.width, size.height));
      smallContext.putImageData(image, 0, 0);
      context.drawImage(small, 0, 0, w, h);
    }
    return distinctColours(context.getImageData(0, 0, w, h).data);
  };

  const render = async (look) => {
    const mine = ++generation;
    const { spec, fit, position, strength, dim, texture, textureStrength, textureSpacing, polarity, ground, roles, target, readable, width, height, scale } = look;
    const style = pictureStyle(spec, fit, position, spec?.kind === "mesh" ? meshURL(spec, width, height) : null);
    document.documentElement.toggleAttribute("data-backdrop", Boolean(style));
    Object.assign(layers.picture.style, { backgroundImage: "", backgroundSize: "", backgroundRepeat: "", backgroundPosition: "" }, style || {});
    layers.picture.style.opacity = String(strength);
    layers.dim.style.background = polarity === "dark" ? "black" : "white";
    layers.dim.style.opacity = style ? String(dim) : "0";
    const textureLook = style ? textureStyle(texture, textureStrength, textureSpacing) : null;
    Object.assign(layers.texture.style, { backgroundImage: "", backgroundSize: "", opacity: "0", mixBlendMode: "normal" }, textureLook || {});
    if (textureLook && texture === "grain") Object.assign(layers.texture.style, { backgroundImage: `url("${grain()}")`, backgroundSize: "256px 256px" });
    if (!style || !readable) {
      layers.scrim.style.opacity = "0";
      return 0;
    }
    const pixels = await sample(spec, fit, position, width, height, scale || 1, ground);
    if (mine !== generation) return null;
    let samples = [];
    if (pixels) {
      for (const pixel of pixels) {
        const shown = composite(pixel, strength, ground, dim, polarity);
        samples.push(shown, ...textureExtremes(shown, texture, textureStrength, polarity));
      }
    } else {
      samples = [polarity === "dark" ? [1, 1, 1] : [0, 0, 0]];
    }
    const alpha = pageScrim(samples, roles, polarity, target, ground);
    layers.scrim.style.background = hex(ground);
    layers.scrim.style.opacity = String(alpha);
    return alpha;
  };

  return { root, layers, render };
}
