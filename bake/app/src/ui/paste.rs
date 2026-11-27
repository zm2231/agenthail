use feed::paste::{self, Piece};
use rune::{ClipboardContent, El, Key, KeyEvent, Mark, MarkRange, PillStyle, TextEdit, Tx, col, row, text, text_input};
use rune_kit::{Button, ButtonVariant};

use crate::App;
use crate::theme::{self, BODY, META, faint, line, wash};

pub struct Editing {
    marker: String,
    pending: String,
    edit: TextEdit,
}

pub fn edit(draft: &str) -> TextEdit {
    let store = paste::shared();
    let mut text = String::with_capacity(draft.len());
    let mut marks = Vec::new();
    for piece in store.pieces(draft) {
        match piece {
            Piece::Plain(plain) => text.push_str(&plain),
            Piece::Token(marker) => {
                let start = text.len();
                text.push_str(&paste::label(store.text(&marker).unwrap_or_default()));
                marks.push(MarkRange { start, end: text.len(), mark: Mark::Pill(marker) });
            }
        }
    }
    let mut out = TextEdit::new(text);
    if !marks.is_empty() {
        out.set_marks(marks);
    }
    out
}

// An open edit lives under its own marker so every draft read, save and merge carries it.
pub fn draft(s: &App) -> String {
    let mut store = paste::shared();
    let Some(open) = &s.paste else { return store.draft(&s.composer.text, s.composer.pills()) };
    store.restore(&open.pending, &open.edit.text);
    store.draft(&s.composer.text, s.composer.pills().map(|(r, id)| (r, if id == open.marker { open.pending.as_str() } else { id })))
}

pub fn expand(draft: &str) -> String {
    paste::shared().expand(draft)
}

pub fn set_composer(s: &mut App, draft: &str) {
    finish(s);
    let focused = s.composer.focused;
    s.composer = edit(draft);
    s.composer.focused = focused;
}

fn insert(s: &mut App, content: &ClipboardContent) -> bool {
    let pasted = content.text();
    if !paste::is_long(pasted) {
        return false;
    }
    let Some(marker) = paste::shared().add(pasted) else { return false };
    s.composer.insert_pill(marker, &paste::label(pasted));
    true
}

pub fn field(input: El) -> El {
    input
        .on_input_paste(|s: &mut App, content, _: &mut Tx| super::attach::paste(s, content) || insert(s, content))
        .on_pill_press(|s: &mut App, id, _, tx: &mut Tx| open(s, id, tx))
        .pill_copy_text(|id| paste::shared().text(id).map(str::to_string))
        .pill_style(|_| PillStyle::new().bg(wash()).fg(theme::text()).ring(line(), 1.0).radius(5.0))
}

fn open(s: &mut App, marker: &str, tx: &mut Tx) -> bool {
    finish(s);
    let Some(pasted) = paste::shared().text(marker).map(str::to_string) else { return false };
    let Some(pending) = paste::shared().add(&pasted) else { return false };
    let mut edit = TextEdit::new(pasted);
    edit.focused = true;
    s.composer.focused = false;
    s.paste = Some(Editing { marker: marker.to_string(), pending, edit });
    tx.focus("paste-editor", false);
    true
}

pub fn unsent_draft(u: &feed::composer::Unsent) -> String {
    u.draft.clone().filter(|d| expand(d).trim() == u.text).unwrap_or_else(|| u.text.clone())
}

pub fn settle(s: &mut App) {
    if s.paste.as_ref().is_some_and(|p| !p.edit.focused) {
        finish(s);
    }
}

fn done(s: &mut App, tx: &mut Tx) {
    finish(s);
    s.composer.focused = true;
    tx.focus("composer", false);
}

pub fn finish(s: &mut App) {
    let Some(done) = s.paste.take() else { return };
    let edited = done.edit.text.clone();
    paste::shared().restore(&done.pending, &edited);
    let Some(range) = s.composer.pills().find(|(_, id)| *id == done.marker).map(|(r, _)| r) else { return };
    if paste::shared().text(&done.marker) == Some(edited.as_str()) {
        return;
    }
    let (caret, before) = (s.composer.focus, s.composer.text.len());
    let marker = (!edited.is_empty()).then_some(done.pending);
    match marker {
        Some(marker) => s.composer.replace_with_pill(range.clone(), marker, &paste::label(&edited)),
        None => {
            s.composer.set_selection(range.start, range.end);
            s.composer.insert(&edited);
        }
    }
    let after = s.composer.text.len();
    s.composer.set_caret(if caret > range.start { (caret + after).saturating_sub(before) } else { caret });
}

fn key(s: &mut App, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let closes = ev.key == Key::Escape || ev.key == Key::Enter && (ev.mods.cmd || ev.mods.ctrl);
    if closes {
        done(s, tx);
    }
    closes
}

pub fn editor(s: &App) -> Option<El> {
    let editing = s.paste.as_ref()?;
    let input = text_input(&editing.edit, BODY.mono())
        .key("paste-editor")
        .label("Pasted text")
        .w_full()
        .h(320.0)
        .px(10.0)
        .py(8.0)
        .radius(6.0)
        .fill(wash())
        .border(1.0, line())
        .color(theme::text())
        .on_edit(|s: &mut App| &mut s.paste.as_mut().expect("the pasted text is open").edit)
        .on_input_key(key);
    let done = Button::new("Done").variant(ButtonVariant::Submit).build(theme::kit()).key("paste-done").on_press_tx(done);
    Some(
        col()
            .key("paste-edit")
            .w_full()
            .gap(8.0)
            .padding(10.0)
            .child(input)
            .child(row().w_full().items_center().child(text(paste::label(&editing.edit.text)).font(META).color(faint())).child(super::spacer()).child(done)),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_draft_with_a_token_shows_its_label_as_a_pill_and_sends_the_paste() {
        let pasted = "line\n".repeat(12);
        let marker = paste::shared().add(&pasted).unwrap();
        let e = edit(&format!("before {marker} after"));
        assert_eq!(e.text, "before Pasted text · 12 lines after");
        assert_eq!(e.pills().map(|(_, id)| id.to_string()).collect::<Vec<_>>(), vec![marker.clone()]);
        assert_eq!(paste::shared().draft(&e.text, e.pills()), format!("before {marker} after"));
    }

    #[test]
    fn a_plain_draft_stays_plain() {
        let e = edit("just words");
        assert_eq!(e.text, "just words");
        assert_eq!(e.pills().count(), 0);
        assert_eq!(paste::shared().draft(&e.text, e.pills()), "just words");
    }
}
