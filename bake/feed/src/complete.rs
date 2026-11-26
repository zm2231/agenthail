use std::ops::Range;

use serde::Deserialize;
use serde_json::{Value, json};
use time::OffsetDateTime;

use crate::catalog::{Capabilities, Client, RespondError, Session, enc};

pub const MENTIONED_SESSIONS: usize = 8;
pub const FILES_ASKED: usize = 50;
pub const PROMPTS_ASKED: usize = 100;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Trigger {
    Mention,
    Command,
    Skill,
}

impl Trigger {
    fn of(c: char) -> Option<Trigger> {
        match c {
            '@' => Some(Trigger::Mention),
            '/' => Some(Trigger::Command),
            '$' => Some(Trigger::Skill),
            _ => None,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Token {
    pub trigger: Trigger,
    pub query: String,
    pub range: Range<usize>,
    pub at_start: bool,
}

impl Token {
    pub fn at(text: &str, caret: usize) -> Option<Token> {
        if caret > text.len() || !text.is_char_boundary(caret) {
            return None;
        }
        let start = text[..caret].char_indices().rev().take_while(|(_, c)| !c.is_whitespace()).last().map(|(i, _)| i)?;
        let trigger = Trigger::of(text[start..].chars().next()?)?;
        let query = text[start + 1..caret].to_string();
        Some(Token { trigger, query, range: start..caret, at_start: text[..start].trim().is_empty() })
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Item {
    pub id: String,
    pub title: String,
    pub detail: String,
    pub insertion: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RuntimeCommand {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub argument_hint: String,
    #[serde(default)]
    pub insert: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct RuntimeCommands {
    #[serde(default)]
    pub commands: Vec<RuntimeCommand>,
    #[serde(default)]
    pub unavailable: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct SessionFile {
    #[serde(default)]
    pub path: String,
    #[serde(default)]
    pub dir: bool,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct SessionFiles {
    #[serde(default)]
    pub files: Vec<SessionFile>,
    #[serde(default)]
    pub truncated: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Slash {
    Name,
    Compact,
    Stop,
    Steer,
    Model,
    Goal,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Can {
    pub stop: bool,
    pub steer: bool,
}

impl Slash {
    pub const ALL: [Slash; 6] = [Slash::Name, Slash::Compact, Slash::Stop, Slash::Steer, Slash::Model, Slash::Goal];

    pub fn word(self) -> &'static str {
        match self {
            Slash::Name => "name",
            Slash::Compact => "compact",
            Slash::Stop => "stop",
            Slash::Steer => "steer",
            Slash::Model => "model",
            Slash::Goal => "goal",
        }
    }

    pub fn token(self) -> String {
        format!("/{}", self.word())
    }

    pub fn needs_argument(self) -> bool {
        matches!(self, Slash::Name | Slash::Steer | Slash::Goal)
    }

    pub fn takes_argument(self) -> bool {
        self.needs_argument() || self == Slash::Model
    }

    pub fn summary(self) -> &'static str {
        match self {
            Slash::Name => "Name this conversation",
            Slash::Compact => "Compact this conversation",
            Slash::Stop => "Stop the current turn",
            Slash::Steer => "Redirect the current turn",
            Slash::Model => "Choose the active model",
            Slash::Goal => "Set the session goal",
        }
    }

    pub fn available(caps: &Capabilities, can: Can) -> Vec<Slash> {
        Slash::ALL
            .into_iter()
            .filter(|c| match c {
                Slash::Name => true,
                Slash::Compact => caps.compact,
                Slash::Stop => can.stop,
                Slash::Steer => can.steer,
                Slash::Model => caps.model,
                Slash::Goal => caps.goal_set,
            })
            .collect()
    }

    fn parse(word: &str) -> Option<Slash> {
        Slash::ALL.into_iter().find(|c| c.word().eq_ignore_ascii_case(word))
    }

    pub fn item(self) -> Item {
        Item { id: self.token(), title: self.token(), detail: self.summary().into(), insertion: if self.takes_argument() { format!("{} ", self.token()) } else { self.token() } }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Input {
    Message,
    Command(Slash, String),
    MissingArgument(Slash),
    Unavailable(Slash),
}

impl Input {
    pub fn parse(text: &str, available: &[Slash]) -> Input {
        let trimmed = text.trim();
        let Some(rest) = trimmed.strip_prefix('/') else { return Input::Message };
        let head = rest.split(char::is_whitespace).next().unwrap_or_default();
        let Some(command) = Slash::parse(head) else { return Input::Message };
        let argument = rest[head.len()..].trim().to_string();
        if !available.contains(&command) {
            Input::Unavailable(command)
        } else if command.needs_argument() && argument.is_empty() {
            Input::MissingArgument(command)
        } else {
            Input::Command(command, argument)
        }
    }

    pub fn problem(&self) -> Option<String> {
        match self {
            Input::MissingArgument(c) => Some(format!("{} needs a value.", c.token())),
            Input::Unavailable(c) => Some(format!("{} isn't available for this session right now.", c.token())),
            Input::Message | Input::Command(..) => None,
        }
    }
}

pub fn model_query(text: &str) -> Option<&str> {
    let value = text.trim_start();
    let head = value.get(..7)?;
    head.eq_ignore_ascii_case("/model ").then(|| value[7..].trim())
}

pub fn model_items(query: &str, models: &[(String, String)], current: &str) -> Vec<Item> {
    if models.iter().any(|(id, _)| id == query) {
        return Vec::new();
    }
    let q = query.to_lowercase();
    models
        .iter()
        .filter(|(id, label)| q.is_empty() || id.to_lowercase().contains(&q) || label.to_lowercase().contains(&q))
        .map(|(id, label)| Item { id: format!("model:{id}"), title: label.clone(), detail: if id == current { "Current".into() } else { id.clone() }, insertion: format!("/model {id}") })
        .collect()
}

pub fn resolve_model(query: &str, models: &[(String, String)]) -> String {
    models.iter().find(|(id, label)| id.eq_ignore_ascii_case(query) || label.eq_ignore_ascii_case(query)).map(|(id, _)| id.clone()).unwrap_or_else(|| query.to_string())
}

pub fn items(token: &Token, app: &[Item], runtime: Option<&RuntimeCommands>, sessions: &[Item], files: &[SessionFile]) -> Vec<Item> {
    match token.trigger {
        Trigger::Command => {
            let mut out = if token.at_start { matching(app, &token.query, |i| &i.title[1..]) } else { Vec::new() };
            out.extend(matching(&runtime_items(runtime, '/'), &token.query, |i| &i.title));
            out
        }
        Trigger::Skill => matching(&runtime_items(runtime, '$'), &token.query, |i| &i.title),
        Trigger::Mention => {
            let mut out: Vec<Item> = matching(sessions, &token.query, |i| &i.title[1..]).into_iter().take(MENTIONED_SESSIONS).collect();
            out.extend(files.iter().map(|f| {
                let shown = if f.dir { format!("{}/", f.path) } else { f.path.clone() };
                Item { id: format!("file:{}", f.path), title: shown.clone(), detail: if f.dir { "Folder".into() } else { String::new() }, insertion: format!("@{shown}") }
            }));
            out
        }
    }
}

pub fn notice(token: &Token, runtime: Option<&RuntimeCommands>) -> Option<String> {
    let reason = &runtime?.unavailable;
    (token.trigger == Trigger::Command && !reason.is_empty()).then(|| reason.clone())
}

fn runtime_items(runtime: Option<&RuntimeCommands>, prefix: char) -> Vec<Item> {
    runtime
        .map(|r| r.commands.as_slice())
        .unwrap_or_default()
        .iter()
        .filter(|c| c.insert.starts_with(prefix))
        .map(|c| Item {
            id: format!("runtime:{}", c.insert),
            title: if c.argument_hint.is_empty() { c.insert.clone() } else { format!("{} {}", c.insert, c.argument_hint) },
            detail: c.description.clone(),
            insertion: c.insert.clone(),
        })
        .collect()
}

fn matching(items: &[Item], query: &str, key: impl Fn(&Item) -> &str) -> Vec<Item> {
    if query.is_empty() {
        return items.to_vec();
    }
    let q = query.to_lowercase();
    let (prefixed, rest): (Vec<&Item>, Vec<&Item>) = items.iter().partition(|i| key(i).to_lowercase().starts_with(&q));
    prefixed.into_iter().chain(rest.into_iter().filter(|i| key(i).to_lowercase().contains(&q))).cloned().collect()
}

pub fn edit(item: &Item, text: &str, token: &Token) -> (Range<usize>, String, usize) {
    let next = text[token.range.end..].chars().next();
    let spaced = next.is_some_and(char::is_whitespace);
    let insertion = item.insertion.as_str();
    let trailing = insertion.ends_with(' ');
    let open = trailing || insertion.ends_with('/');
    let replacement = if spaced && trailing {
        insertion[..insertion.len() - 1].to_string()
    } else if spaced || trailing || (open && next.is_none()) {
        insertion.to_string()
    } else {
        format!("{insertion} ")
    };
    let past = match next {
        _ if open => 0,
        Some(c) if c.is_whitespace() => c.len_utf8(),
        _ => 1,
    };
    let caret = token.range.start + insertion.len() + past;
    (token.range.clone(), replacement, caret)
}

pub fn apply(item: &Item, text: &str, token: &Token) -> (String, usize) {
    let (range, replacement, caret) = edit(item, text, token);
    let mut out = text.to_string();
    out.replace_range(range, &replacement);
    (out, caret)
}

pub fn completes(item: &Item, text: &str, token: &Token) -> bool {
    let tail = &text[token.range.end..];
    let end = token.range.end + tail.find(char::is_whitespace).unwrap_or(tail.len());
    text[token.range.start..end] == *item.insertion.trim()
}

pub fn sessions(all: &[Session], current: &Session) -> Vec<Item> {
    let host = host_prefix(&current.id);
    all.iter()
        .filter(|x| x.id != current.id && host_prefix(&x.id) == host && !x.read_only && !x.archived)
        .filter_map(|x| {
            let alias = x.alias.as_deref().filter(|a| !a.is_empty());
            if alias.is_none() && !x.open {
                return None;
            }
            let handle = alias.map(str::to_string).unwrap_or_else(|| local_id(&x.id).to_string());
            let detail = [x.name.as_str(), x.surface.as_str()].into_iter().filter(|p| !p.is_empty() && *p != handle).collect::<Vec<_>>().join(" · ");
            Some(Item { id: format!("session:{}", x.id), title: format!("@{handle}"), detail, insertion: format!("@{handle}") })
        })
        .collect()
}

fn host_prefix(id: &str) -> &str {
    id.split_once("::").map(|(h, _)| h).unwrap_or_default()
}

fn local_id(id: &str) -> &str {
    id.split_once("::").map(|(_, l)| l).unwrap_or(id)
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Prompt {
    #[serde(default)]
    pub session_id: String,
    #[serde(default)]
    pub item_id: String,
    #[serde(default)]
    pub turn_id: Option<String>,
    #[serde(default)]
    pub ts: String,
    #[serde(default)]
    pub text: String,
}

pub fn sent_texts(local: &[(String, OffsetDateTime)], recorded: &[Prompt]) -> Vec<String> {
    let mut all: Vec<(OffsetDateTime, &str)> = local.iter().map(|(t, at)| (*at, t.as_str())).collect();
    all.extend(recorded.iter().map(|p| (crate::catalog::parse_time(&p.ts).unwrap_or(OffsetDateTime::UNIX_EPOCH), p.text.as_str())));
    all.sort_by(|a, b| b.0.cmp(&a.0));
    all.into_iter().map(|(_, t)| t.to_string()).collect()
}

pub fn distinct(prompts: Vec<Prompt>) -> Vec<Prompt> {
    let mut seen = std::collections::HashSet::new();
    prompts.into_iter().filter(|p| seen.insert(p.text.clone())).collect()
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct History {
    entries: Vec<String>,
    position: Option<usize>,
    draft: String,
    waiting: Option<Vec<String>>,
}

impl History {
    pub fn browsing(&self) -> bool {
        self.position.is_some()
    }

    pub fn replace(&mut self, texts: Vec<String>) {
        if self.browsing() {
            self.waiting = Some(texts);
            return;
        }
        let mut seen = std::collections::HashSet::new();
        self.entries = texts.into_iter().filter(|t| !t.trim().is_empty() && seen.insert(t.clone())).collect();
        if self.position.is_some_and(|p| p >= self.entries.len()) {
            self.position = self.entries.len().checked_sub(1);
        }
    }

    pub fn older(&mut self, current: &str) -> Option<String> {
        let next = self.position.map_or(0, |p| p + 1);
        let entry = self.entries.get(next)?.clone();
        if self.position.is_none() {
            self.draft = current.to_string();
        }
        self.position = Some(next);
        Some(entry)
    }

    pub fn newer(&mut self) -> Option<String> {
        let p = self.position?;
        if p == 0 {
            self.stop();
            return Some(std::mem::take(&mut self.draft));
        }
        self.position = Some(p - 1);
        self.entries.get(p - 1).cloned()
    }

    pub fn stop(&mut self) {
        self.position = None;
        if let Some(texts) = self.waiting.take() {
            self.replace(texts);
        }
    }
}

pub fn forkable(prompt: &Prompt) -> bool {
    prompt.turn_id.as_deref().is_some_and(|t| !t.is_empty())
}

pub fn can_fork(session: &Session) -> bool {
    session.surface == "codex" && !session.read_only
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Found {
    pub prompts: Vec<Prompt>,
    pub problems: Vec<String>,
}

fn connected_hosts(snapshot: &Value) -> Vec<(String, String)> {
    snapshot["daemon"]["hosts"]
        .as_array()
        .into_iter()
        .flatten()
        .filter(|h| h["state"] == "connected")
        .filter_map(|h| Some((h["id"].as_str()?.to_string(), h["name"].as_str().unwrap_or_default().to_string())))
        .collect()
}

fn on_host(mut prompt: Prompt, host: &str) -> Prompt {
    if !prompt.session_id.contains("::") {
        prompt.session_id = format!("{host}::{}", prompt.session_id);
    }
    prompt
}

fn reply_error(code: u16, body: &str) -> String {
    let value: Value = serde_json::from_str(body).unwrap_or_default();
    match value["error"]["message"].as_str() {
        Some(message) if !message.is_empty() => message.to_string(),
        _ if code == 0 => body.to_string(),
        _ => format!("status code {code}"),
    }
}

impl Client {
    fn read(&self, path: &str) -> Result<Value, String> {
        self.get_reply(path).map_err(|(code, body)| reply_error(code, &body))
    }

    pub fn session_commands(&self, session: &str) -> Result<RuntimeCommands, String> {
        serde_json::from_value(self.read(&format!("/api/v1/session-commands?id={}", enc(session)))?).map_err(|e| e.to_string())
    }

    pub fn session_files(&self, session: &str, query: &str) -> Result<SessionFiles, String> {
        serde_json::from_value(self.read(&format!("/api/v1/session-files?id={}&q={}&limit={FILES_ASKED}", enc(session), enc(query)))?).map_err(|e| e.to_string())
    }

    pub fn prompts(&self, session: Option<&str>, query: &str) -> Result<Vec<Prompt>, String> {
        let prompts = self.prompts_on(session, query, None)?;
        Ok(match session.and_then(|id| id.split_once("::")) {
            Some((host, _)) => prompts.into_iter().map(|p| on_host(p, host)).collect(),
            None => prompts,
        })
    }

    pub fn prompts_everywhere(&self, query: &str) -> Result<Found, String> {
        let mut prompts = self.prompts(None, query)?;
        let mut problems = Vec::new();
        let hosts = self.read("/api/v1/snapshot?sessions=none").map(|v| connected_hosts(&v)).unwrap_or_else(|error| {
            problems.push(format!("Couldn't find the other computers to search: {error}"));
            Vec::new()
        });
        let asked: Vec<(String, Result<Vec<Prompt>, String>)> = std::thread::scope(|scope| {
            let running: Vec<_> = hosts.iter().map(|(id, name)| (name.clone(), scope.spawn(move || self.prompts_on(None, query, Some(id))), id.clone())).collect();
            running
                .into_iter()
                .map(|(name, job, id)| {
                    let found = job.join().unwrap_or_else(|_| Err("the search stopped".into())).map(|list| list.into_iter().map(|p| on_host(p, &id)).collect());
                    (name, found)
                })
                .collect()
        });
        for (name, found) in asked {
            match found {
                Ok(list) => prompts.extend(list),
                Err(error) => problems.push(format!("Couldn't search {name}: {error}")),
            }
        }
        prompts.sort_by_key(|p| std::cmp::Reverse(crate::catalog::parse_time(&p.ts).unwrap_or(OffsetDateTime::UNIX_EPOCH)));
        Ok(Found { prompts, problems })
    }

    fn prompts_on(&self, session: Option<&str>, query: &str, host: Option<&str>) -> Result<Vec<Prompt>, String> {
        let mut path = format!("/api/v1/prompts?limit={PROMPTS_ASKED}");
        if let Some(id) = session {
            path.push_str(&format!("&sessionId={}", enc(id)));
        }
        if let Some(host) = host {
            path.push_str(&format!("&host={}", enc(host)));
        }
        if !query.trim().is_empty() {
            path.push_str(&format!("&q={}", enc(query.trim())));
        }
        let value = self.read(&path)?;
        serde_json::from_value(value["prompts"].clone()).map_err(|e| e.to_string())
    }

    pub fn fork(&self, session: &str, before: Option<&str>, key: &str) -> Result<String, RespondError> {
        let mut payload = json!({"sessionId": session});
        if let Some(turn) = before {
            payload["fork"] = json!({"beforeTurnId": turn});
        }
        let reply = self.action_keyed("session-fork", payload, key)?;
        let id = reply["result"]["session"]["id"].as_str().unwrap_or_default();
        Ok(match session.split_once("::") {
            Some((host, _)) if !id.is_empty() && !id.contains("::") => format!("{host}::{id}"),
            _ => id.to_string(),
        })
    }

    pub fn goal_set(&self, session: &str, goal: &str) -> Result<(), RespondError> {
        self.action("goal-set", json!({"sessionId": session, "message": goal})).map(drop)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};

    fn tok(text: &str) -> Option<Token> {
        Token::at(text, text.len())
    }

    fn item(insertion: &str) -> Item {
        Item { id: insertion.into(), title: insertion.into(), detail: String::new(), insertion: insertion.into() }
    }

    #[test]
    fn the_word_at_the_caret_names_what_to_complete() {
        assert_eq!(tok("/com"), Some(Token { trigger: Trigger::Command, query: "com".into(), range: 0..4, at_start: true }));
        assert_eq!(tok("  /"), Some(Token { trigger: Trigger::Command, query: "".into(), range: 2..3, at_start: true }));
        assert_eq!(tok("look at @src/ma").map(|t| (t.trigger, t.query, t.at_start)), Some((Trigger::Mention, "src/ma".into(), false)));
        assert_eq!(tok("run $sk").map(|t| t.trigger), Some(Trigger::Skill));
        assert_eq!(tok("plain words"), None);
        assert_eq!(tok("ends in space "), None);
        assert_eq!(tok(""), None);
        assert_eq!(Token::at("@é x", 3).map(|t| t.query), Some("é".into()));
        assert_eq!(Token::at("@é", 2), None, "inside a character");
        assert_eq!(tok("a/b"), None, "a slash mid-word is a path, not a command");
    }

    #[test]
    fn app_commands_follow_what_the_session_can_do() {
        let caps = Capabilities { compact: true, model: true, ..Capabilities::default() };
        let idle = Slash::available(&caps, Can::default());
        assert_eq!(idle, vec![Slash::Name, Slash::Compact, Slash::Model]);
        let busy = Slash::available(&Capabilities { goal_set: true, ..caps }, Can { stop: true, steer: true });
        assert_eq!(busy, Slash::ALL.to_vec());
        assert_eq!(Slash::Steer.item().insertion, "/steer ");
        assert_eq!(Slash::Model.item().insertion, "/model ");
        assert_eq!(Slash::Stop.item().insertion, "/stop");
    }

    #[test]
    fn a_slash_message_runs_a_command_or_says_why_not() {
        let all = Slash::ALL.to_vec();
        assert_eq!(Input::parse("hello", &all), Input::Message);
        assert_eq!(Input::parse("/unknown thing", &all), Input::Message);
        assert_eq!(Input::parse("  /Compact  ", &all), Input::Command(Slash::Compact, String::new()));
        assert_eq!(Input::parse("/name  scout ", &all), Input::Command(Slash::Name, "scout".into()));
        assert_eq!(Input::parse("/steer focus on\nthe tests", &all), Input::Command(Slash::Steer, "focus on\nthe tests".into()));
        assert_eq!(Input::parse("/goal", &all), Input::MissingArgument(Slash::Goal));
        assert_eq!(Input::parse("/model", &all), Input::Command(Slash::Model, String::new()));
        assert_eq!(Input::parse("/stop", &[Slash::Name]), Input::Unavailable(Slash::Stop));
        assert_eq!(Input::parse("/goal", &all).problem().as_deref(), Some("/goal needs a value."));
        assert_eq!(Input::parse("/stop", &[]).problem().as_deref(), Some("/stop isn't available for this session right now."));
    }

    #[test]
    fn model_choices_narrow_as_the_person_types() {
        let models = vec![("gpt-6.1-sol".to_string(), "GPT-6.1 Sol".to_string()), ("gpt-6.1-terra".to_string(), "GPT-6.1 Terra".to_string())];
        assert_eq!(model_query("/model "), Some(""));
        assert_eq!(model_query("  /MODEL terra"), Some("terra"));
        assert_eq!(model_query("/model"), None);
        assert_eq!(model_query("/models"), None);
        let all = model_items("", &models, "gpt-6.1-sol");
        assert_eq!(all.iter().map(|i| i.detail.as_str()).collect::<Vec<_>>(), vec!["Current", "gpt-6.1-terra"]);
        assert_eq!(model_items("terra", &models, "").iter().map(|i| i.insertion.as_str()).collect::<Vec<_>>(), vec!["/model gpt-6.1-terra"]);
        assert!(model_items("gpt-6.1-sol", &models, "").is_empty());
        assert_eq!(resolve_model("gpt-6.1 terra", &models), "gpt-6.1-terra");
        assert_eq!(resolve_model("opus", &models), "opus");
    }

    #[test]
    fn completions_offer_app_and_runtime_commands_sessions_and_files() {
        let app = vec![Slash::Compact.item(), Slash::Name.item()];
        let runtime = RuntimeCommands {
            commands: vec![
                RuntimeCommand { name: "review".into(), description: "Review the diff".into(), argument_hint: "[target]".into(), insert: "/review".into() },
                RuntimeCommand { name: "lint".into(), description: "".into(), argument_hint: "".into(), insert: "$lint".into() },
            ],
            unavailable: String::new(),
        };
        let at = |text: &str| items(&tok(text).unwrap(), &app, Some(&runtime), &[], &[]);
        assert_eq!(at("/").iter().map(|i| i.id.as_str()).collect::<Vec<_>>(), vec!["/compact", "/name", "runtime:/review"]);
        assert_eq!(at("/re")[0].title, "/review [target]");
        assert_eq!(at("/am").iter().map(|i| i.id.as_str()).collect::<Vec<_>>(), vec!["/name"], "a match inside the word comes after prefix matches");
        assert_eq!(at("fix /").iter().map(|i| i.id.as_str()).collect::<Vec<_>>(), vec!["runtime:/review"], "app commands only start a message");
        assert_eq!(at("$").iter().map(|i| i.insertion.as_str()).collect::<Vec<_>>(), vec!["$lint"]);
        let sessions = vec![item("@scout"), item("@builder")];
        let files = vec![SessionFile { path: "src".into(), dir: true }, SessionFile { path: "src/main.rs".into(), dir: false }];
        let mentioned = items(&tok("@s").unwrap(), &app, None, &sessions, &files);
        assert_eq!(mentioned.iter().map(|i| i.insertion.as_str()).collect::<Vec<_>>(), vec!["@scout", "@src/", "@src/main.rs"]);
        assert_eq!(mentioned[1].detail, "Folder");
        let closed = RuntimeCommands { commands: vec![], unavailable: "This session runs in a terminal.".into() };
        assert_eq!(notice(&tok("/").unwrap(), Some(&closed)).as_deref(), Some("This session runs in a terminal."));
        assert_eq!(notice(&tok("@").unwrap(), Some(&closed)), None);
    }

    #[test]
    fn accepting_keeps_the_rest_of_the_word_and_moves_past_one_space() {
        let t = tok("/com").unwrap();
        assert_eq!(apply(&item("/compact"), "/com", &t), ("/compact ".into(), 9));
        assert_eq!(apply(&item("/steer "), "/st", &tok("/st").unwrap()), ("/steer ".into(), 7));
        let mid = Token::at("/st now", 3).unwrap();
        assert_eq!(apply(&item("/steer "), "/st now", &mid), ("/steer now".into(), 7));
        assert_eq!(apply(&item("@src/"), "see @sr", &tok("see @sr").unwrap()), ("see @src/".into(), 9));
        let inside = Token::at("@srmain", 3).unwrap();
        assert_eq!(apply(&item("@src/"), "@srmain", &inside), ("@src/ main".into(), 5));
        assert_eq!(apply(&item("@scout"), "@sc and", &Token::at("@sc and", 3).unwrap()), ("@scout and".into(), 7));
        assert_eq!(apply(&item("@scout"), "@sc\u{a0}and", &Token::at("@sc\u{a0}and", 3).unwrap()), ("@scout\u{a0}and".into(), 8), "past a wide space");
        assert!(completes(&item("/compact"), "/compact", &tok("/compact").unwrap()));
        assert!(completes(&item("/steer "), "/steer", &tok("/steer").unwrap()));
        assert!(!completes(&item("/compact"), "/com", &t));
    }

    fn session(id: &str, alias: Option<&str>, open: bool) -> Session {
        Session { id: id.into(), alias: alias.map(String::from), open, name: "Fix it".into(), surface: "codex".into(), ..Session::default() }
    }

    #[test]
    fn a_mention_names_sessions_on_the_same_computer() {
        let me = session("mac::me", Some("me"), true);
        let all = vec![
            me.clone(),
            session("mac::a1", Some("scout"), false),
            session("mac::b2", None, true),
            session("mac::c3", None, false),
            session("vps::d4", Some("far"), true),
            Session { read_only: true, ..session("mac::e5", Some("ro"), true) },
        ];
        let found = sessions(&all, &me);
        assert_eq!(found.iter().map(|i| i.title.as_str()).collect::<Vec<_>>(), vec!["@scout", "@b2"]);
        assert_eq!(found[0].detail, "Fix it · codex");
    }

    fn prompt(text: &str, turn: Option<&str>) -> Prompt {
        Prompt { session_id: "s1".into(), item_id: text.into(), turn_id: turn.map(String::from), ts: String::new(), text: text.into() }
    }

    #[test]
    fn history_steps_back_and_returns_to_the_draft() {
        let mut h = History::default();
        assert_eq!(h.older("draft"), None);
        h.replace(sent_texts(&[("just sent".into(), OffsetDateTime::now_utc())], &[prompt("second", Some("t2")), prompt("first", None), prompt("second", None), prompt(" ", None)]));
        assert_eq!(h.older("half typed").as_deref(), Some("just sent"));
        assert!(h.browsing());
        assert_eq!(h.older("ignored").as_deref(), Some("second"));
        assert_eq!(h.older("ignored").as_deref(), Some("first"));
        assert_eq!(h.older("ignored"), None);
        assert_eq!(h.newer().as_deref(), Some("second"));
        assert_eq!(h.newer().as_deref(), Some("just sent"));
        assert_eq!(h.newer().as_deref(), Some("half typed"));
        assert!(!h.browsing());
        assert_eq!(h.newer(), None);
        h.older("x");
        h.older("x");
        h.replace(vec!["only".into()]);
        assert_eq!(h.newer().as_deref(), Some("just sent"), "a refresh waits while the person is browsing");
        assert_eq!(h.newer().as_deref(), Some("x"));
        assert_eq!(h.older("y").as_deref(), Some("only"), "and applies once they're back at their draft");
        h.older("y");
        h.stop();
        assert!(!h.browsing());
        let at = |ts: &str| crate::catalog::parse_time(ts).unwrap();
        let stamped = |text: &str, ts: &str| Prompt { ts: ts.into(), ..prompt(text, None) };
        let recorded = [stamped("b", "2026-11-26T10:02:00Z"), stamped("a", "2026-11-26T10:01:00Z")];
        let mut again = History::default();
        again.replace(sent_texts(&[("a".into(), at("2026-11-26T10:03:00Z"))], &recorded));
        assert_eq!(again.older("").as_deref(), Some("a"), "sending an older prompt again makes it the newest");
        assert_eq!(again.older("").as_deref(), Some("b"));
        assert_eq!(again.older(""), None);
        let mut later = History::default();
        later.replace(sent_texts(&[("a".into(), at("2026-11-26T10:00:00Z"))], &recorded));
        assert_eq!(later.older("").as_deref(), Some("b"), "a prompt recorded since comes first");
        assert_eq!(distinct(vec![prompt("a", None), prompt("a", Some("t")), prompt("b", None)]).len(), 2);
        assert!(forkable(&prompt("a", Some("t1"))) && !forkable(&prompt("a", Some(""))) && !forkable(&prompt("a", None)));
    }

    #[test]
    fn each_read_asks_its_endpoint_and_reads_the_reply() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"commands":[{"name":"review","description":"Review","argumentHint":"","insert":"/review"}],"unavailable":""}"#),
            (200, r#"{"files":[{"path":"src","dir":true}],"truncated":false}"#),
            (200, r#"{"prompts":[{"sessionId":"s 1","itemId":"i1","turnId":"t1","ts":"2026-11-26T10:00:00Z","text":"ship it"}],"nextBefore":""}"#),
            (200, r#"{"prompts":[],"nextBefore":""}"#),
            (403, r#"{"error":{"code":"forbidden","message":"This token can't read prompts."}}"#),
        ]));
        assert_eq!(client.session_commands("h::s 1").unwrap().commands[0].insert, "/review");
        assert_eq!(seen.recv().unwrap().0, "/api/v1/session-commands?id=h%3A%3As+1");
        assert!(client.session_files("h::s 1", "src/m").unwrap().files[0].dir);
        assert_eq!(seen.recv().unwrap().0, "/api/v1/session-files?id=h%3A%3As+1&q=src%2Fm&limit=50");
        let on_host = client.prompts(Some("h::s 1"), "").unwrap();
        assert_eq!((on_host[0].turn_id.as_deref(), on_host[0].session_id.as_str()), (Some("t1"), "h::s 1"));
        assert_eq!(seen.recv().unwrap().0, "/api/v1/prompts?limit=100&sessionId=h%3A%3As+1");
        assert!(client.prompts(None, " ship ").unwrap().is_empty());
        assert_eq!(seen.recv().unwrap().0, "/api/v1/prompts?limit=100&q=ship");
        assert_eq!(client.prompts(None, "").unwrap_err(), "This token can't read prompts.");
    }

    #[test]
    fn all_sessions_searches_each_connected_host_and_names_the_ones_that_failed() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"prompts":[{"sessionId":"s1","itemId":"i1","ts":"2026-11-26T10:00:00Z","text":"here"}]}"#),
            (200, r#"{"daemon":{"hosts":[{"id":"h1","name":"buildbox","state":"connected"},{"id":"h2","name":"laptop","state":"unreachable"}]}}"#),
            (200, r#"{"prompts":[{"sessionId":"s9","itemId":"i9","ts":"2026-11-26T11:00:00Z","text":"there"},{"sessionId":"h1::s8","itemId":"i8","ts":"2026-11-26T09:00:00Z","text":"older"}]}"#),
        ]));
        let found = client.prompts_everywhere("t").unwrap();
        assert_eq!(found.prompts.iter().map(|p| (p.session_id.as_str(), p.text.as_str())).collect::<Vec<_>>(), vec![("h1::s9", "there"), ("s1", "here"), ("h1::s8", "older")]);
        assert!(found.problems.is_empty());
        let paths: Vec<String> = seen.try_iter().map(|(p, _)| p).collect();
        assert_eq!(paths, vec!["/api/v1/prompts?limit=100&q=t", "/api/v1/snapshot?sessions=none", "/api/v1/prompts?limit=100&host=h1&q=t"]);
        let (client, _seen) = daemon(replies(&[(200, r#"{"prompts":[]}"#), (200, r#"{"daemon":{"hosts":[{"id":"h1","name":"buildbox","state":"connected"}]}}"#), (502, r#"{"error":{"message":"host timed out"}}"#)]));
        assert_eq!(client.prompts_everywhere("").unwrap().problems, vec!["Couldn't search buildbox: host timed out".to_string()]);
        let (client, _seen) = daemon(replies(&[(200, r#"{"prompts":[{"sessionId":"s1","text":"here"}]}"#), (500, r#"{"error":{"message":"snapshot failed"}}"#)]));
        let found = client.prompts_everywhere("").unwrap();
        assert_eq!((found.prompts.len(), found.problems), (1, vec!["Couldn't find the other computers to search: snapshot failed".to_string()]));
    }

    #[test]
    fn fork_and_goal_send_the_bodies_their_handlers_read() {
        let (client, seen) = daemon(replies(&[(200, r#"{"ok":true,"result":{"session":{"id":"new"}}}"#), (200, r#"{"ok":true,"result":{"session":{"id":"other"}}}"#), (200, r#"{"ok":true,"result":{"evidence":"delivered"}}"#)]));
        assert_eq!(client.fork("h::s1", Some("t2"), "k1").unwrap(), "h::new");
        assert_eq!(seen.recv().unwrap().1, json!({"action": "session-fork", "sessionId": "h::s1", "fork": {"beforeTurnId": "t2"}}));
        assert_eq!(client.fork("s1", None, "k2").unwrap(), "other");
        assert_eq!(seen.recv().unwrap().1, json!({"action": "session-fork", "sessionId": "s1"}));
        client.goal_set("h::s1", "Ship the importer").unwrap();
        assert_eq!(seen.recv().unwrap().1, json!({"action": "goal-set", "sessionId": "h::s1", "message": "Ship the importer"}));
    }
}
