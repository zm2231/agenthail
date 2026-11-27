use std::time::Duration;

use serde::Deserialize;
use serde_json::Value;

use crate::catalog::{Client, enc};
use crate::derive::text::{plain, short_id};
use crate::derive::world::{Work, World};
use crate::search::{failure, one_line};

pub const LIMIT: usize = 20;
const TIMEOUT: Duration = Duration::from_secs(10);

pub fn work_query(text: &str) -> Option<String> {
    let q = text.trim();
    (q.chars().count() >= 2).then(|| q.to_string())
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Found {
    #[serde(default)]
    pub project_key: String,
    #[serde(default)]
    pub project_name: String,
    #[serde(default)]
    pub host: Option<String>,
    pub id: String,
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub snippet: Option<String>,
}

#[derive(Clone, Debug, Default)]
pub struct Search {
    pub items: Vec<Found>,
    pub truncated: bool,
}

impl Search {
    pub fn note(&self) -> Option<String> {
        self.truncated.then(|| format!("Showing the first {} work matches across every project. Type more to narrow them.", self.items.len()))
    }
}

pub fn read(value: &Value) -> Result<Search, String> {
    let rows = value["items"].as_array().ok_or("Work search returned no items list")?;
    let items = rows.iter().map(|r| serde_json::from_value(r.clone()).map_err(|e| format!("Work search returned an item Agenthail can't read: {e}"))).collect::<Result<_, _>>()?;
    Ok(Search { items, truncated: value["truncated"].as_bool().unwrap_or(false) })
}

impl Client {
    pub fn search_work(&self, query: &str) -> Result<Search, String> {
        let path = format!("/api/v1/ledger/search?q={}&limit={LIMIT}", enc(query));
        match self.timed(TIMEOUT).get_reply(&path) {
            Ok(value) => read(&value),
            Err((code, body)) => Err(failure("work search", code, &body)),
        }
    }

    pub fn found_work(&self, ledger: &str, id: &str) -> Result<Work, String> {
        let path = format!("/api/v1/ledger?projectKey={}&view=item&id={}", enc(ledger), enc(id));
        match self.timed(TIMEOUT).get_reply(&path) {
            Ok(value) => serde_json::from_value(value["item"]["item"].clone()).map_err(|e| format!("This work item came back in a form Agenthail can't read: {e}")),
            Err((code, body)) => Err(failure("this work item", code, &body)),
        }
    }
}

pub struct Row {
    pub title: String,
    pub status: String,
    pub badge: String,
    pub avatar: String,
    pub ledger: String,
    pub id: String,
}

fn state_word(state: &str) -> String {
    match state {
        "open" => "Open".into(),
        "in_progress" => "In progress".into(),
        "in_review" => "In review".into(),
        "done" => "Done".into(),
        "closed" => "Closed".into(),
        "parked" => "Parked".into(),
        "blocked" => "Blocked".into(),
        other => crate::derive::text::capital(&other.replace('_', " ")),
    }
}

impl World {
    pub fn found_rows(&self, search: &Search, listed: impl Fn(&str) -> bool) -> Vec<Row> {
        search
            .items
            .iter()
            .filter(|f| !listed(&f.id))
            .map(|f| {
                let mut status = vec![state_word(&f.state)];
                if !f.project_name.is_empty() {
                    status.push(f.project_name.clone());
                }
                if let Some(h) = f.host.as_deref().filter(|h| !h.is_empty()) {
                    let name = self.hosts.iter().find(|x| x.id == h).map(|x| x.name.as_str()).filter(|n| !n.is_empty()).unwrap_or(h);
                    status.push(format!("on {name}"));
                }
                if let Some(s) = f.snippet.as_deref().map(one_line).filter(|s| !s.is_empty()) {
                    status.push(s);
                }
                Row { title: plain(&f.title), status: status.join(" · "), badge: short_id(&f.id), avatar: f.project_name.clone(), ledger: f.project_key.clone(), id: f.id.clone() }
            })
            .collect()
    }

    pub fn place(&self, mut work: Work, ledger: &str) -> Work {
        work.project = self.records.iter().find(|p| p.ledger_key == ledger || p.id == ledger).map(|p| p.id.clone()).unwrap_or_else(|| ledger.to_string());
        work.ledger_key = ledger.to_string();
        work
    }
}

pub fn work_slot<'a>(groups: impl IntoIterator<Item = &'a str>, commands_lead: bool) -> usize {
    let mut n = 0;
    for g in groups {
        if g == "Sessions" || g == "Codex history" || (g == "Commands" && !commands_lead) {
            return n;
        }
        n += 1;
    }
    n
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;
    use crate::mock::{daemon, replies};

    fn world() -> World {
        let v = json!({
            "updatedAt": "2026-11-27T12:00:00Z",
            "projects": [{"id": "p-1", "displayName": "Atlas", "ledgerKey": "project:p-1"}, {"id": "repo:x/y", "displayName": "Y", "ledgerKey": "repo:x/y"}],
            "hosts": [{"id": "", "name": "this-mac"}, {"id": "h2", "name": "build-box"}]
        });
        World::from_json(&v.to_string()).unwrap()
    }

    #[test]
    fn work_search_waits_for_two_characters() {
        assert_eq!(work_query(" a "), None);
        assert_eq!(work_query(" W6 ").as_deref(), Some("W6"));
        assert_eq!(work_query("é"), None);
    }

    #[test]
    fn the_search_reads_the_daemon_shape_and_says_why_it_failed() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"items":[{"projectKey":"repo:x/y","projectName":"Y","host":"h2","id":"y-W3","title":"Fix the\nexport","state":"done","type":"bug","priority":"P2","updatedAt":"2026-10-01T00:00:00Z","matched":"description","snippet":"…the export\n drops rows…"}],"truncated":true}"#),
            (400, r#"{"error":{"code":"invalid_request","message":"search for at least 2 characters, or an exact item id"}}"#),
            (404, r#"{"error":{"code":"unknown_route","message":"no"}}"#),
            (200, r#"{"results":[]}"#),
        ]));
        let found = client.search_work("export rows").unwrap();
        assert_eq!(seen.recv().unwrap().0, "/api/v1/ledger/search?q=export+rows&limit=20");
        assert_eq!((found.items[0].id.as_str(), found.items[0].host.as_deref(), found.truncated), ("y-W3", Some("h2"), true));
        assert_eq!(found.note().unwrap(), "Showing the first 1 work matches across every project. Type more to narrow them.");
        assert_eq!(client.search_work("x").unwrap_err(), "search for at least 2 characters, or an exact item id");
        assert_eq!(client.search_work("xy").unwrap_err(), "This Agenthail has no work search; it runs an older version.");
        assert_eq!(client.search_work("xy").unwrap_err(), "Work search returned no items list");
        assert!(Client::new("http://127.0.0.1:1", "t").search_work("xy").unwrap_err().starts_with("Couldn't reach Agenthail for work search"));
    }

    #[test]
    fn rows_show_state_project_host_and_snippet_and_skip_listed_work() {
        let w = world();
        let item = |id: &str, host: Option<&str>, snippet: Option<&str>| Found { project_key: "repo:x/y".into(), project_name: "Y".into(), host: host.map(String::from), id: id.into(), title: format!("{id} export"), state: "parked".into(), snippet: snippet.map(String::from) };
        let search = Search { items: vec![item("y-W1", None, None), item("y-W2", Some("h2"), Some("…drops\n rows…")), item("y-W3", Some("h9"), None), item("y-W4", None, None)], truncated: false };
        let rows = w.found_rows(&search, |id| id == "y-W4");
        let seen: Vec<(&str, &str, &str)> = rows.iter().map(|r| (r.id.as_str(), r.status.as_str(), r.ledger.as_str())).collect();
        assert_eq!(seen, [("y-W1", "Parked · Y", "repo:x/y"), ("y-W2", "Parked · Y · on build-box · …drops rows…", "repo:x/y"), ("y-W3", "Parked · Y · on h9", "repo:x/y")]);
        assert_eq!((rows[0].badge.as_str(), rows[0].avatar.as_str()), (short_id("y-W1").as_str(), "Y"));
        assert_eq!(state_word("in_review"), "In review");
        assert_eq!(state_word("deleted"), "Deleted");
    }

    #[test]
    fn an_item_read_by_its_ledger_key_lands_in_its_project() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"item":{"item":{"id":"p-1-W9","text":"Old export","state":"done","timeline":[{"at":"2026-09-01T00:00:00Z","actor":"a","kind":"created"}]}}}"#),
            (200, r#"{"item":{"item":{"id":"z-W1","text":"Elsewhere","state":"parked"}}}"#),
            (404, r#"{"error":{"code":"not_found","message":"no work item z-W2"}}"#),
        ]));
        let w = world();
        let got = w.place(client.found_work("project:p-1", "p-1-W9").unwrap(), "project:p-1");
        assert_eq!(seen.recv().unwrap().0, "/api/v1/ledger?projectKey=project%3Ap-1&view=item&id=p-1-W9");
        assert_eq!((got.project.as_str(), got.ledger_key(), got.state.as_str(), got.timeline.len()), ("p-1", "project:p-1", "done", 1));
        let other = w.place(client.found_work("shared:z", "z-W1").unwrap(), "shared:z");
        assert_eq!((other.project.as_str(), other.ledger_key()), ("shared:z", "shared:z"));
        assert_eq!(client.found_work("shared:z", "z-W2").unwrap_err(), "no work item z-W2");
    }

    #[test]
    fn work_matches_go_after_the_loaded_work_and_before_sessions() {
        assert_eq!(work_slot(["Projects", "Work", "Work", "Sessions", "Commands"], false), 3);
        assert_eq!(work_slot(["Projects", "Commands"], false), 1);
        assert_eq!(work_slot(["Commands", "Projects", "Sessions"], true), 2);
        assert_eq!(work_slot(["Commands", "Commands"], true), 2);
        assert_eq!(work_slot(["Commands"], false), 0);
        assert_eq!(work_slot(["Projects", "Work", "Codex history"], false), 2);
    }
}
