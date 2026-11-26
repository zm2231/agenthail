use crate::catalog::Session;

pub struct TurnEnd {
    pub label: String,
    pub needs_you: bool,
    pub headline: String,
}

pub fn turn_end(x: &Session, asks_you: bool) -> TurnEnd {
    let needs_you = asks_you || x.attention.is_some();
    let shown = if x.display.label.is_empty() { "Idle".to_string() } else { x.display.label.clone() };
    let label = match &x.turn_state {
        _ if needs_you && !matches!(x.display.kind.as_str(), "waiting_person" | "needs_you" | "ask") => "Needs you".to_string(),
        Some(t) if x.display.kind == "idle" => match t.state.as_str() {
            "done" => "Done".into(),
            "waiting_person" => "Needs you".into(),
            "continuing" => "Said it would continue".into(),
            "waiting_work" if !t.reason.trim().is_empty() => format!("Waiting on {}", t.reason.trim()),
            "waiting_work" => "Waiting".into(),
            _ => shown,
        },
        _ => shown,
    };
    let headline = x.end_turn.as_ref().map(|h| h.headline.clone()).unwrap_or_default();
    TurnEnd { needs_you: needs_you || label == "Needs you", label, headline }
}

#[cfg(test)]
mod tests {
    use super::turn_end;
    use crate::catalog::Session;

    fn session(extra: &str) -> Session {
        serde_json::from_str(&format!(r#"{{"id":"s","display":{{"kind":"idle","label":"Idle"}}{extra}}}"#)).unwrap()
    }

    #[test]
    fn an_idle_crew_member_shows_how_its_turn_ended() {
        let label = |extra: &str| turn_end(&session(extra), false).label;
        assert_eq!(label(r#","turnState":{"state":"done"}"#), "Done");
        assert_eq!(label(r#","turnState":{"state":"waiting_person"}"#), "Needs you");
        assert_eq!(label(r#","turnState":{"state":"continuing"}"#), "Said it would continue");
        assert_eq!(label(r#","turnState":{"state":"waiting_work","reason":"CI on the PR"}"#), "Waiting on CI on the PR");
        assert_eq!(label(r#","turnState":{"state":"waiting_work","reason":" "}"#), "Waiting");
        assert_eq!(label(r#","turnState":{"state":"something_new"}"#), "Idle");
        assert_eq!(label(""), "Idle");
    }

    #[test]
    fn a_running_or_asking_session_keeps_its_live_label() {
        let mut working = session(r#","turnState":{"state":"done"}"#);
        working.display.kind = "working".into();
        working.display.label = "Running tests".into();
        assert_eq!(turn_end(&working, false).label, "Running tests");
        let asking = turn_end(&session(r#","turnState":{"state":"done"}"#), true);
        assert_eq!(asking.label, "Needs you");
        assert!(asking.needs_you);
        let ended = turn_end(&session(r#","endTurn":{"headline":"Shipped the fix"},"turnState":{"state":"done"}"#), false);
        assert_eq!(ended.headline, "Shipped the fix");
        assert!(!ended.needs_you);
    }
}
