use std::collections::HashMap;

use time::OffsetDateTime;

use super::*;
use crate::catalog::Session;
use crate::decisions::{Decision, Inbox, Page};

fn now() -> OffsetDateTime {
    OffsetDateTime::from_unix_timestamp(1_795_694_400).unwrap()
}

fn session(json: &str) -> Session {
    serde_json::from_str(json).unwrap()
}

fn decision(json: &str) -> Decision {
    serde_json::from_str(json).unwrap()
}

fn sessions() -> Vec<Session> {
    vec![
        session(r#"{"id":"lead","alias":"lead","project":{"id":"web"},"turnCompletion":{"turnId":"t1","headline":"Shipped the fix","outcome":"finished","at":"2026-11-26T11:58:00Z"}}"#),
        session(r#"{"id":"worker","alias":"worker","project":{"id":"web"},"turnCompletion":{"turnId":"t2","outcome":"finished","at":"2026-11-26T11:59:00Z"}}"#),
        session(r#"{"id":"solo","name":"Solo run","project":{"id":"docs"},"turnCompletion":{"turnId":"t3","outcome":"failed","at":"2026-11-26T11:59:30Z"}}"#),
        session(r#"{"id":"old","name":"Old","turnCompletion":{"turnId":"t4","outcome":"finished","at":"2026-11-26T10:00:00Z"}}"#),
        session(r#"{"id":"asks","name":"Asks","turnCompletion":{"turnId":"t5","outcome":"needs_you","at":"2026-11-26T11:59:00Z"}}"#),
        session(r#"{"id":"gone","name":"Gone","archived":true,"turnCompletion":{"turnId":"t6","outcome":"finished","at":"2026-11-26T11:59:00Z"}}"#),
    ]
}

fn leads() -> HashMap<String, String> {
    HashMap::from([("web".to_string(), "lead".to_string())])
}

fn keys(notices: &[Notice]) -> Vec<&str> {
    notices.iter().map(|n| n.key.as_str()).collect()
}

#[test]
fn a_recent_finished_or_failed_turn_notifies_once_per_turn() {
    let found = finished(&sessions(), &leads(), &Prefs::default(), true, now());
    assert_eq!(keys(&found), ["done|lead|t1", "done|solo|t3"]);
    assert_eq!(found[0].title, "@lead");
    assert_eq!(found[0].thread, "@lead");
    assert_eq!(found[0].body, "Done: Shipped the fix");
    assert_eq!(found[1].body, "Failed");
    assert!(found.iter().all(|n| n.sound && n.kind == Kind::Finished && n.buttons.is_empty()));
}

#[test]
fn other_agents_in_a_led_project_finish_quietly_only_while_the_preference_is_on() {
    let off = Prefs { lead_completions_only: false };
    assert_eq!(keys(&finished(&sessions(), &leads(), &off, true, now())), ["done|lead|t1", "done|worker|t2", "done|solo|t3"]);
    assert_eq!(finished(&sessions(), &leads(), &off, true, now())[1].body, "Finished");
    assert_eq!(keys(&finished(&sessions(), &HashMap::new(), &Prefs::default(), true, now())), ["done|lead|t1", "done|worker|t2", "done|solo|t3"]);
}

#[test]
fn unread_preferences_keep_their_defaults_and_post_silently() {
    let found = finished(&sessions(), &leads(), &Prefs::default(), false, now());
    assert_eq!(keys(&found), ["done|lead|t1", "done|solo|t3"]);
    assert!(found.iter().all(|n| !n.sound));
}

#[test]
fn a_question_gets_a_button_per_option() {
    let d = decision(
        r#"{"sessionId":"lead","kind":"ask","answerable":true,"requests":[{"id":"r1","kind":"question","questions":[{"question":"Which layout?","options":[{"id":"a","label":"Grid (Recommended)"},{"id":"b","label":"List"}]}]}]}"#,
    );
    let found = needs(&[d], &sessions(), true);
    assert_eq!(found.len(), 1);
    let n = &found[0];
    assert_eq!(n.key, "need|lead|r1");
    assert_eq!((n.title.as_str(), n.body.as_str()), ("@lead", "Needs you: Which layout?"));
    assert_eq!(n.request.as_deref(), Some("r1"));
    assert_eq!(n.buttons, [("choice:a".to_string(), "Grid".to_string()), ("choice:b".to_string(), "List".to_string())]);
    assert_eq!(choice_of(&n.buttons[1].0), Some("b"));
    assert_eq!(choice_of(REPLY), None);
    assert!(n.sound);
}

#[test]
fn an_approval_gets_its_actions_or_allow_and_deny() {
    let d = decision(r#"{"sessionId":"worker","answerable":true,"requests":[{"id":"r2","tool":"Bash"},{"id":"r3","title":"Push to main","actions":[{"id":"once","title":"Allow once"},{"id":"never","title":"Never"}]}]}"#);
    let found = needs(&[d], &sessions(), false);
    assert_eq!(keys(&found), ["need|worker|r2", "need|worker|r3"]);
    assert_eq!(found[0].body, "Needs you: @worker wants to use Bash");
    assert_eq!(found[0].buttons, [("choice:allow".to_string(), "Allow".to_string()), ("choice:deny".to_string(), "Deny".to_string())]);
    assert_eq!(found[1].body, "Needs you: Push to main");
    assert_eq!(found[1].buttons[0], ("choice:once".to_string(), "Allow once".to_string()));
    assert!(!found[0].sound);
}

#[test]
fn several_questions_or_one_not_answerable_here_open_the_app_instead() {
    let compound = decision(
        r#"{"sessionId":"lead","answerable":true,"requests":[{"id":"r4","questions":[{"question":"One?","options":[{"id":"a","label":"A"}]},{"question":"Two?","options":[{"id":"b","label":"B"}]}]}]}"#,
    );
    let multi = decision(r#"{"sessionId":"lead","answerable":true,"requests":[{"id":"r5","questions":[{"question":"Pick any","multiSelect":true,"options":[{"id":"a","label":"A"}]}]}]}"#);
    let elsewhere = decision(r#"{"sessionId":"lead","answerable":false,"requests":[{"id":"r6","tool":"Bash"}]}"#);
    let crowded = decision(r#"{"sessionId":"lead","answerable":true,"requests":[{"id":"r7","questions":[{"question":"Which?","options":[{"id":"1","label":"1"},{"id":"2","label":"2"},{"id":"3","label":"3"},{"id":"4","label":"4"},{"id":"5","label":"5"},{"id":"6","label":"6"}]}]}]}"#);
    let found = needs(&[compound, multi, elsewhere, crowded], &sessions(), true);
    assert_eq!(found[0].body, "Needs you: 2 questions");
    assert!(found.iter().all(|n| n.buttons.is_empty()));
}

#[test]
fn a_stop_without_a_request_notifies_and_reviews_and_parked_work_do_not() {
    let stopped = decision(r#"{"group":"stopped","sessionId":"solo","kind":"ask","reason":"Which branch should I use?","since":"2026-11-26T11:00:00Z"}"#);
    let review = decision(r#"{"group":"review","key":"review:W1","sessionId":"solo","workItem":"W1"}"#);
    let parked = decision(r#"{"kind":"parked","sessionId":"solo","workItem":"W2"}"#);
    let found = needs(&[stopped.clone(), review, parked], &sessions(), true);
    assert_eq!(keys(&found), [format!("need|{}", stopped.hold_key()).as_str()]);
    assert_eq!(found[0].body, "Needs you: Which branch should I use?");
    assert_eq!(found[0].title, "Solo run");
    assert!(found[0].request.is_none() && found[0].buttons.is_empty());
}

#[test]
fn a_session_the_app_does_not_list_is_named_by_its_work() {
    let d = decision(r#"{"sessionId":"elsewhere-123456789","workTitle":"Fix the login page","requests":[{"id":"r8","tool":"Edit"}]}"#);
    assert_eq!(needs(&[d], &[], true)[0].title, "Fix the login page");
}

fn here() -> Vec<String> {
    vec![String::new()]
}

fn notice(key: &str, kind: Kind, session: &str) -> Notice {
    from(key, kind, session, "")
}

fn from(key: &str, kind: Kind, session: &str, source: &str) -> Notice {
    Notice { key: key.into(), kind, session: session.into(), source: source.into(), thread: String::new(), title: String::new(), body: String::new(), request: None, buttons: Vec::new(), sound: false }
}

#[test]
fn the_first_look_only_learns_what_is_already_open() {
    let mut n = Notified::default();
    let first = n.take(Kind::Needs, vec![notice("need|a|1", Kind::Needs, "a")], &here(), now(), |_| false);
    assert!(first.post.is_empty());
    let next = n.take(Kind::Needs, vec![notice("need|a|1", Kind::Needs, "a"), notice("need|b|2", Kind::Needs, "b")], &here(), now(), |_| false);
    assert_eq!(keys(&next.post), ["need|b|2"]);
    assert!(next.dirty);
    let again = n.take(Kind::Needs, vec![notice("need|b|2", Kind::Needs, "b")], &here(), now(), |_| false);
    assert!(again.post.is_empty());
    let quiet = n.take(Kind::Needs, vec![notice("need|b|2", Kind::Needs, "b")], &here(), now(), |_| false);
    assert!(!quiet.dirty, "nothing new, nothing to save");
    let done = n.take(Kind::Finished, vec![notice("done|a|t1", Kind::Finished, "a")], &here(), now(), |_| false);
    assert!(done.post.is_empty(), "each kind learns on its own first look");
}

#[test]
fn what_is_on_screen_is_never_posted_later_either() {
    let mut n = Notified::default();
    n.take(Kind::Needs, Vec::new(), &here(), now(), |_| false);
    let step = n.take(Kind::Needs, vec![notice("need|a|1", Kind::Needs, "a"), notice("need|b|2", Kind::Needs, "b")], &here(), now(), |x| x.session == "a");
    assert_eq!(keys(&step.post), ["need|b|2"]);
    let later = n.take(Kind::Needs, vec![notice("need|a|1", Kind::Needs, "a")], &here(), now(), |_| false);
    assert!(later.post.is_empty());
}

#[test]
fn a_computer_that_answers_late_only_learns_what_it_already_had_open() {
    let mut n = Notified::default();
    let both = vec![String::new(), "studio".to_string()];
    n.take(Kind::Needs, vec![from("need|a|1", Kind::Needs, "a", "")], &here(), now(), |_| false);
    let back = n.take(Kind::Needs, vec![from("need|a|1", Kind::Needs, "a", ""), from("need|s|old", Kind::Needs, "s", "studio")], &both, now(), |_| false);
    assert!(back.post.is_empty());
    let later = n.take(Kind::Needs, vec![from("need|s|new", Kind::Needs, "s", "studio"), from("need|a|2", Kind::Needs, "a", "")], &both, now(), |_| false);
    assert_eq!(keys(&later.post), ["need|s|new", "need|a|2"]);
    let away = n.take(Kind::Needs, Vec::new(), &here(), now(), |_| false);
    assert_eq!(away.remove, ["need|a|2"], "only what the computers that answered no longer hold is taken back");
    assert_eq!(n.take(Kind::Needs, Vec::new(), &both, now(), |_| false).remove, ["need|s|new"]);
}

#[test]
fn an_answered_need_is_taken_back_only_after_a_full_read() {
    let mut n = Notified::default();
    n.take(Kind::Needs, Vec::new(), &here(), now(), |_| false);
    n.take(Kind::Needs, vec![notice("need|a|1", Kind::Needs, "a")], &here(), now(), |_| false);
    assert!(n.take(Kind::Needs, Vec::new(), &[], now(), |_| false).remove.is_empty(), "a computer that didn't answer may still hold it");
    assert_eq!(n.take(Kind::Needs, Vec::new(), &here(), now(), |_| false).remove, ["need|a|1"]);
    assert!(n.take(Kind::Needs, Vec::new(), &here(), now(), |_| false).remove.is_empty());
}

#[test]
fn what_was_noticed_survives_a_restart_and_old_keys_are_dropped() {
    let dir = std::env::temp_dir().join(format!("feed-notified-{}", std::process::id()));
    let path = dir.join("notified.json");
    let mut n = Notified::default();
    n.take(Kind::Needs, Vec::new(), &here(), now(), |_| false);
    n.take(Kind::Finished, vec![notice("done|a|t1", Kind::Finished, "a")], &here(), now(), |_| false);
    n.take(Kind::Needs, vec![notice("need|a|1", Kind::Needs, "a")], &here(), now(), |_| false);
    n.save(&path).unwrap();
    let mut back = Notified::load(&path);
    assert_eq!(back, n);
    assert!(back.take(Kind::Needs, vec![notice("need|a|1", Kind::Needs, "a")], &here(), now(), |_| false).post.is_empty());
    let week = now() + time::Duration::days(8);
    back.take(Kind::Finished, Vec::new(), &here(), week, |_| false);
    assert!(!back.seen("done|a|t1"));
    assert!(back.seen("need|a|1"), "a sweep of one kind leaves the other's keys");
    back.take(Kind::Needs, vec![notice("need|a|1", Kind::Needs, "a")], &here(), week, |_| false);
    assert!(back.seen("need|a|1"), "a need still open is kept however old");
    std::fs::remove_dir_all(&dir).unwrap();
    assert_eq!(Notified::load(&path), Notified::default());
}

#[test]
fn preferences_come_from_the_daemon() {
    let (client, asked) = crate::mock::daemon(vec![(200, r#"{"preferences":[{"id":"leadCompletionsOnly","value":false,"default":true}]}"#.into())]);
    assert_eq!(client.notification_preferences().unwrap(), Prefs { lead_completions_only: false });
    assert_eq!(asked.recv().unwrap().0, "/api/v1/notification-preferences");
    let (client, _asked) = crate::mock::daemon(vec![(200, r#"{"preferences":[]}"#.into())]);
    assert_eq!(client.notification_preferences().unwrap(), Prefs::default());
}

#[test]
fn the_inbox_names_each_computer_it_read() {
    let mut inbox = Inbox::default();
    inbox.add(Page::default(), "", "", "own");
    inbox.add(Page::default(), "studio", "Studio", "own");
    assert_eq!(inbox.read, ["", "studio"]);
}

#[test]
fn the_daemon_posts_only_while_its_desktop_notifications_are_on() {
    let home = std::env::temp_dir().join(format!("notify-daemon-posts-{}", std::process::id()));
    let config = home.join(".agenthail/notifications.json");
    std::fs::create_dir_all(config.parent().unwrap()).unwrap();
    assert!(!daemon_posts(&home));
    std::fs::write(&config, "{\n  \"enabled\": true\n}\n").unwrap();
    assert!(daemon_posts(&home));
    std::fs::write(&config, "{}\n").unwrap();
    assert!(!daemon_posts(&home));
    std::fs::write(&config, "not json").unwrap();
    assert!(!daemon_posts(&home));
    std::fs::remove_dir_all(&home).unwrap();
}
