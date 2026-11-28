use serde_json::json;

use super::*;
use crate::mock::{daemon, replies};

fn change(value: serde_json::Value) -> Change {
    crate::reply::parse(value).unwrap()
}

#[test]
fn a_change_names_what_it_did_to_each_extension() {
    let several = change(json!({"rev": "a1", "time": "2026-11-27T05:01:00Z", "extensions": [{"id": "board", "created": true}, {"id": "proxy", "updated": ["index.html"]}, {"id": "old", "removed": true}, {"id": "notes", "added": ["a"], "deleted": ["b"]}]}));
    assert_eq!(several.describe(), "created board, changed 1 file in proxy, removed old, changed 2 files in notes");
    let bare = change(json!({"rev": "a2", "title": "Seed the workspace", "message": "seed"}));
    assert_eq!(bare.describe(), "Seed the workspace");
    assert_eq!(change(json!({"rev": "a3", "message": "tidy"})).describe(), "tidy");
    assert_eq!(change(json!({"rev": "a4"})).describe(), "no extension files");
}

#[test]
fn a_change_line_reads_in_local_time_with_author_and_checks() {
    let c = change(json!({"rev": "a1", "time": "2026-11-27T05:01:00Z", "author": "builder", "checks": ["node --check ok", ""]}));
    assert_eq!(c.meta(Clock::Twelve, |_| -5 * 3600), "Nov 27, 2026 at 12:01 AM · builder · node --check ok");
    assert_eq!(c.meta(Clock::TwentyFour, |_| 0), "Nov 27, 2026 at 05:01 · builder · node --check ok");
    assert_eq!(change(json!({"rev": "a2", "time": "not a time"})).meta(Clock::Twelve, |_| 0), "");
}

#[test]
fn a_build_waits_for_a_listed_healthy_host() {
    assert_eq!(build_blocked(true, None, "forge"), None);
    assert_eq!(build_blocked(false, None, "forge").as_deref(), Some("Waiting for forge to list its extensions."));
    assert_eq!(build_blocked(true, Some("Extensions are off on forge."), "forge").as_deref(), Some("Extensions are off on forge."));
    assert_eq!(build_title(None), "New Extension");
    assert_eq!(build_title(Some("forge")), "New Extension on forge");
}

#[test]
fn a_paired_host_is_named_on_every_path() {
    assert_eq!(on_host("/api/v1/extensions/build", None), "/api/v1/extensions/build");
    assert_eq!(on_host("/api/v1/extensions/build", Some("")), "/api/v1/extensions/build");
    assert_eq!(on_host("/api/v1/extensions/history?limit=50", Some("forge box")), "/api/v1/extensions/history?limit=50&host=forge+box");
    assert_eq!(on_host("/api/v1/extensions/build", Some("h&1")), "/api/v1/extensions/build?host=h%261");
}

#[test]
fn the_client_lists_shows_undoes_and_builds() {
    let (client, rx) = daemon(replies(&[
        (200, r#"{"changes":[{"rev":"r2","time":"2026-11-27T05:01:00Z","extensions":[{"id":"board","updated":["index.html"]}],"checks":["ok"]},{"rev":"r1","time":"2026-11-26T05:01:00Z","root":true}]}"#),
        (200, r#"{"change":{"rev":"r2"},"patch":"diff --git a/board/index.html b/board/index.html\n","truncated":true}"#),
        (200, r#"{"undone":"r2","removed":[]}"#),
        (200, r#"{"session":{"id":"s-builder"},"status":"created"}"#),
        (404, r#"{"error":{"code":"not_found","message":"Unknown change r9."}}"#),
        (400, r#"{"error":{"code":"invalid","message":"Say what the extension should do."}}"#),
    ]));
    let changes = client.extension_changes(Some("forge"), 50).unwrap();
    assert_eq!(rx.recv().unwrap().0, "/api/v1/extensions/history?limit=50&host=forge");
    assert_eq!(changes.iter().map(|c| (c.rev.as_str(), c.root)).collect::<Vec<_>>(), [("r2", false), ("r1", true)]);
    assert_eq!(changes[0].checks, ["ok"]);

    let shown = client.extension_change(None, "r2").unwrap();
    assert_eq!(rx.recv().unwrap().0, "/api/v1/extensions/history/r2");
    assert!(shown.truncated);
    assert!(shown.patch.starts_with("diff --git"));

    client.undo_extension_change(Some("forge"), "r2").unwrap();
    assert_eq!(rx.recv().unwrap().0, "/api/v1/extensions/history/r2/undo?host=forge");

    let started = client.build_extension(None, "a board of open work", "k1").unwrap();
    assert_eq!(rx.recv().unwrap(), ("/api/v1/extensions/build".to_string(), json!({"message": "a board of open work"})));
    assert_eq!(started.session_id().as_deref(), Some("s-builder"));

    assert_eq!(client.extension_change(None, "r9").unwrap_err(), "Unknown change r9.");
    let refused = client.build_extension(Some("forge"), " ", "k2").unwrap_err();
    assert_eq!(rx.recv().unwrap().0, "/api/v1/extensions/history/r9");
    assert_eq!(rx.recv().unwrap().0, "/api/v1/extensions/build?host=forge");
    assert_eq!(refused.message, "Say what the extension should do.");
    assert!(!refused.maybe_delivered);
}

#[test]
fn a_reply_without_a_session_starts_nothing_to_open() {
    assert_eq!(Started::default().session_id(), None);
    assert_eq!(crate::reply::parse::<Started>(json!({"session": {"id": " "}})).unwrap().session_id(), None);
}

#[test]
fn a_build_the_daemon_has_not_confirmed_is_not_reported_as_started() {
    let (client, _rx) = daemon(replies(&[(202, r#"{"ok":true,"status":"submitted"}"#)]));
    let unsure = client.build_extension(None, "a board", "k1").unwrap_err();
    assert!(unsure.maybe_delivered);
    assert_eq!(unsure.code, "submitted");
}

#[test]
fn an_unconfirmed_build_that_names_its_session_still_opens_it() {
    let (client, _rx) = daemon(replies(&[(202, r#"{"ok":true,"status":"submitted","session":{"id":"s-builder"}}"#)]));
    let started = client.build_extension(None, "a board", "k1").unwrap();
    assert_eq!(started.session_id().as_deref(), Some("s-builder"));
    assert!(started.unconfirmed());
}

#[test]
fn a_failed_build_that_made_its_session_opens_it_with_the_failure() {
    let (client, _rx) = daemon(replies(&[
        (502, r#"{"error":{"code":"initial_turn_failed","message":"claude exited before its first turn"},"session":{"id":"s-builder"},"deliveryId":4}"#),
        (500, r#"{"error":{"code":"create_storage_failed","message":"session was created, but its delivery intent could not be persisted"},"session":{"id":"s-other"}}"#),
        (500, r#"{"error":{"code":"internal_error","message":"no workspace"}}"#),
    ]));
    let failed = client.build_extension(None, "a board", "k1").unwrap();
    assert_eq!(failed.session_id().as_deref(), Some("s-builder"));
    assert_eq!(failed.failure.as_deref(), Some("claude exited before its first turn"));
    let stored = client.build_extension(None, "a board", "k2").unwrap();
    assert_eq!(stored.session_id().as_deref(), Some("s-other"));
    assert!(stored.failure.is_some());
    assert_eq!(client.build_extension(None, "a board", "k3").unwrap_err().message, "no workspace");
}

#[test]
fn a_paired_computer_that_dropped_the_answer_may_still_have_acted() {
    let lost = r#"{"error":{"code":"host_unreachable","message":"Host forge is unreachable: connection reset"},"host":"h2"}"#;
    let (client, _rx) = daemon(replies(&[(503, lost), (503, lost), (503, lost)]));
    assert!(client.build_extension(Some("h2"), "a board", "k1").unwrap_err().maybe_delivered);
    assert!(client.undo_extension_change(Some("h2"), "r2").unwrap_err().maybe_delivered);
    assert!(!client.build_extension(None, "a board", "k2").unwrap_err().maybe_delivered);
}

#[test]
fn a_build_or_undo_reply_that_proves_nothing_may_have_happened() {
    let (client, _rx) = daemon(replies(&[(200, r#"{"ok":true}"#), (202, r#"{"ok":true,"status":"submitted"}"#)]));
    let nameless = client.build_extension(None, "a board", "k1").unwrap_err();
    assert!(nameless.maybe_delivered);
    assert_eq!(nameless.code, "no_session");
    let undo = client.undo_extension_change(None, "r2").unwrap_err();
    assert!(undo.maybe_delivered);
    assert_eq!(undo.code, "submitted");
}
