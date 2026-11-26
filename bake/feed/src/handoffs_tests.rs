use super::*;
use crate::mock::{daemon, replies};

fn at(ts: &str) -> OffsetDateTime {
    OffsetDateTime::parse(ts, &time::format_description::well_known::Rfc3339).unwrap()
}

#[test]
fn the_snapshot_lists_channels_with_members_and_relays_with_their_state() {
    let body = r#"{"busyDelivery":"queue","eventCursor":3,
        "channels":[{"name":"launch","members":["s1","s2"],"memberDetails":[{"id":"s1","display":"@writer"},{"id":"s2","display":""}]},{"name":"old","members":["s9"],"memberDetails":null}],
        "relays":[{"id":7,"from":"@research","to":"@builder","pattern":"READY","handoff":"goal_completed","once":true,"active":false,"pendingCount":2,"fireCount":1,"lastFiredAt":"2026-11-26T11:55:00Z","lastError":"destination busy","eventCursor":9}]}"#;
    let h: Handoffs = serde_json::from_str(body).unwrap();
    assert_eq!(h.channel("launch").unwrap().people(), vec![Member { id: "s1".into(), display: "@writer".into() }, Member { id: "s2".into(), display: "s2".into() }]);
    assert_eq!(h.channel("old").unwrap().people(), vec![Member { id: "s9".into(), display: "s9".into() }]);
    let r = &h.relays[0];
    assert_eq!(r.route(), "@research → @builder");
    assert_eq!(r.state(at("2026-11-26T12:00:00Z")), "Goal completed · Once · 2 pending · complete · fired once · last 5m ago");
    assert_eq!(r.problem(), Some("destination busy"));
    let fresh = Relay { fire_count: 3, active: true, last_fired_at: Some("2026-11-26T12:00:00Z".into()), ..Default::default() };
    assert_eq!(fresh.state(at("2026-11-26T12:00:10Z")), "Turn finished · Every time · 0 pending · active · fired 3 times · last just now");
    let empty: Handoffs = serde_json::from_str(r#"{"channels":null,"relays":null}"#).unwrap();
    assert!(empty.channels.is_empty() && empty.relays.is_empty());
    let missing: Handoffs = serde_json::from_str("{}").unwrap();
    assert_eq!(missing, Handoffs::default());
}

#[test]
fn channel_names_follow_the_daemons_rule() {
    assert_eq!(channel_name("  #launch-review "), Ok("launch-review".into()));
    assert!(channel_name("  ").is_err());
    assert!(channel_name("#").is_err());
    assert!(channel_name("launch review").is_err());
    assert!(channel_name("a/b").is_err());
    assert!(channel_name("a:b").is_err());
    assert!(channel_name(&"x".repeat(81)).is_err());
    assert!(channel_name(&"x".repeat(80)).is_ok());
    assert!(channel_name(&"é".repeat(41)).is_err(), "the daemon counts bytes");
}

#[test]
fn a_relay_needs_two_different_sessions_and_defaults_its_pattern() {
    let mut d = RelayDraft::default();
    assert!(d.problem().is_some());
    d.from = " @research ".into();
    assert!(d.problem().is_some());
    d.to = "@research".into();
    assert_eq!(d.problem(), Some("A session can't hand off to itself."));
    d.to = "@builder".into();
    assert_eq!(d.problem(), None);
    assert_eq!(d.body(), json!({"fromId": "@research", "toId": "@builder", "pattern": ".*", "once": false, "handoff": "turn_finished"}));
    d.pattern = " READY|SHIP ".into();
    d.handoff = GOAL_COMPLETED.into();
    d.once = true;
    assert_eq!(d.body(), json!({"fromId": "@research", "toId": "@builder", "pattern": "READY|SHIP", "once": true, "handoff": "goal_completed"}));
}

#[test]
fn a_channel_send_says_who_it_reached() {
    assert_eq!(Sent { sent: 1, ..Default::default() }.summary(), "Delivered to 1 session.");
    assert_eq!(Sent { sent: 2, queued: 1, older: 1, unreachable: vec!["forge".into()], ..Default::default() }.summary(), "Delivered to 2 sessions; queued for 1 session; 1 older session not reached; held for forge.");
    assert_eq!(Sent { submitted: 2, skipped: 1, ..Default::default() }.summary(), "2 deliveries taken but not yet confirmed; 1 member skipped.");
    assert_eq!(Sent::default().summary(), "Sent.");
}

#[test]
fn session_search_offers_local_sessions_and_names_them_for_the_daemon() {
    let page = r#"{"sessions":[
        {"id":"h1::a","name":"Writer","alias":"writer","host":{"state":"local"}},
        {"id":"h1::b","name":"  Docs pass ","alias":"","readOnly":true},
        {"id":"h2::c","name":"Far","host":{"state":"connected"}},
        {"id":"h1::a","name":"Writer again","alias":"writer"},
        {"id":"h1::d","name":""}
    ],"nextCursor":"x"}"#;
    let (client, rx) = daemon(replies(&[(200, page), (403, r#"{"error":{"message":"no"}}"#)]));
    let found = client.find_sessions(" doc & pass ").unwrap();
    let (path, _) = rx.recv().unwrap();
    assert_eq!(path, "/api/v1/snapshot?scope=all&members=1&limit=20&q=doc+%26+pass");
    let all = picks(found.clone(), false);
    assert_eq!(all.iter().map(|p| (p.label(), p.target())).collect::<Vec<_>>(), vec![("@writer".into(), "@writer".into()), ("Docs pass".into(), "h1::b".into()), ("h1::d".to_string(), "h1::d".to_string())]);
    assert_eq!(picks(found, true).iter().map(|p| p.id.as_str()).collect::<Vec<_>>(), vec!["h1::a", "h1::d"]);
    assert!(client.find_sessions("x").unwrap_err().denied);
    let empty: Found = serde_json::from_str(r#"{"sessions":null}"#).unwrap();
    assert!(empty.sessions.is_empty());
}

#[test]
fn an_unsettled_send_is_retried_under_its_own_key_only_while_unchanged() {
    let maybe: Result<(), Failure> = Err(Failure { uncertain: true, ..Failure::new("timed out") });
    let unsent: Result<(), Failure> = Err(Failure::new("connection refused"));
    let mut u = Unsettled::default();
    assert_ne!(u.key_for("ship", "hello"), u.key_for("ship", "hello"));
    u.record("ship", "hello", "k1", &maybe);
    u.record("ops", "status", "k2", &maybe);
    u.record("ship", "other", "k3", &Ok(()));
    assert_eq!((u.key_for("ship", "hello"), u.key_for("ops", "status")), ("k1".to_string(), "k2".to_string()), "a later send elsewhere keeps an earlier open one");
    u.record("ship", "hello", "k1", &unsent);
    assert_eq!(u.key_for("ship", "hello"), "k1", "a retry that surely didn't go out keeps the first request");
    assert_ne!(u.key_for("ship", "hello again"), "k1");
    assert_ne!(u.key_for("ops", "hello"), "k1");
    u.record("ship", "hello", "k1", &Ok(()));
    assert_ne!(u.key_for("ship", "hello"), "k1");
    u.record("ship", "hello", "k4", &maybe);
    u.forget_channel("ship");
    assert_ne!(u.key_for("ship", "hello"), "k4", "a deleted channel recreated under its name is new");
    assert_eq!(u.key_for("ops", "status"), "k2");
    u.record("ship", "hello", "k5", &maybe);
    u.keep_channels(&serde_json::from_str(r#"{"channels":[{"name":"ship"}]}"#).unwrap());
    assert_eq!((u.key_for("ship", "hello"), u.key_for("ops", "status") == "k2"), ("k5".to_string(), false), "a channel gone from the list loses its open sends");
    let (client, _rx) = daemon(replies(&[(400, r#"{"error":{"code":"invalid_request","message":"no members"}}"#)]));
    let plain = client.channel_send("ship", "hello", "k4").unwrap_err();
    assert!(!plain.uncertain && plain.message == "no members");
}

#[test]
fn each_change_posts_its_action_and_reports_refusals() {
    let ok = r#"{"ok":true}"#;
    let sent = r#"{"ok":true,"sent":2,"queued":0,"submitted":0,"failed":0,"skipped":0,"older":0,"recipients":[]}"#;
    let partial = r#"{"error":{"code":"conflict","message":"channel delivery: 1 sent, 0 queued, 0 submitted, 1 failed (@b failed: closed)"}}"#;
    let denied = r#"{"error":{"code":"unauthorized","message":"no"}}"#;
    let submitted = r#"{"status":"submitted"}"#;
    let (client, rx) = daemon(replies(&[(200, ok), (200, ok), (200, ok), (200, ok), (200, sent), (200, ok), (200, ok), (409, partial), (403, denied), (200, submitted), (504, r#"{"error":{"message":"timed out"}}"#)]));
    client.channel_create("ship").unwrap();
    client.channel_add("ship", "@writer").unwrap();
    client.channel_remove("ship", "s2").unwrap();
    client.channel_delete("ship").unwrap();
    assert_eq!(client.channel_send("ship", "hello", "k1").unwrap().summary(), "Delivered to 2 sessions.");
    let draft = RelayDraft { from: "@a".into(), to: "@b".into(), ..Default::default() };
    client.relay_add(&draft).unwrap();
    client.relay_remove(7).unwrap();
    let conflict = client.channel_send("ship", "again", "k2").unwrap_err();
    assert!(!conflict.denied && conflict.uncertain && conflict.message.starts_with(MAY_HAVE_REACHED) && conflict.message.contains("1 failed"));
    let refused = client.relay_remove(8).unwrap_err();
    assert!(refused.denied && refused.scope == "control");
    assert!(client.channel_send("x", "m", "k3").unwrap_err().message.starts_with(MAY_HAVE_REACHED));
    assert!(client.channel_delete("y").unwrap_err().uncertain);
    let bodies: Vec<Value> = rx.try_iter().map(|(path, body)| {
        assert_eq!(path, "/api/v1/actions");
        body
    }).collect();
    assert_eq!(
        bodies[..7],
        [
            json!({"action": "channel-create", "channel": "ship"}),
            json!({"action": "channel-add", "channel": "ship", "targetId": "@writer"}),
            json!({"action": "channel-remove", "channel": "ship", "targetId": "s2"}),
            json!({"action": "channel-delete", "channel": "ship"}),
            json!({"action": "channel-send", "channel": "ship", "message": "hello", "author": "person"}),
            json!({"action": "relay-add", "fromId": "@a", "toId": "@b", "pattern": ".*", "once": false, "handoff": "turn_finished"}),
            json!({"action": "relay-remove", "relayId": 7}),
        ]
    );
}
