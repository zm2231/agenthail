use std::collections::HashMap;
use std::sync::mpsc::Receiver;

use feed::activity::{Page, earlier};
use feed::catalog::Session;
use feed::wake::Sender;
use feed::watch::Removal;
use rune::{El, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Kit};

use crate::App;
use crate::over::ui::{self, META, META_MED, base, strong, weak};
use crate::wake::channel;

#[derive(Default)]
struct Shown {
    open: bool,
    sessions: Vec<Session>,
    next: Option<String>,
    read: bool,
    asked: Option<u64>,
    error: Option<String>,
    removed: Vec<String>,
    pages: usize,
    reread: bool,
    replace: bool,
}

type Got = (String, u64, bool, Result<Page, String>);

pub struct Earlier {
    pub activity_missing: bool,
    projects: HashMap<String, Shown>,
    seq: u64,
    tx: Sender<Got>,
    rx: Receiver<Got>,
}

impl Default for Earlier {
    fn default() -> Self {
        let (tx, rx) = channel();
        Earlier { activity_missing: false, projects: HashMap::new(), seq: 0, tx, rx }
    }
}

fn more(s: &mut App, key: &str) {
    let Some(client) = s.client.clone() else { return };
    s.earlier.seq += 1;
    let seq = s.earlier.seq;
    let shown = s.earlier.projects.entry(key.to_string()).or_default();
    if shown.asked.is_some() || (shown.read && shown.next.is_none()) {
        return;
    }
    shown.open = true;
    shown.asked = Some(seq);
    shown.error = None;
    let (tx, key, cursor) = (s.earlier.tx.clone(), key.to_string(), shown.next.clone());
    std::thread::spawn(move || {
        let got = client.earlier_page(&key, cursor.as_deref());
        let _ = tx.send((key, seq, false, got));
    });
}

fn reread(s: &mut App, key: &str) {
    let Some(client) = s.client.clone() else { return };
    let Some(shown) = s.earlier.projects.get_mut(key) else { return };
    if shown.asked.is_some() {
        return;
    }
    s.earlier.seq += 1;
    let seq = s.earlier.seq;
    shown.asked = Some(seq);
    shown.error = None;
    shown.reread = false;
    shown.replace = true;
    let (tx, key, pages) = (s.earlier.tx.clone(), key.to_string(), shown.pages.max(1));
    std::thread::spawn(move || {
        let mut all = Page { sessions: Vec::new(), next: None };
        for _ in 0..pages {
            match client.earlier_page(&key, all.next.as_deref()) {
                Ok(page) => {
                    all.sessions.extend(page.sessions);
                    all.next = page.next;
                }
                Err(error) => {
                    let _ = tx.send((key, seq, true, Err(error)));
                    return;
                }
            }
            if all.next.is_none() {
                break;
            }
        }
        let _ = tx.send((key, seq, true, Ok(all)));
    });
}

fn show(s: &mut App, key: &str) {
    s.earlier.projects.insert(key.to_string(), Shown { open: true, ..Shown::default() });
    more(s, key);
}

pub fn removed(s: &mut App, gone: &[Removal]) {
    let mut due: Vec<String> = Vec::new();
    for x in gone {
        let listed = s.ov.d.session(&x.id).cloned();
        for (key, shown) in s.earlier.projects.iter_mut() {
            if !x.unlisted {
                shown.sessions.retain(|o| o.id != x.id);
                shown.removed.push(x.id.clone());
                continue;
            }
            shown.removed.retain(|o| *o != x.id);
            if shown.open && (shown.read || shown.asked.is_some()) && listed.as_ref().is_none_or(|r| r.project_key() == *key) {
                shown.reread = true;
                due.push(key.clone());
            }
            if let Some(row) = listed.as_ref().filter(|r| r.project_key() == *key && !shown.sessions.iter().any(|o| o.id == r.id)) {
                shown.sessions.push(row.clone());
            }
        }
    }
    due.sort();
    due.dedup();
    for key in due {
        reread(s, &key);
    }
}

pub fn reopened(s: &mut App) {
    let open: Vec<String> = s.earlier.projects.iter().filter(|(_, x)| x.open).map(|(k, _)| k.clone()).collect();
    s.earlier.projects.clear();
    for key in open {
        show(s, &key);
    }
}

fn again(s: &mut App, key: &str) {
    if s.earlier.projects.get(key).is_some_and(|x| x.reread || x.replace) {
        reread(s, key);
    } else {
        more(s, key);
    }
}

fn hide(s: &mut App, key: &str) {
    if let Some(shown) = s.earlier.projects.get_mut(key) {
        shown.open = false;
    }
}

pub fn poll(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok((key, seq, whole, got)) = s.earlier.rx.try_recv() {
        let Some(shown) = s.earlier.projects.get_mut(&key) else { continue };
        if shown.asked != Some(seq) {
            continue;
        }
        shown.asked = None;
        match got {
            Ok(page) if whole => {
                shown.sessions = page.sessions.into_iter().filter(|x| !shown.removed.contains(&x.id)).collect();
                shown.pages = shown.pages.max(1);
                shown.replace = false;
                shown.read = true;
                shown.next = page.next;
            }
            Ok(page) => {
                shown.pages += 1;
                for x in page.sessions.into_iter().filter(|x| !shown.removed.contains(&x.id)) {
                    shown.sessions.retain(|o| o.id != x.id);
                    shown.sessions.push(x);
                }
                shown.next = page.next;
                shown.read = true;
            }
            Err(error) => shown.error = Some(error),
        }
        if shown.reread && shown.error.is_none() {
            reread(s, &key);
        }
        changed = true;
    }
    changed
}

fn open(s: &mut App, key: &str, id: &str) {
    if s.ov.d.session(id).is_some() {
        crate::over::open_session(s, id);
    } else if let Some(x) = s.earlier.projects.get(key).and_then(|p| p.sessions.iter().find(|x| x.id == id)).cloned() {
        crate::over::search::open_read(s, x);
    }
}

fn session_row(s: &App, key: &str, x: &Session, k: &Kit) -> El {
    let place = [feed::projects::surface_label(&x.surface), x.host.name.clone()].into_iter().filter(|p| !p.is_empty()).collect::<Vec<_>>().join(" · ");
    let title = ui::short_title(&s.ov.d.title(x), 34);
    let ago = s.ov.d.ago(&x.last_active);
    let shows = (key, &x.id, &place, &title, &ago);
    crate::over::rows::memo(format!("earlier-row-{}", x.id), shows, || {
        let (key, id) = (key.to_string(), x.id.clone());
        col()
            .key(format!("earlier-row-{}", x.id))
            .w_full()
            .gap(2.0)
            .px(8.0)
            .py(7.0)
            .radius(8.0)
            .hover(k.c.surface_raised_hover)
            .on_press(move |s: &mut App| open(s, &key, &id))
            .child(row().w_full().gap(6.0).items_center().child(text(title.clone()).font(k.text(META_MED)).color(strong(k)).truncate().shrink()).child(ui::spacer()).child(text(ago.clone()).font(k.text(META)).color(weak(k))))
            .children((!place.is_empty()).then(|| text(place.clone()).font(k.text(META)).color(weak(k)).truncate()))
    })
}

pub fn section(s: &App, key: &str, k: &Kit) -> El {
    let shown = s.earlier.projects.get(key);
    let lead = s.ov.d.project(key).and_then(|p| p.lead.clone());
    let fetched: &[Session] = shown.map(|x| x.sessions.as_slice()).unwrap_or_default();
    let open = shown.is_some_and(|x| x.open);
    let id = key.to_string();
    let toggle = if open {
        Button::new("Hide").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| hide(s, &id)).build(k)
    } else {
        Button::new("Show earlier").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| {
            show(s, &id);
        }).build(k)
    }
    .key(format!("earlier-toggle-{key}"));
    let rows = if open { earlier(&s.ov.d.sessions, fetched, key, lead.as_deref(), s.now) } else { Vec::new() };
    let mut c = col().w_full().gap(6.0).child(ui::section_head("Earlier", open.then_some(rows.len()), Some(toggle), k));
    if !open {
        return c.child(text("Sessions quiet for more than a day.".to_string()).font(k.text(META)).color(weak(k)));
    }
    let loading = shown.is_some_and(|x| x.asked.is_some());
    if rows.is_empty() && !loading && shown.is_some_and(|x| x.read && x.next.is_none()) {
        c = c.child(text("No earlier sessions.".to_string()).font(k.text(META)).color(weak(k)));
    }
    c = c.children(rows.iter().map(|x| session_row(s, key, x, k)));
    if let Some(error) = shown.and_then(|x| x.error.clone()) {
        c = c.child(text(error).font(k.text(META)).color(ui::needs_color(k)).wrap());
    }
    if loading {
        c = c.child(text("Loading…".to_string()).font(k.text(META)).color(weak(k)));
    } else if shown.is_some_and(|x| x.next.is_some() || x.error.is_some()) {
        let id = key.to_string();
        let label = if shown.is_some_and(|x| x.error.is_some()) { "Try again" } else { "Show more" };
        c = c.child(Button::new(label).variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| again(s, &id)).build(k).key(format!("earlier-more-{key}")));
    }
    c
}

pub fn activity_note(s: &App, k: &Kit) -> Option<El> {
    s.earlier.activity_missing.then(|| text(feed::activity::ACTIVITY_UNREAD.to_string()).font(k.text(META)).color(ui::needs_color(k)).wrap().px(10.0).py(6.0).key("activity-unread"))
}

pub fn folders(s: &App, key: &str, k: &Kit) -> Option<El> {
    let record = s.ov.d.record(key)?;
    if record.checkouts.is_empty() {
        return None;
    }
    let here = s.ov.d.hosts.first().map(|h| h.name.clone()).filter(|n| !n.is_empty());
    let mut c = col().w_full().gap(6.0).child(ui::section_head("Folders", Some(record.checkouts.len()), None, k));
    for (i, checkout) in record.checkouts.iter().enumerate() {
        let host = if checkout.host.local() { here.clone().unwrap_or_else(|| checkout.host.name.clone()) } else { checkout.host.name.clone() };
        c = c.child(
            row()
                .key(format!("folder-{i}"))
                .w_full()
                .gap(10.0)
                .items_center()
                .px(8.0)
                .child(text(host).font(k.text(META)).color(weak(k)).truncate().w(96.0))
                .child(text(checkout.root_path.clone()).font(k.text(META.mono())).color(base(k)).truncate().shrink().min_w(0.0).selectable(true)),
        );
    }
    Some(c)
}

#[cfg(test)]
pub(crate) fn asking(s: &mut App, key: &str) -> u64 {
    s.earlier.seq += 1;
    s.earlier.projects.insert(key.to_string(), Shown { open: true, asked: Some(s.earlier.seq), ..Shown::default() });
    s.earlier.seq
}

#[cfg(test)]
pub(crate) fn answer(s: &mut App, key: &str, seq: u64, sessions: Vec<Session>) {
    let _ = s.earlier.tx.send((key.to_string(), seq, false, Ok(Page { sessions, next: None })));
    poll(s);
}

#[cfg(test)]
pub(crate) fn fetched(s: &App, key: &str) -> Vec<String> {
    s.earlier.projects.get(key).map(|x| x.sessions.iter().map(|o| o.id.clone()).collect()).unwrap_or_default()
}

#[cfg(test)]
pub(crate) fn rereads(s: &App, key: &str) -> bool {
    s.earlier.projects.get(key).is_some_and(|x| x.reread || x.replace)
}

#[cfg(test)]
pub(crate) fn start_reread(s: &mut App, key: &str) -> u64 {
    s.earlier.seq += 1;
    if let Some(shown) = s.earlier.projects.get_mut(key) {
        shown.asked = Some(s.earlier.seq);
        shown.reread = false;
        shown.replace = true;
    }
    s.earlier.seq
}

#[cfg(test)]
pub(crate) fn answer_reread(s: &mut App, key: &str, seq: u64, sessions: Option<Vec<Session>>) {
    let _ = s.earlier.tx.send((key.to_string(), seq, true, sessions.ok_or_else(|| "offline".to_string()).map(|sessions| Page { sessions, next: None })));
    poll(s);
}

#[cfg(test)]
pub(crate) fn asks_again(s: &App, key: &str) -> bool {
    s.earlier.projects.get(key).is_some_and(|x| x.reread)
}

#[cfg(test)]
pub(crate) fn status(s: &App, key: &str, id: &str) -> Option<String> {
    s.earlier.projects.get(key)?.sessions.iter().find(|x| x.id == id).map(|x| x.status.clone())
}

#[cfg(test)]
pub(crate) fn error(s: &App, key: &str) -> Option<String> {
    s.earlier.projects.get(key)?.error.clone()
}

#[cfg(test)]
pub(crate) fn fail(s: &mut App, key: &str, seq: u64) {
    let _ = s.earlier.tx.send((key.to_string(), seq, false, Err("offline".into())));
    poll(s);
}
