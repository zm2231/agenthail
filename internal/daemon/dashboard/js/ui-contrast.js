// Port of native/ScrimField.swift; both must match scripts/fixtures/contrast-vectors.json.

export const MARGIN = 0.1;
export const TINT_DEPTH = 0.6;

export function linear(c) {
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
}

export function gamma(l) {
  return l <= 0.0031308 ? l * 12.92 : 1.055 * Math.pow(Math.max(l, 0), 1 / 2.4) - 0.055;
}

export function luminance([r, g, b]) {
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
}

export function ratio(a, b) {
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

export function boundary(roles, polarity, target) {
  const goal = target + MARGIN;
  if (polarity === "dark") return roles.length ? Math.min(...roles.map((l) => (l + 0.05) / goal - 0.05)) : 0;
  return roles.length ? Math.max(...roles.map((l) => (l + 0.05) * goal - 0.05)) : 1;
}

export function passes(shown, limit, polarity) {
  return polarity === "dark" ? shown <= limit : shown >= limit;
}

export function reachable(colour, polarity, limit) {
  const l = colour.map(linear);
  const y = 0.2126 * l[0] + 0.7152 * l[1] + 0.0722 * l[2];
  if (polarity === "dark") {
    const top = limit * TINT_DEPTH;
    if (y <= top) return colour;
    return l.map((v) => gamma(v * (top / y)));
  }
  const bottom = 1 - (1 - limit) * TINT_DEPTH;
  if (y >= bottom) return colour;
  const scale = (1 - bottom) / Math.max(1 - y, 1e-6);
  return l.map((v) => gamma(1 - (1 - v) * scale));
}

// Browsers blend in gamma-encoded sRGB, not linear light as ScrimField.floor assumes.
export function blendFloor(background, tint, polarity, limit) {
  const shown = (a) => luminance(background.map((b, i) => b + (tint[i] - b) * a));
  if (passes(shown(0), limit, polarity)) return 0;
  if (!passes(shown(1), limit, polarity)) return 1;
  let low = 0;
  let high = 1;
  for (let step = 0; step < 24; step++) {
    const mid = (low + high) / 2;
    if (passes(shown(mid), limit, polarity)) high = mid;
    else low = mid;
  }
  return Math.min(Math.ceil(high * 255) / 255, 1);
}

export const ACCENT_TEXT_CONTRAST = 4.5;

function accentLuminance([r, g, b]) {
  const channel = (v) => (v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4));
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

export function accentContrast(a, b) {
  return ratio(accentLuminance(a), accentLuminance(b));
}

export function readableAccent(accent, surfaces, dark) {
  const target = dark ? [1, 1, 1] : [0, 0, 0];
  let amount = 0;
  let colour = accent;
  while (amount < 1 && surfaces.some((surface) => accentContrast(colour, surface) < ACCENT_TEXT_CONTRAST)) {
    amount = Math.min(1, amount + 0.05);
    colour = accent.map((v, i) => v + (target[i] - v) * amount);
  }
  return colour;
}

export function foregroundOn(accent) {
  return accentContrast([1, 1, 1], accent) >= accentContrast([0, 0, 0], accent) ? [1, 1, 1] : [0, 0, 0];
}

export function readableOnTint(accent, surfaces, dark, alpha) {
  let colour = readableAccent(accent, surfaces, dark);
  for (let pass = 0; pass < 8; pass++) {
    const tinted = surfaces.map((surface) => surface.map((v, i) => colour[i] * alpha + v * (1 - alpha)));
    const next = readableAccent(accent, [...surfaces, ...tinted], dark);
    if (next.every((v, i) => v === colour[i])) break;
    colour = next;
  }
  return colour;
}
