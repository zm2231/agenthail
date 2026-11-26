use serde::Deserialize;

#[derive(Deserialize, Clone, Debug, PartialEq, Default)]
#[serde(rename_all = "camelCase")]
pub struct AgentMessage {
    #[serde(default)]
    pub direction: String,
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub peer: Option<String>,
    #[serde(default)]
    pub channel: Option<String>,
    #[serde(default)]
    pub summary: String,
    #[serde(default)]
    pub body: Option<String>,
}

impl AgentMessage {
    pub fn is_incoming(&self) -> bool {
        self.direction == "in"
    }

    pub fn is_send(&self) -> bool {
        self.direction == "out" && self.kind == "send"
    }

    pub fn chip(&self) -> String {
        let name = self.peer.clone().filter(|p| !p.is_empty()).unwrap_or_else(|| "another agent".into());
        match &self.channel {
            Some(channel) if self.kind == "peer" && !channel.is_empty() => format!("{name} · #{channel}"),
            _ => name,
        }
    }
}

#[derive(Deserialize, Clone, Debug, PartialEq, Default)]
#[serde(rename_all = "camelCase")]
pub struct Item {
    pub id: String,
    pub kind: String,
    #[serde(default)]
    pub role: Option<String>,
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub text: String,
    #[serde(default)]
    pub timestamp: Option<String>,
    #[serde(default)]
    pub call_id: Option<String>,
    #[serde(default)]
    pub status: Option<String>,
    #[serde(default)]
    pub truncated: bool,
    #[serde(default)]
    pub body_ref: Option<String>,
    #[serde(default)]
    pub turn_id: Option<String>,
    #[serde(default)]
    pub agent: Option<AgentMessage>,
}

impl Item {
    pub fn role_is(&self, role: &str) -> bool {
        self.role.as_deref() == Some(role)
    }

    pub fn is_instructions(&self) -> bool {
        self.kind == "instructions"
            || ((self.kind == "message" || self.kind == "text") && (self.role_is("developer") || self.role_is("system")))
    }
}
