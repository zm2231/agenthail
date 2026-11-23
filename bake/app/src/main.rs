mod theme;
mod transcript;
mod ui;

use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Duration;

use feed::catalog::{BoardRow, Client, Session, Snapshot, State, WorkItem};
use feed::source::{Live, Message};
use rune::{AppSpec, Key, KeyEvent, TextEdit, Tx};
use time::OffsetDateTime;

use crate::transcript::{Pane, Replay};

pub enum Bg {
    Snapshot(Vec<Session>),
    Item(String, WorkItem),
    Board(String, Vec<BoardRow>),
    Sent(String, Result<(), String>),
    Error(String),
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
    if let Some(project) = arg("--project") {
        open_project(&mut app, &project);
    }
    app
}

pub fn session<'a>(s: &'a App, id: &str) -> Option<&'a Session> {
    s.sessions.iter().find(|x| x.id == id)
}

pub fn select(s: &mut App, id: &str) {
    s.selected = Some(id.to_string());
    s.screen = Screen::Session;
    s.composer = TextEdit::new("");
    s.composer.focused = true;
    s.send_status.clear();
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
    if let Some(work) = session(s, id).and_then(|x| x.held_work.first().cloned()) {
        fetch_item(s, &work.project_key, &work.item_id);
    }
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
    if message.is_empty() {
        return;
    }
    s.composer = TextEdit::new("");
    match s.client.clone() {
        Some(client) => {
            s.send_status = "Sending…".into();
            let out = s.outbox.clone();
            std::thread::spawn(move || {
                let result = client.send(&id, &message).map_err(|e| e.to_string());
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
                    if let Some(work) = session(s, &id).and_then(|x| x.held_work.first().cloned()) {
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
            Bg::Sent(_, result) => {
                s.send_status = match result {
                    Ok(()) => "Sent".into(),
                    Err(error) => format!("Not sent: {error}"),
                };
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
        let next = ui::ordered(s)
            .into_iter()
            .filter(|x| x.state(s.now) == State::NeedsYou)
            .map(|x| x.id.clone())
            .skip_while(|id| Some(id) != s.selected.as_ref())
            .nth(1)
            .or_else(|| ui::ordered(s).into_iter().find(|x| x.state(s.now) == State::NeedsYou).map(|x| x.id.clone()));
        if let Some(id) = next {
            select(s, &id);
        }
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
    rune::run(AppSpec::<App> { poll: Some(poll), on_key: Some(on_key), ..AppSpec::new("Agenthail", (1380.0, 880.0), init, ui::view) });
}
