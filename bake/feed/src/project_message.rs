use serde::Deserialize;
use serde_json::{Value, json};

use crate::catalog::{Client, RespondError, confirmed};

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Recipient {
    #[serde(default)]
    pub session_id: String,
    #[serde(default)]
    pub coordinator: String,
    #[serde(default)]
    pub outcome: String,
    #[serde(default)]
    pub detail: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct HostLeg {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub detail: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Broadcast {
    #[serde(default)]
    pub recipients: Vec<Recipient>,
    #[serde(default)]
    pub hosts: Vec<HostLeg>,
    #[serde(default)]
    pub unreachable: Vec<String>,
}

pub fn prompt(project: &str) -> String {
    format!("Message every active conversation in {project}")
}

pub fn help(project: &str) -> String {
    format!("Send one message to every active conversation in {project}")
}

fn named(parts: &[&str]) -> String {
    parts.iter().filter(|p| !p.is_empty()).copied().collect::<Vec<_>>().join(": ")
}

pub fn sent(result: &Broadcast, name: impl Fn(&str) -> String) -> String {
    let label = |r: &Recipient| if r.coordinator.is_empty() { name(&r.session_id) } else { r.coordinator.clone() };
    let reached: Vec<String> = result.recipients.iter().filter(|r| r.outcome != "failed").map(label).collect();
    let missed: Vec<String> = result.recipients.iter().filter(|r| r.outcome == "failed").map(|r| named(&[&label(r), &r.detail])).collect();
    let mut notice = format!("Sent to {}{}.", reached.len(), if reached.is_empty() { String::new() } else { format!(": {}", reached.join(", ")) });
    if !missed.is_empty() {
        notice += &format!(" Not delivered to {}.", missed.join("; "));
    }
    if !result.unreachable.is_empty() {
        notice += &format!(" Held for {} until it answers.", result.unreachable.join(", "));
    }
    let refused: Vec<String> = result.hosts.iter().filter(|h| h.state == "refused").map(|h| named(&[&h.name, &h.detail])).collect();
    if !refused.is_empty() {
        notice += &format!(" Refused by {}.", refused.join("; "));
    }
    let unreached: Vec<&str> = result.hosts.iter().filter(|h| h.state == "unreached").map(|h| h.name.as_str()).collect();
    if !unreached.is_empty() {
        notice += &format!(" Couldn't reach {}.", unreached.join(", "));
    }
    notice
}

fn read(reply: &Value) -> Option<Broadcast> {
    reply["recipients"].is_array().then(|| serde_json::from_value(reply.clone()).ok()).flatten()
}

impl Client {
    pub fn broadcast(&self, project: &str, message: &str, key: &str) -> Result<Broadcast, RespondError> {
        match self.action_keyed("channel-send", json!({"channel": format!("#{project}"), "message": message, "author": "person"}), key).and_then(confirmed) {
            Ok(reply) => read(&reply).ok_or_else(|| RespondError {
                message: "Agenthail's answer didn't say who got the message".into(),
                code: "unreadable_reply".into(),
                maybe_delivered: true,
                answered: true,
                reply,
                status: 0,
            }),
            Err(mut e) if e.status == 400 && e.message.ends_with("has no members other than the sender") => {
                e.code = "no_members".into();
                Err(e)
            }
            Err(e) if e.status == 409 => match read(&e.reply).filter(|b| !b.recipients.is_empty() || !b.hosts.is_empty()) {
                Some(partial) => Ok(partial),
                None => Err(e),
            },
            Err(e) => Err(e),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};

    #[test]
    fn the_summary_names_who_got_it_and_who_did_not() {
        let result: Broadcast = serde_json::from_value(json!({
            "recipients": [
                {"sessionId": "s1", "outcome": "sent"},
                {"coordinator": "@lead", "outcome": "queued"},
                {"sessionId": "s2", "outcome": "failed", "detail": "session ended"},
            ],
            "hosts": [{"name": "forge", "state": "refused", "detail": "not allowed"}, {"name": "old", "state": "unreached"}],
            "unreachable": ["spare"],
        }))
        .unwrap();
        let note = sent(&result, |id| if id == "s1" { "scout".into() } else { id.to_string() });
        assert_eq!(note, "Sent to 2: scout, @lead. Not delivered to s2: session ended. Held for spare until it answers. Refused by forge: not allowed. Couldn't reach old.");
        assert_eq!(sent(&Broadcast::default(), |id| id.to_string()), "Sent to 0.");
    }

    #[test]
    fn a_broadcast_goes_to_the_projects_channel_as_the_person_and_reads_partial_delivery() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"ok":true,"recipients":[{"sessionId":"s1","outcome":"sent"}]}"#),
            (409, r#"{"error":{"code":"conflict","message":"channel delivery: 1 sent"},"recipients":[{"sessionId":"s1","outcome":"sent"},{"sessionId":"s2","outcome":"failed","detail":"gone"}]}"#),
            (400, r##"{"error":{"code":"invalid_request","message":"#p1 has no members other than the sender"}}"##),
            (202, r#"{"ok":true,"status":"submitted"}"#),
            (200, r#"{"ok":true}"#),
        ]));
        assert_eq!(client.broadcast("p1", "hi all", "k1").unwrap().recipients.len(), 1);
        let (path, body) = seen.recv().unwrap();
        assert_eq!((path.as_str(), body), ("/api/v1/actions", json!({"action": "channel-send", "channel": "#p1", "message": "hi all", "author": "person"})));
        assert_eq!(client.broadcast("p1", "hi", "k2").unwrap().recipients[1].outcome, "failed");
        let none = client.broadcast("p1", "hi", "k3").unwrap_err();
        assert_eq!((none.code.as_str(), none.maybe_delivered), ("no_members", false));
        assert!(client.broadcast("p1", "hi", "k4").unwrap_err().maybe_delivered);
        assert_eq!(client.broadcast("p1", "hi", "k5").unwrap_err().code, "unreadable_reply");
    }
}
