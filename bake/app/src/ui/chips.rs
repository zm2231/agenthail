use feed::catalog::Session;
use feed::settings::{Chip, ContextUsage};
use rune::{El, icon, row, text};
use rune_kit::ProgressCircle;

use crate::App;
use crate::icons;
use crate::theme::{self, META, faint, line, muted, wash};
use crate::ui::session_menu::{chips_for, compact};

fn chip_el(s: &App, x: &Session, chip: &Chip) -> El {
    let busy = s.menus.busy.contains(&(x.id.clone(), chip.key));
    let label = if busy { "Changing…".to_string() } else { chip.label() };
    let mut el = row()
        .key(format!("chip-{}-trigger", chip.key))
        .gap(4.0)
        .items_center()
        .px(8.0)
        .py(3.0)
        .radius(7.0)
        .fill(wash())
        .border(1.0, line())
        .label(format!("{}, {}", chip.name, chip.label()))
        .child(text(label).font(META).color(muted()).truncate().max_w(200.0));
    if let Some(note) = &chip.note {
        el = el.description(note.clone());
    }
    if !chip.changeable || busy {
        return el;
    }
    let (id, key) = (x.id.clone(), chip.key);
    el.hover(crate::theme::ink(0.08))
        .on_press(move |s: &mut App| {
            s.menus.open = None;
            s.menus.chip = match &s.menus.chip {
                Some((open, k)) if *open == id && *k == key => None,
                _ => Some((id.clone(), key)),
            };
        })
        .child(icon(&icons::SELECTOR, 11.0).color(faint()))
}

fn context_el(usage: &ContextUsage) -> El {
    let mut r = row().key("context-use").gap(5.0).items_center().px(4.0).label(format!("Context {}", usage.label()));
    if let Some(f) = usage.fraction() {
        r = r.child(ProgressCircle::new(f).size(14.0).label("Context used").build(theme::kit()));
    }
    let words = if usage.compacting { "Compacting…".to_string() } else { usage.label() };
    r.child(text(words).font(META).color(faint()))
}

fn compact_el(s: &App, x: &Session) -> El {
    let busy = s.menus.busy.contains(&(x.id.clone(), "compact"));
    let id = x.id.clone();
    let mut b = row()
        .key("compact")
        .gap(4.0)
        .items_center()
        .px(8.0)
        .py(3.0)
        .radius(7.0)
        .border(1.0, line())
        .child(icon(&icons::SHRINK, 11.0).color(faint()))
        .child(text(if busy { "Compacting…" } else { "Compact" }).font(META).color(muted()));
    if !busy {
        b = b.hover(crate::theme::ink(0.08)).on_press(move |s: &mut App| compact(s, &id));
    }
    b
}

pub fn turn(s: &App, x: &Session) -> El {
    let mut r = row().key("turn-chips").w_full().flex_wrap().gap(6.0).items_center();
    for chip in chips_for(s, &x.id) {
        r = r.child(chip_el(s, x, &chip));
    }
    if let Some(usage) = s.menus.context.get(&x.id) {
        r = r.child(context_el(usage));
    } else if let Some(error) = s.menus.context_error.get(&x.id) {
        r = r.child(text(format!("Context use unknown: {error}")).font(META).color(faint()).truncate().max_w(280.0).key("context-use"));
    }
    if x.capabilities.compact {
        r = r.child(compact_el(s, x));
    }
    if let Some(note) = s.menus.note.get(&x.id) {
        r = r.child(text(note.clone()).font(META).color(faint()));
    }
    r
}
