use crate::catalog::{Session, parse_time};
use crate::derive::needs::headline;
use crate::derive::World;
use crate::derive::sessions::{busy, hidden};
use crate::derive::text::{first_sentence, plain, short_title};

pub const LEADS: &str = "Project leads";
pub const SESSIONS: &str = "Sessions";
pub const SHOWN: usize = 6;

#[derive(Clone, Debug, PartialEq)]
pub struct Pick {
    pub group: &'static str,
    pub id: String,
    pub title: String,
    pub status: String,
    alias: String,
}

impl Pick {
    pub fn label(&self) -> String {
        if self.group == LEADS { format!("{}'s lead", self.title) } else { self.title.clone() }
    }

    fn has(&self, q: &str) -> bool {
        [&self.title, &self.status, &self.alias, &self.id].iter().any(|t| t.to_lowercase().contains(q))
    }
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Activity {
    pub busy: bool,
    pub state: Option<String>,
    pub latest: Option<String>,
}

fn reachable(x: &Session) -> bool {
    x.callable() && x.local() && !x.read_only && !x.archived && x.subagent.is_none()
}

impl World {
    pub fn voice_picks(&self) -> Vec<Pick> {
        let mut out: Vec<Pick> = Vec::new();
        for p in &self.projects {
            let single = self.records.iter().find(|r| r.id == p.key).is_some_and(|r| r.coordinators.len() == 1);
            let Some(x) = p.lead.as_deref().filter(|_| single).and_then(|id| self.session(id)).filter(|x| reachable(x)) else { continue };
            out.push(Pick { group: LEADS, id: x.id.clone(), title: p.name.clone(), status: p.lead_name.clone(), alias: x.alias.clone().unwrap_or_default() });
        }
        let mut rest: Vec<&Session> = self.sessions.iter().filter(|x| reachable(x) && !hidden(x) && !out.iter().any(|p| p.id == x.id)).collect();
        rest.sort_by(|a, b| b.last_active.cmp(&a.last_active));
        for x in rest {
            let state = if busy(x) { "Working" } else { "Idle" };
            let project = x.project_name();
            let status = if project.is_empty() { state.to_string() } else { format!("{state} · {project}") };
            out.push(Pick { group: SESSIONS, id: x.id.clone(), title: self.title(x), status, alias: x.alias.clone().unwrap_or_default() });
        }
        out
    }

    pub fn voice_activity(&self, id: &str) -> Option<Activity> {
        let x = self.session(id)?;
        let state = (x.display.kind != "idle" && !x.display.label.is_empty()).then(|| x.display.label.clone());
        let said = self.transcripts.get(id).and_then(|t| t.iter().rev().find(|i| i.kind == "message" && i.role_is("assistant")));
        let when = |at: Option<&str>| at.and_then(parse_time);
        let gist = match (said, headline(x)) {
            (Some(m), Some((h, at))) if when(Some(&at)) > when(m.timestamp.as_deref()) => plain(&h),
            (Some(m), _) => first_sentence(&plain(&m.text), 160),
            _ => self.gist(x),
        };
        let latest = (!gist.is_empty() && gist != x.display.label).then(|| short_title(&gist, 160));
        Some(Activity { busy: busy(x), state, latest })
    }
}

pub fn matching<'a>(picks: &'a [Pick], query: &str, skip: &[&str]) -> Vec<&'a Pick> {
    let q = query.trim().to_lowercase();
    picks.iter().filter(|p| !skip.contains(&p.id.as_str()) && (q.is_empty() || p.has(&q))).take(SHOWN).collect()
}
