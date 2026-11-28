use feed::look as fl;
use rune::Color;
use rune_kit::{Kit, TextRole, TextRoles, legible_on_all};

use crate::theme::Paint;

pub type Backdrops = [Color; 3];

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

#[cfg(test)]
pub fn contrast(a: Color, b: Color) -> f32 {
    let (a, b) = (a.luminance(), b.luminance());
    (a.max(b) + 0.05) / (a.min(b) + 0.05)
}

#[cfg(test)]
pub fn reads(color: Color, surfaces: &[Color], min: f32) -> bool {
    surfaces.iter().all(|&s| contrast(over(color, s), s) >= min)
}

#[cfg(test)]
pub fn fits(color: Color, surfaces: &[Color], min: f32) -> bool {
    let l = color.luminance();
    let side = surfaces.iter().all(|s| l <= s.luminance()) || surfaces.iter().all(|s| l >= s.luminance());
    side && reads(color, surfaces, min)
}

pub fn held(color: Color, surfaces: &[Color], min: f32) -> Color {
    if surfaces.is_empty() {
        return color;
    }
    let lum = surfaces.iter().map(|s| s.luminance());
    let (lo, hi) = (lum.clone().fold(f32::INFINITY, f32::min), lum.fold(0.0, f32::max));
    let (under, above) = ((lo + 0.05) / 0.05, 1.05 / (hi + 0.05));
    if under.max(above) < min {
        return if under >= above { Color::BLACK } else { Color::WHITE };
    }
    legible_on_all(color, surfaces, min)
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

pub fn hold_kit(mut k: Kit, behinds: Backdrops) -> Kit {
    let all: Vec<Color> = behinds.iter().flat_map(|&b| kit_surfaces(&k, b)).collect();
    let (body, faint) = if k.increased_contrast { (7.0, 4.5) } else { (4.5, 3.0) };
    let c = &mut k.c;
    for token in [&mut c.text_strong, &mut c.text_base, &mut c.text_weak, &mut c.text_muted, &mut c.text_accent] {
        *token = held(*token, &all, body);
    }
    c.text_faint = held(c.text_faint, &all, faint);
    for role in TextRole::ALL {
        k.roles.set(role, held(k.roles.get(role), &all, body));
    }
    k
}
