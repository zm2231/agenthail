use gpui::{Hsla, Rgba, WindowAppearance, rgb};

#[derive(Clone, Copy)]
pub struct Palette {
    pub side: Hsla,
    pub window: Hsla,
    pub raised: Hsla,
    pub line: Hsla,
    pub text: Hsla,
    pub text2: Hsla,
    pub muted: Hsla,
    pub bubble: Hsla,
    pub fill: Hsla,
    pub dock: Hsla,
    pub accent: Hsla,
    pub on_accent: Hsla,
    pub selection: Hsla,
    pub attention: Hsla,
    pub attention_background: Hsla,
    pub attention_line: Hsla,
    pub working: Hsla,
    pub done: Hsla,
    pub failed: Hsla,
}

fn pick(dark: bool, light: u32, dark_hex: u32) -> Hsla {
    let color: Rgba = rgb(if dark { dark_hex } else { light });
    color.into()
}

impl Palette {
    pub fn for_appearance(appearance: WindowAppearance) -> Self {
        let dark = matches!(appearance, WindowAppearance::Dark | WindowAppearance::VibrantDark);
        let accent = pick(dark, 0x0A64D8, 0x2F8CFF);
        Self {
            side: pick(dark, 0xF3F3F4, 0x1D1E21),
            window: pick(dark, 0xFFFFFF, 0x17181A),
            raised: pick(dark, 0xFFFFFF, 0x232427),
            line: pick(dark, 0xE3E3E6, 0x2E2F33),
            text: pick(dark, 0x1D1D1F, 0xECECEE),
            text2: pick(dark, 0x5F6066, 0xA3A3A8),
            muted: pick(dark, 0x9A9BA1, 0x6F7075),
            bubble: pick(dark, 0xF1F1F3, 0x26272A),
            fill: pick(dark, 0xE9E9EC, 0x2A2B2F),
            dock: pick(dark, 0xF7F7F8, 0x1B1C1F),
            accent,
            on_accent: pick(dark, 0xFFFFFF, 0xFFFFFF),
            selection: accent.opacity(0.18),
            attention: pick(dark, 0x965A00, 0xFFB224),
            attention_background: pick(dark, 0xFFF5E0, 0x2E2414),
            attention_line: pick(dark, 0xF2D9A6, 0x5C4415),
            working: pick(dark, 0x2A64CC, 0x7AA7FF),
            done: pick(dark, 0x1A7F4E, 0x4CC38A),
            failed: pick(dark, 0xC4271B, 0xFF6B61),
        }
    }
}

pub mod ty {
    pub const CAPTION: f32 = 11.;
    pub const META: f32 = 11.5;
    pub const BODY: f32 = 13.;
    pub const PROSE: f32 = 14.5;
    pub const TITLE: f32 = 15.;
}

pub mod space {
    pub const XS: f32 = 4.;
    pub const S: f32 = 8.;
    pub const M: f32 = 12.;
    pub const L: f32 = 16.;
    pub const XL: f32 = 20.;
}

pub mod radius {
    pub const CONTROL: f32 = 6.;
    pub const CARD: f32 = 10.;
}
