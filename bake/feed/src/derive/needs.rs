use std::collections::HashSet;

use crate::catalog::{Session, parse_time};

use super::asks::Ask;
use super::sessions::busy;
use super::text::{plain, short_id};
use super::world::{Work, WorkKey, World};

#[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Debug)]
pub enum NeedKind {
    Question,
    Approval,
    Check,
    CiFailing,
    Finished,
}

impl NeedKind {
    pub fn label(self) -> &'static str {
        match self {
            NeedKind::Question => "Question",
            NeedKind::Approval => "Approval",
            NeedKind::Check => "Check the proof",
            NeedKind::CiFailing => "CI failing",
            NeedKind::Finished => "Just finished",
        }
    }

    pub fn rank(self) -> u8 {
        match self {
            NeedKind::Question => 0,
            NeedKind::Approval | NeedKind::Check => 1,
            NeedKind::CiFailing => 2,
            NeedKind::Finished => 3,
        }
    }

    pub fn wanted(self) -> &'static str {
        match self {
            NeedKind::Question => "Answer one question",
            NeedKind::Approval => "Give the go-ahead",
            NeedKind::Check => "Check the proof",
            NeedKind::CiFailing => "Fix or rerun CI",
            NeedKind::Finished => "Read what it did",
        }
    }

    pub fn ask(self) -> &'static str {
        match self {
            NeedKind::Question => "Wants your answer",
            NeedKind::Approval => "Wants your go-ahead",
            NeedKind::Check => "Does this do what was asked?",
            NeedKind::CiFailing => "Checks are failing on the PR",
            NeedKind::Finished => "Finished a turn in the last few minutes",
        }
    }

    pub fn primary(self) -> &'static str {
        match self {
            NeedKind::Question | NeedKind::Approval => "Reply",
            NeedKind::Check => "Approve",
            NeedKind::CiFailing => "See the failure",
            NeedKind::Finished => "Open",
        }
    }
}

#[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Debug)]
pub enum Group {
    Holding,
    Waiting,
}

pub const GROUPS: [Group; 2] = [Group::Holding, Group::Waiting];

impl Group {
    pub fn label(self) -> &'static str {
        match self {
            Group::Holding => "Holding up work",
            Group::Waiting => "Waiting",
        }
    }
}

#[derive(Clone, PartialEq, Eq, PartialOrd, Ord, Hash, Debug)]
pub enum Target {
    Session(String),
    Work(WorkKey),
    Project(String),
}

#[derive(Clone, Debug, PartialEq)]
pub struct Need {
    pub kind: NeedKind,
    pub group: Group,
    pub project: String,
    pub project_key: String,
    pub title: String,
    pub who: String,
    pub at: String,
    pub target: Target,
    pub label: Option<String>,
}

pub const REVIEW_WINDOW_HOURS: f64 = 48.0;
const FINISHED_MINUTES: f64 = 3.0;
const ASKED_HOURS: f64 = 24.0;
const WAITING: [&str; 8] = ["waiting on you", "waiting on your", "ready to publish", "standing by for", "your call", "your ok", "needs your", "asked which"];
const APPROVAL: [&str; 11] = ["ok to push", "your ok", "publish", "merge", "push the", "approv", "release", "going live", "go live", "sign-off", "sign off"];

pub fn headline(s: &Session) -> Option<(String, String)> {
    [&s.turn_completion, &s.end_turn]
        .into_iter()
        .flatten()
        .filter(|h| !h.headline.is_empty())
        .map(|h| (h.headline.clone(), h.at.clone().unwrap_or_else(|| s.last_active.clone())))
        .max_by(|a, b| a.1.cmp(&b.1))
}

pub fn flagged(s: &Session) -> bool {
    let latest = [&s.turn_completion, &s.end_turn].into_iter().flatten().filter(|h| !h.headline.is_empty()).max_by(|a, b| a.at.cmp(&b.at));
    latest.is_some_and(|h| h.outcome == "needs_you") || s.attention.is_some()
}

pub fn is_new(at: &str, looked: Option<&str>) -> bool {
    match (parse_time(at), looked.and_then(parse_time)) {
        (Some(at), Some(looked)) => at > looked,
        _ => false,
    }
}

pub fn arrange(needs: &mut [Need], looked: Option<&str>) {
    needs.sort_by(|a, b| {
        a.group
            .cmp(&b.group)
            .then(is_new(&b.at, looked).cmp(&is_new(&a.at, looked)))
            .then(a.kind.rank().cmp(&b.kind.rank()))
            .then(a.at.cmp(&b.at))
            .then(a.title.cmp(&b.title))
            .then(a.target.cmp(&b.target))
    });
}

pub fn need_of<'a>(needs: &'a [Need], session: &str, replayed: Option<&str>) -> Option<&'a Need> {
    needs.iter().find(|n| n.target == Target::Session(session.to_string()) && !(n.kind == NeedKind::Question && replayed == Some(session)))
}

pub fn needy(needs: &[Need], replayed: Option<&str>) -> HashSet<String> {
    needs
        .iter()
        .filter_map(|n| match &n.target {
            Target::Session(id) if !(n.kind == NeedKind::Question && replayed == Some(id.as_str())) => Some(id.clone()),
            _ => None,
        })
        .collect()
}

fn ci_failing(w: &Work) -> bool {
    let last = w.timeline.iter().rev().filter_map(|t| t.text.as_deref()).find(|x| !x.is_empty());
    last.is_some_and(|x| {
        let x = x.to_lowercase();
        x.contains("ci") && (x.contains("failed") || x.contains("failing")) && !["passed", "cause", "fixed", "merged"].iter().any(|w| x.contains(w))
    })
}

impl World {
    pub fn is_waiting_on_you(&self, w: &Work) -> bool {
        w.state == "in_review" && self.hours(&w.submitted_at()) <= REVIEW_WINDOW_HOURS
    }

    pub fn group(&self, kind: NeedKind, s: &Session) -> Group {
        if kind == NeedKind::Finished {
            return Group::Waiting;
        }
        let stopped = s.attention.as_ref().is_some_and(|a| a.kind != "replacedRequest") || s.turn_state.as_ref().is_some_and(|t| t.state == "waiting_person");
        let worker = self.started_by.contains_key(&s.id) && !s.archived;
        let in_progress = s.held_work.iter().any(|h| self.work.get(&h.key()).map_or(h.state.as_str(), |w| w.state.as_str()) == "in_progress");
        if busy(s) || stopped || worker || in_progress || s.queue_count > 0 { Group::Holding } else { Group::Waiting }
    }

    pub fn needs(&self) -> Vec<Need> {
        let mut out = Vec::new();
        for s in self.sessions.iter() {
            let Some((headline, at)) = headline(s) else { continue };
            let h = headline.to_lowercase();
            let flagged = flagged(s);
            let waiting = WAITING.iter().any(|w| h.contains(w));
            if s.project.is_none() || (self.started_by.contains_key(&s.id) && !flagged) {
                continue;
            }
            let finished = !flagged && !waiting && s.turn_completion.as_ref().is_some_and(|t| t.outcome == "finished") && self.hours(&at) * 60.0 <= FINISHED_MINUTES;
            let kind = if finished {
                NeedKind::Finished
            } else if flagged || (waiting && self.hours(&at) < ASKED_HOURS) {
                if APPROVAL.iter().any(|w| h.contains(w)) { NeedKind::Approval } else { NeedKind::Question }
            } else {
                continue;
            };
            out.push(Need { kind, group: self.group(kind, s), project: s.project_name(), project_key: s.project_key(), title: plain(&headline), who: self.title(s), at, target: Target::Session(s.id.clone()), label: None });
        }
        for w in self.works().filter(|w| self.is_waiting_on_you(w)) {
            out.push(Need {
                kind: if ci_failing(w) { NeedKind::CiFailing } else { NeedKind::Check },
                group: Group::Waiting,
                project: self.project(&w.project).map(|p| p.name.clone()).unwrap_or_default(),
                project_key: w.project.clone(),
                title: plain(&w.text),
                who: self.worker(w),
                at: w.submitted_at(),
                target: Target::Work(w.key()),
                label: Some(short_id(&w.id)),
            });
        }
        arrange(&mut out, None);
        out
    }

    pub fn ask_need(&self, a: &Ask) -> Option<Need> {
        let x = self.session(&a.session)?;
        Some(Need { kind: NeedKind::Question, group: self.group(NeedKind::Question, x), project: x.project_name(), project_key: x.project_key(), title: a.question.clone(), who: self.title(x), at: a.at.clone(), target: Target::Session(a.session.clone()), label: None })
    }
}
