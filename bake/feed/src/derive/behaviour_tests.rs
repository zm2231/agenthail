use std::collections::HashSet;

use serde_json::{Value, json};

use super::*;

const NOW: &str = "2026-11-24T12:00:00Z";

fn session(id: &str, extra: Value) -> Value {
    let mut s = json!({"id": id, "name": id, "lastActive": "2026-11-24T11:50:00Z", "project": {"id": "p", "name": "P"}, "display": {"kind": "idle"}});
    s.as_object_mut().unwrap().extend(extra.as_object().unwrap().clone());
    s
}

fn said(at: &str, text: &str) -> Value {
    json!({"id": at, "kind": "message", "role": "user", "text": text, "timestamp": at})
}

fn world(v: Value) -> World {
    let mut base = json!({"updatedAt": NOW, "projects": [{"id": "p", "displayName": "P"}]});
    base.as_object_mut().unwrap().extend(v.as_object().unwrap().clone());
    World::from_json(&base.to_string()).unwrap()
}

#[test]
fn hands_on_skips_first_prompts_agent_messages_and_agent_started_sessions() {
    let w = world(json!({
        "sessions": [session("steered", json!({})), session("first-only", json!({})), session("relayed", json!({})), session("worker", json!({"startedBy": "steered"})), session("helper", json!({"subagent": {}}))],
        "transcripts": {
            "steered": [said("2026-11-24T10:00:00Z", "start"), said("2026-11-24T11:45:00Z", "try again")],
            "first-only": [said("2026-11-24T11:55:00Z", "start")],
            "relayed": [said("2026-11-24T10:00:00Z", "start"), said("2026-11-24T11:55:00Z", "<cross-session-message from=x>hi"), said("2026-11-24T11:56:00Z", "[from @lead] hi")],
            "worker": [said("2026-11-24T11:00:00Z", "start"), said("2026-11-24T11:55:00Z", "go")],
            "helper": [said("2026-11-24T11:00:00Z", "start"), said("2026-11-24T11:55:00Z", "go")]
        }
    }));
    let ids: Vec<&str> = w.hands_on(&[]).iter().map(|s| s.id.as_str()).collect();
    assert_eq!(ids, ["steered"]);
}

#[test]
fn a_pinned_session_stays_hands_on_after_the_person_goes_quiet() {
    let w = world(json!({
        "sessions": [session("quiet", json!({})), session("steered", json!({}))],
        "transcripts": {"steered": [said("2026-11-24T10:00:00Z", "start"), said("2026-11-24T11:45:00Z", "go")]}
    }));
    let ids: Vec<&str> = w.hands_on(&["quiet".into()]).iter().map(|s| s.id.as_str()).collect();
    assert_eq!(ids, ["quiet", "steered"]);
}

#[test]
fn a_worker_started_by_a_worker_is_listed_under_it() {
    let w = world(json!({"sessions": [session("lead", json!({})), session("worker", json!({"startedBy": "lead"})), session("helper", json!({"startedBy": "worker"}))]}));
    let rows: Vec<(&str, u8)> = w.ordered_with_crew(w.sessions.iter().collect(), &HashSet::new()).into_iter().map(|(s, d)| (s.id.as_str(), d)).collect();
    assert_eq!(rows, [("lead", 0), ("worker", 1), ("helper", 2)]);
}

#[test]
fn the_timeline_marks_a_move_back_to_an_earlier_phase() {
    let w = world(json!({"ledgers": {"p": [{"id": "p-W1", "state": "in_progress", "timeline": [
        {"at": "2026-11-24T09:00:00Z", "kind": "state", "state": "in_review"},
        {"at": "2026-11-24T10:00:00Z", "kind": "dep"},
        {"at": "2026-11-24T11:00:00Z", "kind": "state", "state": "in_progress"}
    ]}]}}));
    let steps = timeline::timeline(&w.work["p-W1"], 10);
    let shown: Vec<(&str, bool)> = steps.iter().map(|s| (s.what.as_str(), s.moved_back)).collect();
    assert_eq!(shown, [("Moved back to Build", true), ("Moved to Check", false)]);
}

#[test]
fn a_review_past_the_window_leaves_needs_but_stays_on_the_board() {
    let review = |id: &str, at: &str| json!({"id": id, "state": "in_review", "timeline": [{"at": at, "kind": "state", "state": "in_review"}]});
    let w = world(json!({
        "sessions": [session("s", json!({}))],
        "ledgers": {"p": [review("p-W1", "2026-11-24T09:00:00Z"), review("p-W2", "2026-11-20T09:00:00Z")]}
    }));
    let needs: Vec<Target> = w.needs().into_iter().map(|n| n.target).collect();
    assert_eq!(needs, [Target::Work("p-W1".into())]);
    let board = w.board("p");
    assert_eq!(board.lanes[0].iter().map(|x| x.id.as_str()).collect::<Vec<_>>(), ["p-W1"]);
    assert_eq!(board.stale_review.iter().map(|x| x.id.as_str()).collect::<Vec<_>>(), ["p-W2"]);
}

#[test]
fn an_ended_worker_is_named_by_its_alias() {
    let w = world(json!({"actors": {"gone": {"alias": "fixer", "surface": "codex"}, "nameless": {"surface": "codex"}}}));
    assert_eq!(w.name_of("gone"), "@fixer");
    assert_eq!(w.name_of("nameless"), "Codex namele");
}
