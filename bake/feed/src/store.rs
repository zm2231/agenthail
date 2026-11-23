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
}

fn is_prompt(item: &Item) -> bool {
    starts_turn(item) && item.agent.is_none()
}

impl Transcript {
    pub fn items(&self) -> &[Item] {
        &self.items
    }

    fn touch(&mut self, at: usize) {
        self.dirty = Some(self.dirty.map_or(at, |d| d.min(at)));
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
