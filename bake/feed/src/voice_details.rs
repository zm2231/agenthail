use super::Call;

#[derive(Clone, Debug, PartialEq)]
pub struct Section {
    pub title: &'static str,
    pub rows: Vec<(String, String)>,
}

fn row(rows: &mut Vec<(String, String)>, name: &str, value: impl Into<String>) {
    let value = value.into();
    if !value.trim().is_empty() {
        rows.push((name.into(), value));
    }
}

impl Call {
    pub fn details(&self) -> Vec<Section> {
        let state = self.state.as_ref();
        let mut connection = Vec::new();
        row(&mut connection, "Provider", "Codex Voice");
        if let Some(target) = state.and_then(|s| s.target.as_ref()) {
            row(&mut connection, "Coding target", if target.name.is_empty() { &target.id } else { &target.name });
            row(&mut connection, "Coding provider", state.and_then(|s| s.coding_provider.clone()).unwrap_or_default());
        }
        row(&mut connection, "Audio provider", state.and_then(|s| s.audio_provider.clone()).unwrap_or_default());
        row(&mut connection, "Audio", self.title());
        if !self.has_local_call() {
            row(&mut connection, "Call unavailable", self.unavailable().unwrap_or_default());
        }
        row(&mut connection, "Call ID", state.and_then(|s| s.attempt_id.clone()).unwrap_or_default());
        if let Some(state) = state.filter(|s| s.phase == "ended") {
            if let Some(closed) = state.events.iter().rev().find(|e| e.method == "thread/realtime/closed") {
                let reason = closed.params["reason"].as_str().filter(|r| !r.is_empty()).unwrap_or("unspecified");
                row(&mut connection, "Host close", format!("{reason} · event #{}", closed.sequence));
            }
        }
        let mut sections = vec![Section { title: "Connection", rows: connection }];
        if let Some(attempt) = self.trail.last() {
            let mut log = Vec::new();
            row(&mut log, "Call", attempt.id.clone());
            row(&mut log, "Ended because", attempt.cause.clone().unwrap_or_default());
            row(&mut log, "Failed at", attempt.failed_at.clone().unwrap_or_default());
            for (name, seconds) in &attempt.milestones {
                row(&mut log, name, format!("{seconds:.2}s"));
            }
            sections.push(Section { title: "Call log", rows: log });
        }
        if let Some(session) = state.and_then(|s| s.session.as_ref()) {
            let mut orchestrator = Vec::new();
            row(&mut orchestrator, "Session", session.name.clone());
            row(&mut orchestrator, "ID", session.id.clone());
            row(&mut orchestrator, "Skill", "Agenthail operations");
            row(&mut orchestrator, "Skill digest", state.and_then(|s| s.skill_digest.clone()).unwrap_or_default());
            sections.push(Section { title: "Persistent orchestrator", rows: orchestrator });
        }
        sections
    }
}
