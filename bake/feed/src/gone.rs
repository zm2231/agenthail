use serde_json::Value;

use crate::catalog::{Client, RespondError, Session, enc};

#[derive(Clone, Debug, PartialEq)]
pub enum Why {
    Archived,
    Offline(String),
    Unlisted,
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
    pub user: String,
    pub assistant: String,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Gone {
    pub row: Session,
    pub why: Why,
    pub fix: Option<Fix>,
    pub exchanges: Vec<Exchange>,
}

impl Gone {
    pub fn note(&self) -> String {
        match &self.why {
            Why::Archived => "This session is archived. You're reading what it last had.".into(),
            Why::Offline(host) if host.is_empty() => "This session is offline because its computer isn't reachable.".into(),
            Why::Offline(host) => format!("This session is offline because {host} isn't reachable."),
            Why::Unlisted => "Agenthail no longer lists this session. You're reading what it last had.".into(),
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
        v["exchanges"].as_array().into_iter().flatten().map(|e| Exchange { user: text(e, "user"), assistant: text(e, "assistant") }).filter(|e| !e.user.is_empty() || !e.assistant.is_empty()).collect()
    }

    fn row(&self, id: &str) -> Option<Session> {
        let Detail::Found(v) = self else { return None };
        let raw = &v["session"];
        let text = |k: &str| raw[k].as_str().unwrap_or_default().to_string();
        let alias = v["alias"].as_str().filter(|a| !a.is_empty()).map(String::from);
        Some(Session { id: id.to_string(), surface: text("surface"), name: text("name"), alias, status: text("status"), last_active: text("lastActive"), cwd: raw["cwd"].as_str().map(String::from), ..Session::default() })
    }
}

fn reachable(x: &Session) -> bool {
    matches!(x.host.state.as_str(), "" | "local" | "connected") && x.unavailable_reason.as_deref().is_none_or(str::is_empty)
}

pub fn classify(id: &str, detail: &Detail, listed: Option<Session>, known: Option<&Session>) -> Result<Gone, String> {
    let row = listed.clone().or_else(|| known.cloned()).or_else(|| detail.row(id));
    let row = match (row, detail) {
        (Some(r), _) => r,
        (None, Detail::Unreachable) => Session { id: id.to_string(), ..Session::default() },
        (None, Detail::Missing) => return Err("Agenthail has no session with this id any more.".into()),
        (None, Detail::Failed(e)) => return Err(format!("Couldn't look this session up: {e}")),
        (None, Detail::Found(_)) => unreachable!("a found detail always gives a row"),
    };
    let archived = listed.as_ref().map_or(row.archived, |l| l.archived);
    let why = if archived {
        Why::Archived
    } else if *detail == Detail::Unreachable || !reachable(&row) {
        Why::Offline(row.host.name.clone())
    } else {
        Why::Unlisted
    };
    let fix = match why {
        Why::Archived => Some(Fix::Unarchive),
        _ if row.capabilities.lifecycle.resume || detail.resume() => Some(Fix::Resume),
        _ => None,
    };
    Ok(Gone { row, why, fix, exchanges: detail.exchanges() })
}

const PAGES: usize = 10;

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
    pub fn look_up(&self, id: &str, known: Option<&Session>) -> Result<Gone, String> {
        let detail = Detail::read(self.get_coded(&format!("/api/v1/session?id={}&limit=20", enc(id))));
        let found = detail.row(id);
        let terms = search_terms([known, found.as_ref()].into_iter().flatten());
        let listed = terms.iter().find_map(|term| self.listed(id, term));
        classify(id, &detail, listed, known)
    }

    fn listed(&self, id: &str, term: &str) -> Option<Session> {
        let mut cursor: Option<String> = None;
        for _ in 0..PAGES {
            let mut path = format!("/api/v1/snapshot?q={}&limit=200", enc(term));
            if let Some(c) = &cursor {
                path.push_str(&format!("&cursor={}", enc(c)));
            }
            let page = self.get_json(&path).ok()?;
            if let Some(row) = page["sessions"].as_array().into_iter().flatten().find(|r| r["id"] == id) {
                return serde_json::from_value(row.clone()).ok();
            }
            cursor = Some(page["nextCursor"].as_str().filter(|c| !c.is_empty())?.to_string());
        }
        None
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
        serde_json::from_value(json).unwrap()
    }

    fn detail() -> Detail {
        Detail::Found(json!({"session": {"id": "s1", "name": "Fix the importer", "surface": "codex", "status": "idle", "lastActive": "2026-11-26T07:00:00Z"}, "alias": "fixer", "capabilities": {"lifecycle": {"resume": true}}}))
    }

    #[test]
    fn an_archived_listing_offers_unarchive_whatever_its_capabilities() {
        let listed = row(json!({"id": "s1", "name": "Fix the importer", "archived": true, "host": {"name": "studio", "state": "local"}}));
        let g = classify("s1", &detail(), Some(listed), None).unwrap();
        assert_eq!(g.why, Why::Archived);
        assert_eq!(g.fix, Some(Fix::Unarchive));
        assert!(g.note().contains("archived"));
    }

    #[test]
    fn an_unreachable_host_is_offline_and_resumes_only_when_the_session_can() {
        let known = row(json!({"id": "s1", "name": "Fix the importer", "host": {"name": "buildbox", "state": "connected"}}));
        let g = classify("s1", &Detail::Unreachable, None, Some(&known)).unwrap();
        assert_eq!(g.why, Why::Offline("buildbox".into()));
        assert_eq!(g.fix, None);
        assert_eq!(g.note(), "This session is offline because buildbox isn't reachable.");
        let able = row(json!({"id": "s1", "host": {"name": "buildbox", "state": "disconnected"}, "capabilities": {"lifecycle": {"resume": true}}}));
        let g = classify("s1", &Detail::Failed("timeout".into()), Some(able), None).unwrap();
        assert_eq!((g.why, g.fix), (Why::Offline("buildbox".into()), Some(Fix::Resume)));
    }

    #[test]
    fn an_unavailable_reason_counts_as_offline() {
        let listed = row(json!({"id": "s1", "host": {"name": "studio", "state": "local"}, "unavailableReason": "host down"}));
        assert_eq!(classify("s1", &detail(), Some(listed), None).unwrap().why, Why::Offline("studio".into()));
    }

    #[test]
    fn a_reachable_session_that_left_the_list_is_unlisted_and_uses_the_detail_for_its_header() {
        let g = classify("s1", &detail(), None, None).unwrap();
        assert_eq!(g.why, Why::Unlisted);
        assert_eq!(g.fix, Some(Fix::Resume));
        assert_eq!((g.row.name.as_str(), g.row.alias.as_deref(), g.row.surface.as_str()), ("Fix the importer", Some("fixer"), "codex"));
    }

    #[test]
    fn the_listing_decides_archived_over_an_older_known_copy() {
        let known = row(json!({"id": "s1", "archived": true}));
        let listed = row(json!({"id": "s1", "host": {"state": "local"}}));
        assert_eq!(classify("s1", &detail(), Some(listed), Some(&known)).unwrap().why, Why::Unlisted);
        assert_eq!(classify("s1", &detail(), None, Some(&known)).unwrap().why, Why::Archived);
    }

    #[test]
    fn a_session_the_daemon_never_heard_of_gives_an_error_not_a_spinner() {
        assert!(classify("s1", &Detail::Missing, None, None).unwrap_err().contains("no session"));
        assert!(classify("s1", &Detail::Failed("refused".into()), None, None).unwrap_err().contains("refused"));
        let g = classify("s1", &Detail::Unreachable, None, None).unwrap();
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
        let g = classify("s1", &d, None, None).unwrap();
        assert_eq!(g.exchanges, vec![Exchange { user: "Speed it up".into(), assistant: "Done, twice as fast".into() }]);
        assert!(classify("s1", &Detail::Unreachable, None, None).unwrap().exchanges.is_empty());
    }

    #[test]
    fn the_search_tries_name_alias_and_folder_once_each() {
        let known = row(json!({"id": "s1", "name": "Fix it", "alias": "fixer", "cwd": "/work/app"}));
        let found = row(json!({"id": "s1", "name": "Fix it", "cwd": " "}));
        assert_eq!(search_terms([&known, &found].into_iter()), vec!["Fix it", "fixer", "/work/app"]);
        assert!(search_terms([&row(json!({"id": "s1"}))].into_iter()).is_empty());
    }

    #[test]
    fn error_replies_are_read_by_status_and_code() {
        assert_eq!(Detail::read(Err((503, "host_unreachable".into()))), Detail::Unreachable);
        assert_eq!(Detail::read(Err((404, "not_found".into()))), Detail::Missing);
        assert_eq!(Detail::read(Err((409, "host_mismatch".into()))), Detail::Failed("host_mismatch".into()));
        assert_eq!(Detail::read(Err((500, String::new()))), Detail::Failed("status code 500".into()));
    }
}
