use crate::catalog::Session;
use crate::Item;

use super::world::World;

pub const HANDS_ON_CAP: usize = 5;
const RECENT_HOURS: f64 = 0.5;

fn from_person(i: &Item) -> bool {
    let t = i.text.trim_start();
    i.kind == "message" && i.role_is("user") && i.agent.is_none() && !t.starts_with("<cross-session-message") && !t.starts_with("[from ")
}

impl World {
    // The first prompt starts a session and does not show the person is steering it.
    pub fn last_said(&self, session: &str) -> Option<&str> {
        if self.helpers.contains(session) || self.started_by.contains_key(session) {
            return None;
        }
        self.transcripts.get(session)?.iter().filter(|i| from_person(i)).skip(1).last()?.timestamp.as_deref()
    }

    pub fn hands_on(&self, pinned: &[String]) -> Vec<&Session> {
        let mut list: Vec<&Session> = pinned.iter().filter_map(|id| self.session(id)).filter(|s| !s.archived).collect();
        let mut recent: Vec<&Session> = self
            .sessions
            .iter()
            .filter(|s| !s.archived && !pinned.contains(&s.id) && self.last_said(&s.id).is_some_and(|at| self.hours(at) <= RECENT_HOURS))
            .collect();
        recent.sort_by(|a, b| self.hours(&a.last_active).total_cmp(&self.hours(&b.last_active)));
        list.extend(recent);
        list.truncate(HANDS_ON_CAP);
        list
    }
}
