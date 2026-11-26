use std::collections::HashSet;

use serde::Deserialize;
use serde_json::Value;

use crate::catalog::{Client, enc};
use crate::item::Item;

pub const PAGE_EXCHANGES: usize = 50;
pub const WALK_PAGES: usize = 8;
pub const BODY_CAP: usize = 4 << 20;

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
    let timeline: Timeline = serde_json::from_value(value["timeline"].clone()).map_err(|e| Miss::Failed(e.to_string()))?;
    if let Some(reason) = timeline.unavailable_reason.filter(|r| !r.is_empty()) {
        return Err(Miss::Failed(reason));
    }
    Ok(Page { items: timeline.items, next_before: timeline.next_before.max(0) })
}

pub fn walk(mut fetch: impl FnMut(i64) -> Result<Page, Miss>, from: i64, known: &HashSet<String>) -> Result<Older, Miss> {
    let mut cursor = from;
    for _ in 0..WALK_PAGES {
        let page = fetch(cursor)?;
        let end = page.items.iter().position(|i| known.contains(&i.id)).unwrap_or(page.items.len());
        let items: Vec<Item> = page.items.into_iter().take(end).collect();
        cursor = page.next_before;
        if !items.is_empty() || cursor == 0 {
            return Ok(Older { items, next: cursor });
        }
    }
    Ok(Older { items: Vec::new(), next: cursor })
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

    pub fn older(&self, session: &str, from: i64, known: &HashSet<String>) -> Result<Older, Miss> {
        walk(|before| self.session_page(session, before), from, known)
    }

    pub fn full_body(&self, session: &str, body_ref: &str) -> Result<String, String> {
        let mut body = String::new();
        loop {
            let path = format!("/api/v1/session-stream-body?id={}&ref={}&start={}", enc(session), enc(body_ref), body.len());
            let value = self.get_reply(&path).map_err(|(code, text)| refusal(code, &text))?;
            let chunk = value["body"].as_str().unwrap_or_default();
            if !value["truncated"].as_bool().unwrap_or(false) {
                body.push_str(chunk);
                return Ok(body);
            }
            let whole = chunk.trim_end_matches('\u{FFFD}');
            if whole.is_empty() || body.len() + whole.len() > BODY_CAP {
                return Err("the full message is too large to show here".into());
            }
            body.push_str(whole);
        }
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

    #[test]
    fn walking_back_skips_pages_already_held_and_keeps_only_older_rows() {
        let pages = vec![
            (0, Page { items: vec![item("c"), item("d")], next_before: 90 }),
            (90, Page { items: vec![item("a"), item("b"), item("c")], next_before: 40 }),
        ];
        let mut asked = Vec::new();
        let older = walk(
            |before| {
                asked.push(before);
                Ok(pages.iter().find(|(at, _)| *at == before).unwrap().1.clone())
            },
            0,
            &known(&["c", "d"]),
        )
        .unwrap();
        assert_eq!(asked, vec![0, 90]);
        assert_eq!(older.items.iter().map(|i| i.id.as_str()).collect::<Vec<_>>(), vec!["a", "b"]);
        assert_eq!(older.next, 40);
    }

    #[test]
    fn the_walk_stops_at_the_start_and_after_a_bounded_number_of_pages() {
        let start = walk(|_| Ok(Page { items: vec![item("a")], next_before: 0 }), 0, &known(&["a"])).unwrap();
        assert_eq!(start, Older { items: vec![], next: 0 });
        let mut calls = 0;
        let held = walk(
            |_| {
                calls += 1;
                Ok(Page { items: vec![item("a")], next_before: 7 })
            },
            0,
            &known(&["a"]),
        )
        .unwrap();
        assert_eq!((calls, held.next, held.items.len()), (WALK_PAGES, 7, 0));
        assert_eq!(walk(|_| Err(Miss::Gone), 5, &known(&[])), Err(Miss::Gone));
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
    }

    #[test]
    fn a_long_body_is_read_in_pieces_without_splitting_a_character() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"body":"héllo wor��","end":12,"total":20,"truncated":true}"#),
            (200, r#"{"body":"ld — done","end":20,"total":20,"truncated":false}"#),
        ]));
        assert_eq!(client.full_body("s1", "body/a").unwrap(), "héllo world — done");
        assert_eq!(seen.recv().unwrap().0, "/api/v1/session-stream-body?id=s1&ref=body%2Fa&start=0");
        assert_eq!(seen.recv().unwrap().0, "/api/v1/session-stream-body?id=s1&ref=body%2Fa&start=10");
        let (client, _seen) = daemon(replies(&[(404, r#"{"error":{"code":"body_unavailable","message":"The retained body is unavailable."}}"#)]));
        assert_eq!(client.full_body("s1", "x").unwrap_err(), "The retained body is unavailable.");
    }
}
