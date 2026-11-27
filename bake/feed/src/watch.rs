use std::collections::{HashMap, VecDeque};
use std::io::BufRead;
use std::time::Duration;

use anyhow::{Result, anyhow};
use serde_json::Value;

use crate::catalog::{Client, Session, enc};

pub const SAFETY: Duration = Duration::from_secs(30);
pub const OVERVIEW_EVERY: Duration = Duration::from_secs(120);
pub const SETTLE: Duration = Duration::from_millis(180);
pub const OVERVIEW_GAP: Duration = Duration::from_secs(5);
pub const DECISIONS_GAP: Duration = Duration::from_secs(10);
pub const LEDGERS_FIRST: Duration = Duration::from_secs(5);
pub const LEDGERS_SETTLE: Duration = Duration::from_secs(1);
const KEEP: usize = 2000;
const IDLE: Duration = Duration::from_secs(120);

#[derive(Debug, Clone, PartialEq)]
pub struct Event {
    pub id: u64,
    pub kind: String,
    pub data: Value,
}

pub fn read(reader: impl BufRead, mut on: impl FnMut(Event) -> bool) -> std::io::Result<()> {
    let (mut kind, mut id, mut data) = (String::new(), 0u64, String::new());
    for line in reader.lines() {
        let line = line?;
        if let Some(v) = line.strip_prefix("event:") {
            kind = v.trim().to_string();
        } else if let Some(v) = line.strip_prefix("id:") {
            id = v.trim().parse().unwrap_or(id);
        } else if let Some(v) = line.strip_prefix("data:") {
            if !data.is_empty() {
                data.push('\n');
            }
            data.push_str(v.strip_prefix(' ').unwrap_or(v));
        } else if line.is_empty() {
            if !kind.is_empty() && !on(Event { id, kind: std::mem::take(&mut kind), data: serde_json::from_str(&data).unwrap_or(Value::Null) }) {
                return Ok(());
            }
            kind.clear();
            data.clear();
        }
    }
    Ok(())
}

#[derive(Debug, Default, Clone, PartialEq)]
pub struct Wants {
    pub snapshot: bool,
    pub overview: bool,
    pub hosts: bool,
    pub extensions: bool,
    pub reset: bool,
    pub session: Option<String>,
}

impl Wants {
    pub fn merge(&mut self, other: Wants) {
        self.snapshot |= other.snapshot;
        self.overview |= other.overview;
        self.hosts |= other.hosts;
        self.extensions |= other.extensions;
        self.reset |= other.reset;
        if other.session.is_some() {
            self.session = other.session;
        }
    }
}

pub fn wants(event: &Event) -> Wants {
    let kind = event.kind.as_str();
    let reset = kind == "stream.reset";
    let hosts = matches!(kind, "host.paired" | "host.removed" | "host.state");
    let snapshot = reset || hosts || matches!(kind, "state.changed" | "settings.updated" | "signin.changed");
    let extensions = reset || hosts || matches!(kind, "extension.changed" | "extension.badge");
    let session = event.data["entityId"].as_str().filter(|e| !e.is_empty()).map(String::from);
    Wants { snapshot, overview: snapshot, hosts, extensions, reset, session }
}

#[derive(Debug, Clone, PartialEq)]
pub struct CatalogEvent {
    pub seq: u64,
    pub kind: String,
    pub data: Value,
}

impl CatalogEvent {
    pub fn of(event: &Event) -> Option<CatalogEvent> {
        let d = &event.data;
        if d["stream"] != "catalog" {
            return None;
        }
        Some(CatalogEvent { seq: d["seq"].as_u64().unwrap_or(event.id), kind: d["type"].as_str().unwrap_or(&event.kind).to_string(), data: d["data"].clone() })
    }
}

#[derive(Debug, Default, PartialEq)]
pub struct Applied {
    pub changed: bool,
    pub reread: bool,
    pub news: bool,
    pub rev: Option<String>,
    pub touched: Vec<String>,
    pub restart: bool,
    pub removed: Vec<Removal>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Removal {
    pub id: String,
    pub unlisted: bool,
}

#[derive(Default)]
pub struct Catalog {
    sessions: Vec<Session>,
    epoch: String,
    seq: u64,
    adopted: bool,
    recent: VecDeque<CatalogEvent>,
}

impl Catalog {
    pub fn sessions(&self) -> &[Session] {
        &self.sessions
    }

    pub fn seq(&self) -> u64 {
        self.seq
    }

    pub fn epoch(&self) -> &str {
        &self.epoch
    }

    pub fn forget(&mut self) {
        self.recent.clear();
    }

    pub fn adopt(&mut self, sessions: Vec<Session>, epoch: &str, seq: u64) -> Applied {
        let restart = epoch != self.epoch && !self.epoch.is_empty();
        if restart {
            self.recent.clear();
        }
        self.epoch = epoch.to_string();
        self.sessions = sessions;
        self.seq = seq;
        self.adopted = true;
        self.recent.retain(|e| e.seq > seq);
        let later: Vec<CatalogEvent> = self.recent.iter().cloned().collect();
        let mut out = Applied::default();
        for e in later {
            self.step(e, &mut out);
        }
        out.changed = true;
        out.restart = restart;
        out
    }

    pub fn apply(&mut self, events: Vec<CatalogEvent>) -> Applied {
        let mut out = Applied::default();
        for e in events {
            if self.recent.iter().any(|r| r.seq == e.seq) {
                continue;
            }
            self.recent.push_back(e.clone());
            if self.recent.len() > KEEP {
                self.recent.pop_front();
                out.reread = true;
            }
            if self.adopted {
                self.step(e, &mut out);
            }
        }
        out
    }

    fn step(&mut self, e: CatalogEvent, out: &mut Applied) {
        if e.seq <= self.seq {
            return;
        }
        self.seq = e.seq;
        let d = &e.data;
        let id = d["sessionId"].as_str().unwrap_or_default().to_string();
        match e.kind.as_str() {
            "session.upserted" => match serde_json::from_value::<Session>(d["session"].clone()) {
                Ok(row) if !row.id.is_empty() => {
                    out.touched.push(row.id.clone());
                    match self.sessions.iter_mut().find(|x| x.id == row.id) {
                        Some(x) => *x = row,
                        None => {
                            self.sessions.push(row);
                            out.news = true;
                        }
                    }
                    out.changed = true;
                }
                _ => out.reread = true,
            },
            "session.queue" => {
                if let (Some(x), Some(n)) = (self.sessions.iter_mut().find(|x| x.id == id), d["queueCount"].as_i64()) {
                    x.queue_count = n;
                    out.changed = true;
                    out.touched.push(id);
                }
                out.reread = true;
            }
            "session.removed" => {
                if id.is_empty() {
                    out.reread = true;
                    return;
                }
                let before = self.sessions.len();
                self.sessions.retain(|x| x.id != id);
                out.changed |= self.sessions.len() != before;
                out.news = true;
                out.reread |= d["rekeyedTo"].as_str().is_some_and(|r| !r.is_empty());
                out.removed.push(Removal { id: id.clone(), unlisted: d["reason"] == "unlisted" });
                out.touched.push(id);
            }
            "decisions.changed" => out.rev = d["rev"].as_str().map(String::from).or(out.rev.take()),
            kind if kind.starts_with("coordinator.") => out.news = true,
            _ => {}
        }
    }
}

#[derive(Default)]
pub struct Ledgers {
    versions: HashMap<String, String>,
}

impl Ledgers {
    pub fn changed(&mut self, event: &Event, settled: bool) -> Option<String> {
        let key = event.data["projectKey"].as_str()?.to_string();
        let version = event.data["version"].as_str().unwrap_or_default().to_string();
        match self.versions.insert(key.clone(), version.clone()) {
            Some(was) if was == version => None,
            None if !settled => None,
            _ => Some(key),
        }
    }
}

#[derive(Clone, Default)]
pub struct Stream(std::sync::Arc<std::sync::Mutex<Option<std::time::Instant>>>);

impl Stream {
    pub fn open(&self) {
        *self.0.lock().unwrap_or_else(|e| e.into_inner()) = Some(std::time::Instant::now() + LEDGERS_SETTLE);
    }

    pub fn close(&self) {
        *self.0.lock().unwrap_or_else(|e| e.into_inner()) = None;
    }

    pub fn settles_at(&self) -> Option<std::time::Instant> {
        *self.0.lock().unwrap_or_else(|e| e.into_inner())
    }

    pub fn settled(&self) -> bool {
        self.settles_at().is_some_and(|at| std::time::Instant::now() >= at)
    }
}

pub enum Opened {
    Stream,
    Gap,
}

impl Client {
    fn stream(&self, path: &str, after: Option<u64>) -> std::result::Result<impl BufRead + use<>, (u16, String)> {
        let agent = ureq::AgentBuilder::new().timeout_connect(Duration::from_secs(5)).timeout_read(IDLE).build();
        let mut request = agent.get(&format!("{}{path}", self.base())).set("Authorization", &self.bearer()).set("Accept", "text/event-stream");
        if let Some(after) = after {
            request = request.set("Last-Event-ID", &after.to_string());
        }
        match request.call() {
            Ok(response) => Ok(std::io::BufReader::new(response.into_reader())),
            Err(ureq::Error::Status(code, response)) => {
                let body: Value = response.into_json().unwrap_or(Value::Null);
                Err((code, body["error"]["code"].as_str().unwrap_or_default().to_string()))
            }
            Err(e) => Err((0, e.to_string())),
        }
    }

    pub fn follow(&self, after: u64, opened: impl FnOnce(), on: impl FnMut(Event) -> bool) -> Result<()> {
        let reader = self.stream(&format!("/api/v1/events?after={after}"), Some(after)).map_err(|(_, e)| anyhow!(e))?;
        opened();
        Ok(read(reader, on)?)
    }

    pub fn follow_catalog(&self, after: u64, on: impl FnMut(Event) -> bool) -> Result<Opened> {
        let reader = match self.stream(&format!("/api/v1/catalog-events?after={}", enc(&after.to_string())), Some(after)) {
            Err((409, _)) => return Ok(Opened::Gap),
            Err((_, e)) => return Err(anyhow!(e)),
            Ok(r) => r,
        };
        read(reader, on)?;
        Ok(Opened::Stream)
    }

    pub fn follow_ledgers(&self, opened: impl FnOnce(), on: impl FnMut(Event) -> bool) -> Result<()> {
        let reader = self.stream("/api/v1/ledger/events", None).map_err(|(_, e)| anyhow!(e))?;
        opened();
        Ok(read(reader, on)?)
    }
}

#[derive(Default)]
pub struct Pace {
    last: Option<std::time::Instant>,
    soon: bool,
}

impl Pace {
    pub fn wait(&self, now: std::time::Instant) -> Option<Duration> {
        let last = self.last?;
        self.soon.then(|| (last + DECISIONS_GAP).saturating_duration_since(now))
    }

    pub fn ask(&mut self, now: bool, at: std::time::Instant) -> bool {
        self.soon = true;
        now || self.last.is_none_or(|last| at >= last + DECISIONS_GAP)
    }

    pub fn read(&mut self, at: std::time::Instant) {
        self.last = Some(at);
        self.soon = false;
    }
}

pub fn stamp() -> String {
    time::OffsetDateTime::now_utc().format(&time::format_description::well_known::Rfc3339).unwrap_or_default()
}

pub struct Reconnect {
    delay: u64,
}

impl Default for Reconnect {
    fn default() -> Reconnect {
        Reconnect { delay: 1 }
    }
}

impl Reconnect {
    pub fn next(&mut self) -> Duration {
        let now = self.delay;
        self.delay = (self.delay * 2).min(20);
        Duration::from_secs(now)
    }

    pub fn reset(&mut self) {
        self.delay = 1;
    }
}

pub fn backoff(failures: u32) -> Duration {
    Duration::from_secs(1u64 << failures.min(3)).min(Duration::from_secs(5))
}

#[cfg(test)]
#[path = "watch_tests.rs"]
mod tests;
