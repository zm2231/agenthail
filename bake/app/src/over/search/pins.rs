use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::catalog::Session;
use feed::search::Recheck;

use crate::App;

const RECHECK: Duration = Duration::from_secs(20);

pub struct Pins {
    rows: Vec<Session>,
    revision: HashMap<String, u64>,
    checking: Option<(String, u64)>,
    checked: HashMap<String, Instant>,
    requests: u64,
    tx: Sender<(String, u64, u64, Recheck)>,
    rx: Receiver<(String, u64, u64, Recheck)>,
}

impl Default for Pins {
    fn default() -> Pins {
        let (tx, rx) = channel();
        Pins { rows: Vec::new(), revision: HashMap::new(), checking: None, checked: HashMap::new(), requests: 0, tx, rx }
    }
}

impl Pins {
    pub fn get(&self, id: &str) -> Option<&Session> {
        self.rows.iter().find(|x| x.id == id)
    }

    pub fn pin(&mut self, row: Session) {
        let id = row.id.clone();
        self.rows.retain(|x| x.id != id);
        self.rows.push(row);
        self.touch(&id);
    }

    pub fn patch(&mut self, id: &str, patch: &feed::menu::Patch) {
        if let Some(x) = self.rows.iter_mut().find(|x| x.id == id) {
            patch.apply(x);
            self.touch(id);
        }
    }

    fn touch(&mut self, id: &str) {
        *self.revision.entry(id.to_string()).or_default() += 1;
        self.checked.remove(id);
    }

    fn unpin(&mut self, id: &str) {
        self.rows.retain(|x| x.id != id);
        self.revision.remove(id);
        self.checked.remove(id);
    }

    fn take(&mut self, id: String, seq: u64, revision: u64, found: Recheck) {
        if self.checking.as_ref() != Some(&(id.clone(), seq)) {
            return;
        }
        self.checking = None;
        if self.revision.get(&id).copied().unwrap_or_default() != revision {
            return;
        }
        match found {
            Recheck::Fresh(row) => {
                if let Some(x) = self.rows.iter_mut().find(|x| x.id == id) {
                    *x = *row;
                }
                self.checked.insert(id, Instant::now());
            }
            Recheck::Gone => self.unpin(&id),
            Recheck::Unknown(_) => {
                self.checked.insert(id, Instant::now());
            }
        }
    }
}

fn recheck(s: &mut App) -> bool {
    let listed: HashSet<&str> = s.sessions.iter().map(|x| x.id.as_str()).collect();
    let pins = &mut s.ov.search.pins;
    let before = pins.rows.len();
    pins.rows.retain(|x| !listed.contains(x.id.as_str()));
    let dropped = pins.rows.len() != before;
    let Some(id) = super::selected(s) else { return dropped };
    let pins = &s.ov.search.pins;
    let Some(row) = pins.get(&id).cloned() else { return dropped };
    if pins.checking.is_some() || pins.checked.get(&id).is_some_and(|at| at.elapsed() < RECHECK) {
        return dropped;
    }
    let Some(client) = s.client.clone() else { return dropped };
    let pins = &mut s.ov.search.pins;
    pins.requests += 1;
    let (seq, revision) = (pins.requests, pins.revision.get(&id).copied().unwrap_or_default());
    pins.checking = Some((id.clone(), seq));
    let tx = pins.tx.clone();
    std::thread::spawn(move || {
        let found = client.recheck(&row);
        let _ = tx.send((id, seq, revision, found));
    });
    dropped
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok((id, seq, revision, found)) = s.ov.search.pins.rx.try_recv() {
        s.ov.search.pins.take(id, seq, revision, found);
        changed = true;
    }
    changed | recheck(s)
}
