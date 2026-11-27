use crate::catalog::{Client, enc};
use crate::search::{Entry, failure};

pub const SESSION_HISTORY: usize = 50;
pub const RECENT_HISTORY: usize = 50;
pub const OUTCOMES: usize = 8;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Tone {
    Done,
    Pending,
    Problem,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Outcome {
    pub id: i64,
    pub label: String,
    pub target: String,
    pub detail: String,
    pub is_error: bool,
    pub tone: Tone,
    pub created_at: String,
}

fn some(v: &Option<String>) -> Option<&str> {
    v.as_deref().filter(|s| !s.is_empty())
}

fn label(evidence: &str, kind: &str) -> String {
    match evidence {
        "queued" => "Queued".into(),
        "transport_accepted" => "Transport accepted".into(),
        "held" => "Held by the receiver".into(),
        "delivered" => "Delivered".into(),
        "reply_observed" => "Reply observed".into(),
        "failed" => "Failed".into(),
        "unknown" => "Never confirmed".into(),
        "canceled" => "Canceled".into(),
        _ => crate::search::kind_label(kind),
    }
}

fn tone(evidence: &str) -> Tone {
    match evidence {
        "delivered" | "reply_observed" => Tone::Done,
        "failed" | "unknown" => Tone::Problem,
        _ => Tone::Pending,
    }
}

pub fn outcomes(history: &[Entry]) -> Vec<Outcome> {
    history
        .iter()
        .filter_map(|e| {
            let evidence = some(&e.evidence)?;
            let error = some(&e.error);
            let detail = error.or(some(&e.message)).or(some(&e.result)).unwrap_or("No details recorded").to_string();
            Some(Outcome { id: e.id, label: label(evidence, &e.kind), target: some(&e.target).unwrap_or("Agenthail").to_string(), detail, is_error: error.is_some(), tone: tone(evidence), created_at: e.created_at.clone() })
        })
        .take(OUTCOMES)
        .collect()
}

impl Client {
    pub fn session_history(&self, session: &str) -> Result<Vec<Entry>, String> {
        self.history_at(&format!("/api/v1/history?sessionId={}&limit={SESSION_HISTORY}", enc(session)))
    }

    pub fn recent_history(&self) -> Result<Vec<Entry>, String> {
        self.history_at(&format!("/api/v1/history?limit={RECENT_HISTORY}"))
    }

    fn history_at(&self, path: &str) -> Result<Vec<Entry>, String> {
        let page = self.get_reply(path).map_err(|(code, body)| failure("delivery history", code, &body))?;
        serde_json::from_value(page["items"].clone()).map_err(|e| format!("Agenthail sent delivery history this app can't read: {e}"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};
    use serde_json::json;

    fn entry(v: serde_json::Value) -> Entry {
        serde_json::from_value(v).unwrap()
    }

    #[test]
    fn outcomes_keep_entries_with_evidence_and_name_them() {
        let history = vec![
            entry(json!({"id": 1, "kind": "send", "evidence": "delivered", "target": "@b", "message": "ship it", "createdAt": "t1"})),
            entry(json!({"id": 2, "kind": "mcp", "message": "agenthail_send"})),
            entry(json!({"id": 3, "kind": "send", "evidence": "failed", "error": "401", "message": "x"})),
            entry(json!({"id": 4, "kind": "relay_fire", "evidence": "odd", "target": ""})),
            entry(json!({"id": 5, "kind": "send", "evidence": "unknown", "result": "ok"})),
        ];
        let got = outcomes(&history);
        assert_eq!(got.iter().map(|o| o.id).collect::<Vec<_>>(), vec![1, 3, 4, 5]);
        assert_eq!((got[0].label.as_str(), got[0].target.as_str(), got[0].detail.as_str(), got[0].tone), ("Delivered", "@b", "ship it", Tone::Done));
        assert_eq!((got[1].label.as_str(), got[1].detail.as_str(), got[1].is_error, got[1].tone), ("Failed", "401", true, Tone::Problem));
        assert_eq!((got[2].label.as_str(), got[2].target.as_str(), got[2].detail.as_str(), got[2].tone), ("Relay fire", "Agenthail", "No details recorded", Tone::Pending));
        assert_eq!((got[3].label.as_str(), got[3].tone, got[3].is_error), ("Never confirmed", Tone::Problem, false));
        let many: Vec<Entry> = (0..20).map(|id| entry(json!({"id": id, "evidence": "queued"}))).collect();
        assert_eq!(outcomes(&many).len(), OUTCOMES);
    }

    #[test]
    fn a_sessions_history_is_read_by_session_and_the_recent_one_unfiltered() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"items":[{"id":9,"kind":"send","sessionId":"s 1","evidence":"queued"}],"hasMore":false}"#),
            (200, r#"{"items":[]}"#),
            (400, r#"{"error":{"code":"invalid_request","message":"a session's history takes only a limit"}}"#),
        ]));
        let got = client.session_history("s 1").unwrap();
        assert_eq!((got[0].id, got[0].evidence.as_deref()), (9, Some("queued")));
        assert_eq!(seen.recv().unwrap().0, "/api/v1/history?sessionId=s+1&limit=50");
        assert!(client.recent_history().unwrap().is_empty());
        assert_eq!(seen.recv().unwrap().0, "/api/v1/history?limit=50");
        assert_eq!(client.session_history("s").unwrap_err(), "a session's history takes only a limit");
    }
}
