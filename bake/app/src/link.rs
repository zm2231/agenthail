use std::cell::RefCell;

use feed::link::{self, Link};
use feed::search::Command;
use rune::Tx;

use crate::{App, Bg, Screen};

const RELEASE: bool = matches!(env!("AGENTHAIL_RELEASE").as_bytes(), b"true");
pub const SCHEME: &str = if RELEASE { "agenthail" } else { "agenthail-bake" };
pub const INSTANCE: &str = if RELEASE { "com.agenthail.app" } else { "com.agenthail.bake" };
pub const LOCK: &str = if RELEASE { "menu-app.lock" } else { "bake-app.lock" };

pub struct Found {
    reference: String,
    result: Result<feed::catalog::Session, String>,
}

#[derive(Default)]
struct Waiting {
    listed: bool,
    reference: Option<String>,
    asked: Option<String>,
    at: Option<(Screen, Option<String>)>,
}

thread_local! {
    static WAITING: RefCell<Waiting> = RefCell::new(Waiting::default());
}

fn place(s: &App) -> (Screen, Option<String>) {
    (s.screen.clone(), s.selected.clone())
}

pub fn on_open_url(s: &mut App, url: &str, tx: &mut Tx) {
    let Some(link) = Link::parse(url, SCHEME) else {
        eprintln!("agenthail: ignored a link it doesn't handle");
        return;
    };
    crate::menubar::front(s);
    let listed = WAITING.with_borrow_mut(|w| {
        *w = Waiting { listed: w.listed, ..Waiting::default() };
        w.listed
    });
    match link {
        Link::Open => {}
        Link::NewSession => crate::over::search::run(s, Command::NewSession, tx),
        Link::Session(reference) if !listed && s.fixtures.is_none() => {
            let at = place(s);
            WAITING.with_borrow_mut(|w| (w.reference, w.at) = (Some(reference), Some(at)));
        }
        Link::Session(reference) => open(s, &reference),
    }
}

pub fn watch(s: &App) {
    WAITING.with_borrow_mut(|w| {
        if w.at.as_ref().is_some_and(|at| *at != place(s)) {
            (w.reference, w.asked, w.at) = (None, None, None);
        }
    });
}

pub fn listed(s: &mut App) {
    watch(s);
    let waiting = WAITING.with_borrow_mut(|w| {
        w.listed = true;
        w.at = None;
        w.reference.take()
    });
    if let Some(reference) = waiting {
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
    let at = place(s);
    WAITING.with_borrow_mut(|w| (w.asked, w.at) = (Some(reference.clone()), Some(at)));
    let tx = s.outbox.clone();
    std::thread::spawn(move || {
        let result = client.session_for_link(&reference);
        let _ = tx.send(Bg::Link(Found { reference, result }));
    });
}

pub fn found(s: &mut App, found: Found) {
    watch(s);
    let asked = WAITING.with_borrow_mut(|w| {
        let asked = w.asked.take_if(|r| *r == found.reference);
        if asked.is_some() {
            w.at = None;
        }
        asked
    });
    if asked.is_none() {
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
