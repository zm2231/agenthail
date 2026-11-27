use serde_json::json;

use super::live::{Known, ledger};
use super::phase::{Steps, step_word};
use super::world::{Recorded, World};
use crate::mock::{daemon, replies};
use crate::worksearch::{Found, Search};

const RENAMED: &str = r#"{"scope":"Scope","build":"Make","review":"Review","verify":"Verify","check":"Sign-off","ship":"Ship"}"#;

fn world(ledgers: serde_json::Value) -> World {
    let raw: Recorded = serde_json::from_value(json!({
        "updatedAt": "2026-11-27T12:00:00Z",
        "projects": [{"id": "p-1", "displayName": "Atlas", "ledgerKey": "project:p-1"}, {"id": "p-2", "displayName": "Birch", "ledgerKey": "project:p-2"}],
        "ledgers": ledgers,
    }))
    .unwrap();
    World::new(raw)
}

#[test]
fn a_project_that_renames_two_steps_reads_them_with_its_work_rows() {
    let page = |rows: &str, cursor: &str| format!(r#"{{"work":[{rows}],"phaseLabels":{RENAMED},"nextCursor":"{cursor}"}}"#);
    let (client, seen) = daemon(replies(&[
        (200, &page(r#"{"id":"p-1-W1","text":"Wire the export","state":"in_progress","phase":"build"}"#, "c2")),
        (200, &page(r#"{"id":"p-1-W2","text":"Check the export","state":"in_review","phase":"check"}"#, "")),
    ]));
    let got = ledger(&client, "project:p-1", &Known::default()).unwrap();
    assert_eq!(seen.recv().unwrap().0, "/api/v1/ledger?projectKey=project%3Ap-1&view=work&limit=100");
    assert!(seen.recv().unwrap().0.ends_with("&cursor=c2"));
    assert_eq!(got.rows.len(), 2);
    for w in &got.rows {
        assert_eq!(w.steps.names().collect::<Vec<_>>(), ["Scope", "Make", "Review", "Verify", "Sign-off", "Ship"], "{}", w.id);
    }
    assert_eq!(step_word(&got.rows[0].phase, &got.rows[0].state, &got.rows[0].steps), "Make");
    assert_eq!(step_word(&got.rows[1].phase, &got.rows[1].state, &got.rows[1].steps), "Sign-off");
}

#[test]
fn a_project_with_no_names_of_its_own_shows_the_default_steps() {
    let (client, _seen) = daemon(replies(&[(200, r#"{"work":[{"id":"p-2-W1","text":"Plan it","state":"in_progress","phase":"build"}],"phaseLabels":{"scope":"Scope","build":"Build","review":"Review","verify":"Verify","check":"Check","ship":"Ship"}}"#)]));
    let got = ledger(&client, "project:p-2", &Known::default()).unwrap();
    assert_eq!(got.rows[0].steps, Steps::default());
    let (client, _older) = daemon(replies(&[(200, r#"{"work":[{"id":"p-2-W1","text":"Plan it","state":"in_progress","phase":"build"}]}"#)]));
    let older = ledger(&client, "project:p-2", &Known::default()).unwrap();
    assert_eq!(step_word(&older.rows[0].phase, &older.rows[0].state, &older.rows[0].steps), "Build");
}

#[test]
fn jump_to_and_work_search_name_each_projects_own_steps() {
    let renamed: serde_json::Value = serde_json::from_str(RENAMED).unwrap();
    let w = world(json!({
        "p-1": [{"id": "p-1-W1", "text": "Wire the export", "state": "in_progress", "phase": "build", "phaseLabels": renamed}, {"id": "p-1-W2", "text": "Check the export", "state": "in_review", "phase": "check", "phaseLabels": renamed}],
        "p-2": [{"id": "p-2-W1", "text": "Wire the import", "state": "in_progress", "phase": "build"}],
    }));
    let mut status: Vec<(String, String)> = w.jump("wire", &[], &[]).into_iter().filter(|h| h.group == "Work").map(|h| (h.title, h.status.split(" · ").next().unwrap().to_string())).collect();
    status.sort();
    assert_eq!(status, [("Wire the export".to_string(), "Make".to_string()), ("Wire the import".to_string(), "Build".to_string())]);
    let check = w.jump("check the", &[], &[]).into_iter().find(|h| h.group == "Work").unwrap();
    assert!(check.status.starts_with("Sign-off · "), "{}", check.status);
    let found = |key: &str, id: &str, phase: &str| Found { project_key: key.into(), project_name: "X".into(), id: id.into(), state: "in_progress".into(), phase: phase.into(), ..Found::default() };
    let search = Search { items: vec![found("project:p-1", "p-1-W7", "check"), found("project:p-2", "p-2-W7", "check"), found("project:p-9", "p-9-W1", "build")], truncated: false };
    let rows: Vec<String> = w.found_rows(&search, |_| false).into_iter().map(|r| r.status).collect();
    assert_eq!(rows, ["Sign-off · X", "Check · X", "Build · X"]);
}

#[test]
fn a_work_item_read_carries_its_projects_step_names() {
    let reply = format!(r#"{{"kind":"work","item":{{"item":{{"id":"p-1-W1","state":"in_progress","phase":"build"}}}},"phaseLabels":{RENAMED}}}"#);
    let (client, _seen) = daemon(replies(&[(200, &reply), (200, &reply)]));
    let item = client.work_item("project:p-1", "p-1-W1").unwrap();
    assert_eq!(item.steps.name(1), "Make");
    let found = client.found_work("project:p-1", "p-1-W1").unwrap();
    assert_eq!(step_word(&found.phase, &found.state, &found.steps), "Make");
}
