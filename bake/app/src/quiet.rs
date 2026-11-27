use time::OffsetDateTime;

use crate::{App, Screen};

#[derive(PartialEq)]
pub struct Outside {
    sessions: *const Vec<feed::catalog::Session>,
    screen: Screen,
    selected: Option<String>,
    problem: Option<String>,
    status: String,
    activity_missing: bool,
    tabs: feed::tabs::Tabs,
    archived: Option<(String, String, bool)>,
    ready: Vec<String>,
}

pub fn outside(s: &App) -> Outside {
    Outside {
        sessions: std::sync::Arc::as_ptr(&s.sessions),
        screen: s.screen.clone(),
        selected: s.selected.clone(),
        problem: s.link.problem.as_ref().map(|p| p.detail()),
        status: s.status.clone(),
        activity_missing: s.earlier.activity_missing,
        tabs: s.tabs.tabs.clone(),
        archived: s.tabs.archived.as_ref().map(|a| (a.id.clone(), a.title.clone(), a.unarchiving)),
        ready: crate::ui::ready::counted(s),
    }
}

pub fn wall(s: &App) -> OffsetDateTime {
    if s.fixtures.is_some() { s.ov.d.now } else { OffsetDateTime::now_utc() }
}

pub fn deadlines(s: &App) -> Vec<OffsetDateTime> {
    let roots = s.limits.roots.values().flat_map(|r| r.iter().flatten()).flat_map(|r| r.deadlines());
    s.sessions.iter().flat_map(|x| x.deadlines()).chain(roots).collect()
}

pub fn shown_minute(s: &App) -> Option<OffsetDateTime> {
    s.fixtures.is_none().then(|| feed::wake::minute_of(s.now))
}
