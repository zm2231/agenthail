use std::collections::HashMap;
use std::time::Instant;

use serde_json::{Value, json};

use super::live::{Fetched, Ledger, Overview};
use super::*;
use crate::catalog::Session;

fn session(id: &str, project: &str, last_active: &str, status: &str) -> Session {
    serde_json::from_value(json!({"id": id, "name": id, "status": status, "lastActive": last_active, "project": {"id": project, "name": project}})).unwrap()
}

fn work(id: &str, state: &str) -> Work {
    serde_json::from_value(json!({"id": id, "text": id, "state": state, "timeline": [{"at": "2026-11-24T11:00:00Z", "actor": "", "kind": "created"}]})).unwrap()
}

fn recorded(at: &str, sessions: Vec<Session>) -> Recorded {
    let mut r: Recorded = serde_json::from_value(json!({"updatedAt": at, "projects": [{"id": "p", "displayName": "P"}, {"id": "old", "displayName": "Old"}]})).unwrap();
    r.sessions = sessions;
    r
}

fn fetched(at: &str, sessions: Vec<Session>, ledgers: Vec<(&str, Result<Vec<Work>, String>)>) -> Fetched {
    Fetched { recorded: recorded(at, sessions), ledgers: ledgers.into_iter().map(|(k, r)| (k.to_string(), r)).collect(), started: Instant::now() }
}

fn status(w: &World, id: &str) -> String {
    w.session(id).map(|s| s.status.clone()).unwrap_or_default()
}

fn board_ids(w: &World, project: &str) -> Vec<String> {
    let mut ids: Vec<String> = w.board(project).lanes.iter().flatten().map(|w| w.id.clone()).collect();
    ids.sort();
    ids
}

#[test]
fn an_overview_fetched_before_a_newer_session_snapshot_keeps_the_newer_sessions() {
    let mut ov = Overview::default();
    ov.refresh(fetched("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")], vec![]));
    assert!(ov.take_sessions("2026-11-24T12:00:04Z", vec![session("a", "p", "2026-11-24T12:00:03Z", "busy")]));
    ov.refresh(fetched("2026-11-24T12:00:02Z", vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")], vec![("p", Ok(vec![work("w1", "open")]))]));
    let w = ov.world(&[]);
    assert_eq!(status(&w, "a"), "busy");
    assert_eq!(board_ids(&w, "p"), ["w1"], "the rest of the older refresh still lands");
    assert!(!ov.take_sessions("2026-11-24T12:00:03Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert_eq!(status(&ov.world(&[]), "a"), "busy");
    ov.refresh(fetched("2026-11-24T12:00:04Z", vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")], vec![]));
    assert_eq!(status(&ov.world(&[]), "a"), "busy", "a refresh stamped the same time keeps the sessions held");
    ov.refresh(fetched("2026-11-24T12:00:10Z", vec![session("a", "p", "2026-11-24T12:00:09Z", "idle")], vec![]));
    assert_eq!(status(&ov.world(&[]), "a"), "idle", "a newer overview replaces the sessions");
}

#[test]
fn a_failed_ledger_fetch_keeps_the_last_rows_marked_stale_until_the_next_success() {
    let sessions = || vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")];
    let mut ov = Overview::default();
    ov.refresh(fetched("2026-11-24T12:00:00Z", sessions(), vec![("p", Ok(vec![work("w1", "open"), work("w2", "in_progress")]))]));
    assert_eq!(ov.world(&[]).rows.get("p"), None);
    ov.refresh(fetched("2026-11-24T12:00:20Z", sessions(), vec![("p", Err("timed out".into()))]));
    let w = ov.world(&[]);
    assert_eq!(board_ids(&w, "p"), ["w1", "w2"]);
    assert_eq!(w.rows.get("p"), Some(&Rows::Stale));
    ov.refresh(fetched("2026-11-24T12:00:40Z", sessions(), vec![("p", Ok(vec![work("w3", "open")]))]));
    let w = ov.world(&[]);
    assert_eq!(board_ids(&w, "p"), ["w3"]);
    assert_eq!(w.rows.get("p"), None);
}

#[test]
fn a_ledger_that_never_loaded_is_unavailable_rather_than_empty() {
    let mut ov = Overview::default();
    ov.refresh(fetched("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")], vec![("p", Err("refused".into()))]));
    assert_eq!(ov.world(&[]).rows.get("p"), Some(&Rows::Unavailable));
}

#[test]
fn a_listed_project_without_recent_sessions_gets_its_board_when_opened() {
    let sessions = || vec![session("a", "p", "2026-11-24T11:59:00Z", "idle"), session("b", "old", "2026-11-10T09:00:00Z", "idle")];
    let mut ov = Overview::default();
    ov.refresh(fetched("2026-11-24T12:00:00Z", sessions(), vec![("p", Ok(vec![]))]));
    assert!(ov.world(&[]).project("old").is_some(), "the project is listed");
    assert!(!ov.wants("p"), "refreshes already cover it");
    assert!(ov.wants("old"));
    assert!(!ov.wants("old"), "one fetch at a time");
    assert_eq!(ov.world(&[]).rows.get("old"), Some(&Rows::Loading));
    ov.take_ledger("old", Instant::now(), Ok(Ledger { rows: vec![work("w9", "open")], actors: HashMap::new() }));
    let w = ov.world(&[]);
    assert_eq!(board_ids(&w, "old"), ["w9"]);
    assert_eq!(w.rows.get("old"), None);
    ov.refresh(fetched("2026-11-24T12:00:20Z", sessions(), vec![("p", Ok(vec![]))]));
    assert_eq!(board_ids(&ov.world(&[]), "old"), ["w9"], "a refresh that skips the project keeps its rows");
    assert!(ov.wants("old"), "and it is fetched again while open");
}

#[test]
fn a_recording_shows_every_ledger_it_holds() {
    let raw: Value = json!({"updatedAt": "2026-11-24T12:00:00Z", "projects": [{"id": "p", "displayName": "P"}], "sessions": [{"id": "a", "lastActive": "2026-11-01T00:00:00Z", "project": {"id": "p", "name": "P"}}], "ledgers": {"p": [{"id": "w1", "state": "open", "timeline": []}]}});
    let mut ov = Overview::recorded(serde_json::from_value(raw).unwrap());
    assert!(!ov.wants("p"));
    assert_eq!(board_ids(&ov.world(&[]), "p"), ["w1"]);
}

#[test]
fn an_overview_without_a_valid_time_never_replaces_the_held_sessions() {
    let mut ov = Overview::default();
    assert!(!ov.take_sessions("", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    ov.refresh(fetched("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:59:00Z", "busy")], vec![]));
    assert!(!ov.take_sessions("not a time", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    ov.refresh(fetched("", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")], vec![]));
    assert_eq!(status(&ov.world(&[]), "a"), "busy");
}

#[test]
fn a_ledger_fetch_that_started_earlier_never_overwrites_a_later_one() {
    let sessions = || vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")];
    let (earlier, later) = (Instant::now(), Instant::now() + std::time::Duration::from_millis(1));
    let mut ov = Overview::default();
    ov.take_ledger("p", later, Ok(Ledger { rows: vec![work("new", "open")], actors: HashMap::new() }));
    ov.refresh(Fetched { started: earlier, ..fetched("2026-11-24T12:00:00Z", sessions(), vec![("p", Ok(vec![work("old", "open")]))]) });
    assert_eq!(board_ids(&ov.world(&[]), "p"), ["new"], "a refresh that started first and landed last");
    ov.take_ledger("p", earlier, Err("timed out".into()));
    assert_eq!(ov.world(&[]).rows.get("p"), None, "nor does its failure mark the newer rows stale");
    let latest = later + std::time::Duration::from_millis(1);
    ov.refresh(Fetched { started: latest, ..fetched("2026-11-24T12:00:20Z", sessions(), vec![("p", Ok(vec![work("newest", "open")]))]) });
    ov.take_ledger("p", later, Ok(Ledger { rows: vec![work("new", "open")], actors: HashMap::new() }));
    assert_eq!(board_ids(&ov.world(&[]), "p"), ["newest"], "an on-demand fetch that started first and landed last");
}
