mod history;
mod icons;
mod instance;
mod link;
mod look;
mod menubar;
mod notify;
mod over;
mod settings;
mod theme;
mod trace;
mod transcript;
mod ui;
mod voice;
mod wake;
mod web;

#[cfg(test)]
#[path = "loop_tests.rs"]
mod loop_tests;

use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Duration;

use feed::catalog::{Client, OpenRequest, Session, WorkItem};
use feed::composer::{Busy, Delivered, Drafts, Unconfirmed, Unsent};
use feed::derive::{Recorded, WorkKey};
use feed::derive::live::{Fetched, Ledger, Overview};
use feed::derive::tails::{self, Tail};
use feed::source::{Live, Message};
use rune::{AppSpec, Chrome, KeyEvent, TextEdit, Tx, WindowOptions};
use time::OffsetDateTime;

use crate::over::Over;
use crate::transcript::{Pane, Replay};

pub enum Bg {
    Snapshot(String, String, u64, Vec<Session>, Option<Vec<String>>),
    DecisionsRev(String),
    Changed(feed::watch::Wants),
    Reopened,
    LedgerChanged(String, std::time::Instant),
    LedgersReopened,
    SessionNews,
    SessionsMoved,
    Removed(Vec<feed::watch::Removal>),
    Item(WorkKey, WorkItem),
    Overview(Box<Fetched>),
    Ledger(String, std::time::Instant, Result<Ledger, String>),
    Tail(String, String, Result<Tail, String>, Option<tails::Resume>),
    OverviewError(String),
    SendSettings(String, HashMap<String, String>, Vec<feed::catalog::QueueRow>),
    Sent(String, Unsent, bool, Result<(Delivered, serde_json::Value), feed::catalog::RespondError>),
    Stopped(String, Result<feed::composer::Stop, feed::catalog::RespondError>),
    Requests(String, Vec<OpenRequest>, std::time::Instant),
    Answered(String, String, Result<(), feed::catalog::RespondError>),
    NewSession(ui::new_session::Loaded),
    Looked(String, u64, Result<feed::gone::Gone, feed::gone::Lost>),
    Fixed(String, u64, feed::gone::Fix, Result<(), feed::catalog::RespondError>),
    Queue(String, u64, Result<feed::queue::Read, String>),
    QueueDone(String, (String, i64), feed::queue::Done),
    Recovered(Vec<(feed::queue::Taking, feed::queue::Recovered)>),
    Older(String, u64, Result<feed::history::Older, feed::history::Miss>),
    Body(String, u64, String, u64, Result<String, String>),
    Attach(ui::attach::Done),
    Crew(ui::subagents::Done),
    Complete(ui::complete::Done),
    Down(feed::connect::Problem),
    Started(Result<(), feed::connect::Problem>),
    LeadReplacements(Vec<feed::catalog::LeadReplacement>),
    Notice(ui::notice::Done),
    Link(link::Found),
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
    Work(WorkKey),
    Sessions,
    Extensions,
    Extension(String),
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
    pub composer: TextEdit,
    pub composer_for: Option<String>,
    pub wake: wake::Wake,
    pub drafts: Drafts,
    pub unsent: Unconfirmed,
    pub in_flight: HashMap<String, Vec<Unsent>>,
    confirming: HashMap<String, Confirm>,
    pub queued: HashMap<String, Vec<(u64, Delivered)>>,
    snapshots: u64,
    link: ui::connect::Link,
    queue_rows: Vec<feed::catalog::QueueRow>,
    pub stopping: HashSet<String>,
    pub queue: HashMap<String, Vec<feed::queue::Queued>>,
    pub queue_working: HashSet<(String, i64)>,
    queue_fetching: HashMap<String, u64>,
    queue_gen: HashMap<String, u64>,
    queue_epoch: u64,
    pub queue_note: HashMap<String, String>,
    pub queue_taking: Vec<feed::queue::Taking>,
    pub crew: ui::subagents::Crew,
    pub dock: feed::background::DockChoices,
    pub cover: feed::composer::Cover,
    pub busy_default: Option<Busy>,
    pub busy_pick: HashMap<String, Busy>,
    pub rekeyed: HashMap<String, String>,
    pub items: HashMap<WorkKey, WorkItem>,
    pub requests: HashMap<String, Vec<OpenRequest>>,
    pub answering: Option<String>,
    pub settled: HashMap<String, Option<std::time::Instant>>,
    pub answer_status: HashMap<String, String>,
    pub status_line: HashMap<String, String>,
    pub picks: HashMap<String, Vec<String>>,
    pub typed: HashMap<String, TextEdit>,
    pub asks: ui::waiting::Asks,
    pub requests_at: HashMap<String, std::time::Instant>,
    pub tab: Tab,
    pub work_pick: HashMap<String, WorkKey>,
    pub diff: rune_kit::diff::DiffState,
    pub diff_key: (String, usize),
    pub ov: Over,
    pub menus: ui::session_menu::Menus,
    pub limits: ui::limit::Limits,
    pub accounts: ui::accounts::Accounts,
    pub prs: ui::pr_link::Prs,
    pub turn: ui::turn_options::Turns,
    redeliver: HashMap<String, feed::composer::Backoff>,
    pub inspect: ui::inspect::Inspect,
    pub native_queue: ui::native_queue::NativeQueue,
    pub roles: ui::role::Roles,
    pub delivery: ui::delivery::Delivery,
    pub readies: ui::ready::Readies,
    pub earlier: over::earlier::Earlier,
    pub new_session: Option<ui::new_session::Sheet>,
    bar: menubar::Bar,
    pub attach: ui::attach::Attach,
    pub complete: ui::complete::Complete,
    pub paste: Option<ui::paste::Editing>,
    pub notices: ui::notice::Notices,
    pub holder_menu: Option<String>,
    revealed: Option<String>,
    pub missing: Option<ui::gone::Missing>,
    pub notify: notify::State,
    last_row: Option<Session>,
    client: Option<Client>,
    overview_poke: Option<Sender<Ask>>,
    fixtures: Option<Fixtures>,
    inbox: Receiver<Bg>,
    outbox: Sender<Bg>,
    pub status: String,
    clock: f64,
    panes_moved: bool,
    built_at: f64,
    #[cfg(test)]
    rebuilds: u64,
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

#[derive(Clone, PartialEq)]
pub enum Ask {
    Refresh,
    Reset,
    Reopened,
    Open(String),
    Ledger(String, std::time::Instant),
    Read(String),
}

fn watch_overview(client: Client, tx: Sender<Bg>, poke: Receiver<Ask>, stream: feed::watch::Stream) {
    std::thread::spawn(move || {
        let first = std::time::Instant::now();
        while !stream.settled() && first.elapsed() < feed::watch::LEDGERS_FIRST {
            std::thread::sleep(Duration::from_millis(50));
        }
        let mut known = feed::derive::live::Known::default();
        let (mut due, mut reset, mut last) = (true, false, None::<std::time::Instant>);
        loop {
            if due {
                if let Some(at) = last.filter(|_| !reset) {
                    std::thread::sleep(feed::watch::OVERVIEW_GAP.saturating_sub(at.elapsed()));
                }
                reset = false;
                if let Some(at) = stream.settles_at() {
                    std::thread::sleep(at.saturating_duration_since(std::time::Instant::now()));
                }
                due = false;
                last = Some(std::time::Instant::now());
                let message = match feed::derive::live::fetch(&client, &known, stream.settles_at()) {
                    Ok(fetched) => {
                        due = known.learn(&fetched);
                        Bg::Overview(Box::new(fetched))
                    }
                    Err(error) => {
                        known.unread(std::time::Instant::now());
                        Bg::OverviewError(error.to_string())
                    }
                };
                if tx.send(message).is_err() {
                    return;
                }
            }
            let retry = known.next_retry().map(|at| at.saturating_duration_since(std::time::Instant::now()));
            let wait = if due || !known.orphans().is_empty() { Duration::ZERO } else { feed::watch::OVERVIEW_EVERY.saturating_sub(last.map(|at| at.elapsed()).unwrap_or_default()).min(retry.unwrap_or(Duration::MAX)) };
            let mut reads = Vec::new();
            let mut take = |ask: Ask| match ask {
                Ask::Refresh => due = true,
                Ask::Reset => {
                    reads.extend(known.opened());
                    known = known.carry();
                    (due, reset) = (true, true);
                }
                Ask::Ledger(key, at) => match known.changed(&key, at) {
                    feed::derive::live::Change::Read(id) => reads.push(id),
                    feed::derive::live::Change::Overview => due = true,
                    feed::derive::live::Change::Nothing => {}
                },
                Ask::Read(id) => reads.push(id),
                Ask::Open(id) => {
                    if known.open(&id) {
                        reads.push(id);
                    }
                }
                Ask::Reopened => {
                    due = true;
                    reads.extend(known.opened());
                }
            };
            match poke.recv_timeout(wait) {
                Ok(ask) => take(ask),
                Err(std::sync::mpsc::RecvTimeoutError::Timeout) if retry.is_some_and(|r| r <= wait) => {}
                Err(std::sync::mpsc::RecvTimeoutError::Timeout) => take(Ask::Refresh),
                Err(std::sync::mpsc::RecvTimeoutError::Disconnected) => return,
            }
            while let Ok(ask) = poke.try_recv() {
                take(ask);
            }
            due |= last.is_none_or(|at| at.elapsed() >= feed::watch::OVERVIEW_EVERY) || known.actors_due(std::time::Instant::now());
            reads.extend(known.retries_due(std::time::Instant::now()));
            reads.extend(known.orphans());
            reads.sort();
            reads.dedup();
            if let Some(at) = stream.settles_at().filter(|_| !reads.is_empty()) {
                std::thread::sleep(at.saturating_duration_since(std::time::Instant::now()));
            }
            for id in reads {
                let started = std::time::Instant::now();
                let result = feed::derive::live::ledger(&client, &id, &known);
                known.read(&id, started, &result);
                if tx.send(Bg::Ledger(id, started, result)).is_err() {
                    return;
                }
            }
        }
    });
}

fn empty(live: Overview, persist: bool) -> App {
    let (outbox, inbox) = channel();
    App {
        sessions: Vec::new(),
        now: OffsetDateTime::now_utc(),
        selected: None,
        screen: Screen::Now,
        panes: HashMap::new(),
        lru: Vec::new(),
        open: HashSet::new(),
        counter: 0,
        composer: TextEdit::new(""),
        composer_for: None,
        wake: wake::Wake::default(),
        drafts: Drafts::default(),
        unsent: Unconfirmed::default(),
        in_flight: HashMap::new(),
        confirming: HashMap::new(),
        queued: HashMap::new(),
        snapshots: 0,
        link: Default::default(),
        queue_rows: Vec::new(),
        stopping: HashSet::new(),
        queue: HashMap::new(),
        queue_working: HashSet::new(),
        queue_fetching: HashMap::new(),
        queue_gen: HashMap::new(),
        queue_epoch: 0,
        queue_note: HashMap::new(),
        queue_taking: Vec::new(),
        crew: Default::default(),
        dock: Default::default(),
        cover: Default::default(),
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
        asks: Default::default(),
        requests_at: HashMap::new(),
        tab: Tab::Conversation,
        work_pick: HashMap::new(),
        diff: rune_kit::diff::DiffState::new(Vec::new()),
        diff_key: (String::new(), usize::MAX),
        ov: Over::new(live, persist),
        menus: ui::session_menu::Menus::new(),
        limits: ui::limit::Limits::new(),
        accounts: ui::accounts::Accounts::new(),
        prs: ui::pr_link::Prs::new(),
        turn: ui::turn_options::Turns::default(),
        redeliver: HashMap::new(),
        inspect: ui::inspect::Inspect::new(),
        native_queue: ui::native_queue::NativeQueue::new(),
        roles: ui::role::Roles::new(),
        delivery: ui::delivery::Delivery::default(),
        readies: ui::ready::Readies::default(),
        earlier: over::earlier::Earlier::default(),
        new_session: None,
        bar: menubar::Bar::default(),
        attach: ui::attach::Attach::default(),
        complete: ui::complete::Complete::default(),
        paste: None,
        notices: Default::default(),
        holder_menu: None,
        revealed: None,
        missing: None,
        notify: notify::State::default(),
        last_row: None,
        client: None,
        overview_poke: None,
        fixtures: None,
        inbox,
        outbox,
        status: "connecting".into(),
        clock: 0.0,
        panes_moved: false,
        built_at: 0.0,
        #[cfg(test)]
        rebuilds: 0,
    }
}

fn init() -> App {
    let fixture = arg("--fixture");
    let fixtures = arg("--fixtures").or_else(|| std::env::var("AGENTHAIL_FIXTURES").ok());
    let recording = fixtures.as_ref().map(|dir| format!("{dir}/snapshot.json")).or_else(|| fixture.clone());
    let (live, world_error) = match recording.as_deref().map(load_recorded) {
        Some(Ok(raw)) => (Overview::recorded(raw), None),
        Some(Err(error)) => (Overview::default(), Some(error)),
        None => (Overview::default(), None),
    };
    let mut app = empty(live, recording.is_none());
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
        ui::connect::start(&mut app);
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
const REPLAY_QUIET_SECS: f64 = 1.5;
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
    feed::search::lookup(s.sessions.iter().find(|x| x.id == id), over::search::pinned(s, id), || ui::subagents::fetched(s, id))
}

pub fn select(s: &mut App, id: &str) {
    link::moved();
    let id = &feed::composer::current(&s.rekeyed, id).to_string();
    s.selected = Some(id.to_string());
    s.screen = Screen::Session;
    s.tab = Tab::Conversation;
    switch_composer(s);
    s.composer.focused = true;
    load_pane(s, id);
    if let Some(work) = session(s, id).and_then(|x| x.primary_work(s.work_pick.get(id)).cloned()) {
        fetch_item(s, &work.key());
    }
    fetch_requests(s, id);
    ui::session_menu::load(s, id);
    ui::queue::fetch(s, id);
    ui::subagents::load(s, id);
    ui::inspect::load(s, id);
    ui::native_queue::load(s, id);
    ui::role::load(s, id);
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
    over::jump::keep_highlight(s, |s| {
        let panes: Vec<(&str, &[feed::Item])> = s.panes.iter().map(|(id, p)| (id.as_str(), p.transcript.items())).collect();
        s.ov.rebuild(&panes);
    });
    settings::sessions_changed(&s.sessions, s.ov.needs.len());
    s.panes_moved = false;
    s.built_at = s.clock;
    #[cfg(test)]
    {
        s.rebuilds += 1;
    }
}

fn fetch_tails(s: &mut App) {
    let Some(client) = s.client.clone() else { return };
    let open: HashSet<String> = s.panes.keys().cloned().collect();
    for (id, last_active, at) in s.ov.live.due_tails(&open) {
        let (tx, client) = (s.outbox.clone(), client.clone());
        std::thread::spawn(move || {
            let read = std::panic::AssertUnwindSafe(|| tails::fetch(&client, &id, tails::READ_BUDGET, at));
            let (result, at) = std::panic::catch_unwind(read).unwrap_or_else(|_| (Err("the read stopped".into()), None));
            let _ = tx.send(Bg::Tail(id, last_active, result, at));
        });
    }
}

pub fn fetch_ledger(s: &mut App, project: &str) {
    let ask = if s.ov.live.wants(project) { Ask::Read(project.to_string()) } else { Ask::Open(project.to_string()) };
    ask_overview(s, ask);
}

fn selected_changed(s: &mut App, id: &str) {
    ui::gone::changed(s, id);
    fetch_requests(s, id);
    if ui::queue::wants(s, id) {
        ui::queue::fetch(s, id);
    }
    ui::subagents::refresh(s, id);
    ui::native_queue::changed(s, id);
    if let Some(work) = session(s, id).and_then(|x| x.primary_work(s.work_pick.get(id)).cloned()) {
        fetch_item(s, &work.key());
    }
}

pub fn ask_overview(s: &App, ask: Ask) {
    if let Some(poke) = &s.overview_poke {
        let _ = poke.send(ask);
    }
}

fn changed_elsewhere(s: &mut App, wants: feed::watch::Wants) {
    if wants.reset {
        return reopened(s);
    }
    if wants.snapshot {
        ui::connect::changed(s);
        ui::delivery::stale(s);
    }
    if wants.overview_unless(|id| s.ov.live.holds_ledger_of(id)) {
        ask_overview(s, Ask::Refresh);
    }
    if wants.hosts {
        over::inbox::changed(s);
    }
    if wants.extensions {
        over::extensions::changed(s);
    }
    if let Some(id) = wants.session
        && s.selected.as_deref() == Some(id.as_str())
    {
        selected_changed(s, &id);
        selected_moved(s, &id);
    }
}

fn selected_moved(s: &mut App, id: &str) {
    ui::role::refresh(s, id);
    ui::session_menu::agent_mode_changed(s, id);
}

fn reopened(s: &mut App) {
    let clock = s.clock;
    s.redeliver.values_mut().for_each(|b| b.wake(clock));
    over::work::reopened(s);
    ui::connect::retry(s);
    ui::delivery::stale(s);
    over::earlier::reopened(s);
    over::listing::reopened(s);
    ask_overview(s, Ask::Reset);
    over::inbox::reopened(s);
    over::extensions::reopened(s);
    if let Some(id) = s.selected.clone() {
        selected_changed(s, &id);
        selected_moved(s, &id);
    }
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

pub fn open_settings(url: &str) {
    if url.starts_with("x-apple.systempreferences:") {
        let _ = std::process::Command::new("open").arg(url).spawn();
    }
}

pub fn draft_reply(s: &mut App, reply: &str) {
    let draft = ui::paste::draft(s);
    let draft = draft.trim_end();
    let text = if draft.is_empty() { reply.to_string() } else { format!("{draft}\n{reply}") };
    ui::paste::set_composer(s, &text);
    s.composer.focused = true;
}

pub fn answer_request(s: &mut App, request: &str, response: feed::request::Response) {
    settle_request(s, request, move |client, id, request| client.answer(id, request, &response));
}

pub fn withdraw_request(s: &mut App, request: &str) {
    settle_request(s, request, |client, id, request| client.withdraw_request(id, request));
}

fn settle_request(s: &mut App, request: &str, run: impl FnOnce(&Client, &str, &str) -> Result<(), feed::catalog::RespondError> + Send + 'static) {
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
        let result = run(&client, &id, &request);
        let _ = out.send(Bg::Answered(id, request, result));
    });
}

pub fn pick_work(s: &mut App, item: &WorkKey) {
    let Some(id) = s.selected.clone() else { return };
    s.work_pick.insert(id.clone(), item.clone());
    if session(s, &id).is_some_and(|x| x.held_work.iter().any(|w| w.key() == *item)) {
        fetch_item(s, item);
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
    ui::paste::set_composer(s, &message);
    send(s, tx);
}

fn fetch_item(s: &mut App, key: &WorkKey) {
    if s.items.contains_key(key) {
        return;
    }
    if let Some(f) = &s.fixtures {
        if let Ok(text) = std::fs::read_to_string(format!("{}/items/{}.json", f.dir, key.id)) {
            if let Ok(value) = serde_json::from_str(&text) {
                s.items.insert(key.clone(), value);
            }
        }
        return;
    }
    if let Some(client) = s.client.clone() {
        let (tx, item) = (s.outbox.clone(), key.clone());
        std::thread::spawn(move || {
            if let Ok(work) = client.work_item(&item.ledger, &item.id) {
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
    if closed(s, id) {
        return;
    }
    if !ui::complete::run(s, id, tx) {
        send_with(s, id, None, tx);
    }
}

pub fn closed(s: &App, id: &str) -> bool {
    session(s, id).is_some_and(|x| feed::notice::read_only(x).is_some())
}

pub fn steer_text(s: &mut App, id: &str, text: String, tx: &mut Tx) {
    if closed(s, id) {
        return;
    }
    ui::paste::set_composer(s, &text);
    send_with(s, id, Some(Busy::Steer), tx);
}

fn send_with(s: &mut App, id: &str, busy: Option<Busy>, tx: &mut Tx) {
    ui::paste::finish(s);
    let draft = ui::paste::draft(s).trim().to_string();
    let message = ui::paste::expand(&draft).trim().to_string();
    if message.is_empty() && !ui::attach::has(s, id) {
        return;
    }
    if s.client.is_none() {
        note(s, id, "Fixture mode: not sent".into());
        return;
    }
    let attachments = match ui::attach::ready(s, id) {
        Ok(attachments) => attachments,
        Err(hold) => {
            note(s, id, hold);
            return;
        }
    };
    let turn = match ui::turn_options::options(s, id) {
        Ok(turn) => turn,
        Err(problem) => {
            note(s, id, problem.into());
            return;
        }
    };
    let refusal = session(s, id).and_then(|x| feed::attach::steer_refusal(&attachments, &x.capabilities)).or(turn.as_ref().map(|_| feed::composer::STEER_TURN_OPTIONS));
    let busy = match (busy.or_else(|| send_busy(s, id)), refusal) {
        (Some(Busy::Steer), Some(why)) => {
            note(s, id, why.into());
            Some(Busy::Queue)
        }
        _ => busy,
    };
    ui::paste::set_composer(s, "");
    ui::attach::clear(s, id);
    ui::complete::sending(s, id);
    if waiting_earlier(s, id) || sending(s, id) {
        let mut pending = fresh(s, id, message, attachments);
        pending.draft = Some(draft).filter(|d| *d != pending.text);
        pending.busy = busy.or(pending.busy);
        pending.turn = turn;
        hold(s, id, pending);
        return;
    }
    match s.unsent.take_same(id, &message, &attachments) {
        Some(again) if again.reuses_key() => {
            if again.turn != turn || !same_delivery(&again, busy, busy.or_else(|| send_busy(s, id))) {
                note(s, id, "Sent again just as it first went, since it may already have arrived".into());
            }
            dispatch(s, id, again, true, tx);
        }
        _ => {
            let mut pending = fresh(s, id, message, attachments);
            pending.draft = Some(draft).filter(|d| *d != pending.text);
            pending.busy = busy.or(pending.busy);
            pending.turn = turn;
            dispatch(s, id, pending, false, tx);
        }
    }
}

fn hold(s: &mut App, id: &str, pending: Unsent) {
    let held = Unsent { maybe_delivered: false, error: "an earlier message isn't confirmed yet".into(), ..pending };
    s.unsent.add(id, held.clone());
    if let Err(error) = over::layout::save_now(s) {
        let kept = Unsent { unanswered: false, error: format!("the message couldn't be saved first ({error})"), ..held };
        put_back(s, id, &kept);
        s.unsent.add(id, kept);
        over::layout::save(s);
    }
}

fn waiting_earlier(s: &App, id: &str) -> bool {
    let now = feed::composer::now_ms();
    s.unsent.list(id).iter().any(|u| u.redelivering(now))
}

fn clear_sent(s: &mut App, id: &str, unsent: &Unsent) {
    let chips = s.attach.tray.chips(id).len();
    let same = ui::attach::adding(s, id) == 0 && ui::paste::expand(&shown_text(s, id)).trim() == unsent.text && chips == unsent.attachments.len() && s.attach.tray.ready(id).is_ok_and(|now| unsent.same(&unsent.text, &now));
    if !same {
        return;
    }
    if s.composer_for.as_deref() == Some(id) {
        ui::paste::set_composer(s, "");
        s.composer.focused = true;
    } else {
        s.drafts.set(id, "");
    }
    ui::attach::clear(s, id);
}

pub fn can_retry(s: &App, id: &str) -> bool {
    !closed(s, id) && s.unsent.latest(id).is_some_and(|u| feed::attach::missing_upload(&u.error).is_none())
}

pub fn retry(s: &mut App, id: &str, tx: &mut Tx) {
    let now = feed::composer::now_ms();
    let oldest = s.unsent.list(id).iter().find(|u| u.redelivering(now)).or(s.unsent.latest(id)).map(|u| (u.key.clone(), u.redelivering(now)));
    if let Some((key, auto)) = oldest {
        retry_key(s, id, &key, auto, tx);
    }
}

fn retry_key(s: &mut App, id: &str, key: &str, auto: bool, tx: &mut Tx) {
    let Some(unsent) = s.unsent.list(id).iter().find(|u| u.key == key).cloned() else { return };
    if sending(s, id) || s.client.is_none() || closed(s, id) || feed::attach::missing_upload(&unsent.error).is_some() {
        return;
    }
    if !auto {
        clear_sent(s, id, &unsent);
    }
    s.unsent.remove(id, &unsent.key);
    if unsent.reuses_key() {
        dispatch(s, id, unsent, true, tx);
    } else {
        let mut pending = Unsent { draft: unsent.draft.clone(), turn: unsent.turn.clone(), ..fresh(s, id, unsent.text, unsent.attachments) };
        pending.busy = if auto { unsent.busy } else { steered(unsent.busy, pending.busy) };
        if pending.turn.is_some() && pending.busy == Some(Busy::Steer) {
            pending.busy = Some(Busy::Queue);
        }
        if auto {
            pending.sent_at = unsent.sent_at;
            pending.order = unsent.order;
            pending.pin = unsent.pin;
        }
        dispatch(s, id, pending, false, tx);
    }
}

fn redeliver(s: &mut App, tx: &mut Tx) {
    let now = feed::composer::now_ms();
    let expired: Vec<(String, Unsent)> = s.unsent.iter().filter(|(_, u)| u.unanswered && u.sent_at > 0 && !u.redelivering(now)).map(|(id, u)| (id.clone(), u.clone())).collect();
    for (id, unsent) in expired {
        let stopped = Unsent { unanswered: false, ..unsent };
        s.unsent.remove(&id, &stopped.key);
        put_back(s, &id, &stopped);
        s.unsent.add(&id, stopped);
    }
    let mut due: Vec<(String, String)> = Vec::new();
    for (id, unsent) in s.unsent.iter().filter(|(_, u)| u.redelivering(now) && feed::attach::missing_upload(&u.error).is_none()) {
        if !due.iter().any(|(seen, _)| seen == id) {
            due.push((id.clone(), unsent.key.clone()));
        }
    }
    let flying: Vec<String> = s.in_flight.keys().cloned().collect();
    s.redeliver.retain(|id, _| due.iter().any(|(d, _)| d == id) || flying.contains(id));
    if s.client.is_none() {
        return;
    }
    for (id, key) in due {
        if sending(s, &id) || closed(s, &id) {
            continue;
        }
        let clock = s.clock;
        if s.redeliver.entry(id.clone()).or_insert_with(|| feed::composer::Backoff::starting(clock)).due <= clock {
            retry_key(s, &id, &key, true, tx);
        }
    }
}

fn same_delivery(again: &Unsent, asked: Option<Busy>, now: Option<Busy>) -> bool {
    asked.is_none_or(|b| again.busy == Some(b)) && (now.is_none() || (again.busy == Some(Busy::Steer)) == (now == Some(Busy::Steer)))
}

fn steered(was: Option<Busy>, now: Option<Busy>) -> Option<Busy> {
    if was == Some(Busy::Steer) { was } else { now }
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
    if unsent.busy == Some(Busy::Steer) {
        s.busy_pick.insert(id.to_string(), Busy::Steer);
    }
    let merged = feed::composer::put_back(&shown_text(s, id), &ui::paste::unsent_draft(&unsent));
    if s.composer_for.as_deref() == Some(id) {
        ui::paste::set_composer(s, &merged);
        s.composer.focused = true;
    }
    s.drafts.set(id, &merged);
    ui::attach::restore(s, id, &unsent);
    over::layout::save(s);
}

pub fn unsent_held(s: &App, id: &str) -> bool {
    s.unsent.latest(id).is_some_and(|u| !shows(s, id, u))
}

fn shows(s: &App, id: &str, unsent: &Unsent) -> bool {
    let attached = s.attach.tray.ready(id).unwrap_or_default();
    ui::paste::expand(&shown_text(s, id)).trim() == unsent.text && unsent.attachments.iter().all(|a| attached.iter().any(|b| b.id == a.id))
}

pub fn offers_new_send(s: &App, id: &str) -> bool {
    can_retry(s, id) && s.unsent.latest(id).is_some_and(|u| u.offers_new_send())
}

pub fn send_as_new(s: &mut App, id: &str, tx: &mut Tx) {
    let Some(unsent) = s.unsent.latest(id).cloned().filter(|u| u.offers_new_send()) else { return };
    if sending(s, id) || s.client.is_none() || !confirmed(s, id, Confirm::SendNew, true) {
        return;
    }
    clear_sent(s, id, &unsent);
    s.unsent.remove(id, &unsent.key);
    let mut pending = Unsent { draft: unsent.draft.clone(), turn: unsent.turn.clone(), ..fresh(s, id, unsent.text, unsent.attachments) };
    pending.busy = steered(unsent.busy, pending.busy);
    if pending.turn.is_some() && pending.busy == Some(Busy::Steer) {
        note(s, id, feed::composer::STEER_TURN_OPTIONS.into());
        pending.busy = Some(Busy::Queue);
    }
    if waiting_earlier(s, id) {
        hold(s, id, pending);
        return;
    }
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

fn fresh(s: &App, id: &str, text: String, attachments: Vec<feed::attach::Upload>) -> Unsent {
    Unsent {
        session: id.to_string(),
        text,
        attachments,
        key: feed::catalog::request_key(),
        busy: send_busy(s, id),
        maybe_delivered: true,
        error: "the app closed before the daemon answered".into(),
        tries: 0,
        pin: feed::holders::pins().get(id),
        draft: None,
        turn: None,
        sent_at: feed::composer::now_ms(),
        order: feed::composer::send_stamp(feed::composer::now_ms()),
        unanswered: true,
    }
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
        s.drafts.set(&id, &ui::paste::draft(s));
    }
    if let Err(error) = over::layout::save_now(s) {
        settle(s, &id, &pending.key);
        put_back(s, &id, &pending);
        let maybe_delivered = resent && pending.maybe_delivered;
        s.unsent.add(&id, Unsent { maybe_delivered, unanswered: false, error: format!("the message couldn't be saved first ({error})"), ..pending });
        over::layout::save(s);
        return;
    }
    tx.scroll_to_bottom(format!("transcript-{id}"));
    let out = s.outbox.clone();
    std::thread::spawn(move || {
        let result = client.send_receipt(&pending.session, &pending.text, &pending.ids(), pending.busy, pending.turn.as_ref(), pending.pin, &pending.key);
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

pub(crate) fn note(s: &mut App, id: &str, text: String) {
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
        && !shows(s, id, latest)
    {
        parts.push(latest.quoted());
    }
    let now = feed::composer::now_ms();
    let waiting: Vec<&Unsent> = s.unsent.list(id).iter().filter(|u| u.redelivering(now)).collect();
    match waiting.as_slice() {
        [] => parts.extend(s.unsent.note(id)),
        [first] => parts.push(format!("Not confirmed yet: {}. Sending again automatically.", first.error)),
        [first, ..] => parts.push(format!("{} messages waiting. Not confirmed yet: {}. Sending them in order automatically.", waiting.len(), first.error)),
    }
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

fn shown_text(s: &App, id: &str) -> String {
    if s.composer_for.as_deref() == Some(id) { ui::paste::draft(s) } else { s.drafts.get(id).to_string() }
}

fn put_back(s: &mut App, id: &str, unsent: &Unsent) {
    if !shown_text(s, id).trim().is_empty() || ui::attach::has(s, id) {
        return;
    }
    ui::attach::restore(s, id, unsent);
    if unsent.busy == Some(Busy::Steer) {
        s.busy_pick.insert(id.to_string(), Busy::Steer);
    }
    let text = ui::paste::unsent_draft(unsent);
    if s.composer_for.as_deref() == Some(id) {
        ui::paste::set_composer(s, &text);
    } else {
        s.drafts.set(id, &text);
    }
}

fn take_sent(s: &mut App, id: String, pending: Unsent, retry: bool, result: Result<(Delivered, serde_json::Value), feed::catalog::RespondError>) {
    let id = feed::composer::current(&s.rekeyed, &id).to_string();
    let (result, reply) = match result {
        Ok((delivered, reply)) => (Ok(delivered), reply),
        Err(error) => (Err(error), serde_json::Value::Null),
    };
    settle(s, &id, &pending.key);
    s.unsent.remove(&id, &pending.key);
    let failure = match result {
        Ok(Delivered::Unconfirmed) => Some((true, true, "the daemon took it but hasn't confirmed delivery".to_string(), false)),
        Ok(receipt @ (Delivered::Queued(_) | Delivered::QueuedUntilAnswer(_))) => {
            s.status_line.remove(&id);
            s.queued.entry(id.clone()).or_default().push((s.snapshots, receipt));
            ui::connect::confirm(s);
            None
        }
        Ok(delivered) => {
            note(s, &id, feed::composer::receipt_line(&delivered.note(0), &reply));
            ui::complete::delivered(s, &id, &pending.text);
            None
        }
        Err(error) => Some((error.maybe_delivered, error.answered, error.message.clone(), feed::composer::redelivers(&error))),
    };
    let clock = s.clock;
    match failure {
        Some((maybe_delivered, answered, error, again)) => {
            let failed = Unsent { unanswered: again, ..pending.failed(retry, maybe_delivered, answered, error) };
            if failed.redelivering(feed::composer::now_ms()) {
                s.redeliver.entry(id.clone()).or_insert_with(|| feed::composer::Backoff::starting(clock)).failed(clock);
                s.unsent.add(&id, failed);
            } else {
                answered_now(s, &id, clock);
                put_back(s, &id, &failed);
                s.unsent.add(&id, failed);
            }
        }
        None => answered_now(s, &id, clock),
    }
}

fn answered_now(s: &mut App, id: &str, clock: f64) {
    s.redeliver.insert(id.to_string(), feed::composer::Backoff::starting(clock));
}

pub fn switch_composer(s: &mut App) {
    let target = over::composer_target(s);
    if target == s.composer_for {
        return;
    }
    ui::paste::finish(s);
    let carried = match s.composer_for.take() {
        Some(old) => {
            s.drafts.set(&old, &ui::paste::draft(s));
            String::new()
        }
        None => ui::paste::draft(s).trim().to_string(),
    };
    let draft = target.as_deref().map(|id| s.drafts.get(id).to_string()).unwrap_or_default();
    ui::paste::set_composer(s, &if carried.is_empty() { draft } else { feed::composer::put_back(&draft, &carried) });
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
    ui::complete::leave_all_history(s);
    if let Some(id) = s.composer_for.clone() {
        s.drafts.set(&id, &ui::paste::draft(s));
    }
    let now = feed::composer::now_ms();
    let mut restored: Vec<(String, String)> = s.unsent.iter().filter(|(id, u)| !u.redelivering(now) && ui::paste::expand(s.drafts.get(id)).trim() == u.text).map(|(id, u)| (id.clone(), ui::paste::unsent_draft(u))).collect();
    restored.dedup_by(|a, b| a.0 == b.0);
    for (id, _) in &restored {
        s.drafts.set(id, "");
    }
    s.drafts.rekey(&s.rekeyed);
    s.attach.tray.rekey(&s.rekeyed);
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
        ui::attach::rekey(s, &old, &new);
        ui::complete::rekey(s, &old, &new);
        move_key(&mut s.status_line, &old, &new);
        move_key(&mut s.busy_pick, &old, &new);
        move_key(&mut s.redeliver, &old, &new);
        ui::turn_options::rekey(s, &old, &new);
        move_key(&mut s.work_pick, &old, &new);
        move_member(&mut s.stopping, &old, &new);
        move_key(&mut s.queue_note, &old, &new);
        move_key(&mut s.queue, &old, &new);
        s.queue_fetching.remove(&old);
        ui::queue::invalidate(s, &new);
    }
    if let Some(id) = s.composer_for.clone()
        && s.drafts.get(&id) != ui::paste::draft(s)
    {
        let draft = s.drafts.get(&id).to_string();
        ui::paste::set_composer(s, &draft);
    }
}

fn poll(s: &mut App, t: f64, tx: &mut Tx) -> bool {
    s.clock = t;
    let mut changed = false;
    let mut arrived = false;
    while let Ok(message) = s.inbox.try_recv() {
        changed = true;
        match message {
            Bg::Snapshot(at, epoch, seq, sessions, touched) => {
                if s.ov.live.take_sessions(&at, &epoch, seq, sessions) && s.ov.live.stale() {
                    s.sessions = s.ov.live.sessions().to_vec();
                    s.notify.sessions_moved = true;
                    feed::holders::pins().prune(&s.sessions);
                    fetch_tails(s);
                    if s.ov.live.misses_a_ledger() {
                        ask_overview(s, Ask::Refresh);
                    }
                }
                s.now = OffsetDateTime::now_utc();
                s.status = "live".into();
                ui::connect::up(s);
                link::listed(s);
                if let Some(id) = s.selected.clone()
                    && touched.as_ref().is_none_or(|t| t.contains(&id))
                {
                    selected_changed(s, &id);
                    if touched.is_some() {
                        selected_moved(s, &id);
                    }
                }
            }
            Bg::Changed(wants) => changed_elsewhere(s, wants),
            Bg::Reopened => reopened(s),
            Bg::LedgerChanged(key, at) => {
                ui::ready::ledger_changed(s, &key);
                over::worksearch::ledger_changed(s, Some(&key));
                ask_overview(s, Ask::Ledger(key, at));
            }
            Bg::LedgersReopened => {
                ui::ready::reopened(s);
                over::worksearch::ledger_changed(s, None);
                ask_overview(s, Ask::Reopened);
            }
            Bg::SessionNews => {
                ask_overview(s, Ask::Refresh);
                over::listing::news(s);
            }
            Bg::SessionsMoved => over::listing::news(s),
            Bg::Removed(gone) => {
                over::earlier::removed(s, &gone);
                over::listing::removed(s, &gone);
            }
            Bg::Item(id, item) => {
                s.items.insert(id, item);
            }
            Bg::Overview(fetched) => {
                s.earlier.activity_missing = fetched.recorded.activity_missing;
                ui::ready::moved(s, &fetched.moved);
                s.ov.live.refresh(*fetched);
                s.sessions = s.ov.live.sessions().to_vec();
                s.notify.sessions_moved = true;
                feed::holders::pins().prune(&s.sessions);
                fetch_tails(s);
                arrived = true;
            }
            Bg::Tail(id, last_active, result, at) => {
                s.ov.live.take_tail(&id, last_active, result, at);
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
                let mut left = Vec::new();
                for (id, receipts) in s.queued.iter_mut() {
                    let before = receipts.len();
                    receipts.retain(|(at, receipt)| feed::composer::queued_note(rows, keys, id, receipt, seen >= at + 2).is_some());
                    if receipts.len() < before {
                        left.push(id.clone());
                    }
                }
                s.queued.retain(|_, receipts| !receipts.is_empty());
                for id in left {
                    ui::complete::refresh(s, &id);
                }
            }
            Bg::DecisionsRev(rev) => over::inbox::rev_seen(s, &rev),
            Bg::LeadReplacements(list) => ui::notice::take_replacements(s, list),
            Bg::Notice(done) => ui::notice::take(s, done),
            Bg::Link(found) => link::found(s, found),
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
                if let Some(client) = &s.client {
                    over::inbox::refresh(client, &s.ov.inbox);
                }
            }
            Bg::NewSession(loaded) => ui::new_session::take(s, loaded),
            Bg::Queue(id, asked, result) => ui::queue::take(s, id, asked, result),
            Bg::Crew(done) => ui::subagents::done(s, done),
            Bg::QueueDone(id, queue_id, done) => ui::queue::done(s, id, queue_id, done),
            Bg::Recovered(back) => ui::queue::restore(s, back),
            Bg::Older(id, generation, result) => history::take_older(s, id, generation, result),
            Bg::Body(id, generation, item, version, result) => history::take_body(s, id, generation, item, version, result),
            Bg::Attach(done) => ui::attach::done(s, done, tx),
            Bg::Complete(done) => ui::complete::done(s, done),
            Bg::Down(problem) => ui::connect::down(s, problem),
            Bg::Started(result) => ui::connect::started(s, result),
            Bg::Looked(id, seq, result) => ui::gone::looked(s, &id, seq, result),
            Bg::Fixed(id, seq, fix, result) => ui::gone::fixed(s, &id, seq, fix, result),
        }
    }
    let mut busy = false;
    let mut counter = s.counter;
    let mut panes_moved = false;
    let mut caught: Vec<String> = Vec::new();
    let mut streamed: Vec<(String, feed::wire::Streamed)> = Vec::new();
    for (id, pane) in s.panes.iter_mut() {
        let mut moved = false;
        if let Some(replay) = &mut pane.replay {
            let (c, more) = replay.pump(t, &mut pane.transcript);
            moved |= c;
            busy |= more;
            if !more {
                pane.status = "replay finished".into();
            }
        }
        let arrived: Vec<Message> = pane.live.as_ref().map(|rx| rx.try_iter().collect()).unwrap_or_default();
        if pane.live.is_some() {
            busy = true;
            for message in arrived {
                match message {
                    Message::Batch(b) => {
                        streamed.extend(feed::inspect::streamed(&b.events).into_iter().map(|x| (id.clone(), x)));
                        history::batch_arrived(pane, t);
                        moved |= pane.transcript.apply(b.seq, b.events);
                    }
                    Message::Status(status) => {
                        if status == "live" && !pane.caught_up {
                            pane.last_batch = pane.last_batch.min(t);
                        }
                        if status.starts_with("reconnecting") && !pane.caught_up && pane.transcript.items().is_empty() && !caught.contains(id) {
                            caught.push(id.clone());
                        }
                        pane.status = status;
                    }
                    Message::CaughtUp => caught.push(id.clone()),
                }
            }
            if !pane.caught_up && t - pane.last_batch >= REPLAY_QUIET_SECS && !caught.contains(id) {
                caught.push(id.clone());
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
    for id in caught {
        history::caught_up(s, &id);
    }
    changed |= !streamed.is_empty();
    for (id, x) in streamed {
        ui::inspect::streamed(s, &id, x);
    }
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
        && s.ov.sidebar.is_open()
        && let Some(id) = s.selected.clone()
        && (s.ov.d.session(&id).is_some() || ui::gone::shown(s, &id).is_some())
    {
        tx.scroll_into_view_in("sidebar-scroll", over::ui::session_key(&id));
        s.revealed = Some(id);
    }
    voice::share(s.built_at, &s.ov.d);
    if s.ov.live.follow_tail(voice::operator()) {
        fetch_tails(s);
    }
    if let Some(id) = voice::take_opened_session() {
        menubar::front(s);
        over::open_session(s, &id);
        changed = true;
    }
    changed |= ui::session_menu::drain(s);
    changed |= over::inbox::drain(s);
    changed |= over::work::drain(s);
    changed |= over::search::drain(s);
    changed |= over::worksearch::drain(s);
    changed |= notify::tick(s);
    changed |= ui::gone::watch(s);
    changed |= ui::limit::drain(s) | ui::accounts::drain(s) | ui::pr_link::drain(s) | ui::inspect::drain(s);
    changed |= ui::role::drain(s);
    changed |= ui::native_queue::drain(s);
    changed |= ui::attach::pump(s);
    changed |= ui::text_size::pump(s);
    redeliver(s, tx);
    changed |= transcript::take_behind_moved();
    changed |= over::extensions::drain(s);
    changed |= over::projects::drain(s);
    changed |= ui::delivery::poll(s) | ui::ready::poll(s) | over::earlier::poll(s) | over::listing::poll(s);
    if s.ov.live.tail_retry_due() {
        fetch_tails(s);
    }
    changed |= ui::complete::pump(s);
    changed |= look::poll(look::Window::Main, tx);
    ui::paste::settle(s);
    changed |= ui::notice::poll(s);
    switch_composer(s);
    if let Some(id) = s.composer_for.clone() {
        s.drafts.set(&id, &ui::paste::draft(s));
    }
    over::layout::save(s);
    if changed || !s.bar.is_shown() {
        menubar::refresh(s);
    }
    menubar::sync_keys(s);
    menubar::warn(s);
    menubar::badge(s);
    if let Some(due) = over::worksearch::wait(s) {
        s.wake.after(tx, due);
    }
    s.wake.after(tx, Duration::from_millis(if busy { 16 } else { 250 }));
    changed
}

fn on_key(s: &mut App, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if over::note_open(s) {
        return over::on_key(s, ev, tx);
    }
    if ev.is_command(',') {
        settings::open();
        return true;
    }
    (!over::projects::open(s) && ui::new_session::on_key(s, ev, tx)) || over::on_key(s, ev, tx)
}

fn on_quit(s: &mut App, _: &mut Tx) {
    ui::paste::finish(s);
    if let Some(id) = s.composer_for.clone() {
        s.drafts.set(&id, &ui::paste::draft(s));
    }
    if let Err(error) = over::layout::save_now(s) {
        eprintln!("agenthail: couldn't save before quitting: {error}");
    }
}

fn main() {
    if args().iter().any(|a| a == "--voice") {
        voice::run_alone();
        return;
    }
    if args().iter().any(|a| a == "--link-scheme") {
        println!("{}", link::SCHEME);
        return;
    }
    rune::url_schemes([link::SCHEME]);
    if !rune::single_instance(link::INSTANCE) || !instance::only_copy(link::LOCK) {
        return;
    }
    let mut app = rune::App::new(AppSpec::<App> {
        poll: Some(poll),
        on_key: Some(on_key),
        setup: Some(ui::attach::setup),
        fonts: rune_theme_opencode::FONTS,
        families: rune_theme_opencode::FAMILIES,
        min_size: Some((1100.0, 700.0)),
        ..AppSpec::new("Agenthail", (1440.0, 900.0), init, ui::view)
    })
        .chrome(Chrome { full_size_content: true, traffic_lights: Some((20.0, 20.0)), drag_regions: true })
        .window_options(WindowOptions { transparent: true, resizable: true, visible: true, ..Default::default() })
        .srgb_blend(true)
        .on_quit(on_quit)
        .on_open_url(link::on_open_url);
    let mut prefs = settings::spec();
    prefs.options = WindowOptions { visible: false, resizable: true, ..Default::default() };
    let (handle, window) = app.window(prefs);
    settings::keep(handle, window);
    menubar::install(menubar::with_menus(app)).run();
}
