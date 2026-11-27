use serde::Deserialize;
use time::OffsetDateTime;

use crate::catalog::{Client, Session, enc, parse_time};
use crate::search::failure;

pub const EARLIER_PAGE: usize = 50;
pub const ACTIVITY_UNREAD: &str = "Couldn't read project activity, so projects with only older sessions may be missing. Agenthail tries again on the next refresh.";
const RECENT_HOURS: i64 = 24;

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ProjectActivity {
    pub id: String,
    #[serde(default, deserialize_with = "crate::catalog::known_time")]
    pub last_active: String,
    #[serde(default)]
    pub session_count: i64,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Page {
    pub sessions: Vec<Session>,
    pub next: Option<String>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Ready {
    pub ledger_key: String,
    pub count: i64,
}

pub fn is_now(x: &Session, now: OffsetDateTime) -> bool {
    let running = !x.archived && (crate::derive::sessions::busy(x) || x.subagents.as_ref().is_some_and(|r| r.working > 0));
    let waiting = x.waiting_on.as_ref().is_some_and(|w| !w.is_null()) || x.ask.as_ref().is_some_and(asks_person);
    if running || waiting || x.queue_count > 0 || x.attention.is_some() {
        return true;
    }
    parse_time(&x.last_active).is_some_and(|at| now - at <= time::Duration::hours(RECENT_HOURS))
}

fn asks_person(ask: &serde_json::Value) -> bool {
    let text = |k: &str| ask[k].as_str().unwrap_or_default();
    !text("id").is_empty() && text("routedTo") == "person" && text("answeredAt").is_empty()
}

pub fn earlier(listed: &[Session], fetched: &[Session], project: &str, lead: Option<&str>, now: OffsetDateTime) -> Vec<Session> {
    let mut seen: std::collections::HashSet<&str> = std::collections::HashSet::new();
    let mut out: Vec<Session> = Vec::new();
    for x in listed.iter().chain(fetched) {
        if !seen.insert(x.id.as_str()) || x.project_key() != project || x.subagent.is_some() || x.archived || Some(x.id.as_str()) == lead {
            continue;
        }
        if !is_now(x, now) {
            out.push(x.clone());
        }
    }
    out.sort_by(|a, b| b.last_active.cmp(&a.last_active));
    out
}

pub fn ready_label(count: i64) -> Option<String> {
    (count > 0).then(|| format!("{count} ready"))
}

impl Client {
    pub fn earlier_page(&self, project: &str, cursor: Option<&str>) -> Result<Page, String> {
        let mut path = format!("/api/v1/snapshot?scope=all&projectId={}&limit={EARLIER_PAGE}", enc(project));
        if let Some(c) = cursor {
            path.push_str(&format!("&cursor={}", enc(c)));
        }
        let page = self.get_reply(&path).map_err(|(code, body)| failure("earlier sessions", code, &body))?;
        let sessions = serde_json::from_value(page["sessions"].clone()).map_err(|e| format!("Agenthail sent sessions this app can't read: {e}"))?;
        Ok(Page { sessions, next: page["nextCursor"].as_str().filter(|c| !c.is_empty()).map(String::from) })
    }

    pub fn ready(&self, project: &str) -> Result<Ready, String> {
        let path = format!("/api/v1/ledger?projectKey={}&view=backlog&ready=true&limit=1", enc(project));
        let page = self.get_reply(&path).map_err(|(code, body)| failure("the ready count", code, &body))?;
        let count = page["total"].as_i64().ok_or("Agenthail's ready count had no total")?;
        Ok(Ready { ledger_key: page["projectKey"].as_str().filter(|k| !k.is_empty()).unwrap_or(project).to_string(), count })
    }

    pub fn project_activity(&self) -> anyhow::Result<Vec<ProjectActivity>> {
        let page = self.get_json("/api/v1/projects/activity")?;
        Ok(serde_json::from_value(page["projects"].clone())?)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};
    use serde_json::json;

    fn session(id: &str, project: &str, last_active: &str) -> Session {
        serde_json::from_value(json!({"id": id, "project": {"id": project, "name": project}, "lastActive": last_active})).unwrap()
    }

    #[test]
    fn earlier_holds_this_projects_quiet_sessions_newest_first_without_the_lead() {
        let now = parse_time("2026-11-27T12:00:00Z").unwrap();
        let mut busy = session("busy", "p", "2026-11-01T00:00:00Z");
        busy.status = "busy".into();
        let mut queued = session("queued", "p", "2026-11-01T00:00:00Z");
        queued.queue_count = 1;
        let mut helper = session("helper", "p", "2026-11-01T00:00:00Z");
        helper.subagent = Some(json!({"parentId": "x"}));
        let mut gone = session("gone", "p", "2026-11-01T00:00:00Z");
        gone.archived = true;
        let listed = vec![session("today", "p", "2026-11-27T09:00:00Z"), session("old", "p", "2026-11-20T00:00:00Z"), busy, queued, helper, gone, session("lead", "p", "2026-11-01T00:00:00Z"), session("elsewhere", "q", "2026-11-01T00:00:00Z")];
        let fetched = vec![session("older", "p", "2026-11-10T00:00:00Z"), session("old", "p", "2026-11-02T00:00:00Z"), session("busy", "p", "2026-11-01T00:00:00Z"), session("today", "p", "2026-11-01T00:00:00Z")];
        let ids: Vec<String> = earlier(&listed, &fetched, "p", Some("lead"), now).into_iter().map(|x| x.id).collect();
        assert_eq!(ids, vec!["old", "older"]);
    }

    #[test]
    fn a_session_waiting_on_you_or_with_working_helpers_is_now_however_old() {
        let now = parse_time("2026-11-27T12:00:00Z").unwrap();
        let old = session("a", "p", "2026-11-01T00:00:00Z");
        assert!(!is_now(&old, now));
        let waiting: Session = serde_json::from_value(json!({"id": "a", "lastActive": "2026-11-01T00:00:00Z", "attention": {"kind": "ask"}})).unwrap();
        assert!(is_now(&waiting, now));
        let crew: Session = serde_json::from_value(json!({"id": "a", "lastActive": "2026-11-01T00:00:00Z", "subagents": {"count": 2, "working": 1}})).unwrap();
        assert!(is_now(&crew, now));
        let held: Session = serde_json::from_value(json!({"id": "a", "lastActive": "2026-11-01T00:00:00Z", "waitingOn": {"kind": "ask"}})).unwrap();
        assert!(is_now(&held, now));
        let asked = |routed: &str, answered: &str| -> Session { serde_json::from_value(json!({"id": "a", "lastActive": "2026-11-01T00:00:00Z", "ask": {"id": "q1", "routedTo": routed, "answeredAt": answered}})).unwrap() };
        assert!(is_now(&asked("person", ""), now));
        assert!(!is_now(&asked("lead", ""), now));
        assert!(!is_now(&asked("person", "2026-11-02T00:00:00Z"), now));
        assert!(is_now(&session("a", "p", "2026-11-26T12:00:00Z"), now));
        assert!(!is_now(&session("a", "p", ""), now));
    }

    #[test]
    fn earlier_pages_the_projects_whole_history() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"sessions":[{"id":"a","lastActive":"2026-11-01T00:00:00Z"}],"nextCursor":"c 2"}"#),
            (200, r#"{"sessions":[],"nextCursor":""}"#),
            (404, r#"{"error":{"code":"unknown_route","message":"no"}}"#),
        ]));
        let first = client.earlier_page("repo:x/y", None).unwrap();
        assert_eq!((first.sessions[0].id.as_str(), first.next.as_deref()), ("a", Some("c 2")));
        assert_eq!(seen.recv().unwrap().0, "/api/v1/snapshot?scope=all&projectId=repo%3Ax%2Fy&limit=50");
        let last = client.earlier_page("repo:x/y", Some("c 2")).unwrap();
        assert_eq!(last.next, None);
        assert_eq!(seen.recv().unwrap().0, "/api/v1/snapshot?scope=all&projectId=repo%3Ax%2Fy&limit=50&cursor=c+2");
        assert!(client.earlier_page("p", None).unwrap_err().contains("older version"));
    }

    #[test]
    fn the_ready_count_comes_from_the_backlog_total_under_the_ledgers_own_key() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"projectKey":"repo:x","total":3,"backlog":[{"id":"W1"}]}"#),
            (200, r#"{"total":0}"#),
            (200, r#"{"backlog":[]}"#),
        ]));
        assert_eq!(client.ready("p 1").unwrap(), Ready { ledger_key: "repo:x".into(), count: 3 });
        assert_eq!(seen.recv().unwrap().0, "/api/v1/ledger?projectKey=p+1&view=backlog&ready=true&limit=1");
        assert_eq!(client.ready("p").unwrap(), Ready { ledger_key: "p".into(), count: 0 });
        assert!(client.ready("p").is_err());
        assert_eq!(ready_label(0), None);
        assert_eq!(ready_label(2).as_deref(), Some("2 ready"));
    }

    #[test]
    fn project_activity_reads_counts_and_times() {
        let (client, seen) = daemon(replies(&[(200, r#"{"projects":[{"id":"p","lastActive":"2026-11-27T05:57:15.321000000Z","sessionCount":4,"checkouts":[]}]}"#)]));
        let list = client.project_activity().unwrap();
        assert_eq!(seen.recv().unwrap().0, "/api/v1/projects/activity");
        assert_eq!((list[0].id.as_str(), list[0].session_count, list[0].last_active.is_empty()), ("p", 4, false));
    }
}
