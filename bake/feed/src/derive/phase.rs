use super::world::{Work, World};

pub const PHASES: [&str; 6] = ["Scope", "Build", "Review", "Verify", "Check", "Ship"];
const KEYS: [&str; 6] = ["scope", "build", "review", "verify", "check", "ship"];
pub const CHECK: usize = 4;
pub const SHIP: usize = 5;

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

pub fn step_word(phase: &str, state: &str) -> String {
    if let Some(word) = off_board(state) {
        return word.into();
    }
    match known(phase) {
        Some(i) => PHASES[i].into(),
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
        assert_eq!(step_word("scope", "parked"), "Parked");
        assert_eq!(step_word("", "closed"), "Closed");
        assert_eq!(step_word("verify", "in_progress"), "Verify");
        assert_eq!(step_word("ship", "done"), "Ship");
        assert_eq!(step_word("", "in_review"), "In review");
        assert_eq!(step_word("", "in_progress"), "In progress");
        assert_eq!(step_word("triage_later", "open"), "Triage later");
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
        assert_eq!(step_word(&w.phase, &w.state), "Ship");
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
