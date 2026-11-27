use std::collections::{HashMap, HashSet};

use serde::Deserialize;

use crate::catalog::{Client, Session, enc};
use crate::search::failure;

pub const PAGE: usize = 50;
const REREAD_PAGE: usize = 200;

pub const SCOPES: [(&str, &str); 4] = [("Working", "running"), ("Recent", "current"), ("All", "all"), ("Archived", "archived")];
pub const RECENT: usize = 1;

pub const STATUSES: [(&str, &str); 9] = [
    ("unavailable", "Unavailable"),
    ("waiting", "Waiting on you"),
    ("working", "Working"),
    ("limited", "Usage limit"),
    ("stopped", "Stopped"),
    ("finishing", "Finishing"),
    ("queued", "Queued"),
    ("offline", "Offline"),
    ("idle", "Idle"),
];

pub fn status_label(kind: &str) -> String {
    STATUSES.iter().find(|(k, _)| *k == kind).map(|(_, l)| l.to_string()).unwrap_or_else(|| kind.to_string())
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct Request {
    pub scope: String,
    pub project: Option<String>,
    pub surface: Option<String>,
    pub status: Option<String>,
    pub query: String,
}

impl Request {
    pub fn path(&self, cursor: Option<&str>, limit: usize) -> String {
        let mut path = format!("/api/v1/snapshot?scope={}&members=1&limit={limit}", enc(&self.scope));
        let query = self.query.trim();
        for (name, value) in [("projectId", self.project.as_deref()), ("surface", self.surface.as_deref()), ("status", self.status.as_deref()), ("q", Some(query))] {
            if let Some(v) = value.filter(|v| !v.is_empty()) {
                path.push_str(&format!("&{name}={}", enc(v)));
            }
        }
        if let Some(c) = cursor {
            path.push_str(&format!("&cursor={}", enc(c)));
        }
        path
    }

    pub fn refined(&self) -> bool {
        self.surface.is_some() || self.status.is_some()
    }
}

#[derive(Deserialize, Clone, Copy, Debug, Default, PartialEq)]
pub struct Count {
    #[serde(default)]
    pub families: usize,
    #[serde(default)]
    pub sessions: usize,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Summary {
    #[serde(default)]
    pub queued_messages: usize,
    #[serde(default)]
    pub attention_families: usize,
    #[serde(default)]
    pub working_families: usize,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct SurfaceCounts {
    #[serde(default)]
    pub surface: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ViewProject {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub counts: Count,
    #[serde(default, deserialize_with = "crate::catalog::null_list")]
    pub collapsed_family_ids: Vec<String>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Member {
    pub id: String,
    #[serde(default)]
    pub depth: u8,
    #[serde(default)]
    pub context_only: bool,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Family {
    pub id: String,
    #[serde(default)]
    pub root_id: String,
    #[serde(default)]
    pub project_id: String,
    #[serde(default)]
    pub checkout_id: String,
    #[serde(default, deserialize_with = "crate::catalog::null_list")]
    pub members: Vec<Member>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct View {
    #[serde(default)]
    pub counts: HashMap<String, Count>,
    #[serde(default)]
    pub summary: Summary,
    #[serde(default, deserialize_with = "crate::catalog::null_list")]
    pub surfaces: Vec<SurfaceCounts>,
    #[serde(default)]
    pub refresh_at: Option<String>,
    #[serde(default, deserialize_with = "crate::catalog::null_list")]
    pub projects: Vec<ViewProject>,
    #[serde(default, deserialize_with = "crate::catalog::null_list")]
    pub families: Vec<Family>,
}

impl View {
    fn merge(&mut self, next: View) {
        let held: HashSet<String> = self.families.iter().map(|f| f.id.clone()).collect();
        self.families.extend(next.families.into_iter().filter(|f| !held.contains(&f.id)));
    }

    pub fn count(&self, scope: &str) -> Option<usize> {
        self.counts.get(scope).map(|c| c.families)
    }
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Page {
    pub sessions: Vec<Session>,
    pub view: View,
    pub next: Option<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Listing {
    pub view: View,
    pub rows: HashMap<String, Session>,
    pub next: Option<String>,
}

impl Listing {
    pub fn first(page: Page) -> Listing {
        let mut listing = Listing { view: page.view, rows: HashMap::new(), next: page.next };
        listing.rows.extend(page.sessions.into_iter().map(|x| (x.id.clone(), x)));
        listing
    }

    pub fn append(&mut self, page: Page) {
        self.view.merge(page.view);
        self.rows.extend(page.sessions.into_iter().map(|x| (x.id.clone(), x)));
        self.next = page.next;
    }

    pub fn families(&self) -> usize {
        self.view.families.len()
    }

    pub fn remove(&mut self, id: &str) -> bool {
        self.rows.remove(id).is_some()
    }
}

fn read(client: &Client, path: &str) -> Result<Page, String> {
    let page = client.get_reply(path).map_err(|(code, body)| failure("the session list", code, &body))?;
    let sessions = serde_json::from_value(page["sessions"].clone()).map_err(|e| format!("Agenthail sent sessions this app can't read: {e}"))?;
    let view = serde_json::from_value(page["view"].clone()).map_err(|e| format!("Agenthail sent a session list this app can't read: {e}"))?;
    Ok(Page { sessions, view, next: page["nextCursor"].as_str().filter(|c| !c.is_empty()).map(String::from) })
}

impl Client {
    pub fn listing_page(&self, request: &Request, cursor: Option<&str>) -> Result<Page, String> {
        read(self, &request.path(cursor, PAGE))
    }

    pub fn listing_reread(&self, request: &Request, families: usize) -> Result<Listing, String> {
        let mut listing = Listing::first(read(self, &request.path(None, REREAD_PAGE))?);
        while listing.families() < families {
            let Some(cursor) = listing.next.clone() else { break };
            listing.append(read(self, &request.path(Some(&cursor), REREAD_PAGE))?);
        }
        Ok(listing)
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct Row<'a> {
    pub session: &'a Session,
    pub depth: u8,
    pub context_only: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub struct TreeFamily<'a> {
    pub id: String,
    pub rows: Vec<Row<'a>>,
}

impl TreeFamily<'_> {
    fn holds(&self, id: Option<&str>) -> bool {
        id.is_some_and(|id| self.rows.iter().any(|r| r.session.id == id))
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct TreeCheckout<'a> {
    pub id: String,
    pub label: String,
    pub branch: Option<String>,
    pub host: Option<String>,
    pub dirty: bool,
    pub families: Vec<TreeFamily<'a>>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct TreeProject<'a> {
    pub id: String,
    pub name: String,
    pub counts: Count,
    pub collapsed: HashSet<String>,
    pub checkouts: Vec<TreeCheckout<'a>>,
}

impl<'a> TreeProject<'a> {
    pub fn family_count(&self) -> usize {
        self.checkouts.iter().map(|c| c.families.len()).sum()
    }

    pub fn sessions_label(&self) -> String {
        if self.counts.sessions == 1 { "1 session".into() } else { format!("{} sessions", self.counts.sessions) }
    }

    pub fn shows_checkouts(&self) -> bool {
        if self.checkouts.len() < 2 {
            return false;
        }
        let branches: HashSet<&str> = self.checkouts.iter().filter_map(|c| c.branch.as_deref()).collect();
        branches.len() > 1 && branches.iter().any(|b| !matches!(*b, "main" | "master"))
    }

    pub fn shown(&self, expanded: bool, keeping: Option<&str>) -> TreeProject<'a> {
        if expanded {
            return self.clone();
        }
        let mut copy = self.clone();
        copy.checkouts = self
            .checkouts
            .iter()
            .filter_map(|c| {
                let families: Vec<TreeFamily> = c.families.iter().filter(|f| self.collapsed.contains(&f.id) || f.holds(keeping)).cloned().collect();
                (!families.is_empty()).then(|| TreeCheckout { families, ..c.clone() })
            })
            .collect();
        copy
    }

    pub fn more_label(&self, expanded: bool, keeping: Option<&str>) -> Option<String> {
        let shown = self.shown(expanded, keeping).family_count();
        let total = self.family_count();
        if total <= shown && !(expanded && total > self.collapsed.len()) {
            return None;
        }
        Some(if expanded { "Show less".into() } else { format!("Show {} more", total - shown) })
    }
}

pub fn project_choices(known: &[(String, String)]) -> Vec<(String, String)> {
    let mut seen: HashSet<&str> = HashSet::new();
    let unique: Vec<&(String, String)> = known.iter().filter(|(id, _)| !id.is_empty() && seen.insert(id)).collect();
    let mut out: Vec<(String, String)> = unique
        .iter()
        .map(|(id, name)| {
            let shared = unique.iter().filter(|(_, n)| n == name).count() > 1;
            (id.clone(), if shared { format!("{name} ({id})") } else { name.clone() })
        })
        .collect();
    out.sort_by(|a, b| a.1.to_lowercase().cmp(&b.1.to_lowercase()));
    out
}

fn branch(x: &Session) -> Option<String> {
    x.checkout.branch.clone().filter(|b| !b.is_empty())
}

fn remote(x: &Session) -> bool {
    !x.host.state.is_empty() && x.host.state != "local"
}

fn checkout_id(x: &Session, family: &Family) -> String {
    if !family.checkout_id.is_empty() {
        return family.checkout_id.clone();
    }
    let place = x.checkout_path().unwrap_or_else(|| x.project_key());
    if remote(x) { format!("{place}@{}", x.host.id) } else { place }
}

pub fn checkout_labels(roots: &[&Session]) -> Vec<String> {
    let paths: Vec<Option<Vec<&str>>> = roots.iter().map(|x| if branch(x).is_some() { None } else { x.checkout.path.as_deref().or(x.cwd.as_deref()).map(|p| p.split('/').filter(|c| !c.is_empty()).collect()) }).collect();
    let mut depth = vec![1usize; roots.len()];
    let label = |i: usize, depth: &[usize]| -> String {
        if let Some(b) = branch(roots[i]) {
            return b;
        }
        match &paths[i] {
            None => "unknown checkout".into(),
            Some(parts) if parts.is_empty() => "/".into(),
            Some(parts) => parts[parts.len().saturating_sub(depth[i])..].join("/"),
        }
    };
    loop {
        let labels: Vec<String> = (0..roots.len()).map(|i| label(i, &depth)).collect();
        let grow: Vec<usize> = (0..roots.len())
            .filter(|&i| paths[i].as_ref().is_some_and(|p| depth[i] < p.len()) && (0..roots.len()).any(|j| j != i && paths[j].is_some() && labels[j] == labels[i]))
            .collect();
        if grow.is_empty() {
            return labels;
        }
        for i in grow {
            depth[i] += 1;
        }
    }
}

pub fn tree<'a>(view: &View, row: impl Fn(&str) -> Option<&'a Session>) -> Vec<TreeProject<'a>> {
    let mut out = Vec::new();
    for p in &view.projects {
        let mut project = TreeProject { id: p.id.clone(), name: p.name.clone(), counts: p.counts, collapsed: p.collapsed_family_ids.iter().cloned().collect(), checkouts: Vec::new() };
        for f in view.families.iter().filter(|f| f.project_id == p.id) {
            let Some(root) = row(&f.root_id) else { continue };
            let mut rows = vec![Row { session: root, depth: 0, context_only: f.members.iter().any(|m| m.id == f.root_id && m.context_only) }];
            rows.extend(f.members.iter().filter(|m| m.id != f.root_id).filter_map(|m| row(&m.id).map(|x| Row { session: x, depth: m.depth.max(1), context_only: m.context_only })));
            let family = TreeFamily { id: f.id.clone(), rows };
            let id = checkout_id(root, f);
            match project.checkouts.iter_mut().find(|c| c.id == id) {
                Some(c) => c.families.push(family),
                None => project.checkouts.push(TreeCheckout { id, label: String::new(), branch: branch(root), host: remote(root).then(|| root.host.name.clone()).filter(|n| !n.is_empty()), dirty: root.checkout.dirty, families: vec![family] }),
            }
        }
        if project.checkouts.is_empty() {
            continue;
        }
        let roots: Vec<&Session> = project.checkouts.iter().map(|c| c.families[0].rows[0].session).collect();
        for (c, label) in project.checkouts.iter_mut().zip(checkout_labels(&roots)) {
            c.label = label;
        }
        out.push(project);
    }
    out
}

#[cfg(test)]
#[path = "listing_tests.rs"]
mod tests;
