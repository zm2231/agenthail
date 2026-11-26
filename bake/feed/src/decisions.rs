use serde::Deserialize;

use crate::action::Dismiss;
use crate::catalog::{Client, OpenRequest, RespondError, enc};

pub const GROUPS: [(&str, &str); 4] = [("stopped", "Stopped on you"), ("holding", "Holding up work"), ("waiting", "Waiting on you"), ("review", "Waiting for your review")];
pub const HISTORY_KEPT: usize = 20;

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Decision {
    #[serde(default)]
    pub group: String,
    #[serde(default)]
    pub key: String,
    #[serde(default)]
    pub session_id: String,
    #[serde(default)]
    pub project_id: String,
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub reason: String,
    #[serde(default)]
    pub since: String,
    #[serde(default)]
    pub answerable: bool,
    #[serde(default)]
    pub requests: Vec<OpenRequest>,
    #[serde(default)]
    pub request_error: String,
    #[serde(default)]
    pub holds_up: Vec<String>,
    #[serde(default)]
    pub work_item: String,
    #[serde(default)]
    pub work_title: String,
    #[serde(skip)]
    pub host: String,
    #[serde(skip)]
    pub host_name: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct LeadHandled {
    #[serde(default)]
    pub session_id: String,
    #[serde(default)]
    pub project_id: String,
    #[serde(default)]
    pub held_by: String,
    #[serde(default)]
    pub role: String,
    #[serde(default)]
    pub reason: String,
    #[serde(default)]
    pub since: String,
}

#[derive(Deserialize, Clone, Debug, Default)]
#[serde(rename_all = "camelCase")]
pub struct Page {
    #[serde(default)]
    pub decisions: Vec<Decision>,
    #[serde(default)]
    pub lead_handling: Vec<LeadHandled>,
}

#[derive(Clone, Debug, Default)]
pub struct Inbox {
    pub decisions: Vec<Decision>,
    pub lead_handling: Vec<LeadHandled>,
    pub notes: Vec<String>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Handled {
    pub title: String,
    pub session: String,
    pub outcome: String,
    pub at: String,
}

impl Decision {
    pub fn id(&self) -> String {
        if !self.key.is_empty() {
            return self.key.clone();
        }
        format!("{}/{}/{}/{}", self.host, self.session_id, self.work_item, self.requests.first().map(|r| r.id.as_str()).unwrap_or_default())
    }

    pub fn is_review(&self) -> bool {
        self.group == "review" || self.kind == "review" || self.key.starts_with("review:")
    }

    pub fn group_title(&self) -> &'static str {
        GROUPS.iter().find(|(g, _)| *g == self.group).map(|(_, t)| *t).unwrap_or(if self.is_review() { GROUPS[3].1 } else { GROUPS[0].1 })
    }

    pub fn rank(&self) -> usize {
        GROUPS.iter().position(|(g, _)| *g == self.group).unwrap_or(if self.is_review() { 3 } else { 0 })
    }

    pub fn reason_text(&self) -> String {
        let prefix = format!("{} waits on you: ", self.work_item);
        self.reason.strip_prefix(prefix.as_str()).unwrap_or(&self.reason).to_string()
    }

    pub fn recommended(&self) -> Option<(String, Vec<String>)> {
        if self.is_review() || !self.answerable {
            return None;
        }
        let request = self.requests.first()?;
        let questions = request.questions.as_ref().filter(|q| !q.is_empty())?;
        let choices = questions.iter().map(|q| q.suggested.and_then(|i| q.options.get(i)).map(|o| o.id.clone())).collect::<Option<Vec<String>>>()?;
        Some((request.id.clone(), choices))
    }

    pub fn dismissals(&self) -> Vec<Dismiss<'_>> {
        if !self.work_item.is_empty() {
            return vec![Dismiss::Parked { work_item: &self.work_item, host: &self.host }];
        }
        if self.answerable && !self.requests.is_empty() {
            return self.requests.iter().map(|r| Dismiss::Request { session: &self.session_id, request: &r.id }).collect();
        }
        vec![Dismiss::Stopped { session: &self.session_id }]
    }

    pub fn not_now_label(&self) -> String {
        match self.dismissals().len() {
            n if n > 1 => format!("Not now for all {n}"),
            _ => "Not now".into(),
        }
    }
}

pub fn gone(error: &RespondError) -> bool {
    error.code == "stale" || error.code == "not_found" || error.message.contains("404")
}

pub fn order(decisions: &mut [Decision]) {
    decisions.sort_by(|a, b| a.rank().cmp(&b.rank()).then(a.since.cmp(&b.since)).then(a.session_id.cmp(&b.session_id)).then(a.work_item.cmp(&b.work_item)));
}

pub fn remember(history: &mut Vec<Handled>, entry: Handled) {
    history.insert(0, entry);
    history.truncate(HISTORY_KEPT);
}

impl Client {
    pub fn decisions(&self, host: Option<&str>) -> anyhow::Result<Page> {
        let path = match host {
            Some(h) => format!("/api/v1/decisions?host={}", enc(h)),
            None => "/api/v1/decisions".into(),
        };
        Ok(serde_json::from_value(self.get_json(&path)?)?)
    }

    pub fn inbox(&self) -> anyhow::Result<Inbox> {
        let local = self.decisions(None)?;
        let mut inbox = Inbox { decisions: local.decisions, lead_handling: local.lead_handling, notes: Vec::new() };
        let hosts = self.get_json("/api/v1/hosts").unwrap_or_default();
        let paired: Vec<(String, String)> = hosts["hosts"]
            .as_array()
            .into_iter()
            .flatten()
            .filter(|h| h["state"] == "connected")
            .filter_map(|h| Some((h["id"].as_str()?.to_string(), h["name"].as_str().unwrap_or_default().to_string())))
            .collect();
        let pages: Vec<(String, String, anyhow::Result<Page>)> = std::thread::scope(|scope| {
            let jobs: Vec<_> = paired.iter().map(|(id, name)| (id.clone(), name.clone(), scope.spawn(|| self.decisions(Some(id))))).collect();
            jobs.into_iter().map(|(id, name, job)| (id, name, job.join().unwrap_or_else(|_| Err(anyhow::anyhow!("the fetch stopped"))))).collect()
        });
        for (id, name, page) in pages {
            match page {
                Ok(page) => {
                    inbox.decisions.extend(page.decisions.into_iter().map(|d| Decision { host: id.clone(), host_name: name.clone(), ..d }));
                    inbox.lead_handling.extend(page.lead_handling);
                }
                Err(e) if e.to_string().contains("404") => inbox.notes.push(format!("{name} runs an older Agenthail without the Inbox.")),
                Err(_) => inbox.notes.push(format!("{name} isn't answering, so its decisions aren't shown.")),
            }
        }
        order(&mut inbox.decisions);
        Ok(inbox)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn decision(json: &str) -> Decision {
        serde_json::from_str(json).unwrap()
    }

    #[test]
    fn accept_recommended_needs_a_suggestion_for_every_question() {
        let d = decision(r#"{"group":"waiting","sessionId":"s","answerable":true,"requests":[{"id":"r1","questions":[
            {"question":"Colour?","suggested":1,"options":[{"id":"red","label":"Red"},{"id":"blue","label":"Blue"}]},
            {"question":"Size?","suggested":0,"options":[{"id":"s","label":"Small"}]}]}]}"#);
        assert_eq!(d.recommended(), Some(("r1".into(), vec!["blue".into(), "s".into()])));
        let partial = decision(r#"{"group":"waiting","sessionId":"s","answerable":true,"requests":[{"id":"r1","questions":[
            {"question":"Colour?","suggested":1,"options":[{"id":"red","label":"Red"},{"id":"blue","label":"Blue"}]},
            {"question":"Size?","options":[{"id":"s","label":"Small"}]}]}]}"#);
        assert_eq!(partial.recommended(), None);
        let unanswerable = decision(r#"{"group":"stopped","sessionId":"s","requests":[{"id":"r1","questions":[{"question":"Q","suggested":0,"options":[{"id":"a","label":"A"}]}]}]}"#);
        assert_eq!(unanswerable.recommended(), None);
    }

    #[test]
    fn not_now_dismisses_what_the_item_is() {
        let parked = Decision { work_item: "p-W1".into(), session_id: "s".into(), host: "h2".into(), ..Default::default() };
        assert_eq!(parked.dismissals(), [Dismiss::Parked { work_item: "p-W1", host: "h2" }]);
        let asks = decision(r#"{"sessionId":"s","answerable":true,"requests":[{"id":"a"},{"id":"b"}]}"#);
        assert_eq!(asks.dismissals(), [Dismiss::Request { session: "s", request: "a" }, Dismiss::Request { session: "s", request: "b" }]);
        assert_eq!(asks.not_now_label(), "Not now for all 2");
        let stopped = decision(r#"{"group":"stopped","sessionId":"s"}"#);
        assert_eq!(stopped.dismissals(), [Dismiss::Stopped { session: "s" }]);
        assert_eq!(stopped.not_now_label(), "Not now");
    }

    #[test]
    fn a_review_item_is_shown_but_never_answered_here() {
        let d = decision(r#"{"group":"review","key":"review:p:p-W3","sessionId":"s","answerable":true,"requests":[{"id":"r","questions":[{"question":"Q","suggested":0,"options":[{"id":"a","label":"A"}]}]}]}"#);
        assert!(d.is_review());
        assert_eq!(d.group_title(), "Waiting for your review");
        assert_eq!(d.recommended(), None);
        assert_eq!(d.id(), "review:p:p-W3");
        assert!(decision(r#"{"group":"waiting","key":"review:p:p-W4"}"#).is_review());
    }

    #[test]
    fn decisions_are_grouped_then_oldest_first_and_reasons_lose_the_item_prefix() {
        let mut list = vec![
            decision(r#"{"group":"waiting","sessionId":"b","since":"2026-11-24T10:00:00Z"}"#),
            decision(r#"{"group":"review","sessionId":"r","since":"2026-11-24T08:00:00Z"}"#),
            decision(r#"{"group":"waiting","sessionId":"a","since":"2026-11-24T09:00:00Z"}"#),
            decision(r#"{"group":"stopped","sessionId":"c","since":"2026-11-24T11:00:00Z"}"#),
        ];
        order(&mut list);
        let ids: Vec<&str> = list.iter().map(|d| d.session_id.as_str()).collect();
        assert_eq!(ids, ["c", "a", "b", "r"]);
        let d = decision(r#"{"workItem":"p-W1","reason":"p-W1 waits on you: pick a name"}"#);
        assert_eq!(d.reason_text(), "pick a name");
    }

    #[test]
    fn history_keeps_the_newest_first() {
        let mut h = Vec::new();
        for i in 0..HISTORY_KEPT + 3 {
            remember(&mut h, Handled { title: format!("t{i}"), session: String::new(), outcome: "Not now".into(), at: String::new() });
        }
        assert_eq!(h.len(), HISTORY_KEPT);
        assert_eq!(h[0].title, format!("t{}", HISTORY_KEPT + 2));
    }

    #[test]
    fn the_inbox_reads_this_computer_and_each_connected_one() {
        use std::io::{BufRead, BufReader, Write};
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        std::thread::spawn(move || {
            for _ in 0..4 {
                let (stream, _) = listener.accept().unwrap();
                let mut reader = BufReader::new(stream.try_clone().unwrap());
                let mut line = String::new();
                reader.read_line(&mut line).unwrap();
                loop {
                    let mut h = String::new();
                    reader.read_line(&mut h).unwrap();
                    if h.trim().is_empty() {
                        break;
                    }
                }
                let path = line.split_whitespace().nth(1).unwrap_or_default().to_string();
                let (status, body) = match path.as_str() {
                    "/api/v1/decisions" => (200, r#"{"decisions":[{"group":"waiting","sessionId":"local","since":"2026-11-24T10:00:00Z"}],"leadHandling":[]}"#.to_string()),
                    "/api/v1/hosts" => (200, r#"{"hosts":[{"id":"h2","name":"forge","state":"connected"},{"id":"h3","name":"old","state":"connected"},{"id":"h4","name":"off","state":"unreachable"}]}"#.to_string()),
                    "/api/v1/decisions?host=h2" => (200, r#"{"decisions":[{"group":"stopped","sessionId":"remote","workItem":"p-W2"}],"leadHandling":[]}"#.to_string()),
                    _ => (404, r#"{"error":{"code":"not_found","message":"no"}}"#.to_string()),
                };
                let mut stream = stream;
                write!(stream, "HTTP/1.1 {status} X\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len()).unwrap();
            }
        });
        let inbox = Client::new(&base, "t").inbox().unwrap();
        let rows: Vec<(&str, &str)> = inbox.decisions.iter().map(|d| (d.session_id.as_str(), d.host.as_str())).collect();
        assert_eq!(rows, [("remote", "h2"), ("local", "")]);
        assert_eq!(inbox.decisions[0].dismissals(), [Dismiss::Parked { work_item: "p-W2", host: "h2" }]);
        assert_eq!(inbox.notes, ["old runs an older Agenthail without the Inbox."]);
    }
}
