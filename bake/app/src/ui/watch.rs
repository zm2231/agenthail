use std::cell::Cell;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{Receiver, Sender};
use std::time::Instant;

use feed::catalog::Client;
use feed::watch::{CatalogEvent, Ledgers, Opened, Stream, Wants, backoff, wants};

use crate::Bg;

pub enum Feed {
    Poke,
    Changed,
    Confirm,
    Catalog(u64, CatalogEvent),
    Gap(u64),
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

pub fn ledgers(client: Client, tx: Sender<Bg>, stream: Stream) {
    std::thread::spawn(move || {
        let (mut seen, mut failures, mut lost) = (Ledgers::default(), 0u32, false);
        loop {
            let gone = Cell::new(false);
            let result = client.follow_ledgers(
                || {
                    stream.open();
                    if lost && tx.send(Bg::LedgersReopened).is_err() {
                        gone.set(true);
                    }
                },
                |e| {
                    if let Some(key) = seen.changed(&e, stream.settled())
                        && tx.send(Bg::LedgerChanged(key, Instant::now())).is_err()
                    {
                        gone.set(true);
                    }
                    !gone.get()
                },
            );
            stream.close();
            if gone.get() {
                return;
            }
            lost = true;
            failures = if result.is_ok() { 0 } else { failures + 1 };
            std::thread::sleep(backoff(failures));
        }
    });
}

pub fn catalog(client: Client, cursor: Arc<AtomicU64>, resume: Receiver<()>, tx: Sender<Feed>, generation: Arc<AtomicU64>, mine: u64) {
    let current = move || generation.load(Ordering::Acquire) == mine;
    std::thread::spawn(move || {
        let mut failures = 0u32;
        while current() {
            let mut gone = false;
            let result = client.follow_catalog(cursor.load(Ordering::Acquire), |e| {
                if !current() {
                    gone = true;
                    return false;
                }
                let Some(event) = CatalogEvent::of(&e) else { return true };
                cursor.fetch_max(event.seq, Ordering::AcqRel);
                gone |= tx.send(Feed::Catalog(mine, event)).is_err();
                !gone
            });
            if gone || !current() {
                return;
            }
            match result {
                Ok(Opened::Gap) => {
                    if tx.send(Feed::Gap(mine)).is_err() || resume.recv().is_err() {
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
