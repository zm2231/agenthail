use std::collections::HashMap;
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
    Fetched { recorded: recorded(at, sessions), ledgers: ledgers.into_iter().map(|(k, r)| (k.to_string(), r)).collect(), held: Vec::new(), asked: Vec::new(), started: Instant::now() }
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
    ov.take_ledger("old", Instant::now(), Ok(Ledger { rows: vec![work("w9", "open")], actors: HashMap::new(), asked: Vec::new() }));
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
    ov.take_ledger("p", later, Ok(Ledger { rows: vec![work("new", "open")], actors: HashMap::new(), asked: Vec::new() }));
    ov.refresh(Fetched { started: earlier, ..fetched("2026-11-24T12:00:00Z", sessions(), vec![("p", Ok(vec![work("old", "open")]))]) });
    assert_eq!(board_ids(&ov.world(&[]), "p"), ["new"], "a refresh that started first and landed last");
    ov.take_ledger("p", earlier, Err("timed out".into()));
    assert_eq!(ov.world(&[]).rows.get("p"), None, "nor does its failure mark the newer rows stale");
    let latest = later + std::time::Duration::from_millis(1);
    ov.refresh(Fetched { started: latest, ..fetched("2026-11-24T12:00:20Z", sessions(), vec![("p", Ok(vec![work("newest", "open")]))]) });
    ov.take_ledger("p", later, Ok(Ledger { rows: vec![work("new", "open")], actors: HashMap::new(), asked: Vec::new() }));
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
    ov.take_ledger("p", later, Ok(Ledger { rows: vec![], actors: actor("fresh"), asked: Vec::new() }));
    let mut stale = fetched("2026-11-24T12:00:00Z", sessions(), vec![]);
    stale.recorded.actors = actor("stale");
    ov.refresh(Fetched { started: earlier, ..stale });
    assert!(ov.world(&[]).name_of("helper-1").contains("fresh"), "a refresh that started first and landed last");
    let latest = later + std::time::Duration::from_millis(1);
    let mut newest = fetched("2026-11-24T12:00:20Z", sessions(), vec![]);
    newest.recorded.actors = actor("newest");
    ov.refresh(Fetched { started: latest, ..newest });
    ov.take_ledger("p", later, Ok(Ledger { rows: vec![], actors: actor("fresh"), asked: Vec::new() }));
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
        ov.take_tail(id, "2026-11-24T11:59:00Z".into(), Err("timed out".into()));
    }
    ov.take_ledger("p", Instant::now(), Ok(Ledger { rows: vec![work("w1", "open")], actors: HashMap::new(), asked: Vec::new() }));
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
    let mut f = Fetched { started, ..fetched("2026-11-24T12:00:00Z", Vec::new(), vec![("p", Ok(vec![work("p-W1", "open")]))]) };
    f.recorded.projects[0].ledger_key = "shared:x".into();
    f
}

#[test]
fn a_ledger_that_changes_during_an_overview_read_is_read_again() {
    let mut known = super::live::Known::default();
    let f = keyed(Instant::now() - std::time::Duration::from_millis(10));
    assert_eq!(known.changed("shared:x", Instant::now()), Change::Nothing, "nothing is known yet; the read in flight checks for itself");
    assert!(known.learn(&f), "the read began before the change, so it goes again");
    assert!(!known.skips_ledger("p", Some(f.started)));
    assert_eq!(known.changed("shared:x", Instant::now()), Change::Overview, "a wanted ledger that isn't held needs the overview");
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
    let read = Ok(Ledger { rows: vec![work("p-W1", "open")], actors: HashMap::new(), asked: vec!["missing".into()] });
    let at = Instant::now();
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
    known.read("old", opened, &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: Vec::new() }));
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
    assert_eq!(known.changed("old", Instant::now()), Change::Read("old".into()), "a change to a failed one tries at once");
    known.read("old", Instant::now(), &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: Vec::new() }));
    assert_eq!(known.next_retry(), None);
}

#[test]
fn failed_lookups_come_due_when_the_next_overview_starts() {
    let mut known = super::live::Known::default();
    let started = Instant::now() - std::time::Duration::from_secs(61);
    let f = Fetched { started, asked: vec!["gone".into()], ..fetched("2026-11-24T12:00:00Z", Vec::new(), vec![("h2::p", Err("status code 404".into()))]) };
    known.learn(&f);
    assert!(!known.skips_actor("gone") && !known.skips_ledger("h2::p", None), "retries count from when the read started");
}

#[test]
fn a_reopened_ledger_stream_reads_opened_projects_again() {
    let mut known = super::live::Known::default();
    known.learn(&fetched("2026-11-24T12:00:00Z", Vec::new(), vec![("p", Ok(Vec::new()))]));
    known.read("old", Instant::now(), &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: Vec::new() }));
    known.read("p", Instant::now(), &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: Vec::new() }));
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
    known.read("old", Instant::now(), &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: vec!["gone".into()] }));
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
    known.read("old", at, &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: vec!["gone".into()] }));
    assert_eq!(known.next_retry(), Some(at + super::live::RETRY));
    assert!(!known.actors_due(at) && known.actors_due(at + super::live::RETRY));
}

#[test]
fn a_due_lookup_retires_once_an_overview_no_longer_asks_for_it_and_a_failed_overview_waits() {
    let mut known = super::live::Known::default();
    let long_ago = Instant::now() - std::time::Duration::from_secs(120);
    known.read("old", long_ago, &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: vec!["listed-now".into()] }));
    known.unread(Instant::now());
    assert!(!known.actors_due(Instant::now()), "a failed overview read puts due retries off a minute");
    assert!(known.skips_actor("listed-now"));
    let mut known = super::live::Known::default();
    known.read("old", long_ago, &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: vec!["listed-now".into()] }));
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
    known.read("p", Instant::now(), &Ok(Ledger { rows: Vec::new(), actors: HashMap::new(), asked: Vec::new() }));
    assert!(known.orphans().is_empty());
}
