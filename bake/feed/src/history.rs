use std::collections::HashSet;

use serde::Deserialize;
use serde_json::Value;

use crate::catalog::{Client, enc};
use crate::item::Item;

pub const PAGE_EXCHANGES: usize = 50;
pub const WALK_PAGES: usize = 8;
pub const BODY_CAP: usize = 8 << 20;

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Page {
    pub items: Vec<Item>,
    pub next_before: i64,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Miss {
    Gone,
    Failed(String),
}

impl Miss {
    pub fn note(&self) -> String {
        match self {
            Miss::Gone => "Older history is no longer kept".into(),
            Miss::Failed(error) => format!("Couldn't load earlier messages: {error}"),
        }
    }
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Older {
    pub items: Vec<Item>,
    pub next: i64,
    pub anchored: bool,
}

#[derive(Deserialize, Default)]
#[serde(rename_all = "camelCase")]
struct Timeline {
    #[serde(default)]
    next_before: i64,
    #[serde(default)]
    items: Vec<Item>,
    #[serde(default)]
    unavailable_reason: Option<String>,
}

fn read_page(value: &Value) -> Result<Page, Miss> {
    if let Some(error) = value["readError"].as_str().filter(|e| !e.is_empty()) {
        return Err(Miss::Failed(error.to_string()));
    }
    if value["catchingUp"].as_bool() == Some(true) {
        return Err(Miss::Failed("the session is still catching up".into()));
    }
    let timeline: Timeline = serde_json::from_value(value["timeline"].clone()).map_err(|e| Miss::Failed(e.to_string()))?;
    if let Some(reason) = timeline.unavailable_reason.filter(|r| !r.is_empty()) {
        return Err(Miss::Failed(reason));
    }
    Ok(Page { items: timeline.items, next_before: timeline.next_before.max(0) })
}

pub fn walk(mut fetch: impl FnMut(i64) -> Result<Page, Miss>, from: i64, oldest: Option<&str>, known: &HashSet<String>) -> Result<Older, Miss> {
    let fresh = |items: Vec<Item>| -> Vec<Item> { items.into_iter().filter(|i| !known.contains(&i.id)).collect() };
    let Some(oldest) = oldest else {
        let page = fetch(from)?;
        return Ok(Older { items: fresh(page.items), next: page.next_before, anchored: true });
    };
    let mut cursor = from;
    for _ in 0..WALK_PAGES {
        let mut page = fetch(cursor)?;
        if let Some(at) = page.items.iter().position(|i| i.id == oldest) {
            page.items.truncate(at);
            return Ok(Older { items: fresh(page.items), next: page.next_before, anchored: true });
        }
        cursor = page.next_before;
        if cursor == 0 {
            break;
        }
    }
    Ok(Older { items: Vec::new(), next: cursor, anchored: false })
}

fn refusal(code: u16, body: &str) -> String {
    if code == 0 {
        return body.to_string();
    }
    let value: Value = serde_json::from_str(body).unwrap_or_default();
    value["error"]["message"].as_str().map(String::from).unwrap_or_else(|| if body.trim().is_empty() { format!("status {code}") } else { body.trim().to_string() })
}

fn at_start(code: u16, body: &str) -> bool {
    let value: Value = serde_json::from_str(body).unwrap_or_default();
    code == 409 && value["error"]["code"] == "history_gap" && value["earliestPosition"].as_i64().is_some_and(|p| p <= 1)
}

fn page_miss(code: u16, body: &str) -> Miss {
    let value: Value = serde_json::from_str(body).unwrap_or_default();
    if code == 409 && value["error"]["code"] == "history_gap" {
        return Miss::Gone;
    }
    Miss::Failed(refusal(code, body))
}

impl Client {
    pub fn session_page(&self, session: &str, before: i64) -> Result<Page, Miss> {
        let mut path = format!("/api/v1/session?id={}&timeline=1&limit={PAGE_EXCHANGES}", enc(session));
        if before > 0 {
            path.push_str(&format!("&timelineBefore={before}"));
        }
        match self.get_reply(&path) {
            Ok(value) => read_page(&value),
            Err((code, body)) if at_start(code, &body) => Ok(Page::default()),
            Err((code, body)) => Err(page_miss(code, &body)),
        }
    }

    pub fn older(&self, session: &str, from: i64, oldest: Option<&str>, known: &HashSet<String>) -> Result<Older, Miss> {
        walk(|before| self.session_page(session, before), from, oldest, known)
    }

    pub fn full_body(&self, session: &str, body_ref: &str) -> Result<String, String> {
        let read = || {
            collect_body(
                |start| {
                    let path = format!("/api/v1/session-stream-body?id={}&ref={}&start={start}", enc(session), enc(body_ref));
                    let value = self.get_reply(&path).map_err(|(code, text)| refusal(code, &text))?;
                    let (end, total) = (value["end"].as_u64().map_or(start, |e| e as usize), value["total"].as_u64().unwrap_or_default() as usize);
                    Ok(Chunk { text: value["body"].as_str().unwrap_or_default().to_string(), end, total, more: value["truncated"].as_bool().unwrap_or(false) })
                },
                BODY_CAP,
            )
        };
        settled(read)
    }
}

pub fn settled(mut read: impl FnMut() -> Result<String, String>) -> Result<String, String> {
    let mut last = read()?;
    for _ in 0..2 {
        let again = read()?;
        if again == last {
            return Ok(again);
        }
        last = again;
    }
    Err("it changed while loading; try again".into())
}

pub struct Chunk {
    pub text: String,
    pub end: usize,
    pub total: usize,
    pub more: bool,
}

pub fn collect_body(mut fetch: impl FnMut(usize) -> Result<Chunk, String>, cap: usize) -> Result<String, String> {
    let (mut body, mut start, mut size) = (String::new(), 0, None);
    loop {
        let Chunk { text, end, total, more } = fetch(start)?;
        if *size.get_or_insert(total) != total {
            return Err("it changed while loading; try again".into());
        }
        body.push_str(&text);
        if !more {
            return if body.len() == total { Ok(body) } else { Err("it changed while loading; try again".into()) };
        }
        if end <= start {
            return Err("the daemon stopped returning more of it".into());
        }
        if end > cap {
            return Err("it is larger than the daemon keeps".into());
        }
        start = end;
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};

    fn item(id: &str) -> Item {
        Item { id: id.into(), kind: "message".into(), role: Some("user".into()), text: id.into(), ..Default::default() }
    }

    fn known(ids: &[&str]) -> HashSet<String> {
        ids.iter().map(|s| s.to_string()).collect()
    }

    fn pages(list: Vec<(i64, Vec<&'static str>, i64)>) -> impl FnMut(i64) -> Result<Page, Miss> {
        move |before| {
            let (_, ids, next) = list.iter().find(|(at, _, _)| *at == before).cloned().unwrap();
            Ok(Page { items: ids.iter().map(|id| item(id)).collect(), next_before: next })
        }
    }

    fn ids(older: &Older) -> Vec<&str> {
        older.items.iter().map(|i| i.id.as_str()).collect()
    }

    #[test]
    fn the_first_walk_takes_only_rows_before_the_oldest_held_ones() {
        let history = vec![(0, vec!["x", "y"], 90), (90, vec!["a", "b", "c", "d"], 40), (40, vec!["older"], 0)];
        let older = walk(pages(history.clone()), 0, Some("c"), &known(&["c", "d", "x"])).unwrap();
        assert_eq!((ids(&older), older.next, older.anchored), (vec!["a", "b"], 40, true));
        let after = walk(pages(history), 40, None, &known(&["a", "b", "c"])).unwrap();
        assert_eq!((ids(&after), after.next), (vec!["older"], 0));
        let split = vec![(0, vec!["x", "c"], 50), (50, vec!["p", "a", "b"], 0)];
        let across = walk(pages(split), 0, Some("a"), &known(&["a", "c"])).unwrap();
        assert_eq!((ids(&across), across.next), (vec!["p"], 0));
    }

    #[test]
    fn newer_rows_a_stalled_replay_has_not_reached_are_never_put_in_front() {
        let history = vec![(0, vec!["n1", "n2"], 70), (70, vec!["m1", "a"], 0)];
        let older = walk(pages(history), 0, Some("a"), &known(&["a"])).unwrap();
        assert_eq!((ids(&older), older.next), (vec!["m1"], 0));
    }

    #[test]
    fn a_walk_that_never_meets_the_held_rows_places_nothing() {
        let unmet = walk(pages(vec![(0, vec!["n"], 0)]), 0, Some("a"), &known(&["a"])).unwrap();
        assert_eq!(unmet, Older { items: vec![], next: 0, anchored: false });
        let mut calls = 0;
        let far = walk(
            |_| {
                calls += 1;
                Ok(Page { items: vec![item("n")], next_before: 7 })
            },
            0,
            Some("a"),
            &known(&["a"]),
        )
        .unwrap();
        assert_eq!((calls, far.next, far.items.len(), far.anchored), (WALK_PAGES, 7, 0, false));
        let empty = walk(pages(vec![(0, vec!["p", "q"], 5)]), 0, None, &known(&[])).unwrap();
        assert_eq!((ids(&empty), empty.next, empty.anchored), (vec!["p", "q"], 5, true));
        assert_eq!(walk(|_| Err(Miss::Gone), 5, None, &known(&[])), Err(Miss::Gone));
    }

    #[test]
    fn a_page_is_read_from_the_session_endpoint() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"timeline":{"nextBefore":12,"items":[{"id":"a","kind":"message","role":"assistant","text":"hi","truncated":true,"bodyRef":"body-a"}]}}"#),
            (409, r#"{"earliestPosition":40,"error":{"code":"history_gap","message":"gone"}}"#),
            (409, r#"{"earliestPosition":1,"latestPosition":707,"error":{"code":"history_gap","message":"gone"}}"#),
            (200, r#"{"timeline":{"nextBefore":0,"items":[],"unavailableReason":"transcript unavailable"}}"#),
            (500, r#"{"error":{"code":"registry_error","message":"disk"}}"#),
            (200, r#"{"readError":"Session journal could not be read.","timeline":{"nextBefore":0,"items":[]}}"#),
            (200, r#"{"catchingUp":true,"timeline":{"nextBefore":0,"items":[]}}"#),
        ]));
        let page = client.session_page("s 1", 0).unwrap();
        assert_eq!(seen.recv().unwrap().0, "/api/v1/session?id=s+1&timeline=1&limit=50");
        assert_eq!((page.next_before, page.items[0].body_ref.as_deref(), page.items[0].truncated), (12, Some("body-a"), true));
        assert_eq!(client.session_page("s1", 12), Err(Miss::Gone));
        assert_eq!(seen.recv().unwrap().0, "/api/v1/session?id=s1&timeline=1&limit=50&timelineBefore=12");
        assert_eq!(client.session_page("s1", 1), Ok(Page::default()));
        assert_eq!(client.session_page("s1", 3), Err(Miss::Failed("transcript unavailable".into())));
        assert_eq!(client.session_page("s1", 3), Err(Miss::Failed("disk".into())));
        assert_eq!(client.session_page("s1", 3), Err(Miss::Failed("Session journal could not be read.".into())));
        assert_eq!(client.session_page("s1", 0), Err(Miss::Failed("the session is still catching up".into())));
    }

    #[test]
    fn a_long_body_is_read_in_pieces_at_the_ranges_the_daemon_returns() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"body":"��héllo","end":12,"total":21,"truncated":true}"#),
            (200, r#"{"body":" — done","end":21,"total":21,"truncated":false}"#),
            (200, r#"{"body":"��héllo","end":12,"total":21,"truncated":true}"#),
            (200, r#"{"body":" — done","end":21,"total":21,"truncated":false}"#),
        ]));
        assert_eq!(client.full_body("s1", "body/a").unwrap(), "\u{FFFD}\u{FFFD}héllo — done");
        assert_eq!(seen.recv().unwrap().0, "/api/v1/session-stream-body?id=s1&ref=body%2Fa&start=0");
        assert_eq!(seen.recv().unwrap().0, "/api/v1/session-stream-body?id=s1&ref=body%2Fa&start=12");
        let (client, _seen) = daemon(replies(&[(404, r#"{"error":{"code":"body_unavailable","message":"The retained body is unavailable."}}"#)]));
        assert_eq!(client.full_body("s1", "x").unwrap_err(), "The retained body is unavailable.");
    }

    #[test]
    fn a_body_stops_at_the_cap_the_daemon_keeps() {
        assert_eq!(BODY_CAP, 8 << 20);
        let mut starts = Vec::new();
        let chunk = |text: &str, end: usize, total: usize, more: bool| Ok(Chunk { text: text.into(), end, total, more });
        let read = collect_body(
            |start| {
                starts.push(start);
                chunk("abcd", start + 4, 12, start < 8)
            },
            16,
        );
        assert_eq!((read.unwrap(), starts), ("abcdabcdabcd".to_string(), vec![0, 4, 8]));
        assert!(collect_body(|start| chunk("abcdef", start + 6, 30, true), 16).unwrap_err().contains("larger"));
        assert!(collect_body(|start| chunk("", start, 9, true), 16).unwrap_err().contains("stopped"));
    }

    #[test]
    fn a_body_counts_only_once_two_reads_agree() {
        let mut reads = vec!["v2", "v2", "v1"].into_iter();
        assert_eq!(settled(|| Ok(reads.next().unwrap().to_string())).unwrap(), "v2");
        let mut moving = 0;
        assert!(settled(|| {
            moving += 1;
            Ok(moving.to_string())
        })
        .unwrap_err()
        .contains("changed"));
        let mut flips = vec!["a", "b", "b"].into_iter();
        assert_eq!(settled(|| Ok(flips.next().unwrap().to_string())).unwrap(), "b");
    }

    #[test]
    fn a_body_replaced_between_pieces_is_not_stitched_together() {
        let resized = collect_body(|start| Ok(Chunk { text: "ab".into(), end: start + 2, total: if start == 0 { 4 } else { 6 }, more: start == 0 }), 16);
        assert!(resized.unwrap_err().contains("changed"));
        let short = collect_body(|start| Ok(Chunk { text: "ab".into(), end: start + 2, total: 5, more: start == 0 }), 16);
        assert!(short.unwrap_err().contains("changed"));
    }
}
