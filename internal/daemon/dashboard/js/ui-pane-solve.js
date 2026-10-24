import { blendFloor, boundary, linear, passes } from "./ui-contrast.js";

function solve(visit, roles, polarity, target, tint, start = 0) {
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

export function alphaOver(samples, roles, polarity, target, tint, floor = 0) {
  return solve((check) => {
    for (const [r, g, b] of samples) if (!check(r, g, b)) return;
  }, roles, polarity, target, tint, floor);
}

function visitShown(colours, { strength, dim, ground, polarity, texture, textureStrength }) {
  const veil = polarity === "dark" ? 0 : 1;
  const shown = (byte, i) => {
    const over = ground[i] + (byte / 255 - ground[i]) * strength;
    return over + (veil - over) * dim;
  };
  const grain = texture === "grain" && textureStrength > 0 ? textureStrength * 0.5 : 0;
  const lines = texture === "scanlines" && textureStrength > 0 && polarity === "light" ? textureStrength : 0;
  const lift = polarity === "light" ? (v) => v + (Math.max(2 * v - 1, 0) - v) * grain : (v) => v + (Math.min(2 * v, 1) - v) * grain;
  return (check) => {
    for (let k = 0; k < colours.length; k++) {
      const n = colours[k];
      const r = shown((n >> 16) & 255, 0);
      const g = shown((n >> 8) & 255, 1);
      const b = shown(n & 255, 2);
      if (!check(r, g, b)) return;
      if (grain && !check(lift(r), lift(g), lift(b))) return;
      if (lines && !check(r * (1 - lines), g * (1 - lines), b * (1 - lines))) return;
    }
  };
}

export function alphaForColours(colours, look, tint, floor = 0) {
  return solve(visitShown(colours, look), look.roles, look.polarity, look.target, tint, floor);
}

export function paneAlphas(coloursByKind, look) {
  const out = {};
  for (const [kind, floor] of Object.entries(look.glass.panes)) {
    const colours = coloursByKind[kind];
    out[kind] = colours ? alphaForColours(colours, look, look.glass.tint, floor) : floor;
  }
  return out;
}
