use std::collections::HashMap;
use std::sync::{LazyLock, Mutex};

use serde::Deserialize;
use serde_json::Value;

use crate::catalog::Session;

#[derive(Deserialize, Clone, Debug, Default, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct Holder {
    pub pid: i64,
    #[serde(default)]
    pub tty: Option<String>,
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub started_at: Option<String>,
}

impl Holder {
    pub fn label(&self) -> String {
        [Some(format!("pid {}", self.pid)), self.tty.clone(), Some(self.status.clone())].into_iter().flatten().filter(|s| !s.is_empty()).collect::<Vec<_>>().join(", ")
    }

    pub fn started_at_ms(&self) -> Option<i64> {
        let at = crate::catalog::parse_time(self.started_at.as_deref()?)?;
        i64::try_from(at.unix_timestamp_nanos().div_euclid(1_000_000)).ok()
    }

    pub fn pin(&self) -> Pin {
        Pin { pid: self.pid, started_at: self.started_at_ms() }
    }
}

impl Session {
    pub fn shared_holders(&self) -> &[Holder] {
        match &self.holders {
            Some(holders) if self.surface == "claude" && holders.len() > 1 => holders,
            _ => &[],
        }
    }
}

pub fn summary(holders: &[Holder]) -> String {
    format!("Open in {} terminals: {}", holders.len(), holders.iter().map(Holder::label).collect::<Vec<_>>().join("; "))
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct Pin {
    pub pid: i64,
    pub started_at: Option<i64>,
}

impl Pin {
    pub fn key(&self, key: &str) -> String {
        format!("{key}-pid{}-{}", self.pid, self.started_at.unwrap_or(0))
    }

    pub fn apply(&self, fields: &mut serde_json::Map<String, Value>) {
        fields.insert("pid".into(), self.pid.into());
        if let Some(at) = self.started_at {
            fields.insert("pidStartedAt".into(), at.into());
        }
    }
}

const UNPINNED: [&str; 3] = ["session-create", "notion-create", "queue-cancel"];
const UNPINNED_PREFIXES: [&str; 3] = ["channel-", "relay-", "project-"];

pub fn pinnable(action: &str) -> bool {
    !UNPINNED.contains(&action) && !UNPINNED_PREFIXES.iter().any(|p| action.starts_with(p))
}

// A pick lasts only while its conversation still lists that process among several holders.
#[derive(Debug, Default)]
pub struct Pins(Mutex<HashMap<String, Pin>>);

pub fn pins() -> &'static Pins {
    static SHARED: LazyLock<Pins> = LazyLock::new(Pins::default);
    &SHARED
}

impl Pins {
    fn map(&self) -> std::sync::MutexGuard<'_, HashMap<String, Pin>> {
        self.0.lock().unwrap_or_else(|e| e.into_inner())
    }

    pub fn get(&self, session: &str) -> Option<Pin> {
        self.map().get(session).copied()
    }

    pub fn pick(&self, session: &str, pin: Option<Pin>) {
        let mut map = self.map();
        match pin {
            Some(pin) => map.insert(session.to_string(), pin),
            None => map.remove(session),
        };
    }

    pub fn prune(&self, sessions: &[Session]) {
        let shared: HashMap<&str, &[Holder]> = sessions.iter().map(|x| (x.id.as_str(), x.shared_holders())).collect();
        self.map().retain(|id, pin| shared.get(id.as_str()).is_some_and(|holders| holders.iter().any(|h| h.pin() == *pin)));
    }

    pub fn picked<'a>(&self, x: &'a Session) -> Option<&'a Holder> {
        let pin = self.get(&x.id)?;
        x.shared_holders().iter().find(|h| h.pin() == pin)
    }

    pub fn pinned(&self, name: &str, fields: &mut serde_json::Map<String, Value>, key: &str) -> String {
        let pin = fields.get("sessionId").and_then(Value::as_str).filter(|_| pinnable(name)).and_then(|id| self.get(id));
        match pin {
            Some(pin) => {
                pin.apply(fields);
                pin.key(key)
            }
            None => key.to_string(),
        }
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;

    fn session(id: &str, surface: &str, holders: Value) -> Session {
        serde_json::from_value(json!({"id": id, "surface": surface, "holders": holders})).unwrap()
    }

    fn two() -> Value {
        json!([{"pid": 41, "tty": "ttys003", "status": "busy", "startedAt": "2026-11-26T10:00:00.250Z"}, {"pid": 42, "status": "idle"}])
    }

    #[test]
    fn only_a_claude_conversation_in_several_terminals_shares_holders() {
        assert_eq!(session("a", "claude", two()).shared_holders().len(), 2);
        assert!(session("a", "codex", two()).shared_holders().is_empty());
        assert!(session("a", "claude", json!([{"pid": 41, "status": "busy"}])).shared_holders().is_empty());
        assert!(session("a", "claude", Value::Null).shared_holders().is_empty());
    }

    #[test]
    fn a_holder_reads_like_the_mac_apps() {
        let x = session("a", "claude", two());
        assert_eq!(x.shared_holders()[0].label(), "pid 41, ttys003, busy");
        assert_eq!(x.shared_holders()[1].label(), "pid 42, idle");
        assert_eq!(summary(x.shared_holders()), "Open in 2 terminals: pid 41, ttys003, busy; pid 42, idle");
        assert_eq!(x.shared_holders()[0].started_at_ms(), Some(1_795_687_200_250));
    }

    #[test]
    fn a_pick_pins_session_writes_and_changes_their_key() {
        let pins = Pins::default();
        let x = session("a", "claude", two());
        pins.pick("a", Some(x.shared_holders()[0].pin()));
        let mut fields = json!({"sessionId": "a", "message": "hi"}).as_object().unwrap().clone();
        let key = pins.pinned("send", &mut fields, "k1");
        assert_eq!(key, "k1-pid41-1795687200250");
        assert_eq!(fields["pid"], 41);
        assert_eq!(fields["pidStartedAt"], 1_795_687_200_250i64);
        assert_eq!(pins.picked(&x).map(|h| h.pid), Some(41));
    }

    #[test]
    fn writes_that_name_a_session_without_writing_to_it_stay_unpinned() {
        let pins = Pins::default();
        pins.pick("a", Some(Pin { pid: 42, started_at: None }));
        for action in ["session-create", "notion-create", "queue-cancel", "channel-send", "relay-subscribe", "project-setting"] {
            let mut fields = json!({"sessionId": "a"}).as_object().unwrap().clone();
            assert_eq!(pins.pinned(action, &mut fields, "k"), "k", "{action}");
            assert!(!fields.contains_key("pid"), "{action}");
        }
        let mut other = json!({"sessionId": "b"}).as_object().unwrap().clone();
        assert_eq!(pins.pinned("send", &mut other, "k"), "k");
        let mut no_start = json!({"sessionId": "a"}).as_object().unwrap().clone();
        assert_eq!(pins.pinned("interrupt", &mut no_start, "k"), "k-pid42-0");
        assert!(!no_start.contains_key("pidStartedAt"));
    }

    #[test]
    fn a_pick_goes_once_its_process_is_no_longer_one_of_several() {
        let pins = Pins::default();
        let x = session("a", "claude", two());
        pins.pick("a", Some(x.shared_holders()[1].pin()));
        pins.prune(&[x.clone()]);
        assert!(pins.get("a").is_some());
        pins.prune(&[session("a", "claude", json!([{"pid": 42, "status": "idle"}]))]);
        assert!(pins.get("a").is_none());
        pins.pick("a", Some(x.shared_holders()[1].pin()));
        pins.prune(&[session("a", "claude", json!([{"pid": 41, "status": "busy"}, {"pid": 42, "status": "idle", "startedAt": "2026-11-26T11:00:00Z"}]))]);
        assert!(pins.get("a").is_none(), "a reused pid is a different process");
        pins.pick("a", Some(x.shared_holders()[1].pin()));
        pins.pick("a", None);
        assert!(pins.get("a").is_none());
    }
}
