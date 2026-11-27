use serde::Deserialize;
use serde_json::{Value, json};

use crate::catalog::{Client, Pr, RespondError};
use crate::composer::{Busy, Delivered, Stop};

type Outcome<T> = std::result::Result<T, RespondError>;

#[derive(Clone, Debug, PartialEq)]
pub enum Dismiss<'a> {
    Stopped { session: &'a str },
    Request { session: &'a str, request: &'a str },
    /// `host` names the computer whose ledger reported the item; "" is this one.
    Parked { work_item: &'a str, host: &'a str },
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PrOp {
    Link,
    Unlink,
    Watch,
    Unwatch,
}

impl PrOp {
    pub fn as_str(self) -> &'static str {
        match self {
            PrOp::Link => "link",
            PrOp::Unlink => "unlink",
            PrOp::Watch => "watch",
            PrOp::Unwatch => "unwatch",
        }
    }
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
    pub fn interrupt(&self, session: &str) -> Outcome<Stop> {
        Ok(Stop::read(&self.action("interrupt", json!({"sessionId": session}))?))
    }

    pub fn send_as(&self, session: &str, message: &str, attachments: &[String], busy: Option<Busy>, pin: Option<crate::holders::Pin>, key: &str) -> Outcome<Delivered> {
        self.send_receipt(session, message, attachments, busy, pin, key).map(|(delivered, _)| delivered)
    }

    /// `busy` None leaves a busy session to the daemon's busy-delivery setting.
    // A message keeps the terminal it was written for, so a retry replays the same request.
    pub fn send_receipt(&self, session: &str, message: &str, attachments: &[String], busy: Option<Busy>, pin: Option<crate::holders::Pin>, key: &str) -> Outcome<(Delivered, Value)> {
        let mut payload = json!({"sessionId": session, "message": message, "author": "person"});
        if !attachments.is_empty() {
            payload["attachments"] = json!(attachments);
        }
        if let Some(busy) = busy {
            payload["busyDelivery"] = Value::String(busy.as_str().into());
        }
        let reply = self.action_pinned("send", payload, key, pin)?;
        Ok((Delivered::read(&reply), reply))
    }

    pub fn acknowledge_lead_replacement(&self, id: i64) -> Outcome<()> {
        self.action("lead-replacement-ack", json!({"replacementId": id})).map(drop)
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
        self.action(name, json!({"sessionId": session})).and_then(crate::catalog::confirmed).map(drop)
    }

    pub fn session_pin(&self, session: &str, pinned: bool) -> Outcome<()> {
        let name = if pinned { "session-pin" } else { "session-unpin" };
        self.action(name, json!({"sessionId": session})).and_then(crate::catalog::confirmed).map(drop)
    }

    pub fn alias(&self, session: &str, name: &str) -> Outcome<String> {
        let reply = self.action("alias", json!({"sessionId": session, "alias": name}))?;
        Ok(reply["alias"].as_str().unwrap_or_default().to_string())
    }

    pub fn session_read(&self, session: &str, revision: &str, read: bool) -> Outcome<Option<ReadState>> {
        let name = if read { "session-read" } else { "session-unread" };
        let reply = crate::catalog::confirmed(self.action(name, json!({"sessionId": session, "revision": revision}))?)?;
        Ok(serde_json::from_value(reply["readState"].clone()).ok())
    }

    pub fn turn_setting(&self, session: &str, setting: &str, value: &str) -> Outcome<()> {
        self.action("turn-setting", json!({"sessionId": session, "setting": setting, "value": value})).map(drop)
    }

    pub fn session_setting(&self, session: &str, surface: &str, setting: &str, value: &str) -> Outcome<()> {
        match setting {
            _ if !crate::create::is_acp(surface) => self.turn_setting(session, setting, value),
            "mode" => self.action("permission-mode", json!({"sessionId": session, "permissionMode": value})).map(drop),
            "model" => self.action("model", json!({"sessionId": session, "model": value})).map(drop),
            _ => self.turn_setting(session, setting, value),
        }
    }

    pub fn compact(&self, session: &str) -> Outcome<()> {
        self.action("compact", json!({"sessionId": session})).map(drop)
    }

    pub fn session_focus(&self, session: &str) -> Outcome<()> {
        self.action("session-focus", json!({"sessionId": session})).and_then(crate::catalog::confirmed).map(drop)
    }

    pub fn dismiss_decision(&self, target: Dismiss) -> Outcome<()> {
        let payload = match target {
            Dismiss::Stopped { session } => json!({"sessionId": session}),
            Dismiss::Request { session, request } => json!({"sessionId": session, "requestId": request}),
            Dismiss::Parked { work_item, host } => json!({"workItem": work_item, "host": host}),
        };
        self.action("dismiss-decision", payload).and_then(crate::catalog::confirmed).map(drop)
    }

    pub fn limit_resume(&self, session: &str, resume: bool) -> Outcome<()> {
        let name = if resume { "limit-resume" } else { "limit-resume-cancel" };
        self.action(name, json!({"sessionId": session})).map(drop)
    }

    pub fn pr(&self, session: &str, op: PrOp, pr: &str) -> Outcome<Vec<Pr>> {
        let reply = self.action("pr", json!({"sessionId": session, "op": op.as_str(), "pr": pr})).map_err(|mut error| {
            error.maybe_delivered |= error.code == "internal_error";
            error
        })?;
        serde_json::from_value(reply["prs"].clone()).map_err(|e| RespondError {
            message: format!("the daemon's answer didn't list the session's pull requests: {e}"),
            code: "unreadable_reply".into(),
            maybe_delivered: true,
            answered: true,
            reply,
            status: 0,
        })
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
    fn a_submitted_answer_or_dismissal_is_unconfirmed() {
        let submitted = r#"{"ok":true,"status":"submitted"}"#;
        let (client, _seen) = daemon(vec![(202, submitted), (202, submitted)]);
        let e = client.respond("s1", "r1", vec!["a".into()], Default::default()).unwrap_err();
        assert!(e.maybe_delivered && e.code == "submitted");
        let e = client.dismiss_decision(Dismiss::Stopped { session: "s1" }).unwrap_err();
        assert!(e.maybe_delivered && e.code == "submitted");
    }

    #[test]
    fn a_copy_of_the_client_keeps_the_terminal_picked_when_it_was_made() {
        let (client, seen) = daemon(vec![(200, r#"{"ok":true}"#), (200, r#"{"ok":true}"#)]);
        let pins = crate::holders::pins();
        pins.pick("s-frozen", Some(crate::holders::Pin { pid: 41, started_at: None }));
        let copy = client.clone();
        pins.pick("s-frozen", Some(crate::holders::Pin { pid: 42, started_at: None }));
        copy.compact("s-frozen").unwrap();
        assert_eq!(seen.recv().unwrap().body["pid"], 41);
        client.compact("s-frozen").unwrap();
        assert_eq!(seen.recv().unwrap().body["pid"], 42);
        pins.pick("s-frozen", None);
    }

    #[test]
    fn a_submitted_menu_change_is_not_shown_as_landed() {
        let submitted = r#"{"ok":true,"status":"submitted"}"#;
        let (client, _seen) = daemon(vec![(202, submitted); 4]);
        assert_eq!(client.session_pin("s1", true).unwrap_err().code, "submitted");
        assert_eq!(client.session_archive("s1", true).unwrap_err().code, "submitted");
        assert_eq!(client.session_read("s1", "r1", true).unwrap_err().code, "submitted");
        assert_eq!(client.session_focus("s1").unwrap_err().code, "submitted");
    }

    #[test]
    fn each_action_sends_the_body_its_handler_reads() {
        type Call = Box<dyn Fn(&Client) -> Outcome<()>>;
        let calls: Vec<(Call, Value)> = vec![
            (Box::new(|c| c.interrupt("s1").map(drop)), json!({"action": "interrupt", "sessionId": "s1"})),
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
            (Box::new(|c| c.session_setting("s1", "codex", "mode", "plan")), json!({"action": "turn-setting", "sessionId": "s1", "setting": "mode", "value": "plan"})),
            (Box::new(|c| c.session_setting("s1", "devin", "mode", "ask")), json!({"action": "permission-mode", "sessionId": "s1", "permissionMode": "ask"})),
            (Box::new(|c| c.session_setting("s1", "devin", "model", "swe-2")), json!({"action": "model", "sessionId": "s1", "model": "swe-2"})),
            (Box::new(|c| c.compact("s1")), json!({"action": "compact", "sessionId": "s1"})),
            (Box::new(|c| c.session_focus("s1")), json!({"action": "session-focus", "sessionId": "s1"})),
            (Box::new(|c| c.dismiss_decision(Dismiss::Stopped { session: "s1" })), json!({"action": "dismiss-decision", "sessionId": "s1"})),
            (Box::new(|c| c.dismiss_decision(Dismiss::Request { session: "s1", request: "ask-1" })), json!({"action": "dismiss-decision", "sessionId": "s1", "requestId": "ask-1"})),
            (Box::new(|c| c.dismiss_decision(Dismiss::Parked { work_item: "w-1", host: "" })), json!({"action": "dismiss-decision", "workItem": "w-1", "host": ""})),
            (Box::new(|c| c.limit_resume("s1", true)), json!({"action": "limit-resume", "sessionId": "s1"})),
            (Box::new(|c| c.limit_resume("s1", false)), json!({"action": "limit-resume-cancel", "sessionId": "s1"})),
            (Box::new(|c| c.limit_move("s1", None, false).map(drop)), json!({"action": "limit-move", "sessionId": "s1", "stopTerminal": false})),
            (Box::new(|c| c.limit_move("s1", Some("/home/a/.claude-b"), true).map(drop)), json!({"action": "limit-move", "sessionId": "s1", "configRoot": "/home/a/.claude-b", "stopTerminal": true})),
            (Box::new(|c| c.pr("s1", PrOp::Watch, "https://github.com/o/r/pull/12").map(drop)), json!({"action": "pr", "sessionId": "s1", "op": "watch", "pr": "https://github.com/o/r/pull/12"})),
            (Box::new(|c| c.pr("s1", PrOp::Link, "34").map(drop)), json!({"action": "pr", "sessionId": "s1", "op": "link", "pr": "34"})),
            (Box::new(|c| c.pr("s1", PrOp::Unlink, "34").map(drop)), json!({"action": "pr", "sessionId": "s1", "op": "unlink", "pr": "34"})),
            (Box::new(|c| c.pr("s1", PrOp::Unwatch, "34").map(drop)), json!({"action": "pr", "sessionId": "s1", "op": "unwatch", "pr": "34"})),
            (Box::new(|c| c.send("s1", "hi")), json!({"action": "send", "sessionId": "s1", "message": "hi", "author": "person"})),
            (Box::new(|c| c.send_as("s1", "hi", &[], None, None, &crate::catalog::request_key()).map(drop)), json!({"action": "send", "sessionId": "s1", "message": "hi", "author": "person"})),
            (
                Box::new(|c| c.send_as("s1", "hi", &[], Some(Busy::Steer), None, &crate::catalog::request_key()).map(drop)),
                json!({"action": "send", "sessionId": "s1", "message": "hi", "author": "person", "busyDelivery": "steer"}),
            ),
            (
                Box::new(|c| c.send_as("s1", "hi", &[], Some(Busy::Queue), None, &crate::catalog::request_key()).map(drop)),
                json!({"action": "send", "sessionId": "s1", "message": "hi", "author": "person", "busyDelivery": "queue"}),
            ),
            (
                Box::new(|c| c.send_as("s1", "", &["up-1".into(), "up-2".into()], None, None, &crate::catalog::request_key()).map(drop)),
                json!({"action": "send", "sessionId": "s1", "message": "", "author": "person", "attachments": ["up-1", "up-2"]}),
            ),
            (
                Box::new(|c| c.send_as("s1", "hi", &[], None, Some(crate::holders::Pin { pid: 41, started_at: Some(1_795_687_200_250) }), &crate::catalog::request_key()).map(drop)),
                json!({"action": "send", "sessionId": "s1", "message": "hi", "author": "person", "pid": 41, "pidStartedAt": 1_795_687_200_250i64}),
            ),
            (Box::new(|c| c.acknowledge_lead_replacement(5)), json!({"action": "lead-replacement-ack", "replacementId": 5})),
            (
                Box::new(|c| c.respond("s1", "ask-2", vec!["Yes".into()], Default::default())),
                json!({"action": "respond", "sessionId": "s1", "requestId": "ask-2", "decision": {"choices": ["Yes"], "answers": {}}}),
            ),
            (
                Box::new(|c| c.answer("s1", "e-1", &crate::request::Response::form(json!({"repo": "a"})))),
                json!({"action": "respond", "sessionId": "s1", "requestId": "e-1", "decision": {"choices": ["allow"], "answers": {}, "content": {"repo": "a"}}}),
            ),
            (Box::new(|c| c.withdraw_request("s1", "p-1")), json!({"action": "request-withdraw", "sessionId": "s1", "requestId": "p-1"})),
        ];
        let (client, seen) = daemon(calls.iter().map(|_| (200, r#"{"ok":true,"prs":[]}"#)).collect());
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
            (200, r#"{"ok":true,"sessionId":"s1","prs":[{"repo":"o/r","number":12,"url":"https://github.com/o/r/pull/12","watch":true,"checks":"failing"}]}"#),
            (200, r#"{"ok":true,"result":{"sessionId":"s2"}}"#),
            (500, r#"{"error":{"code":"internal_error","message":"database is locked"}}"#),
            (200, r#"{"ok":true,"sessionId":"s1"}"#),
            (200, r#"{"ok":true,"result":{"move":{"state":"done","detail":"its resume under the new account is unconfirmed: timeout"}}}"#),
            (409, r#"{"error":{"code":"move_needs_consent","message":"a terminal runs this session"}}"#),
            (409, r#"{"error":{"code":"move_refused","message":"no other Claude account has room for this session now"}}"#),
        ]);
        assert_eq!(client.queue_cancel(7, "").unwrap(), "take this back");
        assert_eq!(client.alias("s1", "@scout").unwrap(), "scout");
        assert_eq!(client.session_read("s1", "r9", true).unwrap(), Some(ReadState { first_unread: String::new(), revision: "r10".into() }));
        let prs = client.pr("s1", PrOp::Watch, "12").unwrap();
        assert_eq!((prs[0].number, prs[0].watch, prs[0].checks.as_str()), (12, true, "failing"));
        let unread = client.pr("s1", PrOp::Link, "13").unwrap_err();
        assert!(unread.maybe_delivered && unread.code == "unreadable_reply", "{unread:?}");
        let half = client.pr("s1", PrOp::Watch, "13").unwrap_err();
        assert!(half.maybe_delivered, "the daemon changes the watch before it reads the list back: {half:?}");
        use crate::limit::Moved;
        assert_eq!(client.limit_move("s1", Some("/r/b"), false).unwrap(), Moved::Done(None));
        assert_eq!(client.limit_move("s1", Some("/r/b"), false).unwrap(), Moved::Done(Some("its resume under the new account is unconfirmed: timeout".into())));
        assert_eq!(client.limit_move("s1", Some("/r/b"), false).unwrap(), Moved::NeedsConsent("a terminal runs this session".into()));
        assert_eq!(client.limit_move("s1", None, false).unwrap_err().code, "move_refused");
    }

    #[test]
    fn a_refusal_carries_the_daemons_code_and_was_not_delivered() {
        let (client, _seen) = daemon(vec![
            (409, r#"{"error":{"code":"read_state_conflict","message":"the conversation moved on"}}"#),
            (400, r#"{"error":{"code":"invalid_request","message":"this session cannot be interrupted"}}"#),
            (502, r#"{"error":{"code":"bad_gateway","message":"session ended"}}"#),
        ]);
        let conflict = client.session_read("s1", "r9", true).unwrap_err();
        assert_eq!((conflict.code.as_str(), conflict.message.as_str(), conflict.maybe_delivered, conflict.answered), ("read_state_conflict", "the conversation moved on", false, true));
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
    fn an_unreadable_success_reply_is_maybe_delivered() {
        let (client, _seen) = daemon(vec![(200, r#"{"ok":tr"#)]);
        let error = client.send_as("s1", "hi", &[], None, None, "k").unwrap_err();
        assert!(error.maybe_delivered, "{}", error.message);
        assert_eq!(error.code, "unreadable_reply");
    }

    #[test]
    fn a_stop_still_in_progress_is_not_reported_as_done() {
        let (client, _seen) = daemon(vec![(202, r#"{"ok":true,"status":"submitted"}"#), (200, r#"{"ok":true,"result":{"action":"interrupt","evidence":"delivered"}}"#)]);
        assert_eq!(client.interrupt("s1").unwrap(), Stop::Unconfirmed);
        assert_eq!(client.interrupt("s1").unwrap(), Stop::Stopped);
    }

    #[test]
    fn a_retried_send_carries_the_request_id_it_was_given() {
        let (client, seen) = daemon(vec![
            (200, r#"{"ok":true,"result":{"evidence":"queued","queueId":3}}"#),
            (202, r#"{"ok":true,"status":"submitted"}"#),
        ]);
        assert_eq!(client.send_as("s1", "hi", &[], Some(Busy::Queue), None, "retry-1").unwrap(), Delivered::Queued(Some(3)));
        assert_eq!(seen.recv().unwrap().key, "retry-1");
        assert_eq!(client.send_as("s1", "hi", &[], Some(Busy::Queue), None, "retry-1").unwrap(), Delivered::Unconfirmed);
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
        assert!(!error.maybe_delivered && !error.answered, "{}", error.message);
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
