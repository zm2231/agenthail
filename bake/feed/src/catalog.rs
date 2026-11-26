use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Duration;

use anyhow::{Result, anyhow};
use serde::Deserialize;
use serde_json::Value;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Named {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub display_name: String,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub root_path: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Display {
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub detail: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Attention {
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub reason: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Headline {
    #[serde(default)]
    pub headline: String,
    #[serde(default)]
    pub outcome: String,
    #[serde(default)]
    pub at: Option<String>,
    #[serde(default)]
    pub turn_id: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Checkout {
    #[serde(default)]
    pub path: Option<String>,
    #[serde(default)]
    pub branch: Option<String>,
    #[serde(default)]
    pub dirty: bool,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct HeldWork {
    #[serde(default)]
    pub item_id: String,
    #[serde(default)]
    pub text: String,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub project_key: String,
    #[serde(default)]
    pub log: Option<Value>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Plan {
    #[serde(default)]
    pub done: u32,
    #[serde(default)]
    pub total: u32,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Pr {
    #[serde(default)]
    pub number: u64,
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub url: String,
    #[serde(default)]
    pub repo: String,
    #[serde(default)]
    pub watch: bool,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub checks: String,
    #[serde(default)]
    pub problem: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct TurnState {
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub reason: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Session {
    pub id: String,
    #[serde(default)]
    pub surface: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub alias: Option<String>,
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub last_active: String,
    #[serde(default)]
    pub open: bool,
    #[serde(default)]
    pub current: bool,
    #[serde(default)]
    pub archived: bool,
    #[serde(default)]
    pub display: Display,
    #[serde(default)]
    pub attention: Option<Attention>,
    #[serde(default)]
    pub turn_state: Option<TurnState>,
    #[serde(default)]
    pub turn_completion: Option<Headline>,
    #[serde(default)]
    pub end_turn: Option<Headline>,
    #[serde(default)]
    pub project: Option<Named>,
    #[serde(default)]
    pub host: Named,
    #[serde(default)]
    pub checkout: Checkout,
    #[serde(default)]
    pub held_work: Vec<HeldWork>,
    #[serde(default)]
    pub plan: Option<Plan>,
    #[serde(default)]
    pub prs: Vec<Pr>,
    #[serde(default)]
    pub usage_limit: Option<crate::limit::UsageLimit>,
    #[serde(default)]
    pub limit_resume: Option<crate::limit::LimitResume>,
    #[serde(default)]
    pub claude_config_root: Option<crate::limit::SessionRoot>,
    #[serde(default)]
    pub queue_count: i64,
    #[serde(default)]
    pub cwd: Option<String>,
    #[serde(default)]
    pub started_by: Option<String>,
    #[serde(default)]
    pub subagent: Option<Value>,
    #[serde(default)]
    pub delegated: Option<bool>,
    #[serde(default)]
    pub pinned_at: Option<String>,
    #[serde(default)]
    pub read_state: Option<crate::action::ReadState>,
    #[serde(default)]
    pub runtime: Option<Runtime>,
    #[serde(default, deserialize_with = "capabilities_or_default")]
    pub capabilities: Capabilities,
    #[serde(default)]
    pub read_only: bool,
    #[serde(default)]
    pub unavailable_reason: Option<String>,
}

#[derive(Deserialize, Clone, Copy, Debug, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Capabilities {
    #[serde(default)]
    pub interrupt: bool,
    #[serde(default)]
    pub steer: bool,
    #[serde(default)]
    pub model: bool,
    #[serde(default)]
    pub compact: bool,
    #[serde(default)]
    pub commands: bool,
    #[serde(default)]
    pub lifecycle: Lifecycle,
}

#[derive(Deserialize, Clone, Copy, Debug, Default, PartialEq)]
pub struct Lifecycle {
    #[serde(default)]
    pub resume: bool,
}

fn capabilities_or_default<'de, D: serde::Deserializer<'de>>(d: D) -> std::result::Result<Capabilities, D::Error> {
    Ok(Option::<Capabilities>::deserialize(d)?.unwrap_or_default())
}

impl Default for Capabilities {
    fn default() -> Self {
        Capabilities { interrupt: true, steer: true, model: false, compact: false, commands: false, lifecycle: Lifecycle::default() }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Runtime {
    #[serde(default)]
    pub focusable: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum State {
    NeedsYou,
    Working,
    Done,
    Idle,
}

impl State {
    pub fn label(self) -> &'static str {
        match self {
            State::NeedsYou => "Needs you",
            State::Working => "Working",
            State::Done => "Done",
            State::Idle => "Idle",
        }
    }
}

pub fn parse_time(ts: &str) -> Option<OffsetDateTime> {
    OffsetDateTime::parse(ts, &Rfc3339).ok()
}

pub fn ago(ts: &str, now: OffsetDateTime) -> String {
    let Some(at) = parse_time(ts) else { return String::new() };
    let seconds = (now - at).whole_seconds().max(0);
    match seconds {
        0..=59 => "now".into(),
        60..=3599 => format!("{}m", seconds / 60),
        3600..=86399 => format!("{}h", seconds / 3600),
        _ => format!("{}d", seconds / 86400),
    }
}

impl Session {
    pub fn title(&self) -> String {
        match &self.alias {
            Some(alias) if !alias.is_empty() => format!("@{alias}"),
            _ if self.name.trim_start().starts_with('<') || self.name.trim().is_empty() => "Untitled session".into(),
            _ => self.name.clone(),
        }
    }

    pub fn project_name(&self) -> String {
        self.project.as_ref().map(|p| if p.display_name.is_empty() { p.name.clone() } else { p.display_name.clone() }).unwrap_or_else(|| "No project".into())
    }

    pub fn primary_work(&self, pick: Option<&str>) -> Option<&HeldWork> {
        let rank = |w: &HeldWork| match w.state.as_str() {
            "in_progress" => 0,
            "in_review" => 1,
            "blocked" => 2,
            _ => 3,
        };
        pick.and_then(|id| self.held_work.iter().find(|w| w.item_id == id)).or_else(|| self.held_work.iter().min_by_key(|w| rank(w)))
    }

    pub fn checkout_path(&self) -> Option<String> {
        self.checkout.path.clone().or_else(|| self.cwd.clone())
    }

    pub fn project_key(&self) -> String {
        self.project.as_ref().map(|p| p.id.clone()).unwrap_or_default()
    }

    pub fn state(&self, now: OffsetDateTime) -> State {
        let waiting = self.attention.is_some()
            || self.turn_state.as_ref().is_some_and(|t| t.state == "waiting_person")
            || matches!(self.display.kind.as_str(), "waiting_person" | "needs_you" | "ask");
        if waiting {
            return State::NeedsYou;
        }
        if self.status == "busy" || matches!(self.display.kind.as_str(), "working" | "finishing" | "starting") {
            return State::Working;
        }
        let recent = parse_time(&self.last_active).is_some_and(|t| (now - t).whole_hours() < 3);
        let finished = self.turn_completion.as_ref().or(self.end_turn.as_ref()).is_some();
        if recent && finished { State::Done } else { State::Idle }
    }

    pub fn summary(&self) -> String {
        if let Some(stop) = self.limit(OffsetDateTime::now_utc()) {
            return stop.label;
        }
        if let Some(a) = &self.attention {
            if !a.reason.is_empty() {
                return a.reason.clone();
            }
        }
        if self.status == "busy" && !self.display.detail.is_empty() {
            return self.display.detail.clone();
        }
        for h in [&self.end_turn, &self.turn_completion].into_iter().flatten() {
            if !h.headline.is_empty() {
                return h.headline.clone();
            }
        }
        self.held_work.first().map(|w| w.text.clone()).unwrap_or_else(|| self.display.label.clone())
    }

    pub fn visible(&self, now: OffsetDateTime) -> bool {
        if self.archived {
            return false;
        }
        let state = self.state(now);
        let recent = parse_time(&self.last_active).is_some_and(|t| (now - t).whole_hours() < 24);
        let closed = self.display.label == "Closed";
        self.pinned_at.is_some() || matches!(state, State::NeedsYou | State::Working) || (recent && !closed && (self.open || self.current || self.alias.is_some()))
    }
}

#[derive(Deserialize, Clone, Debug, Default)]
#[serde(rename_all = "camelCase")]
pub struct Snapshot {
    #[serde(default)]
    pub updated_at: String,
    #[serde(default)]
    pub sessions: Vec<Session>,
    #[serde(default)]
    pub busy_delivery: String,
    #[serde(default)]
    pub rekeyed: std::collections::HashMap<String, String>,
    #[serde(default)]
    pub queue: Vec<QueueRow>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct QueueRow {
    #[serde(default)]
    pub id: i64,
    #[serde(default)]
    pub session_id: String,
    #[serde(default)]
    pub status: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct TimelineEntry {
    #[serde(default)]
    pub at: String,
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub state: Option<String>,
    #[serde(default)]
    pub text: Option<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct WorkItem {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub text: String,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub priority: String,
    #[serde(rename = "type", default)]
    pub kind: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub acceptance: String,
    #[serde(default)]
    pub timeline: Vec<TimelineEntry>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct BoardRow {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub text: String,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub priority: String,
    #[serde(rename = "type", default)]
    pub kind: String,
}

pub fn phase(state: &str) -> usize {
    match state {
        "open" | "ready" => 0,
        "in_progress" => 1,
        "in_review" => 2,
        "done" | "closed" => 4,
        _ => 0,
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct RespondError {
    pub message: String,
    pub code: String,
    pub maybe_delivered: bool,
    pub reply: Value,
    pub answered: bool,
    pub status: u16,
}

impl RespondError {
    pub fn from_status(action: &str, status: u16, body: &str) -> Self {
        let reply = serde_json::from_str::<Value>(body).unwrap_or_default();
        let error = reply["error"].clone();
        let code = error["code"].as_str().unwrap_or_default().to_string();
        let message = error["message"].as_str().map(str::to_string).unwrap_or_else(|| format!("status code {status}"));
        let released_answer = action == "respond" && code == "bad_gateway" && !message.starts_with("the answer may have reached");
        let maybe_delivered = match status {
            504 => true,
            502 => !released_answer,
            _ => false,
        };
        RespondError { message, code, maybe_delivered, reply, answered: true, status }
    }

    fn from_transport(error: &ureq::Transport) -> Self {
        use ureq::ErrorKind::*;
        let unsent = matches!(error.kind(), InvalidUrl | UnknownScheme | Dns | InsecureRequestHttpsOnly | ConnectionFailed | InvalidProxyUrl | ProxyConnect | ProxyUnauthorized);
        RespondError { message: error.to_string(), code: String::new(), maybe_delivered: !unsent, reply: Value::Null, answered: false, status: 0 }
    }
}

pub const PHASES: [&str; 5] = ["Plan", "Build", "Review", "Merge", "Done"];

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RequestOption {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub description: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RequestQuestion {
    #[serde(default)]
    pub question: String,
    #[serde(default)]
    pub header: String,
    #[serde(default)]
    pub multi_select: bool,
    #[serde(default)]
    pub options: Vec<RequestOption>,
    #[serde(default)]
    pub suggested: Option<usize>,
    #[serde(default)]
    pub links: Vec<RequestLink>,
    #[serde(default)]
    pub detail: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct RequestLink {
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub url: String,
}

pub fn suggests(suggested: Option<usize>, index: usize, label: &str) -> bool {
    match suggested {
        Some(pick) => pick == index,
        None => {
            let label = label.trim().to_lowercase();
            label.ends_with("(recommended)") || label.ends_with("— recommended") || label.ends_with("- recommended") || label.contains("(recommended)")
        }
    }
}

impl RequestLink {
    pub fn is_web(&self) -> bool {
        self.url.starts_with("https://") || self.url.starts_with("http://")
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RequestAction {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub title: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OpenRequest {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub tool: String,
    #[serde(default)]
    pub title: Option<String>,
    #[serde(default)]
    pub description: Option<String>,
    #[serde(default)]
    pub questions: Option<Vec<RequestQuestion>>,
    #[serde(default)]
    pub actions: Vec<RequestAction>,
}

pub fn numbered_options(body: &str) -> Vec<String> {
    let mut found = Vec::new();
    let mut expect = 1;
    for line in body.lines() {
        let t = line.trim();
        let digits: String = t.chars().take_while(|c| c.is_ascii_digit()).collect();
        let rest = &t[digits.len()..];
        if !digits.is_empty() && (rest.starts_with(". ") || rest.starts_with(") ")) && digits.parse::<usize>().ok() == Some(expect) {
            let label = rest[2..].replace("**", "").replace('`', "");
            found.push(label.trim().to_string());
            expect += 1;
        } else if expect > 1 && !t.is_empty() && !line.starts_with(' ') {
            if found.len() >= 2 {
                break;
            }
            found.clear();
            expect = 1;
        }
    }
    if found.len() >= 2 && found.len() <= 6 { found } else { Vec::new() }
}

#[derive(Clone)]
pub struct Client {
    base: String,
    token: String,
    agent: ureq::Agent,
}

pub(crate) fn enc(value: &str) -> String {
    url::form_urlencoded::byte_serialize(value.as_bytes()).collect()
}

impl Client {
    pub fn from_env() -> Result<Self> {
        let (base, token) = crate::daemon()?;
        Ok(Self::new(&base, &token))
    }

    pub fn new(base: &str, token: &str) -> Self {
        let agent = ureq::AgentBuilder::new().timeout_connect(Duration::from_secs(5)).timeout(Duration::from_secs(30)).build();
        Self { base: base.trim_end_matches('/').into(), token: token.into(), agent }
    }

    pub(crate) fn timed(&self, timeout: Duration) -> Client {
        let agent = ureq::AgentBuilder::new().timeout_connect(timeout.min(Duration::from_secs(5))).timeout(timeout).build();
        Client { base: self.base.clone(), token: self.token.clone(), agent }
    }

    fn get(&self, path: &str) -> Result<Value> {
        let response = self
            .agent
            .get(&format!("{}{path}", self.base))
            .set("Authorization", &format!("Bearer {}", self.token))
            .set("Accept", "application/json")
            .call()
            .map_err(|e| anyhow!(e.to_string()))?;
        Ok(response.into_json()?)
    }

    pub(crate) fn get_json(&self, path: &str) -> Result<Value> {
        self.get(path)
    }

    pub(crate) fn get_coded(&self, path: &str) -> std::result::Result<Value, (u16, String)> {
        match self.agent.get(&format!("{}{path}", self.base)).set("Authorization", &format!("Bearer {}", self.token)).set("Accept", "application/json").call() {
            Ok(response) => response.into_json().map_err(|e| (0, e.to_string())),
            Err(ureq::Error::Status(code, response)) => {
                let body: Value = response.into_json().unwrap_or(Value::Null);
                Err((code, body["error"]["code"].as_str().unwrap_or_default().to_string()))
            }
            Err(ureq::Error::Transport(error)) => Err((0, error.to_string())),
        }
    }

    pub(crate) fn get_reply(&self, path: &str) -> std::result::Result<Value, (u16, String)> {
        let request = self.agent.get(&format!("{}{path}", self.base)).set("Authorization", &format!("Bearer {}", self.token)).set("Accept", "application/json");
        match request.call() {
            Ok(response) => response.into_json().map_err(|e| (0, e.to_string())),
            Err(ureq::Error::Status(code, response)) => Err((code, response.into_string().unwrap_or_default())),
            Err(error) => Err((0, error.to_string())),
        }
    }

    pub(crate) fn ledger_rows(&self, project: &str) -> Result<Vec<Value>> {
        let mut rows = Vec::new();
        let mut cursor: Option<String> = None;
        for _ in 0..20 {
            let mut path = format!("/api/v1/ledger?projectKey={}&view=work&limit=100", enc(project));
            if let Some(c) = &cursor {
                path.push_str(&format!("&cursor={}", enc(c)));
            }
            let value = self.get(&path)?;
            rows.extend(value["work"].as_array().cloned().unwrap_or_default());
            cursor = value["nextCursor"].as_str().filter(|c| !c.is_empty()).map(String::from);
            if cursor.is_none() {
                break;
            }
        }
        Ok(rows)
    }

    pub(crate) fn session_stream(&self, session: &str, budget: Duration) -> Result<impl std::io::BufRead + use<>> {
        let agent = ureq::AgentBuilder::new().timeout(budget).build();
        let response = agent
            .get(&format!("{}/api/v1/session-stream?id={}&after=0", self.base, enc(session)))
            .set("Authorization", &format!("Bearer {}", self.token))
            .set("Accept", "text/event-stream")
            .call()
            .map_err(|e| anyhow!(e.to_string()))?;
        Ok(std::io::BufReader::new(response.into_reader()))
    }

    pub fn snapshot(&self) -> Result<Snapshot> {
        Ok(serde_json::from_value(self.get("/api/v1/snapshot")?)?)
    }

    pub fn work_item(&self, project: &str, id: &str) -> Result<WorkItem> {
        let value = self.get(&format!("/api/v1/ledger?projectKey={}&view=item&id={}", enc(project), enc(id)))?;
        Ok(serde_json::from_value(value["item"]["item"].clone())?)
    }

    pub fn board(&self, project: &str) -> Result<Vec<BoardRow>> {
        let mut rows = Vec::new();
        let mut cursor: Option<String> = None;
        for _ in 0..20 {
            let mut path = format!("/api/v1/ledger?projectKey={}&view=work&limit=100", enc(project));
            if let Some(c) = &cursor {
                path.push_str(&format!("&cursor={}", enc(c)));
            }
            let value = self.get(&path)?;
            let page: Vec<BoardRow> = serde_json::from_value(value["work"].clone()).unwrap_or_default();
            rows.extend(page);
            cursor = value["nextCursor"].as_str().filter(|c| !c.is_empty()).map(String::from);
            if cursor.is_none() {
                break;
            }
        }
        Ok(rows)
    }

    pub fn requests(&self, session: &str) -> Result<Vec<OpenRequest>> {
        let value = self.get(&format!("/api/v1/requests?sessionId={}", enc(session)))?;
        Ok(serde_json::from_value(value["requests"].clone()).unwrap_or_default())
    }

    pub fn respond(&self, session: &str, request: &str, choices: Vec<String>, answers: std::collections::BTreeMap<String, String>) -> std::result::Result<(), RespondError> {
        self.action("respond", serde_json::json!({"sessionId": session, "requestId": request, "decision": {"choices": choices, "answers": answers}})).and_then(confirmed).map(drop)
    }

    pub fn send(&self, session: &str, message: &str) -> std::result::Result<(), RespondError> {
        self.action("send", serde_json::json!({"sessionId": session, "message": message, "author": "person"})).map(drop)
    }

    pub(crate) fn post_json(&self, path: &str, body: Value) -> std::result::Result<Value, RespondError> {
        match self.agent.post(&format!("{}{path}", self.base)).set("Authorization", &format!("Bearer {}", self.token)).set("Accept", "application/json").send_json(body) {
            Ok(response) => response.into_json().map_err(|e| RespondError { message: format!("the daemon's reply couldn't be read: {e}"), code: "unreadable_reply".into(), maybe_delivered: true, answered: true, reply: Value::Null, status: 0 }),
            Err(ureq::Error::Status(code, response)) => Err(RespondError::from_status(path, code, &response.into_string().unwrap_or_default())),
            Err(ureq::Error::Transport(error)) => Err(RespondError::from_transport(&error)),
        }
    }

    pub fn action(&self, name: &str, payload: Value) -> std::result::Result<Value, RespondError> {
        self.action_keyed(name, payload, &request_key())
    }

    pub fn action_keyed(&self, name: &str, payload: Value, key: &str) -> std::result::Result<Value, RespondError> {
        let body = action_body(name, payload)?;
        match self
            .agent
            .post(&format!("{}/api/v1/actions", self.base))
            .set("Authorization", &format!("Bearer {}", self.token))
            .set("Idempotency-Key", key)
            .send_json(body)
        {
            Ok(response) => response.into_json().map_err(|e| RespondError { message: format!("the daemon's reply couldn't be read: {e}"), code: "unreadable_reply".into(), maybe_delivered: true, answered: true, reply: Value::Null, status: 0 }),
            Err(ureq::Error::Status(code, response)) => Err(RespondError::from_status(name, code, &response.into_string().unwrap_or_default())),
            Err(ureq::Error::Transport(error)) => Err(RespondError::from_transport(&error)),
        }
    }
}

impl Client {
    pub(crate) fn post_empty(&self, path: &str, name: &str, key: &str) -> std::result::Result<Value, RespondError> {
        match self.agent.post(&format!("{}{path}", self.base)).set("Authorization", &format!("Bearer {}", self.token)).set("Idempotency-Key", key).call() {
            Ok(response) => response.into_json().map_err(|e| RespondError { message: format!("the daemon's reply couldn't be read: {e}"), code: "unreadable_reply".into(), maybe_delivered: true, answered: true, reply: Value::Null, status: 0 }),
            Err(ureq::Error::Status(code, response)) => Err(RespondError::from_status(name, code, &response.into_string().unwrap_or_default())),
            Err(ureq::Error::Transport(error)) => Err(RespondError::from_transport(&error)),
        }
    }
}

pub fn confirmed(reply: Value) -> std::result::Result<Value, RespondError> {
    if reply["status"] == "submitted" {
        return Err(RespondError { message: "the daemon took it but hasn't confirmed it ran".into(), code: "submitted".into(), maybe_delivered: true, answered: true, reply, status: 0 });
    }
    Ok(reply)
}

fn action_body(name: &str, payload: Value) -> std::result::Result<Value, RespondError> {
    let Value::Object(mut fields) = payload else {
        return Err(RespondError { message: format!("the {name} payload is not a JSON object"), code: "invalid_request".into(), maybe_delivered: false, reply: Value::Null, answered: false, status: 0 });
    };
    fields.insert("action".into(), Value::String(name.into()));
    Ok(Value::Object(fields))
}

pub fn request_key() -> String {
    static SEQ: AtomicU64 = AtomicU64::new(0);
    format!("bake-{}-{}-{}", std::process::id(), OffsetDateTime::now_utc().unix_timestamp_nanos(), SEQ.fetch_add(1, Ordering::Relaxed))
}

#[cfg(test)]
mod respond_error_tests {
    use super::RespondError;

    #[test]
    fn a_maybe_delivered_answer_is_not_offered_again() {
        let e = RespondError::from_status("respond", 502, r#"{"error":{"code":"bad_gateway","message":"the answer may have reached the session, so it is not sent again: timeout"}}"#);
        assert!(e.maybe_delivered);
        assert!(e.message.starts_with("the answer may have reached"));
    }

    #[test]
    fn a_released_answer_can_be_given_again() {
        let e = RespondError::from_status("respond", 502, r#"{"error":{"code":"bad_gateway","message":"session ended"}}"#);
        assert!(!e.maybe_delivered);
        let stale = RespondError::from_status("respond", 409, r#"{"error":{"code":"stale","message":"this request was already answered or has expired"}}"#);
        assert!(!stale.maybe_delivered);
        assert_eq!(stale.message, "this request was already answered or has expired");
        assert_eq!(stale.code, "stale");
        assert_eq!(RespondError::from_status("respond", 500, "not json").message, "status code 500");
    }

    #[test]
    fn only_the_named_suggestion_is_recommended() {
        use super::suggests;
        assert!(suggests(Some(1), 1, "Blue"));
        assert!(!suggests(Some(1), 0, "Red (Recommended)"));
        assert!(suggests(None, 0, "Red (Recommended)"));
        assert!(!suggests(None, 0, "Not recommended"));
        assert!(!suggests(None, 0, "Recommended reading list"));
        assert!(!suggests(None, 0, "No longer recommended"));
        assert!(suggests(None, 0, "Banana (recommended), ripest"));
        assert!(suggests(None, 0, "Banana — recommended"));
    }

    #[test]
    fn a_send_lost_at_the_gateway_is_unconfirmed() {
        assert!(RespondError::from_status("send", 504, "").maybe_delivered);
        assert!(RespondError::from_status("send", 502, "").maybe_delivered);
        assert!(!RespondError::from_status("send", 503, r#"{"error":{"code":"unavailable","message":"down"}}"#).maybe_delivered);
    }
}

#[cfg(test)]
mod capabilities_tests {
    use super::Session;

    fn caps(json: &str) -> super::Capabilities {
        serde_json::from_str::<Session>(&format!(r#"{{"id":"s"{json}}}"#)).unwrap().capabilities
    }

    #[test]
    fn a_session_without_capabilities_can_be_stopped_and_steered() {
        for c in [caps(""), caps(r#","capabilities":null"#)] {
            assert!(c.interrupt && c.steer);
            assert!(!c.model && !c.compact && !c.commands);
        }
    }

    #[test]
    fn a_capability_the_daemon_leaves_out_is_not_offered() {
        let c = caps(r#","capabilities":{}"#);
        assert!(!c.interrupt && !c.steer && !c.model && !c.compact && !c.commands);
        let c = caps(r#","capabilities":{"model":true}"#);
        assert!(c.model && !c.interrupt && !c.steer);
        let c = caps(r#","capabilities":{"interrupt":true,"steer":false,"compact":true}"#);
        assert!(c.interrupt && !c.steer && c.compact && !c.commands);
    }
}
