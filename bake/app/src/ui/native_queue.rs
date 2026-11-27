use std::sync::mpsc::{Receiver, Sender, channel};

use feed::catalog::Session;
use feed::native_queue::{self as nq, Act, Keys, Outcome, Page, Queue};
use rune::{El, TextEdit, col, el, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, IconButton, IconButtonVariant, Kit, Menu, MenuItem};

use super::rail::rail_section;
use crate::icons;
use crate::theme::{self, BODY, META, accent, failed, faint, muted, wash};
use crate::{App, session};

pub enum Done {
    Listed { session: String, generation: u64, more: bool, result: Result<Page, String> },
    Changed { session: String, generation: u64, act: Act, outcome: Outcome },
}

pub struct Editor {
    pub session: String,
    pub item: Option<String>,
    pub original: String,
    pub text: TextEdit,
    pub saving: bool,
    pub error: Option<String>,
}

pub struct NativeQueue {
    queue: Queue,
    keys: Keys,
    menu: Option<String>,
    pub editor: Option<Editor>,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl NativeQueue {
    pub fn new() -> NativeQueue {
        let (tx, rx) = channel();
        NativeQueue { queue: Queue::default(), keys: Keys::default(), menu: None, editor: None, tx, rx }
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

pub fn load(s: &mut App, id: &str) {
    if !session(s, id).is_some_and(nq::shown) {
        return;
    }
    s.native_queue.queue.reset(id);
    s.native_queue.menu = None;
    fetch(s, false);
}

pub fn changed(s: &mut App, id: &str) {
    if s.native_queue.queue.session.as_deref() == Some(id) && s.native_queue.queue.loaded {
        fetch(s, false);
    }
}

fn fetch(s: &mut App, more: bool) {
    if s.client.is_none() {
        return;
    }
    let cursor = if more {
        match s.native_queue.queue.next_cursor.clone() {
            Some(c) => Some(c),
            None => return,
        }
    } else {
        None
    };
    let Some((id, generation)) = s.native_queue.queue.begin() else { return };
    spawn(s, move |c| Done::Listed { result: c.native_queue_list(&id, cursor.as_deref()).map_err(|e| e.message), session: id, generation, more });
}

fn act(s: &mut App, act: Act) {
    s.native_queue.menu = None;
    if s.client.is_none() {
        s.ov.notice = Some("Fixture mode: not sent".into());
        return;
    }
    let Some(id) = s.native_queue.queue.session.clone() else { return };
    if !writable(s, &id) || s.native_queue.queue.busy {
        return;
    }
    let key = s.native_queue.keys.key(&id, &act);
    let Some((id, generation)) = s.native_queue.queue.begin() else { return };
    spawn(s, move |c| Done::Changed { outcome: Outcome::of(c.native_queue(&id, &act, &key)), session: id, generation, act });
}

fn settled(s: &mut App, done: Done) {
    match done {
        Done::Listed { session, generation, more, result } => {
            if result.is_ok() {
                s.native_queue.keys.observed(&session);
            }
            s.native_queue.queue.listed(&session, generation, more, result);
        }
        Done::Changed { session, generation, act, outcome } => {
            s.native_queue.keys.settled(&session, &act, &outcome);
            let editing = s.native_queue.editor.as_ref().is_some_and(|e| e.session == session && e.saving);
            let done = s.native_queue.queue.mutated(&session, generation, &outcome);
            if editing && let Some(e) = s.native_queue.editor.as_mut() {
                e.saving = false;
                e.error = match &outcome {
                    Outcome::Unknown(error) | Outcome::Failed(error) => Some(error.clone()),
                    Outcome::Done => None,
                };
            }
            if !done {
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
    s.native_queue.editor = Some(Editor { session: id, item, original, text, saving: false, error: None });
}

fn close_editor(s: &mut App) {
    s.native_queue.editor = None;
}

fn save_editor(s: &mut App) {
    let Some(e) = s.native_queue.editor.as_mut() else { return };
    if e.saving || !nq::can_save(&e.original, &e.text.text) || s.native_queue.queue.busy {
        return;
    }
    let typed = e.text.text.trim().to_string();
    let a = match &e.item {
        Some(id) => Act::Update { id: id.clone(), text: typed },
        None => Act::Add(typed),
    };
    e.saving = true;
    act(s, a);
    if !s.native_queue.queue.busy && let Some(e) = s.native_queue.editor.as_mut() {
        e.saving = false;
    }
}

fn small(label: &str, key: &str, disabled: bool, on: impl Fn(&mut App) + 'static, k: &Kit) -> El {
    Button::new(label).variant(ButtonVariant::Neutral).control_size(ControlSize::Small).disabled(disabled).on_press(on).build(k).key(key)
}

fn menu_key(item: &str) -> String {
    format!("codex-queue-{item}")
}

fn item_row(item: &nq::Submission, can_write: bool, busy: bool, k: &Kit) -> El {
    let shown = item.shown_text();
    let mut r = row().key(format!("codex-queue-row-{}", item.id)).w_full().gap(6.0).items_start().child(text(shown.clone()).font(META).color(theme::text()).wrap_anywhere().max_lines(3).grow(1.0).min_w(0.0).help(shown));
    if can_write {
        let id = item.id.clone();
        let more = IconButton::new(&icons::MORE)
            .variant(IconButtonVariant::GhostMuted)
            .control_size(ControlSize::Small)
            .label("Queue item actions")
            .disabled(busy)
            .on_press(move |s: &mut App| s.native_queue.menu = if s.native_queue.menu.as_deref() == Some(id.as_str()) { None } else { Some(id.clone()) })
            .build(k)
            .key(format!("{}-trigger", menu_key(&item.id)));
        r = r.child(more);
    }
    r
}

pub(crate) fn section(s: &App, x: &Session, k: &Kit) -> Option<El> {
    if !nq::shown(x) {
        return None;
    }
    let q = &s.native_queue.queue;
    let current = q.session.as_deref() == Some(x.id.as_str());
    let can_write = nq::writable(x);
    let busy = current && q.busy;
    let reload_id = x.id.clone();
    let reload = IconButton::new(&icons::ROTATE_CW)
        .variant(IconButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .label("Reload Codex queue")
        .help("Reload Codex's queue")
        .disabled(busy)
        .on_press(move |s: &mut App| {
            s.native_queue.queue.reset(&reload_id);
            fetch(s, false);
        })
        .build(k)
        .key("codex-queue-reload");
    let top = row().w_full().gap(6.0).items_center().children(busy.then(|| text("Working…").font(META).color(faint()))).child(el().grow(1.0)).child(reload);
    let mut body = col().w_full().gap(8.0).child(top);
    if !current {
        return Some(rail_section(nq::TITLE, &icons::INBOX, body));
    }
    if q.loaded && q.items.is_empty() {
        body = body.child(text(nq::EMPTY).font(META).color(muted()));
    } else if !q.loaded && let Some(error) = &q.error {
        body = body.child(text(nq::UNAVAILABLE).font(META).color(muted()).wrap().help(error.clone()));
    }
    body = body.children(q.items.iter().map(|i| item_row(i, can_write, busy, k)));
    if q.next_cursor.is_some() {
        let load = text("Load more").font(META).color(if busy { faint() } else { accent() }).key("codex-queue-more");
        body = body.child(if busy { load } else { load.on_press(|s: &mut App| fetch(s, true)) });
    }
    if can_write && q.loaded {
        let mut buttons = row().w_full().flex_wrap().gap(6.0).child(small("Add…", "codex-queue-add", busy, |s: &mut App| open_editor(s, None, String::new()), k));
        if !q.items.is_empty() {
            buttons = buttons.child(small("Start next", "codex-queue-start-next", busy, |s: &mut App| act(s, Act::Start(None)), k).help("Let Codex start its next queued input"));
        }
        body = body.child(buttons);
    }
    if q.loaded && let Some(error) = &q.error {
        body = body.child(text(error.clone()).font(META).color(failed()).wrap_anywhere());
    }
    Some(rail_section(nq::TITLE, &icons::INBOX, body))
}

fn row_menu(s: &App, item_id: &str, layer: &mut Vec<El>, k: &Kit) {
    let q = &s.native_queue.queue;
    let Some(index) = q.items.iter().position(|i| i.id == item_id) else { return };
    let item = &q.items[index];
    let id = item.id.clone();
    let mut m = Menu::new(menu_key(item_id)).open(true).min_width(180.0).on_close(|s: &mut App| s.native_queue.menu = None);
    m = m.item(MenuItem::new("Start now").key("codex-queue-start-now").on_select(move |s: &mut App| act(s, Act::Start(Some(id.clone())))));
    if let Some(original) = item.editable_text() {
        let id = item.id.clone();
        m = m.item(MenuItem::new("Edit…").key("codex-queue-edit").on_select(move |s: &mut App| open_editor(s, Some(id.clone()), original.clone())));
    }
    if q.can_reorder() {
        let (up, down) = (item.id.clone(), item.id.clone());
        m = m.separator();
        m = m.item(MenuItem::new("Move up").key("codex-queue-move-up").disabled(index == 0).on_select(move |s: &mut App| {
            if let Some(a) = s.native_queue.queue.moved(&up, -1) {
                act(s, a);
            }
        }));
        m = m.item(MenuItem::new("Move down").key("codex-queue-move-down").disabled(index + 1 == q.items.len()).on_select(move |s: &mut App| {
            if let Some(a) = s.native_queue.queue.moved(&down, 1) {
                act(s, a);
            }
        }));
    }
    let id = item.id.clone();
    m = m.separator().item(MenuItem::new("Delete").key("codex-queue-delete").danger(true).on_select(move |s: &mut App| act(s, Act::Delete(id.clone()))));
    let _ = m.build(k, layer);
}

fn editor(e: &Editor, layer: &mut Vec<El>, k: &Kit) {
    let ok = !e.saving && nq::can_save(&e.original, &e.text.text);
    let input = text_input(&e.text, BODY).key("codex-queue-editor-input").grow(1.0).min_w(0.0).min_h(100.0).color(theme::text()).label("Queued input").on_edit(|s: &mut App| &mut s.native_queue.editor.as_mut().expect("queue editor is open").text);
    let field = row().w_full().px(10.0).py(7.0).radius(8.0).fill(wash()).border(1.0, theme::line()).child(input);
    let mut body = col().w_full().gap(10.0).px(16.0).pb(16.0).child(text(nq::EDITOR_NOTE).font(META).color(muted()).wrap()).child(field);
    if let Some(error) = &e.error {
        body = body.child(text(error.clone()).font(META).color(failed()).wrap_anywhere());
    }
    let save = if e.saving { "Saving…" } else if e.item.is_none() { "Add" } else { "Save" };
    let buttons = row()
        .w_full()
        .gap(8.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(close_editor).build(k).key("codex-queue-editor-cancel"))
        .child(Button::new(save).variant(ButtonVariant::Contrast).control_size(ControlSize::Small).disabled(!ok).on_press(save_editor).build(k).key("codex-queue-editor-save"));
    let title = if e.item.is_none() { "Add to Codex queue" } else { "Edit queued input" };
    Dialog::new("codex-queue-editor", title).open(true).fit().width(400.0).on_close(close_editor).child(body.child(buttons)).build(k, layer);
}

pub fn overlays(s: &App, layer: &mut Vec<El>, k: &Kit) {
    if let Some(id) = &s.native_queue.menu {
        row_menu(s, id, layer, k);
    }
    if let Some(e) = &s.native_queue.editor {
        editor(e, layer, k);
    }
}
