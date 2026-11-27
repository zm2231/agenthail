use super::*;
use crate::over::rows::built;

fn raw(path: &str, later: i64, without: Option<&str>) -> Recorded {
    let mut v: serde_json::Value = serde_json::from_str(&std::fs::read_to_string(path).unwrap()).unwrap();
    let rfc = time::format_description::well_known::Rfc3339;
    let at = time::OffsetDateTime::parse(v["updatedAt"].as_str().unwrap(), &rfc).unwrap() + time::Duration::seconds(later);
    v["updatedAt"] = json!(at.format(&rfc).unwrap());
    if let Some(id) = without {
        v["sessions"].as_array_mut().unwrap().retain(|x| x["id"] != json!(id));
    }
    serde_json::from_value(v).unwrap()
}

fn poll(path: &str, later: i64, without: Option<&str>, sent: &mut Sent) -> Bg {
    let mut recorded = raw(path, later, without);
    recorded.transcripts.clear();
    let key_of = |id: &str| recorded.projects.iter().find(|p| p.id == id).map_or(String::new(), |p| p.ledger_key.clone());
    let ledgers: HashMap<_, _> = std::mem::take(&mut recorded.ledgers)
        .into_iter()
        .map(|(k, rows)| {
            let steps = recorded.steps.get(&k).cloned().unwrap_or_default();
            let rows = feed::derive::live::shared_rows(sent.rows.get(&k), &k, &key_of(&k), &steps, rows);
            sent.rows.insert(k.clone(), rows.clone());
            (k, (std::time::Instant::now(), Ok(rows)))
        })
        .collect();
    sent.sessions = feed::derive::live::shared_sessions(&sent.sessions, std::mem::take(&mut recorded.sessions));
    Bg::Overview(Box::new(Fetched { recorded, sessions: sent.sessions.clone(), ledgers, held: Vec::new(), asked: Vec::new(), actors_at: std::time::Instant::now(), usage: None, moved: Vec::new() }))
}

#[derive(Default)]
struct Sent {
    rows: HashMap<String, Vec<std::sync::Arc<feed::derive::Work>>>,
    sessions: std::sync::Arc<Vec<feed::catalog::Session>>,
}

fn tally(ids: Vec<String>) -> String {
    let mut by: std::collections::BTreeMap<String, usize> = Default::default();
    for id in ids {
        let kind = id.split(['-', '/']).next().unwrap_or("").to_string();
        *by.entry(kind).or_default() += 1;
    }
    format!("{by:?}")
}

#[test]
fn probe() {
    let Some(path) = std::env::var_os("AGENTHAIL_LIVE_SHAPE") else { return };
    let out = std::env::var("AGENTHAIL_LIVE_OUT").unwrap();
    if !isolated("live_shape::probe") {
        return;
    }
    let path = path.to_string_lossy().to_string();
    let first = raw(&path, 0, None);
    let dir = Dir::new("live-shape");
    let mut app = crate::empty(Overview::recorded(first), false);
    app.sessions = app.ov.d.sessions.clone();
    app.now = app.ov.d.now;
    app.fixtures = Some(Fixtures { dir: dir.path(), streams: HashMap::new(), speed: 1.0 });
    app.status = "fixtures".into();
    if let Ok(path) = std::env::var("AGENTHAIL_LIVE_DECISIONS") {
        let page: feed::decisions::Page = serde_json::from_str(&std::fs::read_to_string(path).unwrap()).unwrap();
        let mut inbox = feed::decisions::Inbox::default();
        inbox.add(page, "", "", "");
        inbox.settle();
        app.ov.inbox.inbox = Some(inbox);
    }
    let mut rt = runtime(app, (1440.0, 900.0));
    rt.update(|s, _| {
        crate::over::go(s, Screen::Now);
        s.ov.now_brief = false;
        crate::over::layout::set_collapsed(s, [false, false, false]);
    });
    for _ in 0..6 {
        rt.settle_all();
        rt.frame(DT);
    }
    let top = rt.state.ov.needs.iter().find_map(|n| match &n.target {
        crate::over::Target::Session(id) => Some(id.clone()),
        _ => None,
    });
    let mut lines = vec![format!("needs {} sessions {} top session need {top:?}", rt.state.ov.needs.len(), rt.state.ov.d.sessions.len())];
    built::take();
    rt.frame(DT);
    lines.push(format!("idle frame: {}", tally(built::take())));
    let mut sent = Sent::default();
    let steps = [("same", 5, None), ("drop top", 10, top.as_deref()), ("back", 15, None)];
    for (name, later, without) in steps {
        send(&rt, poll(&path, later, without, &mut sent));
        rt.frame(DT);
        rt.frame(DT);
        lines.push(format!("{name}: rows built {}", tally(built::take())));
    }
    let (mut outside, mut view, mut commit, mut after, mut built_rows) = (Vec::new(), Vec::new(), Vec::new(), Vec::new(), Vec::new());
    let copies = feed::shared::copies();
    let steady = std::env::var_os("AGENTHAIL_LIVE_STEADY").is_some();
    for round in 0..30 {
        let next = poll(&path, 20 + round * 5, if round % 2 == 1 && !steady { top.as_deref() } else { None }, &mut sent);
        built::take();
        phases();
        let at = std::time::Instant::now();
        send(&rt, next);
        rt.frame(DT);
        let wall = at.elapsed().as_secs_f64() * 1000.0;
        built_rows.push(built::take().len() as f64);
        let p = phase_ms();
        outside.push(wall - p[1]);
        view.push(p[0]);
        commit.push(p[1]);
        let at = std::time::Instant::now();
        rt.frame(DT);
        after.push(at.elapsed().as_secs_f64() * 1000.0 - phase_ms()[1]);
    }
    lines.push(format!(
        "fresh poll{}, 30 frames: outside commit median {:.2} ms (max {:.2}), view median {:.2} ms, commit median {:.2} ms, next frame outside commit median {:.2} ms, shared copies {}, rows built median {:.0}",
        if steady { " (steady)" } else { "" },
        median(outside.clone()),
        outside.iter().copied().fold(0.0, f64::max),
        median(view),
        median(commit),
        median(after),
        feed::shared::copies() - copies,
        median(built_rows)
    ));
    if let Ok(dump) = std::env::var("AGENTHAIL_LIVE_PATHS") {
        let paths: Vec<String> = rt.elements().into_iter().filter(|e| !e.exiting).map(|e| e.path).collect();
        std::fs::write(dump, paths.join("\n")).unwrap();
    }
    let mut extra = rt.state.ov.needs[rt.state.ov.needs.len() - 1].clone();
    extra.target = crate::over::Target::Session("probe-inserted".into());
    extra.title = "A need inserted by the probe".into();
    let (mut view, mut commit, mut apply, mut rows) = (Vec::new(), Vec::new(), Vec::new(), Vec::new());
    for i in 0..40 {
        rt.update(|s, _| {
            if i % 2 == 0 {
                s.ov.needs.insert(0, extra.clone());
                s.ov.need_ix += 1;
            } else {
                s.ov.needs.remove(0);
                s.ov.need_ix -= 1;
            }
        });
        phases();
        rt.frame(DT);
        let p = phase_ms();
        view.push(p[0]);
        commit.push(p[1]);
        apply.push(p[5]);
        rows.push(built::take().len() as f64);
        rt.frame(DT);
        built::take();
    }
    lines.push(format!("a need in and out at the top, 40 frames: rows built median {:.0}, view median {:.2} ms, commit median {:.2} ms, apply median {:.2} ms", median(rows), median(view), median(commit), median(apply)));
    std::fs::write(out, lines.join("\n")).unwrap();
}

fn phases() {
    rune::take_frame_phases();
}

fn median(mut v: Vec<f64>) -> f64 {
    v.sort_by(f64::total_cmp);
    v[v.len() / 2]
}

fn phase_ms() -> [f64; 12] {
    rune::take_frame_phases()
}
