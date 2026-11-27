use serde_json::Value;

use crate::catalog::{Client, Session, enc};

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Link {
    Open,
    NewSession,
    Session(String),
}

impl Link {
    pub fn parse(url: &str, scheme: &str) -> Option<Link> {
        let (given, rest) = url.split_once("://")?;
        if !given.eq_ignore_ascii_case(scheme) || rest.contains(['?', '#']) {
            return None;
        }
        let (host, path) = rest.split_once('/').unwrap_or((rest, ""));
        if host.contains(['@', ':']) {
            return None;
        }
        let parts: Vec<String> = path.split('/').filter(|p| !p.is_empty()).map(decode).collect::<Option<_>>()?;
        match (host.to_ascii_lowercase().as_str(), parts.as_slice()) {
            ("" | "open", []) => Some(Link::Open),
            ("new", []) => Some(Link::NewSession),
            ("session", [reference]) => {
                let reference = reference.trim();
                (!reference.is_empty()).then(|| Link::Session(reference.to_string()))
            }
            _ => None,
        }
    }
}

pub fn matches(session: &Session, reference: &str) -> bool {
    if session.id == reference {
        return true;
    }
    let bare = reference.strip_prefix('@').unwrap_or(reference);
    session.alias.as_deref().is_some_and(|a| !a.is_empty() && a == bare)
}

pub fn resolve<'a>(sessions: &'a [Session], reference: &str) -> Option<&'a str> {
    sessions.iter().find(|x| matches(x, reference)).map(|x| x.id.as_str())
}

pub const NOT_FOUND: &str = "No session matches that link.";

const DETAIL_FIELDS: [&str; 7] = ["alias", "capabilities", "readOnly", "readOnlyReason", "limitResume", "attention", "ask"];

impl Client {
    pub fn session_for_link(&self, reference: &str) -> Result<Session, String> {
        match self.get_coded(&format!("/api/v1/session?id={}&limit=1", enc(reference))) {
            Ok(v) => listed_row(&v).ok_or_else(|| NOT_FOUND.to_string()),
            Err((404, _)) => Err(NOT_FOUND.to_string()),
            Err((503, code)) if code == "host_unreachable" => Err("The computer that session runs on isn't reachable right now.".into()),
            Err((0, _)) => Err("Couldn't reach Agenthail to open that link.".into()),
            Err((code, _)) => Err(format!("Couldn't open the session in that link (status code {code}).")),
        }
    }
}

pub fn listed_row(detail: &Value) -> Option<Session> {
    let mut row = detail["session"].as_object()?.clone();
    for key in DETAIL_FIELDS {
        if let Some(v) = detail.get(key).filter(|v| !v.is_null()) {
            row.insert(key.into(), v.clone());
        }
    }
    row.insert("open".into(), Value::Bool(true));
    let mut session: Session = serde_json::from_value(Value::Object(row)).ok()?;
    session.id = session.id.trim().to_string();
    session.alias = session.alias.filter(|a| !a.is_empty());
    (!session.id.is_empty()).then_some(session)
}

fn decode(part: &str) -> Option<String> {
    let bytes = part.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' {
            let hex = std::str::from_utf8(bytes.get(i + 1..i + 3)?).ok()?;
            out.push(u8::from_str_radix(hex, 16).ok()?);
            i += 3;
        } else {
            out.push(bytes[i]);
            i += 1;
        }
    }
    String::from_utf8(out).ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn link(url: &str) -> Option<Link> {
        Link::parse(url, "agenthail")
    }

    #[test]
    fn links_read_like_the_mac_app() {
        assert_eq!(link("agenthail://"), Some(Link::Open));
        assert_eq!(link("agenthail://open"), Some(Link::Open));
        assert_eq!(link("AgentHail://OPEN"), Some(Link::Open));
        assert_eq!(link("agenthail://new"), Some(Link::NewSession));
        assert_eq!(link("agenthail://session/019a-thread"), Some(Link::Session("019a-thread".into())));
        assert_eq!(link("agenthail://session/@optimizer"), Some(Link::Session("@optimizer".into())));
        assert_eq!(link("agenthail://session/a%20b"), Some(Link::Session("a b".into())));
        assert_eq!(link("agenthail://session/a/"), Some(Link::Session("a".into())));
    }

    #[test]
    fn anything_else_is_refused() {
        for url in [
            "agenthail://session",
            "agenthail://session/",
            "agenthail://session/%20",
            "agenthail://session/a/b",
            "agenthail://session/%zz",
            "agenthail://session/%ff",
            "agenthail://pair?endpoint=x&secret=y",
            "agenthail://unknown",
            "agenthail://open/extra",
            "agenthail://new/x",
            "https://session/a",
            "agenthail-bake://open",
            "agenthail:open",
            "agenthail://?x=1",
            "agenthail://open?x=1",
            "agenthail://open?",
            "agenthail://new#x",
            "agenthail://session/a?x=1",
            "agenthail://session/a#x",
            "agenthail://user@session/a",
            "agenthail://session:1/a",
        ] {
            assert_eq!(link(url), None, "{url}");
        }
    }

    #[test]
    fn test_builds_answer_only_their_own_scheme() {
        assert_eq!(Link::parse("agenthail-bake://new", "agenthail-bake"), Some(Link::NewSession));
        assert_eq!(Link::parse("agenthail://new", "agenthail-bake"), None);
    }

    #[test]
    fn an_unlisted_reference_reads_the_session_the_daemon_returns() {
        let detail = serde_json::json!({
            "session": {"id": "019a-thread", "surface": "codex", "name": "Fix the importer", "status": "idle", "cwd": "/work"},
            "alias": "optimizer",
            "capabilities": {"interrupt": true, "steer": true},
            "readOnly": true,
            "readOnlyReason": "Another computer owns it",
            "exchanges": [],
        });
        let row = listed_row(&detail).unwrap();
        assert_eq!((row.id.as_str(), row.surface.as_str(), row.name.as_str(), row.status.as_str()), ("019a-thread", "codex", "Fix the importer", "idle"));
        assert_eq!(row.alias.as_deref(), Some("optimizer"));
        assert_eq!(row.cwd.as_deref(), Some("/work"));
        assert!(row.open);
        assert!(row.read_only);
        assert_eq!(listed_row(&serde_json::json!({"session": {"id": "c1"}, "alias": ""})).unwrap().alias, None);
        assert_eq!(listed_row(&serde_json::json!({"session": {"id": "  "}})), None);
        assert_eq!(listed_row(&serde_json::json!({"session": {}})), None);
        assert_eq!(listed_row(&serde_json::json!({})), None);
    }

    #[test]
    fn the_daemon_resolves_an_unlisted_alias_to_its_session() {
        let (client, paths) = crate::mock::daemon(vec![
            (200, r#"{"session": {"id": "019a-thread"}, "alias": "optimizer"}"#.into()),
            (404, r#"{"error": {"code": "not_found"}}"#.into()),
            (503, r#"{"error": {"code": "host_unreachable", "message": "raw"}}"#.into()),
            (500, r#"{"error": {"code": "internal"}}"#.into()),
        ]);
        let row = client.session_for_link("@optimizer").unwrap();
        assert_eq!((row.id.as_str(), row.alias.as_deref()), ("019a-thread", Some("optimizer")));
        assert_eq!(paths.recv().unwrap().0, "/api/v1/session?id=%40optimizer&limit=1");
        assert_eq!(client.session_for_link("@nobody"), Err(NOT_FOUND.into()));
        assert_eq!(client.session_for_link("x"), Err("The computer that session runs on isn't reachable right now.".into()));
        assert_eq!(client.session_for_link("x"), Err("Couldn't open the session in that link (status code 500).".into()));
    }

    #[test]
    fn a_reference_finds_a_session_by_id_or_exact_alias() {
        let s = Session { id: "019a-thread".into(), alias: Some("optimizer".into()), ..Session::default() };
        let other = Session { id: "c2".into(), alias: Some(String::new()), ..Session::default() };
        assert!(matches(&s, "019a-thread"));
        assert!(matches(&s, "@optimizer"));
        assert!(matches(&s, "optimizer"));
        assert!(!matches(&s, "Optimizer"));
        assert!(!matches(&s, "other"));
        assert!(!matches(&other, "@"));
        let list = [other.clone(), s.clone()];
        assert_eq!(resolve(&list, "@optimizer"), Some("019a-thread"));
        assert_eq!(resolve(&list, "c2"), Some("c2"));
        assert_eq!(resolve(&list, "@nobody"), None);
    }
}
