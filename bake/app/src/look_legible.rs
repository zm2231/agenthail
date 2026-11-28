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

pub fn held(color: Color, surfaces: &[Color], min: f32) -> Color {
    if surfaces.iter().all(|&s| legible(color, s, min) == color) {
        return color;
    }
    surfaces.iter().fold(color, |c, &s| legible(c, s, min))
}

pub fn surfaces(paint: &Paint, floor: Color) -> [Color; 5] {
    [floor, paint.card, paint.card_deep, paint.raised, paint.glass_tint].map(|s| over(s, floor))
}

pub fn hold_paint(paint: &mut Paint, roles: &mut TextRoles, floor: Color, body: f32, increased: bool) {
    let on = surfaces(paint, floor);
    for c in [&mut paint.text, &mut paint.muted, &mut paint.accent, &mut paint.attention, &mut paint.done, &mut paint.failed] {
        *c = held(*c, &on, body);
    }
    paint.faint = held(paint.faint, &on, faint_floor(increased));
    for role in TextRole::ALL {
        roles.set(role, held(roles.get(role), &on, body));
    }
}

pub fn hold_kit(mut k: Kit, behind: Color) -> Kit {
    let mut m = k.clone();
    m.c.bg_deep = over(k.c.bg_deep, behind);
    m.c.bg_base = over(k.c.bg_base, m.c.bg_deep);
    m.c.layer_01 = over(k.c.layer_01, m.c.bg_deep);
    let m = m.legible_text();
    k.c.text_strong = m.c.text_strong;
    k.c.text_base = m.c.text_base;
    k.c.text_weak = m.c.text_weak;
    k.c.text_muted = m.c.text_muted;
    k.c.text_accent = m.c.text_accent;
    k.c.text_faint = m.c.text_faint;
    k.roles = m.roles;
    k
}
