use std::collections::HashSet;

use crate::item::Item;

pub const WORK_KINDS: [&str; 5] = ["toolCall", "toolResult", "reasoning", "tool", "command"];

pub fn is_work(kind: &str) -> bool {
    WORK_KINDS.contains(&kind)
}

pub fn starts_turn(item: &Item) -> bool {
    if item.agent.as_ref().is_some_and(|a| a.is_incoming()) {
        return true;
    }
    item.role_is("user") && ["message", "text", "file", "attachment"].contains(&item.kind.as_str()) && !item.is_instructions()
}

pub fn is_end_turn(tool: &str) -> bool {
    tool == "agenthail_end_turn" || (tool.starts_with("mcp__") && tool.ends_with("__agenthail_end_turn"))
}

pub fn is_ask(item: &Item) -> bool {
    item.kind == "toolCall"
        && (["AskUserQuestion", "request_user_input", "agenthail_ask"].contains(&item.title.as_str()) || item.title.ends_with("__agenthail_ask"))
}

pub fn shown(items: &[Item]) -> Vec<Item> {
    let hidden: HashSet<&str> = items
        .iter()
        .filter(|i| i.kind == "toolCall" && is_end_turn(&i.title))
        .filter_map(|i| i.call_id.as_deref())
        .collect();
    items
        .iter()
        .filter(|i| !(i.kind == "toolCall" && is_end_turn(&i.title)))
        .filter(|i| !(i.kind == "toolResult" && i.call_id.as_deref().is_some_and(|c| hidden.contains(c))))
        .cloned()
        .collect()
}

pub fn focused(items: &[Item]) -> Vec<Item> {
    let mut out: Vec<Item> = Vec::new();
    let mut work: Vec<Item> = Vec::new();
    let mut reply: Option<usize> = None;
    let mut kept: HashSet<String> = HashSet::new();
    fn end_turn(out: &mut Vec<Item>, work: &mut Vec<Item>, reply: &mut Option<usize>) {
        match reply.take() {
            Some(at) => {
                let tail = out.split_off(at);
                out.append(work);
                out.extend(tail);
            }
            None => out.append(work),
        }
    }
    for item in items {
        if item.kind == "notice" || (item.kind == "event" && item.title != "Turn duration") {
            continue;
        }
        if starts_turn(item) {
            end_turn(&mut out, &mut work, &mut reply);
            out.push(item.clone());
        } else if is_ask(item) || item.agent.is_some() {
            if let Some(call) = &item.call_id {
                kept.insert(call.clone());
            }
            out.push(item.clone());
        } else if item.agent.is_none()
            && is_work(&item.kind)
            && !(item.kind == "toolResult" && item.call_id.as_ref().is_some_and(|c| kept.contains(c)))
        {
            work.push(item.clone());
        } else {
            if item.role_is("assistant") && (item.kind == "message" || item.kind == "text") {
                reply = Some(out.len());
            }
            out.push(item.clone());
        }
    }
    end_turn(&mut out, &mut work, &mut reply);
    out
}

#[derive(Default)]
pub struct AskRuns {
    calls: HashSet<String>,
    last_was_ask: Option<bool>,
}

impl AskRuns {
    pub fn breaks(&mut self, item: &Item) -> bool {
        let ask = is_ask(item) || (item.kind == "toolResult" && item.call_id.as_ref().is_some_and(|c| self.calls.contains(c)));
        if is_ask(item) {
            if let Some(call) = &item.call_id {
                self.calls.insert(call.clone());
            }
        }
        let breaks = self.last_was_ask.is_some_and(|last| last != ask);
        self.last_was_ask = Some(ask);
        breaks
    }
}
