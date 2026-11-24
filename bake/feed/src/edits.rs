use serde_json::Value;

use crate::item::Item;

#[derive(Clone, Debug, PartialEq)]
pub struct Edit {
    pub path: String,
    pub old: String,
    pub new: String,
    pub item: String,
    pub turn: Option<String>,
    pub created: bool,
}

fn str_field<'a>(v: &'a Value, keys: &[&str]) -> Option<&'a str> {
    keys.iter().find_map(|k| v.get(*k).and_then(Value::as_str))
}

fn apply_patch(text: &str, item: &Item, out: &mut Vec<Edit>) {
    let mut current: Option<(String, String, String, bool)> = None;
    let flush = |current: &mut Option<(String, String, String, bool)>, out: &mut Vec<Edit>| {
        if let Some((path, old, new, created)) = current.take() {
            out.push(Edit { path, old, new, item: item.id.clone(), turn: item.turn_id.clone(), created });
        }
    };
    for line in text.lines() {
        let header = ["*** Update File: ", "*** Add File: ", "*** Delete File: "].iter().find_map(|h| line.strip_prefix(h).map(|p| (p.trim().to_string(), *h)));
        if let Some((path, kind)) = header {
            flush(&mut current, out);
            current = Some((path, String::new(), String::new(), kind.starts_with("*** Add")));
            continue;
        }
        let Some((_, old, new, _)) = current.as_mut() else { continue };
        if line.starts_with("***") || line.starts_with("@@") {
            continue;
        }
        if let Some(rest) = line.strip_prefix('+') {
            new.push_str(rest);
            new.push('\n');
        } else if let Some(rest) = line.strip_prefix('-') {
            old.push_str(rest);
            old.push('\n');
        } else {
            let rest = line.strip_prefix(' ').unwrap_or(line);
            old.push_str(rest);
            old.push('\n');
            new.push_str(rest);
            new.push('\n');
        }
    }
    flush(&mut current, out);
}

pub fn edits(items: &[Item]) -> Vec<Edit> {
    let mut out = Vec::new();
    for item in items.iter().filter(|i| i.kind == "toolCall") {
        let title = item.title.as_str();
        if title == "apply_patch" || item.text.contains("*** Begin Patch") {
            let patch = serde_json::from_str::<Value>(&item.text).ok().and_then(|v| str_field(&v, &["input", "patch", "command"]).map(String::from)).unwrap_or_else(|| item.text.clone());
            apply_patch(&patch, item, &mut out);
            continue;
        }
        if !matches!(title, "Edit" | "Write" | "MultiEdit" | "str_replace_based_edit_tool" | "NotebookEdit") {
            continue;
        }
        let Ok(v) = serde_json::from_str::<Value>(&item.text) else { continue };
        let Some(path) = str_field(&v, &["file_path", "path", "notebook_path"]) else { continue };
        let push = |out: &mut Vec<Edit>, old: &str, new: &str, created: bool| {
            out.push(Edit { path: path.to_string(), old: old.to_string(), new: new.to_string(), item: item.id.clone(), turn: item.turn_id.clone(), created });
        };
        match title {
            "Write" => push(&mut out, "", str_field(&v, &["content"]).unwrap_or(""), true),
            "MultiEdit" => {
                for e in v.get("edits").and_then(Value::as_array).into_iter().flatten() {
                    push(&mut out, str_field(e, &["old_string"]).unwrap_or(""), str_field(e, &["new_string"]).unwrap_or(""), false);
                }
            }
            _ => push(&mut out, str_field(&v, &["old_string", "old_str"]).unwrap_or(""), str_field(&v, &["new_string", "new_str", "new_source"]).unwrap_or(""), false),
        }
    }
    out
}
