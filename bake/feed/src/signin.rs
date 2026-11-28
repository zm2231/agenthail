use serde::{Deserialize, Deserializer};
use serde_json::{Value, json};

use crate::catalog::{Client, enc};
use crate::prefs::{Failure, read, write_failure};

fn listed<'de, D: Deserializer<'de>, T: Deserialize<'de>>(d: D) -> Result<Vec<T>, D::Error> {
    Ok(Option::<Vec<T>>::deserialize(d)?.unwrap_or_default())
}

pub const ABOUT: &str = "Notion and Claude sessions reached over Remote Control read your sign-in cookies from your system default browser's last used profile. Pick another profile or turn sign-in off for each below, or run agenthail signin use. Nothing else reads cookies.";
pub const NO_PROFILES: &str = "No pinned profiles. Add one below to use a profile other than the default browser's.";
pub const ALL_ADDED: &str = "Every browser profile found on this Mac is already added.";
pub const AUTO: &str = "auto";
pub const OFF: &str = "none";

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Profile {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub browser: String,
    #[serde(default)]
    pub profile: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Consumer {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub purpose: String,
    #[serde(default)]
    pub profile_id: Option<String>,
    #[serde(default)]
    pub mode: String,
    #[serde(default)]
    pub browser: Option<String>,
    #[serde(default)]
    pub browser_label: Option<String>,
    #[serde(default)]
    pub profile: Option<String>,
    #[serde(default)]
    pub profile_name: Option<String>,
    #[serde(default)]
    pub problem: Option<String>,
    #[serde(default)]
    pub unavailable: Option<String>,
}

impl Consumer {
    pub fn choice(&self) -> String {
        match self.mode.as_str() {
            "off" => OFF.into(),
            "profile" => self.profile_id.clone().unwrap_or_else(|| AUTO.into()),
            _ => AUTO.into(),
        }
    }

    pub fn resolved(&self) -> String {
        if self.mode == "off" {
            return "Off. Reads no cookies.".into();
        }
        let Some(browser_label) = self.browser_label.as_deref().filter(|b| !b.is_empty()) else {
            return "Uses the system default browser.".into();
        };
        let profile = self.profile.clone().unwrap_or_default();
        if self.mode == "profile" {
            let name = self.profile_name.clone().unwrap_or_else(|| profile.clone());
            return format!("{name}: {browser_label} · {profile}.");
        }
        let named = match &self.profile_name {
            Some(n) if *n != profile => format!(" ({n})"),
            _ => String::new(),
        };
        let place = if self.browser.as_deref() == Some("safari") { "Safari".to_string() } else { format!("{browser_label} · {profile}{named}") };
        format!("Automatic: {place}, the system default browser.")
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Detected {
    #[serde(default)]
    pub profile: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub profile_id: Option<String>,
}

impl Detected {
    pub fn choice_label(&self) -> String {
        if self.name == self.profile { self.name.clone() } else { format!("{} ({})", self.name, self.profile) }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Browser {
    #[serde(default)]
    pub browser: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub available: bool,
    #[serde(default)]
    pub detail: Option<String>,
    #[serde(default, deserialize_with = "listed")]
    pub profiles: Vec<Detected>,
}

impl Browser {
    pub fn addable(&self) -> Vec<&Detected> {
        self.profiles.iter().filter(|p| p.profile_id.is_none()).collect()
    }

    pub fn unavailable_line(&self) -> String {
        format!("{} is unavailable: {}.", self.label, self.detail.as_deref().filter(|d| !d.is_empty()).unwrap_or("no profiles found"))
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Signin {
    #[serde(default, deserialize_with = "listed")]
    pub profiles: Vec<Profile>,
    #[serde(default, deserialize_with = "listed")]
    pub consumers: Vec<Consumer>,
    #[serde(default, deserialize_with = "listed")]
    pub browsers: Vec<Browser>,
}

impl Signin {
    pub fn browser_label(&self, browser: &str) -> String {
        self.browsers.iter().find(|b| b.browser == browser).map(|b| b.label.clone()).unwrap_or_else(|| browser.into())
    }

    pub fn addable_browsers(&self) -> Vec<&Browser> {
        self.browsers.iter().filter(|b| b.available && !b.addable().is_empty()).collect()
    }

    pub fn unavailable_browsers(&self) -> Vec<&Browser> {
        self.browsers.iter().filter(|b| !b.available).collect()
    }

    pub fn choices(&self) -> Vec<(String, String)> {
        const AUTO_LABEL: &str = "Automatic (system default browser)";
        const OFF_LABEL: &str = "Off";
        let mut taken: Vec<String> = vec![AUTO_LABEL.into(), OFF_LABEL.into()];
        taken.extend(self.profiles.iter().filter(|p| self.profiles.iter().filter(|o| o.label == p.label).count() == 1).map(|p| p.label.clone()));
        let mut out = vec![(AUTO_LABEL.to_string(), AUTO.to_string())];
        for p in &self.profiles {
            let alone = p.label != AUTO_LABEL && p.label != OFF_LABEL && self.profiles.iter().filter(|o| o.label == p.label).count() == 1;
            let label = if alone {
                p.label.clone()
            } else {
                let base = format!("{} ({})", p.label, self.pinned_detail(p));
                let mut label = base.clone();
                let mut n = 2;
                while taken.contains(&label) {
                    label = format!("{base} {n}");
                    n += 1;
                }
                taken.push(label.clone());
                label
            };
            out.push((label, p.id.clone()));
        }
        out.push((OFF_LABEL.into(), OFF.into()));
        out
    }

    pub fn pinned_detail(&self, p: &Profile) -> String {
        format!("{} · {}", self.browser_label(&p.browser), p.profile)
    }

    pub fn pick(&self, browser: &str, profile: &str) -> (String, String) {
        let browsers = self.addable_browsers();
        let Some(b) = browsers.iter().find(|b| b.browser == browser).or(browsers.first()) else {
            return (String::new(), String::new());
        };
        let addable = b.addable();
        let p = addable.iter().find(|p| p.profile == profile).or(addable.first()).map(|p| p.profile.clone()).unwrap_or_default();
        (b.browser.clone(), p)
    }

    pub fn can_add(&self, browser: &str, profile: &str) -> bool {
        self.addable_browsers().iter().any(|b| b.browser == browser && b.addable().iter().any(|p| p.profile == profile))
    }
}

impl Client {
    pub fn signin(&self) -> Result<Signin, Failure> {
        read(self, "/api/v1/signin", "settings")
    }

    pub fn add_signin_profile(&self, browser: &str, profile: &str, label: &str) -> Result<(), Failure> {
        self.post_json("/api/v1/signin/profiles", json!({"browser": browser, "profile": profile, "label": label.trim()})).map(drop).map_err(|e| write_failure("settings", e))
    }

    pub fn bind_signin(&self, consumer: &str, choice: &str) -> Result<(), Failure> {
        self.put_json("/api/v1/signin/bindings", json!({"consumer": consumer, "profileId": choice})).map(drop).map_err(|e| write_failure("settings", e))
    }

    pub fn remove_signin_profile(&self, id: &str) -> Result<(), Failure> {
        self.delete_json(&format!("/api/v1/signin/profiles?id={}", enc(id)), Value::Null).map(drop).map_err(|e| write_failure("settings", e))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock::{daemon, replies};

    fn state() -> Signin {
        crate::reply::parse(json!({
            "profiles": [{"id": "p1", "label": "Work", "browser": "chrome", "profile": "Profile 1", "createdAt": "2026-11-20T10:00:00Z"}],
            "consumers": [
                {"id": "notion", "label": "Notion", "purpose": "Reads Notion's sign-in.", "mode": "auto", "browser": "chrome", "browserLabel": "Chrome", "profile": "Default", "profileName": "Personal"},
                {"id": "claude", "label": "Claude", "purpose": "Reads claude.ai's sign-in.", "mode": "profile", "profileId": "p1", "browser": "chrome", "browserLabel": "Chrome", "profile": "Profile 1", "profileName": "Work", "problem": "Chrome is locked"},
                {"id": "x", "label": "X", "purpose": "", "mode": "off"},
                {"id": "y", "label": "Y", "purpose": "", "mode": "auto", "browser": "safari", "browserLabel": "Safari", "profile": "default"},
                {"id": "z", "label": "Z", "purpose": "", "mode": "auto", "unavailable": "No browser on this computer"}],
            "browsers": [
                {"browser": "chrome", "label": "Chrome", "available": true, "profiles": [{"profile": "Default", "name": "Personal"}, {"profile": "Profile 1", "name": "Work", "profileId": "p1"}]},
                {"browser": "arc", "label": "Arc", "available": true, "profiles": [{"profile": "Default", "name": "Default", "profileId": "p9"}]},
                {"browser": "firefox", "label": "Firefox", "available": false, "detail": "", "profiles": null},
                {"browser": "brave", "label": "Brave", "available": false, "detail": "not installed", "profiles": []}]
        }))
        .unwrap()
    }

    #[test]
    fn consumers_say_which_profile_they_read() {
        let s = state();
        let lines: Vec<String> = s.consumers.iter().map(Consumer::resolved).collect();
        assert_eq!(lines, vec!["Automatic: Chrome · Default (Personal), the system default browser.", "Work: Chrome · Profile 1.", "Off. Reads no cookies.", "Automatic: Safari, the system default browser.", "Uses the system default browser."]);
        assert_eq!(s.consumers.iter().map(Consumer::choice).collect::<Vec<_>>(), vec!["auto", "p1", "none", "auto", "auto"]);
        assert_eq!(s.choices(), vec![("Automatic (system default browser)".into(), "auto".into()), ("Work".into(), "p1".into()), ("Off".into(), "none".into())]);
        assert_eq!(s.pinned_detail(&s.profiles[0]), "Chrome · Profile 1");
        let clash: Signin = crate::reply::parse(json!({"profiles": [
            {"id": "a", "label": "Off", "browser": "chrome", "profile": "Default"},
            {"id": "b", "label": "Automatic (system default browser)", "browser": "arc", "profile": "Default"},
            {"id": "c", "label": "Work", "browser": "chrome", "profile": "Profile 1"},
            {"id": "d", "label": "Work", "browser": "brave", "profile": "Default"},
            {"id": "e", "label": "Off (Chrome · Default)", "browser": "safari", "profile": "default"},
            {"id": "f", "label": "Work", "browser": "brave", "profile": "Default"}],
            "browsers": [{"browser": "chrome", "label": "Chrome"}]})).unwrap();
        let labels: Vec<String> = clash.choices().into_iter().map(|(l, _)| l).collect();
        assert_eq!(labels, vec!["Automatic (system default browser)", "Off (Chrome · Default) 2", "Automatic (system default browser) (arc · Default)", "Work (Chrome · Profile 1)", "Work (brave · Default)", "Off (Chrome · Default)", "Work (brave · Default) 2", "Off"]);
        let mut unique = labels.clone();
        unique.sort();
        unique.dedup();
        assert_eq!(unique.len(), labels.len());
    }

    #[test]
    fn only_profiles_not_yet_pinned_can_be_added() {
        let s = state();
        assert_eq!(s.addable_browsers().iter().map(|b| b.browser.as_str()).collect::<Vec<_>>(), vec!["chrome"]);
        assert_eq!(s.unavailable_browsers().iter().map(|b| b.unavailable_line()).collect::<Vec<_>>(), vec!["Firefox is unavailable: no profiles found.", "Brave is unavailable: not installed."]);
        assert_eq!(s.pick("", ""), ("chrome".into(), "Default".into()));
        assert_eq!(s.pick("arc", "Default"), ("chrome".into(), "Default".into()));
        assert!(s.can_add("chrome", "Default") && !s.can_add("chrome", "Profile 1") && !s.can_add("arc", "Default"));
        assert_eq!(s.browsers[0].profiles[0].choice_label(), "Personal (Default)");
        assert_eq!(s.browsers[1].profiles[0].choice_label(), "Default");
        assert_eq!(Signin::default().pick("chrome", "x"), (String::new(), String::new()));
    }

    #[test]
    fn changes_reach_the_daemon_as_the_native_app_sends_them() {
        let (client, rx) = daemon(replies(&[(200, r#"{"profiles":null,"consumers":[],"browsers":[]}"#), (201, r#"{"profile":{"id":"p2"}}"#), (200, "{}"), (200, r#"{"profile":{"id":"p2"}}"#), (400, r#"{"error":{"message":"Unknown consumer \"q\"."}}"#), (401, "{}")]));
        assert_eq!(client.signin().unwrap(), Signin::default());
        client.add_signin_profile("chrome", "Default", " Home ").unwrap();
        client.bind_signin("notion", "none").unwrap();
        client.remove_signin_profile("Work & Play").unwrap();
        assert_eq!(client.bind_signin("q", "auto").unwrap_err().message, "Unknown consumer \"q\".");
        assert!(client.signin().unwrap_err().denied);
        let got: Vec<(String, Value)> = rx.try_iter().collect();
        assert_eq!(
            got,
            vec![
                ("/api/v1/signin".into(), Value::Null),
                ("/api/v1/signin/profiles".into(), json!({"browser": "chrome", "profile": "Default", "label": "Home"})),
                ("/api/v1/signin/bindings".into(), json!({"consumer": "notion", "profileId": "none"})),
                ("/api/v1/signin/profiles?id=Work+%26+Play".into(), Value::Null),
                ("/api/v1/signin/bindings".into(), json!({"consumer": "q", "profileId": "auto"})),
                ("/api/v1/signin".into(), Value::Null),
            ]
        );
    }
}
