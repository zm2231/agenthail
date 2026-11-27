use serde_json::{Value, json};

use super::*;
use crate::mock::{daemon, replies};

fn record(v: Value) -> ProjectRecord {
    serde_json::from_value(v).unwrap()
}

fn here(id: &str, added: bool) -> Value {
    json!({"host": {"id": "h1", "name": "mac", "state": "local"}, "projectId": id, "rootPath": "/w/app", "added": added})
}

fn ids(list: &[&str]) -> Vec<String> {
    list.iter().map(|s| s.to_string()).collect()
}

fn labels(groups: &[Vec<Entry>]) -> Vec<Vec<String>> {
    groups.iter().map(|g| g.iter().map(|e| e.label.clone()).collect()).collect()
}

#[test]
fn an_added_project_with_one_lead_offers_the_mac_menu() {
    let p = record(json!({"id": "p1", "displayName": "App", "checkouts": [here("p1", true)], "coordinators": [{"name": "lead", "surface": "claude"}], "mcp": "on"}));
    let got = entries(&p, &Arrangement::default(), &ids(&["p0", "p1", "p2"]));
    assert_eq!(
        labels(&got),
        vec![
            vec!["Rename…", "New conversation", "Change Lead…", "Clear Lead"],
            vec!["Pin to Front", "Move Earlier", "Move Later"],
            vec!["Keep the Agenthail Tools from Terminal Sessions", "Archive Project…", "Remove…"],
        ]
    );
    assert_eq!(got[2][0].note.as_deref(), Some(TOOLS_HELP));
}

#[test]
fn a_project_found_from_sessions_can_be_archived_but_not_renamed_or_removed() {
    let p = record(json!({"id": "implicit:/w/x", "displayName": "x", "checkouts": [here("implicit:/w/x", false)]}));
    let got = labels(&entries(&p, &Arrangement::default(), &ids(&["implicit:/w/x"])));
    assert_eq!(got, vec![vec!["New conversation", "Set Lead…"], vec!["Pin to Front", "Move Earlier", "Move Later"], vec!["Archive Project…"]]);
}

#[test]
fn an_archived_project_offers_only_unarchive_and_remove() {
    let p = record(json!({"id": "p1", "displayName": "App", "archived": true, "checkouts": [here("p1", true)], "coordinators": [{"name": "a"}, {"name": "b"}]}));
    let got = labels(&entries(&p, &Arrangement::default(), &[]));
    assert_eq!(got, vec![vec!["Rename…"], vec!["Unarchive Project", "Remove…"]]);
}

#[test]
fn two_leads_cannot_be_cleared_from_the_menu() {
    let p = record(json!({"id": "p1", "coordinators": [{"name": "a"}, {"name": "b"}]}));
    let got = entries(&p, &Arrangement::default(), &[]);
    assert!(got[0].iter().all(|e| e.item != Item::ClearLead));
    assert_eq!(p.lead_note(), "Starts a new lead in place of @a and @b. To make an existing conversation the lead, use Make Project Lead on it.");
}

#[test]
fn a_mirrored_project_does_not_take_the_tools_setting() {
    let p = record(json!({"id": "h2::p1", "mcp": "off"}));
    assert!(entries(&p, &Arrangement::default(), &[]).iter().flatten().all(|e| !matches!(e.item, Item::Tools { .. })));
    let mine = record(json!({"id": "p1", "mcp": "off"}));
    assert!(entries(&mine, &Arrangement::default(), &[]).iter().flatten().any(|e| e.item == Item::Tools { off: true } && e.label == "Give Terminal Sessions the Agenthail Tools"));
}

#[test]
fn checkouts_on_several_computers_are_named_by_host() {
    let p = record(json!({"id": "p1", "displayName": "App", "checkouts": [
        here("p1", true),
        {"host": {"id": "h2", "name": "forge", "state": "connected"}, "projectId": "h2::p9", "rootPath": "/home/a/app", "added": true},
        {"host": {"id": "h3", "name": "old", "state": "unreachable"}, "projectId": "h3::p9", "rootPath": "/a", "added": false},
    ]}));
    let removes: Vec<(String, Item)> = entries(&p, &Arrangement::default(), &[]).into_iter().flatten().filter(|e| matches!(e.item, Item::Remove { .. })).map(|e| (e.label, e.item)).collect();
    assert_eq!(
        removes,
        vec![
            ("Remove on mac…".into(), Item::Remove { project_id: "p1".into(), place: " on mac".into() }),
            ("Remove on forge…".into(), Item::Remove { project_id: "h2::p9".into(), place: " on forge".into() }),
        ]
    );
    let starts: Vec<Item> = entries(&p, &Arrangement::default(), &[]).into_iter().flatten().filter(|e| matches!(e.item, Item::NewConversation { .. })).map(|e| e.item).collect();
    assert_eq!(
        starts,
        vec![
            Item::NewConversation { host: "".into(), folder: "/w/app".into() },
            Item::NewConversation { host: "h2".into(), folder: "/home/a/app".into() },
            Item::NewConversation { host: "h3".into(), folder: "/a".into() },
        ]
    );
    assert_eq!(p.lead_places().len(), 2, "an unreachable computer can't start a lead");
    assert_eq!(p.lead_places()[1].host.target(), "h2");
    assert_eq!(p.lead_places()[0].host.target(), "");
}

#[test]
fn the_edge_projects_cannot_step_past_the_end_of_their_group() {
    let tiles = Arrangement { order: ids(&["a", "b", "c"]), pinned: ["a".to_string()].into() };
    let arranged = ids(&["a", "b", "c"]);
    let enabled = |id: &str| {
        let p = record(json!({"id": id}));
        entries(&p, &tiles, &arranged)[1].iter().map(|e| e.enabled).collect::<Vec<_>>()
    };
    assert_eq!(enabled("a"), vec![true, false, false], "a lone pinned project has no neighbour");
    assert_eq!(enabled("b"), vec![true, false, true]);
    assert_eq!(enabled("c"), vec![true, true, false]);
    let p = record(json!({"id": "a"}));
    assert_eq!(entries(&p, &tiles, &arranged)[1][0].label, "Unpin");
}

#[test]
fn rename_needs_a_new_name_of_at_most_120_characters() {
    assert!(rename_problem("  ", "App").is_some());
    assert!(rename_problem(&"x".repeat(121), "App").is_some());
    assert_eq!(rename_problem(" App ", "App"), Some(""));
    assert_eq!(rename_problem(&"e".repeat(120), "App"), None);
    assert!(rename_problem(&"é".repeat(61), "App").is_some(), "the daemon counts bytes");
}

#[test]
fn pin_moves_and_steps_follow_the_mac_arrangement() {
    let none = Arrangement::default();
    let arranged = ids(&["a", "b", "c"]);
    let pinned = none.applying(&TileChange::Pin("c".into(), true), &arranged);
    assert_eq!(pinned.order, arranged, "pinning fixes the shown order");
    assert!(pinned.pinned.contains("c"));
    let moved = pinned.applying(&TileChange::Move("a".into(), "c".into()), &arranged);
    assert_eq!(moved.order, ids(&["b", "a", "c"]));
    assert!(moved.pinned.contains("a"), "moving onto a pinned project pins it");
    let back = moved.applying(&TileChange::Move("a".into(), "b".into()), &ids(&["c", "a", "b"]));
    assert!(!back.pinned.contains("a"));
    let shown = ids(&["a", "b", "c"]);
    let plain = Arrangement { order: shown.clone(), pinned: Default::default() };
    assert_eq!(plain.applying(&TileChange::Step("b".into(), -1), &shown).order, ids(&["b", "a", "c"]));
    assert_eq!(plain.applying(&TileChange::Step("b".into(), 1), &shown).order, ids(&["a", "c", "b"]));
    assert_eq!(plain.applying(&TileChange::Step("a".into(), -1), &shown), plain);
    let extra = Arrangement { order: ids(&["gone", "a"]), pinned: Default::default() };
    assert_eq!(extra.applying(&TileChange::Pin("b".into(), true), &ids(&["a", "b"])).order, ids(&["a", "b", "gone"]), "unshown projects keep their place at the end");
}

#[test]
fn the_list_sorts_pinned_then_by_order_then_by_name() {
    let tiles = Arrangement { order: ids(&["c", "a"]), pinned: ["b".to_string()].into() };
    let mut list = vec![("a", "Alpha"), ("b", "Beta"), ("c", "Gamma"), ("z", "apple"), ("y", "Zed")];
    tiles.sort(&mut list, |x| x.0, |x| x.1);
    assert_eq!(list.iter().map(|x| x.0).collect::<Vec<_>>(), vec!["b", "c", "a", "z", "y"]);
    let mut activity = vec![("y", "Zed"), ("a", "Alpha")];
    Arrangement::default().sort(&mut activity, |x| x.0, |x| x.1);
    assert_eq!(activity[0].0, "y", "with no order set the list keeps its activity order");
}

fn order(o: &[&str], pinned: &[&str], revision: i64) -> TileOrder {
    TileOrder { order: ids(o), pinned: ids(pinned), revision }
}

#[test]
fn the_daemons_order_is_taken_and_a_change_waits_until_it_is_heard() {
    let mut tiles = Tiles::default();
    assert_eq!(tiles.change(&TileChange::Pin("a".into(), true), &ids(&["a"])), None, "nothing is sent before the daemon's order is read");
    assert!(tiles.arrangement.pinned.contains("a"), "the change shows at once");
    let sent = tiles.adopt(Some(&order(&["b", "a"], &[], 4))).expect("the unsent change goes out on the daemon's revision");
    assert_eq!(sent, order(&["a"], &["a"], 4));
    assert!(tiles.sending());
    assert_eq!(tiles.adopt(Some(&order(&["b"], &[], 5))), None, "a read while a write is out is ignored");
    assert_eq!(tiles.answered(TileReply::Saved(order(&["a"], &["a"], 5))), None);
    assert_eq!(tiles.adopt(Some(&order(&["a"], &["a"], 5))), None);
    assert_eq!(tiles.arrangement.order, ids(&["a"]));
    tiles.adopt(Some(&order(&["x", "a"], &[], 6)));
    assert_eq!(tiles.arrangement.order, ids(&["x", "a"]), "a newer order from another device replaces this one");
    assert_eq!(tiles.adopt(Some(&order(&["old"], &[], 2))), None);
    assert_eq!(tiles.arrangement.order, ids(&["x", "a"]), "an older order is ignored");
}

#[test]
fn a_change_made_while_sending_goes_out_after_the_write_lands() {
    let mut tiles = Tiles::default();
    tiles.adopt(Some(&order(&["a", "b"], &[], 1)));
    let first = tiles.change(&TileChange::Pin("b".into(), true), &ids(&["a", "b"])).unwrap();
    assert_eq!(first.revision, 1);
    assert_eq!(tiles.change(&TileChange::Pin("a".into(), true), &ids(&["b", "a"])), None);
    let second = tiles.answered(TileReply::Saved(TileOrder { revision: 2, ..first })).expect("the later change follows");
    assert_eq!((second.revision, second.pinned.clone()), (2, ids(&["a", "b"])));
    assert_eq!(tiles.answered(TileReply::Saved(second)), None);
}

#[test]
fn a_stale_write_is_tried_once_on_top_of_the_newer_order_then_the_daemon_wins() {
    let mut tiles = Tiles::default();
    tiles.adopt(Some(&order(&["a", "b"], &[], 1)));
    tiles.change(&TileChange::Pin("b".into(), true), &ids(&["a", "b"])).unwrap();
    let again = tiles.answered(TileReply::Stale(order(&["c", "a", "b"], &[], 3))).expect("one retry");
    assert_eq!((again.revision, again.pinned.clone()), (3, ids(&["b"])));
    assert_eq!(tiles.answered(TileReply::Stale(order(&["c"], &["c"], 4))), None);
    assert_eq!(tiles.arrangement, Arrangement { order: ids(&["c"]), pinned: ["c".to_string()].into() });
}

#[test]
fn a_failed_write_keeps_the_change_and_says_so() {
    let mut tiles = Tiles::default();
    tiles.adopt(Some(&order(&["a", "b"], &[], 1)));
    tiles.change(&TileChange::Step("b".into(), -1), &ids(&["a", "b"])).unwrap();
    assert_eq!(tiles.answered(TileReply::Failed("timed out".into())), None);
    assert_eq!(tiles.arrangement.order, ids(&["b", "a"]));
    assert_eq!(tiles.error.as_deref(), Some("The project order didn't save: timed out"));
    let resent = tiles.adopt(Some(&order(&["a", "b"], &[], 1))).expect("the next read sends it again");
    assert_eq!(resent.order, ids(&["b", "a"]));
}

#[test]
fn merged_worktrees_offer_remove_check_or_delete_the_ignored_files() {
    let w = |v: Value| serde_json::from_value::<Worktree>(v).unwrap();
    let merged = w(json!({"path": "/w/.worktrees/x", "branch": "x", "state": "settled", "settledReason": "merged"}));
    assert!(merged.merged());
    assert_eq!(merged.offer(), Offer::Remove);
    assert!(!w(json!({"state": "settled", "settledReason": "merged", "cleanup": "removed"})).merged());
    assert!(!w(json!({"state": "active"})).merged());
    let ignored = w(json!({"branch": "x", "state": "settled", "settledReason": "merged", "cleanup": "kept", "cleanupReason": "ignored", "keptPaths": [".env"], "hint": {"reason": "It holds ignored files."}}));
    assert_eq!(ignored.offer(), Offer::Ignored(ids(&[".env"])));
    assert_eq!(ignored.outcome(), "Kept: It holds ignored files.");
    assert_eq!(confirm_ignored(&ignored, &ids(&[".env"])).message, "Remove x anyway? It deletes this ignored file with the worktree: .env");
    assert_eq!(w(json!({"cleanup": "kept", "cleanupReason": "dirty"})).offer(), Offer::Check);
    assert_eq!(w(json!({"cleanup": "removed", "branch": "x"})).outcome(), "Removed x.");
}

#[test]
fn each_project_action_sends_the_body_its_handler_reads() {
    let ok = r#"{"ok":true,"coordinator":{"name":"lead","projectRoot":"/w/app"},"project":{"id":"p9","displayName":"App"},"created":false,"worktree":{"cleanup":"removed","branch":"x"}}"#;
    let (client, seen) = daemon(replies(&[(200, ok); 15]));
    let added = client.project_add("/w/app", "", "").unwrap();
    assert_eq!(added.notice(), "App is already a project.");
    client.project_add("/w/app", "App", "h2").unwrap();
    client.project_rename("p1", "New").unwrap();
    client.project_remove("p1").unwrap();
    client.project_archive("p1", true).unwrap();
    client.project_archive("p1", false).unwrap();
    client.project_tools("p1", false).unwrap();
    assert_eq!(client.lead_start("codex", "/w/app", "", "", false).unwrap(), Lead { name: "lead".into(), project_root: "/w/app".into() });
    client.lead_start("claude", "/w/app", "h2", "boss", true).unwrap();
    client.lead_set("s1", false).unwrap();
    client.lead_set("s1", true).unwrap();
    client.lead_clear("p1").unwrap();
    client.lead_send("p1", "hi").unwrap();
    assert_eq!(client.work_clean("s1", &[]).unwrap().outcome(), "Removed x.");
    client.work_clean("s1", &ids(&[".env"])).unwrap();
    let want = [
        json!({"action": "project-add", "path": "/w/app"}),
        json!({"action": "project-add", "path": "/w/app", "title": "App", "host": "h2"}),
        json!({"action": "project-rename", "projectId": "p1", "title": "New"}),
        json!({"action": "project-remove", "projectId": "p1"}),
        json!({"action": "project-archive", "projectId": "p1"}),
        json!({"action": "project-unarchive", "projectId": "p1"}),
        json!({"action": "project-setting", "projectId": "p1", "setting": "mcp", "value": "off"}),
        json!({"action": "coordinator-new", "surface": "codex", "cwd": "/w/app"}),
        json!({"action": "coordinator-new", "surface": "claude", "cwd": "/w/app", "host": "h2", "name": "boss", "replace": true}),
        json!({"action": "coordinator-set", "sessionId": "s1"}),
        json!({"action": "coordinator-set", "sessionId": "s1", "replace": true}),
        json!({"action": "coordinator-clear", "projectId": "p1"}),
        json!({"action": "coordinator-send", "project": "p1", "message": "hi", "author": "person"}),
        json!({"action": "work-clean", "sessionId": "s1"}),
        json!({"action": "work-clean", "sessionId": "s1", "removeIgnored": [".env"]}),
    ];
    for body in want {
        let (path, got) = seen.recv().unwrap();
        assert_eq!((path.as_str(), got), ("/api/v1/actions", body));
    }
}

#[test]
fn a_lead_already_in_place_asks_to_replace_it() {
    let (client, _seen) = daemon(replies(&[
        (409, r#"{"error":{"code":"coordinator_exists","message":"/w/app already has coordinator @old; replace it to take over"}}"#),
        (400, r#"{"error":{"code":"invalid_request","message":"a coordinator needs an absolute folder on its host"}}"#),
    ]));
    let e = client.lead_set("s1", false).unwrap_err();
    assert!(needs_replace(&e));
    assert_eq!(confirm_replace(&e), Confirm { message: "/w/app already has coordinator @old; replace it to take over. Replace it?".into(), action: "Replace" });
    assert!(!needs_replace(&client.lead_start("claude", "x", "", "", false).unwrap_err()));
}

#[test]
fn the_tile_order_is_put_with_its_revision_and_a_conflict_returns_the_current_one() {
    let (client, seen) = daemon(replies(&[
        (200, r#"{"tileOrder":{"order":["a"],"pinned":[],"revision":8}}"#),
        (409, r#"{"error":{"code":"conflict","message":"changed"},"tileOrder":{"order":["b"],"pinned":["b"],"revision":9}}"#),
        (500, r#"{"error":{"code":"internal_error","message":"disk full"}}"#),
    ]));
    assert_eq!(client.put_tiles(&order(&["a"], &[], 7)), TileReply::Saved(order(&["a"], &[], 8)));
    let (path, body) = seen.recv().unwrap();
    assert_eq!((path.as_str(), body), ("/api/v1/projects/tiles", json!({"order": ["a"], "pinned": [], "revision": 7})));
    assert_eq!(client.put_tiles(&order(&["a"], &[], 7)), TileReply::Stale(order(&["b"], &["b"], 9)));
    assert_eq!(client.put_tiles(&order(&["a"], &[], 7)), TileReply::Failed("disk full".into()));
}

#[test]
fn a_snapshot_project_reads_its_checkouts_tools_and_lead() {
    let snap = std::fs::read_to_string(concat!(env!("CARGO_MANIFEST_DIR"), "/tests/project_snapshot.json")).unwrap();
    let v: Value = serde_json::from_str(&snap).unwrap();
    let p = record(v["projects"][0].clone());
    assert_eq!((p.name(), p.checkouts.len(), p.tools_off()), ("app", 2, Some(false)));
    assert_eq!(p.coordinators[0].surface, "claude");
    assert_eq!(p.coordinators[0].host.as_ref().map(|h| h.local()), Some(true));
    let tiles: TileOrder = serde_json::from_value(v["tileOrder"].clone()).unwrap();
    assert_eq!(tiles.revision, 3);
}

#[test]
fn an_added_project_or_one_with_a_lead_is_listed_before_it_has_sessions() {
    let world = crate::derive::World::from_json(
        &json!({"updatedAt": "2026-11-27T02:00:00Z", "projects": [
            {"id": "p1", "displayName": "New", "checkouts": [here("p1", true)], "lastActive": "2026-11-26T02:00:00Z"},
            {"id": "p2", "displayName": "Led", "coordinators": [{"name": "lead"}]},
            {"id": "implicit:/w/x", "displayName": "x", "checkouts": [here("implicit:/w/x", false)]},
        ], "tileOrder": {"order": ["p2"], "pinned": [], "revision": 1}})
        .to_string(),
    )
    .unwrap();
    assert_eq!(world.projects.iter().map(|p| p.key.as_str()).collect::<Vec<_>>(), vec!["p1", "p2"]);
    assert_eq!(world.project("p1").unwrap().last_active, "2026-11-26T02:00:00Z");
    assert_eq!(world.record("implicit:/w/x").map(|r| r.name()), Some("x"));
    assert_eq!(world.tile_order.as_ref().map(|t| t.revision), Some(1));
}

#[test]
fn a_remote_lead_is_found_under_its_hosts_session_id_and_a_new_one_waits_for_its_first_message() {
    let world = crate::derive::World::from_json(
        &json!({"updatedAt": "2026-11-27T02:00:00Z",
            "sessions": [{"id": "h2::s9", "project": {"id": "p1"}, "lastActive": "2026-11-27T01:00:00Z"}],
            "projects": [
                {"id": "p1", "displayName": "Far", "coordinators": [{"name": "far", "surface": "codex", "sessionId": "s9", "host": {"id": "h2", "name": "forge", "state": "connected"}}]},
                {"id": "p2", "displayName": "New", "coordinators": [{"name": "fresh", "surface": "claude"}]},
                {"id": "p3", "displayName": "Gone", "coordinators": [{"name": "old", "surface": "claude", "sessionId": "s0"}]},
            ]})
        .to_string(),
    )
    .unwrap();
    assert_eq!(world.project("p1").unwrap().lead.as_deref(), Some("h2::s9"));
    assert_eq!(world.project("p1").unwrap().lead_pending, None);
    assert_eq!(world.project("p2").unwrap().lead_pending, Some(("Claude".to_string(), "starts with the first message sent to it")));
    assert_eq!(world.project("p3").unwrap().lead_pending, Some(("Claude".to_string(), "not running · the next message picks it up")));
    let here = |id: &str, host: Option<Value>| serde_json::from_value::<crate::derive::world::Coordinator>(json!({"sessionId": id, "host": host})).unwrap();
    assert_eq!(lead_session_id(&here("s1", None)).as_deref(), Some("s1"));
    assert_eq!(lead_session_id(&here("s1", Some(json!({"id": "h1", "state": "local"})))).as_deref(), Some("s1"));
    assert_eq!(lead_session_id(&here("h2::s1", Some(json!({"id": "h2", "state": "connected"})))).as_deref(), Some("h2::s1"));
    assert_eq!(lead_session_id(&here("", None)), None);
}

#[test]
fn a_saved_order_survives_a_restart_and_an_unsent_change_goes_out_first() {
    let mut tiles = Tiles::default();
    tiles.adopt(Some(&order(&["a", "b"], &[], 2)));
    tiles.change(&TileChange::Pin("b".into(), true), &ids(&["a", "b"])).unwrap();
    tiles.answered(TileReply::Failed("offline".into()));
    let saved = tiles.saved();
    assert!(saved.unsent);
    let mut again = Tiles::restore(serde_json::from_value(serde_json::to_value(&saved).unwrap()).unwrap());
    assert!(again.arrangement.pinned.contains("b"));
    assert_eq!(again.adopt(Some(&order(&["a", "b"], &[], 2))), Some(order(&["a", "b"], &["b"], 2)));
    let mut first = Tiles::restore(Saved { order: ids(&["x"]), pinned: vec![], revision: 0, unsent: false });
    assert_eq!(first.adopt(Some(&TileOrder::default())), Some(order(&["x"], &[], 0)), "an order kept here goes to a daemon that has none");
}

#[test]
fn a_failed_change_is_sent_again_on_the_next_fresh_read_even_when_the_order_is_unchanged() {
    let mut tiles = Tiles::default();
    let daemon = order(&["a", "b"], &[], 1);
    assert_eq!(tiles.poll(Some(&daemon), true), None);
    tiles.change(&TileChange::Step("b".into(), -1), &ids(&["a", "b"])).unwrap();
    tiles.answered(TileReply::Failed("timed out".into()));
    assert_eq!(tiles.poll(Some(&daemon), false), None, "the same read again sends nothing");
    assert_eq!(tiles.poll(Some(&daemon), true).map(|o| o.order), Some(ids(&["b", "a"])));
    assert_eq!(tiles.poll(Some(&daemon), true), None, "nothing more while it is out");
    tiles.answered(TileReply::Saved(order(&["b", "a"], &[], 2)));
    assert_eq!(tiles.poll(Some(&daemon), true), None, "nothing is left to send");
}

#[test]
fn a_newer_order_seen_while_a_write_is_out_is_taken_once_the_write_lands() {
    let mut tiles = Tiles::default();
    tiles.poll(Some(&order(&["a", "b"], &[], 1)), true);
    tiles.change(&TileChange::Pin("b".into(), true), &ids(&["a", "b"])).unwrap();
    let newer = order(&["c", "b", "a"], &[], 3);
    assert_eq!(tiles.poll(Some(&newer), true), None);
    tiles.answered(TileReply::Saved(order(&["a", "b"], &["b"], 2)));
    assert_eq!(tiles.poll(Some(&newer), false), None);
    assert_eq!(tiles.arrangement.order, ids(&["c", "b", "a"]), "the newer order from elsewhere wins");
}

#[test]
fn an_action_the_daemon_only_took_is_not_reported_as_done() {
    let submitted = r#"{"ok":true,"status":"submitted"}"#;
    let (client, _seen) = daemon(replies(&[(202, submitted); 4]));
    assert_eq!(client.lead_send("p1", "hi").unwrap(), Delivered::Unconfirmed);
    for e in [client.project_remove("p1").unwrap_err(), client.lead_set("s1", false).unwrap_err(), client.work_clean("s1", &[]).unwrap_err()] {
        assert!(e.maybe_delivered && e.code == "submitted", "{e:?}");
    }
}

#[test]
fn a_project_without_sessions_starts_new_work_in_its_own_folder_here_first() {
    let p = record(json!({"id": "p1", "checkouts": [
        {"host": {"id": "h2", "name": "forge", "state": "connected"}, "projectId": "h2::p1", "rootPath": "/home/a/app", "added": true},
        {"host": {"id": "h1", "name": "mac", "state": "local"}, "projectId": "p1", "rootPath": "/w/app", "added": true},
    ]}));
    let c = p.start_place().unwrap();
    assert_eq!((c.host.target(), c.root_path.as_str()), ("", "/w/app"));
    let far = record(json!({"id": "p2", "checkouts": [
        {"host": {"id": "h3", "name": "old", "state": "unreachable"}, "projectId": "h3::p2", "rootPath": "/x"},
        {"host": {"id": "h2", "name": "forge", "state": "connected"}, "projectId": "h2::p2", "rootPath": "/home/a/p2"},
    ]}));
    assert_eq!(far.start_place().map(|c| c.host.target()), Some("h2"));
    assert_eq!(record(json!({"id": "p3"})).start_place(), None);
}

#[test]
fn computers_with_the_same_name_get_distinct_labels() {
    let labels = host_labels([("h1", "mac.local"), ("h2", "mac.local"), ("h3", "forge.lan"), ("h4", "studio.a"), ("h5", "studio.b")].into_iter());
    assert_eq!(labels["h1"], "mac.local (h1)");
    assert_eq!(labels["h2"], "mac.local (h2)");
    assert_eq!(labels["h3"], "forge");
    assert_eq!((labels["h4"].as_str(), labels["h5"].as_str()), ("studio.a", "studio.b"));
    let p = record(json!({"id": "p1", "checkouts": [
        {"host": {"id": "h1", "name": "mac", "state": "local"}, "projectId": "p1", "rootPath": "/a", "added": true},
        {"host": {"id": "h2", "name": "mac", "state": "connected"}, "projectId": "h2::p1", "rootPath": "/b", "added": true},
    ]}));
    let removes: Vec<String> = entries(&p, &Arrangement::default(), &[]).into_iter().flatten().filter(|e| matches!(e.item, Item::Remove { .. })).map(|e| e.label).collect();
    assert_eq!(removes, vec!["Remove on mac (h1)…", "Remove on mac (h2)…"]);
}

#[test]
fn ordering_keeps_working_while_a_save_is_out() {
    let mut tiles = Tiles::default();
    tiles.poll(Some(&order(&["a", "b", "c"], &[], 1)), true);
    let first = tiles.change(&TileChange::Pin("c".into(), true), &ids(&["a", "b", "c"])).unwrap();
    let p = record(json!({"id": "a"}));
    assert!(entries(&p, &tiles.arrangement, &ids(&["c", "a", "b"]))[1].iter().all(|e| e.label != "Move Later" || e.enabled), "the menu stays usable");
    assert_eq!(tiles.change(&TileChange::Step("a".into(), 1), &ids(&["c", "a", "b"])), None, "it waits for the save");
    let next = tiles.answered(TileReply::Saved(TileOrder { revision: 2, ..first })).unwrap();
    assert_eq!(next.order, ids(&["c", "b", "a"]));
}

#[test]
fn an_answer_without_its_result_is_not_reported_as_done() {
    let (client, _seen) = daemon(replies(&[(200, r#"{"ok":true}"#), (200, r#"{"ok":true,"coordinator":{"name":""}}"#), (200, r#"{"ok":true,"worktree":"x"}"#)]));
    for e in [client.lead_set("s1", false).unwrap_err(), client.lead_start("claude", "/w", "", "", false).unwrap_err(), client.work_clean("s1", &[]).unwrap_err()] {
        assert!(e.maybe_delivered && e.code == "unreadable_reply", "{e:?}");
    }
}

#[test]
fn a_change_that_failed_on_some_computers_says_where() {
    let body = r#"{"ok":false,"projectId":"p1","checkouts":[{"host":"mac","ok":true},{"host":"forge","ok":false,"error":"host is no longer paired"}],"archived":true}"#;
    let (client, _seen) = daemon(replies(&[(502, body), (502, r#"{"ok":false,"checkouts":[{"host":"forge","ok":false}]}"#)]));
    let e = client.project_archive("p1", true).unwrap_err();
    assert_eq!((e.code.as_str(), e.maybe_delivered), ("partial", false));
    assert_eq!(e.message, "It changed on mac but failed on forge (host is no longer paired)");
    assert_eq!(client.project_rename("p1", "New").unwrap_err().message, "It failed on forge (no reason given)");
}

#[test]
fn a_lead_starts_where_chosen_or_on_the_one_computer_left() {
    let two = |state: &str| record(json!({"id": "p1", "checkouts": [
        {"host": {"id": "h1", "name": "mac", "state": "local"}, "projectId": "p1", "rootPath": "/w/app"},
        {"host": {"id": "h2", "name": "forge", "state": state}, "projectId": "h2::p1", "rootPath": "/home/a/app"},
        {"host": {"id": "h3", "name": "spare", "state": "connected"}, "projectId": "h3::p1", "rootPath": "/s/app", "archived": true},
    ]}));
    let both = two("connected");
    assert_eq!(both.lead_place(&both.checkouts[1].key()).map(|c| c.host.id.as_str()), Some("h2"));
    assert_eq!(both.lead_place("gone"), None, "with a choice left, nothing is picked for you");
    let one = two("unreachable");
    assert_eq!(one.lead_place(&one.checkouts[1].key()).map(|c| c.host.id.as_str()), Some("h1"), "the one computer left is used and shown");
    let same = record(json!({"id": "p1", "checkouts": [
        {"host": {"id": "h1", "name": "mac", "state": "local"}, "projectId": "implicit:/w", "rootPath": "/w"},
        {"host": {"id": "h2", "name": "forge", "state": "connected"}, "projectId": "implicit:/w", "rootPath": "/w"},
    ]}));
    assert_eq!(same.lead_place(&same.checkouts[1].key()).map(|c| c.host.id.as_str()), Some("h2"), "the same folder id on two computers stays apart");
}

#[test]
fn a_message_to_a_lead_says_whether_it_arrived_or_waits() {
    let (client, _seen) = daemon(replies(&[
        (200, r#"{"ok":true,"result":{"evidence":"delivered","status":"sent","sessionId":"s1"},"started":true}"#),
        (200, r#"{"ok":true,"result":{"evidence":"queued","queueId":4}}"#),
        (200, r#"{"ok":true,"result":{"evidence":"queued","queueId":5,"waitingOn":{"kind":"ask"}}}"#),
    ]));
    let notes: Vec<String> = (0..3).map(|_| lead_sent("@lead", &client.lead_send("p1", "hi").unwrap())).collect();
    assert_eq!(notes, vec!["Sent to @lead.", "Queued for @lead; it goes when @lead is free.", "Queued for @lead; it goes once @lead's waiting question is answered."]);
}

#[test]
fn a_lead_message_without_a_receipt_is_unconfirmed() {
    let (client, _seen) = daemon(replies(&[(200, r#"{"ok":true}"#), (200, r#"{"ok":true,"result":null}"#), (200, r#"{"ok":true,"result":{}}"#), (200, r#"{"ok":true,"result":"x"}"#), (200, r#"{"ok":true,"result":{"evidence":"queued","queueId":"bad"}}"#)]));
    for _ in 0..5 {
        assert_eq!(client.lead_send("p1", "hi").unwrap(), Delivered::Unconfirmed);
    }
}

#[test]
fn a_tile_answer_without_a_revision_is_not_taken() {
    let (client, _seen) = daemon(replies(&[(200, r#"{"tileOrder":{}}"#), (409, r#"{"error":{"code":"conflict","message":"changed"},"tileOrder":{}}"#)]));
    assert!(matches!(client.put_tiles(&order(&["a"], &[], 1)), TileReply::Failed(_)));
    assert_eq!(client.put_tiles(&order(&["a"], &[], 1)), TileReply::Failed("changed".into()));
}
