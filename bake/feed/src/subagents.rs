use std::collections::{HashMap, HashSet};
use std::time::Duration;

use serde::Deserialize;
use serde_json::Value;

use crate::catalog::{Client, Session, enc, parse_time};

#[derive(Deserialize, Clone, Debug, Default, PartialEq, Eq, Hash)]
#[serde(rename_all = "camelCase")]
pub struct Rollup {
    #[serde(default)]
    pub count: i64,
    #[serde(default)]
    pub working: i64,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Identity {
    #[serde(default)]
    pub parent_id: String,
    #[serde(default)]
    pub root_id: String,
    #[serde(default)]
    pub depth: i64,
    #[serde(default)]
    pub nickname: String,
    #[serde(default)]
    pub role: String,
}

pub fn identity(x: &Session) -> Option<Identity> {
    x.subagent.as_ref().and_then(|v| serde_json::from_value::<Identity>(v.clone()).ok()).filter(|i| !i.parent_id.is_empty())
}

pub fn parent_of(x: &Session) -> Option<String> {
    identity(x).map(|i| i.parent_id).filter(|p| *p != x.id)
}

pub fn label(x: &Session) -> String {
    match identity(x) {
        Some(i) if !i.nickname.is_empty() && !i.role.is_empty() => format!("{} ({})", i.nickname, i.role),
        Some(i) if !i.nickname.is_empty() => i.nickname,
        _ => x.title(),
    }
}

pub fn wants_page(x: &Session) -> bool {
    x.hidden_subagents.as_ref().is_some_and(|h| h.count > 0)
}

pub fn page_wanted(x: &Session, listed: bool) -> bool {
    !listed || wants_page(x)
}

pub fn page_key(x: &Session) -> String {
    let roll = |r: &Option<Rollup>| r.as_ref().map_or("-".to_string(), |r| format!("{}/{}", r.count, r.working));
    format!("{}|{}|{}|{}", x.id, roll(&x.subagents), roll(&x.hidden_subagents), roll(&x.live_hidden_subagents))
}

pub const PAGE_EVERY: Duration = Duration::from_secs(60);
pub const PAGE_OPEN_EVERY: Duration = Duration::from_secs(15);
pub const CLAUDE_EVERY: Duration = Duration::from_secs(15);
pub const CLAUDE_IDLE_EVERY: Duration = Duration::from_secs(60);

pub fn page_due(same_key: bool, since: Duration, holds_open: bool) -> bool {
    !same_key || since >= if holds_open { PAGE_OPEN_EVERY } else { PAGE_EVERY }
}

pub struct Asked<'a> {
    pub stamp: &'a str,
    pub since: Duration,
    pub failed: bool,
}

pub fn claude_due(asked: Option<Asked>, stamp: &str, working: bool) -> bool {
    match asked {
        None => true,
        Some(a) if a.stamp != stamp || working || a.failed => a.since >= CLAUDE_EVERY,
        Some(a) => a.since >= CLAUDE_IDLE_EVERY,
    }
}

pub fn children(parent: &str, listed: &[Session], fetched: &[Session]) -> Vec<Session> {
    let live: Vec<&Session> = listed.iter().filter(|x| parent_of(x).as_deref() == Some(parent)).collect();
    let ids: HashSet<&str> = live.iter().map(|x| x.id.as_str()).collect();
    fetched.iter().filter(|x| parent_of(x).as_deref() == Some(parent) && !ids.contains(x.id.as_str())).chain(live).cloned().collect()
}

pub fn in_daemon_order(children: &mut [Session], listed: &[Session]) {
    let ids: HashSet<&str> = listed.iter().map(|x| x.id.as_str()).collect();
    children.sort_by(|a, b| match (ids.contains(a.id.as_str()), ids.contains(b.id.as_str())) {
        (true, true) => parse_time(&b.registered_at).cmp(&parse_time(&a.registered_at)).then_with(|| a.id.cmp(&b.id)),
        (true, false) => std::cmp::Ordering::Less,
        (false, true) => std::cmp::Ordering::Greater,
        (false, false) => std::cmp::Ordering::Equal,
    });
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ClaudeSubagent {
    #[serde(default)]
    pub parent_session_id: String,
    #[serde(default)]
    pub agent_id: String,
    #[serde(default)]
    pub agent_type: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub tool_use_id: String,
    #[serde(default)]
    pub depth: i64,
    #[serde(default)]
    pub working: bool,
    #[serde(default)]
    pub started_at: String,
    #[serde(default)]
    pub last_active: String,
    #[serde(default)]
    pub transcript_path: String,
}

impl ClaudeSubagent {
    pub fn key(&self) -> String {
        format!("{}{}", self.agent_id, self.transcript_path)
    }

    pub fn title(&self) -> String {
        if !self.description.is_empty() {
            return self.description.clone();
        }
        if !self.agent_type.is_empty() {
            return self.agent_type.clone();
        }
        format!("Subagent {}", self.agent_id.chars().take(8).collect::<String>())
    }

    pub fn kind_label(&self) -> Option<&str> {
        (!self.description.is_empty() && !self.agent_type.is_empty()).then_some(self.agent_type.as_str())
    }
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct ClaudeSubagents {
    pub links: Vec<ClaudeSubagent>,
    pub total: usize,
}

pub const RECENT_SHOWN: usize = 8;

#[derive(Clone, Debug, PartialEq)]
pub struct ClaudeList<'a> {
    pub shown: &'a [ClaudeSubagent],
    pub earlier: usize,
    pub working: usize,
    pub total: usize,
}

impl ClaudeSubagents {
    pub fn list(&self, expanded: bool) -> ClaudeList<'_> {
        let working = self.links.iter().filter(|l| l.working).count();
        let total = self.total.max(self.links.len());
        let shown = if expanded { &self.links[..] } else { &self.links[..self.links.len().min(working + RECENT_SHOWN)] };
        ClaudeList { shown, earlier: total - shown.len(), working, total }
    }
}

impl ClaudeList<'_> {
    pub fn summary(&self) -> String {
        let noun = if self.total == 1 { "subagent" } else { "subagents" };
        if self.working > 0 { format!("{} working, {} {noun} in all", self.working, self.total) } else { format!("{} {noun} in all", self.total) }
    }

    pub fn earlier_label(&self) -> String {
        format!("Show {} earlier", self.earlier)
    }
}

impl ClaudeSubagents {
    pub fn working(&self) -> bool {
        self.links.iter().any(|l| l.working)
    }
}

pub fn claude_subagents(metadata: &Value) -> Result<ClaudeSubagents, String> {
    if let Some(error) = metadata["errors"]["claudeSubagents"].as_str() {
        return Err(error.to_string());
    }
    let links: Vec<ClaudeSubagent> = serde_json::from_value(metadata["claudeSubagents"].clone()).unwrap_or_default();
    let total = metadata["claudeSubagentsTotal"].as_u64().map_or(links.len(), |t| t as usize);
    Ok(ClaudeSubagents { links, total })
}

#[derive(Clone, Debug, PartialEq)]
pub struct ClaudeRead {
    pub subagents: Result<ClaudeSubagents, String>,
    pub metadata: Result<crate::inspect::Metadata, String>,
}

const PAGE_TRIES: usize = 3;

impl Client {
    pub fn claude_subagents(&self, session: &str) -> ClaudeRead {
        match self.get_json(&format!("/api/v1/session-metadata?id={}&subagents=all", enc(session))) {
            Ok(value) => ClaudeRead { subagents: claude_subagents(&value), metadata: Ok(crate::inspect::metadata(&value)) },
            Err(error) => ClaudeRead { subagents: Err(error.to_string()), metadata: Err(error.to_string()) },
        }
    }

    pub fn session_row(&self, id: &str) -> RowRead {
        match self.get_coded(&format!("/api/v1/session?id={}&limit=1", enc(id))) {
            Ok(value) => detail_row(&value).map_or_else(RowRead::Failed, RowRead::Found),
            Err((404, _)) => RowRead::Gone,
            Err((0, message)) => RowRead::Failed(message),
            Err((code, name)) if name.is_empty() => RowRead::Failed(format!("status code {code}")),
            Err((_, name)) => RowRead::Failed(name),
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub enum RowRead {
    Found(Session),
    Gone,
    Failed(String),
}

pub fn detail_row(detail: &Value) -> Result<Session, String> {
    let Value::Object(mut raw) = detail["session"].clone() else { return Err("Agenthail returned no session".into()) };
    for key in ["capabilities", "readOnly", "readOnlyReason", "archived", "attention"] {
        if let Some(v) = detail.get(key).filter(|v| !v.is_null()) {
            raw.insert(key.into(), v.clone());
        }
    }
    if let Some(alias) = detail["alias"].as_str().filter(|a| !a.is_empty()) {
        raw.insert("alias".into(), Value::String(alias.into()));
    }
    let row: Session = serde_json::from_value(Value::Object(raw)).map_err(|e| e.to_string())?;
    if row.id.is_empty() {
        return Err("Agenthail returned no session".into());
    }
    Ok(row)
}

impl Client {
    pub fn subagent_sessions(&self, parent: &str) -> Result<Vec<Session>, String> {
        for _ in 0..PAGE_TRIES {
            match self.subagent_pages(parent) {
                Err((409, _)) => continue,
                Err((0, message)) => return Err(message),
                Err((code, name)) if name.is_empty() => return Err(format!("status code {code}")),
                Err((_, name)) => return Err(name),
                Ok(rows) => return Ok(rows),
            }
        }
        Err("The session list kept changing. Try again.".into())
    }

    fn subagent_pages(&self, parent: &str) -> Result<Vec<Session>, (u16, String)> {
        let mut sessions = Vec::new();
        let mut cursor: Option<String> = None;
        loop {
            let mut path = format!("/api/v1/snapshot?parentId={}&limit=200", enc(parent));
            if let Some(c) = &cursor {
                path.push_str(&format!("&cursor={}", enc(c)));
            }
            let page = self.get_coded(&path)?;
            sessions.extend(serde_json::from_value::<Vec<Session>>(page["sessions"].clone()).map_err(|e| (0, e.to_string()))?);
            cursor = page["nextCursor"].as_str().filter(|c| !c.is_empty()).map(String::from);
            if cursor.is_none() {
                return Ok(sessions);
            }
        }
    }
}

#[derive(Default)]
pub struct Seen(HashMap<String, ((u64, bool), bool)>);

impl Seen {
    fn apply(&mut self, id: &str, at: (u64, bool), present: bool) -> bool {
        if self.0.get(id).is_some_and(|(newest, _)| at <= *newest) {
            return false;
        }
        self.0.insert(id.to_string(), (at, present));
        true
    }

    pub fn read(&mut self, id: &str, asked: u64, present: bool) -> bool {
        self.apply(id, (asked, false), present)
    }

    pub fn gone(&mut self, id: &str, last_asked: u64) {
        self.apply(id, (last_asked, true), false);
    }

    pub fn absent(&self, id: &str) -> bool {
        matches!(self.0.get(id), Some((_, false)))
    }

    pub fn settle(&mut self, oldest_in_flight: Option<u64>) {
        match oldest_in_flight {
            Some(oldest) => self.0.retain(|_, (at, _)| *at >= (oldest, false)),
            None => self.0.clear(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};

    fn row(json: &str) -> Session {
        serde_json::from_str(json).unwrap()
    }

    fn child(id: &str, parent: &str) -> Session {
        row(&format!(r#"{{"id":"{id}","name":"{id} task","subagent":{{"parentId":"{parent}","rootId":"{parent}","depth":1}}}}"#))
    }

    #[test]
    fn a_child_is_named_by_its_nickname_and_role() {
        let x = row(r#"{"id":"c","name":"Bounded verification","subagent":{"parentId":"p","rootId":"p","depth":1,"nickname":"Archimedes","role":"explorer"}}"#);
        assert_eq!(label(&x), "Archimedes (explorer)");
        assert_eq!(parent_of(&x).as_deref(), Some("p"));
        let plain = row(r#"{"id":"c","name":"Bounded verification","subagent":{"parentId":"p","rootId":"p","depth":1,"nickname":"Archimedes"}}"#);
        assert_eq!(label(&plain), "Archimedes");
        assert_eq!(label(&child("c", "p")), "c task");
        assert_eq!(parent_of(&row(r#"{"id":"c","subagent":{"parentId":"c","rootId":"c","depth":0}}"#)), None);
        assert_eq!(parent_of(&row(r#"{"id":"c"}"#)), None);
    }

    #[test]
    fn a_listed_child_takes_the_place_of_its_fetched_copy() {
        let listed = vec![child("b", "p"), child("x", "other"), row(r#"{"id":"p"}"#)];
        let mut stale = child("b", "p");
        stale.name = "stale".into();
        let fetched = vec![child("a", "p"), stale, child("z", "other")];
        let got: Vec<(String, String)> = children("p", &listed, &fetched).into_iter().map(|x| (x.id, x.name)).collect();
        assert_eq!(got, vec![("a".into(), "a task".into()), ("b".into(), "b task".into())]);
    }

    #[test]
    fn listed_children_come_newest_first_and_the_rest_keep_the_page_order() {
        let at = |mut x: Session, when: &str| {
            x.registered_at = when.into();
            x
        };
        let listed = vec![row(r#"{"id":"p"}"#), at(child("old", "p"), "2026-11-26T09:00:00Z"), at(child("new", "p"), "2026-11-26T12:00:00.5Z"), at(child("b-tie", "p"), "2026-11-26T08:00:00Z"), at(child("a-tie", "p"), "2026-11-26T08:00:00.000Z")];
        let fetched = vec![child("h1", "p"), child("old", "p"), child("h2", "p")];
        let mut list = children("p", &listed, &fetched);
        in_daemon_order(&mut list, &listed);
        let ids: Vec<&str> = list.iter().map(|x| x.id.as_str()).collect();
        assert_eq!(ids, ["new", "old", "a-tie", "b-tie", "h1", "h2"]);
    }

    #[test]
    fn the_page_is_fetched_only_while_some_children_are_hidden() {
        let parent = row(r#"{"id":"p","subagents":{"count":28,"working":0},"hiddenSubagents":{"count":27,"working":0}}"#);
        assert!(wants_page(&parent));
        assert!(!wants_page(&row(r#"{"id":"p","subagents":{"count":2,"working":1}}"#)));
        assert!(!wants_page(&row(r#"{"id":"p","hiddenSubagents":{"count":0,"working":0}}"#)));
        let busier = row(r#"{"id":"p","subagents":{"count":28,"working":1},"hiddenSubagents":{"count":27,"working":1}}"#);
        assert_ne!(page_key(&parent), page_key(&busier));
    }

    #[test]
    fn a_row_from_outside_the_list_always_reads_its_page() {
        let counted = row(r#"{"id":"p","hiddenSubagents":{"count":0,"working":0}}"#);
        assert!(!page_wanted(&counted, true));
        assert!(page_wanted(&counted, false));
        assert!(page_wanted(&row(r#"{"id":"p"}"#), false));
        assert!(page_wanted(&row(r#"{"id":"p","hiddenSubagents":{"count":2,"working":0}}"#), true));
    }

    #[test]
    fn claude_subagents_show_the_working_and_eight_recent() {
        let link = |id: &str, working: bool| format!(r#"{{"parentSessionId":"p","agentId":"{id}","agentType":"fork","description":"Task {id}","working":{working},"transcriptPath":"/t/{id}.jsonl"}}"#);
        let mut links: Vec<String> = (0..2).map(|i| link(&format!("w{i}"), true)).collect();
        links.extend((0..10).map(|i| link(&format!("d{i}"), false)));
        let meta: Value = serde_json::from_str(&format!(r#"{{"claudeSubagents":[{}],"claudeSubagentsTotal":12}}"#, links.join(","))).unwrap();
        let got = claude_subagents(&meta).unwrap();
        let short = got.list(false);
        assert_eq!((short.shown.len(), short.earlier, short.working, short.total), (10, 2, 2, 12));
        assert_eq!(short.summary(), "2 working, 12 subagents in all");
        assert_eq!(short.earlier_label(), "Show 2 earlier");
        let all = got.list(true);
        assert_eq!((all.shown.len(), all.earlier), (12, 0));
        assert_eq!(got.links[0].title(), "Task w0");
        assert_eq!(got.links[0].kind_label(), Some("fork"));
    }

    #[test]
    fn a_claude_subagent_falls_back_to_its_kind_then_its_id() {
        let typed = ClaudeSubagent { agent_id: "a8f06ab0304ff8236".into(), agent_type: "general-purpose".into(), ..Default::default() };
        assert_eq!(typed.title(), "general-purpose");
        assert_eq!(typed.kind_label(), None);
        let bare = ClaudeSubagent { agent_id: "a8f06ab0304ff8236".into(), ..Default::default() };
        assert_eq!(bare.title(), "Subagent a8f06ab0");
        let one = ClaudeSubagents { links: vec![bare], total: 1 };
        assert_eq!(one.list(false).summary(), "1 subagent in all");
        assert_eq!(claude_subagents(&Value::Null), Ok(ClaudeSubagents::default()));
    }

    #[test]
    fn a_failed_read_is_not_an_empty_list() {
        let meta: Value = serde_json::from_str(r#"{"sessionId":"s","errors":{"claudeSubagents":"transcript unreadable"}}"#).unwrap();
        assert_eq!(claude_subagents(&meta), Err("transcript unreadable".into()));
        let other: Value = serde_json::from_str(r#"{"sessionId":"s","claudeSubagents":[],"errors":{"context":"timeout"}}"#).unwrap();
        assert_eq!(claude_subagents(&other), Ok(ClaudeSubagents::default()));
    }

    #[test]
    fn claude_subagents_are_read_again_while_any_could_have_changed() {
        let asked = |stamp, secs, failed| Some(Asked { stamp, since: Duration::from_secs(secs), failed });
        assert!(claude_due(None, "t1", false));
        assert!(!claude_due(asked("t1", 59, false), "t1", false));
        assert!(claude_due(asked("t1", 60, false), "t1", false));
        assert!(claude_due(asked("t1", 15, false), "t1", true));
        assert!(!claude_due(asked("t1", 5, false), "t1", true));
        assert!(claude_due(asked("t0", 15, false), "t1", false));
        assert!(!claude_due(asked("t0", 14, false), "t1", false));
        assert!(claude_due(asked("t1", 15, true), "t1", false));
    }

    #[test]
    fn the_hidden_page_is_read_again_when_its_counts_move_or_a_minute_passes() {
        assert!(page_due(false, Duration::ZERO, false));
        assert!(!page_due(true, Duration::from_secs(59), false));
        assert!(page_due(true, PAGE_EVERY, false));
        assert!(!page_due(true, Duration::from_secs(14), true));
        assert!(page_due(true, PAGE_OPEN_EVERY, true));
        let a = row(r#"{"id":"p","hiddenSubagents":{"count":3,"working":0},"liveHiddenSubagents":{"count":1,"working":0}}"#);
        let b = row(r#"{"id":"p","hiddenSubagents":{"count":3,"working":0},"liveHiddenSubagents":{"count":2,"working":0}}"#);
        assert_ne!(page_key(&a), page_key(&b));
    }

    #[test]
    fn a_parent_outside_the_list_is_read_as_a_row() {
        let (client, asked) = daemon(replies(&[
            (200, r#"{"session":{"id":"p","name":"Plan the importer","status":"idle","subagent":{"parentId":"g","rootId":"g","depth":1,"nickname":"Noether"}},"alias":"planner"}"#),
            (404, r#"{"error":{"code":"not_found"}}"#),
        ]));
        let RowRead::Found(got) = client.session_row("p") else { panic!("expected a row") };
        assert_eq!((got.id.as_str(), got.title(), parent_of(&got)), ("p", "@planner".to_string(), Some("g".to_string())));
        assert_eq!(asked.recv().unwrap().0, "/api/v1/session?id=p&limit=1");
        assert_eq!(client.session_row("p"), RowRead::Gone);
        let (client, _asked) = daemon(replies(&[(503, r#"{"error":{"code":"host_unreachable"}}"#)]));
        assert_eq!(client.session_row("p"), RowRead::Failed("host_unreachable".into()));
    }

    #[test]
    fn a_parent_row_keeps_what_it_may_do() {
        let detail: Value = serde_json::from_str(r#"{"session":{"id":"p","name":"Plan","surface":"codex"},"capabilities":{"send":true,"interrupt":false,"steer":false},"readOnly":true,"archived":true}"#).unwrap();
        let row = detail_row(&detail).unwrap();
        assert!(row.read_only && row.archived);
        assert!(!row.capabilities.interrupt && !row.capabilities.steer);
        assert!(detail_row(&serde_json::json!({"session": null})).is_err());
    }

    #[test]
    fn hidden_children_are_read_page_by_page() {
        let (client, asked) = daemon(replies(&[
            (200, r#"{"sessions":[{"id":"a","subagent":{"parentId":"p 1","rootId":"p 1","depth":1}}],"nextCursor":"c2"}"#),
            (200, r#"{"sessions":[{"id":"b","subagent":{"parentId":"p 1","rootId":"p 1","depth":1}}]}"#),
        ]));
        let got: Vec<String> = client.subagent_sessions("p 1").unwrap().into_iter().map(|x| x.id).collect();
        assert_eq!(got, vec!["a", "b"]);
        assert_eq!(asked.recv().unwrap().0, "/api/v1/snapshot?parentId=p+1&limit=200");
        assert_eq!(asked.recv().unwrap().0, "/api/v1/snapshot?parentId=p+1&limit=200&cursor=c2");
    }

    #[test]
    fn a_changed_catalog_restarts_from_the_first_page() {
        let (client, asked) = daemon(replies(&[
            (200, r#"{"sessions":[{"id":"a"}],"nextCursor":"c2"}"#),
            (409, r#"{"error":{"code":"cursor_expired","message":"the session list changed"}}"#),
            (200, r#"{"sessions":[{"id":"a2"}]}"#),
        ]));
        let got: Vec<String> = client.subagent_sessions("p").unwrap().into_iter().map(|x| x.id).collect();
        assert_eq!(got, vec!["a2"]);
        let paths: Vec<String> = (0..3).map(|_| asked.recv().unwrap().0).collect();
        assert_eq!(paths[2], "/api/v1/snapshot?parentId=p&limit=200");
    }

    #[test]
    fn a_catalog_that_keeps_changing_gives_up() {
        let conflict = r#"{"error":{"code":"cursor_expired"}}"#;
        let (client, _asked) = daemon(replies(&[(409, conflict), (409, conflict), (409, conflict)]));
        assert_eq!(client.subagent_sessions("p").unwrap_err(), "The session list kept changing. Try again.");
        let (client, _asked) = daemon(replies(&[(404, r#"{"error":{"code":"not_found"}}"#)]));
        assert_eq!(client.subagent_sessions("p").unwrap_err(), "not_found");
    }

    #[test]
    fn claude_subagents_are_read_in_full() {
        let (client, asked) = daemon(replies(&[(200, r#"{"sessionId":"s","goal":{"objective":"Ship","status":"active"},"claudeRuns":[],"claudeSubagents":[{"parentSessionId":"s","agentId":"a1","working":true,"transcriptPath":"/t"}],"claudeSubagentsTotal":1}"#), (500, "")]));
        let read = client.claude_subagents("s");
        let got = read.subagents.unwrap();
        assert_eq!(got.total, 1);
        assert!(got.links[0].working);
        let metadata = read.metadata.unwrap();
        assert_eq!((metadata.goal.unwrap().objective.as_str(), metadata.runs), ("Ship", Some(vec![])));
        assert_eq!(asked.recv().unwrap().0, "/api/v1/session-metadata?id=s&subagents=all");
        let failed = client.claude_subagents("s");
        assert!(failed.subagents.is_err() && failed.metadata.is_err());
    }

    #[test]
    fn the_newest_asked_answer_about_a_session_wins() {
        let mut seen = Seen::default();
        assert!(seen.read("c1", 3, true));
        seen.gone("c1", 7);
        assert!(seen.absent("c1"));
        assert!(!seen.read("c1", 5, true));
        assert!(!seen.read("c1", 7, true));
        assert!(seen.read("c1", 8, true));
        assert!(!seen.absent("c1"));
        assert!(!seen.read("c1", 6, false));
        assert!(!seen.read("c1", 7, true));
        assert!(!seen.absent("c1"));
        assert!(seen.read("c1", 9, false));
        assert!(seen.absent("c1"));
    }

    #[test]
    fn answers_are_forgotten_once_no_older_read_can_arrive() {
        let mut seen = Seen::default();
        seen.gone("c1", 4);
        seen.gone("c2", 9);
        seen.settle(Some(5));
        assert!(!seen.absent("c1"));
        assert!(seen.absent("c2"));
        assert!(!seen.read("c2", 8, true));
        seen.settle(None);
        assert!(seen.read("c2", 8, true));
    }
}
