use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{LazyLock, Mutex, MutexGuard};

use serde_json::{Value, json};

use crate::catalog::Session;

pub const REMEMBERED: usize = 500;
pub const SENDS_AS_NOTE: &str = "Sends as a note";
pub const READ_ONLY: &str = "This session is closed. You can read it, but not message it.";

pub fn reply_refusal(row: &crate::subagents::RowRead) -> Option<String> {
    match row {
        crate::subagents::RowRead::Found(x) => read_only(x),
        crate::subagents::RowRead::Gone => Some("This session no longer exists.".into()),
        crate::subagents::RowRead::Failed(error) => Some(format!("Couldn't check the session is still open: {error}")),
    }
}

pub fn refused(why: String) -> crate::catalog::RespondError {
    crate::catalog::RespondError { message: why, code: "read_only".into(), maybe_delivered: false, answered: false, reply: Value::Null, status: 0 }
}

pub fn read_only(x: &Session) -> Option<String> {
    if !x.read_only && x.capabilities.send {
        return None;
    }
    Some(x.read_only_reason.clone().filter(|r| !r.trim().is_empty()).unwrap_or_else(|| READ_ONLY.to_string()))
}

pub fn turn_notice(x: &Session) -> String {
    if !x.capabilities.reaches_as_note {
        return String::new();
    }
    let mut text = String::from("What you write reaches this terminal session as a note from Agenthail, not as your own turn.");
    if x.capabilities.remote_control {
        text.push_str(" Run /remote-control in its terminal to write as yourself.");
    }
    if x.capabilities.lifecycle.adopt && !x.read_only {
        text.push_str(" Open in Agenthail also makes it your own turn.");
    }
    text
}

static CHANGES: AtomicU64 = AtomicU64::new(0);

pub fn shared() -> MutexGuard<'static, TurnNotice> {
    static SHARED: LazyLock<Mutex<TurnNotice>> = LazyLock::new(Mutex::default);
    SHARED.lock().unwrap_or_else(|e| e.into_inner())
}

pub fn changes() -> u64 {
    CHANGES.load(Ordering::Relaxed)
}

pub fn change(f: impl FnOnce(&mut TurnNotice)) {
    f(&mut shared());
    CHANGES.fetch_add(1, Ordering::Relaxed);
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TurnNotice {
    pub hidden: bool,
    pub dismissed: Vec<String>,
}

impl TurnNotice {
    pub fn shows(&self, session: &str) -> bool {
        !self.hidden && !self.dismissed.iter().any(|d| d == session)
    }

    pub fn dismiss(&mut self, session: &str) {
        self.dismissed.retain(|d| d != session);
        self.dismissed.push(session.to_string());
        let over = self.dismissed.len().saturating_sub(REMEMBERED);
        self.dismissed.drain(..over);
    }

    pub fn set_shown(&mut self, shown: bool) {
        self.hidden = !shown;
        if shown {
            self.dismissed.clear();
        }
    }

    pub fn to_json(&self) -> Value {
        json!({"hidden": self.hidden, "dismissed": self.dismissed})
    }

    pub fn from_json(value: &Value) -> TurnNotice {
        let dismissed = value["dismissed"].as_array().into_iter().flatten().filter_map(Value::as_str).map(str::to_string).collect();
        TurnNotice { hidden: value["hidden"].as_bool().unwrap_or(false), dismissed }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn session(capabilities: Value, read_only: bool) -> Session {
        crate::reply::parse(json!({"id": "s1", "capabilities": capabilities, "readOnly": read_only})).unwrap()
    }

    #[test]
    fn a_read_only_session_or_one_that_takes_no_messages_gets_the_closed_notice() {
        assert_eq!(read_only(&session(json!({"send": true}), false)), None);
        assert_eq!(read_only(&crate::reply::parse(json!({"id": "s1"})).unwrap()), None);
        assert_eq!(read_only(&session(json!({"send": true}), true)).as_deref(), Some(READ_ONLY));
        assert_eq!(read_only(&session(json!({"send": false}), false)).as_deref(), Some(READ_ONLY));
        assert_eq!(read_only(&session(json!({}), false)).as_deref(), Some(READ_ONLY));
        let why: Session = crate::reply::parse(json!({"id": "s1", "capabilities": {"send": true}, "readOnly": true, "readOnlyReason": "This Claude session is being written to from another computer."})).unwrap();
        assert_eq!(read_only(&why).as_deref(), Some("This Claude session is being written to from another computer."));
        let blank: Session = crate::reply::parse(json!({"id": "s1", "readOnly": true, "readOnlyReason": " "})).unwrap();
        assert_eq!(read_only(&blank).as_deref(), Some(READ_ONLY));
    }

    #[test]
    fn a_reply_goes_only_to_a_session_read_as_open() {
        use crate::subagents::{RowRead, detail_row};
        let closed = detail_row(&json!({"session": {"id": "s1"}, "capabilities": {"send": true}, "readOnly": true, "readOnlyReason": "Read-only here."})).unwrap();
        assert_eq!(reply_refusal(&RowRead::Found(closed)).as_deref(), Some("Read-only here."));
        let open = detail_row(&json!({"session": {"id": "s1"}, "capabilities": {"send": true}})).unwrap();
        assert_eq!(reply_refusal(&RowRead::Found(open)), None);
        let archived = detail_row(&json!({"session": {"id": "s1"}, "capabilities": {"send": true}, "archived": true})).unwrap();
        assert_eq!(reply_refusal(&RowRead::Found(archived)), None, "archiving files a session away; it can still be messaged");
        assert!(reply_refusal(&RowRead::Gone).is_some());
        assert_eq!(reply_refusal(&RowRead::Failed("timed out".into())).as_deref(), Some("Couldn't check the session is still open: timed out"));
        let error = refused("Read-only here.".into());
        assert!(!error.maybe_delivered && error.message == "Read-only here.");
    }

    #[test]
    fn only_a_session_that_takes_notes_gets_the_notice() {
        assert_eq!(turn_notice(&session(json!({"personMessage": "turn"}), false)), "");
        assert_eq!(turn_notice(&session(json!({}), false)), "");
        assert_eq!(turn_notice(&session(json!({"personMessage": null}), false)), "");
        assert_eq!(turn_notice(&session(json!({"personMessage": "note"}), false)), "What you write reaches this terminal session as a note from Agenthail, not as your own turn.");
    }

    #[test]
    fn the_notice_says_how_to_write_as_yourself() {
        let all = session(json!({"personMessage": "note", "remoteControl": true, "lifecycle": {"adopt": true}}), false);
        assert_eq!(
            turn_notice(&all),
            "What you write reaches this terminal session as a note from Agenthail, not as your own turn. Run /remote-control in its terminal to write as yourself. Open in Agenthail also makes it your own turn."
        );
        let read_only = session(json!({"personMessage": "note", "lifecycle": {"adopt": true}}), true);
        assert!(!turn_notice(&read_only).contains("Open in Agenthail"));
    }

    #[test]
    fn hiding_is_per_session_or_everywhere_and_showing_again_forgets_both() {
        let mut pref = TurnNotice::default();
        assert!(pref.shows("a"));
        pref.dismiss("a");
        assert!(!pref.shows("a") && pref.shows("b"));
        pref.dismiss("a");
        assert_eq!(pref.dismissed, vec!["a".to_string()]);
        pref.set_shown(false);
        assert!(!pref.shows("b"));
        assert_eq!(pref.dismissed, vec!["a".to_string()], "hiding everywhere keeps the per-session list");
        pref.set_shown(true);
        assert!(pref.shows("a") && pref.dismissed.is_empty());
    }

    #[test]
    fn a_change_from_any_window_is_counted_and_shared() {
        let before = changes();
        change(|n| n.set_shown(true));
        assert!(changes() > before);
        assert!(!shared().hidden);
    }

    #[test]
    fn the_newest_five_hundred_dismissals_are_kept() {
        let mut pref = TurnNotice::default();
        for i in 0..=REMEMBERED {
            pref.dismiss(&format!("s{i}"));
        }
        assert_eq!(pref.dismissed.len(), REMEMBERED);
        assert!(pref.shows("s0") && !pref.shows(&format!("s{REMEMBERED}")));
        assert_eq!(TurnNotice::from_json(&pref.to_json()), pref);
        assert_eq!(TurnNotice::from_json(&Value::Null), TurnNotice::default());
    }
}
