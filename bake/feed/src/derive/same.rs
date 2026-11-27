use std::sync::Arc;

use super::world::World;

impl World {
    pub fn same_shown(&self, other: &World) -> bool {
        self.shown == other.shown
            && Arc::ptr_eq(&self.sessions, &other.sessions)
            && self.work.len() == other.work.len()
            && self.work.iter().all(|(k, w)| other.work.get(k).is_some_and(|o| Arc::ptr_eq(w, o)))
            && self.transcripts.len() == other.transcripts.len()
            && self.transcripts.iter().all(|(k, t)| other.transcripts.get(k).is_some_and(|o| Arc::ptr_eq(t, o)))
            && self.steps == other.steps
            && self.prompted_before == other.prompted_before
            && self.started_by == other.started_by
            && self.helpers == other.helpers
            && self.rows == other.rows
            && self.tile_order == other.tile_order
            && self.found_names == other.found_names
            && self.names == other.names
            && format!("{:?}{:?}{:?}", self.records, self.usage, self.hosts) == format!("{:?}{:?}{:?}", other.records, other.usage, other.hosts)
    }
}

#[cfg(test)]
#[path = "same_tests.rs"]
mod tests;
