use serde::Deserialize;
use serde_json::Value;

use crate::catalog::{Client, Session, enc};

pub const CODEX_MIN_CHARS: usize = 3;
pub const HISTORY_PAGE: usize = 25;
pub const HISTORY_MAX_QUERY: usize = 200;

pub fn one_line(text: &str) -> String {
    text.split_whitespace().collect::<Vec<_>>().join(" ")
}

pub fn codex_query(query: &str) -> Option<String> {
    let q = query.trim();
    (q.chars().count() >= CODEX_MIN_CHARS).then(|| q.to_string())
}

#[derive(Clone, Debug, PartialEq)]
pub struct CodexHit {
    pub session: Session,
    pub snippet: String,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Codex {
    pub hits: Vec<CodexHit>,
    pub remote_error: Option<String>,
}

impl Codex {
    pub fn fresh<'a>(&'a self, listed: impl Fn(&str) -> bool) -> impl Iterator<Item = &'a CodexHit> {
        self.hits.iter().filter(move |h| !listed(&h.session.id))
    }

    pub fn note(&self, shown: usize) -> Option<String> {
        match &self.remote_error {
            Some(e) => Some(format!("Only Agenthail's saved Codex conversations were searched: {e}")),
            None if shown == 0 => Some("No older matches.".into()),
            None => None,
        }
    }
}

pub fn read_codex(value: &Value) -> Result<Codex, String> {
    let rows = match &value["results"] {
        Value::Null if value.get("results").is_some() => &[][..],
        v => v.as_array().ok_or("Codex history search returned no results list")?.as_slice(),
    };
    let mut hits = Vec::new();
    for row in rows {
        let mut session: Session = crate::reply::parse(row["session"].clone()).map_err(|e| format!("Codex history search returned a session Agenthail can't read: {e}"))?;
        session.name = one_line(&session.name);
        let snippet = one_line(row["snippet"].as_str().unwrap_or_default());
        hits.push(CodexHit { session, snippet });
    }
    let remote_error = value["remoteError"].as_str().map(str::trim).filter(|e| !e.is_empty()).map(String::from);
    Ok(Codex { hits, remote_error })
}

pub fn failure(what: &str, code: u16, body: &str) -> String {
    let parsed: Value = serde_json::from_str(body).unwrap_or(Value::Null);
    let message = parsed["error"]["message"].as_str().map(str::trim).filter(|m| !m.is_empty());
    let name = parsed["error"]["code"].as_str().unwrap_or_default();
    match (code, name, message) {
        (0, _, _) => format!("Couldn't reach Agenthail for {what}: {body}"),
        (401 | 403, _, Some(m)) => format!("This app's Agenthail token can't read {what}: {m}"),
        (401 | 403, _, None) => format!("This app's Agenthail token can't read {what}."),
        (404, "unknown_route" | "", _) => format!("This Agenthail has no {what}; it runs an older version."),
        (_, _, Some(m)) => m.to_string(),
        _ => format!("{what} failed with status {code}"),
    }
}

#[derive(Clone, Debug, Default, PartialEq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Entry {
    pub id: i64,
    #[serde(default)]
    pub created_at: String,
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub session_id: Option<String>,
    #[serde(default)]
    pub source_session_id: Option<String>,
    #[serde(default)]
    pub target: Option<String>,
    #[serde(default)]
    pub source: Option<String>,
    #[serde(default)]
    pub queue_id: Option<i64>,
    #[serde(default)]
    pub message: Option<String>,
    #[serde(default)]
    pub result: Option<String>,
    #[serde(default)]
    pub error: Option<String>,
    #[serde(default)]
    pub operation: Option<String>,
    #[serde(default)]
    pub evidence: Option<String>,
}

fn some(v: &Option<String>) -> Option<&str> {
    v.as_deref().filter(|s| !s.is_empty())
}

pub fn kind_label(kind: &str) -> String {
    let words: Vec<&str> = (if kind.is_empty() { "event" } else { kind }).split(['_', '-', ' ']).filter(|w| !w.is_empty()).collect();
    let words = words.join(" ").to_lowercase();
    let mut c = words.chars();
    c.next().map(|f| f.to_uppercase().chain(c).collect()).unwrap_or_default()
}

impl Entry {
    pub fn heading(&self) -> String {
        match some(&self.operation) {
            Some(op) if op != "message" => format!("{} · {}", kind_label(&self.kind), kind_label(op)),
            _ => kind_label(&self.kind),
        }
    }

    pub fn target(&self) -> &str {
        some(&self.target).or(some(&self.session_id)).unwrap_or("Agenthail")
    }

    pub fn source(&self) -> Option<&str> {
        some(&self.source).filter(|s| *s != self.target())
    }

    pub fn route(&self) -> String {
        match self.source() {
            Some(from) => format!("{from} → {}", self.target()),
            None => self.target().to_string(),
        }
    }

    pub fn is_error(&self) -> bool {
        some(&self.error).is_some()
    }

    pub fn content(&self) -> &str {
        some(&self.error).or(some(&self.message)).or(some(&self.result)).unwrap_or("No message content recorded")
    }
}

#[derive(Clone, Debug, Default, PartialEq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Page {
    #[serde(default)]
    pub items: Vec<Entry>,
    #[serde(default)]
    pub has_more: bool,
    #[serde(default)]
    pub next_before: i64,
    #[serde(default)]
    pub kinds: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct Filter {
    pub kind: String,
    pub query: String,
}

impl Filter {
    pub fn new(kind: &str, query: &str) -> Filter {
        let query = query.trim();
        let mut end = query.len().min(HISTORY_MAX_QUERY);
        while !query.is_char_boundary(end) {
            end -= 1;
        }
        Filter { kind: kind.trim().to_string(), query: query[..end].trim_end().to_string() }
    }

    pub fn path(&self, before: i64) -> String {
        let mut path = format!("/api/v1/history?limit={HISTORY_PAGE}");
        if before > 0 {
            path.push_str(&format!("&before={before}"));
        }
        if !self.kind.is_empty() {
            path.push_str(&format!("&kind={}", enc(&self.kind)));
        }
        if !self.query.is_empty() {
            path.push_str(&format!("&q={}", enc(&self.query)));
        }
        path
    }
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Activity {
    pub items: Vec<Entry>,
    pub kinds: Vec<String>,
    pub has_more: bool,
    pub next_before: i64,
}

impl Activity {
    pub fn take(&mut self, page: Page, reset: bool) {
        if reset {
            self.items.clear();
        }
        let have: std::collections::HashSet<i64> = self.items.iter().map(|e| e.id).collect();
        self.items.extend(page.items.into_iter().filter(|e| !have.contains(&e.id)));
        self.kinds = page.kinds;
        self.has_more = page.has_more && page.next_before > 0;
        self.next_before = page.next_before;
    }
}

impl Client {
    pub fn search_codex(&self, query: &str) -> Result<Codex, String> {
        let path = format!("/api/v1/search?surface=codex&q={}", enc(query));
        match self.get_reply(&path) {
            Ok(value) => read_codex(&value),
            Err((code, body)) => Err(failure("Codex history search", code, &body)),
        }
    }

    pub fn history(&self, filter: &Filter, before: i64) -> Result<Page, String> {
        match self.get_reply(&filter.path(before)) {
            Ok(value) => crate::reply::parse(value).map_err(|e| format!("Agenthail sent activity history this app can't read: {e}")),
            Err((code, body)) => Err(failure("activity history", code, &body)),
        }
    }
}

#[derive(Clone, Copy)]
pub enum Pinned<'a> {
    Unpinned,
    Verified(&'a Session),
    Withheld,
}

pub fn lookup<'a>(listed: Option<&'a Session>, pinned: Pinned<'a>, cached: impl FnOnce() -> Option<&'a Session>) -> Option<&'a Session> {
    listed.or_else(|| match pinned {
        Pinned::Verified(x) => Some(x),
        Pinned::Withheld => None,
        Pinned::Unpinned => cached(),
    })
}

#[derive(Clone, Debug, PartialEq)]
pub enum Recheck {
    Fresh(Box<Session>),
    Gone,
    Unlisted(String),
    Unknown(String),
}

pub fn reread(detail: crate::gone::Detail, known: &Session) -> Recheck {
    use crate::gone::Detail;
    let v = match detail {
        Detail::Found(v) => v,
        Detail::Missing | Detail::Unreachable => return Recheck::Gone,
        Detail::Failed(e) => return Recheck::Unknown(e),
    };
    let Ok(fresh) = crate::reply::parse::<Session>(v["session"].clone()) else {
        return Recheck::Unknown("Agenthail sent a session this app can't read".into());
    };
    if fresh.id != known.id {
        return Recheck::Unknown("Agenthail answered for a different session".into());
    }
    let mut row = known.clone();
    row.surface = fresh.surface;
    row.name = one_line(&fresh.name);
    row.status = fresh.status;
    row.last_active = fresh.last_active;
    row.cwd = fresh.cwd.or(row.cwd);
    row.alias = v["alias"].as_str().filter(|a| !a.is_empty()).map(String::from);
    if let Ok(caps) = crate::reply::parse(v["capabilities"].clone()) {
        row.capabilities = caps;
    }
    row.read_only = v["readOnly"].as_bool().unwrap_or(false);
    Recheck::Fresh(Box::new(row))
}

fn hidden_from_list(x: &Session) -> bool {
    x.delegated == Some(true) || x.subagent.as_ref().is_some_and(|v| !v.is_null())
}

fn started_by_agent(raw: &Value) -> bool {
    !raw["subagent"].is_null() || matches!(raw["source"].as_str(), Some("exec" | "subAgent"))
}

impl Client {
    pub fn recheck(&self, known: &Session) -> Recheck {
        let detail = crate::gone::Detail::read(self.get_coded(&format!("/api/v1/session?id={}&limit=1", enc(&known.id))));
        let (stored, delegated) = match &detail {
            crate::gone::Detail::Found(v) => (v["session"]["name"].as_str().unwrap_or_default().to_string(), started_by_agent(&v["session"])),
            _ => (String::new(), false),
        };
        let found = reread(detail, known);
        let Recheck::Fresh(row) = &found else { return found };
        let probe = Session { name: stored, alias: row.alias.clone(), cwd: row.cwd.clone(), ..Session::default() };
        let terms = crate::gone::search_terms(std::iter::once(&probe));
        if terms.is_empty() {
            return Recheck::Unlisted("it has no name or folder to look it up by".into());
        }
        for term in terms {
            match self.listed(&row.id, &term) {
                Ok(Some(mut listed)) => {
                    listed.name = one_line(&listed.name);
                    return Recheck::Fresh(Box::new(listed));
                }
                Ok(None) => {}
                Err(e) => return Recheck::Unknown(e),
            }
        }
        if delegated || hidden_from_list(known) {
            return Recheck::Unlisted("Agenthail's session list hides this kind of session".into());
        }
        found
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Command {
    NewSession,
    Stop,
    Rename,
    Window,
    Inspector,
    Refresh,
    Restart,
    Activity,
}

#[derive(Clone, Debug, PartialEq)]
pub struct CommandRow {
    pub command: Command,
    pub title: String,
    pub shortcut: &'static str,
}

pub struct Selected<'a> {
    pub title: &'a str,
    pub can_stop: bool,
    pub inspector: bool,
}

pub fn commands(query: &str, selected: Option<Selected>) -> Vec<CommandRow> {
    let q = one_line(query).to_lowercase();
    let mut rows = vec![CommandRow { command: Command::NewSession, title: "New session".into(), shortcut: "⌘N" }];
    if let Some(x) = selected {
        if x.can_stop {
            rows.push(CommandRow { command: Command::Stop, title: format!("Stop {}", one_line(x.title)), shortcut: "⌘." });
        }
        rows.push(CommandRow { command: Command::Rename, title: format!("Rename {}", one_line(x.title)), shortcut: "" });
        rows.push(CommandRow { command: Command::Window, title: format!("Open {} in new window", one_line(x.title)), shortcut: "" });
        rows.push(CommandRow { command: Command::Inspector, title: if x.inspector { "Hide inspector" } else { "Show inspector" }.into(), shortcut: "⌥⌘I" });
    }
    rows.push(CommandRow { command: Command::Refresh, title: "Refresh".into(), shortcut: "⌘R" });
    rows.push(CommandRow { command: Command::Restart, title: "Restart Agenthail".into(), shortcut: "" });
    rows.push(CommandRow { command: Command::Activity, title: "Activity history".into(), shortcut: "" });
    rows.retain(|r| q.is_empty() || r.title.to_lowercase().contains(&q));
    rows.sort_by_key(|r| !names(&r.title, &q));
    rows
}

fn names(title: &str, q: &str) -> bool {
    let q = one_line(q).to_lowercase();
    let title = title.to_lowercase();
    !q.is_empty() && (title == q || title.strip_prefix(&q).is_some_and(|rest| rest.starts_with(' ')))
}

pub fn commands_lead(query: &str, rows: &[CommandRow]) -> bool {
    rows.first().is_some_and(|r| names(&r.title, query))
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn a_null_results_list_reads_as_no_hits_and_a_missing_one_is_refused() {
        assert!(read_codex(&json!({"results": null})).is_ok());
        assert!(read_codex(&json!({})).is_err());
    }

    #[test]
    fn codex_search_waits_for_three_characters() {
        assert_eq!(codex_query("  ab "), None);
        assert_eq!(codex_query(" abc "), Some("abc".into()));
        assert_eq!(codex_query("été"), Some("été".into()));
    }

    #[test]
    fn codex_results_read_the_daemon_shape_and_keep_its_remote_error() {
        let v = json!({"results": [{"session": {"id": "c1", "surface": "codex", "name": "Fix the importer", "status": "idle", "lastActive": "2026-10-01T10:00:00Z", "capabilities": {"interrupt": true, "steer": true, "lifecycle": {"resume": true}}, "readOnly": false}, "snippet": "  Saved by Agenthail "}], "remoteError": "Codex history search is unavailable in this Codex version"});
        let found = read_codex(&v).unwrap();
        assert_eq!(found.hits.len(), 1);
        assert_eq!(found.hits[0].session.title(), "Fix the importer");
        assert_eq!(found.hits[0].snippet, "Saved by Agenthail");
        let multi = read_codex(&json!({"results": [{"session": {"id": "c2"}, "snippet": "first line\n\n  second\tline "}]})).unwrap();
        assert_eq!(multi.hits[0].snippet, "first line second line");
        let named = read_codex(&json!({"results": [{"session": {"id": "c3", "name": "Port the export\nto the new schema"}}]})).unwrap();
        assert_eq!(named.hits[0].session.title(), "Port the export to the new schema");
        assert_eq!(one_line(" Fix\nthe   importer "), "Fix the importer");
        assert!(found.hits[0].session.capabilities.lifecycle.resume);
        assert_eq!(found.note(1).unwrap(), "Only Agenthail's saved Codex conversations were searched: Codex history search is unavailable in this Codex version");
        let empty = read_codex(&json!({"results": [], "remoteError": ""})).unwrap();
        assert_eq!(empty.remote_error, None);
        assert_eq!(empty.note(0).as_deref(), Some("No older matches."));
        assert!(read_codex(&json!({"error": "x"})).is_err());
    }

    #[test]
    fn codex_results_leave_out_sessions_already_listed() {
        let hit = |id: &str| CodexHit { session: Session { id: id.into(), ..Session::default() }, snippet: String::new() };
        let found = Codex { hits: vec![hit("a"), hit("b"), hit("c")], remote_error: None };
        let ids: Vec<&str> = found.fresh(|id| id == "b").map(|h| h.session.id.as_str()).collect();
        assert_eq!(ids, ["a", "c"]);
    }

    #[test]
    fn failures_say_what_the_daemon_said_or_why_it_could_not() {
        assert_eq!(failure("activity history", 400, r#"{"error":{"code":"invalid_request","message":"history filters are too long"}}"#), "history filters are too long");
        assert_eq!(failure("activity history", 404, r#"{"error":{"code":"unknown_route","message":"no route"}}"#), "This Agenthail has no activity history; it runs an older version.");
        assert_eq!(failure("Codex history search", 403, r#"{"error":{"code":"forbidden","message":"token lacks read"}}"#), "This app's Agenthail token can't read Codex history search: token lacks read");
        assert_eq!(failure("Codex history search", 0, "connection refused"), "Couldn't reach Agenthail for Codex history search: connection refused");
        assert_eq!(failure("activity history", 500, "oops"), "activity history failed with status 500");
    }

    #[test]
    fn history_paths_carry_only_the_filters_set() {
        assert_eq!(Filter::new("", " ").path(0), "/api/v1/history?limit=25");
        assert_eq!(Filter::new(" delivery ", "fix the build").path(42), "/api/v1/history?limit=25&before=42&kind=delivery&q=fix+the+build");
        assert_eq!(Filter::new("", &"x".repeat(300)).query.len(), 200);
        let wide = Filter::new("", &"é".repeat(150)).query;
        assert_eq!(wide.len(), 200);
        assert_eq!(wide.chars().count(), 100);
        assert_eq!(Filter::new("", &format!("{}€", "x".repeat(199))).query.len(), 199);
    }

    fn entry(v: Value) -> Entry {
        crate::reply::parse(v).unwrap()
    }

    #[test]
    fn rows_read_like_the_mac_activity_list() {
        let e = entry(json!({"id": 7, "createdAt": "2026-11-26T10:00:00Z", "kind": "queue_delivery", "operation": "steer", "target": "@builder", "source": "@lead", "message": "keep going"}));
        assert_eq!(e.heading(), "Queue delivery · Steer");
        assert_eq!(e.route(), "@lead → @builder");
        assert_eq!(e.content(), "keep going");
        assert!(!e.is_error());
        let plain = entry(json!({"id": 8, "kind": "send", "operation": "message", "sessionId": "s1", "source": "s1", "result": "delivered"}));
        assert_eq!(plain.heading(), "Send");
        assert_eq!(plain.route(), "s1");
        assert_eq!(plain.content(), "delivered");
        let failed = entry(json!({"id": 9, "kind": "", "error": "host unreachable", "message": "hi"}));
        assert_eq!(failed.heading(), "Event");
        assert_eq!(failed.target(), "Agenthail");
        assert!(failed.is_error());
        assert_eq!(failed.content(), "host unreachable");
        assert_eq!(entry(json!({"id": 10})).content(), "No message content recorded");
    }

    #[test]
    fn pages_append_without_repeats_and_stop_when_the_daemon_has_no_more() {
        let page = |ids: &[i64], more: bool, next: i64| Page { items: ids.iter().map(|&id| Entry { id, ..Entry::default() }).collect(), has_more: more, next_before: next, kinds: vec!["send".into()] };
        let mut a = Activity::default();
        a.take(page(&[5, 4], true, 4), true);
        a.take(page(&[4, 3], false, 0), false);
        assert_eq!(a.items.iter().map(|e| e.id).collect::<Vec<_>>(), [5, 4, 3]);
        assert!(!a.has_more);
        assert_eq!(a.kinds, ["send"]);
        a.take(page(&[9], true, 0), true);
        assert_eq!(a.items.len(), 1);
        assert!(!a.has_more);
    }

    #[test]
    fn a_recheck_takes_the_daemons_current_row_and_keeps_what_it_does_not_send() {
        use crate::gone::Detail;
        let known = Session { id: "c1".into(), name: "Old name".into(), alias: Some("old".into()), status: "working".into(), archived: true, pinned_at: Some("2026-11-26T09:00:00Z".into()), cwd: Some("/work/importer".into()), ..Session::default() };
        let v = json!({"session": {"id": "c1", "surface": "codex", "name": "Fix the\nimporter", "status": "idle", "lastActive": "2026-11-26T10:00:00Z"}, "alias": "fixer", "capabilities": {"interrupt": false, "steer": true, "lifecycle": {"resume": true}}, "readOnly": true});
        let Recheck::Fresh(row) = reread(Detail::Found(v), &known) else { panic!("expected a fresh row") };
        assert_eq!((row.name.as_str(), row.status.as_str(), row.alias.as_deref()), ("Fix the importer", "idle", Some("fixer")));
        assert!(!row.capabilities.interrupt && row.capabilities.lifecycle.resume && row.read_only && row.archived);
        assert_eq!((row.pinned_at.as_deref(), row.cwd.as_deref()), (Some("2026-11-26T09:00:00Z"), Some("/work/importer")));
        let unaliased = reread(Detail::Found(json!({"session": {"id": "c1"}, "alias": ""})), &known);
        assert!(matches!(unaliased, Recheck::Fresh(r) if r.alias.is_none()));
        assert_eq!(reread(Detail::Missing, &known), Recheck::Gone);
        assert_eq!(reread(Detail::Unreachable, &known), Recheck::Gone);
        assert_eq!(reread(Detail::Failed("timeout".into()), &known), Recheck::Unknown("timeout".into()));
        assert!(matches!(reread(Detail::Found(json!({"session": {"id": "other"}})), &known), Recheck::Unknown(_)));
    }

    #[test]
    fn a_recheck_reads_the_archived_state_from_the_session_list() {
        let known = Session { id: "c1".into(), name: "Fix the importer".into(), ..Session::default() };
        let detail = r#"{"session": {"id": "c1", "surface": "codex", "name": "Fix the\nimporter", "status": "busy"}, "capabilities": {"lifecycle": {"resume": true}}}"#;
        let listing = r#"{"sessions": [{"id": "c0", "name": "Fix the importer too"}, {"id": "c1", "name": "Fix the\nimporter", "status": "busy", "archived": true, "attention": {"kind": "request", "reason": "Pick a schema"}, "turnState": {"state": "waiting_person"}}], "nextCursor": ""}"#;
        let (client, asked) = crate::mock::daemon(vec![(200, detail.into()), (200, listing.into())]);
        let Recheck::Fresh(row) = client.recheck(&known) else { panic!("expected a fresh row") };
        assert!(row.archived && row.attention.is_some() && row.turn_state.is_some());
        assert_eq!(row.name, "Fix the importer");
        assert_eq!(asked.recv().unwrap().0, "/api/v1/session?id=c1&limit=1");
        assert_eq!(asked.recv().unwrap().0, "/api/v1/snapshot?q=Fix+the%0Aimporter&limit=200");
        let unlisted = r#"{"sessions": [], "nextCursor": ""}"#;
        let (client, _asked) = crate::mock::daemon(vec![(200, detail.into()), (200, unlisted.into())]);
        assert!(matches!(client.recheck(&known), Recheck::Fresh(r) if !r.archived && r.status == "busy"));
        let delegated = Session { delegated: Some(true), ..known.clone() };
        let (client, _hidden) = crate::mock::daemon(vec![(200, detail.into()), (200, unlisted.into())]);
        assert!(matches!(client.recheck(&delegated), Recheck::Unlisted(_)));
        for marked in [r#"{"session": {"id": "c1", "name": "Fix the importer", "source": "exec"}}"#, r#"{"session": {"id": "c1", "name": "Fix the importer", "source": "subAgent"}}"#, r#"{"session": {"id": "c1", "name": "Fix the importer", "subagent": {"parentId": "p1"}}}"#] {
            let (client, _marked) = crate::mock::daemon(vec![(200, marked.into()), (200, unlisted.into())]);
            assert!(matches!(client.recheck(&known), Recheck::Unlisted(_)), "{marked}");
        }
        let (client, _failed) = crate::mock::daemon(vec![(200, detail.into()), (500, r#"{"error":{"code":"internal_error","message":"boom"}}"#.into())]);
        assert!(matches!(client.recheck(&known), Recheck::Unknown(_)));
    }

    #[test]
    fn commands_follow_the_selected_session_and_the_query() {
        let all: Vec<Command> = commands("", None).into_iter().map(|r| r.command).collect();
        assert_eq!(all, [Command::NewSession, Command::Refresh, Command::Restart, Command::Activity]);
        let idle = commands("", Some(Selected { title: "@fixer", can_stop: false, inspector: false }));
        assert_eq!(idle.iter().map(|r| r.command).collect::<Vec<_>>(), [Command::NewSession, Command::Rename, Command::Window, Command::Inspector, Command::Refresh, Command::Restart, Command::Activity]);
        assert_eq!(idle[1].title, "Rename @fixer");
        assert_eq!(idle[2].title, "Open @fixer in new window");
        assert_eq!((idle[3].title.as_str(), idle[3].shortcut), ("Show inspector", "⌥⌘I"));
        assert_eq!(commands("inspector", Some(Selected { title: "@fixer", can_stop: false, inspector: true }))[0].title, "Hide inspector");
        let working = commands("stop", Some(Selected { title: "@fixer", can_stop: true, inspector: false }));
        assert_eq!(working.len(), 1);
        assert_eq!(working[0].title, "Stop @fixer");
        assert_eq!(commands("FIXER", Some(Selected { title: "@fixer", can_stop: true, inspector: false })).len(), 3);
        assert!(commands("zzz", None).is_empty());
    }

    #[test]
    fn a_query_that_names_a_command_puts_it_first() {
        for q in ["Activity history", "activity history", "  ACTIVITY   history ", "activity"] {
            let rows = commands(q, None);
            assert_eq!(rows[0].command, Command::Activity, "{q}");
            assert!(commands_lead(q, &rows), "{q}");
        }
        let rows = commands("re", Some(Selected { title: "@fixer", can_stop: false, inspector: false }));
        assert_eq!(rows.iter().map(|r| r.command).collect::<Vec<_>>(), [Command::Rename, Command::Refresh, Command::Restart]);
        assert!(!commands_lead("re", &rows));
        let rows = commands("restart", None);
        assert_eq!(rows.iter().map(|r| r.command).collect::<Vec<_>>(), [Command::Restart]);
        assert_eq!(rows[0].title, "Restart Agenthail");
        assert!(commands_lead("restart", &rows));
        let rows = commands("refresh", Some(Selected { title: "@refresher", can_stop: false, inspector: false }));
        assert_eq!(rows.iter().map(|r| r.command).collect::<Vec<_>>(), [Command::Refresh, Command::Rename, Command::Window]);
        assert!(commands_lead("refresh", &rows));
        let rows = commands("history", None);
        assert_eq!(rows[0].command, Command::Activity);
        assert!(!commands_lead("history", &rows));
        assert!(!commands_lead("", &commands("", None)));
        assert!(!commands_lead("zzz", &[]));
        let named = commands("rename fix the importer", Some(Selected { title: "Fix the\n  importer", can_stop: true, inspector: false }));
        assert_eq!(named.iter().map(|r| r.title.as_str()).collect::<Vec<_>>(), ["Rename Fix the importer"]);
        assert!(commands_lead("Rename Fix the importer", &named));
    }

    #[test]
    fn an_unverified_search_pin_hides_a_cached_row() {
        let listed = Session { id: "c1".into(), name: "Listed".into(), ..Session::default() };
        let pinned = Session { id: "c1".into(), name: "Pinned".into(), ..Session::default() };
        let cached = Session { id: "c1".into(), name: "Cached".into(), ..Session::default() };
        let name = |x: Option<&Session>| x.map(|x| x.name.clone());
        assert_eq!(name(lookup(None, Pinned::Withheld, || Some(&cached))), None);
        assert_eq!(name(lookup(None, Pinned::Verified(&pinned), || Some(&cached))), Some("Pinned".into()));
        assert_eq!(name(lookup(None, Pinned::Unpinned, || Some(&cached))), Some("Cached".into()));
        assert_eq!(name(lookup(Some(&listed), Pinned::Withheld, || Some(&cached))), Some("Listed".into()));
    }
}
