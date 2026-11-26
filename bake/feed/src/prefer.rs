use serde::Deserialize;
use serde_json::{Value, json};

use crate::catalog::{Client, RespondError, enc};

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Meter {
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub percent: f64,
    #[serde(default)]
    pub resets_at: Option<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ProxyAccount {
    pub id: String,
    #[serde(default)]
    pub provider: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub preferred: bool,
    #[serde(default)]
    pub routed: bool,
    #[serde(default)]
    pub set_on: Option<String>,
    #[serde(default)]
    pub meters: Vec<Meter>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Proxy {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub id: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Set {
    #[serde(default)]
    pub host: String,
    #[serde(default)]
    pub host_id: String,
    #[serde(default)]
    pub root: String,
    #[serde(default)]
    pub local: bool,
    #[serde(default)]
    pub provider: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub account: String,
    #[serde(default)]
    pub login: String,
    #[serde(default)]
    pub meters: Vec<Meter>,
    #[serde(default)]
    pub problem: String,
    #[serde(default)]
    pub proxy: Option<Proxy>,
    #[serde(default)]
    pub accounts: Vec<ProxyAccount>,
    #[serde(default)]
    pub proxy_problem: String,
}

impl Set {
    pub fn title(&self) -> String {
        [&self.login, &self.account, &self.name].into_iter().find(|t| !t.is_empty()).cloned().unwrap_or_else(|| self.provider.clone())
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct ProxyGroup {
    pub proxy: Proxy,
    pub host: String,
    pub local: bool,
    pub accounts: Vec<ProxyAccount>,
    pub problem: String,
}

impl ProxyGroup {
    pub fn has_peers(&self, account: &ProxyAccount) -> bool {
        self.accounts.iter().any(|a| a.id != account.id && a.provider == account.provider)
    }
}

pub fn proxies(sets: &[Set]) -> Vec<ProxyGroup> {
    let mut out: Vec<ProxyGroup> = Vec::new();
    let mut ordered: Vec<&Set> = sets.iter().filter(|s| s.proxy.as_ref().is_some_and(|p| !p.id.is_empty())).collect();
    ordered.sort_by_key(|s| !s.local);
    for set in ordered {
        let proxy = set.proxy.clone().unwrap_or_default();
        if out.iter().any(|g| g.proxy.id == proxy.id) {
            continue;
        }
        out.push(ProxyGroup { proxy, host: set.host.clone(), local: set.local, accounts: set.accounts.clone(), problem: set.proxy_problem.clone() });
    }
    out
}

pub fn accounts(sets: &[Set]) -> Vec<&Set> {
    sets.iter().filter(|s| s.proxy.is_none()).collect()
}

impl Set {
    pub fn runs(&self, x: &crate::catalog::Session) -> bool {
        let here = if x.local() { self.local } else { self.host_id == x.host.id };
        here && !self.root.is_empty() && x.claude_config_root.as_ref().is_some_and(|r| r.root == self.root)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Prefer {
    On,
    Off,
    Only,
}

impl Prefer {
    pub fn as_str(self) -> &'static str {
        match self {
            Prefer::On => "on",
            Prefer::Off => "off",
            Prefer::Only => "only",
        }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Change {
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub before: f64,
    #[serde(default)]
    pub after: f64,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Named {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub name: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct PlanHost {
    #[serde(default)]
    pub host: String,
    #[serde(default)]
    pub roots: Vec<String>,
    #[serde(default)]
    pub sessions: Vec<Named>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Plan {
    #[serde(default)]
    pub set: String,
    #[serde(default)]
    pub changes: Vec<Change>,
    #[serde(default)]
    pub release: Vec<Named>,
    #[serde(default)]
    pub hosts: Vec<PlanHost>,
    #[serde(default)]
    pub nothing: Option<String>,
    #[serde(default)]
    pub confirm: Option<String>,
    #[serde(default)]
    pub warnings: Vec<String>,
    #[serde(default)]
    pub plan_id: String,
}

fn number(value: f64) -> String {
    if value.fract() == 0.0 { format!("{}", value as i64) } else { format!("{value}") }
}

pub fn word(value: f64) -> String {
    if value == 0.0 {
        "not preferred".into()
    } else if value >= 10.0 {
        "preferred".into()
    } else {
        format!("preference {}", number(value))
    }
}

impl Plan {
    pub fn change_lines(&self) -> Vec<String> {
        if let Some(nothing) = self.nothing.as_ref().filter(|n| !n.is_empty()) {
            return vec![nothing.clone()];
        }
        let mut lines: Vec<String> = self.changes.iter().map(|c| format!("{}: {} → {}", c.label, word(c.before), word(c.after))).collect();
        if self.set != Prefer::Only.as_str() {
            lines.push("Other accounts are not changed.".into());
        }
        if !self.release.is_empty() {
            lines.push(format!("Releases the sessions on {}, so they move on their next request.", self.release.iter().map(|a| a.label.as_str()).collect::<Vec<_>>().join(", ")));
        }
        lines
    }

    pub fn affect_lines(&self) -> Vec<String> {
        if self.nothing.as_ref().is_some_and(|n| !n.is_empty()) {
            return Vec::new();
        }
        self.hosts
            .iter()
            .map(|h| {
                let running: Vec<&str> = h.sessions.iter().map(|s| s.name.as_str()).collect();
                let tail = if running.is_empty() { " · no running sessions".to_string() } else { format!(" · running: {}", running.join(", ")) };
                format!("{}: {}{tail}", h.host, h.roots.join(", "))
            })
            .collect()
    }

    pub fn changes_anything(&self) -> bool {
        self.nothing.as_ref().is_none_or(|n| n.is_empty())
    }
}

impl Client {
    pub fn usage_sets(&self) -> anyhow::Result<Vec<Set>> {
        Ok(serde_json::from_value(self.get_json("/api/v1/usage")?["sets"].clone()).unwrap_or_default())
    }

    pub fn prefer_plan(&self, proxy: &str, account: &str, set: Prefer, now: bool) -> anyhow::Result<Plan> {
        let path = format!("/api/v1/proxies/prefer?proxy={}&account={}&set={}&now={now}", enc(proxy), enc(account), set.as_str());
        Ok(serde_json::from_value(self.get_json(&path)?)?)
    }

    pub fn prefer_apply(&self, proxy: &str, account: &str, set: Prefer, now: bool, plan: &str) -> Result<String, RespondError> {
        let reply: Value = self.post_json("/api/v1/proxies/prefer", json!({"proxy": proxy, "account": account, "set": set.as_str(), "now": now, "planId": plan}))?;
        Ok(reply["message"].as_str().unwrap_or_default().to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{BufRead, BufReader, Read, Write};
    use std::net::TcpListener;
    use std::sync::mpsc;

    fn sets() -> Vec<Set> {
        serde_json::from_value(json!([
            {"host": "laptop", "local": false, "proxy": {"name": "shared", "id": "px"}, "accounts": [{"id": "far.json", "provider": "claude", "label": "far"}]},
            {"host": "mac", "local": true, "provider": "claude", "login": "a@example.com", "meters": [{"kind": "session", "label": "Session", "percent": 40}]},
            {"host": "mac", "local": true, "proxy": {"name": "shared", "id": "px"}, "accounts": [
                {"id": "lead.json", "provider": "claude", "label": "lead", "preferred": true, "setOn": "mac"},
                {"id": "spare.json", "provider": "claude", "label": "spare"},
                {"id": "gpt.json", "provider": "codex", "label": "gpt"}
            ]}
        ]))
        .unwrap()
    }

    #[test]
    fn each_proxy_shows_once_read_from_here_first() {
        let sets = sets();
        let groups = proxies(&sets);
        assert_eq!(groups.len(), 1);
        assert_eq!((groups[0].host.as_str(), groups[0].accounts.len()), ("mac", 3));
        let [lead, _, gpt] = &groups[0].accounts[..] else { panic!() };
        assert!(lead.preferred && groups[0].has_peers(lead));
        assert!(!groups[0].has_peers(gpt));
        assert_eq!(accounts(&sets).iter().map(|s| s.title()).collect::<Vec<_>>(), ["a@example.com"]);
    }

    #[test]
    fn an_account_runs_the_sessions_started_under_its_root_on_its_computer() {
        let set: Set = serde_json::from_value(json!({"host": "mac", "hostId": "h1", "local": true, "root": "/r/a"})).unwrap();
        let session = |host: Value, root: &str| -> crate::catalog::Session { serde_json::from_value(json!({"id": "s", "host": host, "claudeConfigRoot": {"name": "a", "root": root}})).unwrap() };
        assert!(set.runs(&session(json!({"state": "local"}), "/r/a")));
        assert!(!set.runs(&session(json!({"state": "local"}), "/r/b")));
        assert!(!set.runs(&session(json!({"id": "h2", "state": "connected"}), "/r/a")));
        let far: Set = serde_json::from_value(json!({"host": "forge", "hostId": "h2", "root": "/r/a"})).unwrap();
        assert!(far.runs(&session(json!({"id": "h2", "state": "connected"}), "/r/a")));
    }

    #[test]
    fn a_plan_reads_as_the_native_sheet_does() {
        let plan: Plan = serde_json::from_value(json!({
            "set": "only", "planId": "p1",
            "changes": [{"id": "spare.json", "label": "spare", "before": 0, "after": 10}, {"id": "lead.json", "label": "lead", "before": 10, "after": 0}, {"id": "x", "label": "x", "before": 2.5, "after": 0}],
            "release": [{"id": "lead.json", "label": "lead"}],
            "hosts": [{"host": "mac", "roots": ["~/.claude-proxy"], "sessions": [{"id": "s1", "name": "@scout"}]}, {"host": "laptop", "roots": ["~/.p"], "sessions": []}]
        }))
        .unwrap();
        assert_eq!(
            plan.change_lines(),
            ["spare: not preferred → preferred", "lead: preferred → not preferred", "x: preference 2.5 → not preferred", "Releases the sessions on lead, so they move on their next request."]
        );
        assert_eq!(plan.affect_lines(), ["mac: ~/.claude-proxy · running: @scout", "laptop: ~/.p · no running sessions"]);
        let on = Plan { set: "on".into(), release: Vec::new(), ..plan.clone() };
        assert_eq!(on.change_lines().last().map(String::as_str), Some("Other accounts are not changed."));
        let nothing = Plan { nothing: Some("spare is already preferred.".into()), ..plan };
        assert_eq!((nothing.change_lines(), nothing.affect_lines().len(), nothing.changes_anything()), (vec!["spare is already preferred.".to_string()], 0, false));
    }

    #[test]
    fn the_preview_and_the_change_go_where_the_daemon_reads_them() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let (tx, rx) = mpsc::channel();
        std::thread::spawn(move || {
            let replies = [(200, r#"{"set":"on","planId":"p9","changes":[],"release":[],"hosts":[]}"#), (200, r#"{"done":["spare set to 10"],"message":"spare set to 10."}"#), (409, r#"{"error":{"code":"plan_stale","message":"the proxy changed"}}"#)];
            for (status, body) in replies {
                let (stream, _) = listener.accept().unwrap();
                let mut reader = BufReader::new(stream.try_clone().unwrap());
                let mut line = String::new();
                reader.read_line(&mut line).unwrap();
                let mut length = 0usize;
                loop {
                    let mut header = String::new();
                    reader.read_line(&mut header).unwrap();
                    if header.trim_end().is_empty() {
                        break;
                    }
                    match header.trim_end().split_once(':') {
                        Some((name, value)) if name.eq_ignore_ascii_case("content-length") => length = value.trim().parse().unwrap(),
                        _ => {}
                    }
                }
                let mut raw = vec![0; length];
                reader.read_exact(&mut raw).unwrap();
                tx.send((line.trim_end().to_string(), String::from_utf8(raw).unwrap())).unwrap();
                let mut stream = stream;
                write!(stream, "HTTP/1.1 {status} X\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len()).unwrap();
            }
        });
        let client = Client::new(&base, "secret");
        assert_eq!(client.prefer_plan("px", "spare.json", Prefer::On, false).unwrap().plan_id, "p9");
        assert_eq!(rx.recv().unwrap().0, "GET /api/v1/proxies/prefer?proxy=px&account=spare.json&set=on&now=false HTTP/1.1");
        assert_eq!(client.prefer_apply("px", "spare.json", Prefer::Only, true, "p9").unwrap(), "spare set to 10.");
        let (line, body) = rx.recv().unwrap();
        assert_eq!(line, "POST /api/v1/proxies/prefer HTTP/1.1");
        assert_eq!(serde_json::from_str::<Value>(&body).unwrap(), json!({"proxy": "px", "account": "spare.json", "set": "only", "now": true, "planId": "p9"}));
        let stale = client.prefer_apply("px", "spare.json", Prefer::Only, true, "p9").unwrap_err();
        assert_eq!((stale.code.as_str(), stale.message.as_str()), ("plan_stale", "the proxy changed"));
    }
}
