use feed::look as fl;
use rune::Color;
use rune_kit::{Kit, TextRole, TextRoles};

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

pub fn fits(color: Color, surfaces: &[Color], min: f32) -> bool {
    let l = color.luminance();
    let side = surfaces.iter().all(|s| l <= s.luminance()) || surfaces.iter().all(|s| l >= s.luminance());
    side && reads(color, surfaces, min)
}

pub fn held(color: Color, surfaces: &[Color], min: f32) -> Color {
    if fits(color, surfaces, min) {
        return color;
    }
    let from = color.alpha(1.0).to_oklab();
    let toward = |end: Color, t: f32| {
        let to = end.to_oklab();
        Color::from_oklab(std::array::from_fn(|i| from[i] + (to[i] - from[i]) * t))
    };
    let goal = min * 1.01;
    let reach = |end: Color| {
        if !fits(end, surfaces, goal) {
            return None;
        }
        let (mut low, mut high) = (0.0f32, 1.0f32);
        for _ in 0..24 {
            let mid = (low + high) * 0.5;
            if fits(toward(end, mid), surfaces, goal) { high = mid } else { low = mid }
        }
        Some((high, toward(end, high)))
    };
    match [reach(Color::BLACK), reach(Color::WHITE)] {
        [Some(a), Some(b)] => if a.0 <= b.0 { a.1 } else { b.1 },
        [Some((_, c)), None] | [None, Some((_, c))] => c,
        [None, None] => if least(Color::BLACK, surfaces) >= least(Color::WHITE, surfaces) { Color::BLACK } else { Color::WHITE },
    }
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
    let keep = |served: Color, lifted: Color, min: f32| if fits(lifted, &all, min) { lifted } else { held(served, &all, min) };
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
