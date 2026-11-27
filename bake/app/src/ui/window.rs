use std::collections::HashMap;
use std::mem::swap;

use feed::composer::Cover;
use feed::windows::{Frame, Saved};
use rune::TextEdit;
use rune_kit::diff::DiffState;

use crate::{App, Screen, Tab};

pub type Id = u64;
pub const MAIN: Id = 0;

pub struct Local {
    selected: Option<String>,
    screen: Screen,
    tab: Tab,
    composer: TextEdit,
    composer_for: Option<String>,
    diff: DiffState,
    diff_key: (String, usize),
    cover: Cover,
    revealed: Option<String>,
    wake: crate::wake::Wake,
    paste: Option<super::paste::Editing>,
    holder_menu: Option<String>,
    attach_menu: Option<super::attach::Open>,
    menu: Option<super::session_menu::Open>,
    chip: Option<(String, &'static str)>,
    confirm: Option<super::session_menu::Confirm>,
    renaming: Option<String>,
    rename_text: TextEdit,
    rename_error: Option<String>,
    rename_saving: bool,
    rename_seq: u64,
    popup: super::complete::Popup,
    inspector: rune_kit::shell::PaneState,
    look: u64,
}

impl Local {
    fn blank(id: Id, selected: Option<String>, screen: Screen) -> Local {
        Local {
            selected,
            screen,
            tab: Tab::Conversation,
            composer: TextEdit::new(""),
            composer_for: None,
            diff: DiffState::new(Vec::new()),
            diff_key: (String::new(), usize::MAX),
            cover: Cover::default(),
            revealed: None,
            wake: crate::wake::Wake::default(),
            paste: None,
            holder_menu: None,
            attach_menu: None,
            menu: None,
            chip: None,
            confirm: None,
            renaming: None,
            rename_text: TextEdit::new(""),
            rename_error: None,
            rename_saving: false,
            rename_seq: id << 32,
            popup: Default::default(),
            inspector: {
                let mut p = rune_kit::shell::PaneState::new(284.0).range(260.0, 520.0);
                p.set_open(false);
                p
            },
            look: u64::MAX,
        }
    }
}

impl Local {
    pub fn renaming(&self) -> Option<&str> {
        self.renaming.as_deref()
    }

    pub fn popup(&self) -> &super::complete::Popup {
        &self.popup
    }
}

pub struct Opened {
    pub id: Id,
    pub session: String,
    pub frame: Option<Frame>,
}

#[derive(Default)]
pub struct Windows {
    current: Id,
    stash: HashMap<Id, Local>,
    pub open: Vec<Opened>,
    next: Id,
    to_show: Vec<Id>,
    look: u64,
}

impl Windows {
    pub fn current(&self) -> Id {
        self.current
    }

    pub fn holds(&self, session: &str) -> bool {
        self.open.iter().any(|w| w.session == session)
    }

    pub fn of(&self, session: &str) -> Option<Id> {
        self.open.iter().find(|w| w.session == session).map(|w| w.id)
    }

    pub fn session(&self, id: Id) -> Option<&str> {
        self.open.iter().find(|w| w.id == id).map(|w| w.session.as_str())
    }

    pub fn saved(&self) -> Vec<Saved> {
        self.open.iter().map(|w| Saved { session: w.session.clone(), frame: w.frame }).collect()
    }
}

fn swap_in(s: &mut App, l: &mut Local) {
    swap(&mut s.selected, &mut l.selected);
    swap(&mut s.screen, &mut l.screen);
    swap(&mut s.tab, &mut l.tab);
    swap(&mut s.composer, &mut l.composer);
    swap(&mut s.composer_for, &mut l.composer_for);
    swap(&mut s.diff, &mut l.diff);
    swap(&mut s.diff_key, &mut l.diff_key);
    swap(&mut s.cover, &mut l.cover);
    swap(&mut s.revealed, &mut l.revealed);
    swap(&mut s.wake, &mut l.wake);
    swap(&mut s.paste, &mut l.paste);
    swap(&mut s.holder_menu, &mut l.holder_menu);
    swap(&mut s.attach.menu, &mut l.attach_menu);
    swap(&mut s.menus.open, &mut l.menu);
    swap(&mut s.menus.chip, &mut l.chip);
    swap(&mut s.menus.confirm, &mut l.confirm);
    swap(&mut s.menus.renaming, &mut l.renaming);
    swap(&mut s.menus.rename_text, &mut l.rename_text);
    swap(&mut s.menus.rename_error, &mut l.rename_error);
    swap(&mut s.menus.rename_saving, &mut l.rename_saving);
    s.menus.swap_rename_seq(&mut l.rename_seq);
    s.complete.swap_popup(&mut l.popup);
    swap(&mut s.ov.inspector, &mut l.inspector);
    swap(&mut s.windows.look, &mut l.look);
}

fn keep_draft(s: &mut App) {
    if let Some(id) = s.composer_for.clone() {
        s.drafts.set(&id, &super::paste::draft(s));
    }
}

fn take_draft(s: &mut App) {
    let Some(id) = s.composer_for.clone() else { return };
    let saved = s.drafts.get(&id).to_string();
    if saved != super::paste::draft(s) {
        super::paste::finish(s);
        super::paste::set_composer(s, &saved);
    }
}

pub fn enter(s: &mut App, w: Id) {
    if s.windows.current == w || (w != MAIN && !s.windows.open.iter().any(|o| o.id == w)) {
        return;
    }
    keep_draft(s);
    let mut next = s.windows.stash.remove(&w).unwrap_or_else(|| Local::blank(w, None, Screen::Now));
    swap_in(s, &mut next);
    let left = s.windows.current;
    s.windows.stash.insert(left, next);
    s.windows.current = w;
    take_draft(s);
}

pub fn open(s: &mut App, session: &str) -> Id {
    let session = feed::composer::current(&s.rekeyed, session).to_string();
    if let Some(id) = s.windows.of(&session) {
        return id;
    }
    s.windows.next += 1;
    let id = s.windows.next;
    s.windows.open.push(Opened { id, session: session.clone(), frame: None });
    s.windows.stash.insert(id, Local::blank(id, Some(session.clone()), Screen::Session));
    let back = s.windows.current;
    enter(s, id);
    crate::select(s, &session);
    enter(s, back);
    id
}

pub fn show(s: &mut App, session: &str) -> Id {
    let id = open(s, session);
    if !s.windows.to_show.contains(&id) {
        s.windows.to_show.push(id);
    }
    id
}

pub fn take_to_show(s: &mut App) -> Vec<Id> {
    std::mem::take(&mut s.windows.to_show)
}

pub fn restore(s: &mut App, saved: &[Saved]) -> Vec<Id> {
    let mut ids = Vec::new();
    for w in saved {
        let id = open(s, &w.session);
        if let Some(o) = s.windows.open.iter_mut().find(|o| o.id == id) {
            o.frame = w.frame;
        }
        ids.push(id);
        if !s.windows.to_show.contains(&id) {
            s.windows.to_show.push(id);
        }
    }
    ids
}

pub fn placed(s: &mut App, id: Id, frame: Frame) {
    if let Some(o) = s.windows.open.iter_mut().find(|o| o.id == id) {
        o.frame = Some(frame);
    }
}

pub fn close(s: &mut App, id: Id) {
    if id == MAIN || !s.windows.open.iter().any(|o| o.id == id) {
        return;
    }
    let back = if s.windows.current == id { MAIN } else { s.windows.current };
    enter(s, id);
    super::paste::finish(s);
    keep_draft(s);
    enter(s, back);
    s.windows.stash.remove(&id);
    s.windows.open.retain(|o| o.id != id);
    s.windows.to_show.retain(|o| *o != id);
}

pub fn rekeyed(s: &mut App, old: &str, new: &str) {
    for o in s.windows.open.iter_mut().filter(|o| o.session == old) {
        o.session = new.to_string();
    }
    for l in s.windows.stash.values_mut() {
        if l.selected.as_deref() == Some(old) {
            l.selected = Some(new.to_string());
        }
        if l.composer_for.as_deref() == Some(old) {
            l.composer_for = Some(new.to_string());
        }
    }
}

pub fn holder(s: &App, holds: impl Fn(&Local) -> bool) -> Option<Id> {
    s.windows.stash.iter().find(|(id, l)| **id != s.windows.current && holds(l)).map(|(id, _)| *id)
}

pub fn within(s: &mut App, w: Id, f: impl FnOnce(&mut App)) {
    let back = s.windows.current;
    enter(s, w);
    f(s);
    enter(s, back);
}

pub fn key(id: Id) -> String {
    if id == MAIN { "main".into() } else { format!("session-{id}") }
}

fn id_of(key: &str) -> Id {
    key.strip_prefix("session-").and_then(|n| n.parse().ok()).unwrap_or(MAIN)
}

pub fn on_enter(s: &mut App, key: &str) {
    enter(s, id_of(key));
}

pub fn on_leave(s: &mut App, _: &str) {
    enter(s, MAIN);
}

pub fn poll(s: &mut App, _: f64, tx: &mut rune::Tx) -> bool {
    let mut changed = false;
    if let Some(session) = s.windows.session(s.windows.current).map(str::to_string)
        && s.selected.as_deref() != Some(session.as_str())
    {
        crate::select(s, &session);
        changed = true;
    }
    changed |= crate::look::poll_seen(&mut s.windows.look, tx);
    crate::switch_composer(s);
    changed |= super::complete::pump(s);
    keep_draft(s);
    super::paste::settle(s);
    changed
}

#[cfg(test)]
#[path = "window_tests.rs"]
mod tests;
