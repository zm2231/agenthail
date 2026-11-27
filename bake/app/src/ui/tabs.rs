use feed::derive::needs::Target;
use feed::tabs::{self, Tabs};
use rune::{El, Tx, row, text};
use rune_kit::shell::{TabItem, TabItemStatus, TabOrientation, TabStrip};
use rune_kit::{Kit, Menu, MenuItem};

use super::window::{self, MAIN};
use crate::theme::{META, accent, muted};
use crate::over::rows::Part;
use crate::{App, Screen};

pub const REOPEN_SECS: f64 = 6.0;
pub const ARCHIVED_SECS: f64 = 10.0;

#[derive(Clone)]
pub struct Archived {
    pub id: String,
    pub title: String,
    pub until: f64,
    pub unarchiving: bool,
}

pub struct State {
    pub tabs: Tabs,
    capacity: usize,
    pub closed: Option<(String, String, f64)>,
    pub archived: Option<Archived>,
    pub menu: Option<(String, rune::PointerPress)>,
}

impl Default for State {
    fn default() -> State {
        State { tabs: Tabs::default(), capacity: usize::MAX, closed: None, archived: None, menu: None }
    }
}

pub fn on() -> bool {
    crate::look::tabs().on
}

fn in_main(s: &App) -> bool {
    s.windows.current() == MAIN
}

fn title(s: &App, id: &str, label: &str) -> String {
    match crate::session(s, id) {
        Some(x) => tabs::title(x.alias.as_deref(), Some(&x.name), id, label),
        None => s.tabs.tabs.tabs().iter().find(|t| t.id == id).map(|t| tabs::title(t.alias.as_deref(), t.name.as_deref(), id, label)).unwrap_or_else(|| tabs::title(None, None, id, label)),
    }
}

fn needs_you(s: &App, id: &str) -> bool {
    s.ov.needs.iter().any(|n| n.target == Target::Session(id.to_string()))
}

fn holds(s: &App, id: &str) -> bool {
    !s.drafts.get(id).trim().is_empty() || needs_you(s, id) || crate::session(s, id).is_some_and(|x| x.pinned())
}

fn name(s: &mut App, id: &str) {
    let (alias, name) = match crate::session(s, id) {
        Some(x) => (x.alias.clone(), Some(x.name.clone())),
        None => return,
    };
    s.tabs.tabs.name(id, alias.as_deref(), name.as_deref());
}

pub fn shown(s: &mut App, id: &str) {
    if !in_main(s) || !on() {
        return;
    }
    s.tabs.tabs.open(id);
    name(s, id);
    fit(s);
}

fn fit(s: &mut App) {
    let capacity = s.tabs.capacity;
    let mut tabs = std::mem::take(&mut s.tabs.tabs);
    let shut = tabs.fit(capacity, |id| holds(s, id));
    s.tabs.tabs = tabs;
    if let Some(last) = shut.last() {
        let t = title(s, last, "alias");
        s.tabs.closed = Some((last.clone(), t, s.clock + REOPEN_SECS));
    }
}

pub fn set_width(s: &mut App, width: f32) {
    let capacity = tabs::capacity(width);
    if capacity != s.tabs.capacity {
        s.tabs.capacity = capacity;
        fit(s);
    }
}

pub fn activate(s: &mut App, id: &str) {
    s.tabs.tabs.activate(id);
    crate::over::open_session(s, id);
}

fn show_active(s: &mut App) {
    match s.tabs.tabs.active().map(str::to_string) {
        Some(id) => crate::over::open_session(s, &id),
        None => crate::over::go(s, Screen::Now),
    }
}

pub fn close(s: &mut App, id: &str) {
    let showing = s.screen == Screen::Session && s.selected.as_deref() == Some(id);
    s.tabs.tabs.close(id);
    if showing {
        show_active(s);
    }
}

pub fn close_others(s: &mut App, id: &str) {
    s.tabs.tabs.close_others(id);
    if s.screen == Screen::Session && s.selected.as_deref() != Some(id) {
        show_active(s);
    }
}

pub fn step(s: &mut App, offset: isize) {
    if let Some(next) = s.tabs.tabs.neighbour(offset).map(str::to_string) {
        activate(s, &next);
    }
}

pub fn reopen(s: &mut App) {
    if let Some((id, _, _)) = s.tabs.closed.take() {
        crate::over::open_session(s, &id);
    }
}

pub fn to_window(s: &mut App, id: &str) {
    close(s, id);
    window::show(s, id);
}

pub fn active_tab(s: &App) -> Option<&str> {
    (in_main(s) && on() && s.screen == Screen::Session).then(|| s.tabs.tabs.active()).flatten()
}

pub fn reconcile(s: &mut App) {
    let rekeyed = s.rekeyed.clone();
    let mut tabs = std::mem::take(&mut s.tabs.tabs);
    let dropped = tabs.reconcile(|id| feed::composer::current(&rekeyed, id).to_string(), |id| crate::session(s, id).is_some_and(|x| !x.archived));
    s.tabs.tabs = tabs;
    let ids: Vec<String> = s.tabs.tabs.tabs().iter().map(|t| t.id.clone()).collect();
    for id in ids {
        name(s, &id);
    }
    let dropped: Vec<String> = dropped.iter().map(|id| feed::composer::current(&rekeyed, id).to_string()).collect();
    if let Some(id) = dropped.iter().find(|id| crate::session(s, id).is_some_and(|x| x.archived)) {
        let t = title(s, id, "alias");
        s.tabs.archived = Some(Archived { id: id.clone(), title: t, until: s.clock + ARCHIVED_SECS, unarchiving: false });
    }
    if s.screen == Screen::Session && s.selected.as_ref().is_some_and(|sel| dropped.contains(sel)) {
        show_active(s);
    }
}

pub fn unarchive(s: &mut App) {
    let Some(a) = s.tabs.archived.as_mut().filter(|a| !a.unarchiving) else { return };
    a.unarchiving = true;
    let id = a.id.clone();
    crate::ui::session_menu::run(s, &id, feed::menu::Act::Archive(false));
}

fn stuck(s: &App, a: &Archived) -> bool {
    a.unarchiving && !crate::ui::session_menu::changing(s, &a.id) && crate::session(s, &a.id).is_none_or(|x| x.archived)
}

pub fn expire(s: &mut App) -> bool {
    let clock = s.clock;
    let before = (s.tabs.closed.is_some(), s.tabs.archived.clone().map(|a| (a.unarchiving, stuck(s, &a))));
    if let Some(a) = s.tabs.archived.clone().filter(|a| a.unarchiving)
        && crate::session(s, &a.id).is_some_and(|x| !x.archived)
    {
        s.tabs.archived = None;
        crate::over::open_session(s, &a.id);
    }
    s.tabs.closed.take_if(|(_, _, until)| crate::wake::due_clock(*until, clock));
    s.tabs.archived.take_if(|a| !a.unarchiving && crate::wake::due_clock(a.until, clock));
    before != (s.tabs.closed.is_some(), s.tabs.archived.clone().map(|a| (a.unarchiving, stuck(s, &a))))
}

pub fn strip(s: &App, k: &Kit, sizes: bool) -> Option<El> {
    strip_part(s, k, sizes).map(|p| p.el(k))
}

#[derive(Hash)]
struct Shown {
    id: String,
    title: String,
    project: String,
    status: u8,
    marked: bool,
}

pub fn strip_part<'a>(s: &'a App, k: &'a Kit, sizes: bool) -> Option<Part<'a>> {
    if !in_main(s) || !on() {
        return None;
    }
    let closed = s.tabs.closed.as_ref().map(|(_, t, _)| t.clone());
    let archived = s.tabs.archived.as_ref().map(|a| (a.title.clone(), stuck(s, a), a.unarchiving));
    if s.tabs.tabs.tabs().is_empty() {
        if closed.is_none() && archived.is_none() {
            return None;
        }
        return Some(Part::row("tab-strip", crate::over::rows::hash(&(&closed, &archived)), move || notice(s, k).unwrap_or_else(row)));
    }
    let label = crate::look::tabs().label;
    let shown: Vec<Shown> = s
        .tabs
        .tabs
        .tabs()
        .iter()
        .map(|t| Shown {
            id: t.id.clone(),
            title: title(s, &t.id, &label),
            project: crate::session(s, &t.id).map(|x| x.project_name()).unwrap_or_default(),
            status: if needs_you(s, &t.id) {
                2
            } else if s.ov.d.session(&t.id).is_some_and(feed::derive::sessions::busy) {
                1
            } else {
                0
            },
            marked: s.tabs.menu.as_ref().is_some_and(|(m, _)| *m == t.id),
        })
        .collect();
    let active = s.tabs.tabs.active().filter(|_| s.screen == Screen::Session).map(str::to_string);
    let v = crate::over::rows::hash(&(sizes, &shown, &active, &closed, &archived));
    Some(Part::row("tab-strip", v, move || {
        let items = shown.into_iter().map(|t| {
            let status = match t.status {
                2 => Some(TabItemStatus::Attention),
                1 => Some(TabItemStatus::Busy),
                _ => None,
            };
            let id = t.id.clone();
            TabItem::new(t.id, t.title)
                .avatar(crate::over::ui::avatar(&t.project, 16.0))
                .status(status)
                .marked(t.marked)
                .decorate(move |el, _| el.on_secondary_press(move |s: &mut App, at, _: &mut Tx| s.tabs.menu = Some((id.clone(), at))))
        });
        let strip = TabStrip::<App>::new("session-tabs", TabOrientation::Horizontal)
            .tabs(items)
            .active(active.as_deref())
            .label("Sessions")
            .on_select(|s: &mut App, id, _| activate(s, id))
            .on_close(|s: &mut App, id, _| close(s, id))
            .build(k)
            .grow(1.0)
            .min_w(0.0);
        let strip = if sizes { strip.on_layout(|s: &mut App, r: [f32; 4]| set_width(s, r[2])) } else { strip };
        let mut bar = row().w_full().items_center().gap(8.0).child(strip);
        if let Some(n) = notice(s, k) {
            bar = bar.child(n);
        }
        bar
    }))
}

fn notice(s: &App, k: &Kit) -> Option<El> {
    if let Some((_, t, _)) = &s.tabs.closed {
        return Some(
            row()
                .key("tab-reopen")
                .gap(4.0)
                .px(8.0)
                .py(3.0)
                .radius(10.0)
                .fill(k.c.surface_raised)
                .child(text(format!("Closed {t} ·")).font(k.text(META)).color(muted()).truncate())
                .child(text("Reopen".to_string()).font(k.text(META)).color(accent()))
                .help("Agenthail closed the least recently used tab to make room")
                .on_press(|s: &mut App| reopen(s)),
        );
    }
    let a = s.tabs.archived.as_ref()?;
    let (said, action) = match (stuck(s, a), a.unarchiving) {
        (true, _) => (format!("{} is still archived ·", a.title), "Try Again"),
        (false, true) => (format!("Unarchiving {}…", a.title), ""),
        (false, false) => (format!("{} was archived ·", a.title), "Unarchive"),
    };
    let mut n = row().key("tab-archived").gap(4.0).px(8.0).py(3.0).radius(10.0).fill(k.c.surface_raised).child(text(said).font(k.text(META)).color(muted()).truncate());
    if !action.is_empty() {
        n = n.child(text(action.to_string()).font(k.text(META)).color(accent())).on_press(|s: &mut App| {
            if let Some(a) = s.tabs.archived.as_mut() {
                a.unarchiving = false;
            }
            unarchive(s);
        });
    }
    Some(n)
}

pub fn overlays(s: &App, layer: &mut Vec<El>, k: &Kit) {
    let Some((id, at)) = s.tabs.menu.clone() else { return };
    let many = s.tabs.tabs.tabs().len() > 1;
    let (a, b, c) = (id.clone(), id.clone(), id);
    let menu = Menu::new("tab-menu")
        .open(true)
        .at(at.x, at.y)
        .min_width(200.0)
        .on_close(|s: &mut App| s.tabs.menu = None)
        .item(MenuItem::new("Open in New Window").on_select(move |s: &mut App| {
            s.tabs.menu = None;
            to_window(s, &a);
        }))
        .separator()
        .item(MenuItem::new("Close Tab").on_select(move |s: &mut App| {
            s.tabs.menu = None;
            close(s, &b);
        }))
        .item(MenuItem::new("Close Other Tabs").disabled(!many).on_select(move |s: &mut App| {
            s.tabs.menu = None;
            close_others(s, &c);
        }));
    let _ = menu.build(k, layer);
}

pub fn saved(s: &App) -> serde_json::Value {
    s.tabs.tabs.to_json()
}

pub fn load(s: &mut App, v: &serde_json::Value) {
    if let Some(tabs) = Tabs::from_json(v) {
        s.tabs.tabs = tabs;
    }
}

#[cfg(test)]
#[path = "tabs_tests.rs"]
mod tests;
