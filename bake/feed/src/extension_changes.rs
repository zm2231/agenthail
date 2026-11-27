use serde::Deserialize;
use serde_json::{Value, json};
use time::{OffsetDateTime, UtcOffset};

use crate::background::Clock;
use crate::catalog::{Client, RespondError, confirmed, enc, parse_time, request_key};

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Files {
    pub id: String,
    #[serde(default)]
    pub created: bool,
    #[serde(default)]
    pub removed: bool,
    #[serde(default)]
    pub added: Vec<String>,
    #[serde(default)]
    pub updated: Vec<String>,
    #[serde(default)]
    pub deleted: Vec<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Change {
    pub rev: String,
    #[serde(default)]
    pub author: String,
    #[serde(default)]
    pub time: String,
    #[serde(default)]
    pub message: String,
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub extensions: Vec<Files>,
    #[serde(default)]
    pub root: bool,
    #[serde(default)]
    pub checks: Vec<String>,
}

impl Change {
    pub fn describe(&self) -> String {
        let parts: Vec<String> = self
            .extensions
            .iter()
            .map(|f| {
                if f.created {
                    return format!("created {}", f.id);
                }
                if f.removed {
                    return format!("removed {}", f.id);
                }
                let count = f.added.len() + f.updated.len() + f.deleted.len();
                format!("changed {count} {} in {}", if count == 1 { "file" } else { "files" }, f.id)
            })
            .collect();
        if !parts.is_empty() {
            return parts.join(", ");
        }
        [&self.title, &self.message].into_iter().map(|t| t.trim()).find(|t| !t.is_empty()).unwrap_or("no extension files").to_string()
    }

    pub fn meta(&self, clock: Clock, offset_at: impl Fn(OffsetDateTime) -> i32) -> String {
        let mut parts = Vec::new();
        if let Some(at) = parse_time(&self.time) {
            let at = at.to_offset(UtcOffset::from_whole_seconds(offset_at(at)).unwrap_or(UtcOffset::UTC));
            let month = at.month().to_string();
            parts.push(format!("{} {}, {} at {}", &month[..3], at.day(), at.year(), clock.show(at.hour(), at.minute())));
        }
        if !self.author.trim().is_empty() {
            parts.push(self.author.trim().to_string());
        }
        parts.extend(self.checks.iter().filter(|c| !c.trim().is_empty()).cloned());
        parts.join(" · ")
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Changes {
    #[serde(default)]
    pub changes: Vec<Change>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Shown {
    #[serde(default)]
    pub patch: String,
    #[serde(default)]
    pub truncated: bool,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Started {
    #[serde(default)]
    pub session: Option<StartedSession>,
    #[serde(default)]
    pub status: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct StartedSession {
    #[serde(default)]
    pub id: String,
}

impl Started {
    pub fn session_id(&self) -> Option<String> {
        self.session.as_ref().map(|s| s.id.trim().to_string()).filter(|id| !id.is_empty())
    }

    pub fn unconfirmed(&self) -> bool {
        self.status == "submitted"
    }
}

pub fn on_host(path: &str, host: Option<&str>) -> String {
    match host.filter(|h| !h.is_empty()) {
        Some(host) => format!("{path}{}host={}", if path.contains('?') { '&' } else { '?' }, enc(host)),
        None => path.to_string(),
    }
}

fn rev_path(rev: &str) -> String {
    format!("/api/v1/extensions/history/{}", url::form_urlencoded::byte_serialize(rev.as_bytes()).collect::<String>().replace('+', "%20"))
}

pub fn build_title(host_name: Option<&str>) -> String {
    match host_name {
        Some(name) => format!("New Extension on {name}"),
        None => "New Extension".into(),
    }
}

pub fn build_blocked(listed: bool, error: Option<&str>, host_name: &str) -> Option<String> {
    if let Some(error) = error {
        return Some(error.to_string());
    }
    (!listed).then(|| format!("Waiting for {host_name} to list its extensions."))
}

fn unreadable(what: &str, e: serde_json::Error, maybe_delivered: bool) -> RespondError {
    RespondError { message: format!("the {what} reply couldn't be read: {e}"), code: "unreadable_reply".into(), maybe_delivered, answered: true, reply: Value::Null, status: 0 }
}

impl Client {
    fn read_extensions<T: serde::de::DeserializeOwned>(&self, path: &str) -> Result<T, String> {
        let reply = match self.get_reply(path) {
            Ok(reply) => reply,
            Err((0, error)) => return Err(error),
            Err((code, body)) => return Err(RespondError::from_status("read", code, &body).message),
        };
        serde_json::from_value(reply).map_err(|e| format!("the reply couldn't be read: {e}"))
    }

    pub fn extension_changes(&self, host: Option<&str>, limit: usize) -> Result<Vec<Change>, String> {
        self.read_extensions::<Changes>(&on_host(&format!("/api/v1/extensions/history?limit={limit}"), host)).map(|c| c.changes)
    }

    pub fn extension_change(&self, host: Option<&str>, rev: &str) -> Result<Shown, String> {
        self.read_extensions(&on_host(&rev_path(rev), host))
    }

    pub fn undo_extension_change(&self, host: Option<&str>, rev: &str) -> std::result::Result<(), RespondError> {
        self.post_empty(&on_host(&format!("{}/undo", rev_path(rev)), host), "undo", &request_key()).map(drop)
    }

    pub fn build_extension(&self, host: Option<&str>, message: &str, key: &str) -> std::result::Result<Started, RespondError> {
        let reply = self.post_json_keyed(&on_host("/api/v1/extensions/build", host), "build", json!({"message": message}), key)?;
        let started: Started = serde_json::from_value(reply.clone()).map_err(|e| unreadable("builder", e, true))?;
        match started.session_id() {
            None => confirmed(reply).map(|_| started),
            Some(_) => Ok(started),
        }
    }
}

#[cfg(test)]
#[path = "extension_changes_tests.rs"]
mod tests;
