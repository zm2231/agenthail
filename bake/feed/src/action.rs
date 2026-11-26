use serde::Deserialize;
use serde_json::{Value, json};

use crate::catalog::{Client, RespondError};
use crate::composer::{Busy, Delivered};

type Outcome<T> = std::result::Result<T, RespondError>;

#[derive(Clone, Debug, PartialEq)]
pub enum Dismiss<'a> {
    Stopped { session: &'a str },
    Request { session: &'a str, request: &'a str },
    /// `host` names the computer whose ledger reported the item; "" is this one.
    Parked { work_item: &'a str, host: &'a str },
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ReadState {
    #[serde(default)]
    pub first_unread: String,
    #[serde(default)]
    pub revision: String,
}

impl Client {
    pub fn interrupt(&self, session: &str) -> Outcome<()> {
        self.action("interrupt", json!({"sessionId": session})).map(drop)
    }

    /// `busy` None leaves a busy session to the daemon's busy-delivery setting.
    pub fn send_as(&self, session: &str, message: &str, busy: Option<Busy>, key: &str) -> Outcome<Delivered> {
        let mut payload = json!({"sessionId": session, "message": message, "author": "person"});
        if let Some(busy) = busy {
            payload["busyDelivery"] = Value::String(busy.as_str().into());
        }
        Ok(Delivered::read(&self.action_keyed("send", payload, key)?))
    }

    pub fn steer(&self, session: &str, message: &str) -> Outcome<()> {
        self.action("steer", json!({"sessionId": session, "message": message, "author": "person"})).map(drop)
    }

    /// `host` names the computer that holds the queue row; "" is this one.
    pub fn queue_cancel(&self, queue_id: i64, host: &str) -> Outcome<String> {
        let reply = self.action("queue-cancel", json!({"queueId": queue_id, "host": host}))?;
        Ok(reply["result"]["message"].as_str().unwrap_or_default().to_string())
    }

    pub fn session_archive(&self, session: &str, archived: bool) -> Outcome<()> {
        let name = if archived { "session-archive" } else { "session-unarchive" };
        self.action(name, json!({"sessionId": session})).map(drop)
    }

    pub fn session_pin(&self, session: &str, pinned: bool) -> Outcome<()> {
        let name = if pinned { "session-pin" } else { "session-unpin" };
        self.action(name, json!({"sessionId": session})).map(drop)
    }

    pub fn alias(&self, session: &str, name: &str) -> Outcome<String> {
        let reply = self.action("alias", json!({"sessionId": session, "alias": name}))?;
        Ok(reply["alias"].as_str().unwrap_or_default().to_string())
    }

    pub fn session_read(&self, session: &str, revision: &str, read: bool) -> Outcome<Option<ReadState>> {
        let name = if read { "session-read" } else { "session-unread" };
        let reply = self.action(name, json!({"sessionId": session, "revision": revision}))?;
        Ok(serde_json::from_value(reply["readState"].clone()).ok())
    }

    pub fn turn_setting(&self, session: &str, setting: &str, value: &str) -> Outcome<()> {
        self.action("turn-setting", json!({"sessionId": session, "setting": setting, "value": value})).map(drop)
    }

    pub fn compact(&self, session: &str) -> Outcome<()> {
        self.action("compact", json!({"sessionId": session})).map(drop)
    }

    pub fn session_focus(&self, session: &str) -> Outcome<()> {
        self.action("session-focus", json!({"sessionId": session})).map(drop)
    }

    pub fn dismiss_decision(&self, target: Dismiss) -> Outcome<()> {
        let payload = match target {
            Dismiss::Stopped { session } => json!({"sessionId": session}),
            Dismiss::Request { session, request } => json!({"sessionId": session, "requestId": request}),
            Dismiss::Parked { work_item, host } => json!({"workItem": work_item, "host": host}),
        };
        self.action("dismiss-decision", payload).map(drop)
    }

    pub fn limit_resume(&self, session: &str, resume: bool) -> Outcome<()> {
        let name = if resume { "limit-resume" } else { "limit-resume-cancel" };
        self.action(name, json!({"sessionId": session})).map(drop)
    }

    /// `None` takes the account the daemon offers.
    pub fn limit_move(&self, session: &str, config_root: Option<&str>, stop_terminal: bool) -> Outcome<Value> {
        let mut payload = json!({"sessionId": session, "stopTerminal": stop_terminal});
        if let Some(root) = config_root {
            payload["configRoot"] = Value::String(root.into());
        }
        self.action("limit-move", payload)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{BufRead, BufReader, Read, Write};
    use std::net::TcpListener;
    use std::sync::mpsc;

    struct Seen {
        path: String,
        key: String,
        auth: String,
        body: Value,
    }

    fn daemon(replies: Vec<(u16, &'static str)>) -> (Client, mpsc::Receiver<Seen>) {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let (tx, rx) = mpsc::channel();
        std::thread::spawn(move || {
            for (status, body) in replies {
                let (stream, _) = listener.accept().unwrap();
                let mut reader = BufReader::new(stream.try_clone().unwrap());
                let mut line = String::new();
                reader.read_line(&mut line).unwrap();
                let path = line.split_whitespace().nth(1).unwrap_or_default().to_string();
                let (mut key, mut auth, mut length) = (String::new(), String::new(), 0usize);
                loop {
                    let mut header = String::new();
                    reader.read_line(&mut header).unwrap();
                    let header = header.trim_end();
                    if header.is_empty() {
                        break;
                    }
                    let (name, value) = header.split_once(':').unwrap();
                    match name.to_ascii_lowercase().as_str() {
                        "idempotency-key" => key = value.trim().into(),
                        "authorization" => auth = value.trim().into(),
                        "content-length" => length = value.trim().parse().unwrap(),
                        _ => {}
                    }
                }
                let mut raw = vec![0; length];
                reader.read_exact(&mut raw).unwrap();
                tx.send(Seen { path, key, auth, body: serde_json::from_slice(&raw).unwrap() }).unwrap();
                let mut stream = stream;
                write!(stream, "HTTP/1.1 {status} X\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len()).unwrap();
            }
        });
        (Client::new(&base, "secret"), rx)
    }

    #[test]
    fn each_action_sends_the_body_its_handler_reads() {
        type Call = Box<dyn Fn(&Client) -> Outcome<()>>;
        let calls: Vec<(Call, Value)> = vec![
            (Box::new(|c| c.interrupt("s1")), json!({"action": "interrupt", "sessionId": "s1"})),
            (Box::new(|c| c.steer("s1", "stop")), json!({"action": "steer", "sessionId": "s1", "message": "stop", "author": "person"})),
            (Box::new(|c| c.queue_cancel(7, "").map(drop)), json!({"action": "queue-cancel", "queueId": 7, "host": ""})),
            (Box::new(|c| c.queue_cancel(7, "mac").map(drop)), json!({"action": "queue-cancel", "queueId": 7, "host": "mac"})),
            (Box::new(|c| c.session_archive("s1", true)), json!({"action": "session-archive", "sessionId": "s1"})),
            (Box::new(|c| c.session_archive("s1", false)), json!({"action": "session-unarchive", "sessionId": "s1"})),
            (Box::new(|c| c.session_pin("s1", true)), json!({"action": "session-pin", "sessionId": "s1"})),
            (Box::new(|c| c.session_pin("s1", false)), json!({"action": "session-unpin", "sessionId": "s1"})),
            (Box::new(|c| c.alias("s1", "scout").map(drop)), json!({"action": "alias", "sessionId": "s1", "alias": "scout"})),
            (Box::new(|c| c.session_read("s1", "r9", true).map(drop)), json!({"action": "session-read", "sessionId": "s1", "revision": "r9"})),
            (Box::new(|c| c.session_read("s1", "r9", false).map(drop)), json!({"action": "session-unread", "sessionId": "s1", "revision": "r9"})),
            (Box::new(|c| c.turn_setting("s1", "effort", "high")), json!({"action": "turn-setting", "sessionId": "s1", "setting": "effort", "value": "high"})),
            (Box::new(|c| c.compact("s1")), json!({"action": "compact", "sessionId": "s1"})),
            (Box::new(|c| c.session_focus("s1")), json!({"action": "session-focus", "sessionId": "s1"})),
            (Box::new(|c| c.dismiss_decision(Dismiss::Stopped { session: "s1" })), json!({"action": "dismiss-decision", "sessionId": "s1"})),
            (Box::new(|c| c.dismiss_decision(Dismiss::Request { session: "s1", request: "ask-1" })), json!({"action": "dismiss-decision", "sessionId": "s1", "requestId": "ask-1"})),
            (Box::new(|c| c.dismiss_decision(Dismiss::Parked { work_item: "w-1", host: "" })), json!({"action": "dismiss-decision", "workItem": "w-1", "host": ""})),
            (Box::new(|c| c.limit_resume("s1", true)), json!({"action": "limit-resume", "sessionId": "s1"})),
            (Box::new(|c| c.limit_resume("s1", false)), json!({"action": "limit-resume-cancel", "sessionId": "s1"})),
            (Box::new(|c| c.limit_move("s1", None, false).map(drop)), json!({"action": "limit-move", "sessionId": "s1", "stopTerminal": false})),
            (Box::new(|c| c.limit_move("s1", Some("/home/a/.claude-b"), true).map(drop)), json!({"action": "limit-move", "sessionId": "s1", "configRoot": "/home/a/.claude-b", "stopTerminal": true})),
            (Box::new(|c| c.send("s1", "hi")), json!({"action": "send", "sessionId": "s1", "message": "hi", "author": "person"})),
            (Box::new(|c| c.send_as("s1", "hi", None, &crate::catalog::request_key()).map(drop)), json!({"action": "send", "sessionId": "s1", "message": "hi", "author": "person"})),
            (
                Box::new(|c| c.send_as("s1", "hi", Some(Busy::Steer), &crate::catalog::request_key()).map(drop)),
                json!({"action": "send", "sessionId": "s1", "message": "hi", "author": "person", "busyDelivery": "steer"}),
            ),
            (
                Box::new(|c| c.send_as("s1", "hi", Some(Busy::Queue), &crate::catalog::request_key()).map(drop)),
                json!({"action": "send", "sessionId": "s1", "message": "hi", "author": "person", "busyDelivery": "queue"}),
            ),
            (
                Box::new(|c| c.respond("s1", "ask-2", vec!["Yes".into()], Default::default())),
                json!({"action": "respond", "sessionId": "s1", "requestId": "ask-2", "decision": {"choices": ["Yes"], "answers": {}}}),
            ),
        ];
        let (client, seen) = daemon(calls.iter().map(|_| (200, r#"{"ok":true}"#)).collect());
        let mut keys = std::collections::HashSet::new();
        for (call, want) in calls {
            call(&client).unwrap();
            let got = seen.recv().unwrap();
            assert_eq!(got.path, "/api/v1/actions");
            assert_eq!(got.auth, "Bearer secret");
            assert_eq!(got.body, want);
            assert!(!got.key.is_empty() && got.key.len() <= 128, "{}", got.key);
            assert!(keys.insert(got.key), "each call carries its own request id");
        }
    }

    #[test]
    fn replies_come_back_typed() {
        let (client, _seen) = daemon(vec![
            (200, r#"{"ok":true,"result":{"message":"take this back","attachments":[]}}"#),
            (200, r#"{"ok":true,"alias":"scout"}"#),
            (200, r#"{"ok":true,"sessionId":"s1","readState":{"revision":"r10"}}"#),
        ]);
        assert_eq!(client.queue_cancel(7, "").unwrap(), "take this back");
        assert_eq!(client.alias("s1", "@scout").unwrap(), "scout");
        assert_eq!(client.session_read("s1", "r9", true).unwrap(), Some(ReadState { first_unread: String::new(), revision: "r10".into() }));
    }

    #[test]
    fn a_refusal_carries_the_daemons_code_and_was_not_delivered() {
        let (client, _seen) = daemon(vec![
            (409, r#"{"error":{"code":"read_state_conflict","message":"the conversation moved on"}}"#),
            (400, r#"{"error":{"code":"invalid_request","message":"this session cannot be interrupted"}}"#),
            (502, r#"{"error":{"code":"bad_gateway","message":"session ended"}}"#),
        ]);
        let conflict = client.session_read("s1", "r9", true).unwrap_err();
        assert_eq!((conflict.code.as_str(), conflict.message.as_str(), conflict.maybe_delivered), ("read_state_conflict", "the conversation moved on", false));
        assert!(!client.interrupt("s1").unwrap_err().maybe_delivered);
        assert!(!client.respond("s1", "ask-1", vec!["Yes".into()], Default::default()).unwrap_err().maybe_delivered);
    }

    #[test]
    fn an_unknown_outcome_is_maybe_delivered() {
        let (client, _seen) = daemon(vec![
            (504, r#"{"error":{"code":"host_outcome_unknown","message":"Host mac did not answer; the outcome is unknown"}}"#),
            (502, r#"{"error":{"code":"control_failed","message":"compact outcome unknown"},"result":{"sessionId":"s1","action":"compact","evidence":"accepted"}}"#),
            (502, r#"{"error":{"code":"bad_gateway","message":"turn busy"}}"#),
            (502, r#"{"error":{"code":"host_answer_too_large","message":"Host mac answered with more than 8 MiB."}}"#),
            (502, r#"{"error":{"code":"bad_gateway","message":"the answer may have reached the session, so it is not sent again: timeout"}}"#),
        ]);
        assert!(client.compact("s1").unwrap_err().maybe_delivered);
        assert!(client.compact("s1").unwrap_err().maybe_delivered);
        assert!(client.steer("s1", "stop").unwrap_err().maybe_delivered);
        assert!(client.turn_setting("s1", "effort", "high").unwrap_err().maybe_delivered);
        assert!(client.respond("s1", "ask-1", vec!["Yes".into()], Default::default()).unwrap_err().maybe_delivered);
    }

    #[test]
    fn a_retried_send_carries_the_request_id_it_was_given() {
        let (client, seen) = daemon(vec![
            (200, r#"{"ok":true,"result":{"evidence":"queued","queueId":3}}"#),
            (202, r#"{"ok":true,"status":"submitted"}"#),
        ]);
        assert_eq!(client.send_as("s1", "hi", Some(Busy::Queue), "retry-1").unwrap(), Delivered::Queued);
        assert_eq!(seen.recv().unwrap().key, "retry-1");
        assert_eq!(client.send_as("s1", "hi", Some(Busy::Queue), "retry-1").unwrap(), Delivered::Unconfirmed);
        assert_eq!(seen.recv().unwrap().key, "retry-1");
    }

    #[test]
    fn any_action_name_gets_a_key_the_daemon_accepts() {
        let (client, seen) = daemon(vec![(200, r#"{"ok":true}"#)]);
        let name = "x".repeat(200);
        client.action(&name, json!({})).unwrap();
        let got = seen.recv().unwrap();
        assert!(got.key.len() <= 128, "{}", got.key);
        assert_eq!(got.body["action"], name.as_str());
    }

    #[test]
    fn a_daemon_that_never_answered_the_connection_got_nothing() {
        let error = Client::new("http://127.0.0.1:0", "secret").steer("s1", "stop").unwrap_err();
        assert!(!error.maybe_delivered, "{}", error.message);
    }

    #[test]
    fn a_reply_cut_off_after_the_request_went_out_is_maybe_delivered() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        std::thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            let mut buf = [0; 4096];
            let _ = stream.read(&mut buf);
        });
        let error = Client::new(&base, "secret").interrupt("s1").unwrap_err();
        assert!(error.maybe_delivered, "{}", error.message);
    }

    #[test]
    fn a_payload_that_is_not_an_object_is_refused_before_sending() {
        let error = Client::new("http://127.0.0.1:0", "secret").action("compact", json!(["s1"])).unwrap_err();
        assert_eq!((error.code.as_str(), error.maybe_delivered), ("invalid_request", false));
    }
}
