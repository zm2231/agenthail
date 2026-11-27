use serde::Deserialize;
use serde_json::{Value, json};
use time::{OffsetDateTime, UtcOffset};

use crate::background::Clock;
use crate::catalog::{Client, Lifecycle, RespondError, Session, parse_time};

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Goal {
    #[serde(default)]
    pub objective: String,
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub time_used_seconds: Option<i64>,
    #[serde(default)]
    pub tokens_used: Option<i64>,
    #[serde(default)]
    pub token_budget: Option<i64>,
    #[serde(default)]
    pub created_at: Option<String>,
    #[serde(default)]
    pub updated_at: Option<String>,
    #[serde(default)]
    pub reason: Option<String>,
}

impl Goal {
    pub fn display_status(&self) -> String {
        match self.status.as_str() {
            "active" => "Active".into(),
            "paused" => "Paused".into(),
            "blocked" | "usageLimited" | "budgetLimited" => "Needs you".into(),
            "complete" => "Complete".into(),
            other => capitalized(other),
        }
    }

    pub fn needs_attention(&self) -> bool {
        matches!(self.status.as_str(), "blocked" | "usageLimited" | "budgetLimited")
    }

    pub fn reason(&self) -> Option<&str> {
        self.reason.as_deref().filter(|r| !r.is_empty())
    }

    pub fn usage(&self) -> String {
        let mut parts = Vec::new();
        if let Some(seconds) = self.time_used_seconds {
            parts.push(format!("{} elapsed", duration(seconds)));
        }
        match (self.tokens_used, self.token_budget) {
            (Some(used), Some(budget)) => parts.push(format!("{} of {} tokens", compact(used), compact(budget))),
            (Some(used), None) => parts.push(format!("{} tokens", compact(used))),
            (None, Some(budget)) => parts.push(format!("{} token budget", compact(budget))),
            (None, None) => {}
        }
        parts.join(" · ")
    }

    pub fn times(&self, now: OffsetDateTime, clock: Clock, offset_at: impl Fn(OffsetDateTime) -> i32) -> String {
        let created = self.created_at.as_deref().and_then(|t| when(t, now, clock, &offset_at)).map(|t| format!("Created {t}"));
        let updated = self.updated_at.as_deref().and_then(|t| when(t, now, clock, &offset_at)).map(|t| format!("updated {t}"));
        [created, updated].into_iter().flatten().collect::<Vec<_>>().join(", ")
    }
}

fn capitalized(word: &str) -> String {
    let mut chars = word.chars();
    match chars.next() {
        Some(first) => first.to_uppercase().chain(chars).collect(),
        None => String::new(),
    }
}

pub fn duration(seconds: i64) -> String {
    if seconds < 60 {
        return format!("{seconds}s");
    }
    let minutes = seconds / 60;
    if minutes < 60 {
        return format!("{minutes}m {}s", seconds % 60);
    }
    format!("{}h {}m", minutes / 60, minutes % 60)
}

pub fn compact(n: i64) -> String {
    let scaled = |value: f64, unit: &str| {
        let shown = if value < 10.0 { (value * 10.0).round() / 10.0 } else { value.round() };
        if shown.fract() == 0.0 { format!("{}{unit}", shown as i64) } else { format!("{shown:.1}{unit}") }
    };
    let a = n.unsigned_abs() as f64;
    let sign = if n < 0 { "-" } else { "" };
    let body = match a {
        a if a >= 1e12 => scaled(a / 1e12, "T"),
        a if a >= 1e9 => scaled(a / 1e9, "B"),
        a if a >= 1e6 => scaled(a / 1e6, "M"),
        a if a >= 1e3 => scaled(a / 1e3, "K"),
        a => (a as i64).to_string(),
    };
    format!("{sign}{body}")
}

const MONTHS: [&str; 12] = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

pub fn when(ts: &str, now: OffsetDateTime, clock: Clock, offset_at: &impl Fn(OffsetDateTime) -> i32) -> Option<String> {
    let local = |t: OffsetDateTime| t.to_offset(UtcOffset::from_whole_seconds(offset_at(t)).unwrap_or(UtcOffset::UTC));
    let at = local(parse_time(ts)?);
    let time = clock.show(at.hour(), at.minute());
    if at.date() == local(now).date() {
        return Some(time);
    }
    Some(format!("{} {}, {}, {time}", MONTHS[at.month() as usize - 1], at.day(), at.year()))
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ClaudeRun {
    #[serde(default)]
    pub record_path: String,
    #[serde(default)]
    pub job_id: String,
    #[serde(default)]
    pub session_id: Option<String>,
    #[serde(default)]
    pub resume_session_id: Option<String>,
    #[serde(default)]
    pub run_type: Option<String>,
    #[serde(default)]
    pub provider_state: Option<String>,
    #[serde(default)]
    pub created_at: Option<String>,
    #[serde(default)]
    pub updated_at: Option<String>,
}

impl ClaudeRun {
    pub fn lines(&self, now: OffsetDateTime, clock: Clock, offset_at: impl Fn(OffsetDateTime) -> i32) -> Vec<String> {
        let named = |label: &str, value: &Option<String>| value.as_deref().filter(|v| !v.is_empty()).map(|v| format!("{label}: {v}"));
        let at = |label: &str, value: &Option<String>| value.as_deref().and_then(|t| when(t, now, clock, &offset_at)).map(|t| format!("{label} {t}"));
        [
            named("Type", &self.run_type),
            named("Provider state", &self.provider_state),
            named("Session", &self.session_id),
            named("Resume session", &self.resume_session_id),
            at("Created", &self.created_at),
            at("Updated", &self.updated_at),
            (!self.record_path.is_empty()).then(|| format!("Record: {}", self.record_path)),
        ]
        .into_iter()
        .flatten()
        .collect()
    }
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Metadata {
    pub goal: Option<Goal>,
    pub goal_failed: bool,
    pub runs: Option<Vec<ClaudeRun>>,
    pub warnings: Vec<String>,
}

impl Metadata {
    pub fn after(mut self, old: Option<&Metadata>) -> Metadata {
        if self.goal_failed && self.goal.is_none() {
            self.goal = old.and_then(|m| m.goal.clone());
        }
        self
    }

    pub fn keep_goal(mut self, shown: Option<&Metadata>) -> Metadata {
        self.goal = shown.and_then(|m| m.goal.clone());
        self.goal_failed = false;
        self
    }

    pub fn failed(old: Option<&Metadata>, error: String) -> Metadata {
        Metadata { goal: old.and_then(|m| m.goal.clone()), goal_failed: true, runs: old.and_then(|m| m.runs.clone()), warnings: vec![error] }
    }
}

fn field_name(key: &str) -> &str {
    match key {
        "context" => "Context usage",
        "goal" => "Goal",
        "model" => "Current model",
        "models" => "Model list",
        "turnSettings" => "Turn settings",
        "compactSettings" => "Compact settings",
        "claudeRuns" => "Claude runs",
        "claudeSubagents" => "Subagents",
        other => other,
    }
}

pub fn metadata(value: &Value) -> Metadata {
    let goal = serde_json::from_value::<Goal>(value["goal"].clone()).ok().filter(|g| !g.objective.is_empty());
    let runs = value.get("claudeRuns").filter(|v| !v.is_null()).map(|v| serde_json::from_value(v.clone()).unwrap_or_default());
    let mut keys: Vec<(&String, &Value)> = value["errors"].as_object().into_iter().flatten().collect();
    keys.sort_by(|a, b| a.0.cmp(b.0));
    let warnings = keys
        .into_iter()
        .map(|(key, reason)| {
            let reason = reason.as_str().unwrap_or_default().trim();
            if reason.is_empty() { field_name(key).to_string() } else { format!("{}: {reason}", field_name(key)) }
        })
        .collect();
    Metadata { goal, goal_failed: value["errors"].get("goal").is_some(), runs, warnings }
}

#[derive(Clone, Debug, Default)]
pub struct Reads {
    next: u64,
    applied: std::collections::HashMap<String, u64>,
    rereading: std::collections::HashMap<String, (u64, bool)>,
}

impl Reads {
    pub fn ticket(&mut self) -> u64 {
        self.next += 1;
        self.next
    }

    pub fn issued(&self) -> u64 {
        self.next
    }

    pub fn accept(&mut self, id: &str, ticket: u64) -> bool {
        if self.applied.get(id).is_some_and(|newest| *newest > ticket) {
            return false;
        }
        self.applied.insert(id.to_string(), ticket);
        true
    }

    pub fn want(&mut self, id: &str) -> bool {
        match self.rereading.get_mut(id) {
            Some(pending) => {
                pending.1 = true;
                false
            }
            None => true,
        }
    }

    pub fn started(&mut self, id: &str, ticket: u64) {
        self.rereading.insert(id.to_string(), (ticket, false));
    }

    pub fn landed(&mut self, id: &str, ticket: u64) -> bool {
        match self.rereading.get(id).copied() {
            Some((asked, again)) if ticket == asked => {
                self.rereading.remove(id);
                again
            }
            _ => false,
        }
    }

    pub fn rekey(&mut self, old: &str, new: &str) {
        if let Some(v) = self.applied.remove(old) {
            let kept = self.applied.entry(new.to_string()).or_insert(v);
            *kept = (*kept).max(v);
        }
        if let Some(v) = self.rereading.remove(old) {
            self.rereading.entry(new.to_string()).or_insert(v);
        }
    }

    pub fn keep_only(&mut self, shown: &[String]) {
        self.rereading.retain(|k, _| shown.contains(k));
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum GoalAct {
    Set,
    Edit,
    Pause,
    Resume,
    Clear,
    Budget,
}

impl GoalAct {
    pub fn action(self) -> &'static str {
        match self {
            GoalAct::Set => "goal-set",
            GoalAct::Edit => "goal-edit",
            GoalAct::Pause => "goal-pause",
            GoalAct::Resume => "goal-resume",
            GoalAct::Clear => "goal-clear",
            GoalAct::Budget => "goal-budget",
        }
    }

    pub fn doing(self) -> &'static str {
        match self {
            GoalAct::Set => "Setting the goal",
            GoalAct::Edit => "Changing the goal",
            GoalAct::Pause => "Pausing the goal",
            GoalAct::Resume => "Resuming the goal",
            GoalAct::Clear => "Clearing the goal",
            GoalAct::Budget => "Changing the token budget",
        }
    }

    pub fn title(self) -> &'static str {
        match self {
            GoalAct::Budget => "Token budget",
            GoalAct::Edit => "Edit goal",
            _ => "Set goal",
        }
    }

    pub fn takes_text(self) -> bool {
        matches!(self, GoalAct::Set | GoalAct::Edit | GoalAct::Budget)
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct GoalControls {
    pub editable: bool,
    pub controllable: bool,
}

pub fn goal_controls(x: &Session) -> GoalControls {
    let editable = !x.read_only && x.capabilities.goal_set;
    GoalControls { editable, controllable: editable && x.capabilities.goal }
}

pub fn goal_buttons(goal: Option<&Goal>, can: GoalControls) -> Vec<(GoalAct, &'static str)> {
    if !can.editable {
        return Vec::new();
    }
    let Some(goal) = goal else { return vec![(GoalAct::Set, "Set goal")] };
    let mut out = vec![(GoalAct::Edit, "Edit")];
    if can.controllable {
        match goal.status.as_str() {
            "active" => out.push((GoalAct::Pause, "Pause")),
            "paused" => out.push((GoalAct::Resume, "Resume")),
            _ => {}
        }
        out.push((GoalAct::Budget, if goal.token_budget.is_none() { "Set budget" } else { "Budget" }));
    }
    out.push((GoalAct::Clear, "Clear"));
    out
}

pub fn can_save(act: GoalAct, text: &str, budget: Option<i64>) -> bool {
    let trimmed = text.trim();
    match act {
        GoalAct::Budget if trimmed.is_empty() => budget.is_some(),
        GoalAct::Budget => trimmed.parse::<i64>().is_ok_and(|n| n >= 0),
        GoalAct::Set | GoalAct::Edit => !trimmed.is_empty(),
        _ => true,
    }
}

#[derive(Default)]
pub struct Overlay {
    sessions: std::collections::HashMap<String, std::collections::HashMap<&'static str, u64>>,
}

impl Overlay {
    pub fn hold(&mut self, id: &str) {
        self.sessions.entry(id.to_string()).or_default();
    }

    pub fn keep(&mut self, held: &[String]) {
        self.sessions.retain(|id, _| held.contains(id));
    }

    pub fn rekey(&mut self, old: &str, new: &str) -> bool {
        let kinds = self.sessions.remove(old);
        if self.sessions.contains_key(new) {
            return true;
        }
        if let Some(kinds) = kinds {
            self.sessions.insert(new.to_string(), kinds);
        }
        false
    }

    pub fn stream(&mut self, id: &str, kind: &'static str, shown: bool, at: u64) -> bool {
        if !shown {
            if let Some(kinds) = self.sessions.get_mut(id) {
                kinds.remove(kind);
            }
            return false;
        }
        self.sessions.entry(id.to_string()).or_default().insert(kind, at);
        true
    }

    pub fn holds(&self, id: &str, kind: &str) -> bool {
        self.sessions.get(id).is_some_and(|kinds| kinds.contains_key(kind))
    }

    pub fn streamed_since(&self, id: &str, kind: &str, ticket: u64) -> bool {
        self.sessions.get(id).and_then(|kinds| kinds.get(kind)).is_some_and(|at| ticket <= *at)
    }
}

pub fn streamed(events: &[crate::wire::Event]) -> Vec<crate::wire::Streamed> {
    events.iter().filter_map(|e| if let crate::wire::Event::State(x) = e { Some(x.clone()) } else { None }).collect()
}

pub fn lifecycle(x: &Session) -> Lifecycle {
    if x.read_only { Lifecycle::default() } else { x.capabilities.lifecycle }
}

pub fn lifecycle_any(l: Lifecycle) -> bool {
    l.status || l.logs || l.stop || l.resume
}

pub fn lifecycle_title(x: &Session) -> &'static str {
    if x.runtime_kind.as_deref() == Some("claude-bg") { "Background session" } else { "Process" }
}

pub fn lifecycle_detail(x: &Session) -> &'static str {
    match x.runtime_kind.as_deref() {
        Some("claude-bg") => "A Claude Code background job.",
        Some("claude-host") => "Agenthail runs this session. Logs shows what its Claude process wrote to stderr; Stop closes it, and the next message or Resume opens it again.",
        _ => "Controls for this session's process.",
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum LifecycleAct {
    Status,
    Logs,
    Stop,
    Resume,
}

impl LifecycleAct {
    pub const ALL: [LifecycleAct; 4] = [LifecycleAct::Status, LifecycleAct::Logs, LifecycleAct::Stop, LifecycleAct::Resume];

    pub fn name(self) -> &'static str {
        match self {
            LifecycleAct::Status => "status",
            LifecycleAct::Logs => "logs",
            LifecycleAct::Stop => "stop",
            LifecycleAct::Resume => "resume",
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            LifecycleAct::Status => "Status",
            LifecycleAct::Logs => "Logs",
            LifecycleAct::Stop => "Stop",
            LifecycleAct::Resume => "Resume",
        }
    }

    pub fn offered(self, l: Lifecycle) -> bool {
        match self {
            LifecycleAct::Status => l.status,
            LifecycleAct::Logs => l.logs,
            LifecycleAct::Stop => l.stop,
            LifecycleAct::Resume => l.resume,
        }
    }

    pub fn changes(self) -> bool {
        matches!(self, LifecycleAct::Stop | LifecycleAct::Resume)
    }

    pub fn done(self) -> Option<&'static str> {
        match self {
            LifecycleAct::Stop => Some("Stopped."),
            LifecycleAct::Resume => Some("Resumed."),
            _ => None,
        }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct LifecycleResult {
    #[serde(default)]
    pub id: Option<String>,
    #[serde(default)]
    pub session_id: Option<String>,
    #[serde(default)]
    pub state: Option<String>,
    #[serde(default)]
    pub action: Option<String>,
    #[serde(default)]
    pub output: Option<String>,
    #[serde(default)]
    pub unchanged: Option<bool>,
}

impl LifecycleResult {
    pub fn line(&self) -> String {
        let state = self.state.as_deref().filter(|s| !s.is_empty()).map(|s| format!("State: {s}"));
        let job = self.id.as_deref().filter(|s| !s.is_empty()).map(|s| format!("Job {s}"));
        [state, job].into_iter().flatten().collect::<Vec<_>>().join(" · ")
    }
}

pub const RESUME_UNCHANGED: &str = "Already running, nothing to resume.";
pub const NO_RUNS: &str = "No observed Claude runs.";
pub const NO_LOGS: &str = "No log output.";

impl Client {
    pub fn goal(&self, session: &str, act: GoalAct, text: &str) -> Result<bool, RespondError> {
        let payload = if act.takes_text() { json!({"sessionId": session, "message": text.trim()}) } else { json!({"sessionId": session}) };
        let reply = self.action(act.action(), payload)?;
        Ok(crate::composer::Delivered::read(&reply) != crate::composer::Delivered::Unconfirmed)
    }

    pub fn lifecycle(&self, session: &str, act: LifecycleAct) -> Result<LifecycleResult, RespondError> {
        let reply = crate::catalog::confirmed(self.action(&format!("session-lifecycle-{}", act.name()), json!({"sessionId": session}))?)?;
        Ok(serde_json::from_value(reply["result"].clone()).unwrap_or_default())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};

    fn session(v: Value) -> Session {
        serde_json::from_value(v).unwrap()
    }

    fn noon() -> OffsetDateTime {
        parse_time("2026-11-26T12:00:00Z").unwrap()
    }

    fn goal(v: Value) -> Goal {
        serde_json::from_value(v).unwrap()
    }

    #[test]
    fn a_goal_reads_its_status_usage_and_times_as_the_mac_app_does() {
        let g = goal(json!({"objective": "Ship it", "status": "budgetLimited", "timeUsedSeconds": 3725, "tokensUsed": 12400, "tokenBudget": 1_200_000, "createdAt": "2026-11-26T09:05:00Z", "updatedAt": "2026-11-20T21:30:00Z", "reason": ""}));
        assert_eq!(g.display_status(), "Needs you");
        assert!(g.needs_attention());
        assert_eq!(g.reason(), None);
        assert_eq!(g.usage(), "1h 2m elapsed · 12K of 1.2M tokens");
        let now = noon();
        assert_eq!(g.times(now, Clock::Twelve, |_| 0), "Created 9:05 AM, updated Nov 20, 2026, 9:30 PM");
        assert_eq!(g.times(now, Clock::TwentyFour, |_| 0), "Created 09:05, updated Nov 20, 2026, 21:30");
        let zero = goal(json!({"objective": "x", "status": "active", "createdAt": "0001-01-01T00:00:00Z"}));
        assert_eq!(zero.times(now, Clock::Twelve, |_| 0), "");
        assert_eq!(goal(json!({"objective": "x", "status": "weird"})).display_status(), "Weird");
        assert_eq!(goal(json!({"objective": "x", "status": "active", "tokenBudget": 5000})).usage(), "5K token budget");
        assert_eq!(goal(json!({"objective": "x", "status": "active", "timeUsedSeconds": 59, "tokensUsed": 999})).usage(), "59s elapsed · 999 tokens");
        assert_eq!(duration(61), "1m 1s");
        assert_eq!((compact(1_000_000), compact(15_600), compact(1_050), compact(0)), ("1M".into(), "16K".into(), "1.1K".into(), "0".into()));
    }

    #[test]
    fn claude_answers_set_and_status_and_codex_everything() {
        let claude = session(json!({"id": "c", "capabilities": {"send": true, "goalSet": true, "goalStatus": true}}));
        let codex = session(json!({"id": "x", "capabilities": {"send": true, "goalSet": true, "goal": true}}));
        let active = goal(json!({"objective": "o", "status": "active"}));
        let paused = goal(json!({"objective": "o", "status": "paused", "tokenBudget": 10}));
        let labels = |g: Option<&Goal>, x: &Session| goal_buttons(g, goal_controls(x)).into_iter().map(|b| b.1).collect::<Vec<_>>();
        assert_eq!(labels(None, &claude), ["Set goal"]);
        assert_eq!(labels(Some(&active), &claude), ["Edit", "Clear"]);
        assert_eq!(labels(Some(&active), &codex), ["Edit", "Pause", "Set budget", "Clear"]);
        assert_eq!(labels(Some(&paused), &codex), ["Edit", "Resume", "Budget", "Clear"]);
        let archived = session(json!({"id": "x", "archived": true, "capabilities": {"send": true, "goalSet": true, "goal": true}}));
        assert_eq!(labels(Some(&active), &archived), ["Edit", "Pause", "Set budget", "Clear"], "archiving files a session away without closing it");
        let closed = session(json!({"id": "x", "readOnly": true, "capabilities": {"goalSet": true, "goal": true}}));
        assert!(labels(Some(&active), &closed).is_empty() && labels(None, &closed).is_empty());
        let none = session(json!({"id": "n", "capabilities": {"send": true}}));
        assert!(labels(None, &none).is_empty());
    }

    #[test]
    fn a_budget_is_a_whole_number_or_empty_to_remove_one() {
        assert!(can_save(GoalAct::Budget, " 5000 ", None));
        assert!(!can_save(GoalAct::Budget, "-1", Some(3)));
        assert!(!can_save(GoalAct::Budget, "5k", Some(3)));
        assert!(!can_save(GoalAct::Budget, "", None));
        assert!(can_save(GoalAct::Budget, " ", Some(3)));
        assert!(!can_save(GoalAct::Edit, "  ", None) && can_save(GoalAct::Set, "Ship", None));
    }

    #[test]
    fn metadata_reads_the_goal_runs_and_named_warnings() {
        let m = metadata(&json!({"goal": {"objective": "Ship", "status": "active"}, "claudeRuns": [{"recordPath": "/r/1.json", "jobId": "j1", "runType": "bg", "providerState": "", "createdAt": "2026-11-26T10:00:00Z", "updatedAt": "0001-01-01T00:00:00Z"}], "errors": {"model": " timed out ", "claudeSubagents": "", "zzz": "x"}}));
        assert_eq!(m.goal.as_ref().map(|g| g.objective.as_str()), Some("Ship"));
        let runs = m.runs.unwrap();
        assert_eq!(runs[0].lines(noon(), Clock::TwentyFour, |_| 0), ["Type: bg", "Created 10:00", "Record: /r/1.json"]);
        assert_eq!(m.warnings, ["Subagents", "Current model: timed out", "zzz: x"]);
        let empty = metadata(&json!({"goal": {"objective": "", "status": ""}, "claudeRuns": []}));
        assert_eq!((empty.goal, empty.runs, empty.warnings.len()), (None, Some(vec![]), 0));
        let codex = metadata(&json!({"goal": null}));
        assert_eq!((codex.runs, codex.goal_failed), (None, false));
        let failed = metadata(&json!({"errors": {"goal": "timed out"}}));
        assert!(failed.goal.is_none() && failed.goal_failed);
        assert_eq!(failed.warnings, ["Goal: timed out"]);
    }

    #[test]
    fn a_failed_read_keeps_what_was_known_and_never_reads_as_no_goal() {
        let known = metadata(&json!({"goal": {"objective": "Ship", "status": "active"}, "claudeRuns": []}));
        let field = metadata(&json!({"errors": {"goal": "timed out"}})).after(Some(&known));
        assert_eq!(field.goal.as_ref().map(|g| g.objective.as_str()), Some("Ship"));
        let cleared = metadata(&json!({"goal": null})).after(Some(&known));
        assert_eq!(cleared.goal, None, "a read that answered drops a cleared goal");
        let whole = Metadata::failed(Some(&known), "connection refused".into());
        assert!(whole.goal_failed && whole.goal.is_some() && whole.runs == Some(vec![]));
        assert_eq!(whole.warnings, ["connection refused"]);
        let first = Metadata::failed(None, "connection refused".into());
        assert!(first.goal_failed && first.goal.is_none());
    }

    #[test]
    fn an_older_read_never_replaces_a_newer_one() {
        let mut reads = Reads::default();
        let (old, new) = (reads.ticket(), reads.ticket());
        assert!(reads.accept("s", new));
        assert!(!reads.accept("s", old));
        assert!(reads.accept("s", new) && reads.accept("other", old));
    }

    #[test]
    fn goal_changes_during_a_reread_ask_for_one_more_read_only() {
        let mut reads = Reads::default();
        assert!(reads.want("s"));
        let first = reads.ticket();
        reads.started("s", first);
        assert!(!reads.want("s") && !reads.want("s"), "events while a reread runs are coalesced");
        let before = first - 1;
        let other = reads.ticket();
        assert!(!reads.landed("s", before), "an earlier read doesn't settle the reread");
        assert!(!reads.landed("s", other), "nor does a newer read from somewhere else");
        assert!(!reads.want("s"));
        assert!(reads.landed("s", first), "one more read follows the coalesced events");
        assert!(reads.want("s"));
        let second = reads.ticket();
        reads.started("s", second);
        assert!(!reads.landed("s", second));
        assert!(reads.want("s"));
        reads.started("s", reads.next + 1);
        reads.keep_only(&["other".to_string()]);
        assert!(reads.want("s"), "switching sessions forgets a pending reread");
    }

    #[test]
    fn switching_away_and_back_still_refuses_an_older_read() {
        let mut reads = Reads::default();
        let (old, new) = (reads.ticket(), reads.ticket());
        assert!(reads.accept("a", new));
        reads.keep_only(&["b".to_string()]);
        reads.keep_only(&["a".to_string()]);
        assert!(!reads.accept("a", old));
    }

    #[test]
    fn a_stream_wins_over_reads_asked_before_it_and_not_after() {
        let mut o = Overlay::default();
        o.hold("a");
        assert!(!o.stream("a", "goal", false, 1), "a stream nothing shows is ignored");
        assert!(o.stream("a", "goal", true, 5));
        assert!(o.streamed_since("a", "goal", 5) && o.streamed_since("a", "goal", 3), "a read asked before the stream keeps the streamed goal");
        assert!(!o.streamed_since("a", "goal", 6), "a read asked after it is newer");
        assert!(!o.streamed_since("a", "context", 1));
        o.hold("a");
        assert!(o.holds("a", "goal"), "holding again keeps what streamed");
    }

    #[test]
    fn each_shown_session_streams_on_its_own_until_its_window_closes() {
        let mut o = Overlay::default();
        assert!(o.stream("a", "goal", true, 1) && o.stream("b", "context", true, 1), "two windows, two streams");
        assert!(o.holds("a", "goal") && o.holds("b", "context") && !o.holds("a", "context"));
        o.keep(&["a".to_string()]);
        assert!(!o.holds("b", "context"), "a closed window's session is let go");
        assert!(!o.stream("b", "goal", false, 2) && !o.holds("b", "goal"));
    }

    #[test]
    fn a_hidden_session_s_missed_stream_lets_reads_through_again() {
        let mut o = Overlay::default();
        assert!(o.stream("a", "goal", true, 4));
        assert!(!o.stream("a", "goal", false, 6));
        assert!(!o.streamed_since("a", "goal", 1), "a value streamed while hidden was missed, so the next read wins");
    }

    #[test]
    fn a_rekey_onto_a_held_session_keeps_its_streams_and_says_so() {
        let mut o = Overlay::default();
        o.stream("a", "goal", true, 3);
        o.stream("b", "goal", true, 7);
        o.stream("b", "context", true, 2);
        assert!(o.rekey("b", "a"), "a is held, so the rekey collides");
        assert!(o.streamed_since("a", "goal", 3) && !o.streamed_since("a", "goal", 4) && !o.holds("a", "context") && !o.holds("b", "goal"));
        o.hold("e");
        assert!(o.rekey("gone", "e"), "a collision even when the old id was already let go");
        o.stream("c", "goal", true, 5);
        assert!(!o.rekey("c", "d"));
        assert!(o.streamed_since("d", "goal", 5), "a rekey onto a free id carries its streams");
    }

    #[test]
    fn a_read_keeps_the_streamed_goal_and_takes_the_rest() {
        let shown = Metadata { goal: Some(Goal { objective: "streamed".into(), ..Default::default() }), ..Default::default() };
        let read = Metadata { goal: Some(Goal { objective: "older".into(), ..Default::default() }), goal_failed: true, runs: Some(Vec::new()), warnings: vec!["Claude runs: slow".into()] };
        let kept = read.keep_goal(Some(&shown));
        assert_eq!(kept.goal.map(|g| g.objective).as_deref(), Some("streamed"));
        assert!(!kept.goal_failed && kept.runs.is_some() && kept.warnings.len() == 1);
    }

    #[test]
    fn streamed_context_and_goal_values_are_picked_out_of_a_batch() {
        use crate::wire::{Event, Streamed};
        let item = Event::Upsert(crate::Item { kind: "message".into(), ..Default::default() });
        let goal = Streamed::Goal(Some(Goal { objective: "ship it".into(), ..Default::default() }));
        assert_eq!(streamed(&[item.clone(), Event::State(goal.clone()), Event::State(Streamed::Context(None))]), vec![goal, Streamed::Context(None)]);
        assert!(streamed(&[item, Event::Done]).is_empty());
    }

    #[test]
    fn lifecycle_is_offered_from_capabilities_and_never_on_a_closed_session() {
        let host = session(json!({"id": "c", "runtimeKind": "claude-host", "capabilities": {"send": true, "lifecycle": {"status": true, "logs": true, "stop": true, "resume": true}}}));
        let l = lifecycle(&host);
        assert!(lifecycle_any(l) && LifecycleAct::ALL.iter().all(|a| a.offered(l)));
        assert_eq!(lifecycle_title(&host), "Process");
        assert!(lifecycle_detail(&host).starts_with("Agenthail runs this session."));
        let bg = session(json!({"id": "b", "runtimeKind": "claude-bg", "capabilities": {"lifecycle": {"stop": true}}}));
        assert_eq!((lifecycle_title(&bg), lifecycle_detail(&bg)), ("Background session", "A Claude Code background job."));
        assert!(LifecycleAct::Stop.offered(lifecycle(&bg)) && !LifecycleAct::Logs.offered(lifecycle(&bg)));
        let closed = session(json!({"id": "b", "readOnly": true, "capabilities": {"lifecycle": {"stop": true, "resume": true}}}));
        assert!(!lifecycle_any(lifecycle(&closed)));
        let archived = session(json!({"id": "b", "archived": true, "capabilities": {"lifecycle": {"stop": true}}}));
        assert!(LifecycleAct::Stop.offered(lifecycle(&archived)));
        assert_eq!((LifecycleAct::Stop.done(), LifecycleAct::Status.done()), (Some("Stopped."), None));
        let adopt_only = session(json!({"id": "a", "capabilities": {"lifecycle": {"adopt": true}}}));
        assert!(!lifecycle_any(lifecycle(&adopt_only)));
    }

    #[test]
    fn goal_and_lifecycle_send_the_bodies_their_handlers_read() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"ok":true,"result":{}}"#),
            (202, r#"{"ok":true,"status":"submitted"}"#),
            (200, r#"{"ok":true}"#),
            (200, r#"{"ok":true,"result":{"id":"job-1","sessionId":"s1","state":"running","action":"status"}}"#),
            (200, r#"{"ok":true,"result":{"output":"line\n"}}"#),
            (202, r#"{"ok":true,"status":"submitted"}"#),
            (409, r#"{"error":{"code":"conflict","message":"not running"}}"#),
        ]));
        assert!(client.goal("h::s1", GoalAct::Budget, " 5000 ").unwrap());
        assert_eq!(seen.recv().unwrap().1, json!({"action": "goal-budget", "sessionId": "h::s1", "message": "5000"}));
        assert!(!client.goal("s1", GoalAct::Pause, "ignored").unwrap(), "a submitted change isn't confirmed");
        assert_eq!(seen.recv().unwrap().1, json!({"action": "goal-pause", "sessionId": "s1"}));
        assert!(client.goal("s1", GoalAct::Clear, "").unwrap());
        assert_eq!(seen.recv().unwrap().1, json!({"action": "goal-clear", "sessionId": "s1"}));
        let status = client.lifecycle("s1", LifecycleAct::Status).unwrap();
        assert_eq!(seen.recv().unwrap().1, json!({"action": "session-lifecycle-status", "sessionId": "s1"}));
        assert_eq!(status.line(), "State: running · Job job-1");
        assert_eq!(client.lifecycle("s1", LifecycleAct::Logs).unwrap().output.as_deref(), Some("line\n"));
        assert!(client.lifecycle("s1", LifecycleAct::Stop).unwrap_err().maybe_delivered);
        let refused = client.lifecycle("s1", LifecycleAct::Resume).unwrap_err();
        assert!(!refused.maybe_delivered && refused.message.contains("not running"));
        assert_eq!(LifecycleResult::default().line(), "");
    }
}
