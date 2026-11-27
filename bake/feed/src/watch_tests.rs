use serde_json::json;

use super::*;

fn events(text: &str) -> Vec<Event> {
    let mut out = Vec::new();
    read(std::io::Cursor::new(text.to_string()), |e| {
        out.push(e);
        true
    })
    .unwrap();
    out
}

fn upsert(seq: u64, id: &str, status: &str) -> CatalogEvent {
    CatalogEvent { seq, kind: "session.upserted".into(), data: json!({"session": {"id": id, "status": status}}) }
}

fn row(id: &str, status: &str) -> Session {
    serde_json::from_value(json!({"id": id, "status": status})).unwrap()
}

fn statuses(c: &Catalog) -> Vec<(String, String)> {
    c.sessions().iter().map(|s| (s.id.clone(), s.status.clone())).collect()
}

#[test]
fn the_stream_reader_gives_each_event_its_kind_id_and_data() {
    let got = events(": connected\n\nid: 7\nevent: session.upserted\ndata: {\"stream\":\"catalog\",\"seq\":7,\"type\":\"session.upserted\",\"data\":{\"session\":{\"id\":\"a\"}}}\n\nevent: ledger.changed\ndata: {\"projectKey\":\"project:p-1\",\"version\":\"v1\"}\n\n");
    assert_eq!(got.len(), 2);
    assert_eq!((got[0].id, got[0].kind.as_str()), (7, "session.upserted"));
    assert_eq!(CatalogEvent::of(&got[0]).unwrap(), CatalogEvent { seq: 7, kind: "session.upserted".into(), data: json!({"session": {"id": "a"}}) });
    assert_eq!(got[1].data["projectKey"], "project:p-1");
    assert_eq!(CatalogEvent::of(&got[1]), None);
}

#[test]
fn the_native_rereads_refresh_the_snapshot_and_host_changes_the_decisions() {
    let e = |kind: &str, data: Value| Event { id: 1, kind: kind.into(), data };
    for kind in ["state.changed", "settings.updated", "signin.changed"] {
        let w = wants(&e(kind, Value::Null));
        assert!(w.snapshot && w.overview && !w.hosts && !w.reset, "{kind}");
    }
    for kind in ["host.paired", "host.removed", "host.state"] {
        let w = wants(&e(kind, Value::Null));
        assert!(w.snapshot && w.hosts && w.extensions, "{kind}");
    }
    let reset = wants(&e("stream.reset", Value::Null));
    assert!(reset.reset && reset.snapshot && reset.extensions);
    assert!(wants(&e("extension.badge", Value::Null)).extensions && wants(&e("extension.changed", Value::Null)).extensions);
    let quiet = wants(&e("extension.source", json!({"extension": "ideas"})));
    assert_eq!(quiet, Wants::default());
    let turn = wants(&e("turn.completed", json!({"entityId": "s1"})));
    assert_eq!(turn, Wants { session: Some("s1".into()), ..Wants::default() });
}

#[test]
fn a_change_to_one_known_session_rereads_the_snapshot_but_not_the_overview() {
    let e = |data: Value| wants(&Event { id: 1, kind: "state.changed".into(), data });
    let known = |id: &str| id == "s1";
    let session = e(json!({"entityId": "s1"}));
    assert!(session.snapshot && !session.overview_unless(known));
    assert!(e(json!({"entityId": "p-1"})).overview_unless(known), "a project or channel change can move what the overview shows");
    assert!(e(json!({"entityId": "s9"})).overview_unless(known), "a session not held yet");
    assert!(e(Value::Null).overview_unless(known), "a change that names nothing");
    let settings = wants(&Event { id: 2, kind: "settings.updated".into(), data: json!({"entityId": "s1"}) });
    assert!(settings.overview_unless(known));
    let mut both = session.clone();
    both.merge(e(Value::Null));
    assert!(both.overview_unless(known), "a burst that includes an unscoped change still rereads it");
    let mut two = Wants::default();
    two.merge(e(json!({"entityId": "s9"})));
    two.merge(e(json!({"entityId": "s1"})));
    assert!(two.overview_unless(known), "changes to two sessions, one not held yet");
    let mut same = Wants::default();
    same.merge(e(json!({"entityId": "s1"})));
    same.merge(e(json!({"entityId": "s1"})));
    assert!(!same.overview_unless(known));
}

#[test]
fn session_changes_apply_in_place_without_a_reread() {
    let mut c = Catalog::default();
    c.adopt(vec![row("a", "idle"), row("b", "idle")], "e", 10);
    let out = c.apply(vec![upsert(11, "a", "working")]);
    assert_eq!(out, Applied { changed: true, placed: true, touched: vec!["a".into()], ..Applied::default() });
    assert_eq!(statuses(&c), vec![("a".into(), "working".into()), ("b".into(), "idle".into())]);

    let out = c.apply(vec![upsert(12, "c", "idle"), CatalogEvent { seq: 13, kind: "session.removed".into(), data: json!({"sessionId": "b"}) }]);
    assert!(out.changed && out.news && !out.reread);
    assert_eq!(out.removed, vec![Removal { id: "b".into(), unlisted: false }]);
    let out = c.apply(vec![CatalogEvent { seq: 14, kind: "session.removed".into(), data: json!({"sessionId": "older"}) }]);
    assert!(!out.changed && out.removed == vec![Removal { id: "older".into(), unlisted: false }]);
    assert_eq!(statuses(&c), vec![("a".into(), "working".into()), ("c".into(), "idle".into())]);
    assert_eq!(c.seq(), 14);
}

#[test]
fn a_known_session_moves_in_the_list_only_when_its_placement_changes() {
    let placed = |seq: u64, id: &str, status: &str, placement: &str| CatalogEvent { seq, kind: "session.upserted".into(), data: json!({"session": {"id": id, "status": status, "placement": placement}}) };
    let mut c = Catalog::default();
    c.adopt(vec![serde_json::from_value(json!({"id": "a", "status": "idle", "placement": "p1"})).unwrap()], "e", 10);
    let same = c.apply(vec![placed(11, "a", "idle", "p1")]);
    assert!(same.changed && !same.placed && !same.news);
    let moved = c.apply(vec![placed(12, "a", "working", "p2")]);
    assert!(moved.placed && !moved.news, "a moved session rereads the list, not the overview");
    let queued = c.apply(vec![CatalogEvent { seq: 13, kind: "session.queue".into(), data: json!({"sessionId": "a", "queueCount": 1}) }]);
    assert!(queued.placed);
}

#[test]
fn a_session_dropped_from_the_list_is_told_apart_from_one_removed_or_rekeyed() {
    let mut c = Catalog::default();
    c.adopt(vec![row("a", "idle"), row("b", "idle"), row("c", "idle")], "e", 1);
    let out = c.apply(vec![
        CatalogEvent { seq: 2, kind: "session.removed".into(), data: json!({"sessionId": "a", "reason": "unlisted"}) },
        CatalogEvent { seq: 3, kind: "session.removed".into(), data: json!({"sessionId": "b", "reason": "omitted_from_successful_discovery"}) },
        CatalogEvent { seq: 4, kind: "session.removed".into(), data: json!({"sessionId": "c", "rekeyedTo": "c2"}) },
    ]);
    assert!(c.sessions().is_empty());
    assert_eq!(out.removed, vec![Removal { id: "a".into(), unlisted: true }, Removal { id: "b".into(), unlisted: false }, Removal { id: "c".into(), unlisted: false }]);
}

#[test]
fn a_queue_count_patches_the_row_and_a_rekey_asks_for_the_snapshot_that_carries_it() {
    let mut c = Catalog::default();
    c.adopt(vec![row("a", "idle")], "e", 1);
    let out = c.apply(vec![CatalogEvent { seq: 2, kind: "session.queue".into(), data: json!({"sessionId": "a", "queueCount": 2}) }]);
    assert!(out.changed && out.placed && !out.reread, "the queue rows come with the state.changed the daemon sends alongside");
    assert_eq!(c.sessions()[0].queue_count, 2);
    let out = c.apply(vec![CatalogEvent { seq: 3, kind: "session.removed".into(), data: json!({"sessionId": "a", "rekeyedTo": "a2"}) }]);
    assert!(out.reread && c.sessions().is_empty());
    let out = c.apply(vec![CatalogEvent { seq: 4, kind: "session.upserted".into(), data: json!({}) }]);
    assert!(out.reread && !out.changed);
}

#[test]
fn a_decisions_change_names_the_latest_revision_and_nothing_else() {
    let mut c = Catalog::default();
    c.adopt(Vec::new(), "e", 1);
    let rev = |seq, r: &str| CatalogEvent { seq, kind: "decisions.changed".into(), data: json!({"rev": r, "own": "o"}) };
    let out = c.apply(vec![rev(2, "r1"), rev(3, "r2")]);
    assert_eq!(out, Applied { rev: Some("r2".into()), ..Applied::default() });
}

#[test]
fn a_snapshot_read_before_events_already_seen_replays_them() {
    let mut c = Catalog::default();
    c.apply(vec![upsert(5, "a", "working")]);
    assert!(c.sessions().is_empty());
    c.adopt(vec![row("a", "idle")], "e", 4);
    assert_eq!(statuses(&c), vec![("a".into(), "working".into())]);
    c.adopt(vec![row("a", "done")], "e", 5);
    assert_eq!(statuses(&c), vec![("a".into(), "done".into())]);
    let out = c.apply(vec![upsert(5, "a", "working"), upsert(3, "a", "old")]);
    assert_eq!(out, Applied::default());
    assert_eq!(statuses(&c), vec![("a".into(), "done".into())]);
}

#[test]
fn a_ledger_counts_as_changed_only_when_its_version_moves() {
    let mut l = Ledgers::default();
    let e = |key: &str, v: &str| Event { id: 0, kind: "ledger.changed".into(), data: json!({"projectKey": key, "version": v}) };
    assert_eq!(l.changed(&e("project:p-1", "v1"), false), None);
    assert_eq!(l.changed(&e("project:p-1", "v1"), true), None);
    assert_eq!(l.changed(&e("project:p-1", "v2"), true).as_deref(), Some("project:p-1"));
    assert_eq!(l.changed(&e("shared:x", "v1"), false), None);
}

#[test]
fn reconnects_back_off_to_five_seconds() {
    assert_eq!([0, 1, 2, 3, 9].map(backoff), [1, 2, 4, 5, 5].map(Duration::from_secs));
}

#[test]
fn a_new_catalog_epoch_drops_events_from_the_old_one() {
    let mut c = Catalog::default();
    c.adopt(vec![row("a", "idle")], "e1", 10);
    c.apply(vec![upsert(50, "a", "old-epoch")]);
    assert!(c.adopt(vec![row("a", "fresh")], "e2", 3).restart, "a new epoch restarts the reader from the snapshot");
    assert_eq!(statuses(&c), vec![("a".into(), "fresh".into())]);
    assert!(!c.adopt(vec![row("a", "fresh")], "e2", 3).restart);
    let out = c.apply(vec![upsert(4, "a", "working")]);
    assert!(out.changed);
    assert_eq!(statuses(&c), vec![("a".into(), "working".into())]);
}

#[test]
fn a_gap_forgets_history_before_the_next_snapshot() {
    let mut c = Catalog::default();
    c.adopt(vec![row("a", "idle")], "e", 10);
    c.apply(vec![upsert(11, "a", "stale")]);
    c.forget();
    c.adopt(vec![row("a", "fresh")], "e", 5);
    assert_eq!(statuses(&c), vec![("a".into(), "fresh".into())]);
}

#[test]
fn history_stays_short_once_snapshots_cover_it() {
    let mut c = Catalog::default();
    for round in 0..3u64 {
        let base = round * 1500;
        let out = c.apply((1..=1500).map(|i| upsert(base + i, "a", "working")).collect());
        assert!(!out.reread, "round {round}");
        c.adopt(vec![row("a", "idle")], "e", base + 1500);
    }
}

#[test]
fn once_the_stream_settles_a_ledger_seen_for_the_first_time_counts_as_changed() {
    let mut l = Ledgers::default();
    let e = |key: &str, v: &str| Event { id: 0, kind: "ledger.changed".into(), data: json!({"projectKey": key, "version": v}) };
    assert_eq!(l.changed(&e("project:p-1", "v1"), false), None, "the opening list only records");
    assert_eq!(l.changed(&e("project:p-1", "v1"), true), None);
    assert_eq!(l.changed(&e("project:p-2", "v1"), true).as_deref(), Some("project:p-2"));
}

#[test]
fn a_stream_settles_a_moment_after_it_opens_and_not_once_closed() {
    let s = Stream::default();
    assert!(!s.settled() && s.settles_at().is_none());
    s.open();
    assert!(!s.settled() && s.settles_at().is_some());
    s.close();
    assert!(s.settles_at().is_none());
}

#[test]
fn decisions_are_read_at_once_when_asked_now_and_otherwise_no_sooner_than_ten_seconds_apart() {
    let t0 = std::time::Instant::now();
    let mut p = Pace::default();
    assert_eq!(p.wait(t0), None, "nothing asked, nothing to wait for");
    assert!(p.ask(false, t0), "the first change reads at once");
    p.read(t0);
    assert_eq!(p.wait(t0), None);
    assert!(!p.ask(false, t0 + Duration::from_secs(2)), "a change two seconds later waits");
    assert_eq!(p.wait(t0 + Duration::from_secs(2)), Some(Duration::from_secs(8)));
    assert!(p.ask(true, t0 + Duration::from_secs(3)), "the person's own refresh doesn't wait");
    p.read(t0 + Duration::from_secs(3));
    assert_eq!(p.wait(t0 + Duration::from_secs(3)), None, "no timer of its own after a read, failed or not");
    assert!(!p.ask(false, t0 + Duration::from_secs(4)));
    assert_eq!(p.wait(t0 + Duration::from_secs(4)), Some(Duration::from_secs(9)));
}

#[test]
fn reconnects_back_off_to_twenty_seconds_and_reset_on_an_event() {
    let mut r = Reconnect::default();
    let delays: Vec<u64> = (0..7).map(|_| r.next().as_secs()).collect();
    assert_eq!(delays, [1, 2, 4, 8, 16, 20, 20]);
    r.reset();
    assert_eq!(r.next().as_secs(), 1);
}
