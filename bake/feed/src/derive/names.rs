use std::collections::HashMap;

use crate::catalog::Session;

use super::text::{clip_words, plain};
use super::world::{Actor, Work, WorkKey, World};

pub(crate) fn build(sessions: &[Session], actors: &HashMap<String, Actor>, work: &HashMap<WorkKey, std::sync::Arc<Work>>) -> HashMap<String, String> {
    let mut names = HashMap::new();
    for s in sessions {
        names.insert(s.id.clone(), title_of(s, work));
    }
    for (id, a) in actors {
        let named = a.alias.as_deref().filter(|x| !x.is_empty()).map(|x| format!("@{x}")).or_else(|| a.name.clone().filter(|x| !x.is_empty()));
        names.insert(id.clone(), named.unwrap_or_else(|| kind_and_id(a.surface.as_deref(), id)));
    }
    names
}

fn title_of(s: &Session, work: &HashMap<WorkKey, std::sync::Arc<Work>>) -> String {
    let t = s.title();
    if t.starts_with('@') || (t.split_whitespace().count() <= 3 && t.len() <= 32) {
        return t;
    }
    if let Some(w) = s.held_work.first().and_then(|h| work.get(&h.key())) {
        return clip_words(&plain(&w.text), 44);
    }
    clip_words(&t, 44)
}

fn kind_and_id(surface: Option<&str>, id: &str) -> String {
    let short: String = id.trim_start_matches("td:").trim_start_matches("ses_").chars().take(6).collect();
    let kind = match surface {
        Some("claude") => "Claude",
        Some("codex") => "Codex",
        Some("notion") => "Notion",
        _ => "Session",
    };
    format!("{kind} {short}")
}

impl World {
    pub fn title(&self, s: &Session) -> String {
        self.names.get(&s.id).cloned().unwrap_or_else(|| s.title())
    }

    pub fn name_of(&self, actor: &str) -> String {
        if actor == crate::ledger::PERSON {
            return "You".into();
        }
        if let Some(n) = self.names.get(actor) {
            return n.clone();
        }
        if actor.is_empty() {
            return "Someone".into();
        }
        if actor.contains(' ') {
            return actor.trim_start_matches("td:").to_string();
        }
        kind_and_id(None, actor)
    }

    pub fn worker(&self, w: &Work) -> String {
        let holder = w.assignee.clone().unwrap_or_default();
        let actor = w.timeline.iter().rev().find(|t| t.kind == "log" && t.actor != holder).map(|t| t.actor.clone()).or(w.assignee.clone()).unwrap_or_default();
        self.name_of(&actor)
    }
}
