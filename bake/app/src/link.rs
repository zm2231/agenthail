use std::cell::RefCell;

use feed::link::{self, Link, Waiting};
use feed::search::Command;
use rune::Tx;

use crate::{App, Bg};

const RELEASE: bool = matches!(env!("AGENTHAIL_RELEASE").as_bytes(), b"true");
pub const SCHEME: &str = if RELEASE { "agenthail" } else { "agenthail-bake" };
pub const INSTANCE: &str = if RELEASE { "com.agenthail.app" } else { "com.agenthail.bake" };
pub const LOCK: &str = if RELEASE { "menu-app.lock" } else { "bake-app.lock" };

pub struct Found {
    ask: u64,
    result: Result<feed::catalog::Session, String>,
}

thread_local! {
    static WAITING: RefCell<Waiting> = RefCell::new(Waiting::default());
}

pub fn on_open_url(s: &mut App, url: &str, tx: &mut Tx) {
    let Some(link) = Link::parse(url, SCHEME) else {
        eprintln!("agenthail: ignored a link it doesn't handle");
        return;
    };
    crate::menubar::front(s);
    WAITING.with_borrow_mut(Waiting::moved);
    match link {
        Link::Open => {}
        Link::NewSession => crate::over::search::run(s, Command::NewSession, tx),
        Link::Session(reference) if s.fixtures.is_some() => open(s, &reference),
        Link::Session(reference) => {
            if let Some(reference) = WAITING.with_borrow_mut(|w| w.hold(reference)) {
                open(s, &reference);
            }
        }
    }
}

pub fn moved() {
    WAITING.with_borrow_mut(Waiting::moved);
}

pub fn listed(s: &mut App) {
    if let Some(reference) = WAITING.with_borrow_mut(Waiting::listed) {
        open(s, &reference);
    }
}

fn open(s: &mut App, reference: &str) {
    let reference = feed::composer::current(&s.rekeyed, reference).to_string();
    if let Some(id) = link::resolve(&s.sessions, &reference) {
        let id = id.to_string();
        crate::over::open_session(s, &id);
        return;
    }
    let Some(client) = s.client.clone() else {
        crate::over::open_session(s, &reference);
        return;
    };
    let ask = WAITING.with_borrow_mut(Waiting::ask);
    let tx = s.outbox.clone();
    std::thread::spawn(move || {
        let result = client.session_for_link(&reference);
        let _ = tx.send(Bg::Link(Found { ask, result }));
    });
}

pub fn found(s: &mut App, found: Found) {
    if !WAITING.with_borrow_mut(|w| w.answered(found.ask)) {
        return;
    }
    match found.result {
        Ok(row) => match link::resolve(&s.sessions, &row.id).map(str::to_string) {
            Some(id) => crate::over::open_session(s, &id),
            None => crate::over::search::open_read(s, row),
        },
        Err(message) => s.ov.notice = Some(message),
    }
}
