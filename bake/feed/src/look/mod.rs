mod edit;
mod paint;
#[cfg(test)]
mod tests;

use std::collections::HashMap;

use serde::Deserialize;
use serde_json::{Map, Value};

pub use edit::{Change, Entry, KEYS, Preset, Scope, describe, device_class, device_name, owner};
pub use paint::{Decoded, Placement, blend, boundary, contrast, decode, extreme, gamma, linear, luminance, mesh, pane_alpha, place, reachable, worst};

pub type Rgb = [f32; 3];

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Served {
    #[serde(default)]
    pub dark: String,
    #[serde(default)]
    pub light: String,
    #[serde(default, rename = "systemAccent")]
    pub system_accent: bool,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Problem {
    #[serde(default)]
    pub line: i64,
    #[serde(default)]
    pub key: String,
    #[serde(default)]
    pub message: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct View {
    #[serde(default)]
    pub rev: String,
    #[serde(default)]
    pub text: String,
    #[serde(default)]
    pub values: Map<String, Value>,
    #[serde(default)]
    pub colors: HashMap<String, Served>,
    #[serde(default)]
    pub chosen: HashMap<String, String>,
    #[serde(default)]
    pub overrides: HashMap<String, String>,
    #[serde(default)]
    pub problems: Vec<Problem>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Theme {
    System,
    Dark,
    Light,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Wallpaper {
    None,
    Desktop,
    Image(String),
    Linear { angle: f32, stops: Vec<(Rgb, f32)> },
    Mesh { columns: usize, rows: usize, colors: Vec<Rgb> },
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Fit {
    Cover,
    Contain,
    Center,
    Tile,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Position {
    Center,
    Top,
    Bottom,
    Left,
    Right,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Tint {
    Ground,
    Accent,
    Color(Rgb),
}

#[derive(Clone, Debug, PartialEq)]
pub struct Look {
    pub rev: String,
    pub theme: Theme,
    pub accent: Option<(Rgb, Rgb)>,
    pub background: Wallpaper,
    pub background_light: Wallpaper,
    pub fit: Fit,
    pub position: Position,
    pub dim: f32,
    pub opacity: f32,
    pub session_opacity: f32,
    pub glass: bool,
    pub blur: f32,
    pub tint: Tint,
    pub pane_opacity: f32,
    pub float_opacity: f32,
    pub readable: bool,
    pub border: f32,
    pub gutter: f32,
    pub scrim_black: bool,
    pub text_scale: f32,
    pub target: f32,
    pub roles: HashMap<String, (Rgb, Rgb)>,
}

impl Default for Look {
    fn default() -> Look {
        Look::from_view(&View::default())
    }
}

pub fn hex(value: &str) -> Option<Rgb> {
    let digits = value.trim().strip_prefix('#')?;
    if digits.len() != 6 || !digits.chars().all(|c| c.is_ascii_hexdigit()) {
        return None;
    }
    let n = u32::from_str_radix(digits, 16).ok()?;
    Some([((n >> 16) & 0xff) as f32 / 255.0, ((n >> 8) & 0xff) as f32 / 255.0, (n & 0xff) as f32 / 255.0])
}

pub fn parse_background(value: &str) -> Option<Wallpaper> {
    let value = value.trim();
    match value {
        "none" => return Some(Wallpaper::None),
        "desktop" => return Some(Wallpaper::Desktop),
        _ => {}
    }
    let (kind, spec) = value.split_once(':')?;
    let spec = spec.trim();
    let parts: Vec<&str> = spec.split(',').map(str::trim).filter(|p| !p.is_empty()).collect();
    match kind.trim() {
        "image" => (!spec.is_empty()).then(|| Wallpaper::Image(spec.to_string())),
        "gradient" => {
            let angle: f32 = parts.first()?.strip_suffix("deg")?.trim().parse().ok()?;
            if parts.len() < 3 {
                return None;
            }
            let mut stops = Vec::new();
            for part in &parts[1..] {
                let mut fields = part.split_whitespace();
                let (color, at, rest) = (fields.next()?, fields.next()?, fields.next());
                if rest.is_some() {
                    return None;
                }
                stops.push((hex(color)?, at.strip_suffix('%')?.parse::<f32>().ok()? / 100.0));
            }
            Some(Wallpaper::Linear { angle, stops })
        }
        "mesh" => {
            let (c, r) = parts.first()?.split_once('x')?;
            let (columns, rows): (usize, usize) = (c.trim().parse().ok()?, r.trim().parse().ok()?);
            if columns < 2 || rows < 2 {
                return None;
            }
            let colors: Vec<Rgb> = parts[1..].iter().map(|p| hex(p)).collect::<Option<_>>()?;
            (colors.len() == columns * rows).then_some(Wallpaper::Mesh { columns, rows, colors })
        }
        _ => None,
    }
}

fn text<'a>(values: &'a Map<String, Value>, key: &str, fallback: &'a str) -> &'a str {
    values.get(key).and_then(Value::as_str).unwrap_or(fallback)
}

fn number(values: &Map<String, Value>, key: &str, fallback: f32, range: (f32, f32)) -> f32 {
    let n = values.get(key).and_then(Value::as_f64).map(|n| n as f32).filter(|n| n.is_finite()).unwrap_or(fallback);
    n.clamp(range.0, range.1)
}

fn pair(served: &Served) -> Option<(Rgb, Rgb)> {
    Some((hex(&served.dark)?, hex(&served.light)?))
}

impl Look {
    pub fn from_view(view: &View) -> Look {
        let v = &view.values;
        let background = parse_background(text(v, "background", "none")).unwrap_or(Wallpaper::None);
        let background_light = v.get("background.light").and_then(Value::as_str).and_then(parse_background).unwrap_or_else(|| background.clone());
        let roles = view.colors.iter().filter(|(name, _)| name.as_str() != "accent").filter_map(|(name, served)| Some((name.clone(), pair(served)?))).collect();
        Look {
            rev: view.rev.clone(),
            theme: match text(v, "theme", "system") {
                "dark" => Theme::Dark,
                "light" => Theme::Light,
                _ => Theme::System,
            },
            accent: view.colors.get("accent").filter(|c| !c.system_accent).and_then(pair),
            background,
            background_light,
            fit: match text(v, "background.fit", "cover") {
                "contain" => Fit::Contain,
                "center" => Fit::Center,
                "tile" => Fit::Tile,
                _ => Fit::Cover,
            },
            position: match text(v, "background.position", "center") {
                "top" => Position::Top,
                "bottom" => Position::Bottom,
                "left" => Position::Left,
                "right" => Position::Right,
                _ => Position::Center,
            },
            dim: number(v, "background.dim", 0.0, (0.0, 0.8)),
            opacity: number(v, "background.opacity", 1.0, (0.0, 1.0)),
            session_opacity: number(v, "background.session-opacity", 1.0, (0.0, 1.0)),
            glass: text(v, "glass", "on") == "on",
            blur: number(v, "glass.blur", 30.0, (0.0, 64.0)),
            tint: match text(v, "glass.tint", "ground") {
                "ground" => Tint::Ground,
                "accent" => Tint::Accent,
                other => hex(other).map(Tint::Color).unwrap_or(Tint::Ground),
            },
            pane_opacity: number(v, "glass.opacity", 0.3, (0.0, 1.0)),
            float_opacity: number(v, "glass.composer.opacity", 0.5, (0.0, 1.0)),
            readable: text(v, "glass.readability", "auto") == "auto",
            border: number(v, "glass.border", 0.08, (0.0, 0.3)),
            gutter: number(v, "glass.gutter", 0.0, (0.0, 16.0)),
            scrim_black: text(v, "scrim.tint", "auto") == "black",
            text_scale: number(v, "text.scale", 1.0, (0.8, 1.6)),
            target: number(v, "contrast.target", 4.5, (4.5, 7.0)),
            roles,
        }
    }

    pub fn dark(&self, system_dark: bool) -> bool {
        match self.theme {
            Theme::System => system_dark,
            Theme::Dark => true,
            Theme::Light => false,
        }
    }

    pub fn wallpaper(&self, dark: bool) -> &Wallpaper {
        if dark { &self.background } else { &self.background_light }
    }

    pub fn images(&self) -> Vec<String> {
        let mut paths = Vec::new();
        for w in [&self.background, &self.background_light] {
            if let Wallpaper::Image(p) = w {
                if !paths.contains(p) {
                    paths.push(p.clone());
                }
            }
        }
        paths
    }

    pub fn role(&self, name: &str, dark: bool) -> Option<Rgb> {
        self.roles.get(name).map(|(d, l)| if dark { *d } else { *l })
    }

    pub fn pane_tint(&self, dark: bool, worst: Rgb, ground: Rgb, tint: Rgb, strength: f32, minimum: f32) -> (f32, Rgb) {
        if !self.readable {
            return (minimum, tint);
        }
        let behind = blend(blend(ground, worst, strength), if dark { [0.0; 3] } else { [1.0; 3] }, self.dim);
        let mut roles: Vec<f32> = READ_ON_PANES.iter().filter_map(|r| self.role(r, dark)).map(luminance).collect();
        if roles.is_empty() {
            roles.push(if dark { 0.84 } else { 0.012 });
        }
        let edge = boundary(&roles, dark, self.target);
        let tint = reachable(tint, edge, dark);
        (pane_alpha(luminance(behind), luminance(tint), edge, dark, minimum), tint)
    }

    pub fn translucent(&self, dark: bool) -> bool {
        dark && self.glass && self.drawn(dark)
    }

    pub fn drawn(&self, dark: bool) -> bool {
        !matches!(self.wallpaper(dark), Wallpaper::None | Wallpaper::Desktop)
    }
}

const READ_ON_PANES: [&str; 5] = ["body", "muted", "faint", "meta", "code"];

pub const NOT_DRAWN: [&str; 6] = [
    "Text colours for timestamps, links, code, live work, removed diff lines and other agents' messages: this app keeps its own; body, secondary, quiet, needs-you, done and error text follow the settings.",
    "Texture: it lies only over open background, and this app's panes cover the whole window.",
    "Desktop: this app can't read the desktop picture yet, so it draws no background for it.",
    "Space between panes (glass.gutter): panes keep this app's fixed spacing.",
    "Readability layer colour (scrim.tint): this app's panes cover the whole window, so there is no open area for it.",
    "Text size scales the whole window here, not only the text.",
];
