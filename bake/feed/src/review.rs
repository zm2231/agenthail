use serde_json::{Value, json};

use crate::catalog::{Client, Pr, RespondError};
use crate::decisions::Decision;
use crate::derive::Work;

pub const RELEASE: &str = "v0.2.173";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Verdict {
    Approve,
    Reject,
}

impl Verdict {
    pub fn as_str(self) -> &'static str {
        match self {
            Verdict::Approve => "approve",
            Verdict::Reject => "reject",
        }
    }

    pub fn outcome(self) -> &'static str {
        match self {
            Verdict::Approve => "Approved",
            Verdict::Reject => "Asked for changes",
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Target {
    pub ledger_key: String,
    pub work_item: String,
    pub host: String,
}

pub fn key(ledger_key: &str, work_item: &str) -> String {
    format!("review:{ledger_key}:{work_item}")
}

pub fn target(d: &Decision) -> Option<Target> {
    if !d.is_review() || d.work_item.is_empty() {
        return None;
    }
    let ledger_key = if d.ledger_key.is_empty() { d.key.strip_prefix("review:")?.strip_suffix(d.work_item.as_str())?.strip_suffix(':')?.to_string() } else { d.ledger_key.clone() };
    (!ledger_key.is_empty()).then(|| Target { ledger_key, work_item: d.work_item.clone(), host: d.host.clone() })
}

pub fn of_work(listed: &[Decision], complete: bool, w: &Work) -> Option<Decision> {
    let key = key(&w.project, &w.id);
    if let Some(d) = listed.iter().find(|d| d.is_review() && (d.key == key || target(d).is_some_and(|t| t.ledger_key == w.project && t.work_item == w.id))) {
        return Some(d.clone());
    }
    complete.then(|| Decision { group: "review".into(), kind: "review".into(), key, session_id: w.assignee.clone().unwrap_or_default(), work_item: w.id.clone(), work_title: w.text.clone(), ledger_key: w.project.clone(), ..Default::default() })
}

pub fn note_problem(note: &str) -> Option<&'static str> {
    note.trim().is_empty().then_some("Say what it still needs.")
}

pub fn needs_newer(e: &RespondError) -> bool {
    e.answered && (e.status == 404 || e.code == "not_found" || e.message == "unsupported dashboard action" || e.message == "sessionId is required")
}

pub fn needs_newer_note() -> String {
    format!("Needs Agenthail {RELEASE} or later")
}

pub fn refusal(e: &RespondError) -> String {
    match e.code.as_str() {
        "stale" => "It's no longer waiting for review.".into(),
        "forbidden" => "Only you can review here, and the daemon didn't take this as you.".into(),
        _ => e.message.clone(),
    }
}

pub fn pr_link<'a>(prs: impl IntoIterator<Item = &'a Pr>, number: Option<&str>) -> Option<&'a Pr> {
    let prs: Vec<&Pr> = prs.into_iter().filter(|p| p.url.starts_with("https://") || p.url.starts_with("http://")).collect();
    match number.and_then(|n| n.parse::<u64>().ok()) {
        Some(n) => prs.iter().find(|p| p.number == n || p.url.ends_with(&format!("/pull/{n}"))).or(prs.first()).copied(),
        None => prs.first().copied(),
    }
}

pub fn timeline_pr(w: &Work) -> Option<Pr> {
    for t in w.timeline.iter().rev() {
        let text = t.text.as_deref().unwrap_or("");
        for (at, _) in text.match_indices("https://") {
            let url: String = text[at..].chars().take_while(|c| !c.is_whitespace() && !matches!(c, ')' | ']' | '>' | '"' | '\'' | ',')).collect();
            let Some((_, tail)) = url.split_once("/pull/") else { continue };
            let digits: String = tail.chars().take_while(|c| c.is_ascii_digit()).collect();
            if let Ok(number) = digits.parse() {
                let url = url[..url.len() - tail.len() + digits.len()].to_string();
                return Some(Pr { number, title: String::new(), url });
            }
        }
    }
    None
}

impl Client {
    pub fn work_review(&self, t: &Target, verdict: Verdict, note: &str) -> Result<Value, RespondError> {
        let mut payload = json!({"ledgerKey": t.ledger_key, "workItem": t.work_item, "verdict": verdict.as_str(), "host": t.host});
        if !note.trim().is_empty() {
            payload["note"] = Value::String(note.trim().into());
        }
        self.action("work-review", payload).and_then(crate::catalog::confirmed)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{BufRead, BufReader, Read, Write};
    use std::net::TcpListener;
    use std::sync::mpsc;

    fn decision(json: &str) -> Decision {
        serde_json::from_str(json).unwrap()
    }

    fn daemon(replies: Vec<(u16, &'static str)>) -> (Client, mpsc::Receiver<(String, Value)>) {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let (tx, rx) = mpsc::channel();
        std::thread::spawn(move || {
            for (status, body) in replies {
                let (stream, _) = listener.accept().unwrap();
                let mut reader = BufReader::new(stream.try_clone().unwrap());
                let mut line = String::new();
                reader.read_line(&mut line).unwrap();
                let (mut key, mut length) = (String::new(), 0usize);
                loop {
                    let mut header = String::new();
                    reader.read_line(&mut header).unwrap();
                    let header = header.trim_end();
                    if header.is_empty() {
                        break;
                    }
                    let (name, value) = header.split_once(':').unwrap();
                    match name.to_ascii_lowercase().as_str() {
                        "idempotency-key" => key = value.trim().into(),
                        "content-length" => length = value.trim().parse().unwrap(),
                        _ => {}
                    }
                }
                let mut raw = vec![0; length];
                reader.read_exact(&mut raw).unwrap();
                tx.send((key, serde_json::from_slice(&raw).unwrap())).unwrap();
                let mut stream = stream;
                write!(stream, "HTTP/1.1 {status} X\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len()).unwrap();
            }
        });
        (Client::new(&base, "secret"), rx)
    }

    #[test]
    fn a_review_item_reads_its_ledger_proof_and_prs() {
        let d = decision(
            r#"{"group":"review","kind":"review","key":"review:p-1:p-W6","sessionId":"holder","workItem":"p-W6","workTitle":"Wire review","ledgerKey":"p-1","phase":"check",
                "proof":{"pass":2,"fail":1,"pending":3},"prs":[{"url":"https://github.com/o/r/pull/12","number":12,"state":"OPEN","checks":"passing"}]}"#,
        );
        assert_eq!(target(&d), Some(Target { ledger_key: "p-1".into(), work_item: "p-W6".into(), host: String::new() }));
        assert_eq!(d.phase, "check");
        assert_eq!(d.proof.as_ref().map(|p| (p.pass, p.fail, p.pending)), Some((2, 1, 3)));
        assert_eq!(d.proof_line().as_deref(), Some("2 passed · 1 failed · 3 pending"));
        assert_eq!(d.prs[0].url, "https://github.com/o/r/pull/12");
        let remote = Decision { host: "h2".into(), ledger_key: String::new(), ..d.clone() };
        assert_eq!(target(&remote), Some(Target { ledger_key: "p-1".into(), work_item: "p-W6".into(), host: "h2".into() }));
        assert_eq!(target(&decision(r#"{"group":"waiting","sessionId":"s","workItem":"p-W6"}"#)), None);
    }

    #[test]
    fn the_work_screen_uses_the_listed_review_or_this_computers_ledger() {
        let w = Work { id: "p-W6".into(), text: "Wire review".into(), assignee: Some("holder".into()), project: "p-1".into(), ..Default::default() };
        let listed = vec![Decision { host: "h2".into(), ..decision(r#"{"group":"review","key":"review:p-1:p-W6","sessionId":"h2::holder","workItem":"p-W6","ledgerKey":"p-1"}"#) }];
        assert_eq!(target(&of_work(&listed, false, &w).unwrap()).unwrap().host, "h2");
        let other = vec![Decision { key: "review:p-0:p-W6".into(), ledger_key: "p-0".into(), ..listed[0].clone() }];
        assert_eq!(of_work(&other, false, &w), None);
        let own = of_work(&other, true, &w).unwrap();
        assert_eq!(own.id(), "review:p-1:p-W6");
        assert_eq!(target(&own), Some(Target { ledger_key: "p-1".into(), work_item: "p-W6".into(), host: String::new() }));
        assert_eq!(own.session_id, "holder");
    }

    #[test]
    fn approve_and_ask_for_changes_send_what_the_daemon_reads() {
        let ok = r#"{"ok":true,"result":{"outcome":"approved","workItem":"p-W6"}}"#;
        let (client, seen) = daemon(vec![(200, ok), (200, ok)]);
        let t = Target { ledger_key: "p-1".into(), work_item: "p-W6".into(), host: String::new() };
        client.work_review(&t, Verdict::Approve, "").unwrap();
        let (key, body) = seen.recv().unwrap();
        assert!(!key.is_empty());
        assert_eq!(body, json!({"action": "work-review", "ledgerKey": "p-1", "workItem": "p-W6", "verdict": "approve", "host": ""}));
        let remote = Target { host: "h2".into(), ..t };
        client.work_review(&remote, Verdict::Reject, "  add the 404 test ").unwrap();
        let (_, body) = seen.recv().unwrap();
        assert_eq!(body, json!({"action": "work-review", "ledgerKey": "p-1", "workItem": "p-W6", "verdict": "reject", "note": "add the 404 test", "host": "h2"}));
    }

    #[test]
    fn an_older_daemon_is_told_apart_from_a_refusal() {
        let (client, _seen) = daemon(vec![
            (400, r#"{"error":{"code":"invalid_request","message":"sessionId is required"}}"#),
            (400, r#"{"error":{"code":"invalid_request","message":"unsupported dashboard action"}}"#),
            (404, "not here"),
            (404, r#"{"error":{"code":"route_missing","message":"nothing here"}}"#),
            (409, r#"{"error":{"code":"stale","message":"this request was already answered or has expired"}}"#),
            (403, r#"{"error":{"code":"forbidden","message":"only the person reviews through work-review"}}"#),
            (400, r#"{"error":{"code":"invalid_request","message":"sending work back needs a note: what it still needs"}}"#),
            (504, r#"{"error":{"code":"host_outcome_unknown","message":"Host mac did not answer"}}"#),
            (202, r#"{"ok":true,"status":"submitted"}"#),
        ]);
        let t = Target { ledger_key: "p-1".into(), work_item: "p-W6".into(), host: String::new() };
        for _ in 0..4 {
            let e = client.work_review(&t, Verdict::Approve, "").unwrap_err();
            assert!(needs_newer(&e), "{e:?}");
        }
        let stale = client.work_review(&t, Verdict::Approve, "").unwrap_err();
        assert!(!needs_newer(&stale) && !stale.maybe_delivered);
        assert_eq!(refusal(&stale), "It's no longer waiting for review.");
        let forbidden = client.work_review(&t, Verdict::Approve, "").unwrap_err();
        assert!(!needs_newer(&forbidden));
        assert_eq!(refusal(&forbidden), "Only you can review here, and the daemon didn't take this as you.");
        let note = client.work_review(&t, Verdict::Reject, "x").unwrap_err();
        assert!(!needs_newer(&note));
        assert_eq!(refusal(&note), "sending work back needs a note: what it still needs");
        let unknown = client.work_review(&t, Verdict::Approve, "").unwrap_err();
        assert!(unknown.maybe_delivered && !needs_newer(&unknown));
        let submitted = client.work_review(&t, Verdict::Approve, "").unwrap_err();
        assert!(submitted.maybe_delivered && submitted.code == "submitted");
        let unsent = Client::new("http://127.0.0.1:0", "secret").work_review(&t, Verdict::Approve, "").unwrap_err();
        assert!(!needs_newer(&unsent));
        assert_eq!(needs_newer_note(), "Needs Agenthail v0.2.173 or later");
    }

    #[test]
    fn asking_for_changes_needs_words() {
        assert_eq!(note_problem("  \n"), Some("Say what it still needs."));
        assert_eq!(note_problem("add a test"), None);
    }

    #[test]
    fn open_pr_takes_a_real_link_matching_the_number() {
        let prs = vec![Pr { number: 7, title: String::new(), url: String::new() }, Pr { number: 12, title: String::new(), url: "https://github.com/o/r/pull/12".into() }, Pr { number: 13, title: String::new(), url: "https://github.com/o/r/pull/13".into() }];
        assert_eq!(pr_link(&prs, Some("13")).map(|p| p.number), Some(13));
        assert_eq!(pr_link(&prs, Some("99")).map(|p| p.number), Some(12));
        assert_eq!(pr_link(&prs, None).map(|p| p.number), Some(12));
        assert_eq!(pr_link(&prs[..1], None), None);
        let w = Work {
            timeline: vec![
                crate::derive::Entry { text: Some("opened (https://github.com/o/r/pull/1328), CI green".into()), ..Default::default() },
                crate::derive::Entry { text: Some("see https://example.com/x then".into()), ..Default::default() },
            ],
            ..Default::default()
        };
        assert_eq!(timeline_pr(&w), Some(Pr { number: 1328, title: String::new(), url: "https://github.com/o/r/pull/1328".into() }));
        assert_eq!(timeline_pr(&Work::default()), None);
    }
}
