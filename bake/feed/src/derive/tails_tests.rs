use std::collections::HashSet;
use std::io::{BufRead, BufReader, Write};
use std::net::TcpListener;
use std::time::{Duration, Instant};

use serde_json::{Value, json};

use super::live::{Fetched, Overview};
use super::tails::{self, IN_FLIGHT, TAIL_ITEMS, Tail, Tails};
use super::*;
use crate::Item;
use crate::catalog::{Client, Session, parse_time};

fn session(id: &str, last_active: &str) -> Session {
    serde_json::from_value(json!({"id": id, "name": id, "lastActive": last_active, "project": {"id": "p", "name": "P"}})).unwrap()
}

fn said_row(id: &str, at: &str, text: &str) -> Value {
    json!({"id": id, "kind": "message", "role": "user", "text": text, "timestamp": at})
}

fn reply_row(id: &str) -> Value {
    json!({"id": id, "kind": "message", "role": "assistant", "text": "ok", "timestamp": "2026-11-24T10:30:00Z"})
}

fn said(id: &str, at: &str, text: &str) -> Item {
    serde_json::from_value(said_row(id, at, text)).unwrap()
}

fn frame(v: serde_json::Value) -> Vec<String> {
    vec![format!("data: {v}"), String::new()]
}

fn upsert(seq: u64, row: Value) -> Vec<String> {
    frame(json!({"seq": seq, "data": {"op": "upsert", "itemId": row["id"]}, "item": row}))
}

fn lines(v: Vec<String>) -> impl Iterator<Item = std::io::Result<String>> {
    v.into_iter().map(Ok)
}

fn long_conversation_steered_at(at: &str) -> Vec<Value> {
    let mut rows = vec![said_row("start", "2026-11-24T10:00:00Z", "build it")];
    rows.extend((0..TAIL_ITEMS + 10).map(|i| reply_row(&format!("r{i}"))));
    rows.push(said_row("steer", at, "use the other table"));
    rows
}

fn tail(items: Vec<Item>) -> Tail {
    Tail { items, prompted_before: false }
}

fn overview_with(sessions: Vec<Session>) -> Overview {
    let recorded: Recorded = serde_json::from_value(json!({"updatedAt": "2026-11-24T12:00:00Z", "projects": [{"id": "p", "displayName": "P"}]})).unwrap();
    let mut ov = Overview::default();
    ov.refresh(Fetched { recorded: Recorded { sessions, ..recorded }, ledgers: Default::default(), held: Vec::new(), asked: Vec::new(), started: Instant::now(), usage: None, moved: Vec::new() });
    ov
}

fn hands_on_ids(w: &World) -> Vec<String> {
    let mut ids: Vec<String> = w.hands_on(&[]).iter().map(|s| s.id.clone()).collect();
    ids.sort();
    ids
}

#[test]
fn a_stream_tail_rebuilds_items_with_appends_and_removals() {
    let mut v = vec![": connected".to_string(), String::new()];
    v.extend(frame(json!({"seq": 1, "data": {"op": "upsert", "itemId": "a"}, "item": {"id": "a", "kind": "message", "role": "assistant", "text": "Hel"}})));
    v.extend(frame(json!({"seq": 2, "type": "append", "itemId": "a", "from": 3, "text": "lo"})));
    v.extend(frame(json!({"seq": 3, "data": {"op": "upsert", "itemId": "b"}, "item": {"id": "b", "kind": "message", "role": "user", "text": "gone"}})));
    v.extend(frame(json!({"seq": 4, "data": {"op": "remove", "itemId": "b"}})));
    v.push(": live".into());
    let tail = tails::read(lines(v)).unwrap();
    assert_eq!(tail.items.iter().map(|i| (i.id.as_str(), i.text.as_str())).collect::<Vec<_>>(), [("a", "Hello")]);
}

#[test]
fn a_replay_cut_off_before_the_live_point_is_not_kept_and_is_read_again_later() {
    let now = parse_time("2026-11-24T12:00:00Z").unwrap();
    let sessions = vec![session("a", "2026-11-24T11:50:00Z")];
    let prefix = upsert(1, said_row("1", "2026-11-24T10:00:00Z", "start"));
    let ended = tails::read(lines(prefix.clone()));
    assert!(ended.is_err(), "a stream that closes partway is not a tail");
    let broken = tails::read(lines(prefix).chain([Err(std::io::Error::new(std::io::ErrorKind::TimedOut, "timed out"))]));
    assert!(broken.is_err(), "a read that times out partway is not a tail");
    let mut tails = Tails::default();
    assert_eq!(tails.due(&sessions, now, &HashSet::new()).len(), 1);
    tails.take("a", "2026-11-24T11:50:00Z".into(), broken);
    assert!(tails.due(&sessions, now, &HashSet::new()).is_empty(), "a failed read waits before trying again");
    let moved = vec![session("a", "2026-11-24T11:55:00Z")];
    assert!(tails.due(&moved, now, &HashSet::new()).is_empty(), "new activity doesn't cut the wait after a failure short");
    assert!(tails.due(&[], now, &HashSet::new()).is_empty());
    assert!(tails.due(&moved, now, &HashSet::new()).is_empty(), "dropping out of the recent sessions and back doesn't cut it short either");
    tails.failed_until_now("a");
    assert_eq!(tails.due(&moved, now, &HashSet::new()).len(), 1, "once the wait is over the session is read again");
    tails.take("a", "2026-11-24T11:55:00Z".into(), tails::read(lines(vec![": live".into()])));
    let sessions = moved;
    assert!(tails.due(&sessions, now, &HashSet::new()).is_empty(), "a complete tail is kept, even an empty one");
}

#[test]
fn a_tail_keeps_the_last_items_and_shortens_long_tool_output() {
    let mut items: Vec<Item> = (0..TAIL_ITEMS + 20).map(|i| said(&format!("m{i}"), "2026-11-24T11:00:00Z", "hi")).collect();
    let last = items.len() - 1;
    items[last].kind = "toolResult".into();
    items[last].text = "é".repeat(2000);
    let tail = tails::trim(&items);
    assert_eq!(tail.items.len(), TAIL_ITEMS);
    assert_eq!(tail.items[0].id, "m20");
    assert!(tail.items[TAIL_ITEMS - 1].text.len() <= 1200);
    assert!(tail.prompted_before);
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
    tails.take("fresh", "2026-11-24T11:50:00Z".into(), Ok(tail(vec![said("x", "2026-11-24T11:40:00Z", "go")])));
    assert!(tails.due(&sessions, now, &open).is_empty(), "nothing moved");
    let moved = vec![session("fresh", "2026-11-24T11:58:00Z")];
    assert_eq!(tails.due(&moved, now, &open).len(), 1);
}

#[test]
fn reads_in_flight_stay_bounded_when_the_recent_sessions_change() {
    let now = parse_time("2026-11-24T12:00:00Z").unwrap();
    let batch = |prefix: &str| (0..10).map(|i| session(&format!("{prefix}{i}"), "2026-11-24T11:50:00Z")).collect::<Vec<_>>();
    let mut tails = Tails::default();
    let first = tails.due(&batch("a"), now, &HashSet::new());
    assert_eq!(first.len(), IN_FLIGHT);
    assert!(tails.due(&batch("b"), now, &HashSet::new()).is_empty(), "earlier reads still count while they run");
    tails.take(&first[0].0, first[0].1.clone(), Err("timed out".into()));
    assert_eq!(tails.due(&batch("b"), now, &HashSet::new()).len(), 1);
}

#[test]
fn hands_on_comes_from_fetched_tails_and_loaded_panes() {
    let mut ov = overview_with(vec![session("fetched", "2026-11-24T11:58:00Z"), session("paned", "2026-11-24T11:59:00Z")]);
    assert!(ov.world(&[]).hands_on(&[]).is_empty());
    let open: HashSet<String> = ["paned".to_string()].into();
    let due = ov.due_tails(&open);
    assert_eq!(due.len(), 1);
    ov.take_tail("fetched", due[0].1.clone(), Ok(tail(vec![said("1", "2026-11-24T10:00:00Z", "start"), said("2", "2026-11-24T11:50:00Z", "try again")])));
    let pane = [said("1", "2026-11-24T10:00:00Z", "start"), said("2", "2026-11-24T11:55:00Z", "and this")];
    assert_eq!(hands_on_ids(&ov.world(&[("paned", &pane)])), ["fetched", "paned"]);
}

#[test]
fn a_long_conversation_steered_once_recently_is_hands_on() {
    let mut ov = overview_with(vec![session("fetched", "2026-11-24T11:58:00Z"), session("paned", "2026-11-24T11:59:00Z"), session("quiet", "2026-11-24T11:57:00Z")]);
    let rows = long_conversation_steered_at("2026-11-24T11:50:00Z");
    let conversation: Vec<Item> = rows.iter().map(|r| serde_json::from_value(r.clone()).unwrap()).collect();
    let mut v = Vec::new();
    for (seq, row) in rows.into_iter().enumerate() {
        v.extend(upsert(seq as u64 + 1, row));
    }
    v.push(": live".into());
    let open: HashSet<String> = ["paned".to_string()].into();
    for (id, at) in ov.due_tails(&open) {
        let read = if id == "fetched" { tails::read(lines(v.clone())) } else { Ok(tails::trim(&conversation[conversation.len() - 20..conversation.len() - 1])) };
        ov.take_tail(&id, at, read);
    }
    assert_eq!(hands_on_ids(&ov.world(&[("paned", &conversation)])), ["fetched", "paned"], "a session whose only kept prompt is not its first still counts; one with none does not");
}

fn serve(respond: impl FnOnce(&mut std::net::TcpStream) + Send + 'static) -> Client {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let base = format!("http://{}", listener.local_addr().unwrap());
    std::thread::spawn(move || {
        let (mut stream, _) = listener.accept().unwrap();
        let mut request = BufReader::new(stream.try_clone().unwrap());
        let mut line = String::new();
        while request.read_line(&mut line).is_ok_and(|n| n > 0) && line != "\r\n" {
            line.clear();
        }
        respond(&mut stream);
    });
    Client::new(&base, "t")
}

const HEAD: &[u8] = b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n";
const BUDGET: Duration = Duration::from_millis(1500);
const SLACK: Duration = Duration::from_millis(700);

#[test]
fn a_tail_read_ends_within_its_budget_when_headers_are_slow() {
    let client = serve(|stream| {
        std::thread::sleep(Duration::from_secs(5));
        let _ = stream.write_all(HEAD);
    });
    let start = Instant::now();
    assert!(tails::fetch(&client, "a", BUDGET).is_err());
    assert!(start.elapsed() < BUDGET + SLACK, "took {:?}", start.elapsed());
}

#[test]
fn a_tail_read_ends_within_its_budget_when_rows_trickle_in() {
    let client = serve(|stream| {
        std::thread::sleep(Duration::from_millis(800));
        let _ = stream.write_all(HEAD);
        for seq in 1..50u64 {
            let mut row = upsert(seq, reply_row(&format!("r{seq}"))).join("\n");
            row.push('\n');
            if stream.write_all(row.as_bytes()).and_then(|_| stream.flush()).is_err() {
                return;
            }
            std::thread::sleep(Duration::from_millis(300));
        }
    });
    let start = Instant::now();
    assert!(tails::fetch(&client, "a", BUDGET).is_err(), "rows that never reach the live point are not a tail");
    assert!(start.elapsed() < BUDGET + SLACK, "took {:?}", start.elapsed());
}

#[test]
fn a_tail_read_returns_at_the_live_point_while_the_stream_stays_open() {
    let client = serve(|stream| {
        let mut body = HEAD.to_vec();
        body.extend(upsert(1, said_row("1", "2026-11-24T10:00:00Z", "start")).join("\n").as_bytes());
        body.extend(b"\n: live\n\n");
        let _ = stream.write_all(&body);
        std::thread::sleep(Duration::from_secs(5));
    });
    let start = Instant::now();
    let tail = tails::fetch(&client, "a", BUDGET).unwrap();
    assert_eq!(tail.items.len(), 1);
    assert!(start.elapsed() < Duration::from_millis(500), "took {:?}", start.elapsed());
}

#[test]
fn a_followed_session_is_read_even_when_it_is_not_among_the_recent_ones() {
    let now = parse_time("2026-11-24T12:00:00Z").unwrap();
    let sessions = vec![session("fresh", "2026-11-24T11:50:00Z"), session("operator", "2026-11-20T11:00:00Z")];
    let mut tails = Tails::default();
    assert!(tails.follow(Some("operator".into())), "a newly followed session asks for a read");
    assert!(!tails.follow(Some("operator".into())));
    let mut due: Vec<String> = tails.due(&sessions, now, &HashSet::new()).into_iter().map(|(id, _)| id).collect();
    due.sort();
    assert_eq!(due, ["fresh", "operator"]);
    tails.take("operator", "2026-11-20T11:00:00Z".into(), Ok(tail(vec![])));
    assert!(tails.due(&sessions, now, &HashSet::new()).is_empty(), "read again only when it moves");
    assert!(tails.follow(None));
    tails.due(&sessions, now, &HashSet::new());
    assert!(tails.held().all(|(id, _)| id != "operator"), "an unfollowed old session is let go");
}
