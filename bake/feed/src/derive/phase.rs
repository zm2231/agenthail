use std::collections::HashMap;
use std::sync::Arc;

use serde::{Deserialize, Deserializer};

use super::world::{Work, World};

pub const PHASES: [&str; 6] = ["Scope", "Build", "Review", "Verify", "Check", "Ship"];
const KEYS: [&str; 6] = ["scope", "build", "review", "verify", "check", "ship"];
pub const CHECK: usize = 4;
pub const SHIP: usize = 5;

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Steps(Option<Arc<[String; 6]>>);

impl Steps {
    pub fn from_labels(labels: &HashMap<String, String>) -> Steps {
        let own = |i: usize| labels.iter().find(|(k, _)| k.eq_ignore_ascii_case(KEYS[i])).map(|(_, v)| v.trim()).filter(|v| !v.is_empty());
        let names: [String; 6] = std::array::from_fn(|i| own(i).unwrap_or(PHASES[i]).to_string());
        if names.iter().zip(PHASES).all(|(n, d)| n == d) { Steps(None) } else { Steps(Some(Arc::new(names))) }
    }

    pub fn from_reply(reply: &serde_json::Value) -> Steps {
        serde_json::from_value::<HashMap<String, String>>(reply["phaseLabels"].clone()).map(|l| Steps::from_labels(&l)).unwrap_or_default()
    }

    pub fn name(&self, i: usize) -> &str {
        let i = i.min(SHIP);
        self.0.as_ref().map_or(PHASES[i], |n| n[i].as_str())
    }

    pub fn names(&self) -> impl Iterator<Item = &str> {
        (0..PHASES.len()).map(|i| self.name(i))
    }
}

impl<'de> Deserialize<'de> for Steps {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Steps, D::Error> {
        Ok(Option::<HashMap<String, String>>::deserialize(d)?.map(|l| Steps::from_labels(&l)).unwrap_or_default())
    }
}

pub fn move_of(whose: &str) -> String {
    if whose == "You" { "Your move".into() } else { format!("{whose}'s move") }
}

pub fn state_phase(state: &str) -> usize {
    match state {
        "in_progress" => 1,
        "in_review" => CHECK,
        "done" | "closed" => SHIP,
        _ => 0,
    }
}

pub fn off_board(state: &str) -> Option<&'static str> {
    match state {
        "parked" => Some("Parked"),
        "closed" => Some("Closed"),
        _ => None,
    }
}

pub fn known(phase: &str) -> Option<usize> {
    KEYS.iter().position(|k| k.eq_ignore_ascii_case(phase.trim()))
}

pub fn shown_step(phase: &str, state: &str) -> Option<usize> {
    known(phase).filter(|_| off_board(state).is_none())
}

pub fn step(phase: &str, state: &str) -> usize {
    known(phase).unwrap_or_else(|| state_phase(state))
}

pub fn step_word(phase: &str, state: &str, steps: &Steps) -> String {
    if let Some(word) = off_board(state) {
        return word.into();
    }
    match known(phase) {
        Some(i) => steps.name(i).into(),
        None if phase.trim().is_empty() => state_name(state),
        None => super::text::capital(&phase.trim().replace('_', " ")),
    }
}

pub fn state_name(state: &str) -> String {
    match state {
        "open" => "Open".into(),
        "in_progress" => "In progress".into(),
        "in_review" => "In review".into(),
        "done" => "Done".into(),
        "closed" => "Closed".into(),
        "parked" => "Parked".into(),
        "blocked" => "Blocked".into(),
        other => super::text::capital(&other.replace('_', " ")),
    }
}

pub fn phase(w: &Work) -> usize {
    if w.state == "in_review" { CHECK } else { step(&w.phase, &w.state) }
}

impl World {
    pub fn steps_for(&self, ledger: &str) -> Option<&Steps> {
        let record = self.records.iter().find(|p| p.ledger_key == ledger || p.id == ledger);
        let keys = [record.map(|p| p.id.as_str()), Some(ledger), record.map(|p| p.ledger_key.as_str())];
        keys.into_iter().flatten().find_map(|k| self.steps.get(k))
    }

    pub fn steps_of(&self, ledger: &str) -> Steps {
        self.steps_for(ledger).cloned().unwrap_or_default()
    }

    pub fn whose_move(&self, w: &Work) -> String {
        if matches!(w.state.as_str(), "parked" | "closed") {
            return "No one".into();
        }
        match phase(w) {
            CHECK => "You".into(),
            0 | SHIP => match self.project(&w.project) {
                Some(p) if p.lead.is_none() && p.lead_pending.is_none() => "Anyone".into(),
                Some(p) => p.lead_name.clone(),
                None => "The lead".into(),
            },
            _ if w.assignee.as_deref() == Some(crate::ledger::PERSON) => "You".into(),
            _ => self.worker(w),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_step_is_the_ledger_phase_and_falls_back_to_the_state() {
        for (i, key) in KEYS.iter().enumerate() {
            assert_eq!(step(key, "in_progress"), i, "{key}");
            assert_eq!(PHASES[i].to_lowercase(), *key);
        }
        assert_eq!(PHASES[step("verify", "in_progress")], "Verify");
        assert_eq!(PHASES[step("Check", "in_progress")], "Check");
        assert_eq!(PHASES[step("", "in_review")], "Check");
        assert_eq!(PHASES[step("", "in_progress")], "Build");
        assert_eq!(PHASES[step("", "done")], "Ship");
        assert_eq!(PHASES[step("", "open")], "Scope");
        assert_eq!(PHASES[step("a later phase", "in_progress")], "Build");
    }

    #[test]
    fn the_step_word_names_parked_and_closed_work_then_the_phase_then_the_state() {
        assert_eq!(step_word_default("scope", "parked"), "Parked");
        assert_eq!(step_word_default("", "closed"), "Closed");
        assert_eq!(step_word_default("verify", "in_progress"), "Verify");
        assert_eq!(step_word_default("ship", "done"), "Ship");
        assert_eq!(step_word_default("", "in_review"), "In review");
        assert_eq!(step_word_default("", "in_progress"), "In progress");
        assert_eq!(step_word_default("triage_later", "open"), "Triage later");
        assert_eq!(state_name("deleted"), "Deleted");
        assert_eq!(shown_step("verify", "in_progress"), Some(3));
        assert_eq!(shown_step("scope", "parked"), None);
        assert_eq!(shown_step("", "in_review"), None);
    }

    #[test]
    fn work_reads_its_phase_from_the_ledger_not_from_its_log_words() {
        let w: Work = serde_json::from_value(serde_json::json!({"id": "w", "state": "in_progress", "phase": "review", "timeline": [{"at": "2026-11-27T01:00:00Z", "kind": "log", "text": "gate passed, verified"}]})).unwrap();
        assert_eq!(PHASES[phase(&w)], "Review");
        let older: Work = serde_json::from_value(serde_json::json!({"id": "w", "state": "in_progress", "timeline": [{"at": "2026-11-27T01:00:00Z", "kind": "log", "text": "verified"}]})).unwrap();
        assert_eq!(PHASES[phase(&older)], "Build");
    }

    #[test]
    fn merged_work_still_in_review_waits_on_its_check_and_shows_ship() {
        let w: Work = serde_json::from_value(serde_json::json!({"id": "w", "state": "in_review", "phase": "ship"})).unwrap();
        assert_eq!(phase(&w), CHECK);
        assert_eq!(shown_step(&w.phase, &w.state), Some(SHIP));
        assert_eq!(step_word(&w.phase, &w.state, &w.steps), "Ship");
    }

    fn step_word_default(phase: &str, state: &str) -> String {
        step_word(phase, state, &Steps::default())
    }

    #[test]
    fn a_project_that_renames_two_steps_shows_its_names_and_keeps_the_rest() {
        let steps: Steps = serde_json::from_value(serde_json::json!({"scope": "Scope", "build": "Make", "review": "Review", "verify": "  ", "check": "Sign-off", "ship": "Ship"})).unwrap();
        assert_eq!(steps.names().collect::<Vec<_>>(), ["Scope", "Make", "Review", "Verify", "Sign-off", "Ship"]);
        assert_eq!(step_word("build", "in_progress", &steps), "Make");
        assert_eq!(step_word("CHECK", "in_review", &steps), "Sign-off");
        assert_eq!(step_word("", "in_review", &steps), "In review");
        assert_eq!(step_word("check", "parked", &steps), "Parked");
        assert_eq!(steps.name(9), "Ship");
    }

    #[test]
    fn default_names_when_a_project_has_none() {
        let same: Steps = serde_json::from_value(serde_json::json!({"scope": "Scope", "build": "Build", "review": "Review", "verify": "Verify", "check": "Check", "ship": "Ship"})).unwrap();
        assert_eq!(same, Steps::default());
        assert_eq!(serde_json::from_value::<Steps>(serde_json::Value::Null).unwrap(), Steps::default());
        assert_eq!(Steps::from_reply(&serde_json::json!({"work": []})), Steps::default());
        assert_eq!(Steps::default().names().collect::<Vec<_>>(), PHASES);
        let w: Work = serde_json::from_value(serde_json::json!({"id": "w", "state": "in_progress", "phase": "verify"})).unwrap();
        assert_eq!(step_word(&w.phase, &w.state, &w.steps), "Verify");
    }

    #[test]
    fn parked_and_closed_work_is_off_the_phases() {
        assert_eq!(off_board("parked"), Some("Parked"));
        assert_eq!(off_board("closed"), Some("Closed"));
        for state in ["open", "blocked", "in_progress", "in_review", "done"] {
            assert_eq!(off_board(state), None, "{state}");
        }
    }
}
