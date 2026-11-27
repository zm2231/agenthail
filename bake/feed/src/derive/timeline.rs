use super::phase::{state_phase, state_word};
use super::text::plain;
use super::world::{Entry, Work};

#[derive(Debug)]
pub struct Step<'a> {
    pub entry: &'a Entry,
    pub moved_back: bool,
    pub what: String,
}

fn what(t: &Entry, moved_back: bool) -> String {
    let text = match (t.kind.as_str(), t.state.as_deref()) {
        ("state", Some("parked")) => match t.text.as_deref().and_then(park_why) {
            Some(why) => format!("Parked: {why}"),
            None => format!("Moved to {}", state_word("parked")),
        },
        ("state", Some(st)) if moved_back => format!("Moved back to {}", state_word(st)),
        ("state", Some(st)) => format!("Moved to {}", state_word(st)),
        ("created", _) => "Created".into(),
        ("comment", _) => format!("Note: {}", t.text.as_deref().unwrap_or("")),
        _ => t.text.clone().unwrap_or_default(),
    };
    plain(&text)
}

fn park_why(text: &str) -> Option<&str> {
    let start = text.find("parked (")?;
    let part = &text[start..];
    let part = part.find("; handoff: ").map_or(part, |end| &part[..end]);
    part.split_once("): ").map(|(_, why)| why).filter(|w| !w.is_empty())
}

pub fn timeline(w: &Work, max: usize) -> Vec<Step<'_>> {
    let mut prev = 0;
    let mut back = Vec::with_capacity(w.timeline.len());
    for t in &w.timeline {
        let mut b = false;
        if t.kind == "state" {
            let p = state_phase(t.state.as_deref().unwrap_or(""));
            b = p < prev;
            prev = p;
        }
        back.push(b);
    }
    w.timeline
        .iter()
        .zip(back)
        .rev()
        .filter(|(t, _)| t.kind != "dep" && t.kind != "imported")
        .take(max)
        .map(|(entry, moved_back)| Step { entry, moved_back, what: what(entry, moved_back) })
        .collect()
}
