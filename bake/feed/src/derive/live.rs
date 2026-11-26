use std::collections::{BTreeSet, HashMap, HashSet};

use anyhow::Result;
use serde_json::{Value, json};
use time::OffsetDateTime;

use crate::catalog::{Client, Session, parse_time};
use crate::derive::world::{Actor, Recorded, Rows, Work, World};

const LEDGER_WINDOW_H: f64 = 72.0;
const DONE_KEPT: usize = 40;
const RECENT_DAYS: i64 = 4;

pub struct Fetched {
    pub recorded: Recorded,
    pub ledgers: HashMap<String, Result<Vec<Work>, String>>,
}

pub struct Ledger {
    pub rows: Vec<Work>,
    pub actors: HashMap<String, Actor>,
}

pub fn fetch(client: &Client) -> Result<Fetched> {
    let snap = client.get_json("/api/v1/snapshot")?;
    let now = snap["updatedAt"].as_str().and_then(parse_time).unwrap_or_else(OffsetDateTime::now_utc);
    let sessions: Vec<Session> = serde_json::from_value(snap["sessions"].clone()).unwrap_or_default();
    let recent: BTreeSet<String> = sessions.iter().filter(|s| hours(now, &s.last_active) < LEDGER_WINDOW_H).map(|s| s.project_key()).collect();
    let keys: Vec<String> = snap["projects"].as_array().into_iter().flatten().filter(|p| p["archived"] != json!(true)).filter_map(|p| p["id"].as_str()).filter(|k| recent.contains(*k)).map(String::from).collect();
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
    let raw = json!({"updatedAt": snap["updatedAt"], "projects": snap["projects"], "usage": usage, "hosts": hosts});
    let mut recorded: Recorded = serde_json::from_value(raw)?;
    recorded.actors = actors(client, &sessions, ledgers.values().filter_map(|r| r.as_ref().ok()).flatten());
    recorded.sessions = sessions;
    Ok(Fetched { recorded, ledgers })
}

pub fn ledger(client: &Client, project: &str, sessions: &[Session]) -> Result<Ledger, String> {
    let rows = rows(client, project, OffsetDateTime::now_utc())?;
    let actors = actors(client, sessions, rows.iter());
    Ok(Ledger { rows, actors })
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

fn actors<'a>(client: &Client, sessions: &[Session], rows: impl Iterator<Item = &'a Work>) -> HashMap<String, Actor> {
    let known: BTreeSet<&str> = sessions.iter().map(|s| s.id.as_str()).collect();
    let wanted: BTreeSet<String> = rows.flat_map(|w| &w.timeline).map(|t| t.actor.clone()).filter(|a| !a.is_empty() && !known.contains(a.as_str()) && !a.contains(' ') && !a.starts_with("td:")).collect();
    std::thread::scope(|scope| {
        let jobs: Vec<_> = wanted.iter().map(|a| (a.clone(), scope.spawn(|| client.get_json(&format!("/api/v1/session?id={}", crate::catalog::enc(a)))))).collect();
        jobs.into_iter()
            .filter_map(|(a, h)| {
                let v = h.join().ok()?.ok()?;
                let x = &v["session"];
                Some((a, Actor { alias: v["alias"].as_str().map(String::from), name: x["name"].as_str().map(String::from), surface: x["surface"].as_str().map(String::from) }))
            })
            .collect()
    })
}

// Sessions only move forward in time, and a project's rows survive a failed fetch.
#[derive(Default)]
pub struct Overview {
    base: Recorded,
    ledgers: HashMap<String, Vec<Work>>,
    failed: HashSet<String>,
    covered: HashSet<String>,
    pending: HashSet<String>,
}

fn newer(at: &str, than: &str) -> bool {
    match (parse_time(at), parse_time(than)) {
        (Some(a), Some(b)) => a > b,
        (_, None) => true,
        (None, Some(_)) => false,
    }
}

impl Overview {
    pub fn recorded(mut raw: Recorded) -> Overview {
        let ledgers = std::mem::take(&mut raw.ledgers);
        let covered = raw.projects.iter().map(|p| p.id.clone()).collect();
        Overview { base: raw, ledgers, failed: HashSet::new(), covered, pending: HashSet::new() }
    }

    pub fn sessions(&self) -> &[Session] {
        &self.base.sessions
    }

    pub fn take_sessions(&mut self, at: &str, sessions: Vec<Session>) -> bool {
        if !newer(at, &self.base.updated_at) {
            return false;
        }
        self.base.updated_at = at.to_string();
        self.base.sessions = sessions;
        true
    }

    pub fn refresh(&mut self, fetched: Fetched) {
        let Fetched { mut recorded, ledgers } = fetched;
        if newer(&self.base.updated_at, &recorded.updated_at) {
            recorded.updated_at = std::mem::take(&mut self.base.updated_at);
            recorded.sessions = std::mem::take(&mut self.base.sessions);
        }
        let mut actors = std::mem::take(&mut self.base.actors);
        actors.extend(std::mem::take(&mut recorded.actors));
        recorded.actors = actors;
        recorded.transcripts = std::mem::take(&mut self.base.transcripts);
        self.base = recorded;
        self.covered = ledgers.keys().cloned().collect();
        for (key, result) in ledgers {
            self.apply(key, result);
        }
        let listed: HashSet<&str> = self.base.projects.iter().filter(|p| !p.archived).map(|p| p.id.as_str()).collect();
        self.ledgers.retain(|k, _| listed.contains(k.as_str()));
        self.failed.retain(|k| listed.contains(k.as_str()));
    }

    pub fn wants(&mut self, project: &str) -> bool {
        if self.covered.contains(project) || self.pending.contains(project) {
            return false;
        }
        self.pending.insert(project.to_string());
        true
    }

    pub fn take_ledger(&mut self, project: &str, result: Result<Ledger, String>) {
        self.pending.remove(project);
        let rows = result.map(|l| {
            self.base.actors.extend(l.actors);
            l.rows
        });
        self.apply(project.to_string(), rows);
    }

    fn apply(&mut self, key: String, result: Result<Vec<Work>, String>) {
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

    pub fn world(&self) -> World {
        let mut raw = self.base.clone();
        raw.ledgers = self.ledgers.clone();
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
