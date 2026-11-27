use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use serde::Deserialize;
use time::OffsetDateTime;

use crate::catalog::{Session, parse_time};
use crate::Item;

#[derive(Deserialize, Clone, Default, Debug, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Entry {
    #[serde(default)]
    pub at: String,
    #[serde(default)]
    pub actor: String,
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub state: Option<String>,
    #[serde(default)]
    pub text: Option<String>,
    #[serde(default)]
    pub log_type: Option<String>,
    #[serde(skip)]
    pub(crate) ci_failed: Lazy<bool>,
}

impl Entry {
    pub fn reports_ci_failure(&self) -> bool {
        *self.ci_failed.0.get_or_init(|| self.kind == "log" && super::digest::ci_failed(self.text.as_deref().unwrap_or("")))
    }
}

#[derive(Default, Debug)]
pub(crate) struct Lazy<T>(std::sync::OnceLock<T>);

impl<T> Clone for Lazy<T> {
    fn clone(&self) -> Self {
        Lazy(std::sync::OnceLock::new())
    }
}

impl<T> PartialEq for Lazy<T> {
    fn eq(&self, _: &Self) -> bool {
        true
    }
}

#[derive(Deserialize, Clone, Default, Debug, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Work {
    pub id: String,
    #[serde(default)]
    pub text: String,
    #[serde(rename = "type", default)]
    pub kind: String,
    #[serde(default)]
    pub priority: String,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub phase: String,
    #[serde(default)]
    pub assignee: Option<String>,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub acceptance: String,
    #[serde(default)]
    pub timeline: Vec<Entry>,
    #[serde(skip)]
    pub steps: super::phase::Steps,
    #[serde(skip)]
    pub project: String,
    #[serde(skip)]
    pub ledger_key: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct WorkKey {
    pub ledger: String,
    pub id: String,
}

impl WorkKey {
    pub fn new(ledger: impl Into<String>, id: impl Into<String>) -> WorkKey {
        WorkKey { ledger: ledger.into(), id: id.into() }
    }
}

impl Work {
    pub fn key(&self) -> WorkKey {
        WorkKey::new(self.ledger_key(), &self.id)
    }

    pub fn ledger_key(&self) -> &str {
        if self.ledger_key.is_empty() { &self.project } else { &self.ledger_key }
    }

    pub fn submitted_at(&self) -> String {
        self.timeline.iter().rev().find(|t| t.state.as_deref() == Some("in_review")).map(|t| t.at.clone()).unwrap_or_default()
    }

    pub fn last_at(&self) -> &str {
        self.timeline.last().map(|t| t.at.as_str()).unwrap_or("")
    }
}

#[derive(Deserialize, Clone, Default, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Meter {
    #[serde(default)]
    pub provider: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub percent: Option<f64>,
    #[serde(default)]
    pub resets_at: Option<String>,
    #[serde(default)]
    pub name: Option<String>,
}

#[derive(Deserialize, Clone, Default, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Host {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub state: String,
}

#[derive(Deserialize, Clone, Default, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Coordinator {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub session_id: Option<String>,
    #[serde(default)]
    pub surface: String,
    #[serde(default)]
    pub host: Option<crate::projects::Place>,
}

#[derive(Deserialize, Clone, Default, Debug)]
#[serde(rename_all = "camelCase")]
pub struct ProjectRecord {
    pub id: String,
    #[serde(default)]
    pub display_name: String,
    #[serde(default)]
    pub coordinators: Vec<Coordinator>,
    #[serde(default)]
    pub archived: bool,
    #[serde(default)]
    pub checkouts: Vec<crate::projects::Checkout>,
    #[serde(default)]
    pub mcp: Option<String>,
    #[serde(default, deserialize_with = "crate::catalog::known_time")]
    pub last_active: String,
    #[serde(default)]
    pub ledger_key: String,
    #[serde(default)]
    pub session_count: i64,
}

#[derive(Deserialize, Clone, Default, Debug)]
pub struct Actor {
    #[serde(default)]
    pub alias: Option<String>,
    #[serde(default)]
    pub name: Option<String>,
    #[serde(default)]
    pub surface: Option<String>,
}

#[derive(Deserialize, Clone, Default)]
#[serde(rename_all = "camelCase")]
pub struct Recorded {
    #[serde(default)]
    pub updated_at: String,
    #[serde(default)]
    pub host_epoch: String,
    #[serde(default)]
    pub catalog_seq: u64,
    #[serde(default)]
    pub projects: Vec<ProjectRecord>,
    #[serde(default)]
    pub sessions: Vec<Session>,
    #[serde(default)]
    pub ledgers: HashMap<String, Vec<Work>>,
    #[serde(default, rename = "phaseLabels")]
    pub steps: HashMap<String, super::phase::Steps>,
    #[serde(default)]
    pub transcripts: HashMap<String, Vec<Item>>,
    #[serde(skip)]
    pub prompted_before: HashSet<String>,
    #[serde(default)]
    pub usage: Vec<Meter>,
    #[serde(default)]
    pub hosts: Vec<Host>,
    #[serde(default)]
    pub actors: HashMap<String, Actor>,
    #[serde(default)]
    pub tile_order: Option<crate::projects::TileOrder>,
    #[serde(skip)]
    pub activity_missing: bool,
}

#[derive(Clone, Debug)]
pub struct Project {
    pub key: String,
    pub name: String,
    pub lead: Option<String>,
    pub lead_name: String,
    pub lead_pending: Option<(String, &'static str)>,
    pub sessions: Vec<String>,
    pub last_active: String,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Rows {
    Loading,
    Stale,
    Unavailable,
}

impl Rows {
    pub fn note(self) -> &'static str {
        match self {
            Rows::Loading => "Loading this project's work…",
            Rows::Stale => "Couldn't refresh this project's work. Showing what it last had.",
            Rows::Unavailable => "Couldn't load this project's work.",
        }
    }
}

pub struct Parts<'a> {
    pub updated_at: &'a str,
    pub sessions: Arc<Vec<Session>>,
    pub work: Vec<Arc<Work>>,
    pub steps: HashMap<String, super::phase::Steps>,
    pub transcripts: HashMap<String, Arc<[Item]>>,
    pub prompted_before: HashSet<String>,
    pub actors: &'a HashMap<String, Actor>,
    pub records: Vec<ProjectRecord>,
    pub usage: Vec<Meter>,
    pub hosts: Vec<Host>,
    pub tile_order: Option<crate::projects::TileOrder>,
}

pub fn prepared(mut w: Work, project: &str, ledger_key: &str, steps: &super::phase::Steps) -> Work {
    w.project = project.to_string();
    w.ledger_key = ledger_key.to_string();
    w.steps = steps.clone();
    w
}

pub struct World {
    pub now: OffsetDateTime,
    pub shown: OffsetDateTime,
    pub sessions: Arc<Vec<Session>>,
    pub projects: Vec<Project>,
    pub work: HashMap<WorkKey, Arc<Work>>,
    pub steps: HashMap<String, super::phase::Steps>,
    pub transcripts: HashMap<String, Arc<[Item]>>,
    pub prompted_before: HashSet<String>,
    pub started_by: HashMap<String, String>,
    pub helpers: HashSet<String>,
    pub usage: Vec<Meter>,
    pub hosts: Vec<Host>,
    pub rows: HashMap<String, Rows>,
    pub records: Vec<ProjectRecord>,
    pub tile_order: Option<crate::projects::TileOrder>,
    pub found_names: HashMap<String, String>,
    pub(crate) names: HashMap<String, String>,
}

impl World {
    pub fn from_json(json: &str) -> serde_json::Result<World> {
        Ok(World::new(serde_json::from_str(json)?))
    }

    pub fn new(raw: Recorded) -> World {
        let mut work = Vec::new();
        for (key, rows) in raw.ledgers {
            let ledger_key = raw.projects.iter().find(|p| p.id == key).map(|p| p.ledger_key.clone()).unwrap_or_default();
            let steps = raw.steps.get(&key).cloned().unwrap_or_default();
            work.extend(rows.into_iter().map(|w| Arc::new(prepared(w, &key, &ledger_key, &steps))));
        }
        World::shared(Parts {
            updated_at: &raw.updated_at,
            sessions: Arc::new(raw.sessions),
            work,
            steps: raw.steps,
            transcripts: raw.transcripts.into_iter().map(|(id, items)| (id, items.into())).collect(),
            prompted_before: raw.prompted_before,
            actors: &raw.actors,
            records: raw.projects,
            usage: raw.usage,
            hosts: raw.hosts,
            tile_order: raw.tile_order,
        })
    }

    pub fn shared(raw: Parts) -> World {
        let now = parse_time(raw.updated_at).unwrap_or_else(OffsetDateTime::now_utc);
        let helpers = raw.sessions.iter().filter(|s| s.subagent.is_some() || s.delegated == Some(true)).map(|s| s.id.clone()).collect();
        let started_by = raw.sessions.iter().filter_map(|s| s.started_by.clone().filter(|b| !b.is_empty()).map(|b| (s.id.clone(), b))).collect();
        let work: HashMap<WorkKey, Arc<Work>> = raw.work.into_iter().map(|w| (w.key(), w)).collect();
        let names = crate::derive::names::build(&raw.sessions, raw.actors, &work);
        let projects = projects(&raw.records, &raw.sessions);
        World {
            now,
            shown: crate::wake::minute_of(now),
            sessions: raw.sessions,
            projects,
            work,
            steps: raw.steps,
            transcripts: raw.transcripts,
            prompted_before: raw.prompted_before,
            started_by,
            helpers,
            usage: raw.usage,
            hosts: raw.hosts,
            rows: HashMap::new(),
            records: raw.records,
            tile_order: raw.tile_order,
            found_names: HashMap::new(),
            names,
        }
    }

    pub fn works(&self) -> impl Iterator<Item = &Work> {
        self.work.values().map(|w| &**w)
    }

    pub fn work_item(&self, key: &WorkKey) -> Option<&Work> {
        self.work.get(key).map(|w| &**w)
    }

    pub fn session(&self, id: &str) -> Option<&Session> {
        self.sessions.iter().find(|s| s.id == id)
    }

    pub fn project(&self, key: &str) -> Option<&Project> {
        self.projects.iter().find(|p| p.key == key)
    }

    pub fn project_name(&self, key: &str) -> String {
        self.project(key).map(|p| p.name.clone()).or_else(|| self.record(key).map(|r| r.display_name.clone())).or_else(|| self.found_names.get(key).cloned()).unwrap_or_default()
    }

    pub fn record(&self, key: &str) -> Option<&ProjectRecord> {
        self.records.iter().find(|p| p.id == key)
    }

    pub fn hours(&self, at: &str) -> f64 {
        parse_time(at).map(|t| (self.shown - t).as_seconds_f64() / 3600.0).unwrap_or(1e9)
    }

    pub fn ago(&self, at: &str) -> String {
        let h = self.hours(at);
        if h > 1e8 {
            return String::new();
        }
        let m = (h * 60.0).round() as i64;
        match m {
            i64::MIN..=0 => "now".into(),
            1..=59 => format!("{m}m"),
            60..=1439 => format!("{}h", m / 60),
            _ => format!("{}d", m / 1440),
        }
    }
}

fn projects(records: &[ProjectRecord], sessions: &[Session]) -> Vec<Project> {
    let mut out = Vec::new();
    for p in records.iter().filter(|p| !p.archived) {
        let mut mine: Vec<&Session> = sessions.iter().filter(|s| !s.archived && s.project_key() == p.id).collect();
        if mine.is_empty() && p.session_count == 0 && !p.checkouts.iter().any(|c| c.added) && p.coordinators.is_empty() {
            continue;
        }
        mine.sort_by(|a, b| b.last_active.cmp(&a.last_active));
        let latest = mine.first().map(|s| s.last_active.clone()).unwrap_or_default().max(p.last_active.clone());
        let coord = p.coordinators.first();
        let lead = coord.and_then(crate::projects::lead_session_id).filter(|id| sessions.iter().any(|s| &s.id == id));
        let lead_pending = coord.filter(|_| lead.is_none()).map(|c| (crate::projects::surface_label(&c.surface), crate::projects::lead_note(c)));
        out.push(Project {
            key: p.id.clone(),
            name: p.display_name.clone(),
            lead_name: coord.map(|c| format!("@{}", c.name)).unwrap_or_else(|| "no lead".into()),
            lead,
            lead_pending,
            last_active: latest,
            sessions: mine.iter().map(|s| s.id.clone()).collect(),
        });
    }
    out.sort_by(|a, b| b.last_active.cmp(&a.last_active));
    out
}
