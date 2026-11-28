use serde_json::Value;

use crate::catalog::{Client, RespondError, Session, enc};

#[derive(Clone, Debug, PartialEq)]
pub enum Why {
    Archived,
    Offline(String),
    Unlisted,
    Unchecked(String),
}

#[derive(Clone, Debug, PartialEq)]
pub enum Listing {
    Found(Session),
    Absent,
    Unchecked(String),
    Unsearchable(String),
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Fix {
    Unarchive,
    Resume,
}

impl Fix {
    pub fn label(self) -> &'static str {
        match self {
            Fix::Unarchive => "Unarchive",
            Fix::Resume => "Resume",
        }
    }

    pub fn doing(self) -> &'static str {
        match self {
            Fix::Unarchive => "Unarchiving",
            Fix::Resume => "Resuming",
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct Exchange {
    pub from: String,
    pub user: String,
    pub assistant: String,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Gone {
    pub row: Session,
    pub why: Why,
    pub fix: Option<Fix>,
    pub exchanges: Vec<Exchange>,
    pub read_error: Option<String>,
    pub history: History,
    pub incomplete: bool,
}

pub const PAGE: usize = 50;

#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct History {
    pub returned: usize,
    pub cut_text: bool,
}

impl History {
    pub fn note(self) -> String {
        let older = if self.returned >= PAGE { format!("Showing the last {PAGE} exchanges.") } else { "Showing the latest exchanges Agenthail could load.".to_string() };
        let cut = if self.cut_text { " Long messages are cut short." } else { "" };
        format!("{older}{cut} Older ones aren't loaded here.")
    }
}

impl Gone {
    pub fn empty_note(&self) -> String {
        match (&self.read_error, &self.why) {
            (Some(_), _) => "Nothing could be read from this conversation.".into(),
            (None, Why::Offline(_)) => "Its conversation can't be read while it's offline.".into(),
            (None, _) => "Agenthail returned no conversation for this session.".into(),
        }
    }

    pub fn note(&self) -> String {
        match &self.why {
            Why::Archived => "This session is archived. You're reading what it last had.".into(),
            Why::Offline(host) if host.is_empty() => "This session is offline because its computer isn't reachable.".into(),
            Why::Offline(host) => format!("This session is offline because {host} isn't reachable."),
            Why::Unlisted => "Agenthail no longer lists this session. You're reading what it last had.".into(),
            Why::Unchecked(e) => format!("Agenthail no longer lists this session, and couldn't check whether it was archived: {e}"),
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub enum Detail {
    Found(Value),
    Unreachable,
    Missing,
    Failed(String),
}

impl Detail {
    pub fn read(result: Result<Value, (u16, String)>) -> Detail {
        match result {
            Ok(v) => Detail::Found(v),
            Err((503, code)) if code == "host_unreachable" => Detail::Unreachable,
            Err((404, _)) => Detail::Missing,
            Err((0, message)) => Detail::Failed(message),
            Err((code, name)) if name.is_empty() => Detail::Failed(format!("status code {code}")),
            Err((_, name)) => Detail::Failed(name),
        }
    }

    fn resume(&self) -> bool {
        match self {
            Detail::Found(v) => v["capabilities"]["lifecycle"]["resume"].as_bool().unwrap_or(false),
            _ => false,
        }
    }

    fn exchanges(&self) -> Vec<Exchange> {
        let Detail::Found(v) = self else { return Vec::new() };
        let text = |e: &Value, k: &str| e[k].as_str().unwrap_or_default().trim().to_string();
        v["exchanges"].as_array().into_iter().flatten().map(|e| Exchange { from: sender(&text(e, "origin"), &text(e, "sender")), user: text(e, "user"), assistant: text(e, "assistant") }).filter(|e| !e.user.is_empty() || !e.assistant.is_empty()).collect()
    }

    fn history(&self) -> History {
        let Detail::Found(v) = self else { return History::default() };
        let returned = v["transcriptReturnedExchanges"].as_u64().map(|x| x as usize).unwrap_or_else(|| v["exchanges"].as_array().map_or(0, Vec::len));
        History { returned, cut_text: v["transcriptTruncated"].as_bool().unwrap_or(false) }
    }

    fn read_error(&self) -> Option<String> {
        let Detail::Found(v) = self else { return None };
        match &v["readError"] {
            Value::Null => None,
            Value::String(e) if e.trim().is_empty() => None,
            Value::String(e) => Some(e.trim().to_string()),
            other => other["message"].as_str().map(String::from).or_else(|| Some(other.to_string())),
        }
    }

    fn row(&self, id: &str) -> Option<Session> {
        let Detail::Found(v) = self else { return None };
        let raw = &v["session"];
        let text = |k: &str| raw[k].as_str().unwrap_or_default().to_string();
        let alias = v["alias"].as_str().filter(|a| !a.is_empty()).map(String::from);
        Some(Session { id: id.to_string(), surface: text("surface"), name: text("name"), alias, status: text("status"), last_active: text("lastActive"), cwd: raw["cwd"].as_str().map(String::from), subagent: raw.get("subagent").filter(|v| !v.is_null()).cloned(), ..Session::default() })
    }
}

pub fn sender(origin: &str, sender: &str) -> String {
    match origin {
        "" | "person" => "You".into(),
        "peer" if !sender.is_empty() => sender.to_string(),
        "peer" => "Another agent".into(),
        other => {
            let words = other.replace(['-', '_'], " ");
            let mut c = words.chars();
            c.next().map(|f| f.to_uppercase().chain(c).collect()).unwrap_or_default()
        }
    }
}

fn reachable(x: &Session) -> bool {
    matches!(x.host.state.as_str(), "" | "local" | "connected") && x.unavailable_reason.as_deref().is_none_or(str::is_empty)
}

#[derive(Clone, Debug, PartialEq)]
pub enum Lost {
    Deleted(String),
    Failed(String),
}

impl Lost {
    pub fn message(&self) -> &str {
        match self {
            Lost::Deleted(m) | Lost::Failed(m) => m,
        }
    }
}

pub fn classify(id: &str, detail: &Detail, listing: Listing, known: Option<&Session>) -> Result<Gone, Lost> {
    let listed = match &listing {
        Listing::Found(r) => Some(r.clone()),
        _ => None,
    };
    match (detail, &listed) {
        (Detail::Missing, _) => return Err(Lost::Deleted("Agenthail has no session with this id any more.".into())),
        (Detail::Failed(e), None) => return Err(Lost::Failed(format!("Couldn't look this session up: {e}"))),
        _ => {}
    }
    let mut row = listed.clone().or_else(|| known.cloned()).or_else(|| detail.row(id)).unwrap_or_else(|| Session { id: id.to_string(), ..Session::default() });
    row.name = crate::search::one_line(&row.name);
    let archived = listed.as_ref().map_or(row.archived, |l| l.archived);
    let incomplete = matches!(detail, Detail::Failed(_)) || detail.read_error().is_some() || matches!(listing, Listing::Unchecked(_));
    let why = if archived {
        Why::Archived
    } else if *detail == Detail::Unreachable || !reachable(&row) {
        Why::Offline(row.host.name.clone())
    } else if let Listing::Unchecked(e) | Listing::Unsearchable(e) = listing {
        Why::Unchecked(e)
    } else {
        Why::Unlisted
    };
    let resume = match (&listed, detail) {
        (Some(l), _) => l.capabilities.lifecycle.resume,
        (None, Detail::Found(_)) => detail.resume(),
        _ => row.capabilities.lifecycle.resume,
    };
    let fix = match why {
        Why::Archived => Some(Fix::Unarchive),
        _ if resume => Some(Fix::Resume),
        _ => None,
    };
    Ok(Gone { row, why, fix, exchanges: detail.exchanges(), read_error: match detail {
        Detail::Failed(e) => Some(e.clone()),
        _ => detail.read_error(),
    }, history: detail.history(), incomplete })
}

pub fn search_terms<'a>(rows: impl Iterator<Item = &'a Session>) -> Vec<String> {
    let mut out: Vec<String> = Vec::new();
    for r in rows {
        for term in [Some(r.name.clone()), r.alias.clone(), r.cwd.clone()].into_iter().flatten() {
            let term = term.trim().to_string();
            if !term.is_empty() && !out.contains(&term) {
                out.push(term);
            }
        }
    }
    out
}

impl Client {
    pub fn look_up(&self, id: &str, known: Option<&Session>) -> Result<Gone, Lost> {
        let detail = Detail::read(self.get_coded(&format!("/api/v1/session?id={}&limit={PAGE}", enc(id))));
        let found = detail.row(id);
        let terms = search_terms([known, found.as_ref()].into_iter().flatten());
        let mut listing = if terms.is_empty() { Listing::Unsearchable("it has no name or folder to search by".into()) } else { Listing::Absent };
        for term in &terms {
            match self.listed(id, term) {
                Ok(Some(row)) => {
                    listing = Listing::Found(row);
                    break;
                }
                Ok(None) => {}
                Err(e) => listing = Listing::Unchecked(e),
            }
        }
        classify(id, &detail, listing, known)
    }

    pub(crate) fn listed(&self, id: &str, term: &str) -> Result<Option<Session>, String> {
        let mut cursor: Option<String> = None;
        loop {
            let mut path = format!("/api/v1/snapshot?q={}&limit=200", enc(term));
            if let Some(c) = &cursor {
                path.push_str(&format!("&cursor={}", enc(c)));
            }
            let page = self.get_json(&path).map_err(|e| e.to_string())?;
            if let Some(row) = page["sessions"].as_array().into_iter().flatten().find(|r| r["id"] == id) {
                return crate::reply::parse(row.clone()).map(Some).map_err(|e| e.to_string());
            }
            match page["nextCursor"].as_str().filter(|c| !c.is_empty() && cursor.as_deref() != Some(*c)) {
                Some(c) => cursor = Some(c.to_string()),
                None => return Ok(None),
            }
        }
    }

    pub fn fix(&self, id: &str, fix: Fix) -> Result<(), RespondError> {
        match fix {
            Fix::Unarchive => self.session_archive(id, false),
            Fix::Resume => self.action("session-lifecycle-resume", serde_json::json!({"sessionId": id})).map(drop),
        }
    }
}

pub fn next_after(order: &[String], id: &str, alive: impl Fn(&str) -> bool) -> Option<String> {
    let at = order.iter().position(|x| x == id)?;
    order[at + 1..].iter().chain(order[..at].iter().rev()).find(|x| *x != id && alive(x)).cloned()
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn row(json: Value) -> Session {
        crate::reply::parse(json).unwrap()
    }

    fn detail() -> Detail {
        Detail::Found(json!({"session": {"id": "s1", "name": "Fix the importer", "surface": "codex", "status": "idle", "lastActive": "2026-11-26T07:00:00Z"}, "alias": "fixer", "capabilities": {"lifecycle": {"resume": true}}}))
    }

    #[test]
    fn a_looked_up_subagent_keeps_its_parent() {
        let found = Detail::Found(json!({"session": {"id": "c1", "name": "Check the parser", "subagent": {"parentId": "p1", "rootId": "p1", "depth": 1, "nickname": "Boyle"}}}));
        let row = found.row("c1").unwrap();
        assert_eq!(crate::subagents::parent_of(&row).as_deref(), Some("p1"));
        assert_eq!(detail().row("s1").unwrap().subagent, None);
    }

    #[test]
    fn an_archived_listing_offers_unarchive_whatever_its_capabilities() {
        let listed = row(json!({"id": "s1", "name": "Fix the importer", "archived": true, "host": {"name": "studio", "state": "local"}}));
        let g = classify("s1", &detail(), Listing::Found(listed), None).unwrap();
        assert_eq!(g.why, Why::Archived);
        assert_eq!(g.fix, Some(Fix::Unarchive));
        assert!(g.note().contains("archived"));
    }

    #[test]
    fn an_unreachable_host_is_offline_and_resumes_only_when_the_session_can() {
        let known = row(json!({"id": "s1", "name": "Fix the importer", "host": {"name": "buildbox", "state": "connected"}}));
        let g = classify("s1", &Detail::Unreachable, Listing::Absent, Some(&known)).unwrap();
        assert_eq!(g.why, Why::Offline("buildbox".into()));
        assert_eq!(g.fix, None);
        assert_eq!(g.note(), "This session is offline because buildbox isn't reachable.");
        let able = row(json!({"id": "s1", "host": {"name": "buildbox", "state": "disconnected"}, "capabilities": {"lifecycle": {"resume": true}}}));
        let g = classify("s1", &Detail::Failed("timeout".into()), Listing::Found(able), None).unwrap();
        assert_eq!((g.why, g.fix), (Why::Offline("buildbox".into()), Some(Fix::Resume)));
    }

    #[test]
    fn an_unavailable_reason_counts_as_offline() {
        let listed = row(json!({"id": "s1", "host": {"name": "studio", "state": "local"}, "unavailableReason": "host down"}));
        assert_eq!(classify("s1", &detail(), Listing::Found(listed), None).unwrap().why, Why::Offline("studio".into()));
    }

    #[test]
    fn a_reachable_session_that_left_the_list_is_unlisted_and_uses_the_detail_for_its_header() {
        let g = classify("s1", &detail(), Listing::Absent, None).unwrap();
        assert_eq!(g.why, Why::Unlisted);
        assert_eq!(g.fix, Some(Fix::Resume));
        assert_eq!((g.row.name.as_str(), g.row.alias.as_deref(), g.row.surface.as_str()), ("Fix the importer", Some("fixer"), "codex"));
    }

    #[test]
    fn a_stored_name_with_line_breaks_heads_the_screen_on_one_line() {
        let d = Detail::Found(json!({"session": {"id": "s1", "name": "Port the export\n\n  to the new schema "}}));
        assert_eq!(classify("s1", &d, Listing::Absent, None).unwrap().row.title(), "Port the export to the new schema");
        assert_eq!(search_terms(d.row("s1").iter()), vec!["Port the export\n\n  to the new schema"]);
        let listed = row(json!({"id": "s1", "name": "Fix the\nimporter", "archived": true}));
        assert_eq!(classify("s1", &detail(), Listing::Found(listed), None).unwrap().row.name, "Fix the importer");
        let known = row(json!({"id": "s1", "name": "Fix\r\nit"}));
        assert_eq!(classify("s1", &Detail::Unreachable, Listing::Absent, Some(&known)).unwrap().row.name, "Fix it");
    }

    #[test]
    fn the_listing_decides_archived_over_an_older_known_copy() {
        let known = row(json!({"id": "s1", "archived": true}));
        let listed = row(json!({"id": "s1", "host": {"state": "local"}}));
        assert_eq!(classify("s1", &detail(), Listing::Found(listed), Some(&known)).unwrap().why, Why::Unlisted);
        assert_eq!(classify("s1", &detail(), Listing::Absent, Some(&known)).unwrap().why, Why::Archived);
    }

    #[test]
    fn a_session_the_daemon_never_heard_of_gives_an_error_not_a_spinner() {
        assert!(matches!(classify("s1", &Detail::Missing, Listing::Absent, None), Err(Lost::Deleted(m)) if m.contains("no session")));
        assert!(matches!(classify("s1", &Detail::Failed("refused".into()), Listing::Absent, None), Err(Lost::Failed(m)) if m.contains("refused")));
        let g = classify("s1", &Detail::Unreachable, Listing::Absent, None).unwrap();
        assert_eq!((g.row.id.as_str(), &g.why), ("s1", &Why::Offline(String::new())));
        assert!(g.note().contains("its computer"));
    }

    #[test]
    fn leaving_an_archived_session_goes_to_the_next_row_then_the_one_before() {
        let order: Vec<String> = ["a", "b", "c", "d"].map(String::from).to_vec();
        assert_eq!(next_after(&order, "b", |_| true).as_deref(), Some("c"));
        assert_eq!(next_after(&order, "b", |x| x != "c").as_deref(), Some("d"));
        assert_eq!(next_after(&order, "d", |_| true).as_deref(), Some("c"));
        assert_eq!(next_after(&order, "b", |x| x == "b"), None);
        assert_eq!(next_after(&order, "z", |_| true), None);
    }

    #[test]
    fn the_returned_exchanges_stand_in_for_the_transcript() {
        let d = Detail::Found(json!({"session": {"id": "s1"}, "exchanges": [{"user": "Speed it up", "assistant": "Done, twice as fast"}, {"user": " ", "assistant": ""}]}));
        let g = classify("s1", &d, Listing::Absent, None).unwrap();
        assert_eq!(g.exchanges, vec![Exchange { from: "You".into(), user: "Speed it up".into(), assistant: "Done, twice as fast".into() }]);
        assert!(classify("s1", &Detail::Unreachable, Listing::Absent, None).unwrap().exchanges.is_empty());
    }

    #[test]
    fn the_search_tries_name_alias_and_folder_once_each() {
        let known = row(json!({"id": "s1", "name": "Fix it", "alias": "fixer", "cwd": "/work/app"}));
        let found = row(json!({"id": "s1", "name": "Fix it", "cwd": " "}));
        assert_eq!(search_terms([&known, &found].into_iter()), vec!["Fix it", "fixer", "/work/app"]);
        assert!(search_terms([&row(json!({"id": "s1"}))].into_iter()).is_empty());
    }

    #[test]
    fn a_lookup_that_fails_shows_the_error_even_with_a_remembered_row() {
        let known = row(json!({"id": "s1", "name": "Fix it", "host": {"state": "local"}}));
        assert!(matches!(classify("s1", &Detail::Missing, Listing::Absent, Some(&known)), Err(Lost::Deleted(m)) if m.contains("no session")));
        assert!(matches!(classify("s1", &Detail::Failed("refused".into()), Listing::Unchecked("refused".into()), Some(&known)), Err(Lost::Failed(m)) if m.contains("refused")));
    }

    #[test]
    fn a_search_that_could_not_run_says_so_instead_of_unlisted() {
        let g = classify("s1", &detail(), Listing::Unchecked("timed out".into()), None).unwrap();
        assert_eq!(g.why, Why::Unchecked("timed out".into()));
        assert!(g.note().contains("couldn't check whether it was archived: timed out"));
    }

    #[test]
    fn a_lookup_that_failed_part_way_is_incomplete() {
        assert!(classify("s1", &detail(), Listing::Unchecked("timed out".into()), None).unwrap().incomplete);
        let listed = Session { id: "s1".into(), archived: true, ..Session::default() };
        assert!(classify("s1", &Detail::Failed("timeout".into()), Listing::Found(listed.clone()), None).unwrap().incomplete);
        assert!(!classify("s1", &detail(), Listing::Found(listed), None).unwrap().incomplete);
        assert!(!classify("s1", &detail(), Listing::Unsearchable("it has no name or folder to search by".into()), None).unwrap().incomplete);
        assert!(!classify("s1", &Detail::Unreachable, Listing::Absent, None).unwrap().incomplete);
        assert!(classify("s1", &Detail::Found(json!({"session": {"id": "s1"}, "readError": "transcript busy"})), Listing::Absent, None).unwrap().incomplete);
    }

    #[test]
    fn prompts_name_who_sent_them() {
        assert_eq!(sender("", ""), "You");
        assert_eq!(sender("person", ""), "You");
        assert_eq!(sender("peer", "@reviewer"), "@reviewer");
        assert_eq!(sender("peer", ""), "Another agent");
        assert_eq!(sender("task-notification", ""), "Task notification");
        let d = Detail::Found(json!({"session": {}, "exchanges": [{"user": "Check this", "origin": "peer", "sender": "@lead", "assistant": "On it"}], "readError": "transcript moved"}));
        let g = classify("s1", &d, Listing::Absent, None).unwrap();
        assert_eq!(g.exchanges[0].from, "@lead");
        assert_eq!(g.read_error.as_deref(), Some("transcript moved"));
    }

    #[test]
    fn the_reader_always_says_older_exchanges_are_not_loaded() {
        let d = |v: Value| classify("s1", &Detail::Found(v), Listing::Absent, None).unwrap().history.note();
        assert_eq!(d(json!({"session": {}, "exchanges": [], "transcriptReturnedExchanges": 50})), "Showing the last 50 exchanges. Older ones aren't loaded here.");
        assert_eq!(d(json!({"session": {}, "exchanges": [{"user": "a"}], "transcriptTruncated": true})), "Showing the latest exchanges Agenthail could load. Long messages are cut short. Older ones aren't loaded here.");
    }

    #[test]
    fn an_offline_session_with_nothing_read_says_why() {
        let known = row(json!({"id": "s1", "host": {"name": "buildbox", "state": "connected"}}));
        assert!(classify("s1", &Detail::Unreachable, Listing::Absent, Some(&known)).unwrap().empty_note().contains("offline"));
        assert!(classify("s1", &detail(), Listing::Absent, None).unwrap().empty_note().contains("no conversation"));
    }

    #[test]
    fn a_failed_read_beside_a_found_listing_shows_the_failure() {
        let listed = row(json!({"id": "s1", "archived": true}));
        let g = classify("s1", &Detail::Failed("conflict".into()), Listing::Found(listed), None).unwrap();
        assert_eq!(g.why, Why::Archived);
        assert_eq!(g.read_error.as_deref(), Some("conflict"));
        assert_eq!(g.empty_note(), "Nothing could be read from this conversation.");
    }

    #[test]
    fn resume_follows_the_freshest_answer() {
        let known = row(json!({"id": "s1", "host": {"state": "local"}, "capabilities": {"lifecycle": {"resume": true}}}));
        let now_no = Detail::Found(json!({"session": {}, "capabilities": {"lifecycle": {"resume": false}}}));
        assert_eq!(classify("s1", &now_no, Listing::Absent, Some(&known)).unwrap().fix, None);
        let listed_no = row(json!({"id": "s1", "host": {"name": "b", "state": "disconnected"}}));
        assert_eq!(classify("s1", &Detail::Unreachable, Listing::Found(listed_no), Some(&known)).unwrap().fix, None);
        let gone_host = row(json!({"id": "s1", "host": {"name": "b", "state": "disconnected"}, "capabilities": {"lifecycle": {"resume": true}}}));
        assert_eq!(classify("s1", &Detail::Unreachable, Listing::Absent, Some(&gone_host)).unwrap().fix, Some(Fix::Resume));
    }

    #[test]
    fn error_replies_are_read_by_status_and_code() {
        assert_eq!(Detail::read(Err((503, "host_unreachable".into()))), Detail::Unreachable);
        assert_eq!(Detail::read(Err((404, "not_found".into()))), Detail::Missing);
        assert_eq!(Detail::read(Err((409, "host_mismatch".into()))), Detail::Failed("host_mismatch".into()));
        assert_eq!(Detail::read(Err((500, String::new()))), Detail::Failed("status code 500".into()));
    }
}
