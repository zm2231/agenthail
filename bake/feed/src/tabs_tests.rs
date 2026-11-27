use super::*;

fn ids(t: &Tabs) -> Vec<&str> {
    t.tabs().iter().map(|t| t.id.as_str()).collect()
}

fn opened(list: &[&str]) -> Tabs {
    let mut t = Tabs::default();
    for id in list {
        t.open(id);
    }
    t
}

#[test]
fn opening_adds_beside_the_active_tab_and_switches() {
    let mut t = opened(&["a", "b"]);
    assert_eq!((ids(&t), t.active()), (vec!["a", "b"], Some("b")));
    t.open("a");
    assert_eq!((ids(&t), t.active()), (vec!["a", "b"], Some("a")));
    t.open("c");
    assert_eq!(ids(&t), ["a", "c", "b"]);
}

#[test]
fn fit_closes_least_recently_used_never_active_or_held() {
    let mut t = opened(&["a", "b", "c", "d"]);
    t.activate("a");
    t.activate("d");
    assert_eq!(t.fit(2, |id| id == "b"), ["c", "a"]);
    assert_eq!((ids(&t), t.active()), (vec!["b", "d"], Some("d")));
    assert!(t.fit(1, |_| true).is_empty());
    assert_eq!(ids(&t), ["b", "d"]);
    assert!(t.fit(5, |_| false).is_empty());
}

#[test]
fn closing_activates_right_then_left_neighbour() {
    let mut t = opened(&["a", "b"]);
    t.open("a");
    t.open("c");
    t.activate("c");
    t.close("c");
    assert_eq!((ids(&t), t.active()), (vec!["a", "b"], Some("b")));
    t.close("b");
    assert_eq!(t.active(), Some("a"));
    t.close("a");
    assert_eq!((t.tabs().is_empty(), t.active()), (true, None));
}

#[test]
fn stepping_wraps_and_close_others_keeps_one() {
    let mut t = opened(&["a", "b", "c"]);
    t.activate("c");
    assert_eq!((t.neighbour(1), t.neighbour(-1)), (Some("a"), Some("b")));
    t.close_others("b");
    assert_eq!((ids(&t), t.active()), (vec!["b"], Some("b")));
    assert_eq!(Tabs::default().neighbour(1), None);
}

#[test]
fn reconcile_follows_rekeys_drops_gone_and_merges() {
    let mut t = opened(&["a", "b", "c", "d"]);
    t.activate("b");
    let dropped = t.reconcile(|id| if id == "a" { "a2".into() } else { id.into() }, |id| id != "b");
    assert_eq!((ids(&t), dropped), (vec!["a2", "c", "d"], vec!["b".to_string()]));
    assert_eq!(t.active(), Some("c"));
    let mut m = opened(&["a", "b"]);
    m.reconcile(|id| if id == "b" { "a".into() } else { id.into() }, |_| true);
    assert_eq!((ids(&m), m.active()), (vec!["a"], Some("a")));
}

#[test]
fn a_started_session_replaces_the_new_session_tab_in_place() {
    let mut t = opened(&["a", NEW_SESSION, "b"]);
    t.replace(NEW_SESSION, "c");
    assert_eq!((ids(&t), t.active()), (vec!["a", "c", "b"], Some("c")));
    t.replace(NEW_SESSION, "a");
    assert_eq!((ids(&t), t.active()), (vec!["a", "c", "b"], Some("a")));
}

#[test]
fn round_trips_and_reads_what_the_mac_app_saved() {
    let mut t = opened(&["a", "b"]);
    t.name("a", Some("helper"), Some("Fix the login page"));
    assert_eq!(Tabs::from_json(&t.to_json()), Some(t.clone()));
    let older: serde_json::Value = serde_json::from_str(r#"{"tabs":[{"id":"a","kept":false},{"id":"b","kept":true}],"active":"b"}"#).unwrap();
    let back = Tabs::from_json(&older).unwrap();
    assert_eq!((ids(&back), back.active()), (vec!["a", "b"], Some("b")));
}

#[test]
fn names_survive_an_unknown_session_and_titles_follow_the_label() {
    let mut t = opened(&["n"]);
    t.name("n", Some("helper"), Some("Fix the login page"));
    t.name("n", None, None);
    let tab = &t.tabs()[0];
    assert_eq!(title(tab.alias.as_deref(), tab.name.as_deref(), &tab.id, "alias"), "@helper");
    assert_eq!(title(tab.alias.as_deref(), tab.name.as_deref(), &tab.id, "title"), "Fix the login page");
    assert_eq!(title(tab.alias.as_deref(), tab.name.as_deref(), &tab.id, "alias-title"), "@helper Fix the login page");
    assert_eq!(title(None, None, "x", "alias"), "Untitled conversation");
    assert_eq!(title(None, Some("x"), "x", "title"), "Untitled conversation");
    assert_eq!(title(None, Some("0b6f3a2e-1c4d-4e5f-8a9b-0c1d2e3f4a5b"), "x", "title"), "Untitled conversation");
    assert_eq!(title(None, Some("https://github.com/acme/site/pull/4"), "x", "title"), "acme / site");
    assert_eq!(title(None, None, NEW_SESSION, "alias"), "New Session");
}

#[test]
fn tabs_follow_the_titlebar_setting_and_fit_the_width() {
    let values = |v: serde_json::Value| v.as_object().unwrap().clone();
    assert_eq!(setting(&values(serde_json::json!({}))), Setting { on: true, label: "alias".into() });
    assert_eq!(setting(&values(serde_json::json!({"titlebar.tabs": "off", "titlebar.tabs.label": "title"}))), Setting { on: false, label: "title".into() });
    assert_eq!((capacity(0.0), capacity(380.0), capacity(1440.0)), (1, 2, 8));
}
