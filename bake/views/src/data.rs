use std::collections::{HashMap, HashSet};

use feed::Item;
use feed::catalog::{Session, parse_time};
use serde::Deserialize;
use time::OffsetDateTime;

pub const FIXTURE: &str = include_str!("../fixture/data.json");

#[derive(Deserialize, Clone, Default)]
#[serde(rename_all = "camelCase")]
struct Coordinator {
    #[serde(default)]
    name: String,
    #[serde(default)]
    session_id: Option<String>,
}

#[derive(Deserialize, Clone, Default)]
#[serde(rename_all = "camelCase")]
struct RawProject {
    id: String,
    #[serde(default)]
    display_name: String,
    #[serde(default)]
    coordinators: Vec<Coordinator>,
    #[serde(default)]
    archived: bool,
}

#[derive(Deserialize, Clone, Default, Debug)]
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

#[derive(Deserialize, Clone, Default, Debug)]
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

#[derive(Deserialize, Default)]
#[serde(rename_all = "camelCase")]
struct Origin {
    id: String,
    #[serde(default)]
    started_by: Option<String>,
}

#[derive(Deserialize, Default)]
#[serde(rename_all = "camelCase")]
struct Origins {
    #[serde(default)]
    sessions: Vec<Origin>,
}

#[derive(Deserialize, Default)]
#[serde(rename_all = "camelCase")]
struct Raw {
    updated_at: String,
    #[serde(default)]
    projects: Vec<RawProject>,
    #[serde(default)]
    sessions: Vec<Session>,
    #[serde(default)]
    ledgers: HashMap<String, Vec<Work>>,
    #[serde(default)]
    transcripts: HashMap<String, Vec<Item>>,
}

pub struct Project {
    pub key: String,
    pub name: String,
    pub lead: Option<String>,
    pub lead_name: String,
    pub sessions: Vec<String>,
    pub last_active: String,
}

#[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Debug)]
pub enum NeedKind {
    Question,
    Approval,
    Check,
    CiFailing,
}

impl NeedKind {
    pub fn label(self) -> &'static str {
        match self {
            NeedKind::Question => "Question",
            NeedKind::Approval => "Approval",
            NeedKind::Check => "Check the proof",
            NeedKind::CiFailing => "CI failing",
        }
    }

    pub fn ask(self) -> &'static str {
        match self {
            NeedKind::Question => "Wants your answer",
            NeedKind::Approval => "Wants your go-ahead",
            NeedKind::Check => "Does this do what was asked?",
            NeedKind::CiFailing => "Checks are failing on the PR",
        }
    }

    pub fn primary(self) -> &'static str {
        match self {
            NeedKind::Question => "Reply",
            NeedKind::Approval => "Approve",
            NeedKind::Check => "Approve",
            NeedKind::CiFailing => "See the failure",
        }
    }
}

#[derive(Clone, PartialEq, Debug)]
pub enum Target {
    Session(String),
    Work(String),
    Project(String),
}

#[derive(Clone, Debug)]
pub struct Need {
    pub kind: NeedKind,
    pub project: String,
    pub project_key: String,
    pub title: String,
    pub who: String,
    pub at: String,
    pub target: Target,
    pub label: Option<String>,
}

#[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Debug)]
pub enum Happened {
    Shipped,
    Waiting,
    Failed,
    Moved,
    Said,
}

impl Happened {
    pub fn label(self) -> &'static str {
        match self {
            Happened::Shipped => "Shipped",
            Happened::Waiting => "Waiting on you",
            Happened::Failed => "Failed",
            Happened::Moved => "Moved",
            Happened::Said => "Said",
        }
    }
}

#[derive(Clone, Debug)]
pub struct Line {
    pub kind: Happened,
    pub text: String,
    pub detail: String,
    pub at: String,
    pub target: Target,
}

pub const PHASES: [&str; 6] = ["Scope", "Build", "Review", "Verify", "Check", "Ship"];

pub struct Proof {
    pub expected: String,
    pub observed: String,
    pub passed: Option<bool>,
}

pub struct Data {
    pub now: OffsetDateTime,
    pub sessions: Vec<Session>,
    pub projects: Vec<Project>,
    pub work: HashMap<String, Work>,
    pub transcripts: HashMap<String, Vec<Item>>,
    pub person: HashMap<String, String>,
    pub spawned: HashSet<String>,
    names: HashMap<String, String>,
}

fn lower(s: &str) -> String {
    s.to_lowercase()
}

fn first_sentence(text: &str, max: usize) -> String {
    let t = text.trim();
    let cut = t.find(". ").map(|i| &t[..i]).unwrap_or(t);
    let cut = cut.trim_end_matches('.');
    if cut.chars().count() > max {
        let s: String = cut.chars().take(max).collect();
        format!("{}…", s.trim_end())
    } else {
        cut.to_string()
    }
}

pub fn plain(text: &str) -> String {
    text.replace(" — ", ", ").replace(" – ", ", ").replace('—', "--").replace('–', "-")
}

impl Data {
    pub fn load(json: &str) -> Data {
        let raw: Raw = serde_json::from_str(json).unwrap_or_default();
        let origins: Origins = serde_json::from_str(json).unwrap_or_default();
        let spawned = origins.sessions.into_iter().filter(|o| o.started_by.as_deref().is_some_and(|b| !b.is_empty())).map(|o| o.id).collect();
        let now = parse_time(&raw.updated_at).unwrap_or_else(OffsetDateTime::now_utc);
        let mut names = HashMap::new();
        for s in &raw.sessions {
            names.insert(s.id.clone(), title_of(s, &raw.ledgers));
        }
        let mut work = HashMap::new();
        for (key, rows) in raw.ledgers {
            for mut w in rows {
                w.project = key.clone();
                work.insert(w.id.clone(), w);
            }
        }
        let mut projects = Vec::new();
        for p in raw.projects.iter().filter(|p| !p.archived) {
            let mut sessions: Vec<&Session> = raw.sessions.iter().filter(|s| !s.archived && s.project_key() == p.id).collect();
            if sessions.is_empty() {
                continue;
            }
            sessions.sort_by(|a, b| b.last_active.cmp(&a.last_active));
            let coord = p.coordinators.first();
            let lead = coord.and_then(|c| c.session_id.clone()).filter(|id| raw.sessions.iter().any(|s| &s.id == id));
            projects.push(Project {
                key: p.id.clone(),
                name: p.display_name.clone(),
                lead_name: coord.map(|c| format!("@{}", c.name)).unwrap_or_else(|| "no lead".into()),
                lead,
                last_active: sessions[0].last_active.clone(),
                sessions: sessions.iter().map(|s| s.id.clone()).collect(),
            });
        }
        projects.sort_by(|a, b| b.last_active.cmp(&a.last_active));
        let mut person = HashMap::new();
        for (sid, items) in &raw.transcripts {
            if let Some(at) = items.iter().rev().find(|i| i.kind == "message" && i.role_is("user")).and_then(|i| i.timestamp.clone()) {
                person.insert(sid.clone(), at);
            }
        }
        Data { now, sessions: raw.sessions, projects, work, transcripts: raw.transcripts, person, spawned, names }
    }

    pub fn session(&self, id: &str) -> Option<&Session> {
        self.sessions.iter().find(|s| s.id == id)
    }

    pub fn project(&self, key: &str) -> Option<&Project> {
        self.projects.iter().find(|p| p.key == key)
    }

    pub fn title(&self, s: &Session) -> String {
        self.names.get(&s.id).cloned().unwrap_or_else(|| s.title())
    }

    pub fn name_of(&self, actor: &str) -> String {
        if let Some(n) = self.names.get(actor) {
            return n.clone();
        }
        if actor.is_empty() {
            return "Someone".into();
        }
        if actor.contains(' ') {
            return actor.to_string();
        }
        "a past worker".into()
    }

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

    pub fn headline(s: &Session) -> Option<(String, String)> {
        for h in [&s.turn_completion, &s.end_turn].into_iter().flatten() {
            if !h.headline.is_empty() {
                return Some((h.headline.clone(), h.at.clone().unwrap_or_else(|| s.last_active.clone())));
            }
        }
        None
    }

    pub fn gist(&self, s: &Session) -> String {
        if let Some((h, _)) = Data::headline(s) {
            return plain(&h);
        }
        if let Some(w) = s.held_work.first() {
            return plain(&w.text);
        }
        if let Some(m) = self.transcripts.get(&s.id).and_then(|t| t.iter().rev().find(|i| i.kind == "message" && i.role_is("assistant"))) {
            return first_sentence(&plain(&m.text), 140);
        }
        s.display.label.clone()
    }

    pub fn needs(&self) -> Vec<Need> {
        let mut out = Vec::new();
        let mut seen = HashSet::new();
        for s in &self.sessions {
            let Some((headline, at)) = Data::headline(s) else { continue };
            let h = lower(&headline);
            let flagged = s.turn_completion.as_ref().is_some_and(|t| t.outcome == "needs_you") || s.attention.is_some();
            let waiting = ["waiting on you", "waiting on your", "ready to publish", "standing by for", "your call", "your ok", "needs your", "asked which"].iter().any(|w| h.contains(w));
            if !(flagged || (waiting && self.hours(&at) < 24.0)) || s.project.is_none() {
                continue;
            }
            let kind = if ["ok to push", "publish", "merge", "push the", "approve", "release"].iter().any(|w| h.contains(w)) { NeedKind::Approval } else { NeedKind::Question };
            seen.insert(s.id.clone());
            out.push(Need {
                kind,
                project: s.project_name(),
                project_key: s.project_key(),
                title: plain(&headline),
                who: self.title(s),
                at,
                target: Target::Session(s.id.clone()),
                label: None,
            });
        }
        for w in self.work.values() {
            if w.state != "in_review" {
                continue;
            }
            let at = w.timeline.iter().rev().find(|t| t.state.as_deref() == Some("in_review")).map(|t| t.at.clone()).unwrap_or_default();
            if self.hours(&at) > 48.0 {
                continue;
            }
            let failing = w.timeline.iter().rev().find(|t| t.text.as_deref().is_some_and(|x| !x.is_empty())).and_then(|t| t.text.as_deref()).is_some_and(|x| {
                let x = lower(x);
                x.contains("ci") && (x.contains("failed") || x.contains("failing")) && !["passed", "cause", "fixed", "merged"].iter().any(|w| x.contains(w))
            });
            out.push(Need {
                kind: if failing { NeedKind::CiFailing } else { NeedKind::Check },
                project: self.project(&w.project).map(|p| p.name.clone()).unwrap_or_default(),
                project_key: w.project.clone(),
                title: plain(&w.text),
                who: self.worker(w),
                at,
                target: Target::Work(w.id.clone()),
                label: Some(short_id(&w.id)),
            });
        }
        out.sort_by(|a, b| b.at.cmp(&a.at));
        out
    }

    pub fn worker(&self, w: &Work) -> String {
        let actor = w
            .timeline
            .iter()
            .rev()
            .find(|t| t.kind == "log" && t.actor != w.assignee.clone().unwrap_or_default())
            .map(|t| t.actor.clone())
            .or(w.assignee.clone())
            .unwrap_or_default();
        self.name_of(&actor)
    }

    pub fn hands_on(&self) -> Vec<&Session> {
        let mut list: Vec<&Session> = self
            .sessions
            .iter()
            .filter(|s| !s.archived && s.project.is_some() && !self.spawned.contains(&s.id) && s.display.label != "Closed")
            .filter(|s| {
                let typed = self.person.get(&s.id).is_some_and(|at| self.hours(at) < 0.5);
                let opened = s.open && !self.spawned.contains(&s.id) && s.display.label != "Closed" && self.hours(&s.last_active) < 0.5;
                typed || opened
            })
            .collect();
        list.sort_by(|a, b| self.hours(&b.last_active).partial_cmp(&self.hours(&a.last_active)).unwrap_or(std::cmp::Ordering::Equal).reverse());
        list.truncate(5);
        list
    }

    pub fn moving(&self, skip: &[&str]) -> Vec<&Session> {
        let mut list: Vec<&Session> = self
            .sessions
            .iter()
            .filter(|s| !skip.contains(&s.id.as_str()) && s.project.is_some())
            .filter(|s| matches!(s.display.kind.as_str(), "working" | "finishing" | "starting") || s.status == "busy")
            .collect();
        list.sort_by(|a, b| b.last_active.cmp(&a.last_active));
        list
    }

    pub fn digest(&self, project: &Project, window_hours: f64) -> Vec<Line> {
        let mut lines = Vec::new();
        for w in self.work.values().filter(|w| w.project == project.key) {
            for t in &w.timeline {
                if self.hours(&t.at) > window_hours {
                    continue;
                }
                let text = plain(&w.text);
                let target = Target::Work(w.id.clone());
                match (t.kind.as_str(), t.state.as_deref()) {
                    ("state", Some("done")) => lines.push(Line { kind: Happened::Shipped, text, detail: String::new(), at: t.at.clone(), target }),
                    ("state", Some("in_review")) if w.state == "in_review" => lines.push(Line { kind: Happened::Waiting, text, detail: "Check the proof".into(), at: t.at.clone(), target }),
                    ("state", Some("in_progress")) => lines.push(Line { kind: Happened::Moved, text, detail: format!("Started by {}", self.name_of(&t.actor)), at: t.at.clone(), target }),
                    ("log", _) => {
                        let body = t.text.clone().unwrap_or_default();
                        let l = lower(&body);
                        let ci = l.contains("ci run") || l.contains("ci ") || l.contains("test run") || l.contains("suite");
                        if ci && w.state != "done" && l.contains(" failed") && !["not from", "since fixed", "cause", "passed"].iter().any(|w| l.contains(w)) {
                            lines.push(Line { kind: Happened::Failed, text, detail: first_sentence(&plain(&body), 120), at: t.at.clone(), target });
                        }
                    }
                    _ => {}
                }
            }
        }
        for sid in &project.sessions {
            let Some(s) = self.session(sid) else { continue };
            if let Some((h, at)) = Data::headline(s) {
                if self.hours(&at) <= window_hours {
                    lines.push(Line { kind: Happened::Said, text: plain(&h), detail: self.title(s), at, target: Target::Session(s.id.clone()) });
                }
            }
        }
        let mut dedup = HashSet::new();
        lines.sort_by(|a, b| a.kind.cmp(&b.kind).then(b.at.cmp(&a.at)));
        lines.retain(|l| dedup.insert((l.kind, l.text.clone())));
        lines
    }

    pub fn phase(&self, w: &Work) -> usize {
        match w.state.as_str() {
            "in_progress" => {
                let logs: Vec<String> = w.timeline.iter().filter(|t| t.kind == "log").map(|t| lower(t.text.as_deref().unwrap_or(""))).collect();
                if logs.iter().any(|l| l.contains("sim run") || l.contains("gate pass") || l.contains("passed") || l.contains("verified")) {
                    3
                } else if logs.iter().any(|l| l.contains("review") || l.contains("ship")) {
                    2
                } else {
                    1
                }
            }
            "in_review" => 4,
            "done" | "closed" => 5,
            _ => 0,
        }
    }

    pub fn whose_move(&self, w: &Work) -> String {
        match self.phase(w) {
            4 => "You".into(),
            0 | 5 => self.project(&w.project).map(|p| p.lead_name.clone()).unwrap_or_else(|| "The lead".into()),
            _ => self.worker(w),
        }
    }

    pub fn pr(&self, w: &Work) -> Option<String> {
        for t in w.timeline.iter().rev() {
            let text = t.text.as_deref().unwrap_or("");
            if let Some(i) = text.find("pull/") {
                let n: String = text[i + 5..].chars().take_while(|c| c.is_ascii_digit()).collect();
                if !n.is_empty() {
                    return Some(n);
                }
            }
            for (i, _) in text.match_indices("PR #") {
                let n: String = text[i + 4..].chars().take_while(|c| c.is_ascii_digit()).collect();
                if n.len() >= 3 {
                    return Some(n);
                }
            }
            for (i, _) in text.match_indices('#') {
                let n: String = text[i + 1..].chars().take_while(|c| c.is_ascii_digit()).collect();
                if n.len() == 4 {
                    return Some(n);
                }
            }
        }
        None
    }

    pub fn proof(&self, w: &Work) -> Vec<Proof> {
        let mut steps = Vec::new();
        let results: Vec<&Entry> = w.timeline.iter().filter(|t| t.kind == "log" && t.log_type.as_deref() == Some("result")).collect();
        let clauses: Vec<String> = w
            .acceptance
            .split("; ")
            .flat_map(|c| c.split(". "))
            .map(|c| c.rsplit(": ").next().unwrap_or(c).trim().trim_end_matches('.').to_string())
            .filter(|c| c.len() > 12)
            .collect();
        let mut used = HashSet::new();
        for clause in clauses {
            let words: Vec<String> = lower(&clause).split(|c: char| !c.is_alphanumeric()).filter(|x| x.len() > 3).map(String::from).collect();
            let best = results
                .iter()
                .enumerate()
                .filter(|(i, _)| !used.contains(i))
                .map(|(i, r)| {
                    let body = lower(r.text.as_deref().unwrap_or(""));
                    (i, words.iter().filter(|w| body.contains(w.as_str())).count(), r)
                })
                .filter(|(_, n, _)| *n >= 2)
                .last();
            match best {
                Some((i, n, r)) if n >= 2 => {
                    used.insert(i);
                    let body = r.text.clone().unwrap_or_default();
                    let l = lower(&body);
                    let pass = !(l.contains("failed") && !l.contains("since fixed")) || l.contains("exit 0");
                    steps.push(Proof { expected: capital(&plain(&clause)), observed: first_sentence(&plain(&body), 220), passed: Some(pass) });
                }
                _ => steps.push(Proof { expected: capital(&plain(&clause)), observed: "Nothing in the record checks this.".into(), passed: None }),
            }
        }
        if let Some(last) = results.last() {
            let i = results.len() - 1;
            if !used.contains(&i) {
                let body = last.text.clone().unwrap_or_default();
                steps.push(Proof { expected: "The full test suite passes on the final commit".into(), observed: first_sentence(&plain(&body), 220), passed: Some(lower(&body).contains("exit 0") || lower(&body).contains("ok")) });
            }
        }
        steps
    }

    pub fn unchecked(&self, w: &Work) -> Vec<String> {
        let submitted = w.timeline.iter().rev().find(|t| t.state.as_deref() == Some("in_review")).map(|t| t.at.clone()).unwrap_or_default();
        w.timeline.iter().filter(|t| t.kind == "comment" && t.at > submitted).map(|t| first_sentence(&plain(t.text.as_deref().unwrap_or("")), 260)).collect()
    }

    pub fn also_holds<'a>(&'a self, s: &Session, except: &str) -> Vec<&'a Work> {
        s.held_work.iter().filter(|h| h.item_id != except).filter_map(|h| self.work.get(&h.item_id)).collect()
    }

    pub fn board(&self, project: &str) -> [Vec<&Work>; 4] {
        let mut cols: [Vec<&Work>; 4] = Default::default();
        for w in self.work.values().filter(|w| w.project == project) {
            let last = w.timeline.last().map(|t| t.at.clone()).unwrap_or_default();
            match w.state.as_str() {
                "in_review" if self.hours(&w.timeline.iter().rev().find(|t| t.state.as_deref() == Some("in_review")).map(|t| t.at.clone()).unwrap_or_default()) <= 48.0 => cols[0].push(w),
                "in_progress" => cols[1].push(w),
                "open" | "ready" => cols[2].push(w),
                "done" if self.hours(&last) < 24.0 => cols[3].push(w),
                _ => {}
            }
        }
        let rank = |w: &Work| w.priority.trim_start_matches('P').parse::<u8>().unwrap_or(9);
        for c in cols.iter_mut() {
            c.sort_by(|a, b| {
                let la = a.timeline.last().map(|t| t.at.as_str()).unwrap_or("");
                let lb = b.timeline.last().map(|t| t.at.as_str()).unwrap_or("");
                lb.cmp(la)
            });
        }
        cols[2].sort_by_key(|w| rank(w));
        cols
    }
}

fn title_of(s: &Session, ledgers: &HashMap<String, Vec<Work>>) -> String {
    let t = s.title();
    if t.starts_with('@') || (t.split_whitespace().count() <= 3 && t.len() <= 32) {
        return t;
    }
    if let Some(h) = s.held_work.first() {
        if let Some(w) = ledgers.values().flatten().find(|w| w.id == h.item_id) {
            return clip_words(&plain(&w.text), 44);
        }
    }
    clip_words(&t, 44)
}

pub fn clip_words(t: &str, max: usize) -> String {
    let t = t.split([':', ';', '.']).next().unwrap_or(t).trim();
    if t.chars().count() <= max {
        return t.to_string();
    }
    let mut out = String::new();
    for w in t.split_whitespace() {
        if out.chars().count() + w.chars().count() + 1 > max {
            break;
        }
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(w);
    }
    format!("{out}…")
}

fn capital(t: &str) -> String {
    let mut c = t.chars();
    c.next().map(|f| f.to_uppercase().collect::<String>() + c.as_str()).unwrap_or_default()
}

pub fn short_id(id: &str) -> String {
    id.rsplit('-').next().unwrap_or(id).to_string()
}
