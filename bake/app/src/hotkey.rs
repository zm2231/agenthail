use std::cell::{OnceCell, RefCell};
use std::sync::mpsc::{Receiver, Sender};

use feed::hotkey::{Ask, Hotkey, Refusal, Registrar, Return, Window};
use feed::shortcuts::{ANY_APP, ANY_APP_ID, Stored};
use rune::{AppHandle, Shortcut, ShortcutError, ShortcutId, Tx};

use crate::App;
use crate::ui::bindings;

thread_local! {
    static HANDLE: OnceCell<AppHandle<App>> = const { OnceCell::new() };
    static WORKER: OnceCell<Sender<(Ask, Option<Stored>)>> = const { OnceCell::new() };
    static REFUSED: RefCell<Option<(Refusal, Option<Stored>)>> = const { RefCell::new(None) };
    static RETURN: RefCell<Return> = RefCell::new(Return::default());
    static SENT: RefCell<Option<Option<Stored>>> = const { RefCell::new(None) };
    static OFF: RefCell<Option<String>> = const { RefCell::new(None) };
}

struct Live<'a>(&'a AppHandle<App>);

impl Registrar for Live<'_> {
    type Id = ShortcutId;

    fn register(&mut self, shortcut: Stored) -> Result<ShortcutId, Refusal> {
        let parsed = chord(shortcut).ok_or(Refusal::Invalid)?;
        self.0.register_shortcut_named(parsed, ANY_APP_ID, ANY_APP, pressed).map_err(refusal)
    }

    fn unregister(&mut self, id: ShortcutId) -> Result<(), Refusal> {
        self.0.unregister_shortcut(id).map_err(|error| Refusal::Failed(format!("the previous shortcut couldn't be released ({error})")))
    }
}

fn chord(shortcut: Stored) -> Option<Shortcut> {
    shortcut.global_spec()?.parse().ok()
}

fn refusal(error: ShortcutError) -> Refusal {
    match error {
        ShortcutError::Conflict => Refusal::Taken(None),
        ShortcutError::PermissionDenied => Refusal::Denied,
        ShortcutError::Unsupported => Refusal::Unsupported,
        ShortcutError::InvalidAccelerator(_) => Refusal::Invalid,
        other => Refusal::Failed(other.to_string()),
    }
}

fn run(handle: AppHandle<App>, asks: Receiver<(Ask, Option<Stored>)>) {
    let mut state = Hotkey::default();
    while let Ok(first) = asks.recv() {
        let Some(asked) = feed::hotkey::latest(std::iter::once(first).chain(asks.try_iter())) else { continue };
        if state.ask(&mut Live(&handle), asked) {
            let want = asked.shortcut;
            let refused = state.refused().cloned();
            handle.update(move |_, _| shown(refused, want));
        }
    }
}

fn shown(refused: Option<Refusal>, tried: Option<Stored>) {
    let named = match refused {
        Some(Refusal::Taken(None)) => {
            let owner = HANDLE.with(|h| Some(h.get()?.shortcut_owner(&chord(tried?)?)?));
            Some(Refusal::Taken(owner))
        }
        other => other,
    };
    REFUSED.set(named.map(|r| (r, tried)));
    crate::settings::redraw();
}

pub fn keep(handle: AppHandle<App>) {
    let (tx, rx) = crate::wake::to_thread();
    let worker = handle.clone();
    let spawned = std::thread::Builder::new().name("agenthail-shortcut".into()).spawn(move || run(worker, rx));
    if let Err(error) = spawned {
        eprintln!("agenthail: the shortcut from any app is off: {error}");
        OFF.set(Some(error.to_string()));
        return;
    }
    HANDLE.with(|h| {
        let _ = h.set(handle);
    });
    WORKER.with(|w| {
        let _ = w.set(tx);
    });
}

fn ask(ask: Ask) {
    let want = bindings::read(|sc| sc.any_app());
    if ask == Ask::Want && SENT.with_borrow(|sent| *sent == Some(want)) {
        return;
    }
    WORKER.with(|w| {
        if let Some(tx) = w.get() {
            if tx.send((ask, want)).is_ok() {
                SENT.set(Some(want));
            }
        }
    });
}

pub fn sync() {
    ask(Ask::Want);
}

pub fn chose() {
    ask(Ask::Choose);
}

pub fn tick() {
    RETURN.with_borrow_mut(Return::tick);
}

pub fn closed(window: Window) {
    RETURN.with_borrow_mut(|r| r.closed(window));
}

pub fn focus(window: Window, focused: bool) {
    if RETURN.with_borrow_mut(|r| r.focus(window, focused)) {
        bindings::refresh();
        ask(Ask::Regain);
    }
}

fn current() -> Option<(Refusal, Option<Shortcut>)> {
    let now = bindings::read(|sc| sc.any_app());
    let refused = REFUSED.with_borrow(|r| r.clone().filter(|(_, tried)| *tried == now))?;
    Some((refused.0, now.and_then(chord)))
}

pub fn asks_again() -> bool {
    current().is_some_and(|(refusal, _)| !matches!(refusal, Refusal::Taken(_)))
}

pub fn keycaps(shortcut: &Shortcut) -> String {
    shortcut.keycaps().join(if cfg!(target_os = "macos") { "" } else { "+" })
}

pub fn note() -> Option<String> {
    if let Some(error) = OFF.with_borrow(Clone::clone) {
        return Some(format!("Shortcuts from other apps are off: {error}."));
    }
    current().map(|(refusal, shortcut)| feed::hotkey::note(&refusal, &shortcut.map_or_else(|| "The shortcut".to_string(), |s| keycaps(&s))))
}

fn pressed(s: &mut App, tx: &mut Tx) {
    HANDLE.with(|h| {
        let Some(handle) = h.get() else { return };
        match tx.activation_token() {
            Some(token) => handle.activate(token),
            None => handle.open(),
        }
    });
    if s.ov.jump.is_none() && !covered(s) {
        crate::over::open_jump(s, tx);
    }
}

fn covered(s: &App) -> bool {
    let menus = &s.menus;
    crate::ui::window::open_in(s, crate::ui::window::MAIN)
        || menus.open.is_some()
        || menus.chip.is_some()
        || menus.renaming.is_some()
        || menus.confirm.is_some()
        || s.attach.menu.is_some()
        || (s.screen == crate::Screen::Sessions && (s.ov.list.status_open || s.ov.list.surface_open || s.ov.sessions_project_open))
        || s.holder_menu.is_some()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn shortcut_errors_read_as_the_keyboard_page_says_them() {
        assert_eq!(refusal(ShortcutError::Conflict), Refusal::Taken(None));
        assert_eq!(refusal(ShortcutError::PermissionDenied), Refusal::Denied);
        assert_eq!(refusal(ShortcutError::Unsupported), Refusal::Unsupported);
        assert_eq!(refusal(ShortcutError::InvalidAccelerator("space".into())), Refusal::Invalid);
        assert_eq!(refusal(ShortcutError::NotRunning), Refusal::Failed("the shortcut's app is not running".into()));
    }

    #[test]
    fn every_stored_shortcut_from_any_app_parses_for_rune() {
        for spec in ["alt+mod+k", "ctrl+alt+space", "shift+mod+f12", "ctrl+backquote"] {
            assert!(chord(Stored::parse(spec).unwrap()).is_some(), "{spec}");
        }
    }
}
