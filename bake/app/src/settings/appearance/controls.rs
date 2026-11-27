use feed::look::{self as fl, Change, View};
use rune::{Color, El, el, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Kit, SegmentedControl, Slider};

use super::{Prefs, number, set, slide, write, writing};
use crate::settings::parts;
use crate::theme::{self, META, muted};

const ACCENTS: [&str; 6] = ["#4f8ff7", "#3fb68b", "#e9a23b", "#e5534b", "#a371f7", "#e86ba5"];

pub(super) fn chosen<'a>(view: &'a View, key: &str, fallback: &'a str) -> &'a str {
    view.chosen.get(key).map(String::as_str).unwrap_or(fallback)
}

pub(super) fn value(s: &Prefs, view: &View, key: &'static str, fallback: f32) -> f32 {
    s.appearance.drafts.get(key).copied().unwrap_or_else(|| view.values.get(key).and_then(|v| v.as_f64()).map(|n| n as f32).unwrap_or(fallback))
}

pub(super) fn override_chip(s: &Prefs, view: &View, key: &'static str, k: &Kit) -> Option<El> {
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
            .build(k)
            .key(format!("appearance-clear-{key}")),
    )
}

pub(super) fn row_for(s: &Prefs, view: &View, key: &'static str, label: &str, control: El, k: &Kit) -> El {
    let control = row().gap(8.0).items_center().child(control).children(override_chip(s, view, key, k));
    parts::line(label, None, control, k)
}

pub(super) fn segmented(s: &Prefs, view: &View, key: &'static str, label: &'static str, choices: &'static [(&'static str, &'static str)], fallback: &str, disabled: bool, k: &Kit) -> El {
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
        .build(k)
        .key(format!("appearance-{key}"));
    row_for(s, view, key, label, control, k)
}

pub(super) fn slider(s: &Prefs, view: &View, key: &'static str, label: &'static str, range: (f32, f32), fallback: f32, unit: &str, disabled: bool, k: &Kit) -> El {
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
                .build(k),
        )
        .child(text(format!("{}{unit}", number(v))).font(k.text(META)).color(muted()).w(44.0));
    row_for(s, view, key, label, control, k)
}

pub(super) fn switch(s: &Prefs, view: &View, key: &'static str, label: &'static str, on_value: &'static str, disabled: bool, k: &Kit) -> El {
    let on = chosen(view, key, on_value) == on_value;
    let control = parts::switch(key, label, on, disabled || writing(s), move |s: &mut Prefs| {
        let next = if on { "off" } else { on_value };
        set(s, key, next, &format!("{} {}", label.to_lowercase(), if on { "off" } else { "on" }));
    }, k);
    row_for(s, view, key, label, control, k)
}

pub(super) fn swatch(fill: El, selected: bool, help: &str, press: impl Fn(&mut Prefs) + 'static) -> El {
    let ring = if selected { theme::text() } else { Color::TRANSPARENT };
    el().size(24.0, 24.0).radius(12.0).border(1.5, ring).center().child(fill.size(18.0, 18.0).radius(9.0)).key(format!("appearance-swatch-{help}")).role("button").label(help.to_string()).help(help.to_string()).on_press(press)
}

pub(super) fn accents(s: &Prefs, view: &View, k: &Kit) -> El {
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
    let custom = current != "system" && !ACCENTS.contains(&current.as_str());
    let any = if custom {
        let c = fl::hex(&current).unwrap_or([0.5; 3]);
        el().fill(Color { r: fl::linear(c[0]), g: fl::linear(c[1]), b: fl::linear(c[2]), a: 1.0 })
    } else {
        el().fill(theme::wash()).border(1.0, theme::faint()).center().child(text("+").font(k.text(META)).color(theme::text()))
    };
    r = r.child(swatch(any, custom, "Any colour", |s: &mut Prefs| {
        if !writing(s) {
            super::custom::open_accent(s);
        }
    }));
    row_for(s, view, "theme.accent", "Accent", r, k)
}
