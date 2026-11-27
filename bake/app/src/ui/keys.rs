use feed::search::Command;
use rune::{Chord, KeyEvent, MenuItem, Tx};

use super::bindings;
use crate::over::{self, search};
use crate::{App, Screen};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Act {
    NewSession,
    GoToSession,
    Stop,
    Refresh,
    PreviousSession,
    NextSession,
    JumpToLatest,
    Find,
    Talk,
    TextBigger,
    TextSmaller,
    TextActual,
}

pub struct Binding {
    pub act: Act,
    pub id: &'static str,
    pub menu: Option<(&'static str, &'static str)>,
}

pub const APP: [Binding; 11] = [
    Binding { act: Act::NewSession, id: "newSession", menu: Some(("File", "New Session")) },
    Binding { act: Act::GoToSession, id: "goToSession", menu: Some(("Session", "Go to Session…")) },
    Binding { act: Act::Stop, id: "stop", menu: Some(("Session", "Stop")) },
    Binding { act: Act::Refresh, id: "refresh", menu: Some(("View", "Refresh")) },
    Binding { act: Act::PreviousSession, id: "previousSession", menu: None },
    Binding { act: Act::NextSession, id: "nextSession", menu: None },
    Binding { act: Act::JumpToLatest, id: "jumpToLatest", menu: None },
    Binding { act: Act::Find, id: "findSession", menu: None },
    Binding { act: Act::Talk, id: "talk", menu: Some(("Session", "Talk to Orchestrator…")) },
    Binding { act: Act::TextBigger, id: "textBigger", menu: Some(("View", "Make Text Bigger")) },
    Binding { act: Act::TextSmaller, id: "textSmaller", menu: Some(("View", "Make Text Smaller")) },
];

pub fn find(matches: impl Fn(&Chord) -> bool) -> Option<Act> {
    APP.iter().find(|b| bindings::chord(b.id).is_some_and(|c| matches(&c))).map(|b| b.act)
}

fn chord_of(act: Act) -> Option<Chord> {
    APP.iter().find(|b| b.act == act).and_then(|b| bindings::chord(b.id))
}

pub fn is(act: Act, ev: &KeyEvent, tx: &Tx) -> bool {
    chord_of(act).is_some_and(|c| c.matches_tx(ev, tx))
}

pub fn pressed(act: Act, ev: &KeyEvent) -> bool {
    chord_of(act).is_some_and(|c| c.matches(ev))
}

pub fn on_key(s: &mut App, ev: &KeyEvent, tx: &mut Tx) -> bool {
    match find(|c| c.matches_tx(ev, tx)) {
        Some(act @ (Act::PreviousSession | Act::NextSession)) => {
            step(s, if act == Act::NextSession { 1 } else { -1 });
            true
        }
        Some(act) => {
            run(s, act, tx);
            true
        }
        None => false,
    }
}

pub fn in_field(s: &mut App, ev: &KeyEvent, tx: &mut Tx) -> bool {
    match find(|c| c.matches_tx(ev, tx)) {
        Some(Act::PreviousSession) => step(s, -1),
        Some(Act::NextSession) => step(s, 1),
        _ => false,
    }
}

pub fn run(s: &mut App, act: Act, tx: &mut Tx) {
    match act {
        Act::NewSession => search::run(s, Command::NewSession, tx),
        Act::GoToSession => {
            if s.ov.jump.take().is_none() {
                over::open_jump(s, tx);
            }
        }
        Act::Stop => search::run(s, Command::Stop, tx),
        Act::Refresh => search::run(s, Command::Refresh, tx),
        Act::PreviousSession => {
            step(s, -1);
        }
        Act::NextSession => {
            step(s, 1);
        }
        Act::JumpToLatest => {
            if let Some(id) = search::selected(s) {
                tx.scroll_to_bottom(format!("transcript-{id}"));
            }
        }
        Act::Find => {
            over::go(s, Screen::Sessions);
            s.composer.focused = false;
            s.ov.sessions_query.focused = true;
            tx.focus("sessions-search", false);
        }
        Act::Talk => crate::voice::open("", ""),
        Act::TextBigger => super::text_size::step(s, Some(1)),
        Act::TextSmaller => super::text_size::step(s, Some(-1)),
        Act::TextActual => super::text_size::step(s, None),
    }
}

pub fn stepped<'a>(order: &'a [String], current: Option<&str>, by: isize) -> Option<&'a str> {
    let last = order.len().checked_sub(1)?;
    let at = current.and_then(|id| order.iter().position(|x| x == id));
    let next = at.map_or(0, |i| i.saturating_add_signed(by).min(last));
    (at != Some(next)).then(|| order[next].as_str())
}

fn step(s: &mut App, by: isize) -> bool {
    let selected = search::selected(s);
    let project = match (&s.screen, &selected) {
        (_, Some(id)) => crate::ui::gone::shown(s, id).and_then(|x| over::ui::project_for(s, x)),
        (Screen::Project(key), None) => Some(key.clone()),
        (Screen::Work(key), None) => s.ov.d.work.get(key).map(|w| w.project.clone()),
        _ => None,
    };
    let order = over::ui::sidebar_order(s, project.as_deref(), selected.as_deref());
    let Some(next) = stepped(&order, selected.as_deref(), by).map(str::to_string) else { return false };
    over::open_session(s, &next);
    true
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct MenuState {
    stop: bool,
    focus: bool,
    keys: u64,
}

pub fn menu_state(s: &App) -> MenuState {
    MenuState { stop: search::selected(s).is_some_and(|id| search::can_stop(s, &id)), focus: crate::transcript::focused(), keys: bindings::generation() }
}

pub fn menu_items(state: MenuState) -> Vec<MenuItem<App>> {
    APP.iter()
        .filter_map(|b| {
            let (menu, title) = b.menu?;
            let act = b.act;
            let enabled = act != Act::Stop || state.stop;
            let item = MenuItem::with_action(title, move |s: &mut App, tx| run(s, act, tx)).id(format!("key-{act:?}")).menu(menu).keys_first().enabled(enabled);
            Some(match bindings::shortcut(b.id) {
                Some(shortcut) => item.key_equivalent(shortcut),
                None => item,
            })
        })
        .chain([
            MenuItem::with_action("Actual Text Size", |s: &mut App, tx| run(s, Act::TextActual, tx)).id("key-TextActual").menu("View"),
            MenuItem::with_action("Focus", |s: &mut App, _| crate::transcript::set_focus(s, !crate::transcript::focused())).id("key-Focus").menu("View").checked(state.focus),
        ])
        .collect()
}

#[cfg(test)]
mod tests {
    use rune::{Key, KeyPlatform, Mods};

    use super::*;

    fn press(key: Key, mods: Mods) -> Option<Act> {
        let ev = KeyEvent { key, mods };
        find(|c| c.matches_on(&ev, KeyPlatform::Mac))
    }

    const CMD: Mods = Mods { shift: false, cmd: true, alt: false, ctrl: false, logo: false };

    #[test]
    fn the_mac_apps_shortcuts_reach_their_commands() {
        assert_eq!(press(Key::Command('n'), CMD), Some(Act::NewSession));
        assert_eq!(press(Key::Command('k'), CMD), Some(Act::GoToSession));
        assert_eq!(press(Key::Command('.'), CMD), Some(Act::Stop));
        assert_eq!(press(Key::Command('r'), CMD), Some(Act::Refresh));
        assert_eq!(press(Key::Up, CMD), Some(Act::PreviousSession));
        assert_eq!(press(Key::Down, CMD), Some(Act::NextSession));
        assert_eq!(press(Key::Command('l'), CMD), Some(Act::JumpToLatest));
        assert_eq!(press(Key::Command('f'), CMD), Some(Act::Find));
        assert_eq!(press(Key::Command('t'), Mods { alt: true, ..CMD }), Some(Act::Talk));
        assert_eq!(press(Key::Command('='), CMD), Some(Act::TextBigger));
        assert_eq!(press(Key::Command('-'), CMD), Some(Act::TextSmaller));
    }

    #[test]
    fn other_modifiers_leave_the_keys_alone() {
        assert_eq!(press(Key::Command('.'), Mods { ctrl: true, ..CMD }), None);
        assert_eq!(press(Key::Command('r'), Mods { alt: true, ..CMD }), None);
        assert_eq!(press(Key::Up, Mods::default()), None);
        assert_eq!(press(Key::Text(".".into()), Mods::default()), None);
        assert_eq!(press(Key::Command('s'), Mods { shift: true, ..CMD }), None, "⇧⌘S stays the Sessions screen's");
        assert_eq!(press(Key::Command('0'), CMD), None, "⌘0 stays Now");
        assert_eq!(press(Key::Command('j'), CMD), None, "⌘J stays next need");
        assert_eq!(press(Key::Command('t'), CMD), None, "⌘T alone is left free");
        assert_eq!(press(Key::Command('b'), Mods { alt: true, ..CMD }), None, "⌥⌘B stays the brief toggle");
    }

    #[test]
    fn every_binding_is_distinct_and_menu_keys_parse() {
        for (i, a) in APP.iter().enumerate() {
            assert!(bindings::shortcut(a.id).is_some(), "{}", a.id);
            assert!(feed::shortcuts::command(a.id).is_some(), "{} is listed in Settings", a.id);
            for b in &APP[i + 1..] {
                let (x, y) = (bindings::chord(a.id).unwrap(), bindings::chord(b.id).unwrap());
                assert!(!x.same_keys(&y, KeyPlatform::Mac), "{} and {} clash", a.id, b.id);
            }
        }
        let menus: Vec<_> = APP.iter().filter_map(|b| b.menu).collect();
        assert_eq!(menus, vec![("File", "New Session"), ("Session", "Go to Session…"), ("Session", "Stop"), ("View", "Refresh"), ("Session", "Talk to Orchestrator…"), ("View", "Make Text Bigger"), ("View", "Make Text Smaller")]);
    }

    #[test]
    fn stepping_stops_at_either_end_and_starts_from_the_top() {
        let order: Vec<String> = ["a", "b", "c"].map(String::from).to_vec();
        assert_eq!(stepped(&order, Some("b"), 1), Some("c"));
        assert_eq!(stepped(&order, Some("b"), -1), Some("a"));
        assert_eq!(stepped(&order, Some("c"), 1), None);
        assert_eq!(stepped(&order, Some("a"), -1), None);
        assert_eq!(stepped(&order, Some("gone"), 1), Some("a"));
        assert_eq!(stepped(&order, None, -1), Some("a"));
        assert_eq!(stepped(&[], Some("a"), 1), None);
    }
}
