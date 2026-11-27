use std::collections::HashMap;

use feed::shortcuts::{ANY_APP, ANY_APP_ID, COMMANDS, Group, Shortcuts};
use rune::{El, Shortcut, ShortcutError, el, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Kit, ShortcutRecorder, ShortcutRecorderState, shortcut_error_message};

use super::Prefs;
use super::parts::{self, line, section};
use crate::ui::bindings;

#[derive(Default)]
pub struct Form {
    rows: HashMap<&'static str, ShortcutRecorderState>,
    any_app: ShortcutRecorderState,
    kept: Vec<(&'static str, &'static str)>,
    kept_at: u64,
}

pub const PAGE: usize = 2;

pub fn opened(s: &mut Prefs) {
    if s.tab != super::GENERAL_TAB || s.general_page != PAGE {
        return;
    }
    bindings::refresh();
    s.keyboard.kept.clear();
    sync_rows(s);
}

fn sync_rows(s: &mut Prefs) {
    if !s.keyboard.any_app.is_recording() {
        s.keyboard.any_app = ShortcutRecorderState::new(bindings::any_app());
    }
    for c in COMMANDS {
        let state = s.keyboard.rows.entry(c.id).or_default();
        if !state.is_recording() {
            *state = ShortcutRecorderState::new(bindings::shortcut(c.id));
        }
    }
}

fn row_state<'a>(s: &'a mut Prefs, id: &'static str) -> &'a mut ShortcutRecorderState {
    s.keyboard.rows.entry(id).or_insert_with(|| ShortcutRecorderState::new(bindings::shortcut(id)))
}

fn composer(id: &str) -> bool {
    feed::shortcuts::command(id).is_some_and(|c| c.group == Group::Composer && id != "stop")
}

fn propose(id: &'static str, next: Option<&Shortcut>) -> Result<(), ShortcutError> {
    let stored = match next {
        None => None,
        Some(shortcut) => Some(bindings::stored(shortcut).ok_or_else(|| ShortcutError::InvalidAccelerator(shortcut.to_string()))?),
    };
    if stored.is_some_and(|s| s.types()) {
        return Err(ShortcutError::InvalidAccelerator(next.map(Shortcut::to_string).unwrap_or_default()));
    }
    bindings::change(|sc| sc.set(id, stored)).map_err(|_| ShortcutError::Conflict)
}

fn propose_any_app(next: Option<&Shortcut>) -> Result<(), ShortcutError> {
    let stored = match next {
        None => None,
        Some(shortcut) => Some(bindings::stored(shortcut).ok_or_else(|| ShortcutError::InvalidAccelerator(shortcut.to_string()))?),
    };
    let changed = bindings::change(|sc| sc.set_any_app(stored)).map_err(|_| ShortcutError::Conflict);
    crate::hotkey::sync();
    changed
}

fn message(id: &'static str, error: &ShortcutError, shortcut: Option<&Shortcut>) -> String {
    let Some(shortcut) = shortcut else { return shortcut_error_message(error, None) };
    let keys = bindings::keycaps(shortcut);
    match error {
        ShortcutError::Conflict => match bindings::stored(shortcut).and_then(|s| bindings::read(|sc| sc.owner(s, id))) {
            Some(owner) => format!("{keys} is already used by {owner}."),
            None => shortcut_error_message(error, Some(shortcut)),
        },
        ShortcutError::InvalidAccelerator(_) if bindings::stored(shortcut).is_some_and(|s| s.types()) => format!("{keys} types in the message box. Add ⌘, ⌃ or ⌥."),
        _ => shortcut_error_message(error, Some(shortcut)),
    }
}

fn reset(s: &mut Prefs, id: &'static str) {
    if bindings::change(|sc| sc.reset(id)).is_ok() {
        *row_state(s, id) = ShortcutRecorderState::new(bindings::shortcut(id));
    }
}

fn restore(s: &mut Prefs) {
    s.keyboard.kept = bindings::change(Shortcuts::reset_all);
    s.keyboard.kept_at = bindings::generation();
    s.keyboard.rows.clear();
    s.keyboard.any_app = ShortcutRecorderState::default();
    sync_rows(s);
    crate::hotkey::sync();
}

fn any_app_state(s: &mut Prefs) -> &mut ShortcutRecorderState {
    &mut s.keyboard.any_app
}

fn any_app_row(s: &Prefs, k: &Kit) -> El {
    let recorder = ShortcutRecorder::new("settings-shortcut-any-app", &s.keyboard.any_app, any_app_state)
        .label(ANY_APP.to_string())
        .width(180.0)
        .on_change(|_: &mut Prefs, next, _| propose_any_app(next))
        .error_message(|error, shortcut| message(ANY_APP_ID, error, shortcut))
        .build(k);
    let row = line(ANY_APP, Some("Brings Agenthail forward with the session palette open, from any app.".to_string()), recorder, k);
    section("", vec![row], crate::hotkey::note(), k)
}

fn command_row(s: &Prefs, id: &'static str, title: &str, k: &Kit) -> El {
    let fresh;
    let state = match s.keyboard.rows.get(id) {
        Some(state) => state,
        None => {
            fresh = ShortcutRecorderState::new(bindings::shortcut(id));
            &fresh
        }
    };
    let mut recorder = ShortcutRecorder::new(format!("settings-shortcut-{id}"), state, move |s: &mut Prefs| row_state(s, id))
        .label(title.to_string())
        .width(180.0)
        .on_change(move |_: &mut Prefs, next, _| propose(id, next))
        .error_message(move |error, shortcut| message(id, error, shortcut));
    if composer(id) {
        recorder = recorder.allow_unmodified();
    }
    let mut controls = row().gap(8.0).items_center();
    if bindings::read(|sc| sc.customized(id)) {
        let blocker = bindings::read(|sc| sc.default_blocker(id));
        let help = blocker.map_or_else(|| "Use the default shortcut.".to_string(), |owner| format!("The default is used by {owner}."));
        let reset = Button::new("Reset")
            .variant(ButtonVariant::Ghost)
            .control_size(ControlSize::Small)
            .disabled(blocker.is_some())
            .help(help)
            .on_press(move |s: &mut Prefs| reset(s, id))
            .build(k)
            .key(format!("settings-shortcut-reset-{id}"));
        controls = controls.child(reset);
    }
    line(title, None, controls.child(recorder.build(k)), k)
}

pub fn page(s: &Prefs, k: &Kit) -> El {
    let mut out = parts::page().child(any_app_row(s, k));
    for group in Group::ALL {
        let rows = COMMANDS.iter().filter(|c| c.group == group).map(|c| command_row(s, c.id, c.title, k)).collect();
        out = out.child(section(group.title(), rows, None, k));
    }
    let restore = Button::new("Restore Defaults")
        .control_size(ControlSize::Small)
        .disabled(!bindings::read(Shortcuts::any_customized))
        .on_press(restore)
        .build(k)
        .key("settings-shortcuts-restore");
    let current = s.keyboard.kept_at == bindings::generation();
    for (title, owner) in s.keyboard.kept.iter().filter(|_| current) {
        out = out.child(text(format!("{title} kept its shortcut: its default is used by {owner}.")).font(k.text(crate::theme::META)).color(crate::theme::muted()).wrap().px(4.0));
    }
    out.child(row().w_full().child(el().grow(1.0)).child(restore))
}
