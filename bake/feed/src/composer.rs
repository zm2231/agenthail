use std::collections::HashMap;

use serde::Deserialize;
use serde_json::{Value, json};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Busy {
    Queue,
    Steer,
}

impl Busy {
    pub fn parse(mode: &str) -> Option<Busy> {
        match mode {
            "queue" => Some(Busy::Queue),
            "steer" => Some(Busy::Steer),
            _ => None,
        }
    }

    pub fn as_str(self) -> &'static str {
        match self {
            Busy::Queue => "queue",
            Busy::Steer => "steer",
        }
    }

    pub fn other(self) -> Busy {
        match self {
            Busy::Queue => Busy::Steer,
            Busy::Steer => Busy::Queue,
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            Busy::Queue => "After this turn",
            Busy::Steer => "Steer now",
        }
    }

    pub fn hint(self) -> &'static str {
        match self {
            Busy::Queue => "Return queues this for after the turn",
            Busy::Steer => "Return steers the turn in progress",
        }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Receipt {
    #[serde(default)]
    pub evidence: String,
    #[serde(default)]
    pub queue_id: Option<i64>,
    #[serde(default)]
    pub not_steered: Option<bool>,
    #[serde(default)]
    pub waiting_on: Option<Value>,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Delivered {
    Sent,
    Steered,
    NotSteered,
    Queued,
    QueuedUntilAnswer,
    Unconfirmed,
}

impl Delivered {
    pub fn read(reply: &Value) -> Delivered {
        let result = &reply["result"];
        if result.is_null() {
            return if reply["status"] == "submitted" { Delivered::Unconfirmed } else { Delivered::Sent };
        }
        let receipt: Receipt = serde_json::from_value(result.clone()).unwrap_or_default();
        match (receipt.evidence.as_str(), receipt.not_steered) {
            ("queued", _) if receipt.waiting_on.as_ref().is_some_and(|w| !w.is_null()) => Delivered::QueuedUntilAnswer,
            ("queued", _) => Delivered::Queued,
            ("unknown" | "submitted", _) => Delivered::Unconfirmed,
            (_, Some(true)) => Delivered::NotSteered,
            (_, Some(false)) => Delivered::Steered,
            _ => Delivered::Sent,
        }
    }

    pub fn note(&self, queued: i64) -> String {
        match self {
            Delivered::Sent => "Sent".into(),
            Delivered::Steered => "Steered".into(),
            Delivered::NotSteered => "Sent as a new message; this session didn't take it as a steer".into(),
            Delivered::Queued => format!("Queued ({})", queued.max(1)),
            Delivered::QueuedUntilAnswer => "Queued until you answer".into(),
            Delivered::Unconfirmed => "Sent, not confirmed yet".into(),
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct Unsent {
    pub session: String,
    pub text: String,
    pub key: String,
    pub busy: Option<Busy>,
    pub maybe_delivered: bool,
    pub error: String,
}

impl Unsent {
    /// The daemon replays a reused id only for the identical body, so an unconfirmed send is retried exactly as first sent.
    pub fn resend(&self, text: &str) -> bool {
        self.maybe_delivered && self.text == text
    }

    pub fn to_json(&self) -> Value {
        json!({"session": self.session, "text": self.text, "key": self.key, "busy": self.busy.map(Busy::as_str), "maybeDelivered": self.maybe_delivered, "error": self.error})
    }

    pub fn from_json(id: &str, value: &Value) -> Option<Unsent> {
        let session = value["session"].as_str().filter(|s| !s.is_empty()).unwrap_or(id).to_string();
        let text = value["text"].as_str().filter(|t| !t.trim().is_empty())?.to_string();
        let key = value["key"].as_str().filter(|k| !k.is_empty())?.to_string();
        let busy = value["busy"].as_str().and_then(Busy::parse);
        let maybe_delivered = value["maybeDelivered"].as_bool().unwrap_or(true);
        Some(Unsent { session, text, key, busy, maybe_delivered, error: value["error"].as_str().unwrap_or_default().to_string() })
    }

    pub fn note(&self) -> String {
        if self.maybe_delivered { format!("Not confirmed: {}. Retry is safe: it won't send twice.", self.error) } else { format!("Not sent: {}", self.error) }
    }
}

pub fn put_back(draft: &str, text: &str) -> String {
    let draft = draft.trim_end();
    if draft.is_empty() || draft == text {
        text.to_string()
    } else if draft.contains(text) {
        draft.to_string()
    } else {
        format!("{text}\n\n{draft}")
    }
}

pub fn take_back(composer: &str, text: &str) -> Option<String> {
    let composer = composer.trim();
    if composer == text {
        return Some(String::new());
    }
    composer.strip_prefix(text).and_then(|rest| rest.strip_prefix("\n\n")).map(|rest| rest.trim_start().to_string())
}

pub fn current<'a>(rekeyed: &'a HashMap<String, String>, mut id: &'a str) -> &'a str {
    for _ in 0..16 {
        match rekeyed.get(id) {
            Some(next) if next != id => id = next,
            _ => break,
        }
    }
    id
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Drafts(HashMap<String, String>);

impl Drafts {
    pub fn get(&self, id: &str) -> &str {
        self.0.get(id).map(String::as_str).unwrap_or_default()
    }

    pub fn set(&mut self, id: &str, text: &str) {
        if text.trim().is_empty() {
            self.0.remove(id);
        } else {
            self.0.insert(id.to_string(), text.to_string());
        }
    }

    pub fn take(&mut self, id: &str) -> String {
        self.0.remove(id).unwrap_or_default()
    }

    pub fn rekey(&mut self, rekeyed: &HashMap<String, String>) {
        let retired: Vec<String> = self.0.keys().filter(|id| current(rekeyed, id) != id.as_str()).cloned().collect();
        for old in retired {
            let moving = self.take(&old);
            let new = current(rekeyed, &old).to_string();
            let existing = self.get(&new).to_string();
            let text = if existing.is_empty() || existing == moving { moving } else { format!("{existing}\n\n{moving}") };
            self.set(&new, &text);
        }
    }

    pub fn to_json(&self) -> Value {
        Value::Object(self.0.iter().map(|(k, v)| (k.clone(), Value::String(v.clone()))).collect())
    }

    pub fn from_json(value: &Value) -> Drafts {
        let map = value.as_object().map(|m| m.iter().filter_map(|(k, v)| v.as_str().filter(|t| !t.trim().is_empty()).map(|t| (k.clone(), t.to_string()))).collect());
        Drafts(map.unwrap_or_default())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn map(pairs: &[(&str, &str)]) -> HashMap<String, String> {
        pairs.iter().map(|(a, b)| (a.to_string(), b.to_string())).collect()
    }

    #[test]
    fn busy_delivery_reads_the_daemons_words() {
        assert_eq!(Busy::parse("queue"), Some(Busy::Queue));
        assert_eq!(Busy::parse("steer"), Some(Busy::Steer));
        assert_eq!(Busy::parse(""), None);
        assert_eq!(Busy::Steer.other(), Busy::Queue);
        assert_eq!(Busy::Queue.as_str(), "queue");
    }

    #[test]
    fn a_receipt_says_what_happened_to_the_message() {
        assert_eq!(Delivered::read(&json!({"ok": true, "result": {"evidence": "delivered", "status": "delivered"}})), Delivered::Sent);
        assert_eq!(Delivered::read(&json!({"ok": true, "result": {"evidence": "transport_accepted", "notSteered": false}})), Delivered::Steered);
        assert_eq!(Delivered::read(&json!({"ok": true, "result": {"evidence": "delivered", "notSteered": true}})), Delivered::NotSteered);
        assert_eq!(Delivered::read(&json!({"ok": true, "result": {"evidence": "queued", "queueId": 4}})), Delivered::Queued);
        assert_eq!(Delivered::read(&json!({"ok": true, "result": {"evidence": "queued", "queueId": 4, "waitingOn": {"kind": "ask"}}})), Delivered::QueuedUntilAnswer);
        assert_eq!(Delivered::read(&json!({"ok": true, "result": {"evidence": "unknown"}})), Delivered::Unconfirmed);
        assert_eq!(Delivered::read(&json!({"ok": true, "result": {"evidence": "submitted", "status": "submitted"}})), Delivered::Unconfirmed);
        assert_eq!(Delivered::read(&json!({"ok": true, "status": "submitted"})), Delivered::Unconfirmed);
        assert_eq!(Delivered::read(&json!({"ok": true})), Delivered::Sent);
        assert_eq!(Delivered::Queued.note(0), "Queued (1)");
        assert_eq!(Delivered::Queued.note(3), "Queued (3)");
        assert_eq!(Delivered::Steered.note(0), "Steered");
    }

    #[test]
    fn only_an_unconfirmed_message_is_sent_again_as_it_was() {
        let unsent = Unsent { session: "s1".into(), text: "ship it".into(), key: "k1".into(), busy: Some(Busy::Steer), maybe_delivered: true, error: "timeout".into() };
        assert!(unsent.resend("ship it"));
        assert!(!unsent.resend("ship it now"));
        let refused = Unsent { maybe_delivered: false, ..unsent };
        assert!(!refused.resend("ship it"));
        assert!(refused.note().starts_with("Not sent: timeout"));
    }

    #[test]
    fn an_unconfirmed_message_survives_a_restart_with_its_request_id() {
        let unsent = Unsent { session: "old".into(), text: "ship it".into(), key: "k1".into(), busy: Some(Busy::Queue), maybe_delivered: true, error: "the app closed".into() };
        assert_eq!(Unsent::from_json("new", &unsent.to_json()), Some(unsent.clone()));
        let idle = Unsent { busy: None, maybe_delivered: false, ..unsent };
        assert_eq!(Unsent::from_json("new", &idle.to_json()), Some(idle));
        assert_eq!(Unsent::from_json("s1", &json!({"text": "hi"})), None);
        assert_eq!(Unsent::from_json("s1", &json!({"text": " ", "key": "k"})), None);
        let bare = Unsent::from_json("s1", &json!({"text": "hi", "key": "k"})).unwrap();
        assert!(bare.maybe_delivered);
        assert_eq!(bare.session, "s1");
    }

    #[test]
    fn a_message_put_back_keeps_what_was_typed_since() {
        assert_eq!(put_back("", "hello"), "hello");
        assert_eq!(put_back("hello", "hello"), "hello");
        assert_eq!(put_back("and more", "hello"), "hello\n\nand more");
        assert_eq!(put_back("hello\n\nand more", "hello"), "hello\n\nand more");
    }

    #[test]
    fn a_retry_takes_back_only_the_copy_that_was_put_back() {
        assert_eq!(take_back("hello", "hello").as_deref(), Some(""));
        assert_eq!(take_back(&put_back("and more", "hello"), "hello").as_deref(), Some("and more"));
        assert_eq!(take_back("hello world", "hello"), None);
        assert_eq!(take_back("say hello", "hello"), None);
    }

    #[test]
    fn a_retired_id_leads_to_the_current_one() {
        let rekeyed = map(&[("a", "b"), ("b", "c"), ("loop", "loop")]);
        assert_eq!(current(&rekeyed, "a"), "c");
        assert_eq!(current(&rekeyed, "c"), "c");
        assert_eq!(current(&rekeyed, "loop"), "loop");
        assert_eq!(current(&rekeyed, "x"), "x");
    }

    #[test]
    fn drafts_follow_a_rekeyed_session_and_merge() {
        let mut drafts = Drafts::default();
        drafts.set("old", "half a thought");
        drafts.set("other", "keep me");
        drafts.set("gone", "  ");
        drafts.rekey(&map(&[("old", "new")]));
        assert_eq!(drafts.get("old"), "");
        assert_eq!(drafts.get("new"), "half a thought");
        assert_eq!(drafts.get("other"), "keep me");
        assert_eq!(drafts.get("gone"), "");
        drafts.set("older", "first");
        drafts.rekey(&map(&[("older", "new")]));
        assert_eq!(drafts.get("new"), "half a thought\n\nfirst");
        drafts.rekey(&map(&[("older", "new")]));
        assert_eq!(drafts.get("new"), "half a thought\n\nfirst");
    }

    #[test]
    fn drafts_round_trip_through_the_layout_file() {
        let mut drafts = Drafts::default();
        drafts.set("s1", "line one\nline two");
        let back = Drafts::from_json(&drafts.to_json());
        assert_eq!(back, drafts);
        assert_eq!(Drafts::from_json(&json!({"s1": 3, "s2": " ", "s3": "ok"})).get("s3"), "ok");
        assert_eq!(Drafts::from_json(&json!(null)), Drafts::default());
    }
}
