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

fn press(s: &mut App, rt: &mut rune::Runtime<App>, key: &str, ev: rune::KeyEvent) {
    turn_with(s, rt, key, move |s, tx| {
        crate::on_key(s, &ev, tx);
    });
}

const ESCAPE: rune::KeyEvent = rune::KeyEvent { key: rune::Key::Escape, mods: rune::Mods { shift: false, cmd: false, alt: false, ctrl: false, logo: false } };

fn confirm(session: &str) -> crate::ui::limit::Confirm {
    let target = feed::limit::Target { root: "/r".into(), name: "Work".into(), detail: None, full: false, offered: true };
    crate::ui::limit::Confirm { session: session.into(), since: "t".into(), target }
}

#[test]
fn a_dialog_stays_with_its_window_when_another_window_opens_the_palette_and_keys_go_to_each_one_s_own() {
    let (mut s, mut main, mut win, w) = two_windows();
    turn_with(&mut s, &mut win, &key(w), |s, _| s.limits.confirm = Some(confirm("b")));
    turn_with(&mut s, &mut main, "main", |s, tx| crate::over::open_jump(s, tx));
    turn(&mut s, &mut win, &key(w));
    turn(&mut s, &mut main, "main");
    assert_eq!((owner(&s, Kind::LimitConfirm), owner(&s, Kind::Jump)), (Some(w), Some(MAIN)));
    assert_eq!((win.presented("limit-confirm-cancel").is_some(), main.presented("limit-confirm-cancel").is_some()), (true, false));
    assert!(main.presented("jump-input").is_some() && win.presented("jump-input").is_none());
    press(&mut s, &mut win, &key(w), ESCAPE);
    assert!(s.ov.jump.is_some(), "Escape in the session window doesn't reach main's palette");
    press(&mut s, &mut main, "main", ESCAPE);
    assert!(s.ov.jump.is_none(), "Escape in main closes its palette");
    assert_eq!((s.limits.confirm.as_ref().map(|c| c.session.as_str()), owner(&s, Kind::LimitConfirm)), (Some("b"), Some(w)), "the confirm never moved");
}

#[test]
fn opening_a_kind_another_window_has_open_leaves_it_there() {
    let (mut s, mut main, mut win, w) = two_windows();
    turn_with(&mut s, &mut win, &key(w), |s, _| s.limits.confirm = Some(confirm("b")));
    turn_with(&mut s, &mut main, "main", |s, _| assert!(!may_open(s, Kind::LimitConfirm), "main may not open a second confirm"));
    assert!(may_open(&s, Kind::PrLink));
    assert_eq!(owner(&s, Kind::LimitConfirm), Some(w));
    close(&mut s, w);
    assert_eq!(owner(&s, Kind::LimitConfirm), None, "closing the owner frees its dialogs");
}

#[test]
fn the_palette_lives_in_main_and_command_k_in_a_session_window_opens_it_there() {
    let (mut s, mut main, mut win, w) = two_windows();
    let cmd_k = || rune::KeyEvent { key: rune::Key::Command('k'), mods: rune::Mods { cmd: true, ..rune::Mods::default() } };
    press(&mut s, &mut win, &key(w), cmd_k());
    assert_eq!((s.ov.jump.is_some(), owner(&s, Kind::Jump)), (true, Some(MAIN)));
    turn_with(&mut s, &mut main, "main", |s, _| {
        if s.ov.jump.is_some() {
            s.ov.jump = Some(rune::TextEdit::new("fix"));
        }
    });
    press(&mut s, &mut win, &key(w), cmd_k());
    assert_eq!(s.ov.jump.as_ref().map(|j| j.text.as_str()), Some("fix"), "pressing it again in the session window keeps main's palette and its query");
    turn_with(&mut s, &mut win, &key(w), |s, tx| crate::over::search::run(s, feed::search::Command::Refresh, tx));
    assert!(s.ov.jump.is_some(), "a command in the session window leaves main's palette open");
    turn(&mut s, &mut main, "main");
    assert!(main.presented("jump-input").is_some());
    press(&mut s, &mut main, "main", cmd_k());
    assert!(s.ov.jump.is_none());
}

#[test]
fn a_result_goes_to_the_window_that_asked_or_else_the_one_showing_its_session() {
    let mut s = app();
    crate::select(&mut s, "b");
    let w = open(&mut s, "b");
    assert_eq!(asker_or_showing(&s, w, "b"), Some(w), "the asker wins even though main shows the session too");
    assert_eq!(asker_or_showing(&s, MAIN, "b"), Some(MAIN));
    assert_eq!(asker_or_showing(&s, w + 9, "b"), Some(MAIN), "a closed asker falls back to the window showing the session");
    assert_eq!(asker_or_showing(&s, w + 9, "zzz"), None);
    s.inspect.logs = Some(("b".into(), "log lines".into()));
    give(&mut s, Kind::Logs, w);
    assert_eq!(owner(&s, Kind::Logs), Some(w));
    assert!(is_open(&s, Kind::Logs), "a second logs result finds the kind open and goes to the Process section instead");
}

#[test]
fn a_move_from_one_window_leaves_another_window_s_limit_dialog_and_new_session_leaves_activity_alone() {
    let (mut s, mut main, mut win, w) = two_windows();
    turn_with(&mut s, &mut win, &key(w), |s, _| s.limits.confirm = Some(confirm("b")));
    turn_with(&mut s, &mut main, "main", |s, _| s.limits.picking = Some(("a".into(), "t".into())));
    assert_eq!((owner(&s, Kind::LimitConfirm), owner(&s, Kind::LimitPick)), (Some(w), Some(MAIN)));
    assert!(together(&s, Kind::LimitPick, Kind::Jump) && !together(&s, Kind::LimitConfirm, Kind::LimitPick));
}

fn codex(id: &str) -> feed::catalog::Session {
    feed::catalog::Session { id: id.into(), surface: "codex".into(), ..Default::default() }
}

#[test]
fn a_session_window_keeps_its_codex_queue_and_editor_while_main_shows_another_session() {
    use crate::ui::native_queue::{Editor, drain, queue};
    let mut s = app();
    s.sessions = vec![codex("a"), codex("b")];
    crate::select(&mut s, "a");
    s.screen = Screen::Session;
    let w = open(&mut s, "b");
    assert!(queue(&s, "a").is_some() && queue(&s, "b").is_some(), "each shown session has its own queue");
    s.native_queue.editor = Some(Editor { session: "b".into(), item: None, original: String::new(), text: rune::TextEdit::new("then tag"), saving: None, error: None, unknown: true });
    give(&mut s, Kind::Queue, w);
    drain(&mut s);
    assert!(s.native_queue.editor.as_ref().is_some_and(|e| e.session == "b" && e.unknown), "main's poll keeps the window's editor and its unknown outcome");
    assert!(queue(&s, "a").is_some() && queue(&s, "b").is_some(), "main's poll keeps the window's queue");
    assert_eq!(owner(&s, Kind::Queue), Some(w));
    close(&mut s, w);
    drain(&mut s);
    assert!(s.native_queue.editor.is_none() && queue(&s, "b").is_none() && queue(&s, "a").is_some(), "closing the window drops only its session's queue");
}

#[test]
fn a_fork_result_lands_in_the_window_that_asked() {
    use crate::ui::complete::{Done, done};
    let mut s = app();
    crate::select(&mut s, "a");
    let w = open(&mut s, "b");
    let refused = feed::catalog::RespondError::from_status("session-fork", 400, r#"{"error":{"code":"invalid_request","message":"no fork here"}}"#);
    done(&mut s, Done::Forked("b".into(), "b/".into(), String::new(), "k1".into(), w, Err(refused.clone())));
    assert_eq!((s.ov.notice.as_deref(), s.status_line.get("b").map(String::as_str)), (None, Some("Couldn't fork: no fork here")), "a refusal shows in the window that asked, not main's notice");
    let interrupted = feed::catalog::RespondError::from_status("session-fork", 409, r#"{"error":{"code":"action_outcome_unknown","message":"This request was interrupted before its outcome was recorded, so it may have happened."}}"#);
    done(&mut s, Done::Forked("b".into(), "b/".into(), String::new(), "k2".into(), w, Err(interrupted)));
    assert!(s.ov.notice.is_none() && s.status_line.get("b").is_some_and(|l| l.ends_with("Fork again as new sends a new request.")));
    within(&mut s, w, |s| s.composer_for = None);
    done(&mut s, Done::Forked("b".into(), "b/".into(), String::new(), "k3".into(), w, Ok("c".into())));
    assert!(s.windows.of("c").is_none());
    assert!(s.ov.notice.is_none() && s.status_line.get("b").is_some_and(|l| l.starts_with("The fork is ready")));
    within(&mut s, w, |s| s.composer_for = Some("b".into()));
    done(&mut s, Done::Forked("b".into(), "b/".into(), String::new(), "k4".into(), w, Ok("d".into())));
    assert!(s.windows.of("d").is_some(), "a fork from a session window opens in its own window");
    assert_eq!((s.windows.current(), s.selected.as_deref()), (MAIN, Some("a")), "main stays where it was");
    done(&mut s, Done::Forked("a".into(), "a/".into(), String::new(), "k5".into(), MAIN, Err(refused)));
    assert_eq!(s.ov.notice.as_deref(), Some("Couldn't fork: no fork here"), "a fork from main tells main");
}

#[test]
fn a_codex_queue_row_menu_open_in_one_window_is_not_taken_over_by_another() {
    let mut s = app();
    s.sessions = vec![codex("a"), codex("b")];
    crate::select(&mut s, "a");
    s.screen = Screen::Session;
    let w = open(&mut s, "b");
    enter(&mut s, w);
    s.native_queue.editor = None;
    crate::ui::native_queue::open_row_menu(&mut s, "b", "q1");
    claim(&mut s);
    enter(&mut s, MAIN);
    crate::ui::native_queue::open_row_menu(&mut s, "a", "q9");
    assert_eq!((owner(&s, Kind::Queue), s.native_queue.row_menu()), (Some(w), Some(("b", "q1"))), "the menu stays with the window that opened it");
}

#[test]
fn a_session_list_rebuilds_the_overview_once_with_a_session_window_open() {
    let (mut s, _, mut win, w) = two_windows();
    let env = rune::Env { system_dark: true, scale: 1.0 };
    let main = rune::AppSpec::<App> { poll: Some(crate::poll), ..rune::AppSpec::new("Agenthail", (1440.0, 900.0), crate::placeholder, crate::ui::view) };
    let mut main = rune::App::new(main).init(crate::placeholder).headless_runtime((1440.0, 900.0), env);
    let rows = |ids: &[&str]| ids.iter().map(|id| serde_json::from_value(serde_json::json!({"id": id, "surface": "claude", "name": id, "status": "idle", "open": true, "lastActive": "2026-11-27T09:00:00Z"})).unwrap()).collect::<Vec<feed::catalog::Session>>();
    let both = |s: &mut App, main: &mut rune::Runtime<App>, win: &mut rune::Runtime<App>| {
        for _ in 0..2 {
            turn(s, main, "main");
            turn(s, win, &key(w));
        }
    };
    s.outbox.send(crate::Bg::Snapshot("2026-11-27T10:00:10Z".into(), String::new(), 0, rows(&["a", "b"]), None)).unwrap();
    both(&mut s, &mut main, &mut win);
    let before = s.rebuilds;

    s.outbox.send(crate::Bg::Snapshot("2026-11-27T10:00:20Z".into(), String::new(), 0, rows(&["a", "b"]), None)).unwrap();
    both(&mut s, &mut main, &mut win);
    assert_eq!(s.rebuilds, before, "the same list rebuilds nothing in either window");

    s.outbox.send(crate::Bg::Snapshot("2026-11-27T10:00:30Z".into(), String::new(), 0, rows(&["a", "b", "c"]), None)).unwrap();
    both(&mut s, &mut main, &mut win);
    assert_eq!(s.rebuilds, before + 1, "a change rebuilds once for the app, not once per window");
}

fn beside(queued: bool) -> (App, Id) {
    let mut s = app();
    s.client = Some(feed::catalog::Client::new("http://127.0.0.1:9", "t"));
    let row = |id: &str, count: i64| serde_json::from_value(serde_json::json!({"id": id, "surface": "codex", "name": id, "status": "idle", "open": true, "queueCount": count})).unwrap();
    s.sessions = vec![row("a", 0), row("b", if queued { 1 } else { 0 })];
    crate::select(&mut s, "a");
    let w = open(&mut s, "b");
    s.queue_fetching.clear();
    (s, w)
}

#[test]
fn a_change_to_the_session_a_window_shows_refreshes_it_while_main_shows_another() {
    let (mut s, _) = beside(true);
    crate::changed_elsewhere(&mut s, feed::watch::Wants { session: Some("b".into()), ..Default::default() });
    assert!(s.queue_fetching.contains_key("b"), "the window's session is read again, not only main's");
    assert_eq!((s.windows.current(), s.selected.as_deref()), (MAIN, Some("a")), "main is left as it was");
}

#[test]
fn a_reconnect_rereads_the_codex_queue_of_every_window() {
    let (mut s, _) = beside(false);
    for id in ["a", "b"] {
        crate::ui::native_queue::hold(&mut s, id);
    }
    crate::reopened(&mut s);
    assert!(["a", "b"].iter().all(|id| crate::ui::native_queue::reading(&s, id)), "both shown queues are read again");
    assert_eq!(s.selected.as_deref(), Some("a"));
}

#[test]
fn a_session_window_on_changes_follows_new_edits() {
    let (mut s, _, _, w) = two_windows();
    let env = rune::Env { system_dark: true, scale: 1.0 };
    let main = rune::AppSpec::<App> { poll: Some(crate::poll), ..rune::AppSpec::new("Agenthail", (1440.0, 900.0), crate::placeholder, crate::ui::view) };
    let mut main = rune::App::new(main).init(crate::placeholder).headless_runtime((1440.0, 900.0), env);
    let edit = |n: usize| {
        let item: feed::Item = serde_json::from_value(serde_json::json!({"id": format!("e{n}"), "kind": "toolCall", "title": "Write", "text": serde_json::json!({"file_path": format!("/w/f{n}.txt"), "content": "hi"}).to_string()})).unwrap();
        feed::wire::Event::Upsert(item)
    };
    let key = |s: &mut App| {
        let mut key = (String::new(), 0);
        within(s, w, |s| key = s.diff_key.clone());
        key
    };
    s.panes.get_mut("b").unwrap().transcript.apply(1, vec![edit(1)]);
    within(&mut s, w, crate::show_changes);
    assert_eq!(key(&mut s), ("b".to_string(), 1));

    s.panes.get_mut("b").unwrap().transcript.apply(2, vec![edit(2)]);
    s.outbox.send(crate::Bg::SessionsMoved).unwrap();
    turn(&mut s, &mut main, "main");
    assert_eq!(key(&mut s), ("b".to_string(), 2), "the window's diff follows the edit");
    assert_eq!(s.diff_key.0, "", "main's own diff is untouched");
}

#[test]
fn a_session_main_left_for_now_is_refreshed_as_the_window_that_shows_it() {
    let (mut s, w) = beside(false);
    crate::select(&mut s, "b");
    s.screen = Screen::Now;
    assert_eq!(shown(&s), vec![(w, "b".to_string())], "the window on the session, not main on Now");
    crate::select(&mut s, "a");
    s.screen = Screen::Session;
    let mut ids: Vec<String> = shown(&s).into_iter().map(|(_, id)| id).collect();
    ids.sort();
    assert_eq!(ids, ["a", "b"]);
}

#[test]
fn a_role_read_that_failed_for_a_window_s_session_is_tried_again() {
    let (mut s, _) = beside(false);
    s.screen = Screen::Session;
    crate::ui::role::forget(&mut s);
    crate::ui::role::failed_a_while_ago(&mut s, "b");
    crate::ui::role::drain(&mut s);
    assert!(crate::ui::role::asked(&s, "b"), "retried though main shows another session");
    crate::ui::role::load(&mut s, "a");
    assert!(crate::ui::role::asked(&s, "b"), "selecting in main keeps what a window shows");
}

#[test]
fn subagent_rows_a_window_shows_are_held_while_main_shows_another_session() {
    let (s, _) = beside(false);
    assert!(crate::ui::subagents::holds(&s, "a") && crate::ui::subagents::holds(&s, "b"));
}

#[test]
fn a_missing_session_in_each_window_gets_its_own_lookup() {
    let (mut s, _) = beside(false);
    s.snapshots = 1;
    crate::select(&mut s, "x");
    s.screen = Screen::Session;
    let w = open(&mut s, "y");
    assert!(crate::ui::gone::watch(&mut s));
    assert!(s.missing.contains_key("x") && s.missing.contains_key("y"), "main's and the window's missing sessions are both looked up");
    close(&mut s, w);
    crate::ui::gone::watch(&mut s);
    assert!(!s.missing.contains_key("y"), "a session no window shows is let go");
}

#[test]
fn failed_session_settings_are_retried_for_every_window() {
    let (mut s, _) = beside(false);
    s.screen = Screen::Session;
    for id in ["a", "b"] {
        crate::ui::session_menu::failed_a_while_ago(&mut s, id);
    }
    crate::ui::session_menu::drain(&mut s);
    assert!(["a", "b"].iter().all(|id| crate::ui::session_menu::asked(&s, id)), "each shown session's settings are read again");
}

fn goal(text: &str) -> feed::wire::Streamed {
    feed::wire::Streamed::Goal(Some(feed::inspect::Goal { objective: text.into(), ..Default::default() }))
}

#[test]
fn a_window_s_session_coming_back_leaves_main_s_inspector_stream_alone() {
    let (mut s, _) = beside(false);
    s.screen = Screen::Session;
    crate::ui::inspect::streamed(&mut s, "a", goal("main's goal"));
    s.snapshots = 1;
    let rows = s.sessions.clone();
    s.sessions.retain(|x| x.id != "b");
    crate::ui::gone::watch(&mut s);
    s.sessions = rows;
    crate::ui::gone::watch(&mut s);
    assert!(crate::ui::inspect::is_streamed(&s, "a", "goal"), "b returning in its window doesn't touch main's stream");
}

#[test]
fn each_window_s_inspector_takes_its_own_session_s_streamed_goal() {
    let (mut s, w) = beside(false);
    s.screen = Screen::Session;
    crate::ui::inspect::streamed(&mut s, "a", goal("main's goal"));
    crate::ui::inspect::streamed(&mut s, "b", goal("the window's goal"));
    assert!(crate::ui::inspect::is_streamed(&s, "a", "goal") && crate::ui::inspect::is_streamed(&s, "b", "goal"), "both windows stream");
    crate::select(&mut s, "c");
    assert!(crate::ui::inspect::is_streamed(&s, "b", "goal"), "main moving on leaves the window's stream");
    close(&mut s, w);
    assert!(!crate::ui::inspect::holds_any(&s, "b"), "the stream is let go with its window");
    crate::ui::inspect::streamed(&mut s, "b", goal("late"));
    assert!(!crate::ui::inspect::holds_any(&s, "b"), "and nothing shows it, so a late event is ignored");
}

#[test]
fn selecting_a_session_another_window_shows_keeps_its_stream_ahead_of_reads() {
    let (mut s, _) = beside(false);
    s.screen = Screen::Session;
    crate::ui::inspect::streamed(&mut s, "b", goal("streamed"));
    crate::select(&mut s, "b");
    assert!(crate::ui::inspect::is_streamed(&s, "b", "goal"), "a read landing now must not undo the window's streamed goal");
}

#[test]
fn a_rekeyed_session_s_new_id_streams_at_once() {
    let (mut s, _) = beside(false);
    s.screen = Screen::Session;
    s.selected = Some("a2".into());
    crate::ui::inspect::streamed(&mut s, "a2", goal("after the rekey"));
    assert!(crate::ui::inspect::is_streamed(&s, "a2", "goal"));
}

#[test]
fn main_coming_back_to_its_session_finds_its_inspector_state_kept() {
    let (mut s, w) = beside(false);
    s.screen = Screen::Session;
    crate::ui::inspect::streamed(&mut s, "a", goal("main's goal"));
    crate::ui::inspect::expand(&mut s, "a");
    s.screen = Screen::Now;
    within(&mut s, w, |s| crate::select(s, "c"));
    s.screen = Screen::Session;
    assert!(crate::ui::inspect::is_streamed(&s, "a", "goal") && crate::ui::inspect::expanded(&s, "a"), "another window's selection keeps a's stream and expanded goal");
    crate::ui::inspect::streamed(&mut s, "a", goal("newer"));
    assert!(crate::ui::inspect::is_streamed(&s, "a", "goal"));
}

#[test]
fn selecting_the_session_already_shown_keeps_its_stream_ahead_of_reads() {
    let (mut s, _) = beside(false);
    s.screen = Screen::Session;
    crate::ui::inspect::streamed(&mut s, "a", goal("streamed"));
    crate::select(&mut s, "a");
    assert!(crate::ui::inspect::is_streamed(&s, "a", "goal"));
}

#[test]
fn a_stream_missed_while_hidden_is_read_again_when_the_session_shows() {
    let (mut s, _) = beside(false);
    s.screen = Screen::Session;
    crate::ui::inspect::streamed(&mut s, "a", goal("first"));
    s.screen = Screen::Now;
    crate::ui::inspect::streamed(&mut s, "a", goal("while hidden"));
    assert!(!crate::ui::inspect::is_streamed(&s, "a", "goal") && crate::ui::inspect::missed(&s, "a"), "the next read may replace the stale goal");
    crate::ui::inspect::drain(&mut s);
    assert!(crate::ui::inspect::missed(&s, "a"), "nothing is read while it stays hidden");
    s.screen = Screen::Session;
    crate::ui::inspect::drain(&mut s);
    assert!(!crate::ui::inspect::missed(&s, "a"), "coming back reads it again");
}

#[test]
fn a_rekey_carries_the_inspector_to_the_new_id() {
    let (mut s, _) = beside(false);
    s.screen = Screen::Session;
    crate::ui::inspect::streamed(&mut s, "a", goal("streamed"));
    crate::ui::inspect::expand(&mut s, "a");
    crate::follow_rekeys(&mut s, HashMap::from([("a".to_string(), "a2".to_string())]));
    assert_eq!(s.selected.as_deref(), Some("a2"));
    assert!(crate::ui::inspect::is_streamed(&s, "a2", "goal") && crate::ui::inspect::expanded(&s, "a2") && crate::ui::inspect::has_meta(&s, "a2"));
}

#[test]
fn a_rekey_keeps_the_streamed_context_and_lands_late_reads_on_the_new_id() {
    let (mut s, _) = beside(false);
    s.screen = Screen::Session;
    let usage: feed::settings::ContextUsage = serde_json::from_value(serde_json::json!({"usedTokens": 10, "contextWindow": 100})).unwrap();
    crate::ui::inspect::streamed(&mut s, "a", feed::wire::Streamed::Context(Some(usage)));
    let ticket = crate::ui::inspect::ticket(&mut s);
    crate::follow_rekeys(&mut s, HashMap::from([("a".to_string(), "a2".to_string())]));
    assert!(s.menus.context.contains_key("a2") && !s.menus.context.contains_key("a"), "the streamed context moves with the session");
    crate::ui::inspect::read(&mut s, "a", ticket, Ok(feed::inspect::Metadata::default()));
    assert!(crate::ui::inspect::has_meta(&s, "a2") && !crate::ui::inspect::has_meta(&s, "a"), "a read asked under the old id lands on the new one");
}

#[test]
fn a_read_landing_after_its_window_closed_is_not_kept() {
    let (mut s, w) = beside(false);
    s.screen = Screen::Session;
    close(&mut s, w);
    crate::ui::inspect::read(&mut s, "b", 1, Ok(feed::inspect::Metadata::default()));
    assert!(!crate::ui::inspect::has_meta(&s, "b"));
}
