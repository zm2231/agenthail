use super::*;
use crate::over::rows::built;

const SIZE: (f32, f32) = (1440.0, 900.0);

fn shipped(id: &str, text: &str, at: &str) -> serde_json::Value {
    json!({"id": id, "text": text, "state": "done", "type": "task", "timeline": [{"kind": "state", "state": "done", "at": at, "actor": "a"}]})
}

fn people() -> Vec<Session> {
    vec![
        session("a", 0, json!({"name": "Alpha one", "status": "busy"})),
        session("b", 1, json!({"name": "Bravo two", "status": "busy", "project": {"id": "p2", "name": "Beta"}})),
        session("c", 2, json!({"name": "Charlie three"})),
        session("old1", 0, json!({"name": "Old one", "lastActive": "2026-11-20T08:00:00Z", "host": {"name": "studio"}})),
        session("old2", 0, json!({"name": "Old two", "lastActive": "2026-11-19T08:00:00Z", "host": {"name": "studio"}})),
        session("m1", 0, json!({"name": "Merged one", "worktree": {"state": "settled", "settledReason": "merged", "branch": "fix/one"}})),
        session("m2", 0, json!({"name": "Merged two", "worktree": {"state": "settled", "settledReason": "merged", "branch": "fix/two"}})),
    ]
}

pub(super) fn world(dir: &Dir) -> Runtime<App> {
    let mut raw: Recorded = serde_json::from_value(json!({
        "updatedAt": "2026-11-27T10:00:00Z",
        "hostEpoch": EPOCH,
        "catalogSeq": 1,
        "projects": [{"id": "p1", "displayName": "Alpha", "lastActive": "2026-11-27T10:00:00Z"}, {"id": "p2", "displayName": "Beta", "lastActive": "2026-11-27T09:00:00Z"}],
    }))
    .unwrap();
    raw.sessions = people();
    raw.ledgers.insert(
        "p1".into(),
        serde_json::from_value(json!([
            shipped("W1", "Wire the export", "2026-11-27T09:50:00Z"),
            shipped("W2", "Check the import", "2026-11-27T09:40:00Z"),
            shipped("W3", "Fix the menu", "2026-11-27T09:30:00Z"),
        ]))
        .unwrap(),
    );
    let mut app = crate::empty(Overview::recorded(raw), false);
    app.sessions = app.ov.d.sessions.clone();
    app.now = app.ov.d.now;
    app.fixtures = Some(Fixtures { dir: dir.path(), streams: HashMap::new(), speed: 1.0 });
    app.status = "fixtures".into();
    runtime(app, SIZE)
}

pub(super) fn settle(rt: &mut Runtime<App>) {
    rt.settle_all();
    rt.frame(DT);
    rt.frame(DT);
    rt.frame(DT);
}

fn rebuilt(rt: &mut Runtime<App>, prefix: &str, change: impl FnOnce(&mut App)) -> Vec<String> {
    settle(rt);
    built::take();
    rt.update(|s, _| change(s));
    rt.frame(DT);
    rt.frame(DT);
    let mut ids: Vec<String> = built::take().into_iter().filter(|id| id.starts_with(prefix)).collect();
    ids.sort();
    ids.dedup();
    ids
}

fn work<'a>(s: &'a mut App, id: &str) -> &'a mut feed::derive::Work {
    s.ov.world_changed();
    feed::shared::own(s.ov.d.work.values_mut().find(|w| w.id == id).unwrap())
}

pub(super) fn x<'a>(s: &'a mut App, id: &str) -> &'a mut Session {
    s.ov.world_changed();
    feed::shared::own(&mut s.ov.d.sessions).iter_mut().find(|x| x.id == id).unwrap()
}

fn shown(rt: &Runtime<App>, key: &str) {
    assert!(rt.presented(key).is_some(), "{key} is on screen");
}

pub(super) fn now(rt: &mut Runtime<App>, brief: bool) {
    rt.update(|s, _| {
        crate::over::go(s, Screen::Now);
        s.ov.now_brief = brief;
        crate::over::layout::set_collapsed(s, [false, false, false]);
        s.ov.hands_on = vec!["a".into(), "c".into()];
    });
    settle(rt);
}

fn board(rt: &mut Runtime<App>) {
    rt.update(|s, _| {
        s.ov.boards.insert("p1".into(), true);
        crate::over::go(s, Screen::Project("p1".into()));
    });
    settle(rt);
}

#[test]
fn a_work_change_rebuilds_only_its_digest_line() {
    if !isolated("rows_memo_lines::a_work_change_rebuilds_only_its_digest_line") {
        return;
    }
    let dir = Dir::new("rows-digest");
    let mut rt = world(&dir);
    now(&mut rt, false);
    let line = |id: &str| format!("d-p1-Shipped-w:p1|{id}");
    shown(&rt, &line("W1"));
    shown(&rt, &line("W2"));
    assert_eq!(rebuilt(&mut rt, "d-", |_| {}), Vec::<String>::new(), "nothing changed");
    assert_eq!(rebuilt(&mut rt, "d-", |s| work(s, "W2").text = "Check the import again".into()), [line("W2")], "its text");
}

#[test]
fn a_work_change_rebuilds_only_its_since_card() {
    if !isolated("rows_memo_lines::a_work_change_rebuilds_only_its_since_card") {
        return;
    }
    let dir = Dir::new("rows-since");
    let mut rt = world(&dir);
    board(&mut rt);
    let card = |id: &str| format!("since-Shipped-w:p1|{id}");
    shown(&rt, &card("W1"));
    assert_eq!(rebuilt(&mut rt, "since-", |_| {}), Vec::<String>::new(), "nothing changed");
    let changed = rebuilt(&mut rt, "since-", |s| work(s, "W1").text = "Wire the export again".into());
    assert_eq!(changed, [card("W1")], "its text");
}

#[test]
fn a_busy_change_rebuilds_only_its_project_chip() {
    if !isolated("rows_memo_lines::a_busy_change_rebuilds_only_its_project_chip") {
        return;
    }
    let dir = Dir::new("rows-chips");
    let mut rt = world(&dir);
    now(&mut rt, false);
    shown(&rt, "chip-p1");
    shown(&rt, "chip-p2");
    assert_eq!(rebuilt(&mut rt, "chip-", |_| {}), Vec::<String>::new(), "nothing changed");
    assert_eq!(rebuilt(&mut rt, "chip-", |s| x(s, "c").status = "busy".into()), ["chip-p1"], "its count");
}

#[test]
fn a_session_change_rebuilds_only_its_strip_tab() {
    if !isolated("rows_memo_lines::a_session_change_rebuilds_only_its_strip_tab") {
        return;
    }
    let dir = Dir::new("rows-tabs");
    let mut rt = world(&dir);
    now(&mut rt, true);
    shown(&rt, "tab-a");
    shown(&rt, "tab-c");
    assert_eq!(rebuilt(&mut rt, "tab-", |_| {}), Vec::<String>::new(), "nothing changed");
    assert_eq!(rebuilt(&mut rt, "tab-", |s| x(s, "c").status = "busy".into()), ["tab-c"], "busy");
    assert_eq!(rebuilt(&mut rt, "tab-", |s| x(s, "a").project.as_mut().unwrap().display_name = "Renamed".into()), ["tab-a"], "its project");
}

#[test]
fn a_session_change_rebuilds_only_its_earlier_row() {
    if !isolated("rows_memo_lines::a_session_change_rebuilds_only_its_earlier_row") {
        return;
    }
    let dir = Dir::new("rows-earlier");
    let mut rt = world(&dir);
    board(&mut rt);
    let (x0, y0) = rt.find("earlier-toggle-p1").expect("the Earlier toggle is on the board");
    rt.pointer_move(x0, y0);
    rt.pointer_down(x0, y0);
    rt.pointer_up(x0, y0);
    settle(&mut rt);
    shown(&rt, "earlier-row-old1");
    shown(&rt, "earlier-row-old2");
    assert_eq!(rebuilt(&mut rt, "earlier-row-", |_| {}), Vec::<String>::new(), "nothing changed");
    assert_eq!(rebuilt(&mut rt, "earlier-row-", |s| x(s, "old2").host.name = "forge".into()), ["earlier-row-old2"], "its host");
}

#[test]
fn a_worktree_change_rebuilds_only_its_merged_row() {
    if !isolated("rows_memo_lines::a_worktree_change_rebuilds_only_its_merged_row") {
        return;
    }
    let dir = Dir::new("rows-merged");
    let mut rt = world(&dir);
    board(&mut rt);
    shown(&rt, "merged-m1");
    shown(&rt, "merged-m2");
    let m2 = "merged-m2".to_string();
    assert_eq!(rebuilt(&mut rt, "merged-", |_| {}), Vec::<String>::new(), "nothing changed");
    assert_eq!(rebuilt(&mut rt, "merged-", |s| x(s, "m2").worktree.as_mut().unwrap().branch = "fix/renamed".into()), [m2.clone()], "its branch");
    assert_eq!(rebuilt(&mut rt, "merged-", |s| x(s, "m2").worktree.as_mut().unwrap().cleanup_reason = "kept".into()), [m2], "what its buttons act on");
}

#[test]
fn digest_lines_follow_the_minute_and_the_last_look_without_a_poll() {
    if !isolated("rows_memo_lines::digest_lines_follow_the_minute_and_the_last_look_without_a_poll") {
        return;
    }
    let dir = Dir::new("rows-digest-minute");
    let mut rt = world(&dir);
    now(&mut rt, false);
    let line = |id: &str| format!("d-p1-Shipped-w:p1|{id}");
    assert_eq!(rt.state.ov.d.ago("2026-11-27T09:50:00Z"), "10m");
    let ticked = rebuilt(&mut rt, "d-", |s| s.outbox.send(Bg::Snapshot("2026-11-27T10:01:00Z".into(), EPOCH.into(), 2, people(), None)).unwrap());
    assert_eq!(rt.state.ov.d.ago("2026-11-27T09:50:00Z"), "11m", "a snapshot in the next minute moves the clock");
    assert_eq!(ticked, [line("W1"), line("W2")], "every shown age moves on");
    rt.update(|s, _| s.ov.looked_now = Some("2026-11-27T09:45:00Z".into()));
    settle(&mut rt);
    shown(&rt, &line("W1"));
    assert!(rt.presented(&line("W2")).is_none(), "a line from before the last look leaves Since you left");
    rt.update(|s, _| s.ov.looked_now = None);
    settle(&mut rt);
    shown(&rt, &line("W2"));
}

#[test]
fn a_review_need_rebuilds_when_its_review_changes_and_acts_on_the_review_it_showed() {
    if !isolated("rows_memo_lines::a_review_need_rebuilds_when_its_review_changes_and_acts_on_the_review_it_showed") {
        return;
    }
    use crate::over::{Group, Need, NeedKind, Target};
    use feed::decisions::{Decision, Inbox};
    let dir = Dir::new("rows-review-need");
    let mut rt = world(&dir);
    let review = |since: &str| Decision { group: "review".into(), key: "review:p1:W1".into(), work_item: "W1".into(), ledger_key: "p1".into(), since: since.into(), ..Default::default() };
    let listed = |s: &mut App, d: Decision| s.ov.inbox.inbox = Some(Inbox { decisions: vec![d], ..Default::default() });
    rt.update(|s, _| {
        let key = |id: &str| s.ov.d.work.values().find(|w| w.id == id).unwrap().key();
        let need = |kind, id: &str, title: &str| Need { kind, group: Group::Waiting, project: "Alpha".into(), project_key: "p1".into(), title: title.into(), who: "a".into(), at: "2026-11-27T09:00:00Z".into(), target: Target::Work(key(id)), label: None };
        s.ov.needs = vec![need(NeedKind::Approval, "W2", "Approve the import"), need(NeedKind::Check, "W1", "Check the export"), need(NeedKind::CiFailing, "W3", "Fix the menu build")];
        listed(s, review("2026-11-27T09:00:00Z"));
    });
    now(&mut rt, false);
    let row = "need-w:p1|W1".to_string();
    shown(&rt, &row);
    shown(&rt, "need-row-review:p1:W1-approve");
    let id = review("").id();
    assert_eq!(rebuilt(&mut rt, "need-", |_| {}), Vec::<String>::new(), "nothing changed");
    assert_eq!(rebuilt(&mut rt, "need-", |s| { s.ov.inbox.acting.insert(id.clone()); }), [row.clone()], "sending");
    assert_eq!(rebuilt(&mut rt, "need-", |s| { s.ov.inbox.acting.clear(); }), [row.clone()], "sent");
    let hold = review("2026-11-27T09:00:00Z").hold_key();
    assert_eq!(rebuilt(&mut rt, "need-", |s| { s.ov.inbox.unconfirmed.insert(hold.clone()); }), [row.clone()], "not confirmed");
    assert_eq!(rebuilt(&mut rt, "need-", |s| { s.ov.inbox.unconfirmed.clear(); }), [row.clone()], "confirmed");
    assert_eq!(rebuilt(&mut rt, "need-", |s| { s.ov.inbox.notes.insert(id.clone(), (hold.clone(), "Try again".into())); }), [row.clone()], "a note on it");
    assert_eq!(rebuilt(&mut rt, "need-", |s| listed(s, review("2026-11-27T09:30:00Z"))), [row.clone()], "a resubmitted review no longer holds the note");
    assert_eq!(rebuilt(&mut rt, "need-", |s| s.ov.inbox.inbox = None), [row.clone()], "no review listed");
    let pr = "need-w:p1|W3".to_string();
    assert_eq!(rebuilt(&mut rt, "need-", |s| work(s, "W3").timeline.push(serde_json::from_value(json!({"kind": "log", "at": "2026-11-27T09:55:00Z", "text": "Opened https://github.com/o/r/pull/7"})).unwrap())), [pr], "the pull request a failing build opens");

    rt.update(|s, _| listed(s, review("2026-11-27T09:40:00Z")));
    settle(&mut rt);
    let (x, y) = rt.find("need-row-review:p1:W1-approve").expect("approve is on screen");
    rt.update(|s, _| listed(s, review("2026-11-27T09:45:00Z")));
    rt.pointer_down(x, y);
    rt.pointer_up(x, y);
    rt.frame(DT);
    let acted = rt.state.ov.inbox.notes.get(&id).map(|(hold, _)| hold.clone());
    assert_eq!(acted, Some(review("2026-11-27T09:40:00Z").hold_key()), "approve acts on the review it showed, not a resubmission listed since");
    settle(&mut rt);
    assert_eq!(rebuilt(&mut rt, "need-", |s| listed(s, review("2026-11-27T09:50:00Z"))), [row.clone()], "a resubmission rebuilds the row its buttons act on");
}

#[test]
fn a_decision_change_rebuilds_only_its_line_on_now() {
    if !isolated("rows_memo_lines::a_decision_change_rebuilds_only_its_line_on_now") {
        return;
    }
    use feed::decisions::{Decision, Inbox, LeadHandled};
    let dir = Dir::new("rows-decisions");
    let mut rt = world(&dir);
    let review = |item: &str| Decision { group: "review".into(), key: format!("review:p1:{item}"), work_item: item.into(), ledger_key: "p1".into(), session_id: "a".into(), project_id: "p1".into(), since: "2026-11-27T09:00:00Z".into(), ..Default::default() };
    let waiting = Decision { group: "waiting".into(), kind: "turn".into(), session_id: "c".into(), project_id: "p1".into(), reason: "Which branch?".into(), since: "2026-11-27T09:10:00Z".into(), ..Default::default() };
    let lead = |item: &str| LeadHandled { session_id: "b".into(), held_by: "a".into(), reason: "Checking it first".into(), work_item: item.into(), ledger_key: "p1".into(), ..Default::default() };
    rt.update(|s, _| s.ov.inbox.inbox = Some(Inbox { decisions: vec![review("W1"), review("W2"), waiting.clone()], lead_handling: vec![lead("W3"), lead("W4")], ..Default::default() }));
    now(&mut rt, false);
    let [w1, w2, wait] = [review("W1").id(), review("W2").id(), waiting.id()].map(|id| format!("decision-{id}"));
    for key in [&w1, &w2, &wait] {
        shown(&rt, key);
    }
    let built = |rt: &mut Runtime<App>, change: &dyn Fn(&mut App)| {
        rebuilt(rt, "", change).into_iter().filter(|id| id.starts_with("decision-") || id.starts_with("lead-handled-")).collect::<Vec<_>>()
    };
    assert_eq!(built(&mut rt, &|_| {}), Vec::<String>::new(), "nothing changed");
    assert_eq!(built(&mut rt, &|s| s.ov.inbox.inbox.as_mut().unwrap().decisions[1].work_title = "Check the import again".into()), [w2.clone()], "its title");
    assert_eq!(built(&mut rt, &|s| s.ov.inbox.inbox.as_mut().unwrap().decisions[2].reason = "Which branch, again?".into()), [wait.clone()], "its reason");
    assert_eq!(built(&mut rt, &|s| { s.ov.inbox.acting.insert(review("W1").id()); }), [w1.clone()], "sending");
    assert_eq!(built(&mut rt, &|s| { let hold = s.ov.inbox.inbox.as_ref().unwrap().decisions[2].hold_key(); s.ov.inbox.unconfirmed.insert(hold); }), [wait.clone()], "not confirmed");
    assert_eq!(built(&mut rt, &|s| { s.ov.inbox.notes.insert(review("W2").id(), (review("W2").hold_key(), "Not sent".into())); }), [w2.clone()], "a note on it");
    let renamed = |s: &mut App| {
        let mut all = people();
        all[2].name = "Charlie renamed".into();
        s.outbox.send(Bg::Snapshot("2026-11-27T10:00:00Z".into(), EPOCH.into(), 2, all, None)).unwrap();
    };
    assert_eq!(built(&mut rt, &renamed), [wait.clone()], "who it waits on");
    assert_eq!(built(&mut rt, &|s| s.ov.d.projects.iter_mut().for_each(|p| p.name = "Renamed".into())), [wait.clone(), w1.clone(), w2.clone()], "its project");
    let w0 = format!("decision-{}", review("W0").id());
    assert_eq!(built(&mut rt, &|s| s.ov.inbox.inbox.as_mut().unwrap().decisions.insert(0, review("W0"))), [w0], "a decision arriving above the others builds only itself");
    let lead3 = "lead-handled-p1|W3|b".to_string();
    assert_eq!(built(&mut rt, &|s| s.ov.inbox.inbox.as_mut().unwrap().lead_handling[0].reason = "Still checking".into()), [lead3], "a lead's reason");
}
