use std::collections::HashMap;
use std::sync::Arc;

use time::OffsetDateTime;

use super::digest::{Happened, Line, first_of_each};
use super::needs::{REVIEW_WINDOW_HOURS, headline};
use super::world::{Project, Work, World};

#[derive(Default)]
pub struct Held {
    projects: HashMap<String, Kept>,
}

struct Kept {
    now: OffsetDateTime,
    window_hours: f64,
    works: Vec<Arc<Work>>,
    names: Vec<(String, String)>,
    work_sorted: Arc<Vec<Line>>,
    said: Vec<Said>,
    said_sorted: Arc<Vec<Line>>,
    lines: Arc<Vec<Line>>,
}

type Said = (String, Option<(String, String)>, String);

impl World {
    pub fn digests_held(&self, held: &mut Held, window_hours: f64) -> Vec<(&Project, Arc<Vec<Line>>)> {
        let mut by: HashMap<&str, Vec<&Arc<Work>>> = HashMap::new();
        for w in self.work.values() {
            by.entry(w.project.as_str()).or_default().push(w);
        }
        let mut kept = HashMap::with_capacity(self.projects.len());
        let mut out = Vec::new();
        for p in &self.projects {
            let mut works: Vec<Arc<Work>> = by.get(p.key.as_str()).into_iter().flatten().map(|w| Arc::clone(w)).collect();
            works.sort_by_key(|w| Arc::as_ptr(w) as usize);
            let said: Vec<Said> = p.sessions.iter().filter_map(|id| self.session(id)).map(|s| (s.id.clone(), headline(s), self.title(s))).collect();
            let k = self.keep(held.projects.remove(&p.key), p, works, said, window_hours);
            if !k.lines.is_empty() {
                out.push((p, Arc::clone(&k.lines)));
            }
            kept.insert(p.key.clone(), k);
        }
        held.projects = kept;
        super::digest::rank(&mut out);
        out
    }

    fn keep(&self, last: Option<Kept>, p: &Project, works: Vec<Arc<Work>>, said: Vec<Said>, window_hours: f64) -> Kept {
        let last = last.filter(|k| self.shown >= k.now && (self.shown - k.now).as_seconds_f64() / 3600.0 >= window_hours - k.window_hours);
        let same_work = last.as_ref().is_some_and(|k| {
            k.works.len() == works.len() && k.works.iter().zip(&works).all(|(a, b)| Arc::ptr_eq(a, b)) && k.names.iter().all(|(actor, name)| self.name_of(actor) == *name)
        });
        let same_said = last.as_ref().is_some_and(|k| k.said == said);
        let (names, work_sorted) = match last.as_ref().filter(|_| same_work) {
            Some(k) => (k.names.clone(), self.still_within(&k.work_sorted, window_hours)),
            None => (self.starters(&works, window_hours), Arc::new(self.work_lines(works.iter().map(|w| &**w), window_hours))),
        };
        let said_sorted = match last.as_ref().filter(|_| same_said) {
            Some(k) => self.still_within(&k.said_sorted, window_hours),
            None => Arc::new(self.said_lines(p, window_hours)),
        };
        let lines = match last {
            Some(k) if Arc::ptr_eq(&k.work_sorted, &work_sorted) && Arc::ptr_eq(&k.said_sorted, &said_sorted) => k.lines,
            _ => Arc::new(first_of_each(&work_sorted).into_iter().chain(first_of_each(&said_sorted)).collect()),
        };
        Kept { now: self.shown, window_hours, works, names, work_sorted, said, said_sorted, lines }
    }

    fn starters(&self, works: &[Arc<Work>], window_hours: f64) -> Vec<(String, String)> {
        let mut actors: Vec<&str> = works
            .iter()
            .flat_map(|w| w.timeline.iter())
            .filter(|t| t.kind == "state" && t.state.as_deref() == Some("in_progress") && self.hours(&t.at) <= window_hours)
            .map(|t| t.actor.as_str())
            .collect();
        actors.sort_unstable();
        actors.dedup();
        actors.into_iter().map(|a| (a.to_string(), self.name_of(a))).collect()
    }

    fn still_within(&self, lines: &Arc<Vec<Line>>, window_hours: f64) -> Arc<Vec<Line>> {
        let within = |l: &Line| self.hours(&l.at) <= if l.kind == Happened::Waiting { REVIEW_WINDOW_HOURS } else { window_hours };
        if lines.iter().all(within) {
            return Arc::clone(lines);
        }
        Arc::new(lines.iter().filter(|l| within(l)).cloned().collect())
    }
}
