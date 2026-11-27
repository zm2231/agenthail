use feed::derive::needs::Target;
use feed::tabs::{self, Tabs};
use rune::{El, Tx, row, text};
use rune_kit::shell::{TabItem, TabItemStatus, TabOrientation, TabStrip};
use rune_kit::{Kit, Menu, MenuItem};

use super::window::{self, MAIN};
use crate::theme::{META, accent, muted};
use crate::{App, Screen};

pub const REOPEN_SECS: f64 = 6.0;
pub const ARCHIVED_SECS: f64 = 10.0;

pub struct State {
    pub tabs: Tabs,
    capacity: usize,
    pub closed: Option<(String, String, f64)>,
    pub archived: Option<(String, String, f64)>,
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
    let archived = |id: &str| crate::session(s, id).is_some_and(|x| x.archived);
    let gone: Vec<String> = s.tabs.tabs.tabs().iter().map(|t| t.id.clone()).filter(|id| archived(&feed::composer::current(&rekeyed, id))).collect();
    let mut tabs = std::mem::take(&mut s.tabs.tabs);
    tabs.reconcile(|id| feed::composer::current(&rekeyed, id).to_string(), |id| !gone.iter().any(|g| feed::composer::current(&rekeyed, g) == id));
    s.tabs.tabs = tabs;
    let ids: Vec<String> = s.tabs.tabs.tabs().iter().map(|t| t.id.clone()).collect();
    for id in ids {
        name(s, &id);
    }
    if let Some(id) = gone.first() {
        let t = title(s, id, "alias");
        s.tabs.archived = Some((id.clone(), t, s.clock + ARCHIVED_SECS));
        if s.screen == Screen::Session && s.selected.as_deref() == Some(id.as_str()) {
            show_active(s);
        }
    }
}

pub fn unarchive(s: &mut App) {
    let Some((id, _, _)) = s.tabs.archived.take() else { return };
    crate::ui::session_menu::run(s, &id, feed::menu::Act::Archive(false));
    crate::over::open_session(s, &id);
}

pub fn expire(s: &mut App) -> bool {
    let clock = s.clock;
    let before = (s.tabs.closed.is_some(), s.tabs.archived.is_some());
    s.tabs.closed.take_if(|(_, _, until)| clock >= *until);
    s.tabs.archived.take_if(|(_, _, until)| clock >= *until);
    before != (s.tabs.closed.is_some(), s.tabs.archived.is_some())
}

pub fn strip(s: &App, k: &Kit) -> Option<El> {
    if !in_main(s) || !on() || s.tabs.tabs.tabs().is_empty() {
        return None;
    }
    let label = crate::look::tabs().label;
    let items = s.tabs.tabs.tabs().iter().map(|t| {
        let x = crate::session(s, &t.id);
        let status = if needs_you(s, &t.id) {
            Some(TabItemStatus::Attention)
        } else if s.ov.d.session(&t.id).is_some_and(feed::derive::sessions::busy) {
            Some(TabItemStatus::Busy)
        } else {
            None
        };
        let project = x.map(|x| x.project_name()).unwrap_or_default();
        let id = t.id.clone();
        TabItem::new(t.id.clone(), title(s, &t.id, &label))
            .avatar(crate::over::ui::avatar(&project, 16.0))
            .status(status)
            .marked(s.tabs.menu.as_ref().is_some_and(|(m, _)| *m == t.id))
            .decorate(move |el, _| el.on_secondary_press(move |s: &mut App, at, _: &mut Tx| s.tabs.menu = Some((id.clone(), at))))
    });
    let strip = TabStrip::<App>::new("session-tabs", TabOrientation::Horizontal)
        .tabs(items)
        .active(s.tabs.tabs.active().filter(|_| s.screen == Screen::Session))
        .label("Sessions")
        .on_select(|s: &mut App, id, _| activate(s, id))
        .on_close(|s: &mut App, id, _| close(s, id))
        .build(k)
        .grow(1.0)
        .min_w(0.0)
        .on_layout(|s: &mut App, r: [f32; 4]| set_width(s, r[2]));
    let mut bar = row().w_full().items_center().gap(8.0).child(strip);
    if let Some(n) = notice(s, k) {
        bar = bar.child(n);
    }
    Some(bar)
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
                .child(text(format!("Closed {t} ·")).font(META).color(muted()).truncate())
                .child(text("Reopen".to_string()).font(META).color(accent()))
                .help("Agenthail closed the least recently used tab to make room")
                .on_press(|s: &mut App| reopen(s)),
        );
    }
    let (_, t, _) = s.tabs.archived.as_ref()?;
    Some(
        row()
            .key("tab-archived")
            .gap(4.0)
            .px(8.0)
            .py(3.0)
            .radius(10.0)
            .fill(k.c.surface_raised)
            .child(text(format!("{t} was archived ·")).font(META).color(muted()).truncate())
            .child(text("Unarchive".to_string()).font(META).color(accent()))
            .on_press(|s: &mut App| unarchive(s)),
    )
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
