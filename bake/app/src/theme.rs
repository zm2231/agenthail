use std::sync::OnceLock;

use rune::{Color, Spring, TextStyle, oklch};
use rune_kit::Kit;

pub const META: TextStyle = TextStyle::new(12.0, 450.0);
pub const META_BOLD: TextStyle = TextStyle::new(12.0, 600.0);
pub const BODY: TextStyle = TextStyle::new(13.5, 420.0);
pub const BODY_BOLD: TextStyle = TextStyle::new(13.5, 600.0);
pub const LABEL: TextStyle = TextStyle::new(10.5, 680.0);
pub const TITLE: TextStyle = TextStyle::new(15.0, 650.0);
pub const HEADING: TextStyle = TextStyle::new(22.0, 680.0);

pub fn kit() -> &'static Kit {
    static KIT: OnceLock<Kit> = OnceLock::new();
    KIT.get_or_init(|| Kit::neutral(true))
}

pub const SPRING: Spring = Spring::new(0.32, 0.08);

pub fn text() -> Color {
    oklch(0.96, 0.004, 270.0)
}

pub fn muted() -> Color {
    oklch(0.74, 0.012, 270.0)
}

pub fn faint() -> Color {
    oklch(0.58, 0.012, 270.0)
}

pub fn card() -> Color {
    oklch(0.185, 0.008, 275.0)
}

pub fn card_deep() -> Color {
    oklch(0.16, 0.008, 275.0)
}

pub fn raised() -> Color {
    oklch(0.235, 0.01, 275.0)
}

pub fn line() -> Color {
    Color::WHITE.alpha(0.075)
}

pub fn wash() -> Color {
    Color::WHITE.alpha(0.055)
}

pub fn accent() -> Color {
    oklch(0.72, 0.15, 268.0)
}

pub fn accent_deep() -> Color {
    oklch(0.48, 0.16, 270.0)
}

pub fn attention() -> Color {
    oklch(0.80, 0.15, 70.0)
}

pub fn done() -> Color {
    oklch(0.78, 0.15, 155.0)
}

pub fn failed() -> Color {
    oklch(0.70, 0.18, 25.0)
}

pub fn glass_tint() -> Color {
    oklch(0.13, 0.012, 275.0).alpha(0.58)
}

pub fn hue_for(name: &str) -> f32 {
    let h = name.bytes().fold(2166136261u32, |h, b| (h ^ b as u32).wrapping_mul(16777619));
    (h % 360) as f32
}
