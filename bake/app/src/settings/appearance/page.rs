use feed::look::{self as fl, Change, View, Wallpaper};
use rune::{Color, El, col, el, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, SegmentedControl, Slider};

use super::{Prefs, number, scope, set, slide, write, writing};
use crate::settings::parts;
use crate::theme::{self, BODY, META, META_BOLD, muted};

const ACCENTS: [&str; 6] = ["#4f8ff7", "#3fb68b", "#e9a23b", "#e5534b", "#a371f7", "#e86ba5"];
const TILE_COLUMNS: usize = 5;
const TILE_WIDTH: f32 = 104.0;

fn chosen<'a>(view: &'a View, key: &str, fallback: &'a str) -> &'a str {
    view.chosen.get(key).map(String::as_str).unwrap_or(fallback)
}

fn value(s: &Prefs, view: &View, key: &'static str, fallback: f32) -> f32 {
    s.appearance.drafts.get(key).copied().unwrap_or_else(|| view.values.get(key).and_then(|v| v.as_f64()).map(|n| n as f32).unwrap_or(fallback))
}

fn override_chip(s: &Prefs, view: &View, key: &'static str) -> Option<El> {
    let line = view.overrides.get(key)?.clone();
    let owner = fl::owner(&line, &crate::look::device());
    if s.appearance.here && owner == "This computer" {
        return None;
    }
    let change = Change::clear_override(key, &line, &owner);
    Some(
        Button::new(format!("{owner} ↺"))
            .variant(ButtonVariant::Ghost)
            .control_size(ControlSize::Small)
            .disabled(writing(s))
            .on_press(move |s: &mut Prefs| write(s, change.clone()))
            .build(theme::kit())
            .key(format!("appearance-clear-{key}")),
    )
}

fn row_for(s: &Prefs, view: &View, key: &'static str, label: &str, control: El) -> El {
    let control = row().gap(8.0).items_center().child(control).children(override_chip(s, view, key));
    parts::line(label, None, control)
}

fn segmented(s: &Prefs, view: &View, key: &'static str, label: &'static str, choices: &'static [(&'static str, &'static str)], fallback: &str, disabled: bool) -> El {
    let current = chosen(view, key, fallback);
    let labels: Vec<&str> = choices.iter().map(|(l, _)| *l).collect();
    let picked = choices.iter().position(|(_, v)| *v == current);
    let control = SegmentedControl::new(&labels, picked)
        .label(label)
        .fit()
        .on_pick(move |s: &mut Prefs, i, _| {
            if disabled || writing(s) {
                return;
            }
            if let Some((_, v)) = choices.get(i) {
                set(s, key, v, &format!("{} to {v}", label.to_lowercase()));
            }
        })
        .build(theme::kit())
        .key(format!("appearance-{key}"));
    row_for(s, view, key, label, control)
}

fn slider(s: &Prefs, view: &View, key: &'static str, label: &'static str, range: (f32, f32), fallback: f32, unit: &str, disabled: bool) -> El {
    let v = value(s, view, key, fallback);
    let control = row()
        .gap(10.0)
        .items_center()
        .child(
            Slider::new(format!("appearance-{key}"), v)
                .range(range.0..=range.1)
                .step(0.01)
                .label(label)
                .hide_label()
                .width(200.0)
                .disabled(disabled || writing(s))
                .on_change(move |s: &mut Prefs, n, done| slide(s, key, &label.to_lowercase(), n, done))
                .build(theme::kit()),
        )
        .child(text(format!("{}{unit}", number(v))).font(META).color(muted()).w(44.0));
    row_for(s, view, key, label, control)
}

fn switch(s: &Prefs, view: &View, key: &'static str, label: &'static str, on_value: &'static str, disabled: bool) -> El {
    let on = chosen(view, key, on_value) == on_value;
    let control = parts::switch(key, label, on, disabled || writing(s), move |s: &mut Prefs| {
        let next = if on { "off" } else { on_value };
        set(s, key, next, &format!("{} {}", label.to_lowercase(), if on { "off" } else { "on" }));
    });
    row_for(s, view, key, label, control)
}

fn swatch(fill: El, selected: bool, help: &str, press: impl Fn(&mut Prefs) + 'static) -> El {
    let ring = if selected { theme::text() } else { Color::TRANSPARENT };
    el().size(24.0, 24.0).radius(12.0).border(1.5, ring).center().child(fill.size(18.0, 18.0).radius(9.0)).label(help.to_string()).help(help.to_string()).on_press(press)
}

fn accents(s: &Prefs, view: &View) -> El {
    let current = chosen(view, "theme.accent", "system").to_string();
    let mut r = row().gap(6.0).items_center().key("appearance-accents");
    let rainbow = el().linear_gradient(std::f32::consts::FRAC_PI_2, [(0.0, Color { r: 1.0, g: 0.1, b: 0.1, a: 1.0 }), (0.5, Color { r: 0.1, g: 0.8, b: 0.2, a: 1.0 }), (1.0, Color { r: 0.3, g: 0.2, b: 1.0, a: 1.0 })]);
    r = r.child(swatch(rainbow, current == "system", "System accent", |s: &mut Prefs| {
        if !writing(s) {
            set(s, "theme.accent", "system", "accent to the system accent");
        }
    }));
    for hex in ACCENTS {
        let c = fl::hex(hex).unwrap_or([0.5; 3]);
        let fill = el().fill(Color { r: fl::linear(c[0]), g: fl::linear(c[1]), b: fl::linear(c[2]), a: 1.0 });
        r = r.child(swatch(fill, current == hex, hex, move |s: &mut Prefs| {
            if !writing(s) {
                set(s, "theme.accent", hex, &format!("accent to {hex}"));
            }
        }));
    }
    if current != "system" && !ACCENTS.contains(&current.as_str()) {
        r = r.child(text(current).font(META).color(muted()));
    }
    row_for(s, view, "theme.accent", "Accent", r)
}

fn swatch_fill(s: &Prefs, value: &str) -> El {
    let key = match fl::parse_background(value) {
        Some(Wallpaper::Image(path)) => path,
        Some(Wallpaper::Linear { angle, stops }) => {
            let stops: Vec<(f32, Color)> = stops.iter().map(|(c, at)| (*at, Color { r: fl::linear(c[0]), g: fl::linear(c[1]), b: fl::linear(c[2]), a: 1.0 })).collect();
            return el().linear_gradient(angle.to_radians(), stops);
        }
        _ => value.to_string(),
    };
    match s.appearance.thumbs.get(&key) {
        Some(p) => el().pixels_fit(p, rune::SurfaceFit::Fill),
        None => el().fill(theme::wash()),
    }
}

fn tile(name: &str, key: String, fill: El, selected: bool, press: impl Fn(&mut Prefs, &mut rune::Tx) + 'static) -> El {
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
        .child(fill.w_full().h(52.0).radius(8.0).border(border.0, border.1))
        .child(text(name.to_string()).font(if selected { META_BOLD } else { META }).color(if selected { theme::text() } else { muted() }).truncate())
}

fn grid(s: &Prefs, view: &View) -> El {
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

fn history(s: &Prefs) -> El {
    let k = theme::kit();
    let now = time::OffsetDateTime::now_utc();
    let entries = match &s.appearance.history {
        Some(Ok(e)) => e.clone(),
        _ => Vec::new(),
    };
    let last = match entries.first() {
        Some(e) => col().gap(2.0).grow(1.0).min_w(0.0).child(text(e.message.clone()).font(BODY).color(theme::text()).truncate()).child(text(fl::describe(e, now)).font(META).color(muted())),
        None => col().grow(1.0).child(text("No changes yet").font(BODY).color(muted())),
    };
    let busy = writing(s);
    let undo = Button::new("Undo").control_size(ControlSize::Small).disabled(busy || entries.len() < 2).on_press(|s: &mut Prefs| write(s, Change::undo(None))).build(k).key("appearance-undo");
    let reset = Button::new("Reset to Default…").control_size(ControlSize::Small).disabled(busy).on_press(|s: &mut Prefs| s.appearance.confirm_reset = true).build(k).key("appearance-reset");
    let mut rows = vec![row().w_full().gap(10.0).items_center().px(14.0).py(10.0).child(last).child(undo).child(reset)];
    if s.appearance.confirm_reset {
        let message = if s.appearance.here { "This removes every appearance setting made for this computer only." } else { "This removes every appearance setting that applies to all devices, and this computer's own settings that would hide the reset. Other devices keep their own settings." };
        let confirm = Button::new("Reset").variant(ButtonVariant::Danger).control_size(ControlSize::Small).disabled(busy).on_press(|s: &mut Prefs| {
            let change = Change::reset(&scope(s));
            write(s, change);
        });
        let cancel = Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut Prefs| s.appearance.confirm_reset = false);
        rows.push(
            row()
                .w_full()
                .gap(10.0)
                .items_center()
                .px(14.0)
                .py(10.0)
                .key("appearance-reset-confirm")
                .child(col().grow(1.0).min_w(0.0).gap(2.0).child(text("Reset appearance to the defaults?").font(BODY).color(theme::text())).child(text(message).font(META).color(muted()).wrap()))
                .child(cancel.build(k).key("appearance-reset-cancel"))
                .child(confirm.build(k).key("appearance-reset-go")),
        );
    }
    if entries.len() > 1 {
        let open = s.appearance.history_open;
        rows.push(
            row()
                .w_full()
                .px(14.0)
                .py(8.0)
                .key("appearance-history-toggle")
                .role("button")
                .on_press(|s: &mut Prefs| s.appearance.history_open = !s.appearance.history_open)
                .child(text(if open { "▾ History" } else { "▸ History" }).font(BODY).color(theme::text())),
        );
        if open {
            for (i, e) in entries.iter().enumerate().skip(1) {
                let rev = e.rev.clone();
                let undo = Button::new("Undo").control_size(ControlSize::Small).variant(ButtonVariant::Ghost).disabled(busy).on_press(move |s: &mut Prefs| write(s, Change::undo(Some(&rev)))).build(k).key(format!("appearance-undo-{i}"));
                rows.push(
                    row()
                        .w_full()
                        .gap(10.0)
                        .items_center()
                        .px(14.0)
                        .py(6.0)
                        .child(col().grow(1.0).min_w(0.0).gap(2.0).child(text(e.message.clone()).font(BODY).color(theme::text()).truncate()).child(text(fl::describe(e, now)).font(META).color(muted())))
                        .child(undo),
                );
            }
        }
    }
    if let Some(Err(f)) = &s.appearance.history {
        rows.push(parts::problem(format!("Couldn't read the change history: {}", f.message)));
    }
    parts::section("", rows, None)
}

pub fn page(s: &Prefs) -> El {
    let mut out = parts::page().key("appearance-page");
    let Some(view) = crate::look::view() else {
        return out.child(match crate::look::problem() {
            Some(p) => parts::problem(p),
            None if s.client.is_none() => parts::problem(super::super::unread(s).message),
            None => parts::reading("appearance settings"),
        });
    };
    if !view.problems.is_empty() {
        let rows = view.problems.iter().map(|p| parts::problem(format!("ui.conf line {}: {}", p.line, p.message))).collect();
        out = out.child(parts::section("Problems in ui.conf", rows, Some("These lines are skipped; the rest of the file still applies.".into())));
    }
    if let Some(p) = crate::look::problem() {
        out = out.child(text(p).font(META).color(theme::attention()).wrap().px(4.0));
    }
    if let Some(f) = s.appearance.error.clone() {
        out = out.child(parts::failure(s, &f));
    }
    let device = crate::look::device();
    let scope_control = SegmentedControl::new(&["All devices", "This computer only"], Some(usize::from(s.appearance.here)))
        .label("Apply changes to")
        .fit()
        .on_pick(|s: &mut Prefs, i, _| s.appearance.here = i == 1 && !crate::look::device().is_empty())
        .build(theme::kit())
        .key("appearance-scope");
    let scope_detail = (!device.is_empty()).then(|| format!("This computer is {device}."));
    out = out.child(parts::section(
        "",
        vec![
            parts::line("Apply changes to", scope_detail, scope_control),
            segmented(s, &view, "theme", "Appearance", &[("System", "system"), ("Light", "light"), ("Dark", "dark")], "system", false),
            accents(s, &view),
            slider(s, &view, "text.scale", "Text size", (0.8, 1.6), 1.0, "×", false),
        ],
        None,
    ));
    let is_image = chosen(&view, "background", "").starts_with("image:");
    let mut background = vec![grid(s, &view)];
    background.push(slider(s, &view, "background.dim", "Dim", (0.0, 0.8), 0.0, "", false));
    background.push(slider(s, &view, "background.opacity", "Strength on empty views", (0.0, 1.0), 1.0, "", false));
    background.push(slider(s, &view, "background.session-opacity", "Strength in a session", (0.0, 1.0), 1.0, "", false));
    if is_image {
        background.push(segmented(s, &view, "background.fit", "Fit", &[("Cover", "cover"), ("Contain", "contain"), ("Center", "center"), ("Tile", "tile")], "cover", false));
        background.push(segmented(s, &view, "background.position", "Position", &[("Center", "center"), ("Top", "top"), ("Bottom", "bottom"), ("Left", "left"), ("Right", "right")], "center", false));
    }
    out = out.child(parts::section("Background", background, None));
    let glass_off = chosen(&view, "glass", "on") != "on";
    out = out.child(parts::section(
        "Glass",
        vec![
            switch(s, &view, "glass", "Translucent panes", "on", false),
            slider(s, &view, "glass.blur", "Blur", (0.0, 64.0), 30.0, " pt", glass_off),
            slider(s, &view, "glass.opacity", "Pane tint", (0.0, 1.0), 0.3, "", glass_off),
            slider(s, &view, "glass.composer.opacity", "Floating controls tint", (0.0, 1.0), 0.5, "", glass_off),
            segmented(s, &view, "glass.tint", "Tint colour", &[("Window", "ground"), ("Accent", "accent")], "ground", glass_off),
            switch(s, &view, "glass.readability", "Keep text readable", "auto", glass_off),
        ],
        Some("Panes are translucent only in dark appearance; light appearance uses solid panes.".into()),
    ));
    let no_texture = chosen(&view, "texture", "none") == "none";
    out = out.child(parts::section(
        "Texture",
        vec![
            segmented(s, &view, "texture", "Texture", &[("None", "none"), ("Grain", "grain"), ("Scanlines", "scanlines")], "none", false),
            slider(s, &view, "texture.strength", "Strength", (0.0, 0.6), 0.2, "", no_texture),
        ],
        None,
    ));
    out = out.child(history(s));
    let notes = fl::NOT_DRAWN.iter().map(|n| parts::words(*n)).collect();
    out.child(parts::section("Not drawn in this app yet", notes, Some("These still apply on the other Agenthail apps.".into())))
}
