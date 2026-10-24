import { PNG } from "pngjs";
import { luminance, ratio } from "../../internal/daemon/dashboard/js/ui-contrast.js";

export const QUANTIZATION = 0.05;

export function decode(buffer) {
  const png = PNG.sync.read(buffer);
  return { width: png.width, height: png.height, data: png.data };
}

export function brightLuminance({ width, height, data }, share = 0.01) {
  const values = new Float64Array(width * height);
  for (let i = 0; i < values.length; i++) values[i] = luminance([data[i * 4] / 255, data[i * 4 + 1] / 255, data[i * 4 + 2] / 255]);
  values.sort();
  return values[Math.floor((values.length - 1) * (1 - share))];
}

const INK = 8;

function inked(entry, ground, shown, scale) {
  const out = [];
  for (const [x, y, w, h] of entry.rects) {
    const left = Math.max(Math.floor(x * scale), 0);
    const top = Math.max(Math.floor(y * scale), 0);
    const right = Math.min(Math.ceil((x + w) * scale), ground.width);
    const bottom = Math.min(Math.ceil((y + h) * scale), ground.height);
    for (let row = top; row < bottom; row++) {
      for (let column = left; column < right; column++) {
        const i = (row * ground.width + column) * 4;
        if (Math.max(Math.abs(shown.data[i] - ground.data[i]), Math.abs(shown.data[i + 1] - ground.data[i + 1]), Math.abs(shown.data[i + 2] - ground.data[i + 2])) >= INK) out.push([column, row, 1 / scale, 1 / scale]);
      }
    }
  }
  return out.map(([column, row, w, h]) => [column / scale, row / scale, w, h]);
}

export function worstContrast(entry, ground, scale, shown = null) {
  const glyphs = shown ? inked(entry, ground, shown, scale) : [];
  return worstOver(entry, glyphs.length ? glyphs : entry.rects, ground, scale);
}

function worstOver(entry, rects, ground, scale) {
  const [r, g, b, a] = entry.colour;
  const alpha = a * entry.opacity;
  let worst = Infinity;
  let at = null;
  let over = null;
  for (const [x, y, w, h] of rects) {
    const left = Math.max(Math.floor(x * scale), 0);
    const top = Math.max(Math.floor(y * scale), 0);
    const right = Math.min(Math.ceil((x + w) * scale), ground.width);
    const bottom = Math.min(Math.ceil((y + h) * scale), ground.height);
    for (let row = top; row < bottom; row++) {
      for (let column = left; column < right; column++) {
        const i = (row * ground.width + column) * 4;
        const under = [ground.data[i] / 255, ground.data[i + 1] / 255, ground.data[i + 2] / 255];
        const text = [r, g, b].map((v, k) => v * alpha + under[k] * (1 - alpha));
        const value = ratio(luminance(text), luminance(under));
        if (value < worst) {
          worst = value;
          at = [column, row];
          over = `#${[ground.data[i], ground.data[i + 1], ground.data[i + 2]].map((v) => v.toString(16).padStart(2, "0")).join("")}`;
        }
      }
    }
  }
  return { worst, at, over };
}
