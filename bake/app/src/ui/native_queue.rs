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
}

pub struct NativeQueue {
    queue: Queue,
    keys: Keys,
    ops: u64,
    seen: Option<(String, String)>,
    menu: Option<String>,
    pub editor: Option<Editor>,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl NativeQueue {
    pub fn new() -> NativeQueue {
        let (tx, rx) = channel();
        NativeQueue { queue: Queue::default(), keys: Keys::default(), ops: 0, seen: None, menu: None, editor: None, tx, rx }
    }
}

fn showing(s: &App, id: &str) -> bool {
    s.screen == crate::Screen::Session && s.selected.as_deref() == Some(id)
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

fn status(s: &App, id: &str) -> Option<(String, String)> {
    session(s, id).map(|x| (x.id.clone(), x.status.clone()))
}

pub fn load(s: &mut App, id: &str) {
    if !session(s, id).is_some_and(nq::shown) {
        return;
    }
    s.native_queue.seen = status(s, id);
    s.native_queue.queue.reset(id);
    s.native_queue.menu = None;
    fetch(s, false);
}

pub fn changed(s: &mut App, id: &str) {
    let now = status(s, id);
    if now.is_none() || now == s.native_queue.seen {
        return;
    }
    s.native_queue.seen = now;
    if s.native_queue.queue.invalidate(id) {
        fetch(s, false);
    }
}

fn reload(s: &mut App, id: &str) {
    s.native_queue.queue.reset(id);
    fetch(s, false);
}

fn fetch(s: &mut App, more: bool) {
    if s.client.is_none() {
        return;
    }
    let cursor = match (more, s.native_queue.queue.next_cursor.clone()) {
        (false, _) => None,
        (true, Some(c)) => Some(c),
        (true, None) => return,
    };
    let Some((id, generation)) = s.native_queue.queue.begin() else { return };
    let read = s.native_queue.keys.read_started();
    spawn(s, move |c| Done::Listed { result: c.native_queue_list(&id, cursor.as_deref()).map_err(|e| e.message), session: id, generation, read, more });
}

fn act(s: &mut App, act: Act) -> Option<u64> {
    s.native_queue.menu = None;
    if s.client.is_none() {
        s.ov.notice = Some("Fixture mode: not sent".into());
        return None;
    }
    let id = s.native_queue.queue.session.clone()?;
    if !writable(s, &id) || s.native_queue.queue.busy {
        return None;
    }
    let key = s.native_queue.keys.key(&id, &act);
    let (id, generation) = s.native_queue.queue.begin()?;
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
            if s.native_queue.queue.listed(&session, generation, more, result) && s.native_queue.queue.take_dirty() {
                fetch(s, false);
            }
        }
        Done::Changed { session, generation, op, key, act, outcome } => {
            s.native_queue.keys.settled(&key, &outcome);
            let editing = s.native_queue.editor.as_ref().is_some_and(|e| e.saving == Some(op));
            if editing && let Some(e) = s.native_queue.editor.as_mut() {
                e.saving = None;
                e.error = outcome.error().map(str::to_string);
            }
            if !s.native_queue.queue.mutated(&session, generation, &outcome) {
                if s.native_queue.queue.take_dirty() {
                    fetch(s, false);
                }
                return;
            }
            if editing {
                s.native_queue.editor = None;
            }
            if matches!(act, Act::Start(_)) {
                crate::ask_overview(s, crate::Ask::Refresh);
            }
            fetch(s, false);
        }
    }
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok(done) = s.native_queue.rx.try_recv() {
        changed = true;
        settled(s, done);
    }
    if let Some(id) = s.selected.clone().filter(|id| showing(s, id) && s.native_queue.queue.session.as_deref() != Some(id.as_str()))
        && s.client.is_some()
        && session(s, &id).is_some_and(nq::shown)
    {
        load(s, &id);
        changed = true;
    }
    let gone = |s: &App, id: &str| !showing(s, id) || !writable(s, id);
    if s.native_queue.editor.as_ref().is_some_and(|e| gone(s, &e.session)) {
        s.native_queue.editor = None;
        changed = true;
    }
    if s.native_queue.menu.is_some() && s.native_queue.queue.session.as_deref().is_none_or(|id| gone(s, id)) {
        s.native_queue.menu = None;
        changed = true;
    }
    changed
}

fn open_editor(s: &mut App, item: Option<String>, original: String) {
    let Some(id) = s.native_queue.queue.session.clone() else { return };
    s.native_queue.menu = None;
    let mut text = TextEdit::new(&original);
    text.focused = true;
    s.composer.focused = false;
    s.native_queue.editor = Some(Editor { session: id, item, original, text, saving: None, error: None });
}

fn close_editor(s: &mut App) {
    s.native_queue.editor = None;
}

fn save_editor(s: &mut App) {
    let Some(e) = s.native_queue.editor.as_ref() else { return };
    if e.saving.is_some() || !nq::can_save(&e.original, &e.text.text) || s.native_queue.queue.busy {
        return;
    }
    let typed = e.text.text.trim().to_string();
    let a = match &e.item {
        Some(id) => Act::Update { id: id.clone(), text: typed },
        None => Act::Add(typed),
    };
    let op = act(s, a);
    if let Some(e) = s.native_queue.editor.as_mut().filter(|_| op.is_some()) {
        e.saving = op;
        e.error = None;
    }
}
