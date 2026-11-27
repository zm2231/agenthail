use feed::catalog::Session;
use feed::native_queue::{self as nq, Act};
use rune::{El, col, el, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, IconButton, IconButtonVariant, Kit, Menu, MenuItem};

use super::{Editor, act, close_editor, fetch, open_editor, reload, save_editor};
use crate::App;
use crate::icons;
use crate::theme::{self, BODY, META, accent, failed, faint, muted, wash};
use crate::ui::rail::rail_section_with;

fn small(label: &str, key: &str, disabled: bool, on: impl Fn(&mut App) + 'static, k: &Kit) -> El {
    Button::new(label).variant(ButtonVariant::Neutral).control_size(ControlSize::Small).disabled(disabled).on_press(on).build(k).key(key)
}

fn menu_key(item: &str) -> String {
    format!("codex-queue-{item}")
}

fn item_row(item: &nq::Submission, can_write: bool, busy: bool, k: &Kit) -> El {
    let shown = item.shown_text();
    let mut r = row().key(format!("codex-queue-row-{}", item.id)).w_full().gap(6.0).items_start().child(text(shown.clone()).font(k.text(META)).color(theme::text()).wrap_anywhere().max_lines(3).grow(1.0).min_w(0.0).help(shown));
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
        .on_press(move |s: &mut App| reload(s, &reload_id))
        .build(k)
        .key("codex-queue-reload");
    let trailing: Vec<El> = busy.then(|| text("Working…").font(k.text(META)).color(faint())).into_iter().chain([reload]).collect();
    let section = |body: El| rail_section_with(nq::TITLE, &icons::INBOX, trailing, body, k);
    let mut body = col().w_full().gap(8.0);
    if !current {
        return Some(section(body.child(text(nq::LOADING).font(k.text(META)).color(muted()))));
    }
    if q.loaded && q.items.is_empty() {
        body = body.child(text(nq::EMPTY).font(k.text(META)).color(muted()));
    } else if !q.loaded && let Some(error) = &q.error {
        body = body.child(text(nq::UNAVAILABLE).font(k.text(META)).color(muted()).wrap().help(error.clone()));
    } else if !q.loaded {
        body = body.child(text(nq::LOADING).font(k.text(META)).color(muted()));
    }
    body = body.children(q.items.iter().map(|i| item_row(i, can_write, busy, k)));
    if q.next_cursor.is_some() {
        let load = text("Load more").font(k.text(META)).color(if busy { faint() } else { accent() }).key("codex-queue-more");
        body = body.child(if busy { load } else { load.on_press(|s: &mut App| fetch(s, true)) });
    }
    if can_write && q.loaded {
        let mut buttons = row().w_full().flex_wrap().gap(6.0).child(small("Add…", "codex-queue-add", busy, |s: &mut App| open_editor(s, None, String::new()), k));
        if !q.items.is_empty() {
            let start = |s: &mut App| {
                act(s, Act::Start(None));
            };
            buttons = buttons.child(small("Start next", "codex-queue-start-next", busy, start, k).help("Let Codex start its next queued input"));
        }
        body = body.child(buttons);
    }
    if q.loaded && let Some(error) = &q.error {
        body = body.child(text(error.clone()).font(k.text(META)).color(failed()).wrap_anywhere());
    }
    Some(section(body))
}

fn row_menu(s: &App, item_id: &str, layer: &mut Vec<El>, k: &Kit) {
    let q = &s.native_queue.queue;
    let Some(index) = q.items.iter().position(|i| i.id == item_id) else { return };
    let item = &q.items[index];
    let run = |a: Act| {
        move |s: &mut App| {
            act(s, a.clone());
        }
    };
    let mut m = Menu::new(menu_key(item_id)).open(true).min_width(180.0).on_close(|s: &mut App| s.native_queue.menu = None);
    m = m.item(MenuItem::new("Start now").key("codex-queue-start-now").on_select(run(Act::Start(Some(item.id.clone())))));
    if let Some(original) = item.editable_text() {
        let id = item.id.clone();
        m = m.item(MenuItem::new("Edit…").key("codex-queue-edit").on_select(move |s: &mut App| open_editor(s, Some(id.clone()), original.clone())));
    }
    if q.can_reorder() {
        let shift = |by: isize| {
            let id = item.id.clone();
            move |s: &mut App| {
                if let Some(a) = s.native_queue.queue.moved(&id, by) {
                    act(s, a);
                }
            }
        };
        m = m.separator();
        m = m.item(MenuItem::new("Move up").key("codex-queue-move-up").disabled(index == 0).on_select(shift(-1)));
        m = m.item(MenuItem::new("Move down").key("codex-queue-move-down").disabled(index + 1 == q.items.len()).on_select(shift(1)));
    }
    m = m.separator().item(MenuItem::new("Delete").key("codex-queue-delete").danger(true).on_select(run(Act::Delete(item.id.clone()))));
    let _ = m.build(k, layer);
}

fn editor(e: &Editor, layer: &mut Vec<El>, k: &Kit) {
    let saving = e.saving.is_some();
    let ok = !saving && nq::can_save(&e.original, &e.text.text);
    let input = text_input(&e.text, k.text(BODY)).key("codex-queue-editor-input").grow(1.0).min_w(0.0).min_h(100.0).color(theme::text()).label("Queued input").on_edit(|s: &mut App| &mut s.native_queue.editor.as_mut().expect("queue editor is open").text);
    let field = row().w_full().px(10.0).py(7.0).radius(8.0).fill(wash()).border(1.0, theme::line()).child(input);
    let mut body = col().w_full().gap(10.0).px(16.0).pb(16.0).child(text(nq::EDITOR_NOTE).font(k.text(META)).color(muted()).wrap()).child(field);
    if let Some(error) = &e.error {
        body = body.child(text(error.clone()).font(k.text(META)).color(failed()).wrap_anywhere());
    }
    let save = match (saving, e.item.is_none(), e.unknown) {
        (true, _, _) => "Saving…",
        (false, true, true) => "Add as new",
        (false, true, false) => "Add",
        (false, false, true) => "Save as new",
        (false, false, false) => "Save",
    };
    let buttons = row()
        .w_full()
        .gap(8.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(close_editor).build(k).key("codex-queue-editor-cancel"))
        .child(Button::new(save).variant(ButtonVariant::Contrast).control_size(ControlSize::Small).disabled(!ok).on_press(save_editor).build(k).key("codex-queue-editor-save"));
    let title = if e.item.is_none() { "Add to Codex queue" } else { "Edit queued input" };
    Dialog::new("codex-queue-editor", title).open(true).fit().width(400.0).on_close(close_editor).child(body.child(buttons)).build(k, layer);
}

pub(crate) fn overlays(s: &App, layer: &mut Vec<El>, k: &Kit) {
    if let Some(id) = &s.native_queue.menu {
        row_menu(s, id, layer, k);
    }
    if let Some(e) = &s.native_queue.editor {
        editor(e, layer, k);
    }
}
