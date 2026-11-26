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
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};

    fn at(ts: &str) -> OffsetDateTime {
        OffsetDateTime::parse(ts, &time::format_description::well_known::Rfc3339).unwrap()
    }

    #[test]
    fn the_snapshot_lists_channels_with_members_and_relays_with_their_state() {
        let body = r#"{"busyDelivery":"queue","eventCursor":3,
            "channels":[{"name":"launch","members":["s1","s2"],"memberDetails":[{"id":"s1","display":"@writer"},{"id":"s2","display":""}]},{"name":"old","members":["s9"],"memberDetails":null}],
            "relays":[{"id":7,"from":"@research","to":"@builder","pattern":"READY","handoff":"goal_completed","once":true,"active":false,"pendingCount":2,"fireCount":1,"lastFiredAt":"2026-11-26T11:55:00Z","lastError":"destination busy","eventCursor":9}]}"#;
        let h: Handoffs = serde_json::from_str(body).unwrap();
        assert_eq!(h.channel("launch").unwrap().people(), vec![Member { id: "s1".into(), display: "@writer".into() }, Member { id: "s2".into(), display: "s2".into() }]);
        assert_eq!(h.channel("old").unwrap().people(), vec![Member { id: "s9".into(), display: "s9".into() }]);
        let r = &h.relays[0];
        assert_eq!(r.route(), "@research → @builder");
        assert_eq!(r.state(at("2026-11-26T12:00:00Z")), "Goal completed · Once · 2 pending · complete · fired once · last 5m ago");
        assert_eq!(r.problem(), Some("destination busy"));
        let fresh = Relay { fire_count: 3, active: true, last_fired_at: Some("2026-11-26T12:00:00Z".into()), ..Default::default() };
        assert_eq!(fresh.state(at("2026-11-26T12:00:10Z")), "Turn finished · Every time · 0 pending · active · fired 3 times · last just now");
        let empty: Handoffs = serde_json::from_str(r#"{"channels":null,"relays":null}"#).unwrap();
        assert!(empty.channels.is_empty() && empty.relays.is_empty());
        let missing: Handoffs = serde_json::from_str("{}").unwrap();
        assert_eq!(missing, Handoffs::default());
    }

    #[test]
    fn channel_names_follow_the_daemons_rule() {
        assert_eq!(channel_name("  #launch-review "), Ok("launch-review".into()));
        assert!(channel_name("  ").is_err());
        assert!(channel_name("#").is_err());
        assert!(channel_name("launch review").is_err());
        assert!(channel_name("a/b").is_err());
        assert!(channel_name("a:b").is_err());
        assert!(channel_name(&"x".repeat(81)).is_err());
        assert!(channel_name(&"x".repeat(80)).is_ok());
        assert!(channel_name(&"é".repeat(41)).is_err(), "the daemon counts bytes");
    }

    #[test]
    fn a_relay_needs_two_different_sessions_and_defaults_its_pattern() {
        let mut d = RelayDraft::default();
        assert!(d.problem().is_some());
        d.from = " @research ".into();
        assert!(d.problem().is_some());
        d.to = "@research".into();
        assert_eq!(d.problem(), Some("A session can't hand off to itself."));
        d.to = "@builder".into();
        assert_eq!(d.problem(), None);
        assert_eq!(d.body(), json!({"fromId": "@research", "toId": "@builder", "pattern": ".*", "once": false, "handoff": "turn_finished"}));
        d.pattern = " READY|SHIP ".into();
        d.handoff = GOAL_COMPLETED.into();
        d.once = true;
        assert_eq!(d.body(), json!({"fromId": "@research", "toId": "@builder", "pattern": "READY|SHIP", "once": true, "handoff": "goal_completed"}));
    }

    #[test]
    fn a_channel_send_says_who_it_reached() {
        assert_eq!(Sent { sent: 1, ..Default::default() }.summary(), "Delivered to 1 session.");
        assert_eq!(Sent { sent: 2, queued: 1, older: 1, unreachable: vec!["forge".into()], ..Default::default() }.summary(), "Delivered to 2 sessions; queued for 1 session; 1 older session not reached; held for forge.");
        assert_eq!(Sent { submitted: 2, skipped: 1, ..Default::default() }.summary(), "2 deliveries taken but not yet confirmed; 1 member skipped.");
        assert_eq!(Sent::default().summary(), "Sent.");
    }

    #[test]
    fn session_search_offers_local_sessions_and_names_them_for_the_daemon() {
        let page = r#"{"sessions":[
            {"id":"h1::a","name":"Writer","alias":"writer","host":{"state":"local"}},
            {"id":"h1::b","name":"  Docs pass ","alias":"","readOnly":true},
            {"id":"h2::c","name":"Far","host":{"state":"connected"}},
            {"id":"h1::a","name":"Writer again","alias":"writer"},
            {"id":"h1::d","name":""}
        ],"nextCursor":"x"}"#;
        let (client, rx) = daemon(replies(&[(200, page), (403, r#"{"error":{"message":"no"}}"#)]));
        let found = client.find_sessions(" doc & pass ").unwrap();
        let (path, _) = rx.recv().unwrap();
        assert_eq!(path, "/api/v1/snapshot?scope=all&members=1&limit=20&q=doc+%26+pass");
        let all = picks(found.clone(), false);
        assert_eq!(all.iter().map(|p| (p.label(), p.target())).collect::<Vec<_>>(), vec![("@writer".into(), "@writer".into()), ("Docs pass".into(), "h1::b".into()), ("h1::d".to_string(), "h1::d".to_string())]);
        assert_eq!(picks(found, true).iter().map(|p| p.id.as_str()).collect::<Vec<_>>(), vec!["h1::a", "h1::d"]);
        assert!(client.find_sessions("x").unwrap_err().denied);
        let empty: Found = serde_json::from_str(r#"{"sessions":null}"#).unwrap();
        assert!(empty.sessions.is_empty());
    }

    #[test]
    fn each_change_posts_its_action_and_reports_refusals() {
        let ok = r#"{"ok":true}"#;
        let sent = r#"{"ok":true,"sent":2,"queued":0,"submitted":0,"failed":0,"skipped":0,"older":0,"recipients":[]}"#;
        let partial = r#"{"error":{"code":"conflict","message":"channel delivery: 1 sent, 0 queued, 0 submitted, 1 failed (@b failed: closed)"}}"#;
        let denied = r#"{"error":{"code":"unauthorized","message":"no"}}"#;
        let submitted = r#"{"status":"submitted"}"#;
        let (client, rx) = daemon(replies(&[(200, ok), (200, ok), (200, ok), (200, ok), (200, sent), (200, ok), (200, ok), (409, partial), (403, denied), (200, submitted), (504, r#"{"error":{"message":"timed out"}}"#)]));
        client.channel_create("ship").unwrap();
        client.channel_add("ship", "@writer").unwrap();
        client.channel_remove("ship", "s2").unwrap();
        client.channel_delete("ship").unwrap();
        assert_eq!(client.channel_send("ship", "hello").unwrap().summary(), "Delivered to 2 sessions.");
        let draft = RelayDraft { from: "@a".into(), to: "@b".into(), ..Default::default() };
        client.relay_add(&draft).unwrap();
        client.relay_remove(7).unwrap();
        let conflict = client.channel_send("ship", "again").unwrap_err();
        assert!(!conflict.denied && !conflict.uncertain && conflict.message.contains("1 failed"));
        let refused = client.relay_remove(8).unwrap_err();
        assert!(refused.denied && refused.scope == "control");
        assert!(client.channel_delete("x").unwrap_err().uncertain);
        assert!(client.channel_delete("y").unwrap_err().uncertain);
        let bodies: Vec<Value> = rx.try_iter().map(|(path, body)| {
            assert_eq!(path, "/api/v1/actions");
            body
        }).collect();
        assert_eq!(
            bodies[..7],
            [
                json!({"action": "channel-create", "channel": "ship"}),
                json!({"action": "channel-add", "channel": "ship", "targetId": "@writer"}),
                json!({"action": "channel-remove", "channel": "ship", "targetId": "s2"}),
                json!({"action": "channel-delete", "channel": "ship"}),
                json!({"action": "channel-send", "channel": "ship", "message": "hello", "author": "person"}),
                json!({"action": "relay-add", "fromId": "@a", "toId": "@b", "pattern": ".*", "once": false, "handoff": "turn_finished"}),
                json!({"action": "relay-remove", "relayId": 7}),
            ]
        );
    }
}
