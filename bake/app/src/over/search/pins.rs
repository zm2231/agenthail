use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::catalog::Session;
use feed::search::{Pinned, Recheck};

use crate::App;

const RECHECK: Duration = Duration::from_secs(20);

pub struct Pins {
    rows: Vec<Session>,
    verified: HashSet<String>,
    read: HashSet<String>,
    revision: HashMap<String, u64>,
    checking: HashMap<String, u64>,
    checked: HashMap<String, Instant>,
    requests: u64,
    tx: Sender<(String, u64, u64, Recheck)>,
    rx: Receiver<(String, u64, u64, Recheck)>,
}

impl Default for Pins {
    fn default() -> Pins {
        let (tx, rx) = channel();
        Pins { rows: Vec::new(), verified: HashSet::new(), read: HashSet::new(), revision: HashMap::new(), checking: HashMap::new(), checked: HashMap::new(), requests: 0, tx, rx }
    }
}

impl Pins {
    pub fn get(&self, id: &str) -> Pinned<'_> {
        match self.row(id) {
            Some(x) if self.verified.contains(id) => Pinned::Verified(x),
            Some(_) => Pinned::Withheld,
            None => Pinned::Unpinned,
        }
    }

    fn row(&self, id: &str) -> Option<&Session> {
        self.rows.iter().find(|x| x.id == id)
    }

    pub fn pin(&mut self, row: Session) {
        let id = row.id.clone();
        self.rows.retain(|x| x.id != id);
        self.rows.push(row);
        self.verified.remove(&id);
        self.read.remove(&id);
        self.touch(&id);
    }

    pub fn pin_read(&mut self, row: Session) {
        let id = row.id.clone();
        self.pin(row);
        self.verified.insert(id.clone());
        self.read.insert(id.clone());
        self.checked.insert(id, Instant::now());
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
        self.verified.remove(id);
        self.read.remove(id);
        self.revision.remove(id);
        self.checked.remove(id);
    }

    fn take(&mut self, id: String, seq: u64, revision: u64, found: Recheck) -> Option<String> {
        if self.checking.get(&id) != Some(&seq) {
            return None;
        }
        self.checking.remove(&id);
        if self.revision.get(&id).copied().unwrap_or_default() != revision {
            return None;
        }
        match found {
            Recheck::Fresh(row) => {
                if let Some(x) = self.rows.iter_mut().find(|x| x.id == id) {
                    *x = *row;
                }
                self.verified.insert(id.clone());
                self.checked.insert(id, Instant::now());
            }
            Recheck::Gone => {
                self.unpin(&id);
                return Some(id);
            }
            Recheck::Unknown(_) => {
                if !self.read.contains(&id) {
                    self.verified.remove(&id);
                }
                self.checked.insert(id, Instant::now());
            }
        }
        None
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
    let Some(row) = pins.row(&id).cloned() else { return dropped };
    if pins.checking.contains_key(&id) || pins.checked.get(&id).is_some_and(|at| at.elapsed() < RECHECK) {
        return dropped;
    }
    let Some(client) = s.client.clone() else { return dropped };
    let pins = &mut s.ov.search.pins;
    pins.requests += 1;
    let (seq, revision) = (pins.requests, pins.revision.get(&id).copied().unwrap_or_default());
    pins.checking.insert(id.clone(), seq);
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
        if let Some(gone) = s.ov.search.pins.take(id, seq, revision, found) {
            crate::ui::subagents::forget(s, &gone);
        }
        changed = true;
    }
    changed | recheck(s)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn verdict(p: &Pins, id: &str) -> &'static str {
        match p.get(id) {
            Pinned::Unpinned => "unpinned",
            Pinned::Verified(_) => "verified",
            Pinned::Withheld => "withheld",
        }
    }

    fn check(p: &mut Pins, id: &str, found: Recheck) -> Option<String> {
        p.requests += 1;
        let (seq, revision) = (p.requests, p.revision.get(id).copied().unwrap_or_default());
        p.checking.insert(id.to_string(), seq);
        p.take(id.to_string(), seq, revision, found)
    }

    #[test]
    fn a_pin_is_withheld_until_verified_and_a_gone_one_is_handed_back() {
        let row = Session { id: "c1".into(), ..Session::default() };
        let mut p = Pins::default();
        assert_eq!(verdict(&p, "c1"), "unpinned");
        p.pin(row.clone());
        assert_eq!(verdict(&p, "c1"), "withheld");
        check(&mut p, "c1", Recheck::Fresh(Box::new(row.clone())));
        assert_eq!(verdict(&p, "c1"), "verified");
        check(&mut p, "c1", Recheck::Unknown("offline".into()));
        assert_eq!(verdict(&p, "c1"), "withheld");
        assert_eq!(check(&mut p, "c1", Recheck::Gone), Some("c1".to_string()));
        assert_eq!(verdict(&p, "c1"), "unpinned");
        p.pin(row);
        check(&mut p, "c1", Recheck::Fresh(Box::new(Session { id: "c1".into(), ..Session::default() })));
        assert_eq!(verdict(&p, "c1"), "verified");
    }

    #[test]
    fn a_pin_read_from_the_session_itself_shows_until_it_is_gone() {
        let row = Session { id: "c1".into(), ..Session::default() };
        let mut p = Pins::default();
        p.pin_read(row.clone());
        assert_eq!(verdict(&p, "c1"), "verified");
        check(&mut p, "c1", Recheck::Unknown("Agenthail's session list hides this kind of session".into()));
        assert_eq!(verdict(&p, "c1"), "verified");
        p.pin(row.clone());
        assert_eq!(verdict(&p, "c1"), "withheld", "a search pin of the same session is checked again");
        check(&mut p, "c1", Recheck::Unknown("offline".into()));
        assert_eq!(verdict(&p, "c1"), "withheld");
        p.pin_read(row);
        assert_eq!(check(&mut p, "c1", Recheck::Gone), Some("c1".to_string()));
        assert_eq!(verdict(&p, "c1"), "unpinned");
    }
}
