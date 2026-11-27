use std::cell::RefCell;

use feed::link::{self, Link};
use feed::search::Command;
use rune::Tx;

use crate::App;

const RELEASE: bool = matches!(env!("AGENTHAIL_RELEASE").as_bytes(), b"true");
pub const SCHEME: &str = if RELEASE { "agenthail" } else { "agenthail-bake" };
pub const INSTANCE: &str = if RELEASE { "com.agenthail.app" } else { "com.agenthail.bake" };

thread_local! {
    static WAITING: RefCell<Option<String>> = const { RefCell::new(None) };
}

pub fn on_open_url(s: &mut App, url: &str, tx: &mut Tx) {
    let Some(link) = Link::parse(url, SCHEME) else {
        eprintln!("agenthail: ignored a link it doesn't handle");
        return;
    };
    crate::menubar::front(s);
    match link {
        Link::Open => {}
        Link::NewSession => crate::over::search::run(s, Command::NewSession, tx),
        Link::Session(reference) if s.status != "live" && s.fixtures.is_none() => WAITING.with_borrow_mut(|w| *w = Some(reference)),
        Link::Session(reference) => open(s, &reference),
    }
}

pub fn listed(s: &mut App) {
    if let Some(reference) = WAITING.with_borrow_mut(Option::take) {
        open(s, &reference);
    }
}

fn open(s: &mut App, reference: &str) {
    let reference = feed::composer::current(&s.rekeyed, reference).to_string();
    let id = link::resolve(&s.sessions, &reference).unwrap_or(&reference).to_string();
    crate::over::open_session(s, &id);
}
