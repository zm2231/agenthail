use feed::derive::Recorded;
use feed::derive::live::Overview;
use rune::Runtime;
use serde_json::json;

use super::{App, DT, Dir, at, isolated, runtime, session};

fn many_projects(dir: &Dir) -> Runtime<App> {
    let names: Vec<String> = (1..=12).map(|i| format!("p{i}")).collect();
    let mut projects: Vec<serde_json::Value> = names.iter().map(|id| json!({"id": id, "displayName": format!("Project {id}"), "lastActive": at(1)})).collect();
    projects.push(json!({"id": "old", "displayName": "Old", "archived": true, "lastActive": at(1)}));
    let mut raw: Recorded = feed::reply::parse(json!({"updatedAt": at(1), "hostEpoch": super::EPOCH, "catalogSeq": 1, "projects": projects})).unwrap();
    raw.sessions = names.iter().enumerate().map(|(i, id)| session(&format!("s-{id}"), i as u64, json!({"project": {"id": id, "name": format!("Project {id}")}}))).collect();
    let mut app = crate::empty(Overview::recorded(raw), false);
    app.sessions = app.ov.d.sessions.clone();
    app.now = app.ov.d.now;
    app.fixtures = Some(crate::Fixtures { dir: dir.path(), streams: std::collections::HashMap::new(), speed: 1.0 });
    app.status = "fixtures".into();
    runtime(app, (1440.0, 900.0))
}

fn press(rt: &mut Runtime<App>, key: &str) {
    let (x, y) = rt.find(key).unwrap_or_else(|| panic!("{key} is in the sidebar"));
    rt.pointer_move(x, y);
    rt.pointer_down(x, y);
    rt.pointer_up(x, y);
    rt.frame(DT);
    rt.settle_all();
    rt.frame(DT);
}

fn opens_beside(rt: &mut Runtime<App>, trigger: &str, menu: &str) {
    press(rt, trigger);
    let (r, opacity) = rt.presented(&format!("{menu}-menu")).unwrap_or_else(|| panic!("{menu} opens"));
    assert!(r[2] > 0.0 && r[3] > 0.0 && opacity > 0.01, "{menu} shows with a size: {r:?} at opacity {opacity}");
    let t = rt.presented(trigger).unwrap().0;
    assert!((r[1] - (t[1] + t[3])).abs() < 40.0 || (r[1] + r[3] - t[1]).abs() < 40.0, "{menu} {r:?} opens at its row {t:?}");
}

#[test]
fn more_and_archived_projects_open_their_menus_at_their_rows() {
    if !isolated("project_menus::more_and_archived_projects_open_their_menus_at_their_rows") {
        return;
    }
    let dir = Dir::new("project-menus");
    let mut rt = many_projects(&dir);
    opens_beside(&mut rt, "more-projects-trigger", "more-projects");
    rt.key(rune::KeyEvent { key: rune::Key::Escape, mods: rune::Mods::default() });
    rt.frame(DT);
    rt.settle_all();
    opens_beside(&mut rt, "archived-projects-trigger", "archived-projects");
}
