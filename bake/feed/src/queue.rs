use std::collections::HashMap;

use serde::Deserialize;
use serde_json::Value;
use time::OffsetDateTime;

use crate::catalog::{Capabilities, Client, RespondError, ago, enc};
use crate::composer::current;

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Queued {
    #[serde(default)]
    pub id: i64,
    #[serde(default)]
    pub session_id: String,
    #[serde(default)]
    pub message: String,
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub last_error: Option<String>,
    #[serde(default)]
    pub queued_at: String,
    #[serde(default)]
    pub held_by: String,
    #[serde(default)]
    pub evidence: String,
    #[serde(default)]
    pub operation: String,
    #[serde(default)]
    pub attachments: Vec<Value>,
    #[serde(default)]
    pub author: String,
    #[serde(default)]
    pub model: String,
    #[serde(default)]
    pub service_tier: String,
    #[serde(default)]
    pub output_schema: Option<Value>,
}

impl Queued {
    pub fn first_line(&self) -> &str {
        self.message.lines().find(|l| !l.trim().is_empty()).unwrap_or_default().trim()
    }

    fn hold(&self) -> &'static str {
        match self.held_by.as_str() {
            "waiting" => "Held: this agent is waiting on you",
            "busy" => "Held: this agent is working",
            "offline" => "Held: this agent is offline",
            "unknown" => "Held: this agent's state is unknown",
            "refused" => "Held: this agent refused to start",
            "retrying" => "Held: waiting to retry",
            "behind" => "Held: behind an earlier message",
            "notLoaded" => "Held: this conversation is not loaded",
            "hostUnreachable" => "Held: this agent's host is unreachable",
            _ => "Delivers on the next scan",
        }
    }

    pub fn summary(&self, now: OffsetDateTime) -> String {
        let reason = match self.evidence.as_str() {
            "transport_accepted" => "Transport accepted; receiver completion is pending",
            "unknown" => "Submitted",
            "failed" => "Delivery failed",
            _ => self.hold(),
        };
        let mut parts = vec![reason.to_string()];
        if let Some(error) = self.last_error.as_deref().map(str::trim).filter(|e| !e.is_empty()) {
            parts.push(error.to_string());
        }
        match ago(&self.queued_at, now).as_str() {
            "" => {}
            "now" => parts.push("Queued just now".into()),
            age => parts.push(format!("Queued {age} ago")),
        }
        parts.join(" · ")
    }

    pub fn steer_refusal(&self, caps: &Capabilities) -> Option<&'static str> {
        if !caps.steer {
            return Some("This session can't take a steer. It sends when the turn ends.");
        }
        if !self.author.is_empty() && self.author != "person" {
            return Some("This message came from another session, and Steer would send it as yours. It sends when the turn ends.");
        }
        if !self.model.is_empty() || !self.service_tier.is_empty() || self.output_schema.as_ref().is_some_and(|s| !s.is_null()) {
            return Some("Steer can't carry this message's model or turn settings. It sends when the turn ends.");
        }
        if !self.attachments.is_empty() {
            return Some("Steer can't carry this message's attachments from here. It sends when the turn ends.");
        }
        None
    }

    pub fn take_back_refusal(&self) -> Option<&'static str> {
        (!self.attachments.is_empty()).then_some("Editing here would drop this message's attachments.")
    }
}

pub fn waiting(rows: &[Queued], rekeyed: &HashMap<String, String>, session: &str) -> Vec<Queued> {
    let mut out: Vec<Queued> = rows.iter().filter(|r| r.status == "pending" && matches!(r.operation.as_str(), "" | "message") && current(rekeyed, &r.session_id) == session).cloned().collect();
    out.sort_by_key(|r| r.id);
    out
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Op {
    Remove,
    TakeBack,
    Steer,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Done {
    Removed,
    TakenBack(String),
    Steered,
    SteerFailed(String, RespondError),
    Failed(RespondError),
}

impl Client {
    pub fn queue(&self, session: &str) -> anyhow::Result<Vec<Queued>> {
        let value = self.get_json(&format!("/api/v1/queue?sessionId={}", enc(session)))?;
        Ok(serde_json::from_value(value["items"].clone()).unwrap_or_default())
    }

    pub fn queue_op(&self, op: Op, row: &Queued, host: &str) -> Done {
        let message = match self.queue_cancel(row.id, host) {
            Ok(text) if text.is_empty() => row.message.clone(),
            Ok(text) => text,
            Err(error) => return Done::Failed(error),
        };
        match op {
            Op::Remove => Done::Removed,
            Op::TakeBack => Done::TakenBack(message),
            Op::Steer => match self.steer(&row.session_id, &message) {
                Ok(()) => Done::Steered,
                Err(error) => Done::SteerFailed(message, error),
            },
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};
    use serde_json::json;

    fn row(id: i64, session: &str, status: &str) -> Queued {
        Queued { id, session_id: session.into(), status: status.into(), message: format!("m{id}"), author: "person".into(), ..Default::default() }
    }

    #[test]
    fn only_messages_still_waiting_for_this_session_show_oldest_first() {
        let mut steer = row(4, "s1", "pending");
        steer.operation = "steer".into();
        let rows = vec![row(9, "s1", "pending"), row(3, "old", "pending"), row(5, "s1", "delivered"), row(6, "s2", "pending"), steer, row(2, "s1", "dead")];
        let rekeyed: HashMap<String, String> = [("old".to_string(), "s1".to_string())].into();
        let ids: Vec<i64> = waiting(&rows, &rekeyed, "s1").iter().map(|r| r.id).collect();
        assert_eq!(ids, vec![3, 9]);
    }

    #[test]
    fn a_row_reads_the_daemons_fields() {
        let rows: Vec<Queued> = serde_json::from_value(json!([{"id": 7, "sessionId": "s1", "target": "@b", "message": "\n check the build\nthen ship", "status": "pending", "attempts": 0, "queuedAt": "2026-11-26T10:00:00Z", "heldBy": "busy", "historical": false, "evidence": "queued", "operation": "message", "author": "person", "serviceTier": "flex"}])).unwrap();
        let r = &rows[0];
        assert_eq!((r.id, r.first_line(), r.service_tier.as_str()), (7, "check the build", "flex"));
        let now = crate::catalog::parse_time("2026-11-26T10:05:00Z").unwrap();
        assert_eq!(r.summary(now), "Held: this agent is working · Queued 5m ago");
        let failed = Queued { evidence: "failed".into(), last_error: Some(" 401 ".into()), queued_at: String::new(), ..r.clone() };
        assert_eq!(failed.summary(now), "Delivery failed · 401");
    }

    #[test]
    fn steer_is_refused_for_what_it_cant_carry() {
        let caps = Capabilities { steer: true, ..Default::default() };
        let mine = row(1, "s1", "pending");
        assert_eq!(mine.steer_refusal(&caps), None);
        assert!(mine.steer_refusal(&Capabilities { steer: false, ..caps }).is_some());
        assert!(Queued { author: "agent".into(), ..mine.clone() }.steer_refusal(&caps).unwrap().contains("another session"));
        assert!(Queued { model: "opus".into(), ..mine.clone() }.steer_refusal(&caps).unwrap().contains("model"));
        assert!(Queued { output_schema: Some(json!({})), ..mine.clone() }.steer_refusal(&caps).is_some());
        let attached = Queued { attachments: vec![json!({"name": "a.png"})], ..mine.clone() };
        assert!(attached.steer_refusal(&caps).unwrap().contains("attachments"));
        assert!(attached.take_back_refusal().is_some());
        assert_eq!(mine.take_back_refusal(), None);
    }

    #[test]
    fn the_queue_is_read_for_one_session() {
        let (client, seen) = daemon(replies(&[(200, r#"{"items":[{"id":3,"sessionId":"s 1","message":"hi","status":"pending"}]}"#)]));
        let rows = client.queue("s 1").unwrap();
        assert_eq!(seen.recv().unwrap().0, "/api/v1/queue?sessionId=s+1");
        assert_eq!((rows[0].id, rows[0].message.as_str()), (3, "hi"));
    }

    #[test]
    fn each_queue_op_cancels_on_the_host_then_does_its_part() {
        let r = row(7, "s1", "pending");
        let (client, seen) = daemon(replies(&[
            (200, r#"{"ok":true,"result":{"message":"edited text"}}"#),
            (200, r#"{"ok":true,"result":{"message":""}}"#),
            (200, r#"{"ok":true,"result":{"message":"steer me"}}"#),
            (200, r#"{"ok":true}"#),
        ]));
        assert_eq!(client.queue_op(Op::TakeBack, &r, "mac"), Done::TakenBack("edited text".into()));
        assert_eq!(seen.recv().unwrap().1, json!({"action": "queue-cancel", "queueId": 7, "host": "mac"}));
        assert_eq!(client.queue_op(Op::Remove, &r, ""), Done::Removed);
        seen.recv().unwrap();
        assert_eq!(client.queue_op(Op::Steer, &r, ""), Done::Steered);
        seen.recv().unwrap();
        assert_eq!(seen.recv().unwrap().1, json!({"action": "steer", "sessionId": "s1", "message": "steer me", "author": "person"}));
    }

    #[test]
    fn a_failed_cancel_keeps_the_row_and_a_failed_steer_hands_the_text_back() {
        let r = row(7, "s1", "pending");
        let (client, _seen) = daemon(replies(&[
            (400, r#"{"error":{"code":"invalid_request","message":"already delivered"}}"#),
            (200, r#"{"ok":true,"result":{"message":"steer me"}}"#),
            (502, r#"{"error":{"code":"bad_gateway","message":"turn busy"}}"#),
        ]));
        match client.queue_op(Op::Steer, &r, "") {
            Done::Failed(error) => assert_eq!(error.message, "already delivered"),
            other => panic!("{other:?}"),
        }
        match client.queue_op(Op::Steer, &r, "") {
            Done::SteerFailed(text, error) => assert!(text == "steer me" && error.maybe_delivered),
            other => panic!("{other:?}"),
        }
    }
}
