use feed::look::{self as fl, View, Wallpaper};
use rune::{Color, El, col, el, row, text};

use super::controls::chosen;
use super::{Prefs, set, writing};
use crate::theme::{self, META, META_BOLD, muted};

const TILE_COLUMNS: usize = 5;
const TILE_WIDTH: f32 = 104.0;

pub(super) fn swatch_fill(s: &Prefs, value: &str) -> El {
    let key = match fl::parse_background(value) {
        Some(Wallpaper::Image(path)) => path,
        Some(Wallpaper::Linear { angle, stops }) => {
            let stops: Vec<(f32, Color)> = stops.iter().map(|(c, at)| (*at, Color { r: fl::linear(c[0]), g: fl::linear(c[1]), b: fl::linear(c[2]), a: 1.0 })).collect();
            return el().linear_gradient(angle.to_radians(), stops);
        }
        Some(Wallpaper::Mesh { columns, rows, colors }) if !s.appearance.thumbs.contains_key(value) => {
            let (w, h, rgba) = fl::mesh(columns, rows, &colors, 1.6);
            let p = rune::Pixels::new();
            p.set(w, h, rgba);
            return el().pixels(&p);
        }
        _ => value.to_string(),
    };
    match s.appearance.thumbs.get(&key) {
        Some(p) => el().pixels_fit(p, rune::SurfaceFit::Fill),
        None => el().fill(theme::wash()),
    }
}

pub(super) fn tile(name: &str, key: String, fill: El, selected: bool, press: impl Fn(&mut Prefs, &mut rune::Tx) + 'static) -> El {
    let k = theme::kit();
    let border = if selected { (2.5, k.c.border_focus) } else { (1.0, theme::line()) };
    col()
        .w(TILE_WIDTH)
        .gap(5.0)
        .items_center()
        .key(key)
        .role("button")
        .label(name.to_string())
        .checked(selected)
        .on_press_tx(press)
        .child(el().w_full().h(56.0).radius(10.0).padding(2.0).border(border.0, border.1).child(fill.w_full().h_full().radius(7.0)))
        .child(text(name.to_string()).font(if selected { META_BOLD } else { META }).color(if selected { theme::text() } else { muted() }).truncate())
}

pub(super) fn grid(s: &Prefs, view: &View) -> El {
    let dark = theme::paint().dark;
    let current = chosen(view, "background", "").to_string();
    let presets = match &s.appearance.presets {
        Some(Ok(p)) => p.clone(),
        _ => Vec::new(),
    };
    let mut tiles = Vec::new();
    for p in &presets {
        let fill = swatch_fill(s, if dark { &p.dark } else { &p.light });
        let (value, name) = (p.value.clone(), p.name.clone());
        let label = format!("background to {name}");
        tiles.push(tile(&p.name, format!("appearance-bg-{}", p.value), fill, current == p.value, move |s: &mut Prefs, _| {
            if !writing(s) {
                set(s, "background", &value, &label);
            }
        }));
    }
    tiles.push(tile("Desktop", "appearance-bg-desktop".into(), el().fill(theme::wash()), current == "desktop", |s: &mut Prefs, _| {
        if !writing(s) {
            set(s, "background", "desktop", "background to the desktop picture");
        }
    }));
    tiles.push(tile("None", "appearance-bg-none".into(), el().fill(theme::paint().ground), current == "none", |s: &mut Prefs, _| {
        if !writing(s) {
            set(s, "background", "none", "no background");
        }
    }));
    let preset = presets.iter().any(|p| p.value == current);
    let custom = current.starts_with("mesh:") || current.starts_with("gradient:") && !preset;
    let custom_fill = if custom { swatch_fill(s, &current) } else { el().fill(theme::wash()).center().child(text("Make…").font(META).color(muted())) };
    tiles.push(tile("Custom…", "appearance-bg-custom".into(), custom_fill, custom, |s: &mut Prefs, _| {
        if !writing(s) {
            super::custom::open_mesh(s);
        }
    }));
    let image_fill = if current.starts_with("image:") { swatch_fill(s, &current) } else { el().fill(theme::wash()).center().child(text("Choose…").font(META).color(muted())) };
    tiles.push(tile("Image…", "appearance-bg-image".into(), image_fill, current.starts_with("image:"), super::choose_image));
    let mut grid = col().w_full().gap(12.0).px(14.0).py(12.0).key("appearance-backgrounds");
    let mut it = tiles.into_iter().peekable();
    while it.peek().is_some() {
        let mut r = row().w_full().gap(12.0);
        for t in it.by_ref().take(TILE_COLUMNS) {
            r = r.child(t);
        }
        grid = grid.child(r);
    }
    if matches!(s.appearance.presets, None) {
        grid = grid.child(text("Reading the built-in backgrounds…").font(META).color(muted()));
    }
    if let Some(Err(f)) = &s.appearance.presets {
        grid = grid.child(text(format!("Couldn't read the built-in backgrounds: {}", f.message)).font(META).color(theme::failed()).wrap());
    }
    grid
}
