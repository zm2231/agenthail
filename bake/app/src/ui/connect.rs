use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{Receiver, RecvTimeoutError, Sender, channel};
use std::time::{Duration, Instant};

use feed::catalog::Client;
use feed::connect::{self, Problem};
use feed::watch::{Catalog, SAFETY};
use rune::{Cx, El, col, el, icon, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize};

use crate::icons;
use crate::ui::watch::{self, Feed};
use crate::theme::{self, BODY, HEADING, META, failed, faint, muted};
use crate::{App, Bg};

const EVERY: Duration = Duration::from_secs(2);
const SETTLE: Duration = Duration::from_secs(2);
const RECHECK: Duration = Duration::from_secs(30);
const GAP: Duration = Duration::from_secs(1);

#[derive(Default)]
pub struct Link {
    pub problem: Option<Problem>,
    pub starting: bool,
    poke: Option<Sender<Feed>>,
    as_of: Option<Instant>,
}

pub fn start(s: &mut App) {
    match connect::open() {
        Ok(client) => attach(s, client),
        Err(problem) => down(s, problem),
    }
}

fn attach(s: &mut App, client: Client) {
    let (poke, poked) = channel();
    s.link.poke = Some(poke.clone());
    sessions(client.clone(), s.outbox.clone(), poke, poked);
    let stream = feed::watch::Stream::default();
    watch::ledgers(client.clone(), s.outbox.clone(), stream.clone());
    let (poke, poked) = channel();
    crate::watch_overview(client.clone(), s.outbox.clone(), poked, stream);
    s.overview_poke = Some(poke);
    crate::over::inbox::start(&mut s.ov.inbox, &client);
    s.client = Some(client);
}

fn sessions(c: Client, tx: Sender<Bg>, poke: Sender<Feed>, poked: Receiver<Feed>) {
    std::thread::spawn(move || {
        let mut catalog = Catalog::default();
        let mut cursor = Arc::new(AtomicU64::new(0));
        let (mut resume, resumed) = channel();
        let generation = Arc::new(AtomicU64::new(0));
        let mut resumed = Some(resumed);
        let (mut checked, mut last): (Option<Instant>, Option<Instant>) = (None, None);
        let (mut due, mut down, mut gap, mut confirm) = (true, false, false, 0u8);
        loop {
            if due && last.is_none_or(|at| at.elapsed() >= GAP) {
                last = Some(Instant::now());
                let probed = if checked.is_some_and(|at| at.elapsed() < RECHECK) { Ok(()) } else { c.probe().map(|_| checked = Some(Instant::now())) };
                let message = match probed.and_then(|()| c.snapshot().map_err(|e| c.probe().err().unwrap_or_else(|| Problem::of(&e.to_string())))) {
                    Err(problem) => {
                        checked = None;
                        down = true;
                        Bg::Down(problem)
                    }
                    Ok(snap) => {
                        down = false;
                        due = confirm > 0;
                        confirm = confirm.saturating_sub(1);
                        let applied = catalog.adopt(snap.sessions, &snap.host_epoch, snap.catalog_seq);
                        let rev = applied.rev.unwrap_or(snap.decisions_rev);
                        if tx.send(Bg::SendSettings(snap.busy_delivery, snap.rekeyed, snap.queue)).is_err() || tx.send(Bg::DecisionsRev(rev)).is_err() {
                            return;
                        }
                        if let Some(resumed) = resumed.take() {
                            cursor.store(catalog.seq(), Ordering::Release);
                            watch::catalog(c.clone(), cursor.clone(), resumed, poke.clone(), generation.clone(), 0);
                            watch::events(c.clone(), snap.event_cursor, tx.clone());
                        } else if applied.restart {
                            gap = false;
                            let mine = generation.fetch_add(1, Ordering::AcqRel) + 1;
                            cursor = Arc::new(AtomicU64::new(catalog.seq()));
                            let (fresh, resumed) = channel();
                            resume = fresh;
                            watch::catalog(c.clone(), cursor.clone(), resumed, poke.clone(), generation.clone(), mine);
                        } else if std::mem::take(&mut gap) {
                            cursor.store(catalog.seq(), Ordering::Release);
                            let _ = resume.send(());
                        }
                        Bg::Snapshot(snap.updated_at, catalog.epoch().to_string(), catalog.seq(), catalog.sessions().to_vec(), None)
                    }
                };
                if tx.send(message).is_err() {
                    return;
                }
            }
            let since = last.map(|at| at.elapsed()).unwrap_or_default();
            let wait = if down { EVERY } else if due { GAP.saturating_sub(since) } else { SAFETY.saturating_sub(since) };
            let mut batch = Vec::new();
            match poked.recv_timeout(wait) {
                Err(RecvTimeoutError::Disconnected) => return,
                Err(RecvTimeoutError::Timeout) => due = true,
                Ok(first) => {
                    let mut next = Some(first);
                    let settle = Instant::now() + feed::watch::SETTLE;
                    while let Some(feed) = next.take() {
                        match feed {
                            Feed::Poke => {
                                due = true;
                                last = None;
                            }
                            Feed::Changed => due = true,
                            Feed::Confirm => {
                                due = true;
                                last = None;
                                confirm = 1;
                            }
                            Feed::Gap(from) if from != generation.load(Ordering::Acquire) => {}
                            Feed::Gap(_) => {
                                due = true;
                                gap = true;
                            }
                            Feed::Catalog(from, event) => {
                                if from == generation.load(Ordering::Acquire) {
                                    batch.push(event);
                                }
                            }
                        }
                        next = poked.recv_timeout(settle.saturating_duration_since(Instant::now())).ok();
                    }
                }
            }
            if batch.is_empty() {
                if gap {
                    catalog.forget();
                }
                continue;
            }
            let applied = catalog.apply(batch);
            if gap {
                catalog.forget();
            }
            due |= applied.reread;
            if let Some(rev) = applied.rev
                && tx.send(Bg::DecisionsRev(rev)).is_err()
            {
                return;
            }
            if applied.news && tx.send(Bg::SessionNews).is_err() {
                return;
            }
            if applied.changed && tx.send(Bg::Snapshot(feed::watch::stamp(), catalog.epoch().to_string(), catalog.seq(), catalog.sessions().to_vec(), Some(applied.touched))).is_err() {
                return;
            }
        }
    });
}

pub fn down(s: &mut App, problem: Problem) {
    if !matches!(s.link.problem, Some(Problem::StartFailed(_))) {
        s.status = problem.status();
        s.link.problem = Some(problem);
    }
}

pub fn up(s: &mut App) {
    s.link.as_of = Some(Instant::now());
    if !matches!(s.link.problem, Some(Problem::StartFailed(_))) {
        s.link.problem = None;
    }
}

pub fn confirm(s: &App) {
    if let Some(poke) = &s.link.poke {
        let _ = poke.send(Feed::Confirm);
    }
}

pub fn changed(s: &App) {
    if let Some(poke) = &s.link.poke {
        let _ = poke.send(Feed::Changed);
    }
}

pub fn retry(s: &mut App) {
    if matches!(s.link.problem, Some(Problem::StartFailed(_))) {
        s.link.problem = None;
    }
    if s.client.is_none() {
        return start(s);
    }
    if let Some(poke) = &s.link.poke {
        let _ = poke.send(Feed::Poke);
    }
}

pub fn start_daemon(s: &mut App) {
    if s.link.starting || s.fixtures.is_some() {
        return;
    }
    s.link.starting = true;
    let tx = s.outbox.clone();
    std::thread::spawn(move || {
        let result = connect::start_daemon();
        if result.is_ok() {
            std::thread::sleep(SETTLE);
        }
        let _ = tx.send(Bg::Started(result));
    });
}

pub fn started(s: &mut App, result: Result<(), Problem>) {
    s.link.starting = false;
    match result {
        Ok(()) => {
            if matches!(s.link.problem, Some(Problem::StartFailed(_))) {
                s.link.problem = None;
            }
            retry(s);
        }
        Err(problem) => {
            s.link.problem = None;
            down(s, problem);
        }
    }
}

pub fn screen(s: &App, cx: &mut Cx<App>) -> Option<El> {
    if s.snapshots > 0 || s.fixtures.is_some() {
        return None;
    }
    let problem = s.link.problem.as_ref()?;
    Some(crate::over::ui::frame(cx, s, None, panel(s, problem), None, rune_kit::shell::Toolbar::new()))
}

fn actions(s: &App, problem: &Problem, size: ControlSize) -> El {
    let k = theme::kit();
    let mut r = row().gap(8.0).items_center();
    if s.link.starting {
        return r.child(rune_kit::Loader::new().size(12.0).color(faint()).build(k)).child(text("Starting Agenthail…").font(META).color(muted()));
    }
    if let Some(label) = problem.start() {
        r = r.child(Button::new(label).variant(ButtonVariant::Contrast).control_size(size).on_press(start_daemon).build(k).key("connect-start"));
    }
    if problem.retries() {
        r = r.child(Button::new("Try again").variant(ButtonVariant::Ghost).control_size(size).on_press(retry).build(k).key("connect-retry"));
    }
    r
}

fn panel(s: &App, problem: &Problem) -> El {
    let k = theme::kit();
    let glyph = if *problem == Problem::Slow { &icons::CLOCK } else { &icons::BOLT };
    let mut c = col().key("connect").w_full().h_full().items_center().justify_center().gap(12.0).child(icon(glyph, 28.0).color(faint())).child(text(problem.title()).font(HEADING).color(muted()));
    c = c.child(text(problem.detail()).font(BODY).color(faint()).wrap().max_w(460.0));
    if *problem == Problem::Slow {
        c = c.child(rune_kit::Loader::new().size(16.0).color(faint()).build(k));
    }
    c.child(actions(s, problem, ControlSize::Regular)).child(el().h(80.0))
}

pub fn bar(s: &App) -> Option<El> {
    if s.snapshots == 0 || s.fixtures.is_some() {
        return None;
    }
    let problem = s.link.problem.as_ref()?;
    Some(
        row()
            .key("connect-bar")
            .gap(6.0)
            .items_center()
            .child(el().size(6.0, 6.0).radius(3.0).fill(failed()))
            .child(text(problem.bar(s.link.as_of.map(|at| at.elapsed()))).font(META).color(muted()))
            .child(actions(s, problem, ControlSize::Small))
            .help(problem.detail()),
    )
}

#[cfg(test)]
mod tests {
    use rune::{AppSpec, Env, Runtime};

    use crate::App;

    fn shown(rt: &Runtime<App>) -> String {
        rt.elements().into_iter().filter_map(|e| e.text).collect::<Vec<_>>().join("\n")
    }

    fn settle(rt: &mut Runtime<App>, until: &str) -> String {
        for _ in 0..300 {
            rt.frame(1.0 / 60.0);
            let text = shown(rt);
            if text.contains(until) {
                return text;
            }
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        shown(rt)
    }

    fn press(rt: &mut Runtime<App>, key: &str) {
        let (x, y) = rt.find(key).unwrap_or_else(|| panic!("no {key}"));
        rt.pointer_move(x, y);
        rt.pointer_down(x, y);
        rt.frame(1.0 / 60.0);
        rt.pointer_up(x, y);
        rt.frame(1.0 / 60.0);
    }

    fn runtime() -> Runtime<App> {
        let spec = AppSpec::<App> {
            poll: Some(crate::poll),
            on_key: Some(crate::on_key),
            fonts: rune_theme_opencode::FONTS,
            families: rune_theme_opencode::FAMILIES,
            ..AppSpec::new("Agenthail", (1440.0, 900.0), crate::init, crate::ui::view)
        };
        rune::App::new(spec).headless_runtime((1440.0, 900.0), Env { system_dark: true, scale: 1.0 })
    }

    fn in_child(test: &str, url: &str, key: Option<&str>) -> Option<std::path::PathBuf> {
        if let Ok(home) = std::env::var("AGENTHAIL_CONNECT_HOME") {
            return Some(home.into());
        }
        let home = std::env::temp_dir().join(format!("agenthail-connect-{}-{test}", std::process::id()));
        std::fs::create_dir_all(home.join(".agenthail")).unwrap();
        if let Some(key) = key {
            std::fs::write(home.join(".agenthail/dashboard.token"), key).unwrap();
        }
        let out = std::process::Command::new(std::env::current_exe().unwrap())
            .args([&format!("ui::connect::tests::{test}"), "--exact", "--nocapture", "--test-threads=1"])
            .env("AGENTHAIL_CONNECT_HOME", &home)
            .env("HOME", &home)
            .env("AGENTHAIL_API_URL", url)
            .env_remove("AGENTHAIL_API_TOKEN")
            .env_remove("AGENTHAIL_FIXTURES")
            .output()
            .unwrap();
        let _ = std::fs::remove_dir_all(&home);
        let said = format!("{}{}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr));
        assert!(out.status.success() && said.contains("1 passed"), "{said}");
        None
    }

    #[test]
    fn without_a_key_or_a_daemon_the_window_says_what_is_wrong_and_offers_to_start() {
        let closed = std::net::TcpListener::bind("127.0.0.1:0").unwrap().local_addr().unwrap();
        let url = std::env::var("AGENTHAIL_API_URL").ok().filter(|_| std::env::var("AGENTHAIL_CONNECT_HOME").is_ok()).unwrap_or(format!("http://{closed}"));
        let Some(home) = in_child("without_a_key_or_a_daemon_the_window_says_what_is_wrong_and_offers_to_start", &url, None) else { return };
        let mut rt = runtime();
        let text = settle(&mut rt, "no Agenthail key");
        assert!(text.contains("Agenthail isn't running"), "{text}");
        assert!(text.contains("no Agenthail key yet"), "{text}");
        assert!(rt.find("connect-start").is_some());
        assert!(rt.find("connect-retry").is_some());

        std::fs::write(home.join(".agenthail/dashboard.token"), "t\n").unwrap();
        press(&mut rt, "connect-retry");
        let text = settle(&mut rt, "Nothing is answering");
        assert!(text.contains(&format!("Nothing is answering at {url}")), "{text}");
        assert!(rt.find("connect-start").is_some());
    }

    #[test]
    fn a_daemon_on_another_protocol_asks_for_a_restart() {
        use std::io::{Read, Write};
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        std::thread::spawn(move || {
            for stream in listener.incoming().flatten() {
                let mut stream = stream;
                let _ = stream.read(&mut [0; 4096]);
                let body = r#"{"protocol":5,"minimumProtocol":5,"maximumProtocol":5}"#;
                let _ = write!(stream, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len());
            }
        });
        let url = std::env::var("AGENTHAIL_API_URL").ok().filter(|_| std::env::var("AGENTHAIL_CONNECT_HOME").is_ok()).unwrap_or(url);
        if in_child("a_daemon_on_another_protocol_asks_for_a_restart", &url, Some("t")).is_none() {
            return;
        }
        let mut rt = runtime();
        let text = settle(&mut rt, "protocol 5");
        assert!(text.contains("Agenthail needs an update"), "{text}");
        assert!(text.contains("speaks protocol 5; this app needs protocol 1"), "{text}");
        assert!(rt.find("connect-start").is_some());
    }
}
