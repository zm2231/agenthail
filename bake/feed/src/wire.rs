use std::collections::HashMap;

use serde::Deserialize;

use crate::item::Item;

#[derive(Deserialize, Clone, Debug, Default)]
#[serde(rename_all = "camelCase")]
pub struct Data {
    #[serde(default)]
    pub item_id: String,
    #[serde(default)]
    pub op: String,
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub reason: Option<String>,
    #[serde(default)]
    pub version: u64,
    #[serde(default)]
    pub context: serde_json::Value,
    #[serde(default)]
    pub goal: serde_json::Value,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Streamed {
    Context(Option<crate::settings::ContextUsage>),
    Goal(Option<crate::inspect::Goal>),
}

#[derive(Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Wire {
    pub seq: u64,
    #[serde(rename = "type", default)]
    pub kind: Option<String>,
    #[serde(default)]
    pub data: Option<Data>,
    #[serde(default)]
    pub item: Option<Item>,
    #[serde(default)]
    pub item_id: Option<String>,
    #[serde(default)]
    pub from: Option<usize>,
    #[serde(default)]
    pub text: Option<String>,
    #[serde(default)]
    pub ts: Option<String>,
}

#[derive(Clone, Debug)]
pub enum Event {
    Upsert(Item),
    Remove(String),
    Done,
    SourceError(String),
    State(Streamed),
}

pub enum Outcome {
    Events(u64, Vec<Event>),
    Resync,
    Skip,
}

#[derive(Default)]
pub struct Rebuild {
    rows: HashMap<String, (Item, usize)>,
}

impl Rebuild {
    pub fn keep(&mut self, ids: &std::collections::HashSet<String>) {
        self.rows.retain(|id, _| ids.contains(id));
    }

    pub fn apply(&mut self, wire: Wire) -> Outcome {
        if wire.kind.as_deref() == Some("append") {
            let (Some(id), Some(from), Some(text)) = (wire.item_id, wire.from, wire.text) else { return Outcome::Resync };
            let Some((item, bytes)) = self.rows.get_mut(&id) else { return Outcome::Resync };
            if *bytes != from {
                return Outcome::Resync;
            }
            item.text.push_str(&text);
            *bytes += text.len();
            if let Some(ts) = wire.ts {
                item.timestamp = Some(ts);
            }
            item.truncated = false;
            return Outcome::Events(wire.seq, vec![Event::Upsert(item.clone())]);
        }
        let Some(data) = wire.data else { return Outcome::Skip };
        let mut events = Vec::new();
        if data.kind == "source-error" {
            events.push(Event::SourceError(data.reason.unwrap_or_default()));
        } else if data.op == "remove" {
            self.rows.remove(&data.item_id);
            events.push(Event::Remove(data.item_id.clone()));
        } else if let Some(mut item) = wire.item {
            item.version = data.version;
            let bytes = item.text.len();
            self.rows.insert(item.id.clone(), (item.clone(), bytes));
            events.push(Event::Upsert(item));
        } else if data.kind == "context" {
            events.push(Event::State(Streamed::Context(crate::reply::parse(data.context.clone()).ok())));
        } else if data.kind == "goal" {
            events.push(Event::State(Streamed::Goal(crate::reply::parse::<crate::inspect::Goal>(data.goal.clone()).ok().filter(|g| !g.objective.is_empty()))));
        }
        if data.kind == "done" {
            events.push(Event::Done);
        }
        Outcome::Events(wire.seq, events)
    }
}

#[derive(Default)]
pub struct Sse {
    data: Vec<String>,
}

impl Sse {
    pub fn line(&mut self, line: &str) -> Option<String> {
        if line.is_empty() {
            if self.data.is_empty() {
                return None;
            }
            let joined = self.data.join("\n");
            self.data.clear();
            return Some(joined);
        }
        if let Some(value) = line.strip_prefix("data:") {
            self.data.push(value.strip_prefix(' ').unwrap_or(value).to_string());
        }
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_upsert_carries_the_rows_version() {
        let wire: Wire = serde_json::from_str(r#"{"seq":4,"type":"item","data":{"itemId":"a","op":"upsert","kind":"toolResult","version":812,"truncated":true,"bodyRef":"r"},"item":{"id":"a","kind":"toolResult","text":"head","truncated":true,"bodyRef":"r"}}"#).unwrap();
        let Outcome::Events(4, events) = Rebuild::default().apply(wire) else { panic!("no events") };
        let Event::Upsert(item) = &events[0] else { panic!("{events:?}") };
        assert_eq!((item.version, item.body_ref.as_deref(), item.truncated), (812, Some("r"), true));
    }

    #[test]
    fn a_context_or_goal_row_arrives_as_state_with_its_value() {
        let apply = |raw: &str| {
            let wire: Wire = serde_json::from_str(raw).unwrap();
            let Outcome::Events(9, events) = Rebuild::default().apply(wire) else { panic!("no events") };
            events
        };
        let context = apply(r#"{"seq":9,"type":"item","data":{"itemId":"state:context","version":1,"op":"replace","kind":"context","context":{"usedTokens":12,"contextWindow":100}}}"#);
        assert!(matches!(&context[..], [Event::State(Streamed::Context(Some(c)))] if c.used_tokens == 12 && c.context_window == 100), "{context:?}");
        let goal = apply(r#"{"seq":9,"type":"item","data":{"itemId":"state:goal","version":1,"op":"replace","kind":"goal","goal":{"objective":"ship it","status":"active"}}}"#);
        assert!(matches!(&goal[..], [Event::State(Streamed::Goal(Some(g)))] if g.objective == "ship it"), "{goal:?}");
        let cleared = apply(r#"{"seq":9,"type":"item","data":{"itemId":"state:goal","version":2,"op":"replace","kind":"goal","goal":null}}"#);
        assert!(matches!(&cleared[..], [Event::State(Streamed::Goal(None))]), "{cleared:?}");
    }
}
