use std::collections::HashMap;

use serde_json::{Value, json};

use crate::catalog::{Client, RespondError};
use crate::derive::{Work, WorkKey};

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

pub const SETTLED_WHILE_OPEN: &str = "The earlier send settled while this was open. Close it and start again to send another.";

pub const ITEM_GONE: &str = "This work item isn't loaded right now, so it can't take this yet. Your words stay here until it's back.";

pub const NOT_OFFERED: &str = "This work item moved on while you wrote, so it doesn't take this now. Your words stay here until you cancel.";

pub const OTHER_HELD: &str = "Another move on this work item isn't settled yet. Your words stay here until it settles.";

pub const ANSWERED_UNKNOWN: &str = "Agenthail can't tell whether the earlier send happened, and asking again gets the same answer. Check the item, then use Send as new.";

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
    body: Value,
    resend: Option<Resend>,
    woke: bool,
    answered_unknown: bool,
}

impl Pending {
    pub fn unconfirmed(&self) -> bool {
        self.resend.is_some()
    }
}

#[derive(Default)]
pub struct Sends(HashMap<(WorkKey, Move), Pending>);

impl Sends {
    pub fn sending(&mut self, w: &Work, m: Move, words: &str) -> (String, Value) {
        let fresh = || Pending { words: words.to_string(), key: crate::catalog::request_key(), body: body(w, m, words), resend: None, woke: false, answered_unknown: false };
        let p = self.0.entry((w.key(), m)).or_insert_with(fresh);
        if p.words != words {
            *p = fresh();
        }
        p.woke = false;
        (p.key.clone(), p.body.clone())
    }

    pub fn resending(&mut self, k: &WorkKey, m: Move) -> Option<(String, Value)> {
        let p = self.0.get_mut(&(k.clone(), m)).filter(|p| p.unconfirmed())?;
        p.woke = false;
        Some((p.key.clone(), p.body.clone()))
    }

    pub fn get(&self, k: &WorkKey, m: Move) -> Option<&Pending> {
        self.0.get(&(k.clone(), m))
    }

    pub fn held(&self, k: &WorkKey) -> Option<Move> {
        self.0.iter().find(|((item, _), p)| item == k && p.unconfirmed()).map(|((_, m), _)| *m)
    }

    pub fn held_key(&self, k: &WorkKey, m: Move) -> Option<&str> {
        self.get(k, m).filter(|p| p.unconfirmed()).map(|p| p.key.as_str())
    }

    pub fn held_words(&self, k: &WorkKey, m: Move) -> Option<&str> {
        self.get(k, m).filter(|p| p.unconfirmed()).map(|p| p.words.as_str())
    }

    pub fn asks_again(&self, k: &WorkKey, m: Move) -> bool {
        self.get(k, m).is_some_and(|p| p.unconfirmed() && !p.answered_unknown)
    }

    pub fn forget(&mut self, k: &WorkKey, m: Move) -> Option<Pending> {
        self.0.remove(&(k.clone(), m))
    }

    pub fn answered(&mut self, k: &WorkKey, m: Move, result: &Result<Value, RespondError>, now: f64) -> (bool, String) {
        let slot = (k.clone(), m);
        let held = self.0.get(&slot).and_then(|p| p.resend);
        let woke = self.0.get(&slot).is_some_and(|p| p.woke);
        match result {
            Ok(_) => {
                self.0.remove(&slot);
                (false, m.done().into())
            }
            Err(e) if e.outcome_unknown() => {
                if let Some(p) = self.0.get_mut(&slot) {
                    p.resend = Some(held.unwrap_or(Resend { due: now, wait: RESEND_FIRST, since: now }));
                    p.answered_unknown = true;
                    p.woke = false;
                }
                (true, crate::catalog::unknown_outcome(&e.message))
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
                    p.resend = Some(Resend { due: if woke { now } else { next.due }, ..next });
                    p.woke = false;
                }
                (true, if e.maybe_delivered { unconfirmed(e, held.is_some()) } else { unreached(e) })
            }
            Err(e) => {
                self.0.remove(&slot);
                (true, format!("Not done: {}", refusal(e)))
            }
        }
    }

    pub fn due(&self, now: f64) -> Vec<(WorkKey, Move, String)> {
        self.0.iter().filter(|(_, p)| !p.answered_unknown).filter_map(|((k, m), p)| p.resend.filter(|r| r.due <= now && now - r.since < RESEND_FOR).map(|_| (k.clone(), *m, p.words.clone()))).collect()
    }

    pub fn wake(&mut self, now: f64) {
        for p in self.0.values_mut() {
            p.woke = true;
            if let Some(r) = p.resend.as_mut() {
                r.due = r.due.min(now);
            }
        }
    }
}

impl Client {
    pub fn work_move(&self, w: &Work, m: Move, words: &str, key: &str) -> Result<Value, RespondError> {
        self.work_post(m, body(w, m, words), key)
    }

    pub fn work_post(&self, m: Move, body: Value, key: &str) -> Result<Value, RespondError> {
        self.post_json_keyed("/api/v1/ledger/work", m.action(), body, key).and_then(crate::catalog::confirmed)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};

    fn k6() -> WorkKey {
        WorkKey::new("project:p-1", "p-W6")
    }

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
        let (key, body) = sends.sending(w, m, words);
        let result = client.work_post(m, body, &key);
        sends.answered(&w.key(), m, &result, now)
    }

    #[test]
    fn an_unconfirmed_claim_keeps_its_key_when_someone_else_claims_it() {
        let ok = r#"{"item":{"id":"p-W6","state":"in_progress","assignee":"agenthail-person"}}"#;
        let (client, _seen) = daemon(replies(&[(202, SUBMITTED), (200, ok)]));
        let mut sends = Sends::default();
        let open = work("open", None);
        sent(&mut sends, &client, &open, Move::Claim, "", 0.0);
        let key = sends.get(&k6(), Move::Claim).unwrap().key.clone();
        assert_eq!(sends.held(&k6()), Some(Move::Claim));
        let theirs = work("in_progress", Some("worker"));
        assert!(!offered(&theirs).contains(&Move::Claim));
        assert!(sends.due(9.9).is_empty(), "nothing goes again before ten seconds");
        assert_eq!(sends.due(10.0), vec![(k6(), Move::Claim, String::new())]);
        assert_eq!(sends.sending(&theirs, Move::Claim, "").0, key, "the resend asks about the same send");
        assert_eq!(sent(&mut sends, &client, &theirs, Move::Claim, "", 10.0), (false, "You hold it now.".into()));
        assert!(sends.get(&k6(), Move::Claim).is_none());
    }

    #[test]
    fn an_unconfirmed_claim_settles_from_the_reply_when_someone_else_parks_it() {
        let refused = r#"{"error":{"code":"refused","message":"p-W6 is parked"}}"#;
        let (client, _seen) = daemon(replies(&[(504, r#"{"error":{"code":"host_outcome_unknown","message":"Host mac did not answer"}}"#), (202, SUBMITTED), (409, refused)]));
        let mut sends = Sends::default();
        sent(&mut sends, &client, &work("open", None), Move::Claim, "", 0.0);
        let parked = work("parked", None);
        assert_eq!(sends.held(&k6()), Some(Move::Claim), "someone else's park is no answer to our claim");
        let (failed, note) = sent(&mut sends, &client, &parked, Move::Claim, "", 10.0);
        assert!(failed && note.starts_with("Still not confirmed: "), "a replayed submitted is still no answer");
        assert!(sends.due(29.9).is_empty() && !sends.due(30.0).is_empty(), "the wait doubles");
        assert_eq!(sent(&mut sends, &client, &parked, Move::Claim, "", 30.0), (true, "Not done: p-W6 is parked".into()));
        assert_eq!(sends.held(&k6()), None);
    }

    #[test]
    fn resends_wait_at_least_ten_seconds_up_to_a_minute_for_an_hour() {
        let lost = Err(RespondError::from_status("claim", 504, ""));
        let mut sends = Sends::default();
        sends.sending(&work("open", None), Move::Park, "later");
        let mut now = 0.0;
        let mut waits = Vec::new();
        sends.answered(&k6(), Move::Park, &lost, now);
        while let Some(due) = (0..=60).map(|s| now + s as f64).find(|t| !sends.due(*t).is_empty()) {
            waits.push(due - now);
            now = due;
            sends.answered(&k6(), Move::Park, &lost, now);
        }
        assert_eq!(&waits[..4], &[10.0, 20.0, 40.0, 60.0]);
        assert!(waits[4..].iter().all(|w| *w == 60.0));
        assert!(now < RESEND_FOR && now + 60.0 >= RESEND_FOR, "it stops asking after an hour");
        assert_eq!(sends.held_words(&k6(), Move::Park), Some("later"), "it stays unconfirmed after that");
    }

    #[test]
    fn an_interrupted_move_stays_unconfirmed_on_its_key_and_is_not_sent_again_by_itself() {
        let interrupted = r#"{"error":{"code":"action_outcome_unknown","message":"This request was interrupted before its outcome was recorded, so it may have happened. Check what it was meant to change before sending it again as a new request."}}"#;
        let (client, seen) = crate::mock::keyed(replies(&[(504, ""), (409, interrupted)]));
        let mut sends = Sends::default();
        let open = work("open", None);
        sent(&mut sends, &client, &open, Move::Claim, "", 0.0);
        let key = sends.held_key(&k6(), Move::Claim).unwrap().to_string();
        assert!(sends.asks_again(&k6(), Move::Claim));
        let (failed, note) = sent(&mut sends, &client, &open, Move::Claim, "", 10.0);
        assert!(failed && note.starts_with("This request was interrupted") && note.ends_with("as a new request. The outcome is unknown; check the session before retrying."), "{note}");
        assert_eq!(sends.held(&k6()), Some(Move::Claim));
        assert_eq!(sends.held_key(&k6(), Move::Claim), Some(key.as_str()), "the key stays with the send that may have happened");
        assert!(!sends.asks_again(&k6(), Move::Claim), "asking again gets the same answer");
        sends.wake(20.0);
        assert!(sends.due(3600.0).is_empty(), "nothing goes again by itself");
        let keys: Vec<String> = (0..2).map(|_| seen.recv().unwrap().2).collect();
        assert_eq!(keys, [key.clone(), key]);
        assert!(sends.forget(&k6(), Move::Claim).is_some(), "Send as new starts over");
    }

    #[test]
    fn a_reconnect_asks_again_now() {
        let mut sends = Sends::default();
        sends.sending(&work("open", None), Move::Claim, "");
        sends.answered(&k6(), Move::Claim, &Err(RespondError::from_status("claim", 504, "")), 0.0);
        assert!(sends.due(3.0).is_empty());
        sends.wake(3.0);
        assert_eq!(sends.due(3.0).len(), 1);
    }

    #[test]
    fn an_unreachable_daemon_keeps_an_unconfirmed_send_and_drops_a_fresh_one() {
        let unreached = Err(RespondError { message: "down".into(), code: String::new(), maybe_delivered: false, reply: Value::Null, answered: false, status: 0 });
        let mut sends = Sends::default();
        sends.sending(&work("open", None), Move::Claim, "");
        assert_eq!(sends.answered(&k6(), Move::Claim, &unreached, 0.0), (true, "Not done: down".into()));
        assert!(sends.get(&k6(), Move::Claim).is_none());
        sends.sending(&work("open", None), Move::Claim, "");
        sends.answered(&k6(), Move::Claim, &Err(RespondError::from_status("claim", 504, "")), 0.0);
        let (failed, note) = sends.answered(&k6(), Move::Claim, &unreached, 10.0);
        assert!(failed && note.starts_with("Agenthail didn't settle it: down."));
        assert_eq!(sends.held(&k6()), Some(Move::Claim));
        assert!(sends.due(29.9).is_empty() && !sends.due(30.0).is_empty());
    }

    #[test]
    fn a_resend_sends_the_first_body_and_the_same_id_elsewhere_is_its_own_send() {
        let (client, seen) = daemon(replies(&[(504, ""), (200, r#"{"ok":true}"#)]));
        let mut sends = Sends::default();
        sent(&mut sends, &client, &work("open", None), Move::Claim, "", 0.0);
        let first = seen.recv().unwrap().1;
        assert_eq!(first["projectKey"], "project:p-1");
        let key = sends.held_key(&k6(), Move::Claim).unwrap().to_string();
        let other = Work { ledger_key: "project:p-2".into(), ..work("open", None) };
        let (other_key, other_body) = sends.sending(&other, Move::Claim, "");
        assert_ne!(other_key, key, "the same id in another ledger is another item");
        assert_eq!(other_body["projectKey"], "project:p-2");
        assert_eq!(sends.held(&k6()), Some(Move::Claim));
        assert_eq!(sends.held(&WorkKey::new("project:p-2", "p-W6")), None);
        sent(&mut sends, &client, &work("open", None), Move::Claim, "", 10.0);
        assert_eq!(seen.recv().unwrap().1, first, "the resend posts the first body");
        assert_eq!(sends.held(&k6()), None);
    }

    #[test]
    fn a_reconnect_during_a_resend_asks_again_once_it_returns() {
        let lost = Err(RespondError::from_status("claim", 504, ""));
        let w = work("open", None);
        let mut sends = Sends::default();
        sends.sending(&w, Move::Claim, "");
        sends.answered(&k6(), Move::Claim, &lost, 0.0);
        sends.sending(&w, Move::Claim, "");
        sends.wake(12.0);
        sends.answered(&k6(), Move::Claim, &lost, 13.0);
        assert_eq!(sends.due(13.0).len(), 1, "the reconnect isn't lost to the backoff");
        sends.sending(&w, Move::Claim, "");
        sends.answered(&k6(), Move::Claim, &lost, 14.0);
        assert!(sends.due(14.0 + 39.9).is_empty(), "a reconnect asks once, then the backoff resumes");
    }

    #[test]
    fn a_held_send_is_known_by_its_key_until_it_settles() {
        let w = work("open", None);
        let mut sends = Sends::default();
        let (key, _) = sends.sending(&w, Move::Comment, "hi");
        assert_eq!(sends.held_key(&k6(), Move::Comment), None, "a send in flight isn't held yet");
        sends.answered(&k6(), Move::Comment, &Err(RespondError::from_status("comment", 504, "")), 0.0);
        assert_eq!(sends.held_key(&k6(), Move::Comment), Some(key.as_str()));
        sends.answered(&k6(), Move::Comment, &Ok(json!({"ok": true})), 10.0);
        assert_eq!(sends.held_key(&k6(), Move::Comment), None, "a dialog opened on it can tell it settled");
        assert_ne!(sends.sending(&w, Move::Comment, "hi").0, key, "a later send is a new one");
    }

    #[test]
    fn a_reconnect_during_the_first_send_asks_again_once_it_returns() {
        let mut sends = Sends::default();
        let (key, body) = sends.sending(&work("open", None), Move::Claim, "");
        sends.wake(2.0);
        sends.answered(&k6(), Move::Claim, &Err(RespondError::from_status("claim", 504, "")), 3.0);
        assert_eq!(sends.due(3.0).len(), 1, "the reconnect isn't lost to the first wait");
        assert_eq!(sends.resending(&k6(), Move::Claim), Some((key, body)));
        sends.answered(&k6(), Move::Claim, &Err(RespondError::from_status("claim", 504, "")), 4.0);
        assert!(sends.due(4.0 + 19.9).is_empty(), "then the backoff resumes");
    }

    #[test]
    fn a_held_send_resends_without_its_item() {
        let mut sends = Sends::default();
        let (key, body) = sends.sending(&work("open", None), Move::Park, "later");
        assert_eq!(sends.resending(&k6(), Move::Park), None, "only a held send is asked about again");
        sends.answered(&k6(), Move::Park, &Err(RespondError::from_status("park", 504, "")), 0.0);
        assert_eq!(sends.resending(&k6(), Move::Park), Some((key, body)));
    }
}

