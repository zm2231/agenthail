use std::collections::HashMap;

use feed::catalog::Session;
use feed::derive::live::Overview;
use serde_json::json;

use super::*;
use crate::ui::window;
use crate::{App, Fixtures, Screen};

fn app() -> App {
    let mut s = crate::empty(Overview::default(), false);
    s.fixtures = Some(Fixtures { dir: std::env::temp_dir().display().to_string(), streams: HashMap::new(), speed: 1.0 });
    s
}

fn row(id: &str, archived: bool) -> Session {
    serde_json::from_value(json!({"id": id, "surface": "claude", "name": format!("Work on {id}"), "alias": id, "status": "idle", "open": true, "archived": archived})).unwrap()
}

fn ids(s: &App) -> Vec<&str> {
    s.tabs.tabs.tabs().iter().map(|t| t.id.as_str()).collect()
}

fn open(s: &mut App, list: &[&str]) {
    for id in list {
        crate::over::open_session(s, id);
    }
}

#[test]
fn opening_sessions_in_the_main_window_opens_tabs_and_a_session_window_does_not() {
    let mut s = app();
    open(&mut s, &["a", "b"]);
    assert_eq!((ids(&s), s.tabs.tabs.active()), (vec!["a", "b"], Some("b")));
    let w = window::open(&mut s, "c");
    window::enter(&mut s, w);
    crate::select(&mut s, "c");
    window::enter(&mut s, window::MAIN);
    assert_eq!(ids(&s), ["a", "b"]);
    assert_eq!(active_tab(&s), Some("b"));
}

#[test]
fn closing_the_shown_tab_shows_its_neighbour_then_now() {
    let mut s = app();
    open(&mut s, &["a", "b", "c"]);
    activate(&mut s, "b");
    close(&mut s, "b");
    assert_eq!((s.selected.as_deref(), ids(&s)), (Some("c"), vec!["a", "c"]));
    close(&mut s, "a");
    assert_eq!(s.selected.as_deref(), Some("c"), "closing a tab that isn't shown leaves the session shown");
    close(&mut s, "c");
    assert!(s.screen == Screen::Now && ids(&s).is_empty());
}

#[test]
fn stepping_wraps_and_close_others_keeps_the_shown_one() {
    let mut s = app();
    open(&mut s, &["a", "b", "c"]);
    step(&mut s, 1);
    assert_eq!(s.selected.as_deref(), Some("a"));
    step(&mut s, -1);
    assert_eq!(s.selected.as_deref(), Some("c"));
    close_others(&mut s, "c");
    assert_eq!((ids(&s), s.selected.as_deref()), (vec!["c"], Some("c")));
}

#[test]
fn a_full_strip_closes_the_oldest_unheld_tab_and_offers_it_back_for_six_seconds() {
    let mut s = app();
    s.sessions = vec![row("a", false), row("b", false), row("c", false)];
    open(&mut s, &["a", "b", "c"]);
    s.drafts.set("a", "keep me");
    s.clock = 10.0;
    set_width(&mut s, 380.0);
    assert_eq!(ids(&s), ["a", "c"], "a tab with a draft stays; the oldest other one closes");
    let (id, title, until) = s.tabs.closed.clone().unwrap();
    assert_eq!((id.as_str(), title.as_str(), until), ("b", "@b", 16.0));
    s.clock = 15.9;
    assert!(!expire(&mut s));
    reopen(&mut s);
    assert_eq!(s.selected.as_deref(), Some("b"));
    assert!(s.tabs.closed.is_some() || ids(&s).contains(&"b"));
    s.clock = 30.0;
    expire(&mut s);
    assert!(s.tabs.closed.is_none());
}

#[test]
fn an_archived_tab_closes_and_says_so() {
    let mut s = app();
    s.sessions = vec![row("a", false), row("b", false)];
    open(&mut s, &["a", "b"]);
    s.sessions = vec![row("a", false), row("b", true)];
    s.clock = 5.0;
    reconcile(&mut s);
    assert_eq!((ids(&s), s.selected.as_deref()), (vec!["a"], Some("a")));
    assert_eq!(s.tabs.archived.as_ref().map(|a| (a.id.as_str(), a.title.as_str(), a.until)), Some(("b", "@b", 15.0)));
    s.clock = 20.0;
    expire(&mut s);
    assert!(s.tabs.archived.is_none(), "the notice goes after ten seconds");
}

#[test]
fn several_archived_at_once_move_off_the_shown_one_and_gone_sessions_drop() {
    let mut s = app();
    s.sessions = vec![row("a", false), row("b", false), row("c", false), row("d", false)];
    open(&mut s, &["a", "b", "c", "d"]);
    activate(&mut s, "c");
    s.sessions = vec![row("a", false), row("b", true), row("c", true)];
    reconcile(&mut s);
    assert_eq!(ids(&s), ["a"]);
    assert_eq!(s.selected.as_deref(), Some("a"));
}

#[test]
fn unarchiving_keeps_the_notice_until_it_lands_and_offers_a_retry_when_it_does_not() {
    let mut s = app();
    s.sessions = vec![row("a", false), row("b", false)];
    open(&mut s, &["a", "b"]);
    s.sessions = vec![row("a", false), row("b", true)];
    reconcile(&mut s);
    unarchive(&mut s);
    let a = s.tabs.archived.clone().expect("the notice stays while it is unarchived");
    assert!(a.unarchiving && stuck(&s, &a), "nothing was sent (no daemon), so it offers Try Again");
    s.clock = 100.0;
    expire(&mut s);
    assert!(s.tabs.archived.is_some(), "a failed unarchive doesn't time out");
    s.sessions = vec![row("a", false), row("b", false)];
    expire(&mut s);
    assert!(s.tabs.archived.is_none());
    assert_eq!((s.selected.as_deref(), s.tabs.tabs.active()), (Some("b"), Some("b")));
}

#[test]
fn a_draft_typed_just_before_switching_keeps_its_tab() {
    let mut s = app();
    s.sessions = vec![row("a", false), row("b", false), row("c", false)];
    open(&mut s, &["a", "b"]);
    set_width(&mut s, 380.0);
    activate(&mut s, "a");
    crate::ui::paste::set_composer(&mut s, "unsaved words");
    crate::over::open_session(&mut s, "c");
    assert_eq!(ids(&s), ["a", "c"], "b closes, not a, whose draft was still in the composer");
}

#[test]
fn restored_tabs_show_on_now() {
    let mut s = app();
    s.sessions = vec![row("a", false)];
    open(&mut s, &["a"]);
    crate::over::go(&mut s, Screen::Now);
    let spec = rune::AppSpec::<App> { ..rune::AppSpec::new("Agenthail", (1440.0, 900.0), crate::init, crate::ui::view) };
    let mut rt = rune::App::new(spec).init(move || s).headless_runtime((1440.0, 900.0), rune::Env { system_dark: true, scale: 1.0 });
    rt.frame(1.0 / 60.0);
    rt.frame(1.0 / 60.0);
    assert!(rt.presented("session-tabs").is_some());
    assert_eq!(rt.state.tabs.capacity, usize::MAX, "the Now toolbar doesn't size the strip");
}

#[test]
fn a_moved_session_keeps_its_tab() {
    let mut s = app();
    open(&mut s, &["old", "b"]);
    s.rekeyed.insert("old".into(), "new".into());
    s.sessions = vec![row("new", false), row("b", false)];
    reconcile(&mut s);
    assert_eq!(ids(&s), ["new", "b"]);
}

#[test]
fn a_tab_opens_in_its_own_window() {
    let mut s = app();
    open(&mut s, &["a", "b"]);
    to_window(&mut s, "b");
    assert_eq!(ids(&s), ["a"]);
    assert!(s.windows.holds("b"));
    assert_eq!(window::take_to_show(&mut s).len(), 1);
}

#[test]
fn close_tab_key_closes_a_tab_only_on_the_session_screen() {
    let mut s = app();
    assert_eq!(active_tab(&s), None);
    open(&mut s, &["a"]);
    assert_eq!(active_tab(&s), Some("a"));
    crate::over::go(&mut s, Screen::Now);
    assert_eq!(active_tab(&s), None);
}

#[test]
fn tabs_save_and_load() {
    let mut s = app();
    open(&mut s, &["a", "b"]);
    let saved = saved(&s);
    let mut t = app();
    load(&mut t, &saved);
    assert_eq!((ids(&t), t.tabs.tabs.active()), (vec!["a", "b"], Some("b")));
    load(&mut t, &json!("junk"));
    assert_eq!(ids(&t), ["a", "b"]);
}

#[test]
fn the_strip_shows_in_the_session_toolbar_and_a_right_click_offers_a_window() {
    let mut s = app();
    s.sessions = vec![row("a", false), row("b", false)];
    open(&mut s, &["a", "b"]);
    let spec = rune::AppSpec::<App> { ..rune::AppSpec::new("Agenthail", (1440.0, 900.0), crate::init, crate::ui::view) };
    let mut rt = rune::App::new(spec).init(move || s).headless_runtime((1440.0, 900.0), rune::Env { system_dark: true, scale: 1.0 });
    rt.frame(1.0 / 60.0);
    rt.frame(1.0 / 60.0);
    assert!(rt.presented("session-tabs").is_some(), "the tab strip is drawn");
    assert_eq!(rt.state.tabs.capacity, feed::tabs::capacity(rt.presented("session-tabs").unwrap().0[2]));
    rt.update(|s, _| s.tabs.menu = Some(("a".into(), rune::PointerPress { x: 100.0, y: 20.0 })));
    rt.frame(1.0 / 60.0);
    assert!(rt.presented("tab-menu-menu").is_some(), "the tab menu opens");
}

#[test]
fn archiving_the_last_tab_still_shows_the_notice() {
    let mut s = app();
    s.sessions = vec![row("a", false)];
    open(&mut s, &["a"]);
    s.sessions = vec![row("a", true)];
    reconcile(&mut s);
    assert!(ids(&s).is_empty() && s.screen == Screen::Now);
    let spec = rune::AppSpec::<App> { ..rune::AppSpec::new("Agenthail", (1440.0, 900.0), crate::init, crate::ui::view) };
    let mut rt = rune::App::new(spec).init(move || s).headless_runtime((1440.0, 900.0), rune::Env { system_dark: true, scale: 1.0 });
    rt.frame(1.0 / 60.0);
    rt.frame(1.0 / 60.0);
    assert!(rt.presented("tab-archived").is_some());
}
