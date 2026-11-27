use std::collections::HashMap;
use std::path::PathBuf;

use feed::catalog::Session;
use feed::derive::Recorded;
use feed::derive::live::{Fetched, Overview};
use rune::{AppSpec, Env, Runtime};
use serde_json::json;

use crate::transcript::Pane;
use crate::{App, Bg, Fixtures, Screen};

const DT: f64 = 1.0 / 60.0;
const EPOCH: &str = "e1";

fn at(seq: u64) -> String {
    format!("2026-11-27T10:{:02}:00Z", seq % 60)
}

fn session(id: &str, minutes_ago: u64, extra: serde_json::Value) -> Session {
    let mut row = json!({
        "id": id,
        "surface": "claude",
        "name": id,
        "status": "idle",
        "lastActive": format!("2026-11-27T08:{:02}:00Z", 59 - minutes_ago.min(59)),
        "open": true,
        "project": {"id": "p1", "name": "Alpha"},
    });
    if let (Some(row), Some(extra)) = (row.as_object_mut(), extra.as_object()) {
        row.extend(extra.clone());
    }
    serde_json::from_value(row).unwrap()
}

fn sessions(ids: &[&str]) -> Vec<Session> {
    ids.iter().enumerate().map(|(i, id)| session(id, i as u64, json!({}))).collect()
}

fn recorded(seq: u64, sessions: Vec<Session>) -> Recorded {
    let mut raw: Recorded = serde_json::from_value(json!({
        "updatedAt": at(seq),
        "hostEpoch": EPOCH,
        "catalogSeq": seq,
        "projects": [{"id": "p1", "displayName": "Alpha", "lastActive": at(seq)}],
    }))
    .unwrap();
    raw.sessions = sessions;
    raw
}

fn snapshot(seq: u64, sessions: Vec<Session>, touched: Option<&[&str]>) -> Bg {
    Bg::Snapshot(at(seq), EPOCH.into(), seq, sessions, touched.map(|t| t.iter().map(|x| x.to_string()).collect()))
}

fn overview(seq: u64, sessions: Vec<Session>) -> Bg {
    Bg::Overview(Box::new(Fetched { recorded: recorded(seq, sessions), ledgers: HashMap::new(), held: Vec::new(), asked: Vec::new(), started: std::time::Instant::now(), usage: None, moved: Vec::new() }))
}

struct Dir(PathBuf);

impl Dir {
    fn new(name: &str) -> Dir {
        let unique = std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap_or_default().as_nanos();
        let dir = std::env::temp_dir().join(format!("agenthail-loop-{}-{unique}-{name}", std::process::id()));
        std::fs::create_dir_all(dir.join("items")).unwrap();
        Dir(dir)
    }

    fn path(&self) -> String {
        self.0.display().to_string()
    }
}

impl Drop for Dir {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

fn isolated(test: &str) -> bool {
    if std::env::var_os("AGENTHAIL_LOOP_CHILD").is_some() {
        return true;
    }
    let home = Dir::new(&format!("home-{test}"));
    let api = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    api.set_nonblocking(true).unwrap();
    let out = std::process::Command::new(std::env::current_exe().unwrap())
        .args([&format!("loop_tests::{test}"), "--exact", "--nocapture", "--test-threads=1"])
        .env("AGENTHAIL_LOOP_CHILD", "1")
        .env("HOME", &home.0)
        .env("CFFIXED_USER_HOME", &home.0)
        .env("AGENTHAIL_API_URL", format!("http://{}", api.local_addr().unwrap()))
        .env_remove("AGENTHAIL_API_TOKEN")
        .env_remove("AGENTHAIL_FIXTURES")
        .env_remove("XDG_CONFIG_HOME")
        .env_remove("XDG_DATA_HOME")
        .env_remove("XDG_STATE_HOME")
        .output()
        .unwrap();
    let said = format!("{}{}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr));
    assert!(out.status.success() && said.contains("1 passed"), "{said}");
    assert!(api.accept().is_err_and(|e| e.kind() == std::io::ErrorKind::WouldBlock), "the test reached the API");
    false
}

fn app(sessions: Vec<Session>, dir: &Dir) -> App {
    let mut app = crate::empty(Overview::recorded(recorded(1, sessions)), false);
    app.sessions = app.ov.d.sessions.clone();
    app.now = app.ov.d.now;
    app.fixtures = Some(Fixtures { dir: dir.path(), streams: HashMap::new(), speed: 1.0 });
    app.status = "fixtures".into();
    app
}

fn runtime(app: App, size: (f32, f32)) -> Runtime<App> {
    let spec = AppSpec::<App> {
        poll: Some(crate::poll),
        on_key: Some(crate::on_key),
        fonts: rune_theme_opencode::FONTS,
        families: rune_theme_opencode::FAMILIES,
        ..AppSpec::new("Agenthail", size, crate::init, crate::ui::view)
    };
    let mut rt = rune::App::new(spec).init(move || app).headless_runtime(size, Env { system_dark: true, scale: 1.0 });
    rt.frame(DT);
    rt.frame(DT);
    rt
}

fn send(rt: &Runtime<App>, message: Bg) {
    rt.state.outbox.send(message).unwrap();
}

fn wake_in(rt: &Runtime<App>) -> f64 {
    rt.wake_deadline().expect("the loop always asks to wake") - rt.state.clock
}

fn shown_in_sidebar(rt: &Runtime<App>, id: &str) -> bool {
    let Some(([_, top, _, height], _)) = rt.presented("sidebar-scroll") else { return false };
    let Some(([_, y, _, h], _)) = rt.presented(&crate::over::ui::session_key(id)) else { return false };
    y >= top && y + h <= top + height
}

#[test]
fn a_burst_of_updates_in_one_frame_rebuilds_the_overview_once_from_the_newest() {
    if !isolated("a_burst_of_updates_in_one_frame_rebuilds_the_overview_once_from_the_newest") {
        return;
    }
    let dir = Dir::new("burst");
    let mut rt = runtime(app(sessions(&["a"]), &dir), (1440.0, 900.0));
    let before = rt.state.rebuilds;

    send(&rt, snapshot(2, sessions(&["a", "b"]), None));
    send(&rt, snapshot(3, sessions(&["a", "b", "c"]), None));
    send(&rt, overview(4, sessions(&["a", "b", "c", "d"])));
    send(&rt, snapshot(5, sessions(&["a", "b", "c", "d", "e"]), None));
    send(&rt, snapshot(6, sessions(&["a", "b", "c", "d", "e", "f"]), None));
    rt.frame(DT);

    assert!(rt.state.inbox.try_recv().is_err(), "one frame takes every queued message");
    assert!(!rt.state.ov.live.stale(), "the overview is rebuilt in the same frame");
    assert_eq!(rt.state.rebuilds, before + 1, "five updates in one frame rebuild the overview once");
    assert_eq!(rt.state.ov.d.sessions.len(), 6);
    assert!(rt.state.ov.d.session("f").is_some());
    assert_eq!(rt.state.sessions.len(), 6);

    rt.frame(DT);
    rt.frame(DT);
    assert_eq!(rt.state.rebuilds, before + 1, "quiet frames don't rebuild again");
}

#[test]
fn an_older_snapshot_in_a_burst_does_not_replace_a_newer_one() {
    if !isolated("an_older_snapshot_in_a_burst_does_not_replace_a_newer_one") {
        return;
    }
    let dir = Dir::new("older");
    let mut rt = runtime(app(sessions(&["a"]), &dir), (1440.0, 900.0));

    let before = rt.state.rebuilds;
    send(&rt, snapshot(5, sessions(&["a", "b", "c"]), None));
    send(&rt, snapshot(3, sessions(&["a"]), None));
    rt.frame(DT);

    assert_eq!(rt.state.rebuilds, before + 1);
    assert_eq!(rt.state.ov.d.sessions.len(), 3);
    assert!(!rt.state.ov.live.stale());
}

fn bare(_: &App, _: &mut rune::Cx<App>) -> rune::El {
    rune::col()
}

fn unframed(app: App) -> Runtime<App> {
    let spec = AppSpec::<App> { poll: Some(crate::poll), ..AppSpec::new("Agenthail", (1440.0, 900.0), crate::init, bare) };
    rune::App::new(spec).init(move || app).headless_runtime((1440.0, 900.0), Env { system_dark: true, scale: 1.0 })
}

fn streaming(app: &mut App, id: &str) -> std::sync::mpsc::Sender<feed::source::Message> {
    let (stream, live) = std::sync::mpsc::channel();
    let mut pane = Pane::new();
    pane.live = Some(live);
    app.panes.insert(id.into(), pane);
    stream
}

#[test]
fn an_idle_loop_asks_to_wake_in_a_quarter_second_and_keeps_that_pace() {
    if !isolated("an_idle_loop_asks_to_wake_in_a_quarter_second_and_keeps_that_pace") {
        return;
    }
    let dir = Dir::new("idle");
    let mut rt = unframed(app(sessions(&["a"]), &dir));
    rt.frame(DT);
    assert!((wake_in(&rt) - 0.250).abs() < 1e-9, "idle wake in {}", wake_in(&rt));
    for _ in 0..4 {
        let polled = rt.state.clock;
        rt.frame(wake_in(&rt));
        assert!((rt.state.clock - polled - 0.250).abs() < 1e-9);
        assert!((wake_in(&rt) - 0.250).abs() < 1e-9, "idle wake in {}", wake_in(&rt));
    }
}

#[test]
fn polls_run_early_by_other_frames_do_not_add_wakes() {
    if !isolated("polls_run_early_by_other_frames_do_not_add_wakes") {
        return;
    }
    let dir = Dir::new("early");
    let mut rt = unframed(app(sessions(&["a"]), &dir));
    rt.frame(DT);
    for _ in 0..10 {
        rt.frame(0.03);
    }
    rt.frame(wake_in(&rt));
    for _ in 0..8 {
        let polled = rt.state.clock;
        rt.frame(wake_in(&rt));
        assert!(rt.state.clock - polled > 0.249, "woke {} after the last poll", rt.state.clock - polled);
    }
}

#[test]
fn an_open_stream_makes_the_loop_ask_for_the_next_frame() {
    if !isolated("an_open_stream_makes_the_loop_ask_for_the_next_frame") {
        return;
    }
    let dir = Dir::new("busy");
    let mut first = app(sessions(&["a"]), &dir);
    let stream = streaming(&mut first, "a");
    let mut rt = unframed(first);
    rt.frame(DT);
    assert!((wake_in(&rt) - 0.016).abs() < 1e-9, "busy wake in {}", wake_in(&rt));
    rt.frame(wake_in(&rt));
    assert!((wake_in(&rt) - 0.016).abs() < 1e-9, "busy wake in {}", wake_in(&rt));

    rt.state.panes.remove("a");
    drop(stream);
    let at = rt.wake_deadline().unwrap();
    rt.frame(wake_in(&rt));
    assert!(rt.state.clock >= at);
    assert!((wake_in(&rt) - 0.250).abs() < 1e-9, "idle again in {}", wake_in(&rt));
}

fn many(target: &str) -> Vec<Session> {
    let mut list: Vec<Session> = (0..20).map(|i| session(&format!("s{i:02}"), i, json!({}))).collect();
    list.push(session(target, 50, json!({"alias": "far"})));
    list
}

#[test]
fn a_link_waiting_for_the_list_opens_when_a_snapshot_lists_its_session_and_reveals_it() {
    if !isolated("a_link_waiting_for_the_list_opens_when_a_snapshot_lists_its_session_and_reveals_it") {
        return;
    }
    let dir = Dir::new("link");
    let mut first = app(sessions(&["s00"]), &dir);
    let fixtures = first.fixtures.take();
    let mut rt = runtime(first, (1100.0, 420.0));

    rt.update(|s, tx| crate::link::on_open_url(s, &format!("{}://session/@far", crate::link::SCHEME), tx));
    rt.frame(DT);
    assert_eq!(rt.state.selected, None, "the link waits until the sessions are listed");

    rt.state.fixtures = fixtures;
    send(&rt, snapshot(2, many("target"), None));
    rt.frame(DT);
    assert_eq!(rt.state.selected.as_deref(), Some("target"));
    assert!(rt.state.screen == Screen::Session);
    assert_eq!(rt.state.revealed.as_deref(), Some("target"));

    rt.settle_all();
    rt.frame(DT);
    rt.frame(DT);
    assert!(shown_in_sidebar(&rt, "target"), "the opened session is scrolled into the sidebar");
}

#[test]
fn selecting_a_session_scrolls_the_sidebar_to_it() {
    if !isolated("selecting_a_session_scrolls_the_sidebar_to_it") {
        return;
    }
    let dir = Dir::new("reveal");
    let mut rt = runtime(app(many("target"), &dir), (1100.0, 420.0));

    rt.update(|s, _| {
        crate::over::open_session(s, "target");
        s.revealed = Some("target".into());
    });
    rt.settle_all();
    rt.frame(DT);
    rt.frame(DT);
    assert!(!shown_in_sidebar(&rt, "target"), "without the reveal the row sits below the fold");

    rt.state.revealed = None;
    rt.frame(DT);
    assert_eq!(rt.state.revealed.as_deref(), Some("target"));
    rt.settle_all();
    rt.frame(DT);
    rt.frame(DT);
    assert!(shown_in_sidebar(&rt, "target"));
}

#[test]
fn a_snapshot_that_did_not_touch_the_selected_session_does_not_reread_it() {
    if !isolated("a_snapshot_that_did_not_touch_the_selected_session_does_not_reread_it") {
        return;
    }
    let dir = Dir::new("touched");
    std::fs::write(dir.0.join("items/W1.json"), r#"{"id":"W1","text":"Ship it","state":"in_progress"}"#).unwrap();
    let held = json!({"heldWork": [{"itemId": "W1", "projectKey": "p1", "state": "in_progress"}]});
    let list = || vec![session("a", 0, held.clone()), session("b", 1, json!({}))];
    let mut rt = runtime(app(list(), &dir), (1440.0, 900.0));

    rt.update(|s, _| crate::over::open_session(s, "a"));
    rt.frame(DT);
    assert!(rt.state.items.contains_key("W1"), "opening the session reads its work item");

    rt.state.items.clear();
    send(&rt, snapshot(2, list(), Some(&["b"])));
    rt.frame(DT);
    assert!(!rt.state.items.contains_key("W1"), "an untouched selected session isn't read again");

    send(&rt, snapshot(3, list(), Some(&["a"])));
    rt.frame(DT);
    assert!(rt.state.items.contains_key("W1"), "a touched selected session is read again");

    rt.state.items.clear();
    send(&rt, snapshot(4, list(), None));
    rt.frame(DT);
    assert!(rt.state.items.contains_key("W1"), "a snapshot without a touched set rereads it");
}

#[test]
fn a_refused_send_does_not_pass_its_retry_wait_to_the_next_message() {
    if !isolated("a_refused_send_does_not_pass_its_retry_wait_to_the_next_message") {
        return;
    }
    let dir = Dir::new("refused");
    let mut s = app(sessions(&["a"]), &dir);
    s.clock = 100.0;
    let refused = |answered: bool| feed::catalog::RespondError { message: "no".into(), code: String::new(), maybe_delivered: false, reply: serde_json::Value::Null, answered, status: if answered { 409 } else { 0 } };
    let first = crate::fresh(&s, "a", "first".into(), Vec::new());
    for _ in 0..6 {
        crate::take_sent(&mut s, "a".into(), first.clone(), true, Err(refused(false)));
    }
    crate::take_sent(&mut s, "a".into(), first, true, Err(refused(true)));
    let next = crate::fresh(&s, "a", "next".into(), Vec::new());
    crate::take_sent(&mut s, "a".into(), next, false, Err(refused(false)));
    assert_eq!(s.redeliver["a"].due, 101.0, "the next message retries a second later");
}

#[test]
fn earlier_keeps_a_session_dropped_from_the_list_and_blocks_one_removed() {
    if !isolated("earlier_keeps_a_session_dropped_from_the_list_and_blocks_one_removed") {
        return;
    }
    let dir = Dir::new("earlier");
    let mut s = app(sessions(&["kept", "gone", "moved"]), &dir);
    let removal = |id: &str, unlisted| feed::watch::Removal { id: id.into(), unlisted };
    let seq = crate::over::earlier::asking(&mut s, "p1");
    crate::over::earlier::removed(&mut s, &[removal("kept", true), removal("gone", false), removal("moved", false)]);
    assert_eq!(crate::over::earlier::fetched(&s, "p1"), vec!["kept".to_string()], "a session dropped from the list keeps its last row");
    crate::over::earlier::answer(&mut s, "p1", seq, sessions(&["kept", "gone", "moved"]));
    assert_eq!(crate::over::earlier::fetched(&s, "p1"), vec!["kept".to_string()], "a page already asked for doesn't bring back a removed or rekeyed session");
}

#[test]
fn earlier_reads_its_pages_again_when_a_shown_session_leaves_the_list() {
    if !isolated("earlier_reads_its_pages_again_when_a_shown_session_leaves_the_list") {
        return;
    }
    let dir = Dir::new("earlier-reread");
    let busy = session("kept", 0, json!({"status": "busy"}));
    let unlisted = [feed::watch::Removal { id: "kept".into(), unlisted: true }];
    let mut s = app(vec![busy.clone()], &dir);
    let seq = crate::over::earlier::asking(&mut s, "p1");
    crate::over::earlier::removed(&mut s, &unlisted);
    assert!(crate::over::earlier::rereads(&s, "p1"), "leaving the list while the first page loads reads it again");
    crate::over::earlier::answer(&mut s, "p1", seq, vec![busy.clone()]);
    assert!(crate::over::earlier::rereads(&s, "p1"), "the page asked before still reads again");
    crate::over::earlier::answer_reread(&mut s, "p1", None);
    assert!(crate::over::earlier::error(&s, "p1").is_some() && crate::over::earlier::rereads(&s, "p1"), "a failed reread is what Try again does next");
    crate::over::earlier::answer_reread(&mut s, "p1", Some(vec![session("kept", 0, json!({})), session("other", 1, json!({}))]));
    assert!(!crate::over::earlier::rereads(&s, "p1"));
    assert_eq!(crate::over::earlier::status(&s, "p1", "kept").as_deref(), Some("idle"), "the reread replaces the last listed row");
    assert_eq!(crate::over::earlier::fetched(&s, "p1"), vec!["kept".to_string(), "other".to_string()]);

    crate::over::earlier::removed(&mut s, &unlisted);
    assert!(crate::over::earlier::rereads(&s, "p1"), "a loaded Earlier reads again too");
}

#[test]
fn a_moved_ledger_key_drops_the_ready_count_and_its_old_read_and_follows_the_new_key() {
    if !isolated("a_moved_ledger_key_drops_the_ready_count_and_its_old_read_and_follows_the_new_key") {
        return;
    }
    let dir = Dir::new("ready-moved");
    let mut rt = unframed(app(sessions(&["a"]), &dir));
    let ready = |key: &str, count| feed::activity::Ready { ledger_key: key.into(), count };
    let first = crate::ui::ready::asking(&mut rt.state, "p1");
    crate::ui::ready::answer(&mut rt.state, "p1", first, ready("old", 3));
    assert_eq!(crate::ui::ready::count(&rt.state, "p1"), 3);
    let before = crate::ui::ready::asking(&mut rt.state, "p1");

    let mut moved = Fetched { recorded: recorded(2, sessions(&["a"])), ledgers: HashMap::new(), held: Vec::new(), asked: Vec::new(), started: std::time::Instant::now(), usage: None, moved: vec!["p1".into()] };
    moved.recorded.projects[0].ledger_key = "new".into();
    send(&rt, Bg::Overview(Box::new(moved)));
    rt.frame(DT);
    assert_eq!(crate::ui::ready::count(&rt.state, "p1"), 0, "a moved ledger key drops the old count");
    let after = crate::ui::ready::asking(&mut rt.state, "p1");
    crate::ui::ready::answer(&mut rt.state, "p1", before, ready("old", 5));
    assert_eq!(crate::ui::ready::count(&rt.state, "p1"), 0, "a read asked before the move doesn't come back");
    crate::ui::ready::answer(&mut rt.state, "p1", after, ready("new", 7));
    assert_eq!(crate::ui::ready::count(&rt.state, "p1"), 7);
    crate::ui::ready::ledger_changed(&mut rt.state, "old");
    assert_eq!(crate::ui::ready::count(&rt.state, "p1"), 7, "the old key's events no longer touch it");
    crate::ui::ready::ledger_changed(&mut rt.state, "new");
    assert_eq!(crate::ui::ready::count(&rt.state, "p1"), 0, "the new key's events refresh it");
}
