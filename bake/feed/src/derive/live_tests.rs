use std::collections::HashMap;
use std::sync::Arc;
use std::time::Instant;

use serde_json::{Value, json};

use super::live::{Change, Fetched, Ledger, Overview};
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
    Fetched { recorded: recorded(at, Vec::new()), sessions: Arc::new(sessions), ledgers: ledgers.into_iter().map(|(k, r)| (k.to_string(), (Instant::now(), r.map(|rows| rows.into_iter().map(Arc::new).collect())))).collect(), held: Vec::new(), asked: Vec::new(), actors_at: Instant::now(), usage: None, moved: Vec::new() }
}

fn stamped(mut f: Fetched, at: Instant) -> Fetched {
    for (read, _) in f.ledgers.values_mut() {
        *read = at;
    }
    f.actors_at = at;
    f
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
    assert!(ov.take_sessions("2026-11-24T12:00:04Z", "", 0, vec![session("a", "p", "2026-11-24T12:00:03Z", "busy")]));
    ov.refresh(fetched("2026-11-24T12:00:02Z", vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")], vec![("p", Ok(vec![work("w1", "open")]))]));
    let w = ov.world(&[]);
    assert_eq!(status(&w, "a"), "busy");
    assert_eq!(board_ids(&w, "p"), ["w1"], "the rest of the older refresh still lands");
    assert!(!ov.take_sessions("2026-11-24T12:00:03Z", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
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
    ov.take_ledger("old", Instant::now(), Ok(Ledger { rows: vec![work("w9", "open").into()], actors: HashMap::new(), asked: Vec::new(), actors_at: Instant::now(), steps: Default::default() }));
    let w = ov.world(&[]);
    assert_eq!(board_ids(&w, "old"), ["w9"]);
    assert_eq!(w.rows.get("old"), None);
    ov.refresh(fetched("2026-11-24T12:00:20Z", sessions(), vec![("p", Ok(vec![]))]));
    assert_eq!(board_ids(&ov.world(&[]), "old"), ["w9"], "a refresh that skips the project keeps its rows");
    assert!(!ov.wants("old"), "while open, the ledger stream says when it changes");
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
    assert!(!ov.take_sessions("", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    ov.refresh(fetched("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:59:00Z", "busy")], vec![]));
    assert!(!ov.take_sessions("not a time", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    ov.refresh(fetched("", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")], vec![]));
    assert_eq!(status(&ov.world(&[]), "a"), "busy");
}

#[test]
fn a_ledger_fetch_that_started_earlier_never_overwrites_a_later_one() {
    let sessions = || vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")];
    let (earlier, later) = (Instant::now(), Instant::now() + std::time::Duration::from_millis(1));
    let mut ov = Overview::default();
    ov.take_ledger("p", later, Ok(Ledger { rows: vec![work("new", "open").into()], actors: HashMap::new(), asked: Vec::new(), actors_at: later, steps: Default::default() }));
    ov.refresh(stamped(fetched("2026-11-24T12:00:00Z", sessions(), vec![("p", Ok(vec![work("old", "open")]))]), earlier));
    assert_eq!(board_ids(&ov.world(&[]), "p"), ["new"], "a refresh that started first and landed last");
    ov.take_ledger("p", earlier, Err("timed out".into()));
    assert_eq!(ov.world(&[]).rows.get("p"), None, "nor does its failure mark the newer rows stale");
    let latest = later + std::time::Duration::from_millis(1);
    ov.refresh(stamped(fetched("2026-11-24T12:00:20Z", sessions(), vec![("p", Ok(vec![work("newest", "open")]))]), latest));
    ov.take_ledger("p", later, Ok(Ledger { rows: vec![work("new", "open").into()], actors: HashMap::new(), asked: Vec::new(), actors_at: later, steps: Default::default() }));
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
    ov.take_ledger("p", later, Ok(Ledger { rows: vec![], actors: actor("fresh"), asked: Vec::new(), actors_at: later, steps: Default::default() }));
    let mut stale = fetched("2026-11-24T12:00:00Z", sessions(), vec![]);
    stale.recorded.actors = actor("stale");
    ov.refresh(stamped(stale, earlier));
    assert!(ov.world(&[]).name_of("helper-1").contains("fresh"), "a refresh that started first and landed last");
    let latest = later + std::time::Duration::from_millis(1);
    let mut newest = fetched("2026-11-24T12:00:20Z", sessions(), vec![]);
    newest.recorded.actors = actor("newest");
    ov.refresh(stamped(newest, latest));
    ov.take_ledger("p", later, Ok(Ledger { rows: vec![], actors: actor("fresh"), asked: Vec::new(), actors_at: later, steps: Default::default() }));
    assert!(ov.world(&[]).name_of("helper-1").contains("newest"), "an on-demand fetch that started first and landed last");
}

#[test]
fn a_burst_of_arrivals_leaves_one_rebuild_pending() {
    let sessions = || vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")];
    let mut ov = Overview::default();
    ov.refresh(fetched("2026-11-24T12:00:00Z", sessions(), vec![]));
    ov.world(&[]);
    assert!(!ov.stale());
    assert!(!ov.take_sessions("2026-11-24T11:00:00Z", "", 0, sessions()));
    assert!(!ov.stale(), "a snapshot that changes nothing asks for no rebuild");
    for id in ["t1", "t2", "t3"] {
        ov.take_tail(id, "2026-11-24T11:59:00Z".into(), Err("timed out".into()), None);
    }
    ov.take_ledger("p", Instant::now(), Ok(Ledger { rows: vec![work("w1", "open").into()], actors: HashMap::new(), asked: Vec::new(), actors_at: Instant::now(), steps: Default::default() }));
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
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.patch("a", Patch::Alias(Some("scout".into())), Instant::now()));
    let w = ov.world(&[]);
    assert_eq!(w.title(w.session("a").unwrap()), "@scout", "names follow the change");
    assert!(ov.take_sessions("2026-11-24T12:00:04Z", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert_eq!(ov.sessions()[0].alias.as_deref(), Some("scout"), "a row read before the change");
    let mut shown = session("a", "p", "2026-11-24T11:00:00Z", "idle");
    shown.alias = Some("scout".into());
    assert!(ov.take_sessions("2026-11-24T12:00:06Z", "", 0, vec![shown]));
    assert!(ov.take_sessions("2026-11-24T12:00:08Z", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
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
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", "", 0, vec![row(vec![pr(12, false, "pending"), pr(13, false, "")])]));
    assert!(ov.patch("a", Patch::Pr { pr: pr(12, true, "pending"), linked: true }, Instant::now()));
    assert!(ov.patch("a", Patch::Pr { pr: pr(13, false, ""), linked: false }, Instant::now()), "a change to another pull request is its own");
    assert!(ov.take_sessions("2026-11-24T12:00:04Z", "", 0, vec![row(vec![pr(12, false, "failing"), pr(13, false, "")])]));
    let shown: Vec<(u64, bool, String)> = ov.sessions()[0].prs.iter().map(|p| (p.number, p.watch, p.checks.clone())).collect();
    assert_eq!(shown, vec![(12, true, "failing".to_string())], "a stale row keeps both changes but its newer checks still show");
    assert!(ov.take_sessions("2026-11-24T12:00:06Z", "", 0, vec![row(vec![pr(12, true, "passing")])]));
    assert!(ov.take_sessions("2026-11-24T12:00:08Z", "", 0, vec![row(vec![pr(12, false, "passing"), pr(14, false, "")])]));
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
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", "", 0, vec![row(Vec::new())]));
    assert!(ov.patch("a", Patch::Pr { pr: pr("pending", true), linked: true }, Instant::now()));
    ov.patch("a", Patch::Pr { pr: pr("passing", true), linked: true }, Instant::now());
    assert!(ov.take_sessions("2026-11-24T12:00:04Z", "", 0, vec![row(Vec::new())]));
    assert_eq!(ov.sessions()[0].prs.len(), 1, "a stale row still shows the linked pull request");
}

#[test]
fn a_change_the_daemon_never_shows_gives_way() {
    use crate::menu::Patch;
    let mut ov = Overview::default();
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
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
    assert!(ov.take_sessions("2026-11-24T12:00:02Z", "", 0, vec![pinned]));
    assert!(!ov.patch("a", Patch::Pinned(Some("2026-11-24T12:00:03Z".into())), Instant::now()));
    assert_eq!(ov.sessions()[0].pinned_at.as_deref(), Some("2026-11-24T12:00:01Z"));
    assert!(ov.take_sessions("2026-11-24T12:00:04Z", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.sessions()[0].pinned_at.is_none(), "an unpin after it wins");
}

#[test]
fn an_older_overview_does_not_count_as_the_daemon_showing_a_change() {
    use crate::menu::Patch;
    let mut ov = Overview::default();
    assert!(ov.take_sessions("2026-11-24T12:00:05Z", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.patch("a", Patch::Archived(true), Instant::now()));
    ov.refresh(fetched("2026-11-24T12:00:01Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")], vec![]));
    assert!(ov.sessions()[0].archived);
    assert!(ov.take_sessions("2026-11-24T12:00:07Z", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.sessions()[0].archived, "a row read before the archive");
    ov.refresh(fetched("2026-11-24T12:00:09Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")], vec![]));
    assert!(ov.sessions()[0].archived, "a newer overview with the old row still holds");
}

#[test]
fn a_repeated_confirmation_keeps_the_change_held() {
    use crate::menu::Patch;
    let mut ov = Overview::default();
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.patch("a", Patch::Pinned(Some("2026-11-24T12:00:01Z".into())), Instant::now()));
    assert!(ov.patch("a", Patch::Pinned(Some("2026-11-24T12:00:02Z".into())), Instant::now()));
    assert!(ov.take_sessions("2026-11-24T12:00:03Z", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.sessions()[0].pinned(), "a row read before both pins");
    assert!(ov.patch("a", Patch::Pinned(None), Instant::now()));
    assert!(!ov.sessions()[0].pinned());
}

#[test]
fn a_held_change_gives_way_after_the_hold_without_a_newer_snapshot() {
    use crate::menu::Patch;
    let mut ov = Overview::default();
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    let almost = Instant::now().checked_sub(super::live::EXPECT_HOLD - std::time::Duration::from_millis(300)).unwrap();
    assert!(ov.patch("a", Patch::Archived(true), almost));
    assert!(!ov.expire());
    assert!(ov.sessions()[0].archived);
    std::thread::sleep(std::time::Duration::from_millis(400));
    assert!(!ov.take_sessions("2026-11-24T12:00:00Z", "", 0, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]), "the same cached snapshot");
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
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", "", 0, vec![at_a.clone()]));
    assert!(ov.patch("a", Patch::Read { receipt: ReadState { first_unread: String::new(), revision: "B".into() }, from: vec!["A".into()] }, Instant::now()));
    assert!(ov.patch("a", Patch::Read { receipt: ReadState { first_unread: "item-1".into(), revision: "C".into() }, from: vec!["B".into()] }, Instant::now()));
    assert!(ov.take_sessions("2026-11-24T12:00:03Z", "", 0, vec![at_a]));
    assert_eq!(ov.sessions()[0].read_state.as_ref().unwrap().revision, "C");
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

#[test]
fn sessions_are_ordered_by_catalog_position_not_by_whose_clock_is_later() {
    let mut ov = Overview::default();
    assert!(ov.take_sessions("2026-11-24T12:00:00Z", "e", 100, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.take_sessions("2026-11-24T12:00:01Z", "e", 105, vec![session("a", "p", "2026-11-24T11:59:00Z", "busy")]));
    let mut older = fetched("2026-11-24T12:00:09Z", vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")], Vec::new());
    older.recorded.host_epoch = "e".into();
    older.recorded.catalog_seq = 101;
    ov.refresh(older);
    assert_eq!(ov.sessions()[0].status, "busy", "a later read of an earlier catalog position doesn't undo a change");
    assert!(!ov.take_sessions("2026-11-24T12:00:10Z", "e", 104, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]));
    assert!(ov.take_sessions("2026-11-24T11:00:00Z", "e2", 3, vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")]), "the catalog follower's move to a new epoch wins whatever the clocks say");
    let mut stale = fetched("2026-11-24T11:30:00Z", vec![session("a", "p", "2026-11-24T11:59:00Z", "busy")], Vec::new());
    stale.recorded.host_epoch = "e".into();
    stale.recorded.catalog_seq = 200;
    ov.refresh(stale);
    assert_eq!(ov.sessions()[0].status, "idle", "an overview read from the old epoch doesn't undo it");
}

#[test]
fn an_overview_skips_ledgers_read_after_their_stream_settled_and_retries_failures_later() {
    let mut known = super::live::Known::default();
    let settled = Some(Instant::now() - std::time::Duration::from_secs(1));
    let mut f = Fetched { asked: vec!["found".into(), "gone".into()], ..fetched("2026-11-24T12:00:00Z", Vec::new(), vec![("p", Ok(Vec::new())), ("h2::p", Err("status code 404".into()))]) };
    f.recorded.actors.insert("found".into(), Actor::default());
    assert!(!known.learn(&f));
    assert!(known.skips_ledger("p", settled), "a read ledger waits for the stream to say it changed");
    assert!(!known.skips_ledger("p", None), "without the stream every overview reads it");
    assert!(!known.skips_ledger("p", Some(Instant::now())), "a stream that settled after the read, after a reconnect say, can't vouch for it");
    assert!(known.skips_ledger("h2::p", settled) && known.skips_ledger("h2::p", None), "a failed read waits a minute");
    assert!(!known.skips_ledger("q", settled));
    assert!(known.skips_actor("found") && known.skips_actor("gone") && !known.skips_actor("new"));
}

fn keyed(started: Instant) -> Fetched {
    let mut f = stamped(fetched("2026-11-24T12:00:00Z", Vec::new(), vec![("p", Ok(vec![work("p-W1", "open")]))]), started);
    f.recorded.projects[0].ledger_key = "shared:x".into();
    f
}

#[test]
fn a_ledger_that_changes_during_an_overview_read_is_read_again() {
    let mut known = super::live::Known::default();
    let f = keyed(Instant::now() - std::time::Duration::from_millis(10));
    assert_eq!(known.changed("shared:x", Instant::now()), Change::Nothing, "nothing is known yet; the read in flight checks for itself");
    assert!(known.learn(&f), "the read began before the change, so it goes again");
    assert!(!known.skips_ledger("p", Some(f.actors_at)));
    assert_eq!(known.changed("shared:x", Instant::now()), Change::Overview, "a wanted ledger that isn't held needs the overview");
}

#[test]
fn a_change_heard_after_the_overview_began_but_before_that_ledger_was_read_is_already_in_it() {
    let mut known = super::live::Known::default();
    let began = Instant::now() - std::time::Duration::from_millis(20);
    let heard = began + std::time::Duration::from_millis(5);
    let mut f = keyed(began);
    f.ledgers.get_mut("p").unwrap().0 = heard + std::time::Duration::from_millis(5);
    assert_eq!(known.changed("shared:x", heard), Change::Nothing);
    assert!(!known.learn(&f), "its own read started after the change, so it isn't read again");
}

#[test]
fn a_change_heard_after_the_read_landed_reads_that_ledger_alone() {
    let mut known = super::live::Known::default();
    let heard_at = Instant::now();
    let started = heard_at - std::time::Duration::from_millis(10);
    assert!(!known.learn(&keyed(started)));
    assert_eq!(known.changed("shared:x", heard_at), Change::Read("p".into()), "a change that happened during the read, processed after it");
    assert_eq!(known.changed("shared:x", started - std::time::Duration::from_millis(1)), Change::Nothing, "a change older than the read is already in it");
    assert_eq!(known.changed("project:elsewhere", Instant::now()), Change::Nothing);
    let at = Instant::now();
    let read = Ok(Ledger { rows: vec![work("p-W1", "open").into()], actors: HashMap::new(), asked: vec!["missing".into()], actors_at: at, steps: Default::default() });
    known.read("p", at, &read);
    assert!(known.skips_ledger("p", Some(at)) && known.skips_actor("missing"), "a single read also records its failed lookups");
}

#[test]
fn a_project_opened_outside_the_overview_hears_its_ledger_change_by_key() {
    let mut known = super::live::Known::default();
    let mut f = fetched("2026-11-24T12:00:00Z", Vec::new(), Vec::new());
    f.recorded.projects[1].ledger_key = "project:old".into();
    known.learn(&f);
    let opened = Instant::now() - std::time::Duration::from_millis(10);
    known.read("old", opened, &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: Vec::new(), actors_at: opened, steps: Default::default() }));
    assert_eq!(known.changed("project:old", Instant::now()), Change::Read("old".into()));
}

#[test]
fn a_failed_read_of_an_opened_project_is_tried_again_on_its_own_timer() {
    let mut known = super::live::Known::default();
    known.learn(&fetched("2026-11-24T12:00:00Z", Vec::new(), vec![("p", Ok(Vec::new()))]));
    let at = Instant::now();
    known.read("old", at, &Err("daemon down".into()));
    let due = known.next_retry().expect("a retry is scheduled");
    assert!(due > at);
    assert!(known.retries_due(at).is_empty());
    assert_eq!(known.retries_due(due), ["old".to_string()]);
    assert_eq!(known.changed("old", Instant::now()), Change::Nothing, "a change to a failed one waits for its retry");
    known.read("old", Instant::now(), &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: Vec::new(), actors_at: Instant::now(), steps: Default::default() }));
    assert_eq!(known.next_retry(), None);
}

#[test]
fn failed_lookups_come_due_when_the_next_overview_starts() {
    let mut known = super::live::Known::default();
    let started = Instant::now() - std::time::Duration::from_secs(61);
    let f = stamped(Fetched { asked: vec!["gone".into()], ..fetched("2026-11-24T12:00:00Z", Vec::new(), vec![("h2::p", Err("status code 404".into()))]) }, started);
    known.learn(&f);
    assert!(!known.skips_actor("gone") && !known.skips_ledger("h2::p", None), "retries count from when the read started");
}

#[test]
fn a_reopened_ledger_stream_reads_opened_projects_again() {
    let mut known = super::live::Known::default();
    known.learn(&fetched("2026-11-24T12:00:00Z", Vec::new(), vec![("p", Ok(Vec::new()))]));
    known.read("old", Instant::now(), &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: Vec::new(), actors_at: Instant::now(), steps: Default::default() }));
    known.read("p", Instant::now(), &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: Vec::new(), actors_at: Instant::now(), steps: Default::default() }));
    assert_eq!(known.opened(), ["old".to_string()], "projects the overview reads anyway aren't read twice");
}

#[test]
fn opening_a_project_the_overview_already_reads_keeps_following_it_after_it_goes_quiet() {
    let mut known = super::live::Known::default();
    known.learn(&fetched("2026-11-24T12:00:00Z", Vec::new(), vec![("p", Ok(Vec::new()))]));
    assert!(!known.open("p"), "its rows are already held");
    assert!(known.open("new"), "a project with nothing held is read");
    known.learn(&fetched("2026-11-24T12:01:00Z", Vec::new(), Vec::new()));
    let mut opened = known.opened();
    opened.sort();
    assert_eq!(opened, ["new", "p"]);
    assert_eq!(known.changed("p", Instant::now()), Change::Read("p".into()), "once out of the overview, its changes still read it");
}

#[test]
fn a_reset_keeps_every_opened_project_but_forgets_what_was_read() {
    let mut known = super::live::Known::default();
    known.learn(&fetched("2026-11-24T12:00:00Z", Vec::new(), vec![("p", Ok(Vec::new()))]));
    known.open("p");
    known.read("old", Instant::now(), &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: vec!["gone".into()], actors_at: Instant::now(), steps: Default::default() }));
    assert_eq!(known.opened(), ["old".to_string()], "only projects outside the overview are read again");
    let mut fresh = known.carry();
    assert_eq!(fresh.opened(), ["old".to_string()], "the overview still reads the other");
    assert_eq!(fresh.changed("old", Instant::now()), Change::Read("old".into()), "both stay followed");
    assert!(!fresh.skips_ledger("p", Some(Instant::now() - std::time::Duration::from_secs(5))) && !fresh.skips_actor("gone"));
    assert!(fresh.open("p"), "nothing is held any more");
}

#[test]
fn a_failed_lookup_sets_the_overview_retry_timer() {
    let mut known = super::live::Known::default();
    let at = Instant::now();
    known.read("old", at, &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: vec!["gone".into()], actors_at: at, steps: Default::default() }));
    assert_eq!(known.next_retry(), Some(at + super::live::RETRY));
    assert!(!known.actors_due(at) && known.actors_due(at + super::live::RETRY));
}

#[test]
fn a_due_lookup_retires_once_an_overview_no_longer_asks_for_it_and_a_failed_overview_waits() {
    let mut known = super::live::Known::default();
    let long_ago = Instant::now() - std::time::Duration::from_secs(120);
    known.read("old", long_ago, &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: vec!["listed-now".into()], actors_at: long_ago, steps: Default::default() }));
    known.unread(Instant::now());
    assert!(!known.actors_due(Instant::now()), "a failed overview read puts due retries off a minute");
    assert!(known.skips_actor("listed-now"));
    let mut known = super::live::Known::default();
    known.read("old", long_ago, &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: vec!["listed-now".into()], actors_at: long_ago, steps: Default::default() }));
    assert!(known.actors_due(Instant::now()));
    known.learn(&fetched("2026-11-24T12:00:00Z", Vec::new(), Vec::new()));
    assert!(!known.actors_due(Instant::now()) && known.next_retry().is_none(), "an overview that no longer asks retires it");
}

#[test]
fn an_opened_project_that_drops_out_of_the_overview_after_a_reset_is_read_on_its_own() {
    let mut known = super::live::Known::default();
    known.learn(&fetched("2026-11-24T12:00:00Z", Vec::new(), vec![("p", Ok(Vec::new()))]));
    known.open("p");
    let mut known = known.carry();
    assert!(known.orphans().is_empty(), "the overview after a reset reads it with the rest");
    known.learn(&fetched("2026-11-24T12:01:00Z", Vec::new(), Vec::new()));
    assert_eq!(known.orphans(), ["p".to_string()], "the overview no longer reads it, so it is read alone");
    assert_eq!(known.changed("p", Instant::now()), Change::Read("p".into()));
    known.read("p", Instant::now(), &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: Vec::new(), actors_at: Instant::now(), steps: Default::default() }));
    assert!(known.orphans().is_empty());
}

#[test]
fn an_opened_project_reads_its_ledger_under_the_snapshots_key() {
    let mut known = super::live::Known::default();
    assert_eq!(known.ledger_key_of("p"), "p", "before any overview the id is all there is");
    known.learn(&keyed(Instant::now()));
    assert_eq!(known.ledger_key_of("p"), "shared:x");
    assert_eq!(known.ledger_key_of("q"), "q");
    for key in ["repo:github.com/o/new", "repo:github.com/o/newer"] {
        let mut moved = keyed(Instant::now());
        moved.recorded.projects[0].ledger_key = key.into();
        known.learn(&moved);
        assert_eq!(known.ledger_key_of("p"), key, "a moved ledger is read under its latest key, never an older one");
    }
}

#[test]
fn a_project_whose_ledger_key_moved_is_read_again_even_when_its_stream_is_quiet() {
    let mut known = super::live::Known::default();
    let settled = Some(Instant::now() + std::time::Duration::from_secs(60));
    let read = keyed(Instant::now() + std::time::Duration::from_secs(120));
    known.learn(&read);
    assert!(known.holds("p", "shared:x", settled), "the same key with a quiet stream keeps the rows it has");
    assert!(!known.holds("p", "repo:github.com/o/moved", settled), "a new key means the rows on screen belong to the old ledger");
}

#[test]
fn usage_read_in_one_overview_is_kept_for_two_minutes() {
    let mut known = super::live::Known::default();
    let t0 = Instant::now();
    assert_eq!(known.usage_kept(t0), None);
    let meters = vec![serde_json::json!({"provider": "claude", "percent": 12})];
    let read_at = t0 + std::time::Duration::from_secs(20);
    known.learn(&stamped(Fetched { usage: Some((read_at, Some(meters.clone()))), ..fetched("2026-11-24T12:00:00Z", Vec::new(), Vec::new()) }, t0));
    assert_eq!(known.usage_kept(t0 + std::time::Duration::from_secs(60)), Some(meters.clone()), "an overview a minute later, after an actor retry say, reuses it");
    assert_eq!(known.usage_kept(t0 + super::live::USAGE_EVERY), Some(meters.clone()), "two minutes run from the usage read itself, not from the overview that began earlier");
    known.learn(&stamped(fetched("2026-11-24T12:01:00Z", Vec::new(), Vec::new()), t0 + std::time::Duration::from_secs(60)));
    assert_eq!(known.usage_kept(t0 + std::time::Duration::from_secs(90)), Some(meters.clone()), "an overview that reused it doesn't extend it");
    assert_eq!(known.usage_kept(read_at + super::live::USAGE_EVERY), None);
    let failed_at = read_at + super::live::USAGE_EVERY;
    known.learn(&stamped(Fetched { usage: Some((failed_at, None)), ..fetched("2026-11-24T12:03:00Z", Vec::new(), Vec::new()) }, failed_at));
    assert_eq!(known.usage_kept(failed_at + std::time::Duration::from_secs(5)), Some(meters), "a failed read waits two minutes too, as the Mac app does, and the last figures stay");
}

#[test]
fn a_moved_ledger_key_drops_the_old_rows_even_when_the_new_read_fails_or_the_project_is_quiet() {
    let mut known = super::live::Known::default();
    let first = keyed(Instant::now());
    known.learn(&first);
    assert!(!known.open("p"), "its rows are held");
    let keys: HashMap<String, String> = [("p".to_string(), "repo:github.com/o/moved".to_string())].into();
    assert_eq!(known.moved(&keys), vec!["p".to_string()]);
    let mut quiet = fetched("2026-11-24T12:05:00Z", Vec::new(), Vec::new());
    quiet.recorded.projects = first.recorded.projects.clone();
    quiet.recorded.projects[0].ledger_key = "repo:github.com/o/moved".into();
    quiet.moved = vec!["p".into()];
    known.learn(&quiet);
    assert_eq!(known.orphans(), vec!["p".to_string()], "an opened project outside the overview is read again under the new key");
    let mut ov = Overview::default();
    ov.refresh(first);
    assert_eq!(board_ids(&ov.world(&[]), "p"), vec!["p-W1".to_string()]);
    let mut failed = fetched("2026-11-24T12:05:00Z", Vec::new(), vec![("p", Err("status code 503".into()))]);
    failed.recorded.projects = quiet.recorded.projects.clone();
    failed.moved = vec!["p".into()];
    ov.refresh(failed);
    assert!(board_ids(&ov.world(&[]), "p").is_empty(), "a failed read under the new key never shows the old ledger's work as the new one's");
}

#[test]
fn a_reconnect_keeps_the_usage_wait() {
    let mut known = super::live::Known::default();
    let t0 = Instant::now();
    let meters = vec![serde_json::json!({"provider": "claude", "percent": 3})];
    known.learn(&stamped(Fetched { usage: Some((t0, Some(meters.clone()))), ..fetched("2026-11-24T12:00:00Z", Vec::new(), Vec::new()) }, t0));
    let fresh = known.carry();
    assert_eq!(fresh.usage_kept(t0 + std::time::Duration::from_secs(30)), Some(meters));
}

#[test]
fn a_ledger_change_waits_out_a_failed_reads_retry() {
    let mut known = super::live::Known::default();
    let t0 = Instant::now();
    assert!(known.open("p"));
    known.read("p", t0, &Err("status code 503".into()));
    assert!(matches!(known.changed("p", t0 + std::time::Duration::from_secs(1)), Change::Nothing), "a change inside the wait leaves it to the retry");
}

#[test]
fn project_activity_lists_a_project_whose_sessions_are_all_older_and_dates_it() {
    let projects = json!([{"id": "p", "displayName": "P"}, {"id": "quiet", "displayName": "Quiet"}, {"id": "gone", "displayName": "Gone"}]);
    let activity: Vec<crate::activity::ProjectActivity> = serde_json::from_value(json!([{"id": "quiet", "lastActive": "2026-11-01T00:00:00Z", "sessionCount": 3}, {"id": "gone", "sessionCount": 0}])).unwrap();
    let merged = super::live::with_activity(projects, activity);
    assert_eq!(merged[1]["sessionCount"], json!(3));
    assert_eq!(merged[2].get("lastActive"), None);
    let mut r: Recorded = serde_json::from_value(json!({"updatedAt": "2026-11-24T12:00:00Z", "projects": merged})).unwrap();
    r.sessions = vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")];
    let w = World::new(r);
    let keys: Vec<&str> = w.projects.iter().map(|p| p.key.as_str()).collect();
    assert_eq!(keys, vec!["p", "quiet"]);
    assert_eq!(w.project("quiet").unwrap().last_active, "2026-11-01T00:00:00Z");
}

#[test]
fn a_failed_activity_read_keeps_the_last_counts() {
    let mut ov = Overview::default();
    let mut first = fetched("2026-11-24T12:00:00Z", vec![], vec![]);
    first.recorded.projects[1].session_count = 2;
    first.recorded.projects[1].last_active = "2026-11-01T00:00:00Z".into();
    ov.refresh(first);
    assert!(ov.world(&[]).project("old").is_some());
    let mut second = fetched("2026-11-24T12:00:20Z", vec![], vec![]);
    second.recorded.activity_missing = true;
    ov.refresh(second);
    let w = ov.world(&[]);
    assert_eq!(w.project("old").map(|p| p.last_active.as_str()), Some("2026-11-01T00:00:00Z"));
    let mut third = fetched("2026-11-24T12:00:40Z", vec![], vec![]);
    third.recorded.activity_missing = false;
    ov.refresh(third);
    assert!(ov.world(&[]).project("old").is_none(), "a read that answers replaces the counts");
}

#[test]
fn the_person_reads_as_you_wherever_they_acted() {
    let w = Overview::default().world(&[]);
    assert_eq!(w.name_of(crate::ledger::PERSON), "You");
    assert_eq!(w.name_of(""), "Someone");
}

#[test]
fn open_work_in_a_project_with_no_lead_is_anyones_move() {
    let mut ov = Overview::default();
    ov.refresh(fetched("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")], vec![("p", Ok(vec![work("w1", "open")]))]));
    let w = ov.world(&[]);
    assert_eq!(w.whose_move(&w.work[&WorkKey::new("p", "w1")]), "Anyone");
}

#[test]
fn the_persons_move_reads_as_yours() {
    assert_eq!(super::phase::move_of("You"), "Your move");
    assert_eq!(super::phase::move_of("@lead"), "@lead's move");
}

#[test]
fn work_the_person_holds_is_their_move_whoever_logged_before() {
    let mut held = work("w1", "in_progress");
    held.assignee = Some(crate::ledger::PERSON.into());
    held.timeline.push(serde_json::from_value(json!({"at": "2026-11-24T11:30:00Z", "actor": "a", "kind": "log", "text": "started"})).unwrap());
    let mut ov = Overview::default();
    ov.refresh(fetched("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")], vec![("p", Ok(vec![held]))]));
    let w = ov.world(&[]);
    assert_eq!(w.whose_move(&w.work[&WorkKey::new("p", "w1")]), "You");
}

#[test]
fn a_search_finds_settled_work_after_the_active_work() {
    let mut ov = Overview::default();
    let items = vec![work("w1 export", "in_progress"), work("w2 export", "closed"), work("w3 export", "done"), work("w4 export", "parked"), work("w5 other", "closed"), work("w6 export", "blocked")];
    ov.refresh(fetched("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")], vec![("p", Ok(items))]));
    let w = ov.world(&[]);
    let found: Vec<(String, String)> = w.jump("export", &[], &[]).into_iter().filter(|h| h.group == "Work").map(|h| (h.title, h.status.split(" · ").next().unwrap_or_default().to_string())).collect();
    assert_eq!(found[0].0, "w1 export");
    let mut settled: Vec<(String, String)> = found[1..].to_vec();
    settled.sort();
    assert_eq!(settled, [("w2 export".into(), "Closed".into()), ("w3 export".into(), "Done".into()), ("w4 export".into(), "Parked".into()), ("w6 export".into(), "Blocked".into())]);
    assert!(w.jump("", &[], &[]).iter().all(|h| h.title != "w2 export"), "an empty search lists only active work");
}

#[test]
fn parked_and_closed_work_is_no_ones_move() {
    let mut ov = Overview::default();
    ov.refresh(fetched("2026-11-24T12:00:00Z", vec![session("a", "p", "2026-11-24T11:59:00Z", "idle")], vec![("p", Ok(vec![work("w1", "parked"), work("w2", "closed")]))]));
    let w = ov.world(&[]);
    assert_eq!(w.whose_move(&w.work[&WorkKey::new("p", "w1")]), "No one");
    assert_eq!(super::phase::move_of(&w.whose_move(&w.work[&WorkKey::new("p", "w2")])), "No one's move");
}

#[test]
fn a_parked_step_shows_why_it_waits() {
    let mut w = work("w1", "parked");
    w.timeline.push(serde_json::from_value(json!({"at": "2026-11-24T11:30:00Z", "actor": "agenthail-person", "kind": "state", "state": "parked", "text": "parked (later): after the release"})).unwrap());
    w.timeline.push(serde_json::from_value(json!({"at": "2026-11-24T11:40:00Z", "actor": "x", "kind": "state", "state": "parked", "text": "handoff: done []"})).unwrap());
    let steps = super::timeline::timeline(&w, 5);
    assert_eq!(steps[1].what, "Parked: after the release");
    let mut semi = work("w2", "parked");
    semi.timeline.push(serde_json::from_value(json!({"at": "2026-11-24T11:30:00Z", "actor": "x", "kind": "state", "state": "parked", "text": "took over; parked (later): wait for release; then check logs; handoff: done [\"a\"], remaining [], decisions [], uncertain []"})).unwrap());
    assert_eq!(super::timeline::timeline(&semi, 5)[0].what, "Parked: wait for release; then check logs");
    let words = |text: &str| {
        let mut w = work("w3", "parked");
        w.timeline.push(serde_json::from_value(json!({"at": "2026-11-24T11:30:00Z", "actor": "x", "kind": "state", "state": "parked", "text": text})).unwrap());
        super::timeline::timeline(&w, 5)[0].what.clone()
    };
    assert_eq!(words("parked (later): wait; handoff: ask Sam"), "Parked: wait; handoff: ask Sam");
    assert_eq!(words("parked (later): wait; handoff: ask Sam; handoff: done [\"a\"], remaining [], decisions [], uncertain []"), "Parked: wait; handoff: ask Sam");
    assert_eq!(words("parked (later): wait; handoff: done [ask Sam]"), "Parked: wait; handoff: done [ask Sam]");
    assert!(steps[0].what.starts_with("Moved to "), "{}", steps[0].what);
}

#[test]
fn a_session_is_left_to_its_own_reads_only_once_its_project_ledger_is_held() {
    let mut ov = Overview::default();
    let sessions = || vec![session("s", "p", "2026-11-24T11:00:00Z", "idle"), session("quiet", "old", "2026-11-01T11:00:00Z", "idle")];
    ov.refresh(fetched("2026-11-24T12:00:00Z", sessions(), vec![("p", Ok(vec![work("p-W1", "open")]))]));
    assert!(ov.holds_ledger_of("s"));
    assert!(!ov.holds_ledger_of("quiet"), "a change can bring a quiet project's work into the overview");
    assert!(!ov.holds_ledger_of("unknown"));
}

#[test]
fn a_quiet_session_turning_active_asks_for_its_project_work() {
    let mut ov = Overview::default();
    let held = || session("s", "p", "2026-11-24T11:00:00Z", "idle");
    ov.refresh(fetched("2026-11-24T12:00:00Z", vec![held(), session("quiet", "old", "2026-11-01T11:00:00Z", "idle")], vec![("p", Ok(vec![work("p-W1", "open")]))]));
    assert!(ov.missing_ledgers().is_empty(), "every recent project's work is held");
    assert!(ov.take_sessions("2026-11-24T12:00:05Z", "", 0, vec![held(), session("quiet", "old", "2026-11-24T12:00:04Z", "busy")]));
    assert_eq!(ov.missing_ledgers().into_iter().collect::<Vec<_>>(), ["old"], "the change list alone brought a quiet project back");
    assert!(ov.wants("old"));
    assert!(ov.missing_ledgers().is_empty(), "asked for once");
}

#[test]
fn a_project_unarchived_by_its_session_comes_back_with_the_session_list() {
    let mut ov = Overview::default();
    let row = |archived: bool, last: &str| {
        let mut x = session("s", "old", last, "idle");
        x.project.as_mut().unwrap().archived = Some(archived);
        x
    };
    let mut first = fetched("2026-11-24T12:00:00Z", vec![row(true, "2026-11-01T11:00:00Z")], vec![]);
    first.recorded.projects.iter_mut().filter(|p| p.id == "old").for_each(|p| p.archived = true);
    ov.refresh(first);
    assert!(ov.missing_ledgers().is_empty());
    let _ = ov.world(&[]);
    assert!(ov.take_sessions("2026-11-24T12:00:05Z", "", 0, vec![row(false, "2026-11-24T12:00:04Z")]));
    assert!(ov.stale());
    assert_eq!(ov.missing_ledgers().into_iter().collect::<Vec<_>>(), ["old"], "the session's word on its project is taken at once");
    assert!(ov.world(&[]).projects.iter().any(|p| p.key == "old"));
}

#[test]
fn a_revived_project_stays_shown_whatever_the_row_order_or_an_older_overview_says() {
    let mut ov = Overview::default();
    let row = |id: &str, archived: bool, last: &str| {
        let mut x = session(id, "old", last, "idle");
        x.project.as_mut().unwrap().archived = Some(archived);
        x
    };
    let archived = |at: &str, sessions: Vec<Session>| {
        let mut f = fetched(at, sessions, vec![]);
        f.recorded.projects.iter_mut().filter(|p| p.id == "old").for_each(|p| p.archived = true);
        f
    };
    ov.refresh(archived("2026-11-24T12:00:00Z", vec![row("a", true, "2026-11-01T11:00:00Z"), row("b", true, "2026-11-01T11:00:00Z")]));
    assert!(ov.take_sessions("2026-11-24T12:00:05Z", "", 0, vec![row("a", false, "2026-11-24T12:00:04Z"), row("b", true, "2026-11-01T11:00:00Z")]));
    assert_eq!(ov.missing_ledgers().len(), 1, "one row's word is enough, before or after the others");
    ov.refresh(archived("2026-11-24T12:00:02Z", vec![row("a", true, "2026-11-01T11:00:00Z"), row("b", true, "2026-11-01T11:00:00Z")]));
    assert!(ov.world(&[]).projects.iter().any(|p| p.key == "old"), "an overview read before the revival doesn't hide it again");
    assert_eq!(ov.missing_ledgers().len(), 1);
}

#[test]
fn a_project_archived_after_its_work_was_read_reads_it_again_when_revived() {
    let mut ov = Overview::default();
    let row = |archived: bool, last: &str| {
        let mut x = session("s", "old", last, "idle");
        x.project.as_mut().unwrap().archived = Some(archived);
        x
    };
    ov.refresh(fetched("2026-11-24T12:00:00Z", vec![row(false, "2026-11-24T11:00:00Z")], vec![("old", Ok(vec![work("old-W1", "open")]))]));
    assert!(ov.holds_ledger_of("s"));
    let mut archived = fetched("2026-11-24T12:01:00Z", vec![row(true, "2026-11-24T11:00:00Z")], vec![]);
    archived.recorded.projects.iter_mut().filter(|p| p.id == "old").for_each(|p| p.archived = true);
    ov.refresh(archived);
    assert!(!ov.holds_ledger_of("s"), "an archived project's work is let go");
    assert!(ov.take_sessions("2026-11-24T12:02:00Z", "", 0, vec![row(false, "2026-11-24T12:01:59Z")]));
    assert_eq!(ov.missing_ledgers().into_iter().collect::<Vec<_>>(), ["old"], "revived, its work is read again");
}

#[test]
fn a_reread_ledger_hands_back_the_same_rows_except_the_one_whose_value_changed() {
    let steps = super::phase::Steps::default();
    let rows = || vec![work("w1", "open"), work("w2", "open"), work("w3", "open")];
    let first = super::live::shared_rows(None, "p", "shared:p", &steps, rows());
    let again = super::live::shared_rows(Some(&first), "p", "shared:p", &steps, rows());
    assert!(first.iter().zip(&again).all(|(a, b)| Arc::ptr_eq(a, b)), "an unchanged read is the same rows");
    let mut changed = rows();
    changed[1].timeline[0].actor = "someone".into();
    let next = super::live::shared_rows(Some(&again), "p", "shared:p", &steps, changed.clone());
    assert!(Arc::ptr_eq(&next[0], &again[0]) && Arc::ptr_eq(&next[2], &again[2]));
    assert!(!Arc::ptr_eq(&next[1], &again[1]), "one field deep in one item makes only that item new");
    assert_eq!(next[1].timeline[0].actor, "someone");
    let moved = super::live::shared_rows(Some(&next), "p", "shared:q", &steps, changed);
    assert!(moved.iter().zip(&next).all(|(a, b)| !Arc::ptr_eq(a, b)), "a new ledger key is a new value for every row");
    assert!(moved.iter().all(|w| w.ledger_key == "shared:q" && w.project == "p"));
}

#[test]
fn an_unchanged_session_list_is_the_same_list() {
    let list = || vec![session("a", "p", "2026-11-24T11:00:00Z", "idle"), session("b", "p", "2026-11-24T11:00:00Z", "busy")];
    let first = super::live::shared_sessions(&Arc::default(), list());
    assert!(Arc::ptr_eq(&first, &super::live::shared_sessions(&first, list())));
    let mut busy = list();
    busy[0].status = "busy".into();
    let next = super::live::shared_sessions(&first, busy);
    assert!(!Arc::ptr_eq(&first, &next));
    assert_eq!(status_of(&next, "a"), "busy");
}

fn status_of(sessions: &[Session], id: &str) -> String {
    sessions.iter().find(|x| x.id == id).map(|x| x.status.clone()).unwrap_or_default()
}

#[test]
fn the_overview_shows_the_same_world_as_the_recording_it_holds() {
    let mut raw: Recorded = serde_json::from_value(json!({"updatedAt": "2026-11-24T12:00:00Z", "projects": [{"id": "p", "displayName": "P", "ledgerKey": "shared:p"}], "phaseLabels": {"p": {"build": "Craft"}}})).unwrap();
    raw.sessions = vec![session("a", "p", "2026-11-24T11:00:00Z", "idle")];
    raw.ledgers.insert("p".into(), vec![work("p-W1", "open"), work("p-W2", "in_progress")]);
    raw.transcripts.insert("a".into(), Vec::new());
    let recorded = World::new(raw.clone());
    let shown = Overview::recorded(raw).world(&[]);
    assert_eq!(*recorded.sessions, *shown.sessions);
    assert_eq!(recorded.work, shown.work);
    assert!(shown.work.values().all(|w| w.ledger_key == "shared:p" && w.project == "p" && w.steps == recorded.steps_of("p")));
    assert_eq!(recorded.names, shown.names);
    assert_eq!(recorded.transcripts, shown.transcripts);
    assert_eq!(recorded.projects.iter().map(|p| &p.key).collect::<Vec<_>>(), shown.projects.iter().map(|p| &p.key).collect::<Vec<_>>());
}

#[test]
fn rows_read_before_their_project_had_a_ledger_key_take_it_when_it_arrives() {
    let mut ov = Overview::default();
    ov.refresh(fetched("2026-11-24T12:00:00Z", Vec::new(), vec![("p", Ok(vec![work("p-W1", "open")]))]));
    assert!(ov.world(&[]).work.contains_key(&WorkKey::new("p", "p-W1")));
    let mut keyed = fetched("2026-11-24T12:01:00Z", Vec::new(), vec![]);
    keyed.recorded.projects[0].ledger_key = "shared:p".into();
    ov.refresh(keyed);
    let w = ov.world(&[]);
    assert!(w.work.contains_key(&WorkKey::new("shared:p", "p-W1")) && !w.work.contains_key(&WorkKey::new("p", "p-W1")));
}

#[test]
fn the_watcher_keeps_rows_only_for_projects_it_still_reads() {
    let mut known = super::live::Known::default();
    let read = |held: &[&str], ledgers: Vec<(&str, Result<Vec<Work>, String>)>| {
        let mut f = Fetched { held: held.iter().map(|x| x.to_string()).collect(), ..fetched("2026-11-24T12:00:00Z", Vec::new(), ledgers) };
        f.recorded.projects.push(serde_json::from_value(json!({"id": "opened"})).unwrap());
        f
    };
    known.learn(&read(&[], vec![("p", Ok(vec![work("p-W1", "open")])), ("old", Ok(vec![work("old-W1", "open")]))]));
    assert_eq!(known.rows_held().into_iter().collect::<Vec<_>>(), ["old", "p"]);
    known.read("opened", Instant::now(), &Ok(Ledger { rows: vec![work("o-W1", "open").into()], actors: HashMap::new(), asked: Vec::new(), actors_at: Instant::now(), steps: Default::default() }));
    known.learn(&read(&["p"], vec![]));
    assert_eq!(known.rows_held().into_iter().collect::<Vec<_>>(), ["opened", "p"], "a project no longer read lets its rows go; held and opened ones keep theirs");
    let mut archived = read(&["p"], vec![]);
    archived.recorded.projects.iter_mut().filter(|p| p.id == "opened").for_each(|p| p.archived = true);
    known.learn(&archived);
    assert_eq!(known.rows_held().into_iter().collect::<Vec<_>>(), ["p"], "an opened project archived since lets its rows go");
    known.read("opened", Instant::now(), &Ok(Ledger { rows: vec![work("o-W1", "open").into()], actors: HashMap::new(), asked: Vec::new(), actors_at: Instant::now(), steps: Default::default() }));
    let mut removed = read(&["p"], vec![]);
    removed.recorded.projects.retain(|p| p.id != "opened");
    known.learn(&removed);
    assert_eq!(known.rows_held().into_iter().collect::<Vec<_>>(), ["p"], "so does one no longer listed at all");
}
