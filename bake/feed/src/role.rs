use anyhow::Result;
use serde::Deserialize;
use serde_json::Value;

use crate::catalog::{Client, enc};

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct SessionRole {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub reach: String,
    #[serde(default)]
    pub source: String,
    #[serde(default)]
    pub replaces: String,
    #[serde(default)]
    pub label: String,
}

impl SessionRole {
    pub fn shown(&self) -> Option<(&str, &str)> {
        let label = self.label.trim();
        (!label.is_empty()).then_some((label, self.source.trim()))
    }
}

pub fn role_of(detail: &Value) -> Option<SessionRole> {
    serde_json::from_value(detail.get("role")?.clone()).ok()
}

impl Client {
    pub fn session_role(&self, session: &str) -> Result<Option<SessionRole>> {
        Ok(role_of(&self.get_json(&format!("/api/v1/session?id={}&limit=1", enc(session)))?))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn a_role_reads_as_its_label_with_where_it_came_from() {
        let detail = json!({"session": {"id": "s1"}, "role": {"name": "reviewer", "reach": "project", "source": "assigned to this session", "replaces": "lead", "label": "lead (replaced by reviewer)"}});
        let role = role_of(&detail).unwrap();
        assert_eq!(role.shown(), Some(("lead (replaced by reviewer)", "assigned to this session")));
        assert_eq!(role_of(&json!({"session": {"id": "s1"}})), None);
        assert_eq!(role_of(&json!({"role": {"name": "worker", "label": " "}})).unwrap().shown(), None);
    }

    #[test]
    fn the_role_comes_from_one_short_session_read() {
        let (client, seen) = crate::mock::daemon(crate::mock::replies(&[
            (200, r#"{"session":{"id":"h1::s 1"},"role":{"name":"worker","reach":"descendants","source":"the default for sessions an agent starts","label":"worker"}}"#),
            (200, r#"{"session":{"id":"h1::s 1"}}"#),
            (404, r#"{"error":{"code":"not_found","message":"session not found"}}"#),
        ]));
        assert_eq!(client.session_role("h1::s 1").unwrap().unwrap().label, "worker");
        assert_eq!(client.session_role("h1::s 1").unwrap(), None);
        assert!(client.session_role("h1::s 1").is_err());
        let paths: Vec<String> = seen.try_iter().map(|(path, _)| path).collect();
        assert_eq!(paths, vec!["/api/v1/session?id=h1%3A%3As+1&limit=1"; 3]);
    }
}
