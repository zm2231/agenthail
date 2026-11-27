use super::*;
use crate::over::{Group, Need, NeedKind, Target};

const ROWS: usize = 100;
const FRAMES: usize = 200;

fn need(i: usize) -> Need {
    Need { kind: NeedKind::Question, group: Group::Waiting, project: "Alpha".into(), project_key: "p1".into(), title: format!("Question number {i} about the export"), who: format!("s{i}"), at: "2026-11-27T08:50:00Z".into(), target: Target::Session(format!("s{i}")), label: None }
}

fn median(mut v: Vec<f64>) -> f64 {
    v.sort_by(f64::total_cmp);
    v[v.len() / 2]
}

#[test]
fn one_changed_row_on_a_now_of_a_hundred_rows() {
    let Some(out) = std::env::var_os("AGENTHAIL_ROW_COST") else { return };
    if !isolated("row_cost::one_changed_row_on_a_now_of_a_hundred_rows") {
        return;
    }
    let dir = Dir::new("row-cost");
    let all: Vec<Session> = (0..ROWS).map(|i| session(&format!("s{i}"), (i % 60) as u64, json!({"name": format!("Session {i}"), "status": if i % 10 == 0 { "busy" } else { "idle" }}))).collect();
    let mut rt = runtime(app(all, &dir), (1440.0, 900.0));
    rt.update(|s, _| {
        crate::over::go(s, Screen::Now);
        s.ov.now_brief = false;
        crate::over::layout::set_collapsed(s, [false, false, false]);
        s.ov.hands_on = (0..6).map(|i| format!("s{}", i * 7 + 1)).collect();
        s.ov.needs = (0..ROWS).map(need).collect();
    });
    for _ in 0..10 {
        rt.settle_all();
        rt.frame(DT);
    }
    rune::take_frame_phases();
    let (mut view, mut commit, mut apply, mut frame) = (Vec::new(), Vec::new(), Vec::new(), Vec::new());
    for f in 0..FRAMES {
        rt.update(|s, _| s.ov.needs[ROWS / 2].title = format!("Question {f} about the export"));
        let at = std::time::Instant::now();
        rt.frame(DT);
        frame.push(at.elapsed().as_secs_f64() * 1e3);
        let p = rune::take_frame_phases();
        view.push(p[0]);
        commit.push(p[1]);
        apply.push(p[5]);
    }
    let line = format!("frames {FRAMES} rows {ROWS}: frame median {:.3} ms, commit median {:.3} ms, view median {:.3} ms, apply median {:.3} ms", median(frame), median(commit), median(view), median(apply));
    std::fs::write(out, line).unwrap();
}
