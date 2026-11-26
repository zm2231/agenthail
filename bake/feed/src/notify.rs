use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::path::Path;

use anyhow::Result;
use serde::{Deserialize, Serialize};
use time::OffsetDateTime;

use crate::catalog::{Client, OpenRequest, Session, parse_time};
use crate::decisions::Decision;

pub const REPLY: &str = "reply";
const CHOICE: &str = "choice:";
const MAX_BUTTONS: usize = 5;
const RECENT_MINUTES: i64 = 10;
const KEEP_DAYS: i64 = 7;
const LEAD_COMPLETIONS_ONLY: &str = "leadCompletionsOnly";

#[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Debug)]
pub enum Kind {
    Needs,
    Finished,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Notice {
    pub key: String,
    pub kind: Kind,
    pub session: String,
    pub source: String,
    pub thread: String,
    pub title: String,
    pub body: String,
    pub request: Option<String>,
    pub buttons: Vec<(String, String)>,
    pub sound: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Prefs {
    pub lead_completions_only: bool,
}

impl Default for Prefs {
    fn default() -> Self {
        Prefs { lead_completions_only: true }
    }
}

#[derive(Deserialize)]
struct PrefRow {
    #[serde(default)]
    id: String,
    #[serde(default)]
    value: bool,
}

impl Client {
    pub fn notification_preferences(&self) -> Result<Prefs> {
        let value = self.get_json("/api/v1/notification-preferences")?;
        let rows: Vec<PrefRow> = serde_json::from_value(value["preferences"].clone())?;
        let mut prefs = Prefs::default();
        if let Some(row) = rows.iter().find(|r| r.id == LEAD_COMPLETIONS_ONLY) {
            prefs.lead_completions_only = row.value;
        }
        Ok(prefs)
    }
}

pub fn button_id(choice: &str) -> String {
    format!("{CHOICE}{choice}")
}

pub fn choice_of(action: &str) -> Option<&str> {
    action.strip_prefix(CHOICE)
}

fn title_of(sessions: &[Session], id: &str, fallback: &str) -> String {
    match sessions.iter().find(|s| s.id == id) {
        Some(s) => s.title(),
        None if !fallback.is_empty() => fallback.to_string(),
        None => crate::derive::text::short_id(id),
    }
}

fn buttons(request: &OpenRequest) -> Vec<(String, String)> {
    let found: Vec<(String, String)> = match request.questions.as_deref() {
        Some([q]) if !q.multi_select => q.options.iter().map(|o| (o.id.clone(), o.label.replace(" (Recommended)", "").replace(" (recommended)", ""))).collect(),
        Some(qs) if !qs.is_empty() => Vec::new(),
        _ if request.actions.is_empty() => vec![("allow".into(), "Allow".into()), ("deny".into(), "Deny".into())],
        _ => request.actions.iter().map(|a| (a.id.clone(), a.title.clone())).collect(),
    };
    let usable = found.len() <= MAX_BUTTONS && found.iter().all(|(id, label)| !id.is_empty() && !label.trim().is_empty());
    if usable { found.into_iter().map(|(id, label)| (button_id(&id), label)).collect() } else { Vec::new() }
}

fn asked(request: &OpenRequest, who: &str) -> String {
    let question = request.questions.as_ref().and_then(|qs| match qs.as_slice() {
        [q] => Some(q.question.trim().to_string()),
        [] => None,
        qs => Some(format!("{} questions", qs.len())),
    });
    let line = question
        .filter(|q| !q.is_empty())
        .or_else(|| request.title.clone().filter(|t| !t.trim().is_empty()))
        .or_else(|| request.description.clone().filter(|t| !t.trim().is_empty()))
        .unwrap_or_else(|| if request.tool.is_empty() { "A question".to_string() } else { format!("{who} wants to use {}", request.tool) });
    format!("Needs you: {}", line.trim())
}

pub fn needs(decisions: &[Decision], sessions: &[Session], sound: bool) -> Vec<Notice> {
    let mut out = Vec::new();
    for d in decisions.iter().filter(|d| !d.is_review() && !d.is_parked() && !d.session_id.is_empty()) {
        let thread = title_of(sessions, &d.session_id, &d.work_title);
        let notice = |key: String, body: String, request: Option<String>, buttons: Vec<(String, String)>| Notice { key, kind: Kind::Needs, session: d.session_id.clone(), source: d.host.clone(), thread: thread.clone(), title: thread.clone(), body, request, buttons, sound };
        if d.requests.is_empty() {
            let reason = d.reason_text();
            let body = if reason.trim().is_empty() { "Needs you".to_string() } else { format!("Needs you: {}", reason.trim()) };
            out.push(notice(format!("need|{}", d.hold_key()), body, None, Vec::new()));
            continue;
        }
        for r in &d.requests {
            let buttons = if d.answerable { buttons(r) } else { Vec::new() };
            out.push(notice(format!("need|{}|{}", d.session_id, r.id), asked(r, &thread), Some(r.id.clone()), buttons));
        }
    }
    out
}

pub fn finished(sessions: &[Session], leads: &HashMap<String, String>, prefs: &Prefs, sound: bool, now: OffsetDateTime) -> Vec<Notice> {
    let quiet = prefs.lead_completions_only;
    let mut out = Vec::new();
    for s in sessions.iter().filter(|s| !s.archived) {
        let Some(done) = &s.turn_completion else { continue };
        if !matches!(done.outcome.as_str(), "finished" | "failed") {
            continue;
        }
        let Some(at) = done.at.as_deref().and_then(parse_time) else { continue };
        if (now - at).whole_minutes() >= RECENT_MINUTES {
            continue;
        }
        if quiet && leads.get(&s.project_key()).is_some_and(|lead| lead != &s.id) {
            continue;
        }
        let turn = if done.turn_id.is_empty() { done.at.clone().unwrap_or_default() } else { done.turn_id.clone() };
        let body = match (done.outcome.as_str(), done.headline.trim()) {
            ("failed", _) => "Failed".to_string(),
            (_, "") => "Finished".to_string(),
            (_, headline) => format!("Done: {headline}"),
        };
        let title = s.title();
        out.push(Notice { key: format!("done|{}|{turn}", s.id), kind: Kind::Finished, session: s.id.clone(), source: String::new(), thread: title.clone(), title, body, request: None, buttons: Vec::new(), sound });
    }
    out
}

#[derive(Default, Debug, PartialEq)]
pub struct Step {
    pub post: Vec<Notice>,
    pub remove: Vec<String>,
    pub dirty: bool,
}

#[derive(Serialize, Deserialize, Default, Debug, Clone, PartialEq)]
pub struct Notified {
    #[serde(default)]
    seen: BTreeMap<String, i64>,
    #[serde(default)]
    primed: BTreeSet<String>,
    #[serde(default)]
    posted: BTreeMap<String, String>,
}

fn prefix(kind: Kind) -> &'static str {
    match kind {
        Kind::Needs => "need|",
        Kind::Finished => "done|",
    }
}

impl Notified {
    pub fn load(path: &Path) -> Self {
        std::fs::read_to_string(path).ok().and_then(|t| serde_json::from_str(&t).ok()).unwrap_or_default()
    }

    pub fn save(&self, path: &Path) -> std::io::Result<()> {
        if let Some(dir) = path.parent() {
            std::fs::create_dir_all(dir)?;
        }
        let tmp = path.with_extension("json.tmp");
        std::fs::write(&tmp, serde_json::to_vec(self).unwrap_or_default())?;
        std::fs::rename(tmp, path)
    }

    pub fn seen(&self, key: &str) -> bool {
        self.seen.contains_key(key)
    }

    pub fn take(&mut self, kind: Kind, notices: Vec<Notice>, read: &[String], now: OffsetDateTime, shown: impl Fn(&Notice) -> bool) -> Step {
        let stamp = now.unix_timestamp();
        let prefix = prefix(kind);
        let mut step = Step::default();
        let current: BTreeSet<String> = notices.iter().map(|n| n.key.clone()).collect();
        for n in notices {
            let fresh = self.seen.insert(n.key.clone(), stamp).is_none();
            step.dirty |= fresh;
            if fresh && self.primed.contains(&format!("{prefix}{}", n.source)) && !shown(&n) {
                if n.kind == Kind::Needs {
                    self.posted.insert(n.key.clone(), n.source.clone());
                }
                step.post.push(n);
            }
        }
        for source in read {
            step.dirty |= self.primed.insert(format!("{prefix}{source}"));
        }
        let gone: Vec<String> = self.posted.iter().filter(|(k, source)| k.starts_with(prefix) && read.contains(source) && !current.contains(*k)).map(|(k, _)| k.clone()).collect();
        for key in gone {
            self.posted.remove(&key);
            step.remove.push(key);
            step.dirty = true;
        }
        let oldest = stamp - KEEP_DAYS * 86_400;
        let before = self.seen.len();
        self.seen.retain(|k, at| !k.starts_with(prefix) || current.contains(k) || *at >= oldest);
        step.dirty |= self.seen.len() != before;
        step
    }
}

pub fn daemon_posts(home: &Path) -> bool {
    #[derive(Deserialize)]
    struct Config {
        #[serde(default)]
        enabled: bool,
    }
    std::fs::read(home.join(".agenthail/notifications.json")).ok().and_then(|b| serde_json::from_slice::<Config>(&b).ok()).is_some_and(|c| c.enabled)
}

#[cfg(test)]
#[path = "notify_tests.rs"]
mod tests;
