use std::collections::HashMap;
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::catalog::{RespondError, request_key};
use feed::create::Host;
use feed::extension_changes::{Change, Shown, Started, build_title};
use rune::{El, ScrollSpec, TextEdit, col, el, row, text, text_input};
use rune_kit::diff::{DiffState, DiffView, parse};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, Spinner};

use crate::App;
use crate::over::open_session;
use crate::over::ui::{BODY, META, kit, strong, weak};

const HISTORY_LIMIT: usize = 50;

enum Done {
    Changes(u64, Result<Vec<Change>, String>),
    Patch(u64, Result<Shown, String>),
    Undone(Result<(), RespondError>),
    Built(String, String, Result<Started, RespondError>),
}

pub struct History {
    host: Host,
    changes: Option<Result<Vec<Change>, String>>,
    failure: Option<String>,
    undoing: Option<String>,
    patch: Option<(String, u64, Option<Result<bool, String>>)>,
    read: u64,
}

pub struct Building {
    host: Host,
    text: TextEdit,
    starting: bool,
    failure: Option<String>,
}

pub struct Sheets {
    pub history: Option<History>,
    pub building: Option<Building>,
    pub diff: DiffState,
    unsure: HashMap<(String, String), String>,
    seq: u64,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl Sheets {
    pub fn new() -> Sheets {
        let (tx, rx) = channel();
        Sheets { history: None, building: None, diff: DiffState::new(Vec::new()), unsure: HashMap::new(), seq: 0, tx, rx }
    }

    pub fn is_open(&self) -> bool {
        self.history.is_some() || self.building.is_some()
    }
}

fn on(host: &Host) -> Option<String> {
    (!host.is_local()).then(|| host.id.clone())
}

fn next(s: &mut App) -> u64 {
    s.ov.ext.sheets.seq += 1;
    s.ov.ext.sheets.seq
}

pub fn open_history(s: &mut App, host: Host) {
    s.composer.focused = false;
    s.ov.ext.sheets.history = Some(History { host, changes: None, failure: None, undoing: None, patch: None, read: 0 });
    s.ov.ext.sheets.diff.set_files(Vec::new());
    load(s);
}

fn load(s: &mut App) {
    let Some(host) = s.ov.ext.sheets.history.as_ref().map(|h| h.host.clone()) else { return };
    let Some(client) = s.client.clone() else {
        if let Some(h) = s.ov.ext.sheets.history.as_mut() {
            h.changes = Some(Err("Fixture mode: no daemon to read history from.".into()));
        }
        return;
    };
    let (tx, seq) = (s.ov.ext.sheets.tx.clone(), next(s));
    if let Some(h) = s.ov.ext.sheets.history.as_mut() {
        h.read = seq;
    }
    std::thread::spawn(move || {
        let _ = tx.send(Done::Changes(seq, client.extension_changes(on(&host).as_deref(), HISTORY_LIMIT)));
    });
}

fn toggle_patch(s: &mut App, rev: String) {
    s.ov.ext.sheets.diff.set_files(Vec::new());
    let Some(h) = s.ov.ext.sheets.history.as_mut() else { return };
    if h.patch.as_ref().is_some_and(|(open, _, _)| *open == rev) {
        h.patch = None;
        return;
    }
    let host = h.host.clone();
    let Some(client) = s.client.clone() else { return };
    let (tx, seq) = (s.ov.ext.sheets.tx.clone(), next(s));
    if let Some(h) = s.ov.ext.sheets.history.as_mut() {
        h.patch = Some((rev.clone(), seq, None));
    }
    std::thread::spawn(move || {
        let shown = client.extension_change(on(&host).as_deref(), &rev);
        let _ = tx.send(Done::Patch(seq, shown));
    });
}

fn undo(s: &mut App, rev: String) {
    let Some(h) = s.ov.ext.sheets.history.as_mut() else { return };
    if h.undoing.is_some() {
        return;
    }
    let Some(client) = s.client.clone() else {
        s.ov.notice = Some("Fixture mode: not sent".into());
        return;
    };
    h.undoing = Some(rev.clone());
    let host = h.host.clone();
    let tx = s.ov.ext.sheets.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(Done::Undone(client.undo_extension_change(on(&host).as_deref(), &rev)));
    });
}

fn close_history(s: &mut App) {
    if s.ov.ext.sheets.history.as_ref().is_some_and(|h| h.undoing.is_none()) {
        s.ov.ext.sheets.history = None;
    }
}

pub fn open_build(s: &mut App, host: Host) {
    s.composer.focused = false;
    let mut text = TextEdit::new("");
    text.focused = true;
    s.ov.ext.sheets.building = Some(Building { host, text, starting: false, failure: None });
}

fn close_build(s: &mut App) {
    if s.ov.ext.sheets.building.as_ref().is_some_and(|b| !b.starting) {
        s.ov.ext.sheets.building = None;
    }
}

fn start(s: &mut App) {
    let Some(b) = s.ov.ext.sheets.building.as_mut() else { return };
    let message = b.text.text.trim().to_string();
    if b.starting || message.is_empty() {
        return;
    }
    let Some(client) = s.client.clone() else {
        s.ov.notice = Some("Fixture mode: not sent".into());
        return;
    };
    b.starting = true;
    b.failure = None;
    let host = b.host.clone();
    let key = s.ov.ext.sheets.unsure.get(&(host.id.clone(), message.clone())).cloned().unwrap_or_else(request_key);
    let tx = s.ov.ext.sheets.tx.clone();
    std::thread::spawn(move || {
        let result = client.build_extension(on(&host).as_deref(), &message, &key);
        let _ = tx.send(Done::Built(message, key, result));
    });
}

fn settle(s: &mut App, done: Done) {
    match done {
        Done::Changes(at, result) => {
            if let Some(h) = s.ov.ext.sheets.history.as_mut().filter(|h| h.read == at) {
                h.changes = Some(result);
            }
        }
        Done::Patch(at, result) => {
            let sheets = &mut s.ov.ext.sheets;
            let Some((_, _, shown)) = sheets.history.as_mut().and_then(|h| h.patch.as_mut()).filter(|(_, asked, _)| *asked == at) else { return };
            *shown = Some(result.map(|patch| {
                sheets.diff.set_files(parse(&patch.patch));
                patch.truncated
            }));
        }
        Done::Undone(result) => {
            s.ov.ext.sheets.diff.set_files(Vec::new());
            let host = match s.ov.ext.sheets.history.as_mut() {
                Some(h) => {
                    h.undoing = None;
                    h.failure = result.as_ref().err().map(|e| if e.maybe_delivered { format!("The undo may have gone through anyway: {}", e.message) } else { e.message.clone() });
                    h.patch = None;
                    Some(h.host.clone())
                }
                None => None,
            };
            load(s);
            if let Some(host) = host {
                crate::over::extensions::relist(s, &host);
            }
        }
        Done::Built(message, key, result) => {
            let sheets = &mut s.ov.ext.sheets;
            let Some(b) = sheets.building.as_mut() else { return };
            let asked = (b.host.id.clone(), message);
            match &result {
                Err(e) if e.maybe_delivered => {
                    sheets.unsure.insert(asked, key);
                }
                Err(e) if !e.answered => {}
                _ => {
                    sheets.unsure.remove(&asked);
                }
            }
            b.starting = false;
            let host = b.host.clone();
            match result {
                Ok(started) => {
                    s.ov.ext.sheets.building = None;
                    crate::over::extensions::relist(s, &host);
                    if let Some(id) = started.session_id() {
                        open_session(s, &id);
                        if let Some(failure) = &started.failure {
                            s.ov.notice = Some(format!("The builder session was made, but it may not have started: {failure}"));
                        } else if started.unconfirmed() {
                            s.ov.notice = Some("The builder session was made, but the daemon hasn't confirmed its first message ran yet.".into());
                        }
                    }
                }
                Err(e) if e.maybe_delivered => b.failure = Some(format!("It may have started anyway: {}. Check your sessions before trying again.", e.message)),
                Err(e) => b.failure = Some(e.message),
            }
        }
    }
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok(done) = s.ov.ext.sheets.rx.try_recv() {
        settle(s, done);
        changed = true;
    }
    changed
}

pub fn escape(s: &mut App) -> bool {
    if s.ov.ext.sheets.building.is_some() {
        close_build(s);
        return true;
    }
    if s.ov.ext.sheets.history.is_some() {
        close_history(s);
        return true;
    }
    false
}

fn small(label: &str, variant: ButtonVariant) -> Button {
    Button::new(label).variant(variant).control_size(ControlSize::Small)
}

fn change_row(s: &App, h: &History, change: &Change) -> El {
    let k = kit();
    let busy = h.undoing.is_some();
    let open = h.patch.as_ref().is_some_and(|(rev, _, _)| *rev == change.rev);
    let offset = |t: time::OffsetDateTime| rune::time::LocalTime::at(t.unix_timestamp() * 1000).offset;
    let mut actions = row().gap(6.0).items_center();
    let rev = change.rev.clone();
    actions = actions.child(small(if open { "Hide patch" } else { "Show patch" }, ButtonVariant::Ghost).on_press(move |s: &mut App| toggle_patch(s, rev.clone())).build(k).key(format!("ext-change-patch-{}", change.rev)));
    if !change.root {
        let rev = change.rev.clone();
        let label = if h.undoing.as_deref() == Some(change.rev.as_str()) { "Undoing…" } else { "Undo" };
        actions = actions.child(small(label, ButtonVariant::Neutral).disabled(busy).help("Put the extension files back as they were before this change").on_press(move |s: &mut App| undo(s, rev.clone())).build(k).key(format!("ext-change-undo-{}", change.rev)));
    }
    let mut card = col()
        .key(format!("ext-change-{}", change.rev))
        .w_full()
        .gap(6.0)
        .py(8.0)
        .child(
            row()
                .w_full()
                .gap(10.0)
                .items_start()
                .child(col().grow(1.0).shrink().min_w(0.0).gap(2.0).child(text(change.describe()).font(BODY).color(strong()).wrap().w_full()).child(text(change.meta(crate::ui::background::clock(), offset)).font(META).color(weak()).wrap().w_full()))
                .child(actions),
        );
    if let Some((_, _, shown)) = h.patch.as_ref().filter(|_| open) {
        card = card.child(match shown {
            None => row().gap(6.0).items_center().child(Spinner::new().size(11.0).build(k)).child(text("Reading the patch…".to_string()).font(META).color(weak())),
            Some(Err(error)) => text(error.clone()).font(META).color(crate::theme::failed()).wrap().w_full(),
            Some(Ok(truncated)) => {
                let mut patch = col().w_full().gap(4.0);
                if s.ov.ext.sheets.diff.files().is_empty() {
                    patch = patch.child(text("This change has no file differences to show.".to_string()).font(META).color(weak()));
                } else {
                    patch = patch.child(el().w_full().h(320.0).radius(8.0).border(1.0, crate::over::ui::edge()).child(DiffView::new(|s: &App| &s.ov.ext.sheets.diff, |s: &mut App| &mut s.ov.ext.sheets.diff).key("ext-change-diff").build(s, k)));
                }
                if *truncated {
                    patch = patch.child(text("Cut: the change is larger than this view.".to_string()).font(META).color(weak()));
                }
                patch
            }
        });
    }
    card
}

fn history_dialog(s: &App, h: &History, layer: &mut Vec<El>) {
    let k = kit();
    let note = if h.host.is_local() { "Changes are kept as they are made. Undo any change here.".to_string() } else { format!("Changes on {}. Undo any change here.", h.host.name) };
    let mut body = col().w_full().gap(8.0).px(16.0).pb(16.0);
    if let Some(failure) = &h.failure {
        body = body.child(text(failure.clone()).font(META).color(crate::theme::failed()).wrap().w_full());
    }
    body = body.child(match &h.changes {
        None => row().w_full().h(120.0).items_center().justify_center().child(Spinner::new().size(14.0).build(k)),
        Some(Err(error)) => text(error.clone()).font(META).color(crate::theme::failed()).wrap().w_full(),
        Some(Ok(changes)) if changes.is_empty() => text("No changes recorded yet.".to_string()).font(BODY).color(weak()),
        Some(Ok(changes)) => col().key("ext-history-list").w_full().max_h(460.0).scroll(ScrollSpec { stick_bottom: false }).children(changes.iter().map(|c| change_row(s, h, c))),
    });
    let done = row().w_full().child(el().grow(1.0)).child(small("Done", ButtonVariant::Contrast).disabled(h.undoing.is_some()).on_press(close_history).build(k).key("ext-history-done"));
    Dialog::new("ext-history", "Extension History").description(note).open(true).fit().width(620.0).on_close(close_history).child(body.child(done)).build(k, layer);
}

fn build_dialog(b: &Building, layer: &mut Vec<El>) {
    let k = kit();
    let input = text_input(&b.text, BODY)
        .key("ext-build-message")
        .label("What the extension should do".to_string())
        .w_full()
        .min_h(120.0)
        .px(10.0)
        .py(7.0)
        .radius(8.0)
        .fill(crate::theme::wash())
        .border(1.0, crate::theme::line())
        .color(strong())
        .placeholder("A board of open work across my projects".to_string(), weak())
        .on_edit(|s: &mut App| match s.ov.ext.sheets.building.as_mut() {
            Some(b) => &mut b.text,
            None => &mut s.composer,
        });
    let mut body = col().w_full().gap(10.0).px(16.0).pb(16.0).child(input);
    if let Some(failure) = &b.failure {
        body = body.child(text(failure.clone()).font(META).color(crate::theme::failed()).wrap().w_full());
    }
    let empty = b.text.text.trim().is_empty();
    let buttons = row()
        .w_full()
        .gap(8.0)
        .child(el().grow(1.0))
        .child(small("Cancel", ButtonVariant::Ghost).disabled(b.starting).on_press(close_build).build(k).key("ext-build-cancel"))
        .child(small(if b.starting { "Starting…" } else { "Start Builder" }, ButtonVariant::Contrast).disabled(b.starting || empty).on_press(start).build(k).key("ext-build-start"));
    let title = build_title((!b.host.is_local()).then_some(b.host.name.as_str()));
    Dialog::new("ext-build", title)
        .description("Describe what it should show or do. A builder session starts, and its changes are kept as it makes them.")
        .open(true)
        .fit()
        .width(480.0)
        .on_close(close_build)
        .child(body.child(buttons))
        .build(k, layer);
}

pub fn overlays(s: &App, layer: &mut Vec<El>) {
    if let Some(h) = &s.ov.ext.sheets.history {
        history_dialog(s, h, layer);
    }
    if let Some(b) = &s.ov.ext.sheets.building {
        build_dialog(b, layer);
    }
}
