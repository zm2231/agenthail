use serde::Deserialize;

use crate::action::Dismiss;
use crate::catalog::{Client, OpenRequest, Pr, RespondError, enc};

pub const GROUPS: [(&str, &str); 4] = [("stopped", "Stopped on you"), ("holding", "Holding up work"), ("waiting", "Waiting on you"), ("review", "Waiting for your review")];
pub const HISTORY_KEPT: usize = 20;
const HOST_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(6);

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
    #[serde(default)]
    pub ledger_key: String,
    #[serde(default)]
    pub phase: String,
    #[serde(default)]
    pub proof: Option<ProofCounts>,
    #[serde(default)]
    pub prs: Vec<Pr>,
    #[serde(skip)]
    pub host: String,
    #[serde(skip)]
    pub host_name: String,
    #[serde(skip)]
    pub covers: Vec<Decision>,
}

#[derive(Deserialize, Clone, Copy, Debug, Default, PartialEq)]
pub struct ProofCounts {
    #[serde(default)]
    pub pass: usize,
    #[serde(default)]
    pub fail: usize,
    #[serde(default)]
    pub pending: usize,
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
    pub read: Vec<String>,
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
        match (self.session_id.is_empty(), self.work_item.is_empty()) {
            (_, true) => self.session_id.clone(),
            (true, false) => format!("{}#{}", self.host, self.work_item),
            (false, false) => format!("{}#{}", self.session_id, self.work_item),
        }
    }

    pub fn hold_key(&self) -> String {
        if self.is_review() {
            return self.review_hold();
        }
        match self.requests.first() {
            Some(r) => request_hold(&self.session_id, &r.id),
            None if self.is_parked() => parked_hold(&self.host, &self.work_item),
            None => self.state_hold(),
        }
    }

    fn review_hold(&self) -> String {
        format!("review|{}|{}|{}|{}", self.host, self.key, self.work_item, self.since)
    }

    fn state_hold(&self) -> String {
        format!("state|{}|{}|{}|{}", self.session_id, self.kind, self.reason, self.since)
    }

    pub fn holds(&self, key: &str) -> bool {
        self.state_hold() == key
            || self.is_review() && self.review_hold() == key
            || self.requests.iter().any(|r| request_hold(&self.session_id, &r.id) == key)
            || std::iter::once(self).chain(&self.covers).any(|c| !c.work_item.is_empty() && parked_hold(&c.host, &c.work_item) == key)
    }

    pub fn is_parked(&self) -> bool {
        self.kind == "parked" || !self.work_item.is_empty() && !self.is_review()
    }

    pub fn is_review(&self) -> bool {
        self.group == "review" || self.kind == "review" || self.key.starts_with("review:")
    }

    pub fn proof_line(&self) -> Option<String> {
        let p = self.proof?;
        let parts: Vec<String> = [(p.pass, "passed"), (p.fail, "failed"), (p.pending, "pending")].iter().filter(|(n, _)| *n > 0).map(|(n, w)| format!("{n} {w}")).collect();
        (!parts.is_empty()).then(|| parts.join(" · "))
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

    pub fn recommended_lines(&self) -> Vec<(String, String)> {
        if self.recommended().is_none() {
            return Vec::new();
        }
        let questions = self.requests.first().and_then(|r| r.questions.as_ref()).cloned().unwrap_or_default();
        questions.iter().filter_map(|q| q.suggested.and_then(|i| q.options.get(i)).map(|o| (q.question.clone(), o.label.clone()))).collect()
    }

    pub fn dismissals(&self) -> Vec<Dismiss<'_>> {
        if self.is_parked() {
            if self.covers.is_empty() {
                return vec![Dismiss::Parked { work_item: &self.work_item, host: &self.host }];
            }
            return self.covers.iter().map(|c| Dismiss::Parked { work_item: &c.work_item, host: &c.host }).collect();
        }
        if self.answerable && !self.requests.is_empty() {
            return self.requests.iter().map(|r| Dismiss::Request { session: &self.session_id, request: &r.id }).collect();
        }
        vec![Dismiss::Stopped { session: &self.session_id }]
    }

    pub fn hold_of(&self, target: &Dismiss) -> String {
        match target {
            Dismiss::Request { session, request } => request_hold(session, request),
            Dismiss::Parked { work_item, host } => parked_hold(host, work_item),
            Dismiss::Stopped { .. } => self.state_hold(),
        }
    }

    pub fn open_dismissals(&self, held: &std::collections::HashSet<String>) -> Vec<Dismiss<'_>> {
        self.dismissals().into_iter().filter(|t| !held.contains(&self.hold_of(t))).collect()
    }

    pub fn not_now_label(&self, held: &std::collections::HashSet<String>) -> String {
        match self.open_dismissals(held).len() {
            n if n > 1 => format!("Not now for all {n}"),
            _ => "Not now".into(),
        }
    }
}

fn unread(name: &str, error: &anyhow::Error) -> String {
    if error.to_string().contains("404") {
        format!("{name} runs an older Agenthail without the Inbox.")
    } else {
        format!("{name}'s decisions couldn't be read, so they aren't shown.")
    }
}

pub fn parked_hold(host: &str, work_item: &str) -> String {
    format!("parked|{host}|{work_item}")
}

pub fn request_hold(session: &str, request: &str) -> String {
    format!("{session}|{request}")
}

pub fn gone(error: &RespondError) -> bool {
    error.code == "stale" || error.code == "not_found" || error.message.contains("404")
}

pub fn qualify(id: &str, host: &str, own: &str) -> String {
    let id = if host.is_empty() || id.is_empty() || id.contains("::") { id.to_string() } else { format!("{host}::{id}") };
    let mine = format!("{own}::");
    match id.strip_prefix(mine.as_str()) {
        Some(local) if !own.is_empty() => local.to_string(),
        _ => id,
    }
}

fn owner(id: &str) -> &str {
    id.split_once("::").map(|(h, _)| h).unwrap_or_default()
}

impl Inbox {
    pub fn add(&mut self, page: Page, host: &str, host_name: &str, own: &str) {
        let q = |id: &str| qualify(id, host, own);
        self.read.push(host.to_string());
        for d in page.decisions {
            let mut d = Decision { host: host.into(), host_name: host_name.into(), session_id: q(&d.session_id), project_id: q(&d.project_id), ..d };
            d.holds_up = d.holds_up.iter().map(|x| q(x)).collect();
            d.reason = d.reason_text();
            self.decisions.push(d);
        }
        for h in page.lead_handling {
            self.lead_handling.push(LeadHandled { session_id: q(&h.session_id), project_id: q(&h.project_id), held_by: q(&h.held_by), ..h });
        }
    }

    pub fn settle(&mut self) {
        let mut all: Vec<Decision> = std::mem::take(&mut self.decisions).into_iter().flat_map(|d| if d.covers.is_empty() { vec![d] } else { d.covers }).collect();
        order(&mut all);
        let mut kept: Vec<Decision> = Vec::new();
        for d in all {
            match kept.iter().position(|k| k.id() == d.id()) {
                Some(i) if d.host == owner(&d.session_id) && kept[i].host != owner(&kept[i].session_id) => kept[i] = d,
                Some(_) => {}
                None => kept.push(d),
            }
        }
        let mut out: Vec<Decision> = Vec::new();
        for d in kept {
            match out.iter_mut().find(|o| d.is_parked() && o.is_parked() && !d.session_id.is_empty() && o.session_id == d.session_id && o.reason == d.reason) {
                Some(lead) => {
                    if lead.covers.is_empty() {
                        lead.covers.push(lead.clone());
                    }
                    lead.covers.push(d);
                }
                None => out.push(d),
            }
        }
        self.decisions = out;
    }
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

    pub fn inbox(&self) -> Inbox {
        let quick = self.timed(HOST_TIMEOUT);
        let (local, hosts) = std::thread::scope(|scope| {
            let local = scope.spawn(|| quick.decisions(None));
            let hosts = quick.get_json("/api/v1/hosts");
            (local.join().unwrap_or_else(|_| Err(anyhow::anyhow!("the fetch stopped"))), hosts)
        });
        let mut inbox = Inbox::default();
        let listed = hosts.is_ok();
        let hosts = hosts.unwrap_or_default();
        let own = hosts["host"]["id"].as_str().unwrap_or_default().to_string();
        match local {
            Ok(page) => inbox.add(page, "", "", &own),
            Err(e) => inbox.notes.push(unread("This computer", &e)),
        }
        if !listed {
            inbox.notes.push("Couldn't list the paired computers, so their decisions aren't shown.".into());
        }
        let mut paired: Vec<(String, String)> = Vec::new();
        for h in hosts["hosts"].as_array().into_iter().flatten() {
            let (Some(id), name) = (h["id"].as_str(), h["name"].as_str().unwrap_or_default().to_string()) else { continue };
            match h["state"].as_str().unwrap_or_default() {
                "connected" => paired.push((id.to_string(), name)),
                "incompatible" => inbox.notes.push(format!("{name} runs an older Agenthail without the Inbox.")),
                _ => inbox.notes.push(format!("{name} isn't answering, so its decisions aren't shown.")),
            }
        }
        let pages: Vec<(String, String, anyhow::Result<Page>)> = std::thread::scope(|scope| {
            let jobs: Vec<_> = paired.iter().map(|(id, name)| (id.clone(), name.clone(), scope.spawn(|| quick.decisions(Some(id))))).collect();
            jobs.into_iter().map(|(id, name, job)| (id, name, job.join().unwrap_or_else(|_| Err(anyhow::anyhow!("the fetch stopped"))))).collect()
        });
        for (id, name, page) in pages {
            match page {
                Ok(page) => inbox.add(page, &id, &name, &own),
                Err(e) => inbox.notes.push(unread(&name, &e)),
            }
        }
        inbox.settle();
        inbox
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
        assert_eq!(d.recommended_lines(), [("Colour?".to_string(), "Blue".to_string()), ("Size?".to_string(), "Small".to_string())]);
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
        assert_eq!(asks.not_now_label(&Default::default()), "Not now for all 2");
        let next = decision(r#"{"sessionId":"s","answerable":true,"requests":[{"id":"b"}]}"#);
        assert_eq!(asks.id(), next.id());
        assert_ne!(asks.hold_key(), next.hold_key());
        let grown = decision(r#"{"sessionId":"s","answerable":true,"requests":[{"id":"a"},{"id":"c"}]}"#);
        assert_eq!(asks.hold_key(), grown.hold_key());
        assert!(grown.holds(&asks.hold_key()));
        assert!(!next.holds(&asks.hold_key()));
        let held: std::collections::HashSet<String> = [request_hold("s", "b")].into();
        assert_eq!(asks.open_dismissals(&held), [Dismiss::Request { session: "s", request: "a" }]);
        let stopped = decision(r#"{"group":"stopped","sessionId":"s"}"#);
        assert_eq!(stopped.dismissals(), [Dismiss::Stopped { session: "s" }]);
        assert_eq!(stopped.not_now_label(&Default::default()), "Not now");
        let held: std::collections::HashSet<String> = [stopped.hold_key()].into();
        assert!(stopped.open_dismissals(&held).is_empty());
        let later = decision(r#"{"group":"stopped","sessionId":"s","kind":"stopped","reason":"stopped again","since":"2026-11-24T11:00:00Z"}"#);
        assert_eq!(later.id(), stopped.id());
        assert!(!later.holds(&stopped.hold_key()));
        assert_eq!(later.open_dismissals(&held).len(), 1);
        let a = Decision { kind: "parked".into(), session_id: "s".into(), work_item: "p-A".into(), host: "h2".into(), reason: "pick".into(), ..Default::default() };
        let b = Decision { work_item: "p-B".into(), ..a.clone() };
        let group = Decision { covers: vec![a.clone(), b.clone()], ..a.clone() };
        let doubtful = group.hold_of(&group.dismissals()[1]);
        assert_eq!(doubtful, parked_hold("h2", "p-B"));
        let held: std::collections::HashSet<String> = [doubtful.clone()].into();
        assert_eq!(group.open_dismissals(&held), [Dismiss::Parked { work_item: "p-A", host: "h2" }]);
        assert!(b.holds(&doubtful) && group.holds(&doubtful) && !a.holds(&doubtful));
        assert!(b.open_dismissals(&held).is_empty());
    }

    #[test]
    fn a_review_item_is_shown_but_never_answered_here() {
        let d = decision(r#"{"group":"review","key":"review:p:p-W3","sessionId":"s","answerable":true,"requests":[{"id":"r","questions":[{"question":"Q","suggested":0,"options":[{"id":"a","label":"A"}]}]}]}"#);
        assert!(d.is_review());
        assert_eq!(d.group_title(), "Waiting for your review");
        assert_eq!(d.recommended(), None);
        assert_eq!(d.id(), "review:p:p-W3");
        assert!(decision(r#"{"group":"waiting","key":"review:p:p-W4"}"#).is_review());
        let other = decision(r#"{"group":"review","key":"review:p:p-W5","sessionId":"s","workItem":"p-W5"}"#);
        let mine = decision(r#"{"group":"review","key":"review:p:p-W3","sessionId":"s","workItem":"p-W3"}"#);
        assert_ne!(mine.hold_key(), other.hold_key());
        assert!(mine.holds(&mine.hold_key()) && !other.holds(&mine.hold_key()));
        let elsewhere = Decision { host: "h2".into(), ..mine.clone() };
        assert!(!elsewhere.holds(&mine.hold_key()));
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

    fn serve(routes: Vec<(&'static str, u16, &'static str)>) -> String {
        use std::io::{BufRead, BufReader, Write};
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        std::thread::spawn(move || {
            for stream in listener.incoming() {
                let stream = stream.unwrap();
                let routes = routes.clone();
                std::thread::spawn(move || {
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
                let (status, body) = routes.iter().find(|r| r.0 == path).map(|r| (r.1, r.2)).unwrap_or((404, r#"{"error":{"code":"not_found","message":"no"}}"#));
                if path.ends_with("host=slow") {
                    std::thread::sleep(std::time::Duration::from_secs(9));
                }
                let mut stream = stream;
                let _ = write!(stream, "HTTP/1.1 {status} X\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len());
                });
            }
        });
        base
    }

    #[test]
    fn the_inbox_reads_this_computer_and_each_connected_one_by_its_own_ids() {
        let base = serve(vec![
            ("/api/v1/decisions", 200, r#"{"decisions":[{"group":"waiting","sessionId":"local","since":"2026-11-24T10:00:00Z"}],"leadHandling":[]}"#),
            ("/api/v1/hosts", 200, r#"{"host":{"id":"h1","name":"studio"},"hosts":[{"id":"h2","name":"forge","state":"connected"},{"id":"h3","name":"old","state":"connected"},{"id":"h4","name":"off","state":"unreachable"},{"id":"h5","name":"legacy","state":"incompatible"}]}"#),
            (
                "/api/v1/decisions?host=h2",
                200,
                r#"{"decisions":[
                    {"group":"stopped","kind":"parked","sessionId":"remote","projectId":"p","workItem":"p-W2","reason":"p-W2 waits on you: pick a bucket","holdsUp":["w"]},
                    {"group":"stopped","kind":"parked","sessionId":"remote","projectId":"p","workItem":"p-W3","reason":"p-W3 waits on you: pick a bucket"},
                    {"group":"waiting","sessionId":"h1::local","since":"2026-11-24T10:00:00Z"}],
                  "leadHandling":[{"sessionId":"crew","heldBy":"lead","reason":"run tests"}]}"#,
            ),
        ]);
        let inbox = Client::new(&base, "t").inbox();
        let rows: Vec<(&str, &str, &str)> = inbox.decisions.iter().map(|d| (d.session_id.as_str(), d.project_id.as_str(), d.host.as_str())).collect();
        assert_eq!(rows, [("h2::remote", "h2::p", "h2"), ("local", "", "")]);
        assert_eq!(inbox.decisions[0].holds_up, ["h2::w"]);
        assert_eq!(inbox.decisions[0].reason, "pick a bucket");
        assert_eq!(inbox.decisions[0].dismissals(), [Dismiss::Parked { work_item: "p-W2", host: "h2" }, Dismiss::Parked { work_item: "p-W3", host: "h2" }]);
        assert_eq!(inbox.decisions[0].not_now_label(&Default::default()), "Not now for all 2");
        assert_eq!(inbox.lead_handling[0].session_id, "h2::crew");
        assert_eq!(inbox.lead_handling[0].held_by, "h2::lead");
        assert_eq!(inbox.notes, ["off isn't answering, so its decisions aren't shown.", "legacy runs an older Agenthail without the Inbox.", "old runs an older Agenthail without the Inbox."]);
    }

    #[test]
    fn a_missing_computer_list_is_said_not_hidden() {
        let base = serve(vec![("/api/v1/decisions", 200, r#"{"decisions":[],"leadHandling":[]}"#), ("/api/v1/hosts", 500, r#"{"error":{"code":"internal_error","message":"boom"}}"#)]);
        let inbox = Client::new(&base, "t").inbox();
        assert!(inbox.decisions.is_empty());
        assert_eq!(inbox.notes, ["Couldn't list the paired computers, so their decisions aren't shown."]);
    }

    #[test]
    fn parked_items_without_a_session_stay_apart_across_computers() {
        let mut inbox = Inbox::default();
        let page = |json: &str| serde_json::from_str::<Page>(json).unwrap();
        inbox.add(page(r#"{"decisions":[{"group":"stopped","kind":"parked","sessionId":"","workItem":"p-W1","reason":"pick"}]}"#), "", "", "h1");
        inbox.add(page(r#"{"decisions":[{"group":"stopped","kind":"parked","sessionId":"","workItem":"p-W1","reason":"pick"}]}"#), "h2", "forge", "h1");
        inbox.settle();
        let hosts: Vec<&str> = inbox.decisions.iter().map(|d| d.host.as_str()).collect();
        assert_eq!(hosts, ["", "h2"]);
    }

    #[test]
    fn other_computers_still_show_when_this_one_fails_or_one_stalls() {
        let base = serve(vec![
            ("/api/v1/decisions", 500, r#"{"error":{"code":"internal_error","message":"boom"}}"#),
            ("/api/v1/hosts", 200, r#"{"host":{"id":"h1","name":"studio"},"hosts":[{"id":"h2","name":"forge","state":"connected"},{"id":"slow","name":"stuck","state":"connected"}]}"#),
            ("/api/v1/decisions?host=h2", 200, r#"{"decisions":[{"group":"waiting","sessionId":"remote"}],"leadHandling":[]}"#),
            ("/api/v1/decisions?host=slow", 200, r#"{"decisions":[{"group":"waiting","sessionId":"late"}],"leadHandling":[]}"#),
        ]);
        let started = std::time::Instant::now();
        let inbox = Client::new(&base, "t").inbox();
        assert!(started.elapsed() < std::time::Duration::from_secs(8));
        let ids: Vec<&str> = inbox.decisions.iter().map(|d| d.session_id.as_str()).collect();
        assert_eq!(ids, ["h2::remote"]);
        assert_eq!(inbox.notes, ["This computer's decisions couldn't be read, so they aren't shown.", "stuck's decisions couldn't be read, so they aren't shown."]);
    }

    #[test]
    fn ids_from_another_computer_carry_its_name_unless_they_are_ours() {
        assert_eq!(qualify("s", "h2", "h1"), "h2::s");
        assert_eq!(qualify("h1::s", "h2", "h1"), "s");
        assert_eq!(qualify("h3::s", "h2", "h1"), "h3::s");
        assert_eq!(qualify("s", "", "h1"), "s");
        assert_eq!(qualify("", "h2", "h1"), "");
    }
}
