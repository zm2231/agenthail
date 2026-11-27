use super::*;

const SIZE: (f32, f32) = (1440.0, 900.0);

fn holding(dir: &Dir, item: serde_json::Value) -> Runtime<App> {
    let id = item["id"].as_str().unwrap().to_string();
    std::fs::write(dir.0.join(format!("items/{id}.json")), item.to_string()).unwrap();
    let held = json!({"heldWork": [{"itemId": id, "projectKey": "p1", "state": item["state"]}]});
    let mut rt = runtime(app(vec![session("a", 0, held)], dir), SIZE);
    rt.update(|s, _| {
        crate::over::open_session(s, "a");
        crate::load_pane(s, "a");
    });
    rt.settle_all();
    rt.frame(DT);
    rt.frame(DT);
    rt
}

fn render(rt: &mut Runtime<App>, path: &std::path::Path) {
    let gpu = rune::render::Gpu::headless();
    let mut renderer = rune::render::Renderer::new(&gpu.device, rune::render::OFFSCREEN_FORMAT);
    let target = rune::render::Offscreen::new(&gpu.device, SIZE.0 as u32, SIZE.1 as u32);
    rt.frame(DT);
    renderer.render_runtime_to_offscreen(&gpu.device, &gpu.queue, &target, rt);
    rune::render::write_png(path, SIZE.0 as u32, SIZE.1 as u32, &target.read(&gpu.device)).unwrap();
}

#[test]
fn shot() {
    let Some(out) = std::env::var_os("AGENTHAIL_SHOT_DIR") else { return };
    if !isolated("work_steps::shot") {
        return;
    }
    let out = std::path::PathBuf::from(out);
    let dir = Dir::new("work-steps");
    for (name, item) in [
        ("verify", json!({"id": "W1", "text": "Check the new sign-in flow", "state": "in_progress", "phase": "verify", "priority": "P2", "type": "task"})),
        ("review", json!({"id": "W2", "text": "Wire the pull request watcher", "state": "in_progress", "phase": "review", "priority": "P1", "type": "task"})),
        ("in-review-no-phase", json!({"id": "W3", "text": "Older item without a phase", "state": "in_review", "priority": "P3", "type": "bug"})),
    ] {
        let mut rt = holding(&dir, item);
        render(&mut rt, &out.join(format!("{name}-1.png")));
    }
}

#[test]
fn a_ledger_change_rereads_the_shown_work_item_so_its_step_follows_the_ledger() {
    if !isolated("work_steps::a_ledger_change_rereads_the_shown_work_item_so_its_step_follows_the_ledger") {
        return;
    }
    let dir = Dir::new("work-steps-change");
    let mut rt = holding(&dir, json!({"id": "W1", "text": "Ship it", "state": "in_progress", "phase": "build"}));
    let phase = |rt: &Runtime<App>| rt.state.items.get(&zk("p1", "W1")).map(|i| i.phase.clone());
    assert_eq!(phase(&rt).as_deref(), Some("build"));
    std::fs::write(dir.0.join("items/W1.json"), r#"{"id":"W1","text":"Ship it","state":"in_progress","phase":"verify"}"#).unwrap();
    send(&rt, Bg::LedgerChanged("elsewhere".into(), std::time::Instant::now()));
    rt.frame(DT);
    assert_eq!(phase(&rt).as_deref(), Some("build"), "another ledger's change leaves it");
    send(&rt, Bg::LedgerChanged("p1".into(), std::time::Instant::now()));
    rt.frame(DT);
    assert_eq!(phase(&rt).as_deref(), Some("verify"), "its own ledger's change rereads it");
    std::fs::write(dir.0.join("items/W1.json"), r#"{"id":"W1","text":"Ship it","state":"in_review","phase":"check"}"#).unwrap();
    send(&rt, Bg::LedgersReopened);
    rt.frame(DT);
    assert_eq!(phase(&rt).as_deref(), Some("check"), "a reopened ledger stream rereads it");
}

#[test]
fn an_older_read_of_a_work_item_does_not_replace_a_newer_one() {
    if !isolated("work_steps::an_older_read_of_a_work_item_does_not_replace_a_newer_one") {
        return;
    }
    let dir = Dir::new("work-steps-order");
    let mut rt = holding(&dir, json!({"id": "W1", "text": "Ship it", "state": "in_progress", "phase": "build"}));
    let key = zk("p1", "W1");
    let item = |phase: &str| serde_json::from_value::<feed::catalog::WorkItem>(json!({"id": "W1", "state": "in_progress", "phase": phase})).unwrap();
    rt.state.item_reads.1.insert(key.clone(), 2);
    send(&rt, Bg::Item(key.clone(), 1, Some(item("review"))));
    rt.frame(DT);
    assert_eq!(rt.state.items[&key].phase, "build", "a reply to a read that was asked again is dropped");
    send(&rt, Bg::Item(key.clone(), 2, Some(item("verify"))));
    rt.frame(DT);
    assert_eq!(rt.state.items[&key].phase, "verify", "the latest read lands");
    send(&rt, Bg::Item(key.clone(), 2, Some(item("review"))));
    rt.frame(DT);
    assert_eq!(rt.state.items[&key].phase, "verify", "a read lands once");
    rt.state.item_reads.1.insert(key.clone(), 3);
    send(&rt, Bg::Item(key.clone(), 3, None));
    rt.frame(DT);
    assert!(!rt.state.items.contains_key(&key), "a failed reread drops the old phase rather than keep showing it");
}

#[test]
fn a_read_of_an_item_dropped_by_its_ledger_changing_does_not_come_back() {
    if !isolated("work_steps::a_read_of_an_item_dropped_by_its_ledger_changing_does_not_come_back") {
        return;
    }
    let dir = Dir::new("work-steps-dropped");
    let mut rt = holding(&dir, json!({"id": "W1", "text": "Ship it", "state": "in_progress", "phase": "build"}));
    let hidden = zk("p1", "W9");
    let item = serde_json::from_value::<feed::catalog::WorkItem>(json!({"id": "W9", "state": "in_progress", "phase": "review"})).unwrap();
    rt.state.items.insert(hidden.clone(), item.clone());
    rt.state.item_reads.1.insert(hidden.clone(), 7);
    send(&rt, Bg::LedgerChanged("p1".into(), std::time::Instant::now()));
    rt.frame(DT);
    assert!(!rt.state.items.contains_key(&hidden), "a hidden item of the changed ledger is dropped");
    send(&rt, Bg::Item(hidden.clone(), 7, Some(item)));
    rt.frame(DT);
    assert!(!rt.state.items.contains_key(&hidden), "its read from before the change doesn't put it back");
}

fn renamed(dir: &Dir, item_labels: Option<serde_json::Value>) -> Runtime<App> {
    let labels = json!({"scope": "Scope", "build": "Make", "review": "Review", "verify": "Verify", "check": "Sign-off", "ship": "Ship"});
    let w1 = json!({"id": "W1", "text": "Wire the export", "state": "in_progress", "phase": "build", "priority": "P2", "type": "task"});
    let w2 = json!({"id": "W2", "text": "Check the import", "state": "in_review", "phase": "check", "priority": "P1", "type": "task"});
    if let Some(own) = item_labels {
        let mut item = w1.clone();
        item["phaseLabels"] = own;
        std::fs::write(dir.0.join("items/W1.json"), item.to_string()).unwrap();
    }
    let held = json!({"heldWork": [{"itemId": "W1", "projectKey": "p1", "state": "in_progress"}]});
    let mut raw = recorded(1, vec![session("a", 0, held)]);
    raw.ledgers.insert("p1".into(), serde_json::from_value(json!([w1, w2])).unwrap());
    raw.steps.insert("p1".into(), serde_json::from_value(labels).unwrap());
    let mut app = crate::empty(Overview::recorded(raw), false);
    app.sessions = app.ov.d.sessions.clone();
    app.now = app.ov.d.now;
    app.fixtures = Some(Fixtures { dir: dir.path(), streams: HashMap::new(), speed: 1.0 });
    app.status = "fixtures".into();
    let mut rt = runtime(app, SIZE);
    rt.update(|s, _| {
        crate::over::open_session(s, "a");
        crate::load_pane(s, "a");
    });
    rt.settle_all();
    rt.frame(DT);
    rt.frame(DT);
    rt
}

fn shows(rt: &Runtime<App>, said: &str) -> bool {
    rt.elements().iter().any(|e| !e.exiting && e.text.as_deref() == Some(said))
}

fn on(rt: &mut Runtime<App>, target: crate::over::Target) {
    rt.update(move |s, _| crate::over::open_target(s, &target));
    rt.settle_all();
    rt.frame(DT);
    rt.frame(DT);
}

#[test]
fn a_project_that_renames_two_steps_shows_its_names_on_every_step_bar() {
    if !isolated("work_steps::a_project_that_renames_two_steps_shows_its_names_on_every_step_bar") {
        return;
    }
    let dir = Dir::new("work-steps-renamed");
    let mut rt = renamed(&dir, Some(json!({"build": "Make", "check": "Sign-off"})));
    assert_eq!(rt.state.items[&zk("p1", "W1")].steps.name(1), "Make");
    assert!(shows(&rt, "Make") && shows(&rt, "Sign-off"), "the session's step bar names the project's steps");
    assert!(!shows(&rt, "Build") && !shows(&rt, "Check"));
    on(&mut rt, crate::over::Target::Work(zk("p1", "W1")));
    assert!(shows(&rt, "Make") && shows(&rt, "Sign-off") && !shows(&rt, "Build"), "the work screen's steps");
    on(&mut rt, crate::over::Target::Project("p1".into()));
    assert!(shows(&rt, "Make") && !shows(&rt, "Build"), "the project's work cards");
    let jump: Vec<String> = rt.state.ov.d.jump("export", &[], &[]).into_iter().filter(|h| h.group == "Work").map(|h| h.status).collect();
    assert!(jump.len() == 1 && jump[0].starts_with("Make · "), "{jump:?}");
}

#[test]
fn renamed_shot() {
    let Some(out) = std::env::var_os("AGENTHAIL_SHOT_DIR") else { return };
    if !isolated("work_steps::renamed_shot") {
        return;
    }
    let out = std::path::PathBuf::from(out);
    let dir = Dir::new("work-steps-renamed-shot");
    let mut rt = renamed(&dir, Some(json!({"build": "Make", "check": "Sign-off"})));
    let gpu = rune::render::Gpu::headless();
    let mut renderer = rune::render::Renderer::new(&gpu.device, rune::render::OFFSCREEN_FORMAT);
    let target = rune::render::Offscreen::new(&gpu.device, SIZE.0 as u32, SIZE.1 as u32);
    for (name, to) in [("session", None), ("work", Some(crate::over::Target::Work(zk("p1", "W1")))), ("project", Some(crate::over::Target::Project("p1".into())))] {
        if let Some(to) = to {
            on(&mut rt, to);
        }
        rt.frame(DT);
        renderer.render_runtime_to_offscreen(&gpu.device, &gpu.queue, &target, &mut rt);
        rune::render::write_png(&out.join(format!("renamed-{name}-1.png")), SIZE.0 as u32, SIZE.1 as u32, &target.read(&gpu.device)).unwrap();
    }
}

#[test]
fn the_session_step_bar_follows_the_loaded_ledger_before_its_item_read_lands() {
    if !isolated("work_steps::the_session_step_bar_follows_the_loaded_ledger_before_its_item_read_lands") {
        return;
    }
    let dir = Dir::new("work-steps-unread");
    let rt = renamed(&dir, None);
    assert!(!rt.state.items.contains_key(&zk("p1", "W1")), "no item read has landed");
    assert!(shows(&rt, "Make") && shows(&rt, "Sign-off") && !shows(&rt, "In progress"), "the rail shows the project's steps from its loaded row");
}

#[test]
fn the_session_step_bar_names_steps_as_the_projects_ledger_does_when_its_item_read_differs() {
    if !isolated("work_steps::the_session_step_bar_names_steps_as_the_projects_ledger_does_when_its_item_read_differs") {
        return;
    }
    let dir = Dir::new("work-steps-differs");
    let rt = renamed(&dir, Some(json!({"build": "Draft"})));
    assert_eq!(rt.state.items[&zk("p1", "W1")].steps.name(1), "Draft", "the item read landed with other names");
    assert!(shows(&rt, "Make") && !shows(&rt, "Draft"), "the rail agrees with the work screen and board");
}
