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

fn world(dir: &Dir) -> Runtime<App> {
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

fn settle(rt: &mut Runtime<App>) {
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
    s.ov.d.work.values_mut().find(|w| w.id == id).unwrap()
}

fn x<'a>(s: &'a mut App, id: &str) -> &'a mut Session {
    s.ov.d.sessions.iter_mut().find(|x| x.id == id).unwrap()
}

fn shown(rt: &Runtime<App>, key: &str) {
    assert!(rt.presented(key).is_some(), "{key} is on screen");
}

fn now(rt: &mut Runtime<App>, brief: bool) {
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
