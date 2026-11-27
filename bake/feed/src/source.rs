use std::io::{BufRead, BufReader};
use std::time::{Duration, Instant};

use anyhow::{Context, Result};

use crate::wake::Post;
use crate::wire::{Event, Outcome, Rebuild, Sse, Wire};

pub struct Batch {
    pub seq: u64,
    pub events: Vec<Event>,
}

pub enum Message {
    Batch(Batch),
    Status(String),
    CaughtUp,
}

fn feed_line(line: &str, sse: &mut Sse, rebuild: &mut Rebuild, tx: &impl Post<Message>) -> Result<bool, ()> {
    let Some(json) = sse.line(line) else { return Ok(true) };
    let Ok(wire) = serde_json::from_str::<Wire>(&json) else { return Ok(true) };
    match rebuild.apply(wire) {
        Outcome::Events(seq, events) => Ok(tx.post(Message::Batch(Batch { seq, events })).is_ok()),
        Outcome::Skip => Ok(true),
        Outcome::Resync => Err(()),
    }
}

pub struct Live {
    pub base: String,
    pub token: String,
    pub session: String,
}

impl Live {
    pub fn from_env(session: String) -> Result<Self> {
        let (base, token) = crate::daemon()?;
        Ok(Self { base: base.trim_end_matches('/').into(), token, session })
    }

    pub fn run(self, tx: impl Post<Message>) {
        let agent = ureq::AgentBuilder::new().timeout_connect(Duration::from_secs(5)).build();
        let id: String = url::form_urlencoded::byte_serialize(self.session.as_bytes()).collect();
        let mut after = 0u64;
        loop {
            let url = format!("{}/api/v1/session-stream?id={id}&after={after}", self.base);
            let response = agent
                .get(&url)
                .set("Authorization", &format!("Bearer {}", self.token))
                .set("Accept", "text/event-stream")
                .set("Last-Event-ID", &after.to_string())
                .call();
            match response {
                Ok(response) => {
                    let _ = tx.post(Message::Status("live".into()));
                    let mut sse = Sse::default();
                    let mut rebuild = Rebuild::default();
                    for line in BufReader::new(response.into_reader()).lines() {
                        let Ok(line) = line else { break };
                        if line == ": live" && tx.post(Message::CaughtUp).is_err() {
                            return;
                        }
                        if let Some(json) = line.strip_prefix("data:").map(str::trim) {
                            if let Ok(v) = serde_json::from_str::<serde_json::Value>(json) {
                                if let Some(seq) = v["seq"].as_u64() {
                                    after = after.max(seq);
                                }
                            }
                        }
                        match feed_line(&line, &mut sse, &mut rebuild, &tx) {
                            Ok(true) => {}
                            Ok(false) => return,
                            Err(()) => {
                                after = 0;
                                break;
                            }
                        }
                    }
                }
                Err(error) => {
                    if tx.post(Message::Status(format!("reconnecting: {error}"))).is_err() {
                        return;
                    }
                }
            }
            std::thread::sleep(Duration::from_millis(500));
        }
    }
}

pub struct Replay {
    pub path: String,
    pub speed: f64,
}

impl Replay {
    pub fn run(self, tx: impl Post<Message>) -> Result<()> {
        let file = std::fs::File::open(&self.path).with_context(|| format!("open {}", self.path))?;
        let lines: Vec<(f64, String)> = BufReader::new(file)
            .lines()
            .map_while(Result::ok)
            .filter_map(|l| {
                let (ts, rest) = l.split_once('\t')?;
                Some((ts.parse().ok()?, rest.to_string()))
            })
            .collect();
        let Some(first) = lines.first().map(|l| l.0) else { return Ok(()) };
        let start = Instant::now();
        let mut sse = Sse::default();
        let mut rebuild = Rebuild::default();
        let _ = tx.post(Message::Status("replay".into()));
        for (ts, line) in lines {
            if self.speed > 0.0 {
                let due = Duration::from_secs_f64(((ts - first) / self.speed).max(0.0));
                if let Some(wait) = due.checked_sub(start.elapsed()) {
                    std::thread::sleep(wait);
                }
            }
            if let Ok(false) = feed_line(&line, &mut sse, &mut rebuild, &tx) {
                return Ok(());
            }
        }
        let _ = tx.post(Message::Status("replay finished".into()));
        Ok(())
    }
}
