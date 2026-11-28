use feed::look as fl;
use rune::Color;
use rune_kit::{Kit, TextRole, TextRoles, legible};

use crate::theme::Paint;

pub fn faint_floor(increased: bool) -> f32 {
    if increased { 4.5 } else { 3.0 }
}

pub fn over(surface: Color, under: Color) -> Color {
    if surface.a >= 1.0 {
        return surface;
    }
    let encoded = |c: Color| [fl::gamma(c.r), fl::gamma(c.g), fl::gamma(c.b)];
    let [r, g, b] = fl::blend(encoded(under), encoded(surface), surface.a.clamp(0.0, 1.0)).map(fl::linear);
    Color { r, g, b, a: 1.0 }
}

pub fn contrast(a: Color, b: Color) -> f32 {
    let (a, b) = (a.luminance(), b.luminance());
    (a.max(b) + 0.05) / (a.min(b) + 0.05)
}

fn least(color: Color, surfaces: &[Color]) -> f32 {
    surfaces.iter().map(|&s| contrast(color, s)).fold(f32::INFINITY, f32::min)
}

pub fn reads(color: Color, surfaces: &[Color], min: f32) -> bool {
    surfaces.iter().all(|&s| contrast(over(color, s), s) >= min)
}

pub fn held(color: Color, surfaces: &[Color], min: f32) -> Color {
    if reads(color, surfaces, min) {
        return color;
    }
    let forward = surfaces.iter().fold(color, |c, &s| legible(c, s, min));
    let back = surfaces.iter().rev().fold(color, |c, &s| legible(c, s, min));
    if let Some(c) = [forward, back].into_iter().find(|&c| least(c, surfaces) >= min) {
        return c;
    }
    [color, forward, back, Color::BLACK, Color::WHITE].into_iter().max_by(|&a, &b| least(a, surfaces).total_cmp(&least(b, surfaces))).unwrap_or(color)
}

pub fn surfaces(paint: &Paint, floors: &[Color]) -> Vec<Color> {
    floors.iter().flat_map(|&f| [f, over(paint.card, f), over(paint.card_deep, f), over(paint.raised, f), over(paint.glass_tint, f)]).collect()
}

pub fn hold_paint(paint: &mut Paint, roles: &mut TextRoles, floors: &[Color], body: f32, increased: bool) {
    let on = surfaces(paint, floors);
    for c in [&mut paint.text, &mut paint.muted, &mut paint.accent, &mut paint.attention, &mut paint.done, &mut paint.failed] {
        *c = held(*c, &on, body);
    }
    paint.faint = held(paint.faint, &on, faint_floor(increased));
    for role in TextRole::ALL {
        roles.set(role, held(roles.get(role), &on, body));
    }
}

fn kit_surfaces(k: &Kit, behind: Color) -> [Color; 3] {
    let deep = over(k.c.bg_deep, behind);
    [over(k.c.bg_base, deep), deep, over(k.c.layer_01, deep)]
}

pub fn hold_kit(mut k: Kit, behinds: [Color; 2]) -> Kit {
    let mut m = k.clone();
    [m.c.bg_base, m.c.bg_deep, m.c.layer_01] = kit_surfaces(&k, behinds[0]);
    let m = m.legible_text();
    let all: Vec<Color> = behinds.iter().flat_map(|&b| kit_surfaces(&k, b)).collect();
    let (body, faint) = if k.increased_contrast { (7.0, 4.5) } else { (4.5, 3.0) };
    let keep = |served: Color, lifted: Color, min: f32| if reads(lifted, &all, min) { lifted } else { held(served, &all, min) };
    let c = &mut k.c;
    c.text_strong = keep(c.text_strong, m.c.text_strong, body);
    c.text_base = keep(c.text_base, m.c.text_base, body);
    c.text_weak = keep(c.text_weak, m.c.text_weak, body);
    c.text_muted = keep(c.text_muted, m.c.text_muted, body);
    c.text_accent = keep(c.text_accent, m.c.text_accent, body);
    c.text_faint = keep(c.text_faint, m.c.text_faint, faint);
    for role in TextRole::ALL {
        k.roles.set(role, keep(k.roles.get(role), m.roles.get(role), body));
    }
    k
}
