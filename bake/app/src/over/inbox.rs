use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::brief::Brief;
use feed::catalog::{Client, RespondError, request_key};
use feed::decisions::{Decision, Handled, Inbox, gone, remember};
use feed::version::{Build, notice};

const EVERY: Duration = Duration::from_secs(10);
const VERSION_EVERY: Duration = Duration::from_secs(60);

enum Done {
    Inbox(Instant, Inbox),
    Acted(String, Handled, Result<(), (RespondError, String)>),
    Brief(String, Result<Brief, RespondError>),
    Version(Option<String>),
}

#[derive(Default, Clone)]
pub struct BriefState {
    pub asking: bool,
    pub notice: Option<String>,
    pub failed: bool,
    key: Option<String>,
}

pub struct State {
    tx: Sender<Done>,
    rx: Receiver<Done>,
    pub inbox: Option<Inbox>,
    pub history: Vec<Handled>,
    pub acting: HashSet<String>,
    pub unconfirmed: HashSet<String>,
    pub notes: HashMap<String, (String, String)>,
    pub briefs: HashMap<String, BriefState>,
    pub version: Option<String>,
    newest: Option<Instant>,
    started: bool,
}

impl Default for State {
    fn default() -> Self {
        let (tx, rx) = channel();
        State { tx, rx, inbox: None, history: Vec::new(), acting: HashSet::new(), unconfirmed: HashSet::new(), notes: HashMap::new(), briefs: HashMap::new(), version: None, newest: None, started: false }
    }
}

pub fn app_build() -> Build {
    Build {
        version: option_env!("AGENTHAIL_VERSION").unwrap_or_default().into(),
        revision: option_env!("AGENTHAIL_REVISION").unwrap_or_default().into(),
        modified: option_env!("AGENTHAIL_MODIFIED") == Some("true"),
        release: option_env!("AGENTHAIL_RELEASE") == Some("true"),
    }
}

fn fetch(client: &Client, tx: &Sender<Done>) -> bool {
    let started = Instant::now();
    tx.send(Done::Inbox(started, client.inbox())).is_ok()
}

pub fn start(state: &mut State, client: &Client) {
    if state.started {
        return;
    }
    state.started = true;
    let (client, tx) = (client.clone(), state.tx.clone());
    let (versions, said) = (client.clone(), tx.clone());
    std::thread::spawn(move || {
        loop {
            let shown = versions.version().ok().and_then(|daemon| notice(&app_build(), &daemon));
            if said.send(Done::Version(shown)).is_err() {
                return;
            }
            std::thread::sleep(VERSION_EVERY);
        }
    });
    std::thread::spawn(move || {
        while fetch(&client, &tx) {
            std::thread::sleep(EVERY);
        }
    });
}

fn refresh(client: &Client, tx: &Sender<Done>) {
    let (client, tx) = (client.clone(), tx.clone());
    std::thread::spawn(move || {
        fetch(&client, &tx);
    });
}

pub fn drain(s: &mut crate::App) -> bool {
    let mut changed = false;
    while let Ok(done) = s.ov.inbox.rx.try_recv() {
        changed = true;
        let st = &mut s.ov.inbox;
        match done {
            Done::Inbox(started, inbox) => {
                if st.newest.is_some_and(|n| n > started) {
                    continue;
                }
                st.newest = Some(started);
                if inbox.notes.is_empty() {
                    st.unconfirmed.retain(|hold| inbox.decisions.iter().any(|d| d.holds(hold)));
                }
                st.inbox = Some(inbox);
            }
            Done::Acted(id, entry, result) => {
                st.acting.remove(&id);
                st.newest = Some(Instant::now());
                match result {
                    Ok(()) => {
                        st.notes.remove(&id);
                        if let Some(inbox) = &mut st.inbox {
                            inbox.decisions.retain(|d| d.id() != id);
                        }
                        remember(&mut st.history, entry);
                    }
                    Err((e, hold)) if e.maybe_delivered => {
                        st.notes.insert(id, (hold.clone(), format!("Sent, not confirmed: {}", e.message)));
                        st.unconfirmed.insert(hold);
                    }
                    Err((e, hold)) => {
                        st.notes.insert(id, (hold, format!("Not sent: {}", e.message)));
                    }
                }
                if let Some(client) = &s.client {
                    refresh(client, &s.ov.inbox.tx);
                }
            }
            Done::Brief(project, result) => {
                let b = st.briefs.entry(project).or_default();
                b.asking = false;
                match result {
                    Ok(brief) => {
                        b.notice = Some(brief.notice(time::OffsetDateTime::now_utc()));
                        b.failed = false;
                        b.key = None;
                    }
                    Err(e) => {
                        if !e.maybe_delivered {
                            b.key = None;
                        }
                        b.notice = Some(if e.maybe_delivered { format!("Asked, not confirmed: {}", e.message) } else { format!("Couldn't ask: {}", e.message) });
                        b.failed = true;
                    }
                }
            }
            Done::Version(shown) => st.version = shown,
        }
    }
    changed
}

fn title_of(s: &crate::App, d: &Decision) -> String {
    s.ov.d.session(&d.session_id).map(|x| s.ov.d.title(x)).unwrap_or_else(|| if d.work_title.is_empty() { feed::derive::text::short_id(&d.session_id) } else { d.work_title.clone() })
}

fn act(s: &mut crate::App, d: &Decision, outcome: &str, run: impl FnOnce(&Client, &Decision) -> Result<(), (RespondError, String)> + Send + 'static) {
    let id = d.id();
    if s.ov.inbox.acting.contains(&id) {
        return;
    }
    let Some(client) = s.client.clone() else {
        s.ov.inbox.notes.insert(id, (d.hold_key(), "Fixture mode: not sent".into()));
        return;
    };
    let at = s.ov.d.now.format(&time::format_description::well_known::Rfc3339).unwrap_or_default();
    let entry = Handled { title: title_of(s, d), session: d.session_id.clone(), outcome: outcome.into(), at };
    s.ov.inbox.acting.insert(id.clone());
    s.ov.inbox.notes.remove(&id);
    let (tx, d) = (s.ov.inbox.tx.clone(), d.clone());
    std::thread::spawn(move || {
        let result = run(&client, &d);
        let _ = tx.send(Done::Acted(id, entry, result));
    });
}

pub fn accept(s: &mut crate::App, d: &Decision) {
    if d.recommended().is_none() || s.ov.inbox.unconfirmed.contains(&d.hold_key()) {
        return;
    }
    act(s, d, "Accepted the recommended answer", |client, d| {
        let (request, choices) = d.recommended().expect("checked above");
        client.respond(&d.session_id, &request, choices, Default::default()).map_err(|e| (e, d.hold_key()))
    });
}

pub fn shift_target(st: &State) -> Option<&Decision> {
    st.inbox.as_ref()?.decisions.iter().find(|d| d.recommended().is_some() && !st.unconfirmed.contains(&d.hold_key()) && !st.acting.contains(&d.id()))
}

pub fn accept_first(s: &mut crate::App) -> bool {
    match shift_target(&s.ov.inbox).cloned() {
        Some(d) => {
            accept(s, &d);
            true
        }
        None => false,
    }
}

pub fn not_now(s: &mut crate::App, d: &Decision) {
    let held = s.ov.inbox.unconfirmed.clone();
    if d.open_dismissals(&held).is_empty() {
        return;
    }
    act(s, d, "Not now", move |client, d| {
        for target in d.open_dismissals(&held) {
            let hold = d.hold_of(&target);
            match client.dismiss_decision(target) {
                Ok(()) => {}
                Err(e) if gone(&e) => {}
                Err(e) => return Err((e, hold)),
            }
        }
        Ok(())
    });
}

pub fn ask_brief(s: &mut crate::App, project: &str) {
    let Some(client) = s.client.clone() else {
        s.ov.inbox.briefs.insert(project.into(), BriefState { notice: Some("Fixture mode: not sent".into()), failed: true, ..Default::default() });
        return;
    };
    let b = s.ov.inbox.briefs.entry(project.into()).or_default();
    if b.asking {
        return;
    }
    b.asking = true;
    let key = b.key.get_or_insert_with(request_key).clone();
    let (tx, project) = (s.ov.inbox.tx.clone(), project.to_string());
    std::thread::spawn(move || {
        let result = client.project_brief(&project, &key);
        let _ = tx.send(Done::Brief(project, result));
    });
}
