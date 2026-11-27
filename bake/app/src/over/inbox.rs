use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, RecvTimeoutError, Sender, channel};
use std::time::{Duration, Instant};

use feed::brief::Brief;
use feed::catalog::{Client, RespondError, request_key};
use feed::decisions::{Decision, Handled, Inbox, gone, remember, rev_moved};
use feed::review::{self, Verdict};
use feed::version::{Build, notice};
use rune::TextEdit;

const OLD_RECHECK: Duration = Duration::from_secs(300);

enum Done {
    Inbox(Instant, Inbox),
    Acted(String, Handled, bool, String, Result<(), (RespondError, String)>),
    Brief(String, Result<Brief, RespondError>),
    Version(Option<String>, Option<String>),
}

pub struct Note {
    pub decision: Decision,
    pub title: String,
    pub text: TextEdit,
    pub gone: bool,
    pub why: Option<String>,
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
    pub reads: u64,
    review_old: HashMap<String, Instant>,
    pub note: Option<Note>,
    daemon: Option<String>,
    newest: Option<Instant>,
    rev: Option<String>,
    ask: Option<Sender<bool>>,
}

impl Default for State {
    fn default() -> Self {
        let (tx, rx) = channel();
        State { tx, rx, inbox: None, history: Vec::new(), acting: HashSet::new(), unconfirmed: HashSet::new(), notes: HashMap::new(), briefs: HashMap::new(), version: None, reads: 0, review_old: HashMap::new(), note: None, daemon: None, newest: None, rev: None, ask: None }
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

fn fetch(client: &Client, tx: &Sender<Done>) -> Option<()> {
    let started = Instant::now();
    tx.send(Done::Inbox(started, client.inbox())).ok()
}

pub fn start(state: &mut State, client: &Client) {
    if state.ask.is_some() {
        return;
    }
    let (ask, asked) = channel::<bool>();
    let _ = ask.send(false);
    state.ask = Some(ask);
    let (client, tx) = (client.clone(), state.tx.clone());
    check_version(&client, &tx);
    std::thread::spawn(move || {
        let mut pace = feed::watch::Pace::default();
        loop {
            let got = match pace.wait(Instant::now()) {
                Some(wait) => asked.recv_timeout(wait),
                None => asked.recv().map_err(|_| RecvTimeoutError::Disconnected),
            };
            let mut due = match got {
                Ok(now) => pace.ask(now, Instant::now()),
                Err(RecvTimeoutError::Timeout) => true,
                Err(RecvTimeoutError::Disconnected) => return,
            };
            while let Ok(now) = asked.try_recv() {
                due |= pace.ask(now, Instant::now());
            }
            if !due {
                continue;
            }
            let started = Instant::now();
            if fetch(&client, &tx).is_none() {
                return;
            }
            pace.read(started);
        }
    });
}

fn check_version(client: &Client, tx: &Sender<Done>) {
    let (client, tx) = (client.clone(), tx.clone());
    std::thread::spawn(move || {
        let daemon = client.version().ok();
        let shown = daemon.as_ref().and_then(|d| notice(&app_build(), d));
        let _ = tx.send(Done::Version(shown, daemon.filter(|d| d.known()).map(|d| d.revision)));
    });
}

pub fn changed(s: &crate::App) {
    if let Some(ask) = &s.ov.inbox.ask {
        let _ = ask.send(false);
    }
}

pub fn reopened(s: &crate::App) {
    if let Some(client) = &s.client {
        check_version(client, &s.ov.inbox.tx);
    }
    changed(s);
}

pub fn refresh(client: &Client, st: &State) {
    match &st.ask {
        Some(ask) => {
            let _ = ask.send(true);
        }
        None => {
            let (client, tx) = (client.clone(), st.tx.clone());
            std::thread::spawn(move || {
                fetch(&client, &tx);
            });
        }
    }
}

pub fn rev_seen(s: &mut crate::App, rev: &str) {
    if rev_moved(&mut s.ov.inbox.rev, rev) {
        changed(s);
    }
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
                st.reads += 1;
                if inbox.failed {
                    st.rev = None;
                }
                if inbox.notes.is_empty() {
                    st.unconfirmed.retain(|hold| inbox.decisions.iter().any(|d| d.holds(hold)));
                }
                st.inbox = Some(inbox);
            }
            Done::Acted(id, entry, reviewed, host, result) => {
                st.acting.remove(&id);
                st.newest = Some(Instant::now());
                match result {
                    Err((e, _)) if reviewed && review::needs_newer(&e) => {
                        st.review_old.insert(host, Instant::now());
                        st.notes.remove(&id);
                    }
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
                        let why = if reviewed { review::refusal(&e) } else { e.message };
                        st.notes.insert(id, (hold, format!("Not sent: {why}")));
                    }
                }
                if let Some(client) = &s.client {
                    refresh(client, &s.ov.inbox);
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
            Done::Version(shown, daemon) => {
                st.version = shown;
                if daemon.is_some() && daemon != st.daemon {
                    if st.daemon.is_some() {
                        st.review_old.clear();
                    }
                    st.daemon = daemon;
                }
            }
        }
    }
    changed
}

fn title_of(s: &crate::App, d: &Decision) -> String {
    s.ov.d.session(&d.session_id).map(|x| s.ov.d.title(x)).unwrap_or_else(|| if d.work_title.is_empty() { feed::derive::text::short_id(&d.session_id) } else { d.work_title.clone() })
}

fn act(s: &mut crate::App, d: &Decision, outcome: &str, run: impl FnOnce(&Client, &Decision) -> Result<(), (RespondError, String)> + Send + 'static) {
    let reviewed = review::target(d).is_some();
    let host = d.host.clone();
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
        let _ = tx.send(Done::Acted(id, entry, reviewed, host, result));
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

pub fn too_old(st: &State, d: &Decision) -> bool {
    st.review_old.get(&d.host).is_some_and(|at| at.elapsed() < OLD_RECHECK)
}

pub fn reviewable(st: &State, d: &Decision) -> bool {
    review::target(d).is_some() && !too_old(st, d) && !st.acting.contains(&d.id()) && !st.unconfirmed.contains(&d.hold_key())
}

pub fn review(s: &mut crate::App, d: &Decision, verdict: Verdict, note: &str) {
    if !reviewable(&s.ov.inbox, d) || verdict == Verdict::Reject && review::note_problem(note).is_some() {
        return;
    }
    let note = note.to_string();
    act(s, d, verdict.outcome(), move |client, d| {
        let t = review::target(d).expect("checked above");
        client.work_review(&t, verdict, &note).map(drop).map_err(|e| (e, d.hold_key()))
    });
}

pub fn ask_changes(s: &mut crate::App, d: &Decision) {
    if !reviewable(&s.ov.inbox, d) {
        return;
    }
    let title = if d.work_title.is_empty() { title_of(s, d) } else { d.work_title.clone() };
    let mut text = TextEdit::new("");
    text.focused = true;
    s.composer.focused = false;
    s.ov.inbox.note = Some(Note { decision: d.clone(), title, text, gone: false, why: None });
}

fn listed(st: &State, d: &Decision) -> Option<Decision> {
    st.inbox.as_ref()?.decisions.iter().find(|x| x.host == d.host && x.hold_key() == d.hold_key()).cloned()
}

pub fn send_changes(s: &mut crate::App) {
    let st = &s.ov.inbox;
    let Some(n) = st.note.as_ref() else { return };
    if n.gone || review::note_problem(&n.text.text).is_some() {
        return;
    }
    let current = listed(st, &n.decision);
    let why = match &current {
        None => None,
        Some(c) if !reviewable(st, c) => Some(if too_old(st, c) { review::needs_newer_note() } else { "A verdict on this review is still on its way.".into() }),
        Some(_) => None,
    };
    let note = n.text.text.clone();
    let n = s.ov.inbox.note.as_mut().expect("checked above");
    match (current, why) {
        (None, _) => n.gone = true,
        (Some(_), Some(why)) => n.why = Some(why),
        (Some(current), None) => {
            s.ov.inbox.note = None;
            review(s, &current, Verdict::Reject, &note);
        }
    }
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
