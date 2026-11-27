use serde_json::{Value, json};

use super::*;
use crate::mock::{daemon, replies};

fn session(v: Value) -> Session {
    serde_json::from_value(v).unwrap()
}

fn page(v: Value) -> Page {
    serde_json::from_value(v).unwrap()
}

fn ids(q: &Queue) -> Vec<&str> {
    q.items.iter().map(|i| i.id.as_str()).collect()
}

#[test]
fn only_codex_sessions_show_the_queue_and_read_only_ones_cannot_change_it() {
    assert!(shown(&session(json!({"id": "c", "surface": "codex"}))) && writable(&session(json!({"id": "c", "surface": "codex"}))));
    let closed = session(json!({"id": "c", "surface": "codex", "readOnly": true}));
    assert!(shown(&closed) && !writable(&closed));
    assert!(!shown(&session(json!({"id": "a", "surface": "claude"}))));
}

#[test]
fn rows_read_like_native() {
    let p = page(json!({"data": [
        {"id": "q1", "clientUserMessageId": "k", "input": [{"type": "text", "text": "Run the tests"}, {"type": "text", "text": "then push"}]},
        {"id": "q2", "input": [{"type": "text", "text": "Look"}, {"type": "localImage", "path": "/tmp/a.png"}, {"type": "mention", "name": "README.md"}, {"type": "skill"}]},
        {"id": "q3", "input": []}
    ], "nextCursor": null}));
    assert_eq!(p.data[0].text(), "Run the tests\nthen push");
    assert_eq!(p.data[0].editable_text().as_deref(), Some("Run the tests\nthen push"));
    assert_eq!(p.data[1].text(), "Look\nImage\nREADME.md\nskill");
    assert_eq!(p.data[1].editable_text(), None);
    assert_eq!(p.data[2].shown_text(), EMPTY_INPUT);
    assert_eq!(p.next_cursor, None);
}

#[test]
fn moving_swaps_neighbours_and_stops_at_the_ends() {
    let ids: Vec<String> = ["a", "b", "c"].map(String::from).to_vec();
    assert_eq!(moving(&ids, "b", -1), Some(vec!["b".into(), "a".into(), "c".into()]));
    assert_eq!(moving(&ids, "b", 1), Some(vec!["a".into(), "c".into(), "b".into()]));
    assert_eq!(moving(&ids, "a", -1), None);
    assert_eq!(moving(&ids, "c", 1), None);
    assert_eq!(moving(&ids, "z", 1), None);
    assert_eq!(moving(&ids, "a", 0), None);
}

#[test]
fn reorder_needs_the_whole_queue() {
    let mut q = Queue::default();
    q.reset("s");
    let (s, g) = q.begin().unwrap();
    q.listed(&s, g, false, Ok(page(json!({"data": [{"id": "a"}, {"id": "b"}], "nextCursor": "p2"}))));
    assert!(!q.can_reorder() && q.moved("a", 1).is_none(), "a partial list can't be reordered");
    let (s, g) = q.begin().unwrap();
    q.listed(&s, g, true, Ok(page(json!({"data": [{"id": "b"}, {"id": "c"}], "nextCursor": ""}))));
    assert_eq!(ids(&q), ["a", "b", "c"], "load more skips rows it already has");
    assert!(q.can_reorder());
    assert_eq!(q.moved("c", -1), Some(Act::Reorder(vec!["a".into(), "c".into(), "b".into()])));
}

#[test]
fn a_reply_for_another_session_or_an_older_generation_is_dropped() {
    let mut q = Queue::default();
    q.reset("s1");
    let (s, g) = q.begin().unwrap();
    assert!(q.begin().is_none(), "one request at a time");
    q.reset("s2");
    assert!(!q.busy && !q.loaded);
    assert!(!q.listed(&s, g, false, Ok(page(json!({"data": [{"id": "old"}]})))));
    assert!(q.items.is_empty());
    q.reset("s2");
    let (s2, g2) = q.begin().unwrap();
    assert!(q.busy, "resetting to the same session keeps its request");
    assert!(q.listed(&s2, g2, false, Err("offline".into())));
    assert!(!q.loaded && q.error.as_deref() == Some("offline"));
    let (s2, g2) = q.begin().unwrap();
    assert!(q.mutated(&s2, g2, &Outcome::Done), "a done change reloads");
    let (s2, g2) = q.begin().unwrap();
    assert!(!q.mutated(&s2, g2, &Outcome::Failed("nope".into())));
    assert_eq!(q.error.as_deref(), Some("nope"));
}

#[test]
fn outcomes_follow_the_daemon_codes() {
    let failed = |code: &str, status: u16, maybe: bool| RespondError { message: "boom".into(), code: code.into(), maybe_delivered: maybe, reply: Value::Null, answered: true, status };
    assert_eq!(Outcome::of(Ok(())), Outcome::Done);
    assert_eq!(Outcome::of(Err(failed("session_operation_failed", 502, true))), Outcome::Failed("boom".into()));
    assert_eq!(Outcome::of(Err(failed("session_operation_unknown", 502, true))), Outcome::Unknown("boom The outcome is unknown; check the session before retrying.".into()));
    assert_eq!(Outcome::of(Err(failed("host_outcome_unknown", 502, true))), Outcome::Unknown("boom The outcome is unknown; check the session before retrying.".into()));
    assert_eq!(Outcome::of(Err(failed("submitted", 0, true))), Outcome::Pending("Agenthail is still finishing this request. The outcome is unknown; check the session before retrying.".into()));
    assert!(matches!(Outcome::of(Err(failed("", 0, true))), Outcome::Pending(_)), "a dropped connection after sending is still pending");
    assert!(matches!(Outcome::of(Err(failed("", 0, false))), Outcome::Failed(_)), "a request that never left is a plain failure");
    assert_eq!(Outcome::Pending("x".into()).error(), Some("x"));
    assert_eq!(Outcome::Done.error(), None);
}

#[test]
fn a_retry_after_an_unknown_outcome_reuses_its_key_until_a_later_read() {
    let mut keys = Keys::default();
    let add = Act::Add("Ship it".into());
    let first = keys.key("s", &add);
    let early = keys.read_started();
    keys.settled(&first, &Outcome::Unknown("lost".into()));
    assert_eq!(keys.key("s", &add), first, "the same add retries with the same key");
    assert_ne!(keys.key("s", &Act::Add("Other".into())), first);
    assert_ne!(keys.key("t", &add), first, "another session's add is its own request");
    keys.observed("s", early);
    assert_eq!(keys.key("s", &add), first, "a read that started before the answer doesn't count");
    let late = keys.read_started();
    keys.observed("s", late);
    assert_ne!(keys.key("s", &add), first, "after a later read the next add is a new request");
}

#[test]
fn a_still_running_request_keeps_its_key_across_reads() {
    let mut keys = Keys::default();
    let add = Act::Add("Ship it".into());
    let first = keys.key("s", &add);
    keys.settled(&first, &Outcome::Pending("still finishing".into()));
    let read = keys.read_started();
    keys.observed("s", read);
    assert_eq!(keys.key("s", &add), first, "a submitted add retries with the same key and message id");
    keys.settled(&first, &Outcome::Done);
    assert_ne!(keys.key("s", &add), first);
}

#[test]
fn a_late_answer_only_settles_its_own_request() {
    let mut keys = Keys::default();
    let del = Act::Delete("q1".into());
    let add = Act::Add("Ship it".into());
    let old = keys.key("s", &del);
    let newer = keys.key("s", &add);
    keys.settled(&newer, &Outcome::Pending("still finishing".into()));
    keys.settled(&old, &Outcome::Failed("nope".into()));
    assert_ne!(keys.key("s", &del), old, "a definite answer clears its key");
    assert_eq!(keys.key("s", &add), newer, "and leaves the other request's key alone");
}

#[test]
fn a_change_during_a_read_asks_for_another_read() {
    let mut q = Queue::default();
    q.reset("s");
    assert!(q.invalidate("s"), "idle: read now");
    assert!(!q.invalidate("t"), "another session's change is ignored");
    let (s, g) = q.begin().unwrap();
    assert!(!q.invalidate("s"), "busy: read after this one");
    q.listed(&s, g, true, Ok(page(json!({"data": [{"id": "a"}, {"id": "b"}]}))));
    assert!(q.take_dirty(), "the stale page is followed by a fresh read");
    assert!(!q.take_dirty());
    let (s, g) = q.begin().unwrap();
    assert!(!q.can_reorder(), "no reorder while a read or change is running");
    q.invalidate("s");
    assert!(q.mutated(&s, g, &Outcome::Done));
    assert!(!q.take_dirty(), "a done change reloads anyway");
}

#[test]
fn every_queue_action_sends_the_body_the_daemon_reads() {
    let (client, seen) = daemon(replies(&[
        (200, r#"{"ok":true,"result":{"data":[{"id":"q1","input":[{"type":"text","text":"Hi"}]}],"nextCursor":"c2"}}"#),
        (200, r#"{"ok":true,"result":{"data":[]}}"#),
        (200, r#"{"ok":true,"result":{"queuedSubmissionId":"q2"}}"#),
        (200, r#"{"ok":true,"result":{}}"#),
        (200, r#"{"ok":true,"result":{}}"#),
        (200, r#"{"ok":true,"result":{}}"#),
        (202, r#"{"ok":true,"status":"submitted"}"#),
        (502, r#"{"error":{"code":"session_operation_failed","message":"queued submission not found"}}"#),
        (200, r#"{"ok":true}"#),
    ]));
    let p = client.native_queue_list("h::s1", None).unwrap();
    assert_eq!((p.data[0].text(), p.next_cursor.as_deref()), ("Hi".to_string(), Some("c2")));
    assert_eq!(seen.recv().unwrap().1, json!({"action": "native-queue", "sessionId": "h::s1", "nativeQueue": {"queueAction": "list"}}));
    assert!(client.native_queue_list("s1", Some("c2")).unwrap().data.is_empty());
    assert_eq!(seen.recv().unwrap().1, json!({"action": "native-queue", "sessionId": "s1", "nativeQueue": {"queueAction": "list", "cursor": "c2"}}));
    client.native_queue("s1", &Act::Add("Then tag".into()), "k1").unwrap();
    assert_eq!(seen.recv().unwrap().1, json!({"action": "native-queue", "sessionId": "s1", "nativeQueue": {"queueAction": "add", "message": "Then tag", "clientUserMessageId": "k1"}}));
    client.native_queue("s1", &Act::Update { id: "q1".into(), text: "Edited".into() }, "k2").unwrap();
    assert_eq!(seen.recv().unwrap().1, json!({"action": "native-queue", "sessionId": "s1", "nativeQueue": {"queueAction": "update", "message": "Edited", "queuedSubmissionId": "q1"}}));
    client.native_queue("s1", &Act::Reorder(vec!["q2".into(), "q1".into()]), "k3").unwrap();
    assert_eq!(seen.recv().unwrap().1, json!({"action": "native-queue", "sessionId": "s1", "nativeQueue": {"queueAction": "reorder", "queuedSubmissionIds": ["q2", "q1"]}}));
    client.native_queue("s1", &Act::Start(Some("q2".into())), "k4").unwrap();
    assert_eq!(seen.recv().unwrap().1, json!({"action": "native-queue", "sessionId": "s1", "nativeQueue": {"queueAction": "start", "queuedSubmissionId": "q2"}}));
    let submitted = Outcome::of(client.native_queue("s1", &Act::Start(None), "k5"));
    assert_eq!(seen.recv().unwrap().1, json!({"action": "native-queue", "sessionId": "s1", "nativeQueue": {"queueAction": "start"}}));
    assert!(matches!(submitted, Outcome::Pending(_)));
    let gone = Outcome::of(client.native_queue("s1", &Act::Delete("q9".into()), "k6"));
    assert_eq!(seen.recv().unwrap().1, json!({"action": "native-queue", "sessionId": "s1", "nativeQueue": {"queueAction": "delete", "queuedSubmissionId": "q9"}}));
    assert_eq!(gone, Outcome::Failed("queued submission not found".into()));
    assert!(client.native_queue_list("s1", None).is_err(), "a reply without a result is an error");
}

#[test]
fn saving_needs_a_changed_non_empty_text() {
    assert!(can_save("", "New"));
    assert!(!can_save("", "   "));
    assert!(!can_save("Same", " Same \n"));
    assert!(can_save("Same", "Different"));
}
