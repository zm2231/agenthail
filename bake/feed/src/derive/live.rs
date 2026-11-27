use std::collections::{BTreeSet, HashMap, HashSet};
use std::time::Instant;

use anyhow::Result;
use serde_json::{Value, json};
use time::OffsetDateTime;

use crate::catalog::{Client, Session, parse_time};
use crate::menu::Patch;
use crate::derive::tails::{self, Tail, Tails};
use crate::derive::world::{Actor, Recorded, Rows, Work, World};
use crate::item::Item;

const LEDGER_WINDOW_H: f64 = 72.0;
const DONE_KEPT: usize = 40;
const RECENT_DAYS: i64 = 4;
pub const EXPECT_HOLD: std::time::Duration = std::time::Duration::from_secs(15);

pub struct Fetched {
    pub recorded: Recorded,
    pub ledgers: HashMap<String, Result<Vec<Work>, String>>,
    pub held: Vec<String>,
    pub asked: Vec<String>,
    pub started: Instant,
}

pub const RETRY: std::time::Duration = std::time::Duration::from_secs(60);

struct Held {
    read_at: Instant,
    retry: Option<Instant>,
}

#[derive(Debug, PartialEq)]
pub enum Change {
    Read(String),
    Overview,
    Nothing,
}

#[derive(Default)]
pub struct Known {
    ledgers: HashMap<String, Held>,
    keys: HashMap<String, String>,
    wanted: HashSet<String>,
    opened: HashSet<String>,
    changed: HashMap<String, Instant>,
    actors: HashMap<String, Option<Instant>>,
    candidates: BTreeSet<String>,
    sessions: Vec<Session>,
}

impl Known {
    pub fn learn(&mut self, fetched: &Fetched) -> bool {
        let key_of = |id: &str| fetched.recorded.projects.iter().find(|p| p.id == id && !p.ledger_key.is_empty()).map_or(id.to_string(), |p| p.ledger_key.clone());
        let mut again = false;
        for (id, result) in &fetched.ledgers {
            let key = key_of(id);
            self.keys.insert(key.clone(), id.clone());
            if [key.as_str(), id.as_str()].iter().any(|k| self.changed.get(*k).is_some_and(|at| *at > fetched.started)) {
                self.ledgers.remove(id);
                again = true;
                continue;
            }
            self.hold(id, fetched.started, result.as_ref().ok());
        }
        for p in &fetched.recorded.projects {
            self.keys.insert(if p.ledger_key.is_empty() { p.id.clone() } else { p.ledger_key.clone() }, p.id.clone());
        }
        self.wanted = fetched.ledgers.keys().chain(&fetched.held).cloned().collect();
        self.sessions = fetched.recorded.sessions.clone();
        self.asked(&fetched.asked, &fetched.recorded.actors, fetched.started);
        again
    }

    pub fn read(&mut self, id: &str, started: Instant, result: &Result<Ledger, String>) {
        self.opened.insert(id.to_string());
        self.hold(id, started, result.as_ref().ok().map(|l| &l.rows));
        if let Ok(l) = result {
            self.asked(&l.asked, &l.actors, started);
        }
    }

    fn hold(&mut self, id: &str, read_at: Instant, rows: Option<&Vec<Work>>) {
        self.ledgers.insert(id.to_string(), Held { read_at, retry: rows.is_none().then(|| read_at + RETRY) });
        self.candidates.extend(rows.into_iter().flatten().flat_map(|w| &w.timeline).map(|t| t.actor.clone()));
    }

    fn asked(&mut self, asked: &[String], found: &HashMap<String, Actor>, now: Instant) {
        for id in asked {
            self.actors.insert(id.clone(), (!found.contains_key(id)).then(|| now + RETRY));
        }
    }

    pub fn changed(&mut self, key: &str, at: Instant) -> Change {
        self.changed.insert(key.to_string(), at);
        let id = self.keys.get(key).map_or(key, String::as_str).to_string();
        if !self.wanted.contains(&id) && !self.opened.contains(&id) {
            return Change::Nothing;
        }
        match self.ledgers.get(&id) {
            None => Change::Overview,
            Some(h) if h.retry.is_some() || h.read_at < at => Change::Read(id),
            Some(_) => Change::Nothing,
        }
    }

    fn opened_retries(&self) -> impl Iterator<Item = (&String, Instant)> {
        self.opened.iter().filter(|id| !self.wanted.contains(*id)).filter_map(|id| Some((id, self.ledgers.get(id)?.retry?)))
    }

    pub fn open(&mut self, id: &str) -> bool {
        self.opened.insert(id.to_string());
        !self.ledgers.contains_key(id)
    }

    pub fn carry(&mut self) -> Known {
        Known { opened: std::mem::take(&mut self.opened), ..Known::default() }
    }

    pub fn opened(&self) -> Vec<String> {
        self.opened.iter().filter(|id| !self.wanted.contains(*id)).cloned().collect()
    }

    pub fn next_retry(&self) -> Option<Instant> {
        self.opened_retries().map(|(_, at)| at).min()
    }

    pub fn retries_due(&self, now: Instant) -> Vec<String> {
        self.opened_retries().filter(|(_, at)| *at <= now).map(|(id, _)| id.clone()).collect()
    }

    pub fn sessions(&self) -> &[Session] {
        &self.sessions
    }

    pub fn skips_ledger(&self, key: &str, settled: Option<Instant>) -> bool {
        match self.ledgers.get(key) {
            Some(Held { retry: Some(until), .. }) => Instant::now() < *until,
            Some(Held { read_at, .. }) => settled.is_some_and(|at| *read_at >= at),
            None => false,
        }
    }

    pub fn skips_actor(&self, id: &str) -> bool {
        match self.actors.get(id) {
            Some(None) => true,
            Some(Some(until)) => Instant::now() < *until,
            None => false,
        }
    }
}

pub struct Ledger {
    pub rows: Vec<Work>,
    pub actors: HashMap<String, Actor>,
    pub asked: Vec<String>,
}

pub fn fetch(client: &Client, known: &Known, settled: Option<Instant>) -> Result<Fetched> {
    let started = Instant::now();
    let snap = client.get_json("/api/v1/snapshot")?;
    let now = snap["updatedAt"].as_str().and_then(parse_time).unwrap_or_else(OffsetDateTime::now_utc);
    let sessions: Vec<Session> = serde_json::from_value(snap["sessions"].clone()).unwrap_or_default();
    let recent: BTreeSet<String> = sessions.iter().filter(|s| hours(now, &s.last_active) < LEDGER_WINDOW_H).map(|s| s.project_key()).collect();
    let (held, keys): (Vec<String>, Vec<String>) = snap["projects"].as_array().into_iter().flatten().filter(|p| p["archived"] != json!(true)).filter_map(|p| p["id"].as_str()).filter(|k| recent.contains(*k)).map(String::from).partition(|k| known.skips_ledger(k, settled));
    let ledgers: HashMap<String, Result<Vec<Work>, String>> = std::thread::scope(|scope| {
        let jobs: Vec<_> = keys.iter().map(|k| (k.clone(), scope.spawn(|| rows(client, k, now)))).collect();
        jobs.into_iter().map(|(k, h)| (k, h.join().unwrap_or_else(|_| Err("the fetch stopped".into())))).collect()
    });
    let usage: Vec<Value> = client
        .get_json("/api/v1/usage")
        .ok()
        .and_then(|u| u["sets"].as_array().cloned())
        .into_iter()
        .flatten()
        .flat_map(|st| {
            st["meters"].as_array().cloned().unwrap_or_default().into_iter().map(move |m| json!({"provider": m["provider"], "label": m["label"], "percent": m["percent"], "resetsAt": m["resetsAt"], "name": st["name"]}))
        })
        .collect();
    let hosts: Vec<Value> = client
        .get_json("/api/v1/hosts")
        .ok()
        .map(|hv| std::iter::once(json!({"name": hv["host"]["name"], "state": hv["host"]["state"]})).chain(hv["hosts"].as_array().cloned().unwrap_or_default().into_iter().map(|x| json!({"name": x["name"], "state": x["state"]}))).collect())
        .unwrap_or_default();
    let raw = json!({"updatedAt": snap["updatedAt"], "hostEpoch": snap["hostEpoch"], "catalogSeq": snap["catalogSeq"], "projects": snap["projects"], "usage": usage, "hosts": hosts});
    let mut recorded: Recorded = serde_json::from_value(raw)?;
    let pool: BTreeSet<String> = ledgers.values().filter_map(|r| r.as_ref().ok()).flatten().flat_map(|w| &w.timeline).map(|t| t.actor.clone()).chain(known.candidates.iter().cloned()).collect();
    let (found, asked) = actors(client, &sessions, |a| known.skips_actor(a), pool);
    recorded.actors = found;
    recorded.sessions = sessions;
    Ok(Fetched { recorded, ledgers, held, asked, started })
}

pub fn ledger(client: &Client, project: &str, known: &Known) -> Result<Ledger, String> {
    let rows = rows(client, project, OffsetDateTime::now_utc())?;
    let (actors, asked) = actors(client, known.sessions(), |a| known.skips_actor(a), rows.iter().flat_map(|w| &w.timeline).map(|t| t.actor.clone()).collect());
    Ok(Ledger { rows, actors, asked })
}

fn hours(now: OffsetDateTime, at: &str) -> f64 {
    parse_time(at).map(|t| (now - t).as_seconds_f64() / 3600.0).unwrap_or(1e9)
}

fn rows(client: &Client, project: &str, now: OffsetDateTime) -> Result<Vec<Work>, String> {
    let rows = client.ledger_rows(project).map_err(|e| e.to_string())?;
    trim(rows, now - time::Duration::days(RECENT_DAYS)).into_iter().map(|r| serde_json::from_value(r).map_err(|e| e.to_string())).collect()
}

fn trim(rows: Vec<Value>, cutoff: OffsetDateTime) -> Vec<Value> {
    let last_at = |r: &Value| r["timeline"].as_array().and_then(|t| t.last()).and_then(|e| e["at"].as_str()).map(String::from).unwrap_or_default();
    let recent = |r: &Value| parse_time(&last_at(r)).is_some_and(|t| t >= cutoff);
    let mut done: Vec<&Value> = rows.iter().filter(|r| r["state"] == "done").collect();
    done.sort_by_key(|r| last_at(r));
    let kept: BTreeSet<String> = done.iter().rev().take(DONE_KEPT).filter_map(|r| r["id"].as_str().map(String::from)).collect();
    rows.into_iter().filter(|r| !matches!(r["state"].as_str(), Some("done" | "deleted")) || r["id"].as_str().is_some_and(|id| kept.contains(id)) || recent(r)).collect()
}

fn actors(client: &Client, sessions: &[Session], skip: impl Fn(&str) -> bool, pool: BTreeSet<String>) -> (HashMap<String, Actor>, Vec<String>) {
    let listed: BTreeSet<&str> = sessions.iter().map(|s| s.id.as_str()).collect();
    let wanted: BTreeSet<String> = pool.into_iter().filter(|a| !a.is_empty() && !listed.contains(a.as_str()) && !skip(a) && !a.contains(' ') && !a.starts_with("td:")).collect();
    let found = std::thread::scope(|scope| {
        let jobs: Vec<_> = wanted.iter().map(|a| (a.clone(), scope.spawn(|| client.get_json(&format!("/api/v1/session?id={}", crate::catalog::enc(a)))))).collect();
        jobs.into_iter()
            .filter_map(|(a, h)| {
                let v = h.join().ok()?.ok()?;
                let x = &v["session"];
                Some((a, Actor { alias: v["alias"].as_str().map(String::from), name: x["name"].as_str().map(String::from), surface: x["surface"].as_str().map(String::from) }))
            })
            .collect()
    });
    (found, wanted.into_iter().collect())
}

// Sessions only move forward in time, a project's rows survive a failed fetch,
// and neither rows nor actors from a fetch replace ones from a fetch that started after it.
#[derive(Default)]
pub struct Overview {
    base: Recorded,
    ledgers: HashMap<String, Vec<Work>>,
    ledger_at: HashMap<String, Instant>,
    actor_at: HashMap<String, Instant>,
    stale: bool,
    failed: HashSet<String>,
    covered: HashSet<String>,
    pending: HashSet<String>,
    tails: Tails,
    expected: Vec<Expected>,
}

struct Expected {
    id: String,
    patch: Patch,
    at: Instant,
    daemon: Session,
}

fn newer(at: &str, than: &str) -> bool {
    match (parse_time(at), parse_time(than)) {
        (Some(a), Some(b)) => a > b,
        (Some(_), None) => true,
        (None, _) => false,
    }
}

fn later(at: &str, epoch: &str, seq: u64, base: &Recorded, follows_catalog: bool) -> bool {
    if epoch.is_empty() || base.host_epoch.is_empty() {
        return newer(at, &base.updated_at);
    }
    if epoch != base.host_epoch {
        return follows_catalog;
    }
    seq > base.catalog_seq || seq == base.catalog_seq && newer(at, &base.updated_at)
}

impl Overview {
    pub fn recorded(mut raw: Recorded) -> Overview {
        let ledgers = std::mem::take(&mut raw.ledgers);
        let covered = raw.projects.iter().map(|p| p.id.clone()).collect();
        Overview { base: raw, ledgers, covered, ..Overview::default() }
    }

    pub fn sessions(&self) -> &[Session] {
        &self.base.sessions
    }

    pub fn take_sessions(&mut self, at: &str, epoch: &str, seq: u64, sessions: Vec<Session>) -> bool {
        if !later(at, epoch, seq, &self.base, true) {
            return false;
        }
        self.base.updated_at = at.to_string();
        self.base.host_epoch = epoch.to_string();
        self.base.catalog_seq = seq;
        self.base.sessions = sessions;
        self.reconcile();
        self.stale = true;
        true
    }

    pub fn stale(&self) -> bool {
        self.stale
    }

    /// The daemon can publish a row read before a confirmed change; hold it until shown.
    pub fn patch(&mut self, id: &str, mut patch: Patch, at: Instant) -> bool {
        let Some(x) = self.base.sessions.iter_mut().find(|x| x.id == id) else { return false };
        let held = self.expected.iter().position(|e| e.id == id && e.patch.same_field(&patch));
        let held = held.map(|i| self.expected.remove(i));
        if let Some(e) = &held {
            patch.inherit(&e.patch);
        }
        let daemon = match held {
            Some(e) if e.patch.same_target(&patch) || !patch.shown(x) => e.daemon,
            Some(_) => return false,
            None if patch.shown(x) => return false,
            None => x.clone(),
        };
        if at.elapsed() >= EXPECT_HOLD {
            return false;
        }
        patch.apply(x);
        self.expected.push(Expected { id: id.to_string(), patch, at, daemon });
        self.stale = true;
        true
    }

    fn reconcile(&mut self) {
        let sessions = &mut self.base.sessions;
        self.expected.retain_mut(|e| {
            let Some(x) = sessions.iter_mut().find(|x| x.id == e.id) else { return false };
            if e.patch.shown(x) || e.at.elapsed() >= EXPECT_HOLD {
                return false;
            }
            e.daemon = x.clone();
            e.patch.apply(x);
            true
        });
    }

    pub fn expire(&mut self) -> bool {
        let mut changed = false;
        let sessions = &mut self.base.sessions;
        self.expected.retain(|e| {
            if e.at.elapsed() < EXPECT_HOLD {
                return true;
            }
            if let Some(x) = sessions.iter_mut().find(|x| x.id == e.id) {
                e.patch.restore(x, &e.daemon);
                changed = true;
            }
            false
        });
        self.stale |= changed;
        changed
    }

    pub fn refresh(&mut self, fetched: Fetched) {
        let Fetched { mut recorded, ledgers, held, started, .. } = fetched;
        let fresh = later(&recorded.updated_at, &recorded.host_epoch, recorded.catalog_seq, &self.base, false);
        if !fresh {
            recorded.updated_at = std::mem::take(&mut self.base.updated_at);
            recorded.host_epoch = std::mem::take(&mut self.base.host_epoch);
            recorded.catalog_seq = self.base.catalog_seq;
            recorded.sessions = std::mem::take(&mut self.base.sessions);
        }
        let actors = std::mem::replace(&mut recorded.actors, std::mem::take(&mut self.base.actors));
        recorded.transcripts = std::mem::take(&mut self.base.transcripts);
        self.base = recorded;
        if fresh {
            self.reconcile();
        }
        self.stale = true;
        self.take_actors(started, actors);
        let opened: Vec<String> = self.covered.iter().filter(|k| self.ledgers.contains_key(*k)).cloned().collect();
        self.covered = ledgers.keys().cloned().chain(held).chain(opened).collect();
        for (key, result) in ledgers {
            self.apply(key, started, result);
        }
        let listed: HashSet<&str> = self.base.projects.iter().filter(|p| !p.archived).map(|p| p.id.as_str()).collect();
        self.ledgers.retain(|k, _| listed.contains(k.as_str()));
        self.failed.retain(|k| listed.contains(k.as_str()));
        self.ledger_at.retain(|k, _| listed.contains(k.as_str()));
    }

    pub fn wants(&mut self, project: &str) -> bool {
        if self.covered.contains(project) || self.pending.contains(project) {
            return false;
        }
        self.pending.insert(project.to_string());
        self.stale = true;
        true
    }

    pub fn take_ledger(&mut self, project: &str, started: Instant, result: Result<Ledger, String>) {
        self.pending.remove(project);
        if result.is_ok() {
            self.covered.insert(project.to_string());
        }
        self.stale = true;
        let rows = result.map(|l| {
            self.take_actors(started, l.actors);
            l.rows
        });
        self.apply(project.to_string(), started, rows);
    }

    fn take_actors(&mut self, started: Instant, actors: HashMap<String, Actor>) {
        for (id, actor) in actors {
            if self.actor_at.get(&id).is_some_and(|at| *at > started) {
                continue;
            }
            self.actor_at.insert(id.clone(), started);
            self.base.actors.insert(id, actor);
        }
    }

    fn apply(&mut self, key: String, started: Instant, result: Result<Vec<Work>, String>) {
        if self.ledger_at.get(&key).is_some_and(|at| *at > started) {
            return;
        }
        self.ledger_at.insert(key.clone(), started);
        match result {
            Ok(rows) => {
                self.failed.remove(&key);
                self.ledgers.insert(key, rows);
            }
            Err(_) => {
                self.failed.insert(key);
            }
        }
    }

    pub fn due_tails(&mut self, open: &HashSet<String>) -> Vec<(String, String)> {
        let now = parse_time(&self.base.updated_at).unwrap_or_else(OffsetDateTime::now_utc);
        self.tails.due(&self.base.sessions, now, open)
    }

    pub fn tail_retry_due(&self) -> bool {
        self.tails.retry_due()
    }

    pub fn take_tail(&mut self, session: &str, last_active: String, result: Result<Tail, String>) {
        self.tails.take(session, last_active, result);
        self.stale = true;
    }

    pub fn world(&mut self, panes: &[(&str, &[Item])]) -> World {
        self.stale = false;
        let mut raw = self.base.clone();
        raw.ledgers = self.ledgers.clone();
        let panes = panes.iter().filter(|(_, items)| !items.is_empty()).map(|(id, items)| (id.to_string(), tails::trim(items)));
        for (id, tail) in self.tails.held().map(|(id, tail)| (id.clone(), tail.clone())).chain(panes) {
            if tail.prompted_before {
                raw.prompted_before.insert(id.clone());
            } else {
                raw.prompted_before.remove(&id);
            }
            raw.transcripts.insert(id, tail.items);
        }
        let mut world = World::new(raw);
        for key in &self.failed {
            world.rows.insert(key.clone(), if self.ledgers.contains_key(key) { Rows::Stale } else { Rows::Unavailable });
        }
        for key in self.pending.iter().filter(|k| !self.ledgers.contains_key(*k) && !self.failed.contains(*k)) {
            world.rows.insert(key.clone(), Rows::Loading);
        }
        world
    }
}
