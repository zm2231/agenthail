use std::collections::HashSet;

use serde::Deserialize;
use serde_json::Value;

use crate::catalog::{Client, Session, enc};

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

pub fn page_key(x: &Session) -> String {
    let roll = |r: &Option<Rollup>| r.as_ref().map_or("-".to_string(), |r| format!("{}/{}", r.count, r.working));
    format!("{}|{}|{}", x.id, roll(&x.subagents), roll(&x.hidden_subagents))
}

pub fn children(parent: &str, listed: &[Session], fetched: &[Session]) -> Vec<Session> {
    let live: Vec<&Session> = listed.iter().filter(|x| parent_of(x).as_deref() == Some(parent)).collect();
    let ids: HashSet<&str> = live.iter().map(|x| x.id.as_str()).collect();
    fetched.iter().filter(|x| parent_of(x).as_deref() == Some(parent) && !ids.contains(x.id.as_str())).chain(live).cloned().collect()
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

pub fn claude_subagents(metadata: &Value) -> ClaudeSubagents {
    let links: Vec<ClaudeSubagent> = serde_json::from_value(metadata["claudeSubagents"].clone()).unwrap_or_default();
    let total = metadata["claudeSubagentsTotal"].as_u64().map_or(links.len(), |t| t as usize);
    ClaudeSubagents { links, total }
}

const PAGE_TRIES: usize = 3;

impl Client {
    pub fn claude_subagents(&self, session: &str) -> anyhow::Result<ClaudeSubagents> {
        Ok(claude_subagents(&self.get_json(&format!("/api/v1/session-metadata?id={}&subagents=all", enc(session)))?))
    }

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
    fn the_page_is_fetched_only_while_some_children_are_hidden() {
        let parent = row(r#"{"id":"p","subagents":{"count":28,"working":0},"hiddenSubagents":{"count":27,"working":0}}"#);
        assert!(wants_page(&parent));
        assert!(!wants_page(&row(r#"{"id":"p","subagents":{"count":2,"working":1}}"#)));
        assert!(!wants_page(&row(r#"{"id":"p","hiddenSubagents":{"count":0,"working":0}}"#)));
        let busier = row(r#"{"id":"p","subagents":{"count":28,"working":1},"hiddenSubagents":{"count":27,"working":1}}"#);
        assert_ne!(page_key(&parent), page_key(&busier));
    }

    #[test]
    fn claude_subagents_show_the_working_and_eight_recent() {
        let link = |id: &str, working: bool| format!(r#"{{"parentSessionId":"p","agentId":"{id}","agentType":"fork","description":"Task {id}","working":{working},"transcriptPath":"/t/{id}.jsonl"}}"#);
        let mut links: Vec<String> = (0..2).map(|i| link(&format!("w{i}"), true)).collect();
        links.extend((0..10).map(|i| link(&format!("d{i}"), false)));
        let meta: Value = serde_json::from_str(&format!(r#"{{"claudeSubagents":[{}],"claudeSubagentsTotal":12}}"#, links.join(","))).unwrap();
        let got = claude_subagents(&meta);
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
        assert_eq!(claude_subagents(&Value::Null), ClaudeSubagents::default());
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
        let (client, asked) = daemon(replies(&[(200, r#"{"sessionId":"s","claudeSubagents":[{"parentSessionId":"s","agentId":"a1","working":true,"transcriptPath":"/t"}],"claudeSubagentsTotal":1}"#)]));
        let got = client.claude_subagents("s").unwrap();
        assert_eq!(got.total, 1);
        assert!(got.links[0].working);
        assert_eq!(asked.recv().unwrap().0, "/api/v1/session-metadata?id=s&subagents=all");
    }
}
