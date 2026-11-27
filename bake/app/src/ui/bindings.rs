use std::cell::RefCell;
use std::collections::HashMap;

use feed::shortcuts::{COMMANDS, Shortcuts, Stored};
use rune::{Chord, Shortcut};

struct Bound {
    saved: Shortcuts,
    chords: HashMap<&'static str, Chord>,
    generation: u64,
}

impl Bound {
    fn new(saved: Shortcuts, generation: u64) -> Bound {
        let chords = COMMANDS.iter().filter_map(|c| Some((c.id, saved.spec(c.id)?.parse().ok()?))).collect();
        Bound { saved, chords, generation }
    }
}

thread_local! {
    static BOUND: RefCell<Option<Bound>> = const { RefCell::new(None) };
}

fn with_bound<R>(f: impl FnOnce(&Bound) -> R) -> R {
    BOUND.with_borrow_mut(|b| f(b.get_or_insert_with(|| Bound::new(store::load(), 0))))
}

pub fn chord(id: &str) -> Option<Chord> {
    with_bound(|b| b.chords.get(id).copied())
}

pub fn shortcut(id: &str) -> Option<Shortcut> {
    with_bound(|b| b.saved.spec(id)?.parse().ok())
}

pub fn any_app() -> Option<Shortcut> {
    with_bound(|b| b.saved.any_app()?.spec()?.parse().ok())
}

pub fn generation() -> u64 {
    with_bound(|b| b.generation)
}

pub fn read<R>(f: impl FnOnce(&Shortcuts) -> R) -> R {
    with_bound(|b| f(&b.saved))
}

pub fn refresh() {
    let fresh = store::load();
    let generation = with_bound(|b| b.generation);
    if read(|now| *now != fresh) {
        BOUND.with_borrow_mut(|b| *b = Some(Bound::new(fresh, generation + 1)));
    }
}

pub fn change<T>(f: impl FnOnce(&mut Shortcuts) -> T) -> Result<T, String> {
    let fresh = store::load();
    let mut saved = fresh.clone();
    let out = f(&mut saved);
    if saved != fresh
        && let Err(error) = store::save(&fresh, &saved)
    {
        eprintln!("agenthail: keyboard shortcuts not saved: {error}");
        saved = fresh;
        let generation = with_bound(|b| b.generation);
        if read(|now| *now != saved) {
            BOUND.with_borrow_mut(|b| *b = Some(Bound::new(saved, generation + 1)));
        }
        return Err(error);
    }
    let generation = with_bound(|b| b.generation);
    if read(|now| *now != saved) {
        BOUND.with_borrow_mut(|b| *b = Some(Bound::new(saved, generation + 1)));
    }
    Ok(out)
}

pub fn stored(shortcut: &Shortcut) -> Option<Stored> {
    Stored::parse(&shortcut.to_string())
}

pub fn keycaps(shortcut: &Shortcut) -> String {
    shortcut.menu_keycaps().join(if cfg!(target_os = "macos") { "" } else { "+" })
}

#[cfg(all(target_os = "macos", not(test)))]
mod store {
    use feed::shortcuts::{ANY_APP_KEY, DOMAIN, KEY, Shortcuts};
    use objc2::AllocAnyThread;
    use objc2::rc::Retained;
    use objc2::runtime::AnyObject;
    use objc2_foundation::{NSBundle, NSData, NSString, NSUserDefaults};

    fn defaults() -> Option<Retained<NSUserDefaults>> {
        if NSBundle::mainBundle().bundleIdentifier().is_some_and(|id| id.to_string() == DOMAIN) {
            return Some(NSUserDefaults::standardUserDefaults());
        }
        NSUserDefaults::initWithSuiteName(NSUserDefaults::alloc(), Some(&NSString::from_str(DOMAIN)))
    }

    pub fn load() -> Shortcuts {
        let Some(defaults) = defaults() else { return Shortcuts::default() };
        let data = defaults.dataForKey(&NSString::from_str(KEY)).map(|d| d.to_vec());
        let any_app = defaults.stringForKey(&NSString::from_str(ANY_APP_KEY)).map(|s| s.to_string());
        Shortcuts::load(data.as_deref(), any_app.as_deref())
    }

    pub fn save(before: &Shortcuts, saved: &Shortcuts) -> Result<(), String> {
        let Some(defaults) = defaults() else {
            return Err(format!("the {DOMAIN} settings couldn't be opened"));
        };
        let key = NSString::from_str(KEY);
        match saved.data() {
            Some(bytes) => {
                let data = NSData::from_vec(bytes);
                let object: &AnyObject = &data;
                unsafe { defaults.setObject_forKey(Some(object), &key) };
            }
            None => defaults.removeObjectForKey(&key),
        }
        if before.any_app() == saved.any_app() {
            return Ok(());
        }
        let key = NSString::from_str(ANY_APP_KEY);
        match saved.any_app_json() {
            Some(json) => {
                let value = NSString::from_str(&json);
                let object: &AnyObject = &value;
                unsafe { defaults.setObject_forKey(Some(object), &key) };
            }
            None => defaults.removeObjectForKey(&key),
        }
        Ok(())
    }
}

#[cfg(all(not(target_os = "macos"), not(test)))]
mod store {
    use std::path::PathBuf;

    use feed::shortcuts::Shortcuts;

    fn path(name: &str) -> Option<PathBuf> {
        std::env::var_os("HOME").map(|h| PathBuf::from(h).join(".agenthail").join(name))
    }

    const COMMANDS: &str = "keyboard-shortcuts.json";
    const ANY_APP: &str = "keyboard-shortcut-any-app.json";

    pub fn load() -> Shortcuts {
        let read = |name| path(name).and_then(|p| std::fs::read(p).ok());
        let any_app = read(ANY_APP).and_then(|b| String::from_utf8(b).ok());
        Shortcuts::load(read(COMMANDS).as_deref(), any_app.as_deref())
    }

    fn write(name: &str, bytes: Option<Vec<u8>>) -> Result<(), String> {
        let path = path(name).ok_or("HOME isn't set")?;
        let result = match bytes {
            Some(bytes) => {
                let staged = path.with_extension("json.new");
                std::fs::write(&staged, bytes).and_then(|()| std::fs::rename(&staged, &path))
            }
            None => std::fs::remove_file(&path).or_else(|e| if e.kind() == std::io::ErrorKind::NotFound { Ok(()) } else { Err(e) }),
        };
        result.map_err(|e| e.to_string())
    }

    pub fn save(before: &Shortcuts, saved: &Shortcuts) -> Result<(), String> {
        write(COMMANDS, saved.data())?;
        if before.any_app() == saved.any_app() {
            return Ok(());
        }
        write(ANY_APP, saved.any_app_json().map(String::into_bytes)).inspect_err(|_| {
            let _ = write(COMMANDS, before.data());
        })
    }
}

#[cfg(test)]
mod store {
    use std::cell::RefCell;

    use feed::shortcuts::Shortcuts;

    thread_local! {
        pub static SAVED: RefCell<Shortcuts> = RefCell::new(Shortcuts::default());
        pub static FAILS: std::cell::Cell<bool> = const { std::cell::Cell::new(false) };
    }

    pub fn load() -> Shortcuts {
        SAVED.with_borrow(Clone::clone)
    }

    pub fn save(_: &Shortcuts, saved: &Shortcuts) -> Result<(), String> {
        if FAILS.get() {
            return Err("disk full".into());
        }
        SAVED.with_borrow_mut(|s| *s = saved.clone());
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use rune::{Key, KeyEvent, KeyPlatform, Mods};

    use super::*;

    #[test]
    fn a_change_rebinds_and_a_reset_restores() {
        let before = generation();
        assert!(chord("goToSession").is_some());
        let k = KeyEvent { key: Key::Command('g'), mods: Mods { cmd: true, ..Mods::default() } };
        assert_eq!(change(|s| s.set("goToSession", Stored::parse("mod+g"))), Ok(Ok(())));
        assert!(generation() > before);
        assert!(chord("goToSession").is_some_and(|c| c.matches_on(&k, KeyPlatform::Mac)));
        assert_eq!(change(|s| s.set("goToSession", Stored::parse("mod+g"))), Ok(Ok(())));
        change(Shortcuts::reset_all).unwrap();
        assert!(!chord("goToSession").is_some_and(|c| c.matches_on(&k, KeyPlatform::Mac)));
        assert_eq!(shortcut("goToSession").map(|s| s.to_string()), "mod+k".parse::<Shortcut>().ok().map(|s| s.to_string()));
    }

    #[test]
    fn a_change_starts_from_what_the_mac_app_saved_since() {
        assert_eq!(chord("refresh").map(|c| c.to_string()), Some("mod+r".parse::<Chord>().unwrap().to_string()));
        store::save(&Shortcuts::default(), &Shortcuts::load(Some(br#"{"refresh":{"carbonKeyCode":15,"carbonModifiers":768}}"#), None));
        assert_eq!(change(|s| s.set("stop", Stored::parse("mod+g"))), Ok(Ok(())));
        let saved = store::load();
        assert_eq!(saved.spec("refresh").as_deref(), Some("shift+mod+r"), "the other app's change survives");
        assert_eq!(saved.spec("stop").as_deref(), Some("mod+g"));
        assert_eq!(shortcut("refresh").map(|s| s.to_string()), "shift+mod+r".parse::<Shortcut>().ok().map(|s| s.to_string()));
        store::save(&Shortcuts::default(), &Shortcuts::default());
        let before = generation();
        refresh();
        assert!(generation() > before);
        assert_eq!(shortcut("stop").map(|s| s.to_string()), "mod+period".parse::<Shortcut>().ok().map(|s| s.to_string()));
    }

    #[test]
    fn a_change_that_cant_be_saved_is_refused_and_leaves_the_binding() {
        store::FAILS.set(true);
        assert_eq!(change(|s| s.set_any_app(Stored::parse("alt+mod+k"))), Err("disk full".to_string()));
        store::FAILS.set(false);
        assert_eq!(any_app(), None);
        assert_eq!(store::load().any_app(), None);
    }

    #[test]
    fn recorded_shortcuts_store_in_the_mac_apps_form() {
        let recorded: Shortcut = "super+shift+k".parse().unwrap();
        assert_eq!(stored(&recorded), Stored::parse("shift+mod+k"));
    }
}
