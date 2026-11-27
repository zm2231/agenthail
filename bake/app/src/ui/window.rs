use std::cell::RefCell;
use std::collections::HashMap;
use std::mem::swap;

use feed::composer::Cover;
use feed::windows::{Frame, Saved};
use rune::{AppSpec, Chrome, TextEdit, Tx, WindowOptions, WindowPlacement, WindowSpec};
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

    pub fn rename_seq(&self) -> u64 {
        self.rename_seq
    }

    pub fn popup(&self) -> &super::complete::Popup {
        &self.popup
    }
}

pub struct Opened {
    pub id: Id,
    pub session: String,
    pub frame: Option<Frame>,
    title: String,
    handle: Option<rune::WindowHandle>,
    menu: Option<(rune::AppHandle<App>, Option<(super::keys::MenuState, u64)>)>,
}

impl Opened {
    fn frame_or_saved(&self) -> Option<Frame> {
        self.handle.as_ref().and_then(rune::WindowHandle::placement).map(|now| saved_frame(self.frame, now)).or(self.frame)
    }

    fn note_normal(&mut self) {
        if let Some(now) = self.handle.as_ref().and_then(rune::WindowHandle::placement)
            && !now.maximized
            && !now.full_screen
        {
            self.frame = Some(placement_frame(now));
        }
    }
}

fn saved_frame(normal: Option<Frame>, now: WindowPlacement) -> Frame {
    let mut f = placement_frame(now);
    if (now.maximized || now.full_screen) && now.restore.is_none() && let Some(n) = normal {
        (f.x, f.y, f.width, f.height) = (n.x, n.y, n.width, n.height);
    }
    f
}

fn placement_frame(p: WindowPlacement) -> Frame {
    let ((x, y), (width, height)) = p.restore.filter(|_| p.maximized || p.full_screen).unwrap_or((p.position, p.content_size));
    Frame { x, y, width, height, scale: p.scale, maximized: p.maximized, full_screen: p.full_screen }
}

#[derive(Default)]
pub struct Windows {
    current: Id,
    stash: HashMap<Id, Local>,
    pub open: Vec<Opened>,
    next: Id,
    to_show: Vec<(Id, bool)>,
    look: u64,
    overlay: Option<Id>,
    overlay_kinds: u32,
    asked: HashMap<String, Id>,
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
        self.open.iter().map(|w| Saved { session: w.session.clone(), frame: w.frame_or_saved() }).collect()
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
    s.windows.open.push(Opened { id, session: session.clone(), frame: None, title: String::new(), handle: None, menu: None });
    s.windows.stash.insert(id, Local::blank(id, Some(session.clone()), Screen::Session));
    let back = s.windows.current;
    enter(s, id);
    crate::select(s, &session);
    enter(s, back);
    id
}

pub fn show(s: &mut App, session: &str) -> Id {
    let id = open(s, session);
    if let Some(handle) = s.windows.open.iter().find(|o| o.id == id).and_then(|o| o.handle.as_ref()) {
        handle.show();
        handle.focus();
    }
    queue(s, id, false);
    id
}

fn queue(s: &mut App, id: Id, restored: bool) {
    let shown = s.windows.open.iter().any(|o| o.id == id && o.handle.is_some());
    if !shown && !s.windows.to_show.iter().any(|(w, _)| *w == id) {
        s.windows.to_show.push((id, restored));
    }
}

pub fn take_to_show(s: &mut App) -> Vec<(Id, bool)> {
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
        queue(s, id, true);
    }
    ids
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
    s.windows.overlay.take_if(|w| *w == id);
    s.windows.asked.retain(|_, w| *w != id);
    s.windows.to_show.retain(|(o, _)| *o != id);
}

fn closed(s: &mut App, tx: &mut Tx) {
    if rune::quitting() {
        return;
    }
    if let Some(id) = tx.window().map(id_of) {
        crate::hotkey::closed(feed::hotkey::Window::Session(id));
        close(s, id);
    }
}

fn focused(_: &mut App, focused: bool, tx: &mut Tx) {
    if let Some(id) = tx.window().map(id_of).filter(|id| *id != MAIN) {
        crate::hotkey::focus(feed::hotkey::Window::Session(id), focused);
    }
}

thread_local! {
    static SHARED: RefCell<Option<rune::SharedState<App>>> = const { RefCell::new(None) };
}

pub fn share(app: App) -> rune::SharedState<App> {
    let shared = rune::SharedState::with_placeholder(app, crate::placeholder).on_enter(on_enter).on_leave(on_leave);
    SHARED.with_borrow_mut(|s| *s = Some(shared.clone()));
    shared
}

fn spec() -> AppSpec<App> {
    AppSpec {
        poll: Some(poll),
        on_key: Some(crate::on_key),
        fonts: rune_theme_opencode::FONTS,
        families: rune_theme_opencode::FAMILIES,
        min_size: Some((620.0, 480.0)),
        ..AppSpec::new("Session", (900.0, 820.0), crate::placeholder, super::session_window)
    }
}

pub fn note_frames(s: &mut App) {
    for o in &mut s.windows.open {
        o.note_normal();
    }
}

pub fn open_shown(s: &mut App) {
    let Some(shared) = SHARED.with_borrow(Clone::clone) else { return };
    for (id, restored) in take_to_show(s) {
        let Some(o) = s.windows.open.iter().find(|o| o.id == id) else { continue };
        let mut options = WindowOptions { transparent: true, resizable: true, visible: true, ..Default::default() };
        let mut spec = WindowSpec::new(key(id), spec());
        if let Some(f) = o.frame {
            options.position = Some((f.x, f.y));
            options.physical_size = Some((f.width, f.height));
            spec = spec.maximized(f.maximized).full_screen(f.full_screen);
        }
        spec = spec
            .options(options)
            .shared(&shared)
            .chrome(Chrome { full_size_content: true, traffic_lights: Some((20.0, 20.0)), drag_regions: true })
            .srgb_blend(true)
            .on_close(closed)
            .on_focus(focused)
            .on_quit(crate::on_quit)
            .map_app(crate::menubar::with_menus);
        if restored {
            spec = spec.inactive();
        }
        let app = spec.app.handle();
        let handle = rune::open_window(spec);
        if let Some(o) = s.windows.open.iter_mut().find(|o| o.id == id) {
            o.handle = Some(handle);
            o.menu = Some((app, None));
        }
    }
}

fn sync_menu(s: &mut App) {
    let id = s.windows.current;
    let state = (super::keys::menu_state(s), s.bar.revision());
    let stale = s.windows.open.iter().find(|o| o.id == id).and_then(|o| o.menu.as_ref()).is_some_and(|(_, shown)| *shown != Some(state));
    let Some(items) = crate::menubar::items(s, state.0).filter(|_| stale) else { return };
    if let Some((app, shown)) = s.windows.open.iter_mut().find(|o| o.id == id).and_then(|o| o.menu.as_mut()) {
        let _ = app.set_menu(items);
        *shown = Some(state);
    }
}

fn retitle(s: &mut App, tx: &mut Tx) {
    let id = s.windows.current;
    let Some(session) = s.windows.session(id) else { return };
    let title = crate::session(s, session).map(|x| x.title()).unwrap_or_else(|| "Session".into());
    if let Some(o) = s.windows.open.iter_mut().find(|o| o.id == id && o.title != title) {
        o.title = title.clone();
        tx.set_title(title);
    }
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
    claim(s);
    enter(s, MAIN);
}

fn open_kinds(s: &App) -> u32 {
    let menus = &s.menus;
    [
        s.ov.jump.is_some(),
        crate::over::note_open(s),
        s.ov.ext.sheets.is_open(),
        crate::over::projects::covering(s),
        super::turn_options::covering(s),
        super::native_queue::covering(s),
        menus.ops.adopting.is_some(),
        menus.ops.moving.is_some(),
        !menus.ops.notices.is_empty(),
        s.prs.linking.is_some(),
        s.inspect.editing.is_some(),
        s.inspect.logs.is_some(),
        s.limits.picking.is_some(),
        s.limits.confirm.is_some(),
        s.limits.consent.is_some(),
        crate::over::search::activity_open(s),
        s.accounts.open,
        s.accounts.planning.is_some(),
        s.new_session.is_some(),
    ]
    .iter()
    .enumerate()
    .fold(0, |kinds, (i, open)| if *open { kinds | 1 << i } else { kinds })
}

pub fn shared_open(s: &App) -> bool {
    open_kinds(s) != 0
}

pub fn shows_shared(s: &App) -> bool {
    s.windows.overlay.is_none_or(|w| w == s.windows.current)
}

fn claim(s: &mut App) {
    let kinds = open_kinds(s);
    if kinds == 0 {
        s.windows.overlay = None;
    } else if s.windows.overlay.is_none() || kinds & !s.windows.overlay_kinds != 0 {
        s.windows.overlay = Some(s.windows.current);
    }
    s.windows.overlay_kinds = kinds;
}

pub fn overlays_to(s: &mut App, w: Id) {
    s.windows.overlay_kinds = open_kinds(s);
    if s.windows.overlay_kinds != 0 {
        s.windows.overlay = Some(w);
    }
}

pub fn showing(s: &App, session: &str) -> Option<Id> {
    let shows = |screen: &Screen, selected: &Option<String>| *screen == Screen::Session && selected.as_deref() == Some(session);
    if shows(&s.screen, &s.selected) {
        return Some(s.windows.current);
    }
    s.windows.stash.iter().filter(|(id, _)| **id != s.windows.current).find(|(_, l)| shows(&l.screen, &l.selected)).map(|(id, _)| *id)
}

pub fn asking(s: &mut App, session: &str) {
    let w = s.windows.current;
    s.windows.asked.insert(session.to_string(), w);
}

pub fn overlay_for(s: &mut App, session: &str) {
    if let Some(w) = s.windows.asked.get(session).copied().or_else(|| showing(s, session)) {
        overlays_to(s, w);
    }
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
    retitle(s, tx);
    sync_menu(s);
    let id = s.windows.current;
    if let Some(o) = s.windows.open.iter_mut().find(|o| o.id == id) {
        o.note_normal();
    }
    crate::switch_composer(s);
    changed |= super::complete::pump(s);
    keep_draft(s);
    super::paste::settle(s);
    changed
}

#[cfg(test)]
#[path = "window_tests.rs"]
mod tests;
