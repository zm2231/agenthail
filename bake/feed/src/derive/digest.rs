use std::collections::HashSet;

use super::needs::headline;
use super::text::{first_sentence, plain};
use super::world::{Project, World};
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

pub fn pick(lines: &[Line], per_kind: usize) -> Vec<&Line> {
    HAPPENED.iter().flat_map(|k| lines.iter().filter(move |l| l.kind == *k).take(per_kind)).collect()
}

fn ci_failed(body: &str) -> bool {
    let l = body.to_lowercase();
    let ci = l.contains("ci run") || l.contains("ci ") || l.contains("test run") || l.contains("suite");
    ci && l.contains(" failed") && !["not from", "since fixed", "cause", "passed", "invalid"].iter().any(|w| l.contains(w))
}

impl World {
    pub fn digest(&self, project: &Project, window_hours: f64) -> Vec<Line> {
        let mut lines = Vec::new();
        for w in self.work.values().filter(|w| w.project == project.key) {
            for t in w.timeline.iter().filter(|t| self.hours(&t.at) <= window_hours) {
                let line = |kind, detail| Line { kind, text: plain(&w.text), detail, at: t.at.clone(), target: Target::Work(w.id.clone()) };
                match (t.kind.as_str(), t.state.as_deref()) {
                    ("state", Some("done")) => lines.push(line(Happened::Shipped, String::new())),
                    ("state", Some("in_progress")) => lines.push(line(Happened::Moved, format!("Started by {}", self.name_of(&t.actor)))),
                    ("log", _) if matches!(w.state.as_str(), "in_progress" | "in_review") && ci_failed(t.text.as_deref().unwrap_or("")) => lines.push(line(Happened::Failed, first_sentence(&plain(t.text.as_deref().unwrap_or("")), 120))),
                    _ => {}
                }
            }
        }
        for w in self.waiting_on_you(&project.key) {
            lines.push(Line { kind: Happened::Waiting, text: plain(&w.text), detail: "Check the proof".into(), at: w.submitted_at(), target: Target::Work(w.id.clone()) });
        }
        for s in project.sessions.iter().filter_map(|id| self.session(id)) {
            if let Some((h, at)) = headline(s).filter(|(_, at)| self.hours(at) <= window_hours) {
                lines.push(Line { kind: Happened::Said, text: plain(&h), detail: self.title(s), at, target: Target::Session(s.id.clone()) });
            }
        }
        let mut seen = HashSet::new();
        lines.sort_by(|a, b| a.kind.cmp(&b.kind).then(b.at.cmp(&a.at)).then(a.text.cmp(&b.text)).then(a.target.cmp(&b.target)));
        lines.retain(|l| seen.insert((l.kind, l.text.clone())));
        lines
    }

    pub fn digests(&self, window_hours: f64) -> Vec<(&Project, Vec<Line>)> {
        let mut out: Vec<(&Project, Vec<Line>)> = self.projects.iter().map(|p| (p, self.digest(p, window_hours))).filter(|(_, l)| !l.is_empty()).collect();
        let latest = |l: &[Line]| l.iter().map(|x| x.at.clone()).max().unwrap_or_default();
        let weight = |l: &[Line]| l.iter().filter(|x| x.kind != Happened::Said).count();
        out.sort_by(|a, b| weight(&b.1).cmp(&weight(&a.1)).then(latest(&b.1).cmp(&latest(&a.1))));
        out
    }
}
