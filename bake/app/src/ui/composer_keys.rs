use std::collections::HashMap;
use std::time::{Duration, Instant};

use feed::catalog::Session;
use rune::{Chord, Key, KeyEvent, Mods, Tx};

use crate::App;
use crate::over::search;

const DOUBLE_ESCAPE: Duration = Duration::from_millis(500);

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Edit {
    Newline,
    DeleteToLineStart,
    DeleteWordBackward,
    Paste,
    SendNow,
    CycleMode,
    Interrupt,
    Stash,
}

pub const COMPOSER: [(Edit, &str); 8] = [
    (Edit::Newline, "ctrl+j"),
    (Edit::DeleteToLineStart, "ctrl+u"),
    (Edit::DeleteWordBackward, "ctrl+w"),
    (Edit::Paste, "ctrl+v"),
    (Edit::SendNow, "ctrl+enter"),
    (Edit::CycleMode, "shift+tab"),
    (Edit::Interrupt, "escape"),
    (Edit::Stash, "ctrl+s"),
];

#[derive(Default)]
pub struct State {
    escaped: Option<Instant>,
    stash: HashMap<String, String>,
}

pub fn find(matches: impl Fn(&Chord) -> bool) -> Option<Edit> {
    COMPOSER.iter().find(|(_, spec)| matches(&Chord::parse(spec).expect("a built-in shortcut parses"))).map(|(edit, _)| *edit)
}

pub fn key(s: &mut App, x: &Session, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if super::keys::in_field(s, ev, tx) {
        return true;
    }
    let Some(edit) = find(|c| c.matches_tx(ev, tx)) else { return false };
    if edit != Edit::Interrupt {
        s.complete.keys.escaped = None;
    }
    let id = x.id.as_str();
    match edit {
        Edit::Newline => s.composer.insert("\n"),
        Edit::DeleteToLineStart => edit_key(s, Mods { cmd: true, ..Mods::default() }),
        Edit::DeleteWordBackward => edit_key(s, Mods { alt: true, ..Mods::default() }),
        Edit::Paste => tx.request_paste(),
        Edit::SendNow => send_now(s, x, tx),
        Edit::CycleMode => return cycle_mode(s, id),
        Edit::Interrupt => interrupt(s, id),
        Edit::Stash => return stash(s, id),
    }
    true
}

fn edit_key(s: &mut App, mods: Mods) {
    s.composer.key(&KeyEvent { key: Key::Backspace, mods }, None);
}

fn send_now(s: &mut App, x: &Session, tx: &mut Tx) {
    if s.composer.text.trim().is_empty() && !super::attach::has(s, &x.id) {
        return;
    }
    if crate::busy_now(s, &x.id) && x.capabilities.steer {
        let text = s.composer.text.clone();
        crate::steer_text(s, &x.id, text, tx);
    } else {
        super::complete::submit(s, &x.id, tx);
    }
}

fn cycle_mode(s: &mut App, id: &str) -> bool {
    let chips = super::session_menu::chips_for(s, id);
    let Some(next) = chips.iter().find(|c| c.key == "mode").and_then(|c| c.cycled()) else { return false };
    super::session_menu::set(s, id, "mode", &next.id);
    true
}

fn interrupt(s: &mut App, id: &str) {
    if search::selected(s).as_deref() == Some(id) && search::can_stop(s, id) {
        s.complete.keys.escaped = None;
        crate::stop(s, id);
        return;
    }
    let now = Instant::now();
    if !s.complete.keys.escaped.take().is_some_and(|at| now.duration_since(at) < DOUBLE_ESCAPE) {
        s.complete.keys.escaped = Some(now);
        return;
    }
    if !super::complete::clear_into_history(s, id) {
        super::complete::open_search(s, id);
    }
}

fn stash(s: &mut App, id: &str) -> bool {
    let held = if s.composer.text.is_empty() {
        match s.complete.keys.stash.remove(id) {
            Some(held) => held,
            None => return false,
        }
    } else {
        let current = s.composer.text.clone();
        let back = s.complete.keys.stash.remove(id).unwrap_or_default();
        s.complete.keys.stash.insert(id.to_string(), current);
        back
    };
    super::complete::replace_composer(s, &held);
    true
}

#[cfg(test)]
mod tests {
    use rune::{KeyPlatform, TextEdit};

    use super::*;

    fn press(key: Key, mods: Mods) -> Option<Edit> {
        let ev = KeyEvent { key, mods };
        find(|c| c.matches_on(&ev, KeyPlatform::Mac))
    }

    const CTRL: Mods = Mods { shift: false, cmd: false, alt: false, ctrl: true, logo: false };

    #[test]
    fn the_composer_takes_the_mac_apps_control_keys() {
        assert_eq!(press(Key::Text("j".into()), CTRL), Some(Edit::Newline));
        assert_eq!(press(Key::Text("u".into()), CTRL), Some(Edit::DeleteToLineStart));
        assert_eq!(press(Key::Text("w".into()), CTRL), Some(Edit::DeleteWordBackward));
        assert_eq!(press(Key::Text("v".into()), CTRL), Some(Edit::Paste));
        assert_eq!(press(Key::Enter, CTRL), Some(Edit::SendNow));
        assert_eq!(press(Key::Tab, Mods { shift: true, ..Mods::default() }), Some(Edit::CycleMode));
        assert_eq!(press(Key::Escape, Mods::default()), Some(Edit::Interrupt));
        assert_eq!(press(Key::Text("s".into()), CTRL), Some(Edit::Stash));
    }

    #[test]
    fn plain_typing_and_the_fields_own_keys_pass_through() {
        assert_eq!(press(Key::Text("j".into()), Mods::default()), None);
        assert_eq!(press(Key::Enter, Mods::default()), None, "Return sends");
        assert_eq!(press(Key::Enter, Mods { shift: true, ..Mods::default() }), None, "Shift-Return is a new line");
        assert_eq!(press(Key::Tab, Mods::default()), None, "Tab completes");
        assert_eq!(press(Key::Text("a".into()), CTRL), None, "Control-A stays the field's");
        assert_eq!(press(Key::Text("k".into()), CTRL), None, "Control-K stays the field's");
        assert_eq!(press(Key::Text("r".into()), CTRL), None, "Control-R searches what was sent");
        assert_eq!(press(Key::Escape, Mods { shift: true, ..Mods::default() }), None);
    }

    #[test]
    fn delete_keys_edit_like_the_mac_text_system() {
        let mut edit = TextEdit::new("first line\nsecond word");
        edit.key(&KeyEvent { key: Key::Backspace, mods: Mods { alt: true, ..Mods::default() } }, None);
        assert_eq!(edit.text, "first line\nsecond ");
        edit.key(&KeyEvent { key: Key::Backspace, mods: Mods { cmd: true, ..Mods::default() } }, None);
        assert_eq!(edit.text, "first line\n");
    }
}
