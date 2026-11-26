use std::collections::HashMap;

use serde::Deserialize;

use crate::catalog::QueueRow;
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
    Queued(Option<i64>),
    QueuedUntilAnswer(Option<i64>),
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
            ("queued", _) if receipt.waiting_on.as_ref().is_some_and(|w| !w.is_null()) => Delivered::QueuedUntilAnswer(receipt.queue_id),
            ("queued", _) => Delivered::Queued(receipt.queue_id),
            ("unknown" | "submitted", _) => Delivered::Unconfirmed,
            (_, Some(true)) => Delivered::NotSteered,
            (_, Some(false)) => Delivered::Steered,
            _ => Delivered::Sent,
        }
    }

    pub fn queue_id(&self) -> Option<Option<i64>> {
        match self {
            Delivered::Queued(id) | Delivered::QueuedUntilAnswer(id) => Some(*id),
            _ => None,
        }
    }

    pub fn note(&self, queued: i64) -> String {
        match self {
            Delivered::Sent => "Sent".into(),
            Delivered::Steered => "Steered".into(),
            Delivered::NotSteered => "Sent as a new message; this session didn't take it as a steer".into(),
            Delivered::Queued(_) => format!("Queued ({})", queued.max(1)),
            Delivered::QueuedUntilAnswer(_) => "Queued until you answer".into(),
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

pub fn queued_note(rows: &[QueueRow], rekeyed: &HashMap<String, String>, id: &str, receipt: &Delivered, seen_since: bool) -> Option<String> {
    let queue_id = receipt.queue_id()?;
    let pending: Vec<&QueueRow> = rows.iter().filter(|r| r.status == "pending" && current(rekeyed, &r.session_id) == id).collect();
    let ours = match queue_id {
        Some(q) => pending.iter().any(|r| r.id == q),
        None => !pending.is_empty(),
    };
    (!seen_since || ours).then(|| receipt.note(pending.len() as i64))
}

pub fn take_back(composer: &str, text: &str, restored: Option<&(String, String)>) -> Option<String> {
    if composer.trim() == text {
        return Some(String::new());
    }
    restored.filter(|(merged, _)| merged == composer).map(|(_, before)| before.clone())
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Unconfirmed(HashMap<String, Vec<Unsent>>);

impl Unconfirmed {
    pub fn latest(&self, id: &str) -> Option<&Unsent> {
        self.0.get(id).and_then(|list| list.last())
    }

    pub fn has(&self, id: &str) -> bool {
        self.latest(id).is_some()
    }

    pub fn matching(&self, id: &str, text: &str) -> Option<&Unsent> {
        self.0.get(id).and_then(|list| list.iter().rev().find(|u| u.resend(text)))
    }

    pub fn add(&mut self, id: &str, unsent: Unsent) {
        let list = self.0.entry(id.to_string()).or_default();
        list.retain(|u| u.key != unsent.key);
        list.push(unsent);
    }

    pub fn remove(&mut self, id: &str, key: &str) {
        if let Some(list) = self.0.get_mut(id) {
            list.retain(|u| u.key != key);
            if list.is_empty() {
                self.0.remove(id);
            }
        }
    }

    pub fn note(&self, id: &str) -> Option<String> {
        let list = self.0.get(id)?;
        let latest = list.last()?.note();
        Some(if list.len() > 1 { format!("{} messages not sent or not confirmed. {latest}", list.len()) } else { latest })
    }

    pub fn rekey(&mut self, rekeyed: &HashMap<String, String>) {
        let retired: Vec<String> = self.0.keys().filter(|id| current(rekeyed, id) != id.as_str()).cloned().collect();
        for old in retired {
            let new = current(rekeyed, &old).to_string();
            for unsent in self.0.remove(&old).unwrap_or_default() {
                self.add(&new, unsent);
            }
        }
    }

    pub fn iter(&self) -> impl Iterator<Item = (&String, &Unsent)> {
        self.0.iter().flat_map(|(id, list)| list.iter().map(move |u| (id, u)))
    }

    pub fn to_json(&self) -> Value {
        Value::Object(self.0.iter().map(|(id, list)| (id.clone(), Value::Array(list.iter().map(Unsent::to_json).collect()))).collect())
    }

    pub fn from_json(value: &Value) -> Unconfirmed {
        let mut out = Unconfirmed::default();
        for (id, raw) in value.as_object().into_iter().flatten() {
            let list = raw.as_array().cloned().unwrap_or_else(|| vec![raw.clone()]);
            for unsent in list.iter().filter_map(|u| Unsent::from_json(id, u)) {
                out.add(id, unsent);
            }
        }
        out
    }
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
        assert_eq!(Delivered::read(&json!({"ok": true, "result": {"evidence": "queued", "queueId": 4}})), Delivered::Queued(Some(4)));
        assert_eq!(Delivered::read(&json!({"ok": true, "result": {"evidence": "queued", "queueId": 4, "waitingOn": {"kind": "ask"}}})), Delivered::QueuedUntilAnswer(Some(4)));
        assert_eq!(Delivered::read(&json!({"ok": true, "result": {"evidence": "unknown"}})), Delivered::Unconfirmed);
        assert_eq!(Delivered::read(&json!({"ok": true, "result": {"evidence": "submitted", "status": "submitted"}})), Delivered::Unconfirmed);
        assert_eq!(Delivered::read(&json!({"ok": true, "status": "submitted"})), Delivered::Unconfirmed);
        assert_eq!(Delivered::read(&json!({"ok": true})), Delivered::Sent);
        assert_eq!(Delivered::Queued(None).note(0), "Queued (1)");
        assert_eq!(Delivered::Queued(None).note(3), "Queued (3)");
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
        assert_eq!(take_back("hello", "hello", None).as_deref(), Some(""));
        let merged = put_back("and more", "hello");
        let restored = (merged.clone(), "and more".to_string());
        assert_eq!(take_back(&merged, "hello", Some(&restored)).as_deref(), Some("and more"));
        assert_eq!(take_back("hello\n\n  and more", "hello", Some(&restored)), None);
        assert_eq!(take_back(&merged, "hello", None), None);
        assert_eq!(take_back("hello world", "hello", None), None);
    }

    fn unsent(key: &str, text: &str, maybe_delivered: bool) -> Unsent {
        Unsent { session: "s1".into(), text: text.into(), key: key.into(), busy: None, maybe_delivered, error: "timeout".into() }
    }

    #[test]
    fn each_unconfirmed_send_is_kept_until_it_is_resolved() {
        let mut held = Unconfirmed::default();
        held.add("s1", unsent("k1", "hello", true));
        held.add("s1", unsent("k2", "hello world", false));
        assert_eq!(held.latest("s1").map(|u| u.key.as_str()), Some("k2"));
        assert_eq!(held.matching("s1", "hello").map(|u| u.key.as_str()), Some("k1"));
        assert_eq!(held.matching("s1", "hello world"), None);
        assert!(held.note("s1").unwrap().starts_with("2 messages"));
        held.add("s1", unsent("k1", "hello", true));
        assert_eq!(held.iter().count(), 2);
        held.remove("s1", "k2");
        assert_eq!(held.latest("s1").map(|u| u.key.as_str()), Some("k1"));
        held.remove("s1", "k1");
        assert!(!held.has("s1"));
        assert_eq!(held, Unconfirmed::default());
    }

    #[test]
    fn unconfirmed_sends_follow_a_rekey_and_survive_a_restart() {
        let mut held = Unconfirmed::default();
        held.add("old", unsent("k1", "hello", true));
        held.add("new", unsent("k2", "again", true));
        held.rekey(&map(&[("old", "new")]));
        assert!(!held.has("old"));
        assert_eq!(held.0["new"].len(), 2);
        assert_eq!(held.matching("new", "hello").map(|u| u.session.as_str()), Some("s1"));
        assert_eq!(Unconfirmed::from_json(&held.to_json()), held);
        let single = Unconfirmed::from_json(&json!({"s9": {"text": "hi", "key": "k"}}));
        assert_eq!(single.latest("s9").map(|u| u.session.as_str()), Some("s9"));
    }

    fn row(id: i64, session: &str, status: &str) -> QueueRow {
        QueueRow { id, session_id: session.into(), status: status.into() }
    }

    #[test]
    fn a_queued_note_stays_until_a_fresh_snapshot_shows_the_row_gone() {
        let none = HashMap::new();
        let (seven, unnumbered) = (Delivered::Queued(Some(7)), Delivered::Queued(None));
        assert_eq!(queued_note(&[], &none, "s1", &seven, false).as_deref(), Some("Queued (1)"));
        let rows = [row(6, "s1", "pending"), row(7, "s1", "pending"), row(8, "s2", "pending"), row(5, "s1", "delivered")];
        assert_eq!(queued_note(&rows, &none, "s1", &seven, true).as_deref(), Some("Queued (2)"));
        assert_eq!(queued_note(&rows[..1], &none, "s1", &seven, true), None);
        assert_eq!(queued_note(&rows[..1], &none, "s1", &unnumbered, true).as_deref(), Some("Queued (1)"));
        assert_eq!(queued_note(&rows[2..], &none, "s1", &unnumbered, true), None);
        let moved = [row(7, "old", "pending")];
        assert_eq!(queued_note(&moved, &map(&[("old", "s1")]), "s1", &seven, true).as_deref(), Some("Queued (1)"));
        let answer = Delivered::QueuedUntilAnswer(Some(7));
        assert_eq!(queued_note(&rows, &none, "s1", &answer, true).as_deref(), Some("Queued until you answer"));
        assert_eq!(queued_note(&rows[..1], &none, "s1", &answer, true), None);
        assert_eq!(queued_note(&rows, &none, "s1", &Delivered::Steered, false), None);
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
