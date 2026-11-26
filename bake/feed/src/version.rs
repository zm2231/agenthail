use serde::Deserialize;

use crate::catalog::Client;

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Build {
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub revision: String,
    #[serde(default)]
    pub modified: bool,
    #[serde(skip)]
    pub release: bool,
}

impl Build {
    pub fn known(&self) -> bool {
        let r = self.revision.trim();
        !r.is_empty() && r != "unknown"
    }

    pub fn label(&self) -> String {
        let short: String = self.revision.chars().take(8).collect();
        let tag = if self.modified { format!("{short}, modified") } else { short };
        if self.version.is_empty() { format!("({tag})") } else { format!("{} ({tag})", self.version) }
    }
}

pub fn notice(app: &Build, daemon: &Build) -> Option<String> {
    if !app.release || !app.known() || !daemon.known() || app.modified || daemon.modified || app.revision == daemon.revision {
        return None;
    }
    Some(format!("The running daemon is {}; this app is {}. Restart Agenthail to use this version.", daemon.label(), app.label()))
}

impl Client {
    pub fn version(&self) -> anyhow::Result<Build> {
        Ok(serde_json::from_value(self.get_json("/api/v1/version")?)?)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn build(version: &str, revision: &str, modified: bool) -> Build {
        Build { version: version.into(), revision: revision.into(), modified, release: true }
    }

    #[test]
    fn only_two_known_clean_builds_that_differ_get_a_notice() {
        let app = build("v0.2.170", "e387b0d8aaaa", false);
        let daemon = build("v0.2.171", "1f2373224a7c", false);
        assert_eq!(notice(&app, &daemon).as_deref(), Some("The running daemon is v0.2.171 (1f237322); this app is v0.2.170 (e387b0d8). Restart Agenthail to use this version."));
        assert_eq!(notice(&app, &app), None);
        assert_eq!(notice(&build("", "", false), &daemon), None);
        assert_eq!(notice(&build("v1", "unknown", false), &daemon), None);
        assert_eq!(notice(&build("v1", "abc", true), &daemon), None);
        assert_eq!(notice(&app, &build("v2", "def", true)), None);
    }

    #[test]
    fn a_development_build_never_asks_for_a_restart() {
        let dev = Build { release: false, ..build("v0.2.164", "46a57600aaaa", false) };
        assert_eq!(notice(&dev, &build("v0.2.171", "1f2373224a7c", false)), None);
    }

    #[test]
    fn the_daemon_build_reads_from_the_version_reply() {
        let b: Build = serde_json::from_str(r#"{"protocol":2,"version":"v0.2.171","revision":"1f2373224a7c1441","modified":false}"#).unwrap();
        assert_eq!(b, Build { release: false, ..build("v0.2.171", "1f2373224a7c1441", false) });
        assert_eq!(build("v1", "abcdef123", true).label(), "v1 (abcdef12, modified)");
    }
}
