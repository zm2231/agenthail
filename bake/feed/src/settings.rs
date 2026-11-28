use std::collections::BTreeMap;

use anyhow::Result;
use serde::Deserialize;

use crate::catalog::{Client, Session, enc};

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Setting {
    #[serde(default)]
    pub value: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Permission {
    #[serde(default)]
    pub mode: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SessionSettings {
    #[serde(default)]
    pub surface: String,
    #[serde(default)]
    pub runtime_kind: String,
    #[serde(default)]
    pub model: Option<Setting>,
    #[serde(default)]
    pub effort: Option<Setting>,
    #[serde(default)]
    pub permission: Option<Permission>,
    #[serde(default)]
    pub collaboration_mode: Option<Setting>,
    #[serde(default)]
    pub unavailable: BTreeMap<String, String>,
    #[serde(skip)]
    pub agent_modes: Vec<String>,
    #[serde(skip)]
    pub agent_mode_unread: bool,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ModelOption {
    pub id: String,
    #[serde(default)]
    pub display_name: String,
    #[serde(default)]
    pub default: bool,
    #[serde(default)]
    pub supported_reasoning_efforts: Vec<String>,
    #[serde(default)]
    pub default_reasoning_effort: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ContextUsage {
    #[serde(default)]
    pub used_tokens: i64,
    #[serde(default)]
    pub context_window: i64,
    #[serde(default)]
    pub compacting: bool,
}

impl ContextUsage {
    pub fn fraction(&self) -> Option<f32> {
        (self.context_window > 0).then(|| (self.used_tokens as f32 / self.context_window as f32).clamp(0.0, 1.0))
    }

    pub fn label(&self) -> String {
        match self.fraction() {
            Some(f) => format!("{}% of {}", (f * 100.0).round() as i64, tokens(self.context_window)),
            None => format!("{} tokens", tokens(self.used_tokens)),
        }
    }
}

fn host_of(session: &Session) -> &str {
    if session.host.state == "local" || session.host.state.is_empty() { "" } else { &session.host.id }
}

pub fn tokens(n: i64) -> String {
    match n {
        n if n >= 1_000_000 => {
            let m = n as f64 / 1_000_000.0;
            if (m - m.round()).abs() < 0.05 { format!("{}M", m.round() as i64) } else { format!("{m:.1}M") }
        }
        n if n >= 1_000 => format!("{}k", (n as f64 / 1_000.0).round() as i64),
        n => n.to_string(),
    }
}

impl Client {
    pub fn session_settings(&self, session: &str) -> Result<SessionSettings> {
        Ok(crate::reply::parse(self.get_json(&format!("/api/v1/session-settings?id={}", enc(session)))?)?)
    }

    pub fn settings_for(&self, session: &Session) -> Result<SessionSettings> {
        let mut settings = self.session_settings(&session.id)?;
        if crate::create::is_acp(&session.surface) {
            self.agent_mode(session, &mut settings);
        }
        Ok(settings)
    }

    fn agent_mode(&self, session: &Session, settings: &mut SessionSettings) {
        let mut missing = Vec::new();
        match self.get_json(&format!("/api/v1/requests?sessionId={}", enc(&session.id))) {
            Ok(state) => settings.permission = state["permissionMode"].as_str().filter(|m| !m.is_empty()).map(|m| Permission { mode: m.into() }),
            Err(error) => missing.push(format!("the current mode isn't readable: {error}")),
        }
        match self.session_options(host_of(session)) {
            Ok(options) => settings.agent_modes = options.surface(&session.surface).map(|x| x.modes.clone()).unwrap_or_default(),
            Err(error) => missing.push(format!("the agent's modes aren't readable: {error}")),
        }
        settings.agent_mode_unread = !missing.is_empty();
        if missing.is_empty() {
            settings.unavailable.remove("mode");
        } else {
            settings.unavailable.insert("mode".into(), missing.join("; "));
        }
    }

    pub fn session_models(&self, surface: &str, session: &str) -> Result<Vec<ModelOption>> {
        let value = self.get_json(&format!("/api/v1/models?surface={}&id={}", enc(surface), enc(session)))?;
        Ok(crate::reply::parse(value["models"].clone()).unwrap_or_default())
    }

    pub fn context_read(&self, session: &str) -> ContextRead {
        match self.get_json(&format!("/api/v1/session-metadata?id={}", enc(session))) {
            Ok(value) => ContextRead { context: context_of(&value).map_err(|e| e.to_string()), metadata: Ok(crate::inspect::metadata(&value)) },
            Err(error) => ContextRead { context: Err(error.to_string()), metadata: Err(error.to_string()) },
        }
    }
}

fn context_of(value: &serde_json::Value) -> Result<Option<ContextUsage>> {
    if let Some(error) = value["errors"]["context"].as_str() {
        anyhow::bail!("{error}");
    }
    Ok(crate::reply::parse(value["context"].clone()).ok())
}

#[derive(Clone, Debug, PartialEq)]
pub struct ContextRead {
    pub context: std::result::Result<Option<ContextUsage>, String>,
    pub metadata: std::result::Result<crate::inspect::Metadata, String>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Choice {
    pub id: String,
    pub label: String,
    pub confirm: bool,
}

impl Choice {
    fn new(id: &str, label: &str) -> Choice {
        Choice { id: id.into(), label: label.into(), confirm: false }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Chip {
    pub key: &'static str,
    pub name: &'static str,
    pub value: String,
    pub choices: Vec<Choice>,
    pub changeable: bool,
    pub next_turn: bool,
    pub steps: bool,
    pub note: Option<String>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Pick {
    Unchanged,
    Apply,
    Confirm,
}

impl Chip {
    pub fn label(&self) -> String {
        if let Some(c) = self.choices.iter().find(|c| c.id == self.value) {
            return c.label.clone();
        }
        if !self.value.is_empty() {
            return self.value.clone();
        }
        match self.key {
            "effort" => "Effort".into(),
            "mode" => "Default mode".into(),
            _ => "Default".into(),
        }
    }

    pub fn pick(&self, id: &str) -> Pick {
        match self.choices.iter().find(|c| c.id == id) {
            _ if id == self.value => Pick::Unchanged,
            Some(c) if c.confirm => Pick::Confirm,
            _ => Pick::Apply,
        }
    }

    /// Choices that let the agent act without asking are only ever picked from the menu.
    pub fn cycled(&self) -> Option<&Choice> {
        if !self.changeable || !self.steps {
            return None;
        }
        let quiet: Vec<&Choice> = self.choices.iter().filter(|c| !c.confirm).collect();
        let next = match quiet.iter().position(|c| c.id == self.value) {
            Some(i) => quiet[(i + 1) % quiet.len()],
            None => *quiet.first()?,
        };
        (next.id != self.value).then_some(next)
    }
}

const CLAUDE_HOST: &str = "claude-host";
const CLAUDE_MODES: [(&str, &str, bool); 6] =
    [("manual", "Manual", false), ("acceptEdits", "Accept edits", false), ("plan", "Plan", false), ("auto", "Auto", false), ("dontAsk", "Don't ask", true), ("bypassPermissions", "Bypass permissions", true)];
const CODEX_MODES: [(&str, &str); 2] = [("default", "Default"), ("plan", "Plan")];
const CLAUDE_REMOTE_EFFORTS: [&str; 4] = ["low", "medium", "high", "max"];
const CLAUDE_HOST_EFFORTS: [&str; 5] = ["low", "medium", "high", "xhigh", "max"];

pub fn effort_label(level: &str) -> String {
    if level == "xhigh" {
        return "Extra high".into();
    }
    let mut c = level.chars();
    c.next().map(|f| f.to_uppercase().collect::<String>() + c.as_str()).unwrap_or_default()
}

fn claude_modes() -> Vec<Choice> {
    CLAUDE_MODES.iter().map(|(id, label, confirm)| Choice { id: (*id).into(), label: (*label).into(), confirm: *confirm }).collect()
}

pub fn chips(settings: &SessionSettings, models: &[ModelOption], session: &Session) -> Vec<Chip> {
    let mut out = offered(settings, models, &session.capabilities);
    if session.read_only {
        for chip in &mut out {
            chip.changeable = false;
        }
    }
    out
}

fn offered(settings: &SessionSettings, models: &[ModelOption], caps: &crate::catalog::Capabilities) -> Vec<Chip> {
    let codex = settings.surface == "codex";
    let claude = settings.surface == "claude";
    let hosted = settings.runtime_kind == CLAUDE_HOST;
    let note = |key: &str| settings.unavailable.get(key).cloned();
    let mut out = Vec::new();

    let mut model = settings.model.as_ref().map(|m| m.value.clone()).unwrap_or_default();
    if claude && model.is_empty() {
        model = "default".into();
    }
    let mut model_choices: Vec<Choice> = models.iter().map(|m| Choice::new(&m.id, if m.display_name.is_empty() { &m.id } else { &m.display_name })).collect();
    if !model.is_empty() && !model_choices.iter().any(|c| c.id == model) {
        model_choices.push(Choice::new(&model, &model));
    }
    if !model_choices.is_empty() {
        let changeable = caps.model && !models.is_empty();
        out.push(Chip { key: "model", name: "Model", value: model.clone(), choices: model_choices, changeable, next_turn: codex, steps: false, note: note("model") });
    }

    let running = models.iter().find(|m| m.id == model);
    let efforts: Vec<String> = if codex {
        running.map(|m| m.supported_reasoning_efforts.clone()).unwrap_or_default()
    } else if claude && hosted {
        running.map(|m| m.supported_reasoning_efforts.clone()).filter(|e| !e.is_empty()).unwrap_or_else(|| CLAUDE_HOST_EFFORTS.iter().map(|e| e.to_string()).collect())
    } else if claude && caps.commands {
        CLAUDE_REMOTE_EFFORTS.iter().map(|e| e.to_string()).collect()
    } else {
        Vec::new()
    };
    let mut effort = settings.effort.as_ref().map(|e| e.value.clone()).unwrap_or_default();
    if codex && effort.is_empty() {
        effort = running.map(|m| m.default_reasoning_effort.clone()).unwrap_or_default();
    }
    if !efforts.is_empty() || !effort.is_empty() {
        let changeable = !efforts.is_empty();
        let mut choices: Vec<Choice> = efforts.iter().map(|e| Choice::new(e, &effort_label(e))).collect();
        if !effort.is_empty() && !choices.iter().any(|c| c.id == effort) {
            choices.push(Choice::new(&effort, &effort_label(&effort)));
        }
        out.push(Chip { key: "effort", name: "Effort", value: effort, choices, changeable, next_turn: codex, steps: false, note: note("effort") });
    }

    if codex {
        let value = settings.collaboration_mode.as_ref().map(|m| m.value.clone()).filter(|v| !v.is_empty()).unwrap_or_else(|| "default".into());
        let choices = CODEX_MODES.iter().map(|(id, label)| Choice::new(id, label)).collect();
        out.push(Chip { key: "mode", name: "Mode", value, choices, changeable: true, next_turn: true, steps: true, note: None });
    } else if claude {
        let value = settings.permission.as_ref().map(|p| p.mode.clone()).unwrap_or_default();
        if hosted || !value.is_empty() {
            let note = (!hosted).then(|| "Only sessions Agenthail runs can change this".to_string()).or_else(|| note("permission"));
            out.push(Chip { key: "mode", name: "Permission mode", value, choices: claude_modes(), changeable: hosted, next_turn: false, steps: true, note });
        }
    } else if crate::create::is_acp(&settings.surface) {
        let value = settings.permission.as_ref().map(|p| p.mode.clone()).unwrap_or_default();
        let mut choices: Vec<Choice> = settings.agent_modes.iter().map(|m| Choice::new(m, m)).collect();
        if !value.is_empty() && !choices.iter().any(|c| c.id == value) {
            choices.push(Choice::new(&value, &value));
        }
        if !choices.is_empty() {
            let changeable = caps.permission_mode && !settings.agent_modes.is_empty();
            out.push(Chip { key: "mode", name: "Mode", value, choices, changeable, next_turn: false, steps: false, note: note("mode") });
        }
    }
    out
}

pub fn apply(settings: &mut SessionSettings, key: &str, value: &str) {
    let set = Setting { value: value.into() };
    match key {
        "model" => settings.model = Some(set),
        "effort" => {
            settings.effort = Some(set);
            settings.unavailable.remove("effort");
        }
        "mode" if settings.surface == "codex" => settings.collaboration_mode = Some(set),
        "mode" => settings.permission = Some(Permission { mode: value.into() }),
        _ => {}
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::Capabilities;
    use serde_json::json;

    fn opts(models: serde_json::Value) -> Vec<ModelOption> {
        crate::reply::parse(models).unwrap()
    }

    fn claude_host() -> SessionSettings {
        crate::reply::parse(json!({
            "surface": "claude", "runtimeKind": "claude-host",
            "model": {"value": "claude-opus-5-5", "source": "transcript"}, "effort": null,
            "permission": {"mode": "bypassPermissions", "source": "runtime"},
            "unavailable": {"effort": "the Claude host reports no effort for this session"}
        }))
        .unwrap()
    }

    fn codex() -> SessionSettings {
        crate::reply::parse(json!({
            "surface": "codex", "runtimeKind": "codex-app-server",
            "model": {"value": "gpt-6.1-sol", "source": "rollout", "listed": true},
            "effort": {"value": "xhigh", "source": "rollout"},
            "permission": {"approvalPolicy": "never", "sandbox": "danger-full-access", "source": "rollout"},
            "collaborationMode": {"value": "default", "source": "rollout"}
        }))
        .unwrap()
    }

    fn all() -> Session {
        with(Capabilities { model: true, compact: true, commands: true, ..Capabilities::default() })
    }

    fn with(capabilities: Capabilities) -> Session {
        Session { capabilities, ..Default::default() }
    }

    fn ids(c: &Chip) -> Vec<&str> {
        c.choices.iter().map(|c| c.id.as_str()).collect()
    }

    #[test]
    fn a_hosted_claude_session_offers_its_models_efforts_and_permission_modes() {
        let models = opts(json!([
            {"id": "default", "displayName": "Default (recommended)", "default": true, "supportedReasoningEfforts": ["low", "medium", "high", "xhigh", "max"]},
            {"id": "opus", "displayName": "Opus 5.5", "supportedReasoningEfforts": ["low", "medium", "high", "xhigh", "max"]}
        ]));
        let chips = chips(&claude_host(), &models, &all());
        assert_eq!(chips.iter().map(|c| c.key).collect::<Vec<_>>(), vec!["model", "effort", "mode"]);
        assert_eq!(ids(&chips[0]), vec!["default", "opus", "claude-opus-5-5"]);
        assert_eq!(chips[0].label(), "claude-opus-5-5");
        assert!(chips[0].changeable && !chips[0].next_turn);
        assert_eq!(ids(&chips[1]), vec!["low", "medium", "high", "xhigh", "max"]);
        assert_eq!(chips[1].label(), "Effort");
        assert_eq!(chips[1].note.as_deref(), Some("the Claude host reports no effort for this session"));
        assert_eq!(chips[2].label(), "Bypass permissions");
        assert!(chips[2].changeable);
        assert_eq!(chips[2].pick("plan"), Pick::Apply);
        assert_eq!(chips[2].pick("dontAsk"), Pick::Confirm);
        assert_eq!(chips[2].pick("bypassPermissions"), Pick::Unchanged);
    }

    #[test]
    fn a_hosted_claude_effort_follows_the_running_models_levels() {
        let models = opts(json!([{"id": "claude-opus-5-5", "displayName": "Opus", "supportedReasoningEfforts": ["low", "high"]}]));
        let chips = chips(&claude_host(), &models, &all());
        assert_eq!(ids(&chips[1]), vec!["low", "high"]);
    }

    #[test]
    fn a_terminal_claude_session_shows_its_permission_mode_without_changing_it() {
        let mut s = claude_host();
        s.runtime_kind = "claude-cli".into();
        let chips = chips(&s, &[], &all());
        assert_eq!(chips[0].key, "model");
        assert!(!chips[0].changeable, "no model list, nothing to choose from");
        assert_eq!(ids(&chips[1]), vec!["low", "medium", "high", "max"]);
        let mode = chips.iter().find(|c| c.key == "mode").unwrap();
        assert!(!mode.changeable);
        assert_eq!(mode.label(), "Bypass permissions");
        s.permission = None;
        assert!(super::chips(&s, &[], &all()).iter().all(|c| c.key != "mode"));
    }

    #[test]
    fn a_terminal_claude_session_without_commands_offers_no_effort_the_daemon_would_refuse() {
        let mut s = claude_host();
        s.runtime_kind = "claude-cli".into();
        s.effort = Some(Setting { value: "high".into() });
        let models = opts(json!([{"id": "opus", "displayName": "Opus"}]));
        let caps = Capabilities { model: true, compact: true, commands: false, ..Capabilities::default() };
        let chips = super::chips(&s, &models, &with(caps.clone()));
        let effort = chips.iter().find(|c| c.key == "effort").unwrap();
        assert!(!effort.changeable);
        assert_eq!(effort.label(), "High");
        s.effort = None;
        assert!(super::chips(&s, &models, &with(caps.clone())).iter().all(|c| c.key != "effort"));
        let no_model = Capabilities { model: false, ..caps };
        assert!(!super::chips(&s, &models, &with(no_model))[0].changeable);
    }

    #[test]
    fn a_read_only_session_shows_its_settings_without_offering_changes() {
        let models = opts(json!([{"id": "gpt-6.1-sol", "displayName": "GPT-6.1-Sol", "supportedReasoningEfforts": ["low", "xhigh"]}]));
        let mut x = all();
        x.read_only = true;
        let chips = chips(&codex(), &models, &x);
        assert_eq!(chips.len(), 3);
        assert!(chips.iter().all(|c| !c.changeable));
        assert_eq!(chips[1].label(), "Extra high");
    }

    #[test]
    fn a_codex_session_offers_the_running_models_efforts_and_collaboration_mode() {
        let models = opts(json!([
            {"id": "gpt-6.1-sol", "displayName": "GPT-6.1-Sol", "supportedReasoningEfforts": ["low", "medium", "high", "xhigh"], "defaultReasoningEffort": "medium"},
            {"id": "gpt-6-luna", "displayName": "GPT-6-Luna"}
        ]));
        let chips = chips(&codex(), &models, &all());
        assert_eq!(chips[0].label(), "GPT-6.1-Sol");
        assert!(chips.iter().all(|c| c.next_turn));
        assert_eq!(chips[1].label(), "Extra high");
        assert_eq!(chips[2].name, "Mode");
        assert_eq!(ids(&chips[2]), vec!["default", "plan"]);
        let mut s = codex();
        s.effort = None;
        assert_eq!(super::chips(&s, &models, &all())[1].value, "medium");
        s.model = Some(Setting { value: "gpt-6-luna".into() });
        assert!(super::chips(&s, &models, &all()).iter().all(|c| c.key != "effort"), "a model with no levels has no effort chip");
    }

    #[test]
    fn a_model_the_list_lacks_is_still_shown() {
        let models = opts(json!([{"id": "gpt-6-luna", "displayName": "GPT-6-Luna"}]));
        let chips = chips(&codex(), &models, &all());
        assert_eq!(ids(&chips[0]), vec!["gpt-6-luna", "gpt-6.1-sol"]);
        let effort = chips.iter().find(|c| c.key == "effort").unwrap();
        assert!(!effort.changeable);
        assert_eq!(effort.label(), "Extra high");
    }

    #[test]
    fn a_change_shows_before_the_next_read() {
        let mut s = claude_host();
        apply(&mut s, "effort", "high");
        apply(&mut s, "mode", "plan");
        apply(&mut s, "model", "opus");
        assert_eq!((s.effort.unwrap().value, s.permission.unwrap().mode, s.model.unwrap().value), ("high".into(), "plan".into(), "opus".into()));
        assert!(!s.unavailable.contains_key("effort"));
        let mut c = codex();
        apply(&mut c, "mode", "plan");
        assert_eq!(c.collaboration_mode.unwrap().value, "plan");
    }

    #[test]
    fn context_use_reads_as_a_share_of_the_window() {
        let used = ContextUsage { used_tokens: 246_362, context_window: 475_000, compacting: false };
        assert_eq!(used.label(), "52% of 475k");
        assert!((used.fraction().unwrap() - 0.5187).abs() < 0.001);
        let unknown = ContextUsage { used_tokens: 183_741, context_window: 0, compacting: false };
        assert_eq!((unknown.fraction(), unknown.label()), (None, "184k tokens".into()));
        assert_eq!((tokens(1_000_000), tokens(1_250_000), tokens(950)), ("1M".into(), "1.2M".into(), "950".into()));
        assert_eq!(ContextUsage { used_tokens: 600_000, context_window: 500_000, compacting: false }.fraction(), Some(1.0));
    }

    #[test]
    fn each_read_asks_the_sessions_host_for_its_own_endpoint() {
        use std::io::{BufRead, BufReader, Write};
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let replies = [
            r#"{"surface":"codex","model":{"value":"gpt-6.1-sol"}}"#,
            r#"{"models":[{"id":"gpt-6.1-sol","displayName":"GPT-6.1-Sol"}]}"#,
            r#"{"sessionId":"h1::s 1","context":{"usedTokens":10,"contextWindow":100}}"#,
            r#"{"sessionId":"h1::s 1","errors":{"context":"timed out"}}"#,
            r#"{"sessionId":"h1::s 1"}"#,
        ];
        let paths = std::thread::spawn(move || {
            let mut paths = Vec::new();
            for body in replies {
                let (stream, _) = listener.accept().unwrap();
                let mut reader = BufReader::new(stream.try_clone().unwrap());
                let mut line = String::new();
                reader.read_line(&mut line).unwrap();
                paths.push(line.split_whitespace().nth(1).unwrap_or_default().to_string());
                loop {
                    let mut header = String::new();
                    reader.read_line(&mut header).unwrap();
                    if header.trim().is_empty() {
                        break;
                    }
                }
                let mut stream = stream;
                write!(stream, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len()).unwrap();
            }
            paths
        });
        let client = Client::new(&base, "secret");
        assert_eq!(client.session_settings("h1::s 1").unwrap().model.unwrap().value, "gpt-6.1-sol");
        assert_eq!(client.session_models("codex", "h1::s 1").unwrap()[0].display_name, "GPT-6.1-Sol");
        let read = client.context_read("h1::s 1");
        assert_eq!(read.context.unwrap().unwrap().fraction(), Some(0.1));
        assert!(read.metadata.is_ok());
        let failed = client.context_read("h1::s 1");
        assert_eq!(failed.context.unwrap_err(), "timed out");
        assert!(failed.metadata.unwrap().warnings.contains(&"Context usage: timed out".to_string()));
        assert_eq!(client.context_read("h1::s 1").context.unwrap(), None);
        assert_eq!(
            paths.join().unwrap(),
            vec![
                "/api/v1/session-settings?id=h1%3A%3As+1",
                "/api/v1/models?surface=codex&id=h1%3A%3As+1",
                "/api/v1/session-metadata?id=h1%3A%3As+1",
                "/api/v1/session-metadata?id=h1%3A%3As+1",
                "/api/v1/session-metadata?id=h1%3A%3As+1"
            ]
        );
    }

    #[test]
    fn shift_tab_steps_through_the_modes_that_still_ask_first() {
        let mut s = claude_host();
        let mode = |s: &SessionSettings| chips(s, &[], &all()).into_iter().find(|c| c.key == "mode").unwrap();
        let next = |s: &SessionSettings| mode(s).cycled().map(|c| c.id.clone());
        assert_eq!(next(&s).as_deref(), Some("manual"), "from a mode only the menu picks, start at the first");
        s.permission = Some(Permission { mode: "acceptEdits".into() });
        assert_eq!(next(&s).as_deref(), Some("plan"));
        s.permission = Some(Permission { mode: "auto".into() });
        assert_eq!(next(&s).as_deref(), Some("manual"), "wraps without landing on don't ask or bypass");
        s.runtime_kind = "claude-cli".into();
        assert_eq!(next(&s), None, "a mode Agenthail can't change doesn't cycle");
        let mut codex = codex();
        assert_eq!(next(&codex).as_deref(), Some("plan"));
        codex.collaboration_mode = Some(Setting { value: "plan".into() });
        assert_eq!(next(&codex).as_deref(), Some("default"));
    }

    fn devin() -> SessionSettings {
        let mut s: SessionSettings = crate::reply::parse(json!({
            "surface": "devin", "runtimeKind": "acp",
            "model": {"value": "swe-2", "source": "started"},
            "unavailable": {"effort": "this surface does not report its settings", "permission": "this surface does not report its settings"}
        }))
        .unwrap();
        s.permission = Some(Permission { mode: "ask".into() });
        s.agent_modes = vec!["ask".into(), "default".into(), "bypass".into()];
        s
    }

    #[test]
    fn an_acp_session_offers_its_agents_own_modes_from_the_menu_only() {
        let caps = Capabilities { model: true, permission_mode: true, ..Capabilities::default() };
        let models = opts(json!([{"id": "swe-2", "displayName": "SWE 2"}, {"id": "adaptive", "displayName": "Adaptive"}]));
        let chips = chips(&devin(), &models, &with(caps.clone()));
        assert_eq!(chips.iter().map(|c| c.key).collect::<Vec<_>>(), vec!["model", "mode"]);
        let mode = &chips[1];
        assert_eq!((mode.name, mode.label(), ids(mode)), ("Mode", "ask".to_string(), vec!["ask", "default", "bypass"]));
        assert!(mode.changeable && !mode.next_turn);
        assert_eq!(mode.pick("bypass"), Pick::Apply);
        assert_eq!(mode.cycled(), None, "which of an agent's modes skip asking is unknown, so Shift-Tab leaves them alone");
        let fixed = super::chips(&devin(), &models, &with(Capabilities { permission_mode: false, ..caps.clone() }));
        assert!(!fixed[1].changeable);
        let mut unknown = devin();
        unknown.agent_modes.clear();
        let shown = super::chips(&unknown, &models, &with(caps.clone()));
        assert_eq!((ids(&shown[1]), shown[1].changeable), (vec!["ask"], false), "a mode with no list to choose from is shown, not offered");
        unknown.permission = None;
        assert!(super::chips(&unknown, &models, &with(caps.clone())).iter().all(|c| c.key != "mode"));
        let mut s = devin();
        apply(&mut s, "mode", "bypass");
        assert_eq!(s.permission.unwrap().mode, "bypass");
    }

    #[test]
    fn an_acp_sessions_settings_add_its_mode_and_its_hosts_modes() {
        let (client, seen) = crate::mock::daemon(crate::mock::replies(&[
            (200, r#"{"surface":"devin","model":{"value":"swe-2"}}"#),
            (200, r#"{"permissionMode":"ask","requests":[]}"#),
            (200, r#"{"surfaces":[{"id":"claude","workspace":true},{"id":"devin","workspace":true,"modes":["ask","bypass"]}],"projects":[]}"#),
            (200, r#"{"surface":"devin"}"#),
            (500, r#"{"error":{"code":"x","message":"down"}}"#),
            (200, r#"{"surfaces":[]}"#),
            (200, r#"{"surface":"claude"}"#),
        ]));
        let mut remote: Session = crate::reply::parse(json!({"id": "d1", "surface": "devin", "host": {"id": "h3a", "state": "connected"}})).unwrap();
        let read = client.settings_for(&remote).unwrap();
        assert!(!read.agent_mode_unread);
        assert_eq!((read.permission.unwrap().mode, read.agent_modes, read.unavailable.get("mode")), ("ask".into(), vec!["ask".to_string(), "bypass".into()], None));
        remote.host.state = "local".into();
        let half = client.settings_for(&remote).unwrap();
        assert!(half.permission.is_none() && half.agent_modes.is_empty());
        assert!(half.agent_mode_unread, "a half-read mode is read again");
        assert!(half.unavailable["mode"].starts_with("the current mode isn't readable"), "{:?}", half.unavailable);
        let claude: Session = crate::reply::parse(json!({"id": "c1", "surface": "claude"})).unwrap();
        assert!(client.settings_for(&claude).unwrap().agent_modes.is_empty());
        let paths: Vec<String> = seen.try_iter().map(|(path, _)| path).collect();
        assert_eq!(
            paths,
            vec![
                "/api/v1/session-settings?id=d1",
                "/api/v1/requests?sessionId=d1",
                "/api/v1/session-options?host=h3a",
                "/api/v1/session-settings?id=d1",
                "/api/v1/requests?sessionId=d1",
                "/api/v1/session-options",
                "/api/v1/session-settings?id=c1"
            ]
        );
    }

    #[test]
    fn effort_levels_read_as_words() {
        assert_eq!((effort_label("xhigh"), effort_label("max"), effort_label("")), ("Extra high".into(), "Max".into(), String::new()));
    }
}
