use std::collections::HashMap;
use std::path::PathBuf;

use feed::catalog::Session;
use feed::derive::Recorded;
use feed::derive::live::{Fetched, Overview};
use rune::{AppSpec, Env, Runtime};
use serde_json::json;

use crate::transcript::Pane;
use crate::{App, Bg, Fixtures, Screen};

#[path = "text_scale_tests.rs"]
mod text_scale;
#[path = "keyboard_tests.rs"]
mod keyboard;
#[path = "role_pickers_tests.rs"]
mod role_pickers;
#[path = "project_menus_tests.rs"]
mod project_menus;
#[path = "work_steps_tests.rs"]
mod work_steps;
#[path = "row_memo_tests.rs"]
mod rows_memo;
#[path = "row_memo_lines_tests.rs"]
mod rows_memo_lines;
#[cfg(feature = "frame-profile")]
#[path = "row_cost_tests.rs"]
mod row_cost;

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
    Bg::Overview(Box::new(Fetched { recorded: recorded(seq, sessions), ledgers: HashMap::new(), held: Vec::new(), asked: Vec::new(), actors_at: std::time::Instant::now(), usage: None, moved: Vec::new() }))
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
fn a_reread_that_finds_the_same_sessions_does_not_rebuild_the_overview() {
    if !isolated("a_reread_that_finds_the_same_sessions_does_not_rebuild_the_overview") {
        return;
    }
    let dir = Dir::new("same");
    let mut rt = runtime(app(sessions(&["a"]), &dir), (1440.0, 900.0));
    send(&rt, snapshot(2, sessions(&["a", "b"]), None));
    rt.frame(DT);
    let before = rt.state.rebuilds;

    send(&rt, Bg::Snapshot("2026-11-27T10:02:40Z".into(), EPOCH.into(), 3, sessions(&["a", "b"]), None));
    rt.frame(DT);
    assert_eq!(rt.state.rebuilds, before, "nothing the overview shows moved");

    send(&rt, snapshot(3, sessions(&["a", "b"]), None));
    rt.frame(DT);
    assert_eq!(rt.state.rebuilds, before + 1, "a new minute rebuilds so ages and time windows move on");

    send(&rt, Bg::Snapshot("2026-11-27T10:03:20Z".into(), EPOCH.into(), 4, sessions(&["a", "b", "c"]), None));
    rt.frame(DT);
    assert_eq!(rt.state.rebuilds, before + 2, "a real change still rebuilds in the same frame");
    assert!(rt.state.ov.d.session("c").is_some());
}

#[test]
fn a_session_list_that_brings_a_quiet_project_back_reads_its_work_at_once() {
    if !isolated("a_session_list_that_brings_a_quiet_project_back_reads_its_work_at_once") {
        return;
    }
    let dir = Dir::new("quiet");
    let mut rt = runtime(app(sessions(&["a"]), &dir), (1440.0, 900.0));
    let (poke, asked) = std::sync::mpsc::channel();
    rt.state.overview_poke = Some(poke);
    let quiet = |last: &str| session("q", 0, json!({"lastActive": last, "project": {"id": "p2", "name": "Beta"}}));
    let mut raw = recorded(2, vec![session("a", 0, json!({})), quiet("2026-11-01T10:00:00Z")]);
    raw.projects.push(serde_json::from_value(json!({"id": "p2", "displayName": "Beta"})).unwrap());
    send(&rt, Bg::Overview(Box::new(Fetched { recorded: raw, ledgers: HashMap::new(), held: Vec::new(), asked: Vec::new(), actors_at: std::time::Instant::now(), usage: None, moved: Vec::new() })));
    rt.frame(DT);
    assert!(!asked.try_iter().any(|a| a == crate::Ask::Read("p2".into())), "a project quiet for weeks isn't read");

    send(&rt, snapshot(3, vec![session("a", 0, json!({})), quiet("2026-11-27T10:02:00Z")], Some(&["q"])));
    rt.frame(DT);
    assert!(asked.try_iter().any(|a| a == crate::Ask::Read("p2".into())), "its work is asked for in the same frame, not at the next overview read");
}

fn daemon_log() -> (feed::catalog::Client, std::sync::mpsc::Receiver<(String, std::time::Instant)>) {
    use std::io::{BufRead, Write};
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let base = format!("http://{}", listener.local_addr().unwrap());
    let (heard, log) = std::sync::mpsc::channel();
    std::thread::spawn(move || {
        for stream in listener.incoming().flatten() {
            let heard = heard.clone();
            std::thread::spawn(move || {
                let mut reader = std::io::BufReader::new(stream.try_clone().unwrap());
                let mut line = String::new();
                reader.read_line(&mut line).unwrap_or_default();
                let path = line.split_whitespace().nth(1).unwrap_or_default().to_string();
                loop {
                    let mut header = String::new();
                    if reader.read_line(&mut header).unwrap_or(0) == 0 || header == "\r\n" {
                        break;
                    }
                }
                let _ = heard.send((path, std::time::Instant::now()));
                let mut stream = stream;
                let _ = write!(stream, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{{}}");
            });
        }
    });
    (feed::catalog::Client::new(&base, "t"), log)
}

#[test]
fn a_ledger_asked_for_between_overview_reads_is_read_without_waiting_for_the_next_one() {
    let (client, log) = daemon_log();
    let (outbox, _inbox) = crate::wake::channel();
    let (poke, poked) = std::sync::mpsc::channel();
    let stream = feed::watch::Stream::default();
    stream.open();
    crate::watch_overview(client, outbox, poked, stream);
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(10);
    while !log.recv_timeout(deadline.saturating_duration_since(std::time::Instant::now())).is_ok_and(|(path, _)| path.starts_with("/api/v1/snapshot")) {
        assert!(std::time::Instant::now() < deadline, "the first overview read never came");
    }
    std::thread::sleep(std::time::Duration::from_millis(300));
    log.try_iter().for_each(drop);
    poke.send(crate::Ask::Refresh).unwrap();
    std::thread::sleep(std::time::Duration::from_millis(500));
    let asked = std::time::Instant::now();
    poke.send(crate::Ask::Read("p2".into())).unwrap();
    let ledger = loop {
        let (path, at) = log.recv_timeout(std::time::Duration::from_secs(10)).expect("the ledger was never read");
        if path.starts_with("/api/v1/ledger") {
            break at;
        }
    };
    assert!(ledger.duration_since(asked) < std::time::Duration::from_secs(1), "waited {:?} behind the overview's spacing", ledger.duration_since(asked));
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

fn streaming(app: &mut App, id: &str) -> feed::wake::Sender<feed::source::Message> {
    let (stream, live) = crate::wake::channel();
    let mut pane = Pane::new();
    pane.live = Some(live);
    app.panes.insert(id.into(), pane);
    stream
}

fn rings() -> std::sync::Arc<std::sync::atomic::AtomicUsize> {
    let rings = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let counted = rings.clone();
    crate::wake::MAIN.install(move || {
        counted.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
    });
    rings
}

fn rung(rings: &std::sync::atomic::AtomicUsize) -> usize {
    rings.load(std::sync::atomic::Ordering::SeqCst)
}

fn settle(rt: &mut Runtime<App>) {
    for _ in 0..200 {
        if !crate::wake::MAIN.rung() {
            return;
        }
        rt.frame(DT);
        std::thread::sleep(std::time::Duration::from_millis(5));
    }
    panic!("the loop keeps waking itself");
}

fn answering(body: &'static str) -> feed::catalog::Client {
    use std::io::{BufRead, Write};
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let base = format!("http://{}", listener.local_addr().unwrap());
    std::thread::spawn(move || {
        for stream in listener.incoming().flatten() {
            std::thread::spawn(move || {
                let mut reader = std::io::BufReader::new(stream.try_clone().unwrap());
                loop {
                    let mut line = String::new();
                    if reader.read_line(&mut line).unwrap_or(0) == 0 || line == "\r\n" {
                        break;
                    }
                }
                let mut stream = stream;
                let _ = write!(stream, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len());
            });
        }
    });
    feed::catalog::Client::new(&base, "t")
}

#[test]
fn an_idle_loop_arms_no_timer_and_a_poll_with_nothing_to_drain_changes_nothing() {
    if !isolated("an_idle_loop_arms_no_timer_and_a_poll_with_nothing_to_drain_changes_nothing") {
        return;
    }
    let dir = Dir::new("idle");
    let mut rt = runtime(app(sessions(&["a", "b"]), &dir), (1440.0, 900.0));
    rt.frame(DT);
    assert_eq!(rt.wake_deadline(), None, "nothing pending arms no timer");
    let clock = rt.state.clock + DT;
    let mut drew = true;
    rt.update_if(|s, tx| {
        drew = crate::poll(s, clock, tx);
        drew
    });
    assert!(!drew, "a poll that drains nothing changes nothing, so nothing draws");
    assert_eq!(rt.wake_deadline(), None);
}

#[test]
fn a_session_list_posted_with_no_timer_armed_wakes_the_loop_and_shows_in_one_frame() {
    if !isolated("a_session_list_posted_with_no_timer_armed_wakes_the_loop_and_shows_in_one_frame") {
        return;
    }
    let rings = rings();
    let dir = Dir::new("posted");
    let mut rt = runtime(app(sessions(&["a"]), &dir), (1440.0, 900.0));
    settle(&mut rt);
    assert_eq!(rt.wake_deadline(), None);
    let before = rung(&rings);
    let out = rt.state.outbox.clone();
    std::thread::spawn(move || out.send(snapshot(2, sessions(&["a", "b"]), None)).unwrap()).join().unwrap();
    assert_eq!(rung(&rings), before + 1, "the post wakes the loop");
    rt.frame(DT);
    assert!(rt.state.ov.d.session("b").is_some() && !rt.state.ov.live.stale(), "the next frame takes it and rebuilds what shows");
}

#[test]
fn a_post_that_lands_while_the_loop_drains_wakes_it_again() {
    if !isolated("a_post_that_lands_while_the_loop_drains_wakes_it_again") {
        return;
    }
    let rings = rings();
    let dir = Dir::new("mid");
    let mut rt = unframed(app(sessions(&["a"]), &dir));
    settle(&mut rt);
    let base = rung(&rings);
    let out = rt.state.outbox.clone();
    out.send(snapshot(2, sessions(&["a", "b"]), None)).unwrap();
    out.send(snapshot(3, sessions(&["a", "b"]), None)).unwrap();
    let first = rung(&rings);
    assert_eq!(first, base + 1, "a burst wakes once");
    let clock = rt.state.clock + DT;
    rt.update_if(move |s, tx| {
        let changed = crate::poll(s, clock, tx);
        out.send(snapshot(4, sessions(&["a", "b", "c"]), None)).unwrap();
        changed
    });
    assert_eq!(rung(&rings), first + 1, "a post after the drain began wakes the loop again");
    rt.frame(DT);
    assert!(rt.state.ov.d.session("c").is_some());
}

#[test]
fn a_stream_batch_wakes_the_loop_and_an_open_stream_does_not_poll_every_frame() {
    if !isolated("a_stream_batch_wakes_the_loop_and_an_open_stream_does_not_poll_every_frame") {
        return;
    }
    let rings = rings();
    let dir = Dir::new("stream");
    let mut first = app(sessions(&["a"]), &dir);
    let stream = streaming(&mut first, "a");
    let mut rt = unframed(first);
    settle(&mut rt);
    assert!(rt.wake_deadline().is_none_or(|at| at - rt.state.clock > 0.1), "an open stream doesn't ask for every frame");
    let before = rung(&rings);
    std::thread::spawn(move || stream.send(feed::source::Message::Status("live".into())).unwrap()).join().unwrap();
    assert_eq!(rung(&rings), before + 1, "the stream wakes the loop");
    let clock = rt.state.clock + DT;
    let mut drew = false;
    rt.update_if(|s, tx| {
        drew = crate::poll(s, clock, tx);
        drew
    });
    assert!(drew, "a new stream status redraws");
    assert_eq!(rt.state.panes["a"].status, "live");
}

#[test]
fn an_unconfirmed_change_that_expires_shows_the_daemon_value_in_the_same_poll() {
    if !isolated("an_unconfirmed_change_that_expires_shows_the_daemon_value_in_the_same_poll") {
        return;
    }
    let dir = Dir::new("expire");
    let mut rt = unframed(app(sessions(&["a"]), &dir));
    rt.frame(DT);
    let hold = feed::derive::live::EXPECT_HOLD - std::time::Duration::from_millis(40);
    rt.update(|s, _| {
        assert!(s.ov.live.patch("a", feed::menu::Patch::Alias(Some("renamed".into())), std::time::Instant::now() - hold));
    });
    rt.frame(DT);
    assert_eq!(rt.state.ov.d.session("a").and_then(|x| x.alias.clone()), Some("renamed".into()), "the change shows while it waits");
    std::thread::sleep(std::time::Duration::from_millis(80));
    rt.frame(DT);
    assert!(!rt.state.ov.live.stale());
    assert_eq!(rt.state.ov.d.session("a").and_then(|x| x.alias.clone()), None, "the expiry poll shows the daemon value");
}

#[test]
fn a_second_unconfirmed_change_keeps_its_own_expiry_after_the_first_lapses() {
    if !isolated("a_second_unconfirmed_change_keeps_its_own_expiry_after_the_first_lapses") {
        return;
    }
    let dir = Dir::new("expire-two");
    let mut rt = unframed(app(sessions(&["a", "b"]), &dir));
    rt.frame(DT);
    let hold = feed::derive::live::EXPECT_HOLD;
    let now = std::time::Instant::now();
    rt.update(|s, _| {
        assert!(s.ov.live.patch("a", feed::menu::Patch::Alias(Some("one".into())), now - hold + std::time::Duration::from_millis(40)));
        assert!(s.ov.live.patch("b", feed::menu::Patch::Alias(Some("two".into())), now - hold + std::time::Duration::from_millis(400)));
    });
    rt.frame(DT);
    std::thread::sleep(std::time::Duration::from_millis(80));
    let clock = rt.state.clock + 0.08;
    rt.update_if(|s, tx| crate::poll(s, clock, tx));
    assert_eq!(rt.state.ov.d.session("a").and_then(|x| x.alias.clone()), None);
    let next = crate::wake::next(crate::wake::Window::Main).expect("a deadline is armed");
    let left = next.saturating_duration_since(std::time::Instant::now()).as_secs_f64();
    assert!(left > 0.2 && left < 0.4, "the second change's expiry is armed, in {left}");
}

#[test]
fn a_stream_that_goes_quiet_catches_up_at_its_deadline_and_redraws() {
    if !isolated("a_stream_that_goes_quiet_catches_up_at_its_deadline_and_redraws") {
        return;
    }
    let dir = Dir::new("quiet-stream");
    let mut first = app(sessions(&["a"]), &dir);
    let stream = streaming(&mut first, "a");
    let mut rt = unframed(first);
    rt.frame(DT);
    std::thread::sleep(std::time::Duration::from_millis(200));
    settle(&mut rt);
    stream.send(feed::source::Message::Status("live".into())).unwrap();
    rt.frame(DT);
    let wake = wake_in(&rt);
    assert!((wake - crate::REPLAY_QUIET_SECS).abs() < 0.1, "wakes when the stream goes quiet, in {wake}");
    let poll_at = |rt: &mut Runtime<App>, clock: f64| {
        let mut drew = false;
        rt.update_if(|s, tx| {
            drew = crate::poll(s, clock, tx);
            drew
        });
        drew
    };
    let before = rt.state.clock + wake / 2.0;
    assert!(!poll_at(&mut rt, before), "nothing changes before the stream goes quiet");
    assert!(poll_at(&mut rt, before + wake), "catching up changes what shows");
    assert!(rt.state.panes["a"].caught_up);
}

#[test]
fn a_layout_that_cannot_be_written_retries_on_a_wait_not_in_a_loop() {
    if !isolated("a_layout_that_cannot_be_written_retries_on_a_wait_not_in_a_loop") {
        return;
    }
    let home = std::path::PathBuf::from(std::env::var_os("HOME").unwrap()).join("Library/Application Support");
    std::fs::create_dir_all(&home).unwrap();
    std::fs::write(home.join("Agenthail"), "not a folder").unwrap();
    let rings = rings();
    let dir = Dir::new("layout");
    let mut first = app(sessions(&["a"]), &dir);
    first.ov.persist = true;
    let mut rt = unframed(first);
    rt.frame(DT);
    let mut last = std::time::Instant::now();
    let mut run = |rt: &mut Runtime<App>, for_ms: u64| {
        let mut polls = 0;
        let started = std::time::Instant::now();
        while started.elapsed() < std::time::Duration::from_millis(for_ms) {
            let since = last.elapsed().as_secs_f64();
            let due = rt.wake_deadline().is_some_and(|at| at <= rt.state.clock + since);
            if crate::wake::MAIN.rung() || due {
                let reach = rt.wake_deadline().filter(|_| due).map_or(0.0, |at| at - rt.state.clock + 0.001);
                rt.frame(since.max(reach).max(DT));
                last = std::time::Instant::now();
                polls += 1;
            }
            std::thread::sleep(std::time::Duration::from_millis(2));
        }
        polls
    };
    let mut polls = run(&mut rt, 800);
    assert!(crate::over::layout::failure().is_some(), "the failure shows");
    assert!(polls <= 3, "a failing write waits before trying again: {polls} polls, {} rings", rung(&rings));
    polls += run(&mut rt, 1900);
    assert!(crate::over::layout::failure().is_some(), "the first retry fails the same way");
    std::fs::remove_file(home.join("Agenthail")).unwrap();
    polls += run(&mut rt, 2600);
    assert!(crate::over::layout::failure().is_none(), "the retry after the wait saves and clears the warning");
    assert!(home.join("Agenthail/layout.json").exists());
    assert!(polls <= 12, "retries stay paced: {polls} polls");
}

#[test]
fn a_copied_path_note_arms_its_own_expiry() {
    if !isolated("a_copied_path_note_arms_its_own_expiry") {
        return;
    }
    let dir = Dir::new("copied");
    let mut rt = unframed(app(sessions(&["a"]), &dir));
    rt.frame(DT);
    rt.update(|s, _| s.attach.sent.entry("k".into()).or_default().copied = Some(std::time::Instant::now()));
    let clock = rt.state.clock + DT;
    rt.update_if(|s, tx| crate::poll(s, clock, tx));
    let next = crate::wake::next(crate::wake::Window::Main).expect("the note's expiry is woken for");
    let left = next.saturating_duration_since(std::time::Instant::now()).as_secs_f64();
    assert!(left > 1.8 && left <= 2.0, "wakes when the note lapses, in {left}");
}

#[test]
fn a_module_result_wakes_the_loop_and_shows_in_one_frame() {
    if !isolated("a_module_result_wakes_the_loop_and_shows_in_one_frame") {
        return;
    }
    let rings = rings();
    let dir = Dir::new("module");
    let mut a = app(sessions(&["a"]), &dir);
    a.client = Some(answering(r#"{"role":{"label":"worker","source":"assigned"}}"#));
    let mut rt = unframed(a);
    rt.frame(DT);
    settle(&mut rt);
    let mut seen = rung(&rings);
    rt.update(|s, _| crate::ui::role::refresh(s, "a"));
    for _ in 0..500 {
        if crate::ui::role::shown(&rt.state, "a").is_some() {
            break;
        }
        std::thread::sleep(std::time::Duration::from_millis(10));
        if rung(&rings) != seen {
            seen = rung(&rings);
            rt.frame(DT);
        }
    }
    assert_eq!(crate::ui::role::shown(&rt.state, "a"), Some(("worker", "assigned")), "frames ran only when a result rang");
}

#[test]
fn a_post_while_only_a_session_window_runs_rings_main_which_drains_it() {
    if !isolated("a_post_while_only_a_session_window_runs_rings_main_which_drains_it") {
        return;
    }
    let rings = rings();
    let dir = Dir::new("window");
    let mut main = unframed(app(sessions(&["a"]), &dir));
    main.frame(DT);
    let mut id = 0;
    main.update(|s, _| id = crate::ui::window::open(s, "a"));
    settle(&mut main);
    let polled = rung(&rings);
    let spec = AppSpec::<App> { poll: Some(crate::ui::window::poll), ..AppSpec::new("Session", (900.0, 820.0), crate::placeholder, crate::ui::session_window) };
    let mut window = rune::App::new(spec).headless_runtime((900.0, 820.0), Env { system_dark: true, scale: 1.0 });
    std::mem::swap(&mut main.state, &mut window.state);
    crate::ui::window::on_enter(&mut window.state, &format!("session-{id}"));
    window.frame(DT);
    let out = window.state.outbox.clone();
    std::thread::spawn(move || out.send(snapshot(2, sessions(&["a", "b"]), None)).unwrap()).join().unwrap();
    assert!(rung(&rings) > polled, "main's wake is asked for since main last polled, whichever window is in front");
    for _ in 0..3 {
        window.frame(DT);
    }
    assert!(window.state.ov.d.session("b").is_none(), "the app-wide drain runs only in main's poll");
    assert!(crate::wake::MAIN.rung(), "main's wake stays owed until main polls");
    crate::ui::window::on_leave(&mut window.state, &format!("session-{id}"));
    std::mem::swap(&mut main.state, &mut window.state);
    main.frame(DT);
    assert!(main.state.ov.d.session("b").is_some(), "main's poll takes it");
    assert!(!crate::wake::MAIN.rung());
}

#[test]
fn a_closed_tab_note_arms_its_own_expiry_and_no_other_timer() {
    if !isolated("a_closed_tab_note_arms_its_own_expiry_and_no_other_timer") {
        return;
    }
    let dir = Dir::new("expiry");
    let mut rt = unframed(app(sessions(&["a"]), &dir));
    rt.frame(DT);
    rt.update(|s, _| s.tabs.closed = Some(("a".into(), "a".into(), s.clock + 5.0)));
    rt.frame(DT);
    let wake = wake_in(&rt);
    assert!((wake - 5.0).abs() < 0.1, "wakes when the note expires, in {wake}");
    rt.frame(wake + DT);
    assert!(rt.state.tabs.closed.is_none());
    assert_eq!(rt.wake_deadline(), None);
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
    assert!(rt.state.items.contains_key(&zk("p1", "W1")), "opening the session reads its work item");

    rt.state.items.clear();
    send(&rt, snapshot(2, list(), Some(&["b"])));
    rt.frame(DT);
    assert!(!rt.state.items.contains_key(&zk("p1", "W1")), "an untouched selected session isn't read again");

    send(&rt, snapshot(3, list(), Some(&["a"])));
    rt.frame(DT);
    assert!(rt.state.items.contains_key(&zk("p1", "W1")), "a touched selected session is read again");

    rt.state.items.clear();
    send(&rt, snapshot(4, list(), None));
    rt.frame(DT);
    assert!(rt.state.items.contains_key(&zk("p1", "W1")), "a snapshot without a touched set rereads it");
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
    let first = crate::over::earlier::start_reread(&mut s, "p1");
    crate::over::earlier::answer_reread(&mut s, "p1", first, None);
    assert!(crate::over::earlier::error(&s, "p1").is_some() && crate::over::earlier::rereads(&s, "p1"), "a failed reread is what Try again does next");
    let second = crate::over::earlier::start_reread(&mut s, "p1");
    crate::over::earlier::answer_reread(&mut s, "p1", second, Some(vec![session("kept", 0, json!({})), session("other", 1, json!({}))]));
    assert!(!crate::over::earlier::rereads(&s, "p1"));
    assert_eq!(crate::over::earlier::status(&s, "p1", "kept").as_deref(), Some("idle"), "the reread replaces the last listed row");
    assert_eq!(crate::over::earlier::fetched(&s, "p1"), vec!["kept".to_string(), "other".to_string()]);

    crate::over::earlier::removed(&mut s, &unlisted);
    assert!(crate::over::earlier::rereads(&s, "p1"), "a loaded Earlier reads again too");
    let third = crate::over::earlier::start_reread(&mut s, "p1");
    crate::over::earlier::removed(&mut s, &unlisted);
    crate::over::earlier::answer_reread(&mut s, "p1", third, Some(vec![session("kept", 0, json!({}))]));
    assert!(crate::over::earlier::asks_again(&s, "p1"), "leaving the list during a reread reads once more after it");

    let seq = crate::over::earlier::asking(&mut s, "p2");
    let other = [feed::watch::Removal { id: "elsewhere".into(), unlisted: true }];
    crate::over::earlier::removed(&mut s, &other);
    crate::over::earlier::fail(&mut s, "p2", seq);
    let retry = crate::over::earlier::start_reread(&mut s, "p2");
    crate::over::earlier::answer_reread(&mut s, "p2", retry, Some(Vec::new()));
    crate::over::earlier::removed(&mut s, &other);
    assert!(crate::over::earlier::asks_again(&s, "p2"), "a project first read by a reread still reads again");
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

    let mut moved = Fetched { recorded: recorded(2, sessions(&["a"])), ledgers: HashMap::new(), held: Vec::new(), asked: Vec::new(), actors_at: std::time::Instant::now(), usage: None, moved: vec!["p1".into()] };
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

fn ledger_api(paths: std::sync::mpsc::Sender<String>) -> String {
    use std::io::{BufRead, BufReader, Write};
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let base = format!("http://{}", listener.local_addr().unwrap());
    let w8 = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let w5 = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
    std::thread::spawn(move || {
        for stream in listener.incoming() {
            let (paths, w8, w5) = (paths.clone(), w8.clone(), w5.clone());
            std::thread::spawn(move || {
                let mut stream = stream.unwrap();
                let mut reader = BufReader::new(stream.try_clone().unwrap());
                let mut line = String::new();
                reader.read_line(&mut line).unwrap();
                let path = line.split_whitespace().nth(1).unwrap_or_default().to_string();
                let mut length = 0usize;
                loop {
                    let mut header = String::new();
                    if reader.read_line(&mut header).unwrap() == 0 || header.trim_end().is_empty() {
                        break;
                    }
                    if let Some((name, value)) = header.trim_end().split_once(':')
                        && name.eq_ignore_ascii_case("content-length")
                    {
                        length = value.trim().parse().unwrap_or(0);
                    }
                }
                let mut sent = vec![0; length];
                std::io::Read::read_exact(&mut reader, &mut sent).unwrap();
                let sent = String::from_utf8_lossy(&sent).to_string();
                paths.send(if sent.is_empty() { path.clone() } else { format!("{path} {sent}") }).unwrap();
                let item = |id: &str, title: &str| json!({"projectKey": "shared:z", "projectName": "Zed", "id": id, "title": title, "state": "parked", "updatedAt": "2026-09-01T00:00:00Z", "matched": "description", "snippet": "…a parked\nexport…"});
                let (status, body) = if path.starts_with("/api/v1/ledger/search?q=exp&") {
                    std::thread::sleep(std::time::Duration::from_millis(300));
                    (200, json!({"items": [item("z-W1", "Old export")], "truncated": false}))
                } else if path.starts_with("/api/v1/ledger/search?q=export&") {
                    (200, json!({"items": [item("z-W9", "Parked export")], "truncated": false}))
                } else if path.starts_with("/api/v1/ledger/search?") || path.ends_with("&id=z-W6") || (path == "/api/v1/ledger?projectKey=shared%3Az&view=item&id=z-W8" && w8.fetch_add(1, std::sync::atomic::Ordering::SeqCst) > 0) {
                    (500, json!({"error": {"code": "internal", "message": "the ledger store is busy"}}))
                } else if path == "/api/v1/ledger?projectKey=shared%3Aq&view=item&id=z-W9" {
                    (200, json!({"item": {"item": {"id": "z-W9", "text": "Other export", "state": "open", "type": "task", "priority": "P3"}}}))
                } else if path.starts_with("/api/v1/ledger?projectKey=shared%3Az&view=item&id=") {
                    let id = path.rsplit('=').next().unwrap_or_default();
                    let state = match id {
                        "z-W5" if w5.fetch_add(1, std::sync::atomic::Ordering::SeqCst) > 0 => "open",
                        "z-W5" => "done",
                        _ => "parked",
                    };
                    if id == "z-W7" {
                        std::thread::sleep(std::time::Duration::from_millis(300));
                    }
                    (200, json!({"item": {"item": {"id": id, "text": "Parked export", "state": state, "type": "task", "priority": "P3"}}}))
                } else if path == "/api/v1/ledger/work" {
                    (200, json!({"ok": true}))
                } else {
                    (404, json!({"error": {"code": "unknown_route", "message": "no"}}))
                };
                let body = body.to_string();
                let _ = write!(stream, "HTTP/1.1 {status} X\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len());
            });
        }
    });
    base
}

fn zk(ledger: &str, id: &str) -> feed::derive::WorkKey {
    feed::derive::WorkKey::new(ledger, id)
}

fn until(rt: &mut Runtime<App>, done: impl Fn(&App) -> bool) {
    for _ in 0..400 {
        if done(&rt.state) {
            return;
        }
        std::thread::sleep(std::time::Duration::from_millis(5));
        rt.frame(DT);
    }
    panic!("timed out");
}

fn highlighted_session(rt: &Runtime<App>) -> Option<String> {
    match crate::over::jump::highlighted_pick(&rt.state)? {
        crate::over::jump::Pick::Target(crate::over::Target::Session(id)) => Some(id),
        _ => None,
    }
}

fn work_found(s: &App) -> Option<Result<Vec<String>, String>> {
    match crate::over::worksearch::found_for(s)? {
        Some(Ok(found)) => Some(Ok(found.items.iter().map(|f| f.id.clone()).collect())),
        Some(Err(e)) => Some(Err(e.clone())),
        None => None,
    }
}

#[test]
fn jump_searches_every_ledger_after_typing_settles_and_opens_an_unloaded_item() {
    if !isolated("jump_searches_every_ledger_after_typing_settles_and_opens_an_unloaded_item") {
        return;
    }
    let dir = Dir::new("worksearch");
    let (tx, paths) = std::sync::mpsc::channel();
    let mut a = app(sessions(&["a", "exporter"]), &dir);
    a.client = Some(feed::catalog::Client::new(&ledger_api(tx), "t"));
    let mut rt = runtime(a, (1440.0, 900.0));
    let typed = |rt: &mut Runtime<App>, q: &str| {
        rt.state.ov.jump = Some(rune::TextEdit::new(q));
        rt.frame(DT);
    };
    let asked = |paths: &std::sync::mpsc::Receiver<String>| -> Vec<String> { paths.try_iter().filter(|p| p.starts_with("/api/v1/ledger")).collect() };
    let quiet = |rt: &mut Runtime<App>| {
        for _ in 0..4 {
            std::thread::sleep(std::time::Duration::from_millis(30));
            rt.frame(DT);
        }
    };

    typed(&mut rt, "e");
    assert!(work_found(&rt.state).is_none(), "one character doesn't search");
    typed(&mut rt, "exp");
    assert!((wake_in(&rt) - 0.2).abs() < 0.02, "the loop wakes when typing settles, in {}", wake_in(&rt));
    rt.frame(0.1);
    assert!(asked(&paths).is_empty(), "no search before typing settles");
    rt.frame(wake_in(&rt));
    typed(&mut rt, "export");
    assert_eq!(highlighted_session(&rt), Some("exporter".into()));
    rt.frame(0.21);
    until(&mut rt, |s| work_found(s).is_some());
    assert_eq!(highlighted_session(&rt), Some("exporter".into()), "arriving matches don't move the highlight");
    std::thread::sleep(std::time::Duration::from_millis(400));
    quiet(&mut rt);
    assert_eq!(work_found(&rt.state), Some(Ok(vec!["z-W9".to_string()])), "the older, slower search's reply is dropped");
    let mut both = asked(&paths);
    both.sort();
    assert_eq!(both, ["/api/v1/ledger/search?q=exp&limit=20", "/api/v1/ledger/search?q=export&limit=20"], "the newer query doesn't wait for the older one");
    let hits = crate::over::jump::hit_keys(&rt.state);
    assert!(hits.len() == 2 && hits.iter().all(|h| rt.presented(h).is_some()), "the work match and the session are listed: {hits:?}");

    typed(&mut rt, "broken");
    rt.frame(0.21);
    until(&mut rt, |s| work_found(s).is_some());
    assert_eq!(work_found(&rt.state), Some(Err("the ledger store is busy".to_string())));
    for _ in 0..20 {
        rt.frame(0.25);
    }
    assert_eq!(asked(&paths).len(), 1, "a failed search waits for the next keystroke");
    typed(&mut rt, "broken2");
    rt.frame(0.21);
    until(&mut rt, |s| work_found(s).is_some());
    assert_eq!(asked(&paths).len(), 1, "the next keystroke retries");

    let item = "/api/v1/ledger?projectKey=shared%3Az&view=item&id=z-W9";
    crate::over::worksearch::open(&mut rt.state, &zk("shared:z", "z-W9"));
    assert!(rt.state.screen == Screen::Work(zk("shared:z", "z-W9")));
    assert!(rt.state.ov.jump.is_none());
    assert_eq!(crate::over::worksearch::note(&rt.state, &zk("shared:z", "z-W9")).as_deref(), Some("Loading this work item…"));
    until(&mut rt, |s| s.ov.d.work.contains_key(&zk("shared:z", "z-W9")));
    assert_eq!(asked(&paths), [item]);
    send(&rt, overview(2, sessions(&["a"])));
    rt.frame(DT);
    let w = &rt.state.ov.d.work[&zk("shared:z", "z-W9")];
    assert_eq!((w.state.as_str(), w.ledger_key(), w.project.as_str()), ("parked", "shared:z", "shared:z"), "a rebuild keeps the opened item");
    assert_eq!(rt.state.ov.d.project_name("shared:z"), "Zed", "and the project name the search gave it");
    assert_eq!(crate::over::worksearch::note(&rt.state, &zk("shared:z", "z-W9")), None);

    crate::over::open_target(&mut rt.state, &crate::over::Target::Work(zk("shared:z", "z-W9")));
    quiet(&mut rt);
    assert_eq!(asked(&paths), [item], "opening a found item again rereads it");

    crate::over::worksearch::open(&mut rt.state, &zk("shared:z", "z-W9"));
    let mut loaded = Fetched { recorded: recorded(3, sessions(&["a"])), ledgers: HashMap::new(), held: Vec::new(), asked: Vec::new(), actors_at: std::time::Instant::now(), usage: None, moved: Vec::new() };
    let row: feed::derive::world::Work = serde_json::from_value(json!({"id": "z-W9", "text": "Parked export", "state": "in_progress"})).unwrap();
    loaded.recorded.projects[0].ledger_key = "shared:z".into();
    loaded.ledgers.insert("p1".into(), (std::time::Instant::now(), Ok(vec![row])));
    send(&rt, Bg::Overview(Box::new(loaded)));
    rt.frame(DT);
    quiet(&mut rt);
    assert_eq!(asked(&paths), [item]);
    let w = &rt.state.ov.d.work[&zk("shared:z", "z-W9")];
    assert_eq!((w.state.as_str(), w.project.as_str()), ("in_progress", "p1"), "a read that lands after the ledger loads leaves the loaded item");
    crate::over::worksearch::open(&mut rt.state, &zk("shared:z", "z-W9"));
    quiet(&mut rt);
    assert!(asked(&paths).is_empty(), "a loaded item opens without a read");

    typed(&mut rt, "export");
    rt.frame(0.21);
    until(&mut rt, |s| work_found(s).is_some());
    assert_eq!(crate::over::jump::shown(&rt.state), [("Parked export".to_string(), "In progress · Someone's move · Alpha · …a parked export…".to_string())], "a loaded match takes the search's snippet instead of listing twice");
    assert_eq!(asked(&paths), ["/api/v1/ledger/search?q=export&limit=20"]);

    let (mine, other) = (zk("shared:z", "z-W9"), zk("shared:q", "z-W9"));
    crate::over::worksearch::open(&mut rt.state, &other);
    assert!(rt.state.screen == Screen::Work(other.clone()) && rt.state.ov.notice.is_none());
    until(&mut rt, |s| s.ov.d.work.contains_key(&other));
    assert_eq!(asked(&paths), ["/api/v1/ledger?projectKey=shared%3Aq&view=item&id=z-W9"], "the same id in another ledger is read from that ledger");
    let w = &rt.state.ov.d.work[&mine];
    assert_eq!((w.text.as_str(), w.state.as_str(), w.project.as_str()), ("Parked export", "in_progress", "p1"), "the loaded item keeps its place");
    let w = &rt.state.ov.d.work[&other];
    assert_eq!((w.text.as_str(), w.state.as_str(), w.ledger_key()), ("Other export", "open", "shared:q"), "and the other ledger's item is its own");
    crate::over::work::start(&mut rt.state, &other, feed::ledger::Move::Claim);
    let mut claimed = Vec::new();
    for _ in 0..400 {
        claimed.extend(asked(&paths));
        if claimed.len() >= 2 {
            break;
        }
        std::thread::sleep(std::time::Duration::from_millis(5));
        rt.frame(DT);
    }
    let posted: serde_json::Value = serde_json::from_str(claimed[0].strip_prefix("/api/v1/ledger/work ").expect("the claim is posted first")).unwrap();
    assert_eq!((posted["projectKey"].as_str(), posted["itemId"].as_str(), posted["action"].as_str()), (Some("shared:q"), Some("z-W9"), Some("claim")), "a move on one of them goes to its own ledger");
    assert_eq!(claimed[1], "/api/v1/ledger?projectKey=shared%3Aq&view=item&id=z-W9", "and only that one is read again");
    assert_eq!(rt.state.ov.d.work[&mine].state, "in_progress", "the other is untouched");
    crate::over::go(&mut rt.state, Screen::Work(mine.clone()));
    quiet(&mut rt);
    assert!(asked(&paths).is_empty(), "the loaded one still opens without a read");
    crate::over::work::start(&mut rt.state, &mine, feed::ledger::Move::Comment);
    rt.state.ov.work.words.as_mut().expect("a comment opens its dialog").text = rune::TextEdit::new("Check the other one too");
    let held = rt.state.ov.d.work.remove(&mine).expect("the loaded one");
    crate::over::work::send_words(&mut rt.state);
    quiet(&mut rt);
    assert!(rt.state.ov.work.words.as_ref().is_some_and(|n| n.text.text == "Check the other one too"), "a comment on an item that left keeps its words");
    assert!(asked(&paths).is_empty(), "and isn't sent");
    rt.state.ov.d.work.insert(mine.clone(), held);
    crate::over::work::send_words(&mut rt.state);
    let mut commented = Vec::new();
    for _ in 0..400 {
        commented.extend(asked(&paths));
        if !commented.is_empty() {
            break;
        }
        std::thread::sleep(std::time::Duration::from_millis(5));
        rt.frame(DT);
    }
    quiet(&mut rt);
    commented.extend(asked(&paths));
    assert_eq!(commented.len(), 1, "{commented:?}");
    let posted: serde_json::Value = serde_json::from_str(commented[0].strip_prefix("/api/v1/ledger/work ").expect("the comment is posted")).unwrap();
    assert_eq!((posted["projectKey"].as_str(), posted["itemId"].as_str(), posted["action"].as_str(), posted["text"].as_str()), (Some("shared:z"), Some("z-W9"), Some("comment"), Some("Check the other one too")), "the other one's move goes to its own ledger");
    assert!(rt.state.ov.work.words.is_none());
    crate::over::work::start(&mut rt.state, &other, feed::ledger::Move::Park);
    rt.state.ov.work.words.as_mut().expect("a park opens its dialog").text = rune::TextEdit::new("Wait for the export fix");
    rt.state.ov.d.work.get_mut(&other).expect("the found one").state = "parked".into();
    crate::over::work::send_words(&mut rt.state);
    quiet(&mut rt);
    assert!(rt.state.ov.work.words.as_ref().is_some_and(|n| n.text.text == "Wait for the export fix"), "a park reason stays when the item was parked meanwhile");
    assert!(asked(&paths).is_empty(), "and isn't sent");
    rt.state.ov.work.words = None;

    crate::over::worksearch::open(&mut rt.state, &zk("shared:z", "z-W8"));
    until(&mut rt, |s| s.ov.d.work.contains_key(&zk("shared:z", "z-W8")));
    crate::over::open_target(&mut rt.state, &crate::over::Target::Work(zk("shared:z", "z-W8")));
    quiet(&mut rt);
    assert_eq!(rt.state.ov.d.work[&zk("shared:z", "z-W8")].state, "parked", "a failed refresh keeps what it last had");
    assert_eq!(crate::over::worksearch::note(&rt.state, &zk("shared:z", "z-W8")).as_deref(), Some("Couldn't refresh this work item, so it may be out of date: the ledger store is busy"));

    assert_eq!(asked(&paths).len(), 2, "z-W8 was read on open and again on reopen");
    send(&rt, Bg::LedgerChanged("shared:q".into(), std::time::Instant::now()));
    rt.frame(DT);
    quiet(&mut rt);
    assert!(asked(&paths).is_empty(), "another ledger's change leaves the open item alone");
    send(&rt, Bg::LedgerChanged("shared:z".into(), std::time::Instant::now()));
    rt.frame(DT);
    quiet(&mut rt);
    assert_eq!(asked(&paths), ["/api/v1/ledger?projectKey=shared%3Az&view=item&id=z-W8"], "a change to its ledger rereads the open found item");
    send(&rt, Bg::LedgersReopened);
    rt.frame(DT);
    quiet(&mut rt);
    assert_eq!(asked(&paths).len(), 1, "so does a reconnect that may have missed changes");
    crate::over::go(&mut rt.state, Screen::Now);
    send(&rt, Bg::LedgerChanged("shared:z".into(), std::time::Instant::now()));
    rt.frame(DT);
    quiet(&mut rt);
    assert!(asked(&paths).is_empty(), "a found item that isn't on screen isn't reread on every change");
    crate::over::go(&mut rt.state, Screen::Work(zk("shared:z", "z-W8")));
    quiet(&mut rt);
    assert_eq!(asked(&paths).len(), 1, "coming back to a found item rereads it");

    crate::over::worksearch::open(&mut rt.state, &zk("shared:z", "z-W7"));
    rt.frame(DT);
    send(&rt, Bg::LedgerChanged("shared:z".into(), std::time::Instant::now()));
    rt.frame(DT);
    until(&mut rt, |s| s.ov.d.work.contains_key(&zk("shared:z", "z-W7")));
    std::thread::sleep(std::time::Duration::from_millis(400));
    quiet(&mut rt);
    assert_eq!(asked(&paths).len(), 2, "a change during the first read reads it again");

    crate::over::worksearch::open(&mut rt.state, &zk("shared:z", "z-W6"));
    until(&mut rt, |s| crate::over::worksearch::note(s, &zk("shared:z", "z-W6")).is_some_and(|n| n.starts_with("Couldn't open")));
    crate::over::go(&mut rt.state, Screen::Now);
    crate::over::go(&mut rt.state, Screen::Work(zk("shared:z", "z-W6")));
    quiet(&mut rt);
    send(&rt, Bg::LedgerChanged("shared:z".into(), std::time::Instant::now()));
    rt.frame(DT);
    quiet(&mut rt);
    assert_eq!(asked(&paths), ["/api/v1/ledger?projectKey=shared%3Az&view=item&id=z-W6"; 3], "a failed first read still rereads on return and on its ledger's change");
    crate::over::worksearch::open(&mut rt.state, &zk("shared:z", "z-W6"));
    quiet(&mut rt);
    assert_eq!(asked(&paths).len(), 1, "reopening it from search reads it once");

    crate::over::worksearch::open(&mut rt.state, &zk("shared:z", "z-W5"));
    until(&mut rt, |s| s.ov.d.work.contains_key(&zk("shared:z", "z-W5")));
    assert_eq!(asked(&paths).len(), 1);
    crate::over::work::start(&mut rt.state, &zk("shared:z", "z-W5"), feed::ledger::Move::Reopen);
    let mut moved = Vec::new();
    for _ in 0..400 {
        moved.extend(asked(&paths));
        if moved.len() >= 2 && rt.state.ov.d.work[&zk("shared:z", "z-W5")].state == "open" {
            break;
        }
        std::thread::sleep(std::time::Duration::from_millis(5));
        rt.frame(DT);
    }
    assert_eq!(moved.len(), 2, "{moved:?}");
    assert_eq!(rt.state.ov.d.work[&zk("shared:z", "z-W5")].state, "open", "the screen shows the reopened item");
    let posted: serde_json::Value = serde_json::from_str(moved[0].strip_prefix("/api/v1/ledger/work ").expect("the move is posted first")).unwrap();
    assert_eq!((posted["projectKey"].as_str(), posted["itemId"].as_str(), posted["action"].as_str()), (Some("shared:z"), Some("z-W5"), Some("reopen")), "a move on found work goes to its own ledger");
    assert_eq!(moved[1], "/api/v1/ledger?projectKey=shared%3Az&view=item&id=z-W5", "and the item is read again after it");

    typed(&mut rt, "export");
    rt.frame(0.21);
    std::thread::sleep(std::time::Duration::from_millis(100));
    rt.state.ov.jump = Some(rune::TextEdit::new("exported"));
    rt.state.ov.d.found_names.clear();
    rt.frame(DT);
    assert_eq!(asked(&paths), ["/api/v1/ledger/search?q=export&limit=20"]);
    assert!(!rt.state.ov.d.found_names.contains_key("shared:z"), "a reply to text since changed is dropped whole");
    assert!(work_found(&rt.state).is_none());

    send(&rt, overview(4, sessions(&["a", "exporter"])));
    rt.frame(DT);
    typed(&mut rt, "exporter");
    assert_eq!(highlighted_session(&rt), Some("exporter".into()));
    let mut grew = Fetched { recorded: recorded(5, sessions(&["a", "exporter"])), ledgers: HashMap::new(), held: Vec::new(), asked: Vec::new(), actors_at: std::time::Instant::now(), usage: None, moved: Vec::new() };
    grew.ledgers.insert("p1".into(), (std::time::Instant::now(), Ok(vec![serde_json::from_value(json!({"id": "p1-W1", "text": "Fix the exporter", "state": "in_progress"})).unwrap()])));
    send(&rt, Bg::Overview(Box::new(grew)));
    rt.frame(DT);
    assert!(rt.state.ov.d.work.contains_key(&zk("p1", "p1-W1")));
    assert_eq!(highlighted_session(&rt), Some("exporter".into()), "a rebuild that adds a matching row above keeps the highlight");
    assert_eq!(crate::over::worksearch::note(&rt.state, &zk("shared:z", "z-W9")), None);
}

#[test]
fn showing_one_surface_opens_every_session_of_it_with_the_search_cleared() {
    if !isolated("showing_one_surface_opens_every_session_of_it_with_the_search_cleared") {
        return;
    }
    let dir = Dir::new("show-surface");
    let mut s = app(sessions(&["a"]), &dir);
    s.ov.sessions_query = rune::TextEdit::new("old search");
    s.ov.list.scope = 0;
    s.ov.list.status = Some("idle".into());
    crate::over::sessions::show_surface(&mut s, "codex");
    let want = crate::over::listing::request(&s);
    assert!(matches!(s.screen, Screen::Sessions));
    assert_eq!((want.scope.as_str(), want.surface.as_deref(), want.query.as_str(), want.status.as_deref()), ("all", Some("codex"), "", Some("idle")), "like the Mac app, the status filter stays");
}
