use std::collections::{HashMap, HashSet};
use std::io::BufRead;
use std::time::{Duration, Instant};

use time::OffsetDateTime;

use crate::catalog::{Client, Session, parse_time};
use crate::item::Item;
use crate::store::Transcript;
use crate::wire::{Outcome, Rebuild, Sse, Wire};

pub const TAIL_ITEMS: usize = 160;
const TAIL_HOURS: f64 = 24.0;
const TAIL_SESSIONS: usize = 14;
const LONG_TEXT: usize = 1200;
const READ_BUDGET: Duration = Duration::from_secs(15);

pub fn trim(items: &[Item]) -> Vec<Item> {
    items[items.len().saturating_sub(TAIL_ITEMS)..]
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
        .collect()
}

pub fn read(lines: impl Iterator<Item = String>) -> Vec<Item> {
    let (mut sse, mut rebuild, mut transcript) = (Sse::default(), Rebuild::default(), Transcript::default());
    for line in lines {
        let Some(json) = sse.line(&line) else { continue };
        let Ok(wire) = serde_json::from_str::<Wire>(&json) else { continue };
        match rebuild.apply(wire) {
            Outcome::Events(seq, events) => {
                transcript.apply(seq, events);
            }
            Outcome::Skip => {}
            Outcome::Resync => break,
        }
    }
    trim(transcript.items())
}

pub fn fetch(client: &Client, session: &str) -> Result<Vec<Item>, String> {
    let reader = client.session_stream(session).map_err(|e| e.to_string())?;
    let deadline = Instant::now() + READ_BUDGET;
    Ok(read(reader.lines().map_while(Result::ok).take_while(|_| Instant::now() < deadline)))
}

#[derive(Default)]
pub struct Tails {
    held: HashMap<String, (String, Vec<Item>)>,
    fetching: HashSet<String>,
}

impl Tails {
    pub fn due(&mut self, sessions: &[Session], now: OffsetDateTime, open: &HashSet<String>) -> Vec<(String, String)> {
        let age = |s: &Session| parse_time(&s.last_active).map(|t| (now - t).as_seconds_f64() / 3600.0).unwrap_or(f64::MAX);
        let mut recent: Vec<&Session> = sessions.iter().filter(|s| !s.archived && age(s) < TAIL_HOURS).collect();
        recent.sort_by(|a, b| age(a).total_cmp(&age(b)));
        recent.truncate(TAIL_SESSIONS);
        let keep: HashSet<&str> = recent.iter().map(|s| s.id.as_str()).collect();
        self.held.retain(|id, _| keep.contains(id.as_str()));
        let due: Vec<(String, String)> = recent
            .into_iter()
            .filter(|s| !open.contains(&s.id) && !self.fetching.contains(&s.id) && self.held.get(&s.id).is_none_or(|(at, _)| *at != s.last_active))
            .map(|s| (s.id.clone(), s.last_active.clone()))
            .collect();
        self.fetching.extend(due.iter().map(|(id, _)| id.clone()));
        due
    }

    pub fn take(&mut self, session: &str, last_active: String, result: Result<Vec<Item>, String>) {
        self.fetching.remove(session);
        if let Some(items) = result.ok().filter(|items| !items.is_empty()) {
            self.held.insert(session.to_string(), (last_active, items));
        }
    }

    pub fn items(&self) -> impl Iterator<Item = (&String, &Vec<Item>)> {
        self.held.iter().map(|(id, (_, items))| (id, items))
    }
}
