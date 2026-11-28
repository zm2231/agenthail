use serde::Deserialize;
use serde_json::{Value, json};
use time::OffsetDateTime;

use super::View;
use crate::catalog::{Client, enc, parse_time};
use crate::prefs::{Failure, read, write_failure};

pub const KEYS: [&str; 17] = [
    "theme",
    "theme.accent",
    "background",
    "background.fit",
    "background.position",
    "background.dim",
    "background.opacity",
    "background.session-opacity",
    "glass",
    "glass.blur",
    "glass.tint",
    "glass.opacity",
    "glass.composer.opacity",
    "glass.readability",
    "texture",
    "texture.strength",
    "text.scale",
];

const IMAGE_LIMIT: u64 = 64 << 20;

pub fn device_class() -> &'static str {
    if cfg!(target_os = "macos") { "mac" } else { "web" }
}

pub fn device_name() -> String {
    let named = if cfg!(target_os = "macos") {
        std::process::Command::new("scutil").args(["--get", "ComputerName"]).output().ok().filter(|o| o.status.success()).map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
    } else {
        std::fs::read_to_string("/etc/hostname").ok().map(|h| h.trim().to_string())
    };
    named.filter(|n| !n.is_empty()).unwrap_or_default()
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Scope {
    Everywhere,
    ThisComputer(String),
}

impl Scope {
    fn name(&self, key: &str) -> String {
        match self {
            Scope::Everywhere => key.to_string(),
            Scope::ThisComputer(device) => format!("{key}@device:{device}"),
        }
    }

    fn suffix(&self) -> String {
        match self {
            Scope::Everywhere => String::new(),
            Scope::ThisComputer(device) => format!(" on {device}"),
        }
    }
}

pub fn owner(line: &str, this_device: &str) -> String {
    match line.strip_prefix("device:") {
        Some(named) if named == this_device => "This computer".into(),
        Some(named) => named.to_string(),
        None if line == "mac" => "All Macs".into(),
        None => line.to_string(),
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct Change(pub Value);

impl Change {
    pub fn set(scope: &Scope, key: &str, value: &str, label: &str) -> Change {
        Change(json!({"action": "set", "set": [format!("{} = {value}", scope.name(key))], "message": format!("Set {label}{}", scope.suffix())}))
    }

    pub fn reset(scope: &Scope) -> Change {
        Change(json!({"action": "unset", "unset": KEYS.iter().map(|k| scope.name(k)).collect::<Vec<_>>(), "message": format!("Reset appearance{}", scope.suffix())}))
    }

    pub fn undo(rev: Option<&str>) -> Change {
        match rev {
            Some(rev) => Change(json!({"action": "undo", "rev": rev})),
            None => Change(json!({"action": "undo"})),
        }
    }

    pub fn text_scale(scale: f32, device: &str) -> Change {
        let place = if device.is_empty() { "mac".to_string() } else { format!("device:{device}") };
        let percent = (scale * 100.0).round() as i64;
        Change(json!({"action": "set", "set": [format!("text.scale@{place} = {}", percent as f64 / 100.0)], "message": format!("Text size {percent}%")}))
    }

    pub fn touches(&self, key: &str) -> bool {
        ["set", "unset"].iter().flat_map(|field| self.0[*field].as_array().into_iter().flatten()).filter_map(Value::as_str).any(|line| line.trim_start().starts_with(key))
    }

    pub fn clear_override(key: &str, line: &str, owner: &str) -> Change {
        Change(json!({"action": "unset", "unset": [format!("{key}@{line}")], "message": format!("Reset {key} for {owner}")}))
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Entry {
    #[serde(default)]
    pub rev: String,
    #[serde(default)]
    pub author: String,
    #[serde(default)]
    pub time: String,
    #[serde(default)]
    pub message: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Preset {
    #[serde(default)]
    pub value: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub dark: String,
    #[serde(default)]
    pub light: String,
}

pub fn describe(entry: &Entry, now: OffsetDateTime) -> String {
    let who = if entry.author == "person" || entry.author.is_empty() { "You".to_string() } else { entry.author.clone() };
    let Some(at) = parse_time(&entry.time) else { return who };
    let minutes = (now - at).whole_minutes().max(0);
    let ago = match minutes {
        0 => "just now".to_string(),
        1..=59 => format!("{minutes} min ago"),
        60..=1439 => format!("{} h ago", minutes / 60),
        _ => format!("{} d ago", minutes / 1440),
    };
    format!("{who} · {ago}")
}

pub const TEXT_SCALE: (f32, f32) = (0.8, 1.6);

pub fn text_scale_step(scale: f32, steps: Option<i32>) -> f32 {
    let Some(steps) = steps else { return 1.0 };
    let next = scale + steps as f32 * 0.1;
    ((next * 20.0).round() / 20.0).clamp(TEXT_SCALE.0, TEXT_SCALE.1)
}

fn device_query(class: &str, device: &str) -> String {
    format!("class={}&device={}", enc(class), enc(device))
}

impl Client {
    pub fn ui_view(&self, class: &str, device: &str) -> Result<View, Failure> {
        read(self, &format!("/api/v1/ui?{}", device_query(class, device)), "read")
    }

    pub fn ui_log(&self, limit: usize) -> Result<Vec<Entry>, Failure> {
        let value: Value = read(self, &format!("/api/v1/ui/log?limit={limit}"), "read")?;
        Ok(crate::reply::parse(value["entries"].clone()).unwrap_or_default())
    }

    pub fn ui_presets(&self) -> Result<Vec<Preset>, Failure> {
        let value: Value = read(self, "/api/v1/ui/presets", "read")?;
        Ok(crate::reply::parse(value["presets"].clone()).unwrap_or_default())
    }

    pub fn ui_change(&self, class: &str, device: &str, change: &Change) -> Result<View, Failure> {
        let value = self.post_json(&format!("/api/v1/ui?{}", device_query(class, device)), change.0.clone()).map_err(|e| write_failure("settings", e))?;
        crate::reply::parse(value).map_err(|e| Failure { uncertain: true, ..Failure::new(format!("the daemon's reply couldn't be read: {e}")) })
    }

    pub fn ui_upload(&self, name: &str, data: &[u8]) -> Result<String, Failure> {
        let url = format!("{}/api/v1/ui/background?name={}", self.base, enc(name));
        let request = self.timed(std::time::Duration::from_secs(180)).agent.post(&url).set("Authorization", &self.bearer()).set("Accept", "application/json").set("Content-Type", "application/octet-stream");
        let reply: Value = match request.send_bytes(data) {
            Ok(response) => response.into_json().map_err(|e| Failure::new(format!("the daemon's reply couldn't be read: {e}")))?,
            Err(ureq::Error::Status(code, response)) => return Err(crate::prefs::read_failure("settings", code, &response.into_string().unwrap_or_default())),
            Err(ureq::Error::Transport(error)) => return Err(Failure::new(error.to_string())),
        };
        reply["path"].as_str().filter(|p| !p.is_empty()).map(str::to_string).ok_or_else(|| Failure::new("the daemon didn't say where it kept the picture"))
    }

    pub fn ui_background(&self, path: &str) -> Result<Vec<u8>, String> {
        let url = format!("{}/api/v1/ui/background?path={}", self.base, enc(path));
        let response = match self.timed(std::time::Duration::from_secs(60)).agent.get(&url).set("Authorization", &self.bearer()).call() {
            Ok(response) => response,
            Err(ureq::Error::Status(code, _)) => return Err(format!("the daemon answered {code} for {path}")),
            Err(ureq::Error::Transport(error)) => return Err(error.to_string()),
        };
        let mut data = Vec::new();
        std::io::Read::read_to_end(&mut std::io::Read::take(response.into_reader(), IMAGE_LIMIT + 1), &mut data).map_err(|e| e.to_string())?;
        if data.len() as u64 > IMAGE_LIMIT {
            return Err(format!("{path} is too large to show"));
        }
        Ok(data)
    }
}
