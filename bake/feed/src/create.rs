use anyhow::Result;
use serde::Deserialize;
use serde_json::{Map, Value};

use crate::catalog::{Client, RespondError, enc};

#[derive(Clone, Debug, PartialEq)]
pub struct Host {
    pub id: String,
    pub name: String,
    pub state: String,
}

impl Host {
    pub fn is_local(&self) -> bool {
        self.id.is_empty()
    }

    pub fn available(&self) -> bool {
        self.is_local() || self.state == "connected"
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Surface {
    pub id: String,
    #[serde(default)]
    pub label: Option<String>,
    #[serde(default)]
    pub workspace: bool,
    #[serde(default)]
    pub connected: bool,
    #[serde(default)]
    pub models: Vec<String>,
}

impl Surface {
    pub fn title(&self) -> String {
        match &self.label {
            Some(label) if !label.is_empty() => label.clone(),
            _ => capitalized(&self.id),
        }
    }

    pub fn uses_folder(&self) -> bool {
        self.id != NOTION
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct LaunchProject {
    pub id: String,
    #[serde(default)]
    pub display_name: String,
    pub root_path: String,
    #[serde(default)]
    pub kind: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Launcher {
    pub id: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub agents: Vec<String>,
    #[serde(default)]
    pub available: bool,
    #[serde(default)]
    pub detail: String,
    #[serde(default)]
    pub session_options: bool,
}

impl Launcher {
    pub fn title(&self) -> &str {
        if self.label.is_empty() { &self.id } else { &self.label }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Login {
    #[serde(default)]
    pub email: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Limit {
    pub kind: String,
    pub percent: f64,
    #[serde(default)]
    pub resets_at: Option<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Usage {
    #[serde(default)]
    pub limits: Vec<Limit>,
    #[serde(default, rename = "fetchedAt")]
    pub fetched_at: Option<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ConfigRoot {
    pub name: String,
    pub root: String,
    #[serde(default)]
    pub primary: bool,
    #[serde(default)]
    pub signed_in_as: Option<Login>,
    #[serde(default)]
    pub login: String,
    #[serde(default)]
    pub proxy: Option<String>,
    #[serde(default)]
    pub usage: Option<Usage>,
    #[serde(default)]
    pub best: Option<String>,
    #[serde(default)]
    pub full: bool,
    #[serde(default)]
    pub full_until: Option<String>,
}

impl ConfigRoot {
    pub fn selectable(&self) -> bool {
        self.signed_in_as.is_some() || self.login == "api_key" || self.login == "proxy"
    }

    pub fn reads_usage(&self) -> bool {
        self.signed_in_as.is_some() || self.proxy.as_deref().is_some_and(|p| !p.is_empty())
    }

    pub fn take_usage(&mut self, usage: RootUsage) {
        self.usage = usage.usage;
        self.best = usage.best;
        self.full = usage.full;
        self.full_until = usage.full_until;
    }

    pub fn spent(&self, now: time::OffsetDateTime) -> bool {
        let at_limit = |l: &Limit| l.percent >= 100.0 && l.resets_at.as_deref().and_then(crate::catalog::parse_time).is_some_and(|at| at > now);
        self.full_now(now) || self.usage.as_ref().is_some_and(|u| u.limits.iter().any(at_limit))
    }

    fn full_now(&self, now: time::OffsetDateTime) -> bool {
        self.full && self.full_until.as_deref().and_then(crate::catalog::parse_time).is_none_or(|at| at > now)
    }

    pub fn same_account(&self, other: &ConfigRoot) -> bool {
        self.root == other.root && self.login == other.login && self.proxy == other.proxy && self.signed_in_as.as_ref().map(|l| &l.email) == other.signed_in_as.as_ref().map(|l| &l.email)
    }

    pub fn take_reading(&mut self, usage: RootUsage) -> bool {
        let unread = usage.usage.is_none() && !usage.full && usage.usage_problem.as_deref().is_some_and(|p| !p.is_empty());
        if !unread {
            self.take_usage(usage);
        }
        !unread
    }

    pub fn title(&self) -> String {
        if let Some(proxy) = self.proxy.as_deref().filter(|p| !p.is_empty()) {
            return format!("{proxy} via proxy");
        }
        match self.signed_in_as.as_ref().map(|l| l.email.as_str()).filter(|e| !e.is_empty()) {
            Some(email) => email.split('@').next().unwrap_or(email).to_string(),
            None => self.name.clone(),
        }
    }

    pub fn usage_line(&self, now: time::OffsetDateTime) -> Option<String> {
        let minutes = |at: &str| crate::catalog::parse_time(at).map(|at| ((at - now).whole_seconds() as f64 / 60.0).ceil() as i64);
        if self.full_now(now) {
            return Some(match self.full_until.as_deref().and_then(minutes) {
                Some(left) => format!("out of usage for {}", crate::limit::span(left)),
                None => "out of usage".into(),
            });
        }
        let usage = self.usage.as_ref()?;
        let limits = &usage.limits;
        let windows = [("session", ["session", "5h"]), ("week", ["weekly", "weekly_all"])];
        let parts: Vec<String> = windows
            .iter()
            .filter_map(|(label, kinds)| limits.iter().find(|l| kinds.contains(&l.kind.as_str())).map(|l| format!("{label} {:.0}%", l.percent)))
            .collect();
        let old = usage.fetched_at.as_deref().and_then(minutes).map(|m| -m).filter(|ago| *ago >= 60);
        let line = parts.join(", ");
        match old {
            _ if parts.is_empty() => None,
            Some(ago) => Some(format!("{line}, read {} ago", crate::limit::span(ago))),
            None => Some(line),
        }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RootPick {
    #[serde(default)]
    pub root: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub reason: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RootUsage {
    pub root: String,
    #[serde(default)]
    pub usage_problem: Option<String>,
    #[serde(default)]
    pub usage: Option<Usage>,
    #[serde(default)]
    pub best: Option<String>,
    #[serde(default)]
    pub full: bool,
    #[serde(default)]
    pub full_until: Option<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ModelOption {
    pub id: String,
    #[serde(default)]
    pub display_name: String,
    #[serde(default)]
    pub default: bool,
    #[serde(default)]
    pub supported_reasoning_efforts: Vec<String>,
}

impl ModelOption {
    pub fn title(&self) -> &str {
        if self.display_name.is_empty() { &self.id } else { &self.display_name }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SessionOptions {
    #[serde(default)]
    pub surfaces: Vec<Surface>,
    #[serde(default)]
    pub projects: Vec<LaunchProject>,
    #[serde(default)]
    pub folders: Vec<String>,
    #[serde(default)]
    pub launchers: Vec<Launcher>,
    #[serde(default)]
    pub claude_config_roots: Vec<ConfigRoot>,
}

impl SessionOptions {
    pub fn surface(&self, id: &str) -> Option<&Surface> {
        self.surfaces.iter().find(|s| s.id == id)
    }

    pub fn launchers_for(&self, surface: &str) -> Vec<&Launcher> {
        self.launchers.iter().filter(|l| l.agents.iter().any(|a| a == surface)).collect()
    }

    pub fn first_surface(&self) -> Option<&Surface> {
        [CLAUDE, CODEX].iter().find_map(|id| self.surface(id)).or_else(|| self.surfaces.first())
    }
}

pub const CLAUDE: &str = "claude";
pub const CODEX: &str = "codex";
pub const NOTION: &str = "notion";

pub const CLAUDE_PERMISSION_MODES: [(&str, &str); 6] = [
    ("plan", "Plan"),
    ("auto", "Auto"),
    ("manual", "Manual"),
    ("acceptEdits", "Accept edits"),
    ("dontAsk", "Don't ask"),
    ("bypassPermissions", "Bypass permissions"),
];

pub const CODEX_MODES: [(&str, &str); 2] = [("plan", "Plan"), ("default", "Default")];

pub const CODEX_SERVICE_TIERS: [(&str, &str); 3] = [("default", "Default"), ("fast", "Fast"), ("flex", "Flex")];

const OUTPUT_SCHEMA_LIMIT: usize = 64 << 10;

pub const CODEX_APPROVALS: [(&str, &str); 3] = [("on-request", "Ask when needed"), ("untrusted", "Ask for untrusted work"), ("never", "Never ask")];

pub fn permission_modes(surface: &str) -> &'static [(&'static str, &'static str)] {
    match surface {
        CLAUDE => &CLAUDE_PERMISSION_MODES,
        CODEX => &CODEX_APPROVALS,
        _ => &[],
    }
}

/// One session to start. Empty strings mean the host's default; `host` empty is this computer.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Launch {
    pub host: String,
    pub surface: String,
    pub folder: String,
    pub model: String,
    pub effort: String,
    pub permission: String,
    pub config_root: String,
    pub agent: String,
    pub mode: String,
    pub service_tier: String,
    pub output_schema: String,
    pub launcher: Option<Launcher>,
    pub worktree: bool,
    pub branch: String,
    pub base: String,
    pub alias: String,
    pub message: String,
}

impl Launch {
    pub fn uses_folder(&self) -> bool {
        self.surface != NOTION
    }

    /// A terminal launcher takes only the folder, message, model, account and name.
    pub fn in_terminal(&self) -> bool {
        self.launcher.as_ref().is_some_and(|l| !l.session_options)
    }

    pub fn takes_worktree(&self) -> bool {
        self.uses_folder() && self.launcher.is_none()
    }

    pub fn takes_codex_options(&self) -> bool {
        self.surface == CODEX && !self.in_terminal()
    }

    fn schema(&self) -> std::result::Result<Option<Value>, &'static str> {
        let text = self.output_schema.trim();
        if !self.takes_codex_options() || text.is_empty() {
            return Ok(None);
        }
        match serde_json::from_str::<Value>(text) {
            Ok(schema @ Value::Object(_)) if schema.to_string().len() <= OUTPUT_SCHEMA_LIMIT => Ok(Some(schema)),
            Ok(Value::Object(_)) => Err("The output schema must be at most 64 KiB."),
            _ => Err("The output schema must be a JSON object."),
        }
    }

    pub fn alias_name(&self) -> &str {
        let name = self.alias.trim();
        name.strip_prefix('@').unwrap_or(name)
    }

    pub fn problem(&self) -> Option<&'static str> {
        let alias = self.alias_name();
        if self.surface.is_empty() {
            Some("Choose an agent.")
        } else if self.message.trim().is_empty() {
            Some("Write the first message.")
        } else if self.uses_folder() && self.folder.trim().is_empty() {
            Some("Choose a project or folder.")
        } else if alias.chars().count() > 80 || alias.contains([' ', '\t', '\r', '\n', '/', '#']) {
            Some("A name is up to 80 characters, without spaces, / or #.")
        } else if self.launcher.as_ref().is_some_and(|l| !l.available) {
            Some("That terminal isn't available on this computer.")
        } else if let Err(problem) = self.schema() {
            Some(problem)
        } else {
            None
        }
    }

    pub fn action(&self) -> &'static str {
        if self.surface == NOTION { "notion-create" } else { "session-create" }
    }

    pub fn payload(&self) -> Value {
        let mut body = Map::new();
        let mut put = |key: &str, value: &str| {
            let value = value.trim();
            if !value.is_empty() {
                body.insert(key.into(), Value::String(value.into()));
            }
        };
        put("surface", &self.surface);
        put("message", &self.message);
        put("host", &self.host);
        put("alias", self.alias_name());
        if self.surface != NOTION {
            put("cwd", &self.folder);
            put("model", &self.model);
            if let Some(launcher) = &self.launcher {
                put("launcher", &launcher.id);
            }
            if self.surface == CLAUDE {
                put("configRoot", &self.config_root);
            }
            if !self.in_terminal() {
                put("effort", &self.effort);
                match self.surface.as_str() {
                    CLAUDE => {
                        put("permissionMode", &self.permission);
                        put("agent", &self.agent);
                    }
                    CODEX => {
                        put("approvalPolicy", &self.permission);
                        put("mode", &self.mode);
                        put("serviceTier", &self.service_tier);
                    }
                    _ => {}
                }
            }
            if self.worktree && self.takes_worktree() {
                put("isolation", "worktree");
                put("worktree", &self.branch);
                put("base", &self.base);
            }
        }
        if let Ok(Some(schema)) = self.schema() {
            body.insert("outputSchema".into(), schema);
        }
        Value::Object(body)
    }
}

#[derive(Clone, Debug, PartialEq)]
pub enum Outcome {
    Started { session: String, note: String },
    Submitted(String),
    Halted { session: String, message: String },
    /// Agenthail can't tell whether it started; starting again may make a second one.
    Unknown { session: Option<String>, message: String },
    Failed(String),
}

const UNKNOWN: &str = "Agenthail can't tell whether this session started. Check the sidebar before trying again.";

fn session_id(reply: &Value) -> Option<String> {
    reply["session"]["id"].as_str().or_else(|| reply["sessionId"].as_str()).filter(|id| !id.is_empty()).map(str::to_string)
}

pub fn outcome(reply: std::result::Result<Value, RespondError>, launch: &Launch) -> Outcome {
    match reply {
        Ok(reply) => {
            let warning = reply["warning"].as_str().unwrap_or_default().to_string();
            if let Some(session) = session_id(&reply) {
                return Outcome::Started { session, note: warning };
            }
            if reply["status"] == "submitted" && reply.get("accepted").is_none() {
                return Outcome::Submitted("Agenthail is still starting this session. It appears in the sidebar once it starts.".into());
            }
            let target = reply["launcher"].as_str().map(str::to_string).or_else(|| launch.launcher.as_ref().map(|l| l.title().to_string())).unwrap_or_else(|| launch.surface.clone());
            let note = format!("Submitted to {target}. It appears in the sidebar once it starts.");
            Outcome::Submitted(if warning.is_empty() { note } else { format!("{note}\n{warning}") })
        }
        Err(error) => {
            let session = session_id(&error.reply);
            if matches!(error.code.as_str(), "create_unknown" | "host_outcome_unknown") {
                return Outcome::Unknown { session, message: UNKNOWN.into() };
            }
            if let Some(session) = session {
                return Outcome::Halted { session, message: error.message };
            }
            if error.code == "create_failed" || !error.maybe_delivered {
                return Outcome::Failed(error.message);
            }
            Outcome::Unknown { session: None, message: format!("{UNKNOWN} ({})", error.message) }
        }
    }
}

fn host_query(host: &str) -> String {
    if host.is_empty() { String::new() } else { format!("host={}", enc(host)) }
}

fn path(base: &str, query: &[String]) -> String {
    let query: Vec<&str> = query.iter().map(String::as_str).filter(|q| !q.is_empty()).collect();
    if query.is_empty() { base.to_string() } else { format!("{base}?{}", query.join("&")) }
}

impl Client {
    pub fn hosts(&self) -> Result<Vec<Host>> {
        let value = self.get_json("/api/v1/hosts")?;
        let local = Host { id: String::new(), name: value["host"]["name"].as_str().unwrap_or("This computer").to_string(), state: "connected".into() };
        let paired = value["hosts"].as_array().into_iter().flatten().filter_map(|h| {
            Some(Host { id: h["id"].as_str()?.to_string(), name: h["name"].as_str().unwrap_or_default().to_string(), state: h["state"].as_str().unwrap_or_default().to_string() })
        });
        Ok(std::iter::once(local).chain(paired).collect())
    }

    pub fn session_options(&self, host: &str) -> Result<SessionOptions> {
        Ok(serde_json::from_value(self.get_json(&path("/api/v1/session-options", &[host_query(host)]))?)?)
    }

    pub fn models(&self, surface: &str, host: &str) -> Result<Vec<ModelOption>> {
        let value = self.get_json(&path("/api/v1/models", &[format!("surface={}", enc(surface)), host_query(host)]))?;
        Ok(serde_json::from_value(value["models"].clone()).unwrap_or_default())
    }

    pub fn claude_pick(&self, host: &str) -> Result<RootPick> {
        let value = self.get_json(&path("/api/v1/session-options/claude-pick", &[host_query(host)]))?;
        Ok(serde_json::from_value(value["pick"].clone())?)
    }

    pub fn claude_usage(&self, root: &str, host: &str) -> Result<RootUsage> {
        Ok(serde_json::from_value(self.get_json(&path("/api/v1/session-options/claude-usage", &[format!("root={}", enc(root)), host_query(host)]))?)?)
    }

    pub fn create_session(&self, launch: &Launch) -> Outcome {
        outcome(self.action(launch.action(), launch.payload()), launch)
    }
}

pub(crate) fn capitalized(id: &str) -> String {
    let mut chars = id.chars();
    chars.next().map(|c| c.to_uppercase().chain(chars).collect()).unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::io::{BufRead, BufReader, Read, Write};
    use std::net::TcpListener;
    use std::sync::mpsc;

    fn daemon(replies: Vec<(u16, &'static str)>) -> (Client, mpsc::Receiver<(String, Value)>) {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let (tx, rx) = mpsc::channel();
        std::thread::spawn(move || {
            for (status, body) in replies {
                let (stream, _) = listener.accept().unwrap();
                let mut reader = BufReader::new(stream.try_clone().unwrap());
                let mut line = String::new();
                reader.read_line(&mut line).unwrap();
                let target = line.split_whitespace().nth(1).unwrap_or_default().to_string();
                let mut length = 0usize;
                loop {
                    let mut header = String::new();
                    reader.read_line(&mut header).unwrap();
                    let header = header.trim_end();
                    if header.is_empty() {
                        break;
                    }
                    match header.split_once(':') {
                        Some((name, value)) if name.eq_ignore_ascii_case("content-length") => length = value.trim().parse().unwrap(),
                        _ => {}
                    }
                }
                let mut raw = vec![0; length];
                reader.read_exact(&mut raw).unwrap();
                tx.send((target, serde_json::from_slice(&raw).unwrap_or(Value::Null))).unwrap();
                let mut stream = stream;
                write!(stream, "HTTP/1.1 {status} X\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len()).unwrap();
            }
        });
        (Client::new(&base, "secret"), rx)
    }

    fn claude() -> Launch {
        Launch { surface: CLAUDE.into(), folder: "/work/app".into(), message: "Fix the build".into(), ..Default::default() }
    }

    fn launcher(id: &str, session_options: bool) -> Launcher {
        Launcher { id: id.into(), label: id.into(), agents: vec![CLAUDE.into()], available: true, detail: String::new(), session_options }
    }

    #[test]
    fn a_claude_launch_sends_what_the_handler_reads() {
        let launch = Launch {
            host: "h3a1".into(),
            model: "opus".into(),
            effort: "high".into(),
            permission: "acceptEdits".into(),
            config_root: "/home/a/.claude-b".into(),
            worktree: true,
            branch: "fix-build".into(),
            base: "main".into(),
            alias: " @scout ".into(),
            message: "  Fix the build \n".into(),
            ..claude()
        };
        assert_eq!(launch.action(), "session-create");
        assert_eq!(
            launch.payload(),
            json!({
                "surface": "claude", "message": "Fix the build", "host": "h3a1", "alias": "scout", "cwd": "/work/app", "model": "opus",
                "configRoot": "/home/a/.claude-b", "effort": "high", "permissionMode": "acceptEdits",
                "isolation": "worktree", "worktree": "fix-build", "base": "main"
            })
        );
    }

    #[test]
    fn defaults_are_left_to_the_host() {
        assert_eq!(claude().payload(), json!({"surface": "claude", "message": "Fix the build", "cwd": "/work/app"}));
        let worktree = Launch { worktree: true, ..claude() };
        assert_eq!(worktree.payload()["isolation"], "worktree");
        assert!(worktree.payload().get("worktree").is_none(), "the daemon names the branch");
    }

    #[test]
    fn codex_takes_its_approval_policy_and_no_account() {
        let launch = Launch { surface: CODEX.into(), permission: "never".into(), config_root: "/home/a/.claude".into(), effort: "xhigh".into(), ..claude() };
        assert_eq!(launch.payload(), json!({"surface": "codex", "message": "Fix the build", "cwd": "/work/app", "effort": "xhigh", "approvalPolicy": "never"}));
    }

    #[test]
    fn codex_sends_its_launch_options_and_claude_its_agent() {
        let codex = Launch {
            surface: CODEX.into(),
            mode: "plan".into(),
            service_tier: "flex".into(),
            output_schema: r#" {"type":"object","properties":{"ok":{"type":"boolean"}}} "#.into(),
            agent: "reviewer".into(),
            ..claude()
        };
        assert_eq!(
            codex.payload(),
            json!({"surface": "codex", "message": "Fix the build", "cwd": "/work/app", "mode": "plan", "serviceTier": "flex", "outputSchema": {"type": "object", "properties": {"ok": {"type": "boolean"}}}})
        );
        let claude = Launch { agent: "reviewer".into(), mode: "plan".into(), output_schema: "{}".into(), ..claude() };
        assert_eq!(claude.payload(), json!({"surface": "claude", "message": "Fix the build", "cwd": "/work/app", "agent": "reviewer"}));
        let terminal = Launch { launcher: Some(Launcher { agents: vec![CODEX.into()], ..launcher("tmux", false) }), ..codex };
        assert!(terminal.payload().get("outputSchema").is_none() && terminal.payload().get("mode").is_none());
    }

    #[test]
    fn an_output_schema_must_be_a_small_json_object() {
        let codex = |schema: &str| Launch { surface: CODEX.into(), output_schema: schema.into(), ..claude() };
        assert_eq!(codex("").problem(), None);
        assert_eq!(codex("[1]").problem(), Some("The output schema must be a JSON object."));
        assert_eq!(codex("{not json").problem(), Some("The output schema must be a JSON object."));
        let huge = format!(r#"{{"description":"{}"}}"#, "x".repeat(70_000));
        assert_eq!(codex(&huge).problem(), Some("The output schema must be at most 64 KiB."));
        assert_eq!(Launch { surface: CLAUDE.into(), ..codex("[1]") }.problem(), None, "only Codex reads a schema");
    }

    #[test]
    fn a_terminal_takes_only_what_it_can_apply() {
        let launch = Launch {
            launcher: Some(launcher("tmux", false)),
            effort: "high".into(),
            permission: "plan".into(),
            config_root: "/r".into(),
            worktree: true,
            branch: "b".into(),
            ..claude()
        };
        assert!(launch.in_terminal() && !launch.takes_worktree());
        assert_eq!(launch.payload(), json!({"surface": "claude", "message": "Fix the build", "cwd": "/work/app", "launcher": "tmux", "configRoot": "/r"}));
        let background = Launch { launcher: Some(launcher("claude-bg", true)), effort: "high".into(), worktree: true, ..claude() };
        assert_eq!(background.payload()["effort"], "high");
        assert!(background.payload().get("isolation").is_none(), "a launcher can't start in its own worktree");
    }

    #[test]
    fn notion_sends_only_the_message() {
        let launch = Launch { surface: NOTION.into(), folder: "/work".into(), model: "m".into(), worktree: true, ..claude() };
        assert_eq!(launch.action(), "notion-create");
        assert_eq!(launch.payload(), json!({"surface": "notion", "message": "Fix the build"}));
        assert_eq!(Launch { folder: String::new(), ..launch }.problem(), None);
    }

    #[test]
    fn an_incomplete_launch_says_what_is_missing() {
        assert_eq!(claude().problem(), None);
        assert_eq!(Launch { message: " \n".into(), ..claude() }.problem(), Some("Write the first message."));
        assert_eq!(Launch { folder: String::new(), ..claude() }.problem(), Some("Choose a project or folder."));
        assert!(Launch { alias: "two words".into(), ..claude() }.problem().is_some());
        assert!(Launch { alias: "@a/b".into(), ..claude() }.problem().is_some());
        assert_eq!(Launch { alias: "@scout".into(), ..claude() }.problem(), None);
        let gone = Launcher { available: false, ..launcher("cmux", false) };
        assert!(Launch { launcher: Some(gone), ..claude() }.problem().is_some());
    }

    #[test]
    fn session_options_parse_as_the_daemon_writes_them() {
        let options: SessionOptions = serde_json::from_str(
            r#"{"host":{"id":"he9","name":"forge","state":"local"},
            "surfaces":[{"id":"claude","workspace":true,"connected":true},{"id":"devin","label":"Devin","workspace":true,"connected":false,"models":["adaptive","swe-2"],"modes":["plan"]},{"id":"codex","workspace":true},{"id":"notion","workspace":false,"connected":true}],
            "projects":[{"id":"implicit:/w/app","displayName":"app","rootPath":"/w/app","kind":"git","added":false}],
            "folders":["/tmp/x"],
            "launchers":[{"agents":["claude","codex"],"available":false,"detail":"cmux executable is not installed","id":"cmux","label":"cmux","sessionOptions":false},{"agents":["claude"],"available":true,"id":"claude-bg","label":"claude-bg","sessionOptions":true}],
            "claudeConfigRoots":[{"name":"default","root":"/Users/a/.claude","primary":true,"source":"default","login":"claude.ai","signedInAs":{"email":"a@example.com","plan":"max"}},{"name":"forge-proxy","root":"/Users/a/.fp","primary":false,"login":"proxy","proxy":"shared"},{"name":"spare","root":"/Users/a/.spare","primary":false,"missing":true}]}"#,
        )
        .unwrap();
        assert_eq!(options.first_surface().map(|s| s.id.as_str()), Some("claude"));
        let devin = options.surface("devin").unwrap();
        assert_eq!((devin.title(), devin.models.len(), devin.uses_folder()), ("Devin".to_string(), 2, true));
        assert_eq!(options.surface("codex").unwrap().title(), "Codex");
        assert!(!options.surface("notion").unwrap().uses_folder());
        assert_eq!(options.launchers_for("codex").iter().map(|l| l.id.as_str()).collect::<Vec<_>>(), ["cmux"]);
        assert_eq!(options.launchers_for("claude").len(), 2);
        let roots = &options.claude_config_roots;
        assert_eq!(roots.iter().map(ConfigRoot::title).collect::<Vec<_>>(), ["a", "shared via proxy", "spare"]);
        assert_eq!(roots.iter().map(ConfigRoot::selectable).collect::<Vec<_>>(), [true, true, false]);
        assert_eq!(roots.iter().map(ConfigRoot::reads_usage).collect::<Vec<_>>(), [true, true, false]);
        assert_eq!(options.projects[0].root_path, "/w/app");
    }

    #[test]
    fn an_account_with_a_window_at_its_limit_is_spent_until_that_window_resets() {
        let now = crate::catalog::parse_time("2026-11-26T10:00:00Z").unwrap();
        let reading = |percent: f64, resets: Option<&str>| ConfigRoot { usage: Some(Usage { limits: vec![Limit { kind: "5h".into(), percent, resets_at: resets.map(String::from) }], fetched_at: None }), ..Default::default() };
        assert!(reading(100.0, Some("2026-11-26T11:00:00Z")).spent(now));
        assert!(!reading(100.0, None).spent(now), "a window with no reset is not held out, as the daemon reads it");
        assert!(!reading(100.0, Some("2026-11-26T09:00:00Z")).spent(now));
        assert!(!reading(99.0, Some("2026-11-26T11:00:00Z")).spent(now));
        assert!(ConfigRoot { full: true, ..Default::default() }.spent(now));
        assert!(ConfigRoot { full: true, full_until: Some("2026-11-26T11:00:00Z".into()), ..Default::default() }.spent(now));
        let freed = ConfigRoot { full: true, full_until: Some("2026-11-26T09:59:00Z".into()), ..Default::default() };
        assert!(!freed.spent(now), "a proxy past the time it frees up is not held out");
        assert_eq!(freed.usage_line(now), None);
        let held = ConfigRoot { full: true, full_until: Some("2026-11-26T12:30:00Z".into()), ..Default::default() };
        assert_eq!(held.usage_line(now).as_deref(), Some("out of usage for 2h 30m"));
    }

    #[test]
    fn an_unreadable_reading_keeps_what_was_known() {
        let mut root = ConfigRoot { root: "/r".into(), full: true, full_until: Some("Friday".into()), ..Default::default() };
        let unread: RootUsage = serde_json::from_str(r#"{"root":"/r","proxy":"shared","usageProblem":"the shared proxy's accounts are not readable from this host"}"#).unwrap();
        assert!(!root.take_reading(unread));
        assert!(root.full, "an account known to be out stays out");
        assert!(root.take_reading(RootUsage { root: "/r".into(), ..Default::default() }));
        assert!(!root.full);
    }

    #[test]
    fn a_root_reads_its_usage_once_it_arrives() {
        let mut root: ConfigRoot = serde_json::from_value(json!({"name": "default", "root": "/r", "login": "proxy", "proxy": "shared"})).unwrap();
        let now = crate::catalog::parse_time("2026-11-26T08:00:00Z").unwrap();
        assert_eq!(root.usage_line(now), None);
        let usage: RootUsage = serde_json::from_str(r#"{"best":"a@example.com","proxy":"shared","root":"/r","usage":{"fetchedAt":"2026-11-26T07:07:00Z","limits":[{"kind":"weekly_all","percent":74.4,"resetsAt":"2026-11-28T01:00:00Z"},{"kind":"5h","percent":12}]}}"#).unwrap();
        root.take_usage(usage);
        assert_eq!(root.usage_line(now).as_deref(), Some("session 12%, week 74%"));
        assert_eq!(root.usage_line(crate::catalog::parse_time("2026-11-26T09:30:00Z").unwrap()).as_deref(), Some("session 12%, week 74%, read 2h 23m ago"));
        root.take_usage(RootUsage { root: "/r".into(), full: true, full_until: Some("Friday".into()), ..Default::default() });
        assert_eq!(root.usage_line(now).as_deref(), Some("out of usage"));
    }

    #[test]
    fn reads_go_to_the_chosen_host() {
        let (client, seen) = daemon(vec![
            (200, r#"{"host":{"id":"he9","name":"forge","state":"local"},"hosts":[{"id":"h3a","name":"mac mini","state":"connected"},{"id":"h4b","name":"laptop","state":"offline"}]}"#),
            (200, r#"{"surfaces":[{"id":"claude","workspace":true}],"projects":[],"launchers":[],"claudeConfigRoots":[]}"#),
            (200, r#"{"models":[{"id":"default","displayName":"Default (recommended)","default":true,"supportedReasoningEfforts":["low","high"]},{"id":"opus","displayName":"Opus"}]}"#),
            (200, r#"{"pick":{"root":"","name":"default","reason":"the primary root"}}"#),
            (200, r#"{"root":"/Users/a/my root","usage":{"limits":[]}}"#),
        ]);
        let hosts = client.hosts().unwrap();
        assert_eq!(hosts.iter().map(|h| (h.id.as_str(), h.available())).collect::<Vec<_>>(), [("", true), ("h3a", true), ("h4b", false)]);
        assert_eq!(hosts[0].name, "forge");
        assert_eq!(seen.recv().unwrap().0, "/api/v1/hosts");
        client.session_options("").unwrap();
        assert_eq!(seen.recv().unwrap().0, "/api/v1/session-options");
        let models = client.models("claude", "h3a").unwrap();
        assert_eq!(seen.recv().unwrap().0, "/api/v1/models?surface=claude&host=h3a");
        assert_eq!((models[0].default, models[0].supported_reasoning_efforts.len(), models[1].title()), (true, 2, "Opus"));
        assert_eq!(client.claude_pick("h3a").unwrap().reason, "the primary root");
        assert_eq!(seen.recv().unwrap().0, "/api/v1/session-options/claude-pick?host=h3a");
        assert_eq!(client.claude_usage("/Users/a/my root", "h3a").unwrap().root, "/Users/a/my root");
        assert_eq!(seen.recv().unwrap().0, "/api/v1/session-options/claude-usage?root=%2FUsers%2Fa%2Fmy+root&host=h3a");
    }

    #[test]
    fn starting_posts_the_launch_and_reads_the_new_session() {
        let (client, seen) = daemon(vec![(201, r#"{"ok":true,"session":{"id":"s-new","surface":"claude"},"result":{"uuid":"u1"},"worktree":{"path":"/w/.worktrees/x"}}"#)]);
        let launch = Launch { worktree: true, ..claude() };
        assert_eq!(client.create_session(&launch), Outcome::Started { session: "s-new".into(), note: String::new() });
        let (target, body) = seen.recv().unwrap();
        assert_eq!(target, "/api/v1/actions");
        assert_eq!(body, json!({"action": "session-create", "surface": "claude", "message": "Fix the build", "cwd": "/work/app", "isolation": "worktree"}));
    }

    #[test]
    fn each_answer_maps_to_what_the_sheet_shows() {
        let (client, _seen) = daemon(vec![
            (202, r#"{"ok":true,"status":"submitted","accepted":true,"session":{"id":"s2"},"warning":"slow host"}"#),
            (202, r#"{"ok":true,"status":"submitted","launcher":"tmux"}"#),
            (502, r#"{"error":{"code":"initial_turn_failed","message":"model refused"},"session":{"id":"s3"},"deliveryId":4}"#),
            (502, r#"{"error":{"code":"create_failed","message":"claude exited"}}"#),
            (400, r#"{"error":{"code":"invalid_request","message":"cwd must be an absolute path"}}"#),
            (502, r#"{"error":{"code":"create_unknown","message":"initial turn outcome is ambiguous"}}"#),
            (504, r#"{"error":{"code":"host_outcome_unknown","message":"Host mac did not answer"}}"#),
            (502, r#"{"error":{"code":"bad_gateway","message":"host connection reset"}}"#),
        ]);
        let launch = claude();
        assert_eq!(client.create_session(&launch), Outcome::Started { session: "s2".into(), note: "slow host".into() });
        assert_eq!(client.create_session(&launch), Outcome::Submitted("Agenthail is still starting this session. It appears in the sidebar once it starts.".into()));
        assert_eq!(client.create_session(&launch), Outcome::Halted { session: "s3".into(), message: "model refused".into() });
        assert_eq!(client.create_session(&launch), Outcome::Failed("claude exited".into()));
        assert_eq!(client.create_session(&launch), Outcome::Failed("cwd must be an absolute path".into()));
        assert!(matches!(client.create_session(&launch), Outcome::Unknown { session: None, .. }));
        assert!(matches!(client.create_session(&launch), Outcome::Unknown { session: None, .. }));
        assert!(matches!(client.create_session(&launch), Outcome::Unknown { session: None, .. }));
    }

    #[test]
    fn a_launcher_that_answers_without_a_session_was_submitted() {
        let reply = Ok(json!({"ok": true, "status": "pending", "accepted": true, "launcher": "tmux", "warning": "focus it yourself"}));
        assert_eq!(outcome(reply, &claude()), Outcome::Submitted("Submitted to tmux. It appears in the sidebar once it starts.\nfocus it yourself".into()));
        let unsent = Err(RespondError { message: "connection refused".into(), code: String::new(), maybe_delivered: false, reply: Value::Null, answered: false, status: 0 });
        assert_eq!(outcome(unsent, &claude()), Outcome::Failed("connection refused".into()));
    }
}
