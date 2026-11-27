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
    let zoomed = WindowPlacement { maximized: true, position: (0, 0), content_size: (3000, 1900), ..placed };
    let normal = Frame { x: 7, y: 8, width: 1000, height: 900, scale: 2.0, maximized: false, full_screen: false };
    assert_eq!(saved_frame(Some(normal), zoomed), Frame { maximized: true, ..normal }, "without a restore frame the last normal one is kept");
    assert_eq!(saved_frame(None, zoomed).width, 3000);
}

fn turn(s: &mut App, rt: &mut rune::Runtime<App>, key: &str) {
    turn_with(s, rt, key, |_, _| {});
}

fn turn_with(s: &mut App, rt: &mut rune::Runtime<App>, key: &str, f: impl FnOnce(&mut App, &mut rune::Tx) + 'static) {
    std::mem::swap(s, &mut rt.state);
    on_enter(&mut rt.state, key);
    rt.update(f);
    rt.frame(1.0 / 60.0);
    on_leave(&mut rt.state, key);
    std::mem::swap(s, &mut rt.state);
}

fn two_windows() -> (App, rune::Runtime<App>, rune::Runtime<App>, Id) {
    let mut s = app();
    s.sessions = ["a", "b"].iter().map(|id| serde_json::from_value(serde_json::json!({"id": id, "surface": "claude", "name": id, "status": "idle", "open": true})).unwrap()).collect();
    crate::select(&mut s, "a");
    let w = show(&mut s, "b");
    let env = rune::Env { system_dark: true, scale: 1.0 };
    let main = rune::AppSpec::<App> { ..rune::AppSpec::new("Agenthail", (1440.0, 900.0), crate::placeholder, crate::ui::view) };
    let main = rune::App::new(main).init(crate::placeholder).headless_runtime((1440.0, 900.0), env);
    let win = rune::App::new(spec()).init(crate::placeholder).headless_runtime((900.0, 820.0), env);
    (s, main, win, w)
}

#[test]
fn the_palette_shows_only_in_the_window_that_opened_it_until_the_shortcut_calls_it_to_main() {
    let (mut s, mut main, mut win, w) = two_windows();
    turn_with(&mut s, &mut win, &key(w), |s, tx| crate::over::open_jump(s, tx));
    turn(&mut s, &mut main, "main");
    turn(&mut s, &mut win, &key(w));
    assert_eq!((win.presented("jump-input").is_some(), main.presented("jump-input").is_some(), s.windows.overlay), (true, false, Some(w)));
    let esc = rune::KeyEvent { key: rune::Key::Escape, mods: rune::Mods::default() };
    turn_with(&mut s, &mut main, "main", move |s, tx| {
        crate::on_key(s, &esc, tx);
    });
    assert!(s.ov.jump.is_some() && s.windows.overlay == Some(w), "Escape in main leaves the other window's palette alone");
    let cmd = rune::Mods { cmd: true, ..rune::Mods::default() };
    let now = rune::KeyEvent { key: rune::Key::Command('s'), mods: rune::Mods { shift: true, ..cmd } };
    turn_with(&mut s, &mut main, "main", move |s, tx| {
        crate::on_key(s, &now, tx);
    });
    assert!(s.screen == crate::Screen::Sessions && s.ov.jump.is_some(), "main's own commands still work while the palette is elsewhere");
    let jump = rune::KeyEvent { key: rune::Key::Command('k'), mods: cmd };
    turn_with(&mut s, &mut main, "main", move |s, tx| {
        crate::on_key(s, &jump, tx);
    });
    assert_eq!(s.windows.overlay, Some(MAIN), "Command-K in main brings the open palette over");
    turn_with(&mut s, &mut win, &key(w), |s, _| overlays_to(s, s.windows.current()));
    turn_with(&mut s, &mut main, "main", |s, _| overlays_to(s, MAIN));
    turn(&mut s, &mut win, &key(w));
    turn(&mut s, &mut main, "main");
    assert!(main.presented("jump-input").is_some() && win.presented("jump-input").is_none());
    turn_with(&mut s, &mut main, "main", |s, _| s.ov.jump = None);
    assert_eq!(s.windows.overlay, None);
}

#[test]
fn a_session_window_opens_with_its_inspector_hidden_and_its_toolbar_shows_it() {
    let (mut s, mut main, mut win, w) = two_windows();
    let main_open = s.ov.inspector.is_open();
    turn(&mut s, &mut main, "main");
    turn(&mut s, &mut win, &key(w));
    assert!(win.presented("inspector-toggle").is_some());
    enter(&mut s, w);
    assert!(!s.ov.inspector.is_open());
    enter(&mut s, MAIN);
    turn_with(&mut s, &mut win, &key(w), |s, tx| crate::ui::keys::run(s, crate::ui::keys::Act::ToggleInspector, tx));
    enter(&mut s, w);
    assert!(s.ov.inspector.is_open(), "the inspector command opens it in the window it ran in");
    enter(&mut s, MAIN);
    assert_eq!(s.ov.inspector.is_open(), main_open, "the main window's inspector is its own");
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

#[test]
fn a_result_for_a_session_opens_in_the_window_showing_it() {
    let (mut s, _main, _win, w) = two_windows();
    s.inspect.logs = Some(("b".into(), "log lines".into()));
    overlay_for(&mut s, MAIN + 99, "b");
    assert_eq!(s.windows.overlay, Some(w));
    assert!(!crate::ui::inspect::drain(&mut s) && s.inspect.logs.is_some(), "logs for the session window's session stay open while main shows another");
    assert_eq!(showing(&s, "a"), Some(MAIN));
    assert_eq!(showing(&s, "zzz"), None);
}

#[test]
fn opening_a_second_kind_of_dialog_in_another_window_takes_them_there() {
    let (mut s, mut main, mut win, w) = two_windows();
    turn_with(&mut s, &mut main, "main", |s, tx| crate::over::open_jump(s, tx));
    assert_eq!(s.windows.overlay, Some(MAIN));
    turn_with(&mut s, &mut win, &key(w), |s, _| s.prs.linking = Some("b".into()));
    assert_eq!(s.windows.overlay, Some(w));
}

#[test]
fn a_result_goes_to_the_window_that_asked_even_when_both_show_the_session() {
    let mut s = app();
    crate::select(&mut s, "b");
    let w = open(&mut s, "b");
    s.inspect.logs = Some(("b".into(), "log lines".into()));
    overlay_for(&mut s, w, "b");
    assert_eq!(s.windows.overlay, Some(w), "the asker wins even though main shows the session too");
    overlay_for(&mut s, MAIN, "b");
    assert_eq!(s.windows.overlay, Some(MAIN), "a later request from main is routed by its own asker");
    close(&mut s, w);
    overlay_for(&mut s, w, "b");
    assert_eq!(s.windows.overlay, Some(MAIN), "a closed asker falls back to the window showing the session");
}

#[test]
fn a_command_run_in_one_window_leaves_another_window_s_palette_open() {
    let (mut s, mut main, mut win, w) = two_windows();
    turn_with(&mut s, &mut win, &key(w), |s, tx| crate::over::open_jump(s, tx));
    turn_with(&mut s, &mut main, "main", |s, tx| crate::over::search::run(s, feed::search::Command::Refresh, tx));
    assert!(s.ov.jump.is_some() && s.windows.overlay == Some(w));
    turn_with(&mut s, &mut win, &key(w), |s, tx| crate::over::search::run(s, feed::search::Command::Refresh, tx));
    assert!(s.ov.jump.is_none(), "a command picked in the palette's own window closes it");
}
