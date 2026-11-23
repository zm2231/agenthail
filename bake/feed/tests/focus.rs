use feed::blocks::{build, Kind, Options};
use feed::focus;
use feed::item::{AgentMessage, Item};
use feed::summary;

fn item(id: &str, kind: &str, title: &str, role: Option<&str>, call: Option<&str>, agent: Option<AgentMessage>) -> Item {
    Item {
        id: id.into(),
        kind: kind.into(),
        role: role.map(Into::into),
        title: title.into(),
        text: id.into(),
        call_id: call.map(Into::into),
        agent,
        ..Default::default()
    }
}

fn ids(items: &[Item]) -> Vec<&str> {
    items.iter().map(|i| i.id.as_str()).collect()
}

fn peer() -> AgentMessage {
    AgentMessage { direction: "in".into(), kind: "peer".into(), peer: Some("builder".into()), summary: "Ready".into(), ..Default::default() }
}

#[test]
fn focus_keeps_asks_in_place_and_moves_work_before_reply() {
    let send = AgentMessage { direction: "out".into(), kind: "send".into(), peer: Some("builder".into()), summary: "Go".into(), ..Default::default() };
    let items = vec![
        item("ask-person", "message", "", Some("user"), None, None),
        item("reasoning", "reasoning", "", None, None, None),
        item("bash", "toolCall", "Bash", None, Some("b"), None),
        item("bash-out", "toolResult", "Bash", None, Some("b"), None),
        item("question", "toolCall", "AskUserQuestion", None, Some("q"), None),
        item("answer", "toolResult", "AskUserQuestion", None, Some("q"), None),
        item("mcp-ask", "toolCall", "mcp__agenthail__agenthail_ask", None, Some("m"), None),
        item("codex-ask", "toolCall", "request_user_input", None, Some("c"), None),
        item("compacted", "event", "Context compacted", None, None, None),
        item("notice", "notice", "Notice", None, None, None),
        item("peer", "message", "builder", Some("peer"), None, Some(peer())),
        item("send", "toolCall", "agenthail_send", None, Some("s"), Some(send)),
        item("sent", "toolResult", "agenthail_send", None, Some("s"), None),
        item("streamed", "text", "", Some("assistant"), None, None),
        item("file", "file", "", Some("assistant"), None, None),
        item("image", "attachment", "", Some("assistant"), None, None),
        item("reply", "message", "", Some("assistant"), None, None),
        item("duration", "event", "Turn duration", None, None, None),
        item("done", "done", "", None, None, None),
        item("goal", "goal", "", None, None, None),
    ];
    let focused = focus::focused(&items);
    assert_eq!(
        ids(&focused),
        ["ask-person", "question", "answer", "mcp-ask", "codex-ask", "reasoning", "bash", "bash-out", "peer", "send", "sent", "streamed", "file", "image", "reply", "duration", "done", "goal"]
    );
    let blocks = build(&items, &Options { focus: true, fold_agents: false, turn_open: false });
    let runs: Vec<Vec<&str>> = blocks
        .iter()
        .filter_map(|b| if let Kind::Work(w) = &b.kind { Some(ids(&w.items)) } else { None })
        .collect();
    assert_eq!(runs, vec![vec!["question", "answer", "mcp-ask", "codex-ask"], vec!["reasoning", "bash", "bash-out"]]);
    let reply = blocks.iter().find(|b| b.id == "item:reply").unwrap();
    assert_eq!(reply.finished.as_deref(), Some("duration"));
    assert!(!blocks.iter().any(|b| matches!(b.kind, Kind::Notice(_))));
}

#[test]
fn turns_end_at_next_prompt_or_helper() {
    let turns = vec![
        item("p1", "message", "", Some("user"), None, None),
        item("say1", "message", "", Some("assistant"), None, None),
        item("t1", "toolCall", "Bash", None, Some("1"), None),
        item("say2", "message", "", Some("assistant"), None, None),
        item("t2", "toolCall", "Read", None, Some("2"), None),
        item("helper", "message", "", Some("peer"), None, Some(peer())),
        item("t3", "toolCall", "Bash", None, Some("3"), None),
        item("p2", "message", "", Some("user"), None, None),
        item("t4", "toolCall", "Bash", None, Some("4"), None),
    ];
    assert_eq!(ids(&focus::focused(&turns)), ["p1", "say1", "t1", "t2", "say2", "helper", "t3", "p2", "t4"]);
    let mut timed: Vec<Item> = turns.iter().filter(|i| i.kind == "toolCall").cloned().collect();
    timed[0].timestamp = Some("2026-11-17T10:00:00Z".into());
    timed[1].timestamp = Some("2026-11-17T10:06:00Z".into());
    assert_eq!(summary::focus_line(&timed[..2]), "6m of work · 2 steps");
    assert_eq!(summary::focus_line(&timed[2..3]), "1 step");
}

#[test]
fn work_after_reply_moves_above_it() {
    let after = vec![
        item("ask", "message", "", Some("user"), None, None),
        item("answer", "message", "", Some("assistant"), None, None),
        item("late", "toolCall", "Bash", None, Some("l"), None),
        item("late-out", "toolResult", "Bash", None, Some("l"), None),
        item("took", "event", "Turn duration", None, None, None),
    ];
    assert_eq!(ids(&focus::focused(&after)), ["ask", "late", "late-out", "answer", "took"]);
}

#[test]
fn end_turn_calls_are_hidden() {
    let ended = vec![
        item("bash", "toolCall", "Bash", None, Some("b"), None),
        item("end", "toolCall", "mcp__agenthail__agenthail_end_turn", None, Some("e"), None),
        item("end-ok", "toolResult", "Tool result", None, Some("e"), None),
        item("codex-end", "toolCall", "agenthail_end_turn", None, Some("ce"), None),
        item("codex-end-ok", "toolResult", "Tool result", None, Some("ce"), None),
        item("bash-out", "toolResult", "Tool result", None, Some("b"), None),
        item("lookalike", "toolCall", "mcp__other__agenthail_end_turn_later", None, Some("l"), None),
        item("reply", "message", "", Some("assistant"), None, None),
    ];
    assert_eq!(ids(&focus::shown(&ended)), ["bash", "bash-out", "lookalike", "reply"]);
}

#[test]
fn labels_count_categories() {
    let calls: Vec<Item> = ["Bash", "Bash", "Read", "mcp__agenthail__agenthail_send", "Weird"]
        .iter()
        .enumerate()
        .map(|(i, t)| item(&i.to_string(), "toolCall", t, None, None, None))
        .collect();
    assert_eq!(summary::label(&calls), "Ran 2 commands and read 1 file +2 other");
}

#[test]
fn recorded_session_replays_into_blocks() {
    let path = std::env::var("FEED_FIXTURE").unwrap_or_else(|_| "/Volumes/4/caches/agenthail-v2-fixtures/self.stream.tsv".into());
    let Ok(text) = std::fs::read_to_string(&path) else { return };
    let mut sse = feed::wire::Sse::default();
    let mut rebuild = feed::wire::Rebuild::default();
    let mut transcript = feed::Transcript::default();
    for line in text.lines() {
        let Some((_, rest)) = line.split_once('\t') else { continue };
        let Some(json) = sse.line(rest) else { continue };
        let Ok(wire) = serde_json::from_str::<feed::wire::Wire>(&json) else { continue };
        if let feed::wire::Outcome::Events(seq, events) = rebuild.apply(wire) {
            transcript.apply(seq, events);
        }
    }
    let blocks = transcript.blocks(true);
    assert!(transcript.items().len() > 50, "items {}", transcript.items().len());
    assert!(blocks.iter().any(|b| matches!(b.kind, Kind::Work(_))));
    assert!(blocks.iter().any(|b| matches!(b.kind, Kind::User(_))));
}
