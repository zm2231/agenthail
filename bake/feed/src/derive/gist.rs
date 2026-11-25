use crate::catalog::Session;

use super::needs::headline;
use super::text::{first_sentence, plain};
use super::world::World;

impl World {
    pub fn gist(&self, s: &Session) -> String {
        if let Some((h, _)) = headline(s) {
            return plain(&h);
        }
        if let Some(w) = s.held_work.first() {
            return plain(&w.text);
        }
        if let Some(m) = self.transcripts.get(&s.id).and_then(|t| t.iter().rev().find(|i| i.kind == "message" && i.role_is("assistant"))) {
            return first_sentence(&plain(&m.text), 140);
        }
        s.display.label.clone()
    }
}
