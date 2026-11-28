use serde::Deserialize;
use serde_json::{Value, json};

use crate::catalog::{Client, OUTCOME_UNKNOWN, RespondError, Session, unknown_outcome};

pub const TITLE: &str = "Codex queue";
pub const EMPTY: &str = "Nothing queued in Codex.";
pub const UNAVAILABLE: &str = "Codex's queue isn't available for this session.";
pub const EMPTY_INPUT: &str = "Empty input";
pub const LOADING: &str = "Loading Codex's queue";
pub const EDITOR_NOTE: &str = "Codex starts queued input after the current turn, in order.";
const STILL_FINISHING: &str = "Agenthail is still finishing this request.";

pub fn shown(x: &Session) -> bool {
    x.surface == "codex"
}

pub fn writable(x: &Session) -> bool {
    shown(x) && !x.read_only
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Input {
    #[serde(default, rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub text: Option<String>,
    #[serde(default)]
    pub name: Option<String>,
}

impl Input {
    pub fn summary(&self) -> String {
        match self.kind.as_str() {
            "text" => self.text.clone().unwrap_or_default(),
            "image" | "localImage" => "Image".into(),
            "audio" | "localAudio" => "Audio".into(),
            _ => self.name.clone().unwrap_or_else(|| self.kind.clone()),
        }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Submission {
    pub id: String,
    #[serde(default)]
    pub client_user_message_id: Option<String>,
    #[serde(default)]
    pub input: Vec<Input>,
}

impl Submission {
    pub fn text(&self) -> String {
        self.input.iter().map(Input::summary).filter(|s| !s.is_empty()).collect::<Vec<_>>().join("\n")
    }

    pub fn shown_text(&self) -> String {
        let text = self.text();
        if text.is_empty() { EMPTY_INPUT.into() } else { text }
    }

    pub fn editable_text(&self) -> Option<String> {
        self.input.iter().all(|i| i.kind == "text").then(|| self.input.iter().filter_map(|i| i.text.clone()).collect::<Vec<_>>().join("\n"))
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Page {
    #[serde(default)]
    pub data: Vec<Submission>,
    #[serde(default)]
    pub next_cursor: Option<String>,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Act {
    Add(String),
    Update { id: String, text: String },
    Delete(String),
    Reorder(Vec<String>),
    Start(Option<String>),
}

impl Act {
    fn command(&self, key: Option<&str>) -> Value {
        match self {
            Act::Add(text) => {
                let mut c = json!({"queueAction": "add", "message": text});
                if let Some(key) = key {
                    c["clientUserMessageId"] = json!(key);
                }
                c
            }
            Act::Update { id, text } => json!({"queueAction": "update", "message": text, "queuedSubmissionId": id}),
            Act::Delete(id) => json!({"queueAction": "delete", "queuedSubmissionId": id}),
            Act::Reorder(ids) => json!({"queueAction": "reorder", "queuedSubmissionIds": ids}),
            Act::Start(Some(id)) => json!({"queueAction": "start", "queuedSubmissionId": id}),
            Act::Start(None) => json!({"queueAction": "start"}),
        }
    }

    pub fn identity(&self, session: &str) -> String {
        json!({"sessionId": session, "nativeQueue": self.command(None)}).to_string()
    }
}

pub fn moving(ids: &[String], id: &str, by: isize) -> Option<Vec<String>> {
    let index = ids.iter().position(|x| x == id)?;
    let target = index.checked_add_signed(by)?;
    if target >= ids.len() || target == index {
        return None;
    }
    let mut next = ids.to_vec();
    next.swap(index, target);
    Some(next)
}

#[derive(Clone, Debug, PartialEq)]
pub enum Outcome {
    Done,
    Unknown(String),
    Pending(String),
    Failed(String),
}

impl Outcome {
    pub fn of(result: Result<(), RespondError>) -> Outcome {
        let Err(error) = result else { return Outcome::Done };
        match error.code.as_str() {
            "session_operation_unknown" | "host_outcome_unknown" | OUTCOME_UNKNOWN => Outcome::Unknown(unknown_outcome(&error.message)),
            "submitted" => Outcome::Pending(unknown_outcome(STILL_FINISHING)),
            _ if error.status == 0 && error.maybe_delivered => Outcome::Pending(unknown_outcome(&error.message)),
            _ if error.status == 504 && error.maybe_delivered => Outcome::Pending(unknown_outcome(&error.message)),
            _ => Outcome::Failed(error.message),
        }
    }

    pub fn error(&self) -> Option<&str> {
        match self {
            Outcome::Done => None,
            Outcome::Unknown(e) | Outcome::Pending(e) | Outcome::Failed(e) => Some(e),
        }
    }
}

struct Pending {
    identity: String,
    session: String,
    key: String,
    sent: u32,
    unresolved: bool,
    recorded: Option<u64>,
}

const KEPT_KEYS: usize = 64;

#[derive(Default)]
pub struct Keys {
    pending: Vec<Pending>,
    seq: u64,
}

impl Keys {
    pub fn key(&mut self, session: &str, act: &Act) -> String {
        let identity = act.identity(session);
        if let Some(p) = self.pending.iter_mut().find(|p| p.identity == identity) {
            p.sent += 1;
            return p.key.clone();
        }
        let key = crate::catalog::request_key();
        self.pending.push(Pending { identity, session: session.to_string(), key: key.clone(), sent: 1, unresolved: false, recorded: None });
        key
    }

    pub fn settled(&mut self, key: &str, outcome: &Outcome) {
        self.seq += 1;
        let at = self.seq;
        let Some(p) = self.pending.iter_mut().find(|p| p.key == key) else { return };
        p.sent = p.sent.saturating_sub(1);
        match outcome {
            Outcome::Done => {
                p.unresolved = false;
                p.recorded = None;
            }
            Outcome::Failed(_) => {}
            Outcome::Unknown(_) => {
                p.unresolved = true;
                p.recorded = Some(at);
            }
            Outcome::Pending(_) => {
                p.unresolved = true;
                p.recorded = None;
            }
        }
        self.pending.retain(|p| p.sent > 0 || p.unresolved);
        while self.pending.len() > KEPT_KEYS {
            let Some(oldest) = self.pending.iter().position(|p| p.sent == 0) else { break };
            self.pending.remove(oldest);
        }
    }

    pub fn abandon(&mut self, session: &str, act: &Act) {
        let identity = act.identity(session);
        self.pending.retain(|p| p.identity != identity);
    }

    pub fn read_started(&mut self) -> u64 {
        self.seq += 1;
        self.seq
    }

    #[cfg(test)]
    pub fn len(&self) -> usize {
        self.pending.len()
    }

    pub fn observed(&mut self, session: &str, read: u64) {
        self.pending.retain(|p| p.session != session || p.sent > 0 || p.recorded.is_none_or(|at| at > read));
    }
}

#[derive(Default)]
pub struct Queue {
    pub session: Option<String>,
    pub items: Vec<Submission>,
    pub next_cursor: Option<String>,
    pub loaded: bool,
    pub busy: bool,
    pub error: Option<String>,
    generation: u64,
    dirty: bool,
}

impl Queue {
    pub fn of(session: &str, generation: u64) -> Queue {
        Queue { session: Some(session.to_string()), generation, ..Queue::default() }
    }

    pub fn reset(&mut self, session: &str) {
        if self.session.as_deref() == Some(session) {
            return;
        }
        let generation = self.generation.wrapping_add(1);
        *self = Queue { session: Some(session.to_string()), generation, ..Queue::default() };
    }

    pub fn can_reorder(&self) -> bool {
        self.loaded && !self.busy && self.next_cursor.is_none() && self.items.len() > 1
    }

    pub fn begin(&mut self) -> Option<(String, u64)> {
        let session = self.session.clone().filter(|_| !self.busy)?;
        self.busy = true;
        Some((session, self.generation))
    }

    pub fn invalidate(&mut self, session: &str) -> bool {
        if self.session.as_deref() != Some(session) {
            return false;
        }
        if self.busy {
            self.dirty = true;
            return false;
        }
        true
    }

    pub fn take_dirty(&mut self) -> bool {
        std::mem::take(&mut self.dirty)
    }

    pub fn current(&self, session: &str, generation: u64) -> bool {
        self.generation == generation && self.session.as_deref() == Some(session)
    }

    pub fn listed(&mut self, session: &str, generation: u64, more: bool, result: Result<Page, String>) -> bool {
        if !self.current(session, generation) {
            return false;
        }
        self.busy = false;
        match result {
            Ok(page) => {
                if more {
                    let fresh: Vec<Submission> = page.data.into_iter().filter(|p| !self.items.iter().any(|i| i.id == p.id)).collect();
                    self.items.extend(fresh);
                } else {
                    self.items = page.data;
                }
                self.next_cursor = page.next_cursor.filter(|c| !c.is_empty());
                self.loaded = true;
                self.error = None;
            }
            Err(error) => self.error = Some(error),
        }
        true
    }

    pub fn mutated(&mut self, session: &str, generation: u64, outcome: &Outcome) -> bool {
        if !self.current(session, generation) {
            return false;
        }
        self.busy = false;
        self.error = outcome.error().map(str::to_string);
        if *outcome == Outcome::Done {
            self.dirty = false;
            return true;
        }
        false
    }

    pub fn moved(&self, id: &str, by: isize) -> Option<Act> {
        if !self.can_reorder() {
            return None;
        }
        let ids: Vec<String> = self.items.iter().map(|i| i.id.clone()).collect();
        moving(&ids, id, by).map(Act::Reorder)
    }
}

pub fn can_save(original: &str, typed: &str) -> bool {
    let typed = typed.trim();
    !typed.is_empty() && typed != original.trim()
}

impl Client {
    pub fn native_queue_list(&self, session: &str, cursor: Option<&str>) -> Result<Page, RespondError> {
        let mut command = json!({"queueAction": "list"});
        if let Some(cursor) = cursor {
            command["cursor"] = json!(cursor);
        }
        let reply = self.action("native-queue", json!({"sessionId": session, "nativeQueue": command}))?;
        crate::reply::member(&reply, "result").map_err(|e| RespondError { message: format!("Codex's queue couldn't be read: {e}"), code: "unreadable_reply".into(), maybe_delivered: false, answered: true, reply, status: 0 })
    }

    pub fn native_queue(&self, session: &str, act: &Act, key: &str) -> Result<(), RespondError> {
        let payload = json!({"sessionId": session, "nativeQueue": act.command(Some(key))});
        crate::catalog::confirmed(self.action_keyed("native-queue", payload, key)?).map(drop)
    }
}

#[cfg(test)]
#[path = "native_queue_tests.rs"]
mod tests;
