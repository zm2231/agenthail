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

#[test]
fn ties_resolve_the_same_way_on_every_load() {
    let item = |id: &str, state: &str, at: &str| json!({"id": id, "text": "Same title", "state": state, "priority": "P1", "timeline": [{"at": at, "kind": "state", "state": state}]});
    let mut rows = Vec::new();
    for i in 0..6 {
        rows.push(item(&format!("p-R{i}"), "ready", "2026-11-24T10:00:00Z"));
        rows.push(item(&format!("p-V{i}"), "in_review", "2026-11-24T10:00:00Z"));
        rows.push(item(&format!("p-D{i}"), "done", "2026-11-24T10:00:00Z"));
    }
    let snapshot = json!({"sessions": [session("s", json!({}))], "ledgers": {"p": rows}});
    let order = || {
        let w = world(snapshot.clone());
        let p = w.project("p").unwrap().clone();
        let needs: Vec<Target> = w.needs().into_iter().map(|n| n.target).collect();
        let digest: Vec<Target> = w.digest(&p, WINDOW_HOURS).into_iter().map(|l| l.target).collect();
        let next: Vec<String> = w.board("p").lanes[2].iter().map(|x| x.id.clone()).collect();
        (needs, digest, next)
    };
    let first = order();
    assert_eq!(first.2, (0..6).map(|i| format!("p-R{i}")).collect::<Vec<_>>());
    for _ in 0..16 {
        assert_eq!(order(), first);
    }
}

fn asks(at: &str) -> Value {
    json!({"endTurn": {"headline": "Which branch should this land on?", "outcome": "needs_you", "at": at}})
}

fn with(mut a: Value, b: Value) -> Value {
    a.as_object_mut().unwrap().extend(b.as_object().unwrap().clone());
    a
}

#[test]
fn needs_hold_up_work_only_when_something_running_waits_on_you() {
    let at = "2026-11-24T11:00:00Z";
    let w = world(json!({
        "sessions": [
            session("ask", with(asks(at), json!({"attention": {"kind": "ask"}}))),
            session("permission", with(asks(at), json!({"attention": {"kind": "permission"}}))),
            session("stalled", with(asks(at), json!({"turnState": {"state": "waiting_person"}}))),
            session("running", with(asks(at), json!({"status": "busy"}))),
            session("worker", with(asks(at), json!({"startedBy": "lead"}))),
            session("holder", with(asks(at), json!({"heldWork": [{"itemId": "p-W1", "state": "ready"}]}))),
            session("queued", with(asks(at), json!({"queueCount": 2}))),
            session("idle", asks(at)),
            session("parked", with(asks(at), json!({"heldWork": [{"itemId": "p-W3", "state": "in_progress"}]}))),
            session("lead", json!({})),
            session("finished", json!({"turnCompletion": {"headline": "Wrote the migration", "outcome": "finished", "at": "2026-11-24T11:59:00Z"}})),
        ],
        "ledgers": {"p": [
            {"id": "p-W1", "text": "Held", "state": "in_progress", "timeline": [{"at": at, "kind": "state", "state": "in_progress"}]},
            {"id": "p-W2", "text": "Review me", "state": "in_review", "timeline": [{"at": at, "kind": "state", "state": "in_review"}]},
            {"id": "p-W3", "text": "Parked", "state": "blocked", "timeline": [{"at": at, "kind": "state", "state": "blocked"}]}
        ]}
    }));
    let needs = w.needs();
    let of = |g: Group| -> Vec<String> {
        let mut ids: Vec<String> = needs.iter().filter(|n| n.group == g).map(|n| match &n.target {
            Target::Session(id) | Target::Work(id) | Target::Project(id) => id.clone(),
        }).collect();
        ids.sort();
        ids
    };
    assert_eq!(of(Group::Holding), ["ask", "holder", "permission", "queued", "running", "stalled", "worker"]);
    assert_eq!(of(Group::Waiting), ["finished", "idle", "p-W2", "parked"]);
    let first_waiting = needs.iter().position(|n| n.group == Group::Waiting).unwrap();
    assert!(needs[first_waiting..].iter().all(|n| n.group == Group::Waiting), "groups interleave");
}

#[test]
fn what_arrived_after_you_looked_goes_above_the_cut() {
    let w = world(json!({"sessions": [
        session("old-q", asks("2026-11-24T09:00:00Z")),
        session("new-q", asks("2026-11-24T11:30:00Z")),
        session("old-done", json!({"turnCompletion": {"headline": "Old", "outcome": "needs_you", "at": "2026-11-24T08:00:00Z"}})),
    ]}));
    let ids = |needs: &[Need]| -> Vec<String> { needs.iter().map(|n| match &n.target { Target::Session(id) => id.clone(), other => format!("{other:?}") }).collect() };
    let mut needs = w.needs();
    assert_eq!(ids(&needs), ["old-done", "old-q", "new-q"]);
    let looked = Some("2026-11-24T10:00:00Z");
    needs::arrange(&mut needs, looked);
    assert_eq!(ids(&needs), ["new-q", "old-done", "old-q"]);
    assert!(needs::is_new(&needs[0].at, looked) && !needs[1..].iter().any(|n| needs::is_new(&n.at, looked)));
    needs::arrange(&mut needs, None);
    assert_eq!(ids(&needs), ["old-done", "old-q", "new-q"]);
}

#[test]
fn since_you_left_reaches_back_to_when_you_looked() {
    let w = world(json!({
        "sessions": [session("s", json!({}))],
        "ledgers": {"p": [
            {"id": "p-A", "text": "Shipped long ago", "state": "done", "timeline": [{"at": "2026-11-23T20:00:00Z", "kind": "state", "state": "done"}]},
            {"id": "p-B", "text": "Shipped before you looked", "state": "done", "timeline": [{"at": "2026-11-24T09:00:00Z", "kind": "state", "state": "done"}]},
            {"id": "p-C", "text": "Shipped after", "state": "done", "timeline": [{"at": "2026-11-24T11:00:00Z", "kind": "state", "state": "done"}]},
            {"id": "p-D", "text": "Still waiting", "state": "in_review", "timeline": [{"at": "2026-11-24T08:00:00Z", "kind": "state", "state": "in_review"}]}
        ]}
    }));
    let p = w.project("p").unwrap().clone();
    let text = |lines: &[&Line]| -> Vec<String> { lines.iter().map(|l| l.text.clone()).collect() };
    assert_eq!(w.window(None), WINDOW_HOURS);
    assert_eq!(w.window(Some("not a time")), WINDOW_HOURS);
    let looked = Some("2026-11-24T10:00:00Z");
    assert_eq!(w.window(looked), 2.0);
    let lines = w.digest(&p, w.window(looked));
    let (new, old) = digest::cut(lines.iter().collect(), looked);
    assert_eq!(text(&new), ["Shipped after"]);
    assert_eq!(text(&old), ["Still waiting"]);
    let all = w.digest(&p, w.window(None));
    let (new, old) = digest::cut(all.iter().collect(), None);
    assert!(new.is_empty());
    assert_eq!(text(&old), ["Shipped after", "Shipped before you looked", "Still waiting"]);
}

#[test]
fn jump_finds_every_listed_session_with_or_without_a_project() {
    let w = world(json!({"sessions": [
        session("busyprobe", json!({"project": null})),
        session("helper-run", json!({"project": null, "alias": "probe-helper", "name": "<task>"})),
        session("old-probe", json!({"archived": true})),
        session("closed-probe", json!({"display": {"kind": "idle", "label": "Closed"}})),
        session("other", json!({}))
    ]}));
    let found = |q: &str| -> Vec<Target> { w.jump(q, &[], &[]).into_iter().filter(|h| h.group == "Sessions").map(|h| h.target).collect() };
    assert_eq!(found("busyprobe"), [Target::Session("busyprobe".into())]);
    assert_eq!(found("probe-helper"), [Target::Session("helper-run".into())]);
    assert_eq!(found("old-probe"), [Target::Session("old-probe".into())]);
    assert!(found("closed-probe").is_empty());
    let hit = w.jump("busyprobe", &[], &[]).into_iter().find(|h| h.group == "Sessions").unwrap();
    assert_eq!(hit.badge, "No project");
}
