use std::collections::BTreeMap;
use std::time::Duration;

use serde::Deserialize;
use serde::de::DeserializeOwned;
use serde_json::{Value, json};
use time::OffsetDateTime;

use crate::catalog::{Client, RespondError, ago};

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Person {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub set: bool,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Base {
    #[serde(default)]
    pub busy_delivery: String,
    #[serde(default)]
    pub codex_recent_hours: i64,
    #[serde(default)]
    pub person: Option<Person>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Dashboard {
    #[serde(default)]
    pub codex_recent_hours: i64,
    #[serde(default)]
    pub busy_delivery: String,
    #[serde(default)]
    pub resume_after_limit: Option<bool>,
    #[serde(default)]
    pub move_after_limit: Option<bool>,
}

impl Dashboard {
    pub fn resumes_after_limit(&self) -> bool {
        self.resume_after_limit != Some(false)
    }

    pub fn moves_after_limit(&self) -> bool {
        self.move_after_limit != Some(false)
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct NotifyStatus {
    #[serde(default)]
    pub enabled: bool,
    #[serde(default)]
    pub available: bool,
    #[serde(default)]
    pub authorization: String,
    #[serde(default)]
    pub authorized: bool,
    #[serde(default)]
    pub error: Option<String>,
}

impl NotifyStatus {
    pub fn denied(&self) -> bool {
        self.authorization == "denied"
    }

    pub fn problem(&self) -> Option<&str> {
        self.error.as_deref().filter(|e| !e.is_empty())
    }

    pub fn detail(&self) -> String {
        if let Some(error) = self.problem() {
            return error.to_string();
        }
        if self.denied() {
            return "Blocked in System Settings.".into();
        }
        if self.enabled { "On. Alerts appear in Notification Center.".into() } else { "Off.".into() }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Settings {
    #[serde(default)]
    pub dashboard: Dashboard,
    #[serde(default)]
    pub claude_default_root: String,
    #[serde(default)]
    pub claude_roots: Vec<String>,
    #[serde(default)]
    pub notifications: NotifyStatus,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RemoteRoot {
    #[serde(default)]
    pub root: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub at_startup: Option<bool>,
    #[serde(default)]
    pub problem: Option<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RemoteControl {
    #[serde(default)]
    pub available: bool,
    #[serde(default)]
    pub roots: Vec<RemoteRoot>,
}

impl RemoteControl {
    pub fn shown(&self) -> bool {
        self.available && !self.roots.is_empty()
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Window {
    #[serde(default)]
    pub tokens: Option<i64>,
    #[serde(default)]
    pub auto: Option<bool>,
    #[serde(default)]
    pub ignored: Option<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct IdleCompaction {
    #[serde(default)]
    pub enabled: bool,
    #[serde(default)]
    pub min_context_tokens: i64,
    #[serde(default)]
    pub pre_limit_percent: Option<i64>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CompactRoot {
    pub root: String,
    #[serde(default)]
    pub settings_path: String,
    #[serde(default)]
    pub auto_compact_window: Option<Window>,
    #[serde(default)]
    pub model_windows: BTreeMap<String, Window>,
    #[serde(default)]
    pub auto_compact_enabled: Option<bool>,
    #[serde(default)]
    pub idle_compaction: Option<bool>,
    #[serde(default)]
    pub agenthail_idle_compaction: IdleCompaction,
    #[serde(default)]
    pub overrides: BTreeMap<String, String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Hold {
    #[serde(default)]
    pub session_id: String,
    #[serde(default)]
    pub session: Option<String>,
    #[serde(default)]
    pub root: String,
    #[serde(default)]
    pub context_tokens: i64,
    #[serde(default)]
    pub reason: String,
    #[serde(default)]
    pub at: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Compaction {
    #[serde(default)]
    pub roots: Vec<CompactRoot>,
    #[serde(default)]
    pub idle_compaction_holds: Option<Vec<Hold>>,
}

impl Compaction {
    pub fn holds(&self) -> &[Hold] {
        self.idle_compaction_holds.as_deref().unwrap_or_default()
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Preference {
    pub id: String,
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub detail: String,
    #[serde(default)]
    pub value: bool,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Failure {
    pub message: String,
    pub scope: &'static str,
    pub denied: bool,
    pub uncertain: bool,
}

pub const NO_SETTINGS_SCOPE: &str = "This app's sign-in doesn't include the settings permission, so it can't read or change these here.";
pub const NO_CONTROL_SCOPE: &str = "This app's sign-in doesn't include the control permission, so it can't read or change these here.";

pub fn scope_message(scope: &str) -> &'static str {
    if scope == "control" { NO_CONTROL_SCOPE } else { NO_SETTINGS_SCOPE }
}

fn refused(scope: &'static str) -> Failure {
    Failure { message: format!("The daemon refused this app's sign-in here: it lacks the {scope} permission or is no longer valid."), scope, denied: true, uncertain: false }
}

impl Failure {
    pub fn new(message: impl Into<String>) -> Failure {
        Failure { message: message.into(), scope: "", denied: false, uncertain: false }
    }
}

fn read_failure(scope: &'static str, status: u16, body: &str) -> Failure {
    if status == 401 || status == 403 {
        return refused(scope);
    }
    let reply = serde_json::from_str::<Value>(body).unwrap_or_default();
    let message = reply["error"]["message"].as_str().map(str::to_string).unwrap_or_else(|| if status == 0 { body.to_string() } else { format!("status code {status}") });
    Failure::new(message)
}

fn write_failure(scope: &'static str, error: RespondError) -> Failure {
    if error.status == 401 || error.status == 403 {
        return refused(scope);
    }
    let uncertain = error.maybe_delivered;
    let message = if uncertain { format!("The daemon didn't confirm the change ({}). What shows is what it reports now.", error.message) } else { error.message };
    Failure { uncertain, ..Failure::new(message) }
}

fn read<T: DeserializeOwned>(client: &Client, path: &str, scope: &'static str) -> Result<T, Failure> {
    let value = client.get_reply(path).map_err(|(status, body)| read_failure(scope, status, &body))?;
    serde_json::from_value(value).map_err(|e| Failure::new(format!("the daemon's reply couldn't be read: {e}")))
}

fn reply<T: DeserializeOwned>(value: Value) -> Result<T, Failure> {
    serde_json::from_value(value).map_err(|e| Failure { uncertain: true, ..Failure::new(format!("the daemon's reply couldn't be read: {e}")) })
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Delivery {
    Busy(String),
    Hours(i64),
}

impl Delivery {
    pub fn over(&self, now: &Dashboard) -> (String, i64) {
        let busy = if now.busy_delivery.is_empty() { "queue".to_string() } else { now.busy_delivery.clone() };
        match self {
            Delivery::Busy(mode) => (mode.clone(), now.codex_recent_hours),
            Delivery::Hours(hours) => (busy, *hours),
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Notify {
    Enable,
    Disable,
    OpenSettings,
    Test,
}

impl Notify {
    pub fn action(self) -> &'static str {
        match self {
            Notify::Enable => "notifications-enable",
            Notify::Disable => "notifications-disable",
            Notify::OpenSettings => "notifications-settings",
            Notify::Test => "notifications-test",
        }
    }
}

impl Client {
    pub fn base_settings(&self) -> Result<Base, Failure> {
        read(self, "/api/v1/snapshot?sessions=none", "read")
    }

    pub fn settings(&self) -> Result<Settings, Failure> {
        read(self, "/api/v1/settings", "settings")
    }

    pub fn remote_control(&self) -> Result<RemoteControl, Failure> {
        read(self, "/api/v1/claude/remote-control", "settings")
    }

    pub fn compaction(&self) -> Result<Compaction, Failure> {
        read(self, "/api/v1/claude/compact-settings", "settings")
    }

    pub fn notification_preferences(&self) -> Result<Vec<Preference>, Failure> {
        let value: Value = read(self, "/api/v1/notification-preferences", "control")?;
        reply(value["preferences"].clone()).map_err(|f| Failure { uncertain: false, ..f })
    }

    fn post_settings(&self, body: Value) -> Result<(), Failure> {
        self.post_json("/api/v1/settings", body).map(drop).map_err(|e| write_failure("settings", e))
    }

    pub fn set_delivery(&self, busy_delivery: &str, codex_recent_hours: i64) -> Result<(), Failure> {
        self.post_settings(json!({"action": "dashboard-config", "busyDelivery": busy_delivery, "codexRecentHours": codex_recent_hours}))
    }

    pub fn change_delivery(&self, change: Delivery) -> Result<(), Failure> {
        let now = self.settings()?.dashboard;
        let (busy, hours) = change.over(&now);
        self.set_delivery(&busy, hours)
    }

    pub fn set_resume_after_limit(&self, on: bool) -> Result<(), Failure> {
        self.post_settings(json!({"action": "resume-after-limit", "resumeAfterLimit": on}))
    }

    pub fn set_move_after_limit(&self, on: bool) -> Result<(), Failure> {
        self.post_settings(json!({"action": "move-after-limit", "moveAfterLimit": on}))
    }

    pub fn set_claude_default_root(&self, name: &str) -> Result<(), Failure> {
        self.post_settings(json!({"action": "claude-default-root", "claudeDefaultRoot": name}))
    }

    pub fn set_person_name(&self, name: &str) -> Result<(), Failure> {
        self.post_settings(json!({"action": "person-name", "personName": name.trim()}))
    }

    pub fn notifications(&self, what: Notify) -> Result<(), Failure> {
        let client = if what == Notify::Enable { self.timed(Duration::from_secs(130)) } else { self.clone() };
        client.post_settings(json!({"action": what.action()}))
    }

    pub fn set_remote_control(&self, root: &str, at_startup: bool) -> Result<RemoteControl, Failure> {
        let value = self.post_json("/api/v1/claude/remote-control", json!({"root": root, "atStartup": at_startup})).map_err(|e| write_failure("settings", e))?;
        reply(value)
    }

    pub fn save_compaction(&self, body: Value) -> Result<CompactRoot, Failure> {
        let value = self.post_json("/api/v1/claude/compact-settings", body).map_err(|e| write_failure("settings", e))?;
        reply(value)
    }

    pub fn set_notification_preference(&self, id: &str, value: bool) -> Result<Vec<Preference>, Failure> {
        let value = self.put_json("/api/v1/notification-preferences", json!({"id": id, "value": value})).map_err(|e| write_failure("control", e))?;
        reply(value["preferences"].clone())
    }
}

pub fn name_field(person: &Person) -> String {
    if person.set { person.name.clone() } else { String::new() }
}

pub fn name_submission(draft: &str, current: &Person) -> Option<String> {
    let name = draft.trim();
    if name.is_empty() || current.set && name == current.name {
        return None;
    }
    Some(name.to_string())
}

pub fn name_footer(person: &Person) -> String {
    if person.set {
        format!("Agents on this computer see what you write in Agenthail as from {}.", person.name)
    } else {
        format!("Agents on this computer see what you write in Agenthail as from {}, this account's name, until you set yours.", person.name)
    }
}

pub const CODEX_RECENT_RANGE: std::ops::RangeInclusive<i64> = 1..=168;
const CODEX_RECENT_PRESETS: [i64; 6] = [1, 3, 5, 8, 12, 24];

pub fn codex_recent_choices(current: i64) -> Vec<i64> {
    let mut out = CODEX_RECENT_PRESETS.to_vec();
    if CODEX_RECENT_RANGE.contains(&current) && !out.contains(&current) {
        out.push(current);
        out.sort();
    }
    out
}

pub fn hours_label(hours: i64) -> String {
    if hours == 1 { "1 hour".into() } else { format!("{hours} hours") }
}

pub fn grouped(n: i64) -> String {
    let digits = n.unsigned_abs().to_string();
    let mut out = String::new();
    for (i, c) in digits.chars().enumerate() {
        if i > 0 && (digits.len() - i) % 3 == 0 {
            out.push(',');
        }
        out.push(c);
    }
    if n < 0 { format!("-{out}") } else { out }
}

pub fn window_label(window: Option<&Window>) -> String {
    let Some(window) = window else { return "Claude's default".into() };
    if window.auto == Some(true) {
        return "auto".into();
    }
    if let Some(ignored) = window.ignored.as_deref().filter(|i| !i.is_empty()) {
        return format!("{ignored} (Claude ignores it)");
    }
    format!("{} tokens", grouped(window.tokens.unwrap_or(0)))
}

pub fn toggle_label(value: Option<bool>) -> &'static str {
    match value {
        Some(true) => "on",
        Some(false) => "off",
        None => "not set",
    }
}

pub fn agenthail_idle_label(idle: &IdleCompaction) -> String {
    if idle.enabled { format!("on, from {} tokens", grouped(idle.min_context_tokens)) } else { "off".into() }
}

pub fn pre_limit_label(idle: &IdleCompaction) -> Option<String> {
    let percent = idle.pre_limit_percent?;
    Some(if idle.enabled && percent > 0 { format!("from {percent}%") } else { "off".into() })
}

pub fn model_name(id: &str) -> String {
    let parts: Vec<&str> = id.split('-').collect();
    let major = parts.get(2).and_then(|p| p.parse::<i64>().ok());
    let (Some(major), true) = (major, parts.len() >= 3 && parts[0] == "claude") else { return id.to_string() };
    let minor = parts.get(3).filter(|p| p.len() <= 2).and_then(|p| p.parse::<i64>().ok());
    let mut family = parts[1].chars();
    let family = family.next().map(|f| f.to_uppercase().collect::<String>() + &family.as_str().to_lowercase()).unwrap_or_default();
    match minor {
        Some(minor) => format!("{family} {major}.{minor}"),
        None => format!("{family} {major}"),
    }
}

pub fn model_lines(root: &CompactRoot) -> Vec<(String, String)> {
    root.model_windows.iter().map(|(id, w)| (model_name(id), window_label(Some(w)))).collect()
}

fn components(path: &str) -> Vec<&str> {
    path.split('/').filter(|p| !p.is_empty() && *p != ".").collect()
}

pub fn root_titles(roots: &[&str], home: &str) -> Vec<String> {
    let titles: Vec<String> = roots.iter().map(|r| root_title(r, home)).collect();
    titles.iter().zip(roots).map(|(t, r)| if titles.iter().filter(|o| *o == t).count() > 1 { format!("{t} ({r})") } else { t.clone() }).collect()
}

pub fn root_title(root: &str, home: &str) -> String {
    let mut parts = components(root);
    if parts.last() == Some(&".claude") {
        parts.pop();
    }
    if !home.is_empty() && parts == components(home) {
        return "Default".into();
    }
    match parts.last() {
        Some(name) => name.strip_prefix('.').unwrap_or(name).to_string(),
        None => root.to_string(),
    }
}

pub const HOLDS_SHOWN: usize = 5;

pub fn hold_lines(root: &CompactRoot, holds: &[Hold], now: OffsetDateTime) -> Vec<String> {
    let mine: Vec<&Hold> = holds.iter().filter(|h| h.root == root.root).collect();
    mine.iter().rev().take(HOLDS_SHOWN).map(|hold| {
        let name = hold.session.as_deref().filter(|s| !s.is_empty()).unwrap_or(&hold.session_id);
        let age = match ago(&hold.at, now) {
            a if a.is_empty() => hold.at.clone(),
            a if a == "now" => "just now".into(),
            a => format!("{a} ago"),
        };
        format!("{name} at {} tokens, {age}: {}", grouped(hold.context_tokens), hold.reason)
    }).collect()
}

pub fn override_lines(root: &CompactRoot) -> Vec<String> {
    root.overrides.iter().map(|(k, v)| format!("{k}={v} is set where Agenthail runs and takes precedence over this file.")).collect()
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct CompactDraft {
    pub root: String,
    pub model: String,
    pub window: String,
    pub enabled: Option<bool>,
    pub idle: Option<bool>,
    pub agenthail_idle: Option<bool>,
    pub min_tokens: String,
    pub pre_limit: String,
}

impl CompactDraft {
    pub fn problem(&self) -> Option<&'static str> {
        let tokens = self.min_tokens.trim();
        if !tokens.is_empty() && tokens.parse::<i64>().map(|t| t > 0) != Ok(true) {
            return Some("From context size must be a whole number of tokens.");
        }
        if self.pre_limit_percent().is_none() && !self.pre_limit.trim().is_empty() {
            return Some("Before a usage limit must be off or a percent from 50 to 97.");
        }
        if !self.model.trim().is_empty() && self.window.trim().is_empty() {
            return Some("A model needs a window: tokens, auto, or unset.");
        }
        None
    }

    pub fn changes(&self) -> bool {
        !self.window.trim().is_empty() || self.enabled.is_some() || self.idle.is_some() || self.agenthail_idle.is_some() || !self.min_tokens.trim().is_empty() || !self.pre_limit.trim().is_empty()
    }

    pub fn pre_limit_percent(&self) -> Option<i64> {
        let text = self.pre_limit.trim().to_lowercase();
        if text == "off" {
            return Some(0);
        }
        let percent = text.strip_suffix('%').unwrap_or(&text).trim().parse::<i64>().ok()?;
        (50..=97).contains(&percent).then_some(percent)
    }

    pub fn body(&self) -> Value {
        let mut body = json!({"root": self.root, "model": self.model.trim(), "window": self.window.trim()});
        if let Some(v) = self.enabled {
            body["enabled"] = json!(v);
        }
        if let Some(v) = self.idle {
            body["idleCompaction"] = json!(v);
        }
        if let Some(v) = self.agenthail_idle {
            body["agenthailIdleCompaction"] = json!(v);
        }
        if let Ok(tokens) = self.min_tokens.trim().parse::<i64>() {
            body["agenthailIdleMinTokens"] = json!(tokens);
        }
        if let Some(percent) = self.pre_limit_percent() {
            body["agenthailPreLimitPercent"] = json!(percent);
        }
        body
    }

    pub fn saved(&mut self) {
        *self = CompactDraft { root: std::mem::take(&mut self.root), ..CompactDraft::default() };
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};

    fn person(name: &str, set: bool) -> Person {
        Person { name: name.into(), set }
    }

    #[test]
    fn a_name_is_sent_only_when_it_changes_and_never_cleared_by_an_empty_field() {
        let account = person("sam", false);
        let ada = person("Ada", true);
        assert_eq!((name_field(&account), name_field(&ada)), (String::new(), "Ada".into()));
        assert_eq!(name_submission("", &ada), None);
        assert_eq!(name_submission("   ", &ada), None);
        assert_eq!(name_submission(" Ada ", &ada), None);
        assert_eq!(name_submission(" Grace ", &ada), Some("Grace".into()));
        assert_eq!(name_submission("sam", &account), Some("sam".into()), "setting the account's own name makes it set");
        assert!(name_footer(&account).ends_with("as from sam, this account's name, until you set yours."));
        assert!(name_footer(&ada).ends_with("as from Ada."));
    }

    #[test]
    fn the_codex_window_offers_presets_and_keeps_a_custom_value() {
        assert_eq!(codex_recent_choices(5), vec![1, 3, 5, 8, 12, 24]);
        assert_eq!(codex_recent_choices(48), vec![1, 3, 5, 8, 12, 24, 48]);
        assert_eq!(codex_recent_choices(500), vec![1, 3, 5, 8, 12, 24]);
        assert_eq!((hours_label(1), hours_label(12)), ("1 hour".into(), "12 hours".into()));
    }

    #[test]
    fn limits_stay_on_unless_turned_off() {
        let mut d = Dashboard::default();
        assert!(d.resumes_after_limit() && d.moves_after_limit());
        d.resume_after_limit = Some(false);
        d.move_after_limit = Some(true);
        assert!(!d.resumes_after_limit() && d.moves_after_limit());
    }

    #[test]
    fn notification_status_reads_like_the_mac_app() {
        let mut s = NotifyStatus { enabled: true, authorized: true, authorization: "authorized".into(), ..Default::default() };
        assert_eq!(s.detail(), "On. Alerts appear in Notification Center.");
        s.enabled = false;
        assert_eq!(s.detail(), "Off.");
        s.authorization = "denied".into();
        assert_eq!(s.detail(), "Blocked in System Settings.");
        s.error = Some("helper missing".into());
        assert_eq!(s.detail(), "helper missing");
        s.error = Some(String::new());
        assert_eq!(s.detail(), "Blocked in System Settings.");
    }

    #[test]
    fn compaction_facts_read_as_words() {
        assert_eq!(window_label(None), "Claude's default");
        assert_eq!(window_label(Some(&Window { auto: Some(true), ..Default::default() })), "auto");
        assert_eq!(window_label(Some(&Window { tokens: Some(400_000), ..Default::default() })), "400,000 tokens");
        assert_eq!(window_label(Some(&Window { ignored: Some("90000".into()), ..Default::default() })), "90000 (Claude ignores it)");
        assert_eq!((toggle_label(Some(true)), toggle_label(Some(false)), toggle_label(None)), ("on", "off", "not set"));
        let on = IdleCompaction { enabled: true, min_context_tokens: 200_000, pre_limit_percent: Some(90) };
        assert_eq!(agenthail_idle_label(&on), "on, from 200,000 tokens");
        assert_eq!(pre_limit_label(&on).as_deref(), Some("from 90%"));
        let off = IdleCompaction { enabled: false, ..on.clone() };
        assert_eq!((agenthail_idle_label(&off), pre_limit_label(&off)), ("off".into(), Some("off".into())));
        assert_eq!(pre_limit_label(&IdleCompaction { pre_limit_percent: Some(0), ..on.clone() }).as_deref(), Some("off"));
        assert_eq!(pre_limit_label(&IdleCompaction { pre_limit_percent: None, ..on }), None);
        assert_eq!((grouped(0), grouped(999), grouped(1_000), grouped(1_234_567)), ("0".into(), "999".into(), "1,000".into(), "1,234,567".into()));
    }

    #[test]
    fn model_ids_read_as_names() {
        assert_eq!(model_name("claude-opus-5-5"), "Opus 5.5");
        assert_eq!(model_name("claude-sonnet-4-5-20250929"), "Sonnet 4.5");
        assert_eq!(model_name("claude-haiku-4-20250101"), "Haiku 4");
        assert_eq!(model_name("gpt-6"), "gpt-6");
        assert_eq!(model_name("claude-opus"), "claude-opus");
    }

    #[test]
    fn roots_are_named_by_their_folder() {
        assert_eq!(root_title("/Users/a/.claude", "/Users/a"), "Default");
        assert_eq!(root_title("/Users/a/.claude-work", "/Users/a"), "claude-work");
        assert_eq!(root_title("/Users/a/accounts/backup/.claude", "/Users/a"), "backup");
        assert_eq!(root_title("/Users/a/.claude/", "/Users/a/"), "Default");
        assert_eq!(
            root_titles(&["/Users/a/.claude", "/x/work/.claude", "/y/work/.claude"], "/Users/a"),
            vec!["Default", "work (/x/work/.claude)", "work (/y/work/.claude)"]
        );
    }

    #[test]
    fn a_delivery_change_keeps_the_other_value_the_daemon_holds_now() {
        let settings = |busy: &str, hours: i64| format!(r#"{{"dashboard":{{"busyDelivery":"{busy}","codexRecentHours":{hours}}}}}"#);
        let (s1, s2) = (settings("queue", 12), settings("", 3));
        let (client, rx) = daemon(replies(&[(200, &s1), (200, r#"{"ok":true}"#), (200, &s2), (200, r#"{"ok":true}"#), (401, r#"{"error":{"message":"no"}}"#)]));
        client.change_delivery(Delivery::Busy("steer".into())).unwrap();
        client.change_delivery(Delivery::Hours(24)).unwrap();
        assert!(client.change_delivery(Delivery::Hours(8)).unwrap_err().denied);
        let sent: Vec<(String, Value)> = rx.try_iter().filter(|(p, _)| p == "/api/v1/settings").map(|(p, b)| (p, b)).collect();
        assert_eq!(
            sent.into_iter().map(|(_, b)| b).filter(|b| !b.is_null()).collect::<Vec<_>>(),
            vec![json!({"action": "dashboard-config", "busyDelivery": "steer", "codexRecentHours": 12}), json!({"action": "dashboard-config", "busyDelivery": "queue", "codexRecentHours": 24})]
        );
    }

    #[test]
    fn holds_show_the_newest_five_for_their_root() {
        let now = OffsetDateTime::parse("2026-11-26T12:00:00Z", &time::format_description::well_known::Rfc3339).unwrap();
        let root = CompactRoot { root: "/r/a".into(), ..Default::default() };
        let mut holds: Vec<Hold> = (0..7)
            .map(|i| Hold { session_id: format!("s{i}"), session: (i == 6).then(|| "@docs".into()), root: "/r/a".into(), context_tokens: 250_000, reason: "busy".into(), at: format!("2026-11-26T11:{:02}:00Z", 50 + i) })
            .collect();
        holds.push(Hold { session_id: "other".into(), root: "/r/b".into(), ..Default::default() });
        let lines = hold_lines(&root, &holds, now);
        assert_eq!(lines.len(), 5);
        assert_eq!(lines[0], "@docs at 250,000 tokens, 4m ago: busy");
        assert!(lines[4].starts_with("s2 at"));
        let late = Hold { at: "2026-11-26T12:00:00Z".into(), ..holds[0].clone() };
        assert!(hold_lines(&root, &[late], now)[0].contains("just now"));
        let unparsed = Hold { at: "yesterday".into(), ..holds[0].clone() };
        assert!(hold_lines(&root, &[unparsed], now)[0].contains(", yesterday: "));
    }

    #[test]
    fn overrides_say_they_win_over_the_file() {
        let root = CompactRoot { overrides: [("DISABLE_AUTO_COMPACT".to_string(), "1".to_string())].into(), ..Default::default() };
        assert_eq!(override_lines(&root), vec!["DISABLE_AUTO_COMPACT=1 is set where Agenthail runs and takes precedence over this file."]);
    }

    #[test]
    fn a_compaction_draft_checks_what_the_daemon_would_refuse() {
        let mut d = CompactDraft { root: "/r/a".into(), ..Default::default() };
        assert!(!d.changes() && d.problem().is_none());
        d.min_tokens = "abc".into();
        assert_eq!(d.problem(), Some("From context size must be a whole number of tokens."));
        d.min_tokens = "0".into();
        assert!(d.problem().is_some());
        d.min_tokens = "150000".into();
        d.pre_limit = "40".into();
        assert_eq!(d.problem(), Some("Before a usage limit must be off or a percent from 50 to 97."));
        d.pre_limit = "85%".into();
        assert_eq!(d.pre_limit_percent(), Some(85));
        d.model = "claude-opus-5-5".into();
        assert_eq!(d.problem(), Some("A model needs a window: tokens, auto, or unset."));
        d.window = " auto ".into();
        d.idle = Some(false);
        assert!(d.problem().is_none() && d.changes());
        assert_eq!(
            d.body(),
            json!({"root": "/r/a", "model": "claude-opus-5-5", "window": "auto", "idleCompaction": false, "agenthailIdleMinTokens": 150000, "agenthailPreLimitPercent": 85})
        );
        d.pre_limit = "OFF".into();
        assert_eq!(d.body()["agenthailPreLimitPercent"], json!(0));
        d.saved();
        assert_eq!(d, CompactDraft { root: "/r/a".into(), ..Default::default() });
    }

    #[test]
    fn reads_take_the_daemons_shapes() {
        let settings = r#"{"dashboard":{"enabled":true,"listen":"127.0.0.1:7412","codexRecentHours":8,"busyDelivery":"steer","remoteAccess":{},"resumeAfterLimit":false},"claudeDefaultRoot":"work","claudeRoots":["default","work"],"remoteAccess":{"enabled":false},"notifications":{"enabled":true,"available":true,"authorization":"authorized","authorized":true,"alerts":true,"sounds":true},"daemon":{"pid":1,"running":true}}"#;
        let remote = r#"{"available":true,"roots":[{"root":"/u/.claude","name":"default","settingsPath":"/u/.claude/settings.json","atStartup":true},{"root":"/u/broken","name":"broken","settingsPath":"/u/broken/settings.json","problem":"not a JSON object"}]}"#;
        let compact = r#"{"roots":[{"root":"/u/.claude","settingsPath":"/u/.claude/settings.json","autoCompactWindow":{"tokens":400000},"modelWindows":{"claude-opus-5-5":{"auto":true}},"agenthailIdleCompaction":{"enabled":true,"minContextTokens":200000,"preLimitPercent":90}}],"idleCompactionHolds":null}"#;
        let prefs = r#"{"preferences":[{"id":"leadCompletionsOnly","title":"Only the lead's completions","detail":"Others stay quiet.","default":true,"value":false}]}"#;
        let base = r#"{"sessions":[],"busyDelivery":"steer","codexRecentHours":8,"person":{"name":"Ada","set":true}}"#;
        let (client, rx) = daemon(replies(&[(200, base), (200, settings), (200, remote), (200, compact), (200, prefs)]));
        let b = client.base_settings().unwrap();
        assert_eq!((b.busy_delivery.as_str(), b.codex_recent_hours, b.person), ("steer", 8, Some(person("Ada", true))));
        let s = client.settings().unwrap();
        assert_eq!((s.dashboard.codex_recent_hours, s.dashboard.busy_delivery.as_str(), s.claude_default_root.as_str()), (8, "steer", "work"));
        assert!(!s.dashboard.resumes_after_limit() && s.dashboard.moves_after_limit() && s.notifications.enabled);
        assert_eq!(s.claude_roots, vec!["default", "work"]);
        let r = client.remote_control().unwrap();
        assert!(r.shown());
        assert_eq!((r.roots[0].at_startup, r.roots[1].problem.as_deref()), (Some(true), Some("not a JSON object")));
        let c = client.compaction().unwrap();
        assert!(c.holds().is_empty());
        assert_eq!(model_lines(&c.roots[0]), vec![("Opus 5.5".to_string(), "auto".to_string())]);
        assert_eq!(c.roots[0].agenthail_idle_compaction.pre_limit_percent, Some(90));
        let p = client.notification_preferences().unwrap();
        assert_eq!((p[0].id.as_str(), p[0].value), ("leadCompletionsOnly", false));
        let paths: Vec<String> = rx.try_iter().map(|(p, _)| p).collect();
        assert_eq!(paths, vec!["/api/v1/snapshot?sessions=none", "/api/v1/settings", "/api/v1/claude/remote-control", "/api/v1/claude/compact-settings", "/api/v1/notification-preferences"]);
    }

    #[test]
    fn writes_send_what_the_daemon_reads() {
        let ok = r#"{"ok":true}"#;
        let remote = r#"{"available":true,"roots":[{"root":"/u/.claude","name":"default","atStartup":false}]}"#;
        let saved = r#"{"root":"/u/.claude","agenthailIdleCompaction":{"enabled":false,"minContextTokens":200000,"preLimitPercent":90}}"#;
        let prefs = r#"{"preferences":[{"id":"leadCompletionsOnly","title":"t","detail":"d","value":true}]}"#;
        let (client, rx) = daemon(replies(&[(200, ok), (200, ok), (200, ok), (200, ok), (200, ok), (200, ok), (200, ok), (200, remote), (200, saved), (200, prefs)]));
        client.set_delivery("steer", 8).unwrap();
        client.set_resume_after_limit(false).unwrap();
        client.set_move_after_limit(true).unwrap();
        client.set_claude_default_root("").unwrap();
        client.set_person_name("  Grace ").unwrap();
        client.notifications(Notify::Test).unwrap();
        client.notifications(Notify::Enable).unwrap();
        assert_eq!(client.set_remote_control("/u/.claude", false).unwrap().roots[0].at_startup, Some(false));
        assert!(!client.save_compaction(json!({"root": "/u/.claude", "model": "", "window": "", "agenthailIdleCompaction": false})).unwrap().agenthail_idle_compaction.enabled);
        assert!(client.set_notification_preference("leadCompletionsOnly", true).unwrap()[0].value);
        let sent: Vec<(String, Value)> = rx.try_iter().collect();
        assert_eq!(
            sent,
            vec![
                ("/api/v1/settings".into(), json!({"action": "dashboard-config", "busyDelivery": "steer", "codexRecentHours": 8})),
                ("/api/v1/settings".into(), json!({"action": "resume-after-limit", "resumeAfterLimit": false})),
                ("/api/v1/settings".into(), json!({"action": "move-after-limit", "moveAfterLimit": true})),
                ("/api/v1/settings".into(), json!({"action": "claude-default-root", "claudeDefaultRoot": ""})),
                ("/api/v1/settings".into(), json!({"action": "person-name", "personName": "Grace"})),
                ("/api/v1/settings".into(), json!({"action": "notifications-test"})),
                ("/api/v1/settings".into(), json!({"action": "notifications-enable"})),
                ("/api/v1/claude/remote-control".into(), json!({"root": "/u/.claude", "atStartup": false})),
                ("/api/v1/claude/compact-settings".into(), json!({"root": "/u/.claude", "model": "", "window": "", "agenthailIdleCompaction": false})),
                ("/api/v1/notification-preferences".into(), json!({"id": "leadCompletionsOnly", "value": true})),
            ]
        );
    }

    #[test]
    fn a_missing_permission_reads_as_such_and_other_refusals_keep_the_daemons_words() {
        let unauthorized = r#"{"error":{"code":"unauthorized","message":"A valid Agenthail device token is required."}}"#;
        let refused = r#"{"error":{"code":"invalid_request","message":"unknown Claude root \"nope\""}}"#;
        let conflict = r#"{"error":{"code":"conflict","message":"this install has no Claude Remote Control"}}"#;
        let (client, _rx) = daemon(replies(&[(401, unauthorized), (401, unauthorized), (403, unauthorized), (400, refused), (409, conflict), (504, r#"{"error":{"message":"timed out"}}"#)]));
        let read = client.settings().unwrap_err();
        assert!(read.denied && read.scope == "settings" && read.message.contains("lacks the settings permission or is no longer valid"));
        assert_eq!(scope_message(read.scope), NO_SETTINGS_SCOPE);
        let prefs = client.notification_preferences().unwrap_err();
        assert!(prefs.denied && scope_message(prefs.scope) == NO_CONTROL_SCOPE);
        assert!(client.set_resume_after_limit(true).unwrap_err().denied);
        let write = client.set_claude_default_root("nope").unwrap_err();
        assert_eq!((write.denied, write.uncertain, write.message.as_str()), (false, false, "unknown Claude root \"nope\""));
        assert_eq!(client.set_remote_control("/u/.claude", true).unwrap_err().message, "this install has no Claude Remote Control");
        let unsure = client.set_person_name("Ada").unwrap_err();
        assert!(unsure.uncertain && unsure.message.contains("timed out"));
    }
}
