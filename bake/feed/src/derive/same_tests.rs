use std::sync::Arc;

use serde_json::json;

use super::World;

fn world(updated: &str) -> World {
    let v = json!({
        "updatedAt": updated,
        "projects": [{"id": "p", "displayName": "P"}],
        "sessions": [{"id": "s1", "name": "s1", "lastActive": "2026-11-24T11:50:00Z", "project": {"id": "p", "name": "P"}}],
        "usage": [{"provider": "claude", "label": "5h", "percent": 40.0}],
        "hosts": [{"id": "h", "name": "studio", "state": "local"}],
        "ledgers": {"p": [{"id": "p-A", "text": "Ship the parser", "state": "done", "timeline": [{"at": "2026-11-24T09:00:00Z", "kind": "state", "state": "done"}]}]}
    });
    World::from_json(&v.to_string()).unwrap()
}

fn sharing(a: &World, updated: &str) -> World {
    let mut b = world(updated);
    b.sessions = a.sessions.clone();
    b.work = a.work.clone();
    b
}

#[test]
fn ages_read_the_minute_the_world_was_seen_in() {
    let w = world("2026-11-24T12:00:59Z");
    assert_eq!(w.shown, world("2026-11-24T12:00:00Z").now);
    assert_eq!(w.ago("2026-11-24T11:59:30Z"), "1m", "30 s before the minute is half a minute old, rounded up");
    assert_eq!(w.ago("2026-11-24T11:55:00Z"), "5m", "seconds past the minute don't age a line");
}

#[test]
fn a_world_with_the_same_rows_in_the_same_minute_shows_the_same() {
    let a = world("2026-11-24T12:00:05Z");
    assert!(a.same_shown(&sharing(&a, "2026-11-24T12:00:50Z")), "a refresh later in the same minute");
    assert!(!a.same_shown(&sharing(&a, "2026-11-24T12:01:00Z")), "a refresh in the next minute");
    assert!(!a.same_shown(&world("2026-11-24T12:00:05Z")), "rows read again are new rows");
    let mut b = sharing(&a, "2026-11-24T12:00:05Z");
    Arc::make_mut(&mut b.sessions)[0].name = "renamed".into();
    assert!(!a.same_shown(&b), "a changed session");
    let mut b = sharing(&a, "2026-11-24T12:00:05Z");
    b.usage[0].percent = Some(80.0);
    assert!(!a.same_shown(&b), "a usage meter");
    let mut b = sharing(&a, "2026-11-24T12:00:05Z");
    b.hosts[0].state = "unreachable".into();
    assert!(!a.same_shown(&b), "a computer's state");
    let mut b = sharing(&a, "2026-11-24T12:00:05Z");
    b.records[0].display_name = "Renamed".into();
    assert!(!a.same_shown(&b), "a project's name");
    let mut b = sharing(&a, "2026-11-24T12:00:05Z");
    b.rows.insert("p".into(), super::super::world::Rows::Loading);
    assert!(!a.same_shown(&b), "a ledger loading");
    let mut b = sharing(&a, "2026-11-24T12:00:05Z");
    b.prompted_before.insert("s1".into());
    assert!(!a.same_shown(&b), "a session's earlier prompt");
}
