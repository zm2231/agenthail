use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::catalog::RespondError;
use feed::derive::needs::{NeedKind, Target};
use feed::notify::{Kind, Notice, Notified, Prefs, REPLY, choice_of};
use rune_notify::{Click, Notification};

use crate::{App, Screen};

const WITHDRAWN_KEPT: usize = 64;

enum Done {
    Prefs(Option<Prefs>),
    Acted(String, String, Result<(), RespondError>),
}

pub struct State {
    store: Notified,
    path: Option<PathBuf>,
    prefs: Option<Prefs>,
    on: bool,
    asking: bool,
    stale: bool,
    failed: bool,
    focused: bool,
    reads: u64,
    withdrawn: Vec<String>,
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
        State { store: Notified::default(), path: None, prefs: None, on: false, asking: false, stale: true, failed: false, focused: false, reads: 0, withdrawn: Vec::new(), sessions_moved: false, clicks, clicked, tx, rx }
    }
}

fn path() -> Option<PathBuf> {
    let home = std::env::var_os("HOME")?;
    Some(PathBuf::from(home).join("Library/Application Support/Agenthail/notified.json"))
}

pub fn start(s: &mut App) {
    let st = &mut s.notify;
    if st.on || !rune_notify::available() {
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
}

fn read_prefs(s: &mut App) {
    let Some(client) = s.client.clone() else { return };
    let st = &mut s.notify;
    if st.asking {
        return;
    }
    (st.asking, st.stale) = (true, false);
    let tx = st.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(Done::Prefs(client.notification_preferences().ok()));
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
            let result = client.send(&session, &text);
            let _ = tx.send(Done::Acted(session, "reply".into(), result));
        });
        return;
    }
    let (Some(choice), Some(request)) = (choice_of(&action).map(str::to_string), click.get("request").map(str::to_string)) else { return };
    std::thread::spawn(move || {
        let result = client.respond(&session, &request, vec![choice], Default::default());
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
    let mut changed = false;
    while let Ok(done) = s.notify.rx.try_recv() {
        match done {
            Done::Prefs(prefs) => {
                s.notify.asking = false;
                s.notify.failed = prefs.is_none();
                if prefs.is_some() {
                    s.notify.prefs = prefs;
                }
            }
            Done::Acted(session, what, result) => acted(s, session, what, result),
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
    let mut dirty = false;
    let mut withdrawn = std::mem::take(&mut s.notify.withdrawn);
    for step in steps {
        dirty |= step.dirty;
        withdrawn.extend(step.remove);
        withdrawn.drain(..withdrawn.len().saturating_sub(WITHDRAWN_KEPT));
        let gone: Vec<&str> = withdrawn.iter().map(String::as_str).collect();
        rune_notify::remove(&gone);
        for n in &step.post {
            post(n);
        }
    }
    s.notify.withdrawn = withdrawn;
    if dirty && let Some(path) = &s.notify.path {
        let _ = s.notify.store.save(path);
    }
    changed
}
