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

pub fn said(w: &Work, words: &str) -> usize {
    w.timeline.iter().filter(|t| t.kind == "comment" && t.actor == PERSON && t.text.as_deref().map(str::trim) == Some(words.trim())).count()
}

pub fn landed(w: &Work, m: Move, words: &str, before: usize) -> bool {
    match m {
        Move::Comment => said(w, words) > before,
        _ => !offered(w).contains(&m),
    }
}

pub fn settled_note(w: &Work, m: Move) -> String {
    match m {
        Move::Comment => m.done().into(),
        _ => format!("Settled: it's {} now.", crate::derive::phase::state_word(&w.state)),
    }
}

pub fn settles(e: &RespondError) -> bool {
    e.answered && (400..500).contains(&e.status)
}

pub fn unreached(e: &RespondError) -> String {
    format!("Agenthail didn't settle it: {}. The earlier send is still unconfirmed; pressing again asks about it.", e.message)
}

pub fn unconfirmed(e: &RespondError, again: bool) -> String {
    if again {
        format!("Still not confirmed: {}. If it never shows up, Send as new starts a fresh one.", e.message)
    } else {
        format!("Sent, not confirmed: {}. It settles here once the item shows it; pressing again asks about the same send.", e.message)
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
    fn an_unconfirmed_move_settles_once_the_item_shows_it() {
        let entry = |actor: &str, text: &str| crate::derive::Entry { actor: actor.into(), kind: "comment".into(), text: Some(text.into()), ..Default::default() };
        let mut w = work("open", None);
        w.timeline.push(entry(PERSON, "looks right"));
        let before = said(&w, "looks right");
        assert_eq!(before, 1);
        assert!(!landed(&w, Move::Comment, "looks right", before), "the same words said before don't settle a new send");
        w.timeline.push(entry("worker", "looks right"));
        assert!(!landed(&w, Move::Comment, "looks right", before), "someone else's words don't settle it");
        w.timeline.push(entry(PERSON, "looks right "));
        assert!(landed(&w, Move::Comment, " looks right", before));
        assert!(!landed(&work("open", None), Move::Claim, "", 0));
        let claimed = work("in_progress", Some(PERSON));
        assert!(landed(&claimed, Move::Claim, "", 0));
        assert_eq!(settled_note(&claimed, Move::Claim), "Settled: it's Build now.");
        assert!(landed(&work("parked", None), Move::Park, "later", 0));
        assert_eq!(settled_note(&w, Move::Comment), "Comment added.");
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
        assert_eq!(unconfirmed(&lost, false), "Sent, not confirmed: Host mac did not answer. It settles here once the item shows it; pressing again asks about the same send.");
        let unsent = Client::new("http://127.0.0.1:0", "secret").work_move(&w, Move::Comment, "hi", "k4").unwrap_err();
        assert!(!unsent.answered && !unsent.maybe_delivered);
        assert!(!settles(&unsent));
        assert!(!settles(&RespondError::from_status("claim", 500, r#"{"error":{"code":"action_reservation_failed","message":"busy"}}"#)));
        assert!(settles(&RespondError::from_status("claim", 409, r#"{"error":{"code":"refused","message":"no"}}"#)));
        assert!(unreached(&unsent).starts_with("Agenthail didn't settle it: "));
        assert!(unreached(&unsent).ends_with("The earlier send is still unconfirmed; pressing again asks about it."));
        assert_eq!(unconfirmed(&lost, true), "Still not confirmed: Host mac did not answer. If it never shows up, Send as new starts a fresh one.");
    }

    #[test]
    fn a_pending_receipt_is_not_taken_as_done() {
        let (client, _seen) = daemon(replies(&[(202, r#"{"ok":true,"status":"submitted"}"#)]));
        let e = client.work_move(&work("open", None), Move::Comment, "hi", "k").unwrap_err();
        assert!(e.maybe_delivered && e.code == "submitted");
    }
}
