use std::cell::Cell;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::mpsc::{Receiver, Sender};

use feed::catalog::Client;
use feed::watch::{CatalogEvent, Ledgers, Opened, Wants, backoff, wants};

use crate::Bg;

pub enum Feed {
    Poke,
    Changed,
    Catalog(CatalogEvent),
    Gap,
}

pub fn events(client: Client, mut after: u64, tx: Sender<Bg>) {
    std::thread::spawn(move || {
        let (mut failures, mut lost) = (0u32, false);
        loop {
            let gone = Cell::new(false);
            let result = client.follow(
                after,
                || {
                    if lost {
                        gone.set(tx.send(Bg::Reopened).is_err());
                    }
                },
                |e| {
                    if e.id > 0 {
                        after = after.max(e.id);
                    }
                    let w = wants(&e);
                    if w != Wants::default() && tx.send(Bg::Changed(w)).is_err() {
                        gone.set(true);
                    }
                    !gone.get()
                },
            );
            if gone.get() {
                return;
            }
            failures = if result.is_ok() { 0 } else { failures + 1 };
            lost = true;
            std::thread::sleep(backoff(failures));
        }
    });
}

pub fn ledgers(client: Client, tx: Sender<Bg>, streamed: Arc<AtomicBool>) {
    std::thread::spawn(move || {
        let (mut seen, mut failures) = (Ledgers::default(), 0u32);
        loop {
            let mut gone = false;
            let result = client.follow_ledgers(|| streamed.store(true, Ordering::Release), |e| {
                if let Some(key) = seen.changed(&e) {
                    gone |= tx.send(Bg::LedgerChanged(key)).is_err();
                }
                !gone
            });
            streamed.store(false, Ordering::Release);
            if gone {
                return;
            }
            failures = if result.is_ok() { 0 } else { failures + 1 };
            std::thread::sleep(backoff(failures));
        }
    });
}

pub fn catalog(client: Client, cursor: Arc<AtomicU64>, resume: Receiver<()>, tx: Sender<Feed>) {
    std::thread::spawn(move || {
        let mut failures = 0u32;
        loop {
            let mut gone = false;
            let result = client.follow_catalog(cursor.load(Ordering::Acquire), |e| {
                let Some(event) = CatalogEvent::of(&e) else { return true };
                cursor.fetch_max(event.seq, Ordering::AcqRel);
                gone |= tx.send(Feed::Catalog(event)).is_err();
                !gone
            });
            if gone {
                return;
            }
            match result {
                Ok(Opened::Gap) => {
                    if tx.send(Feed::Gap).is_err() || resume.recv().is_err() {
                        return;
                    }
                    failures = 0;
                    continue;
                }
                Ok(Opened::Stream) => failures = 0,
                Err(_) => failures += 1,
            }
            std::thread::sleep(backoff(failures));
        }
    });
}
