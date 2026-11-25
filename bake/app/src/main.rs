mod icons;
mod theme;
mod transcript;
mod ui;

use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Duration;

use feed::catalog::{BoardRow, Client, OpenRequest, Session, Snapshot, WorkItem};
use feed::source::{Live, Message};
use rune::{AppSpec, Chrome, Key, KeyEvent, TextEdit, Tx, WindowOptions};
use time::OffsetDateTime;

use crate::transcript::{Pane, Replay};

pub enum Bg {
    Snapshot(Vec<Session>),
    Item(String, WorkItem),
    Board(String, Vec<BoardRow>),
    Sent(String, Result<(), feed::catalog::RespondError>),
    Requests(String, Vec<OpenRequest>, std::time::Instant),
    Answered(String, String, Result<(), feed::catalog::RespondError>),
    Error(String),
}

#[derive(Clone, Copy, PartialEq)]
pub enum Tab {
    Conversation,
    Changes,
}

#[derive(Clone, PartialEq)]
pub enum Screen {
    Session,
    Project(String),
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
    pub send_status: String,
    pub palette: Option<TextEdit>,
    pub palette_ix: usize,
    pub palette_order: Vec<String>,
    pub items: HashMap<String, WorkItem>,
    pub boards: HashMap<String, Vec<BoardRow>>,
    pub requests: HashMap<String, Vec<OpenRequest>>,
    pub answering: Option<String>,
    pub settled: HashMap<String, Option<std::time::Instant>>,
    pub answer_status: HashMap<String, String>,
    pub status_line: HashMap<String, String>,
    pub sending: HashSet<String>,
    pub requests_at: HashMap<String, std::time::Instant>,
    pub tab: Tab,
    pub work_pick: HashMap<String, String>,
    pub diff: rune_kit::diff::DiffState,
    pub diff_key: (String, usize),
    client: Option<Client>,
    fixtures: Option<Fixtures>,
    inbox: Receiver<Bg>,
    outbox: Sender<Bg>,
    pub status: String,
    clock: f64,
}

fn args() -> Vec<String> {
    rune::extra_args().to_vec()
}

fn arg(name: &str) -> Option<String> {
    let args = args();
    args.iter().position(|a| a == name).and_then(|i| args.get(i + 1)).cloned()
}

fn load_snapshot(path: &str) -> (Vec<Session>, OffsetDateTime) {
    let text = std::fs::read_to_string(path).unwrap_or_default();
    let raw: serde_json::Value = serde_json::from_str(&text).unwrap_or_default();
    let now = raw["updatedAt"].as_str().and_then(feed::catalog::parse_time).unwrap_or_else(OffsetDateTime::now_utc);
    let snap: Snapshot = serde_json::from_value(raw).unwrap_or_default();
    (snap.sessions, now)
}

fn init() -> App {
    let (outbox, inbox) = channel();
    let mut app = App {
        sessions: Vec::new(),
        now: OffsetDateTime::now_utc(),
        selected: None,
        screen: Screen::Session,
        panes: HashMap::new(),
        lru: Vec::new(),
        open: HashSet::new(),
        counter: 0,
        by_project: false,
        composer: TextEdit::new(""),
        send_status: String::new(),
        palette: None,
        palette_ix: 0,
        palette_order: Vec::new(),
        items: HashMap::new(),
        boards: HashMap::new(),
        requests: HashMap::new(),
        answering: None,
        settled: HashMap::new(),
        answer_status: HashMap::new(),
        status_line: HashMap::new(),
        sending: HashSet::new(),
        requests_at: HashMap::new(),
        tab: Tab::Conversation,
        work_pick: HashMap::new(),
        diff: rune_kit::diff::DiffState::new(Vec::new()),
        diff_key: (String::new(), usize::MAX),
        client: None,
        fixtures: None,
        inbox,
        outbox,
        status: "connecting".into(),
        clock: 0.0,
    };
    if let Some(dir) = arg("--fixtures").or_else(|| std::env::var("AGENTHAIL_FIXTURES").ok()) {
        let (sessions, now) = load_snapshot(&format!("{dir}/snapshot.json"));
        app.sessions = sessions;
        app.now = now;
        let streams = std::fs::read_to_string(format!("{dir}/streams.json")).ok().and_then(|t| serde_json::from_str(&t).ok()).unwrap_or_default();
        let speed = arg("--speed").and_then(|s| s.parse().ok()).unwrap_or(10.0);
        app.fixtures = Some(Fixtures { dir, streams, speed });
        app.status = "fixtures".into();
    } else {
        match Client::from_env() {
            Ok(client) => {
                let tx = app.outbox.clone();
                let c = client.clone();
                std::thread::spawn(move || {
                    loop {
                        let message = match c.snapshot() {
                            Ok(snap) => Bg::Snapshot(snap.sessions),
                            Err(error) => Bg::Error(error.to_string()),
                        };
                        if tx.send(message).is_err() {
                            return;
                        }
                        std::thread::sleep(Duration::from_secs(2));
                    }
                });
                app.client = Some(client);
            }
            Err(error) => app.status = error.to_string(),
        }
    }
    let first = arg("--select").or_else(|| ui::ordered(&app).first().map(|s| s.id.clone()));
    if let Some(id) = first {
        select(&mut app, &id);
    }
    if arg("--tab").as_deref() == Some("changes") {
        app.tab = Tab::Changes;
    }
    if let Some(project) = arg("--project") {
        open_project(&mut app, &project);
    }
    app
}

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
    s.selected = Some(id.to_string());
    s.screen = Screen::Session;
    s.tab = Tab::Conversation;
    s.composer = TextEdit::new("");
    s.composer.focused = true;
    s.send_status = if s.sending.contains(id) { "Sending…".to_string() } else { s.status_line.get(id).cloned().unwrap_or_default() };
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
    if let Some(work) = session(s, id).and_then(|x| x.primary_work(s.work_pick.get(id).map(String::as_str)).cloned()) {
        fetch_item(s, &work.project_key, &work.item_id);
    }
    fetch_requests(s, id);
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

pub fn draft_reply(s: &mut App, reply: &str) {
    let draft = s.composer.text.trim_end();
    let text = if draft.is_empty() { reply.to_string() } else { format!("{draft}\n{reply}") };
    s.composer = TextEdit::new(text.as_str());
    s.composer.focused = true;
}

pub fn answer_request(s: &mut App, request: &str, choices: Vec<String>) {
    let Some(id) = s.selected.clone() else { return };
    let Some(client) = s.client.clone() else {
        s.send_status = "Fixture mode: not sent".into();
        return;
    };
    if s.answering.is_some() || s.is_settled(request) {
        return;
    }
    s.answering = Some(request.to_string());
    s.settled.insert(request.to_string(), None);
    s.send_status = "Answering…".into();
    let (out, request) = (s.outbox.clone(), request.to_string());
    std::thread::spawn(move || {
        let result = client.respond(&id, &request, choices);
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

pub fn open_project(s: &mut App, key: &str) {
    s.screen = Screen::Project(key.to_string());
    if s.boards.contains_key(key) {
        return;
    }
    if let Some(f) = &s.fixtures {
        let file = key.replace(['/', ':'], "_");
        if let Ok(text) = std::fs::read_to_string(format!("{}/boards/{file}.json", f.dir)) {
            if let Ok(rows) = serde_json::from_str(&text) {
                s.boards.insert(key.to_string(), rows);
            }
        }
        return;
    }
    if let Some(client) = s.client.clone() {
        let (tx, key) = (s.outbox.clone(), key.to_string());
        std::thread::spawn(move || {
            if let Ok(rows) = client.board(&key) {
                let _ = tx.send(Bg::Board(key, rows));
            }
        });
    }
}

pub fn send(s: &mut App, tx: &mut Tx) {
    let message = s.composer.text.trim().to_string();
    let Some(id) = s.selected.clone() else { return };
    if message.is_empty() || s.sending.contains(&id) {
        return;
    }
    s.composer = TextEdit::new("");
    match s.client.clone() {
        Some(client) => {
            s.send_status = "Sending…".into();
            s.sending.insert(id.clone());
            let out = s.outbox.clone();
            std::thread::spawn(move || {
                let result = client.send(&id, &message);
                let _ = out.send(Bg::Sent(id, result));
            });
        }
        None => s.send_status = "Fixture mode: not sent".into(),
    }
    tx.scroll_to_bottom(format!("transcript-{}", s.selected.clone().unwrap_or_default()));
}

fn poll(s: &mut App, t: f64, tx: &mut Tx) -> bool {
    s.clock = t;
    let mut changed = false;
    while let Ok(message) = s.inbox.try_recv() {
        changed = true;
        match message {
            Bg::Snapshot(sessions) => {
                s.sessions = sessions;
                s.now = OffsetDateTime::now_utc();
                s.status = "live".into();
                if s.selected.is_none() {
                    if let Some(id) = ui::ordered(s).first().map(|x| x.id.clone()) {
                        select(s, &id);
                    }
                }
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
            Bg::Board(key, rows) => {
                s.boards.insert(key, rows);
            }
            Bg::Sent(id, result) => {
                s.sending.remove(&id);
                let status = match result {
                    Ok(()) => "Sent".to_string(),
                    Err(error) if error.maybe_delivered => format!("Sent, not confirmed: {}", error.message),
                    Err(error) => format!("Not sent: {}", error.message),
                };
                if s.selected.as_deref() == Some(id.as_str()) {
                    s.send_status = status.clone();
                }
                s.status_line.insert(id, status);
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
                if s.selected.as_deref() == Some(id.as_str()) {
                    s.send_status = status.clone();
                }
                s.status_line.insert(id.clone(), status.clone());
                s.answer_status.insert(id.clone(), status);
                fetch_requests_at(s, &id, &[0, 2000, 6000, 9000]);
            }
            Bg::Error(error) => s.status = format!("daemon: {error}"),
        }
    }
    let mut busy = false;
    let mut counter = s.counter;
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
        }
    }
    s.counter = counter;
    if s.tab == Tab::Changes && (changed || s.diff_key.0 != s.selected.clone().unwrap_or_default()) {
        refresh_diff(s);
    }
    tx.wake_after(Duration::from_millis(if busy { 16 } else { 250 }));
    changed
}

pub fn open_palette(s: &mut App, tx: &mut Tx) {
    let mut edit = TextEdit::new("");
    edit.focused = true;
    s.palette = Some(edit);
    s.palette_ix = 0;
    s.palette_order = ui::ordered(s).into_iter().map(|x| x.id.clone()).collect();
    tx.focus("palette-input", false);
}

fn on_key(s: &mut App, key: &KeyEvent, tx: &mut Tx) -> bool {
    if key.is_command('k') {
        if s.palette.is_some() {
            s.palette = None;
        } else {
            open_palette(s, tx);
        }
        return true;
    }
    if key.is_command('j') {
        ui::next_waiting(s);
        return true;
    }
    if key.is_command('1') {
        s.by_project = !s.by_project;
        return true;
    }
    if s.palette.is_some() {
        match &key.key {
            Key::Escape => {
                s.palette = None;
                return true;
            }
            Key::Down => {
                s.palette_ix += 1;
                return true;
            }
            Key::Up => {
                s.palette_ix = s.palette_ix.saturating_sub(1);
                return true;
            }
            _ => {}
        }
    }
    false
}

fn main() {
    rune::App::new(AppSpec::<App> { poll: Some(poll), on_key: Some(on_key), min_size: Some((1100.0, 640.0)), ..AppSpec::new("Agenthail", (1440.0, 900.0), init, ui::view) })
        .chrome(Chrome { full_size_content: true, traffic_lights: Some((20.0, 20.0)), drag_regions: true })
        .window_options(WindowOptions { transparent: true, resizable: true, visible: true, ..Default::default() })
        .srgb_blend(true)
        .run();
}
