import { PNG } from "pngjs";
import { luminance, ratio } from "../../internal/daemon/dashboard/js/ui-contrast.js";

export const QUANTIZATION = 0.05;

export function decode(buffer) {
  const png = PNG.sync.read(buffer);
  return { width: png.width, height: png.height, data: png.data };
}

export function meanLuminance({ width, height, data }) {
  let total = 0;
  for (let i = 0; i < width * height * 4; i += 4) total += luminance([data[i] / 255, data[i + 1] / 255, data[i + 2] / 255]);
  return total / (width * height);
}

export function worstContrast(entry, ground, scale) {
  const [r, g, b, a] = entry.colour;
  const alpha = a * entry.opacity;
  let worst = Infinity;
  let at = null;
  for (const [x, y, w, h] of entry.rects) {
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
        }
      }
    }
  }
  return { worst, at };
}
