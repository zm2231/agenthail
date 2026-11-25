use std::collections::{BTreeMap, BTreeSet};

use feed::catalog::{self, BoardRow, PHASES, Session, State};
use rune::{Color, Cx, El, Icon, ScrollSpec, SystemMaterial, TextStyle, Transition, Tx, col, el, icon, row, text, text_input};
use rune_kit::{Button, ButtonVariant, Keybind, KeybindVariant};

use crate::icons;
use crate::theme::{
    self, BODY, BODY_BOLD, HEADING, HERO, LABEL, META, META_BOLD, NUMBER, SPRING, TITLE, accent, accent_deep, attention, card, done, failed, faint, line, muted, raised, wash,
};
use crate::transcript::{self, session_avatar};
use crate::{App, Screen, Tab, answer_request, pick_work, session_edits, short_path, show_changes, open_palette, open_project, select, send, send_text, session};

mod palette;
mod project;
mod rail;
mod session;
mod sidebar;
mod waiting;

pub use sidebar::{next_waiting, ordered};
use palette::*;
use project::*;
use rail::*;
use session::*;
use sidebar::*;
use waiting::*;

pub(crate) fn state_color(state: State) -> Color {
    match state {
        State::NeedsYou => attention(),
        State::Working => accent(),
        State::Done => done(),
        State::Idle => faint(),
    }
}

pub(crate) fn state_icon(state: State) -> &'static Icon {
    match state {
        State::NeedsYou => &icons::HAND,
        State::Working => &icons::BOLT,
        State::Done => &icons::CHECK_CIRCLE,
        State::Idle => &icons::CLOCK,
    }
}

pub(crate) fn spacer() -> El {
    el().grow(1.0)
}

pub(crate) fn label(value: &str) -> El {
    text(value.to_uppercase()).font(LABEL).color(faint())
}

pub(crate) fn chip(glyph: &'static Icon, value: impl Into<String>) -> El {
    row()
        .gap(5.0)
        .items_center()
        .px(8.0)
        .py(3.0)
        .radius(7.0)
        .fill(wash())
        .border(1.0, line())
        .child(icon(glyph, 12.0).color(faint()))
        .child(text(value.into()).font(META).color(muted()).truncate().max_w(220.0))
}

pub(crate) fn pill(glyph: &'static Icon, value: impl Into<String>, color: Color) -> El {
    row()
        .gap(5.0)
        .items_center()
        .px(10.0)
        .py(5.0)
        .radius(20.0)
        .fill(color.alpha(0.14))
        .border(1.0, color.alpha(0.30))
        .child(icon(glyph, 12.0).color(color))
        .child(text(value.into()).font(META_BOLD).color(color))
}

pub(crate) fn keys(k: &[&str]) -> El {
    Keybind::new(k).variant(KeybindVariant::Ghost).build(theme::kit())
}

pub(crate) fn status_mark(state: State) -> El {
    let color = state_color(state);
    let dot = el().size(10.0, 10.0).radius(5.0).fill(color).border(2.0, theme::card_deep());
    if state == State::NeedsYou { dot.shadow(0.0, 8.0, color.alpha(0.8)) } else { dot }
}

pub(crate) fn avatar_with_state(name: &str, state: State, size: f32) -> El {
    col().size(size, size).child(session_avatar(name, size)).child(status_mark(state).absolute(size - 9.0, size - 9.0))
}

pub fn view(s: &App, _: &mut Cx<App>) -> El {
    let main = match &s.screen {
        Screen::Session => session_screen(s),
        Screen::Project(key) => project_screen(s, key),
    };
    let content = col()
        .grow(1.0)
        .h_full()
        .min_w(0.0)
        .my(8.0)
        .radius(16.0)
        .continuous_corners()
        .fill(card())
        .border(1.0, Color::WHITE.alpha(0.08))
        .shadow(10.0, 40.0, Color::BLACK.alpha(0.40))
        .clip_rounded(16.0)
        .child(main);
    let mut body = row().w_full().h_full().child(sidebar(s)).child(content);
    match (&s.screen, s.selected.as_deref().and_then(|id| session(s, id))) {
        (Screen::Session, Some(x)) => body = body.child(rail(s, x)),
        _ => body = body.child(el().w(8.0)),
    }
    let mut root = col()
        .w_full()
        .h_full()
        .child(el().absolute(0.0, 0.0).w_full().h_full().system_material(SystemMaterial::Glass).fill(theme::card_deep()))
        .child(el().absolute(0.0, 0.0).w_full().h_full().fill(theme::glass_tint()))
        .child(body);
    if let Some(edit) = &s.palette {
        root = root.child(palette(s, edit));
    }
    root
}
