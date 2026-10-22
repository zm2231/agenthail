use serde::Deserialize;
use serde_json::Value;

#[derive(Deserialize, Clone, Debug, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Project {
    pub id: String,
    pub display_name: String,
}

#[derive(Deserialize, Clone, Debug, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Checkout {
    #[serde(default)]
    pub path: String,
    #[serde(default)]
    pub branch: Option<String>,
    #[serde(default)]
    pub is_main: bool,
}

#[derive(Deserialize, Clone, Debug, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Session {
    pub id: String,
    pub surface: String,
    pub name: String,
    #[serde(default)]
    pub alias: Option<String>,
    pub status: String,
    pub last_active: String,
    #[serde(default)]
    pub queue_count: i64,
    #[serde(default)]
    pub open: bool,
    #[serde(default)]
    pub current: bool,
    #[serde(default)]
    pub archived: bool,
    #[serde(default)]
    pub waiting_on: Option<Value>,
    #[serde(default)]
    pub project: Option<Project>,
    #[serde(default)]
    pub checkout: Option<Checkout>,
    #[serde(default)]
    pub capabilities: Value,
}

impl Session {
    pub fn title(&self) -> String {
        match &self.alias {
            Some(alias) if !alias.is_empty() => format!("@{alias}"),
            _ => self.name.clone(),
        }
    }

    pub fn is_working(&self) -> bool {
        self.status == "busy"
    }

    pub fn waiting_kind(&self) -> Option<&str> {
        self.waiting_on.as_ref().and_then(|w| w["kind"].as_str())
    }
}

#[derive(Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Attention {
    #[serde(default)]
    pub session_id: Option<String>,
    #[serde(default)]
    pub reason: Option<String>,
}

#[derive(Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Snapshot {
    pub sessions: Vec<Session>,
    #[serde(default)]
    pub catalog_seq: Option<u64>,
    #[serde(default)]
    pub attention: Vec<Value>,
}

#[derive(Deserialize, Clone, Debug, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct TimelineItem {
    pub id: String,
    pub kind: String,
    #[serde(default)]
    pub role: Option<String>,
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub text: String,
    #[serde(default)]
    pub status: Option<String>,
    #[serde(default)]
    pub sender: Option<String>,
}

#[derive(Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Timeline {
    #[serde(default)]
    pub next_before: Option<i64>,
    #[serde(default)]
    pub items: Vec<TimelineItem>,
    #[serde(default)]
    pub unavailable_reason: Option<String>,
}

#[derive(Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Detail {
    #[serde(default)]
    pub journal_seq: Option<u64>,
    #[serde(default)]
    pub timeline: Option<Timeline>,
    #[serde(default)]
    pub capabilities: Value,
}

#[derive(Deserialize, Clone, Debug)]
pub struct QuestionOption {
    pub label: String,
    #[serde(default)]
    pub description: String,
}

#[derive(Deserialize, Clone, Debug)]
pub struct Question {
    pub question: String,
    #[serde(default)]
    pub header: String,
    pub options: Vec<QuestionOption>,
}

#[derive(Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Request {
    pub id: String,
    pub kind: String,
    pub tool: String,
    #[serde(default)]
    pub title: Option<String>,
    #[serde(default)]
    pub questions: Option<Vec<Question>>,
}

#[derive(Deserialize, Clone, Debug)]
pub struct Requests {
    pub requests: Vec<Request>,
}
