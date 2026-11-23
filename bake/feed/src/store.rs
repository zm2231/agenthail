use std::collections::HashMap;

use crate::blocks::{self, Block, Options};
use crate::item::Item;
use crate::wire::Event;

#[derive(Default)]
pub struct Transcript {
    items: Vec<Item>,
    index: HashMap<String, usize>,
    pub revision: u64,
    pub turn_open: bool,
    pub error: Option<String>,
    pub seq: u64,
}

impl Transcript {
    pub fn items(&self) -> &[Item] {
        &self.items
    }

    pub fn apply(&mut self, seq: u64, events: Vec<Event>) -> bool {
        self.seq = self.seq.max(seq);
        let mut changed = false;
        for event in events {
            match event {
                Event::Upsert(item) => {
                    if item.role_is("user") && matches!(item.kind.as_str(), "message" | "text") || item.agent.as_ref().is_some_and(|a| a.is_incoming()) {
                        self.turn_open = true;
                    }
                    match self.index.get(&item.id) {
                        Some(&at) => {
                            if self.items[at] != item {
                                self.items[at] = item;
                                changed = true;
                            }
                        }
                        None => {
                            self.index.insert(item.id.clone(), self.items.len());
                            self.items.push(item);
                            changed = true;
                        }
                    }
                }
                Event::Remove(id) => {
                    if let Some(at) = self.index.remove(&id) {
                        self.items.remove(at);
                        self.index = self.items.iter().enumerate().map(|(i, item)| (item.id.clone(), i)).collect();
                        changed = true;
                    }
                }
                Event::Done => {
                    self.turn_open = false;
                    changed = true;
                }
                Event::SourceError(reason) => {
                    self.error = Some(reason);
                    changed = true;
                }
            }
        }
        if changed {
            self.revision += 1;
        }
        changed
    }

    pub fn blocks(&self, focus: bool) -> Vec<Block> {
        blocks::build(&self.items, &Options { focus, fold_agents: true, turn_open: self.turn_open })
    }
}
