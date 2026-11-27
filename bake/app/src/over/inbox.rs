use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::brief::Brief;
use feed::catalog::{Client, RespondError, request_key};
use feed::composer::Delivered;
use feed::decisions::{Decision, Handled, Inbox, gone, remember, request_hold, rev_moved};
use feed::request::Response;
use feed::review::{self, Verdict};
use feed::version::{Build, notice};
use rune::TextEdit;

const EVERY: Duration = Duration::from_secs(10);
const VERSION_EVERY: Duration = Duration::from_secs(60);
const OLD_RECHECK: Duration = Duration::from_secs(300);

enum Done {
    Inbox(Instant, Inbox),
    Acted(String, Handled, bool, String, Result<(), (RespondError, String)>),
    Brief(String, Result<Brief, RespondError>),
    Replied(String, String, String, Result<String, RespondError>),
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
    started: bool,
    pub open: Option<String>,
    pub replies: HashMap<String, TextEdit>,
    pub replying: HashSet<String>,
    pub replied: HashMap<String, String>,
    reply_keys: HashMap<String, (String, String)>,
    answered: HashMap<String, String>,
}

impl Default for State {
    fn default() -> Self {
        let (tx, rx) = channel();
        State { tx, rx, inbox: None, history: Vec::new(), acting: HashSet::new(), unconfirmed: HashSet::new(), notes: HashMap::new(), briefs: HashMap::new(), version: None, reads: 0, review_old: HashMap::new(), note: None, daemon: None, newest: None, rev: None, started: false, open: None, replies: HashMap::new(), replying: HashSet::new(), replied: HashMap::new(), reply_keys: HashMap::new(), answered: HashMap::new() }
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
            let daemon = versions.version().ok();
            let shown = daemon.as_ref().and_then(|d| notice(&app_build(), d));
            if said.send(Done::Version(shown, daemon.filter(|d| d.known()).map(|d| d.revision))).is_err() {
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

pub fn refresh(client: &Client, st: &State) {
    let tx = &st.tx;
    let (client, tx) = (client.clone(), tx.clone());
    std::thread::spawn(move || {
        fetch(&client, &tx);
    });
}

pub fn rev_seen(s: &mut crate::App, rev: &str) {
    if rev_moved(&mut s.ov.inbox.rev, rev)
        && let Some(client) = &s.client
    {
        refresh(client, &s.ov.inbox);
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
                if inbox.notes.is_empty() {
                    st.unconfirmed.retain(|hold| inbox.decisions.iter().any(|d| d.holds(hold)));
                }
                st.inbox = Some(inbox);
            }
            Done::Acted(id, entry, reviewed, host, result) => {
                st.acting.remove(&id);
                let answered = st.answered.remove(&id);
                st.newest = Some(Instant::now());
                match result {
                    Err((e, _)) if reviewed && review::needs_newer(&e) => {
                        st.review_old.insert(host, Instant::now());
                        st.notes.remove(&id);
                    }
                    Ok(()) => {
                        st.notes.remove(&id);
                        if let Some(inbox) = &mut st.inbox {
                            if let Some(request) = &answered {
                                inbox.decisions.iter_mut().filter(|d| d.id() == id).for_each(|d| d.requests.retain(|r| &r.id != request));
                            }
                            inbox.decisions.retain(|d| d.id() != id || answered.is_some() && !d.requests.is_empty());
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
            Done::Replied(id, who, words, result) => {
                st.replying.remove(&id);
                match result {
                    Ok(outcome) => {
                        st.notes.remove(&id);
                        if st.replies.get(&id).is_some_and(|t| t.text.trim() == words) {
                            st.replies.remove(&id);
                        }
                        st.reply_keys.remove(&id);
                        st.replied.insert(id, outcome);
                    }
                    Err(e) => {
                        let hold = st.inbox.as_ref().and_then(|i| i.decisions.iter().find(|d| d.id() == id)).map(Decision::hold_key).unwrap_or_default();
                        let why = if e.maybe_delivered { format!("Sent to {who}, not confirmed: {}", e.message) } else { format!("Couldn't send to {who}: {}. Open the session to retry.", e.message) };
                        st.notes.insert(id, (hold, why));
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
    if let Some((request, _)) = d.recommended().filter(|_| !s.ov.inbox.acting.contains(&d.id()) && s.client.is_some()) {
        s.ov.inbox.answered.insert(d.id(), request);
    }
    act(s, d, "Accepted the recommended answer", |client, d| {
        let (request, choices) = d.recommended().expect("checked above");
        client.respond(&d.session_id, &request, choices, Default::default()).map_err(|e| (e, d.hold_key()))
    });
}

pub fn held(s: &crate::App, d: &Decision, request: &str) -> bool {
    let st = &s.ov.inbox;
    st.acting.contains(&d.id()) || st.unconfirmed.contains(&request_hold(&d.session_id, request))
}

pub fn answer(s: &mut crate::App, d: &Decision, request: &str, response: Response) {
    if held(s, d, request) {
        return;
    }
    let request = request.to_string();
    if !s.ov.inbox.acting.contains(&d.id()) && s.client.is_some() {
        s.ov.inbox.answered.insert(d.id(), request.clone());
    }
    let outcome = if response.choices.is_empty() { "Answered in your own words" } else { "Answered" };
    act(s, d, outcome, move |client, d| client.answer(&d.session_id, &request, &response).map_err(|e| (e, request_hold(&d.session_id, &request))));
}

pub fn toggle_open(s: &mut crate::App, d: &Decision, tx: &mut rune::Tx) {
    let id = d.id();
    crate::ui::waiting::put_down_composer(s, tx);
    if s.ov.inbox.open.as_ref() == Some(&id) {
        s.ov.inbox.open = None;
        return;
    }
    tx.focus_when_shown(format!("decision-answer-{id}"), false);
    s.ov.inbox.open = Some(id);
}

pub fn reply(s: &mut crate::App, d: &Decision) {
    let id = d.id();
    let words = s.ov.inbox.replies.get(&id).map(|t| t.text.trim().to_string()).unwrap_or_default();
    if words.is_empty() || d.session_id.is_empty() || s.ov.inbox.replying.contains(&id) {
        return;
    }
    let who = title_of(s, d);
    let Some(client) = s.client.clone() else {
        s.ov.inbox.notes.insert(id, (d.hold_key(), "Fixture mode: not sent".into()));
        return;
    };
    s.ov.inbox.replying.insert(id.clone());
    s.ov.inbox.notes.remove(&id);
    s.ov.inbox.replied.remove(&id);
    let message = d.parked_reply(&words);
    let key = match s.ov.inbox.reply_keys.get(&id) {
        Some((sent, key)) if *sent == message => key.clone(),
        _ => request_key(),
    };
    s.ov.inbox.reply_keys.insert(id.clone(), (message.clone(), key.clone()));
    let (tx, session) = (s.ov.inbox.tx.clone(), d.session_id.clone());
    std::thread::spawn(move || {
        let result = client.send_as(&session, &message, &[], None, None, &key).and_then(|delivered| match delivered {
            Delivered::Unconfirmed => Err(RespondError { message: "the daemon took it but hasn't confirmed it ran".into(), code: "submitted".into(), maybe_delivered: true, answered: false, reply: serde_json::Value::Null, status: 0 }),
            Delivered::Queued(_) => Ok(format!("Queued for {who}")),
            Delivered::QueuedUntilAnswer(_) => Ok(format!("Queued for {who} until it answers")),
            Delivered::Sent | Delivered::Steered | Delivered::NotSteered => Ok(format!("Sent to {who}")),
        });
        let _ = tx.send(Done::Replied(id, who, words, result));
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
