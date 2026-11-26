use serde_json::json;

use super::*;
use crate::mock::{daemon, replies};

const ORIGIN: &str = "http://127.0.0.1:7412";

fn sample() -> List {
    serde_json::from_value(json!({
        "host": "running",
        "extensions": [
            {
                "id": "proxy", "label": "Proxy", "state": "loaded",
                "capabilities": ["read.all"], "actions": [],
                "mounts": [
                    {"name": "console", "label": "Console", "port": 48100, "entry": "/", "upstream": "http://forge:8317", "state": "serving", "app": {"label": "Proxy console", "icon": "server.rack", "order": 2}, "badge": "3"},
                    {"name": "admin", "label": "Admin", "entry": "/admin", "upstream": "http://forge:9000", "state": "pending"}
                ],
                "approvalAsk": {"id": "ask-1", "sessionId": "s-builder"}
            },
            {
                "id": "board", "label": "Board", "description": "Work at a glance", "state": "loaded",
                "page": {"port": 48200, "entry": "index.html"},
                "capabilities": ["ledger.read"], "pendingCapabilities": ["sessions.send"], "actions": ["refresh"],
                "jobs": {"sync": {"lastError": "timed out"}}, "toolsNote": "Its tools are over the budget, so sessions don't see them." 
            },
            {
                "id": "broken", "label": "Broken", "state": "invalid", "error": "extension.json: missing id",
                "page": {"port": 48300, "entry": "/"}, "capabilities": [], "actions": []
            }
        ]
    }))
    .unwrap()
}

#[test]
fn every_page_and_mount_is_an_entry_in_hub_order() {
    let list = sample();
    let all = entries(&list, "mac", "this Mac", ORIGIN);
    let ids: Vec<String> = all.iter().map(Entry::id).collect();
    assert_eq!(ids, ["mac/board/page", "mac/broken/page", "mac/proxy/mount/admin", "mac/proxy/mount/console"]);

    let board = &all[0];
    assert_eq!(board.url.as_deref(), Some("http://127.0.0.1:48200/index.html"));
    assert_eq!(board.detail.as_deref(), Some("Work at a glance"));
    assert_eq!(board.subtitle(), "Work at a glance");

    let broken = &all[1];
    assert_eq!(broken.url, None);
    assert_eq!(broken.unavailable.as_deref(), Some("extension.json: missing id"));

    let admin = &all[2];
    assert!(admin.awaits_approval);
    assert_eq!(admin.approval_session.as_deref(), Some("s-builder"));
    assert_eq!(admin.unavailable.as_deref(), Some("Needs your OK to open."));

    let console = &all[3];
    assert_eq!(console.label, "Proxy console");
    assert_eq!(console.icon, "server.rack");
    assert_eq!(console.badge.as_deref(), Some("3"));
    assert!(console.offered);
    assert_eq!(console.url.as_deref(), Some("http://127.0.0.1:48100/"));
    assert_eq!(console.subtitle(), "Web app, part of Proxy");
}

#[test]
fn a_tailnet_origin_opens_only_published_ports() {
    let list = sample();
    let all = entries(&list, "forge", "forge", "https://mac.tailnet.ts.net:7412");
    let board = all.iter().find(|e| e.extension_id == "board").unwrap();
    assert_eq!(board.url, None);
    assert_eq!(board.unavailable.as_deref(), Some("forge doesn't publish this on the tailnet."));
}

#[test]
fn a_page_without_a_port_says_so() {
    assert_eq!(target(0, "/", false, "", ORIGIN, "forge"), (None, Some("forge isn't serving this yet.".into())));
    assert_eq!(target(9, "/", false, "boom", ORIGIN, "forge"), (None, Some("boom".into())));
}

#[test]
fn approval_goes_to_the_asking_session_or_directly() {
    let list = sample();
    let by_id = |id: &str| list.extensions.iter().find(|e| e.id == id).unwrap().clone();
    assert_eq!(approval(&by_id("proxy")), Approval::Ask("s-builder".into()));
    assert_eq!(approval(&by_id("board")), Approval::Direct);
    assert_eq!(approval(&by_id("broken")), Approval::Nothing);
    assert_eq!(waiting(&by_id("proxy")), ["Admin (http://forge:9000)"]);
}

#[test]
fn problems_name_what_failed_and_what_waits() {
    let list = sample();
    let board = list.extensions.iter().find(|e| e.id == "board").unwrap();
    let texts: Vec<(String, bool)> = problems(board).into_iter().map(|p| (p.text, p.failed)).collect();
    assert_eq!(texts, [("Waits for your approval: sessions.send".to_string(), false), ("Its tools are over the budget, so sessions don't see them.".to_string(), false), ("Job sync failed: timed out".to_string(), true)]);
    assert_eq!(status(board), ("loaded".to_string(), Tone::Failed));
    let broken = list.extensions.iter().find(|e| e.id == "broken").unwrap();
    assert_eq!(status(broken), ("invalid".to_string(), Tone::Failed));
}

#[test]
fn an_entry_waiting_with_no_ask_names_the_command() {
    let mut list = sample();
    list.extensions[0].approval_ask = None;
    let admin = entries(&list, "mac", "this Mac", ORIGIN).into_iter().find(|e| e.label == "Admin").unwrap();
    assert_eq!(admin.closed_reason(), "It needs your OK to open, and nothing has asked for it yet. On this Mac, approve it with agenthail extensions approve proxy.");
}

#[test]
fn a_stopped_host_is_reported() {
    let off: List = serde_json::from_value(json!({"host": "off", "extensions": []})).unwrap();
    assert_eq!(off.host_problem("forge").as_deref(), Some("Extensions are off on forge."));
    let down: List = serde_json::from_value(json!({"host": "unavailable", "hostError": "node exited", "extensions": []})).unwrap();
    assert_eq!(down.host_problem("forge").as_deref(), Some("node exited"));
    assert_eq!(sample().host_problem("forge"), None);
}

#[test]
fn navigation_keeps_the_page_on_its_own_port() {
    let page = "http://127.0.0.1:48200/index.html";
    assert_eq!(navigation("http://127.0.0.1:48200/other", page), Navigation::Allow);
    assert_eq!(navigation("about:blank", page), Navigation::Allow);
    assert_eq!(navigation("http://127.0.0.1:7412/api/v1/snapshot", page), Navigation::Refuse);
    assert_eq!(navigation("https://example.com/", page), Navigation::OpenOutside);
    assert_eq!(navigation("file:///etc/passwd", page), Navigation::Refuse);
    assert_eq!(navigation("not a url", page), Navigation::Refuse);
    assert_eq!(page_host(page).as_deref(), Some("127.0.0.1"));
    assert_eq!(page_host("https://mac.tailnet.ts.net:48100/").as_deref(), Some("mac.tailnet.ts.net"));
    assert_eq!(page_host("http://[::1]:48100/").as_deref(), Some("::1"));
    assert_eq!(page_host("nope"), None);
}

#[test]
fn the_client_reads_lists_mints_a_web_session_and_posts_actions() {
    let list = serde_json::to_string(&json!({"host": "running", "extensions": [{"id": "board", "state": "loaded"}]})).unwrap();
    let (client, rx) = daemon(replies(&[
        (200, &list),
        (200, r#"{"name":"agenthail_web","value":"v1","path":"/","expiresAt":"2026-11-27T00:00:00Z","scopes":["read","control"]}"#),
        (200, &list),
        (200, &list),
        (403, r#"{"error":{"code":"forbidden","message":"This token does not hold the settings scope."}}"#),
    ]));
    assert_eq!(client.extensions(Some("forge box")).unwrap().extensions[0].id, "board");
    assert_eq!(rx.recv().unwrap().0, "/api/v1/extensions?host=forge+box");
    let web = client.web_session().unwrap();
    assert_eq!((web.name.as_str(), web.value.as_str(), web.path.as_str()), ("agenthail_web", "v1", "/"));
    assert_eq!(rx.recv().unwrap().0, "/api/v1/web-session");
    client.approve_extension("board").unwrap();
    assert_eq!(rx.recv().unwrap(), ("/api/v1/extensions".to_string(), json!({"action": "approve", "id": "board"})));
    client.reload_extensions().unwrap();
    assert_eq!(rx.recv().unwrap(), ("/api/v1/extensions".to_string(), json!({"action": "reload"})));
    let refused = client.reload_extensions().unwrap_err();
    assert_eq!(refused.status, 403);
    assert_eq!(refused.message, "This token does not hold the settings scope.");
    assert!(!refused.maybe_delivered);
}

#[test]
fn an_extension_grant_ask_is_answered_with_its_option_id() {
    let requests = r#"{"requests":[{"id":"ask-7","kind":"ask","tool":"","title":"Approve board","optionsOnly":true,
        "questions":[{"question":"Approve extension board? It gets sessions.send","header":"","multiSelect":false,
        "options":[{"id":"approve","label":"Approve","description":"Grant what board/extension.json declares now"},{"id":"decline","label":"Decline","description":""}],
        "suggested":1}],"createdAt":"2026-11-26T10:00:00Z"}]}"#;
    let (client, rx) = daemon(replies(&[(200, requests), (200, r#"{"ok":true,"result":{"requestId":"ask-7","outcome":"approved"}}"#)]));
    let open = client.requests("s-builder").unwrap();
    let q = &open[0].questions.as_ref().unwrap()[0];
    assert_eq!(q.options.iter().map(|o| o.id.as_str()).collect::<Vec<_>>(), ["approve", "decline"]);
    assert!(crate::catalog::suggests(q.suggested, 1, &q.options[1].label));
    assert!(!crate::catalog::suggests(q.suggested, 0, &q.options[0].label));
    assert!(!open[0].is_compound());
    rx.recv().unwrap();
    client.respond("s-builder", "ask-7", vec!["approve".into()], Default::default()).unwrap();
    let (path, body) = rx.recv().unwrap();
    assert_eq!(path, "/api/v1/actions");
    assert_eq!(body, json!({"action": "respond", "sessionId": "s-builder", "requestId": "ask-7", "decision": {"choices": ["approve"], "answers": {}}}));
}

#[test]
fn a_probe_tells_a_refused_sign_in_from_an_unreachable_page() {
    let session = WebSession { name: "agenthail_web".into(), value: "v1".into(), path: "/".into(), expires_at: String::new(), scopes: vec![] };
    let (client, rx) = daemon(replies(&[(401, r#"{"error":{"code":"unauthorized","message":"no"}}"#), (200, "{}"), (404, "{}"), (502, "{}")]));
    let page = format!("{}/index.html", client.origin());
    assert_eq!(probe(&page, &session), Probe::SignIn);
    assert_eq!(rx.recv().unwrap().0, "/index.html");
    assert_eq!(probe(&page, &session), Probe::Open);
    assert_eq!(probe(&page, &session), Probe::Open);
    assert_eq!(probe(&page, &session), Probe::Unreachable("it answered 502".into()));
    let closed = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let gone = format!("http://{}/", closed.local_addr().unwrap());
    drop(closed);
    assert!(matches!(probe(&gone, &session), Probe::Unreachable(_)));
}
