use std::time::Duration;

use serde::Deserialize;
use serde_json::{Value, json};

use crate::catalog::{Client, RespondError, Session};

const WAIT: Duration = Duration::from_secs(31 * 60);

pub const ABOUT: &str = "It stops here and continues on the computer you pick, in the same project. Messages sent during the move wait and follow it.";

#[derive(Deserialize, Clone, Debug, Default, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct Move {
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub host: String,
    #[serde(default)]
    pub host_name: Option<String>,
    #[serde(default)]
    pub root: Option<String>,
    #[serde(default)]
    pub detail: Option<String>,
}

impl Move {
    fn target(&self) -> String {
        if let Some(root) = self.root.as_deref().filter(|r| !r.is_empty()) {
            return format!("account {root}");
        }
        self.host_name.clone().filter(|n| !n.is_empty()).unwrap_or_else(|| self.host.clone())
    }

    pub fn label(&self) -> String {
        let target = self.target();
        match self.state.as_str() {
            "holding" => format!("Moving to {target}: holding"),
            "copying" => format!("Moving to {target}: copying"),
            "settling" => format!("Move to {target} unsettled"),
            _ => format!("Moving to {target}"),
        }
    }
}

impl Session {
    /// Only this computer's daemon moves a session, and only a top-level Claude or Codex conversation.
    pub fn movable(&self) -> bool {
        self.local() && matches!(self.surface.as_str(), "claude" | "codex") && self.subagent.as_ref().is_none_or(Value::is_null) && !self.read_only && self.moving.is_none()
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Moved {
    pub host_name: String,
    pub stopped: Option<String>,
    pub mcp_servers: Vec<String>,
    pub dropped_relays: Vec<String>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Outcome {
    Moved(Moved),
    Unsettled,
    Busy(String),
    NeedsConsent,
    Refused(String),
    Lost,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Notice {
    pub session: String,
    pub host: String,
    pub host_name: String,
    pub title: String,
    pub message: String,
    pub retry: bool,
}

fn moved(reply: &Value, host_name: &str) -> Moved {
    let result = &reply["result"];
    let strings = |v: &Value| v.as_array().into_iter().flatten().filter_map(|s| s.as_str().map(str::to_string)).collect::<Vec<_>>();
    Moved {
        host_name: result["host"]["name"].as_str().filter(|n| !n.is_empty()).unwrap_or(host_name).to_string(),
        stopped: result["stopped"].as_str().filter(|s| !s.is_empty()).map(str::to_string),
        mcp_servers: strings(&result["mcpServersWithoutCredentials"]),
        dropped_relays: result["droppedRelays"]
            .as_array()
            .into_iter()
            .flatten()
            .map(|r| format!("Relay #{} was not taken over: session {}, which it sends to, left this computer's view during the move.", r["relayId"].as_i64().unwrap_or_default(), r["toSession"].as_str().unwrap_or_default()))
            .collect(),
    }
}

impl Outcome {
    fn of(result: Result<Value, RespondError>, host_name: &str) -> Outcome {
        let error = match result {
            Ok(reply) => return Outcome::Moved(moved(&reply, host_name)),
            Err(error) => error,
        };
        match error.code.as_str() {
            "move_outcome_unknown" => Outcome::Unsettled,
            "move_busy" => Outcome::Busy(error.reply["busy"].as_str().filter(|b| !b.is_empty()).map(str::to_string).unwrap_or(error.message)),
            "move_needs_consent" => Outcome::NeedsConsent,
            "move_refused" | "move_failed" => Outcome::Refused(error.message),
            _ if error.maybe_delivered => Outcome::Lost,
            _ => Outcome::Refused(error.message),
        }
    }

    pub fn notice(&self, session: &str, title: &str, host: &str, host_name: &str, here: &str) -> Notice {
        let (heading, message, retry) = match self {
            Outcome::Moved(m) => {
                let mut lines = Vec::new();
                if let Some(stopped) = &m.stopped {
                    lines.push(if stopped == "host" { "Stopped the Claude process Agenthail ran for it.".to_string() } else { format!("Stopped the {stopped} that held it.") });
                }
                if !m.mcp_servers.is_empty() {
                    let mut block = vec![format!("These MCP servers moved without their environment and headers. Add their credentials again on {}:", m.host_name)];
                    block.extend(m.mcp_servers.iter().cloned());
                    lines.push(block.join("\n"));
                }
                lines.extend(m.dropped_relays.iter().cloned());
                (format!("Moved to {}", m.host_name), lines.join("\n\n"), false)
            }
            Outcome::Unsettled => (format!("The move to {host_name} hasn't settled"), format!("No computer writes to this conversation until {host_name} answers. It settles when {host_name} reconnects."), false),
            Outcome::Busy(reason) => (format!("Couldn't move {title}"), format!("{title} is busy ({reason}). Move it once it's idle."), false),
            Outcome::NeedsConsent => ("Stop the terminal and move?".to_string(), format!("A terminal is running {title}. Moving it stops that terminal."), true),
            Outcome::Refused(message) => (format!("Couldn't move {title}"), format!("{message}\n\nIt stays on {here}."), false),
            Outcome::Lost => ("Lost the connection during the move".to_string(), format!("This app can't tell whether the move to {host_name} went through. The session's row shows how it ends."), false),
        };
        Notice { session: session.to_string(), host: host.to_string(), host_name: host_name.to_string(), title: heading, message, retry }
    }
}

impl Client {
    pub fn move_session(&self, session: &str, host: &str, host_name: &str, stop_terminal: bool) -> Outcome {
        let mut body = json!({"sessionId": session, "host": host});
        if stop_terminal {
            body["stopTerminal"] = json!(true);
        }
        let result = self.timed(WAIT).post_json("/api/v1/session-moves", body).and_then(|reply| {
            if reply["error"].is_object() {
                Err(RespondError::from_status("session-moves", 202, &reply.to_string()))
            } else {
                Ok(reply)
            }
        });
        Outcome::of(result, host_name)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::Named;
    use crate::mock::{daemon, replies};

    fn session() -> Session {
        Session { id: "s1".into(), surface: "claude".into(), host: Named { name: "mac".into(), state: "local".into(), ..Default::default() }, ..Default::default() }
    }

    #[test]
    fn a_top_level_claude_or_codex_session_here_can_move() {
        let mut x = session();
        assert!(x.movable());
        x.surface = "codex".into();
        assert!(x.movable());
        x.surface = "notion".into();
        assert!(!x.movable());
        let mut x = session();
        x.host = Named { name: "forge".into(), state: "connected".into(), ..Default::default() };
        assert!(!x.movable(), "the other computer's daemon owns it");
        let mut x = session();
        x.subagent = Some(json!({"parent": "p"}));
        assert!(!x.movable());
        let mut x = session();
        x.read_only = true;
        assert!(!x.movable());
        let mut x = session();
        x.moving = Some(Move { state: "copying".into(), host: "h1".into(), ..Move::default() });
        assert!(!x.movable(), "one move at a time");
        let x: Session = serde_json::from_value(json!({"id": "s1", "surface": "claude", "subagent": null, "move": {"state": "holding", "host": "h1", "hostName": "forge"}})).unwrap();
        assert_eq!(x.moving.unwrap().label(), "Moving to forge: holding");
    }

    #[test]
    fn the_row_names_where_the_move_is_going_and_how_far_it_got() {
        let m = |state: &str, name: Option<&str>, root: Option<&str>| Move { state: state.into(), host: "h1".into(), host_name: name.map(str::to_string), root: root.map(str::to_string), detail: None };
        assert_eq!(m("copying", Some("forge"), None).label(), "Moving to forge: copying");
        assert_eq!(m("settling", Some("forge"), None).label(), "Move to forge unsettled");
        assert_eq!(m("started", None, None).label(), "Moving to h1");
        assert_eq!(m("holding", Some("forge"), Some("work")).label(), "Moving to account work: holding");
    }

    #[test]
    fn each_answer_from_the_daemon_becomes_an_outcome() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"ok":true,"result":{"host":{"id":"h1","name":"forge"},"stopped":"terminal","mcpServersWithoutCredentials":["linear"],"droppedRelays":[{"relayId":7,"toSession":"s9"}]}}"#),
            (202, r#"{"error":{"code":"move_outcome_unknown","message":"unresolved"},"result":{}}"#),
            (409, r#"{"error":{"code":"move_busy","message":"busy"},"busy":"a turn is running"}"#),
            (409, r#"{"error":{"code":"move_needs_consent","message":"terminal"}}"#),
            (409, r#"{"error":{"code":"move_refused","message":"forge is on an older version"}}"#),
            (502, r#"{"error":{"code":"move_failed","message":"copy failed"}}"#),
        ]));
        let Outcome::Moved(m) = client.move_session("s1", "h1", "forge", false) else { panic!() };
        assert_eq!((m.host_name.as_str(), m.stopped.as_deref(), m.mcp_servers.clone()), ("forge", Some("terminal"), vec!["linear".to_string()]));
        assert_eq!(m.dropped_relays, vec!["Relay #7 was not taken over: session s9, which it sends to, left this computer's view during the move."]);
        assert_eq!(seen.recv().unwrap(), ("/api/v1/session-moves".to_string(), json!({"sessionId": "s1", "host": "h1"})));
        assert_eq!(client.move_session("s1", "h1", "forge", true), Outcome::Unsettled);
        assert_eq!(seen.recv().unwrap().1, json!({"sessionId": "s1", "host": "h1", "stopTerminal": true}));
        assert_eq!(client.move_session("s1", "h1", "forge", false), Outcome::Busy("a turn is running".into()));
        assert_eq!(client.move_session("s1", "h1", "forge", false), Outcome::NeedsConsent);
        assert_eq!(client.move_session("s1", "h1", "forge", false), Outcome::Refused("forge is on an older version".into()));
        assert_eq!(client.move_session("s1", "h1", "forge", false), Outcome::Refused("copy failed".into()));
    }

    #[test]
    fn a_dropped_connection_mid_move_is_lost_but_an_unsent_one_is_refused() {
        let lost = RespondError { message: "timed out".into(), code: String::new(), maybe_delivered: true, reply: Value::Null, answered: false, status: 0 };
        assert_eq!(Outcome::of(Err(lost), "forge"), Outcome::Lost);
        let unsent = RespondError { message: "connection refused".into(), code: String::new(), maybe_delivered: false, reply: Value::Null, answered: false, status: 0 };
        assert_eq!(Outcome::of(Err(unsent), "forge"), Outcome::Refused("connection refused".into()));
        let gateway = RespondError::from_status("session-moves", 504, "upstream timed out");
        assert_eq!(Outcome::of(Err(gateway), "forge"), Outcome::Lost, "the daemon keeps moving after the connection drops");
        let refused = RespondError::from_status("session-moves", 400, r#"{"error":{"code":"invalid_request","message":"sessionId and a host or root are required"}}"#);
        assert_eq!(Outcome::of(Err(refused), "forge"), Outcome::Refused("sessionId and a host or root are required".into()));
    }

    #[test]
    fn notices_read_like_the_mac_app() {
        let moved = Outcome::Moved(Moved { host_name: "forge".into(), stopped: Some("host".into()), mcp_servers: vec!["linear".into(), "notion".into()], dropped_relays: vec![] });
        let n = moved.notice("s1", "@scout", "h1", "forge", "mac");
        assert_eq!(n.title, "Moved to forge");
        assert_eq!(n.message, "Stopped the Claude process Agenthail ran for it.\n\nThese MCP servers moved without their environment and headers. Add their credentials again on forge:\nlinear\nnotion");
        assert!(!n.retry);
        let n = Outcome::NeedsConsent.notice("s1", "@scout", "h1", "forge", "mac");
        assert_eq!((n.title.as_str(), n.message.as_str(), n.retry, n.host.as_str()), ("Stop the terminal and move?", "A terminal is running @scout. Moving it stops that terminal.", true, "h1"));
        assert_eq!(Outcome::Refused("no room".into()).notice("s1", "@scout", "h1", "forge", "mac").message, "no room\n\nIt stays on mac.");
        assert_eq!(Outcome::Busy("a turn is running".into()).notice("s1", "@scout", "h1", "forge", "mac").message, "@scout is busy (a turn is running). Move it once it's idle.");
        assert_eq!(Outcome::Unsettled.notice("s1", "@scout", "h1", "forge", "mac").title, "The move to forge hasn't settled");
        let lost = Outcome::Lost.notice("s1", "@scout", "h1", "forge", "mac");
        assert_eq!((lost.title.as_str(), lost.message.as_str()), ("Lost the connection during the move", "This app can't tell whether the move to forge went through. The session's row shows how it ends."));
    }
}
