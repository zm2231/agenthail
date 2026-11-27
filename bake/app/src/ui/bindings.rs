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

pub fn generation() -> u64 {
    with_bound(|b| b.generation)
}

pub fn read<R>(f: impl FnOnce(&Shortcuts) -> R) -> R {
    with_bound(|b| f(&b.saved))
}

pub fn change<T>(f: impl FnOnce(&mut Shortcuts) -> T) -> T {
    let (mut saved, generation) = with_bound(|b| (b.saved.clone(), b.generation));
    let out = f(&mut saved);
    if read(|now| *now != saved) {
        store::save(&saved);
        BOUND.with_borrow_mut(|b| *b = Some(Bound::new(saved, generation + 1)));
    }
    out
}

pub fn stored(shortcut: &Shortcut) -> Option<Stored> {
    Stored::parse(&shortcut.to_string())
}

pub fn keycaps(shortcut: &Shortcut) -> String {
    shortcut.menu_keycaps().join(if cfg!(target_os = "macos") { "" } else { "+" })
}

#[cfg(all(target_os = "macos", not(test)))]
mod store {
    use feed::shortcuts::{DOMAIN, KEY, Shortcuts};
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
        let data = defaults().and_then(|d| d.dataForKey(&NSString::from_str(KEY))).map(|d| d.to_vec());
        Shortcuts::load(data.as_deref())
    }

    pub fn save(saved: &Shortcuts) {
        let Some(defaults) = defaults() else {
            eprintln!("agenthail: keyboard shortcuts not saved: the {DOMAIN} settings couldn't be opened");
            return;
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
    }
}

#[cfg(all(not(target_os = "macos"), not(test)))]
mod store {
    use std::path::PathBuf;

    use feed::shortcuts::Shortcuts;

    fn path() -> Option<PathBuf> {
        std::env::var_os("HOME").map(|h| PathBuf::from(h).join(".agenthail/keyboard-shortcuts.json"))
    }

    pub fn load() -> Shortcuts {
        Shortcuts::load(path().and_then(|p| std::fs::read(p).ok()).as_deref())
    }

    pub fn save(saved: &Shortcuts) {
        let Some(path) = path() else { return };
        let result = match saved.data() {
            Some(bytes) => std::fs::write(&path, bytes),
            None => std::fs::remove_file(&path).or_else(|e| if e.kind() == std::io::ErrorKind::NotFound { Ok(()) } else { Err(e) }),
        };
        if let Err(e) = result {
            eprintln!("agenthail: keyboard shortcuts not saved: {e}");
        }
    }
}

#[cfg(test)]
mod store {
    use feed::shortcuts::Shortcuts;

    pub fn load() -> Shortcuts {
        Shortcuts::default()
    }

    pub fn save(_: &Shortcuts) {}
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
        assert_eq!(change(|s| s.set("goToSession", Stored::parse("mod+g"))), Ok(()));
        assert!(generation() > before);
        assert!(chord("goToSession").is_some_and(|c| c.matches_on(&k, KeyPlatform::Mac)));
        assert_eq!(change(|s| s.set("goToSession", Stored::parse("mod+g"))), Ok(()));
        change(Shortcuts::reset_all);
        assert!(!chord("goToSession").is_some_and(|c| c.matches_on(&k, KeyPlatform::Mac)));
        assert_eq!(shortcut("goToSession").map(|s| s.to_string()), "mod+k".parse::<Shortcut>().ok().map(|s| s.to_string()));
    }

    #[test]
    fn recorded_shortcuts_store_in_the_mac_apps_form() {
        let recorded: Shortcut = "super+shift+k".parse().unwrap();
        assert_eq!(stored(&recorded), Stored::parse("shift+mod+k"));
    }
}
