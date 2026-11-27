use std::collections::HashSet;

use crate::catalog::parse_time;

use super::needs::{headline, is_new};
use super::text::{first_sentence, plain};
use super::world::{Project, Work, World};
use super::needs::Target;

pub const WINDOW_HOURS: f64 = 12.0;

#[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Debug)]
pub enum Happened {
    Shipped,
    Waiting,
    Failed,
    Moved,
    Said,
}

pub const HAPPENED: [Happened; 5] = [Happened::Shipped, Happened::Waiting, Happened::Failed, Happened::Moved, Happened::Said];

impl Happened {
    pub fn label(self) -> &'static str {
        match self {
            Happened::Shipped => "Shipped",
            Happened::Waiting => "Waiting on you",
            Happened::Failed => "Failed",
            Happened::Moved => "Moved",
            Happened::Said => "Said",
        }
    }

    pub fn word(self) -> &'static str {
        match self {
            Happened::Shipped => "shipped",
            Happened::Waiting => "waiting on you",
            Happened::Failed => "failed",
            Happened::Moved => "moved",
            Happened::Said => "said",
        }
    }
}

#[derive(Clone, Debug)]
pub struct Line {
    pub kind: Happened,
    pub text: String,
    pub detail: String,
    pub at: String,
    pub target: Target,
}

pub fn count(lines: &[Line], kind: Happened) -> usize {
    lines.iter().filter(|l| l.kind == kind).count()
}

pub fn cut<'a>(lines: Vec<&'a Line>, looked: Option<&str>) -> (Vec<&'a Line>, Vec<&'a Line>) {
    lines.into_iter().partition(|l| is_new(&l.at, looked))
}

pub fn pick(lines: &[Line], per_kind: usize) -> Vec<&Line> {
    HAPPENED.iter().flat_map(|k| lines.iter().filter(move |l| l.kind == *k).take(per_kind)).collect()
}

pub(super) fn ci_failed(body: &str) -> bool {
    let l = body.to_lowercase();
    let ci = l.contains("ci run") || l.contains("ci ") || l.contains("test run") || l.contains("suite");
    ci && l.contains(" failed") && !["not from", "since fixed", "cause", "passed", "invalid"].iter().any(|w| l.contains(w))
}

impl World {
    pub fn window(&self, looked: Option<&str>) -> f64 {
        looked.filter(|t| parse_time(t).is_some()).map_or(WINDOW_HOURS, |t| self.hours(t).max(0.0))
    }

    pub fn digest(&self, project: &Project, window_hours: f64) -> Vec<Line> {
        let work = self.work_lines(self.works().filter(|w| w.project == project.key), window_hours);
        first_of_each(&work).into_iter().chain(first_of_each(&self.said_lines(project, window_hours))).collect()
    }

    pub(super) fn work_lines<'a>(&self, works: impl Iterator<Item = &'a Work>, window_hours: f64) -> Vec<Line> {
        let mut lines = Vec::new();
        let within = |at: &str| self.hours(at) <= window_hours;
        for w in works {
            let moving = matches!(w.state.as_str(), "in_progress" | "in_review");
            for t in &w.timeline {
                let line = |kind, detail| Line { kind, text: plain(&w.text), detail, at: t.at.clone(), target: Target::Work(w.key()) };
                match (t.kind.as_str(), t.state.as_deref()) {
                    ("state", Some("done")) if within(&t.at) => lines.push(line(Happened::Shipped, String::new())),
                    ("state", Some("in_progress")) if within(&t.at) => lines.push(line(Happened::Moved, format!("Started by {}", self.name_of(&t.actor)))),
                    ("log", _) if moving && within(&t.at) && t.reports_ci_failure() => lines.push(line(Happened::Failed, first_sentence(&plain(t.text.as_deref().unwrap_or("")), 120))),
                    _ => {}
                }
            }
            if self.is_waiting_on_you(w) {
                lines.push(Line { kind: Happened::Waiting, text: plain(&w.text), detail: "Check the proof".into(), at: w.submitted_at(), target: Target::Work(w.key()) });
            }
        }
        sorted(lines)
    }

    pub(super) fn said_lines(&self, project: &Project, window_hours: f64) -> Vec<Line> {
        let mut lines = Vec::new();
        for s in project.sessions.iter().filter_map(|id| self.session(id)) {
            if let Some((h, at)) = headline(s).filter(|(_, at)| self.hours(at) <= window_hours) {
                lines.push(Line { kind: Happened::Said, text: plain(&h), detail: self.title(s), at, target: Target::Session(s.id.clone()) });
            }
        }
        sorted(lines)
    }

    pub fn digests(&self, window_hours: f64) -> Vec<(&Project, Vec<Line>)> {
        self.digests_held(&mut Default::default(), window_hours).into_iter().map(|(p, lines)| (p, lines.to_vec())).collect()
    }
}

fn sorted(mut lines: Vec<Line>) -> Vec<Line> {
    lines.sort_by(|a, b| a.kind.cmp(&b.kind).then(b.at.cmp(&a.at)).then(a.text.cmp(&b.text)).then(a.target.cmp(&b.target)));
    lines
}

pub(super) fn first_of_each(sorted: &[Line]) -> Vec<Line> {
    let mut seen = HashSet::new();
    sorted.iter().filter(|l| seen.insert((l.kind, l.text.as_str()))).cloned().collect()
}

pub(super) fn rank(out: &mut [(&Project, std::sync::Arc<Vec<Line>>)]) {
    let latest = |l: &[Line]| l.iter().map(|x| x.at.as_str()).max().unwrap_or_default().to_string();
    let weight = |l: &[Line]| l.iter().filter(|x| x.kind != Happened::Said).count();
    out.sort_by_cached_key(|(_, l)| (std::cmp::Reverse(weight(&l[..])), std::cmp::Reverse(latest(&l[..]))));
}
