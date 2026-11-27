use std::time::Duration;

use serde_json::{Value, json};

use crate::catalog::{Client, RespondError, Session};

const WAIT: Duration = Duration::from_secs(150);

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Adopted {
    Opened { summary: String, session: Option<String> },
    NeedsConsent(String),
    Busy(String),
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Phase {
    Ready,
    Adopting,
    Confirm(String),
    Done(String),
    Failed(String),
}

pub const ABOUT: &str = "Agenthail stops the process that runs the conversation once it is idle and continues it in its own host, on the same Claude account.";

impl Session {
    pub fn adoptable(&self) -> bool {
        self.capabilities.lifecycle.adopt && !self.read_only
    }
}

impl Phase {
    pub fn after(result: Result<Adopted, RespondError>) -> Phase {
        match result {
            Ok(Adopted::Opened { summary, .. }) => Phase::Done(summary),
            Ok(Adopted::NeedsConsent(message)) => Phase::Confirm(format!("{message} Stopping it ends the terminal session; the conversation continues in Agenthail.")),
            Ok(Adopted::Busy(reason)) => Phase::Failed(format!("The session is busy: {reason}. Open it in Agenthail when it is idle.")),
            Err(error) if error.maybe_delivered => Phase::Failed(format!("Opening it may not have gone through: {}", error.message)),
            Err(error) => Phase::Failed(error.message),
        }
    }
}

fn summary(result: &Value) -> String {
    if result["unchanged"].as_bool() == Some(true) {
        return "Already runs in Agenthail.".into();
    }
    match result["stopped"].as_str().filter(|s| !s.is_empty()) {
        Some(stopped) => format!("Opened in Agenthail. Stopped the {stopped} that held it."),
        None => "Opened in Agenthail.".into(),
    }
}

impl Client {
    pub fn session_adopt(&self, session: &str, stop_terminal: bool) -> Result<Adopted, RespondError> {
        match self.timed(WAIT).action("session-adopt", json!({"sessionId": session, "stopTerminal": stop_terminal})).and_then(crate::catalog::confirmed) {
            Ok(reply) => {
                let result = &reply["result"];
                let session = result["session"]["id"].as_str().filter(|id| !id.is_empty()).map(str::to_string);
                Ok(Adopted::Opened { summary: summary(result), session })
            }
            Err(error) if error.code == "session_adopt_needs_consent" => Ok(Adopted::NeedsConsent(error.message)),
            Err(error) if error.code == "session_adopt_unrecorded" => Ok(Adopted::Opened { summary: format!("Opened in Agenthail, but Agenthail couldn't record it: {}", error.message), session: None }),
            Err(error) if error.code == "session_adopt_busy" => Ok(Adopted::Busy(error.reply["busy"].as_str().filter(|b| !b.is_empty()).map(str::to_string).unwrap_or(error.message))),
            Err(mut error) => {
                error.maybe_delivered |= error.code == "session_adopt_unknown";
                Err(error)
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::{Capabilities, Lifecycle};
    use crate::mock::{daemon, replies};

    #[test]
    fn only_a_session_the_daemon_can_open_is_offered() {
        let mut x = Session::default();
        assert!(!x.adoptable());
        x.capabilities = Capabilities { lifecycle: Lifecycle { adopt: true, ..Lifecycle::default() }, ..Capabilities::default() };
        assert!(x.adoptable());
        x.read_only = true;
        assert!(!x.adoptable());
    }

    #[test]
    fn the_snapshot_says_which_sessions_can_be_opened() {
        let x: Session = serde_json::from_value(json!({"id": "s1", "capabilities": {"lifecycle": {"adopt": true, "resume": true}}})).unwrap();
        assert!(x.adoptable() && x.capabilities.lifecycle.resume);
    }

    #[test]
    fn opening_reads_each_answer_the_daemon_gives() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"ok":true,"result":{"session":{"id":"s2"},"stopped":"terminal process"}}"#),
            (409, r#"{"error":{"code":"session_adopt_needs_consent","message":"This session is open in a terminal."}}"#),
            (409, r#"{"error":{"code":"session_adopt_busy","message":"busy: turn running"},"busy":"a turn is running"}"#),
            (502, r#"{"error":{"code":"session_adopt_unknown","message":"timed out"}}"#),
            (409, r#"{"error":{"code":"session_adopt_refused","message":"the process ended"}}"#),
            (200, r#"{"ok":true,"result":{"session":{"id":"s1"},"unchanged":true}}"#),
            (202, r#"{"ok":true,"status":"submitted"}"#),
            (500, r#"{"error":{"code":"session_adopt_unrecorded","message":"session s1 opened in Agenthail, but its registration failed: disk full"}}"#),
        ]));
        assert_eq!(client.session_adopt("s1", false).unwrap(), Adopted::Opened { summary: "Opened in Agenthail. Stopped the terminal process that held it.".into(), session: Some("s2".into()) });
        assert_eq!(seen.recv().unwrap().1, json!({"action": "session-adopt", "sessionId": "s1", "stopTerminal": false}));
        assert_eq!(client.session_adopt("s1", true).unwrap(), Adopted::NeedsConsent("This session is open in a terminal.".into()));
        assert_eq!(seen.recv().unwrap().1["stopTerminal"], json!(true));
        assert_eq!(client.session_adopt("s1", false).unwrap(), Adopted::Busy("a turn is running".into()));
        let unknown = client.session_adopt("s1", false).unwrap_err();
        assert!(unknown.maybe_delivered);
        let refused = client.session_adopt("s1", false).unwrap_err();
        assert!(!refused.maybe_delivered);
        assert_eq!(Phase::after(Err(refused)), Phase::Failed("the process ended".into()));
        assert_eq!(client.session_adopt("s1", false).unwrap(), Adopted::Opened { summary: "Already runs in Agenthail.".into(), session: Some("s1".into()) });
        let submitted = client.session_adopt("s1", false).unwrap_err();
        assert!(submitted.maybe_delivered);
        assert!(matches!(Phase::after(Err(submitted)), Phase::Failed(m) if m.starts_with("Opening it may not have gone through")));
        assert_eq!(client.session_adopt("s1", false).unwrap(), Adopted::Opened { summary: "Opened in Agenthail, but Agenthail couldn't record it: session s1 opened in Agenthail, but its registration failed: disk full".into(), session: None });
    }

    #[test]
    fn each_answer_reads_like_the_mac_sheet() {
        assert_eq!(Phase::after(Ok(Adopted::NeedsConsent("It runs in a terminal.".into()))), Phase::Confirm("It runs in a terminal. Stopping it ends the terminal session; the conversation continues in Agenthail.".into()));
        assert_eq!(Phase::after(Ok(Adopted::Busy("a turn is running".into()))), Phase::Failed("The session is busy: a turn is running. Open it in Agenthail when it is idle.".into()));
        assert_eq!(Phase::after(Ok(Adopted::Opened { summary: "Opened in Agenthail.".into(), session: None })), Phase::Done("Opened in Agenthail.".into()));
    }
}
