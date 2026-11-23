use rune::{Color, TextStyle, oklch};

pub const META: TextStyle = TextStyle::new(11.5, 400.0);
pub const META_BOLD: TextStyle = TextStyle::new(11.5, 600.0);
pub const BODY: TextStyle = TextStyle::new(13.0, 400.0);
pub const BODY_BOLD: TextStyle = TextStyle::new(13.0, 600.0);
pub const LABEL: TextStyle = TextStyle::new(10.5, 650.0);
pub const TITLE: TextStyle = TextStyle::new(15.0, 650.0);
pub const HEADING: TextStyle = TextStyle::new(20.0, 650.0);

pub fn accent() -> Color {
    oklch(0.72, 0.13, 260.0)
}

pub fn attention() -> Color {
    oklch(0.78, 0.15, 65.0)
}

pub fn done() -> Color {
    oklch(0.75, 0.14, 150.0)
}

pub fn failed() -> Color {
    oklch(0.70, 0.17, 25.0)
}

pub fn side() -> Color {
    oklch(0.165, 0.004, 265.0)
}
