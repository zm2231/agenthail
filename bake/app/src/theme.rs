use std::cell::{Cell, RefCell};

use rune::{Color, Spring, TextStyle, oklch};
use rune_kit::{Kit, KitEnv, KitTheme};

pub const META: TextStyle = TextStyle::new(12.0, 450.0);
pub const META_BOLD: TextStyle = TextStyle::new(12.0, 600.0);
pub const BODY: TextStyle = TextStyle::new(13.5, 420.0);
pub const BODY_BOLD: TextStyle = TextStyle::new(13.5, 600.0);
pub const LABEL: TextStyle = TextStyle::new(10.5, 680.0);
pub const TITLE: TextStyle = TextStyle::new(15.0, 650.0);
pub const HEADING: TextStyle = TextStyle::new(22.0, 680.0);

pub const SPRING: Spring = Spring::new(0.32, 0.08);

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Paint {
    pub dark: bool,
    pub text: Color,
    pub muted: Color,
    pub faint: Color,
    pub card: Color,
    pub card_deep: Color,
    pub raised: Color,
    pub line: Color,
    pub wash: Color,
    pub accent: Color,
    pub accent_deep: Color,
    pub attention: Color,
    pub done: Color,
    pub failed: Color,
    pub glass_tint: Color,
    pub ground: Color,
    pub pane: Color,
    pub border: Color,
    pub blur: f32,
}

impl Paint {
    pub fn dark() -> Paint {
        Paint {
            dark: true,
            text: oklch(0.96, 0.004, 270.0),
            muted: oklch(0.74, 0.012, 270.0),
            faint: oklch(0.58, 0.012, 270.0),
            card: oklch(0.185, 0.008, 275.0),
            card_deep: oklch(0.16, 0.008, 275.0),
            raised: oklch(0.235, 0.01, 275.0),
            line: Color::WHITE.alpha(0.075),
            wash: Color::WHITE.alpha(0.055),
            accent: oklch(0.72, 0.15, 268.0),
            accent_deep: oklch(0.48, 0.16, 270.0),
            attention: oklch(0.80, 0.15, 70.0),
            done: oklch(0.78, 0.15, 155.0),
            failed: oklch(0.70, 0.18, 25.0),
            glass_tint: oklch(0.13, 0.012, 275.0).alpha(0.58),
            ground: oklch(0.16, 0.008, 275.0),
            pane: oklch(0.16, 0.008, 275.0),
            border: Color::WHITE.alpha(0.07),
            blur: 22.0,
        }
    }

    pub fn light() -> Paint {
        Paint {
            dark: false,
            text: oklch(0.22, 0.006, 265.0),
            muted: oklch(0.45, 0.012, 270.0),
            faint: oklch(0.55, 0.012, 270.0),
            card: Color::WHITE,
            card_deep: oklch(0.965, 0.004, 265.0),
            raised: oklch(0.94, 0.005, 265.0),
            line: Color::BLACK.alpha(0.09),
            wash: Color::BLACK.alpha(0.045),
            accent: oklch(0.55, 0.17, 268.0),
            accent_deep: oklch(0.45, 0.17, 270.0),
            attention: oklch(0.58, 0.15, 70.0),
            done: oklch(0.52, 0.14, 155.0),
            failed: oklch(0.55, 0.2, 25.0),
            glass_tint: Color::WHITE.alpha(0.9),
            ground: oklch(0.958, 0.004, 265.0),
            pane: oklch(0.958, 0.004, 265.0),
            border: Color::BLACK.alpha(0.09),
            blur: 0.0,
        }
    }
}

fn first_chrome(env: &KitEnv) -> Kit {
    let mut k = rune_theme_opencode::kit(&env.dark(true).increased_contrast(false));
    k.c.bg_deep = Color::TRANSPARENT;
    k.c.border_muted = Color::WHITE.alpha(0.09);
    k
}

#[derive(Clone)]
pub struct Themes {
    pub main: KitTheme,
    pub chrome: KitTheme,
}

impl Themes {
    fn first() -> Themes {
        Themes { main: KitTheme::new(|_| Kit::neutral(true)), chrome: KitTheme::new(first_chrome) }
    }
}

thread_local! {
    static PAINT: Cell<Paint> = Cell::new(Paint::dark());
    static THEMES: RefCell<Option<Themes>> = const { RefCell::new(None) };
}

pub fn set(paint: Paint, themes: Themes) {
    PAINT.set(paint);
    THEMES.set(Some(themes));
}

pub fn themes() -> Themes {
    THEMES.with_borrow_mut(|t| t.get_or_insert_with(Themes::first).clone())
}

pub fn paint() -> Paint {
    PAINT.get()
}

pub fn text() -> Color {
    paint().text
}

pub fn muted() -> Color {
    paint().muted
}

pub fn faint() -> Color {
    paint().faint
}

pub fn card() -> Color {
    paint().card
}

pub fn card_deep() -> Color {
    paint().card_deep
}

pub fn raised() -> Color {
    paint().raised
}

pub fn line() -> Color {
    paint().line
}

pub fn wash() -> Color {
    paint().wash
}

pub fn accent() -> Color {
    paint().accent
}

pub fn accent_deep() -> Color {
    paint().accent_deep
}

pub fn attention() -> Color {
    paint().attention
}

pub fn done() -> Color {
    paint().done
}

pub fn failed() -> Color {
    paint().failed
}

pub fn glass_tint() -> Color {
    paint().glass_tint
}

pub fn hue_for(name: &str) -> f32 {
    let h = name.bytes().fold(2166136261u32, |h, b| (h ^ b as u32).wrapping_mul(16777619));
    (h % 360) as f32
}

pub fn ink(alpha: f32) -> Color {
    if paint().dark { Color::WHITE.alpha(alpha) } else { Color::BLACK.alpha(alpha * 0.9) }
}

pub fn paint_key() -> u64 {
    use std::hash::{Hash, Hasher};
    let p = paint();
    let mut h = std::collections::hash_map::DefaultHasher::new();
    (p.dark, p.text, p.muted, p.faint, p.card, p.card_deep, p.raised, p.line, p.wash, p.accent, p.accent_deep, p.attention).hash(&mut h);
    (p.done, p.failed, p.glass_tint, p.ground, p.pane, p.border, p.blur.to_bits()).hash(&mut h);
    h.finish()
}

pub fn on_accent() -> Color {
    let p = paint();
    let l = p.accent.mix(p.accent_deep, 0.5).luminance();
    if 1.05 / (l + 0.05) >= (l + 0.05) / 0.05 { Color::WHITE } else { Color::BLACK }
}
