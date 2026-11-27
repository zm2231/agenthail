use std::collections::HashMap;
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::native_queue::{self as nq, Act, Keys, Outcome, Page, Queue};
use rune::TextEdit;

use crate::{App, session};

mod view;

pub(crate) use view::{overlays, section};

pub enum Done {
    Listed { session: String, generation: u64, read: u64, more: bool, result: Result<Page, String> },
    Changed { session: String, generation: u64, op: u64, key: String, act: Act, outcome: Outcome },
}

pub struct Editor {
    pub session: String,
    pub item: Option<String>,
    pub original: String,
    pub text: TextEdit,
    pub saving: Option<u64>,
    pub error: Option<String>,
    pub unknown: bool,
}

pub struct NativeQueue {
    queues: HashMap<String, Queue>,
    keys: Keys,
    ops: u64,
    generations: u64,
    menu: Option<(String, String)>,
    pub editor: Option<Editor>,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl NativeQueue {
    #[cfg(test)]
    pub fn row_menu(&self) -> Option<(&str, &str)> {
        self.menu.as_ref().map(|(session, item)| (session.as_str(), item.as_str()))
    }

    pub fn new() -> NativeQueue {
        let (tx, rx) = channel();
        NativeQueue { queues: HashMap::new(), keys: Keys::default(), ops: 0, generations: 0, menu: None, editor: None, tx, rx }
    }
}

pub fn covering(s: &App) -> bool {
    s.native_queue.menu.is_some() || s.native_queue.editor.is_some()
}

pub fn queue<'a>(s: &'a App, id: &str) -> Option<&'a Queue> {
    s.native_queue.queues.get(id)
}

fn writable(s: &App, id: &str) -> bool {
    session(s, id).is_some_and(nq::writable)
}

fn spawn(s: &App, job: impl FnOnce(&feed::catalog::Client) -> Done + Send + 'static) {
    let Some(client) = s.client.clone() else { return };
    let tx = s.native_queue.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(job(&client));
    });
}

#[cfg(test)]
pub fn hold(s: &mut App, id: &str) {
    s.native_queue.queues.insert(id.to_string(), Queue::of(id, 1));
}

#[cfg(test)]
pub fn reading(s: &App, id: &str) -> bool {
    s.native_queue.queues.get(id).is_some_and(|q| q.busy)
}

pub fn load(s: &mut App, id: &str) {
    if !session(s, id).is_some_and(nq::shown) {
        return;
    }
    if !s.native_queue.queues.contains_key(id) {
        s.native_queue.generations += 1;
        let q = Queue::of(id, s.native_queue.generations);
        s.native_queue.queues.insert(id.to_string(), q);
    }
    fetch(s, id, false);
}

pub fn changed(s: &mut App, id: &str) {
    if s.native_queue.queues.get_mut(id).is_some_and(|q| q.invalidate(id)) {
        fetch(s, id, false);
    }
}

fn reload(s: &mut App, id: &str) {
    fetch(s, id, false);
}

fn fetch(s: &mut App, id: &str, more: bool) {
    if s.client.is_none() {
        return;
    }
    let Some(q) = s.native_queue.queues.get_mut(id) else { return };
    let cursor = match (more, q.next_cursor.clone()) {
        (false, _) => None,
        (true, Some(c)) => Some(c),
        (true, None) => return,
    };
    let Some((id, generation)) = q.begin() else { return };
    let read = s.native_queue.keys.read_started();
    spawn(s, move |c| Done::Listed { result: c.native_queue_list(&id, cursor.as_deref()).map_err(|e| e.message), session: id, generation, read, more });
}

fn act(s: &mut App, id: &str, act: Act) -> Option<u64> {
    s.native_queue.menu = None;
    if s.client.is_none() {
        s.ov.notice = Some("Fixture mode: not sent".into());
        return None;
    }
    if !writable(s, id) || queue(s, id).is_none_or(|q| q.busy) {
        return None;
    }
    let key = s.native_queue.keys.key(id, &act);
    let (id, generation) = s.native_queue.queues.get_mut(id)?.begin()?;
    s.native_queue.ops += 1;
    let op = s.native_queue.ops;
    spawn(s, move |c| Done::Changed { outcome: Outcome::of(c.native_queue(&id, &act, &key)), session: id, generation, op, key, act });
    Some(op)
}

fn settled(s: &mut App, done: Done) {
    match done {
        Done::Listed { session, generation, read, more, result } => {
            if result.is_ok() {
                s.native_queue.keys.observed(&session, read);
            }
            if s.native_queue.queues.get_mut(&session).is_some_and(|q| q.listed(&session, generation, more, result) && q.take_dirty()) {
                fetch(s, &session, false);
            }
        }
        Done::Changed { session, generation, op, key, act, outcome } => {
            s.native_queue.keys.settled(&key, &outcome);
            let editing = s.native_queue.editor.as_ref().is_some_and(|e| e.saving == Some(op));
            if editing && let Some(e) = s.native_queue.editor.as_mut() {
                e.saving = None;
                e.error = outcome.error().map(str::to_string);
                e.unknown = matches!(outcome, Outcome::Unknown(_));
            }
            let Some(q) = s.native_queue.queues.get_mut(&session).filter(|q| q.current(&session, generation)) else { return };
            if !q.mutated(&session, generation, &outcome) {
                if q.take_dirty() || matches!(outcome, Outcome::Unknown(_)) {
                    fetch(s, &session, false);
                }
                return;
            }
            if editing {
                s.native_queue.editor = None;
            }
            if matches!(act, Act::Start(_)) {
                crate::ask_overview(s, crate::Ask::Refresh);
            }
            fetch(s, &session, false);
        }
    }
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok(done) = s.native_queue.rx.try_recv() {
        changed = true;
        settled(s, done);
    }
    let shown: Vec<String> = super::window::shown_sessions(s).into_iter().filter(|id| session(s, id).is_some_and(nq::shown)).collect();
    if s.client.is_some() {
        for id in shown.iter().filter(|id| !s.native_queue.queues.contains_key(id.as_str())).cloned().collect::<Vec<_>>() {
            load(s, &id);
            changed = true;
        }
    }
    let before = s.native_queue.queues.len();
    s.native_queue.queues.retain(|id, _| shown.contains(id));
    changed |= s.native_queue.queues.len() != before;
    let gone = |s: &App, id: &str| !shown.iter().any(|x| x == id) || !writable(s, id);
    if s.native_queue.editor.as_ref().is_some_and(|e| gone(s, &e.session)) {
        s.native_queue.editor = None;
        changed = true;
    }
    if s.native_queue.menu.as_ref().is_some_and(|(id, _)| gone(s, id)) {
        s.native_queue.menu = None;
        changed = true;
    }
    changed
}

pub fn open_row_menu(s: &mut App, session: &str, item: &str) {
    if !super::window::may_open(s, super::window::Kind::Queue) {
        return;
    }
    let open = (session.to_string(), item.to_string());
    s.native_queue.menu = if s.native_queue.menu.as_ref() == Some(&open) { None } else { Some(open) };
}

fn open_editor(s: &mut App, id: &str, item: Option<String>, original: String) {
    if !super::window::may_open(s, super::window::Kind::Queue) || !s.native_queue.queues.contains_key(id) {
        return;
    }
    let id = id.to_string();
    s.native_queue.menu = None;
    let mut text = TextEdit::new(&original);
    text.focused = true;
    s.composer.focused = false;
    s.native_queue.editor = Some(Editor { session: id, item, original, text, saving: None, error: None, unknown: false });
}

fn close_editor(s: &mut App) {
    s.native_queue.editor = None;
}

fn save_editor(s: &mut App) {
    let Some(e) = s.native_queue.editor.as_ref() else { return };
    if e.saving.is_some() || !nq::can_save(&e.original, &e.text.text) || queue(s, &e.session).is_none_or(|q| q.busy) {
        return;
    }
    let typed = e.text.text.trim().to_string();
    let a = match &e.item {
        Some(id) => Act::Update { id: id.clone(), text: typed },
        None => Act::Add(typed),
    };
    let session = e.session.clone();
    if e.unknown {
        s.native_queue.keys.abandon(&session, &a);
    }
    let op = act(s, &session, a);
    if let Some(e) = s.native_queue.editor.as_mut().filter(|_| op.is_some()) {
        e.saving = op;
        e.error = None;
    }
}
