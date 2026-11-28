use std::sync::atomic::{AtomicU64, Ordering};

use serde::{Deserialize, Serialize};
use serde_json::Value;
use time::OffsetDateTime;

use crate::catalog::Client;

#[path = "voice_trail.rs"]
mod trail;
pub use trail::{Attempt, KEPT_CALLS, Setup, Trail, UNFINISHED};

#[path = "voice_details.rs"]
mod details;
pub use details::Section;

#[path = "voice_picks.rs"]
mod picks;
pub use picks::{Activity, LEADS, Pick, SESSIONS, matching};

pub const PROTOCOL: i64 = 1;
pub const HEARTBEAT_SECS: f64 = 10.0;
pub const HEARTBEAT_GRACE_SECS: f64 = 30.0;
pub const RECOVERY_SECS: f64 = 10.0;
pub const MIC_DENIED: &str = "Microphone access is off for Agenthail. Turn it on in System Settings, Privacy & Security, Microphone, then call again.";
pub const MIC_SETTINGS: &str = "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone";
pub const ELSEWHERE: &str = "A call is already active in another Agenthail window. Hang up there to call from here.";
pub const OCCUPIED: &str = "Another device owns this call. Hang up there to call from this Mac.";
const TEXT_UNKNOWN: &str = "Text outcome is unknown.";
pub const TOOLS_OUTDATED: &str = "This voice conversation was created before Agenthail's current tools";

pub fn without_tools_notice(message: &str) -> (Option<String>, Option<String>) {
    let Some(at) = message.find(TOOLS_OUTDATED) else {
        return (Some(message.trim().to_string()).filter(|m| !m.is_empty()), None);
    };
    let rest = message[..at].trim();
    ((!rest.is_empty()).then(|| rest.to_string()), Some(message[at..].trim().to_string()))
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Party {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub name: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Event {
    #[serde(default)]
    pub sequence: i64,
    #[serde(default)]
    pub method: String,
    #[serde(default)]
    pub params: Value,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct State {
    #[serde(default)]
    pub protocol: i64,
    pub session: Option<Party>,
    #[serde(default)]
    pub phase: String,
    pub attempt_id: Option<String>,
    pub message: Option<String>,
    pub sdp: Option<String>,
    #[serde(default)]
    pub events: Vec<Event>,
    #[serde(default)]
    pub truncated: bool,
    #[serde(default)]
    pub occupied: bool,
    pub target: Option<Party>,
    pub terminal_reason: Option<String>,
    pub audio_provider: Option<String>,
    pub coding_provider: Option<String>,
    pub skill_digest: Option<String>,
    pub brief: Option<Brief>,
    pub text_receipt: Option<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Brief {
    #[serde(default)]
    pub session_id: String,
    #[serde(default)]
    pub as_of: String,
    #[serde(default)]
    pub bytes: i64,
    #[serde(default)]
    pub exchanges: i64,
    #[serde(default)]
    pub omitted: Vec<String>,
}

impl Brief {
    pub fn summary(&self, name: &str, now: OffsetDateTime) -> String {
        let exchanges = match self.exchanges {
            1 => "1 recent exchange".to_string(),
            n => format!("{n} recent exchanges"),
        };
        let age = crate::catalog::ago(&self.as_of, now);
        let when = match age.as_str() {
            "" => String::new(),
            "now" => ", read just now".into(),
            age => format!(", read {age} ago"),
        };
        format!("The call was briefed on {name}: {exchanges}{when}.")
    }

    pub fn left_out(&self) -> Option<String> {
        (!self.omitted.is_empty()).then(|| format!("Left out of the brief: {}.", self.omitted.join(", ")))
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Role {
    You,
    Agent,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Line {
    pub id: String,
    pub role: Role,
    pub text: String,
    pub open: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Talk {
    Listening,
    Hearing,
    Speaking,
}

impl State {
    pub fn shown_brief(&self) -> Option<(&Brief, &Party)> {
        let target = self.target.as_ref()?;
        self.brief.as_ref().filter(|b| b.session_id == target.id).map(|b| (b, target))
    }

    fn route(&self) -> String {
        self.target.as_ref().map(|t| t.id.clone()).unwrap_or_default()
    }

    pub fn took_text(&self, message: &str) -> bool {
        self.text_receipt.as_deref().and_then(|r| r.strip_prefix("accepted:")) == Some(message)
            || self.events.iter().any(|e| matches!(e.method.as_str(), "voice/delegation/dispatched" | "voice/delegation/held") && e.params["messageId"].as_str() == Some(message))
    }

    pub fn has_call(&self) -> bool {
        ["starting", "negotiating", "connected", "stopping", "unknown"].contains(&self.phase.as_str())
    }

    fn terminal(&self) -> bool {
        self.phase == "ended" || self.phase == "blocked"
    }

    fn message(&self) -> Option<String> {
        self.message.clone().filter(|m| !m.is_empty())
    }

    pub fn lines(&self) -> Vec<Line> {
        let mut lines: Vec<Line> = Vec::new();
        let mut open: Vec<(Role, usize)> = Vec::new();
        for event in &self.events {
            if event.method == "thread/realtime/started" {
                open.clear();
                for line in &mut lines {
                    line.open = false;
                }
            }
            let role = match event.params["role"].as_str() {
                Some("user") => Role::You,
                Some("assistant") => Role::Agent,
                _ => continue,
            };
            let at = open.iter().position(|(r, _)| *r == role);
            match event.method.as_str() {
                "thread/realtime/transcript/delta" => {
                    let Some(delta) = event.params["delta"].as_str() else { continue };
                    match at {
                        Some(k) => lines[open[k].1].text.push_str(delta),
                        None => {
                            open.push((role, lines.len()));
                            lines.push(Line { id: event.sequence.to_string(), role, text: delta.into(), open: true });
                        }
                    }
                }
                "thread/realtime/transcript/done" => {
                    let Some(text) = event.params["text"].as_str() else { continue };
                    match at {
                        Some(k) => {
                            let (_, index) = open.remove(k);
                            lines[index].text = text.into();
                            lines[index].open = false;
                        }
                        None => lines.push(Line { id: event.sequence.to_string(), role, text: text.into(), open: false }),
                    }
                }
                _ => {}
            }
        }
        lines
    }

    pub fn talk(&self) -> Talk {
        match self.lines().iter().rev().find(|l| l.open).map(|l| l.role) {
            Some(Role::Agent) => Talk::Speaking,
            Some(Role::You) => Talk::Hearing,
            None => Talk::Listening,
        }
    }
}

#[derive(Serialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Action {
    pub action: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub attempt_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sdp: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub text: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub message_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub target_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub muted: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub input_revision: Option<i64>,
}

impl Action {
    pub fn new(name: &str) -> Action {
        Action { action: name.into(), ..Action::default() }
    }

    fn attempt(mut self, id: &str) -> Action {
        self.attempt_id = Some(id.into());
        self
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct Peer {
    pub url: String,
    pub authorization: String,
    pub origin: String,
}

pub fn origin(url: &str) -> Option<String> {
    let parsed = url::Url::parse(url).ok()?;
    let host = parsed.host_str()?.to_ascii_lowercase();
    let secure = match parsed.scheme() {
        "https" => true,
        "http" => ["127.0.0.1", "[::1]", "::1", "localhost"].contains(&host.as_str()),
        _ => false,
    };
    (secure && parsed.username().is_empty() && parsed.password().is_none()).then(|| parsed.origin().ascii_serialization())
}

fn loopback(base: &str) -> String {
    match url::Url::parse(base) {
        Ok(mut parsed) if matches!(parsed.host_str(), Some("0.0.0.0" | "[::]" | "::")) => {
            let _ = parsed.set_host(Some("127.0.0.1"));
            parsed.as_str().trim_end_matches('/').to_string()
        }
        _ => base.to_string(),
    }
}

pub fn shown_address(url: &str) -> String {
    let Ok(mut parsed) = url::Url::parse(url) else { return "an unreadable address".into() };
    let _ = parsed.set_username("");
    let _ = parsed.set_password(None);
    parsed.set_query(None);
    parsed.set_fragment(None);
    parsed.to_string()
}

pub fn peer_allowed(url: &str, peer: &Peer) -> bool {
    url::Url::parse(url).is_ok_and(|u| u.path() == "/api/v1/voice/peer" && u.origin().ascii_serialization() == peer.origin)
}

impl Client {
    pub fn voice_peer(&self) -> Result<Peer, String> {
        let base = loopback(self.base());
        let origin = origin(&base).ok_or("Voice needs Agenthail's loopback address or an HTTPS address.")?;
        Ok(Peer { url: format!("{base}/api/v1/voice/peer"), authorization: self.bearer(), origin })
    }

    pub fn voice_state(&self) -> Result<State, String> {
        let value = self.get_reply("/api/v1/voice").map_err(|(code, body)| failure(code, &body))?;
        parse(value)
    }

    pub fn voice(&self, action: &Action) -> Result<State, Failure> {
        let body = serde_json::to_value(action).map_err(|e| Failure::from(e.to_string()))?;
        let value = self.post_json("/api/v1/voice", body).map_err(|e| match e.status {
            0 => Failure::from(e.message),
            status => Failure { message: failure(status, &e.reply.to_string()), host: crate::reply::parse(e.reply["state"].clone()).ok() },
        })?;
        parse(value).map_err(Failure::from)
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct Failure {
    pub message: String,
    pub host: Option<State>,
}

impl From<String> for Failure {
    fn from(message: String) -> Failure {
        Failure { message, host: None }
    }
}

impl From<&str> for Failure {
    fn from(message: &str) -> Failure {
        Failure::from(message.to_string())
    }
}

fn failure(status: u16, body: &str) -> String {
    let reply: Value = serde_json::from_str(body).unwrap_or_default();
    match reply["error"]["message"].as_str() {
        Some(message) if !message.is_empty() => message.to_string(),
        _ if status == 0 => body.to_string(),
        _ => format!("Voice connection failed ({status})."),
    }
}

fn parse(value: Value) -> Result<State, String> {
    let state: State = crate::reply::parse(value).map_err(|e| format!("the daemon's voice reply couldn't be read: {e}"))?;
    if state.protocol != PROTOCOL {
        return Err(format!("This Agenthail speaks voice protocol {}; this app speaks {PROTOCOL}. Update both to the same version.", state.protocol));
    }
    Ok(state)
}

pub fn fresh_id() -> String {
    static SEQ: AtomicU64 = AtomicU64::new(0);
    let nanos = OffsetDateTime::now_utc().unix_timestamp_nanos() as u64;
    let seed = nanos ^ (u64::from(std::process::id()) << 32) ^ SEQ.fetch_add(1, Ordering::Relaxed).wrapping_mul(0x9e37_79b9_7f4a_7c15);
    let (high, low) = (mix(seed), mix(seed ^ 0xd1b5_4a32_d192_ed03));
    let hex = format!("{high:016x}{low:016x}");
    format!("{}-{}-{}-{}-{}", &hex[0..8], &hex[8..12], &hex[12..16], &hex[16..20], &hex[20..32])
}

fn mix(mut z: u64) -> u64 {
    z = z.wrapping_add(0x9e37_79b9_7f4a_7c15);
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58_476d_1ce4_e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d0_49bb_1331_11eb);
    z ^ (z >> 31)
}

#[derive(Clone, Debug, PartialEq)]
pub enum Req {
    Refresh,
    Select,
    Prepare,
    Start(String),
    Connected(String),
    Heartbeat(String),
    Input(i64),
    Stop,
    Abandon,
    Text,
    Transfer,
    Interrupt,
    TargetInterrupt,
    New,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Job {
    pub generation: u64,
    pub work: u64,
    pub req: Req,
    pub action: Option<Action>,
    moves: u64,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Effect {
    Fetch(Job),
    AskMic(String),
    PeerStart(String),
    PeerEnd,
    PeerAnswer(String),
    PeerMute(bool),
    Note(String),
}

#[derive(Default)]
pub struct Call {
    pub state: Option<State>,
    pub error: Option<String>,
    pub connection_error: Option<String>,
    pub ready: bool,
    pub dialing: bool,
    pub muted: bool,
    pub mic_denied: bool,
    pub audio_connected: bool,
    pub hangup_pending: bool,
    pub hangup_unconfirmed: bool,
    pub target: String,
    pub visible: bool,
    generation: u64,
    work: u64,
    working: bool,
    attempt: Option<String>,
    applied_sdp: Option<String>,
    channel_open: bool,
    connected_reported: bool,
    start_submitted: bool,
    host_has_call: bool,
    reported_muted: bool,
    input_revision: i64,
    hangup_attempt: Option<String>,
    closed: bool,
    refreshing: bool,
    next_refresh: f64,
    beat_due: Option<f64>,
    beating: Option<String>,
    last_beat: f64,
    requested: Option<String>,
    transfer_from: Option<(u64, String)>,
    peer_started: bool,
    asking_mic: bool,
    lost_at: Option<f64>,
    out: Vec<Effect>,
    pub trail: Trail,
    clock: f64,
    setup: Option<(Setup, f64)>,
    setup_stuck: bool,
    unsent: Vec<(String, String, String)>,
    moves: u64,
    sent: Vec<(String, String)>,
}

impl Call {
    pub fn new(target: &str) -> Call {
        Call { target: target.trim().to_string(), visible: true, ..Call::default() }
    }

    pub fn working(&self) -> bool {
        self.working
    }

    pub fn attempt(&self) -> Option<&str> {
        self.attempt.as_deref()
    }

    pub fn has_local_call(&self) -> bool {
        self.dialing || self.audio_connected || self.attempt.is_some()
    }

    pub fn active(&self) -> bool {
        self.has_local_call() || self.hangup_pending || self.hangup_unconfirmed
    }

    pub fn elsewhere(&self) -> bool {
        !self.active() && self.state.as_ref().is_some_and(|s| s.has_call() && !s.occupied)
    }

    pub fn routed(&self) -> bool {
        !self.target.trim().is_empty()
    }

    pub fn unavailable(&self) -> Option<String> {
        let state = self.state.as_ref();
        let reason = if self.closed {
            "This call window is closed."
        } else if !self.ready {
            match self.setup {
                Some((stage, _)) if self.setup_stuck => stage.stuck(),
                Some((stage, _)) => stage.waiting(),
                None => "Call audio is not ready.",
            }
        } else if self.working {
            "Another voice action is in progress."
        } else if self.dialing {
            "A call is starting."
        } else if self.audio_connected {
            "A call is connected."
        } else if self.hangup_pending {
            "The host has not yet confirmed the last hangup."
        } else if self.hangup_unconfirmed {
            "The host did not confirm the last hangup."
        } else if state.is_some_and(|s| s.occupied) {
            OCCUPIED
        } else if self.elsewhere() {
            ELSEWHERE
        } else if state.is_some_and(|s| s.has_call()) {
            "The host still has a call."
        } else if state.is_some_and(|s| s.phase == "blocked") {
            "The host blocked voice calls."
        } else {
            return self.connection_error.clone();
        };
        Some(reason.into())
    }

    pub fn can_call(&self) -> bool {
        self.unavailable().is_none()
    }

    pub fn setup_stuck(&self) -> bool {
        !self.ready && self.setup_stuck
    }

    pub fn checked(&mut self, ok: bool, now: f64) -> Vec<Effect> {
        if self.closed || self.ready || !self.setup.is_some_and(|(s, _)| s == Setup::Check) {
            return Vec::new();
        }
        if ok {
            self.peer("ready", "", now)
        } else {
            self.peer("unavailable", "The audio page didn't start. Check that Agenthail is running and up to date, then open this window again.", now)
        }
    }

    pub fn setup(&mut self, stage: Setup, now: f64) -> Vec<Effect> {
        if self.closed || self.ready || self.setup.is_some_and(|(s, _)| s == stage) {
            return Vec::new();
        }
        self.clock = now;
        self.setup = Some((stage, now));
        self.setup_stuck = false;
        self.out.push(Effect::Note(stage.waiting().into()));
        self.drain()
    }

    pub fn can_start_new(&self) -> bool {
        self.state.as_ref().is_some_and(|s| !s.has_call() && !s.occupied && s.phase != "creating" && s.phase != "blocked")
            && !self.working
            && !self.active()
            && !self.closed
    }

    pub fn connected_call(&self) -> bool {
        self.audio_connected && self.state.as_ref().is_some_and(|s| s.phase == "connected" && !s.occupied)
    }

    pub fn title(&self) -> String {
        if self.hangup_pending {
            return "Audio off; confirming hangup…".into();
        }
        if self.hangup_unconfirmed {
            return "Audio off; hangup unconfirmed".into();
        }
        if self.connection_error.is_some() {
            return "Reconnecting to Agenthail".into();
        }
        if self.audio_connected {
            if self.muted {
                return "Microphone muted".into();
            }
            return match self.state.as_ref().map(State::talk) {
                Some(Talk::Speaking) => "Speaking",
                Some(Talk::Hearing) => "Hearing you",
                _ => "Listening",
            }
            .into();
        }
        if self.dialing {
            return "Connecting audio…".into();
        }
        match self.state.as_ref().map(|s| s.phase.as_str()) {
            Some("starting" | "negotiating") => "Connecting audio…",
            Some("stopping") => "Ending call…",
            Some("unknown") => "Connection needs attention",
            Some("blocked") => "Voice unavailable",
            Some("creating") => "Preparing orchestrator…",
            Some("connected") => "Call in progress",
            Some("ready" | "idle" | "ended") => "Ready to talk",
            _ => "Connecting to Agenthail…",
        }
        .into()
    }

    fn fetch(&mut self, req: Req, action: Option<Action>) {
        let job = Job { generation: self.generation, work: self.work, req, action, moves: self.moves };
        self.out.push(Effect::Fetch(job));
    }

    fn begin_work(&mut self) {
        self.work += 1;
        self.working = true;
    }

    fn end_work(&mut self, work: u64) {
        if self.work == work {
            self.working = false;
        }
    }

    fn drain(&mut self) -> Vec<Effect> {
        std::mem::take(&mut self.out)
    }

    pub fn tick(&mut self, now: f64) -> Vec<Effect> {
        if self.closed {
            return Vec::new();
        }
        self.clock = now;
        if let Some((stage, since)) = self.setup {
            if !self.ready && !self.setup_stuck && now - since >= stage.limit() {
                self.setup_stuck = true;
                self.out.push(Effect::Note(stage.stuck().into()));
            }
        }
        if !self.refreshing && now >= self.next_refresh && (self.visible || self.active()) {
            self.refreshing = true;
            self.fetch(Req::Refresh, None);
        }
        if let (Some(due), Some(id)) = (self.beat_due, self.attempt.clone()) {
            if now >= due && self.beating.is_none() {
                self.beating = Some(id.clone());
                self.fetch(Req::Heartbeat(id.clone()), Some(Action::new("heartbeat").attempt(&id)));
            }
        }
        if self.host_has_call && self.attempt.is_some() && now - self.last_beat >= HEARTBEAT_GRACE_SECS {
            self.error.get_or_insert_with(|| "The connection to Agenthail was lost. Audio has stopped; call again when it reconnects.".into());
            self.end_call("host_unreachable");
        }
        if self.lost_at.is_some_and(|at| now - at >= RECOVERY_SECS) && !self.audio_connected {
            self.error.get_or_insert_with(|| "Audio could not reconnect. Call again to resume this conversation.".into());
            self.end_call("peer_disconnected");
        }
        self.drain()
    }

    pub fn next_due(&self) -> Option<f64> {
        if self.closed {
            return None;
        }
        let setup = self.setup.filter(|_| !self.ready && !self.setup_stuck).map(|(stage, since)| since + stage.limit());
        let refresh = (!self.refreshing && (self.visible || self.active())).then_some(self.next_refresh);
        let beat = self.beat_due.filter(|_| self.attempt.is_some() && self.beating.is_none());
        let grace = (self.host_has_call && self.attempt.is_some()).then_some(self.last_beat + HEARTBEAT_GRACE_SECS);
        let lost = self.lost_at.filter(|_| !self.audio_connected).map(|at| at + RECOVERY_SECS);
        [setup, refresh, beat, grace, lost].into_iter().flatten().min_by(f64::total_cmp)
    }

    pub fn call(&mut self) -> Vec<Effect> {
        if !self.can_call() {
            return Vec::new();
        }
        self.begin_work();
        self.dialing = true;
        self.error = None;
        self.mic_denied = false;
        self.generation += 1;
        let id = fresh_id();
        self.trail.begin(&id, self.clock);
        self.attempt = Some(id);
        self.requested = Some(self.target.trim().to_string()).filter(|t| !t.is_empty());
        let select = Action { target_id: Some(self.target.trim().to_string()), ..Action::new("select") };
        self.fetch(Req::Select, Some(select));
        self.drain()
    }

    fn dial_failed(&mut self, error: String, work: u64, stage: &str, cause: &str) {
        if let Some(id) = &self.attempt {
            self.trail.fail(id, stage);
            self.trail.end(id, cause, self.clock);
        }
        self.error = Some(error);
        self.dialing = false;
        self.attempt = None;
        self.end_peer();
        self.end_work(work);
    }

    pub fn mic(&mut self, attempt: &str, granted: bool) -> Vec<Effect> {
        if self.closed || !self.dialing || !self.asking_mic || self.attempt.as_deref() != Some(attempt) {
            return Vec::new();
        }
        self.asking_mic = false;
        let work = self.work;
        if granted {
            self.trail.mark(attempt, "microphone_granted", self.clock);
            self.peer_started = true;
            self.out.push(Effect::PeerStart(attempt.to_string()));
            self.end_work(work);
        } else {
            self.dial_failed(MIC_DENIED.into(), work, "native_audio_start", "microphone_denied");
            self.mic_denied = true;
        }
        self.drain()
    }

    pub fn heard(&mut self, call: &str, kind: &str, value: &str, now: f64) -> Vec<Effect> {
        let of_a_call = matches!(kind, "offer" | "connection" | "channel" | "error");
        if kind == "ready" || of_a_call && self.attempt.as_deref() != Some(call) {
            return Vec::new();
        }
        self.peer(kind, value, now)
    }

    pub fn peer(&mut self, kind: &str, value: &str, now: f64) -> Vec<Effect> {
        if self.closed {
            return Vec::new();
        }
        self.clock = now;
        if let Some(id) = self.attempt.clone() {
            match kind {
                "offer" => self.trail.mark(&id, "offer_submitted", now),
                "connection" => self.trail.mark(&id, &format!("peer_{value}"), now),
                "channel" if value == "open" => self.trail.mark(&id, "data_channel_open", now),
                _ => {}
            }
        }
        match kind {
            "loading" => self.ready = false,
            "ready" => {
                self.ready = true;
                self.setup = None;
                self.setup_stuck = false;
            }
            "unavailable" => {
                self.ready = false;
                self.setup = None;
                self.setup_stuck = false;
                self.out.push(Effect::Note(value.into()));
                self.error = Some(value.into());
                self.end_call("audio_unavailable");
            }
            "offer" => {
                if let Some(id) = self.attempt.clone() {
                    self.start_submitted = true;
                    let start = Action { sdp: Some(value.into()), ..Action::new("start").attempt(&id) };
                    self.fetch(Req::Start(id), Some(start));
                }
            }
            "connection" => {
                self.audio_connected = value == "connected";
                if self.audio_connected {
                    self.lost_at = None;
                }
                if value == "disconnected" && self.lost_at.is_none() {
                    self.lost_at = Some(now);
                }
                if value == "failed" || value == "closed" {
                    self.error.get_or_insert_with(|| "Audio disconnected. Call again to resume this conversation.".into());
                    self.end_call(&format!("peer_{value}"));
                }
                self.report_connected();
            }
            "channel" => {
                self.channel_open = value == "open";
                self.report_connected();
            }
            "error" => {
                self.error.get_or_insert_with(|| value.to_string());
                self.end_call("audio_error");
            }
            _ => {}
        }
        self.drain()
    }

    fn report_connected(&mut self) {
        let Some(id) = self.attempt.clone() else { return };
        if !self.audio_connected || !self.channel_open || self.connected_reported {
            return;
        }
        self.connected_reported = true;
        self.dialing = false;
        self.fetch(Req::Connected(id.clone()), Some(Action::new("connected").attempt(&id)));
    }

    pub fn toggle_mute(&mut self) -> Vec<Effect> {
        if !self.audio_connected {
            return Vec::new();
        }
        self.muted = !self.muted;
        self.out.push(Effect::PeerMute(self.muted));
        self.report_input();
        self.drain()
    }

    fn report_input(&mut self) {
        let Some(id) = self.attempt.clone() else { return };
        if !self.host_has_call || self.muted == self.reported_muted {
            return;
        }
        self.reported_muted = self.muted;
        self.input_revision += 1;
        let report = Action { muted: Some(self.muted), input_revision: Some(self.input_revision), ..Action::new("input-state").attempt(&id) };
        self.fetch(Req::Input(self.input_revision), Some(report));
    }

    pub fn hangup(&mut self) -> Vec<Effect> {
        self.end_call("local_hangup");
        self.drain()
    }

    fn end_call(&mut self, cause: &str) {
        self.generation += 1;
        self.work += 1;
        self.working = false;
        self.beat_due = None;
        self.beating = None;
        self.lost_at = None;
        self.dialing = false;
        self.audio_connected = false;
        self.channel_open = false;
        self.asking_mic = false;
        self.host_has_call = false;
        self.end_peer();
        let local = self.attempt.take();
        if let Some(id) = &local {
            self.trail.end(id, cause, self.clock);
        }
        let stop = self.start_submitted || self.hangup_unconfirmed;
        self.start_submitted = false;
        let id = local.or_else(|| self.hangup_attempt.clone());
        match id {
            Some(id) if stop && !self.state.as_ref().is_some_and(|s| s.occupied) => {
                self.hangup_pending = true;
                self.hangup_unconfirmed = false;
                self.hangup_attempt = Some(id.clone());
                self.fetch(Req::Stop, Some(Action::new("stop").attempt(&id)));
            }
            _ => {}
        }
    }

    fn end_peer(&mut self) {
        if std::mem::take(&mut self.peer_started) {
            self.out.push(Effect::PeerEnd);
        }
    }

    fn host_stopped(&mut self, next: State) {
        let fallback = next.message().unwrap_or_else(|| "The host ended this voice call.".into());
        self.error.get_or_insert(fallback);
        self.state = Some(next);
        self.start_submitted = false;
        self.end_call("host_stopping");
    }

    pub fn send_text(&mut self, text: &str) -> Vec<Effect> {
        let Some(id) = self.attempt.clone() else { return Vec::new() };
        if self.closed || self.working || text.trim().is_empty() {
            return Vec::new();
        }
        let text = text.trim().to_string();
        let route = self.route_now();
        let message = match self.unsent.iter().find(|(words, _, to)| *words == text && *to == route) {
            Some((_, message, _)) => message.clone(),
            None => {
                let message = fresh_id();
                self.unsent.push((text.clone(), message.clone(), route));
                message
            }
        };
        self.begin_work();
        let typed = Action { text: Some(text), message_id: Some(message), ..Action::new("text").attempt(&id) };
        self.fetch(Req::Text, Some(typed));
        self.drain()
    }

    fn route_now(&self) -> String {
        self.state.as_ref().map(State::route).unwrap_or_default()
    }

    pub fn unsent(&self) -> Vec<&str> {
        self.unsent.iter().map(|(text, _, _)| text.as_str()).collect()
    }

    pub fn take_sent(&mut self) -> Vec<(String, String)> {
        std::mem::take(&mut self.sent)
    }

    fn settle_text(&mut self, message: &str) -> bool {
        let Some(at) = self.unsent.iter().position(|(_, id, _)| id == message) else { return false };
        let (words, id, _) = self.unsent.remove(at);
        self.sent.push((id, words));
        if self.unsent.is_empty() && self.error.as_ref().is_some_and(|e| e.starts_with(TEXT_UNKNOWN)) {
            self.error = None;
        }
        true
    }

    fn texts_landed(&mut self, host: Option<&State>) -> bool {
        let Some(host) = host else { return false };
        let landed: Vec<String> = self.unsent.iter().filter(|(_, id, _)| host.took_text(id)).map(|(_, id, _)| id.clone()).collect();
        for id in &landed {
            self.settle_text(id);
        }
        !landed.is_empty()
    }

    pub fn can_transfer_to(&self, route: &str) -> bool {
        let on = self.state.as_ref().and_then(|s| s.target.as_ref()).map_or("", |t| t.id.as_str());
        self.connected_call() && route.trim() != on
    }

    pub fn transfer(&mut self, target: &str) -> Vec<Effect> {
        if self.closed || self.working || !self.connected_call() {
            return Vec::new();
        }
        let from = std::mem::replace(&mut self.target, target.trim().to_string());
        self.begin_work();
        self.transfer_from = Some((self.work, from));
        let to = Action { target_id: Some(self.target.clone()), ..Action::new("transfer") };
        self.fetch(Req::Transfer, Some(to));
        self.drain()
    }

    pub fn can_interrupt(&self) -> bool {
        !self.closed && !self.working && self.state.as_ref().is_some_and(|s| s.session.is_some() && !s.occupied)
    }

    pub fn interrupt(&mut self) -> Vec<Effect> {
        if !self.can_interrupt() {
            return Vec::new();
        }
        self.begin_work();
        self.fetch(Req::Interrupt, Some(Action::new("interrupt")));
        self.drain()
    }

    pub fn interrupt_target(&mut self) -> Vec<Effect> {
        let attempt = self.state.as_ref().and_then(|s| s.target.as_ref().and(s.attempt_id.clone()));
        let Some(id) = attempt else { return Vec::new() };
        if self.closed || self.working || !self.connected_call() {
            return Vec::new();
        }
        self.begin_work();
        self.fetch(Req::TargetInterrupt, Some(Action::new("target-interrupt").attempt(&id)));
        self.drain()
    }

    pub fn start_new(&mut self) -> Vec<Effect> {
        if !self.can_start_new() {
            return Vec::new();
        }
        self.begin_work();
        self.error = None;
        self.generation += 1;
        self.fetch(Req::New, Some(Action::new("new")));
        self.drain()
    }

    pub fn close(&mut self) -> Vec<Effect> {
        if self.closed {
            return Vec::new();
        }
        self.end_call("window_closed");
        self.closed = true;
        self.state = None;
        self.drain()
    }

    pub fn closed(&self) -> bool {
        self.closed
    }

    pub fn reply(&mut self, job: Job, result: Result<State, Failure>, now: f64) -> Vec<Effect> {
        let current = job.generation == self.generation && !self.closed;
        self.clock = now;
        let (result, host) = match result {
            Ok(next) => (Ok(next), None),
            Err(failure) => (Err(failure.message), failure.host),
        };
        match job.req {
            Req::Refresh => {
                self.refreshing = false;
                self.next_refresh = now + if self.audio_connected { 3.0 } else { 1.0 };
                if current {
                    match result {
                        Ok(mut next) => {
                            if job.moves != self.moves {
                                next.target = self.state.as_ref().and_then(|s| s.target.clone());
                            }
                            self.refreshed(next);
                        }
                        Err(error) => self.connection_error = Some(format!("Reconnecting: {error}")),
                    }
                }
            }
            Req::Select | Req::Prepare => {
                if current {
                    match result {
                        Ok(next) => {
                            if job.req == Req::Select {
                                if self.requested.is_some() {
                                    self.requested = next.target.as_ref().map(|t| t.id.clone()).or(self.requested.take());
                                }
                                self.state = Some(next);
                                self.fetch(Req::Prepare, Some(Action::new("prepare")));
                            } else {
                                self.state = Some(next);
                                self.reset_call();
                                if let Some(id) = self.attempt.clone() {
                                    self.trail.mark(&id, "prepared", self.clock);
                                    self.asking_mic = true;
                                    self.out.push(Effect::AskMic(id));
                                }
                            }
                        }
                        Err(error) => {
                            let stage = if job.req == Req::Select { "target_select" } else { "operator_prepare" };
                            self.dial_failed(error, job.work, stage, &format!("{stage}_failed"));
                        }
                    }
                }
            }
            Req::Start(id) => match result {
                Ok(next) if current => {
                    self.trail.mark(&id, "host_start_accepted", now);
                    let bound = next.target.as_ref().map(|t| t.id.clone());
                    self.state = Some(next);
                    self.host_has_call = true;
                    self.last_beat = now;
                    if bound != self.requested {
                        self.error = Some("Another Agenthail window changed who this call talks to before it started, so it was hung up. Call again.".into());
                        self.end_call("target_changed");
                    } else {
                        self.report_input();
                        self.beat_due = Some(now);
                    }
                }
                Err(error) if current && host.as_ref().is_some_and(|h| h.attempt_id.as_deref() != Some(id.as_str())) => {
                    self.trail.fail(&id, "host_start");
                    self.trail.end(&id, "host_start_rejected", now);
                    self.error = Some(error);
                    self.dialing = false;
                    self.attempt = None;
                    self.start_submitted = false;
                    self.end_peer();
                    self.fetch(Req::Abandon, Some(Action::new("stop").attempt(&id)));
                }
                Err(error) if current => {
                    self.trail.fail(&id, "host_start");
                    self.error = Some(error);
                    if let Some(host) = host {
                        self.state = Some(host);
                    }
                    self.end_call("host_start_rejected");
                }
                _ => {
                    let stale = Job { generation: self.generation, work: 0, req: Req::Abandon, action: Some(Action::new("stop").attempt(&id)), moves: self.moves };
                    self.out.push(Effect::Fetch(stale));
                }
            },
            Req::Connected(id) => {
                if current {
                    match result {
                        Ok(next) => {
                            self.trail.mark(&id, "host_connected", now);
                            self.state = Some(next);
                        }
                        Err(error) => {
                            self.error = Some(error);
                            self.end_call("host_connected_failed");
                        }
                    }
                }
            }
            Req::Heartbeat(id) => {
                if self.beating.as_deref() == Some(id.as_str()) {
                    self.beating = None;
                }
                if current && self.attempt.as_deref() == Some(id.as_str()) {
                    self.beat_due = Some(now + HEARTBEAT_SECS);
                    match result {
                        Ok(next) => {
                            self.last_beat = now;
                            if next.phase == "stopping" && next.attempt_id.as_deref() == Some(id.as_str()) {
                                self.host_stopped(next);
                            } else if ["ended", "blocked", "unknown"].contains(&next.phase.as_str()) {
                                let fallback = next.message().unwrap_or_else(|| "The host ended this call.".into());
                                self.error.get_or_insert(fallback);
                                self.end_call(&format!("host_{}", next.phase));
                            }
                        }
                        Err(_) => {}
                    }
                }
            }
            Req::Input(revision) => {
                if current {
                    match result {
                        Ok(next) if revision == self.input_revision => self.state = Some(next),
                        Ok(_) => {}
                        Err(_) => {
                            self.error.get_or_insert_with(|| "Agenthail could not record whether the microphone is muted, so the call ended.".into());
                            self.end_call("input_report_failed");
                        }
                    }
                }
            }
            Req::Abandon => {}
            Req::Stop => {
                let asked = job.action.as_ref().and_then(|a| a.attempt_id.clone());
                if current && self.hangup_pending && asked.is_some() && asked == self.hangup_attempt {
                    match result {
                        Ok(next) => self.state = Some(next),
                        Err(error) => {
                            self.hangup_pending = false;
                            self.hangup_unconfirmed = true;
                            self.error.get_or_insert(format!("Audio is off locally. Host hangup is unconfirmed: {error}"));
                        }
                    }
                }
            }
            Req::Text => {
                self.end_work(job.work);
                let asked = job.action.as_ref().and_then(|a| a.message_id.clone());
                let asked = asked.filter(|id| current && self.unsent.iter().any(|(_, u, _)| u == id));
                match result {
                    Ok(next) if current => {
                        let took = asked.as_ref().is_some_and(|id| next.took_text(id));
                        self.state = Some(next);
                        match &asked {
                            Some(id) if took => {
                                self.settle_text(id);
                            }
                            Some(_) => self.error = Some(format!("{TEXT_UNKNOWN} The call changed while it was sent; check the conversation before sending it again.")),
                            None => {}
                        }
                    }
                    Ok(_) => {}
                    Err(error) => {
                        let landed = asked.as_ref().is_some_and(|id| host.as_ref().is_some_and(|h| h.took_text(id)));
                        if let (Some(id), true) = (&asked, landed) {
                            self.settle_text(id);
                        } else if asked.is_some() {
                            self.error = Some(format!("{TEXT_UNKNOWN} Check the conversation; sending the same text again won't repeat it. {error}"));
                        }
                    }
                }
            }
            Req::Transfer | Req::Interrupt | Req::TargetInterrupt | Req::New => {
                self.end_work(job.work);
                if job.req == Req::Transfer && self.transfer_from.as_ref().is_some_and(|(work, _)| *work == job.work) {
                    if let Some((_, from)) = self.transfer_from.take().filter(|_| current && result.is_err()) {
                        self.target = from;
                    }
                }
                if current {
                    match result {
                        Ok(next) => {
                            if job.req == Req::Transfer {
                                self.moves += 1;
                            }
                            self.state = Some(next);
                        }
                        Err(error) => self.error = Some(error),
                    }
                }
            }
        }
        self.drain()
    }

    fn reset_call(&mut self) {
        self.applied_sdp = None;
        self.channel_open = false;
        self.connected_reported = false;
        self.start_submitted = false;
        self.muted = false;
        self.host_has_call = false;
        self.reported_muted = false;
        self.input_revision = 0;
        self.asking_mic = false;
        self.unsent.clear();
    }

    fn refreshed(&mut self, next: State) {
        let ours = self.attempt.is_some() && next.attempt_id == self.attempt;
        if next.phase == "unknown" && ours {
            let fallback = next.message().unwrap_or_else(|| "The host can no longer confirm this call. Audio has stopped.".into());
            self.error.get_or_insert(fallback);
            self.state = Some(next);
            self.end_call("host_unknown");
            return;
        }
        if next.phase == "stopping" && ours {
            self.host_stopped(next);
            return;
        }
        let terminal = next.terminal();
        if terminal && (self.hangup_pending || self.hangup_unconfirmed) && next.attempt_id != self.hangup_attempt {
            return;
        }
        if terminal && (self.dialing || self.audio_connected) {
            if !ours {
                return;
            }
            let fallback = if next.phase == "blocked" {
                "The host blocked this voice call. Check the call details before trying again."
            } else if self.dialing {
                "The host ended this voice call before audio connected."
            } else {
                "The host ended this connected voice call."
            };
            self.error.get_or_insert(next.message().unwrap_or_else(|| fallback.into()));
        }
        let answer = next.sdp.clone().filter(|sdp| ours && self.applied_sdp.as_ref() != Some(sdp));
        if ours {
            self.texts_landed(Some(&next));
        }
        self.state = Some(next);
        self.connection_error = None;
        if terminal {
            let had_local = self.has_local_call();
            if let Some(id) = &self.attempt {
                let phase = self.state.as_ref().map(|s| s.phase.clone()).unwrap_or_default();
                self.trail.end(id, &format!("host_{phase}"), self.clock);
            }
            self.audio_connected = false;
            self.dialing = false;
            self.attempt = None;
            if had_local {
                self.end_peer();
            }
            self.beat_due = None;
            self.beating = None;
            self.lost_at = None;
            self.hangup_pending = false;
            self.hangup_unconfirmed = false;
            self.hangup_attempt = None;
        }
        if let Some(sdp) = answer {
            if let Some(id) = &self.attempt {
                self.trail.mark(id, "sdp_answer_sent", self.clock);
            }
            self.applied_sdp = Some(sdp.clone());
            self.out.push(Effect::PeerAnswer(sdp));
        }
    }
}

#[cfg(test)]
#[path = "voice_tests.rs"]
mod tests;
