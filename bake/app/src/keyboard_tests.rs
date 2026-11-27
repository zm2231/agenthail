use feed::shortcuts::{ANY_APP_ID, Stored};
use rune::Runtime;
use rune_kit::ShortcutRecorderState;

use super::text_scale::settings_on;
use super::{DT, isolated};
use crate::settings::{GENERAL_TAB, KEYBOARD_PAGE, Prefs, keyboard_opened, keyboard_recorder};
use crate::ui::bindings;

const NOTHING_ARRIVED: &str = "Nothing arrived; that shortcut may be taken by another app.";

fn shows(rt: &Runtime<Prefs>, said: &str) -> bool {
    rt.elements().iter().any(|e| e.text.as_deref() == Some(said))
}

fn click(rt: &mut Runtime<Prefs>, key: &str) {
    let (x, y) = rt.find(key).unwrap_or_else(|| panic!("{key} is on screen"));
    rt.pointer_down(x, y);
    rt.pointer_up(x, y);
    rt.frame(DT);
}

fn recorder<'a>(rt: &'a Runtime<Prefs>, id: &str) -> &'a ShortcutRecorderState {
    keyboard_recorder(&rt.state, id).unwrap_or_else(|| panic!("{id} has a recorder"))
}

fn shown(rt: &Runtime<Prefs>, id: &str) -> Option<String> {
    recorder(rt, id).shortcut().map(ToString::to_string)
}

fn saved(id: &str) -> Option<String> {
    if id == ANY_APP_ID { bindings::any_app() } else { bindings::shortcut(id) }.map(|s| s.to_string())
}

#[test]
fn a_shortcut_no_key_reaches_times_out_with_the_hint_and_keeps_the_old_binding() {
    if !isolated("keyboard::a_shortcut_no_key_reaches_times_out_with_the_hint_and_keeps_the_old_binding") {
        return;
    }
    let chosen = Stored::parse("alt+mod+space").expect("a valid shortcut");
    bindings::change(|sc| sc.set_any_app(Some(chosen))).expect("saved").expect("free");
    let mut rt = settings_on(GENERAL_TAB, KEYBOARD_PAGE);
    rt.update(|s, _| keyboard_opened(s));
    rt.frame(DT);
    for (key, id) in [("settings-shortcut-newSession", "newSession"), ("settings-shortcut-any-app", ANY_APP_ID)] {
        let stored = saved(id);
        assert!(stored.is_some(), "{id} starts with a shortcut");
        click(&mut rt, key);
        assert!(recorder(&rt, id).is_recording(), "{key} records after a click");
        rt.frame(4.8);
        assert!(recorder(&rt, id).is_recording() && !shows(&rt, NOTHING_ARRIVED), "{key} still waits before five seconds");
        rt.frame(0.3);
        assert!(!recorder(&rt, id).is_recording() && recorder(&rt, id).timed_out(), "{key} stops recording once five seconds pass with no key");
        assert!(shows(&rt, NOTHING_ARRIVED), "{key} says nothing arrived");
        assert_eq!(shown(&rt, id), stored, "{key} keeps showing the old shortcut");
        assert_eq!(saved(id), stored, "{key} keeps the saved binding");
        click(&mut rt, key);
        rt.frame(1.0);
        assert!(recorder(&rt, id).is_recording() && !recorder(&rt, id).timed_out(), "clicking {key} records again");
        assert!(!shows(&rt, NOTHING_ARRIVED), "recording {key} again clears the hint");
        rt.frame(5.1);
    }
}
