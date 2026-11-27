use std::cmp::Reverse;
use std::collections::HashSet;

use crate::catalog::Session;

use super::world::{Project, World};

pub const FILTERS: [&str; 5] = ["All", "Working", "Needs you", "Idle", "Archived"];
pub const ARCHIVED: usize = 4;
const CREW_HOURS: f64 = 24.0;

pub fn busy(x: &Session) -> bool {
    x.status == "busy" || matches!(x.display.kind.as_str(), "working" | "finishing" | "starting")
}

pub fn hidden(x: &Session) -> bool {
    x.name.trim_start().starts_with('<') && x.alias.is_none() || x.display.label == "Closed" && !busy(x)
}

pub fn rank(x: &Session, needy: &HashSet<String>) -> u8 {
    if needy.contains(&x.id) {
        0
    } else if busy(x) {
        1
    } else {
        2
    }
}

pub fn in_filter(filter: usize, x: &Session, needy: &HashSet<String>) -> bool {
    match filter {
        ARCHIVED => x.archived,
        _ if x.archived || hidden(x) => false,
        1 => busy(x),
        2 => needy.contains(&x.id),
        3 => !busy(x) && !needy.contains(&x.id),
        _ => true,
    }
}

pub struct Group<'a> {
    pub name: String,
    pub key: Option<String>,
    pub sessions: Vec<&'a Session>,
}

pub fn group_name(x: &Session) -> String {
    if x.project.is_some() { x.project_name() } else { "No project".into() }
}

pub fn groups<'a>(list: &[&'a Session], needy: &HashSet<String>) -> Vec<Group<'a>> {
    let mut out: Vec<Group> = Vec::new();
    for x in list {
        let name = group_name(x);
        match out.iter_mut().find(|g| g.name == name) {
            Some(g) => g.sessions.push(x),
            None => out.push(Group { name, key: x.project.as_ref().map(|_| x.project_key()), sessions: vec![*x] }),
        }
    }
    out.sort_by_key(|g| {
        let last = g.sessions.iter().map(|x| x.last_active.clone()).max().unwrap_or_default();
        (g.key.is_none(), g.sessions.iter().map(|x| rank(x, needy)).min().unwrap_or(9), Reverse(last))
    });
    out
}

pub fn busy_by_group(sessions: &[Session]) -> Vec<(String, usize)> {
    let mut by: Vec<(String, usize)> = Vec::new();
    for (_, name, n) in busy_by_project(sessions) {
        match by.iter_mut().find(|b| b.0 == name) {
            Some(b) => b.1 += n,
            None => by.push((name, n)),
        }
    }
    by.sort_by_key(|b| Reverse(b.1));
    by
}

pub fn busy_by_project(sessions: &[Session]) -> Vec<(String, String, usize)> {
    let mut by: Vec<(String, String, usize)> = Vec::new();
    for x in sessions.iter().filter(|x| !x.archived && busy(x)) {
        let key = x.project_key();
        match by.iter_mut().find(|b| b.0 == key) {
            Some(b) => b.2 += 1,
            None => by.push((key, group_name(x), 1)),
        }
    }
    by.sort_by_key(|b| Reverse(b.2));
    by
}

impl World {
    pub fn working(&self) -> usize {
        self.sessions.iter().filter(|x| !x.archived && busy(x)).count()
    }

    pub fn listed(&self) -> usize {
        self.sessions.iter().filter(|x| !x.archived && !hidden(x)).count()
    }

    pub fn filter_counts(&self, needy: &HashSet<String>) -> [usize; 5] {
        std::array::from_fn(|f| self.sessions.iter().filter(|x| in_filter(f, x, needy)).count())
    }

    pub fn matches_query(&self, x: &Session, query: &str) -> bool {
        let q = query.trim().to_lowercase();
        q.is_empty() || self.title(x).to_lowercase().contains(&q) || x.project_name().to_lowercase().contains(&q) || self.gist(x).to_lowercase().contains(&q)
    }

    pub fn is_lead(&self, id: &str) -> bool {
        self.projects.iter().any(|p| p.lead.as_deref() == Some(id))
    }

    pub fn role(&self, x: &Session) -> &'static str {
        if self.is_lead(&x.id) {
            "Lead"
        } else if self.title(x).to_lowercase().contains("review") {
            "Reviewer"
        } else if self.started_by.contains_key(&x.id) {
            "Worker"
        } else {
            "Session"
        }
    }

    pub fn crew(&self, lead: &str) -> Vec<&Session> {
        let mut list: Vec<&Session> = self.sessions.iter().filter(|s| self.started_by.get(&s.id).map(String::as_str) == Some(lead) && !s.archived && self.hours(&s.last_active) < CREW_HOURS).collect();
        list.sort_by(|a, b| b.last_active.cmp(&a.last_active));
        list
    }

    pub fn moving(&self, skip: &[String]) -> Vec<&Session> {
        let mut list: Vec<&Session> = self.sessions.iter().filter(|s| !s.archived && !skip.contains(&s.id) && s.project.is_some() && busy(s)).collect();
        list.sort_by(|a, b| b.last_active.cmp(&a.last_active));
        list
    }

    pub fn ordered_with_crew<'a>(&self, mut list: Vec<&'a Session>, needy: &HashSet<String>) -> Vec<(&'a Session, u8)> {
        list.sort_by(|a, b| rank(a, needy).cmp(&rank(b, needy)).then(b.last_active.cmp(&a.last_active)));
        let ids: HashSet<&str> = list.iter().map(|x| x.id.as_str()).collect();
        let parent = |x: &Session| self.started_by.get(&x.id).map(String::as_str).filter(|p| ids.contains(p) && *p != x.id);
        let mut out = Vec::new();
        let mut placed = HashSet::new();
        let mut stack: Vec<(&Session, u8)> = list.iter().rev().filter(|x| parent(x).is_none()).map(|x| (*x, 0)).collect();
        while let Some((x, depth)) = stack.pop() {
            if !placed.insert(x.id.as_str()) {
                continue;
            }
            out.push((x, depth));
            stack.extend(list.iter().rev().filter(|c| parent(c) == Some(x.id.as_str())).map(|c| (*c, depth.saturating_add(1))));
        }
        out.extend(list.iter().filter(|x| !placed.contains(x.id.as_str())).map(|x| (*x, 0)));
        out
    }

    pub fn project_rows(&self, p: &Project) -> Vec<(String, u8)> {
        let mut rows: Vec<(String, u8)> = Vec::new();
        if let Some(lead) = &p.lead {
            rows.push((lead.clone(), 0));
            rows.extend(self.crew(lead).into_iter().map(|c| (c.id.clone(), 1)));
        }
        for id in &p.sessions {
            if !rows.iter().any(|(x, _)| x == id) {
                rows.push((id.clone(), 0));
            }
        }
        rows
    }
}
