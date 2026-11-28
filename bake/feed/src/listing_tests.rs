use super::*;
use crate::mock::{daemon, replies};
use serde_json::{Value, json};

fn session(id: &str, path: &str, branch: Option<&str>, dirty: bool) -> Session {
    serde_json::from_value(json!({"id": id, "checkout": {"path": path, "branch": branch, "dirty": dirty}, "host": {"id": "h1", "name": "mini", "state": "local"}})).unwrap()
}

fn family(id: &str, project: &str, checkout: &str, members: &[(&str, u8)]) -> Value {
    json!({"id": id, "rootId": id, "projectId": project, "checkoutId": checkout, "members": members.iter().map(|(m, d)| json!({"id": m, "depth": d, "contextOnly": false, "pinned": false})).collect::<Vec<_>>()})
}

fn view(projects: Value, families: Vec<Value>) -> View {
    serde_json::from_value(json!({"counts": {"all": {"families": 9, "sessions": 12}, "running": {"families": 2, "sessions": 2}}, "projects": projects, "families": families})).unwrap()
}

#[test]
fn a_request_reads_the_scoped_view_with_members_and_only_the_filters_set() {
    let plain = Request { scope: "current".into(), ..Request::default() };
    assert_eq!(plain.path(None, PAGE), "/api/v1/snapshot?scope=current&members=1&limit=50");
    let full = Request { scope: "all".into(), project: Some("repo:x/y".into()), surface: Some("codex".into()), status: Some("waiting".into()), query: "  fix tabs ".into() };
    assert_eq!(full.path(Some("c 2"), 200), "/api/v1/snapshot?scope=all&members=1&limit=200&projectId=repo%3Ax%2Fy&surface=codex&status=waiting&q=fix+tabs&cursor=c+2");
    assert!(full.refined() && !plain.refined());
}

#[test]
fn pages_append_families_and_rows_and_a_reread_reads_as_far_as_was_shown() {
    let first = json!({"sessions": [{"id": "a"}], "nextCursor": "n1", "view": {"counts": {"all": {"families": 3, "sessions": 3}}, "projects": [{"id": "p", "name": "P", "counts": {"families": 3, "sessions": 3}, "collapsedFamilyIds": ["a"]}], "families": [family("a", "p", "", &[("a", 0)])]}}).to_string();
    let second = json!({"sessions": [{"id": "b"}, {"id": "c"}], "view": {"counts": {}, "projects": [], "families": [family("b", "p", "", &[("b", 0)]), family("a", "p", "", &[("a", 0)]), family("c", "p", "", &[("c", 0)])]}}).to_string();
    let (client, seen) = daemon(replies(&[(200, &first), (200, &second), (200, &first), (200, &second)]));
    let request = Request { scope: "all".into(), ..Request::default() };
    let mut listing = Listing::first(client.listing_page(&request, None).unwrap());
    assert_eq!(listing.next.as_deref(), Some("n1"));
    listing.append(client.listing_page(&request, listing.next.clone().as_deref()).unwrap());
    let ids: Vec<&str> = listing.view.families.iter().map(|f| f.id.as_str()).collect();
    assert_eq!((ids, listing.rows.len(), listing.next.clone()), (vec!["a", "b", "c"], 3, None));
    assert_eq!(listing.view.count("all"), Some(3), "counts come from the first page");
    assert_eq!(seen.recv().unwrap().0, "/api/v1/snapshot?scope=all&members=1&limit=50");
    assert_eq!(seen.recv().unwrap().0, "/api/v1/snapshot?scope=all&members=1&limit=50&cursor=n1");
    let again = client.listing_reread(&request, 3).unwrap();
    assert_eq!(again.families(), 3);
    assert_eq!(seen.recv().unwrap().0, "/api/v1/snapshot?scope=all&members=1&limit=200");
    assert_eq!(seen.recv().unwrap().0, "/api/v1/snapshot?scope=all&members=1&limit=200&cursor=n1");
}

#[test]
fn a_failed_read_says_what_failed() {
    let (client, _seen) = daemon(replies(&[(400, r#"{"error":{"code":"invalid_view","message":"invalid display status"}}"#)]));
    let request = Request { scope: "all".into(), ..Request::default() };
    assert_eq!(client.listing_page(&request, None).unwrap_err(), "invalid display status");
}

#[test]
fn the_tree_groups_families_by_project_then_checkout_in_the_daemons_order() {
    let rows: HashMap<String, Session> = [
        session("lead", "/r/app", Some("main"), false),
        session("worker", "/r/app", Some("main"), false),
        session("tabs", "/r/app-tabs", Some("feature/tabs"), true),
        session("other", "/r/site", Some("main"), false),
    ]
    .into_iter()
    .map(|x| (x.id.clone(), x))
    .collect();
    let v = view(
        json!([{"id": "p2", "name": "site", "counts": {"families": 1, "sessions": 1}, "collapsedFamilyIds": ["other"]}, {"id": "p1", "name": "app", "counts": {"families": 2, "sessions": 3}, "collapsedFamilyIds": ["lead", "tabs"]}, {"id": "empty", "name": "gone", "counts": {"families": 4, "sessions": 4}, "collapsedFamilyIds": []}]),
        vec![family("lead", "p1", "c-main", &[("lead", 0), ("worker", 1), ("missing", 1)]), family("other", "p2", "c-site", &[("other", 0)]), family("tabs", "p1", "c-tabs", &[("tabs", 0)])],
    );
    let t = tree(&v, |id| rows.get(id));
    assert_eq!(t.iter().map(|p| p.name.as_str()).collect::<Vec<_>>(), vec!["site", "app"], "a project with no loaded families is left out");
    let app = &t[1];
    assert_eq!(app.checkouts.iter().map(|c| (c.label.as_str(), c.dirty)).collect::<Vec<_>>(), vec![("main", false), ("feature/tabs", true)]);
    assert!(app.shows_checkouts() && !t[0].shows_checkouts());
    let crew: Vec<(&str, u8)> = app.checkouts[0].families[0].rows.iter().map(|r| (r.session.id.as_str(), r.depth)).collect();
    assert_eq!(crew, vec![("lead", 0), ("worker", 1)], "rows the client lacks are skipped");
    assert_eq!((app.sessions_label(), t[0].sessions_label()), ("3 sessions".to_string(), "1 session".to_string()));
}

#[test]
fn the_same_default_branch_everywhere_shows_no_checkout_lines() {
    let rows: HashMap<String, Session> = [session("a", "/r/one", Some("main"), false), session("b", "/r/two", Some("main"), false), session("c", "/r/three", None, false)].into_iter().map(|x| (x.id.clone(), x)).collect();
    let v = view(json!([{"id": "p", "name": "p", "counts": {"families": 3, "sessions": 3}, "collapsedFamilyIds": ["a", "b", "c"]}]), vec![family("a", "p", "ca", &[("a", 0)]), family("b", "p", "cb", &[("b", 0)]), family("c", "p", "cc", &[("c", 0)])]);
    let t = tree(&v, |id| rows.get(id));
    assert!(!t[0].shows_checkouts());
}

#[test]
fn checkouts_without_a_branch_take_the_shortest_distinct_path() {
    let a = session("a", "/w/one/app", None, false);
    let b = session("b", "/w/two/app", None, false);
    let c = session("c", "/w/site", None, false);
    let d = session("d", "/w/x", Some("dev"), false);
    let mut e = session("e", "", None, false);
    e.checkout.path = None;
    assert_eq!(checkout_labels(&[&a, &b, &c, &d, &e]), vec!["one/app", "two/app", "site", "dev", "unknown checkout"]);
}

#[test]
fn a_remote_root_names_its_host_and_keeps_its_own_checkout() {
    let mut far: Session = session("far", "/r/app", Some("main"), false);
    far.host = serde_json::from_value(json!({"id": "h2", "name": "forge", "state": "connected"})).unwrap();
    let near = session("near", "/r/app", Some("main"), false);
    let rows: HashMap<String, Session> = [far, near].into_iter().map(|x| (x.id.clone(), x)).collect();
    let v = view(json!([{"id": "p", "name": "p", "counts": {"families": 2, "sessions": 2}, "collapsedFamilyIds": ["far", "near"]}]), vec![family("far", "p", "", &[("far", 0)]), family("near", "p", "", &[("near", 0)])]);
    let t = tree(&v, |id| rows.get(id));
    let hosts: Vec<(String, Option<String>)> = t[0].checkouts.iter().map(|c| (c.id.clone(), c.host.clone())).collect();
    assert_eq!(hosts, vec![("/r/app@h2".to_string(), Some("forge".to_string())), ("/r/app".to_string(), None)]);
    assert_eq!(t[0].hosts(), vec!["forge".to_string()]);
}

#[test]
fn a_project_shows_its_collapsed_families_and_the_selection_until_expanded() {
    let ids = ["f1", "f2", "f3", "f4"];
    let rows: HashMap<String, Session> = ids.iter().map(|id| (id.to_string(), session(id, "/r", Some("main"), false))).collect();
    let v = view(json!([{"id": "p", "name": "p", "counts": {"families": 6, "sessions": 6}, "collapsedFamilyIds": ["f1", "f2"]}]), ids.iter().map(|id| family(id, "p", "c", &[(id, 0)])).collect());
    let p = &tree(&v, |id| rows.get(id))[0];
    assert_eq!(p.shown(false, None).family_count(), 2);
    assert_eq!(p.more_label(false, None).as_deref(), Some("Show 4 more"), "the daemon's total, like the Mac app");
    assert_eq!(p.shown(false, Some("f4")).family_count(), 3);
    assert_eq!(p.more_label(false, Some("f4")).as_deref(), Some("Show 3 more"));
    assert_eq!(p.shown(true, None).family_count(), 4);
    assert_eq!(p.more_label(true, None).as_deref(), Some("Show less"));
    let small = view(json!([{"id": "p", "name": "p", "counts": {"families": 2, "sessions": 2}, "collapsedFamilyIds": ["f1", "f2"]}]), ids[..2].iter().map(|id| family(id, "p", "c", &[(id, 0)])).collect());
    let p = &tree(&small, |id| rows.get(id))[0];
    assert_eq!((p.more_label(false, None), p.more_label(true, None)), (None, None));
}

#[test]
fn a_project_with_unread_families_is_filled_from_its_own_scoped_read() {
    let first = Page { sessions: vec![session("a", "/r", None, false)], view: view(json!([{"id": "p", "name": "p", "counts": {"families": 3, "sessions": 3}, "collapsedFamilyIds": ["a"]}, {"id": "q", "name": "q", "counts": {"families": 1, "sessions": 1}, "collapsedFamilyIds": ["z"]}]), vec![family("a", "p", "c", &[("a", 0)]), family("z", "q", "c", &[("z", 0)])]), next: Some("n".into()) };
    let mut listing = Listing::first(first);
    assert_eq!(listing.short(), vec!["p".to_string()]);
    let body = json!({"sessions": [{"id": "b"}, {"id": "c"}], "view": {"projects": [], "families": [family("a", "p", "c", &[("a", 0)]), family("b", "p", "c", &[("b", 0)]), family("c", "p", "c", &[("c", 0)])]}}).to_string();
    let more = json!({"sessions": [], "nextCursor": "p2", "view": {"projects": [], "families": []}}).to_string();
    let (client, seen) = daemon(replies(&[(200, &more), (200, &body)]));
    let request = Request { scope: "all".into(), surface: Some("codex".into()), ..Request::default() };
    listing.fill(client.listing_project(&request, "p").unwrap());
    assert_eq!(seen.recv().unwrap().0, "/api/v1/snapshot?scope=all&members=1&limit=200&projectId=p&surface=codex");
    assert_eq!(seen.recv().unwrap().0, "/api/v1/snapshot?scope=all&members=1&limit=200&projectId=p&surface=codex&cursor=p2", "a project's later pages are read too");
    assert!(listing.short().is_empty());
    assert_eq!((listing.families(), listing.rows.len(), listing.next.as_deref(), listing.view.count("all")), (4, 3, Some("n"), Some(9)), "the list's own cursor and counts stay");
}

#[test]
fn project_choices_keep_ids_and_tell_apart_projects_with_one_name() {
    let known = [("p2", "site"), ("p1", "app"), ("", "No project"), ("p3", "app"), ("p1", "app")].map(|(i, n)| (i.to_string(), n.to_string()));
    let got = project_choices(&known);
    assert_eq!(got, vec![("p1".into(), "app (p1)".into()), ("p3".into(), "app (p3)".into()), ("p2".into(), "site".into())]);
}

#[test]
fn status_labels_read_like_the_mac_app() {
    assert_eq!((status_label("waiting"), status_label("limited"), status_label("new")), ("Waiting on you".into(), "Usage limit".into(), "new".into()));
}

#[test]
fn working_chips_count_by_project_and_merge_names_in_count_order() {
    let busy = |id: &str, project: &str, name: &str| -> Session { serde_json::from_value(json!({"id": id, "status": "busy", "project": {"id": project, "name": name}})).unwrap() };
    let list = vec![busy("1", "a1", "A"), busy("2", "a1", "A"), busy("3", "a1", "A"), busy("4", "b", "B"), busy("5", "b", "B"), busy("6", "b", "B"), busy("7", "b", "B"), busy("8", "a2", "A"), busy("9", "a2", "A"), busy("10", "a2", "A")];
    let by_project = crate::derive::sessions::busy_by_project(&list);
    assert_eq!(by_project.iter().map(|b| (b.0.as_str(), b.2)).collect::<Vec<_>>(), vec![("b", 4), ("a1", 3), ("a2", 3)]);
    assert_eq!(crate::derive::sessions::busy_by_group(&list), vec![("A".to_string(), 6), ("B".to_string(), 4)]);
}

#[test]
fn a_session_list_with_null_lists_reads_as_empty() {
    let empty = json!({"sessions": null, "nextCursor": null, "view": {"counts": null, "surfaces": null, "refreshAt": null, "projects": null, "families": null}}).to_string();
    let rows = json!({"sessions": [{"id": "a", "heldWork": null, "prs": null, "cwd": null, "capabilities": null}], "view": {"counts": {}, "projects": [{"id": "p", "name": "P", "collapsedFamilyIds": null}], "families": [{"id": "a", "rootId": "a", "projectId": "p", "members": null}]}}).to_string();
    let (client, _seen) = daemon(replies(&[(200, &empty), (200, &rows)]));
    let request = Request { scope: "all".into(), ..Request::default() };
    let page = client.listing_page(&request, None).unwrap();
    assert!(page.sessions.is_empty() && page.view.families.is_empty() && page.view.projects.is_empty() && page.next.is_none());
    let page = client.listing_page(&request, None).unwrap();
    assert_eq!((page.sessions[0].held_work.len(), page.sessions[0].prs.len(), page.view.families[0].members.len()), (0, 0, 0));
}
