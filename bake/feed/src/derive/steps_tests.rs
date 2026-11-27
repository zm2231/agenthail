use std::collections::HashMap;
use std::time::{Duration, Instant};

use serde_json::json;

use super::live::{Fetched, Known, Ledger, Overview, ledger};
use super::phase::{Steps, step_word};
use super::world::{Recorded, World};
use crate::mock::{daemon, replies};
use crate::worksearch::{Found, Search};

const RENAMED: &str = r#"{"scope":"Scope","build":"Make","review":"Review","verify":"Verify","check":"Sign-off","ship":"Ship"}"#;

fn recorded(ledgers: serde_json::Value, labels: serde_json::Value) -> Recorded {
    serde_json::from_value(json!({
        "updatedAt": "2026-11-27T12:00:00Z",
        "projects": [{"id": "p-1", "displayName": "Atlas", "ledgerKey": "project:p-1"}, {"id": "p-2", "displayName": "Birch", "ledgerKey": "project:p-2"}],
        "ledgers": ledgers,
        "phaseLabels": labels,
    }))
    .unwrap()
}

fn renamed() -> Steps {
    serde_json::from_str(RENAMED).unwrap()
}

fn found(key: &str, id: &str, phase: &str) -> Found {
    Found { project_key: key.into(), project_name: "X".into(), id: id.into(), state: "in_progress".into(), phase: phase.into(), ..Found::default() }
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
    assert_eq!(got.steps.names().collect::<Vec<_>>(), ["Scope", "Make", "Review", "Verify", "Sign-off", "Ship"]);
    let mut ov = Overview::recorded(recorded(json!({}), json!({})));
    ov.take_ledger("p-1", Instant::now(), Ok(got));
    let w = ov.world(&[]);
    let shown: Vec<String> = ["p-1-W1", "p-1-W2"].iter().map(|id| w.work.values().find(|x| x.id == *id).unwrap()).map(|x| step_word(&x.phase, &x.state, &x.steps)).collect();
    assert_eq!(shown, ["Make", "Sign-off"]);
}

#[test]
fn a_renamed_project_with_no_work_shown_still_names_its_steps_in_search() {
    let (client, _seen) = daemon(replies(&[(200, &format!(r#"{{"work":[{{"id":"p-1-W1","state":"deleted","phase":"ship","timeline":[{{"at":"2026-01-01T00:00:00Z","kind":"state","state":"deleted"}}]}}],"phaseLabels":{RENAMED}}}"#))]));
    let got = ledger(&client, "project:p-1", &Known::default()).unwrap();
    let mut ov = Overview::recorded(recorded(json!({}), json!({})));
    ov.take_ledger("p-1", Instant::now(), Ok(got));
    let w = ov.world(&[]);
    assert!(w.work.is_empty(), "every row was trimmed");
    let rows: Vec<String> = w.found_rows(&Search { items: vec![found("project:p-1", "p-1-W7", "check")], truncated: false }, |_| false).into_iter().map(|r| r.status).collect();
    assert_eq!(rows, ["Sign-off · X"]);
}

#[test]
fn a_reread_ledger_replaces_its_step_names_and_an_older_read_does_not() {
    let row = |id: &str| serde_json::from_value(json!({"id": id, "text": "t", "state": "in_progress", "phase": "build"})).unwrap();
    let ledger = |steps: Steps| Ok(Ledger { rows: vec![row("p-1-W1")], actors: HashMap::new(), asked: Vec::new(), actors_at: Instant::now(), steps });
    let mut ov = Overview::recorded(recorded(json!({}), json!({})));
    let (older, newer) = (Instant::now(), Instant::now() + Duration::from_millis(5));
    ov.take_ledger("p-1", older, ledger(renamed()));
    let word = |ov: &mut Overview| {
        let w = ov.world(&[]);
        let x = w.work.values().next().unwrap().clone();
        (step_word(&x.phase, &x.state, &x.steps), w.steps_of("project:p-1").name(1).to_string())
    };
    assert_eq!(word(&mut ov), ("Make".to_string(), "Make".to_string()));
    ov.take_ledger("p-1", newer, ledger(Steps::default()));
    assert_eq!(word(&mut ov), ("Build".to_string(), "Build".to_string()), "names set back to the defaults show the defaults");
    ov.take_ledger("p-1", older, ledger(renamed()));
    assert_eq!(word(&mut ov), ("Build".to_string(), "Build".to_string()), "an older read doesn't bring old names back");
}

#[test]
fn a_project_with_no_names_of_its_own_shows_the_default_steps() {
    let (client, _seen) = daemon(replies(&[(200, r#"{"work":[{"id":"p-2-W1","text":"Plan it","state":"in_progress","phase":"build"}],"phaseLabels":{"scope":"Scope","build":"Build","review":"Review","verify":"Verify","check":"Check","ship":"Ship"}}"#)]));
    let got = ledger(&client, "project:p-2", &Known::default()).unwrap();
    assert_eq!(got.steps, Steps::default());
    let (client, _older) = daemon(replies(&[(200, r#"{"work":[{"id":"p-2-W1","text":"Plan it","state":"in_progress","phase":"build"}]}"#)]));
    let older = ledger(&client, "project:p-2", &Known::default()).unwrap();
    assert_eq!(step_word(&older.rows[0].phase, &older.rows[0].state, &older.steps), "Build");
}

#[test]
fn jump_to_and_work_search_name_each_projects_own_steps() {
    let labels: serde_json::Value = serde_json::from_str(RENAMED).unwrap();
    let w = World::new(recorded(
        json!({
            "p-1": [{"id": "p-1-W1", "text": "Wire the export", "state": "in_progress", "phase": "build"}, {"id": "p-1-W2", "text": "Check the export", "state": "in_review", "phase": "check"}],
            "p-2": [{"id": "p-2-W1", "text": "Wire the import", "state": "in_progress", "phase": "build"}],
        }),
        json!({"p-1": labels}),
    ));
    let mut status: Vec<(String, String)> = w.jump("wire", &[], &[]).into_iter().filter(|h| h.group == "Work").map(|h| (h.title, h.status.split(" · ").next().unwrap().to_string())).collect();
    status.sort();
    assert_eq!(status, [("Wire the export".to_string(), "Make".to_string()), ("Wire the import".to_string(), "Build".to_string())]);
    let check = w.jump("check the", &[], &[]).into_iter().find(|h| h.group == "Work").unwrap();
    assert!(check.status.starts_with("Sign-off · "), "{}", check.status);
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

#[test]
fn an_overview_ledger_read_that_started_after_a_single_read_wins_whichever_lands_first() {
    let row = |id: &str| serde_json::from_value(json!({"id": id, "text": "t", "state": "in_progress", "phase": "build"})).unwrap();
    let snapshot = Instant::now();
    let (single_at, overview_at) = (snapshot + Duration::from_millis(1), snapshot + Duration::from_millis(2));
    let single = || Ok(Ledger { rows: vec![row("p-1-W1")], actors: HashMap::new(), asked: Vec::new(), actors_at: single_at, steps: renamed() });
    let overview = || {
        let mut recorded = recorded(json!({}), json!({}));
        recorded.steps.insert("p-1".into(), Steps::default());
        Fetched { recorded, ledgers: [("p-1".to_string(), (overview_at, Ok(vec![row("p-1-W2")])))].into(), held: Vec::new(), asked: Vec::new(), actors_at: overview_at, usage: None, moved: Vec::new() }
    };
    for single_lands_first in [true, false] {
        let mut ov = Overview::recorded(recorded(json!({}), json!({})));
        if single_lands_first {
            ov.take_ledger("p-1", single_at, single());
            ov.refresh(overview());
        } else {
            ov.refresh(overview());
            ov.take_ledger("p-1", single_at, single());
        }
        let w = ov.world(&[]);
        let shown: Vec<(String, String)> = w.work.values().map(|x| (x.id.clone(), step_word(&x.phase, &x.state, &x.steps))).collect();
        assert_eq!(shown, [("p-1-W2".to_string(), "Build".to_string())], "single read landed first: {single_lands_first}");
        assert_eq!(w.steps_of("project:p-1").name(1), "Build");
    }
}
