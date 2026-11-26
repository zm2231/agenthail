use serde::Deserialize;
use time::OffsetDateTime;

use crate::catalog::{Client, RespondError, ago};

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Since {
    #[serde(default)]
    pub at: String,
    #[serde(default)]
    pub fallback: bool,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Receipt {
    #[serde(default)]
    pub evidence: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Brief {
    #[serde(default)]
    pub r#ref: String,
    #[serde(default)]
    pub started: bool,
    #[serde(default)]
    pub since: Option<Since>,
    #[serde(default)]
    pub result: Option<Receipt>,
}

impl Brief {
    pub fn notice(&self, now: OffsetDateTime) -> String {
        let who = if self.r#ref.is_empty() { "@lead" } else { self.r#ref.as_str() };
        let first = match &self.since {
            Some(s) if s.fallback => format!("Asked {who} to catch you up on the last 24 hours."),
            Some(s) => match ago(&s.at, now).as_str() {
                "" => format!("Asked {who} to catch you up."),
                "now" => format!("Asked {who} to catch you up since just now."),
                a => format!("Asked {who} to catch you up since you wrote {a} ago."),
            },
            None => format!("Asked {who} to catch you up."),
        };
        if self.started {
            format!("{first} This starts its conversation.")
        } else if self.result.as_ref().is_some_and(|r| r.evidence == "queued") {
            format!("{first} It replies after its current turn.")
        } else {
            first
        }
    }
}

fn segment(value: &str) -> String {
    value.bytes().map(|b| if b.is_ascii_alphanumeric() || b"-._~".contains(&b) { (b as char).to_string() } else { format!("%{b:02X}") }).collect()
}

impl Client {
    pub fn project_brief(&self, project: &str, key: &str) -> Result<Brief, RespondError> {
        read(self.post_empty(&format!("/api/v1/projects/{}/brief", segment(project)), "brief", key)?)
    }
}

fn read(value: serde_json::Value) -> Result<Brief, RespondError> {
    let value = crate::catalog::confirmed(value)?;
    serde_json::from_value(value).map_err(|e| RespondError { message: format!("the daemon's reply couldn't be read: {e}"), code: "unreadable_reply".into(), maybe_delivered: true, answered: true, reply: serde_json::Value::Null })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn brief(json: &str) -> Brief {
        serde_json::from_str(json).unwrap()
    }

    #[test]
    fn the_notice_says_who_was_asked_from_when_and_when_it_replies() {
        let now = crate::catalog::parse_time("2026-11-24T12:00:00Z").unwrap();
        assert_eq!(brief(r#"{"ok":true,"ref":"@v2-lead","started":true,"since":{"at":"2026-11-23T12:00:00Z","fallback":true}}"#).notice(now), "Asked @v2-lead to catch you up on the last 24 hours. This starts its conversation.");
        assert_eq!(brief(r#"{"ok":true,"ref":"@lead","since":{"at":"2026-11-24T09:00:00Z"},"result":{"evidence":"queued"}}"#).notice(now), "Asked @lead to catch you up since you wrote 3h ago. It replies after its current turn.");
        assert_eq!(brief(r#"{"ok":true,"result":{"evidence":"delivered"}}"#).notice(now), "Asked @lead to catch you up.");
    }

    #[test]
    fn a_brief_still_being_handled_is_not_reported_as_asked() {
        let e = read(serde_json::json!({"ok": true, "status": "submitted"})).unwrap_err();
        assert!(e.maybe_delivered);
        assert_eq!(e.code, "submitted");
        assert_eq!(read(serde_json::json!({"ok": true, "ref": "@lead"})).unwrap().r#ref, "@lead");
    }

    #[test]
    fn a_brief_posts_no_body_to_the_projects_brief_route() {
        use std::io::{BufRead, BufReader, Write};
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let (tx, rx) = std::sync::mpsc::channel();
        std::thread::spawn(move || {
            let (stream, _) = listener.accept().unwrap();
            let mut reader = BufReader::new(stream.try_clone().unwrap());
            let mut line = String::new();
            reader.read_line(&mut line).unwrap();
            let mut length = String::from("0");
            let mut key = String::new();
            loop {
                let mut h = String::new();
                reader.read_line(&mut h).unwrap();
                let h = h.trim().to_string();
                if h.is_empty() {
                    break;
                }
                let (name, value) = h.split_once(':').unwrap();
                match name.to_ascii_lowercase().as_str() {
                    "content-length" => length = value.trim().into(),
                    "idempotency-key" => key = value.trim().into(),
                    _ => {}
                }
            }
            tx.send((line.trim().to_string(), length, key)).unwrap();
            let body = r#"{"ok":true,"ref":"@lead","started":false,"result":{"evidence":"queued"}}"#;
            let mut stream = stream;
            write!(stream, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len()).unwrap();
        });
        let b = Client::new(&base, "t").project_brief("h1/p 1", "k1").unwrap();
        let (line, length, key) = rx.recv().unwrap();
        assert_eq!(line, "POST /api/v1/projects/h1%2Fp%201/brief HTTP/1.1");
        assert_eq!(length, "0");
        assert_eq!(key, "k1");
        assert_eq!(b.result.unwrap().evidence, "queued");
    }
}
