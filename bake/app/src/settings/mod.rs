mod claude;
mod general;
mod notify;
mod parts;

use std::cell::OnceCell;
use std::collections::{HashMap, HashSet};
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
}

const ALL: [Read; 5] = [Read::Base, Read::Settings, Read::Remote, Read::Compaction, Read::Preferences];

enum Got {
    Base(Result<Base, Failure>),
    Settings(Result<Settings, Failure>),
    Remote(Result<RemoteControl, Failure>),
    Compaction(Result<Compaction, Failure>),
    Preferences(Result<Vec<Preference>, Failure>),
}

#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug)]
pub enum Section {
    General,
    Remote,
    Compaction,
    Notify,
}

enum Done {
    Read(Read, u64, Got),
    Wrote(String, Section, Result<(), Failure>, &'static [Read]),
}

pub struct Prefs {
    client: Option<Client>,
    no_client: String,
    pub draft_orphaned: bool,
    pub tab: usize,
    pub base: Option<Result<Base, Failure>>,
    pub settings: Option<Result<Settings, Failure>>,
    pub remote: Option<Result<RemoteControl, Failure>>,
    pub compaction: Option<Result<Compaction, Failure>>,
    pub preferences: Option<Result<Vec<Preference>, Failure>>,
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
    loaded: bool,
    in_flight: usize,
    seq: HashMap<Read, u64>,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

thread_local! {
    static WINDOW: OnceCell<(AppHandle<Prefs>, WindowHandle)> = const { OnceCell::new() };
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
            handle.update(|s: &mut Prefs, _| reload(s));
            window.open();
            window.focus();
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
        draft_orphaned: false,
        tab: 0,
        base: None,
        settings: None,
        remote: None,
        compaction: None,
        preferences: None,
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
        };
        let _ = tx.send(Done::Read(what, seq, got));
    });
}

pub fn reload(s: &mut Prefs) {
    s.loaded = true;
    if s.client.is_none() {
        (s.client, s.no_client) = connect();
    }
    if s.client.is_some() {
        s.settling.extend(ALL);
    }
    for what in ALL {
        fetch(s, what);
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
    if c.roots.iter().any(|r| r.root == s.draft.root) {
        return;
    }
    let first = c.roots.first().map(|r| r.root.clone()).unwrap_or_default();
    if s.draft.root.is_empty() && !s.draft_orphaned || !claude::draft(s).touched() {
        s.draft.root = first;
        s.draft_orphaned = false;
    } else if !s.draft.root.is_empty() {
        s.draft.root.clear();
        s.draft_orphaned = true;
    }
}

fn denied<T>(r: &Result<T, Failure>) -> bool {
    r.as_ref().is_err_and(|f| f.denied)
}

fn take(s: &mut Prefs, done: Done) {
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
                    if r.is_ok() {
                        s.base_ok_at = s.tick;
                    }
                    s.base = Some(r);
                    seed_name(s);
                    false
                }
                Got::Settings(r) => {
                    let d = denied(&r);
                    s.settings = Some(r);
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
            };
            refused(s, Some(what), refusal);
        }
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
                Ok(()) => {}
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
    write(s, what.action(), Section::Notify, &[Read::Settings], move |c| c.notifications(what));
}

fn poll(s: &mut Prefs, _: f64, tx: &mut Tx) -> bool {
    let mut changed = false;
    if !s.loaded && (visible() || std::env::args().any(|a| a == "--window")) {
        reload(s);
        changed = true;
    }
    while let Ok(done) = s.rx.try_recv() {
        take(s, done);
        changed = true;
    }
    if s.in_flight > 0 {
        tx.wake_after(Duration::from_millis(60));
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

pub const TABS: [&str; 3] = ["General", "Claude", "Notifications"];

pub fn view(s: &Prefs, _: &mut Cx<Prefs>) -> El {
    let k = theme::kit();
    let mut layer = Vec::new();
    let tabs = SegmentedControl::new(&TABS, Some(s.tab)).label("Settings").as_tabs().fit().on_pick(|s: &mut Prefs, i, _| {
        s.tab = i;
        s.open_select = None;
    });
    let page = match s.tab {
        1 => claude::page(s, &mut layer),
        2 => notify::page(s),
        _ => general::page(s, &mut layer),
    };
    let body = col()
        .w_full()
        .h_full()
        .child(el().absolute(0.0, 0.0).w_full().h_full().fill(theme::card_deep()))
        .child(col().w_full().items_center().pt(14.0).pb(10.0).child(tabs.build(k)))
        .child(parts::scroll(page));
    rune_kit::root(body, layer)
}
