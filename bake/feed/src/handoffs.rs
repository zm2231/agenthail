use serde::{Deserialize, Deserializer};
use serde_json::{Value, json};
use time::OffsetDateTime;

use crate::catalog::{Client, ago, confirmed, enc};
use crate::prefs::{Failure, write_failure};

fn listed<'de, D: Deserializer<'de>, T: Deserialize<'de>>(d: D) -> Result<Vec<T>, D::Error> {
    Ok(Option::<Vec<T>>::deserialize(d)?.unwrap_or_default())
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Member {
    pub id: String,
    #[serde(default)]
    pub display: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Channel {
    pub name: String,
    #[serde(default, deserialize_with = "listed")]
    pub members: Vec<String>,
    #[serde(default)]
    pub member_details: Option<Vec<Member>>,
}

impl Channel {
    pub fn people(&self) -> Vec<Member> {
        match &self.member_details {
            Some(details) => details.iter().map(|m| Member { display: if m.display.is_empty() { m.id.clone() } else { m.display.clone() }, ..m.clone() }).collect(),
            None => self.members.iter().map(|id| Member { id: id.clone(), display: id.clone() }).collect(),
        }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Relay {
    pub id: i64,
    #[serde(default)]
    pub from: String,
    #[serde(default)]
    pub to: String,
    #[serde(default)]
    pub pattern: String,
    #[serde(default)]
    pub handoff: String,
    #[serde(default)]
    pub once: bool,
    #[serde(default)]
    pub active: bool,
    #[serde(default)]
    pub pending_count: i64,
    #[serde(default)]
    pub fire_count: i64,
    #[serde(default)]
    pub last_fired_at: Option<String>,
    #[serde(default)]
    pub last_error: Option<String>,
}

pub const TURN_FINISHED: &str = "turn_finished";
pub const GOAL_COMPLETED: &str = "goal_completed";

pub fn handoff_label(handoff: &str) -> &'static str {
    if handoff == GOAL_COMPLETED { "Goal completed" } else { "Turn finished" }
}

impl Relay {
    pub fn route(&self) -> String {
        format!("{} → {}", self.from, self.to)
    }

    pub fn state(&self, now: OffsetDateTime) -> String {
        let fired = if self.fire_count == 1 { "fired once".to_string() } else { format!("fired {} times", self.fire_count) };
        let mut parts = vec![
            handoff_label(&self.handoff).to_string(),
            if self.once { "Once" } else { "Every time" }.to_string(),
            format!("{} pending", self.pending_count),
            if self.active { "active" } else { "complete" }.to_string(),
            fired,
        ];
        if let Some(last) = &self.last_fired_at {
            match ago(last, now).as_str() {
                "" => {}
                "now" => parts.push("last just now".into()),
                age => parts.push(format!("last {age} ago")),
            }
        }
        parts.join(" · ")
    }

    pub fn problem(&self) -> Option<&str> {
        self.last_error.as_deref().filter(|e| !e.is_empty())
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Handoffs {
    #[serde(default, deserialize_with = "listed")]
    pub channels: Vec<Channel>,
    #[serde(default, deserialize_with = "listed")]
    pub relays: Vec<Relay>,
}

impl Handoffs {
    pub fn channel(&self, name: &str) -> Option<&Channel> {
        self.channels.iter().find(|c| c.name == name)
    }
}

pub fn channel_name(draft: &str) -> Result<String, &'static str> {
    let name = draft.trim().trim_start_matches('#');
    if name.is_empty() {
        return Err("Name the channel.");
    }
    if name.len() > 80 || name.contains([' ', '\t', '\r', '\n', '/', ':', '#']) {
        return Err("A channel name is 1 to 80 characters without spaces, /, : or #.");
    }
    Ok(name.to_string())
}

pub fn session_ref(draft: &str) -> Option<String> {
    let s = draft.trim();
    (!s.is_empty()).then(|| s.to_string())
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct RelayDraft {
    pub from: String,
    pub to: String,
    pub pattern: String,
    pub handoff: String,
    pub once: bool,
}

impl RelayDraft {
    pub fn problem(&self) -> Option<&'static str> {
        match (session_ref(&self.from), session_ref(&self.to)) {
            (None, _) => Some("Name the session whose replies hand off."),
            (_, None) => Some("Name the session that receives them."),
            (Some(a), Some(b)) if a == b => Some("A session can't hand off to itself."),
            _ => None,
        }
    }

    pub fn body(&self) -> Value {
        let pattern = self.pattern.trim();
        let handoff = if self.handoff == GOAL_COMPLETED { GOAL_COMPLETED } else { TURN_FINISHED };
        json!({
            "fromId": self.from.trim(),
            "toId": self.to.trim(),
            "pattern": if pattern.is_empty() { ".*" } else { pattern },
            "once": self.once,
            "handoff": handoff,
        })
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Sent {
    #[serde(default)]
    pub sent: i64,
    #[serde(default)]
    pub queued: i64,
    #[serde(default)]
    pub submitted: i64,
    #[serde(default)]
    pub skipped: i64,
    #[serde(default)]
    pub older: i64,
    #[serde(default, deserialize_with = "listed")]
    pub unreachable: Vec<String>,
}

fn count(n: i64, one: &str, many: &str) -> String {
    if n == 1 { format!("1 {one}") } else { format!("{n} {many}") }
}

impl Sent {
    pub fn summary(&self) -> String {
        let mut parts = vec![];
        if self.sent > 0 {
            parts.push(format!("delivered to {}", count(self.sent, "session", "sessions")));
        }
        if self.queued > 0 {
            parts.push(format!("queued for {}", count(self.queued, "session", "sessions")));
        }
        if self.submitted > 0 {
            parts.push(format!("{} taken but not yet confirmed", count(self.submitted, "delivery", "deliveries")));
        }
        if self.skipped > 0 {
            parts.push(format!("{} skipped", count(self.skipped, "member", "members")));
        }
        if self.older > 0 {
            parts.push(format!("{} not reached", count(self.older, "older session", "older sessions")));
        }
        if !self.unreachable.is_empty() {
            parts.push(format!("held for {}", self.unreachable.join(", ")));
        }
        if parts.is_empty() {
            return "Sent.".into();
        }
        let mut out = parts.join("; ");
        out[..1].make_ascii_uppercase();
        out.push('.');
        out
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct PickHost {
    #[serde(default)]
    pub state: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Pick {
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub alias: Option<String>,
    #[serde(default)]
    pub read_only: bool,
    #[serde(default)]
    pub host: Option<PickHost>,
}

impl Pick {
    pub fn local(&self) -> bool {
        self.host.as_ref().is_none_or(|h| h.state.is_empty() || h.state == "local")
    }

    pub fn alias(&self) -> Option<&str> {
        self.alias.as_deref().filter(|a| !a.is_empty())
    }

    pub fn label(&self) -> String {
        match self.alias() {
            Some(alias) => format!("@{alias}"),
            None if self.name.trim().is_empty() => self.id.clone(),
            None => self.name.trim().to_string(),
        }
    }

    pub fn target(&self) -> String {
        self.alias().map(|a| format!("@{a}")).unwrap_or_else(|| self.id.clone())
    }
}

pub fn picks(found: Vec<Pick>, writable_only: bool) -> Vec<Pick> {
    let mut seen = std::collections::HashSet::new();
    found.into_iter().filter(|p| p.local() && !(writable_only && p.read_only) && seen.insert(p.id.clone())).collect()
}

#[derive(Deserialize)]
struct Found {
    #[serde(default, deserialize_with = "listed")]
    sessions: Vec<Pick>,
}

fn act(client: &Client, name: &str, payload: Value) -> Result<Value, Failure> {
    client.action(name, payload).and_then(confirmed).map_err(|e| write_failure("control", e))
}

impl Client {
    pub fn find_sessions(&self, query: &str) -> Result<Vec<Pick>, Failure> {
        let path = format!("/api/v1/snapshot?scope=all&members=1&limit=20&q={}", enc(query.trim()));
        let value = self.get_reply(&path).map_err(|(status, body)| crate::prefs::read_failure("read", status, &body))?;
        let found: Found = serde_json::from_value(value).map_err(|e| Failure::new(format!("the daemon's reply couldn't be read: {e}")))?;
        Ok(found.sessions)
    }

    pub fn channel_create(&self, name: &str) -> Result<(), Failure> {
        act(self, "channel-create", json!({"channel": name})).map(drop)
    }

    pub fn channel_delete(&self, name: &str) -> Result<(), Failure> {
        act(self, "channel-delete", json!({"channel": name})).map(drop)
    }

    pub fn channel_add(&self, name: &str, target: &str) -> Result<(), Failure> {
        act(self, "channel-add", json!({"channel": name, "targetId": target})).map(drop)
    }

    pub fn channel_remove(&self, name: &str, target: &str) -> Result<(), Failure> {
        act(self, "channel-remove", json!({"channel": name, "targetId": target})).map(drop)
    }

    pub fn channel_send(&self, name: &str, message: &str) -> Result<Sent, Failure> {
        let reply = act(self, "channel-send", json!({"channel": name, "message": message, "author": "person"}))?;
        Ok(serde_json::from_value(reply).unwrap_or_default())
    }

    pub fn relay_add(&self, draft: &RelayDraft) -> Result<(), Failure> {
        act(self, "relay-add", draft.body()).map(drop)
    }

    pub fn relay_remove(&self, id: i64) -> Result<(), Failure> {
        act(self, "relay-remove", json!({"relayId": id})).map(drop)
    }
}

#[cfg(test)]
#[path = "handoffs_tests.rs"]
mod tests;
