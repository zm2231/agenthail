use std::collections::HashMap;
use std::time::Instant;

use serde_json::{Value, json};

use super::live::{Fetched, Ledger, Overview};
use super::world::Actor;
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
    Fetched { recorded: recorded(at, sessions), ledgers: ledgers.into_iter().map(|(k, r)| (k.to_string(), r)).collect(), held: Vec::new(), started: Instant::now() }
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

fn actor(alias: &str) -> HashMap<String, Actor> {
    [("helper-1".to_string(), Actor { alias: Some(alias.into()), name: None, surface: None })].into()
}

#[test]
fn actors_from_a_fetch_that_started_earlier_never_replace_later_ones() {
    let sessions = || vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")];
    let (earlier, later) = (Instant::now(), Instant::now() + std::time::Duration::from_millis(1));
    let mut ov = Overview::default();
    ov.take_ledger("p", later, Ok(Ledger { rows: vec![], actors: actor("fresh") }));
    let mut stale = fetched("2026-11-24T12:00:00Z", sessions(), vec![]);
    stale.recorded.actors = actor("stale");
    ov.refresh(Fetched { started: earlier, ..stale });
    assert!(ov.world(&[]).name_of("helper-1").contains("fresh"), "a refresh that started first and landed last");
    let latest = later + std::time::Duration::from_millis(1);
    let mut newest = fetched("2026-11-24T12:00:20Z", sessions(), vec![]);
    newest.recorded.actors = actor("newest");
    ov.refresh(Fetched { started: latest, ..newest });
    ov.take_ledger("p", later, Ok(Ledger { rows: vec![], actors: actor("fresh") }));
    assert!(ov.world(&[]).name_of("helper-1").contains("newest"), "an on-demand fetch that started first and landed last");
}

#[test]
fn a_burst_of_arrivals_leaves_one_rebuild_pending() {
    let sessions = || vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")];
    let mut ov = Overview::default();
    ov.refresh(fetched("2026-11-24T12:00:00Z", sessions(), vec![]));
    ov.world(&[]);
    assert!(!ov.stale());
    assert!(!ov.take_sessions("2026-11-24T11:00:00Z", sessions()));
    assert!(!ov.stale(), "a snapshot that changes nothing asks for no rebuild");
    for id in ["t1", "t2", "t3"] {
        ov.take_tail(id, "2026-11-24T11:59:00Z".into(), Err("timed out".into()));
    }
    ov.take_ledger("p", Instant::now(), Ok(Ledger { rows: vec![work("w1", "open")], actors: HashMap::new() }));
    ov.refresh(fetched("2026-11-24T12:00:20Z", sessions(), vec![]));
    assert!(ov.stale());
    ov.world(&[]);
    assert!(!ov.stale(), "one rebuild covers every arrival before it");
    assert!(ov.wants("old"));
    assert!(ov.stale(), "a project opened for loading asks for a rebuild to show it");
}

#[test]
fn a_confirmed_change_outlasts_a_stale_row_until_the_daemon_shows_it() {
    use crate::menu::Patch;
    let mut ov = Overview::default();
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.patch("a", Patch::Alias(Some("scout".into())), Instant::now()));
    let w = ov.world(&[]);
    assert_eq!(w.title(w.session("a").unwrap()), "@scout", "names follow the change");
    assert!(ov.take_sessions("2026-11-24T12:00:04Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert_eq!(ov.sessions()[0].alias.as_deref(), Some("scout"), "a row read before the change");
    let mut shown = session("a", "p", "2026-11-24T11:00:00Z", "idle");
    shown.alias = Some("scout".into());
    assert!(ov.take_sessions("2026-11-24T12:00:06Z", vec![shown]));
    assert!(ov.take_sessions("2026-11-24T12:00:08Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert_eq!(ov.sessions()[0].alias, None, "once shown, the daemon's later word wins");
}

#[test]
fn a_pull_request_change_holds_only_that_pull_request_until_the_daemon_shows_it() {
    use crate::catalog::Pr;
    use crate::menu::Patch;
    let pr = |n: u64, watch: bool, checks: &str| Pr { number: n, url: format!("https://github.com/o/r/pull/{n}"), watch, checks: checks.into(), ..Default::default() };
    let row = |prs: Vec<Pr>| {
        let mut x = session("a", "p", "2026-11-24T11:00:00Z", "idle");
        x.prs = prs;
        x
    };
    let mut ov = Overview::default();
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", vec![row(vec![pr(12, false, "pending"), pr(13, false, "")])]));
    assert!(ov.patch("a", Patch::Pr { pr: pr(12, true, "pending"), linked: true }, Instant::now()));
    assert!(ov.patch("a", Patch::Pr { pr: pr(13, false, ""), linked: false }, Instant::now()), "a change to another pull request is its own");
    assert!(ov.take_sessions("2026-11-24T12:00:04Z", vec![row(vec![pr(12, false, "failing"), pr(13, false, "")])]));
    let shown: Vec<(u64, bool, String)> = ov.sessions()[0].prs.iter().map(|p| (p.number, p.watch, p.checks.clone())).collect();
    assert_eq!(shown, vec![(12, true, "failing".to_string())], "a stale row keeps both changes but its newer checks still show");
    assert!(ov.take_sessions("2026-11-24T12:00:06Z", vec![row(vec![pr(12, true, "passing")])]));
    assert!(ov.take_sessions("2026-11-24T12:00:08Z", vec![row(vec![pr(12, false, "passing"), pr(14, false, "")])]));
    let shown: Vec<(u64, bool)> = ov.sessions()[0].prs.iter().map(|p| (p.number, p.watch)).collect();
    assert_eq!(shown, vec![(12, false), (14, false)], "once shown, the daemon's later word wins");
}

#[test]
fn the_same_pull_request_change_confirmed_twice_keeps_its_hold() {
    use crate::catalog::Pr;
    use crate::menu::Patch;
    let pr = |checks: &str, watch: bool| Pr { number: 12, url: "https://github.com/o/r/pull/12".into(), watch, checks: checks.into(), ..Default::default() };
    let row = |prs: Vec<Pr>| {
        let mut x = session("a", "p", "2026-11-24T11:00:00Z", "idle");
        x.prs = prs;
        x
    };
    let mut ov = Overview::default();
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", vec![row(Vec::new())]));
    assert!(ov.patch("a", Patch::Pr { pr: pr("pending", true), linked: true }, Instant::now()));
    ov.patch("a", Patch::Pr { pr: pr("passing", true), linked: true }, Instant::now());
    assert!(ov.take_sessions("2026-11-24T12:00:04Z", vec![row(Vec::new())]));
    assert_eq!(ov.sessions()[0].prs.len(), 1, "a stale row still shows the linked pull request");
}

#[test]
fn a_change_the_daemon_never_shows_gives_way() {
    use crate::menu::Patch;
    let mut ov = Overview::default();
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    let long_ago = Instant::now().checked_sub(super::live::EXPECT_HOLD).unwrap();
    assert!(!ov.patch("a", Patch::Archived(true), long_ago), "an expired change is not shown");
    assert!(!ov.sessions()[0].archived);
    assert!(!ov.patch("missing", Patch::Archived(true), Instant::now()));
}

#[test]
fn a_late_confirmation_does_not_undo_what_the_daemon_already_shows() {
    use crate::menu::Patch;
    let mut ov = Overview::default();
    let mut pinned = session("a", "p", "2026-11-24T11:00:00Z", "idle");
    pinned.pinned_at = Some("2026-11-24T12:00:01Z".into());
    assert!(ov.take_sessions("2026-11-24T12:00:02Z", vec![pinned]));
    assert!(!ov.patch("a", Patch::Pinned(Some("2026-11-24T12:00:03Z".into())), Instant::now()));
    assert_eq!(ov.sessions()[0].pinned_at.as_deref(), Some("2026-11-24T12:00:01Z"));
    assert!(ov.take_sessions("2026-11-24T12:00:04Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.sessions()[0].pinned_at.is_none(), "an unpin after it wins");
}

#[test]
fn an_older_overview_does_not_count_as_the_daemon_showing_a_change() {
    use crate::menu::Patch;
    let mut ov = Overview::default();
    assert!(ov.take_sessions("2026-11-24T12:00:05Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.patch("a", Patch::Archived(true), Instant::now()));
    ov.refresh(fetched("2026-11-24T12:00:01Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")], vec![]));
    assert!(ov.sessions()[0].archived);
    assert!(ov.take_sessions("2026-11-24T12:00:07Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.sessions()[0].archived, "a row read before the archive");
    ov.refresh(fetched("2026-11-24T12:00:09Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")], vec![]));
    assert!(ov.sessions()[0].archived, "a newer overview with the old row still holds");
}

#[test]
fn a_repeated_confirmation_keeps_the_change_held() {
    use crate::menu::Patch;
    let mut ov = Overview::default();
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.patch("a", Patch::Pinned(Some("2026-11-24T12:00:01Z".into())), Instant::now()));
    assert!(ov.patch("a", Patch::Pinned(Some("2026-11-24T12:00:02Z".into())), Instant::now()));
    assert!(ov.take_sessions("2026-11-24T12:00:03Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.sessions()[0].pinned(), "a row read before both pins");
    assert!(ov.patch("a", Patch::Pinned(None), Instant::now()));
    assert!(!ov.sessions()[0].pinned());
}

#[test]
fn a_held_change_gives_way_after_the_hold_without_a_newer_snapshot() {
    use crate::menu::Patch;
    let mut ov = Overview::default();
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    let almost = Instant::now().checked_sub(super::live::EXPECT_HOLD - std::time::Duration::from_millis(300)).unwrap();
    assert!(ov.patch("a", Patch::Archived(true), almost));
    assert!(!ov.expire());
    assert!(ov.sessions()[0].archived);
    std::thread::sleep(std::time::Duration::from_millis(400));
    assert!(!ov.take_sessions("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]), "the same cached snapshot");
    assert!(ov.expire());
    assert!(!ov.sessions()[0].archived, "the daemon's last value");
    assert!(!ov.expire());
}

#[test]
fn chained_read_marks_hold_against_a_row_older_than_both() {
    use crate::action::ReadState;
    use crate::menu::Patch;
    let mut ov = Overview::default();
    let mut at_a = session("a", "p", "2026-11-24T11:00:00Z", "idle");
    at_a.read_state = Some(ReadState { first_unread: "item-1".into(), revision: "A".into() });
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", vec![at_a.clone()]));
    assert!(ov.patch("a", Patch::Read { receipt: ReadState { first_unread: String::new(), revision: "B".into() }, from: vec!["A".into()] }, Instant::now()));
    assert!(ov.patch("a", Patch::Read { receipt: ReadState { first_unread: "item-1".into(), revision: "C".into() }, from: vec!["B".into()] }, Instant::now()));
    assert!(ov.take_sessions("2026-11-24T12:00:03Z", vec![at_a]));
    assert_eq!(ov.sessions()[0].read_state.as_ref().unwrap().revision, "C");
}

#[test]
fn a_ledger_change_refetches_only_a_project_whose_rows_are_shown() {
    let mut ov = Overview::default();
    let mut r = recorded("2026-11-24T12:00:00Z", vec![session("s", "p", "2026-11-24T11:00:00Z", "idle")]);
    r.projects[0].ledger_key = "shared:x".into();
    ov.refresh(Fetched { recorded: r, ledgers: [("p".to_string(), Ok(vec![work("p-W1", "open")]))].into(), held: Vec::new(), started: Instant::now() });
    assert_eq!(ov.ledger_changed("shared:x").as_deref(), Some("p"));
    assert_eq!(ov.ledger_changed("p").as_deref(), Some("p"));
    assert_eq!(ov.ledger_changed("old"), None);
    assert_eq!(ov.ledger_changed("project:elsewhere"), None);
}

#[test]
fn ledgers_held_from_the_change_stream_survive_an_overview_that_skips_them() {
    let mut ov = Overview::default();
    let sessions = || vec![session("s", "p", "2026-11-24T11:00:00Z", "idle")];
    ov.refresh(fetched("2026-11-24T12:00:00Z", sessions(), vec![("p", Ok(vec![work("p-W1", "open")]))]));
    ov.refresh(Fetched { held: vec!["p".into()], ..fetched("2026-11-24T12:01:00Z", sessions(), Vec::new()) });
    assert_eq!(board_ids(&ov.world(&[]), "p"), vec!["p-W1".to_string()]);
    assert!(!ov.wants("p"));
}
