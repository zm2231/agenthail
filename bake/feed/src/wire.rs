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
}
