use std::sync::Arc;

use serde_json::json;

use super::digest_held::Held;
use super::world::{Entry, Work};
use super::*;

fn world() -> World {
    let session = |id: &str, headline: &str| {
        json!({"id": id, "name": id, "lastActive": "2026-11-24T11:50:00Z", "project": {"id": "p", "name": "P"}, "display": {"kind": "idle"},
            "endTurn": {"headline": headline, "outcome": "done", "at": "2026-11-24T11:30:00Z"}})
    };
    let v = json!({
        "updatedAt": "2026-11-24T12:00:00Z",
        "projects": [{"id": "p", "displayName": "P"}],
        "actors": {"worker": {"name": "Builder"}},
        "sessions": [session("s1", "Landed the fix"), session("s2", "Wrote the tests")],
        "ledgers": {"p": [
            {"id": "p-A", "text": "Ship the parser", "state": "done", "timeline": [{"at": "2026-11-24T09:00:00Z", "kind": "state", "state": "done"}]},
            {"id": "p-B", "text": "Fix the cache", "state": "in_progress", "timeline": [
                {"at": "2026-11-24T02:00:00Z", "kind": "state", "state": "in_progress", "actor": "worker"},
                {"at": "2026-11-24T10:00:00Z", "kind": "log", "text": "The CI run failed on the cache test."}
            ]},
            {"id": "p-C", "text": "Review the docs", "state": "in_review", "timeline": [{"at": "2026-11-23T20:00:00Z", "kind": "state", "state": "in_review"}]},
            {"id": "p-D", "text": "Review the menu", "state": "in_review", "timeline": [{"at": "2026-11-24T10:00:00Z", "kind": "state", "state": "in_review"}]}
        ]}
    });
    World::from_json(&v.to_string()).unwrap()
}

fn ledger(rows: serde_json::Value) -> World {
    let s = json!({"id": "s1", "name": "s1", "lastActive": "2026-11-20T11:50:00Z", "project": {"id": "p", "name": "P"}, "display": {"kind": "idle"}});
    let v = json!({"updatedAt": "2026-11-24T12:00:00Z", "projects": [{"id": "p", "displayName": "P"}], "sessions": [s], "ledgers": {"p": rows}});
    World::from_json(&v.to_string()).unwrap()
}

fn shown(w: &World, held: &mut Held, window: f64) -> Vec<String> {
    w.digests_held(held, window).iter().map(|(p, lines)| format!("{} {lines:?}", p.key)).collect()
}

fn fresh(w: &World, window: f64) -> Vec<String> {
    w.digests_held(&mut Held::default(), window).iter().map(|(p, lines)| format!("{} {lines:?}", p.key)).collect()
}

fn work<'a>(w: &'a mut World, id: &str) -> &'a mut Work {
    Arc::make_mut(w.work.values_mut().find(|x| x.id == id).unwrap())
}

fn later(w: &mut World, hours: i64) {
    w.now += time::Duration::hours(hours);
}

#[test]
fn held_digests_reuse_unchanged_lines_and_follow_every_input_they_show() {
    let mut w = world();
    let mut held = Held::default();
    let first = w.digests_held(&mut held, 12.0)[0].1.clone();
    let texts: Vec<&str> = first.iter().map(|l| l.text.as_str()).collect();
    assert!(texts.contains(&"Ship the parser") && texts.contains(&"Fix the cache") && texts.contains(&"Review the docs") && texts.contains(&"Landed the fix"), "{texts:?}");
    assert!(first.iter().any(|l| l.detail == "Started by Builder"), "{first:?}");
    assert!(Arc::ptr_eq(&first, &w.digests_held(&mut held, 12.0)[0].1), "an unchanged World reuses its lines");

    let mut check = |w: &mut World, change: &dyn Fn(&mut World), (from, to): (f64, f64), what: &str| {
        let before = shown(w, &mut held, from);
        change(w);
        let after = shown(w, &mut held, to);
        assert_eq!(after, fresh(w, to), "{what}");
        assert_ne!(after, before, "{what} shows");
    };
    check(&mut w, &|w| work(w, "p-A").text = "Ship the new parser".into(), (12.0, 12.0), "a work item's text");
    check(&mut w, &|w| work(w, "p-A").timeline.push(Entry { at: "2026-11-24T11:00:00Z".into(), kind: "state".into(), state: Some("in_progress".into()), actor: "worker".into(), ..Default::default() }), (12.0, 12.0), "a new step on its timeline");
    check(&mut w, &|w| work(w, "p-B").timeline[1].text = Some("Rebased onto main.".into()), (12.0, 12.0), "a log no longer reporting a failed CI run");
    check(&mut w, &|w| work(w, "p-D").state = "done".into(), (12.0, 12.0), "its state");
    check(&mut w, &|w| Arc::make_mut(&mut w.sessions)[0].end_turn.as_mut().unwrap().headline = "Landed it again".into(), (12.0, 12.0), "a session's last word");
    check(&mut w, &|w| { w.names.insert("s2".into(), "Test writer".into()); }, (12.0, 12.0), "a session's name");
    check(&mut w, &|w| { w.names.insert("worker".into(), "Another builder".into()); }, (12.0, 12.0), "who started an item");
    check(&mut w, &|w| w.projects[0].sessions.retain(|id| id != "s1"), (12.0, 12.0), "a session leaving the project");
    check(&mut w, &|w| later(w, 4), (12.0, 12.0), "time passing a line's window");
    check(&mut w, &|_| {}, (12.0, 48.0), "a wider window");
    check(&mut w, &|w| later(w, 30), (48.0, 48.0), "a review ageing out");
}

#[test]
fn a_log_works_out_whether_it_reports_a_failed_ci_run_from_its_own_text() {
    let e = Entry { kind: "log".into(), text: Some("The CI run failed.".into()), ..Default::default() };
    assert!(e.reports_ci_failure());
    let mut copy = e.clone();
    copy.text = Some("All green.".into());
    assert!(!copy.reports_ci_failure(), "a copy works it out again from its own text");
    assert_eq!(e.clone(), e, "the worked-out answer doesn't make equal entries differ");
    assert!(!Entry { kind: "state".into(), text: Some("The CI run failed.".into()), ..Default::default() }.reports_ci_failure());
}

#[test]
fn a_kept_duplicate_leaving_the_window_brings_back_the_one_it_hid() {
    let done = |id: &str, at: &str| json!({"id": id, "text": "Ship the parser", "state": "done", "timeline": [{"at": at, "kind": "state", "state": "done"}]});
    let mut w = ledger(json!([done("p-A", "2026-11-24T00:30:00Z"), done("p-B", "2026-11-24T00:30:00.5Z")]));
    let mut held = Held::default();
    let first = shown(&w, &mut held, 12.0);
    assert!(first[0].contains("p-A") && !first[0].contains("p-B"), "the same text shows once: {first:?}");
    w.now += time::Duration::milliseconds(30 * 60_000 + 200);
    let after = shown(&w, &mut held, 12.0);
    assert_eq!(after, fresh(&w, 12.0));
    assert!(after[0].contains("p-B"), "the newer one shows once the other leaves: {after:?}");
}

#[test]
fn a_clock_moving_back_works_the_lines_out_again() {
    let mut w = ledger(json!([{"id": "p-C", "text": "Review the docs", "state": "in_review", "timeline": [{"at": "2026-11-22T11:30:00Z", "kind": "state", "state": "in_review"}]}]));
    let mut held = Held::default();
    assert_eq!(shown(&w, &mut held, 72.0), Vec::<String>::new(), "a review 48.5 hours old has left");
    w.now -= time::Duration::hours(1);
    let back = shown(&w, &mut held, 12.0);
    assert_eq!(back, fresh(&w, 12.0));
    assert_eq!(back.len(), 1, "an hour earlier it is 47.5 hours old: {back:?}");
}
