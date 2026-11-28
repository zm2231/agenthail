use serde::Deserialize;
use serde_json::json;
use time::{Duration, OffsetDateTime, Time, UtcOffset};

use crate::background::Clock;
use crate::catalog::{Client, RespondError, Session, parse_time};

#[derive(Deserialize, Clone, Debug, Default, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct Snooze {
    #[serde(default)]
    pub snoozed_at: String,
    #[serde(default)]
    pub until: String,
    #[serde(default)]
    pub wake: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Activity {
    #[serde(default)]
    pub busy_since: String,
    #[serde(default)]
    pub turn_started_at: String,
}

impl Activity {
    fn turn_start(&self) -> Option<OffsetDateTime> {
        parse_time(&self.turn_started_at).or_else(|| parse_time(&self.busy_since))
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum For {
    Hour,
    Morning,
    TurnEnds,
}

impl For {
    pub fn label(self) -> &'static str {
        match self {
            For::Hour => "For an hour",
            For::Morning => "Until tomorrow morning",
            For::TurnEnds => "Until this turn finishes",
        }
    }
}

const MORNING: Time = match Time::from_hms(9, 0, 0) {
    Ok(t) => t,
    Err(_) => Time::MIDNIGHT,
};

fn running(x: &Session) -> bool {
    matches!(x.display.kind.as_str(), "working" | "finishing" | "queued")
}

/// The daemon offers "until this turn finishes" only while the session is busy.
pub fn choices(x: &Session) -> Vec<For> {
    let mut out = vec![For::Hour, For::Morning];
    if x.status == "busy" {
        out.push(For::TurnEnds);
    }
    out
}

pub fn until(choice: For, now: OffsetDateTime, offset_at: impl Fn(OffsetDateTime) -> UtcOffset) -> Option<OffsetDateTime> {
    match choice {
        For::Hour => Some(now + Duration::hours(1)),
        For::Morning => {
            let morning = now.to_offset(offset_at(now)).date().next_day()?.with_time(MORNING);
            let guess = morning.assume_offset(offset_at(now));
            Some(morning.assume_offset(offset_at(guess)).to_offset(UtcOffset::UTC))
        }
        For::TurnEnds => None,
    }
}

impl Session {
    pub fn snoozed(&self, now: OffsetDateTime) -> bool {
        let Some(snooze) = &self.snooze else { return false };
        match snooze.wake.as_str() {
            "time" => parse_time(&snooze.until).is_some_and(|until| now < until),
            "finished" => {
                if !running(self) {
                    return false;
                }
                let start = self.activity.as_ref().and_then(Activity::turn_start);
                let at = parse_time(&snooze.snoozed_at);
                match (start, at) {
                    (None, _) => true,
                    (Some(start), Some(at)) => start.replace_nanosecond(start.millisecond() as u32 * 1_000_000).unwrap_or(start) <= at,
                    (Some(_), None) => false,
                }
            }
            _ => false,
        }
    }
}

fn moment(at: OffsetDateTime, now: OffsetDateTime, clock: Clock) -> String {
    let shown = clock.show(at.hour(), at.minute());
    if at.date() == now.date() {
        shown
    } else if now.date().next_day() == Some(at.date()) {
        format!("tomorrow {shown}")
    } else {
        let day = at.weekday().to_string();
        format!("{} {shown}", &day[..3])
    }
}

pub fn note(x: &Session, now: OffsetDateTime, clock: Clock, offset_at: impl Fn(OffsetDateTime) -> i32) -> Option<String> {
    if !x.snoozed(now) {
        return None;
    }
    let snooze = x.snooze.as_ref()?;
    if snooze.wake == "finished" {
        return Some("Snoozed until this turn finishes".into());
    }
    let until = parse_time(&snooze.until)?;
    let local = |t: OffsetDateTime| t.to_offset(UtcOffset::from_whole_seconds(offset_at(t)).unwrap_or(UtcOffset::UTC));
    Some(format!("Snoozed until {}", moment(local(until), local(now), clock)))
}

fn stamp(at: OffsetDateTime) -> String {
    at.format(&time::format_description::well_known::Rfc3339).unwrap_or_default()
}

impl Client {
    pub fn session_snooze(&self, session: &str, until: Option<OffsetDateTime>) -> Result<Option<Snooze>, RespondError> {
        let payload = match until {
            Some(at) => json!({"sessionId": session, "until": stamp(at)}),
            None => json!({"sessionId": session, "wake": "finished"}),
        };
        let reply = crate::catalog::confirmed(self.action("session-snooze", payload)?)?;
        Ok(crate::reply::parse(reply["snooze"].clone()).ok())
    }

    pub fn session_wake(&self, session: &str) -> Result<(), RespondError> {
        self.action("session-wake", json!({"sessionId": session})).and_then(crate::catalog::confirmed).map(drop)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::Display;
    use crate::mock::{daemon, replies};
    fn t(ts: &str) -> OffsetDateTime {
        parse_time(ts).unwrap()
    }

    fn snoozed(wake: &str, at: &str, until: &str) -> Session {
        Session { id: "s1".into(), snooze: Some(Snooze { snoozed_at: at.into(), until: until.into(), wake: wake.into() }), ..Session::default() }
    }

    #[test]
    fn a_timed_snooze_holds_until_its_time() {
        let x = snoozed("time", "2026-11-26T10:00:00Z", "2026-11-26T11:00:00Z");
        assert!(x.snoozed(t("2026-11-26T10:59:59Z")));
        assert!(!x.snoozed(t("2026-11-26T11:00:00Z")));
        assert!(!Session::default().snoozed(t("2026-11-26T10:00:00Z")));
    }

    #[test]
    fn a_turn_snooze_holds_while_the_turn_it_was_set_in_runs() {
        let now = t("2026-11-26T10:30:00Z");
        let mut x = snoozed("finished", "2026-11-26T10:05:00.250Z", "");
        assert!(!x.snoozed(now), "an idle session has no turn to wait for");
        x.display = Display { kind: "working".into(), ..Display::default() };
        assert!(x.snoozed(now), "no turn start reported reads as the same turn");
        x.activity = Some(Activity { turn_started_at: "2026-11-26T10:00:00Z".into(), ..Activity::default() });
        assert!(x.snoozed(now));
        x.activity = Some(Activity { turn_started_at: "2026-11-26T10:05:00.250900Z".into(), ..Activity::default() });
        assert!(x.snoozed(now), "the daemon compares at millisecond precision");
        x.activity = Some(Activity { turn_started_at: "2026-11-26T10:06:00Z".into(), ..Activity::default() });
        assert!(!x.snoozed(now), "a later turn wakes it");
        x.activity = Some(Activity { busy_since: "2026-11-26T10:01:00Z".into(), ..Activity::default() });
        assert!(x.snoozed(now), "busySince stands in when the turn start is missing");
    }

    #[test]
    fn until_this_turn_finishes_is_offered_only_while_busy() {
        let mut x = Session::default();
        assert_eq!(choices(&x), vec![For::Hour, For::Morning]);
        x.status = "busy".into();
        assert_eq!(choices(&x), vec![For::Hour, For::Morning, For::TurnEnds]);
    }

    #[test]
    fn tomorrow_morning_is_nine_on_the_local_clock() {
        let now = t("2026-11-27T03:30:00Z");
        let offset = |_| UtcOffset::from_hms(-5, 0, 0).unwrap();
        assert_eq!(until(For::Morning, now, offset), Some(t("2026-11-27T14:00:00Z")));
        assert_eq!(until(For::Hour, now, offset), Some(t("2026-11-27T04:30:00Z")));
        assert_eq!(until(For::TurnEnds, now, offset), None);
    }

    #[test]
    fn tomorrow_morning_takes_the_clock_change_overnight_into_account() {
        let spring = |at: OffsetDateTime| UtcOffset::from_hms(if at < t("2026-03-08T07:00:00Z") { -8 } else { -7 }, 0, 0).unwrap();
        assert_eq!(until(For::Morning, t("2026-03-07T20:00:00Z"), spring), Some(t("2026-03-08T16:00:00Z")));
        let fall = |at: OffsetDateTime| UtcOffset::from_hms(if at < t("2026-11-01T09:00:00Z") { -7 } else { -8 }, 0, 0).unwrap();
        assert_eq!(until(For::Morning, t("2026-10-31T20:00:00Z"), fall), Some(t("2026-11-01T17:00:00Z")));
    }

    #[test]
    fn the_note_says_when_it_wakes_on_the_local_clock() {
        let now = t("2026-11-26T15:00:00Z");
        let utc = |_| 0;
        let x = snoozed("time", "2026-11-26T15:00:00Z", "2026-11-26T16:40:00Z");
        assert_eq!(note(&x, now, Clock::Twelve, utc).as_deref(), Some("Snoozed until 4:40 PM"));
        let x = snoozed("time", "2026-11-26T15:00:00Z", "2026-11-27T09:00:00Z");
        assert_eq!(note(&x, now, Clock::TwentyFour, utc).as_deref(), Some("Snoozed until tomorrow 09:00"));
        let x = snoozed("time", "2026-11-26T15:00:00Z", "2026-11-30T09:00:00Z");
        assert_eq!(note(&x, now, Clock::TwentyFour, utc).as_deref(), Some("Snoozed until Mon 09:00"));
        assert_eq!(note(&x, now, Clock::TwentyFour, |_| -5 * 3600).as_deref(), Some("Snoozed until Mon 04:00"));
        let mut x = snoozed("finished", "2026-11-26T15:00:00Z", "");
        x.display.kind = "working".into();
        assert_eq!(note(&x, now, Clock::Twelve, utc).as_deref(), Some("Snoozed until this turn finishes"));
        let x = snoozed("time", "2026-11-26T10:00:00Z", "2026-11-26T11:00:00Z");
        assert_eq!(note(&x, now, Clock::Twelve, utc), None);
    }

    #[test]
    fn snooze_and_wake_send_what_the_daemon_reads() {
        let (client, seen) = daemon(replies(&[
            (200, r#"{"ok":true,"sessionId":"s1","snooze":{"snoozedAt":"2026-11-26T10:00:00Z","until":"2026-11-26T11:00:00Z","wake":"time"}}"#),
            (200, r#"{"ok":true,"sessionId":"s1","snooze":{"snoozedAt":"2026-11-26T10:00:00Z","wake":"finished"}}"#),
            (200, r#"{"ok":true,"sessionId":"s1","snooze":null}"#),
            (409, r#"{"error":{"code":"conflict","message":"the session is not working, so there is no turn to wait for"}}"#),
            (202, r#"{"ok":true,"status":"submitted"}"#),
            (202, r#"{"ok":true,"status":"submitted"}"#),
        ]));
        let got = client.session_snooze("s1", Some(t("2026-11-26T11:00:00Z"))).unwrap().unwrap();
        assert_eq!((got.wake.as_str(), got.until.as_str()), ("time", "2026-11-26T11:00:00Z"));
        let (path, body) = seen.recv().unwrap();
        assert_eq!(path, "/api/v1/actions");
        assert_eq!(body, json!({"action": "session-snooze", "sessionId": "s1", "until": "2026-11-26T11:00:00Z"}));
        assert_eq!(client.session_snooze("s1", None).unwrap().unwrap().wake, "finished");
        assert_eq!(seen.recv().unwrap().1, json!({"action": "session-snooze", "sessionId": "s1", "wake": "finished"}));
        client.session_wake("s1").unwrap();
        assert_eq!(seen.recv().unwrap().1, json!({"action": "session-wake", "sessionId": "s1"}));
        let refused = client.session_snooze("s1", None).unwrap_err();
        assert_eq!(refused.message, "the session is not working, so there is no turn to wait for");
        assert!(client.session_snooze("s1", None).unwrap_err().maybe_delivered, "a submitted snooze is not shown as landed");
        assert!(client.session_wake("s1").unwrap_err().maybe_delivered);
    }
}
