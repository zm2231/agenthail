use super::*;
use crate::over::rows::built;
use crate::over::{Group, Need, NeedKind, Target};

const SIZE: (f32, f32) = (1440.0, 900.0);

const NAMES: [(&str, &str); 3] = [("a", "Alpha one"), ("b", "Bravo two"), ("c", "Charlie three")];

fn three() -> Vec<Session> {
    with("", json!({}))
}

fn with(id: &str, extra: serde_json::Value) -> Vec<Session> {
    NAMES
        .iter()
        .enumerate()
        .map(|(i, (sid, name))| {
            let mut row = json!({"name": name});
            if *sid == id {
                row.as_object_mut().unwrap().extend(extra.as_object().unwrap().clone());
            }
            session(sid, i as u64, row)
        })
        .collect()
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

fn snapshot_rebuilt(rt: &mut Runtime<App>, seq: u64, prefix: &str, sessions: Vec<Session>) -> Vec<String> {
    settle(rt);
    built::take();
    send(rt, snapshot(seq, sessions, None));
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

#[test]
fn a_session_change_rebuilds_only_its_sidebar_row() {
    if !isolated("rows_memo::a_session_change_rebuilds_only_its_sidebar_row") {
        return;
    }
    let dir = Dir::new("rows-sidebar");
    let mut rt = runtime(app(three(), &dir), SIZE);
    rt.update(|s, _| crate::over::go(s, Screen::Project("p1".into())));
    settle(&mut rt);
    for id in ["s-a", "s-b", "s-c"] {
        shown(&rt, id);
    }
    assert_eq!(snapshot_rebuilt(&mut rt, 2, "s-", three()), Vec::<String>::new(), "an unchanged snapshot rebuilds no row");
    assert_eq!(snapshot_rebuilt(&mut rt, 3, "s-", with("b", json!({"name": "Bravo renamed"}))), ["s-b"], "a new title");
    assert_eq!(snapshot_rebuilt(&mut rt, 4, "s-", with("b", json!({"name": "Bravo renamed", "status": "busy"}))), ["s-b"], "busy");
    assert_eq!(snapshot_rebuilt(&mut rt, 5, "s-", with("b", json!({"name": "Bravo renamed", "status": "busy", "display": {"label": "Waiting on you"}}))), ["s-b"], "waiting");
    assert_eq!(rebuilt(&mut rt, "s-", |s| crate::over::open_session(s, "c")), ["s-c"], "selection draws on the opened row only");
    assert_eq!(rebuilt(&mut rt, "s-", |_| crate::look::hold_text_scale(Some(1.2))), ["s-a", "s-b", "s-c"], "a new text size redraws every row");
}

#[test]
fn a_project_change_rebuilds_only_its_sidebar_project_row() {
    if !isolated("rows_memo::a_project_change_rebuilds_only_its_sidebar_project_row") {
        return;
    }
    let dir = Dir::new("rows-projects");
    let mut rt = runtime(app(three(), &dir), SIZE);
    rt.update(|s, _| crate::over::go(s, Screen::Sessions));
    settle(&mut rt);
    shown(&rt, "p-p1");
    assert_eq!(rebuilt(&mut rt, "p-", |_| {}), Vec::<String>::new(), "nothing changed");
    let need = |s: &App| Need { kind: NeedKind::Question, group: Group::Waiting, project: "Alpha".into(), project_key: "p1".into(), title: "Which way?".into(), who: "a".into(), at: s.ov.d.sessions[0].last_active.clone(), target: Target::Session("a".into()), label: None };
    assert_eq!(rebuilt(&mut rt, "p-", |s| s.ov.needs = vec![need(s)]), ["p-p1"], "its waiting count");
    assert_eq!(rebuilt(&mut rt, "p-", |s| s.ov.d.projects[0].name = "Renamed".into()), ["p-p1"], "its name");
    assert_eq!(rebuilt(&mut rt, "p-", |s| s.ov.d.projects[0].last_active = "2026-01-01T00:00:00Z".into()), ["p-p1"], "its age");
}

#[test]
fn a_session_change_rebuilds_only_its_row_in_the_sessions_list() {
    if !isolated("rows_memo::a_session_change_rebuilds_only_its_row_in_the_sessions_list") {
        return;
    }
    let dir = Dir::new("rows-list");
    let mut rt = runtime(app(three(), &dir), SIZE);
    rt.update(|s, _| {
        crate::over::go(s, Screen::Sessions);
        let view = serde_json::from_value(json!({
            "projects": [{"id": "p1", "name": "Alpha", "counts": {"families": 3, "sessions": 3}, "collapsedFamilyIds": ["a", "b", "c"]}],
            "families": [{"id": "a", "rootId": "a", "projectId": "p1", "members": [{"id": "a"}]}, {"id": "b", "rootId": "b", "projectId": "p1", "members": [{"id": "b"}]}, {"id": "c", "rootId": "c", "projectId": "p1", "members": [{"id": "c"}]}],
        }))
        .unwrap();
        let page = feed::listing::Page { sessions: s.ov.d.sessions.clone(), view, next: None };
        crate::over::listing::show(s, feed::listing::Listing::first(page));
    });
    settle(&mut rt);
    for id in ["srow-a", "srow-b", "srow-c"] {
        shown(&rt, id);
    }
    assert_eq!(rebuilt(&mut rt, "srow-", |_| {}), Vec::<String>::new(), "nothing changed");
    assert_eq!(rebuilt(&mut rt, "srow-", |s| x(s, "b").status = "busy".into()), ["srow-b"], "live");
    assert_eq!(rebuilt(&mut rt, "srow-", |s| x(s, "b").host.name = "elsewhere".into()), ["srow-b"], "its host");
    assert_eq!(rebuilt(&mut rt, "srow-", |s| x(s, "b").checkout.branch = Some("fix/rows".into())), ["srow-b"], "its branch");
    assert_eq!(rebuilt(&mut rt, "srow-", |s| x(s, "c").last_active = "2026-01-01T00:00:00Z".into()), ["srow-c"], "its idle age");
    let titled = snapshot_rebuilt(&mut rt, 2, "srow-", with("a", json!({"name": "Alpha renamed"})));
    assert!(titled.contains(&"srow-a".to_string()), "a new title: {titled:?}");
}

fn now_with(rt: &mut Runtime<App>) {
    rt.update(|s, _| {
        crate::over::go(s, Screen::Now);
        s.ov.now_brief = false;
        crate::over::layout::set_collapsed(s, [false, false, false]);
        for id in ["c", "d"] {
            if let Some(x) = s.ov.d.sessions.iter_mut().find(|x| x.id == id) {
                x.status = "busy".into();
            }
        }
        s.ov.hands_on = vec!["a".into(), "b".into()];
        let need = |id: &str, title: &str| Need { kind: NeedKind::Question, group: Group::Waiting, project: "Alpha".into(), project_key: "p1".into(), title: title.into(), who: id.into(), at: "2026-11-27T08:50:00Z".into(), target: Target::Session(id.into()), label: None };
        s.ov.needs = vec![need("a", "First question"), need("b", "Second question"), need("c", "Third question")];
    });
    settle(rt);
}

#[test]
fn a_change_rebuilds_only_its_row_on_now() {
    if !isolated("rows_memo::a_change_rebuilds_only_its_row_on_now") {
        return;
    }
    let dir = Dir::new("rows-now");
    let mut all = three();
    all.push(session("d", 3, json!({"name": "Delta four"})));
    let mut rt = runtime(app(all, &dir), SIZE);
    now_with(&mut rt);
    for id in ["hands-a", "hands-b", "moving-c", "moving-d", "need-s:a", "need-s:b", "need-s:c"] {
        shown(&rt, id);
    }
    assert_eq!(rebuilt(&mut rt, "", |_| {}), Vec::<String>::new(), "nothing changed");

    assert_eq!(rebuilt(&mut rt, "hands-", |s| x(s, "b").status = "busy".into()), ["hands-b"], "a hands-on session starts working");
    assert_eq!(rebuilt(&mut rt, "hands-", |s| x(s, "b").last_active = "2026-01-01T00:00:00Z".into()), Vec::<String>::new(), "a working card shows no age");
    assert_eq!(rebuilt(&mut rt, "hands-", |s| x(s, "a").last_active = "2026-01-01T00:00:00Z".into()), ["hands-a"], "its idle age");
    assert_eq!(rebuilt(&mut rt, "hands-", |s| x(s, "a").project.as_mut().unwrap().display_name = "Renamed".into()), ["hands-a"], "its project");
    assert_eq!(rebuilt(&mut rt, "hands-", |s| s.ov.hands_on.swap(0, 1)), ["hands-a", "hands-b"], "a reorder renumbers both shortcuts");

    let moving = rebuilt(&mut rt, "moving-", |s| x(s, "d").display.detail = "Running the tests".into());
    assert_eq!(moving.len(), 1, "one moving row rebuilds: {moving:?}");
    let moving = rebuilt(&mut rt, "moving-", |s| x(s, "d").project.as_mut().unwrap().display_name = "Other".into());
    assert_eq!(moving.len(), 1, "its project: {moving:?}");

    assert_eq!(rebuilt(&mut rt, "need-", |s| s.ov.needs[1].title = "Second, reworded".into()), ["need-s:b"], "a need's title");
    assert_eq!(rebuilt(&mut rt, "need-", |s| s.ov.needs[2].who = "someone else".into()), ["need-s:c"], "who asked");
    assert_eq!(rebuilt(&mut rt, "need-", |s| s.ov.needs[0].label = Some("W7".into())), ["need-s:a"], "its label");
    assert_eq!(rebuilt(&mut rt, "need-", |s| s.ov.needs[0].at = "2026-01-01T00:00:00Z".into()), ["need-s:a"], "its age");
    assert_eq!(rebuilt(&mut rt, "need-", |s| s.ov.needs[0].kind = NeedKind::Approval), ["need-s:a"], "its kind");

    assert_eq!(rebuilt(&mut rt, "need-", |s| s.ov.need_ix = 1), ["need-s:a", "need-s:b"], "focus moves between two rows");

    assert_eq!(rebuilt(&mut rt, "moving-", |s| x(s, "c").status = "idle".into()), Vec::<String>::new(), "a session that stops moving leaves the rows below it");

    let any = rebuilt(&mut rt, "", |s| s.ov.needs[2].title = "Third, reworded".into());
    assert!(any.iter().all(|id| !id.starts_with("hands-") && !id.starts_with("moving-")), "a need's change leaves the rail: {any:?}");
}

#[test]
fn a_session_change_rebuilds_only_its_jump_row() {
    if !isolated("rows_memo::a_session_change_rebuilds_only_its_jump_row") {
        return;
    }
    let dir = Dir::new("rows-jump");
    let mut rt = runtime(app(three(), &dir), SIZE);
    rt.update(|s, _| s.ov.jump = Some(rune::TextEdit::new("")));
    settle(&mut rt);
    let at = |_: &Runtime<App>, id: &str| match id.strip_prefix("project ") {
        Some(p) => format!("hit-p:{p}"),
        None => format!("hit-s:{id}"),
    };
    let b = at(&rt, "b");
    shown(&rt, &b);
    assert_eq!(rebuilt(&mut rt, "hit-", |_| {}), Vec::<String>::new(), "nothing changed");
    let busy = rebuilt(&mut rt, "hit-", |s| x(s, "b").status = "busy".into());
    assert!(busy.contains(&b), "its state: {busy:?}");
    assert!(!busy.contains(&at(&rt, "a")) && !busy.contains(&at(&rt, "c")), "other sessions keep their rows: {busy:?}");
    let mut expected = vec![b.clone(), at(&rt, "project p1")];
    expected.sort();
    assert_eq!(busy, expected, "and the project's moving count");
    let moved = rebuilt(&mut rt, "hit-", |s| x(s, "b").project.as_mut().unwrap().display_name = "Moved".into());
    assert!(moved.contains(&b), "its project: {moved:?}");
    let titled = snapshot_rebuilt(&mut rt, 2, "hit-", with("b", json!({"name": "Bravo renamed", "status": "busy"})));
    assert!(titled.contains(&at(&rt, "b")), "a new title: {titled:?}");
}

fn board(dir: &Dir) -> Runtime<App> {
    let mut raw = recorded(1, three());
    raw.ledgers.insert(
        "p1".into(),
        serde_json::from_value(json!([
            {"id": "W1", "text": "Wire the export", "state": "in_progress", "phase": "build", "priority": "P2", "type": "task"},
            {"id": "W2", "text": "Check the import", "state": "in_progress", "phase": "build", "priority": "P1", "type": "task"},
            {"id": "W3", "text": "Fix the menu", "state": "open", "priority": "P3", "type": "bug"},
        ]))
        .unwrap(),
    );
    let mut app = crate::empty(Overview::recorded(raw), false);
    app.sessions = app.ov.d.sessions.clone();
    app.now = app.ov.d.now;
    app.fixtures = Some(Fixtures { dir: dir.path(), streams: HashMap::new(), speed: 1.0 });
    app.status = "fixtures".into();
    let mut rt = runtime(app, SIZE);
    rt.update(|s, _| {
        s.ov.boards.insert("p1".into(), true);
        crate::over::go(s, Screen::Project("p1".into()));
    });
    settle(&mut rt);
    rt
}

#[test]
fn a_work_change_rebuilds_only_its_board_card() {
    if !isolated("rows_memo::a_work_change_rebuilds_only_its_board_card") {
        return;
    }
    let dir = Dir::new("rows-board");
    let mut rt = board(&dir);
    let card = |rt: &Runtime<App>, id: &str| {
        let w = rt.state.ov.d.work.values().find(|w| w.id == id).unwrap();
        format!("w-{}-{}", w.ledger_key(), w.id)
    };
    for id in ["W1", "W2", "W3"] {
        shown(&rt, &card(&rt, id));
    }
    assert_eq!(rebuilt(&mut rt, "w-", |_| {}), Vec::<String>::new(), "nothing changed");
    assert_eq!(rebuilt(&mut rt, "w-", |s| work(s, "W2").text = "Check the import again".into()), [card(&rt, "W2")], "its text");
    assert_eq!(rebuilt(&mut rt, "w-", |s| work(s, "W1").phase = "review".into()), [card(&rt, "W1")], "its step");
    let lead = rebuilt(&mut rt, "w-", |s| {
        let id = s.ov.d.work.values().find(|w| w.id == "W1").unwrap().id.clone();
        let wide = s.ov.widths.get(&format!("card-{id}-false")).copied().unwrap();
        s.ov.widths.insert(format!("card-{id}-true"), wide);
        s.ov.boards.insert("p1".into(), false);
    });
    assert!(lead.contains(&card(&rt, "W1")), "the lead view's compact cards build their own: {lead:?}");
}

fn asking(id: &str, minute: u64) -> Session {
    session(id, 0, json!({"name": format!("Asker {id}"), "endTurn": {"headline": format!("Which way for {id}?"), "outcome": "needs_you", "at": format!("2026-11-27T08:{minute:02}:00Z")}}))
}

#[test]
fn a_need_arriving_above_the_focused_one_builds_only_its_row() {
    if !isolated("rows_memo::a_need_arriving_above_the_focused_one_builds_only_its_row") {
        return;
    }
    let dir = Dir::new("rows-arrive");
    let three = || vec![asking("a", 20), asking("b", 30), asking("c", 40)];
    let mut rt = runtime(app(three(), &dir), SIZE);
    rt.update(|s, _| {
        crate::over::go(s, Screen::Now);
        s.ov.now_brief = false;
        crate::over::layout::set_collapsed(s, [false, false, false]);
        s.ov.need_ix = 1;
    });
    settle(&mut rt);
    let focused = |rt: &Runtime<App>| rt.state.ov.needs.get(rt.state.ov.need_ix).map(|n| n.target.clone());
    assert_eq!(rt.state.ov.needs.iter().map(|n| n.target.clone()).collect::<Vec<_>>(), ["a", "b", "c"].map(|x| Target::Session(x.into())));
    assert_eq!(focused(&rt), Some(Target::Session("b".into())));
    for id in ["need-s:a", "need-s:b", "need-s:c"] {
        shown(&rt, id);
    }
    let mut more = three();
    more.push(asking("d", 10));
    assert_eq!(snapshot_rebuilt(&mut rt, 2, "need-", more), ["need-s:d"], "an older need sorts to the top and builds only its row");
    assert_eq!(rt.state.ov.needs[0].target, Target::Session("d".into()));
    assert_eq!(focused(&rt), Some(Target::Session("b".into())), "focus stays on the same need");
    assert_eq!(snapshot_rebuilt(&mut rt, 3, "need-", three()), Vec::<String>::new(), "it leaving builds nothing");
    assert_eq!(focused(&rt), Some(Target::Session("b".into())), "focus still on the same need");
}
