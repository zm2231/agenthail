use std::collections::BTreeMap;

use anyhow::Result;
use serde::Deserialize;
use serde_json::{Value, json};

use crate::catalog::{Client, RespondError, enc};

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Offer {
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub icon: String,
    #[serde(default)]
    pub order: i64,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Page {
    #[serde(default)]
    pub port: u16,
    #[serde(default)]
    pub entry: String,
    #[serde(default)]
    pub published: bool,
    #[serde(default)]
    pub error: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Mount {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub port: u16,
    #[serde(default)]
    pub entry: String,
    #[serde(default)]
    pub upstream: String,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub error: String,
    #[serde(default)]
    pub published: bool,
    #[serde(default)]
    pub key: String,
    #[serde(default)]
    pub signin: bool,
    #[serde(default)]
    pub app: Option<Offer>,
    #[serde(default)]
    pub badge: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Job {
    #[serde(default)]
    pub last_error: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ApprovalAsk {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub session_id: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Extension {
    pub id: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub error: String,
    #[serde(default)]
    pub page: Option<Page>,
    #[serde(default)]
    pub app: Option<Offer>,
    #[serde(default)]
    pub badge: String,
    #[serde(default)]
    pub capabilities: Vec<String>,
    #[serde(default)]
    pub pending_capabilities: Vec<String>,
    #[serde(default)]
    pub actions: Vec<String>,
    #[serde(default)]
    pub tools: Vec<String>,
    #[serde(default)]
    pub tools_note: String,
    #[serde(default)]
    pub jobs: BTreeMap<String, Job>,
    #[serde(default)]
    pub last_event_error: String,
    #[serde(default)]
    pub mounts: Vec<Mount>,
    #[serde(default)]
    pub approval_ask: Option<ApprovalAsk>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct List {
    #[serde(default)]
    pub host: String,
    #[serde(default)]
    pub host_error: String,
    #[serde(default)]
    pub extensions: Vec<Extension>,
}

impl List {
    pub fn host_problem(&self, host_name: &str) -> Option<String> {
        match self.host.as_str() {
            "running" => None,
            "off" => Some(format!("Extensions are off on {host_name}.")),
            _ if !self.host_error.is_empty() => Some(self.host_error.clone()),
            other => Some(format!("The extension host on {host_name} is {other}.")),
        }
    }
}

#[derive(Deserialize, Clone, Debug, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct WebSession {
    pub name: String,
    pub value: String,
    #[serde(default)]
    pub path: String,
    #[serde(default)]
    pub expires_at: String,
    #[serde(default)]
    pub scopes: Vec<String>,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum Kind {
    Page,
    Mount(String),
}

#[derive(Clone, Debug, PartialEq)]
pub struct Entry {
    pub host_id: String,
    pub host_name: String,
    pub extension_id: String,
    pub extension_label: String,
    pub kind: Kind,
    pub label: String,
    pub detail: Option<String>,
    pub icon: String,
    pub order: i64,
    pub offered: bool,
    pub badge: Option<String>,
    pub url: Option<String>,
    pub unavailable: Option<String>,
    pub awaits_approval: bool,
    pub approval_session: Option<String>,
}

impl Entry {
    pub fn id(&self) -> String {
        entry_id(&self.host_id, &self.extension_id, &self.kind)
    }

    pub fn subtitle(&self) -> String {
        if let Some(unavailable) = &self.unavailable {
            return unavailable.clone();
        }
        match self.kind {
            Kind::Mount(_) => format!("Web app, part of {}", self.extension_label),
            Kind::Page => self.detail.clone().unwrap_or_else(|| self.extension_label.clone()),
        }
    }

    pub fn closed_reason(&self) -> String {
        if self.awaits_approval && self.approval_session.is_none() {
            return format!("It needs your OK to open, and nothing has asked for it yet. On {}, approve it with agenthail extensions approve {}.", self.host_name, self.extension_id);
        }
        self.unavailable.clone().unwrap_or_else(|| "This page can't open right now.".into())
    }
}

pub fn entry_id(host: &str, extension: &str, kind: &Kind) -> String {
    match kind {
        Kind::Page => format!("{host}/{extension}/page"),
        Kind::Mount(name) => format!("{host}/{extension}/mount/{name}"),
    }
}

fn shown(text: &str) -> Option<String> {
    let text = text.trim();
    (!text.is_empty()).then(|| text.to_string())
}

fn label(offer: Option<&Offer>, fallback: &str) -> String {
    offer.and_then(|o| shown(&o.label)).unwrap_or_else(|| fallback.to_string())
}

fn distinct(detail: &str, from: &str) -> Option<String> {
    shown(detail).filter(|d| !d.eq_ignore_ascii_case(from))
}

pub fn awaits_approval(mount: &Mount) -> bool {
    mount.state == "pending" && mount.error.is_empty()
}

pub fn target(port: u16, entry: &str, published: bool, error: &str, origin: &str, host_name: &str) -> (Option<String>, Option<String>) {
    if !error.is_empty() {
        return (None, Some(error.to_string()));
    }
    if port == 0 {
        return (None, Some(format!("{host_name} isn't serving this yet.")));
    }
    let Ok(base) = url::Url::parse(origin) else { return (None, Some("This address is not valid.".into())) };
    if base.scheme() == "https" && !published {
        return (None, Some(format!("{host_name} doesn't publish this on the tailnet.")));
    }
    let Some(host) = base.host_str() else { return (None, Some("This address is not valid.".into())) };
    let path = if entry.starts_with('/') { entry.to_string() } else { format!("/{entry}") };
    match url::Url::parse(&format!("{}://{host}:{port}{path}", base.scheme())) {
        Ok(url) => (Some(url.to_string()), None),
        Err(_) => (None, Some("This address is not valid.".into())),
    }
}

fn mount_target(mount: &Mount, origin: &str, host_name: &str) -> (Option<String>, Option<String>) {
    if awaits_approval(mount) {
        return (None, Some("Needs your OK to open.".into()));
    }
    if mount.state != "serving" && mount.error.is_empty() {
        return (None, Some(format!("This mount is {}.", mount.state)));
    }
    target(mount.port, &mount.entry, mount.published, &mount.error, origin, host_name)
}

fn icon(offer: Option<&Offer>) -> String {
    offer.and_then(|o| shown(&o.icon)).unwrap_or_default()
}

pub fn entries(list: &List, host_id: &str, host_name: &str, origin: &str) -> Vec<Entry> {
    let mut out = Vec::new();
    for ext in &list.extensions {
        if let Some(page) = &ext.page {
            let (url, unavailable) = if ext.state == "loaded" {
                let error = if page.error.is_empty() { &ext.error } else { &page.error };
                target(page.port, &page.entry, page.published, error, origin, host_name)
            } else {
                (None, Some(shown(&ext.error).unwrap_or_else(|| format!("This extension is {}.", ext.state))))
            };
            let name = label(ext.app.as_ref(), &ext.label);
            out.push(Entry {
                host_id: host_id.into(),
                host_name: host_name.into(),
                extension_id: ext.id.clone(),
                extension_label: ext.label.clone(),
                kind: Kind::Page,
                detail: distinct(&ext.description, &name),
                label: name,
                icon: icon(ext.app.as_ref()),
                order: ext.app.as_ref().map_or(0, |a| a.order),
                offered: ext.app.is_some(),
                badge: shown(&ext.badge),
                url,
                unavailable,
                awaits_approval: false,
                approval_session: None,
            });
        }
        for mount in &ext.mounts {
            let (url, unavailable) = mount_target(mount, origin, host_name);
            let name = label(mount.app.as_ref(), &mount.label);
            let waits = awaits_approval(mount);
            out.push(Entry {
                host_id: host_id.into(),
                host_name: host_name.into(),
                extension_id: ext.id.clone(),
                extension_label: ext.label.clone(),
                kind: Kind::Mount(mount.name.clone()),
                detail: distinct(&ext.label, &name),
                label: name,
                icon: icon(mount.app.as_ref()),
                order: mount.app.as_ref().map_or(0, |a| a.order),
                offered: mount.app.is_some(),
                badge: shown(&mount.badge),
                url,
                unavailable,
                awaits_approval: waits,
                approval_session: if waits { asking_session(ext) } else { None },
            });
        }
    }
    out.sort_by_key(|e| (e.extension_label.to_lowercase(), e.extension_id.clone(), e.kind != Kind::Page, e.label.to_lowercase()));
    out
}

fn asking_session(ext: &Extension) -> Option<String> {
    ext.approval_ask.as_ref().and_then(|a| shown(&a.session_id))
}

pub fn waiting(ext: &Extension) -> Vec<String> {
    let mut out = ext.pending_capabilities.clone();
    out.extend(ext.mounts.iter().filter(|m| m.state == "pending").map(|m| format!("{} ({})", m.label, m.upstream)));
    out
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Approval {
    Nothing,
    Ask(String),
    Direct,
}

pub fn approval(ext: &Extension) -> Approval {
    match asking_session(ext) {
        Some(session) => Approval::Ask(session),
        None if waiting(ext).is_empty() => Approval::Nothing,
        None => Approval::Direct,
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Problem {
    pub text: String,
    pub failed: bool,
}

pub fn problems(ext: &Extension) -> Vec<Problem> {
    let failed = |text: String| Problem { text, failed: true };
    let mut out = Vec::new();
    if !ext.error.is_empty() {
        out.push(failed(ext.error.clone()));
    }
    if let Some(page) = ext.page.as_ref().filter(|p| !p.error.is_empty()) {
        out.push(failed(format!("Page: {}", page.error)));
    }
    let waits = waiting(ext);
    if !waits.is_empty() {
        out.push(Problem { text: format!("Waits for your approval: {}", waits.join(", ")), failed: false });
    }
    for mount in &ext.mounts {
        if !mount.error.is_empty() {
            out.push(failed(format!("{}: {}", mount.label, mount.error)));
        } else if mount.signin && mount.key.is_empty() && mount.state == "serving" {
            out.push(Problem { text: format!("{} signs in once you name its key: agenthail extensions key {} {} --key-file <path>", mount.label, ext.id, mount.name), failed: false });
        }
    }
    if !ext.tools_note.is_empty() {
        out.push(Problem { text: ext.tools_note.clone(), failed: false });
    }
    for (name, job) in &ext.jobs {
        if !job.last_error.is_empty() {
            out.push(failed(format!("Job {name} failed: {}", job.last_error)));
        }
    }
    if !ext.last_event_error.is_empty() {
        out.push(failed(format!("Event handler failed: {}", ext.last_event_error)));
    }
    out
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Tone {
    Good,
    Waiting,
    Failed,
    Quiet,
}

pub fn status(ext: &Extension) -> (String, Tone) {
    let failed = !ext.error.is_empty() || problems(ext).iter().any(|p| p.failed);
    let tone = match ext.state.as_str() {
        "loaded" if failed => Tone::Failed,
        "loaded" => Tone::Good,
        "failed" | "invalid" | "error" => Tone::Failed,
        _ if !waiting(ext).is_empty() => Tone::Waiting,
        _ => Tone::Quiet,
    };
    let label = if ext.state.is_empty() { "unknown".into() } else { ext.state.clone() };
    (label, tone)
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Navigation {
    Allow,
    Elsewhere,
    Refuse,
}

fn origin_of(url: &url::Url) -> (String, String, u16) {
    (url.scheme().to_lowercase(), url.host_str().unwrap_or_default().to_lowercase(), url.port_or_known_default().unwrap_or(0))
}

// The web session cookie is scoped to a host, not a port, so no other port on the page's host loads in its view.
pub fn navigation(target: &str, page: &str) -> Navigation {
    let Ok(to) = url::Url::parse(target) else { return Navigation::Refuse };
    match to.scheme() {
        "about" | "data" | "blob" => return Navigation::Allow,
        "http" | "https" => {}
        _ => return Navigation::Refuse,
    }
    let Ok(from) = url::Url::parse(page) else { return Navigation::Refuse };
    let (a, b) = (origin_of(&to), origin_of(&from));
    if a == b {
        Navigation::Allow
    } else if a.1 == b.1 {
        Navigation::Refuse
    } else {
        Navigation::Elsewhere
    }
}

pub fn same_origin(target: &str, page: &str) -> bool {
    matches!((url::Url::parse(target), url::Url::parse(page)), (Ok(a), Ok(b)) if matches!(a.scheme(), "http" | "https") && origin_of(&a) == origin_of(&b))
}

pub fn page_host(page: &str) -> Option<String> {
    url::Url::parse(page).ok()?.host_str().map(|h| h.trim_start_matches('[').trim_end_matches(']').to_string())
}

pub fn session_known(page: &str, session: &WebSession) -> std::result::Result<bool, String> {
    let mut url = url::Url::parse(page).map_err(|e| e.to_string())?;
    url.set_path("/_agenthail/signin.js");
    url.set_query(None);
    let agent = ureq::AgentBuilder::new().timeout_connect(std::time::Duration::from_secs(5)).timeout(std::time::Duration::from_secs(10)).redirects(0).build();
    match agent.get(url.as_str()).set("Cookie", &format!("{}={}", session.name, session.value)).call() {
        Ok(_) | Err(ureq::Error::Status(404, _)) => Ok(true),
        Err(ureq::Error::Status(401, _)) => Ok(false),
        Err(ureq::Error::Status(code, _)) => Err(format!("the page's port answered {code}")),
        Err(ureq::Error::Transport(error)) => Err(error.to_string()),
    }
}

pub fn age(secs: u64, refreshing: bool) -> String {
    let read = match secs {
        0..=4 => "just now".to_string(),
        5..=59 => format!("{secs}s ago"),
        60..=3599 => format!("{}m ago", secs / 60),
        _ => format!("{}h ago", secs / 3600),
    };
    if refreshing { format!("Refreshing… last read {read}") } else { format!("Updated {read}") }
}

pub fn holds_settings(client: &Client) -> bool {
    crate::dashboard_token().is_some_and(|token| token == client.token)
}

impl Client {
    pub fn origin(&self) -> &str {
        &self.base
    }

    pub fn extensions(&self, host: Option<&str>) -> Result<List> {
        let path = match host {
            Some(host) => format!("/api/v1/extensions?host={}", enc(host)),
            None => "/api/v1/extensions".into(),
        };
        Ok(serde_json::from_value(self.get_json(&path)?)?)
    }

    pub fn web_session(&self) -> std::result::Result<WebSession, RespondError> {
        let reply = self.post_json("/api/v1/web-session", json!({}))?;
        serde_json::from_value(reply).map_err(|e| RespondError { message: format!("the web session reply couldn't be read: {e}"), code: "unreadable_reply".into(), maybe_delivered: false, answered: true, reply: Value::Null, status: 0 })
    }

    pub fn approve_extension(&self, id: &str) -> std::result::Result<List, RespondError> {
        self.extensions_post(json!({"action": "approve", "id": id}))
    }

    pub fn reload_extensions(&self) -> std::result::Result<List, RespondError> {
        self.extensions_post(json!({"action": "reload"}))
    }

    fn extensions_post(&self, body: Value) -> std::result::Result<List, RespondError> {
        let reply = self.post_json("/api/v1/extensions", body)?;
        serde_json::from_value(reply).map_err(|e| RespondError { message: format!("the extensions reply couldn't be read: {e}"), code: "unreadable_reply".into(), maybe_delivered: true, answered: true, reply: Value::Null, status: 0 })
    }
}

#[cfg(test)]
#[path = "extensions_tests.rs"]
mod tests;
