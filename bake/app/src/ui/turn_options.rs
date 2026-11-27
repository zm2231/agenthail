use std::collections::HashMap;

use feed::catalog::Session;
use feed::composer::{TurnOptions, turn_label};
use feed::create::CODEX_SERVICE_TIERS;
use rune::{El, TextEdit, col, el, icon, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog};

use crate::theme::{self, META, META_BOLD, failed, faint, muted};
use crate::{App, icons};

#[derive(Default)]
pub struct Draft {
    tier: String,
    schema: TextEdit,
}

#[derive(Default)]
pub struct Turns {
    open: Option<String>,
    drafts: HashMap<String, Draft>,
}

pub fn takes(x: &Session) -> bool {
    x.surface == feed::create::CODEX
}

pub fn options(s: &App, id: &str) -> Result<Option<TurnOptions>, &'static str> {
    match (crate::session(s, id).filter(|x| takes(x)), s.turn.drafts.get(id)) {
        (Some(_), Some(d)) => TurnOptions::read(&d.tier, &d.schema.text),
        _ => Ok(None),
    }
}

pub fn problem(s: &App, id: &str) -> Option<&'static str> {
    options(s, id).err()
}

pub fn rekey(s: &mut App, old: &str, new: &str) {
    if let Some(d) = s.turn.drafts.remove(old) {
        s.turn.drafts.entry(new.to_string()).or_insert(d);
    }
    if s.turn.open.as_deref() == Some(old) {
        s.turn.open = Some(new.to_string());
    }
}

pub fn chip(s: &App, x: &Session) -> Option<El> {
    if !takes(x) {
        return None;
    }
    let (tier, schema) = s.turn.drafts.get(&x.id).map(|d| (d.tier.as_str(), d.schema.text.as_str())).unwrap_or_default();
    let label = turn_label(tier, schema);
    let color = if problem(s, &x.id).is_some() { failed() } else { muted() };
    let id = x.id.clone();
    Some(
        row()
            .key("composer-turn-options")
            .gap(4.0)
            .items_center()
            .px(8.0)
            .py(3.0)
            .radius(7.0)
            .hover(crate::theme::ink(0.08))
            .role("button")
            .label(format!("Turn options, {label}"))
            .help("Service tier and output schema for messages you send to this session")
            .on_press(move |s: &mut App| open(s, &id))
            .child(text(label).font(META_BOLD).color(color))
            .child(icon(&icons::SELECTOR, 11.0).color(faint())),
    )
}

fn open(s: &mut App, id: &str) {
    let draft = s.turn.drafts.entry(id.to_string()).or_default();
    draft.schema.focused = false;
    s.turn.open = Some(id.to_string());
    s.composer.focused = false;
}

fn close(s: &mut App) {
    if let Some(id) = s.turn.open.take()
        && s.turn.drafts.get(&id).is_some_and(|d| d.tier.is_empty() && d.schema.text.trim().is_empty())
    {
        s.turn.drafts.remove(&id);
    }
    s.composer.focused = true;
}

fn clear(s: &mut App) {
    if let Some(id) = s.turn.open.clone() {
        s.turn.drafts.remove(&id);
    }
    close(s);
}

fn pick(s: &mut App, tier: &str) {
    if let Some(d) = s.turn.open.clone().and_then(|id| s.turn.drafts.get_mut(&id)) {
        d.tier = tier.to_string();
    }
}

fn schema_of(s: &mut App) -> &mut TextEdit {
    let id = s.turn.open.clone().unwrap_or_default();
    &mut s.turn.drafts.entry(id).or_default().schema
}

fn dialog(s: &App, id: &str, layer: &mut Vec<El>) {
    let k = theme::kit();
    let Some(d) = s.turn.drafts.get(id) else { return };
    let mut tiers = row().w_full().gap(4.0);
    for (value, label) in std::iter::once(("", "Session default")).chain(CODEX_SERVICE_TIERS) {
        let chosen = d.tier == value;
        let mut t = row()
            .key(format!("turn-tier-{}", if value.is_empty() { "session" } else { value }))
            .px(9.0)
            .py(4.0)
            .radius(7.0)
            .role("button")
            .label(label)
            .on_press(move |s: &mut App| pick(s, value))
            .child(text(label).font(META_BOLD).color(if chosen { theme::text() } else { faint() }));
        t = if chosen { t.fill(crate::theme::ink(0.10)) } else { t.hover(crate::theme::ink(0.06)) };
        tiers = tiers.child(t);
    }
    let input = text_input(&d.schema, META.mono())
        .key("turn-schema-input")
        .label("Output JSON schema")
        .w_full()
        .min_h(90.0)
        .color(theme::text())
        .placeholder(r#"{"type": "object", …}"#, faint())
        .on_edit(schema_of);
    let field = col().w_full().px(10.0).py(7.0).radius(8.0).fill(theme::wash()).border(1.0, theme::line()).child(input);
    let mut body = col()
        .w_full()
        .gap(10.0)
        .px(16.0)
        .pb(16.0)
        .child(text("Service tier").font(META_BOLD).color(muted()))
        .child(tiers)
        .child(text("Output JSON schema").font(META_BOLD).color(muted()))
        .child(field);
    if let Some(problem) = TurnOptions::read(&d.tier, &d.schema.text).err() {
        body = body.child(text(problem).font(META).color(failed()).wrap().w_full());
    }
    body = body.child(text("Applies to each message you send here until you clear it. Steer can't carry these, so a message with them waits for the turn to end.").font(META).color(faint()).wrap().w_full());
    let buttons = row()
        .w_full()
        .gap(8.0)
        .child(Button::new("Clear").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(clear).build(k).key("turn-options-clear"))
        .child(el().grow(1.0))
        .child(Button::new("Done").variant(ButtonVariant::Contrast).control_size(ControlSize::Small).on_press(close).build(k).key("turn-options-done"));
    Dialog::new("turn-options", "Turn options")
        .description("For messages you send to this Codex session.")
        .open(true)
        .fit()
        .width(400.0)
        .on_close(close)
        .child(body.child(buttons))
        .build(k, layer);
}

pub fn overlays(s: &App, layer: &mut Vec<El>) {
    if let Some(id) = &s.turn.open {
        dialog(s, id, layer);
    }
}
