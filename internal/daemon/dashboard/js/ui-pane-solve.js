import { blendFloor, boundary, linear, passes } from "./ui-contrast.js";

const washed = (colour, [wash, strength]) => colour.map((v, i) => v + (wash[i] - v) * strength);

function solve(visit, roles, polarity, target, tint, start = 0, washes = []) {
  const limit = boundary(roles, polarity, target);
  const layers = [[tint, null], ...washes.map((wash) => [washed(tint, wash), wash])];
  let alpha = start;
  if (alpha >= 1) return 1;
  visit((r, g, b) => {
    for (const [[tr, tg, tb], wash] of layers) {
      const [cr, cg, cb] = wash ? washed([r, g, b], wash) : [r, g, b];
      const shown = 0.2126 * linear(cr + (tr - cr) * alpha) + 0.7152 * linear(cg + (tg - cg) * alpha) + 0.0722 * linear(cb + (tb - cb) * alpha);
      if (passes(shown, limit, polarity)) continue;
      alpha = Math.max(alpha, blendFloor([cr, cg, cb], [tr, tg, tb], polarity, limit));
      if (alpha >= 1) return false;
    }
    return true;
  });
  return alpha;
}

export function alphaOver(samples, roles, polarity, target, tint, floor = 0, washes = []) {
  return solve((check) => {
    for (const [r, g, b] of samples) if (!check(r, g, b)) return;
  }, roles, polarity, target, tint, floor, washes);
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
  return solve(visitShown(colours, look), look.roles, look.polarity, look.target, tint, floor, look.glass?.washes);
}

export function paneAlphas(coloursByKind, look) {
  const out = {};
  for (const [kind, floor] of Object.entries(look.glass.panes)) {
    const colours = coloursByKind[kind];
    out[kind] = colours ? alphaForColours(colours, look, look.glass.tint, floor) : floor;
  }
  return out;
}
