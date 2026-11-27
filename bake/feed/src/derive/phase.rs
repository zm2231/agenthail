use super::world::{Work, World};

pub const PHASES: [&str; 6] = ["Scope", "Build", "Review", "Verify", "Check", "Ship"];
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

pub fn state_word(st: &str) -> &'static str {
    match st {
        "in_progress" => "Build",
        "in_review" => "Check",
        "done" => "Shipped",
        "open" | "ready" => "Up next",
        "parked" => "Parked",
        "blocked" => "Blocked",
        "closed" => "Closed",
        _ => "a new state",
    }
}

pub fn phase(w: &Work) -> usize {
    if w.state != "in_progress" {
        return state_phase(&w.state);
    }
    let logs: Vec<String> = w.timeline.iter().filter(|t| t.kind == "log").map(|t| t.text.as_deref().unwrap_or("").to_lowercase()).collect();
    if logs.iter().any(|l| l.contains("sim run") || l.contains("gate pass") || l.contains("passed") || l.contains("verified")) {
        3
    } else if logs.iter().any(|l| l.contains("review") || l.contains("ship")) {
        2
    } else {
        1
    }
}

impl World {
    pub fn whose_move(&self, w: &Work) -> String {
        match phase(w) {
            CHECK => "You".into(),
            0 | SHIP => match self.project(&w.project) {
                Some(p) if p.lead.is_none() && p.lead_pending.is_none() => "Anyone".into(),
                Some(p) => p.lead_name.clone(),
                None => "The lead".into(),
            },
            _ => self.worker(w),
        }
    }
}
