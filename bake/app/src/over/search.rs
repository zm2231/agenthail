mod activity;
mod pins;

use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::catalog::Session;
use feed::search::{Codex, Command, codex_query};
use rune::{El, KeyEvent, Tx};

use crate::{App, Screen};

pub(super) const SETTLE: Duration = Duration::from_millis(250);

pub struct State {
    codex: Option<(String, u64, Option<Result<Codex, String>>)>,
    typed: Option<(String, Instant)>,
    requests: u64,
    pins: pins::Pins,
    activity: activity::State,
    tx: Sender<(u64, Result<Codex, String>)>,
    rx: Receiver<(u64, Result<Codex, String>)>,
}

impl Default for State {
    fn default() -> State {
        let (tx, rx) = channel();
        State { codex: None, typed: None, requests: 0, pins: pins::Pins::default(), activity: activity::State::default(), tx, rx }
    }
}

pub fn pinned<'a>(s: &'a App, id: &str) -> feed::search::Pinned<'a> {
    s.ov.search.pins.get(id)
}

pub fn patch_pinned(s: &mut App, id: &str, patch: &feed::menu::Patch) {
    s.ov.search.pins.patch(id, patch);
}

pub fn open_pinned(s: &mut App, row: Session) {
    let id = row.id.clone();
    s.ov.search.pins.pin(row);
    crate::over::open_session(s, &id);
}

pub fn open_read(s: &mut App, row: Session) {
    let id = row.id.clone();
    s.ov.search.pins.pin_read(row);
    crate::over::open_session(s, &id);
}

pub fn activity_view(s: &App, layer: &mut Vec<El>) {
    activity::view(s, layer);
}

pub fn activity_key(s: &mut App, ev: &KeyEvent) -> Option<bool> {
    activity::key(s, ev)
}

fn jump_query(s: &App) -> Option<String> {
    s.ov.jump.as_ref().and_then(|e| codex_query(&e.text))
}

pub fn forget_codex(s: &mut App) {
    s.ov.search.codex = None;
    s.ov.search.typed = None;
}

pub fn codex_for(s: &App) -> Option<&Option<Result<Codex, String>>> {
    let q = jump_query(s)?;
    match &s.ov.search.codex {
        Some((asked, _, found)) if *asked == q => Some(found),
        _ => Some(&None),
    }
}

fn ask_codex(s: &mut App, q: String) {
    let st = &mut s.ov.search;
    st.requests += 1;
    let seq = st.requests;
    let Some(client) = s.client.clone() else {
        s.ov.search.codex = Some((q, seq, Some(Err("Codex history search needs a running Agenthail.".into()))));
        return;
    };
    let st = &mut s.ov.search;
    st.codex = Some((q.clone(), seq, None));
    let tx = st.tx.clone();
    std::thread::spawn(move || {
        let found = client.search_codex(&q);
        let _ = tx.send((seq, found));
    });
}

fn settle_codex(s: &mut App) -> bool {
    let Some(q) = jump_query(s) else {
        let st = &mut s.ov.search;
        let had = st.codex.take().is_some();
        st.typed = None;
        return had;
    };
    let st = &mut s.ov.search;
    match &st.typed {
        Some((typed, _)) if *typed == q => {}
        _ => {
            if st.codex.as_ref().is_some_and(|(asked, _, _)| *asked != q) {
                st.codex = None;
            }
            st.typed = Some((q, Instant::now()));
            return false;
        }
    }
    let due = st.typed.as_ref().is_some_and(|(_, at)| at.elapsed() >= SETTLE);
    let asked = st.codex.as_ref().is_some_and(|(a, _, _)| *a == q);
    if due && !asked {
        ask_codex(s, q);
        return true;
    }
    false
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok((seq, found)) = s.ov.search.rx.try_recv() {
        if let Some((_, _, slot)) = s.ov.search.codex.as_mut().filter(|(_, asked, _)| *asked == seq) {
            *slot = Some(found);
            changed = true;
        }
    }
    changed |= pins::drain(s);
    changed |= settle_codex(s);
    changed |= activity::drain(s);
    changed
}

pub fn selected(s: &App) -> Option<String> {
    (s.screen == Screen::Session).then(|| s.selected.clone()).flatten()
}

pub fn selected_title(s: &App, id: &str) -> String {
    crate::ui::gone::shown(s, id).map(|x| x.title()).unwrap_or_else(|| "this session".into())
}

pub fn can_stop(s: &App, id: &str) -> bool {
    crate::can_stop(s, id) && !s.stopping.contains(id)
}

pub fn run(s: &mut App, command: Command, tx: &mut Tx) {
    s.ov.jump = None;
    match command {
        Command::NewSession => {
            let prefill = crate::ui::new_session::prefill_for(s);
            crate::ui::new_session::open(s, prefill, tx);
        }
        Command::Stop => {
            if let Some(id) = selected(s).filter(|id| can_stop(s, id)) {
                crate::stop(s, &id);
            }
        }
        Command::Rename => {
            if let Some(id) = selected(s) {
                crate::ui::session_menu::run(s, &id, feed::menu::Act::Rename);
            }
        }
        Command::Refresh => refresh(s),
        Command::Restart => crate::ui::connect::start_daemon(s),
        Command::Activity => activity::open(s, tx),
    }
}

fn refresh(s: &mut App) {
    if let Some(poke) = &s.overview_poke {
        let _ = poke.send(());
    }
    if let Some(client) = s.client.clone() {
        crate::over::inbox::refresh(&client, &s.ov.inbox);
    }
    match selected(s) {
        Some(id) => {
            crate::fetch_requests(s, &id);
            crate::ui::session_menu::load(s, &id);
            crate::ui::queue::fetch(s, &id);
        }
        None => crate::over::arrive(s),
    }
}
