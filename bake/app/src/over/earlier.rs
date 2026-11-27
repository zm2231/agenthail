use std::collections::HashMap;
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::activity::{Page, earlier};
use feed::catalog::Session;
use rune::{El, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize};

use crate::App;
use crate::over::ui::{self, META, META_MED, base, kit, strong, weak};

#[derive(Default)]
struct Shown {
    open: bool,
    sessions: Vec<Session>,
    next: Option<String>,
    read: bool,
    asked: Option<u64>,
    error: Option<String>,
    removed: Vec<String>,
}

pub struct Earlier {
    pub activity_missing: bool,
    projects: HashMap<String, Shown>,
    seq: u64,
    tx: Sender<(String, u64, Result<Page, String>)>,
    rx: Receiver<(String, u64, Result<Page, String>)>,
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
        let _ = tx.send((key, seq, got));
    });
}

fn show(s: &mut App, key: &str) {
    s.earlier.projects.insert(key.to_string(), Shown { open: true, ..Shown::default() });
    more(s, key);
}

pub fn removed(s: &mut App, ids: &[String]) {
    for shown in s.earlier.projects.values_mut() {
        shown.sessions.retain(|x| !ids.contains(&x.id));
        shown.removed.extend(ids.iter().cloned());
    }
}

pub fn reopened(s: &mut App) {
    let open: Vec<String> = s.earlier.projects.iter().filter(|(_, x)| x.open).map(|(k, _)| k.clone()).collect();
    s.earlier.projects.clear();
    for key in open {
        show(s, &key);
    }
}

fn hide(s: &mut App, key: &str) {
    if let Some(shown) = s.earlier.projects.get_mut(key) {
        shown.open = false;
    }
}

pub fn poll(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok((key, seq, got)) = s.earlier.rx.try_recv() {
        let Some(shown) = s.earlier.projects.get_mut(&key) else { continue };
        if shown.asked != Some(seq) {
            continue;
        }
        shown.asked = None;
        match got {
            Ok(page) => {
                for x in page.sessions.into_iter().filter(|x| !shown.removed.contains(&x.id)) {
                    shown.sessions.retain(|o| o.id != x.id);
                    shown.sessions.push(x);
                }
                shown.next = page.next;
                shown.read = true;
            }
            Err(error) => shown.error = Some(error),
        }
        changed = true;
    }
    changed
}

fn open(s: &mut App, x: &Session) {
    if s.ov.d.session(&x.id).is_some() {
        crate::over::open_session(s, &x.id);
    } else {
        crate::over::search::open_read(s, x.clone());
    }
}

fn session_row(s: &App, i: usize, x: &Session) -> El {
    let k = kit();
    let it = x.clone();
    let place = [feed::projects::surface_label(&x.surface), x.host.name.clone()].into_iter().filter(|p| !p.is_empty()).collect::<Vec<_>>().join(" · ");
    col()
        .key(format!("earlier-{i}"))
        .w_full()
        .gap(2.0)
        .px(8.0)
        .py(7.0)
        .radius(8.0)
        .hover(k.c.surface_raised_hover)
        .on_press(move |s: &mut App| open(s, &it))
        .child(row().w_full().gap(6.0).items_center().child(text(ui::short_title(&s.ov.d.title(x), 34)).font(META_MED).color(strong()).truncate().shrink()).child(ui::spacer()).child(text(s.ov.d.ago(&x.last_active)).font(META).color(weak())))
        .children((!place.is_empty()).then(|| text(place).font(META).color(weak()).truncate()))
}

pub fn section(s: &App, key: &str) -> El {
    let shown = s.earlier.projects.get(key);
    let lead = s.ov.d.project(key).and_then(|p| p.lead.clone());
    let fetched: &[Session] = shown.map(|x| x.sessions.as_slice()).unwrap_or_default();
    let open = shown.is_some_and(|x| x.open);
    let k = key.to_string();
    let toggle = if open {
        Button::new("Hide").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| hide(s, &k)).build(kit())
    } else {
        Button::new("Show earlier").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| {
            show(s, &k);
        }).build(kit())
    }
    .key(format!("earlier-toggle-{key}"));
    let rows = if open { earlier(&s.ov.d.sessions, fetched, key, lead.as_deref(), s.now) } else { Vec::new() };
    let mut c = col().w_full().gap(6.0).child(ui::section_head("Earlier", open.then_some(rows.len()), Some(toggle)));
    if !open {
        return c.child(text("Sessions quiet for more than a day.".to_string()).font(META).color(weak()));
    }
    let loading = shown.is_some_and(|x| x.asked.is_some());
    if rows.is_empty() && !loading && shown.is_some_and(|x| x.read && x.next.is_none()) {
        c = c.child(text("No earlier sessions.".to_string()).font(META).color(weak()));
    }
    c = c.children(rows.iter().enumerate().map(|(i, x)| session_row(s, i, x)));
    if let Some(error) = shown.and_then(|x| x.error.clone()) {
        c = c.child(text(error).font(META).color(ui::needs_color()).wrap());
    }
    if loading {
        c = c.child(text("Loading…".to_string()).font(META).color(weak()));
    } else if shown.is_some_and(|x| x.next.is_some() || x.error.is_some()) {
        let k = key.to_string();
        let label = if shown.is_some_and(|x| x.error.is_some()) { "Try again" } else { "Show more" };
        c = c.child(Button::new(label).variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| more(s, &k)).build(kit()).key(format!("earlier-more-{key}")));
    }
    c
}

pub fn activity_note(s: &App) -> Option<El> {
    s.earlier.activity_missing.then(|| text(feed::activity::ACTIVITY_UNREAD.to_string()).font(META).color(ui::needs_color()).wrap().px(10.0).py(6.0).key("activity-unread"))
}

pub fn folders(s: &App, key: &str) -> Option<El> {
    let record = s.ov.d.record(key)?;
    if record.checkouts.is_empty() {
        return None;
    }
    let here = s.ov.d.hosts.first().map(|h| h.name.clone()).filter(|n| !n.is_empty());
    let mut c = col().w_full().gap(6.0).child(ui::section_head("Folders", Some(record.checkouts.len()), None));
    for (i, checkout) in record.checkouts.iter().enumerate() {
        let host = if checkout.host.local() { here.clone().unwrap_or_else(|| checkout.host.name.clone()) } else { checkout.host.name.clone() };
        c = c.child(
            row()
                .key(format!("folder-{i}"))
                .w_full()
                .gap(10.0)
                .items_center()
                .px(8.0)
                .child(text(host).font(META).color(weak()).truncate().w(96.0))
                .child(text(checkout.root_path.clone()).font(META.mono()).color(base()).truncate().shrink().min_w(0.0).selectable(true)),
        );
    }
    Some(c)
}
