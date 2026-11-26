use std::cmp::Reverse;
use std::collections::HashSet;

use crate::catalog::Session;

use super::needs::{Need, Target};
use super::sessions::{busy, hidden};
use super::text::{capital, short_title};
use super::world::World;

pub const SHOWN: usize = 5;
const LABEL_CHARS: usize = 48;

#[derive(Clone, Debug, PartialEq)]
pub struct Row {
    pub label: String,
    pub target: Target,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct MenuBar {
    pub needs_you: usize,
    pub needs: Vec<Row>,
    pub working: Vec<Row>,
    pub recent: Vec<Row>,
}

impl MenuBar {
    pub fn build(w: &World, needs: &[Need]) -> MenuBar {
        let asked: HashSet<&str> = needs
            .iter()
            .filter_map(|n| match &n.target {
                Target::Session(id) => Some(id.as_str()),
                _ => None,
            })
            .collect();
        let mut listed: Vec<&Session> = w.sessions.iter().filter(|x| !x.archived && !hidden(x) && !w.helpers.contains(&x.id) && !asked.contains(x.id.as_str())).collect();
        listed.sort_by_key(|x| Reverse(x.last_active.clone()));
        let helping: HashSet<&str> = w.sessions.iter().filter(|x| w.helpers.contains(&x.id) && !x.archived && busy(x)).filter_map(|x| x.subagent.as_ref()?.get("parentId")?.as_str()).collect();
        let (working, recent): (Vec<&Session>, Vec<&Session>) = listed.into_iter().partition(|x| busy(x) || helping.contains(x.id.as_str()));
        MenuBar {
            needs_you: needs.len(),
            needs: needs.iter().take(SHOWN).map(need_row).collect(),
            working: working.into_iter().take(SHOWN).map(|x| row(w, x, false)).collect(),
            recent: recent.into_iter().take(SHOWN).map(|x| row(w, x, true)).collect(),
        }
    }

    pub fn sections(&self) -> Vec<(String, &[Row])> {
        let needs = if self.needs_you > self.needs.len() { format!("Needs you ({}, {} shown)", self.needs_you, self.needs.len()) } else { format!("Needs you ({})", self.needs_you) };
        [(needs, &self.needs[..]), ("Working".to_string(), &self.working[..]), ("Recent".to_string(), &self.recent[..])].into_iter().filter(|(_, rows)| !rows.is_empty()).collect()
    }
}

fn need_row(n: &Need) -> Row {
    let mut label = format!("{}  ·  {}", short_title(&n.title, LABEL_CHARS), short_title(&n.who, LABEL_CHARS / 2));
    if let Some(id) = &n.label {
        label.push_str(&format!("  ·  {id}"));
    }
    Row { label, target: n.target.clone() }
}

pub fn badge(needs_you: usize) -> (u32, String) {
    let count = u32::try_from(needs_you).unwrap_or(u32::MAX);
    let said = match count {
        0 => String::new(),
        1 => "1 thing needs you".to_string(),
        n => format!("{n} things need you"),
    };
    (count, said)
}

fn row(w: &World, x: &Session, age: bool) -> Row {
    let mut label = format!("{}  ·  {}", short_title(&w.title(x), LABEL_CHARS), surface(&x.surface));
    if age {
        let ago = w.ago(&x.last_active);
        if !ago.is_empty() {
            label.push_str(&format!("  ·  {ago}"));
        }
    }
    Row { label, target: Target::Session(x.id.clone()) }
}

fn surface(name: &str) -> String {
    if name.is_empty() { "Session".to_string() } else { capital(name) }
}

pub fn item_id(target: &Target) -> String {
    match target {
        Target::Session(id) => format!("open-session:{id}"),
        Target::Work(id) => format!("open-work:{id}"),
        Target::Project(key) => format!("open-project:{key}"),
    }
}

pub fn target_of(item: &str) -> Option<Target> {
    let (kind, id) = item.split_once(':')?;
    if id.is_empty() {
        return None;
    }
    match kind {
        "open-session" => Some(Target::Session(id.to_string())),
        "open-work" => Some(Target::Work(id.to_string())),
        "open-project" => Some(Target::Project(id.to_string())),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use serde_json::{Value, json};

    use super::*;

    fn session(id: &str, extra: Value) -> Value {
        let mut s = json!({"id": id, "name": id, "surface": "claude", "lastActive": "2026-11-24T11:50:00Z", "project": {"id": "p", "name": "P"}, "display": {"kind": "idle"}});
        s.as_object_mut().unwrap().extend(extra.as_object().unwrap().clone());
        s
    }

    fn world(sessions: Vec<Value>) -> World {
        World::from_json(&json!({"updatedAt": "2026-11-24T12:00:00Z", "projects": [{"id": "p", "displayName": "P"}], "sessions": sessions}).to_string()).unwrap()
    }

    fn ids(rows: &[Row]) -> Vec<String> {
        rows.iter().map(|r| item_id(&r.target)).collect()
    }

    #[test]
    fn sessions_that_need_you_are_listed_once_under_needs_you() {
        let w = world(vec![
            session("asking", json!({"status": "busy", "attention": {"reason": "Asked a question"}, "endTurn": {"headline": "Which branch should I use?", "at": "2026-11-24T11:58:00Z"}})),
            session("busy", json!({"status": "busy", "lastActive": "2026-11-24T11:40:00Z"})),
            session("quiet", json!({"lastActive": "2026-11-24T09:00:00Z"})),
        ]);
        let needs = w.needs();
        let bar = MenuBar::build(&w, &needs);
        assert_eq!(ids(&bar.needs), ["open-session:asking"]);
        assert_eq!(ids(&bar.working), ["open-session:busy"]);
        assert_eq!(ids(&bar.recent), ["open-session:quiet"]);
        assert_eq!(bar.needs[0].label, "Which branch should I use?  ·  asking");
        assert_eq!(bar.working[0].label, "busy  ·  Claude");
        assert_eq!(bar.recent[0].label, "quiet  ·  Claude  ·  3h");
    }

    #[test]
    fn helpers_archived_and_closed_sessions_stay_out() {
        let w = world(vec![
            session("lead", json!({})),
            session("helper", json!({"subagent": {"parentId": "lead"}})),
            session("old", json!({"archived": true})),
            session("closed", json!({"display": {"kind": "idle", "label": "Closed"}})),
        ]);
        let bar = MenuBar::build(&w, &[]);
        assert_eq!(ids(&bar.recent), ["open-session:lead"]);
        assert!(bar.working.is_empty() && bar.needs.is_empty());
    }

    #[test]
    fn each_section_shows_the_newest_few_and_needs_you_counts_them_all() {
        let sessions = (0..8).map(|i| session(&format!("s{i}"), json!({"status": "busy", "lastActive": format!("2026-11-24T11:0{i}:00Z")}))).collect();
        let w = world(sessions);
        let needs: Vec<Need> = (0..7)
            .map(|i| Need { kind: super::super::NeedKind::Check, group: super::super::Group::Waiting, project: "P".into(), project_key: "p".into(), title: "Same title".into(), who: "@worker".into(), at: String::new(), target: Target::Work(format!("p-W{i}")), label: Some(format!("W{i}")) })
            .collect();
        let bar = MenuBar::build(&w, &needs);
        assert_eq!(ids(&bar.working), ["open-session:s7", "open-session:s6", "open-session:s5", "open-session:s4", "open-session:s3"]);
        assert_eq!(bar.needs.len(), SHOWN);
        let titles: Vec<String> = bar.sections().into_iter().map(|(t, _)| t).collect();
        assert_eq!(titles, ["Needs you (7, 5 shown)", "Working"]);
        assert_eq!(bar.needs[0].label, "Same title  ·  @worker  ·  W0");
        assert_ne!(bar.needs[0].label, bar.needs[1].label);
        assert_eq!(badge(bar.needs_you), (7, "7 things need you".to_string()));
    }

    #[test]
    fn a_session_whose_helper_is_working_is_listed_as_working() {
        let w = world(vec![
            session("lead", json!({})),
            session("helper", json!({"status": "busy", "subagent": {"parentId": "lead"}})),
            session("other", json!({})),
        ]);
        let bar = MenuBar::build(&w, &[]);
        assert_eq!(ids(&bar.working), ["open-session:lead"]);
        assert_eq!(ids(&bar.recent), ["open-session:other"]);
    }

    #[test]
    fn nothing_waiting_clears_the_badge() {
        let bar = MenuBar::build(&world(vec![session("quiet", json!({}))]), &[]);
        assert_eq!(badge(bar.needs_you), (0, String::new()));
        let titles: Vec<String> = bar.sections().into_iter().map(|(t, _)| t).collect();
        assert_eq!(titles, ["Recent"]);
    }

    #[test]
    fn a_menu_pick_names_what_to_open() {
        for target in [Target::Session("ses_1".into()), Target::Work("p-W2".into()), Target::Project("p".into())] {
            assert_eq!(target_of(&item_id(&target)), Some(target));
        }
        assert_eq!(target_of("open-session:"), None);
        assert_eq!(target_of("quit"), None);
    }
}
