use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

use crate::focus::is_ask;
use crate::item::Item;

pub fn is_call(item: &Item) -> bool {
    matches!(item.kind.as_str(), "toolCall" | "tool" | "command")
}

pub fn is_failure(item: &Item) -> bool {
    matches!(item.status.as_deref(), Some("failed") | Some("error"))
}

#[derive(Clone, Debug, PartialEq)]
pub enum Entry {
    Call { call: Item, results: Vec<Item> },
    Output(Item),
    Reasoning(Item),
}

impl Entry {
    pub fn id(&self) -> &str {
        match self {
            Entry::Call { call, .. } => &call.id,
            Entry::Output(item) | Entry::Reasoning(item) => &item.id,
        }
    }

    pub fn bodies(&self) -> Vec<(&'static str, &Item)> {
        let parts: Vec<(&'static str, &Item)> = match self {
            Entry::Call { call, results } => std::iter::once(("Input", call)).chain(results.iter().map(|r| ("Output", r))).collect(),
            Entry::Output(item) => vec![("Output", item)],
            Entry::Reasoning(item) => vec![("Thinking", item)],
        };
        parts.into_iter().filter(|(_, item)| !item.text.trim().is_empty() || item.truncated).collect()
    }
}

pub fn clip(text: &str, max: usize) -> (&str, bool) {
    if text.len() <= max {
        return (text, false);
    }
    let mut end = max;
    while !text.is_char_boundary(end) {
        end -= 1;
    }
    (&text[..end], true)
}

pub fn entries(items: &[Item]) -> Vec<Entry> {
    let mut out: Vec<Entry> = Vec::new();
    let mut index = std::collections::HashMap::new();
    for item in items {
        if is_call(item) {
            if let Some(call) = &item.call_id {
                index.insert(call.clone(), out.len());
            }
            out.push(Entry::Call { call: item.clone(), results: vec![] });
        } else if item.kind == "toolResult" {
            match item.call_id.as_ref().and_then(|c| index.get(c)).copied() {
                Some(at) => {
                    if let Entry::Call { results, .. } = &mut out[at] {
                        results.push(item.clone());
                    }
                }
                None => out.push(Entry::Output(item.clone())),
            }
        } else if item.kind == "reasoning" && !item.text.trim().is_empty() {
            out.push(Entry::Reasoning(item.clone()));
        }
    }
    out
}

pub const FOLD_AFTER: usize = 2;

pub fn folds(entries: &[Entry], focus: bool) -> bool {
    let calls: Vec<(&Item, &Vec<Item>)> = entries
        .iter()
        .filter_map(|e| if let Entry::Call { call, results } = e { Some((call, results)) } else { None })
        .collect();
    if calls.iter().any(|(call, results)| call.title == "AskUserQuestion" && results.is_empty()) {
        return false;
    }
    if focus {
        return !entries.is_empty() && !calls.iter().any(|(call, _)| is_ask(call));
    }
    calls.len() > FOLD_AFTER
}

fn parse(ts: &str) -> Option<OffsetDateTime> {
    OffsetDateTime::parse(ts, &Rfc3339).ok()
}

pub fn span(seconds: f64) -> String {
    let total = seconds.round() as i64;
    if total < 60 {
        return format!("{}s", total.max(1));
    }
    let minutes = total / 60;
    if minutes < 60 {
        return format!("{minutes}m");
    }
    if minutes % 60 == 0 { format!("{}h", minutes / 60) } else { format!("{}h {}m", minutes / 60, minutes % 60) }
}

fn steps_text(steps: usize) -> String {
    if steps == 1 { "1 step".into() } else { format!("{steps} steps") }
}

fn spread(items: &[Item]) -> Option<f64> {
    let dates: Vec<OffsetDateTime> = items.iter().filter_map(|i| i.timestamp.as_deref().and_then(parse)).collect();
    let first = dates.iter().min()?;
    let last = dates.iter().max()?;
    (last > first).then(|| (*last - *first).as_seconds_f64())
}

pub fn focus_line(items: &[Item]) -> String {
    let steps = items.iter().filter(|i| is_call(i)).count();
    match spread(items) {
        Some(seconds) => format!("{} of work · {}", span(seconds), steps_text(steps)),
        None if steps == 0 => "Thought".into(),
        None => steps_text(steps),
    }
}

pub fn worked(items: &[Item]) -> String {
    let steps = items.iter().filter(|i| is_call(i)).count();
    match spread(items) {
        Some(seconds) => format!("Worked for {} · {}", span(seconds), steps_text(steps)),
        None => steps_text(steps),
    }
}

struct Category {
    names: &'static [&'static str],
    singular: &'static str,
    plural: &'static str,
}

const CATEGORIES: [Category; 6] = [
    Category { names: &["Bash", "BashOutput", "exec", "exec_command", "shell", "local_shell"], singular: "ran 1 command", plural: "ran {} commands" },
    Category { names: &["Read", "read_file", "view_image", "NotebookRead"], singular: "read 1 file", plural: "read {} files" },
    Category { names: &["Edit", "MultiEdit", "Write", "NotebookEdit", "apply_patch"], singular: "edited 1 file", plural: "edited {} files" },
    Category { names: &["Grep", "Glob", "LS", "ToolSearch"], singular: "searched once", plural: "searched {} times" },
    Category { names: &["WebFetch", "WebSearch", "web_search", "Web search"], singular: "made 1 web lookup", plural: "made {} web lookups" },
    Category { names: &["Agent", "Task", "spawn_agent"], singular: "started 1 subagent", plural: "started {} subagents" },
];

pub fn mcp_parts(title: &str) -> Option<(String, String)> {
    let rest = title.strip_prefix("mcp__")?;
    let (server, tool) = rest.split_once("__")?;
    Some((server.into(), tool.into()))
}

pub fn label(items: &[Item]) -> String {
    let calls: Vec<&Item> = items.iter().filter(|i| is_call(i)).collect();
    if calls.is_empty() {
        return "Thought".into();
    }
    let mut counts = [0usize; 6];
    let mut servers: Vec<(String, usize)> = Vec::new();
    let mut other = 0;
    for call in calls {
        if let Some(at) = CATEGORIES.iter().position(|c| c.names.contains(&call.title.as_str())) {
            counts[at] += 1;
        } else if let Some((server, _)) = mcp_parts(&call.title) {
            match servers.iter_mut().find(|(s, _)| *s == server) {
                Some((_, n)) => *n += 1,
                None => servers.push((server, 1)),
            }
        } else {
            other += 1;
        }
    }
    let mut phrases: Vec<(usize, String)> = counts
        .iter()
        .enumerate()
        .filter(|(_, n)| **n > 0)
        .map(|(at, n)| (*n, if *n == 1 { CATEGORIES[at].singular.to_string() } else { CATEGORIES[at].plural.replace("{}", &n.to_string()) }))
        .collect();
    phrases.extend(servers.into_iter().map(|(server, n)| (n, if n == 1 { format!("used 1 {server} tool") } else { format!("used {n} {server} tools") })));
    let mut indexed: Vec<(usize, (usize, String))> = phrases.into_iter().enumerate().collect();
    indexed.sort_by(|a, b| b.1 .0.cmp(&a.1 .0).then(a.0.cmp(&b.0)));
    let mut phrases: Vec<(usize, String)> = indexed.into_iter().map(|(_, p)| p).collect();
    if other > 0 {
        phrases.push((other, if other == 1 { "used 1 other tool".into() } else { format!("used {other} other tools") }));
    }
    let shown: Vec<String> = phrases.iter().take(2).map(|p| p.1.clone()).collect();
    let remaining: usize = phrases.iter().skip(2).map(|p| p.0).sum();
    let mut label = shown.join(" and ");
    if remaining > 0 {
        label.push_str(&format!(" +{remaining} other"));
    }
    let mut chars = label.chars();
    match chars.next() {
        Some(first) => first.to_uppercase().collect::<String>() + chars.as_str(),
        None => label,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn item(id: &str, kind: &str, text: &str) -> Item {
        Item { id: id.into(), kind: kind.into(), text: text.into(), ..Default::default() }
    }

    #[test]
    fn an_entry_shows_its_input_then_each_output() {
        let call = Entry::Call { call: item("c", "toolCall", "ls"), results: vec![item("r1", "toolResult", "a\nb"), item("r2", "toolResult", " ")] };
        let parts: Vec<(&str, &str)> = call.bodies().iter().map(|(l, i)| (*l, i.id.as_str())).collect();
        assert_eq!(parts, vec![("Input", "c"), ("Output", "r1")]);
        let clipped = Item { truncated: true, ..item("o", "toolResult", "") };
        assert_eq!(Entry::Output(clipped).bodies().len(), 1);
        assert_eq!(Entry::Reasoning(item("t", "reasoning", "hm")).bodies()[0].0, "Thinking");
    }

    #[test]
    fn clipping_keeps_whole_characters() {
        assert_eq!(clip("short", 10), ("short", false));
        assert_eq!(clip("héllo", 2), ("h", true));
        assert_eq!(clip("héllo", 3), ("hé", true));
    }
}
