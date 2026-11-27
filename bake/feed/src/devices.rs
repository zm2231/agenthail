use qrcodegen::{QrCode, QrCodeEcc};
use serde::Deserialize;
use serde_json::{Value, json};
use time::OffsetDateTime;

use crate::catalog::{Client, ago, enc, parse_time};
use crate::prefs::{Failure, read, reply, write_failure};

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RemoteAccess {
    #[serde(default)]
    pub enabled: bool,
    #[serde(default)]
    pub desired: bool,
    #[serde(default)]
    pub dns_name: Option<String>,
    #[serde(default)]
    pub error: Option<String>,
}

impl RemoteAccess {
    pub fn problem(&self) -> Option<&str> {
        self.error.as_deref().filter(|e| !e.is_empty())
    }

    pub fn detail(&self) -> String {
        if self.enabled {
            return match self.dns_name.as_deref().filter(|d| !d.is_empty()) {
                Some(name) => format!("Reachable privately through Tailscale at {name}."),
                None => "Reachable privately through Tailscale.".into(),
            };
        }
        self.problem().map(str::to_string).unwrap_or_else(|| "Your iPhone can reach this computer only while phone access is on.".into())
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Device {
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub scopes: Vec<String>,
    #[serde(default)]
    pub last_seen_at: Option<String>,
    #[serde(default)]
    pub revoked_at: Option<String>,
    #[serde(default)]
    pub push_enabled: bool,
}

impl Device {
    fn is_host(&self) -> bool {
        self.scopes.iter().any(|s| s == "host")
    }

    pub fn detail(&self, now: OffsetDateTime) -> String {
        let mut parts = vec![if self.push_enabled { "Notifications on" } else { "Notifications off" }.to_string()];
        match self.last_seen_at.as_deref().filter(|t| parse_time(t).is_some()).map(|t| ago(t, now)) {
            Some(a) if a == "now" => parts.push("seen just now".into()),
            Some(a) => parts.push(format!("seen {a} ago")),
            None => {}
        }
        parts.join(" · ")
    }
}

pub fn paired(devices: Vec<Device>) -> Vec<Device> {
    devices.into_iter().filter(|d| !d.is_host() && d.revoked_at.as_deref().is_none_or(str::is_empty)).collect()
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Pairing {
    pub id: String,
    #[serde(default)]
    pub expires_at: String,
    #[serde(rename = "pairingURL")]
    pub pairing_url: String,
    #[serde(default)]
    pub endpoint: String,
}

impl Pairing {
    pub fn deadline(&self, now: OffsetDateTime) -> String {
        let Some(at) = parse_time(&self.expires_at) else { return "The code works once, for five minutes.".into() };
        let minutes = ((at - now).whole_seconds().max(0) + 59) / 60;
        match minutes {
            0 | 1 => "The code works once, within the next minute.".into(),
            n => format!("The code works once, within the next {n} minutes."),
        }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct PairedName {
    #[serde(default)]
    pub name: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct PairingStatus {
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub device: Option<PairedName>,
}

pub const EXPIRED: &str = "The pairing code expired. Pair again to show a new one.";

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Progress {
    Waiting,
    Paired(String),
    Expired,
}

impl Progress {
    pub fn of(status: &PairingStatus) -> Progress {
        match status.state.as_str() {
            "paired" => Progress::Paired(status.device.as_ref().map(|d| d.name.clone()).filter(|n| !n.is_empty()).unwrap_or_else(|| "the iPhone".into())),
            "expired" => Progress::Expired,
            _ => Progress::Waiting,
        }
    }

    pub fn notice(&self) -> Option<String> {
        match self {
            Progress::Waiting => None,
            Progress::Paired(name) => Some(format!("Paired {name}.")),
            Progress::Expired => Some(EXPIRED.into()),
        }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Build {
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub revision: String,
    #[serde(default)]
    pub modified: bool,
}

impl Build {
    pub fn label(&self) -> String {
        let short: String = self.revision.chars().take(8).collect();
        format!("{} ({short}{})", self.version, if self.modified { ", modified" } else { "" })
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Host {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub detail: Option<String>,
    #[serde(default)]
    pub notice: Option<String>,
    #[serde(default)]
    pub build: Option<Build>,
}

impl Host {
    pub fn state_label(&self) -> String {
        match self.state.as_str() {
            "connected" => "Connected".into(),
            "connecting" => "Connecting".into(),
            "unreachable" => "Unreachable".into(),
            "unauthorized" => "Pairing revoked".into(),
            "incompatible" => "Needs update".into(),
            other => {
                let mut c = other.chars();
                c.next().map(|f| f.to_uppercase().collect::<String>() + c.as_str()).unwrap_or_default()
            }
        }
    }

    pub fn healthy(&self) -> bool {
        self.state == "connected"
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Daemon {
    #[serde(default)]
    pub host: Option<Host>,
    #[serde(default)]
    pub hosts: Option<Vec<Host>>,
    #[serde(default)]
    pub running: bool,
    #[serde(default)]
    pub stale: Option<bool>,
    #[serde(default, rename = "refreshError")]
    pub refresh_error: Option<String>,
}

pub const NO_HOSTS: &str = "No other hosts are paired. Run agenthail hosts invite on the other host, then agenthail hosts add here.";

pub struct Matrix {
    pub size: usize,
    pub dark: Vec<bool>,
}

pub const QUIET: usize = 4;

pub fn qr(text: &str) -> Option<Matrix> {
    let code = QrCode::encode_text(text, QrCodeEcc::Medium).ok()?;
    let inner = code.size() as usize;
    let size = inner + 2 * QUIET;
    let mut dark = vec![false; size * size];
    for y in 0..inner {
        for x in 0..inner {
            dark[(y + QUIET) * size + x + QUIET] = code.get_module(x as i32, y as i32);
        }
    }
    Some(Matrix { size, dark })
}

impl Matrix {
    pub fn rgba(&self) -> Vec<u8> {
        self.dark.iter().flat_map(|&d| if d { [0, 0, 0, 255] } else { [255, 255, 255, 255] }).collect()
    }
}

impl Client {
    pub fn devices(&self) -> Result<Vec<Device>, Failure> {
        let value: Value = read(self, "/api/v1/devices", "settings")?;
        reply(value["devices"].clone()).map(paired).map_err(|f| Failure { uncertain: false, ..f })
    }

    pub fn start_pairing(&self) -> Result<Pairing, Failure> {
        let value = self.post_json("/api/v1/pairings", json!({"name": "iPhone", "scopes": ["read", "control"]})).map_err(|e| write_failure("settings", e))?;
        reply(value)
    }

    pub fn pairing_status(&self, id: &str) -> Result<PairingStatus, Failure> {
        read(self, &format!("/api/v1/pairings?id={}", enc(id)), "settings")
    }

    pub fn revoke_device(&self, id: &str) -> Result<(), Failure> {
        self.delete_json("/api/v1/devices", json!({"id": id})).map(drop).map_err(|e| write_failure("settings", e))
    }

    pub fn set_phone_access(&self, on: bool) -> Result<(), Failure> {
        self.post_settings(json!({"action": if on { "remote-enable" } else { "remote-disable" }}))
    }

    pub fn login_link(&self) -> Result<String, Failure> {
        let value = self.post_json("/api/v1/dashboard/login-links", json!({"remote": true})).map_err(|e| write_failure("settings", e))?;
        match value["url"].as_str().filter(|u| !u.is_empty()) {
            Some(url) => Ok(url.to_string()),
            None => Err(Failure::new("the daemon's reply had no link")),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};
    use crate::prefs::Base;

    fn at(ts: &str) -> OffsetDateTime {
        parse_time(ts).unwrap()
    }

    #[test]
    fn phone_access_reads_like_the_mac_app() {
        let mut r = RemoteAccess { enabled: true, dns_name: Some("mini.tail.ts.net".into()), ..Default::default() };
        assert_eq!(r.detail(), "Reachable privately through Tailscale at mini.tail.ts.net.");
        r.dns_name = None;
        assert_eq!(r.detail(), "Reachable privately through Tailscale.");
        r.enabled = false;
        assert_eq!(r.detail(), "Your iPhone can reach this computer only while phone access is on.");
        r.error = Some("tailscale isn't running".into());
        assert_eq!((r.detail().as_str(), r.problem()), ("tailscale isn't running", Some("tailscale isn't running")));
        r.error = Some(String::new());
        assert_eq!(r.problem(), None);
    }

    #[test]
    fn paired_devices_leave_out_hosts_and_revoked_tokens() {
        let list: Vec<Device> = serde_json::from_str(
            r#"[{"id":"d1","name":"forge","scopes":["control","host","read"]},{"id":"d2","name":"iPhone","scopes":["read","control"],"pushEnabled":true,"lastSeenAt":"2026-11-26T10:00:00Z"},{"id":"d3","name":"old","scopes":["read"],"revokedAt":"2026-11-01T00:00:00Z"},{"id":"d4","name":"Browser on this computer","scopes":["read"],"browser":true,"revokedAt":""}]"#,
        )
        .unwrap();
        let shown = paired(list);
        assert_eq!(shown.iter().map(|d| d.id.as_str()).collect::<Vec<_>>(), ["d2", "d4"]);
        let now = at("2026-11-26T10:05:30Z");
        assert_eq!(shown[0].detail(now), "Notifications on · seen 5m ago");
        assert_eq!(shown[1].detail(now), "Notifications off");
        let fresh = Device { last_seen_at: Some("2026-11-26T10:05:10Z".into()), ..shown[1].clone() };
        assert_eq!(fresh.detail(now), "Notifications off · seen just now");
    }

    #[test]
    fn pairing_progress_follows_the_daemon_and_the_deadline_reads_as_minutes_left() {
        let p = Pairing { id: "p1".into(), expires_at: "2026-11-26T10:05:00Z".into(), pairing_url: "agenthail://pair?x=1".into(), endpoint: "https://mini:7412".into() };
        let before = at("2026-11-26T10:01:30Z");
        let after = at("2026-11-26T10:05:00Z");
        let status = |state: &str, name: Option<&str>| PairingStatus { state: state.into(), device: name.map(|n| PairedName { name: n.into() }) };
        assert_eq!(Progress::of(&status("pending", None)), Progress::Waiting);
        assert_eq!(Progress::of(&status("", None)), Progress::Waiting);
        assert_eq!(Progress::of(&status("expired", None)), Progress::Expired);
        assert_eq!(Progress::of(&status("paired", Some("Ada's iPhone"))), Progress::Paired("Ada's iPhone".into()));
        assert_eq!(Progress::of(&status("paired", Some(""))).notice().as_deref(), Some("Paired the iPhone."));
        assert_eq!(Progress::Expired.notice().as_deref(), Some(EXPIRED));
        assert_eq!(Progress::Waiting.notice(), None);
        assert_eq!(p.deadline(before), "The code works once, within the next 4 minutes.");
        assert_eq!(p.deadline(at("2026-11-26T10:04:10Z")), "The code works once, within the next minute.");
        assert_eq!(p.deadline(after), "The code works once, within the next minute.");
        let unknown = Pairing { expires_at: "soon".into(), ..p };
        assert_eq!(unknown.deadline(before), "The code works once, for five minutes.");
    }

    #[test]
    fn hosts_come_from_the_snapshot_and_read_like_the_mac_app() {
        let base: Base = serde_json::from_str(
            r#"{"eventCursor":4,"daemon":{"host":{"id":"h1","name":"studio","state":"local"},"hosts":[{"id":"h2","name":"forge","state":"connected","build":{"version":"v0.2.172","revision":"a67ebd8beb12e5f8","modified":false}},{"id":"h3","name":"box","state":"unreachable","detail":"dial tcp: i/o timeout"},{"id":"h4","name":"old","state":"unauthorized"},{"id":"h5","name":"new","state":"incompatible","notice":"Update it"},{"id":"h6","name":"odd","state":"sleeping"}]}}"#,
        )
        .unwrap();
        assert_eq!(base.daemon.host.as_ref().map(|h| h.name.as_str()), Some("studio"));
        let hosts = base.daemon.hosts.unwrap();
        assert_eq!(hosts.iter().map(Host::state_label).collect::<Vec<_>>(), ["Connected", "Unreachable", "Pairing revoked", "Needs update", "Sleeping"]);
        assert_eq!(hosts.iter().map(Host::healthy).collect::<Vec<_>>(), [true, false, false, false, false]);
        assert_eq!(hosts[0].build.as_ref().unwrap().label(), "v0.2.172 (a67ebd8b)");
        assert_eq!(Build { version: "v1".into(), revision: "abc".into(), modified: true }.label(), "v1 (abc, modified)");
        let none: Base = serde_json::from_str(r#"{"eventCursor":1}"#).unwrap();
        assert_eq!(none.daemon, Daemon::default());
    }

    #[test]
    fn a_pairing_qr_code_has_finder_patterns_inside_a_quiet_zone() {
        let m = qr("agenthail://pair?endpoint=https%3A%2F%2Fmini.tail.ts.net%3A7412&secret=0123456789abcdef0123456789abcdef&host=h3a1b97d15648&name=mini&scope=read&scope=control").unwrap();
        assert_eq!(m.dark.len(), m.size * m.size);
        assert_eq!((m.size - 2 * QUIET - 17) % 4, 0, "a QR side is 17 + 4 per version");
        let dark = |x: usize, y: usize| m.dark[y * m.size + x];
        assert!((0..m.size).all(|i| !dark(i, 0) && !dark(0, i) && !dark(i, QUIET - 1) && !dark(m.size - 1, i)));
        let inner = m.size - 2 * QUIET;
        for (ox, oy) in [(0, 0), (inner - 7, 0), (0, inner - 7)] {
            let finder = |x: usize, y: usize| dark(QUIET + ox + x, QUIET + oy + y);
            assert!((0..7).all(|i| finder(i, 0) && finder(i, 6) && finder(0, i) && finder(6, i)));
            assert!((1..6).all(|i| !finder(i, 1) && !finder(1, i)));
            assert!((2..5).all(|x| (2..5).all(|y| finder(x, y))));
        }
        assert_eq!(m.rgba().len(), m.size * m.size * 4);
        assert_eq!(&m.rgba()[..4], &[255, 255, 255, 255]);
    }

    #[test]
    fn pairing_devices_and_links_use_the_daemons_routes() {
        let created = r#"{"id":"p1","expiresAt":"2026-11-26T10:05:00Z","pairingURL":"agenthail://pair?secret=s","endpoint":"https://mini:7412","secret":"s","scopes":["read","control"]}"#;
        let (client, rx) = daemon(replies(&[
            (201, created),
            (200, r#"{"id":"p 1","state":"paired","expiresAt":"2026-11-26T10:05:00Z","device":{"id":"d9","name":"iPhone 17"}}"#),
            (200, r#"{"devices":[{"id":"d9","name":"iPhone 17","scopes":["read","control"],"pushEnabled":false},{"id":"h","name":"forge","scopes":["host"]}]}"#),
            (200, r#"{"ok":true}"#),
            (200, r#"{"ok":true}"#),
            (200, r#"{"url":"https://mini.tail.ts.net/login?t=abc","expiresAt":"2026-11-26T10:20:00Z"}"#),
        ]));
        let p = client.start_pairing().unwrap();
        assert_eq!((p.id.as_str(), p.pairing_url.as_str(), p.endpoint.as_str()), ("p1", "agenthail://pair?secret=s", "https://mini:7412"));
        assert_eq!(rx.recv().unwrap(), ("/api/v1/pairings".into(), json!({"name": "iPhone", "scopes": ["read", "control"]})));
        let s = client.pairing_status("p 1").unwrap();
        assert_eq!((s.state.as_str(), s.device.unwrap().name.as_str()), ("paired", "iPhone 17"));
        assert_eq!(rx.recv().unwrap().0, "/api/v1/pairings?id=p+1");
        assert_eq!(client.devices().unwrap().iter().map(|d| d.id.as_str()).collect::<Vec<_>>(), ["d9"]);
        assert_eq!(rx.recv().unwrap().0, "/api/v1/devices");
        client.revoke_device("d9").unwrap();
        assert_eq!(rx.recv().unwrap(), ("/api/v1/devices".into(), json!({"id": "d9"})));
        client.set_phone_access(false).unwrap();
        assert_eq!(rx.recv().unwrap(), ("/api/v1/settings".into(), json!({"action": "remote-disable"})));
        assert_eq!(client.login_link().unwrap(), "https://mini.tail.ts.net/login?t=abc");
        assert_eq!(rx.recv().unwrap(), ("/api/v1/dashboard/login-links".into(), json!({"remote": true})));
    }

    #[test]
    fn refusals_name_the_settings_permission_and_daemon_reasons_come_through() {
        let off = r#"{"error":{"code":"remote_access_required","message":"Turn on phone access first."}}"#;
        let (client, _rx) = daemon(replies(&[
            (403, r#"{"error":{"code":"forbidden","message":"no"}}"#),
            (409, off),
            (401, r#"{"error":{"code":"unauthorized","message":"no"}}"#),
            (404, r#"{"error":{"code":"device_not_found","message":"No device has that id."}}"#),
            (409, off),
            (200, r#"{"expiresAt":"x"}"#),
        ]));
        let denied = client.devices().unwrap_err();
        assert!(denied.denied && denied.scope == "settings");
        let pairing = client.start_pairing().unwrap_err();
        assert_eq!((pairing.message.as_str(), pairing.denied), ("Turn on phone access first.", false));
        assert!(client.pairing_status("p1").unwrap_err().denied);
        assert_eq!(client.revoke_device("gone").unwrap_err().message, "No device has that id.");
        assert_eq!(client.login_link().unwrap_err().message, "Turn on phone access first.");
        assert_eq!(client.login_link().unwrap_err().message, "the daemon's reply had no link");
    }
}
