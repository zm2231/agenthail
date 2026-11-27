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

impl Client {
    pub fn session_for_link(&self, reference: &str) -> Result<String, String> {
        match self.get_coded(&format!("/api/v1/session?id={}&limit=1", enc(reference))) {
            Ok(v) => canonical(&v).ok_or_else(|| NOT_FOUND.to_string()),
            Err((404, _)) => Err(NOT_FOUND.to_string()),
            Err((_, message)) if !message.is_empty() => Err(format!("Couldn't open the session in that link: {message}")),
            Err((code, _)) => Err(format!("Couldn't open the session in that link: status code {code}")),
        }
    }
}

pub fn canonical(detail: &Value) -> Option<String> {
    detail["session"]["id"].as_str().map(str::trim).filter(|id| !id.is_empty()).map(String::from)
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
    fn an_unlisted_reference_takes_the_id_the_daemon_returns() {
        assert_eq!(canonical(&serde_json::json!({"session": {"id": "019a-thread", "name": "Fix"}})), Some("019a-thread".into()));
        assert_eq!(canonical(&serde_json::json!({"session": {"id": "  "}})), None);
        assert_eq!(canonical(&serde_json::json!({"session": {}})), None);
        assert_eq!(canonical(&serde_json::json!({})), None);
    }

    #[test]
    fn the_daemon_resolves_an_unlisted_alias_to_its_id() {
        let (client, paths) = crate::mock::daemon(vec![
            (200, r#"{"session": {"id": "019a-thread"}}"#.into()),
            (404, r#"{"error": {"code": "not_found"}}"#.into()),
        ]);
        assert_eq!(client.session_for_link("@optimizer"), Ok("019a-thread".into()));
        assert_eq!(paths.recv().unwrap().0, "/api/v1/session?id=%40optimizer&limit=1");
        assert_eq!(client.session_for_link("@nobody"), Err(NOT_FOUND.into()));
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
