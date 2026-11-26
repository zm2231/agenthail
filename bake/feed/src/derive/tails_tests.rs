use std::collections::HashSet;
use std::time::Instant;

use serde_json::json;

use super::live::{Fetched, Overview};
use super::tails::{self, TAIL_ITEMS, Tails};
use super::*;
use crate::Item;
use crate::catalog::{Session, parse_time};

fn session(id: &str, last_active: &str) -> Session {
    serde_json::from_value(json!({"id": id, "name": id, "lastActive": last_active, "project": {"id": "p", "name": "P"}})).unwrap()
}

fn said(id: &str, at: &str, text: &str) -> Item {
    serde_json::from_value(json!({"id": id, "kind": "message", "role": "user", "text": text, "timestamp": at})).unwrap()
}

fn frame(v: serde_json::Value) -> Vec<String> {
    vec![format!("data: {v}"), String::new()]
}

#[test]
fn a_stream_tail_rebuilds_items_with_appends_and_removals() {
    let mut lines = vec![": hello".to_string(), String::new()];
    lines.extend(frame(json!({"seq": 1, "data": {"op": "upsert", "itemId": "a"}, "item": {"id": "a", "kind": "message", "role": "assistant", "text": "Hel"}})));
    lines.extend(frame(json!({"seq": 2, "type": "append", "itemId": "a", "from": 3, "text": "lo"})));
    lines.extend(frame(json!({"seq": 3, "data": {"op": "upsert", "itemId": "b"}, "item": {"id": "b", "kind": "message", "role": "user", "text": "gone"}})));
    lines.extend(frame(json!({"seq": 4, "data": {"op": "remove", "itemId": "b"}})));
    let items = tails::read(lines.into_iter());
    assert_eq!(items.iter().map(|i| (i.id.as_str(), i.text.as_str())).collect::<Vec<_>>(), [("a", "Hello")]);
}

#[test]
fn a_tail_keeps_the_last_items_and_shortens_long_tool_output() {
    let mut items: Vec<Item> = (0..TAIL_ITEMS + 20).map(|i| said(&format!("m{i}"), "2026-11-24T11:00:00Z", "hi")).collect();
    let last = items.len() - 1;
    items[last].kind = "toolResult".into();
    items[last].text = "é".repeat(2000);
    let tail = tails::trim(&items);
    assert_eq!(tail.len(), TAIL_ITEMS);
    assert_eq!(tail[0].id, "m20");
    assert!(tail[TAIL_ITEMS - 1].text.len() <= 1200);
}

#[test]
fn only_recent_sessions_without_a_pane_are_fetched_and_again_only_when_they_move() {
    let now = parse_time("2026-11-24T12:00:00Z").unwrap();
    let sessions = vec![session("fresh", "2026-11-24T11:50:00Z"), session("open", "2026-11-24T11:55:00Z"), session("old", "2026-11-20T11:00:00Z")];
    let open: HashSet<String> = ["open".to_string()].into();
    let mut tails = Tails::default();
    let due = tails.due(&sessions, now, &open);
    assert_eq!(due, [("fresh".to_string(), "2026-11-24T11:50:00Z".to_string())]);
    assert!(tails.due(&sessions, now, &open).is_empty(), "already being fetched");
    tails.take("fresh", "2026-11-24T11:50:00Z".into(), Ok(vec![said("x", "2026-11-24T11:40:00Z", "go")]));
    assert!(tails.due(&sessions, now, &open).is_empty(), "nothing moved");
    let moved = vec![session("fresh", "2026-11-24T11:58:00Z")];
    assert_eq!(tails.due(&moved, now, &open).len(), 1);
}

#[test]
fn a_failed_or_empty_tail_is_retried_on_the_next_refresh() {
    let now = parse_time("2026-11-24T12:00:00Z").unwrap();
    let sessions = vec![session("a", "2026-11-24T11:50:00Z")];
    let mut tails = Tails::default();
    assert_eq!(tails.due(&sessions, now, &HashSet::new()).len(), 1);
    tails.take("a", "2026-11-24T11:50:00Z".into(), Err("timed out".into()));
    assert_eq!(tails.due(&sessions, now, &HashSet::new()).len(), 1);
    tails.take("a", "2026-11-24T11:50:00Z".into(), Ok(Vec::new()));
    assert_eq!(tails.due(&sessions, now, &HashSet::new()).len(), 1, "a read cut off before any item is not kept");
}

#[test]
fn hands_on_comes_from_fetched_tails_and_loaded_panes() {
    let at = "2026-11-24T12:00:00Z";
    let recorded: Recorded = serde_json::from_value(json!({"updatedAt": at, "projects": [{"id": "p", "displayName": "P"}]})).unwrap();
    let mut ov = Overview::default();
    ov.refresh(Fetched { recorded: Recorded { sessions: vec![session("fetched", "2026-11-24T11:58:00Z"), session("paned", "2026-11-24T11:59:00Z")], ..recorded }, ledgers: Default::default(), started: Instant::now() });
    assert!(ov.world(&[]).hands_on(&[]).is_empty());
    let open: HashSet<String> = ["paned".to_string()].into();
    let due = ov.due_tails(&open);
    assert_eq!(due.len(), 1);
    ov.take_tail("fetched", due[0].1.clone(), Ok(vec![said("1", "2026-11-24T10:00:00Z", "start"), said("2", "2026-11-24T11:50:00Z", "try again")]));
    let pane = [said("1", "2026-11-24T10:00:00Z", "start"), said("2", "2026-11-24T11:55:00Z", "and this")];
    let w = ov.world(&[("paned", &pane)]);
    let mut ids: Vec<&str> = w.hands_on(&[]).iter().map(|s| s.id.as_str()).collect();
    ids.sort();
    assert_eq!(ids, ["fetched", "paned"]);
}
