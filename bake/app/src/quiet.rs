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

pub fn shown_minute(s: &App) -> OffsetDateTime {
    let clock = if s.fixtures.is_some() { s.ov.d.now } else { s.now.max(s.ov.d.now) };
    feed::wake::minute_of(clock)
}
