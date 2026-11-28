use serde::Deserialize;
use serde_json::{Value, json};

use crate::catalog::{Client, RespondError, enc};

fn list<'de, D: serde::Deserializer<'de>, T: Deserialize<'de>>(d: D) -> Result<Vec<T>, D::Error> {
    Ok(Option::<Vec<T>>::deserialize(d)?.unwrap_or_default())
}

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
    #[serde(default)]
    pub severity: String,
    #[serde(default)]
    pub aged: bool,
    #[serde(default)]
    pub as_of: Option<String>,
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
    pub preference: f64,
    #[serde(default)]
    pub routed: bool,
    #[serde(default)]
    pub set_on: Option<String>,
    #[serde(default, deserialize_with = "list")]
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
    #[serde(default, deserialize_with = "list")]
    pub meters: Vec<Meter>,
    #[serde(default)]
    pub problem: String,
    #[serde(default)]
    pub proxy: Option<Proxy>,
    #[serde(default, deserialize_with = "list")]
    pub accounts: Vec<ProxyAccount>,
    #[serde(default)]
    pub proxy_problem: String,
}

impl Set {
    pub fn title(&self) -> String {
        match self.provider.as_str() {
            "codex" => "Codex".into(),
            "" => self.host.clone(),
            _ if !self.account.is_empty() => self.account.clone(),
            _ if !self.name.is_empty() => self.name.clone(),
            _ => "Claude".into(),
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct ProxyGroup {
    pub proxy: Proxy,
    pub host: String,
    pub local: bool,
    pub accounts: Vec<ProxyAccount>,
    pub problem: String,
    pub reading: bool,
}

impl ProxyAccount {
    pub fn set_to_prefer(&self) -> bool {
        self.preferred || self.preference >= 10.0
    }

    pub fn prefer_label(&self) -> &'static str {
        match (self.preferred, self.set_to_prefer()) {
            (true, _) => "Preferred ✓",
            (false, true) => "Set to prefer ✓",
            (false, false) => "Prefer",
        }
    }
}

impl ProxyGroup {
    pub fn has_peers(&self, account: &ProxyAccount) -> bool {
        self.accounts.iter().any(|a| a.id != account.id && a.provider == account.provider)
    }
}

pub fn proxies(sets: &[Set]) -> Vec<ProxyGroup> {
    let mut out: Vec<ProxyGroup> = Vec::new();
    let mut ordered: Vec<&Set> = sets.iter().filter(|s| s.provider == "proxy" && s.proxy.as_ref().is_some_and(|p| !p.id.is_empty())).collect();
    ordered.sort_by_key(|s| !s.local);
    for set in ordered {
        let proxy = set.proxy.clone().unwrap_or_default();
        if out.iter().any(|g| g.proxy.id == proxy.id) {
            continue;
        }
        let problem = if set.problem.is_empty() { set.proxy_problem.clone() } else { set.problem.clone() };
        out.push(ProxyGroup { proxy, host: set.host.clone(), local: set.local, accounts: set.accounts.clone(), problem, reading: set.proxy_problem == "reading" });
    }
    out
}

pub fn accounts(sets: &[Set]) -> Vec<&Set> {
    sets.iter().filter(|s| s.provider != "proxy").collect()
}

impl Set {
    pub fn via(&self) -> Option<&str> {
        self.proxy.as_ref().filter(|_| self.provider != "proxy").map(|p| p.name.as_str())
    }
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
    #[serde(default, deserialize_with = "list")]
    pub roots: Vec<String>,
    #[serde(default, deserialize_with = "list")]
    pub sessions: Vec<Named>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Plan {
    #[serde(default)]
    pub set: String,
    #[serde(default, deserialize_with = "list")]
    pub changes: Vec<Change>,
    #[serde(default, deserialize_with = "list")]
    pub release: Vec<Named>,
    #[serde(default, deserialize_with = "list")]
    pub hosts: Vec<PlanHost>,
    #[serde(default)]
    pub nothing: Option<String>,
    #[serde(default)]
    pub confirm: Option<String>,
    #[serde(default, deserialize_with = "list")]
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

impl Meter {
    pub fn critical(&self) -> bool {
        self.severity == "critical" || self.percent >= 90.0
    }
}

pub fn failure(error: &RespondError) -> String {
    let done: Vec<&str> = error.reply["done"].as_array().into_iter().flatten().filter_map(Value::as_str).collect();
    if !done.is_empty() {
        return format!("{} Already changed: {}. Nothing was undone.", error.message.trim_end_matches('.').to_string() + ".", done.join("; "));
    }
    if error.maybe_delivered {
        return format!("The change may have gone through: {}", error.message);
    }
    error.message.clone()
}

impl Client {
    pub fn usage_sets(&self) -> anyhow::Result<Vec<Set>> {
        Ok(crate::reply::parse::<Option<Vec<Set>>>(self.get_json("/api/v1/usage")?["sets"].clone())?.unwrap_or_default())
    }

    pub fn prefer_plan(&self, proxy: &str, account: &str, set: Prefer, now: bool) -> anyhow::Result<Plan> {
        let path = format!("/api/v1/proxies/prefer?proxy={}&account={}&set={}&now={now}", enc(proxy), enc(account), set.as_str());
        Ok(crate::reply::parse(self.get_json(&path)?)?)
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
        crate::reply::parse(json!([
            {"host": "laptop", "local": false, "provider": "proxy", "proxy": {"name": "shared", "id": "px"}, "accounts": [{"id": "far.json", "provider": "claude", "label": "far"}]},
            {"host": "mac", "local": true, "provider": "claude", "login": "claude.ai", "account": "a@example.com", "name": "main", "meters": [{"kind": "session", "label": "Session", "percent": 40}]},
            {"host": "mac", "local": true, "provider": "claude", "login": "signed_out", "name": "spare", "meters": null, "problem": "Not signed in"},
            {"host": "mac", "local": true, "provider": "codex", "meters": null},
            {"host": "mac", "local": true, "provider": "proxy", "meters": null, "proxy": {"name": "shared", "id": "px"}, "accounts": [
                {"id": "lead.json", "provider": "claude", "label": "lead", "preferred": true, "setOn": "mac"},
                {"id": "spare.json", "provider": "claude", "label": "spare"},
                {"id": "gpt.json", "provider": "codex", "label": "gpt", "meters": null}
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
        let tied: ProxyAccount = crate::reply::parse(json!({"id": "b.json", "provider": "claude", "preference": 10, "preferred": false})).unwrap();
        assert!(tied.set_to_prefer() && lead.set_to_prefer(), "a tie at the preferred value can still be turned off");
        assert!(!groups[0].accounts[1].set_to_prefer());
        let disabled: ProxyAccount = crate::reply::parse(json!({"id": "d.json", "provider": "claude", "status": "disabled", "preference": 10})).unwrap();
        assert_eq!([lead.prefer_label(), tied.prefer_label(), disabled.prefer_label(), groups[0].accounts[1].prefer_label()], ["Preferred ✓", "Set to prefer ✓", "Set to prefer ✓", "Prefer"]);
        assert_eq!(accounts(&sets).iter().map(|s| s.title()).collect::<Vec<_>>(), ["a@example.com", "spare", "Codex"]);
    }

    #[test]
    fn an_account_runs_the_sessions_started_under_its_root_on_its_computer() {
        let set: Set = crate::reply::parse(json!({"host": "mac", "hostId": "h1", "local": true, "root": "/r/a"})).unwrap();
        let session = |host: Value, root: &str| -> crate::catalog::Session { crate::reply::parse(json!({"id": "s", "host": host, "claudeConfigRoot": {"name": "a", "root": root}})).unwrap() };
        assert!(set.runs(&session(json!({"state": "local"}), "/r/a")));
        assert!(!set.runs(&session(json!({"state": "local"}), "/r/b")));
        assert!(!set.runs(&session(json!({"id": "h2", "state": "connected"}), "/r/a")));
        let far: Set = crate::reply::parse(json!({"host": "forge", "hostId": "h2", "root": "/r/a"})).unwrap();
        assert!(far.runs(&session(json!({"id": "h2", "state": "connected"}), "/r/a")));
    }

    #[test]
    fn a_root_through_a_proxy_is_an_account_and_the_proxys_own_set_is_its_group() {
        let sets: Vec<Set> = crate::reply::parse(json!([
            {"host": "mac", "local": true, "provider": "claude", "name": "main", "account": "a@example.com", "root": "/r/main", "meters": [{"label": "Session", "percent": 10}]},
            {"host": "mac", "local": true, "provider": "claude", "name": "team", "root": "/r/team", "login": "proxy", "proxy": {"name": "shared", "id": "px"}, "meters": null},
            {"host": "mac", "local": true, "provider": "proxy", "name": "shared", "proxy": {"name": "shared", "id": "px"}, "accounts": [{"id": "dev.json", "provider": "claude", "label": "dev@example.com", "meters": [{"label": "Claude", "percent": 18, "severity": "critical"}]}], "problem": "Every shared Claude account is over its ceiling."}
        ]))
        .unwrap();
        let groups = proxies(&sets);
        assert_eq!(groups.len(), 1);
        assert_eq!((groups[0].accounts.len(), groups[0].problem.as_str()), (1, "Every shared Claude account is over its ceiling."));
        assert_eq!(accounts(&sets).iter().map(|s| (s.title(), s.via())).collect::<Vec<_>>(), [("a@example.com".to_string(), None), ("team".to_string(), Some("shared"))]);
    }

    #[test]
    fn a_proxy_says_what_the_daemon_explains_and_whether_it_is_still_reading_its_accounts() {
        let sets: Vec<Set> = crate::reply::parse(json!([
            {"host": "mac", "local": true, "provider": "proxy", "proxy": {"name": "shared", "id": "px"}, "accounts": [], "proxyProblem": "reading", "problem": "Reading the shared proxy's accounts."},
            {"host": "mac", "local": true, "provider": "proxy", "proxy": {"name": "team", "id": "pt"}, "accounts": [{"id": "a.json", "provider": "claude"}], "problem": "Every team Claude account is over its ceiling."},
            {"host": "mac", "local": true, "provider": "proxy", "proxy": {"name": "old", "id": "po"}, "proxyProblem": "unauthorized"}
        ]))
        .unwrap();
        let groups = proxies(&sets);
        assert_eq!(groups.iter().map(|g| (g.problem.as_str(), g.reading)).collect::<Vec<_>>(), [
            ("Reading the shared proxy's accounts.", true),
            ("Every team Claude account is over its ceiling.", false),
            ("unauthorized", false),
        ]);
    }

    #[test]
    fn a_meter_at_its_ceiling_is_critical_whatever_its_percent() {
        let at_ceiling: Meter = crate::reply::parse(json!({"label": "Session", "percent": 50, "severity": "critical"})).unwrap();
        let busy: Meter = crate::reply::parse(json!({"label": "Session", "percent": 92})).unwrap();
        let calm: Meter = crate::reply::parse(json!({"label": "Session", "percent": 50, "severity": "warning"})).unwrap();
        assert_eq!([at_ceiling.critical(), busy.critical(), calm.critical()], [true, true, false]);
    }

    #[test]
    fn a_failed_change_says_what_already_changed_or_that_it_may_have() {
        let partial = RespondError::from_status("/api/v1/proxies/prefer", 502, r#"{"error":{"code":"proxy_write_failed","message":"lead not changed: refused"},"done":["backup set to 10"]}"#);
        assert_eq!(failure(&partial), "lead not changed: refused. Already changed: backup set to 10. Nothing was undone.");
        let unknown = RespondError::from_status("/api/v1/proxies/prefer", 504, r#"{"error":{"code":"host_outcome_unknown","message":"Host forge did not answer"}}"#);
        assert_eq!(failure(&unknown), "The change may have gone through: Host forge did not answer");
    }

    #[test]
    fn a_plan_reads_as_the_native_sheet_does() {
        let empty: Plan = crate::reply::parse(json!({"set": "on", "planId": "p2", "changes": null, "release": null, "hosts": [{"host": "mac", "roots": null, "sessions": null}], "warnings": null})).unwrap();
        assert_eq!(empty.affect_lines(), ["mac:  · no running sessions"]);
        let plan: Plan = crate::reply::parse(json!({
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
        assert_eq!(failure(&stale), "the proxy changed");
    }
}
