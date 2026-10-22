use std::io::{BufRead, BufReader};
use std::time::Duration;

use anyhow::{Context as _, Result, anyhow};
use serde::de::DeserializeOwned;
use serde_json::Value;

/// A client for the daemon's HTTP API, configured like the Mac app through
/// `AGENTHAIL_API_URL` and `AGENTHAIL_API_TOKEN`.
#[derive(Clone)]
pub struct Api {
    base: String,
    token: String,
    agent: ureq::Agent,
}

pub enum StreamError {
    /// The cursor fell out of the daemon's retained window; reload and restart.
    Gap,
    Unsupported,
    Other(String),
}

impl Api {
    pub fn from_env() -> Result<Self> {
        let base = std::env::var("AGENTHAIL_API_URL").context("AGENTHAIL_API_URL is not set")?;
        let token = std::env::var("AGENTHAIL_API_TOKEN").context("AGENTHAIL_API_TOKEN is not set")?;
        let agent = ureq::AgentBuilder::new().timeout_connect(Duration::from_secs(5)).build();
        Ok(Self { base: base.trim_end_matches('/').to_string(), token, agent })
    }

    fn url(&self, path: &str) -> String {
        format!("{}{}", self.base, path)
    }

    pub fn get<T: DeserializeOwned>(&self, path: &str) -> Result<T> {
        let response = self
            .agent
            .get(&self.url(path))
            .timeout(Duration::from_secs(25))
            .set("Authorization", &format!("Bearer {}", self.token))
            .set("Accept", "application/json")
            .call()
            .map_err(request_error)?;
        Ok(response.into_json()?)
    }

    pub fn post_action(&self, body: Value) -> Result<Value> {
        let response = self
            .agent
            .post(&self.url("/api/v1/actions"))
            .timeout(Duration::from_secs(65))
            .set("Authorization", &format!("Bearer {}", self.token))
            .set("Accept", "application/json")
            .set("Idempotency-Key", &uuid::Uuid::new_v4().to_string())
            .send_json(body)
            .map_err(request_error)?;
        Ok(response.into_json()?)
    }

    /// Reads one server-sent event stream until it ends, handing each event's
    /// JSON to `on_event`. Blocks the calling thread.
    pub fn stream(&self, path: &str, after: u64, mut on_event: impl FnMut(Value) -> bool) -> Result<(), StreamError> {
        let response = self
            .agent
            .get(&self.url(path))
            .set("Authorization", &format!("Bearer {}", self.token))
            .set("Accept", "text/event-stream")
            .set("Last-Event-ID", &after.to_string())
            .call();
        let response = match response {
            Ok(response) => response,
            Err(ureq::Error::Status(409, response)) => {
                let body: Value = response.into_json().unwrap_or(Value::Null);
                return Err(match body["error"]["code"].as_str() {
                    Some("stream_gap") => StreamError::Gap,
                    Some("stream_unsupported") => StreamError::Unsupported,
                    _ => StreamError::Other(body.to_string()),
                });
            }
            Err(error) => return Err(StreamError::Other(error.to_string())),
        };
        let mut data: Vec<String> = Vec::new();
        for line in BufReader::new(response.into_reader()).lines() {
            let line = line.map_err(|error| StreamError::Other(error.to_string()))?;
            if line.is_empty() {
                if !data.is_empty() {
                    let joined = data.join("\n");
                    data.clear();
                    if let Ok(event) = serde_json::from_str::<Value>(&joined) {
                        if !on_event(event) {
                            return Ok(());
                        }
                    }
                }
                continue;
            }
            if let Some(value) = line.strip_prefix("data:") {
                data.push(value.strip_prefix(' ').unwrap_or(value).to_string());
            }
        }
        Err(StreamError::Other("stream closed".into()))
    }
}

fn request_error(error: ureq::Error) -> anyhow::Error {
    match error {
        ureq::Error::Status(code, response) => {
            let body: Value = response.into_json().unwrap_or(Value::Null);
            let message = body["error"]["message"]
                .as_str()
                .or_else(|| body["error"].as_str())
                .unwrap_or("request failed")
                .to_string();
            anyhow!("{code}: {message}")
        }
        other => anyhow!(other.to_string()),
    }
}

pub fn query(value: &str) -> String {
    url::form_urlencoded::byte_serialize(value.as_bytes()).collect()
}
