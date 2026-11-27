use std::collections::BTreeMap;

use serde::{Deserialize, Deserializer};
use serde_json::json;

use crate::catalog::{Client, Session, confirmed};
use crate::prefs::{Failure, read, write_failure};

pub(crate) fn listed<'de, D: Deserializer<'de>, T: Deserialize<'de>>(d: D) -> Result<Vec<T>, D::Error> {
    Ok(Option::<Vec<T>>::deserialize(d)?.unwrap_or_default())
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Note {
    #[serde(default)]
    pub message: String,
    #[serde(default)]
    pub remediation: Option<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Runtime {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub reachable: bool,
    #[serde(default)]
    pub durable: bool,
    #[serde(default)]
    pub problem: Option<String>,
    #[serde(default)]
    pub detail: Option<String>,
    #[serde(default)]
    pub remediation: Option<String>,
    #[serde(default, deserialize_with = "listed")]
    pub notes: Vec<Note>,
}

impl Runtime {
    pub fn advice(&self) -> Vec<String> {
        let mut lines = Vec::new();
        if self.problem.as_deref().is_some_and(|p| !p.is_empty()) {
            let fix = self.remediation.as_ref().map(|r| format!("Fix: {r}"));
            lines.push([self.detail.clone(), fix].into_iter().flatten().collect::<Vec<_>>().join(". "));
        }
        for note in &self.notes {
            lines.push(match &note.remediation {
                Some(fix) => format!("{}. Fix: {fix}", note.message),
                None => note.message.clone(),
            });
        }
        lines
    }

    pub fn availability(&self) -> String {
        if !self.reachable {
            return format!("{} is not connected.", self.name);
        }
        if self.durable { format!("{} stays available in the background.", self.name) } else { format!("{} is open now but will not restart automatically.", self.name) }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Surface {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub connected: bool,
    #[serde(default)]
    pub error: Option<String>,
    #[serde(default)]
    pub health: String,
    #[serde(default)]
    pub health_detail: Option<String>,
    #[serde(default)]
    pub runtime: Option<Runtime>,
    #[serde(default)]
    pub repair_action: Option<String>,
    #[serde(default)]
    pub repair_label: Option<String>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Health {
    Healthy,
    Degraded,
    SigninOff,
    SigninUnavailable,
    AgentOff,
    AgentUnavailable,
    Unavailable,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Tone {
    Done,
    Failed,
    Quiet,
}

impl Health {
    fn of(health: &str) -> Health {
        match health {
            "healthy" => Health::Healthy,
            "degraded" => Health::Degraded,
            "signin_off" => Health::SigninOff,
            "signin_unavailable" => Health::SigninUnavailable,
            "agent_off" => Health::AgentOff,
            "agent_unavailable" => Health::AgentUnavailable,
            _ => Health::Unavailable,
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            Health::Healthy => "Healthy",
            Health::Degraded => "Needs attention",
            Health::SigninOff => "Sign-in off",
            Health::SigninUnavailable => "No browser here",
            Health::AgentOff => "Off",
            Health::AgentUnavailable => "Not installed here",
            Health::Unavailable => "Unavailable",
        }
    }

    pub fn tone(self) -> Tone {
        match self {
            Health::Healthy => Tone::Done,
            Health::Degraded | Health::Unavailable => Tone::Failed,
            _ => Tone::Quiet,
        }
    }

    fn stated_limit(self) -> bool {
        matches!(self, Health::SigninOff | Health::SigninUnavailable | Health::AgentOff | Health::AgentUnavailable)
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Fix {
    BrowserSignIn,
    Repair { action: String, label: String },
}

impl Fix {
    pub fn label(&self) -> &str {
        match self {
            Fix::BrowserSignIn => "Open Browser Sign-in",
            Fix::Repair { label, .. } => label,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Problem {
    pub sentence: String,
    pub fix: Option<Fix>,
    pub details: Vec<String>,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Counts {
    pub working: i64,
    pub open: i64,
    pub recent: i64,
    pub queued: i64,
}

pub type Tally = BTreeMap<String, Counts>;

pub fn tally(sessions: &[Session]) -> Tally {
    let mut out = Tally::new();
    for s in sessions {
        let c = out.entry(s.surface.clone()).or_default();
        if s.status == "busy" && s.waiting_on.as_ref().is_none_or(|w| w.is_null()) {
            c.working += 1;
        }
        if s.open {
            c.open += 1;
        }
        if s.current_reason.as_deref() == Some("recent") {
            c.recent += 1;
        }
        c.queued += s.queue_count;
    }
    out
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Card {
    pub surface: String,
    pub name: String,
    pub health: Health,
    pub detail: String,
    pub availability: Option<String>,
    pub advice: Vec<String>,
    pub problem: Option<Problem>,
    pub working: i64,
    pub current: i64,
    pub current_window: String,
    pub queued: i64,
}

impl Card {
    pub fn quiet(&self) -> bool {
        self.health == Health::Healthy || self.health.stated_limit()
    }
}

pub fn cards(surfaces: &[Surface], tally: &Tally, codex_recent_hours: i64) -> Vec<Card> {
    surfaces
        .iter()
        .map(|s| {
            let counts = tally.get(&s.name).copied().unwrap_or_default();
            let claude = s.name == "claude";
            let problem = problem(s);
            let detail = match &problem {
                Some(p) => p.sentence.clone(),
                None => raw(s).into_iter().next().unwrap_or_else(|| "Ready".into()),
            };
            Card {
                surface: s.name.clone(),
                name: crate::projects::surface_label(&s.name),
                health: Health::of(&s.health),
                detail,
                availability: s.runtime.as_ref().map(Runtime::availability),
                advice: s.runtime.as_ref().map(Runtime::advice).unwrap_or_default(),
                problem,
                working: counts.working,
                current: if claude { counts.open } else { counts.recent },
                current_window: if claude { "Open now".into() } else { format!("Past {codex_recent_hours}h") },
                queued: counts.queued,
            }
        })
        .collect()
}

fn raw(s: &Surface) -> Vec<String> {
    [&s.health_detail, &s.error].into_iter().flatten().filter(|t| !t.is_empty()).cloned().collect()
}

pub fn problem(s: &Surface) -> Option<Problem> {
    let raw = raw(s);
    let text = raw.first().cloned().unwrap_or_default();
    let name = crate::projects::surface_label(&s.name);
    let mut details = raw;
    if let Some(runtime) = &s.runtime {
        details.push(runtime.availability());
        details.extend(runtime.advice());
    }
    let mut seen = Vec::new();
    details.retain(|d| {
        let fresh = !seen.contains(d);
        seen.push(d.clone());
        fresh
    });
    let consumer = text.split_once(": ").map(|(c, _)| c.to_string()).unwrap_or_else(|| name.clone());
    let made = |sentence: String, fix: Option<Fix>| Some(Problem { sentence, fix, details: details.clone() });
    match s.health.as_str() {
        "healthy" => return None,
        "signin_off" => return made(format!("{consumer} can't sign in: browser sign-in is off."), Some(Fix::BrowserSignIn)),
        "signin_unavailable" => return made(format!("{consumer} can't sign in: this computer has no browser."), Some(Fix::BrowserSignIn)),
        "agent_off" => return made(format!("{name} is turned off on this Mac."), None),
        "agent_unavailable" => return made(format!("{name} is not installed on this Mac."), None),
        _ => {}
    }
    let lower = text.to_lowercase();
    if text.contains("agenthail signin use") {
        return made(format!("{consumer} can't sign in: {}.", signin_reason(&text)), Some(Fix::BrowserSignIn));
    }
    if lower.find("notion").is_some_and(|at| lower[at..].contains("401") || lower[at..].contains("context")) {
        return made("Notion is not signed in on this Mac.".into(), Some(Fix::BrowserSignIn));
    }
    if lower.contains("cookie bridge") {
        return made("Claude Code needs to reconnect to your signed-in account.".into(), None);
    }
    let fix = s.repair_action.as_ref().filter(|a| !a.is_empty()).map(|action| Fix::Repair { action: action.clone(), label: repair_label(action, s.repair_label.as_deref()) });
    match s.repair_action.as_deref() {
        Some("codex-launch") => return made("Codex isn't connected: launch it through Agenthail to reach its sessions.".into(), fix),
        Some("runtime-ensure") => {
            let sentence = if lower.contains("reachable but not supervised") { "The Codex background service will stop after a restart." } else { "The Codex background service isn't running." };
            return made(sentence.into(), fix);
        }
        _ => {}
    }
    if s.connected {
        return made(format!("{name} is connected but reports a problem."), fix);
    }
    made(format!("{name} isn't available right now."), fix)
}

fn signin_reason(text: &str) -> String {
    const BROWSERS: [&str; 10] = ["Safari", "Chrome", "Arc", "Brave", "Edge", "Firefox", "Chromium", "Vivaldi", "Orion", "Opera"];
    let browser = BROWSERS.iter().find(|b| text.contains(*b)).copied();
    let lower = text.to_lowercase();
    if lower.contains("no cookie store") || lower.contains("no saved sign-in") {
        return format!("{} has no saved sign-in", browser.unwrap_or("the browser"));
    }
    if lower.contains("not permitted") || lower.contains("permission") {
        return format!("Agenthail isn't allowed to read {}'s sign-in", browser.unwrap_or("the browser"));
    }
    format!("{}'s sign-in can't be read", browser.unwrap_or("the browser"))
}

fn repair_label(action: &str, daemon_label: Option<&str>) -> String {
    match action {
        "codex-launch" => "Launch Codex".into(),
        "runtime-ensure" => "Start Codex Service".into(),
        _ => daemon_label.filter(|l| !l.is_empty()).unwrap_or("Fix").into(),
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Grant {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub target: Option<String>,
    #[serde(default)]
    pub target_name: Option<String>,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub pane: String,
    #[serde(default)]
    pub url: String,
}

impl Grant {
    pub fn id(&self) -> String {
        format!("{}{}", self.name, self.target.as_deref().unwrap_or_default())
    }

    pub fn title(&self) -> String {
        match &self.target_name {
            Some(t) => format!("{} of {t}", self.name),
            None => self.name.clone(),
        }
    }

    pub fn granted(&self) -> bool {
        self.state == "granted"
    }

    pub fn state_label(&self) -> String {
        match self.state.as_str() {
            "granted" => "Granted".into(),
            "missing" => "Missing".into(),
            "denied" => "Denied".into(),
            "target_not_running" => format!("Not checked: {} is not running", self.target_name.as_deref().unwrap_or("the app")),
            _ => "Unknown".into(),
        }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Permissions {
    #[serde(default)]
    pub supported: bool,
    #[serde(default)]
    pub holder: Option<String>,
    #[serde(default, deserialize_with = "listed")]
    pub grants: Vec<Grant>,
    #[serde(default)]
    pub problem: Option<String>,
}

impl Permissions {
    pub fn askable(&self) -> bool {
        self.grants.iter().any(|g| g.state == "missing")
    }
}

pub fn summary(daemon: &crate::devices::Daemon, surfaces: &[Surface], tally: &Tally, needs: usize) -> (String, bool) {
    if daemon.stale == Some(true) {
        let why = daemon.refresh_error.as_deref().filter(|e| !e.is_empty()).unwrap_or("Agent app refresh is temporarily unavailable.");
        return (format!("Showing cached data. {why}"), true);
    }
    if !daemon.running {
        return ("Start Agenthail to deliver work.".into(), false);
    }
    let count = |n: i64, noun: &str| format!("{n} {noun}{}", if n == 1 { "" } else { "s" });
    let connected = surfaces.iter().filter(|s| s.connected).count() as i64;
    let working: i64 = tally.values().map(|c| c.working).sum();
    let queued: i64 = tally.values().map(|c| c.queued).sum();
    let mut parts = vec![if connected == 0 { "No agent apps connected".to_string() } else { format!("{} connected", count(connected, "agent app")) }];
    if working > 0 {
        parts.push(format!("{working} working"));
    }
    if needs > 0 {
        parts.push(format!("{needs} need{} you", if needs == 1 { "s" } else { "" }));
    }
    if queued > 0 {
        parts.push(format!("{} queued", count(queued, "message")));
    }
    (parts.join(" · "), false)
}

pub const NO_APPS: &str = "No agent apps are set up.";
pub const ASK_NOTE: &str = "macOS lists Agenthail Daemon in these panes once it has asked.";

impl Client {
    pub fn permissions(&self) -> Result<Permissions, Failure> {
        read(self, "/api/v1/permissions", "read")
    }

    pub fn request_permissions(&self) -> Result<(), Failure> {
        self.post_json("/api/v1/permissions/request", json!({})).map(drop).map_err(|e| write_failure("settings", e))
    }

    pub fn repair(&self, action: &str) -> Result<(), Failure> {
        self.action(action, json!({})).and_then(confirmed).map_err(|e| write_failure("control", e))?;
        read::<serde_json::Value>(self, "/api/v1/snapshot?sessions=none&fresh=1", "read").map(drop)
    }
}

#[cfg(test)]
mod tests {
    use serde_json::{Value, json};

    use super::*;
    use crate::mock::{daemon, replies};

    fn surface(v: Value) -> Surface {
        serde_json::from_value(v).unwrap()
    }

    fn session(v: Value) -> Session {
        serde_json::from_value(v).unwrap()
    }

    #[test]
    fn a_healthy_surface_has_no_problem_and_counts_its_sessions() {
        let sessions = [
            session(json!({"id": "a", "surface": "claude", "status": "busy", "open": true, "queueCount": 2})),
            session(json!({"id": "b", "surface": "claude", "status": "busy", "open": true, "waitingOn": {"kind": "ask"}})),
            session(json!({"id": "c", "surface": "codex", "status": "idle", "currentReason": "recent", "queueCount": 1})),
            session(json!({"id": "d", "surface": "codex", "status": "busy", "open": true})),
        ];
        let surfaces = [surface(json!({"name": "claude", "connected": true, "health": "healthy"})), surface(json!({"name": "codex", "connected": true, "health": "healthy", "runtime": {"name": "Codex app server", "reachable": true, "durable": true}}))];
        let cards = cards(&surfaces, &tally(&sessions), 8);
        let claude = &cards[0];
        assert_eq!((claude.name.as_str(), claude.health.label(), claude.detail.as_str()), ("Claude", "Healthy", "Ready"));
        assert_eq!((claude.working, claude.current, claude.queued, claude.current_window.as_str()), (1, 2, 2, "Open now"));
        let codex = &cards[1];
        assert_eq!((codex.working, codex.current, codex.queued, codex.current_window.as_str()), (1, 1, 1, "Past 8h"));
        assert_eq!(codex.availability.as_deref(), Some("Codex app server stays available in the background."));
        assert!(codex.problem.is_none() && codex.quiet());
    }

    #[test]
    fn codex_problems_offer_the_native_repairs() {
        let launch = problem(&surface(json!({"name": "codex", "health": "unavailable", "error": "no app server", "repairAction": "codex-launch", "repairLabel": "Launch"}))).unwrap();
        assert_eq!(launch.sentence, "Codex isn't connected: launch it through Agenthail to reach its sessions.");
        assert_eq!(launch.fix, Some(Fix::Repair { action: "codex-launch".into(), label: "Launch Codex".into() }));
        let ensure = surface(json!({"name": "codex", "health": "degraded", "connected": true, "healthDetail": "Codex app server is reachable but not supervised", "repairAction": "runtime-ensure",
            "runtime": {"name": "Codex app server", "reachable": true, "durable": false, "problem": "unsupervised", "detail": "Not under launchd", "remediation": "agenthail runtime ensure", "notes": [{"problem": "x", "message": "Old build", "remediation": "update"}]}}));
        let p = problem(&ensure).unwrap();
        assert_eq!(p.sentence, "The Codex background service will stop after a restart.");
        assert_eq!(p.fix.as_ref().map(Fix::label), Some("Start Codex Service"));
        assert_eq!(p.details, vec!["Codex app server is reachable but not supervised", "Codex app server is open now but will not restart automatically.", "Not under launchd. Fix: agenthail runtime ensure", "Old build. Fix: update"]);
        let stopped = problem(&surface(json!({"name": "codex", "health": "degraded", "healthDetail": "down", "repairAction": "runtime-ensure"}))).unwrap();
        assert_eq!(stopped.sentence, "The Codex background service isn't running.");
        let other = problem(&surface(json!({"name": "devin", "health": "degraded", "connected": true, "error": "boom", "repairAction": "devin-fix", "repairLabel": "Reconnect Devin"}))).unwrap();
        assert_eq!((other.sentence.as_str(), other.fix.as_ref().map(Fix::label)), ("Devin is connected but reports a problem.", Some("Reconnect Devin")));
        let gone = problem(&surface(json!({"name": "devin", "health": "unavailable"}))).unwrap();
        assert_eq!((gone.sentence.as_str(), gone.fix), ("Devin isn't available right now.".into(), None));
    }

    #[test]
    fn sign_in_and_stated_limits_read_as_the_native_app_says() {
        let off = problem(&surface(json!({"name": "notion", "health": "signin_off", "healthDetail": "Notion: sign-in is off"}))).unwrap();
        assert_eq!((off.sentence.as_str(), off.fix), ("Notion can't sign in: browser sign-in is off.", Some(Fix::BrowserSignIn)));
        let none = problem(&surface(json!({"name": "notion", "health": "signin_unavailable"}))).unwrap();
        assert_eq!(none.sentence, "Notion can't sign in: this computer has no browser.");
        let blocked = problem(&surface(json!({"name": "notion", "health": "degraded", "error": "Notion: Chrome cookies not permitted; run agenthail signin use"}))).unwrap();
        assert_eq!(blocked.sentence, "Notion can't sign in: Agenthail isn't allowed to read Chrome's sign-in.");
        let empty = problem(&surface(json!({"name": "notion", "health": "degraded", "error": "Notion: Safari has no cookie store; agenthail signin use"}))).unwrap();
        assert_eq!(empty.sentence, "Notion can't sign in: Safari has no saved sign-in.");
        let lapsed = problem(&surface(json!({"name": "notion", "health": "degraded", "error": "notion returned 401"}))).unwrap();
        assert_eq!((lapsed.sentence.as_str(), lapsed.fix), ("Notion is not signed in on this Mac.", Some(Fix::BrowserSignIn)));
        let bridge = problem(&surface(json!({"name": "claude", "health": "degraded", "error": "Cookie bridge expired"}))).unwrap();
        assert_eq!((bridge.sentence.as_str(), bridge.fix), ("Claude Code needs to reconnect to your signed-in account.", None));
        let off = cards(&[surface(json!({"name": "devin", "health": "agent_off"}))], &Tally::new(), 8);
        assert_eq!((off[0].health.label(), off[0].health.tone(), off[0].detail.as_str()), ("Off", Tone::Quiet, "Devin is turned off on this Mac."));
        assert!(off[0].quiet());
        let missing = cards(&[surface(json!({"name": "devin", "health": "agent_unavailable"}))], &Tally::new(), 8);
        assert_eq!((missing[0].health.label(), missing[0].detail.as_str()), ("Not installed here", "Devin is not installed on this Mac."));
        assert_eq!(Health::of("weird").tone(), Tone::Failed);
    }

    #[test]
    fn the_summary_counts_what_the_native_overview_counts() {
        let daemon = |v: Value| -> crate::devices::Daemon { serde_json::from_value(v).unwrap() };
        let surfaces = [surface(json!({"name": "claude", "connected": true})), surface(json!({"name": "codex", "connected": false}))];
        let sessions = [session(json!({"id": "a", "surface": "claude", "status": "busy", "queueCount": 1}))];
        assert_eq!(summary(&daemon(json!({"running": true})), &surfaces, &tally(&sessions), 1), ("1 agent app connected · 1 working · 1 needs you · 1 message queued".into(), false));
        assert_eq!(summary(&daemon(json!({"running": true})), &surfaces[1..], &Tally::new(), 2).0, "No agent apps connected · 2 need you");
        assert_eq!(summary(&daemon(json!({})), &surfaces, &Tally::new(), 0).0, "Start Agenthail to deliver work.");
        assert_eq!(summary(&daemon(json!({"running": true, "stale": true, "refreshError": "codex timed out"})), &surfaces, &Tally::new(), 0), ("Showing cached data. codex timed out".into(), true));
        let base: crate::prefs::Base = serde_json::from_value(json!({"surfaces": null, "daemon": {"running": true}})).unwrap();
        assert!(base.surfaces.is_empty() && base.daemon.running);
    }

    #[test]
    fn grants_read_with_their_targets_and_states() {
        let p: Permissions = serde_json::from_value(json!({"supported": true, "holder": "Agenthail Daemon", "grants": [
            {"name": "Accessibility", "state": "granted", "pane": "Accessibility", "url": "x-apple.systempreferences:a"},
            {"name": "Automation", "target": "com.apple.Safari", "targetName": "Safari", "state": "target_not_running", "pane": "Automation", "url": "x-apple.systempreferences:b"},
            {"name": "Screen Recording", "state": "missing", "pane": "Screen Recording", "url": "x-apple.systempreferences:c"}]})).unwrap();
        assert_eq!(p.grants[1].title(), "Automation of Safari");
        assert_eq!(p.grants[1].id(), "Automationcom.apple.Safari");
        assert_eq!(p.grants.iter().map(Grant::state_label).collect::<Vec<_>>(), vec!["Granted", "Not checked: Safari is not running", "Missing"]);
        assert!(p.askable() && p.grants[0].granted());
        let none: Permissions = serde_json::from_value(json!({"supported": false, "grants": null})).unwrap();
        assert!(!none.supported && none.grants.is_empty() && !none.askable());
    }

    #[test]
    fn repairs_and_permission_asks_reach_the_daemon() {
        let (client, rx) = daemon(replies(&[(200, r#"{"supported":true,"grants":[]}"#), (202, r#"{"asked":true}"#), (200, "{}"), (200, "{}"), (403, r#"{"error":{"message":"no"}}"#), (409, r#"{"error":{"message":"No GUI session"}}"#)]));
        assert!(client.permissions().unwrap().supported);
        client.request_permissions().unwrap();
        client.repair("runtime-ensure").unwrap();
        let refused = client.repair("codex-launch").unwrap_err();
        assert!(refused.denied && refused.scope == "control");
        assert_eq!(client.request_permissions().unwrap_err().message, "No GUI session");
        let got: Vec<(String, Value)> = rx.try_iter().collect();
        assert_eq!(got.iter().map(|(p, _)| p.as_str()).collect::<Vec<_>>(), vec!["/api/v1/permissions", "/api/v1/permissions/request", "/api/v1/actions", "/api/v1/snapshot?sessions=none&fresh=1", "/api/v1/actions", "/api/v1/permissions/request"]);
        assert_eq!(got[2].1["action"], "runtime-ensure");
        assert_eq!(got[4].1["action"], "codex-launch");
    }
}
