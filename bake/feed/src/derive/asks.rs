use super::text::plain;
use super::world::World;

#[derive(Clone, Debug)]
pub struct AskOption {
    pub label: String,
    pub detail: String,
    pub recommended: bool,
}

#[derive(Clone, Debug)]
pub struct Ask {
    pub session: String,
    pub header: String,
    pub question: String,
    pub options: Vec<AskOption>,
    pub at: String,
    pub answer: Option<String>,
}

fn option(o: &serde_json::Value) -> AskOption {
    let label = o["label"].as_str().unwrap_or("");
    AskOption { label: label.replace(" (Recommended)", ""), detail: plain(o["description"].as_str().unwrap_or("")), recommended: label.contains("(Recommended)") }
}

impl World {
    pub fn asks(&self, session: &str) -> Vec<Ask> {
        let Some(items) = self.transcripts.get(session) else { return Vec::new() };
        let mut out = Vec::new();
        for (i, call) in items.iter().enumerate() {
            if call.kind != "toolCall" || call.title != "AskUserQuestion" {
                continue;
            }
            let v: serde_json::Value = serde_json::from_str(&call.text).unwrap_or_default();
            let answer = items[i + 1..].iter().find(|r| r.kind == "toolResult" && r.call_id == call.call_id).map(|r| r.text.clone());
            for q in v["questions"].as_array().into_iter().flatten() {
                out.push(Ask {
                    session: session.to_string(),
                    header: q["header"].as_str().unwrap_or("").to_string(),
                    question: plain(q["question"].as_str().unwrap_or("")),
                    options: q["options"].as_array().into_iter().flatten().map(option).collect(),
                    at: call.timestamp.clone().unwrap_or_default(),
                    answer: answer.clone(),
                });
            }
        }
        out
    }

    pub fn last_ask(&self) -> Option<Ask> {
        self.transcripts.keys().flat_map(|sid| self.asks(sid)).filter(|a| a.options.iter().any(|o| o.recommended)).max_by(|a, b| a.at.cmp(&b.at).then(a.session.cmp(&b.session)))
    }
}
