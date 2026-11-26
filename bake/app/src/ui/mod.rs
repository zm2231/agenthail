
use feed::catalog::{self, PHASES, Session, State};
use rune::{Color, Cx, El, Icon, ScrollSpec, SystemMaterial, Transition, Tx, col, el, icon, row, text, text_input};
use rune_kit::{Button, ButtonVariant, Keybind, KeybindVariant};

use crate::icons;
use crate::theme::{
    self, BODY, BODY_BOLD, HEADING, LABEL, META, META_BOLD, SPRING, TITLE, accent, accent_deep, attention, card, done, failed, faint, line, muted, raised, wash,
};
use crate::transcript::{self, session_avatar};
use crate::{App, Screen, Tab, answer_request, pick_work, session_edits, short_path, show_changes, send, draft_reply, session, toggle_pick, open_link};

mod chips;
pub mod new_session;
mod rail;
mod session;
mod session_dialogs;
pub mod session_menu;
mod waiting;

use rail::*;
use session::*;

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

pub fn view(s: &App, cx: &mut Cx<App>) -> El {
    let mut layer = Vec::new();
    let body = match &s.screen {
        Screen::Session => session_body(s, cx),
        _ => crate::over::view(s, cx, &mut layer),
    };
    crate::over::overlays(s, &mut layer);
    session_menu::overlays(s, &mut layer);
    new_session::view(s, &mut layer);
    let root = col()
        .w_full()
        .h_full()
        .child(el().absolute(0.0, 0.0).w_full().h_full().system_material(SystemMaterial::Glass).fill(theme::card_deep()))
        .child(el().absolute(0.0, 0.0).w_full().h_full().fill(theme::glass_tint()))
        .child(body);
    rune_kit::root(root, layer)
}

fn session_body(s: &App, cx: &mut Cx<App>) -> El {
    let selected = s.selected.as_deref();
    let x = selected.and_then(|id| session(s, id));
    let project = x.map(|x| x.project_key()).filter(|k| s.ov.d.project(k).is_some());
    let sidebar = crate::over::ui::sidebar(s, project.as_deref(), selected);
    crate::over::ui::frame(cx, s, Some(sidebar), session_screen(s), x.map(|x| rail(s, x)), session_toolbar(s, x))
}

fn session_toolbar(s: &App, x: Option<&Session>) -> rune_kit::shell::Toolbar {
    let bar = rune_kit::shell::Toolbar::new().trailing(crate::over::ui::needs_button(s));
    let Some(x) = x else { return bar };
    let (name, key) = (x.project_name(), x.project_key());
    let title = s.ov.d.session(&x.id).map(|w| s.ov.d.title(w)).unwrap_or_else(|| x.title());
    let crumb = crate::over::ui::crumb(&[(&name, false), (&crate::over::ui::short_title(&title, 40), true)]);
    let crumb = if s.ov.d.project(&key).is_some() { row().on_press(move |s: &mut App| crate::over::go(s, Screen::Project(key.clone()))).child(crumb) } else { crumb };
    bar.leading(row().gap(14.0).items_center().child(crate::over::ui::avatar(&name, 18.0)).child(crumb))
}
