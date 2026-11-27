use std::collections::HashMap;

use serde_json::{Value, json};

use crate::catalog::{Client, RespondError};
use crate::derive::Work;

pub const PERSON: &str = "agenthail-person";

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Move {
    Claim,
    Submit,
    Park,
    Comment,
    Reopen,
}

impl Move {
    fn action(self) -> &'static str {
        match self {
            Move::Claim => "claim",
            Move::Submit => "submit",
            Move::Park => "park",
            Move::Comment => "comment",
            Move::Reopen => "reopen",
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            Move::Claim => "Claim",
            Move::Submit => "Submit for review",
            Move::Park => "Park…",
            Move::Comment => "Comment…",
            Move::Reopen => "Reopen",
        }
    }

    pub fn done(self) -> &'static str {
        match self {
            Move::Claim => "You hold it now.",
            Move::Submit => "Submitted for review.",
            Move::Park => "Parked for later.",
            Move::Comment => "Comment added.",
            Move::Reopen => "Reopened. It's open for the next session.",
        }
    }

    pub fn needs_words(self) -> bool {
        matches!(self, Move::Park | Move::Comment)
    }
}

pub fn offered(w: &Work) -> Vec<Move> {
    let mine = w.assignee.as_deref() == Some(PERSON);
    let mut moves = match w.state.as_str() {
        "open" => vec![Move::Claim, Move::Park],
        "blocked" => vec![Move::Park],
        "in_progress" if mine => vec![Move::Submit, Move::Park],
        "done" | "closed" => vec![Move::Reopen],
        _ => Vec::new(),
    };
    if !w.state.is_empty() && w.state != "deleted" {
        moves.push(Move::Comment);
    }
    moves
}

pub fn words_problem(m: Move, words: &str) -> Option<&'static str> {
    match m {
        Move::Park if words.trim().is_empty() => Some("Say why it waits."),
        Move::Comment if words.trim().is_empty() => Some("Write the comment."),
        _ => None,
    }
}

pub fn body(w: &Work, m: Move, words: &str) -> Value {
    let mut body = json!({"projectKey": w.ledger_key(), "sessionId": PERSON, "action": m.action(), "itemId": w.id});
    match m {
        Move::Comment => body["text"] = Value::String(words.trim().into()),
        Move::Park => body["park"] = json!({"kind": "later", "why": words.trim()}),
        _ => {}
    }
    body
}

pub fn refusal(e: &RespondError) -> String {
    match e.code.as_str() {
        "session_not_found" => "Agenthail doesn't know you yet. Send any session a message once, then try again.".into(),
        _ => e.message.clone(),
    }
}

pub const CHANGED_WORDS: &str = "The last send isn't settled yet, so only the same words can go now. Change them once it settles.";

pub fn settles(e: &RespondError) -> bool {
    e.answered && (400..500).contains(&e.status)
}

pub fn unreached(e: &RespondError) -> String {
    format!("Agenthail didn't settle it: {}. The earlier send is still unconfirmed; pressing again asks about it.", e.message)
}

pub fn unconfirmed(e: &RespondError, again: bool) -> String {
    if again {
        format!("Still not confirmed: {}. If it never settles, Send as new starts a fresh one.", e.message)
    } else {
        format!("Sent, not confirmed: {}. It asks Agenthail about the same send again until it answers.", e.message)
    }
}

pub const RESEND_FIRST: f64 = 10.0;
pub const RESEND_MAX: f64 = 60.0;
pub const RESEND_FOR: f64 = 3600.0;

#[derive(Clone, Copy, Debug, PartialEq)]
struct Resend {
    due: f64,
    wait: f64,
    since: f64,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Pending {
    pub words: String,
    pub key: String,
    resend: Option<Resend>,
}

impl Pending {
    pub fn unconfirmed(&self) -> bool {
        self.resend.is_some()
    }
}

#[derive(Default)]
pub struct Sends(HashMap<(String, Move), Pending>);

impl Sends {
    pub fn key(&mut self, id: &str, m: Move, words: &str) -> String {
        let slot = (id.to_string(), m);
        match self.0.get(&slot) {
            Some(p) if p.words == words => p.key.clone(),
            _ => {
                let key = crate::catalog::request_key();
                self.0.insert(slot, Pending { words: words.to_string(), key: key.clone(), resend: None });
                key
            }
        }
    }

    pub fn get(&self, id: &str, m: Move) -> Option<&Pending> {
        self.0.get(&(id.to_string(), m))
    }

    pub fn held(&self, id: &str) -> Option<Move> {
        self.0.iter().find(|((item, _), p)| item == id && p.unconfirmed()).map(|((_, m), _)| *m)
    }

    pub fn held_words(&self, id: &str, m: Move) -> Option<&str> {
        self.get(id, m).filter(|p| p.unconfirmed()).map(|p| p.words.as_str())
    }

    pub fn forget(&mut self, id: &str, m: Move) -> Option<Pending> {
        self.0.remove(&(id.to_string(), m))
    }

    pub fn answered(&mut self, id: &str, m: Move, result: &Result<Value, RespondError>, now: f64) -> (bool, String) {
        let slot = (id.to_string(), m);
        let held = self.0.get(&slot).and_then(|p| p.resend);
        match result {
            Ok(_) => {
                self.0.remove(&slot);
                (false, m.done().into())
            }
            Err(e) if e.maybe_delivered || (held.is_some() && !settles(e)) => {
                let next = match held {
                    Some(r) => {
                        let wait = (r.wait * 2.0).min(RESEND_MAX);
                        Resend { due: now + wait, wait, since: r.since }
                    }
                    None => Resend { due: now + RESEND_FIRST, wait: RESEND_FIRST, since: now },
                };
                if let Some(p) = self.0.get_mut(&slot) {
                    p.resend = Some(next);
                }
                (true, if e.maybe_delivered { unconfirmed(e, held.is_some()) } else { unreached(e) })
            }
            Err(e) => {
                self.0.remove(&slot);
                (true, format!("Not done: {}", refusal(e)))
            }
        }
    }

    pub fn due(&self, now: f64) -> Vec<(String, Move, String)> {
        self.0.iter().filter_map(|((id, m), p)| p.resend.filter(|r| r.due <= now && now - r.since < RESEND_FOR).map(|_| (id.clone(), *m, p.words.clone()))).collect()
    }

    pub fn wake(&mut self, now: f64) {
        for r in self.0.values_mut().filter_map(|p| p.resend.as_mut()) {
            r.due = r.due.min(now);
        }
    }
}

impl Client {
    pub fn work_move(&self, w: &Work, m: Move, words: &str, key: &str) -> Result<Value, RespondError> {
        self.post_json_keyed("/api/v1/ledger/work", m.action(), body(w, m, words), key).and_then(crate::catalog::confirmed)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};

    fn work(state: &str, assignee: Option<&str>) -> Work {
        Work { id: "p-W6".into(), state: state.into(), assignee: assignee.map(str::to_string), project: "p-1".into(), ledger_key: "project:p-1".into(), ..Default::default() }
    }

    #[test]
    fn each_state_offers_only_what_the_ledger_takes_from_the_person() {
        assert_eq!(offered(&work("open", None)), vec![Move::Claim, Move::Park, Move::Comment]);
        assert_eq!(offered(&work("blocked", None)), vec![Move::Park, Move::Comment]);
        assert_eq!(offered(&work("in_progress", Some(PERSON))), vec![Move::Submit, Move::Park, Move::Comment]);
        assert_eq!(offered(&work("in_progress", Some("worker"))), vec![Move::Comment]);
        assert_eq!(offered(&work("in_review", Some("worker"))), vec![Move::Comment]);
        assert_eq!(offered(&work("parked", None)), vec![Move::Comment]);
        assert_eq!(offered(&work("done", None)), vec![Move::Reopen, Move::Comment]);
        assert_eq!(offered(&work("closed", None)), vec![Move::Reopen, Move::Comment]);
        assert_eq!(offered(&work("deleted", None)), vec![]);
        assert_eq!(offered(&work("", None)), vec![]);
    }

    #[test]
    fn park_and_comment_need_words() {
        assert_eq!(words_problem(Move::Park, "  "), Some("Say why it waits."));
        assert_eq!(words_problem(Move::Comment, "\n"), Some("Write the comment."));
        assert_eq!(words_problem(Move::Park, "after the release"), None);
        assert_eq!(words_problem(Move::Claim, ""), None);
        assert!(Move::Park.needs_words() && Move::Comment.needs_words() && !Move::Claim.needs_words() && !Move::Submit.needs_words() && !Move::Reopen.needs_words());
    }

    #[test]
    fn each_move_writes_the_ledger_as_the_person() {
        let ok = r#"{"item":{"id":"p-W6","state":"in_progress"},"readback":"intent ledger: p-W6 in_progress (found)"}"#;
        let (client, seen) = daemon(replies(&[(200, ok); 5]));
        let w = work("open", None);
        for (m, words) in [(Move::Claim, ""), (Move::Submit, ""), (Move::Park, " after the release "), (Move::Comment, " looks right "), (Move::Reopen, "ignored")] {
            client.work_move(&w, m, words, "k").unwrap();
            let (path, body) = seen.recv().unwrap();
            assert_eq!(path, "/api/v1/ledger/work");
            let mut want = json!({"projectKey": "project:p-1", "sessionId": "agenthail-person", "action": m.action(), "itemId": "p-W6"});
            match m {
                Move::Park => want["park"] = json!({"kind": "later", "why": "after the release"}),
                Move::Comment => want["text"] = json!("looks right"),
                _ => {}
            }
            assert_eq!(body, want);
        }
        let repo = Work { ledger_key: String::new(), project: "repo:github.com/o/r".into(), ..w };
        assert_eq!(body(&repo, Move::Claim, "")["projectKey"], "repo:github.com/o/r");
    }

    #[test]
    fn a_refusal_reads_as_the_ledger_says_it() {
        let (client, _seen) = daemon(replies(&[
            (409, r#"{"error":{"code":"refused","message":"p-W6 waits on p-W2; claim it once they are done"}}"#),
            (404, r#"{"error":{"code":"session_not_found","message":"No session agenthail-person in this Agenthail."}}"#),
            (504, r#"{"error":{"code":"host_outcome_unknown","message":"Host mac did not answer"}}"#),
        ]));
        let w = work("open", None);
        let refused = client.work_move(&w, Move::Claim, "", "k1").unwrap_err();
        assert!(!refused.maybe_delivered);
        assert_eq!(refusal(&refused), "p-W6 waits on p-W2; claim it once they are done");
        let unknown = client.work_move(&w, Move::Claim, "", "k2").unwrap_err();
        assert_eq!(refusal(&unknown), "Agenthail doesn't know you yet. Send any session a message once, then try again.");
        let lost = client.work_move(&w, Move::Claim, "", "k3").unwrap_err();
        assert!(lost.maybe_delivered);
        assert_eq!(unconfirmed(&lost, false), "Sent, not confirmed: Host mac did not answer. It asks Agenthail about the same send again until it answers.");
        let unsent = Client::new("http://127.0.0.1:0", "secret").work_move(&w, Move::Comment, "hi", "k4").unwrap_err();
        assert!(!unsent.answered && !unsent.maybe_delivered);
        assert!(!settles(&unsent));
        assert!(!settles(&RespondError::from_status("claim", 500, r#"{"error":{"code":"action_reservation_failed","message":"busy"}}"#)));
        assert!(settles(&RespondError::from_status("claim", 409, r#"{"error":{"code":"refused","message":"no"}}"#)));
        assert!(unreached(&unsent).starts_with("Agenthail didn't settle it: "));
        assert!(unreached(&unsent).ends_with("The earlier send is still unconfirmed; pressing again asks about it."));
        assert_eq!(unconfirmed(&lost, true), "Still not confirmed: Host mac did not answer. If it never settles, Send as new starts a fresh one.");
    }

    #[test]
    fn a_pending_receipt_is_not_taken_as_done() {
        let (client, _seen) = daemon(replies(&[(202, r#"{"ok":true,"status":"submitted"}"#)]));
        let e = client.work_move(&work("open", None), Move::Comment, "hi", "k").unwrap_err();
        assert!(e.maybe_delivered && e.code == "submitted");
    }

    const SUBMITTED: &str = r#"{"ok":true,"status":"submitted"}"#;

    fn sent(sends: &mut Sends, client: &Client, w: &Work, m: Move, words: &str, now: f64) -> (bool, String) {
        let key = sends.key(&w.id, m, words);
        let result = client.work_move(w, m, words, &key);
        sends.answered(&w.id, m, &result, now)
    }

    #[test]
    fn an_unconfirmed_claim_keeps_its_key_when_someone_else_claims_it() {
        let ok = r#"{"item":{"id":"p-W6","state":"in_progress","assignee":"agenthail-person"}}"#;
        let (client, _seen) = daemon(replies(&[(202, SUBMITTED), (200, ok)]));
        let mut sends = Sends::default();
        let open = work("open", None);
        sent(&mut sends, &client, &open, Move::Claim, "", 0.0);
        let key = sends.get("p-W6", Move::Claim).unwrap().key.clone();
        assert_eq!(sends.held("p-W6"), Some(Move::Claim));
        let theirs = work("in_progress", Some("worker"));
        assert!(!offered(&theirs).contains(&Move::Claim));
        assert!(sends.due(9.9).is_empty(), "nothing goes again before ten seconds");
        assert_eq!(sends.due(10.0), vec![("p-W6".to_string(), Move::Claim, String::new())]);
        assert_eq!(sends.key("p-W6", Move::Claim, ""), key, "the resend asks about the same send");
        assert_eq!(sent(&mut sends, &client, &theirs, Move::Claim, "", 10.0), (false, "You hold it now.".into()));
        assert!(sends.get("p-W6", Move::Claim).is_none());
    }

    #[test]
    fn an_unconfirmed_claim_settles_from_the_reply_when_someone_else_parks_it() {
        let refused = r#"{"error":{"code":"refused","message":"p-W6 is parked"}}"#;
        let (client, _seen) = daemon(replies(&[(504, r#"{"error":{"code":"host_outcome_unknown","message":"Host mac did not answer"}}"#), (202, SUBMITTED), (409, refused)]));
        let mut sends = Sends::default();
        sent(&mut sends, &client, &work("open", None), Move::Claim, "", 0.0);
        let parked = work("parked", None);
        assert_eq!(sends.held("p-W6"), Some(Move::Claim), "someone else's park is no answer to our claim");
        let (failed, note) = sent(&mut sends, &client, &parked, Move::Claim, "", 10.0);
        assert!(failed && note.starts_with("Still not confirmed: "), "a replayed submitted is still no answer");
        assert!(sends.due(29.9).is_empty() && !sends.due(30.0).is_empty(), "the wait doubles");
        assert_eq!(sent(&mut sends, &client, &parked, Move::Claim, "", 30.0), (true, "Not done: p-W6 is parked".into()));
        assert_eq!(sends.held("p-W6"), None);
    }

    #[test]
    fn resends_wait_at_least_ten_seconds_up_to_a_minute_for_an_hour() {
        let lost = Err(RespondError::from_status("claim", 504, ""));
        let mut sends = Sends::default();
        sends.key("p-W6", Move::Park, "later");
        let mut now = 0.0;
        let mut waits = Vec::new();
        sends.answered("p-W6", Move::Park, &lost, now);
        while let Some(due) = (0..=60).map(|s| now + s as f64).find(|t| !sends.due(*t).is_empty()) {
            waits.push(due - now);
            now = due;
            sends.answered("p-W6", Move::Park, &lost, now);
        }
        assert_eq!(&waits[..4], &[10.0, 20.0, 40.0, 60.0]);
        assert!(waits[4..].iter().all(|w| *w == 60.0));
        assert!(now < RESEND_FOR && now + 60.0 >= RESEND_FOR, "it stops asking after an hour");
        assert_eq!(sends.held_words("p-W6", Move::Park), Some("later"), "it stays unconfirmed after that");
    }

    #[test]
    fn a_reconnect_asks_again_now() {
        let mut sends = Sends::default();
        sends.key("p-W6", Move::Claim, "");
        sends.answered("p-W6", Move::Claim, &Err(RespondError::from_status("claim", 504, "")), 0.0);
        assert!(sends.due(3.0).is_empty());
        sends.wake(3.0);
        assert_eq!(sends.due(3.0).len(), 1);
    }

    #[test]
    fn an_unreachable_daemon_keeps_an_unconfirmed_send_and_drops_a_fresh_one() {
        let unreached = Err(RespondError { message: "down".into(), code: String::new(), maybe_delivered: false, reply: Value::Null, answered: false, status: 0 });
        let mut sends = Sends::default();
        sends.key("p-W6", Move::Claim, "");
        assert_eq!(sends.answered("p-W6", Move::Claim, &unreached, 0.0), (true, "Not done: down".into()));
        assert!(sends.get("p-W6", Move::Claim).is_none());
        sends.key("p-W6", Move::Claim, "");
        sends.answered("p-W6", Move::Claim, &Err(RespondError::from_status("claim", 504, "")), 0.0);
        let (failed, note) = sends.answered("p-W6", Move::Claim, &unreached, 10.0);
        assert!(failed && note.starts_with("Agenthail didn't settle it: down."));
        assert_eq!(sends.held("p-W6"), Some(Move::Claim));
        assert!(sends.due(29.9).is_empty() && !sends.due(30.0).is_empty());
    }
}

