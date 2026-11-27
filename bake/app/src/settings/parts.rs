use feed::prefs::Failure;
use rune::{El, ScrollSpec, Tx, col, el, row, text};
use rune_kit::{Kit, Select, SelectAppearance, Switch};

use super::{Prefs, toggle_select};
use crate::theme::{self, BODY, BODY_BOLD, META, attention, failed, faint, muted};

pub const WIDTH: f32 = 600.0;

pub fn scroll(page: El) -> El {
    col().key("settings-scroll").w_full().grow(1.0).items_center().scroll(ScrollSpec { stick_bottom: false }).child(page.w(WIDTH).pb(28.0))
}

pub fn page() -> El {
    col().gap(22.0).pt(6.0)
}

pub fn section(title: &str, rows: Vec<El>, footer: Option<String>) -> El {
    let mut card = col().w_full().radius(10.0).fill(theme::card()).border(1.0, theme::line());
    for (i, r) in rows.into_iter().enumerate() {
        if i > 0 {
            card = card.child(el().w_full().h(1.0).fill(theme::line()));
        }
        card = card.child(r);
    }
    let mut out = col().w_full().gap(7.0);
    if !title.is_empty() {
        out = out.child(text(title.to_string()).font(BODY_BOLD).color(theme::text()).px(4.0));
    }
    out = out.child(card);
    if let Some(footer) = footer {
        out = out.child(text(footer).font(META).color(muted()).wrap().px(4.0));
    }
    out
}

pub fn line(label: &str, detail: Option<String>, control: El) -> El {
    let mut words = col().gap(2.0).grow(1.0).min_w(0.0).child(text(label.to_string()).font(BODY).color(theme::text()).wrap());
    if let Some(detail) = detail.filter(|d| !d.is_empty()) {
        words = words.child(text(detail).font(META).color(muted()).wrap());
    }
    row().w_full().gap(16.0).items_center().px(14.0).py(10.0).child(words).child(control)
}

pub fn fact(label: &str, value: String) -> El {
    row().w_full().gap(16.0).items_center().px(14.0).py(8.0).child(text(label.to_string()).font(BODY).color(theme::text()).grow(1.0)).child(text(value).font(BODY).color(muted()))
}

pub fn words(value: impl Into<String>) -> El {
    row().w_full().px(14.0).py(10.0).child(text(value.into()).font(META).color(muted()).wrap().grow(1.0))
}

pub fn problem(value: impl Into<String>) -> El {
    row().w_full().px(14.0).py(10.0).child(text(value.into()).font(META).color(failed()).wrap().grow(1.0))
}

pub fn failure(s: &Prefs, f: &Failure) -> El {
    let f = &super::explain(s, f);
    let color = if f.denied || f.uncertain { attention() } else { failed() };
    text(f.message.clone()).font(META).color(color).wrap().px(4.0)
}

pub fn reading(what: &str) -> El {
    text(format!("Reading {what}…")).font(META).color(faint()).px(4.0)
}

pub fn switch(key: &str, label: &str, on: bool, disabled: bool, flip: impl Fn(&mut Prefs) + 'static, k: &Kit) -> El {
    Switch::new(on).label(label.to_string()).hide_label().disabled(disabled).on_toggle(flip).build(k).key(format!("settings-{key}"))
}

pub fn select(s: &Prefs, key: &'static str, label: &str, choices: Vec<(String, String)>, current: &str, disabled: bool, layer: &mut Vec<El>, choose: impl Fn(&mut Prefs, String) + Clone + 'static, k: &Kit) -> El {
    let labels: Vec<&str> = choices.iter().map(|(label, _)| label.as_str()).collect();
    let picked = choices.iter().find(|(_, value)| value == current).map(|(label, _)| label.as_str());
    let values = choices.clone();
    Select::new(key, &labels)
        .appearance(SelectAppearance::Field)
        .width(220.0)
        .current(picked)
        .placeholder("Choose…")
        .label(label.to_string())
        .disabled(disabled)
        .open(s.open_select == Some(key))
        .on_toggle(move |s: &mut Prefs| toggle_select(s, key))
        .on_close(|s: &mut Prefs| s.open_select = None)
        .on_choose(move |s: &mut Prefs, label: &str, _: &mut Tx| {
            s.open_select = None;
            if let Some((_, value)) = values.iter().find(|(l, _)| l == label) {
                choose(s, value.clone());
            }
        })
        .build(k, layer)
}
