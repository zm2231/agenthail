use std::collections::{HashMap, HashSet};
use std::io::BufRead;
use std::time::{Duration, Instant};

use time::OffsetDateTime;

use crate::catalog::{Client, Session, parse_time};
use crate::derive::hands_on::from_person;
use crate::item::Item;
use crate::store::Transcript;
use crate::wire::{Event, Outcome, Rebuild, Sse, Wire};

pub const TAIL_ITEMS: usize = 160;
pub const READ_BUDGET: Duration = Duration::from_secs(15);
pub const IN_FLIGHT: usize = 4;
pub const TAIL_RETRY: Duration = Duration::from_secs(10);
const TAIL_HOURS: f64 = 24.0;
const TAIL_SESSIONS: usize = 14;
const LONG_TEXT: usize = 1200;
const LIVE: &str = ": live";

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Tail {
    pub items: Vec<Item>,
    pub prompted_before: bool,
}

pub fn trim(items: &[Item]) -> Tail {
    let start = items.len().saturating_sub(TAIL_ITEMS);
    let prompted_before = items[..start].iter().any(from_person);
    let items = items[start..]
        .iter()
        .map(|i| {
            let mut i = i.clone();
            if matches!(i.kind.as_str(), "toolResult" | "reasoning") && i.text.len() > LONG_TEXT {
                let mut end = LONG_TEXT;
                while !i.text.is_char_boundary(end) {
                    end -= 1;
                }
                i.text.truncate(end);
            }
            i
        })
        .collect();
    Tail { items, prompted_before }
}

const KEEP: usize = TAIL_ITEMS * 2;
const RESYNC: &str = "the replay did not line up";

#[derive(Default)]
pub struct Resume {
    seq: u64,
    rebuild: Rebuild,
    transcript: Transcript,
    prompted_before: bool,
    dropped: HashSet<String>,
}

impl Resume {
    fn settle(mut self) -> (Tail, Resume) {
        let mut tail = trim(self.transcript.items());
        tail.prompted_before |= self.prompted_before;
        let items = self.transcript.items();
        if items.len() > KEEP {
            let cut = items.len() - KEEP;
            self.prompted_before |= items[..cut].iter().any(from_person);
            self.dropped.extend(items[..cut].iter().map(|i| i.id.clone()));
            let kept = items[cut..].to_vec();
            self.rebuild.keep(&kept.iter().map(|i| i.id.clone()).collect());
            let mut transcript = Transcript::default();
            transcript.prepend(kept);
            transcript.seq = self.transcript.seq;
            transcript.turn_open = self.transcript.turn_open;
            self.transcript = transcript;
        }
        (tail, self)
    }

    fn reaches_back(&self, event: &Event) -> bool {
        match event {
            Event::Upsert(item) => self.dropped.contains(&item.id),
            Event::Remove(id) => self.dropped.contains(id),
            _ => false,
        }
    }
}

pub fn read(lines: impl Iterator<Item = std::io::Result<String>>) -> Result<Tail, String> {
    resume(lines, Resume::default()).map(|(tail, _)| tail)
}

pub fn resume(lines: impl Iterator<Item = std::io::Result<String>>, mut at: Resume) -> Result<(Tail, Resume), String> {
    let mut sse = Sse::default();
    for line in lines {
        let line = line.map_err(|e| e.to_string())?;
        if line == LIVE {
            if !at.dropped.is_empty() && at.transcript.items().len() < TAIL_ITEMS {
                return Err(RESYNC.into());
            }
            return Ok(at.settle());
        }
        let Some(json) = sse.line(&line) else { continue };
        let Ok(wire) = serde_json::from_str::<Wire>(&json) else { continue };
        at.seq = at.seq.max(wire.seq);
        match at.rebuild.apply(wire) {
            Outcome::Events(seq, events) => {
                if events.iter().any(|e| at.reaches_back(e)) {
                    return Err(RESYNC.into());
                }
                at.transcript.apply(seq, events);
            }
            Outcome::Skip => {}
            Outcome::Resync => return Err(RESYNC.into()),
        }
    }
    Err("the stream ended before its replay finished".into())
}

pub fn fetch(client: &Client, session: &str, budget: Duration, held: Option<Resume>) -> (Result<Tail, String>, Option<Resume>) {
    let from = |after: u64, at: Resume| client.session_stream(session, after, budget).map_err(|e| e.to_string()).and_then(|r| resume(r.lines(), at));
    let read = match held {
        Some(at) => {
            let after = at.seq;
            match from(after, at) {
                Err(e) if e != RESYNC && e != crate::catalog::STREAM_GAP => Err(e),
                Err(_) => from(0, Resume::default()),
                done => done,
            }
        }
        None => from(0, Resume::default()),
    };
    match read {
        Ok((tail, at)) => (Ok(tail), Some(at)),
        Err(e) => (Err(e), None),
    }
}

#[derive(Default)]
pub struct Tails {
    held: HashMap<String, (String, Tail)>,
    fetching: HashSet<String>,
    failed: HashMap<String, Instant>,
    resumes: HashMap<String, Resume>,
    followed: Option<String>,
}

impl Tails {
    pub fn due(&mut self, sessions: &[Session], now: OffsetDateTime, open: &HashSet<String>) -> Vec<(String, String)> {
        let age = |s: &Session| parse_time(&s.last_active).map(|t| (now - t).as_seconds_f64() / 3600.0).unwrap_or(f64::MAX);
        let mut recent: Vec<&Session> = sessions.iter().filter(|s| !s.archived && age(s) < TAIL_HOURS).collect();
        recent.sort_by(|a, b| age(a).total_cmp(&age(b)));
        recent.truncate(TAIL_SESSIONS);
        if let Some(x) = self.followed.as_deref().and_then(|id| sessions.iter().find(|s| s.id == id && !s.archived)) {
            if !recent.iter().any(|s| s.id == x.id) {
                recent.push(x);
            }
        }
        let keep: HashSet<&str> = recent.iter().map(|s| s.id.as_str()).collect();
        self.held.retain(|id, _| keep.contains(id.as_str()));
        self.resumes.retain(|id, _| keep.contains(id.as_str()));
        let now_i = Instant::now();
        self.failed.retain(|_, until| now_i < *until);
        let waiting = |s: &Session| self.failed.contains_key(&s.id);
        let due: Vec<(String, String)> = recent
            .into_iter()
            .filter(|s| !open.contains(&s.id) && !self.fetching.contains(&s.id) && !waiting(s) && self.held.get(&s.id).is_none_or(|(at, _)| *at != s.last_active))
            .take(IN_FLIGHT.saturating_sub(self.fetching.len()))
            .map(|s| (s.id.clone(), s.last_active.clone()))
            .collect();
        self.fetching.extend(due.iter().map(|(id, _)| id.clone()));
        due
    }

    pub fn resume(&mut self, session: &str) -> Option<Resume> {
        self.resumes.remove(session)
    }

    pub fn hold(&mut self, session: &str, at: Resume) {
        self.resumes.insert(session.to_string(), at);
    }

    pub fn follow(&mut self, session: Option<String>) -> bool {
        let session = session.filter(|id| !id.is_empty());
        let moved = self.followed != session;
        self.followed = session;
        moved
    }

    pub fn take(&mut self, session: &str, last_active: String, result: Result<Tail, String>) -> bool {
        self.fetching.remove(session);
        match result {
            Ok(tail) => {
                self.failed.remove(session);
                let same = self.held.get(session).is_some_and(|(_, held)| *held == tail);
                self.held.insert(session.to_string(), (last_active, tail));
                !same
            }
            Err(_) => {
                self.failed.insert(session.to_string(), Instant::now() + TAIL_RETRY);
                false
            }
        }
    }

    #[cfg(test)]
    pub fn failed_until_now(&mut self, session: &str) {
        self.failed.insert(session.to_string(), Instant::now());
    }

    pub fn retry_due(&self) -> bool {
        let now = Instant::now();
        self.failed.values().any(|until| now >= *until)
    }

    pub fn held(&self) -> impl Iterator<Item = (&String, &Tail)> {
        self.held.iter().map(|(id, (_, tail))| (id, tail))
    }
}
