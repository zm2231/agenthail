use std::path::{Path, PathBuf};
use std::process::Command;
use std::time::Duration;

use serde_json::Value;

use crate::catalog::Client;
use crate::version::Build;

pub const PROTOCOL: i64 = 1;
const PROBE: Duration = Duration::from_secs(8);
const PACKAGE: &str = "/Library/Application Support/Agenthail";

#[derive(Clone, Debug, PartialEq)]
pub enum Problem {
    NoToken,
    NotRunning(String),
    Slow,
    Refused,
    Incompatible(i64),
    Failed(String),
    StartFailed(String),
}

impl Problem {
    pub fn title(&self) -> &'static str {
        match self {
            Problem::Slow => "Waiting for Agenthail",
            Problem::Refused => "Agenthail didn't accept this app",
            Problem::Incompatible(_) => "Agenthail needs an update",
            Problem::StartFailed(_) => "Agenthail didn't start",
            Problem::Failed(_) => "Can't reach Agenthail",
            Problem::NoToken | Problem::NotRunning(_) => "Agenthail isn't running",
        }
    }

    pub fn detail(&self) -> String {
        match self {
            Problem::NoToken => "This Mac has no Agenthail key yet (~/.agenthail/dashboard.token). Start Agenthail to create one.".into(),
            Problem::NotRunning(base) => format!("Nothing is answering at {base}. Start Agenthail to connect."),
            Problem::Slow => "Agenthail is running but took too long to answer. Retrying.".into(),
            Problem::Refused => "Agenthail is running but turned down this app's key. Quit and reopen the app to read the current key from ~/.agenthail/dashboard.token.".into(),
            Problem::Incompatible(p) => format!("The running Agenthail speaks protocol {p}; this app needs protocol {PROTOCOL}. Restart Agenthail to run the installed version, or update it."),
            Problem::Failed(why) => format!("The app can't reach the Agenthail daemon: {why}"),
            Problem::StartFailed(why) => why.clone(),
        }
    }

    pub fn start(&self) -> Option<&'static str> {
        match self {
            Problem::Slow | Problem::Refused => None,
            Problem::Incompatible(_) => Some("Restart Agenthail"),
            Problem::NoToken | Problem::NotRunning(_) | Problem::Failed(_) | Problem::StartFailed(_) => Some("Start Agenthail"),
        }
    }

    pub fn retries(&self) -> bool {
        !matches!(self, Problem::Slow | Problem::Refused)
    }

    pub fn status(&self) -> String {
        format!("daemon: {}", self.title())
    }

    pub fn of(error: &str) -> Problem {
        if error.contains("timed out") { Problem::Slow } else { Problem::Failed(error.into()) }
    }

    pub fn bar(&self, offline: Option<Duration>) -> String {
        match (self, offline) {
            (Problem::Slow, _) => "Agenthail is slow to respond".into(),
            (Problem::StartFailed(_), _) | (_, None) => self.title().into(),
            (_, Some(for_)) => {
                let m = for_.as_secs() / 60;
                let age = match m {
                    0 => "now".to_string(),
                    1..=59 => format!("{m}m"),
                    60..=1439 => format!("{}h", m / 60),
                    _ => format!("{}d", m / 1440),
                };
                format!("Offline · as of {age}")
            }
        }
    }
}

pub fn open() -> Result<Client, Problem> {
    Client::from_env().map_err(|_| Problem::NoToken).and_then(keyed)
}

fn keyed(client: Client) -> Result<Client, Problem> {
    if client.token.trim().is_empty() { Err(Problem::NoToken) } else { Ok(client) }
}

pub fn check(reply: &Value) -> Result<Build, Problem> {
    let protocol = reply["protocol"].as_i64().unwrap_or(PROTOCOL);
    let low = reply["minimumProtocol"].as_i64().unwrap_or(protocol);
    let high = reply["maximumProtocol"].as_i64().unwrap_or(protocol);
    if !(low..=high).contains(&PROTOCOL) {
        return Err(Problem::Incompatible(protocol));
    }
    serde_json::from_value(reply.clone()).map_err(|e| Problem::Failed(e.to_string()))
}

impl Client {
    pub fn probe(&self) -> Result<Build, Problem> {
        self.probe_within(PROBE)
    }

    pub fn probe_within(&self, budget: Duration) -> Result<Build, Problem> {
        let client = self.timed(budget);
        let request = client.agent.get(&format!("{}/api/v1/version", client.base)).set("Authorization", &client.bearer()).set("Accept", "application/json");
        match request.call() {
            Ok(response) => check(&response.into_json::<Value>().map_err(|e| Problem::Failed(e.to_string()))?),
            Err(ureq::Error::Status(401 | 403, _)) => Err(Problem::Refused),
            Err(ureq::Error::Status(code, _)) => Err(Problem::Failed(format!("it answered with status {code}"))),
            Err(ureq::Error::Transport(t)) => Err(match t.kind() {
                ureq::ErrorKind::ConnectionFailed | ureq::ErrorKind::Io if timed_out(&t) => Problem::Slow,
                ureq::ErrorKind::ConnectionFailed => Problem::NotRunning(client.base.clone()),
                _ => Problem::Failed(t.to_string()),
            }),
        }
    }
}

fn timed_out(t: &ureq::Transport) -> bool {
    let io = std::error::Error::source(t).and_then(|e| e.downcast_ref::<std::io::Error>());
    io.is_some_and(|e| matches!(e.kind(), std::io::ErrorKind::TimedOut | std::io::ErrorKind::WouldBlock)) || t.to_string().contains("timed out")
}

pub fn cli(named: Option<String>, exe: Option<PathBuf>, home: Option<String>, runs: impl Fn(&Path) -> bool) -> Option<PathBuf> {
    let bundled = exe.and_then(|e| Some(e.parent()?.parent()?.join("Resources/agenthail")));
    let candidates = [
        named.filter(|n| !n.is_empty()).map(PathBuf::from),
        bundled,
        Some(PathBuf::from("/opt/homebrew/bin/agenthail")),
        Some(PathBuf::from("/usr/local/bin/agenthail")),
        home.map(|h| PathBuf::from(h).join(".local/bin/agenthail")),
    ];
    candidates.into_iter().flatten().find(|p| runs(p))
}

fn executable(path: &Path) -> bool {
    use std::os::unix::fs::PermissionsExt;
    std::fs::metadata(path).is_ok_and(|m| m.is_file() && m.permissions().mode() & 0o111 != 0)
}

pub fn start_daemon() -> Result<(), Problem> {
    let path = cli(std::env::var("AGENTHAIL_CLI").ok(), std::env::current_exe().ok(), std::env::var("HOME").ok(), executable)
        .ok_or_else(|| Problem::StartFailed("No agenthail command was found on this Mac. Install Agenthail, then try again.".into()))?;
    let mut command = Command::new(path);
    command.args(["daemon", "restart"]);
    if Path::new(PACKAGE).join("Agenthail Daemon.app/Contents/MacOS/agenthail").exists() {
        let join = |base: String, old: Option<String>| old.filter(|o| !o.is_empty()).map_or(base.clone(), |o| format!("{base}:{o}"));
        command
            .env("AGENTHAIL_SIDECAR", format!("{PACKAGE}/sidecar.py"))
            .env("AGENTHAIL_COOKIE_BRIDGE", format!("{PACKAGE}/cookie.mjs"))
            .env("AGENTHAIL_PYTHON", format!("{PACKAGE}/runtime/python/bin/python3"))
            .env("PYTHONPATH", join(format!("{PACKAGE}/pydeps"), std::env::var("PYTHONPATH").ok()))
            .env("PATH", join("/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin".into(), std::env::var("PATH").ok()))
            .env("PYTHONDONTWRITEBYTECODE", "1");
    }
    let out = command.output().map_err(|e| Problem::StartFailed(e.to_string()))?;
    if out.status.success() {
        return Ok(());
    }
    let said = format!("{}{}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr)).trim().to_string();
    Err(Problem::StartFailed(if said.is_empty() { "Agenthail could not restart.".into() } else { said }))
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn a_daemon_that_doesnt_speak_this_apps_protocol_is_named() {
        assert_eq!(check(&json!({"protocol": 3, "minimumProtocol": 2, "maximumProtocol": 3})), Err(Problem::Incompatible(3)));
        let ok = check(&json!({"protocol": 2, "minimumProtocol": 1, "maximumProtocol": 2, "version": "v0.2.171", "revision": "1f2373224a7c"})).unwrap();
        assert_eq!(ok.version, "v0.2.171");
        assert!(check(&json!({"version": "v0.1"})).is_ok());
    }

    #[test]
    fn no_daemon_listening_means_not_running_with_a_start_button() {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        drop(listener);
        let problem = Client::new(&base, "t").probe().unwrap_err();
        assert_eq!(problem, Problem::NotRunning(base.clone()));
        assert_eq!(problem.title(), "Agenthail isn't running");
        assert_eq!(problem.start(), Some("Start Agenthail"));
        assert!(problem.retries());
        assert!(problem.detail().contains(&base));
    }

    #[test]
    fn a_refused_key_asks_to_reopen_not_to_start() {
        let (client, _paths) = crate::mock::daemon(vec![(401, r#"{"error":{"code":"unauthorized"}}"#.into())]);
        let problem = client.probe().unwrap_err();
        assert_eq!(problem, Problem::Refused);
        assert_eq!(problem.start(), None);
        assert!(!problem.retries());
        assert!(problem.detail().contains("reopen"));
    }

    #[test]
    fn the_probe_reads_the_version_and_the_protocol() {
        let (client, paths) = crate::mock::daemon(vec![(200, r#"{"protocol":2,"minimumProtocol":1,"maximumProtocol":2,"revision":"abc"}"#.into()), (200, r#"{"protocol":4,"minimumProtocol":3,"maximumProtocol":4}"#.into())]);
        assert_eq!(client.probe().unwrap().revision, "abc");
        assert_eq!(paths.recv().unwrap().0, "/api/v1/version");
        let problem = client.probe().unwrap_err();
        assert_eq!(problem, Problem::Incompatible(4));
        assert_eq!(problem.start(), Some("Restart Agenthail"));
    }

    #[test]
    fn a_failed_read_that_timed_out_is_slow() {
        assert_eq!(Problem::of("http://127.0.0.1:7412/api/v1/snapshot: Network Error: timed out reading response"), Problem::Slow);
        assert_eq!(Problem::of("Network Error: Unexpected EOF"), Problem::Failed("Network Error: Unexpected EOF".into()));
        assert_eq!(Problem::Failed("x".into()).title(), "Can't reach Agenthail");
    }

    #[test]
    fn the_bar_says_how_old_the_shown_data_is() {
        let down = Problem::NotRunning("http://127.0.0.1:7412".into());
        assert_eq!(down.bar(None), "Agenthail isn't running");
        assert_eq!(down.bar(Some(Duration::from_secs(20))), "Offline · as of now");
        assert_eq!(down.bar(Some(Duration::from_secs(5 * 60))), "Offline · as of 5m");
        assert_eq!(down.bar(Some(Duration::from_secs(3 * 3600))), "Offline · as of 3h");
        assert_eq!(Problem::Slow.bar(Some(Duration::from_secs(600))), "Agenthail is slow to respond");
        assert_eq!(Problem::StartFailed("boom".into()).bar(Some(Duration::from_secs(600))), "Agenthail didn't start");
    }

    #[test]
    fn an_empty_key_is_no_key() {
        assert_eq!(keyed(Client::new("http://127.0.0.1:1", " \n")).err(), Some(Problem::NoToken));
        assert!(keyed(Client::new("http://127.0.0.1:1", "t")).is_ok());
    }

    #[test]
    fn a_server_error_is_shown_as_is() {
        let (client, _paths) = crate::mock::daemon(vec![(500, "{}".into())]);
        assert_eq!(client.probe().unwrap_err(), Problem::Failed("it answered with status 500".into()));
    }

    #[test]
    fn a_daemon_that_hangs_reads_as_slow_and_retries_by_itself() {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let problem = Client::new(&base, "t").probe_within(Duration::from_millis(300)).unwrap_err();
        assert_eq!(problem, Problem::Slow);
        assert_eq!(problem.start(), None);
        assert!(!problem.retries());
        drop(listener);
    }

    #[test]
    fn the_command_is_found_where_the_mac_app_looks() {
        let has = |set: &'static [&'static str]| move |p: &Path| set.iter().any(|s| Path::new(s) == p);
        let exe = Some(PathBuf::from("/Applications/Rune.app/Contents/MacOS/rune"));
        let home = Some("/Users/a".to_string());
        assert_eq!(cli(Some("/x/agenthail".into()), exe.clone(), home.clone(), has(&["/x/agenthail", "/usr/local/bin/agenthail"])), Some("/x/agenthail".into()));
        assert_eq!(cli(None, exe.clone(), home.clone(), has(&["/Applications/Rune.app/Contents/Resources/agenthail", "/opt/homebrew/bin/agenthail"])), Some("/Applications/Rune.app/Contents/Resources/agenthail".into()));
        assert_eq!(cli(Some(String::new()), exe.clone(), home.clone(), has(&["/Users/a/.local/bin/agenthail"])), Some("/Users/a/.local/bin/agenthail".into()));
        assert_eq!(cli(None, exe, home, has(&[])), None);
    }
}
