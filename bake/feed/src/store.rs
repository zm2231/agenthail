use std::collections::HashMap;

use crate::blocks::{self, Block, Options};
use crate::focus::starts_turn;
use crate::item::Item;
use crate::wire::Event;

#[derive(Clone, Copy)]
struct Checkpoint {
    item: usize,
    block: usize,
    focus: bool,
}

#[derive(Default)]
pub struct Transcript {
    items: Vec<Item>,
    index: HashMap<String, usize>,
    pub revision: u64,
    pub turn_open: bool,
    pub error: Option<String>,
    pub seq: u64,
    dirty: Option<usize>,
    checkpoint: Option<Checkpoint>,
    expanded: HashMap<String, String>,
}

fn is_prompt(item: &Item) -> bool {
    starts_turn(item) && item.agent.is_none()
}

impl Transcript {
    pub fn items(&self) -> &[Item] {
        &self.items
    }

    pub fn item(&self, id: &str) -> Option<&Item> {
        self.index.get(id).map(|&at| &self.items[at])
    }

    pub fn ids(&self) -> std::collections::HashSet<String> {
        self.index.keys().cloned().collect()
    }

    pub fn prepend(&mut self, older: Vec<Item>) -> bool {
        let mut seen = std::collections::HashSet::new();
        let fresh: Vec<Item> = older.into_iter().filter(|i| !self.index.contains_key(&i.id) && seen.insert(i.id.clone())).collect();
        if fresh.is_empty() {
            return false;
        }
        self.items.splice(0..0, fresh);
        self.index = self.items.iter().enumerate().map(|(i, item)| (item.id.clone(), i)).collect();
        self.touch(0);
        self.revision += 1;
        true
    }

    pub fn expand(&mut self, id: &str, text: String) -> bool {
        let Some(&at) = self.index.get(id) else { return false };
        let item = &mut self.items[at];
        if !item.truncated {
            return false;
        }
        item.text = text.clone();
        item.truncated = false;
        self.expanded.insert(id.to_string(), text);
        self.touch(at);
        self.revision += 1;
        true
    }

    fn touch(&mut self, at: usize) {
        self.dirty = Some(self.dirty.map_or(at, |d| d.min(at)));
    }

    pub fn apply(&mut self, seq: u64, events: Vec<Event>) -> bool {
        self.seq = self.seq.max(seq);
        let mut changed = false;
        for event in events {
            match event {
                Event::Upsert(mut item) => {
                    match self.expanded.get(&item.id) {
                        Some(full) if item.truncated => {
                            item.text = full.clone();
                            item.truncated = false;
                        }
                        Some(_) => {
                            self.expanded.remove(&item.id);
                        }
                        None => {}
                    }
                    if item.role_is("user") && matches!(item.kind.as_str(), "message" | "text") || item.agent.as_ref().is_some_and(|a| a.is_incoming()) {
                        self.turn_open = true;
                    }
                    match self.index.get(&item.id) {
                        Some(&at) => {
                            if self.items[at] != item {
                                self.items[at] = item;
                                self.touch(at);
                                changed = true;
                            }
                        }
                        None => {
                            let at = self.items.len();
                            self.index.insert(item.id.clone(), at);
                            self.items.push(item);
                            self.touch(at);
                            changed = true;
                        }
                    }
                }
                Event::Remove(id) => {
                    if let Some(at) = self.index.remove(&id) {
                        self.items.remove(at);
                        self.index = self.items.iter().enumerate().map(|(i, item)| (item.id.clone(), i)).collect();
                        self.touch(at);
                        changed = true;
                    }
                }
                Event::Done => {
                    self.turn_open = false;
                    self.touch(self.items.len().saturating_sub(1));
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

    fn options(&self, focus: bool, turn_open: bool) -> Options {
        Options { focus, fold_agents: true, turn_open }
    }

    pub fn blocks(&self, focus: bool) -> Vec<Block> {
        blocks::build(&self.items, &self.options(focus, self.turn_open))
    }

    fn last_prompt(&self, from: usize) -> usize {
        (from..self.items.len()).rev().find(|&i| is_prompt(&self.items[i])).unwrap_or(from)
    }

    pub fn tail(&mut self, focus: bool) -> Option<(usize, Vec<Block>)> {
        let dirty = self.dirty.take();
        let reusable = self.checkpoint.filter(|c| c.focus == focus && dirty.is_none_or(|d| d >= c.item));
        let Some(checkpoint) = reusable else {
            let all = self.blocks(focus);
            let cut = self.last_prompt(0);
            let block = blocks::build(&self.items[..cut], &self.options(focus, false)).len();
            self.checkpoint = Some(Checkpoint { item: cut, block, focus });
            return Some((0, all));
        };
        dirty?;
        let tail = blocks::build(&self.items[checkpoint.item..], &self.options(focus, self.turn_open));
        let cut = self.last_prompt(checkpoint.item);
        if cut > checkpoint.item {
            let between = blocks::build(&self.items[checkpoint.item..cut], &self.options(focus, false)).len();
            self.checkpoint = Some(Checkpoint { item: cut, block: checkpoint.block + between, focus });
        }
        Some((checkpoint.block, tail))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn item(id: &str, text: &str) -> Item {
        Item { id: id.into(), kind: "message".into(), role: Some("assistant".into()), text: text.into(), ..Default::default() }
    }

    fn ids(t: &Transcript) -> Vec<&str> {
        t.items().iter().map(|i| i.id.as_str()).collect()
    }

    #[test]
    fn older_rows_go_in_front_once() {
        let mut t = Transcript::default();
        t.apply(1, vec![Event::Upsert(item("c", "c")), Event::Upsert(item("d", "d"))]);
        t.tail(true);
        assert!(t.prepend(vec![item("a", "a"), item("b", "b"), item("c", "stale"), item("a", "again")]));
        assert_eq!(ids(&t), vec!["a", "b", "c", "d"]);
        assert_eq!(t.item("c").unwrap().text, "c");
        assert!(!t.prepend(vec![item("b", "b")]));
        let (start, blocks) = t.tail(true).unwrap();
        assert_eq!((start, blocks.len()), (0, 4));
        t.apply(2, vec![Event::Upsert(item("b", "b2"))]);
        assert_eq!(ids(&t), vec!["a", "b", "c", "d"]);
    }

    #[test]
    fn an_expanded_body_survives_the_stream_resending_its_head() {
        let mut t = Transcript::default();
        let head = Item { truncated: true, body_ref: Some("r".into()), ..item("a", "head") };
        t.apply(1, vec![Event::Upsert(head.clone())]);
        assert!(!t.expand("missing", "x".into()));
        assert!(t.expand("a", "head and the rest".into()));
        assert!(!t.expand("a", "again".into()));
        t.apply(2, vec![Event::Upsert(head)]);
        assert_eq!((t.item("a").unwrap().text.as_str(), t.item("a").unwrap().truncated), ("head and the rest", false));
        t.apply(3, vec![Event::Upsert(item("a", "edited"))]);
        assert_eq!(t.item("a").unwrap().text, "edited");
        t.apply(4, vec![Event::Upsert(Item { truncated: true, ..item("a", "new head") })]);
        assert_eq!(t.item("a").unwrap().text, "new head");
    }
}
