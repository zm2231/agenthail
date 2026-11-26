use serde::Deserialize;
use serde_json::{Value, json};
use time::OffsetDateTime;

use crate::catalog::{Client, RespondError, Session, parse_time};
use crate::create::ConfigRoot;

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct UsageLimit {
    #[serde(default)]
    pub resets_at: Option<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MoveOffer {
    #[serde(default)]
    pub root: String,
    #[serde(default)]
    pub account: Option<String>,
    #[serde(default)]
    pub reason: Option<String>,
}

impl MoveOffer {
    pub fn name(&self) -> String {
        self.account.clone().filter(|a| !a.is_empty()).unwrap_or_else(|| self.root.clone())
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct LimitResume {
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub refused_at: Option<String>,
    #[serde(default)]
    pub resets_at: Option<String>,
    #[serde(default)]
    pub note: Option<String>,
    #[serde(default)]
    pub move_to: Option<MoveOffer>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SessionRoot {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub root: String,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Stop {
    pub label: String,
    pub waiting: bool,
    pub held: bool,
    pub offer: Option<MoveOffer>,
    pub since: String,
}

pub fn span(minutes: i64) -> String {
    match minutes {
        ..=0 => "now".into(),
        1..=59 => format!("{minutes}m"),
        60..=1439 if minutes % 60 == 0 => format!("{}h", minutes / 60),
        60..=1439 => format!("{}h {}m", minutes / 60, minutes % 60),
        _ => format!("{}d {}h", minutes / 1440, (minutes % 1440) / 60),
    }
}

fn minutes_until(at: Option<&str>, now: OffsetDateTime) -> Option<i64> {
    let at = parse_time(at?)?;
    Some(((at - now).whole_seconds() as f64 / 60.0).ceil() as i64)
}

impl Session {
    pub fn limit(&self, now: OffsetDateTime) -> Option<Stop> {
        let outranked = !matches!(self.display.kind.as_str(), "" | "limited") || self.attention.as_ref().is_some_and(|a| a.kind != "usageLimit");
        if outranked {
            return None;
        }
        if let Some(stop) = self.limit_resume.as_ref().filter(|s| s.state != "released") {
            let waiting = stop.state == "waiting";
            let label = match (stop.note.as_deref().map(str::trim).filter(|n| !n.is_empty()), waiting, minutes_until(stop.resets_at.as_deref(), now)) {
                (Some(note), _, _) => note.to_string(),
                (None, false, _) => "Stopped by usage limit".into(),
                (None, true, None) => "Usage limit, resumes when it lifts".into(),
                (None, true, Some(m)) if m <= 0 => "Usage limit, resuming now".into(),
                (None, true, Some(m)) => format!("Usage limit, resumes in {}", span(m)),
            };
            return Some(Stop { label, waiting, held: true, offer: stop.move_to.clone().filter(|o| !o.root.is_empty()), since: stop.refused_at.clone().unwrap_or_default() });
        }
        let limit = self.usage_limit.as_ref()?;
        let label = match minutes_until(limit.resets_at.as_deref(), now) {
            Some(m) if m <= 0 => return None,
            Some(m) => format!("Usage limit, resets in {}", span(m)),
            None => "Usage limit reached".into(),
        };
        Some(Stop { label, waiting: false, held: false, offer: None, since: limit.resets_at.clone().unwrap_or_default() })
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct Target {
    pub root: String,
    pub name: String,
    pub detail: Option<String>,
    pub full: bool,
    pub offered: bool,
}

pub fn carry_usage(fresh: &mut [ConfigRoot], known: &[ConfigRoot]) {
    for root in fresh.iter_mut().filter(|r| r.usage.is_none() && !r.full) {
        if let Some(old) = known.iter().find(|k| k.same_account(root)) {
            root.usage = old.usage.clone();
            root.best = old.best.clone();
            root.full = old.full;
            root.full_until = old.full_until.clone();
        }
    }
}

pub fn targets(stop: &Stop, current: Option<&str>, roots: &[ConfigRoot], now: OffsetDateTime) -> Vec<Target> {
    let mut out = Vec::new();
    if let Some(offer) = &stop.offer {
        let known = roots.iter().find(|r| r.root == offer.root);
        let usage = known.and_then(|r| r.usage_line(now));
        let detail = [offer.reason.clone().filter(|r| !r.is_empty()), usage].into_iter().flatten().collect::<Vec<_>>().join(" · ");
        out.push(Target { root: offer.root.clone(), name: offer.name(), detail: (!detail.is_empty()).then_some(detail), full: known.is_some_and(|r| r.spent(now)), offered: true });
    }
    let offered = stop.offer.as_ref().map(|o| o.root.as_str());
    for root in roots.iter().filter(|r| r.selectable() && Some(r.root.as_str()) != current && Some(r.root.as_str()) != offered) {
        out.push(Target { root: root.root.clone(), name: root.title(), detail: root.usage_line(now), full: root.spent(now), offered: false });
    }
    out
}

#[derive(Clone, Debug, PartialEq)]
pub enum Moved {
    Done,
    NeedsConsent(String),
}

impl Client {
    pub fn limit_move(&self, session: &str, config_root: Option<&str>, stop_terminal: bool) -> Result<Moved, RespondError> {
        let mut payload = json!({"sessionId": session, "stopTerminal": stop_terminal});
        if let Some(root) = config_root {
            payload["configRoot"] = Value::String(root.into());
        }
        match self.action("limit-move", payload) {
            Ok(_) => Ok(Moved::Done),
            Err(error) if error.code == "move_needs_consent" => Ok(Moved::NeedsConsent(error.message)),
            Err(error) => Err(error),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn at(ts: &str) -> OffsetDateTime {
        parse_time(ts).unwrap()
    }

    fn session(extra: Value) -> Session {
        let mut base = json!({"id": "s1", "name": "s1"});
        base.as_object_mut().unwrap().extend(extra.as_object().unwrap().clone());
        serde_json::from_value(base).unwrap()
    }

    #[test]
    fn a_stop_the_computer_holds_reads_as_the_native_app_shows_it() {
        let now = at("2026-11-26T10:00:00Z");
        let waiting = session(json!({"limitResume": {"state": "waiting", "refusedAt": "2026-11-26T09:00:00Z", "resetsAt": "2026-11-26T12:30:00Z", "moveTo": {"root": "/r/b", "account": "b@example.com", "reason": "most room"}}}));
        let stop = waiting.limit(now).unwrap();
        assert_eq!(stop.label, "Usage limit, resumes in 2h 30m");
        assert!(stop.waiting && stop.held);
        assert_eq!(stop.offer.as_ref().map(MoveOffer::name).as_deref(), Some("b@example.com"));
        let unknown = session(json!({"limitResume": {"state": "waiting"}}));
        assert_eq!(unknown.limit(now).unwrap().label, "Usage limit, resumes when it lifts");
        let canceled = session(json!({"limitResume": {"state": "canceled", "resetsAt": "2026-11-26T12:30:00Z"}}));
        let stop = canceled.limit(now).unwrap();
        assert_eq!((stop.label.as_str(), stop.waiting, stop.held), ("Stopped by usage limit", false, true));
        let noted = session(json!({"limitResume": {"state": "lapsed", "note": " Resumed on another account "}}));
        assert_eq!(noted.limit(now).unwrap().label, "Resumed on another account");
        assert_eq!(session(json!({"limitResume": {"state": "released"}})).limit(now), None);
    }

    #[test]
    fn a_limit_without_a_held_stop_offers_nothing_and_ends_at_its_reset() {
        let now = at("2026-11-26T10:00:00Z");
        let limited = session(json!({"usageLimit": {"resetsAt": "2026-11-26T10:20:00Z"}}));
        let stop = limited.limit(now).unwrap();
        assert_eq!((stop.label.as_str(), stop.held, stop.offer), ("Usage limit, resets in 20m", false, None));
        assert_eq!(limited.limit(at("2026-11-26T10:20:00Z")), None);
        assert_eq!(session(json!({"usageLimit": {}})).limit(now).unwrap().label, "Usage limit reached");
        assert_eq!(session(json!({})).limit(now), None);
    }

    #[test]
    fn a_limited_rows_summary_is_its_limit_not_the_attention_reason() {
        let x = session(json!({"attention": {"kind": "usageLimit", "reason": "Stopped; automatic resume canceled"}, "limitResume": {"state": "canceled"}}));
        assert_eq!(x.summary(), "Stopped by usage limit");
        let lifted = session(json!({"attention": {"kind": "stopped", "reason": "API error"}, "usageLimit": {"resetsAt": "2020-01-01T00:00:00Z"}}));
        assert_eq!(lifted.summary(), "API error");
    }

    #[test]
    fn what_the_daemon_ranks_above_a_limit_hides_it() {
        let now = at("2026-11-26T10:00:00Z");
        let stop = json!({"state": "waiting", "resetsAt": "2026-11-26T12:00:00Z"});
        assert!(session(json!({"display": {"kind": "limited"}, "attention": {"kind": "usageLimit"}, "limitResume": stop})).limit(now).is_some());
        for display in ["working", "waiting", "unavailable"] {
            assert_eq!(session(json!({"display": {"kind": display}, "limitResume": stop})).limit(now), None, "{display}");
        }
        let asked = session(json!({"display": {"kind": "limited"}, "attention": {"kind": "ask", "reason": "Which branch?"}, "limitResume": stop}));
        assert_eq!((asked.limit(now), asked.summary()), (None, "Which branch?".to_string()));
    }

    #[test]
    fn a_refreshed_account_list_keeps_what_was_known_until_new_readings_arrive() {
        let mut full = root("c", "/r/c", Some("c@example.com"));
        full.full = true;
        let mut fresh = vec![root("c", "/r/c", Some("c@example.com")), root("d", "/r/d", Some("d@example.com"))];
        carry_usage(&mut fresh, &[full.clone()]);
        assert_eq!(fresh.iter().map(|r| r.full).collect::<Vec<_>>(), [true, false]);
        let mut switched = vec![root("c", "/r/c", Some("other@example.com"))];
        carry_usage(&mut switched, &[full]);
        assert!(!switched[0].full, "another account signed in at the same root starts fresh");
    }

    #[test]
    fn a_stop_is_known_by_when_it_began() {
        let now = at("2026-11-26T10:00:00Z");
        let x = session(json!({"limitResume": {"state": "waiting", "refusedAt": "2026-11-26T09:00:00Z"}}));
        assert_eq!(x.limit(now).unwrap().since, "2026-11-26T09:00:00Z");
    }

    #[test]
    fn spans_read_like_the_status_bar() {
        assert_eq!([span(0), span(1), span(60), span(61), span(1440 + 90)], ["now", "1m", "1h", "1h 1m", "1d 1h"]);
    }

    fn root(name: &str, path: &str, email: Option<&str>) -> ConfigRoot {
        let mut value = json!({"name": name, "root": path});
        if let Some(email) = email {
            value["signedInAs"] = json!({"email": email});
        }
        serde_json::from_value(value).unwrap()
    }

    #[test]
    fn the_daemons_pick_comes_first_and_the_current_account_is_left_out() {
        let stop = Stop { label: String::new(), waiting: true, held: true, offer: Some(MoveOffer { root: "/r/b".into(), account: Some("b@example.com".into()), reason: Some("most room".into()) }), since: String::new() };
        let mut b = root("b", "/r/b", Some("b@example.com"));
        b.take_usage(serde_json::from_value(json!({"root": "/r/b", "usage": {"limits": [{"kind": "5h", "percent": 12}]}})).unwrap());
        let mut c = root("c", "/r/c", Some("c@example.com"));
        c.full = true;
        let roots = vec![root("a", "/r/a", Some("a@example.com")), b, c, root("x", "/r/x", None)];
        let now = at("2026-11-26T10:00:00Z");
        let got = targets(&stop, Some("/r/a"), &roots, now);
        assert_eq!(got.iter().map(|t| (t.root.as_str(), t.name.as_str(), t.offered, t.full)).collect::<Vec<_>>(), [("/r/b", "b@example.com", true, false), ("/r/c", "c", false, true)]);
        assert_eq!(got[0].detail.as_deref(), Some("most room · session 12%"));
        let mut spent = roots.clone();
        spent[1].full = true;
        assert!(targets(&stop, Some("/r/a"), &spent, now)[0].full, "a suggested account that has run out can't be picked");
        let mut exhausted = roots.clone();
        exhausted[0].take_usage(serde_json::from_value(json!({"root": "/r/a", "usage": {"limits": [{"kind": "5h", "percent": 100, "resetsAt": "2026-11-26T11:00:00Z"}]}})).unwrap());
        assert!(targets(&Stop { offer: None, ..stop.clone() }, None, &exhausted, now)[0].full, "a direct account at its limit can't be picked");
        let no_offer = Stop { offer: None, ..stop };
        assert_eq!(targets(&no_offer, None, &roots, now).iter().map(|t| t.root.as_str()).collect::<Vec<_>>(), ["/r/a", "/r/b", "/r/c"]);
    }
}
