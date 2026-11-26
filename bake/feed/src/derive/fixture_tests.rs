use std::collections::HashSet;

use super::sessions::{FILTERS, in_filter};
use super::*;

fn recorded() -> Option<World> {
    let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../views/fixture/data.json");
    let Ok(json) = std::fs::read_to_string(path) else {
        eprintln!("skipped: no recorded snapshot at {path}");
        return None;
    };
    Some(World::from_json(&json).expect("recorded snapshot parses"))
}

#[test]
fn needs_you_is_one_count_across_list_board_digest_and_sessions() {
    let Some(w) = recorded() else { return };
    let needs = w.needs();
    assert!(!needs.is_empty());
    for n in &needs {
        assert!(w.project(&n.project_key).is_some(), "need for an unlisted project: {:?}", n.target);
    }
    for p in &w.projects {
        let work_needs = needs.iter().filter(|n| n.project_key == p.key && matches!(n.target, Target::Work(_))).count();
        let lane = w.board(&p.key).lanes[0].len();
        let digest = w.digest(p, WINDOW_HOURS).iter().filter(|l| l.kind == Happened::Waiting).count();
        assert_eq!(work_needs, lane, "{}: needs list and waiting lane disagree", p.key);
        assert_eq!(work_needs, digest, "{}: needs list and digest disagree", p.key);
    }
    let needy = needs::needy(&needs, None);
    let sessions_needing = needs.iter().filter(|n| matches!(n.target, Target::Session(_))).count();
    assert_eq!(needy.len(), sessions_needing, "one need per session");
    assert_eq!(w.filter_counts(&needy)[FILTERS.iter().position(|f| *f == "Needs you").unwrap()], sessions_needing);
}

#[test]
fn needs_are_ordered_by_group_then_kind_then_oldest_first() {
    let Some(w) = recorded() else { return };
    let needs = w.needs();
    for pair in needs.windows(2) {
        let (a, b) = (&pair[0], &pair[1]);
        assert!((a.group, a.kind.rank(), &a.at) <= (b.group, b.kind.rank(), &b.at), "{:?} {:?} before {:?} {:?}", a.group, a.kind, b.group, b.kind);
    }
}

#[test]
fn an_item_never_reads_failed_and_scope_at_once() {
    let Some(w) = recorded() else { return };
    for p in &w.projects {
        for l in w.digest(p, WINDOW_HOURS).iter().filter(|l| l.kind == Happened::Failed) {
            let Target::Work(id) = &l.target else { panic!("a failure names a work item") };
            let ph = phase(&w.work[id]);
            assert!(ph != 0 && ph != phase::SHIP, "{id} reads Failed while in {}", PHASES[ph]);
        }
    }
    for n in w.needs().iter().filter(|n| n.kind == NeedKind::CiFailing) {
        let Target::Work(id) = &n.target else { panic!("CI failing names a work item") };
        assert_eq!(phase(&w.work[id]), phase::CHECK);
    }
}

#[test]
fn every_board_item_lands_in_exactly_one_lane() {
    let Some(w) = recorded() else { return };
    let mut stale = 0;
    for p in &w.projects {
        let board = w.board(&p.key);
        let placed: Vec<&str> = board.lanes.iter().flatten().chain(&board.stale_review).map(|x| x.id.as_str()).collect();
        let unique: HashSet<&str> = placed.iter().copied().collect();
        assert_eq!(placed.len(), unique.len(), "{}: an item sits in two lanes", p.key);
        let eligible = w
            .work
            .values()
            .filter(|x| x.project == p.key)
            .filter(|x| matches!(x.state.as_str(), "in_review" | "in_progress" | "open" | "ready") || (x.state == "done" && w.hours(x.last_at()) < 24.0))
            .count();
        assert_eq!(placed.len(), eligible, "{}: lane totals and item count differ", p.key);
        for (i, lane) in board.lanes.iter().enumerate() {
            let f = board::fold(lane, false);
            assert_eq!(f.shown.len() + f.hidden, lane.len(), "{} lane {i}", p.key);
            assert!(f.shown.len() <= LANE_CAP);
        }
        stale += board.stale_review.len();
    }
    assert!(stale > 0, "the recording has reviews older than the window");
}

#[test]
fn ended_sessions_resolve_to_their_alias() {
    let Some(w) = recorded() else { return };
    let live: HashSet<&str> = w.sessions.iter().map(|s| s.id.as_str()).collect();
    let mut ended = 0;
    for t in w.work.values().flat_map(|x| &x.timeline) {
        if t.actor.starts_with("td:") || t.actor.is_empty() {
            continue;
        }
        let name = w.name_of(&t.actor);
        assert!(!name.starts_with("Session ") && !name.contains(&t.actor), "{} has no name", t.actor);
        if !live.contains(t.actor.as_str()) {
            ended += 1;
        }
    }
    assert!(ended > 0, "the recording has ended workers in its timelines");
}

#[test]
fn hands_on_holds_only_sessions_the_person_is_steering() {
    let Some(w) = recorded() else { return };
    let list = w.hands_on(&[]);
    assert!(!list.is_empty() && list.len() <= hands_on::HANDS_ON_CAP);
    for s in &list {
        assert!(!w.started_by.contains_key(&s.id) && !w.helpers.contains(&s.id), "{} was started by an agent", s.id);
        assert!(w.last_said(&s.id).is_some_and(|at| w.hours(at) <= 0.5));
    }
}

#[test]
fn the_sessions_list_shows_each_session_once_with_crew_under_its_lead() {
    let Some(w) = recorded() else { return };
    let needy = needs::needy(&w.needs(), None);
    let all: Vec<_> = w.sessions.iter().filter(|x| in_filter(0, x, &needy)).collect();
    let groups = sessions::groups(&all, &needy);
    let mut seen = HashSet::new();
    for g in &groups {
        let rows = w.ordered_with_crew(g.sessions.clone(), &needy);
        assert_eq!(rows.len(), g.sessions.len(), "{}", g.name);
        for (i, (x, depth)) in rows.iter().enumerate() {
            assert!(seen.insert(x.id.as_str()));
            if *depth > 0 {
                let lead = &w.started_by[&x.id];
                assert!(rows[..i].iter().any(|(p, d)| &p.id == lead && *d == depth - 1), "{} is not under its lead", x.id);
            }
        }
    }
    assert_eq!(seen.len(), w.filter_counts(&needy)[0]);
    assert!(groups.windows(2).all(|g| !(g[0].key.is_none() && g[1].key.is_some())), "No project sorts last");
}

#[test]
fn since_you_left_lists_the_busiest_projects_first() {
    let Some(w) = recorded() else { return };
    let digests = w.digests(WINDOW_HOURS);
    assert!(!digests.is_empty());
    let weight = |l: &[Line]| l.iter().filter(|x| x.kind != Happened::Said).count();
    assert!(digests.windows(2).all(|d| weight(&d[0].1) >= weight(&d[1].1)));
    for (_, lines) in &digests {
        let keys: HashSet<(Happened, &str)> = lines.iter().map(|l| (l.kind, l.text.as_str())).collect();
        assert_eq!(keys.len(), lines.len(), "a line repeats");
        assert!(lines.iter().filter(|l| l.kind != Happened::Waiting).all(|l| w.hours(&l.at) <= WINDOW_HOURS));
    }
}

#[test]
fn the_same_recording_always_derives_the_same_order() {
    let Some(a) = recorded() else { return };
    let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../views/fixture/data.json");
    let order = |w: &World| {
        let needs: Vec<String> = w.needs().iter().map(|n| format!("{:?}", n.target)).collect();
        let digests: Vec<String> = w.digests(WINDOW_HOURS).iter().flat_map(|(_, l)| l.iter().map(|x| format!("{:?}", x.target))).collect();
        let lanes: Vec<String> = w.projects.iter().flat_map(|p| w.board(&p.key).lanes.into_iter().flatten().map(|x| x.id.clone())).collect();
        let jump: Vec<String> = w.jump("", &w.needs(), &[]).iter().map(|h| format!("{:?}", h.target)).collect();
        (needs, digests, lanes, jump)
    };
    let first = order(&a);
    for _ in 0..8 {
        let b = World::from_json(&std::fs::read_to_string(path).unwrap()).unwrap();
        assert_eq!(order(&b), first);
    }
}
