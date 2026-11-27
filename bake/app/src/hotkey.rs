use std::cell::{OnceCell, RefCell};

use feed::hotkey::{Hotkey, Refusal, Registrar};
use feed::shortcuts::{ANY_APP, ANY_APP_ID, Stored};
use rune::{AppHandle, Shortcut, ShortcutError, ShortcutId, Tx};

use crate::App;
use crate::ui::bindings;

thread_local! {
    static HANDLE: OnceCell<AppHandle<App>> = const { OnceCell::new() };
    static STATE: RefCell<Hotkey<ShortcutId>> = RefCell::new(Hotkey::default());
}

struct Live<'a>(&'a AppHandle<App>);

impl Registrar for Live<'_> {
    type Id = ShortcutId;

    fn register(&mut self, shortcut: Stored) -> Result<ShortcutId, Refusal> {
        let parsed: Shortcut = shortcut.spec().and_then(|s| s.parse().ok()).ok_or(Refusal::Invalid)?;
        self.0.register_shortcut_named(parsed.clone(), ANY_APP_ID, ANY_APP, pressed).map_err(|e| refusal(e, || self.0.shortcut_owner(&parsed)))
    }

    fn unregister(&mut self, id: ShortcutId) {
        if let Err(error) = self.0.unregister_shortcut(id) {
            eprintln!("agenthail: couldn't release the shortcut from any app: {error}");
        }
    }
}

fn refusal(error: ShortcutError, owner: impl FnOnce() -> Option<String>) -> Refusal {
    match error {
        ShortcutError::Conflict => Refusal::Taken(owner()),
        ShortcutError::PermissionDenied => Refusal::Denied,
        ShortcutError::Unsupported => Refusal::Unsupported,
        ShortcutError::InvalidAccelerator(_) => Refusal::Invalid,
        other => Refusal::Failed(other.to_string()),
    }
}

pub fn keep(handle: AppHandle<App>) {
    HANDLE.with(|h| {
        let _ = h.set(handle);
    });
}

fn with_live(f: impl FnOnce(&mut Hotkey<ShortcutId>, &mut Live) -> bool) {
    let changed = HANDLE.with(|h| {
        let Some(handle) = h.get() else { return false };
        STATE.with_borrow_mut(|state| f(state, &mut Live(handle)))
    });
    if changed {
        crate::settings::redraw();
    }
}

pub fn sync() {
    let want = bindings::read(|sc| sc.any_app());
    with_live(|state, live| state.want(live, want));
}

pub fn regained() {
    bindings::refresh();
    sync();
    with_live(|state, live| state.retry(live));
}

pub fn note() -> Option<String> {
    let keys = bindings::any_app().map(|s| bindings::keycaps(&s))?;
    STATE.with_borrow(|state| state.refused().map(|r| feed::hotkey::note(r, &keys)))
}

fn pressed(s: &mut App, tx: &mut Tx) {
    HANDLE.with(|h| {
        let Some(handle) = h.get() else { return };
        match tx.activation_token() {
            Some(token) => handle.activate(token),
            None => handle.open(),
        }
    });
    if s.ov.jump.is_none() {
        crate::over::open_jump(s, tx);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn shortcut_errors_read_as_the_keyboard_page_says_them() {
        assert_eq!(refusal(ShortcutError::Conflict, || Some("Spotlight".into())), Refusal::Taken(Some("Spotlight".into())));
        assert_eq!(refusal(ShortcutError::Conflict, || None), Refusal::Taken(None));
        assert_eq!(refusal(ShortcutError::PermissionDenied, || unreachable!()), Refusal::Denied);
        assert_eq!(refusal(ShortcutError::Unsupported, || unreachable!()), Refusal::Unsupported);
        assert_eq!(refusal(ShortcutError::InvalidAccelerator("space".into()), || unreachable!()), Refusal::Invalid);
        assert_eq!(refusal(ShortcutError::NotRunning, || unreachable!()), Refusal::Failed("the shortcut's app is not running".into()));
    }

    #[test]
    fn every_stored_shortcut_from_any_app_parses_for_rune() {
        for spec in ["alt+mod+k", "ctrl+alt+space", "shift+mod+f12", "ctrl+backquote"] {
            let stored = Stored::parse(spec).unwrap();
            assert!(stored.spec().and_then(|s| s.parse::<Shortcut>().ok()).is_some(), "{spec}");
        }
    }
}
