use std::collections::HashMap;

use serde::Deserialize;
use serde_json::Value;
use time::OffsetDateTime;

use crate::catalog::{Capabilities, Client, RespondError, ago, enc};
use crate::composer::{Delivered, current};

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
    pub source_session_id: String,
    #[serde(default)]
    pub model: String,
    #[serde(default)]
    pub service_tier: String,
    #[serde(default)]
    pub output_schema: Option<Value>,
    #[serde(skip)]
    pub host: String,
}

impl Queued {
    pub fn from_person(&self) -> bool {
        match self.author.as_str() {
            "person" => true,
            "" => matches!(self.source_session_id.as_str(), "" | "agenthail-person"),
            _ => false,
        }
    }

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
            "limited" => "Held: this agent hit its usage limit",
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
        if !self.from_person() {
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
        if !self.from_person() {
            return Some("This message came from another session; editing it would resend it as yours.");
        }
        if !self.model.is_empty() || !self.service_tier.is_empty() || self.output_schema.as_ref().is_some_and(|s| !s.is_null()) {
            return Some("Editing here would drop this message's model or turn settings.");
        }
        (!self.attachments.is_empty()).then_some("Editing here would drop this message's attachments.")
    }
}

pub fn waiting(rows: &[Queued], rekeyed: &HashMap<String, String>, session: &str) -> Vec<Queued> {
    let mut out: Vec<Queued> = rows.iter().filter(|r| r.status == "pending" && matches!(r.operation.as_str(), "" | "message") && current(rekeyed, &r.session_id) == session).cloned().collect();
    out.sort_by_key(|r| r.id);
    out
}

pub const READ_FAILED: &str = "Couldn't read the queue";

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Read {
    pub rows: Vec<Queued>,
    pub partial: Option<String>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Taking {
    pub session: String,
    pub host: String,
    pub id: i64,
    pub text: String,
    pub attempt: String,
}

impl Taking {

    pub fn to_json(list: &[Taking]) -> Value {
        Value::Array(list.iter().map(|t| serde_json::json!({"session": t.session, "host": t.host, "id": t.id, "text": t.text, "attempt": t.attempt})).collect())
    }

    pub fn from_json(value: &Value) -> Vec<Taking> {
        let read = |v: &Value| -> Option<Taking> {
            let text = v["text"].as_str().filter(|t| !t.trim().is_empty())?.to_string();
            let attempt = v["attempt"].as_str().unwrap_or_default().to_string();
            Some(Taking { session: v["session"].as_str().filter(|s| !s.is_empty())?.to_string(), host: v["host"].as_str().unwrap_or_default().to_string(), id: v["id"].as_i64()?, text, attempt })
        };
        value.as_array().map(|a| a.iter().filter_map(read).collect()).unwrap_or_default()
    }
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
    Steered(Delivered),
    SteerUnconfirmed(String),
    SteerFailed(String, RespondError),
    Unsure(Option<String>, RespondError),
    Failed(RespondError),
}

impl Client {
    fn queue_rows(&self, path: &str, host: &str) -> anyhow::Result<Vec<Queued>> {
        let value = self.get_json(path)?;
        let mut rows: Vec<Queued> = serde_json::from_value(value["items"].clone()).unwrap_or_default();
        for row in &mut rows {
            row.host = host.to_string();
        }
        Ok(rows)
    }

    pub fn queue(&self, session: &str, remote: Option<&str>) -> anyhow::Result<Read> {
        let routed = self.queue_rows(&format!("/api/v1/queue?sessionId={}", enc(session)), remote.unwrap_or_default());
        let Some(host) = remote else { return routed.map(|rows| Read { rows, partial: None }) };
        let held = self.held_here(session);
        match (routed, held) {
            (Err(error), Err(_)) => Err(error),
            (Ok(mut rows), Ok(held)) => {
                rows.extend(held);
                Ok(Read { rows, partial: None })
            }
            (Err(error), Ok(rows)) => Ok(Read { rows, partial: Some(format!("{READ_FAILED} on {host}, so only what this computer holds is shown: {error}")) }),
            (Ok(rows), Err(error)) => Ok(Read { rows, partial: Some(format!("{READ_FAILED} this computer holds for this session: {error}")) }),
        }
    }

    fn held_here(&self, session: &str) -> anyhow::Result<Vec<Queued>> {
        self.queue_rows("/api/v1/queue", "").map(|rows| rows.into_iter().filter(|r| r.session_id == session).collect())
    }

    pub fn finish(&self, taking: &Taking) -> Option<String> {
        let mut path = format!("/api/v1/queue?id={}", taking.id);
        if !taking.host.is_empty() {
            path.push_str(&format!("&host={}", enc(&taking.host)));
        }
        let status = self.queue_rows(&path, &taking.host).ok().and_then(|rows| rows.into_iter().next()).map(|r| r.status);
        match status.as_deref() {
            Some("delivered" | "inflight") => None,
            Some("pending") => match self.queue_cancel(taking.id, &taking.host) {
                Ok(text) if !text.trim().is_empty() => Some(text),
                _ => Some(taking.text.clone()),
            },
            _ => Some(taking.text.clone()),
        }
    }

    pub fn queue_op(&self, op: Op, row: &Queued) -> Done {
        let message = match self.queue_cancel(row.id, &row.host) {
            Ok(text) if text.is_empty() => row.message.clone(),
            Ok(text) => text,
            Err(error) if error.maybe_delivered => return Done::Unsure((op != Op::Remove).then(|| row.message.clone()), error),
            Err(error) => return Done::Failed(error),
        };
        match op {
            Op::Remove => Done::Removed,
            Op::TakeBack => Done::TakenBack(message),
            Op::Steer => match self.action("steer", serde_json::json!({"sessionId": row.session_id, "message": message, "author": "person"})) {
                Ok(reply) => match Delivered::read(&reply) {
                    Delivered::Unconfirmed => Done::SteerUnconfirmed(message),
                    receipt => Done::Steered(receipt),
                },
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
        assert_eq!(Queued { held_by: "limited".into(), queued_at: String::new(), ..r.clone() }.summary(now), "Held: this agent hit its usage limit");
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
        assert!(Queued { author: "agent".into(), ..mine.clone() }.take_back_refusal().unwrap().contains("as yours"));
        let from_writer = Queued { author: String::new(), source_session_id: "writer".into(), ..mine.clone() };
        assert!(from_writer.steer_refusal(&caps).unwrap().contains("another session") && from_writer.take_back_refusal().is_some());
        for source in ["", "agenthail-person"] {
            assert!(Queued { author: String::new(), source_session_id: source.into(), ..mine.clone() }.take_back_refusal().is_none());
        }
        assert!(Queued { author: String::new(), source_session_id: "agenthail".into(), ..mine.clone() }.take_back_refusal().is_some());
        assert!(Queued { source_session_id: "writer".into(), ..mine.clone() }.take_back_refusal().is_none());
        assert!(Queued { service_tier: "flex".into(), ..mine.clone() }.take_back_refusal().unwrap().contains("turn settings"));
    }

    #[test]
    fn a_message_being_taken_back_survives_a_restart() {
        let taking = vec![Taking { session: "s1".into(), host: "vps".into(), id: 7, text: "hold on".into(), attempt: "k1".into() }];
        assert_eq!(Taking::from_json(&Taking::to_json(&taking)), taking);
        assert!(Taking::from_json(&json!([{"session": "s1", "id": 7, "text": " "}, {"id": 7, "text": "x"}, {"session": "s1", "text": "x"}])).is_empty());
        assert_eq!(Taking::from_json(&json!(null)), vec![]);
    }

    #[test]
    fn a_leftover_take_back_is_finished_and_restored_unless_the_message_went_through() {
        let taking = |id: i64, host: &str| Taking { session: "s1".into(), host: host.into(), id, text: format!("t{id}"), attempt: format!("a{id}") };
        let (client, seen) = daemon(replies(&[
            (200, r#"{"items":[{"id":7,"sessionId":"s1","status":"pending"}]}"#),
            (200, r#"{"ok":true,"result":{"message":"as queued"}}"#),
            (200, r#"{"items":[{"id":8,"sessionId":"s1","status":"canceled"}]}"#),
            (200, r#"{"items":[{"id":9,"sessionId":"s1","status":"delivered"}]}"#),
            (200, r#"{"items":[]}"#),
            (500, r#"{"error":{"code":"internal_error","message":"down"}}"#),
            (200, r#"{"items":[{"id":12,"sessionId":"s1","status":"pending"}]}"#),
            (400, r#"{"error":{"code":"invalid_request","message":"already canceled"}}"#),
        ]));
        assert_eq!(client.finish(&taking(7, "vps")).as_deref(), Some("as queued"));
        assert_eq!(seen.recv().unwrap().0, "/api/v1/queue?id=7&host=vps");
        assert_eq!(seen.recv().unwrap().1, json!({"action": "queue-cancel", "queueId": 7, "host": "vps"}));
        assert_eq!(client.finish(&taking(8, "")).as_deref(), Some("t8"));
        assert_eq!(seen.recv().unwrap().0, "/api/v1/queue?id=8");
        assert_eq!(client.finish(&taking(9, "")), None);
        assert_eq!(client.finish(&taking(10, "")).as_deref(), Some("t10"));
        assert_eq!(client.finish(&taking(11, "")).as_deref(), Some("t11"));
        assert_eq!(client.finish(&taking(12, "")).as_deref(), Some("t12"));
    }

    #[test]
    fn the_queue_is_read_for_one_session() {
        let (client, seen) = daemon(replies(&[(200, r#"{"items":[{"id":3,"sessionId":"s 1","message":"hi","status":"pending"}]}"#)]));
        let rows = client.queue("s 1", None).unwrap().rows;
        assert_eq!(seen.recv().unwrap().0, "/api/v1/queue?sessionId=s+1");
        assert_eq!((rows[0].id, rows[0].message.as_str(), rows[0].host.as_str()), (3, "hi", ""));
    }

    #[test]
    fn a_remote_sessions_queue_includes_what_this_computer_holds_for_it() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"items":[{"id":3,"sessionId":"vps/s1","message":"there","status":"pending"}]}"#),
            (200, r#"{"items":[{"id":3,"sessionId":"vps/s1","message":"held here","status":"pending"},{"id":4,"sessionId":"other","status":"pending"}]}"#),
            (502, r#"{"error":{"code":"host_unreachable","message":"Host vps is unreachable"}}"#),
            (200, r#"{"items":[{"id":5,"sessionId":"vps/s1","message":"held","status":"pending"}]}"#),
        ]));
        let read = client.queue("vps/s1", Some("vps")).unwrap();
        assert_eq!(read.partial, None);
        let rows = read.rows;
        assert_eq!(seen.recv().unwrap().0, "/api/v1/queue?sessionId=vps%2Fs1");
        assert_eq!(seen.recv().unwrap().0, "/api/v1/queue");
        let got: Vec<(i64, &str, &str)> = rows.iter().map(|r| (r.id, r.host.as_str(), r.message.as_str())).collect();
        assert_eq!(got, vec![(3, "vps", "there"), (3, "", "held here")]);
        let down = client.queue("vps/s1", Some("vps")).unwrap();
        assert_eq!((down.rows.len(), down.rows[0].id, down.rows[0].host.as_str()), (1, 5, ""));
        assert!(down.partial.unwrap().starts_with("Couldn't read the queue on vps"));
    }

    #[test]
    fn each_queue_op_cancels_on_the_host_then_does_its_part() {
        let r = row(7, "s1", "pending");
        let (client, seen) = daemon(replies(&[
            (200, r#"{"ok":true,"result":{"message":"edited text"}}"#),
            (200, r#"{"ok":true,"result":{"message":""}}"#),
            (200, r#"{"ok":true,"result":{"message":"steer me"}}"#),
            (200, r#"{"ok":true,"result":{"evidence":"transport_accepted","status":"sent","requestId":"r1"}}"#),
            (200, r#"{"ok":true,"result":{"message":"x"}}"#),
            (200, r#"{"ok":true,"result":{"evidence":"queued","queueId":9,"waitingOn":{"kind":"ask"}}}"#),
            (200, r#"{"ok":true,"result":{"message":"x"}}"#),
            (202, r#"{"ok":true,"status":"submitted"}"#),
        ]));
        assert_eq!(client.queue_op(Op::TakeBack, &Queued { host: "mac".into(), ..r.clone() }), Done::TakenBack("edited text".into()));
        assert_eq!(seen.recv().unwrap().1, json!({"action": "queue-cancel", "queueId": 7, "host": "mac"}));
        assert_eq!(client.queue_op(Op::Remove, &r), Done::Removed);
        seen.recv().unwrap();
        assert_eq!(client.queue_op(Op::Steer, &r), Done::Steered(Delivered::Steered));
        seen.recv().unwrap();
        assert_eq!(seen.recv().unwrap().1, json!({"action": "steer", "sessionId": "s1", "message": "steer me", "author": "person"}));
        assert_eq!(client.queue_op(Op::Steer, &r), Done::Steered(Delivered::QueuedUntilAnswer(Some(9))));
        assert_eq!(client.queue_op(Op::Steer, &r), Done::SteerUnconfirmed("x".into()));
    }

    #[test]
    fn a_failed_cancel_keeps_the_row_and_a_failed_steer_hands_the_text_back() {
        let r = row(7, "s1", "pending");
        let (client, _seen) = daemon(replies(&[
            (400, r#"{"error":{"code":"invalid_request","message":"already delivered"}}"#),
            (200, r#"{"ok":true,"result":{"message":"steer me"}}"#),
            (502, r#"{"error":{"code":"bad_gateway","message":"turn busy"}}"#),
            (504, r#"{"error":{"code":"host_outcome_unknown","message":"Host mac did not answer"}}"#),
            (504, r#"{"error":{"code":"host_outcome_unknown","message":"Host mac did not answer"}}"#),
        ]));
        match client.queue_op(Op::Steer, &r) {
            Done::Failed(error) => assert_eq!(error.message, "already delivered"),
            other => panic!("{other:?}"),
        }
        match client.queue_op(Op::Steer, &r) {
            Done::SteerFailed(text, error) => assert!(text == "steer me" && error.maybe_delivered),
            other => panic!("{other:?}"),
        }
        match client.queue_op(Op::TakeBack, &r) {
            Done::Unsure(Some(text), error) => assert!(text == "m7" && error.maybe_delivered),
            other => panic!("{other:?}"),
        }
        assert!(matches!(client.queue_op(Op::Remove, &r), Done::Unsure(None, _)));
    }
}
