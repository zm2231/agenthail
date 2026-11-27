use std::collections::HashMap;
use std::time::{Duration, Instant};

use feed::catalog::Session;
use rune::{Chord, Key, KeyEvent, Mods, Tx};
use serde_json::{Value, json};

use crate::App;
use crate::over::search;

#[cfg(target_os = "macos")]
fn double_escape() -> Duration {
    Duration::from_secs_f64(objc2_app_kit::NSEvent::doubleClickInterval())
}

#[cfg(not(target_os = "macos"))]
fn double_escape() -> Duration {
    Duration::from_millis(500)
}

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
    escaped: Option<(String, Instant)>,
    stash: HashMap<String, String>,
}

impl State {
    pub fn to_json(&self) -> Value {
        json!(self.stash)
    }

    pub fn texts(&self) -> impl Iterator<Item = &str> {
        self.stash.values().map(String::as_str)
    }

    pub fn from_json(value: &Value) -> State {
        let stash = value.as_object().into_iter().flatten().filter_map(|(id, text)| Some((id.clone(), text.as_str()?.to_string()))).collect();
        State { escaped: None, stash }
    }

    pub fn escape_handled(&mut self) {
        self.escaped = None;
    }

    pub fn rekey(&mut self, old: &str, new: &str) {
        if let Some(moving) = self.stash.remove(old) {
            let text = match self.stash.remove(new) {
                Some(existing) if !existing.is_empty() && existing != moving => format!("{existing}\n\n{moving}"),
                _ => moving,
            };
            self.stash.insert(new.to_string(), text);
        }
    }
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
        let text = super::paste::draft(&s.composer);
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
    if crate::over::composer_target(s).as_deref() == Some(id) && search::can_stop(s, id) {
        s.complete.keys.escaped = None;
        crate::stop(s, id);
        return;
    }
    let now = Instant::now();
    if !s.complete.keys.escaped.take().is_some_and(|(at_id, at)| at_id == id && now.duration_since(at) < double_escape()) {
        s.complete.keys.escaped = Some((id.to_string(), now));
        return;
    }
    if !super::complete::clear_into_history(s, id) {
        super::complete::open_search(s, id);
    }
}

fn held(s: &App, id: &str) -> bool {
    s.complete.keys.stash.contains_key(id) || super::attach::stashing(s, id)
}

fn stash(s: &mut App, id: &str) -> bool {
    super::paste::finish(s);
    let empty = s.composer.text.is_empty() && !super::attach::has(s, id);
    if empty && !held(s, id) {
        return false;
    }
    let back = s.complete.keys.stash.remove(id).unwrap_or_default();
    if !s.composer.text.is_empty() {
        s.complete.keys.stash.insert(id.to_string(), super::paste::draft(&s.composer));
    }
    super::attach::swap_stash(s, id);
    super::complete::replace_composer(s, &back);
    true
}

pub fn restore_after_send(s: &mut App, id: &str) {
    if s.composer_for.as_deref() == Some(id) && s.composer.text.is_empty() && !super::attach::has(s, id) && held(s, id) {
        stash(s, id);
    }
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
    fn a_stashed_draft_survives_a_restart_and_follows_its_session() {
        let mut held = State::default();
        held.stash.insert("old".into(), "set aside".into());
        let mut back = State::from_json(&held.to_json());
        back.rekey("old", "new");
        assert_eq!(back.stash.get("new").map(String::as_str), Some("set aside"));
        assert!(!back.stash.contains_key("old"));
        assert!(State::from_json(&Value::Null).stash.is_empty());
        back.stash.insert("older".into(), "another".into());
        back.rekey("older", "new");
        assert_eq!(back.stash.get("new").map(String::as_str), Some("set aside\n\nanother"), "two stashes meeting on one session both stay, as drafts do");
        back.stash.insert("same".into(), "set aside\n\nanother".into());
        back.rekey("same", "new");
        assert_eq!(back.stash.len(), 1);
        assert_eq!(back.stash.get("new").map(String::as_str), Some("set aside\n\nanother"));
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
