use super::*;
use super::rows_memo_lines::{now, settle, world, x};
use feed::decisions::{Decision, Handled, Inbox, LeadHandled};

fn drawn(rt: &Runtime<App>) -> Vec<String> {
    rt.elements()
        .into_iter()
        .filter(|e| !e.exiting && !e.path.starts_with("kit-root/#0/backdrop"))
        .map(|e| format!("{} {} {:?} {:?} {:?} {:?} {:?} {:?}", e.path, e.role, e.text, e.label, e.rect.map(f32::round), e.fill, e.text_color, e.border_color))
        .collect()
}

fn quiet(rt: &mut Runtime<App>) {
    let mut seen = crate::look::generation();
    let mut still = 0;
    for _ in 0..1000 {
        std::thread::sleep(std::time::Duration::from_millis(20));
        settle(rt);
        let now = crate::look::generation();
        still = if now == seen { still + 1 } else { 0 };
        seen = now;
        if still == 100 {
            return;
        }
    }
    panic!("the look kept changing on its own");
}

fn matches_fresh(rt: &mut Runtime<App>, what: &str) {
    let kept = drawn(rt);
    rt.invalidate_memos_by_prefix("");
    settle(rt);
    let fresh = drawn(rt);
    if let Some(i) = (0..kept.len().max(fresh.len())).find(|&i| kept.get(i) != fresh.get(i)) {
        panic!("{what}: the kept view differs from a fresh build at element {i}:\n kept  {:?}\n fresh {:?}", kept.get(i), fresh.get(i));
    }
}

fn shows(rt: &mut Runtime<App>, what: &str, change: impl FnOnce(&mut App)) {
    settle(rt);
    let before = drawn(rt);
    rt.update(|s, _| change(s));
    settle(rt);
    assert_ne!(drawn(rt), before, "{what} changes what Now shows");
    matches_fresh(rt, what);
}

#[test]
fn every_region_on_now_shows_each_change_as_a_fresh_build_would() {
    if !isolated("now_regions::every_region_on_now_shows_each_change_as_a_fresh_build_would") {
        return;
    }
    let dir = Dir::new("now-regions");
    let mut rt = world(&dir);
    let review = |item: &str| Decision { group: "review".into(), key: format!("review:p1:{item}"), work_item: item.into(), ledger_key: "p1".into(), session_id: "a".into(), project_id: "p1".into(), since: "2026-11-27T09:00:00Z".into(), ..Default::default() };
    let lead = |item: &str| LeadHandled { session_id: "b".into(), held_by: "a".into(), reason: "Checking it first".into(), work_item: item.into(), ledger_key: "p1".into(), ..Default::default() };
    rt.update(|s, _| {
        s.ov.inbox.inbox = Some(Inbox { decisions: vec![review("W1")], lead_handling: vec![lead("W3")], ..Default::default() });
        s.ov.d.usage = vec![serde_json::from_value(json!({"provider": "claude", "label": "5h", "percent": 40.0})).unwrap()];
        s.ov.d.hosts = vec![serde_json::from_value(json!({"id": "h1", "name": "studio", "state": "local"})).unwrap()];
    });
    now(&mut rt, false);
    let need = |s: &App, title: &str| crate::over::Need {
        kind: crate::over::NeedKind::Question,
        group: crate::over::Group::Waiting,
        project: "Alpha".into(),
        project_key: "p1".into(),
        title: title.into(),
        who: "Charlie three".into(),
        at: s.ov.d.sessions[0].last_active.clone(),
        target: crate::over::Target::Session(title.into()),
        label: None,
    };
    rt.update(|s, _| s.ov.needs = vec![need(s, "first"), need(s, "second")]);
    quiet(&mut rt);
    matches_fresh(&mut rt, "the first build");

    shows(&mut rt, "a usage meter's level", |s| s.ov.d.usage[0].percent = Some(85.0));
    shows(&mut rt, "a computer going offline", |s| s.ov.d.hosts[0].state = "unreachable".into());
    shows(&mut rt, "a new daemon version", |s| s.ov.inbox.version = Some("A newer daemon is running".into()));
    shows(&mut rt, "a restart starting", |s| s.link.starting = true);
    shows(&mut rt, "the focused need's title", |s| s.ov.needs[0].title = "first, reworded".into());
    shows(&mut rt, "focus moving to the next need", |s| s.ov.need_ix = 1);
    shows(&mut rt, "a need leaving", |s| {
        s.ov.needs.remove(0);
        s.ov.need_ix = 0;
    });
    shows(&mut rt, "a focused pane", |s| s.ov.now_focus = Some((1, [false, false, false])));
    shows(&mut rt, "the panes restored", |s| s.ov.now_focus = None);
    shows(&mut rt, "a project renamed in the sidebar", |s| s.ov.d.projects[1].name = "Beta renamed".into());
    shows(&mut rt, "activity that couldn't be read", |s| s.earlier.activity_missing = true);
    shows(&mut rt, "a session that stops working", |s| x(s, "a").status = "idle".into());
    shows(&mut rt, "a session hands-on", |s| s.ov.hands_on = vec!["c".into()]);
    shows(&mut rt, "a note from a computer", |s| s.ov.inbox.inbox.as_mut().unwrap().notes = vec!["studio isn't answering, so its decisions aren't shown.".into()]);
    shows(&mut rt, "a second decision in the group", |s| s.ov.inbox.inbox.as_mut().unwrap().decisions.push(review("W2")));
    shows(&mut rt, "a lead's reason", |s| s.ov.inbox.inbox.as_mut().unwrap().lead_handling[0].reason = "Running the tests".into());
    shows(&mut rt, "a decision handled here", |s| s.ov.inbox.history = vec![Handled { title: "Wire the export".into(), session: "a".into(), outcome: "Approved".into(), at: "2026-11-27T09:58:00Z".into() }]);
    shows(&mut rt, "the last look", |s| s.ov.looked_now = Some("2026-11-27T09:45:00Z".into()));
    shows(&mut rt, "a newer last look", |s| s.ov.looked_now = Some("2026-11-27T09:55:00Z".into()));
    shows(&mut rt, "the rail collapsed", |s| crate::over::layout::set_collapsed(s, [false, false, true]));
    shows(&mut rt, "the sidebar collapsed", |s| crate::over::layout::set_collapsed(s, [true, false, true]));
    shows(&mut rt, "the centre collapsed", |s| crate::over::layout::set_collapsed(s, [false, true, false]));
    shows(&mut rt, "every pane back", |s| crate::over::layout::set_collapsed(s, [false, false, false]));
    shows(&mut rt, "the sidebar narrower", |s| s.ov.now_side = rune_kit::shell::PaneState::new(240.0).range(200.0, 480.0));
    shows(&mut rt, "the brief layout", |s| s.ov.now_brief = true);
    shows(&mut rt, "the three panes again", |s| s.ov.now_brief = false);
}

#[test]
fn the_brief_layout_shows_each_change_as_a_fresh_build_would() {
    if !isolated("now_regions::the_brief_layout_shows_each_change_as_a_fresh_build_would") {
        return;
    }
    let dir = Dir::new("now-regions-brief");
    let mut rt = world(&dir);
    now(&mut rt, true);
    quiet(&mut rt);
    matches_fresh(&mut rt, "the first build");
    shows(&mut rt, "a digest line's text", |s| super::rows_memo_lines::work(s, "W2").text = "Check the import twice".into());
    shows(&mut rt, "a catch-up being asked", |s| s.ov.inbox.briefs.entry("p1".into()).or_default().asking = true);
    shows(&mut rt, "a catch-up's note", |s| {
        let b = s.ov.inbox.briefs.entry("p1".into()).or_default();
        b.asking = false;
        b.notice = Some("The lead is writing it".into());
    });
    shows(&mut rt, "a moving session's detail", |s| x(s, "b").display.detail = "Running the import tests".into());
    shows(&mut rt, "a session that stops working", |s| x(s, "b").status = "idle".into());
    shows(&mut rt, "the last look", |s| s.ov.looked_now = Some("2026-11-27T09:45:00Z".into()));
}

#[test]
fn a_row_opens_what_it_is_about_after_that_changes_out_of_sight() {
    if !isolated("now_regions::a_row_opens_what_it_is_about_after_that_changes_out_of_sight") {
        return;
    }
    let dir = Dir::new("now-regions-press");
    let mut rt = world(&dir);
    let handled = |session: &str| Handled { title: "Wire the export".into(), session: session.into(), outcome: "Approved".into(), at: "2026-11-27T09:58:00Z".into() };
    rt.update(|s, _| {
        s.ov.inbox.inbox = Some(Inbox::default());
        s.ov.inbox.history = vec![handled("a")];
    });
    now(&mut rt, false);
    rt.update(|s, _| s.ov.inbox.history = vec![handled("c")]);
    settle(&mut rt);
    let (x, y) = rt.find("handled-0").expect("the handled row is on screen");
    rt.pointer_down(x, y);
    rt.pointer_up(x, y);
    rt.frame(DT);
    assert_eq!(rt.state.selected.as_deref(), Some("c"), "the row opens the session it lists now, though it reads the same");
}
