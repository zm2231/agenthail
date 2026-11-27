use feed::paste::{self, Piece};
use rune::{ClipboardContent, El, Key, KeyEvent, Mark, MarkRange, PillStyle, TextEdit, Tx, col, row, text, text_input};
use rune_kit::{Button, ButtonVariant};

use crate::App;
use crate::theme::{self, BODY, META, faint, line, wash};

pub struct Editing {
    marker: String,
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

pub fn draft(e: &TextEdit) -> String {
    paste::shared().draft(&e.text, e.pills())
}

pub fn expanded(e: &TextEdit) -> String {
    let store = paste::shared();
    store.expand(&store.draft(&e.text, e.pills()))
}

pub fn expand(draft: &str) -> String {
    paste::shared().expand(draft)
}

pub fn set_composer(s: &mut App, draft: &str) {
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
    let Some(pasted) = paste::shared().text(marker).map(str::to_string) else { return false };
    finish(s);
    let mut edit = TextEdit::new(pasted);
    edit.focused = true;
    s.composer.focused = false;
    s.paste = Some(Editing { marker: marker.to_string(), edit });
    tx.focus("paste-editor", false);
    true
}

pub fn finish(s: &mut App) {
    let Some(done) = s.paste.take() else { return };
    s.composer.focused = true;
    let Some(range) = s.composer.pills().find(|(_, id)| *id == done.marker).map(|(r, _)| r) else { return };
    let edited = done.edit.text.clone();
    if paste::shared().text(&done.marker) == Some(edited.as_str()) {
        return;
    }
    let (caret, before) = (s.composer.focus, s.composer.text.len());
    let marker = if edited.is_empty() { None } else { paste::shared().add(&edited) };
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

fn key(s: &mut App, ev: &KeyEvent, _: &mut Tx) -> bool {
    let done = ev.key == Key::Escape || ev.key == Key::Enter && (ev.mods.cmd || ev.mods.ctrl);
    if done {
        finish(s);
    }
    done
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
    let done = Button::new("Done").variant(ButtonVariant::Submit).build(theme::kit()).key("paste-done").on_press(finish);
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
        assert_eq!(draft(&e), format!("before {marker} after"));
        assert_eq!(expanded(&e), format!("before {pasted} after"));
    }

    #[test]
    fn a_plain_draft_stays_plain() {
        let e = edit("just words");
        assert_eq!(e.text, "just words");
        assert_eq!(e.pills().count(), 0);
        assert_eq!(expanded(&e), "just words");
    }
}
