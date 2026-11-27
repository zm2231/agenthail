use std::collections::HashMap;

use feed::derive::live::Overview;
use feed::windows::{Frame, Saved};
use rune::WindowPlacement;

use super::*;
use crate::ui::paste;
use crate::{App, Fixtures, Screen};

fn app() -> App {
    let mut s = crate::empty(Overview::default(), false);
    s.fixtures = Some(Fixtures { dir: std::env::temp_dir().display().to_string(), streams: HashMap::new(), speed: 1.0 });
    s
}

fn typed(s: &mut App, text: &str) {
    paste::set_composer(s, text);
}

#[test]
fn each_window_keeps_its_own_session_and_composer() {
    let mut s = app();
    crate::select(&mut s, "a");
    typed(&mut s, "to a");
    let w = open(&mut s, "b");
    assert_eq!((s.windows.current(), s.selected.as_deref()), (MAIN, Some("a")), "opening a window leaves the main window as it was");
    enter(&mut s, w);
    assert!(s.selected.as_deref() == Some("b") && s.screen == Screen::Session);
    assert!(!s.ov.inspector.is_open(), "a session window opens with its inspector hidden");
    typed(&mut s, "to b");
    enter(&mut s, MAIN);
    assert_eq!((s.selected.as_deref(), paste::draft(&s).as_str()), (Some("a"), "to a"));
    assert_eq!(s.drafts.get("b"), "to b", "drafts are shared, saved when a window is left");
    enter(&mut s, w);
    assert_eq!(paste::draft(&s), "to b");
}

#[test]
fn entering_the_current_window_again_changes_nothing_and_any_order_restores() {
    let mut s = app();
    crate::select(&mut s, "a");
    let b = open(&mut s, "b");
    let c = open(&mut s, "c");
    enter(&mut s, b);
    typed(&mut s, "half");
    enter(&mut s, b);
    assert_eq!((s.selected.as_deref(), paste::draft(&s).as_str()), (Some("b"), "half"));
    enter(&mut s, c);
    enter(&mut s, MAIN);
    enter(&mut s, b);
    assert_eq!((s.selected.as_deref(), paste::draft(&s).as_str()), (Some("b"), "half"));
    enter(&mut s, MAIN);
    assert_eq!(s.selected.as_deref(), Some("a"));
    enter(&mut s, 99);
    assert_eq!(s.windows.current(), MAIN, "a window that isn't open is never entered");
}

#[test]
fn the_same_draft_shows_in_every_window_holding_its_session() {
    let mut s = app();
    crate::select(&mut s, "a");
    let w = open(&mut s, "a");
    enter(&mut s, w);
    typed(&mut s, "from the window");
    enter(&mut s, MAIN);
    assert_eq!(paste::draft(&s), "from the window");
    typed(&mut s, "from main");
    enter(&mut s, w);
    assert_eq!(paste::draft(&s), "from main");
}

#[test]
fn a_session_opens_one_window_and_closing_keeps_its_draft() {
    let mut s = app();
    let w = open(&mut s, "b");
    assert_eq!(open(&mut s, "b"), w);
    assert_eq!(s.windows.open.len(), 1);
    enter(&mut s, w);
    typed(&mut s, "unsent words");
    close(&mut s, w);
    assert_eq!(s.windows.current(), MAIN);
    assert!(s.windows.open.is_empty() && !s.windows.holds("b"));
    assert_eq!(s.drafts.get("b"), "unsent words");
    close(&mut s, MAIN);
    assert_eq!(s.windows.current(), MAIN);
}

#[test]
fn a_window_keeps_its_transcript_loaded_past_the_recent_limit() {
    let mut s = app();
    open(&mut s, "held");
    for i in 0..12 {
        crate::select(&mut s, &format!("s{i}"));
    }
    assert!(s.panes.contains_key("held"));
    assert!(s.panes.len() <= 9);
}

#[test]
fn a_moved_session_moves_its_window() {
    let mut s = app();
    let w = open(&mut s, "old");
    rekeyed(&mut s, "old", "new");
    assert_eq!(s.windows.session(w), Some("new"));
    enter(&mut s, w);
    assert_eq!(s.selected.as_deref(), Some("new"));
}

#[test]
fn saved_windows_reopen_with_their_frames() {
    let mut s = app();
    let frame = Frame { x: 40, y: 60, width: 900, height: 820, scale: 1.0, maximized: false, full_screen: false };
    let ids = restore(&mut s, &[Saved { session: "a".into(), frame: Some(frame) }, Saved { session: "b".into(), frame: None }]);
    assert_eq!(ids.len(), 2);
    assert_eq!(s.windows.saved(), vec![Saved { session: "a".into(), frame: Some(frame) }, Saved { session: "b".into(), frame: None }]);
    assert_eq!(take_to_show(&mut s), vec![(ids[0], true), (ids[1], true)], "windows saved at quit come back without taking focus");
    show(&mut s, "c");
    assert_eq!(take_to_show(&mut s).len(), 1);
}

#[test]
fn a_window_saves_where_it_is_and_a_maximized_one_its_normal_frame() {
    let placed = WindowPlacement { position: (10, 20), content_size: (1800, 1640), scale: 2.0, ..Default::default() };
    assert_eq!(placement_frame(placed), Frame { x: 10, y: 20, width: 1800, height: 1640, scale: 2.0, maximized: false, full_screen: false });
    let big = WindowPlacement { maximized: true, restore: Some(((5, 6), (900, 820))), ..placed };
    assert_eq!(placement_frame(big), Frame { x: 5, y: 6, width: 900, height: 820, scale: 2.0, maximized: true, full_screen: false });
}

fn turn(s: &mut App, rt: &mut rune::Runtime<App>, key: &str) {
    std::mem::swap(s, &mut rt.state);
    on_enter(&mut rt.state, key);
    rt.frame(1.0 / 60.0);
    on_leave(&mut rt.state, key);
    std::mem::swap(s, &mut rt.state);
}

#[test]
fn two_windows_take_turns_over_one_app() {
    let mut s = app();
    s.sessions = ["a", "b"].iter().map(|id| serde_json::from_value(serde_json::json!({"id": id, "surface": "claude", "name": id, "status": "idle", "open": true})).unwrap()).collect();
    crate::select(&mut s, "a");
    let w = show(&mut s, "b");
    let env = rune::Env { system_dark: true, scale: 1.0 };
    let main = rune::AppSpec::<App> { poll: Some(crate::poll), ..rune::AppSpec::new("Agenthail", (1440.0, 900.0), crate::placeholder, crate::ui::view) };
    let mut main = rune::App::new(main).init(crate::placeholder).headless_runtime((1440.0, 900.0), env);
    let mut win = rune::App::new(spec()).init(crate::placeholder).headless_runtime((900.0, 820.0), env);
    for _ in 0..2 {
        turn(&mut s, &mut main, "main");
        turn(&mut s, &mut win, &key(w));
    }
    assert_eq!((s.windows.current(), s.selected.as_deref()), (MAIN, Some("a")), "between turns the app is the main window's");
    assert!(main.presented("session-tabs").is_some() && win.presented("session-tabs").is_none());
    assert_eq!(win.state.windows.open.len(), 0, "the window holds a placeholder outside its turn");
    std::mem::swap(&mut s, &mut win.state);
    on_enter(&mut win.state, &key(w));
    assert_eq!(win.state.selected.as_deref(), Some("b"));
    typed(&mut win.state, "from the window");
    on_leave(&mut win.state, &key(w));
    std::mem::swap(&mut s, &mut win.state);
    assert_eq!(s.drafts.get("b"), "from the window");
    assert_eq!(s.windows.open[0].title, "b", "the window is named for its session");
}

#[test]
fn a_session_window_draws_the_conversation_without_the_sidebar() {
    let mut s = app();
    s.sessions = ["a", "b"].iter().map(|id| serde_json::from_value(serde_json::json!({"id": id, "surface": "claude", "name": id, "status": "idle", "open": true})).unwrap()).collect();
    crate::select(&mut s, "a");
    let w = open(&mut s, "b");
    enter(&mut s, w);
    let spec = rune::AppSpec::<App> { poll: Some(poll), ..rune::AppSpec::new("Session", (900.0, 820.0), crate::init, crate::ui::session_window) };
    let mut rt = rune::App::new(spec).init(move || s).headless_runtime((900.0, 820.0), rune::Env { system_dark: true, scale: 1.0 });
    rt.frame(1.0 / 60.0);
    rt.frame(1.0 / 60.0);
    assert!(rt.presented("composer").is_some(), "the window shows its session and composer");
    assert!(rt.presented("sidebar-scroll").is_none(), "a session window has no sidebar");
    assert!(rt.presented("session-tabs").is_none(), "tabs live in the main window only");
    assert_eq!(rt.state.selected.as_deref(), Some("b"));
}

#[test]
fn a_result_for_a_window_that_is_not_in_front_lands_in_that_window() {
    let mut s = app();
    let w = open(&mut s, "b");
    enter(&mut s, w);
    s.menus.renaming = Some("b".into());
    enter(&mut s, MAIN);
    assert_eq!(holder(&s, |l| l.renaming() == Some("b")), Some(w));
    within(&mut s, w, |s| s.menus.rename_saving = true);
    assert!(s.windows.current() == MAIN && !s.menus.rename_saving);
    enter(&mut s, w);
    assert!(s.menus.rename_saving);
    assert_eq!(holder(&s, |l| l.renaming() == Some("b")), None, "the window in front isn't a holder");
}

#[test]
fn the_shown_session_keeps_its_transcript_when_windows_hold_the_rest() {
    let mut s = app();
    for i in 0..9 {
        open(&mut s, &format!("w{i}"));
    }
    crate::select(&mut s, "main");
    assert!(s.panes.contains_key("main"));
}

#[test]
fn a_rename_started_elsewhere_does_not_outdate_this_window_s_rename() {
    let mut s = app();
    let w = open(&mut s, "b");
    let mine = s.windows.stash[&w].rename_seq;
    enter(&mut s, w);
    let main = s.windows.stash[&MAIN].rename_seq;
    assert_ne!(mine, main, "each window counts its renames apart");
    enter(&mut s, MAIN);
    assert_eq!(s.windows.stash[&w].rename_seq, mine);
}

#[test]
fn a_result_goes_to_the_window_whose_request_it_answers_when_two_rename_one_session() {
    let mut s = app();
    crate::select(&mut s, "b");
    let w = open(&mut s, "b");
    s.menus.renaming = Some("b".into());
    enter(&mut s, w);
    s.menus.renaming = Some("b".into());
    let theirs = s.windows.stash[&MAIN].rename_seq;
    enter(&mut s, MAIN);
    let mine = s.windows.stash[&w].rename_seq;
    assert_eq!(holder(&s, |l| l.renaming() == Some("b") && l.rename_seq() == mine), Some(w));
    assert!(s.menus.renaming_with("b", theirs) && !s.menus.renaming_with("b", mine));
}

#[test]
fn the_image_cache_made_for_the_main_window_reaches_the_shared_app() {
    let mut s = app();
    let mut stand_in = crate::placeholder();
    crate::ui::attach::setup(&mut stand_in, std::sync::Arc::new(|| {}));
    assert!(stand_in.attach.cache.is_none() && s.attach.cache.is_none());
    crate::ui::attach::adopt(&mut s);
    assert!(s.attach.cache.is_some());
}
