use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

pub const DOMAIN: &str = "com.agenthail.app";
pub const KEY: &str = "keyboardShortcuts";
pub const ANY_APP_KEY: &str = "KeyboardShortcuts_openPalette";
pub const ANY_APP: &str = "Open sessions from any app";
pub const ANY_APP_ID: &str = "openPalette";

const CMD: i64 = 0x100;
const SHIFT: i64 = 0x200;
const OPTION: i64 = 0x800;
const CONTROL: i64 = 0x1000;
const MODS: [(i64, &str); 4] = [(CONTROL, "ctrl"), (OPTION, "alt"), (SHIFT, "shift"), (CMD, "mod")];

const KEYS: &[(i64, &str)] = &[
    (0x00, "a"), (0x0B, "b"), (0x08, "c"), (0x02, "d"), (0x0E, "e"), (0x03, "f"), (0x05, "g"), (0x04, "h"), (0x22, "i"),
    (0x26, "j"), (0x28, "k"), (0x25, "l"), (0x2E, "m"), (0x2D, "n"), (0x1F, "o"), (0x23, "p"), (0x0C, "q"), (0x0F, "r"),
    (0x01, "s"), (0x11, "t"), (0x20, "u"), (0x09, "v"), (0x0D, "w"), (0x07, "x"), (0x10, "y"), (0x06, "z"),
    (0x1D, "0"), (0x12, "1"), (0x13, "2"), (0x14, "3"), (0x15, "4"), (0x17, "5"), (0x16, "6"), (0x1A, "7"), (0x1C, "8"), (0x19, "9"),
    (0x24, "enter"), (0x30, "tab"), (0x31, "space"), (0x33, "backspace"), (0x35, "escape"), (0x75, "delete"), (0x72, "insert"),
    (0x73, "home"), (0x77, "end"), (0x74, "pageup"), (0x79, "pagedown"), (0x7B, "left"), (0x7C, "right"), (0x7D, "down"), (0x7E, "up"),
    (0x1B, "minus"), (0x18, "equal"), (0x2B, "comma"), (0x2F, "period"), (0x2C, "slash"), (0x29, "semicolon"), (0x27, "quote"),
    (0x21, "bracketleft"), (0x1E, "bracketright"), (0x2A, "backslash"), (0x32, "backquote"),
    (0x7A, "f1"), (0x78, "f2"), (0x63, "f3"), (0x76, "f4"), (0x60, "f5"), (0x61, "f6"), (0x62, "f7"), (0x64, "f8"), (0x65, "f9"), (0x6D, "f10"),
    (0x67, "f11"), (0x6F, "f12"), (0x69, "f13"), (0x6B, "f14"), (0x71, "f15"), (0x6A, "f16"), (0x40, "f17"), (0x4F, "f18"), (0x50, "f19"), (0x5A, "f20"),
];

const ALIASES: &[(&str, &str)] = &[
    ("return", "enter"), ("esc", "escape"), ("del", "delete"), ("-", "minus"), ("=", "equal"), (",", "comma"), (".", "period"),
    ("/", "slash"), (";", "semicolon"), ("'", "quote"), ("[", "bracketleft"), ("]", "bracketright"), ("\\", "backslash"), ("`", "backquote"),
    ("arrowleft", "left"), ("arrowright", "right"), ("arrowup", "up"), ("arrowdown", "down"),
];

#[derive(Serialize, Deserialize, Clone, Copy, Debug, PartialEq, Eq, Hash)]
#[serde(rename_all = "camelCase")]
pub struct Stored {
    pub carbon_key_code: i64,
    pub carbon_modifiers: i64,
}

impl Stored {
    pub fn parse(spec: &str) -> Option<Stored> {
        let mut mods = 0;
        let mut key = None;
        for part in spec.split('+').map(|p| p.trim().to_ascii_lowercase()) {
            let bit = match part.as_str() {
                "mod" | "cmd" | "command" | "super" | "primary" => CMD,
                "ctrl" | "control" => CONTROL,
                "alt" | "option" | "opt" => OPTION,
                "shift" => SHIFT,
                _ => 0,
            };
            if bit != 0 {
                if key.is_some() || mods & bit != 0 {
                    return None;
                }
                mods |= bit;
                continue;
            }
            if key.is_some() {
                return None;
            }
            let name = ALIASES.iter().find(|(a, _)| *a == part).map_or(part.as_str(), |(_, n)| n);
            key = Some(KEYS.iter().find(|(_, n)| *n == name)?.0);
        }
        Some(Stored { carbon_key_code: key?, carbon_modifiers: mods })
    }

    pub fn spec(&self) -> Option<String> {
        self.spec_with("mod")
    }

    pub fn global_spec(&self) -> Option<String> {
        self.spec_with("super")
    }

    fn spec_with(&self, command: &str) -> Option<String> {
        let (_, key) = KEYS.iter().find(|(code, _)| *code == self.carbon_key_code)?;
        if self.carbon_modifiers & !(CMD | SHIFT | OPTION | CONTROL) != 0 {
            return None;
        }
        let mut out = String::new();
        for (bit, name) in MODS {
            if self.carbon_modifiers & bit != 0 {
                out.push_str(if bit == CMD { command } else { name });
                out.push('+');
            }
        }
        out.push_str(key);
        Some(out)
    }

    pub fn types(&self) -> bool {
        if self.carbon_modifiers & (CMD | CONTROL | OPTION) != 0 {
            return false;
        }
        let Some((_, key)) = KEYS.iter().find(|(code, _)| *code == self.carbon_key_code) else { return false };
        let edits = matches!(*key, "space" | "enter" | "backspace" | "delete" | "left" | "right" | "up" | "down" | "home" | "end" | "pageup" | "pagedown");
        let punctuation = matches!(*key, "minus" | "equal" | "comma" | "period" | "slash" | "semicolon" | "quote" | "bracketleft" | "bracketright" | "backslash" | "backquote");
        let completes = *key == "tab" && self.carbon_modifiers & SHIFT == 0;
        key.len() == 1 || edits || punctuation || completes
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Command {
    pub id: &'static str,
    pub title: &'static str,
    pub default: Option<&'static str>,
    pub group: Group,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Group {
    Sessions,
    View,
    Composer,
}

impl Group {
    pub const ALL: [Group; 3] = [Group::Sessions, Group::View, Group::Composer];

    pub fn title(self) -> &'static str {
        match self {
            Group::Sessions => "Sessions",
            Group::View => "View",
            Group::Composer => "Composer",
        }
    }
}

const fn cmd(id: &'static str, title: &'static str, default: &'static str, group: Group) -> Command {
    Command { id, title, default: Some(default), group }
}

pub const COMMANDS: &[Command] = &[
    cmd("newSession", "New session", "mod+n", Group::Sessions),
    cmd("goToSession", "Go to session", "mod+k", Group::Sessions),
    cmd("findSession", "Find session", "mod+f", Group::Sessions),
    cmd("previousSession", "Previous session", "mod+up", Group::Sessions),
    cmd("nextSession", "Next session", "mod+down", Group::Sessions),
    cmd("talk", "Talk to orchestrator", "alt+mod+t", Group::Sessions),
    cmd("jumpToLatest", "Jump to latest", "mod+l", Group::View),
    cmd("refresh", "Refresh", "mod+r", Group::View),
    cmd("textBigger", "Make text bigger", "mod+equal", Group::View),
    cmd("textSmaller", "Make text smaller", "mod+minus", Group::View),
    cmd("stop", "Stop", "mod+period", Group::Composer),
    cmd("composerSendNow", "Send now, steering a running turn", "ctrl+enter", Group::Composer),
    cmd("composerInterrupt", "Stop the running turn (twice clears the draft)", "escape", Group::Composer),
    cmd("composerCycleMode", "Change mode", "shift+tab", Group::Composer),
    cmd("composerStash", "Stash the draft, or bring it back", "ctrl+s", Group::Composer),
    cmd("composerSearchHistory", "Search what you sent", "ctrl+r", Group::Composer),
    cmd("composerNewline", "New line", "ctrl+j", Group::Composer),
    cmd("composerDeleteToLineStart", "Delete to start of line", "ctrl+u", Group::Composer),
    cmd("composerDeleteWordBackward", "Delete previous word", "ctrl+w", Group::Composer),
    cmd("composerPaste", "Paste (also ⌘V)", "ctrl+v", Group::Composer),
];

pub const FIXED: &[(&str, &str)] = &[
    ("mod+comma", "Settings"),
    ("shift+mod+s", "Sessions"),
    ("mod+0", "Now"),
    ("mod+j", "Next session that needs you"),
    ("mod+1", "Hands-on session 1"),
    ("mod+2", "Hands-on session 2"),
    ("mod+3", "Hands-on session 3"),
    ("mod+4", "Hands-on session 4"),
    ("mod+5", "Hands-on session 5"),
    ("alt+mod+b", "Show or hide the brief"),
    ("ctrl+tab", "Next hands-on session"),
    ("ctrl+shift+tab", "Previous hands-on session"),
    ("alt+mod+1", "Show or hide the first pane"),
    ("alt+mod+2", "Show or hide the second pane"),
    ("alt+mod+3", "Show or hide the third pane"),
    ("shift+alt+mod+1", "Focus the first pane"),
    ("shift+alt+mod+2", "Focus the second pane"),
    ("shift+alt+mod+3", "Focus the third pane"),
    ("mod+q", "Quit Agenthail"),
    ("mod+w", "Close window"),
    ("mod+h", "Hide Agenthail"),
    ("mod+m", "Minimize"),
    ("mod+c", "Copy"),
    ("mod+x", "Cut"),
    ("mod+v", "Paste"),
    ("mod+a", "Select all"),
    ("mod+z", "Undo"),
    ("shift+mod+z", "Redo"),
];

pub const MAC_APP_ONLY: &[(&str, &str, Option<&str>)] = &[
    ("showRunning", "Show Working", Some("mod+1")),
    ("showRecent", "Show Recent", Some("mod+2")),
    ("showAll", "Show All", Some("mod+3")),
    ("showArchived", "Show Archived", Some("mod+4")),
    ("toggleSidebar", "Show or hide sidebar", Some("ctrl+mod+s")),
    ("toggleInspector", "Show or hide inspector", Some("alt+mod+i")),
    ("expandAgentMessages", "Expand agent messages", Some("alt+mod+e")),
    ("send", "Send", Some("mod+enter")),
    ("sendAlternate", "Send the other way (queue or steer)", Some("alt+mod+enter")),
    ("rename", "Rename session", None),
    ("openInNewWindow", "Open session in new window", None),
    ("newTab", "New tab", Some("mod+t")),
    ("closeTab", "Close tab", Some("mod+w")),
    ("nextTab", "Next tab", Some("shift+mod+bracketright")),
    ("previousTab", "Previous tab", Some("shift+mod+bracketleft")),
];

pub fn command(id: &str) -> Option<&'static Command> {
    COMMANDS.iter().find(|c| c.id == id)
}

fn default_of(id: &str) -> Option<Stored> {
    command(id)?.default.and_then(Stored::parse)
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Shortcuts {
    overrides: BTreeMap<String, Option<Stored>>,
    any_app: Option<Stored>,
}

impl Shortcuts {
    pub fn load(data: Option<&[u8]>, any_app: Option<&str>) -> Shortcuts {
        let overrides = data.and_then(|d| serde_json::from_slice(d).ok()).unwrap_or_default();
        Shortcuts { overrides, any_app: any_app.and_then(|s| serde_json::from_str(s).ok()) }
    }

    pub fn data(&self) -> Option<Vec<u8>> {
        if self.overrides.is_empty() { None } else { serde_json::to_vec(&self.overrides).ok() }
    }

    pub fn get(&self, id: &str) -> Option<Stored> {
        match self.overrides.get(id) {
            Some(chosen) => *chosen,
            None => default_of(id),
        }
    }

    pub fn spec(&self, id: &str) -> Option<String> {
        self.get(id).and_then(|s| s.spec())
    }

    pub fn customized(&self, id: &str) -> bool {
        self.overrides.contains_key(id)
    }

    pub fn any_customized(&self) -> bool {
        self.any_app.is_some() || COMMANDS.iter().any(|c| self.customized(c.id))
    }

    pub fn any_app(&self) -> Option<Stored> {
        self.any_app
    }

    pub fn any_app_json(&self) -> Option<String> {
        self.any_app.and_then(|s| serde_json::to_string(&s).ok())
    }

    pub fn set_any_app(&mut self, shortcut: Option<Stored>) -> Result<(), &'static str> {
        if let Some(owner) = shortcut.and_then(|s| self.owner(s, ANY_APP_ID)) {
            return Err(owner);
        }
        self.any_app = shortcut;
        Ok(())
    }

    pub fn owner(&self, shortcut: Stored, except: &str) -> Option<&'static str> {
        if let Some(c) = COMMANDS.iter().find(|c| c.id != except && self.get(c.id) == Some(shortcut)) {
            return Some(c.title);
        }
        if except != ANY_APP_ID && self.any_app == Some(shortcut) {
            return Some(ANY_APP);
        }
        let mac_app = |(id, _, default): &&(&str, &str, Option<&str>)| match self.overrides.get(*id) {
            Some(chosen) => *chosen == Some(shortcut),
            None => default.and_then(Stored::parse) == Some(shortcut),
        };
        if let Some((_, title)) = FIXED.iter().find(|(spec, _)| Stored::parse(spec) == Some(shortcut)) {
            return Some(title);
        }
        MAC_APP_ONLY.iter().find(mac_app).map(|(_, title, _)| *title)
    }

    pub fn set(&mut self, id: &str, shortcut: Option<Stored>) -> Result<(), &'static str> {
        if let Some(owner) = shortcut.and_then(|s| self.owner(s, id)) {
            return Err(owner);
        }
        if shortcut == default_of(id) {
            self.overrides.remove(id);
        } else {
            self.overrides.insert(id.into(), shortcut);
        }
        Ok(())
    }

    pub fn default_blocker(&self, id: &str) -> Option<&'static str> {
        default_of(id).and_then(|d| self.owner(d, id))
    }

    pub fn reset(&mut self, id: &str) -> Result<(), &'static str> {
        if let Some(owner) = self.default_blocker(id) {
            return Err(owner);
        }
        self.overrides.remove(id);
        Ok(())
    }

    pub fn reset_all(&mut self) -> Vec<(&'static str, &'static str)> {
        let before = self.overrides.clone();
        self.any_app = None;
        for c in COMMANDS {
            self.overrides.remove(c.id);
        }
        loop {
            let blocked: Vec<&str> = COMMANDS.iter().filter(|c| !self.customized(c.id) && before.contains_key(c.id) && self.default_blocker(c.id).is_some()).map(|c| c.id).collect();
            if blocked.is_empty() {
                break;
            }
            for id in blocked {
                self.overrides.insert(id.into(), before[id]);
            }
        }
        COMMANDS.iter().filter(|c| self.customized(c.id)).filter_map(|c| Some((c.title, self.default_blocker(c.id)?))).collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn s(spec: &str) -> Stored {
        Stored::parse(spec).unwrap()
    }

    #[test]
    fn specs_use_the_mac_apps_key_codes_and_modifiers() {
        assert_eq!(s("mod+k"), Stored { carbon_key_code: 40, carbon_modifiers: 256 });
        assert_eq!(s("ctrl+enter"), Stored { carbon_key_code: 36, carbon_modifiers: 4096 });
        assert_eq!(s("shift+tab"), Stored { carbon_key_code: 48, carbon_modifiers: 512 });
        assert_eq!(s("escape"), Stored { carbon_key_code: 53, carbon_modifiers: 0 });
        assert_eq!(s("alt+mod+t"), Stored { carbon_key_code: 17, carbon_modifiers: 256 | 2048 });
        assert_eq!(s("mod+up"), Stored { carbon_key_code: 126, carbon_modifiers: 256 });
        assert_eq!(s("super+shift+bracketright"), Stored { carbon_key_code: 30, carbon_modifiers: 256 | 512 });
        assert_eq!(s("cmd+."), s("mod+period"));
        assert_eq!(s("mod+="), s("mod+equal"));
        for bad in ["", "mod", "mod+k+j", "mod+mod+k", "k+mod", "hyper+k", "f25"] {
            assert_eq!(Stored::parse(bad), None, "{bad}");
        }
    }

    #[test]
    fn specs_round_trip_in_one_order() {
        for spec in ["ctrl+alt+shift+mod+f12", "mod+period", "escape", "shift+tab", "ctrl+backquote", "alt+mod+bracketleft"] {
            assert_eq!(s(spec).spec().as_deref(), Some(spec));
        }
        assert_eq!(s("cmd+shift+ctrl+a").spec().as_deref(), Some("ctrl+shift+mod+a"));
        assert_eq!(Stored { carbon_key_code: 0x7F, carbon_modifiers: 256 }.spec(), None, "an unknown key binds nothing here");
        assert_eq!(Stored { carbon_key_code: 0, carbon_modifiers: 0x8000 }.spec(), None, "an unknown modifier binds nothing here");
    }

    #[test]
    fn the_shortcut_from_any_app_names_the_super_key_on_every_desktop() {
        assert_eq!(s("super+alt+k").global_spec().as_deref(), Some("alt+super+k"));
        assert_eq!(s("ctrl+super+k").global_spec().as_deref(), Some("ctrl+super+k"));
        assert_eq!(s("ctrl+alt+space").global_spec().as_deref(), Some("ctrl+alt+space"));
        assert_eq!(s("mod+k").spec().as_deref(), Some("mod+k"), "in-app shortcuts keep the platform's primary key");
    }

    #[test]
    fn every_default_parses_and_none_clash() {
        let sc = Shortcuts::default();
        for c in COMMANDS {
            let mine = sc.get(c.id).unwrap_or_else(|| panic!("{} parses", c.id));
            assert_eq!(sc.owner(mine, c.id), None, "{} clashes", c.id);
        }
        for (spec, _) in FIXED {
            assert!(Stored::parse(spec).is_some(), "{spec}");
        }
        for (id, _, spec) in MAC_APP_ONLY {
            assert!(spec.is_none_or(|x| Stored::parse(x).is_some()), "{id}");
            assert!(command(id).is_none(), "{id} is listed twice");
        }
    }

    #[test]
    fn the_mac_apps_saved_overrides_read_and_unknown_commands_survive_a_save() {
        let native = br#"{"goToSession":{"carbonKeyCode":37,"carbonModifiers":256},"jumpToLatest":null,"showRunning":{"carbonKeyCode":18,"carbonModifiers":4352}}"#;
        let mut sc = Shortcuts::load(Some(native), None);
        assert_eq!(sc.spec("goToSession").as_deref(), Some("mod+l"));
        assert_eq!(sc.get("jumpToLatest"), None, "null clears the shortcut");
        assert!(sc.customized("jumpToLatest"));
        assert_eq!(sc.spec("newSession").as_deref(), Some("mod+n"));
        sc.set("refresh", Some(s("shift+mod+r"))).unwrap();
        let saved: serde_json::Value = serde_json::from_slice(&sc.data().unwrap()).unwrap();
        assert_eq!(saved["showRunning"], serde_json::json!({"carbonKeyCode": 18, "carbonModifiers": 4352}));
        assert_eq!(saved["refresh"], serde_json::json!({"carbonKeyCode": 15, "carbonModifiers": 768}));
        assert_eq!(saved["jumpToLatest"], serde_json::Value::Null);
        assert_eq!(Shortcuts::load(Some(b"not json"), Some("false")), Shortcuts::default());
        assert_eq!(Shortcuts::default().data(), None, "no overrides removes the key");
    }

    #[test]
    fn a_taken_shortcut_is_refused_with_its_owner() {
        let mut sc = Shortcuts::default();
        assert_eq!(sc.set("refresh", Some(s("mod+k"))), Err("Go to session"));
        assert_eq!(sc.set("refresh", Some(s("mod+j"))), Err("Next session that needs you"));
        assert_eq!(sc.set("refresh", Some(s("mod+r"))), Ok(()), "a command's own shortcut is not a clash");
        assert!(!sc.customized("refresh"), "choosing the default stores nothing");
        assert_eq!(sc.set("goToSession", Some(s("mod+g"))), Ok(()));
        assert_eq!(sc.set("refresh", Some(s("mod+k"))), Ok(()), "a freed shortcut can be taken");
        assert_eq!(sc.default_blocker("goToSession"), Some("Refresh"));
        assert_eq!(sc.reset("goToSession"), Err("Refresh"));
        assert_eq!(sc.set("refresh", None), Ok(()));
        assert_eq!(sc.get("refresh"), None);
        assert_eq!(sc.reset("goToSession"), Ok(()));
        assert_eq!(sc.spec("goToSession").as_deref(), Some("mod+k"));
        sc.reset_all();
        assert!(!sc.any_customized());
        assert_eq!(sc.spec("refresh").as_deref(), Some("mod+r"));
    }

    #[test]
    fn restoring_defaults_keeps_what_only_the_mac_app_shows() {
        let native = br#"{"refresh":{"carbonKeyCode":15,"carbonModifiers":768},"toggleSidebar":null}"#;
        let mut sc = Shortcuts::load(Some(native), None);
        assert!(sc.any_customized());
        sc.reset_all();
        assert!(!sc.any_customized());
        let saved: serde_json::Value = serde_json::from_slice(&sc.data().unwrap()).unwrap();
        assert_eq!(saved, serde_json::json!({"toggleSidebar": null}));
    }

    #[test]
    fn the_mac_apps_own_commands_and_pane_keys_are_taken() {
        let mut sc = Shortcuts::default();
        assert_eq!(sc.set("refresh", Some(s("mod+enter"))), Err("Send"));
        assert_eq!(sc.set("refresh", Some(s("alt+mod+2"))), Err("Show or hide the second pane"));
        assert_eq!(sc.set("refresh", Some(s("shift+alt+mod+3"))), Err("Focus the third pane"));
        let mut moved = Shortcuts::load(Some(br#"{"send":{"carbonKeyCode":36,"carbonModifiers":4352},"toggleSidebar":null}"#), None);
        assert_eq!(moved.set("refresh", Some(s("mod+enter"))), Ok(()), "the Mac app moved Send elsewhere");
        assert_eq!(moved.set("stop", Some(s("ctrl+mod+enter"))), Err("Send"));
        assert_eq!(moved.set("stop", Some(s("ctrl+mod+s"))), Ok(()), "the Mac app cleared its sidebar shortcut");
        assert_eq!(sc.set("refresh", Some(s("mod+t"))), Err("New tab"));
    }

    #[test]
    fn restoring_defaults_keeps_a_shortcut_whose_default_the_mac_app_took() {
        let native = br#"{"goToSession":{"carbonKeyCode":5,"carbonModifiers":256},"rename":{"carbonKeyCode":40,"carbonModifiers":256},"refresh":{"carbonKeyCode":15,"carbonModifiers":768}}"#;
        let mut sc = Shortcuts::load(Some(native), None);
        assert_eq!(sc.reset_all(), vec![("Go to session", "Rename session")]);
        assert_eq!(sc.spec("goToSession").as_deref(), Some("mod+g"));
        assert_eq!(sc.spec("refresh").as_deref(), Some("mod+r"));
        let mut chained = Shortcuts::load(Some(br#"{"goToSession":{"carbonKeyCode":15,"carbonModifiers":256},"refresh":{"carbonKeyCode":5,"carbonModifiers":256},"rename":{"carbonKeyCode":40,"carbonModifiers":256}}"#), None);
        chained.reset_all();
        assert_eq!(chained.spec("goToSession").as_deref(), Some("mod+r"), "kept, its default is Rename's");
        assert_eq!(chained.spec("refresh").as_deref(), Some("mod+g"), "kept, its default is Go to session's kept shortcut");
        for c in COMMANDS {
            if let Some(mine) = chained.get(c.id) {
                assert_eq!(chained.owner(mine, c.id), None, "{} clashes after restoring", c.id);
            }
        }
    }

    #[test]
    fn the_mac_apps_shortcut_from_any_app_is_taken() {
        let mut sc = Shortcuts::load(None, Some(r#"{"carbonKeyCode":40,"carbonModifiers":2304}"#));
        assert_eq!(sc.set("refresh", Some(s("alt+mod+k"))), Err(ANY_APP));
        assert_eq!(Shortcuts::load(None, Some("false")).set("refresh", Some(s("alt+mod+k"))), Ok(()));
    }

    #[test]
    fn the_shortcut_from_any_app_is_chosen_like_a_command_and_saved_as_the_mac_app_does() {
        let mut sc = Shortcuts::default();
        assert_eq!(sc.any_app(), None, "the Mac app ships it unset");
        assert!(!sc.any_customized());
        assert_eq!(sc.set_any_app(Some(s("mod+k"))), Err("Go to session"));
        assert_eq!(sc.set_any_app(Some(s("mod+q"))), Err("Quit Agenthail"));
        assert_eq!(sc.set_any_app(Some(s("alt+mod+k"))), Ok(()));
        assert_eq!(sc.set_any_app(Some(s("alt+mod+k"))), Ok(()), "its own shortcut is not a clash");
        assert_eq!(sc.any_app_json().as_deref(), Some(r#"{"carbonKeyCode":40,"carbonModifiers":2304}"#));
        assert_eq!(Shortcuts::load(None, sc.any_app_json().as_deref()).any_app(), Some(s("alt+mod+k")));
        assert!(sc.any_customized(), "Restore Defaults clears it");
        sc.reset_all();
        assert_eq!(sc.any_app(), None);
        assert_eq!(sc.any_app_json(), None);
        assert_eq!(sc.set_any_app(None), Ok(()));
    }

    #[test]
    fn keys_that_type_are_told_apart() {
        for typing in ["a", "shift+a", "space", "enter", "shift+enter", "tab", "backspace", "up", "minus", "shift+period"] {
            assert!(s(typing).types(), "{typing}");
        }
        for not in ["escape", "shift+tab", "shift+f5", "ctrl+a", "alt+left", "mod+enter", "f1"] {
            assert!(!s(not).types(), "{not}");
        }
    }
}
