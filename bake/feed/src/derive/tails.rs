use std::collections::{HashMap, HashSet};
use std::io::BufRead;
use std::time::{Duration, Instant};

use time::OffsetDateTime;

use crate::catalog::{Client, Session, parse_time};
use crate::derive::hands_on::from_person;
use crate::item::Item;
use crate::store::Transcript;
use crate::wire::{Outcome, Rebuild, Sse, Wire};

pub const TAIL_ITEMS: usize = 160;
pub const READ_BUDGET: Duration = Duration::from_secs(15);
pub const IN_FLIGHT: usize = 4;
pub const TAIL_RETRY: Duration = Duration::from_secs(10);
const TAIL_HOURS: f64 = 24.0;
const TAIL_SESSIONS: usize = 14;
const LONG_TEXT: usize = 1200;
const LIVE: &str = ": live";

#[derive(Clone, Debug, Default)]
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

pub fn read(lines: impl Iterator<Item = std::io::Result<String>>) -> Result<Tail, String> {
    let (mut sse, mut rebuild, mut transcript) = (Sse::default(), Rebuild::default(), Transcript::default());
    for line in lines {
        let line = line.map_err(|e| e.to_string())?;
        if line == LIVE {
            return Ok(trim(transcript.items()));
        }
        let Some(json) = sse.line(&line) else { continue };
        let Ok(wire) = serde_json::from_str::<Wire>(&json) else { continue };
        match rebuild.apply(wire) {
            Outcome::Events(seq, events) => {
                transcript.apply(seq, events);
            }
            Outcome::Skip => {}
            Outcome::Resync => return Err("the replay did not line up".into()),
        }
    }
    Err("the stream ended before its replay finished".into())
}

pub fn fetch(client: &Client, session: &str, budget: Duration) -> Result<Tail, String> {
    read(client.session_stream(session, budget).map_err(|e| e.to_string())?.lines())
}

#[derive(Default)]
pub struct Tails {
    held: HashMap<String, (String, Tail)>,
    fetching: HashSet<String>,
    failed: HashMap<String, (String, Instant)>,
}

impl Tails {
    pub fn due(&mut self, sessions: &[Session], now: OffsetDateTime, open: &HashSet<String>) -> Vec<(String, String)> {
        let age = |s: &Session| parse_time(&s.last_active).map(|t| (now - t).as_seconds_f64() / 3600.0).unwrap_or(f64::MAX);
        let mut recent: Vec<&Session> = sessions.iter().filter(|s| !s.archived && age(s) < TAIL_HOURS).collect();
        recent.sort_by(|a, b| age(a).total_cmp(&age(b)));
        recent.truncate(TAIL_SESSIONS);
        let keep: HashSet<&str> = recent.iter().map(|s| s.id.as_str()).collect();
        self.held.retain(|id, _| keep.contains(id.as_str()));
        let now_i = Instant::now();
        self.failed.retain(|id, (_, until)| keep.contains(id.as_str()) && now_i < *until);
        let waiting = |s: &Session| self.failed.get(&s.id).is_some_and(|(at, _)| *at == s.last_active);
        let due: Vec<(String, String)> = recent
            .into_iter()
            .filter(|s| !open.contains(&s.id) && !self.fetching.contains(&s.id) && !waiting(s) && self.held.get(&s.id).is_none_or(|(at, _)| *at != s.last_active))
            .take(IN_FLIGHT.saturating_sub(self.fetching.len()))
            .map(|s| (s.id.clone(), s.last_active.clone()))
            .collect();
        self.fetching.extend(due.iter().map(|(id, _)| id.clone()));
        due
    }

    pub fn take(&mut self, session: &str, last_active: String, result: Result<Tail, String>) {
        self.fetching.remove(session);
        match result {
            Ok(tail) => {
                self.failed.remove(session);
                self.held.insert(session.to_string(), (last_active, tail));
            }
            Err(_) => {
                self.failed.insert(session.to_string(), (last_active, Instant::now() + TAIL_RETRY));
            }
        }
    }

    pub fn retry_due(&self) -> bool {
        let now = Instant::now();
        self.failed.values().any(|(_, until)| now >= *until)
    }

    pub fn held(&self) -> impl Iterator<Item = (&String, &Tail)> {
        self.held.iter().map(|(id, (_, tail))| (id, tail))
    }
}
