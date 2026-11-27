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
