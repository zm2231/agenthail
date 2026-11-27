use std::collections::BTreeMap;
use std::io::{Read, Write};
use std::path::PathBuf;
use std::time::Duration;

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};

use crate::catalog::Client;
use crate::prefs::{self, Failure};

#[derive(Serialize, Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Definition {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub description: Option<String>,
    #[serde(default)]
    pub reach: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub actions: Option<BTreeMap<String, String>>,
    #[serde(default)]
    pub tools: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub depth: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub running: Option<i64>,
    #[serde(flatten)]
    pub rest: Map<String, Value>,
}

impl Definition {
    fn normalized(&self) -> Definition {
        Definition {
            description: self.description.clone().filter(|d| !d.is_empty()),
            actions: self.actions.clone().filter(|a| !a.is_empty()),
            depth: self.depth.filter(|d| *d > 0),
            running: self.running.filter(|r| *r > 0),
            ..self.clone()
        }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RoleView {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub built_in: bool,
    #[serde(default)]
    pub edited: bool,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub reach: String,
    #[serde(default)]
    pub decisions: BTreeMap<String, String>,
    #[serde(default)]
    pub definition: Definition,
    #[serde(default)]
    pub tools: Option<Vec<String>>,
    #[serde(default)]
    pub all_tools: bool,
    #[serde(default)]
    pub depth: i64,
    #[serde(default)]
    pub running: i64,
    #[serde(default)]
    pub effective_defaults: Option<BTreeMap<String, String>>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ActionView {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub default: String,
    #[serde(default)]
    pub detail: String,
    #[serde(default)]
    pub own_project: bool,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Assignment {
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub target: String,
    #[serde(default)]
    pub role: String,
    #[serde(default)]
    pub stale: Option<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Roles {
    #[serde(default)]
    pub roles: Vec<RoleView>,
    #[serde(default)]
    pub assignments: Vec<Assignment>,
    #[serde(default)]
    pub actions: Vec<ActionView>,
    #[serde(default)]
    pub tools: Vec<String>,
}

impl Roles {
    pub fn role(&self, name: &str) -> Option<&RoleView> {
        self.roles.iter().find(|r| r.name == name)
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct Loaded {
    pub roles: Roles,
    pub read_only: Option<String>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Decision {
    Allow,
    Notify,
    Ask,
}

impl Decision {
    pub const ALL: [Decision; 3] = [Decision::Allow, Decision::Notify, Decision::Ask];

    pub fn id(self) -> &'static str {
        match self {
            Decision::Allow => "allow",
            Decision::Notify => "notify",
            Decision::Ask => "ask",
        }
    }

    pub fn title(self) -> &'static str {
        match self {
            Decision::Allow => "Allow",
            Decision::Notify => "Notify",
            Decision::Ask => "Ask",
        }
    }

    pub fn of(id: &str) -> Option<Decision> {
        Decision::ALL.into_iter().find(|d| d.id() == id)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Kind {
    Session,
    Identity,
    Alias,
}

impl Kind {
    pub const ALL: [Kind; 3] = [Kind::Session, Kind::Identity, Kind::Alias];

    pub fn id(self) -> &'static str {
        match self {
            Kind::Session => "session",
            Kind::Identity => "identity",
            Kind::Alias => "alias",
        }
    }

    pub fn title(self) -> &'static str {
        match self {
            Kind::Session => "Session",
            Kind::Identity => "Identity",
            Kind::Alias => "Alias",
        }
    }

    pub fn footer(self) -> &'static str {
        match self {
            Kind::Session => "The session acts under this role in place of its usual one.",
            Kind::Identity => "An agent identity holds the role wherever it runs.",
            Kind::Alias => "Whichever session holds the alias acts under this role.",
        }
    }

    pub fn field(self) -> (&'static str, &'static str) {
        match self {
            Kind::Session => ("Or an id or alias", "@reviewer"),
            Kind::Identity => ("Identity", "reviewer"),
            Kind::Alias => ("Alias", "reviewer"),
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub enum Request {
    Put(String, Definition),
    Delete(String),
    Assign { role: String, kind: Kind, target: String },
    Unassign(Assignment),
}

impl Request {
    pub fn body(&self) -> Value {
        match self {
            Request::Put(name, role) => json!({"op": "put", "name": name, "role": role.normalized()}),
            Request::Delete(name) => json!({"op": "delete", "name": name}),
            Request::Assign { role, kind, target } => json!({"op": "assign", "name": role, "kind": kind.id(), "target": target}),
            Request::Unassign(a) => json!({"op": "unassign", "name": a.role, "kind": a.kind, "target": a.target}),
        }
    }
}

pub const REACHES: [(&str, &str); 3] = [("descendants", "Sessions it started"), ("project", "Its project"), ("all", "Every session")];
pub const DEFAULT_DEPTH: i64 = 3;
pub const DEFAULT_RUNNING: i64 = 8;
pub const DEPTH_RANGE: std::ops::RangeInclusive<i64> = 1..=20;
pub const RUNNING_RANGE: std::ops::RangeInclusive<i64> = 1..=64;
pub const DEFAULT_TOOLS_HINT: &str = "Every tool except those whose only action this role leaves to you.";
pub const READ_ONLY: &str = "Update the Agenthail daemon to change roles. Roles read here as they are.";

pub fn reach_title(reach: &str) -> &str {
    REACHES.iter().find(|(id, _)| *id == reach).map_or(reach, |(_, title)| title)
}

pub fn role_kind(role: &RoleView) -> &'static str {
    match (role.built_in, role.edited) {
        (true, true) => "Built in, changed",
        (true, false) => "Built in",
        _ => "Yours",
    }
}

fn effective_default<'a>(action: &'a ActionView, role: &'a RoleView) -> Option<&'a str> {
    if let Some(effective) = &role.effective_defaults {
        return Some(effective.get(&action.name).map_or(action.default.as_str(), String::as_str));
    }
    if role.definition.actions.as_ref().is_none_or(|a| !a.contains_key(&action.name)) {
        return role.decisions.get(&action.name).map(String::as_str);
    }
    None
}

pub fn default_title(action: &ActionView, role: &RoleView, reach: &str) -> String {
    match effective_default(action, role).filter(|d| reach == role.reach && !d.is_empty()) {
        Some(known) => format!("Default ({})", capitalized(known)),
        None => "Default".into(),
    }
}

fn capitalized(word: &str) -> String {
    let mut chars = word.chars();
    chars.next().map(|c| c.to_uppercase().chain(chars).collect()).unwrap_or_default()
}

pub fn name_hint(name: &str, taken: &[&str]) -> Option<String> {
    let name = name.trim();
    if name.is_empty() {
        return None;
    }
    if taken.contains(&name) {
        return Some(format!("A role named {name} exists."));
    }
    let mut chars = name.chars();
    let first = chars.next().is_some_and(|c| c.is_ascii_lowercase());
    let rest = chars.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-');
    if !first || !rest || name.len() > 40 {
        return Some("Lowercase letters, digits and dashes, starting with a letter.".into());
    }
    None
}

pub fn assignment_line(a: &Assignment, session_name: impl Fn(&str) -> Option<String>) -> String {
    if a.kind == "session" { session_name(&a.target).unwrap_or_else(|| a.target.clone()) } else { format!("@{}", a.target) }
}

pub fn assignment_kind(a: &Assignment) -> String {
    capitalized(&a.kind)
}

#[derive(Clone, Debug, PartialEq)]
pub struct Draft {
    pub name: String,
    base: Definition,
    pub definition: Definition,
}

impl Draft {
    pub fn of(role: &RoleView) -> Draft {
        Draft { name: role.name.clone(), base: role.definition.clone(), definition: role.definition.clone() }
    }

    pub fn copying(name: &str, from: &Definition) -> Draft {
        Draft { name: name.into(), base: from.clone(), definition: from.clone() }
    }

    pub fn changed(&self) -> bool {
        self.definition != self.base
    }

    pub fn conflicts(&self, role: &RoleView) -> bool {
        self.changed() && self.base != role.definition
    }

    pub fn follow(&mut self, role: &RoleView) {
        if !self.changed() && self.base != role.definition {
            *self = Draft::of(role);
        }
    }

    pub fn decision(&self, action: &str) -> Option<Decision> {
        self.definition.actions.as_ref()?.get(action).and_then(|d| Decision::of(d))
    }

    pub fn set_decision(&mut self, action: &str, decision: Option<Decision>) {
        let mut actions = self.definition.actions.take().unwrap_or_default();
        match decision {
            Some(d) => actions.insert(action.into(), d.id().into()),
            None => actions.remove(action),
        };
        self.definition.actions = (!actions.is_empty()).then_some(actions);
    }

    pub fn depth(&self) -> i64 {
        self.definition.depth.filter(|d| *d > 0).unwrap_or(DEFAULT_DEPTH)
    }

    pub fn set_depth(&mut self, depth: i64) {
        let depth = depth.clamp(*DEPTH_RANGE.start(), *DEPTH_RANGE.end());
        self.definition.depth = (depth != DEFAULT_DEPTH).then_some(depth);
    }

    pub fn running(&self) -> i64 {
        self.definition.running.filter(|r| *r > 0).unwrap_or(DEFAULT_RUNNING)
    }

    pub fn set_running(&mut self, running: i64) {
        let running = running.clamp(*RUNNING_RANGE.start(), *RUNNING_RANGE.end());
        self.definition.running = (running != DEFAULT_RUNNING).then_some(running);
    }

    pub fn default_tools(&self) -> bool {
        self.definition.tools.is_none()
    }

    pub fn sees(&self, tool: &str) -> bool {
        self.definition.tools.as_ref().is_none_or(|t| t.iter().any(|x| x == tool))
    }

    pub fn set_default_tools(&mut self, on: bool, seen: &[String]) {
        self.definition.tools = if on { None } else { Some(seen.to_vec()) };
    }

    pub fn set_tool(&mut self, tool: &str, shown: bool, every: &[String]) {
        let current = self.definition.tools.clone().unwrap_or_else(|| every.to_vec());
        let keep = |t: &String| if t == tool { shown } else { current.contains(t) };
        self.definition.tools = Some(every.iter().filter(|t| keep(t)).cloned().collect());
    }

    pub fn request(&self) -> Request {
        Request::Put(self.name.clone(), self.definition.clone())
    }
}

pub fn limit_label(value: i64, fallback: i64) -> String {
    if value == fallback { format!("{value} (default)") } else { value.to_string() }
}

pub fn socket_path() -> Option<PathBuf> {
    std::env::var_os("HOME").map(|home| PathBuf::from(home).join(".agenthail/local.sock"))
}

const SOCKET_TIMEOUT: Duration = Duration::from_secs(4);

#[derive(Debug, PartialEq)]
pub enum SocketError {
    Missing,
    Failed(Failure),
}

fn socket_call(method: &str, target: &str, body: Option<&Value>) -> Result<Value, SocketError> {
    #[cfg(unix)]
    {
        let Some(path) = socket_path().filter(|p| p.exists()) else { return Err(SocketError::Missing) };
        let mut stream = match std::os::unix::net::UnixStream::connect(&path) {
            Ok(s) => s,
            Err(e) if matches!(e.kind(), std::io::ErrorKind::NotFound | std::io::ErrorKind::ConnectionRefused) => return Err(SocketError::Missing),
            Err(e) => return Err(SocketError::Failed(Failure::new(format!("Agenthail's local socket couldn't be reached: {e}")))),
        };
        let unanswered = |e: std::io::Error| {
            if matches!(e.kind(), std::io::ErrorKind::WouldBlock | std::io::ErrorKind::TimedOut) {
                SocketError::Failed(Failure { uncertain: method != "GET", ..Failure::new(format!("Agenthail did not answer within {} seconds. Try again in a moment.", SOCKET_TIMEOUT.as_secs())) })
            } else {
                SocketError::Failed(Failure { uncertain: method != "GET", ..Failure::new(format!("Agenthail's answer could not be read: {e}")) })
            }
        };
        stream.set_read_timeout(Some(SOCKET_TIMEOUT)).map_err(unanswered)?;
        stream.set_write_timeout(Some(SOCKET_TIMEOUT)).map_err(unanswered)?;
        let payload = body.map(Value::to_string).unwrap_or_default();
        let mut head = format!("{method} {target} HTTP/1.1\r\nHost: localhost\r\nAccept: application/json\r\nConnection: close\r\n");
        if body.is_some() {
            head.push_str(&format!("Content-Type: application/json\r\nContent-Length: {}\r\n", payload.len()));
        }
        head.push_str("\r\n");
        stream.write_all(head.as_bytes()).and_then(|()| stream.write_all(payload.as_bytes())).map_err(unanswered)?;
        let mut raw = Vec::new();
        stream.read_to_end(&mut raw).map_err(unanswered)?;
        let (status, body) = parse_response(&raw).ok_or_else(|| SocketError::Failed(Failure { uncertain: method != "GET", ..Failure::new("Agenthail's answer could not be read.") }))?;
        if !(200..300).contains(&status) {
            let reply = serde_json::from_slice::<Value>(&body).unwrap_or_default();
            let message = reply["error"]["message"].as_str().filter(|m| !m.is_empty()).map_or_else(|| format!("status code {status}"), str::to_string);
            return Err(SocketError::Failed(Failure::new(message)));
        }
        serde_json::from_slice(&body).map_err(|e| SocketError::Failed(Failure { uncertain: method != "GET", ..Failure::new(format!("Agenthail's answer could not be read: {e}")) }))
    }
    #[cfg(not(unix))]
    {
        let _ = (method, target, body);
        Err(SocketError::Missing)
    }
}

pub fn parse_response(raw: &[u8]) -> Option<(u16, Vec<u8>)> {
    let split = raw.windows(4).position(|w| w == b"\r\n\r\n")?;
    let head = std::str::from_utf8(&raw[..split]).ok()?;
    let rest = &raw[split + 4..];
    let mut lines = head.split("\r\n");
    let status = lines.next()?.split_whitespace().nth(1)?.parse().ok()?;
    let mut chunked = false;
    let mut length = None;
    for line in lines {
        let Some((name, value)) = line.split_once(':') else { continue };
        let (name, value) = (name.trim().to_ascii_lowercase(), value.trim());
        if name == "transfer-encoding" && value.to_ascii_lowercase().contains("chunked") {
            chunked = true;
        } else if name == "content-length" {
            length = value.parse::<usize>().ok();
        }
    }
    if chunked {
        return dechunk(rest).map(|body| (status, body));
    }
    let body = match length {
        Some(n) => rest.get(..n)?.to_vec(),
        None => rest.to_vec(),
    };
    Some((status, body))
}

fn dechunk(mut rest: &[u8]) -> Option<Vec<u8>> {
    let mut out = Vec::new();
    loop {
        let end = rest.windows(2).position(|w| w == b"\r\n")?;
        let size_line = std::str::from_utf8(&rest[..end]).ok()?;
        let size = usize::from_str_radix(size_line.split(';').next()?.trim(), 16).ok()?;
        rest = &rest[end + 2..];
        if size == 0 {
            return Some(out);
        }
        out.extend_from_slice(rest.get(..size)?);
        rest = rest.get(size + 2..)?;
    }
}

impl Client {
    pub fn agent_roles(&self) -> Result<Loaded, Failure> {
        match socket_call("GET", "/api/v1/agents", None) {
            Ok(value) => Ok(Loaded { roles: roles_reply(value)?, read_only: None }),
            Err(SocketError::Failed(f)) => Err(f),
            Err(SocketError::Missing) => Ok(Loaded { roles: prefs::read(self, "/api/v1/agents", "settings")?, read_only: Some(READ_ONLY.into()) }),
        }
    }

    pub fn change_agent_role(&self, request: &Request) -> Result<Roles, Change> {
        match socket_call("POST", "/api/v1/agents", Some(&request.body())) {
            Ok(value) => roles_reply(value).map_err(Change::Failed),
            Err(SocketError::Missing) => Err(Change::ReadOnly),
            Err(SocketError::Failed(f)) => Err(Change::Failed(f)),
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub enum Change {
    ReadOnly,
    Failed(Failure),
}

fn roles_reply(value: Value) -> Result<Roles, Failure> {
    serde_json::from_value(value).map_err(|e| Failure::new(format!("the daemon's reply couldn't be read: {e}")))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn role(json: Value) -> RoleView {
        serde_json::from_value(json).unwrap()
    }

    fn action(name: &str, default: &str) -> ActionView {
        ActionView { name: name.into(), default: default.into(), detail: String::new(), own_project: false }
    }

    #[test]
    fn a_role_put_keeps_what_this_app_does_not_edit_and_drops_empty_fields() {
        let r = role(json!({"name": "reviewer", "definition": {"reach": "project", "tools": null, "ledgers": ["p-1"], "description": "", "actions": {}, "depth": 0}}));
        let body = Draft::of(&r).request().body();
        assert_eq!(body, json!({"op": "put", "name": "reviewer", "role": {"reach": "project", "tools": null, "ledgers": ["p-1"]}}));
    }

    #[test]
    fn assignment_requests_name_the_role_kind_and_target() {
        let a = Assignment { kind: "alias".into(), target: "rev".into(), role: "reviewer".into(), stale: None };
        assert_eq!(Request::Unassign(a).body(), json!({"op": "unassign", "name": "reviewer", "kind": "alias", "target": "rev"}));
        assert_eq!(Request::Assign { role: "reviewer".into(), kind: Kind::Session, target: "s1".into() }.body(), json!({"op": "assign", "name": "reviewer", "kind": "session", "target": "s1"}));
        assert_eq!(Request::Delete("x".into()).body(), json!({"op": "delete", "name": "x"}));
    }

    #[test]
    fn decisions_limits_and_tools_edit_like_the_mac_app() {
        let r = role(json!({"name": "worker", "definition": {"reach": "descendants", "tools": null}}));
        let mut d = Draft::of(&r);
        assert!(!d.changed());
        d.set_decision("send", Some(Decision::Ask));
        assert_eq!(d.decision("send"), Some(Decision::Ask));
        d.set_decision("send", None);
        assert_eq!(d.definition.actions, None, "clearing the last decision leaves no actions");
        assert!(!d.changed());
        assert_eq!((d.depth(), d.running()), (DEFAULT_DEPTH, DEFAULT_RUNNING));
        d.set_depth(5);
        d.set_running(99);
        assert_eq!((d.definition.depth, d.definition.running), (Some(5), Some(64)));
        d.set_depth(DEFAULT_DEPTH);
        assert_eq!(d.definition.depth, None, "the default is stored as unset");
        let every: Vec<String> = ["a", "b", "c"].map(String::from).to_vec();
        assert!(d.default_tools() && d.sees("b"));
        d.set_tool("b", false, &every);
        assert_eq!(d.definition.tools, Some(vec!["a".to_string(), "c".to_string()]));
        d.set_tool("b", true, &every);
        assert_eq!(d.definition.tools, Some(every.clone()), "tools keep the daemon's order");
        d.set_default_tools(true, &every);
        assert!(d.default_tools());
    }

    #[test]
    fn a_change_from_elsewhere_replaces_an_untouched_draft_and_flags_an_edited_one() {
        let before = role(json!({"name": "w", "definition": {"reach": "project"}}));
        let after = role(json!({"name": "w", "definition": {"reach": "all"}}));
        let mut quiet = Draft::of(&before);
        quiet.follow(&after);
        assert_eq!(quiet.definition.reach, "all");
        let mut edited = Draft::of(&before);
        edited.definition.description = Some("mine".into());
        edited.follow(&after);
        assert_eq!(edited.definition.reach, "project");
        assert!(edited.conflicts(&after));
        assert!(!edited.conflicts(&before));
    }

    #[test]
    fn default_titles_name_the_decision_only_for_the_saved_reach() {
        let r = role(json!({"name": "w", "reach": "project", "definition": {"reach": "project"}, "effectiveDefaults": {"send": "notify"}}));
        assert_eq!(default_title(&action("send", "ask"), &r, "project"), "Default (Notify)");
        assert_eq!(default_title(&action("spawn", "ask"), &r, "project"), "Default (Ask)");
        assert_eq!(default_title(&action("send", "ask"), &r, "all"), "Default");
        let old = role(json!({"name": "w", "reach": "project", "definition": {"reach": "project", "actions": {"send": "allow"}}, "decisions": {"send": "allow", "spawn": "ask"}}));
        assert_eq!(default_title(&action("send", "ask"), &old, "project"), "Default");
        assert_eq!(default_title(&action("spawn", "ask"), &old, "project"), "Default (Ask)");
    }

    #[test]
    fn new_role_names_follow_the_daemons_rule() {
        assert_eq!(name_hint("  ", &[]), None);
        assert_eq!(name_hint("reviewer", &["reviewer"]).as_deref(), Some("A role named reviewer exists."));
        assert!(name_hint("Reviewer", &[]).is_some());
        assert!(name_hint("9lives", &[]).is_some());
        assert!(name_hint(&"a".repeat(41), &[]).is_some());
        assert_eq!(name_hint("code-review2", &[]), None);
    }

    #[test]
    fn rows_name_sessions_and_mark_other_targets() {
        let session = Assignment { kind: "session".into(), target: "s1".into(), role: "r".into(), stale: None };
        assert_eq!(assignment_line(&session, |id| (id == "s1").then(|| "@lead".into())), "@lead");
        assert_eq!(assignment_line(&session, |_| None), "s1");
        let alias = Assignment { kind: "alias".into(), ..session };
        assert_eq!(assignment_line(&alias, |_| None), "@s1");
        assert_eq!(assignment_kind(&alias), "Alias");
        assert_eq!(role_kind(&role(json!({"builtIn": true, "edited": true}))), "Built in, changed");
        assert_eq!(limit_label(3, DEFAULT_DEPTH), "3 (default)");
    }

    #[test]
    fn socket_replies_read_with_a_length_or_in_chunks() {
        let plain = b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n{}extra";
        assert_eq!(parse_response(plain), Some((200, b"{}".to_vec())));
        let chunked = b"HTTP/1.1 403 Forbidden\r\nTransfer-Encoding: chunked\r\n\r\n3\r\n{\"a\r\n4;x=y\r\n\":1}\r\n0\r\n\r\n";
        assert_eq!(parse_response(chunked), Some((403, b"{\"a\":1}".to_vec())));
        assert_eq!(parse_response(b"HTTP/1.1 200 OK\r\n"), None);
        assert_eq!(parse_response(b"HTTP/1.1 200 OK\r\nContent-Length: 9\r\n\r\n{}"), None);
    }

    #[test]
    fn the_daemons_roles_reply_reads() {
        let value = json!({"roles": [{"name": "lead", "builtIn": true, "edited": false, "description": "Runs a project", "reach": "project", "decisions": {"send": "allow"}, "effectiveDefaults": {"send": "allow"}, "definition": {"reach": "project", "tools": null, "ledgers": null}, "tools": ["agenthail_send"], "allTools": true, "ledgers": [], "allLedgers": false, "depth": 3, "running": 8}], "assignments": [{"kind": "alias", "target": "rev", "role": "lead", "stale": "no session holds @rev"}], "actions": [{"name": "send", "default": "ask", "detail": "Message a session", "ownProject": true}], "tools": ["agenthail_send"], "identities": []});
        let roles = roles_reply(value).unwrap();
        assert_eq!(roles.role("lead").map(|r| r.depth), Some(3));
        assert_eq!(roles.assignments[0].stale.as_deref(), Some("no session holds @rev"));
        assert_eq!(roles.actions[0].detail, "Message a session");
    }
}
