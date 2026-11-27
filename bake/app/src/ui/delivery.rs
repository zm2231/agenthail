use std::cell::RefCell;
use std::collections::HashSet;
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::catalog::Session;
use feed::queue::{Op, Queued, undelivered};
use feed::search::Entry;
use rune::{El, col, el, icon, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Kit};

use super::rail::rail_section;
use crate::icons;
use crate::theme::{self, BODY, META, META_BOLD, accent, attention, failed, faint, muted};
use crate::{App, Screen};

pub enum Asked {
    TakeBack(Queued),
    Answer { session: String, ask: String },
}

thread_local! {
    static ASKED: RefCell<Vec<Asked>> = const { RefCell::new(Vec::new()) };
}

pub fn ask(what: Asked) {
    ASKED.with_borrow_mut(|list| list.push(what));
}

pub struct Delivery {
    id: String,
    sig: String,
    seq: u64,
    asked: Option<u64>,
    again: bool,
    history: Option<Result<Vec<Entry>, String>>,
    expanded: HashSet<i64>,
    tx: Sender<(String, u64, Result<Vec<Entry>, String>)>,
    rx: Receiver<(String, u64, Result<Vec<Entry>, String>)>,
}

impl Default for Delivery {
    fn default() -> Self {
        let (tx, rx) = channel();
        Delivery { id: String::new(), sig: String::new(), seq: 0, asked: None, again: false, history: None, expanded: HashSet::new(), tx, rx }
    }
}

fn shown(s: &App) -> Option<&Session> {
    let id = s.selected.as_deref().filter(|_| s.screen == Screen::Session)?;
    crate::session(s, id)
}

fn signature(s: &App, x: &Session) -> String {
    let mut parts: Vec<String> = s.queue_rows.iter().filter(|r| r.session_id == x.id).map(|r| format!("{}:{}", r.id, r.status)).collect();
    parts.push(format!("{}|{}|{}", x.queue_count, x.status, x.display.kind));
    parts.join(",")
}

pub fn stale(s: &mut App) {
    s.delivery.again = true;
}

fn read(s: &mut App, x: &Session) {
    super::queue::invalidate(s, &x.id);
    super::queue::fetch(s, &x.id);
    let Some(client) = s.client.clone() else { return };
    s.delivery.again = false;
    if s.delivery.asked.is_some() {
        s.delivery.again = true;
        return;
    }
    s.delivery.seq += 1;
    let seq = s.delivery.seq;
    s.delivery.asked = Some(seq);
    let (id, tx) = (x.id.clone(), s.delivery.tx.clone());
    std::thread::spawn(move || {
        let history = client.session_history(&id);
        let _ = tx.send((id, seq, history));
    });
}

pub fn answer(s: &mut App, session: &str, ask: &str) {
    let decision = s.ov.inbox.inbox.as_ref().and_then(|i| i.decisions.iter().find(|d| d.requests.iter().any(|r| r.id == ask))).map(|d| d.id());
    match decision {
        Some(id) => {
            crate::over::go(s, Screen::Now);
            s.ov.inbox.open = Some(id);
        }
        None => crate::over::open_session(s, session),
    }
}

fn waits_on_decision(s: &App, ask: &str) -> bool {
    !ask.is_empty() && s.ov.inbox.inbox.as_ref().is_some_and(|i| i.decisions.iter().any(|d| d.requests.iter().any(|r| r.id == ask)))
}

fn take_asked(s: &mut App) -> bool {
    let list = ASKED.with_borrow_mut(std::mem::take);
    for what in &list {
        crate::menubar::front(s);
        match what {
            Asked::TakeBack(row) => {
                let id = feed::composer::current(&s.rekeyed, &row.session_id).to_string();
                crate::over::open_session(s, &id);
                super::queue::run(s, &id, row.clone(), Op::TakeBack);
            }
            Asked::Answer { session, ask } => {
                let id = feed::composer::current(&s.rekeyed, session).to_string();
                answer(s, &id, ask);
            }
        }
    }
    !list.is_empty()
}

pub fn poll(s: &mut App) -> bool {
    let mut changed = take_asked(s);
    while let Ok((id, seq, history)) = s.delivery.rx.try_recv() {
        if s.delivery.asked == Some(seq) {
            s.delivery.asked = None;
        }
        if id == s.delivery.id {
            s.delivery.history = Some(history);
            changed = true;
        }
    }
    let Some(x) = shown(s).cloned() else { return changed };
    if x.id != s.delivery.id {
        let old = std::mem::take(&mut s.delivery);
        s.delivery = Delivery { id: x.id.clone(), seq: old.seq, asked: old.asked, tx: old.tx, rx: old.rx, ..Delivery::default() };
        changed = true;
    }
    let sig = signature(s, &x);
    if sig != s.delivery.sig || (s.delivery.again && s.delivery.asked.is_none()) {
        s.delivery.sig = sig;
        read(s, &x);
    }
    changed
}

fn small(label: &str, key: String, disabled: bool, help: &str, on: impl Fn(&mut App) + 'static, k: &Kit) -> El {
    Button::new(label).variant(ButtonVariant::Ghost).control_size(ControlSize::Small).disabled(disabled).help(help.to_string()).on_press(on).build(k).key(key)
}

fn queued_row(s: &App, x: &Session, item: &Queued, k: &Kit) -> El {
    let working = s.queue_working.contains(&(item.host.clone(), item.id));
    let problem = item.stuck();
    let mut actions = row().gap(4.0).items_center();
    if item.can_take_back() && !x.read_only {
        let (id, it) = (x.id.clone(), item.clone());
        actions = actions.child(small("Take back", format!("delivery-take-{}-{}", item.host, item.id), working, "Cancel this queued message and put its text back in the composer", move |s: &mut App| super::queue::run(s, &id, it.clone(), Op::TakeBack), k));
    }
    if item.can_cancel() {
        let (id, it) = (x.id.clone(), item.clone());
        actions = actions.child(small("Cancel", format!("delivery-cancel-{}-{}", item.host, item.id), working, "Cancel this queued message", move |s: &mut App| super::queue::run(s, &id, it.clone(), Op::Remove), k));
    }
    if waits_on_decision(s, &item.ask_id) {
        let (id, ask) = (x.id.clone(), item.ask_id.clone());
        actions = actions.child(small("Answer", format!("delivery-answer-{}-{}", item.host, item.id), false, "Open the decision this message is waiting on", move |s: &mut App| answer(s, &id, &ask), k));
    }
    let tone = if problem { attention() } else { muted() };
    let title = if item.target.is_empty() { x.name.clone() } else { item.target.clone() };
    col()
        .key(format!("delivery-q-{}-{}", item.host, item.id))
        .w_full()
        .gap(3.0)
        .px(10.0)
        .py(8.0)
        .radius(10.0)
        .fill(if problem { attention().alpha(0.08) } else { theme::ink(0.03) })
        .border(1.0, if problem { attention().alpha(0.35) } else { theme::ink(0.07) })
        .child(row().w_full().gap(8.0).items_center().child(text(title).font(META_BOLD).color(theme::text()).truncate().grow(1.0).min_w(0.0)).child(text(item.status_label()).font(META).color(tone)))
        .child(text(item.headline()).font(META).color(muted()).truncate())
        .child(text(if working { "Working…".to_string() } else { item.summary(s.now) }).font(META).color(tone).wrap().max_lines(3))
        .child(actions)
}

fn history_row(s: &App, e: &Entry) -> El {
    let failed_row = e.is_error();
    let open = s.delivery.expanded.contains(&e.id);
    let id = e.id;
    let toggle = row()
        .key(format!("delivery-more-{id}"))
        .gap(4.0)
        .items_center()
        .on_press(move |s: &mut App| {
            if !s.delivery.expanded.remove(&id) {
                s.delivery.expanded.insert(id);
            }
        })
        .child(icon(if open { &icons::CHEVRON_DOWN } else { &icons::CHEVRON_RIGHT }, 11.0).color(accent()))
        .child(text(if open { "Hide details" } else { "View details" }.to_string()).font(META).color(accent()));
    let mut c = col()
        .key(format!("delivery-h-{id}"))
        .w_full()
        .gap(3.0)
        .py(6.0)
        .child(
            row()
                .w_full()
                .gap(8.0)
                .items_center()
                .child(el().size(6.0, 6.0).radius(3.0).fill(if failed_row { failed() } else { faint() }))
                .child(text(e.heading()).font(META_BOLD).color(theme::text()).truncate().grow(1.0).min_w(0.0))
                .child(text(feed::catalog::ago(&e.created_at, s.now)).font(META).color(faint())),
        )
        .child(text(e.route()).font(META).color(muted()).truncate())
        .child(toggle);
    if open {
        c = c.child(text(e.content().to_string()).font(BODY).color(if failed_row { failed() } else { theme::text() }).wrap_anywhere().max_lines(20));
    }
    c
}

fn caption(label: &str) -> El {
    text(label.to_uppercase()).font(crate::theme::LABEL).color(faint()).pt(4.0)
}

pub(crate) fn section(s: &App, x: &Session, k: &Kit) -> Option<El> {
    if s.delivery.id != x.id {
        return None;
    }
    let mut body = col().w_full().gap(6.0);
    let queued = s.queue.get(&x.id).map(|rows| undelivered(rows, &s.rekeyed, &x.id)).unwrap_or_default();
    let history: &[Entry] = s.delivery.history.as_ref().and_then(|r| r.as_ref().ok()).map(Vec::as_slice).unwrap_or_default();
    if !queued.is_empty() {
        body = body.child(caption("Undelivered")).children(queued.iter().map(|q| queued_row(s, x, q, k)));
    }
    let queue_failed = s.queue_note.get(&x.id).filter(|n| n.starts_with(feed::queue::READ_FAILED));
    for problem in queue_failed.into_iter().chain(s.delivery.history.as_ref().and_then(|r| r.as_ref().err())) {
        body = body.child(text(problem.clone()).font(META).color(attention()).wrap());
    }
    match &s.delivery.history {
        None => body = body.child(text("Loading…".to_string()).font(META).color(faint())),
        Some(Ok(_)) if queued.is_empty() && history.is_empty() && queue_failed.is_none() => body = body.child(text("No deliveries for this session yet.".to_string()).font(META).color(muted())),
        _ if !history.is_empty() => body = body.child(caption("Recent")).children(history.iter().map(|e| history_row(s, e))),
        _ => {}
    }
    Some(rail_section("Delivery", &icons::INBOX, body))
}
