use std::collections::{HashMap, HashSet};

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
    pub assignee: Option<String>,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub acceptance: String,
    #[serde(default)]
    pub timeline: Vec<Entry>,
    #[serde(skip)]
    pub project: String,
}

impl Work {
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

#[derive(Deserialize, Default)]
#[serde(rename_all = "camelCase")]
pub struct Recorded {
    #[serde(default)]
    pub updated_at: String,
    #[serde(default)]
    pub projects: Vec<ProjectRecord>,
    #[serde(default)]
    pub sessions: Vec<Session>,
    #[serde(default)]
    pub ledgers: HashMap<String, Vec<Work>>,
    #[serde(default)]
    pub transcripts: HashMap<String, Vec<Item>>,
    #[serde(default)]
    pub usage: Vec<Meter>,
    #[serde(default)]
    pub hosts: Vec<Host>,
    #[serde(default)]
    pub actors: HashMap<String, Actor>,
}

#[derive(Clone, Debug)]
pub struct Project {
    pub key: String,
    pub name: String,
    pub lead: Option<String>,
    pub lead_name: String,
    pub sessions: Vec<String>,
    pub last_active: String,
}

pub struct World {
    pub now: OffsetDateTime,
    pub sessions: Vec<Session>,
    pub projects: Vec<Project>,
    pub work: HashMap<String, Work>,
    pub transcripts: HashMap<String, Vec<Item>>,
    pub started_by: HashMap<String, String>,
    pub helpers: HashSet<String>,
    pub usage: Vec<Meter>,
    pub hosts: Vec<Host>,
    pub(crate) names: HashMap<String, String>,
}

impl World {
    pub fn from_json(json: &str) -> serde_json::Result<World> {
        Ok(World::new(serde_json::from_str(json)?))
    }

    pub fn new(raw: Recorded) -> World {
        let now = parse_time(&raw.updated_at).unwrap_or_else(OffsetDateTime::now_utc);
        let helpers = raw.sessions.iter().filter(|s| s.subagent.is_some() || s.delegated == Some(true)).map(|s| s.id.clone()).collect();
        let started_by = raw.sessions.iter().filter_map(|s| s.started_by.clone().filter(|b| !b.is_empty()).map(|b| (s.id.clone(), b))).collect();
        let mut work = HashMap::new();
        for (key, rows) in raw.ledgers {
            for mut w in rows {
                w.project = key.clone();
                work.insert(w.id.clone(), w);
            }
        }
        let names = crate::derive::names::build(&raw.sessions, &raw.actors, &work);
        let projects = projects(&raw.projects, &raw.sessions);
        World { now, sessions: raw.sessions, projects, work, transcripts: raw.transcripts, started_by, helpers, usage: raw.usage, hosts: raw.hosts, names }
    }

    pub fn session(&self, id: &str) -> Option<&Session> {
        self.sessions.iter().find(|s| s.id == id)
    }

    pub fn project(&self, key: &str) -> Option<&Project> {
        self.projects.iter().find(|p| p.key == key)
    }

    // Unparseable times count as long ago.
    pub fn hours(&self, at: &str) -> f64 {
        parse_time(at).map(|t| (self.now - t).as_seconds_f64() / 3600.0).unwrap_or(1e9)
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
        if mine.is_empty() {
            continue;
        }
        mine.sort_by(|a, b| b.last_active.cmp(&a.last_active));
        let coord = p.coordinators.first();
        let lead = coord.and_then(|c| c.session_id.clone()).filter(|id| sessions.iter().any(|s| &s.id == id));
        out.push(Project {
            key: p.id.clone(),
            name: p.display_name.clone(),
            lead_name: coord.map(|c| format!("@{}", c.name)).unwrap_or_else(|| "no lead".into()),
            lead,
            last_active: mine[0].last_active.clone(),
            sessions: mine.iter().map(|s| s.id.clone()).collect(),
        });
    }
    out.sort_by(|a, b| b.last_active.cmp(&a.last_active));
    out
}
