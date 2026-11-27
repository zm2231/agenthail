use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::catalog::RespondError;
use feed::derive::needs::{NeedKind, Target};
use feed::notify::{Kind, Notice, Notified, Permission, Prefs, REPLY, choice_of, daemon_posts};
use rune_notify::{Authorization, Click, Failure, Notification, PostError};

use crate::{App, Screen};

const WITHDRAWN_KEPT: usize = 64;

enum Done {
    Prefs(Option<Prefs>),
    Acted(String, String, Result<(), RespondError>),
    Allowed(Authorization),
    Dropped(PostError),
}

static SAVED: Mutex<Vec<(String, bool)>> = Mutex::new(Vec::new());
static RECHECK: AtomicBool = AtomicBool::new(false);

pub fn recheck() {
    RECHECK.store(true, Ordering::Relaxed);
}

pub fn preference_saved(id: &str, value: bool) {
    SAVED.lock().unwrap_or_else(|e| e.into_inner()).push((id.to_string(), value));
}

pub struct State {
    store: Notified,
    path: Option<PathBuf>,
    prefs: Option<Prefs>,
    started: bool,
    on: bool,
    asking: bool,
    stale: bool,
    edited: bool,
    failed: bool,
    focused: bool,
    reads: u64,
    withdrawn: Vec<String>,
    permission: Option<Permission>,
    checking: bool,
    again: bool,
    daemon: bool,
    pub sessions_moved: bool,
    clicks: Receiver<Click>,
    clicked: Sender<Click>,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl Default for State {
    fn default() -> Self {
        let (clicked, clicks) = channel();
        let (tx, rx) = channel();
        State { store: Notified::default(), path: None, prefs: None, started: false, on: false, asking: false, stale: true, edited: false, failed: false, focused: false, reads: 0, withdrawn: Vec::new(), permission: None, checking: false, again: false, daemon: false, sessions_moved: false, clicks, clicked, tx, rx }
    }
}

fn path() -> Option<PathBuf> {
    let home = std::env::var_os("HOME")?;
    Some(PathBuf::from(home).join("Library/Application Support/Agenthail/notified.json"))
}

fn start(s: &mut App) {
    let st = &mut s.notify;
    if std::mem::replace(&mut st.started, true) {
        return;
    }
    if !rune_notify::available() {
        set_permission(st, Permission::Unavailable);
        return;
    }
    st.on = true;
    st.path = path();
    if let Some(path) = &st.path {
        st.store = Notified::load(path);
    }
    let clicked = st.clicked.clone();
    rune_notify::on_click(move |click| {
        let _ = clicked.send(click);
    });
    let dropped = st.tx.clone();
    rune_notify::on_post_error(move |error| {
        let _ = dropped.send(Done::Dropped(error));
    });
    check(st);
}

fn check(st: &mut State) {
    st.daemon = std::env::var_os("HOME").is_some_and(|home| daemon_posts(Path::new(&home)));
    if std::mem::replace(&mut st.checking, true) {
        st.again = true;
        return;
    }
    let tx = st.tx.clone();
    rune_notify::authorization(move |answer| {
        let _ = tx.send(Done::Allowed(answer));
    });
}

fn permission_of(answer: Authorization) -> Permission {
    match answer {
        _ if answer.allows_posts() => Permission::Allowed,
        Authorization::Denied => Permission::Off,
        Authorization::NotDetermined => Permission::NotAsked,
        _ => Permission::Unavailable,
    }
}

fn set_permission(st: &mut State, permission: Permission) {
    if st.permission != Some(permission) {
        st.permission = Some(permission);
        crate::settings::permission_changed(permission);
    }
}

pub fn menu_note(s: &App) -> Option<&'static str> {
    s.notify.permission.filter(|p| p.warns() && !s.notify.daemon).and_then(Permission::note).map(|n| n.0)
}

fn read_prefs(s: &mut App) {
    let Some(client) = s.client.clone() else { return };
    let st = &mut s.notify;
    if st.asking {
        return;
    }
    (st.asking, st.stale, st.edited) = (true, false, false);
    let tx = st.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(Done::Prefs(client.notification_preferences().ok().map(|rows| Prefs::from_preferences(&rows))));
    });
}

fn on_screen(s: &App, n: &Notice) -> bool {
    match &s.screen {
        Screen::Session => s.selected.as_deref() == Some(n.session.as_str()),
        Screen::Now if s.ov.now_brief || !s.ov.now_centre_hidden => match n.kind {
            Kind::Needs => true,
            Kind::Finished => s.ov.needs.iter().any(|x| x.kind == NeedKind::Finished && x.target == Target::Session(n.session.clone())),
        },
        _ => false,
    }
}

fn post(n: &Notice) {
    let mut x = Notification::new(&n.key, &n.title).body(&n.body).thread(&n.session, &n.thread).sound(n.sound).info("session", &n.session);
    if let Some(request) = &n.request {
        x = x.info("request", request);
    }
    for (id, label) in &n.buttons {
        x = x.action(id, label);
    }
    rune_notify::post(&x.reply(REPLY, "Message", "Reply"));
}

fn trouble(session: &str, thread: &str, body: String) {
    let key = format!("trouble|{session}|{}", feed::catalog::request_key());
    rune_notify::post(&Notification::new(&key, thread).body(body).thread(session, thread).info("session", session));
}

fn title(s: &App, session: &str) -> String {
    crate::session(s, session).map(|x| s.ov.d.title(x)).unwrap_or_else(|| feed::derive::text::short_id(session))
}

fn clicked(s: &mut App, click: Click) {
    let Some(session) = click.get("session").map(str::to_string) else { return };
    let Some(action) = click.action().map(str::to_string) else {
        rune_notify::bring_to_front();
        crate::over::open_session(s, &session);
        return;
    };
    let Some(client) = s.client.clone() else { return };
    let tx = s.notify.tx.clone();
    if action == REPLY {
        let Some(text) = click.input(REPLY).map(str::trim).filter(|t| !t.is_empty()).map(str::to_string) else { return };
        std::thread::spawn(move || {
            let result = match feed::notice::reply_refusal(&client.session_row(&session)) {
                Some(why) => Err(feed::notice::refused(why)),
                None => client.send(&session, &text),
            };
            let _ = tx.send(Done::Acted(session, "reply".into(), result));
        });
        return;
    }
    let (Some(choice), Some(request)) = (choice_of(&action).map(str::to_string), click.get("request").map(str::to_string)) else { return };
    std::thread::spawn(move || {
        let result = match feed::notice::reply_refusal(&client.session_row(&session)) {
            Some(why) => Err(feed::notice::refused(why)),
            None => client.respond(&session, &request, vec![choice], Default::default()),
        };
        let _ = tx.send(Done::Acted(session, request, result));
    });
}

fn acted(s: &mut App, session: String, what: String, result: Result<(), RespondError>) {
    if let Some(client) = s.client.clone() {
        crate::over::inbox::refresh(&client, &s.ov.inbox);
    }
    let Err(error) = result else { return };
    let verb = match (what == "reply", error.maybe_delivered) {
        (true, true) => "Sent, not confirmed",
        (true, false) => "Couldn't send your reply",
        (false, true) => "Answer sent, not confirmed",
        (false, false) => "Not answered",
    };
    trouble(&session, &title(s, &session), format!("{verb}: {}", error.message));
}

pub fn tick(s: &mut App) -> bool {
    start(s);
    let mut changed = false;
    while let Ok(done) = s.notify.rx.try_recv() {
        match done {
            Done::Prefs(_) if s.notify.edited => s.notify.asking = false,
            Done::Prefs(prefs) => {
                s.notify.asking = false;
                s.notify.failed = prefs.is_none();
                if prefs.is_some() {
                    s.notify.prefs = prefs;
                }
            }
            Done::Acted(session, what, result) => acted(s, session, what, result),
            Done::Allowed(answer) => {
                s.notify.checking = false;
                set_permission(&mut s.notify, permission_of(answer));
                if std::mem::take(&mut s.notify.again) {
                    check(&mut s.notify);
                }
                changed = true;
            }
            Done::Dropped(error) => {
                eprintln!("agenthail: {error}");
                if error.failure == Failure::Denied {
                    set_permission(&mut s.notify, Permission::Off);
                    changed = true;
                }
                check(&mut s.notify);
            }
        }
    }
    while let Ok(click) = s.notify.clicks.try_recv() {
        clicked(s, click);
        changed = true;
    }
    if !s.notify.on {
        return changed;
    }
    let now = time::OffsetDateTime::now_utc();
    let focused = rune_notify::app_focused();
    let st = &mut s.notify;
    for (id, value) in std::mem::take(&mut *SAVED.lock().unwrap_or_else(|e| e.into_inner())) {
        st.prefs.get_or_insert_with(Prefs::default).set(&id, value);
        (st.edited, st.stale) = (true, true);
    }
    if focused && !st.focused || RECHECK.swap(false, Ordering::Relaxed) {
        let daemon = st.daemon;
        check(st);
        changed |= st.daemon != daemon;
    }
    st.stale |= focused && !st.focused || st.failed && st.sessions_moved;
    st.focused = focused;
    if st.stale {
        read_prefs(s);
    }
    let sound = s.notify.prefs.is_some() && !s.notify.failed;
    let mut steps = Vec::new();
    let reads = s.ov.inbox.reads;
    if reads != s.notify.reads
        && let Some(inbox) = &s.ov.inbox.inbox
    {
        s.notify.reads = reads;
        let found = feed::notify::needs(&inbox.decisions, &s.sessions, sound);
        let mut store = std::mem::take(&mut s.notify.store);
        steps.push(store.take(Kind::Needs, found, &inbox.read, now, |n| focused && on_screen(s, n)));
        s.notify.store = store;
    }
    if std::mem::take(&mut s.notify.sessions_moved) {
        let leads: HashMap<String, String> = s.ov.d.projects.iter().filter_map(|p| Some((p.key.clone(), p.lead.clone()?))).collect();
        let found = feed::notify::finished(&s.sessions, &leads, &s.notify.prefs.clone().unwrap_or_default(), sound, now);
        let mut store = std::mem::take(&mut s.notify.store);
        steps.push(store.take(Kind::Finished, found, &[String::new()], now, |n| focused && on_screen(s, n)));
        s.notify.store = store;
    }
    let quiet = !steps.is_empty() && std::env::var_os("HOME").is_some_and(|home| daemon_posts(Path::new(&home)));
    let mut dirty = false;
    let mut withdrawn = std::mem::take(&mut s.notify.withdrawn);
    for step in steps {
        dirty |= step.dirty;
        withdrawn.extend(step.remove);
        let gone: Vec<&str> = withdrawn.iter().map(String::as_str).collect();
        rune_notify::remove(&gone);
        withdrawn.drain(..withdrawn.len().saturating_sub(WITHDRAWN_KEPT));
        if !quiet {
            for n in &step.post {
                post(n);
            }
        }
    }
    s.notify.withdrawn = withdrawn;
    if dirty && let Some(path) = &s.notify.path {
        let _ = s.notify.store.save(path);
    }
    changed
}
