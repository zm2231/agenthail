use serde::Deserialize;
use serde_json::{Value, json};
use time::{OffsetDateTime, UtcOffset};

use crate::catalog::parse_time;

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct BackgroundTasks {
    #[serde(default)]
    pub running: bool,
    #[serde(default)]
    pub tasks: Vec<BackgroundTask>,
    #[serde(default)]
    pub since: String,
    #[serde(default)]
    pub source: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct BackgroundTask {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub parent_id: String,
    #[serde(default)]
    pub ambient: bool,
    #[serde(default)]
    pub since: String,
}

impl BackgroundTasks {
    pub fn is_shown(&self) -> bool {
        self.running || !self.tasks.is_empty()
    }

    pub fn active_count(&self) -> usize {
        self.tasks.iter().filter(|t| !t.ambient).count()
    }

    pub fn summary(&self) -> String {
        if !self.running {
            return "Only ambient tasks run".into();
        }
        match self.active_count() {
            0 => "Background work running".into(),
            1 => "1 background task running".into(),
            n => format!("{n} background tasks running"),
        }
    }

    pub fn source_note(&self) -> Option<&'static str> {
        (self.source == "status").then_some("Claude reports a background shell but not which one.")
    }

    pub fn task_set(&self) -> String {
        let mut ids: Vec<&str> = self.tasks.iter().map(|t| t.id.as_str()).collect();
        ids.sort_unstable();
        std::iter::once(if self.running { "running" } else { "ambient" }).chain(ids).collect::<Vec<_>>().join(",")
    }
}

impl BackgroundTask {
    pub fn kind_label(&self) -> String {
        match self.kind.as_str() {
            "shell" => "Shell".into(),
            "agent" => "Agent".into(),
            "monitor" => "Monitor".into(),
            "" => "Task".into(),
            other => {
                let mut chars = other.chars();
                chars.next().map(|c| c.to_uppercase().chain(chars.flat_map(char::to_lowercase)).collect()).unwrap_or_default()
            }
        }
    }

    pub fn line(&self) -> String {
        let text = if self.description.trim().is_empty() { self.id.as_str() } else { self.description.as_str() };
        let line = format!("{}: {text}", self.kind_label());
        if self.ambient { format!("{line} (ambient)") } else { line }
    }

    pub fn since_at(&self) -> Option<OffsetDateTime> {
        parse_time(&self.since)
    }

    pub fn since_text(&self, now: OffsetDateTime, clock: Clock, offset_at: impl Fn(OffsetDateTime) -> i32) -> Option<String> {
        let local = |t: OffsetDateTime| t.to_offset(UtcOffset::from_whole_seconds(offset_at(t)).unwrap_or(UtcOffset::UTC));
        let at = local(self.since_at()?);
        let now = local(now);
        let clock = clock.show(at.hour(), at.minute());
        if at.date() == now.date() {
            Some(format!("since {clock}"))
        } else {
            let day = at.weekday().to_string();
            Some(format!("since {} {clock}", &day[..3]))
        }
    }

    pub fn timed_line(&self, now: OffsetDateTime, clock: Clock, offset_at: impl Fn(OffsetDateTime) -> i32) -> String {
        match self.since_text(now, clock, offset_at) {
            Some(since) => format!("{} · {since}", self.line()),
            None => self.line(),
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Clock {
    Twelve,
    TwentyFour,
}

const TWELVE_HOUR_REGIONS: [&str; 15] = ["US", "CA", "AU", "NZ", "IN", "PH", "PK", "EG", "SA", "BD", "CO", "MX", "KR", "TW", "HK"];

impl Clock {
    pub fn from_settings(force_24: bool, force_12: bool, locale: &str) -> Clock {
        if force_24 {
            return Clock::TwentyFour;
        }
        if force_12 {
            return Clock::Twelve;
        }
        let base = locale.split(['@', '.']).next().unwrap_or_default();
        let region = base.split(['_', '-']).skip(1).find(|part| part.len() == 2 && part.chars().all(|c| c.is_ascii_uppercase())).unwrap_or_default();
        if TWELVE_HOUR_REGIONS.contains(&region) { Clock::Twelve } else { Clock::TwentyFour }
    }

    pub(crate) fn show(self, hour: u8, minute: u8) -> String {
        match self {
            Clock::TwentyFour => format!("{hour:02}:{minute:02}"),
            Clock::Twelve => {
                let h = match hour % 12 {
                    0 => 12,
                    h => h,
                };
                format!("{h}:{minute:02} {}", if hour < 12 { "AM" } else { "PM" })
            }
        }
    }
}

const REMEMBERED: usize = 500;

#[derive(Clone, Debug, Default, PartialEq)]
pub struct DockChoices {
    dismissed: Vec<(String, String)>,
    open: Vec<(String, String)>,
}

fn value<'a>(list: &'a [(String, String)], session: &str) -> Option<&'a str> {
    list.iter().find(|(id, _)| id == session).map(|(_, v)| v.as_str())
}

fn set(list: &mut Vec<(String, String)>, session: &str, to: String) {
    list.retain(|(id, _)| id != session);
    list.push((session.to_string(), to));
    if list.len() > REMEMBERED {
        list.drain(..list.len() - REMEMBERED);
    }
}

impl DockChoices {
    pub fn shows(&self, background: &BackgroundTasks, session: &str) -> bool {
        background.is_shown() && value(&self.dismissed, session) != Some(background.task_set().as_str())
    }

    pub fn dismiss(&mut self, background: &BackgroundTasks, session: &str) {
        set(&mut self.dismissed, session, background.task_set());
    }

    pub fn is_open(&self, background: &BackgroundTasks, session: &str) -> bool {
        value(&self.open, session).map_or(background.running, |v| v == "open")
    }

    pub fn toggle(&mut self, background: &BackgroundTasks, session: &str) {
        let to = if self.is_open(background, session) { "closed" } else { "open" };
        set(&mut self.open, session, to.into());
    }

    pub fn to_json(&self) -> Value {
        let pairs = |list: &[(String, String)]| list.iter().map(|(id, v)| json!([id, v])).collect::<Vec<_>>();
        json!({ "dismissed": pairs(&self.dismissed), "open": pairs(&self.open) })
    }

    pub fn from_json(v: &Value) -> DockChoices {
        let pairs = |v: &Value| -> Vec<(String, String)> {
            let mut list: Vec<(String, String)> = v.as_array().into_iter().flatten().filter_map(|p| Some((p.get(0)?.as_str()?.to_string(), p.get(1)?.as_str()?.to_string()))).collect();
            if list.len() > REMEMBERED {
                list.drain(..list.len() - REMEMBERED);
            }
            list
        };
        DockChoices { dismissed: pairs(&v["dismissed"]), open: pairs(&v["open"]) }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn at(ts: &str) -> OffsetDateTime {
        parse_time(ts).unwrap()
    }

    fn tasks(json: &str) -> BackgroundTasks {
        serde_json::from_str(json).unwrap()
    }

    #[test]
    fn reads_the_daemon_shape() {
        let b = tasks(r#"{"running":true,"tasks":[{"id":"bz1","kind":"shell","description":"Run HEAD rune lib suite three times","since":"2026-11-26T13:16:50.920000000Z"},{"id":"w1","kind":"monitor","ambient":true}],"since":"2026-11-26T13:16:50.920000000Z","source":"host"}"#);
        assert_eq!(b.summary(), "1 background task running");
        assert_eq!(b.tasks[0].line(), "Shell: Run HEAD rune lib suite three times");
        assert_eq!(b.tasks[1].line(), "Monitor: w1 (ambient)");
        assert_eq!(b.source_note(), None);
        assert!(b.is_shown());
    }

    #[test]
    fn the_summary_follows_what_runs() {
        assert_eq!(tasks(r#"{"running":false,"tasks":[{"id":"w","kind":"monitor","ambient":true}],"source":"host"}"#).summary(), "Only ambient tasks run");
        assert_eq!(tasks(r#"{"running":true,"source":"status"}"#).summary(), "Background work running");
        assert_eq!(tasks(r#"{"running":true,"tasks":[{"id":"a","kind":"shell"},{"id":"b","kind":"agent"}],"source":"host"}"#).summary(), "2 background tasks running");
        assert_eq!(tasks(r#"{"running":true,"source":"status"}"#).source_note(), Some("Claude reports a background shell but not which one."));
        assert!(!tasks(r#"{"running":false,"source":"host"}"#).is_shown());
    }

    #[test]
    fn an_unknown_kind_is_named_plainly() {
        let t = |kind: &str| BackgroundTask { id: "x".into(), kind: kind.into(), ..Default::default() };
        assert_eq!(t("agent").kind_label(), "Agent");
        assert_eq!(t("cron").kind_label(), "Cron");
        assert_eq!(t("").kind_label(), "Task");
    }

    #[test]
    fn since_reads_as_a_local_clock() {
        let t = BackgroundTask { id: "x".into(), kind: "shell".into(), description: "build".into(), since: "2026-11-26T13:16:50Z".into(), ..Default::default() };
        let now = at("2026-11-26T15:00:00Z");
        assert_eq!(t.timed_line(now, Clock::Twelve, |_| 0), "Shell: build · since 1:16 PM");
        assert_eq!(t.timed_line(now, Clock::Twelve, |_| -5 * 3600), "Shell: build · since 8:16 AM");
        assert_eq!(t.timed_line(at("2026-11-27T15:00:00Z"), Clock::Twelve, |_| 0), "Shell: build · since Thu 1:16 PM");
        assert_eq!(t.timed_line(at("2026-11-26T15:00:00Z"), Clock::Twelve, |_| 11 * 3600), "Shell: build · since 12:16 AM");
        let undated = BackgroundTask { since: String::new(), ..t };
        assert_eq!(undated.timed_line(now, Clock::Twelve, |_| 0), "Shell: build");
    }

    #[test]
    fn since_uses_the_clock_of_its_own_moment() {
        let t = BackgroundTask { id: "x".into(), kind: "shell".into(), description: "watch".into(), since: "2026-11-01T05:30:00Z".into(), ..Default::default() };
        let switch = at("2026-11-01T06:00:00Z");
        let eastern = |when: OffsetDateTime| if when < switch { -4 * 3600 } else { -5 * 3600 };
        assert_eq!(t.timed_line(at("2026-11-01T09:00:00Z"), Clock::Twelve, eastern), "Shell: watch · since 1:30 AM");
    }

    #[test]
    fn since_follows_the_clock_setting() {
        let t = BackgroundTask { id: "x".into(), kind: "shell".into(), description: "build".into(), since: "2026-11-26T08:05:00Z".into(), ..Default::default() };
        let now = at("2026-11-26T12:00:00Z");
        assert_eq!(t.timed_line(now, Clock::TwentyFour, |_| 0), "Shell: build · since 08:05");
        assert_eq!(t.timed_line(now, Clock::TwentyFour, |_| 9 * 3600), "Shell: build · since 17:05");
        assert_eq!(t.timed_line(now, Clock::Twelve, |_| 9 * 3600), "Shell: build · since 5:05 PM");
        assert_eq!(Clock::from_settings(false, false, "en_US"), Clock::Twelve);
        assert_eq!(Clock::from_settings(false, false, "en_GB"), Clock::TwentyFour);
        assert_eq!(Clock::from_settings(false, false, "de_DE@calendar=gregorian"), Clock::TwentyFour);
        assert_eq!(Clock::from_settings(true, false, "en_US"), Clock::TwentyFour);
        assert_eq!(Clock::from_settings(false, true, "fr_FR"), Clock::Twelve);
        assert_eq!(Clock::from_settings(false, false, "en-US.UTF-8"), Clock::Twelve);
        assert_eq!(Clock::from_settings(false, false, ""), Clock::TwentyFour);
        assert_eq!(Clock::from_settings(false, false, "en_Latn_US"), Clock::Twelve);
        assert_eq!(Clock::from_settings(false, false, "zh-Hant-TW"), Clock::Twelve);
        assert_eq!(Clock::from_settings(false, false, "ja_JP"), Clock::TwentyFour);
        assert_eq!(Clock::from_settings(false, false, "sr_Latn_RS@calendar=gregorian"), Clock::TwentyFour);
    }

    #[test]
    fn a_dismissed_dock_returns_when_the_tasks_change() {
        let one = tasks(r#"{"running":true,"tasks":[{"id":"a","kind":"shell"}],"source":"host"}"#);
        let two = tasks(r#"{"running":true,"tasks":[{"id":"b","kind":"shell"},{"id":"a","kind":"shell"}],"source":"host"}"#);
        let mut c = DockChoices::default();
        assert!(c.shows(&one, "s"));
        c.dismiss(&one, "s");
        assert!(!c.shows(&one, "s"));
        assert!(c.shows(&one, "other"));
        assert!(c.shows(&two, "s"));
        let back = DockChoices::from_json(&c.to_json());
        assert_eq!(back, c);
        assert!(!back.shows(&one, "s"));
    }

    #[test]
    fn the_dock_opens_unless_only_ambient_tasks_run() {
        let running = tasks(r#"{"running":true,"tasks":[{"id":"a","kind":"shell"}],"source":"host"}"#);
        let ambient = tasks(r#"{"running":false,"tasks":[{"id":"w","kind":"monitor","ambient":true}],"source":"host"}"#);
        let mut c = DockChoices::default();
        assert!(c.is_open(&running, "s"));
        assert!(!c.is_open(&ambient, "s"));
        c.toggle(&running, "s");
        assert!(!c.is_open(&running, "s"));
        c.toggle(&running, "s");
        assert!(c.is_open(&ambient, "s"));
    }

    #[test]
    fn choices_keep_only_the_latest_sessions() {
        let b = tasks(r#"{"running":true,"source":"host"}"#);
        let mut c = DockChoices::default();
        for i in 0..REMEMBERED + 10 {
            c.dismiss(&b, &format!("s{i}"));
        }
        assert!(c.shows(&b, "s0"));
        assert!(!c.shows(&b, &format!("s{}", REMEMBERED + 9)));
        assert_eq!(DockChoices::from_json(&c.to_json()).dismissed.len(), REMEMBERED);
        c.dismiss(&b, "s20");
        assert_eq!(c.dismissed.last().map(|(id, _)| id.as_str()), Some("s20"));
    }
}
