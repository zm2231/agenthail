mod appearance;
mod claude;
mod devices;
mod general;
mod handoffs;
mod notify;
mod parts;

use std::cell::{Cell, OnceCell};
use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Duration;

use feed::catalog::Client;
use feed::prefs::{Base, Compaction, CompactDraft, Failure, Notify, Person, Preference, RemoteControl, Settings};
use rune::{AppHandle, Cx, El, KeyEvent, TextEdit, Tx, WindowHandle, col, el};
use rune_kit::SegmentedControl;

use crate::theme;

#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug)]
pub enum Read {
    Base,
    Settings,
    Remote,
    Compaction,
    Preferences,
    Devices,
}

const ALL: [Read; 6] = [Read::Base, Read::Settings, Read::Remote, Read::Compaction, Read::Preferences, Read::Devices];

enum Got {
    Base(Result<Base, Failure>),
    Settings(Result<Settings, Failure>),
    Remote(Result<RemoteControl, Failure>),
    Compaction(Result<Compaction, Failure>),
    Preferences(Result<Vec<Preference>, Failure>),
    Devices(Result<Vec<feed::devices::Device>, Failure>),
}

#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug)]
pub enum Section {
    General,
    Remote,
    Compaction,
    Notify,
    Devices,
    Relays,
    Channels,
}

enum Done {
    Read(Read, u64, Got),
    Wrote(String, Section, Result<(), Failure>, &'static [Read]),
    Devices(devices::Got),
    Look(appearance::Got),
}

pub struct Prefs {
    client: Option<Client>,
    no_client: String,
    pub draft_orphaned: bool,
    watcher: Arc<AtomicU64>,
    rest_pending: bool,
    external: bool,
    seen: u64,
    rewatch: bool,
    retry_base: Option<std::time::Instant>,
    stream_down: Option<String>,
    pub tab: usize,
    pub base: Option<Result<Base, Failure>>,
    pub settings: Option<Result<Settings, Failure>>,
    pub remote: Option<Result<RemoteControl, Failure>>,
    pub compaction: Option<Result<Compaction, Failure>>,
    pub preferences: Option<Result<Vec<Preference>, Failure>>,
    pub permission: Option<feed::notify::Permission>,
    pub dev: devices::Devices,
    writing: Option<String>,
    settling: HashSet<Read>,
    sent: Option<CompactDraft>,
    tick: u64,
    base_ok_at: u64,
    denied_at: u64,
    pub notes: HashMap<Section, Failure>,
    pub name: TextEdit,
    seeded: Option<Person>,
    pub draft: CompactDraft,
    pub model: TextEdit,
    pub window: TextEdit,
    pub min_tokens: TextEdit,
    pub pre_limit: TextEdit,
    pub saved_notice: bool,
    pub open_select: Option<&'static str>,
    pub holds_open: HashSet<String>,
    pub home: String,
    pub handoffs: handoffs::Form,
    pub appearance: appearance::Form,
    loaded: bool,
    in_flight: usize,
    seq: HashMap<Read, u64>,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

thread_local! {
    static WINDOW: OnceCell<(AppHandle<Prefs>, WindowHandle)> = const { OnceCell::new() };
    static PERMISSION: Cell<Option<feed::notify::Permission>> = const { Cell::new(None) };
}

pub fn permission_changed(permission: feed::notify::Permission) {
    PERMISSION.set(Some(permission));
    WINDOW.with(|w| {
        if let Some((handle, _)) = w.get() {
            handle.update(move |s: &mut Prefs, _| s.permission = Some(permission));
        }
    });
}

pub fn spec() -> rune::WindowSpec<Prefs> {
    rune::WindowSpec::new(
        "settings",
        rune::AppSpec::<Prefs> {
            poll: Some(poll),
            on_key: Some(on_key),
            fonts: rune_theme_opencode::FONTS,
            families: rune_theme_opencode::FAMILIES,
            min_size: Some((620.0, 460.0)),
            ..rune::AppSpec::new("Settings", (720.0, 620.0), init, view)
        },
    )
    .srgb_blend(true)
}

pub fn keep(handle: AppHandle<Prefs>, window: WindowHandle) {
    WINDOW.with(|w| {
        let _ = w.set((handle, window));
    });
}

pub fn open() {
    WINDOW.with(|w| {
        if let Some((handle, window)) = w.get() {
            handle.update(|s: &mut Prefs, _| {
                s.rewatch = true;
                devices::opened(s);
                reload(s);
                appearance::opened(s);
            });
            window.open();
            window.focus();
        }
    });
}

pub fn nudge() {
    WINDOW.with(|w| {
        if let Some((handle, _)) = w.get() {
            handle.update(|s: &mut Prefs, tx| {
                if crate::look::poll(crate::look::Window::Settings, tx) {
                    appearance::changed(s);
                }
            });
        }
    });
}

pub fn open_from_menu(_: &mut crate::App, _: &mut Tx) {
    open();
}

fn visible() -> bool {
    WINDOW.with(|w| w.get().is_some_and(|(handle, _)| handle.is_visible()))
}

fn fixtures() -> bool {
    let args = rune::extra_args();
    args.iter().any(|a| a == "--fixture" || a == "--fixtures") || std::env::var("AGENTHAIL_FIXTURES").is_ok()
}

fn init() -> Prefs {
    let (tx, rx) = channel();
    let (client, no_client) = connect();
    Prefs {
        client,
        no_client,
        permission: PERMISSION.get(),
        draft_orphaned: false,
        watcher: Arc::new(AtomicU64::new(0)),
        rest_pending: false,
        seen: 0,
        rewatch: false,
        retry_base: None,
        stream_down: None,
        external: false,
        tab: 0,
        base: None,
        settings: None,
        remote: None,
        compaction: None,
        preferences: None,
        dev: devices::Devices::default(),
        writing: None,
        settling: HashSet::new(),
        sent: None,
        tick: 0,
        base_ok_at: 0,
        denied_at: 0,
        notes: HashMap::new(),
        name: TextEdit::new(""),
        seeded: None,
        draft: CompactDraft::default(),
        model: TextEdit::new(""),
        window: TextEdit::new(""),
        min_tokens: TextEdit::new(""),
        pre_limit: TextEdit::new(""),
        saved_notice: false,
        open_select: None,
        holds_open: HashSet::new(),
        home: std::env::var("HOME").unwrap_or_default(),
        handoffs: handoffs::Form::default(),
        appearance: appearance::Form::default(),
        loaded: false,
        in_flight: 0,
        seq: HashMap::new(),
        tx,
        rx,
    }
}

fn connect() -> (Option<Client>, String) {
    if fixtures() {
        return (None, "Settings are read from the daemon; this run plays a recording and has no daemon connection.".into());
    }
    match Client::from_env() {
        Ok(client) => (Some(client), String::new()),
        Err(e) => (None, format!("Couldn't reach the daemon's sign-in: {e}")),
    }
}

fn unread(s: &Prefs) -> Failure {
    Failure::new(s.no_client.clone())
}

fn fetch(s: &mut Prefs, what: Read) {
    let Some(client) = s.client.clone() else {
        match what {
            Read::Base => s.base = Some(Err(unread(s))),
            Read::Settings => s.settings = Some(Err(unread(s))),
            Read::Remote => s.remote = Some(Err(unread(s))),
            Read::Compaction => s.compaction = Some(Err(unread(s))),
            Read::Preferences => s.preferences = Some(Err(unread(s))),
            Read::Devices => s.dev.list = Some(Err(unread(s))),
        }
        return;
    };
    let seq = s.seq.entry(what).or_default();
    *seq += 1;
    let seq = *seq;
    s.in_flight += 1;
    let tx = s.tx.clone();
    std::thread::spawn(move || {
        let got = match what {
            Read::Base => Got::Base(client.base_settings()),
            Read::Settings => Got::Settings(client.settings()),
            Read::Remote => Got::Remote(client.remote_control()),
            Read::Compaction => Got::Compaction(client.compaction()),
            Read::Preferences => Got::Preferences(client.notification_preferences()),
            Read::Devices => Got::Devices(client.devices()),
        };
        let _ = tx.send(Done::Read(what, seq, got));
    });
}

pub fn reload(s: &mut Prefs) {
    s.loaded = true;
    let (fresh, why) = connect();
    if fresh.is_some() || s.client.is_none() {
        (s.client, s.no_client) = (fresh, why);
    }
    if s.client.is_none() {
        for what in ALL {
            fetch(s, what);
        }
        return;
    }
    for what in ALL {
        *s.seq.entry(what).or_default() += 1;
    }
    s.settling.extend(ALL);
    s.rest_pending = true;
    fetch(s, Read::Base);
}

fn read_rest(s: &mut Prefs) {
    if !std::mem::take(&mut s.rest_pending) {
        return;
    }
    for what in &ALL[1..] {
        s.settling.insert(*what);
        fetch(s, *what);
    }
}

pub fn write(s: &mut Prefs, key: impl Into<String>, section: Section, then: &'static [Read], job: impl FnOnce(&Client) -> Result<(), Failure> + Send + 'static) {
    let key = key.into();
    let Some(client) = s.client.clone() else {
        let f = unread(s);
        s.notes.insert(section, f);
        return;
    };
    if busy(s) {
        return;
    }
    s.writing = Some(key.clone());
    s.notes.remove(&section);
    if section == Section::Compaction {
        s.saved_notice = false;
    }
    for what in then {
        *s.seq.entry(*what).or_default() += 1;
    }
    s.in_flight += 1;
    let tx = s.tx.clone();
    std::thread::spawn(move || {
        let result = job(&client);
        let _ = tx.send(Done::Wrote(key, section, result, then));
    });
}

pub fn saving(s: &Prefs, key: &str) -> bool {
    s.writing.as_deref() == Some(key)
}

pub fn busy(s: &Prefs) -> bool {
    s.writing.is_some() || !s.settling.is_empty()
}

pub fn send_compaction(s: &mut Prefs, sent: CompactDraft) {
    if busy(s) || s.client.is_none() {
        return;
    }
    let body = sent.body();
    s.sent = Some(sent);
    write(s, "compaction", Section::Compaction, &[Read::Compaction], move |c| c.save_compaction(body).map(drop));
}

pub fn token_verified(s: &Prefs) -> bool {
    matches!(&s.base, Some(Ok(_))) && s.base_ok_at > s.denied_at
}

fn refused(s: &mut Prefs, what: Option<Read>, failed: bool) {
    if failed && what != Some(Read::Base) {
        s.denied_at = s.tick;
        fetch(s, Read::Base);
    }
}

pub fn explain(s: &Prefs, f: &Failure) -> Failure {
    if f.denied && token_verified(s) && !f.scope.is_empty() {
        return Failure { message: feed::prefs::scope_message(f.scope).into(), ..f.clone() };
    }
    f.clone()
}

fn seed_name(s: &mut Prefs) {
    let person = s.base.as_ref().and_then(|b| b.as_ref().ok()).and_then(|b| b.person.clone());
    if person != s.seeded {
        if let Some(p) = &person {
            s.name = TextEdit::new(&feed::prefs::name_field(p));
        }
        s.seeded = person;
    }
}

fn settle_compaction(s: &mut Prefs) {
    let Some(Ok(c)) = &s.compaction else { return };
    let roots: Vec<&str> = c.roots.iter().map(|r| r.root.as_str()).collect();
    let (root, orphaned) = claude::draft(s).retarget(s.draft_orphaned, &roots);
    s.draft.root = root;
    s.draft_orphaned = orphaned;
}

fn denied<T>(r: &Result<T, Failure>) -> bool {
    r.as_ref().is_err_and(|f| f.denied)
}

fn watch(s: &Prefs, from: u64) {
    let Some(client) = s.client.clone() else { return };
    let Some(handle) = WINDOW.with(|w| w.get().map(|(h, _)| h.clone())) else { return };
    let current = s.watcher.clone();
    let mine = current.fetch_add(1, Ordering::AcqRel) + 1;
    std::thread::spawn(move || {
        let mut after = from;
        while current.load(Ordering::Acquire) == mine {
            let up = handle.clone();
            let opened = move || up.update(move |s: &mut Prefs, _| {
                if s.watcher.load(Ordering::Acquire) == mine {
                    s.stream_down = None;
                }
            });
            let result = client.follow_events(after, opened, |kind, id| {
                if current.load(Ordering::Acquire) != mine {
                    return false;
                }
                after = after.max(id);
                if feed::prefs::rereads(kind) && handle.is_visible() {
                    let reset = kind == "stream.reset";
                    handle.update(move |s: &mut Prefs, _| changed_elsewhere(s, id, reset));
                } else if kind == "state.changed" && handle.is_visible() {
                    handle.update(|s: &mut Prefs, _| handoffs::changed(s));
                }
                true
            });
            if current.load(Ordering::Acquire) == mine {
                let why = match result {
                    Err(e) => e.to_string(),
                    Ok(()) => "the daemon closed the stream".into(),
                };
                handle.update(move |s: &mut Prefs, _| {
                    if s.watcher.load(Ordering::Acquire) == mine {
                        s.stream_down = Some(why);
                    }
                });
            }
            std::thread::sleep(Duration::from_secs(2));
            if !handle.is_visible() {
                return;
            }
        }
    });
}

fn changed_elsewhere(s: &mut Prefs, id: u64, reset: bool) {
    if !reset {
        if id <= s.seen {
            return;
        }
        s.seen = id;
    }
    if busy(s) {
        s.external = true;
    } else {
        reload(s);
    }
}

fn take(s: &mut Prefs, done: Done) {
    take_one(s, done);
    if s.external && !busy(s) {
        s.external = false;
        reload(s);
    }
    handoffs::flush(s);
}

fn take_one(s: &mut Prefs, done: Done) {
    s.in_flight = s.in_flight.saturating_sub(1);
    s.tick += 1;
    match done {
        Done::Read(what, seq, got) => {
            if s.seq.get(&what) != Some(&seq) {
                return;
            }
            s.settling.remove(&what);
            let refusal = match got {
                Got::Base(r) => {
                    s.retry_base = r.is_err().then(|| std::time::Instant::now() + Duration::from_secs(10));
                    if let Ok(b) = &r {
                        s.base_ok_at = s.tick;
                        if std::mem::take(&mut s.rewatch) {
                            watch(s, b.event_cursor.max(s.seen));
                            s.rest_pending = true;
                        }
                    }
                    s.base = Some(r);
                    read_rest(s);
                    seed_name(s);
                    false
                }
                Got::Settings(r) => {
                    let d = denied(&r);
                    s.settings = Some(r);
                    devices::settled(s);
                    d
                }
                Got::Remote(r) => {
                    let d = denied(&r);
                    s.remote = Some(r);
                    d
                }
                Got::Compaction(r) => {
                    let d = denied(&r);
                    s.compaction = Some(r);
                    settle_compaction(s);
                    d
                }
                Got::Preferences(r) => {
                    let d = denied(&r);
                    s.preferences = Some(r);
                    d
                }
                Got::Devices(r) => {
                    let d = denied(&r);
                    s.dev.list = Some(r);
                    d
                }
            };
            refused(s, Some(what), refusal);
        }
        Done::Devices(got) => devices::take(s, got),
        Done::Look(got) => appearance::take(s, got),
        Done::Wrote(key, section, result, then) => {
            if s.writing.as_deref() == Some(key.as_str()) {
                s.writing = None;
            }
            let sent = if section == Section::Compaction { s.sent.take() } else { None };
            match result {
                Ok(()) if section == Section::Compaction => {
                    if sent.as_ref() == Some(&claude::draft(s)) {
                        s.draft.saved();
                        for edit in [&mut s.model, &mut s.window, &mut s.min_tokens, &mut s.pre_limit] {
                            *edit = TextEdit::new("");
                        }
                    }
                    s.saved_notice = true;
                }
                Ok(()) => handoffs::saved(s, &key),
                Err(failure) => {
                    let refusal = failure.denied;
                    s.notes.insert(section, failure);
                    refused(s, None, refusal);
                }
            }
            for what in then {
                if s.client.is_some() {
                    s.settling.insert(*what);
                }
                fetch(s, *what);
            }
        }
    }
}

pub fn notify(s: &mut Prefs, what: Notify) {
    write(s, what.action(), Section::Notify, &[Read::Settings], move |c| c.notifications(what).inspect(|_| crate::notify::recheck()));
}

fn poll(s: &mut Prefs, _: f64, tx: &mut Tx) -> bool {
    let mut changed = false;
    if !s.loaded && (visible() || std::env::args().any(|a| a == "--window")) {
        s.rewatch = true;
        reload(s);
        changed = true;
    }
    while let Ok(done) = s.rx.try_recv() {
        take(s, done);
        changed = true;
    }
    changed |= devices::poll(s, tx);
    if crate::look::poll(crate::look::Window::Settings, tx) {
        appearance::changed(s);
        changed = true;
    }
    if let Some(at) = s.retry_base
        && std::time::Instant::now() >= at
        && visible()
    {
        s.retry_base = None;
        fetch(s, Read::Base);
    }
    if s.in_flight > 0 {
        tx.wake_after(Duration::from_millis(60));
    } else if s.tab == appearance::TAB && (visible() || std::env::args().any(|a| a == "--window")) {
        tx.wake_after(Duration::from_millis(300));
    } else if s.retry_base.is_some() && visible() {
        tx.wake_after(Duration::from_secs(1));
    }
    changed
}

fn on_key(s: &mut Prefs, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.is_command('w') {
        tx.close_window();
        return true;
    }
    if matches!(ev.key, rune::Key::Escape) && s.open_select.take().is_some() {
        return true;
    }
    false
}

pub fn toggle_select(s: &mut Prefs, key: &'static str) {
    s.open_select = if s.open_select == Some(key) { None } else { Some(key) };
}

fn unfollowed(s: &Prefs) -> Option<El> {
    let note = match (&s.base, &s.stream_down) {
        (Some(Err(f)), _) => {
            let f = explain(s, f);
            Failure { message: format!("Couldn't read this computer's state, so changes made elsewhere won't show here until it can: {}", f.message), ..f }
        }
        (_, Some(why)) => Failure { uncertain: true, ..Failure::new(format!("Can't follow changes made elsewhere right now ({why}), so what shows may be out of date. Settings keeps trying.")) },
        _ => return None,
    };
    Some(col().w_full().items_center().pb(8.0).child(parts::failure(s, &note).w(parts::WIDTH)))
}

pub const TABS: [&str; 6] = ["General", "Claude", "Notifications", "Devices", "Handoffs", "Appearance"];

pub fn view(s: &Prefs, cx: &mut Cx<Prefs>) -> El {
    crate::look::frame(cx, false, false, false);
    let k = theme::kit();
    let mut layer = Vec::new();
    let tabs = SegmentedControl::new(&TABS, Some(s.tab)).label("Settings").as_tabs().fit().on_pick(|s: &mut Prefs, i, _| {
        s.tab = i;
        s.open_select = None;
        devices::hide_link(s);
        handoffs::changed(s);
        appearance::opened(s);
    });
    let page = match s.tab {
        1 => claude::page(s, &mut layer),
        2 => notify::page(s),
        3 => devices::page(s, &mut layer),
        handoffs::TAB => handoffs::page(s, &mut layer),
        appearance::TAB => appearance::page(s),
        _ => general::page(s, &mut layer),
    };
    let body = col()
        .w_full()
        .h_full()
        .child(el().absolute(0.0, 0.0).w_full().h_full().fill(theme::card_deep()))
        .child(col().w_full().items_center().pt(14.0).pb(10.0).child(tabs.build(k)))
        .children(unfollowed(s))
        .child(parts::scroll(page));
    rune_kit::root(body, layer).on_window_focus(|s: &mut Prefs, focused, _| {
        if !focused {
            devices::hide_link(s);
        }
    })
}
