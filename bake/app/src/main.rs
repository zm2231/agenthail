mod icons;
mod over;
mod theme;
mod transcript;
mod ui;

use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Duration;

use feed::catalog::{Client, OpenRequest, Session, WorkItem};
use feed::composer::{Busy, Delivered, Drafts, Unconfirmed, Unsent};
use feed::derive::Recorded;
use feed::derive::live::{Fetched, Ledger, Overview};
use feed::derive::tails::{self, Tail};
use feed::source::{Live, Message};
use rune::{AppSpec, Chrome, KeyEvent, TextEdit, Tx, WindowOptions};
use time::OffsetDateTime;

use crate::over::Over;
use crate::transcript::{Pane, Replay};

pub enum Bg {
    Snapshot(String, Vec<Session>),
    Item(String, WorkItem),
    Overview(Box<Fetched>),
    Ledger(String, std::time::Instant, Result<Ledger, String>),
    Tail(String, String, Result<Tail, String>),
    OverviewError(String),
    SendSettings(String, HashMap<String, String>, Vec<feed::catalog::QueueRow>),
    Sent(String, Unsent, bool, Result<Delivered, feed::catalog::RespondError>),
    Stopped(String, Result<feed::composer::Stop, feed::catalog::RespondError>),
    Requests(String, Vec<OpenRequest>, std::time::Instant),
    Answered(String, String, Result<(), feed::catalog::RespondError>),
    NewSession(ui::new_session::Loaded),
    Error(String),
}

#[derive(Clone, Copy, PartialEq)]
pub enum Confirm {
    PutBack,
    Discard,
    SendNew,
}

#[derive(Clone, Copy, PartialEq)]
pub enum Tab {
    Conversation,
    Changes,
}

#[derive(Clone, PartialEq)]
pub enum Screen {
    Now,
    Session,
    Project(String),
    Work(String),
    Sessions,
}

pub struct Fixtures {
    dir: String,
    streams: HashMap<String, String>,
    speed: f64,
}

pub struct App {
    pub sessions: Vec<Session>,
    pub now: OffsetDateTime,
    pub selected: Option<String>,
    pub screen: Screen,
    pub panes: HashMap<String, Pane>,
    lru: Vec<String>,
    pub open: HashSet<String>,
    pub counter: u64,
    pub by_project: bool,
    pub composer: TextEdit,
    pub composer_for: Option<String>,
    pub drafts: Drafts,
    pub unsent: Unconfirmed,
    pub in_flight: HashMap<String, Vec<Unsent>>,
    confirming: HashMap<String, Confirm>,
    pub queued: HashMap<String, Vec<(u64, Delivered)>>,
    snapshots: u64,
    queue_rows: Vec<feed::catalog::QueueRow>,
    pub stopping: HashSet<String>,
    pub busy_default: Option<Busy>,
    pub busy_pick: HashMap<String, Busy>,
    pub rekeyed: HashMap<String, String>,
    pub items: HashMap<String, WorkItem>,
    pub requests: HashMap<String, Vec<OpenRequest>>,
    pub answering: Option<String>,
    pub settled: HashMap<String, Option<std::time::Instant>>,
    pub answer_status: HashMap<String, String>,
    pub status_line: HashMap<String, String>,
    pub picks: HashMap<String, Vec<String>>,
    pub typed: HashMap<String, TextEdit>,
    pub requests_at: HashMap<String, std::time::Instant>,
    pub tab: Tab,
    pub work_pick: HashMap<String, String>,
    pub diff: rune_kit::diff::DiffState,
    pub diff_key: (String, usize),
    pub ov: Over,
    pub menus: ui::session_menu::Menus,
    pub limits: ui::limit::Limits,
    pub accounts: ui::accounts::Accounts,
    pub prs: ui::pr_link::Prs,
    pub new_session: Option<ui::new_session::Sheet>,
    revealed: Option<String>,
    client: Option<Client>,
    fixtures: Option<Fixtures>,
    inbox: Receiver<Bg>,
    outbox: Sender<Bg>,
    pub status: String,
    clock: f64,
    panes_moved: bool,
    built_at: f64,
}

fn args() -> Vec<String> {
    rune::extra_args().to_vec()
}

fn arg(name: &str) -> Option<String> {
    let args = args();
    args.iter().position(|a| a == name).and_then(|i| args.get(i + 1)).cloned()
}

fn load_recorded(path: &str) -> Result<Recorded, String> {
    let text = std::fs::read_to_string(path).map_err(|e| format!("{path}: {e}"))?;
    serde_json::from_str(&text).map_err(|e| format!("{path}: {e}"))
}

fn watch_overview(client: Client, tx: Sender<Bg>) {
    std::thread::spawn(move || {
        loop {
            let message = match feed::derive::live::fetch(&client) {
                Ok(fetched) => Bg::Overview(Box::new(fetched)),
                Err(error) => Bg::OverviewError(error.to_string()),
            };
            if tx.send(message).is_err() {
                return;
            }
            std::thread::sleep(Duration::from_secs(20));
        }
    });
}

fn init() -> App {
    let (outbox, inbox) = channel();
    let fixture = arg("--fixture");
    let fixtures = arg("--fixtures").or_else(|| std::env::var("AGENTHAIL_FIXTURES").ok());
    let recording = fixtures.as_ref().map(|dir| format!("{dir}/snapshot.json")).or_else(|| fixture.clone());
    let (live, world_error) = match recording.as_deref().map(load_recorded) {
        Some(Ok(raw)) => (Overview::recorded(raw), None),
        Some(Err(error)) => (Overview::default(), Some(error)),
        None => (Overview::default(), None),
    };
    let mut app = App {
        sessions: Vec::new(),
        now: OffsetDateTime::now_utc(),
        selected: None,
        screen: Screen::Now,
        panes: HashMap::new(),
        lru: Vec::new(),
        open: HashSet::new(),
        counter: 0,
        by_project: false,
        composer: TextEdit::new(""),
        composer_for: None,
        drafts: Drafts::default(),
        unsent: Unconfirmed::default(),
        in_flight: HashMap::new(),
        confirming: HashMap::new(),
        queued: HashMap::new(),
        snapshots: 0,
        queue_rows: Vec::new(),
        stopping: HashSet::new(),
        busy_default: None,
        busy_pick: HashMap::new(),
        rekeyed: HashMap::new(),
        items: HashMap::new(),
        requests: HashMap::new(),
        answering: None,
        settled: HashMap::new(),
        answer_status: HashMap::new(),
        status_line: HashMap::new(),
        picks: HashMap::new(),
        typed: HashMap::new(),
        requests_at: HashMap::new(),
        tab: Tab::Conversation,
        work_pick: HashMap::new(),
        diff: rune_kit::diff::DiffState::new(Vec::new()),
        diff_key: (String::new(), usize::MAX),
        ov: Over::new(live, recording.is_none()),
        menus: ui::session_menu::Menus::new(),
        limits: ui::limit::Limits::new(),
        accounts: ui::accounts::Accounts::new(),
        prs: ui::pr_link::Prs::new(),
        new_session: None,
        revealed: None,
        client: None,
        fixtures: None,
        inbox,
        outbox,
        status: "connecting".into(),
        clock: 0.0,
        panes_moved: false,
        built_at: 0.0,
    };
    if recording.is_some() {
        app.sessions = app.ov.d.sessions.clone();
        app.now = app.ov.d.now;
    }
    if let Some(dir) = fixtures {
        let streams = std::fs::read_to_string(format!("{dir}/streams.json")).ok().and_then(|t| serde_json::from_str(&t).ok()).unwrap_or_default();
        let speed = arg("--speed").and_then(|s| s.parse().ok()).unwrap_or(10.0);
        app.fixtures = Some(Fixtures { dir, streams, speed });
        app.status = "fixtures".into();
    } else if let Some(path) = &fixture {
        let dir = std::path::Path::new(path).parent().map(|p| p.display().to_string()).unwrap_or_default();
        app.fixtures = Some(Fixtures { dir, streams: HashMap::new(), speed: 1.0 });
        app.status = "fixture".into();
    } else {
        match Client::from_env() {
            Ok(client) => {
                let tx = app.outbox.clone();
                let c = client.clone();
                std::thread::spawn(move || {
                    loop {
                        let message = match c.snapshot() {
                            Ok(snap) => {
                                if tx.send(Bg::SendSettings(snap.busy_delivery, snap.rekeyed, snap.queue)).is_err() {
                                    return;
                                }
                                Bg::Snapshot(snap.updated_at, snap.sessions)
                            }
                            Err(error) => Bg::Error(error.to_string()),
                        };
                        if tx.send(message).is_err() {
                            return;
                        }
                        std::thread::sleep(Duration::from_secs(2));
                    }
                });
                watch_overview(client.clone(), app.outbox.clone());
                app.client = Some(client);
            }
            Err(error) => app.status = error.to_string(),
        }
    }
    if app.ov.persist {
        over::layout::load(&mut app);
    }
    app.ov.notice = world_error;
    let looked = (app.ov.looked_now.clone(), app.ov.looked.clone());
    if let Some(project) = arg("--project") {
        over::go(&mut app, Screen::Project(project));
    }
    if let Some(id) = arg("--select") {
        over::open_session(&mut app, &id);
    }
    (app.ov.looked_now, app.ov.looked) = looked;
    app.ov.arrange();
    if arg("--tab").as_deref() == Some("changes") {
        app.tab = Tab::Changes;
    }
    app
}

const PANE_REBUILD_SECS: f64 = 1.0;
const SETTLE_GRACE: std::time::Duration = std::time::Duration::from_secs(6);

impl App {
    pub fn is_settled(&self, request: &str) -> bool {
        self.settled.contains_key(request)
    }

    fn reopen_undelivered(&mut self, requests: &[OpenRequest], fetched_at: std::time::Instant) {
        self.settled.retain(|id, done_at| !(requests.iter().any(|r| &r.id == id) && done_at.is_some_and(|at| fetched_at >= at + SETTLE_GRACE)));
    }
}

pub fn session<'a>(s: &'a App, id: &str) -> Option<&'a Session> {
    s.sessions.iter().find(|x| x.id == id)
}

pub fn select(s: &mut App, id: &str) {
    let id = &feed::composer::current(&s.rekeyed, id).to_string();
    s.selected = Some(id.to_string());
    s.screen = Screen::Session;
    s.tab = Tab::Conversation;
    switch_composer(s);
    s.composer.focused = true;
    load_pane(s, id);
    if let Some(work) = session(s, id).and_then(|x| x.primary_work(s.work_pick.get(id).map(String::as_str)).cloned()) {
        fetch_item(s, &work.project_key, &work.item_id);
    }
    fetch_requests(s, id);
    ui::session_menu::load(s, id);
}

pub fn load_pane(s: &mut App, id: &str) {
    let id = &feed::composer::current(&s.rekeyed, id).to_string();
    s.lru.retain(|x| x != id);
    s.lru.push(id.to_string());
    if !s.panes.contains_key(id) {
        let mut pane = Pane::new();
        if let Some(f) = &s.fixtures {
            match f.streams.get(id) {
                Some(path) => {
                    pane.replay = Some(Replay::load(path, f.speed));
                    pane.status = "replay".into();
                }
                None => pane.status = "no recording for this session".into(),
            }
        } else if let Ok(live) = Live::from_env(id.to_string()) {
            let (tx, rx) = channel();
            std::thread::spawn(move || live.run(tx));
            pane.live = Some(rx);
        }
        s.panes.insert(id.to_string(), pane);
    }
    while s.lru.len() > 8 {
        let old = s.lru.remove(0);
        s.panes.remove(&old);
    }
}

fn rebuild(s: &mut App) {
    let panes: Vec<(&str, &[feed::Item])> = s.panes.iter().map(|(id, p)| (id.as_str(), p.transcript.items())).collect();
    s.ov.rebuild(&panes);
    s.panes_moved = false;
    s.built_at = s.clock;
}

fn fetch_tails(s: &mut App) {
    let Some(client) = s.client.clone() else { return };
    let open: HashSet<String> = s.panes.keys().cloned().collect();
    for (id, last_active) in s.ov.live.due_tails(&open) {
        let (tx, client) = (s.outbox.clone(), client.clone());
        std::thread::spawn(move || {
            let read = std::panic::AssertUnwindSafe(|| tails::fetch(&client, &id, tails::READ_BUDGET));
            let result = std::panic::catch_unwind(read).unwrap_or_else(|_| Err("the read stopped".into()));
            let _ = tx.send(Bg::Tail(id, last_active, result));
        });
    }
}

pub fn fetch_ledger(s: &mut App, project: &str) {
    let Some(client) = s.client.clone() else { return };
    if !s.ov.live.wants(project) {
        return;
    }
    let (tx, project, sessions, started) = (s.outbox.clone(), project.to_string(), s.sessions.clone(), std::time::Instant::now());
    std::thread::spawn(move || {
        let result = feed::derive::live::ledger(&client, &project, &sessions);
        let _ = tx.send(Bg::Ledger(project, started, result));
    });
}

fn fetch_requests(s: &mut App, id: &str) {
    fetch_requests_at(s, id, &[0]);
}

fn fetch_requests_at(s: &mut App, id: &str, delays_ms: &'static [u64]) {
    if let Some(client) = s.client.clone() {
        let (tx, id) = (s.outbox.clone(), id.to_string());
        let start = std::time::Instant::now();
        std::thread::spawn(move || {
            for at in delays_ms {
                let due = start + std::time::Duration::from_millis(*at);
                std::thread::sleep(due.saturating_duration_since(std::time::Instant::now()));
                let fetched_at = std::time::Instant::now();
                if let Ok(requests) = client.requests(&id) {
                    let _ = tx.send(Bg::Requests(id.clone(), requests, fetched_at));
                }
            }
        });
    }
}

pub fn toggle_pick(s: &mut App, request: &str, siblings: &[String], option: &str, multi: bool) {
    let picks = s.picks.entry(request.to_string()).or_default();
    if picks.iter().any(|p| p == option) {
        picks.retain(|p| p != option);
        return;
    }
    if !multi {
        picks.retain(|p| !siblings.contains(p));
    }
    picks.push(option.to_string());
}

pub fn open_link(url: &str) {
    if url.starts_with("https://") || url.starts_with("http://") {
        let _ = std::process::Command::new("open").arg(url).spawn();
    }
}

pub fn draft_reply(s: &mut App, reply: &str) {
    let draft = s.composer.text.trim_end();
    let text = if draft.is_empty() { reply.to_string() } else { format!("{draft}\n{reply}") };
    s.composer = TextEdit::new(text.as_str());
    s.composer.focused = true;
}

pub fn answer_request(s: &mut App, request: &str, choices: Vec<String>, answers: std::collections::BTreeMap<String, String>) {
    let Some(id) = s.selected.clone() else { return };
    let Some(client) = s.client.clone() else {
        note(s, &id, "Fixture mode: not sent".into());
        return;
    };
    if s.answering.is_some() || s.is_settled(request) {
        return;
    }
    s.answering = Some(request.to_string());
    s.settled.insert(request.to_string(), None);
    note(s, &id, "Answering…".into());
    let (out, request) = (s.outbox.clone(), request.to_string());
    std::thread::spawn(move || {
        let result = client.respond(&id, &request, choices, answers);
        let _ = out.send(Bg::Answered(id, request, result));
    });
}

pub fn pick_work(s: &mut App, item: &str) {
    let Some(id) = s.selected.clone() else { return };
    s.work_pick.insert(id.clone(), item.to_string());
    if let Some(work) = session(s, &id).and_then(|x| x.held_work.iter().find(|w| w.item_id == item).cloned()) {
        fetch_item(s, &work.project_key, &work.item_id);
    }
}

pub fn session_edits(s: &App, id: &str) -> Vec<feed::edits::Edit> {
    s.panes.get(id).map(|p| feed::edits::edits(p.transcript.items())).unwrap_or_default()
}

pub fn short_path(s: &App, id: &str, path: &str) -> String {
    let cwd = session(s, id).and_then(|x| x.checkout_path()).unwrap_or_default();
    if let Some(rel) = path.strip_prefix(&format!("{cwd}/")) {
        return rel.to_string();
    }
    let parts: Vec<&str> = path.split('/').filter(|p| !p.is_empty()).collect();
    if parts.len() > 4 { format!("…/{}", parts[parts.len() - 4..].join("/")) } else { path.to_string() }
}

pub fn refresh_diff(s: &mut App) {
    let Some(id) = s.selected.clone() else { return };
    let edits = session_edits(s, &id);
    if s.diff_key == (id.clone(), edits.len()) {
        return;
    }
    let files = edits.iter().rev().map(|e| rune_kit::diff::FileDiff::from_texts(short_path(s, &id, &e.path), &e.old, &e.new)).collect();
    s.diff.set_files(files);
    s.diff_key = (id, edits.len());
}

pub fn show_changes(s: &mut App) {
    s.tab = Tab::Changes;
    refresh_diff(s);
}

pub fn send_text(s: &mut App, message: String, tx: &mut Tx) {
    s.composer = TextEdit::new(message.as_str());
    send(s, tx);
}

fn fetch_item(s: &mut App, project: &str, item: &str) {
    if s.items.contains_key(item) {
        return;
    }
    if let Some(f) = &s.fixtures {
        if let Ok(text) = std::fs::read_to_string(format!("{}/items/{item}.json", f.dir)) {
            if let Ok(value) = serde_json::from_str(&text) {
                s.items.insert(item.to_string(), value);
            }
        }
        return;
    }
    if let Some(client) = s.client.clone() {
        let (tx, project, item) = (s.outbox.clone(), project.to_string(), item.to_string());
        std::thread::spawn(move || {
            if let Ok(work) = client.work_item(&project, &item) {
                let _ = tx.send(Bg::Item(item, work));
            }
        });
    }
}

pub fn send(s: &mut App, tx: &mut Tx) {
    if let Some(id) = s.selected.clone() {
        send_to(s, &id, tx);
    }
}

pub fn send_to(s: &mut App, id: &str, tx: &mut Tx) {
    let message = s.composer.text.trim().to_string();
    if message.is_empty() || sending(s, id) {
        return;
    }
    if s.client.is_none() {
        note(s, id, "Fixture mode: not sent".into());
        return;
    }
    s.composer = TextEdit::new("");
    match s.unsent.take_same(id, &message) {
        Some(again) if again.reuses_key() => dispatch(s, id, again, true, tx),
        _ => {
            let pending = fresh(s, id, message);
            dispatch(s, id, pending, false, tx);
        }
    }
}

pub fn retry(s: &mut App, id: &str, tx: &mut Tx) {
    let Some(unsent) = s.unsent.latest(id).cloned() else { return };
    if sending(s, id) || s.client.is_none() {
        return;
    }
    if s.composer_for.as_deref() == Some(id) && s.composer.text.trim() == unsent.text {
        s.composer = TextEdit::new("");
        s.composer.focused = true;
    } else if s.drafts.get(id).trim() == unsent.text {
        s.drafts.set(id, "");
    }
    s.unsent.remove(id, &unsent.key);
    if unsent.reuses_key() {
        dispatch(s, id, unsent, true, tx);
    } else {
        let pending = fresh(s, id, unsent.text);
        dispatch(s, id, pending, false, tx);
    }
}

fn confirmed(s: &mut App, id: &str, ask: Confirm, needed: bool) -> bool {
    if needed && s.confirming.get(id) != Some(&ask) {
        s.confirming.insert(id.to_string(), ask);
        return false;
    }
    s.confirming.remove(id);
    true
}

pub fn confirm_label(s: &App, id: &str, ask: Confirm) -> &'static str {
    let asking = s.confirming.get(id) == Some(&ask);
    match ask {
        Confirm::PutBack if asking => "It may already have arrived. Put back?",
        Confirm::PutBack => "Put back",
        Confirm::Discard if asking && s.unsent.latest(id).is_some_and(|u| u.maybe_delivered) => "It may already have arrived. Dismiss?",
        Confirm::Discard if asking => "Discard this message?",
        Confirm::Discard => "Dismiss",
        Confirm::SendNew if asking => "It may already have arrived. Send again?",
        Confirm::SendNew => "Send as new",
    }
}

pub fn put_back_unsent(s: &mut App, id: &str) {
    let Some(unsent) = s.unsent.latest(id).cloned() else { return };
    if !confirmed(s, id, Confirm::PutBack, unsent.maybe_delivered) {
        return;
    }
    s.unsent.remove(id, &unsent.key);
    if unsent.maybe_delivered {
        note(s, id, "Put back. It may already have arrived.".into());
    }
    let merged = feed::composer::put_back(shown_text(s, id), &unsent.text);
    if s.composer_for.as_deref() == Some(id) {
        s.composer = TextEdit::new(merged.as_str());
        s.composer.focused = true;
    }
    s.drafts.set(id, &merged);
    over::layout::save(s);
}

pub fn unsent_held(s: &App, id: &str) -> bool {
    s.unsent.latest(id).is_some_and(|u| shown_text(s, id).trim() != u.text)
}

pub fn offers_new_send(s: &App, id: &str) -> bool {
    s.unsent.latest(id).is_some_and(|u| u.offers_new_send())
}

pub fn send_as_new(s: &mut App, id: &str, tx: &mut Tx) {
    let Some(unsent) = s.unsent.latest(id).cloned().filter(|u| u.offers_new_send()) else { return };
    if sending(s, id) || s.client.is_none() || !confirmed(s, id, Confirm::SendNew, true) {
        return;
    }
    if s.composer_for.as_deref() == Some(id) && s.composer.text.trim() == unsent.text {
        s.composer = TextEdit::new("");
    } else if s.drafts.get(id).trim() == unsent.text {
        s.drafts.set(id, "");
    }
    s.unsent.remove(id, &unsent.key);
    let pending = fresh(s, id, unsent.text);
    dispatch(s, id, pending, false, tx);
}

pub fn dismiss_unsent(s: &mut App, id: &str) {
    let needed = unsent_held(s, id) || s.unsent.latest(id).is_some_and(|u| u.maybe_delivered);
    if !confirmed(s, id, Confirm::Discard, needed) {
        return;
    }
    if let Some(key) = s.unsent.latest(id).map(|u| u.key.clone()) {
        s.unsent.remove(id, &key);
        over::layout::save(s);
    }
}

fn fresh(s: &App, id: &str, text: String) -> Unsent {
    Unsent { session: id.to_string(), text, key: feed::catalog::request_key(), busy: send_busy(s, id), maybe_delivered: true, error: "the app closed before the daemon answered".into(), tries: 0 }
}

pub fn send_busy(s: &App, id: &str) -> Option<Busy> {
    let waiting = session(s, id).is_some_and(|x| feed::derive::sessions::busy(x) && x.state(s.now) == feed::catalog::State::NeedsYou);
    busy_delivery(s, id).or_else(|| (waiting || busy_now(s, id) && s.busy_default == Some(Busy::Queue)).then_some(Busy::Queue))
}

fn dispatch(s: &mut App, id: &str, pending: Unsent, resent: bool, tx: &mut Tx) {
    let Some(client) = s.client.clone() else { return };
    let id = id.to_string();
    s.confirming.remove(&id);
    s.in_flight.entry(id.clone()).or_default().push(pending.clone());
    if s.composer_for.as_deref() == Some(id.as_str()) {
        s.drafts.set(&id, &s.composer.text);
    }
    if let Err(error) = over::layout::save_now(s) {
        settle(s, &id, &pending.key);
        put_back(s, &id, &pending.text);
        let maybe_delivered = resent && pending.maybe_delivered;
        s.unsent.add(&id, Unsent { maybe_delivered, error: format!("the message couldn't be saved first ({error})"), ..pending });
        return;
    }
    tx.scroll_to_bottom(format!("transcript-{id}"));
    let out = s.outbox.clone();
    std::thread::spawn(move || {
        let result = client.send_as(&pending.session, &pending.text, pending.busy, &pending.key);
        let _ = out.send(Bg::Sent(id, pending, resent, result));
    });
}

pub fn busy_now(s: &App, id: &str) -> bool {
    session(s, id).is_some_and(|x| feed::derive::sessions::busy(x) && x.state(s.now) != feed::catalog::State::NeedsYou)
}

pub fn can_stop(s: &App, id: &str) -> bool {
    session(s, id).is_some_and(|x| feed::derive::sessions::busy(x) && x.capabilities.interrupt)
}

pub fn busy_delivery(s: &App, id: &str) -> Option<Busy> {
    let steers = session(s, id).is_some_and(|x| x.capabilities.steer);
    (busy_now(s, id) && steers).then(|| s.busy_pick.get(id).copied().or(s.busy_default).unwrap_or(Busy::Queue))
}

pub fn pick_busy(s: &mut App, id: &str, busy: Busy) {
    s.busy_pick.insert(id.to_string(), busy);
}

pub fn stop(s: &mut App, id: &str) {
    if s.stopping.contains(id) {
        return;
    }
    let Some(client) = s.client.clone() else {
        note(s, id, "Fixture mode: not stopped".into());
        return;
    };
    s.stopping.insert(id.to_string());
    let (out, id) = (s.outbox.clone(), id.to_string());
    std::thread::spawn(move || {
        let result = client.interrupt(&id);
        let _ = out.send(Bg::Stopped(id, result));
    });
}

pub fn sending(s: &App, id: &str) -> bool {
    s.in_flight.get(id).is_some_and(|list| !list.is_empty())
}

fn settle(s: &mut App, id: &str, key: &str) {
    if let Some(list) = s.in_flight.get_mut(id) {
        list.retain(|p| p.key != key);
        if list.is_empty() {
            s.in_flight.remove(id);
        }
    }
}

fn note(s: &mut App, id: &str, text: String) {
    s.status_line.insert(id.to_string(), text);
}

pub fn composer_note(s: &App, id: &str) -> Option<String> {
    let mut parts: Vec<String> = Vec::new();
    if s.stopping.contains(id) {
        parts.push("Stopping…".into());
    }
    if sending(s, id) {
        parts.push("Sending…".into());
    }
    if let Some(latest) = s.unsent.latest(id)
        && shown_text(s, id).trim() != latest.text
    {
        parts.push(latest.quoted());
    }
    parts.extend(s.unsent.note(id));
    let queued = s.queued.get(id).into_iter().flatten().find_map(|(at, receipt)| feed::composer::queued_note(&s.queue_rows, &s.rekeyed, id, receipt, s.snapshots >= at + 2));
    if !parts.is_empty() {
        parts.extend(queued);
        return Some(parts.join(" · "));
    }
    match (queued, s.status_line.get(id)) {
        (Some(q), Some(line)) if !line.starts_with("Sent") => Some(format!("{q} · {line}")),
        (Some(q), _) => Some(q),
        (None, line) => line.cloned(),
    }
}

fn shown_text<'a>(s: &'a App, id: &str) -> &'a str {
    if s.composer_for.as_deref() == Some(id) { &s.composer.text } else { s.drafts.get(id) }
}

fn put_back(s: &mut App, id: &str, text: &str) {
    if !shown_text(s, id).trim().is_empty() {
        return;
    }
    if s.composer_for.as_deref() == Some(id) {
        let focused = s.composer.focused;
        s.composer = TextEdit::new(text);
        s.composer.focused = focused;
    } else {
        s.drafts.set(id, text);
    }
}

fn take_sent(s: &mut App, id: String, pending: Unsent, retry: bool, result: Result<Delivered, feed::catalog::RespondError>) {
    let id = feed::composer::current(&s.rekeyed, &id).to_string();
    settle(s, &id, &pending.key);
    s.unsent.remove(&id, &pending.key);
    let failure = match result {
        Ok(Delivered::Unconfirmed) => Some((true, true, "the daemon took it but hasn't confirmed delivery".to_string())),
        Ok(receipt @ (Delivered::Queued(_) | Delivered::QueuedUntilAnswer(_))) => {
            s.status_line.remove(&id);
            s.queued.entry(id.clone()).or_default().push((s.snapshots, receipt));
            None
        }
        Ok(delivered) => {
            note(s, &id, delivered.note(0));
            None
        }
        Err(error) => Some((error.maybe_delivered, error.answered, error.message)),
    };
    if let Some((maybe_delivered, answered, error)) = failure {
        put_back(s, &id, &pending.text);
        s.unsent.add(&id, pending.failed(retry, maybe_delivered, answered, error));
    }
}

pub fn switch_composer(s: &mut App) {
    let target = over::composer_target(s);
    if target == s.composer_for {
        return;
    }
    let carried = match s.composer_for.take() {
        Some(old) => {
            s.drafts.set(&old, &s.composer.text);
            String::new()
        }
        None => s.composer.text.trim().to_string(),
    };
    let draft = target.as_deref().map(|id| s.drafts.get(id).to_string()).unwrap_or_default();
    let focused = s.composer.focused;
    s.composer = TextEdit::new(if carried.is_empty() { draft } else { feed::composer::put_back(&draft, &carried) });
    s.composer.focused = focused;
    s.composer_for = target;
}

fn move_key<V>(map: &mut HashMap<String, V>, old: &str, new: &str) {
    if let Some(v) = map.remove(old) {
        map.entry(new.to_string()).or_insert(v);
    }
}

fn move_member(set: &mut HashSet<String>, old: &str, new: &str) {
    if set.remove(old) {
        set.insert(new.to_string());
    }
}

fn follow_rekeys(s: &mut App, rekeyed: HashMap<String, String>) {
    if rekeyed == s.rekeyed {
        return;
    }
    s.rekeyed = rekeyed;
    if let Some(id) = s.composer_for.clone() {
        s.drafts.set(&id, &s.composer.text);
    }
    let mut restored: Vec<(String, String)> = s.unsent.iter().filter(|(id, u)| s.drafts.get(id).trim() == u.text).map(|(id, u)| (id.clone(), u.text.clone())).collect();
    restored.dedup_by(|a, b| a.0 == b.0);
    for (id, _) in &restored {
        s.drafts.set(id, "");
    }
    s.drafts.rekey(&s.rekeyed);
    s.unsent.rekey(&s.rekeyed);
    for (old, text) in restored {
        let new = feed::composer::current(&s.rekeyed, &old).to_string();
        if s.drafts.get(&new).trim().is_empty() {
            s.drafts.set(&new, &text);
        }
    }
    let retired: Vec<(String, String)> = s.rekeyed.keys().map(|old| (old.clone(), feed::composer::current(&s.rekeyed, old).to_string())).filter(|(old, new)| old != new).collect();
    for (old, new) in retired {
        if s.selected.as_deref() == Some(old.as_str()) {
            s.selected = Some(new.clone());
        }
        if s.composer_for.as_deref() == Some(old.as_str()) {
            s.composer_for = Some(new.clone());
        }
        if s.panes.remove(&old).is_some() {
            s.lru.retain(|x| x != &old);
            load_pane(s, &new);
        }
        if let Some(pending) = s.in_flight.remove(&old) {
            s.in_flight.entry(new.clone()).or_default().extend(pending);
        }
        if let Some(receipts) = s.queued.remove(&old) {
            s.queued.entry(new.clone()).or_default().extend(receipts);
        }
        move_key(&mut s.status_line, &old, &new);
        move_key(&mut s.busy_pick, &old, &new);
        move_key(&mut s.work_pick, &old, &new);
        move_member(&mut s.stopping, &old, &new);
    }
    if let Some(id) = s.composer_for.clone()
        && s.drafts.get(&id) != s.composer.text
    {
        let focused = s.composer.focused;
        s.composer = TextEdit::new(s.drafts.get(&id));
        s.composer.focused = focused;
    }
}

fn poll(s: &mut App, t: f64, tx: &mut Tx) -> bool {
    s.clock = t;
    let mut changed = false;
    let mut arrived = false;
    while let Ok(message) = s.inbox.try_recv() {
        changed = true;
        match message {
            Bg::Snapshot(at, sessions) => {
                if s.ov.live.take_sessions(&at, sessions) {
                    s.sessions = s.ov.live.sessions().to_vec();
                }
                s.now = OffsetDateTime::now_utc();
                s.status = "live".into();
                if let Some(id) = s.selected.clone() {
                    fetch_requests(s, &id);
                    if let Some(work) = session(s, &id).and_then(|x| x.primary_work(s.work_pick.get(&id).map(String::as_str)).cloned()) {
                        fetch_item(s, &work.project_key, &work.item_id);
                    }
                }
            }
            Bg::Item(id, item) => {
                s.items.insert(id, item);
            }
            Bg::Overview(fetched) => {
                s.ov.live.refresh(*fetched);
                s.sessions = s.ov.live.sessions().to_vec();
                fetch_tails(s);
                arrived = true;
            }
            Bg::Tail(id, last_active, result) => {
                s.ov.live.take_tail(&id, last_active, result);
                fetch_tails(s);
            }
            Bg::Ledger(project, started, result) => {
                s.ov.live.take_ledger(&project, started, result);
            }
            Bg::OverviewError(error) => s.ov.notice = Some(format!("Couldn't refresh the overview: {error}")),
            Bg::SendSettings(busy, rekeyed, queue) => {
                s.snapshots += 1;
                s.queue_rows = queue;
                s.busy_default = Busy::parse(&busy);
                follow_rekeys(s, rekeyed);
                let (rows, keys, seen) = (&s.queue_rows, &s.rekeyed, s.snapshots);
                for (id, receipts) in s.queued.iter_mut() {
                    receipts.retain(|(at, receipt)| feed::composer::queued_note(rows, keys, id, receipt, seen >= at + 2).is_some());
                }
                s.queued.retain(|_, receipts| !receipts.is_empty());
            }
            Bg::Sent(id, pending, retry, result) => take_sent(s, id, pending, retry, result),
            Bg::Stopped(id, result) => {
                let id = feed::composer::current(&s.rekeyed, &id).to_string();
                s.stopping.remove(&id);
                let status = match result {
                    Ok(stop) => stop.note().to_string(),
                    Err(error) if error.maybe_delivered => format!("Stop sent, not confirmed: {}", error.message),
                    Err(error) => format!("Couldn't stop: {}", error.message),
                };
                note(s, &id, status);
            }
            Bg::Requests(id, requests, fetched_at) => {
                if s.requests_at.get(&id).is_some_and(|newest| *newest > fetched_at) {
                    continue;
                }
                s.requests_at.insert(id.clone(), fetched_at);
                s.reopen_undelivered(&requests, fetched_at);
                s.requests.insert(id, requests);
            }
            Bg::Answered(id, request, result) => {
                s.answering = None;
                if let Some(done_at) = s.settled.get_mut(&request) {
                    *done_at = Some(std::time::Instant::now());
                }
                let status = match result {
                    Ok(()) => "Answered".to_string(),
                    Err(error) if error.maybe_delivered => format!("Sent, not confirmed: {}", error.message),
                    Err(error) => {
                        s.settled.remove(&request);
                        format!("Not answered: {}", error.message)
                    }
                };
                note(s, &id, status.clone());
                s.answer_status.insert(request.clone(), status);
                fetch_requests_at(s, &id, &[0, 2000, 6000, 9000]);
            }
            Bg::NewSession(loaded) => ui::new_session::take(s, loaded),
            Bg::Error(error) => s.status = format!("daemon: {error}"),
        }
    }
    let mut busy = false;
    let mut counter = s.counter;
    let mut panes_moved = false;
    for pane in s.panes.values_mut() {
        let mut moved = false;
        if let Some(replay) = &mut pane.replay {
            let (c, more) = replay.pump(t, &mut pane.transcript);
            moved |= c;
            busy |= more;
            if !more {
                pane.status = "replay finished".into();
            }
        }
        if let Some(rx) = &pane.live {
            busy = true;
            while let Ok(message) = rx.try_recv() {
                match message {
                    Message::Batch(b) => moved |= pane.transcript.apply(b.seq, b.events),
                    Message::Status(status) => pane.status = status,
                }
            }
        }
        if moved {
            pane.rebuild(&mut counter);
            changed = true;
            panes_moved = true;
        }
    }
    s.counter = counter;
    s.panes_moved |= panes_moved;
    if s.ov.live.stale() || (s.panes_moved && s.clock - s.built_at >= PANE_REBUILD_SECS) {
        rebuild(s);
        changed = true;
    }
    if arrived {
        over::arrive(s);
    }
    if s.tab == Tab::Changes && (changed || s.diff_key.0 != s.selected.clone().unwrap_or_default()) {
        refresh_diff(s);
    }
    if s.screen != Screen::Session {
        s.revealed = None;
    } else if s.revealed != s.selected
        && let Some(id) = s.selected.clone()
        && ui::in_sidebar(s, &id)
    {
        tx.scroll_into_view_in("sidebar-list", format!("row-{id}"));
        s.revealed = Some(id);
    }
    changed |= ui::session_menu::drain(s);
    changed |= ui::limit::drain(s) | ui::accounts::drain(s) | ui::pr_link::drain(s);
    switch_composer(s);
    if let Some(id) = s.composer_for.clone() {
        s.drafts.set(&id, &s.composer.text);
    }
    over::layout::save(s);
    tx.wake_after(Duration::from_millis(if busy { 16 } else { 250 }));
    changed
}

fn on_key(s: &mut App, ev: &KeyEvent, tx: &mut Tx) -> bool {
    ui::new_session::on_key(s, ev, tx) || over::on_key(s, ev, tx)
}

fn main() {
    rune::App::new(AppSpec::<App> {
        poll: Some(poll),
        on_key: Some(on_key),
        fonts: rune_theme_opencode::FONTS,
        families: rune_theme_opencode::FAMILIES,
        min_size: Some((1100.0, 700.0)),
        ..AppSpec::new("Agenthail", (1440.0, 900.0), init, ui::view)
    })
        .chrome(Chrome { full_size_content: true, traffic_lights: Some((20.0, 20.0)), drag_regions: true })
        .window_options(WindowOptions { transparent: true, resizable: true, visible: true, ..Default::default() })
        .srgb_blend(true)
        .run();
}
