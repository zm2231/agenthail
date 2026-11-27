use std::cmp::Ordering;
use std::collections::BTreeSet;

use serde::{Deserialize, Serialize};
use serde_json::{Value, json};

use crate::catalog::{Client, RespondError, confirmed};
use crate::derive::world::{Coordinator, ProjectRecord};

type Outcome<T> = std::result::Result<T, RespondError>;

pub const NAME_LIMIT: usize = 120;
pub const TOOLS_HELP: &str = "Claude sessions Agenthail did not start, such as Claude in a terminal";

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Place {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub state: String,
}

impl Place {
    pub fn local(&self) -> bool {
        self.state == "local"
    }

    pub fn reachable(&self) -> bool {
        matches!(self.state.as_str(), "local" | "connected")
    }

    pub fn target(&self) -> &str {
        if self.local() { "" } else { &self.id }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Checkout {
    #[serde(default)]
    pub host: Place,
    #[serde(default)]
    pub project_id: String,
    #[serde(default)]
    pub root_path: String,
    #[serde(default)]
    pub added: bool,
    #[serde(default)]
    pub archived: bool,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Item {
    NewConversation { host: String, folder: String },
    Rename,
    Lead { change: bool },
    ClearLead,
    Pin(bool),
    Earlier,
    Later,
    Tools { off: bool },
    Archive(bool),
    Remove { project_id: String, place: String },
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Entry {
    pub item: Item,
    pub label: String,
    pub note: Option<String>,
    pub enabled: bool,
}

impl Entry {
    fn on(item: Item, label: impl Into<String>) -> Entry {
        Entry { item, label: label.into(), note: None, enabled: true }
    }
}

impl ProjectRecord {
    pub fn name(&self) -> &str {
        if self.display_name.is_empty() { &self.id } else { &self.display_name }
    }

    pub fn tools_off(&self) -> Option<bool> {
        self.mcp.as_deref().map(|m| m == "off")
    }

    pub fn tools_settable(&self) -> bool {
        !self.id.contains("::")
    }

    pub fn lead_places(&self) -> Vec<&Checkout> {
        self.checkouts.iter().filter(|c| !c.archived && c.host.reachable()).collect()
    }

    pub fn place(&self, checkout: &Checkout) -> String {
        let hosts: BTreeSet<&str> = self.checkouts.iter().map(|c| c.host.id.as_str()).collect();
        if hosts.len() > 1 || !checkout.host.local() {
            let labels = host_labels(self.checkouts.iter().map(|c| (c.host.id.as_str(), c.host.name.as_str())));
            format!(" on {}", labels.get(&checkout.host.id).cloned().unwrap_or_else(|| checkout.host.name.clone()))
        } else {
            String::new()
        }
    }

    pub fn start_place(&self) -> Option<&Checkout> {
        let open: Vec<&Checkout> = self.checkouts.iter().filter(|c| !c.archived && c.host.reachable()).collect();
        open.iter().find(|c| c.host.local() && c.added).or_else(|| open.iter().find(|c| c.host.local())).or_else(|| open.first()).copied()
    }

    pub fn lead_note(&self) -> String {
        if self.coordinators.is_empty() {
            return "Starts a lead conversation. Its first message starts it.".into();
        }
        let names: Vec<String> = self.coordinators.iter().map(|c| format!("@{}", c.name)).collect();
        format!("Starts a new lead in place of {}. To make an existing conversation the lead, use Make Project Lead on it.", names.join(" and "))
    }
}

pub fn entries(p: &ProjectRecord, tiles: &Arrangement, arranged: &[String]) -> Vec<Vec<Entry>> {
    let mut lead = Vec::new();
    if p.checkouts.iter().any(|c| c.added) {
        lead.push(Entry::on(Item::Rename, "Rename…"));
    }
    if !p.archived {
        for c in p.checkouts.iter().filter(|c| !c.archived) {
            lead.push(Entry::on(Item::NewConversation { host: c.host.target().to_string(), folder: c.root_path.clone() }, format!("New conversation{}", p.place(c))));
        }
        let change = !p.coordinators.is_empty();
        lead.push(Entry::on(Item::Lead { change }, if change { "Change Lead…" } else { "Set Lead…" }));
        if p.coordinators.len() == 1 {
            lead.push(Entry::on(Item::ClearLead, "Clear Lead"));
        }
    }
    let mut order = Vec::new();
    if !p.archived && arranged.iter().any(|id| *id == p.id) {
        let pinned = tiles.pinned.contains(&p.id);
        order.push(Entry::on(Item::Pin(!pinned), if pinned { "Unpin" } else { "Pin to Front" }));
        for (item, label, by) in [(Item::Earlier, "Move Earlier", -1), (Item::Later, "Move Later", 1)] {
            let mut entry = Entry::on(item, label);
            entry.enabled = tiles.resolving(&TileChange::Step(p.id.clone(), by), arranged).is_some();
            order.push(entry);
        }
    }
    let mut keep = Vec::new();
    if let Some(off) = p.tools_off().filter(|_| p.tools_settable()) {
        let mut tools = Entry::on(Item::Tools { off }, if off { "Give Terminal Sessions the Agenthail Tools" } else { "Keep the Agenthail Tools from Terminal Sessions" });
        tools.note = Some(TOOLS_HELP.into());
        keep.push(tools);
    }
    keep.push(Entry::on(Item::Archive(!p.archived), if p.archived { "Unarchive Project" } else { "Archive Project…" }));
    for c in p.checkouts.iter().filter(|c| c.added) {
        let place = p.place(c);
        keep.push(Entry::on(Item::Remove { project_id: c.project_id.clone(), place: place.clone() }, format!("Remove{place}…")));
    }
    [lead, order, keep].into_iter().filter(|g| !g.is_empty()).collect()
}

pub fn short_host(name: &str) -> &str {
    name.split('.').next().unwrap_or(name)
}

pub fn host_labels<'a>(hosts: impl Iterator<Item = (&'a str, &'a str)>) -> std::collections::HashMap<String, String> {
    let mut names: Vec<(&str, &str)> = Vec::new();
    for (id, name) in hosts {
        if !names.iter().any(|(i, _)| *i == id) {
            names.push((id, name));
        }
    }
    let count = |f: &dyn Fn(&str) -> &str, n: &str| names.iter().filter(|(_, x)| f(x) == f(n)).count();
    names
        .iter()
        .map(|(id, name)| {
            let label = if count(&|x| x, name) > 1 {
                format!("{name} ({id})")
            } else if count(&short_host, name) > 1 {
                name.to_string()
            } else {
                short_host(name).to_string()
            };
            (id.to_string(), label)
        })
        .collect()
}

pub fn lead_session_id(c: &Coordinator) -> Option<String> {
    let id = c.session_id.as_deref().filter(|id| !id.is_empty())?;
    match &c.host {
        Some(h) if !h.local() && !id.contains("::") => Some(format!("{}::{id}", h.id)),
        _ => Some(id.to_string()),
    }
}

pub fn lead_note(c: &Coordinator) -> &'static str {
    if c.session_id.as_deref().is_none_or(str::is_empty) { "starts with the first message sent to it" } else { "not running · the next message picks it up" }
}

pub fn surface_label(surface: &str) -> String {
    match surface {
        "claude" => "Claude".into(),
        "codex" => "Codex".into(),
        other => crate::create::capitalized(other),
    }
}

pub fn tools_changed(name: &str, on: bool) -> String {
    if on { format!("Terminal sessions in {name} get the Agenthail tools.") } else { format!("Terminal sessions in {name} no longer get the Agenthail tools.") }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Confirm {
    pub message: String,
    pub action: &'static str,
}

pub fn confirm_clear_lead(name: &str) -> Confirm {
    Confirm { message: format!("Clear the lead of {name}? Its conversation and name stay."), action: "Clear Lead" }
}

pub fn confirm_archive(name: &str) -> Confirm {
    Confirm { message: format!("Archive {name}? It leaves the Projects list until you unarchive it. Its sessions and folders stay."), action: "Archive" }
}

pub fn confirm_remove(name: &str, place: &str) -> Confirm {
    Confirm { message: format!("Remove {name}{place} from Agenthail? Its folder and sessions stay; its sessions go back to the folder's own project."), action: "Remove" }
}

pub fn confirm_replace(error: &RespondError) -> Confirm {
    Confirm { message: format!("{}. Replace it?", error.message.trim_end_matches('.')), action: "Replace" }
}

pub fn needs_replace(error: &RespondError) -> bool {
    matches!(error.code.as_str(), "coordinator_exists" | "coordinator_conflict")
}

pub fn rename_problem(title: &str, current: &str) -> Option<&'static str> {
    let t = title.trim();
    if t.is_empty() {
        Some("A project needs a name.")
    } else if t.len() > NAME_LIMIT {
        Some("A project name is too long.")
    } else if t == current {
        Some("")
    } else {
        None
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Lead {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub project_root: String,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Added {
    pub name: String,
    pub created: bool,
}

impl Added {
    pub fn notice(&self) -> String {
        if self.created { format!("Added project {}.", self.name) } else { format!("{} is already a project.", self.name) }
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Hint {
    #[serde(default)]
    pub reason: String,
    #[serde(default)]
    pub command: String,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Worktree {
    #[serde(default)]
    pub path: String,
    #[serde(default)]
    pub branch: String,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub settled_reason: String,
    #[serde(default)]
    pub cleanup: String,
    #[serde(default)]
    pub cleanup_reason: String,
    #[serde(default)]
    pub kept_paths: Vec<String>,
    #[serde(default)]
    pub hint: Option<Hint>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Offer {
    Remove,
    Check,
    Ignored(Vec<String>),
}

impl Worktree {
    pub fn merged(&self) -> bool {
        self.state == "settled" && self.settled_reason == "merged" && self.cleanup != "removed"
    }

    pub fn name(&self) -> &str {
        if self.branch.is_empty() { &self.path } else { &self.branch }
    }

    pub fn offer(&self) -> Offer {
        if self.cleanup != "kept" {
            Offer::Remove
        } else if self.cleanup_reason == "ignored" && !self.kept_paths.is_empty() {
            Offer::Ignored(self.kept_paths.clone())
        } else {
            Offer::Check
        }
    }

    pub fn outcome(&self) -> String {
        if self.cleanup == "removed" {
            return format!("Removed {}.", if self.branch.is_empty() { "the worktree" } else { &self.branch });
        }
        match &self.hint {
            Some(h) => format!("Kept: {}", h.reason),
            None => "Kept.".into(),
        }
    }
}

pub fn confirm_worktree(w: &Worktree) -> Confirm {
    Confirm { message: format!("Remove {}? Its folder and branch are deleted if nothing in them would be lost.", w.name()), action: "Remove" }
}

pub fn confirm_ignored(w: &Worktree, paths: &[String]) -> Confirm {
    let count = if paths.len() == 1 { "this ignored file".to_string() } else { format!("these {} ignored files", paths.len()) };
    Confirm { message: format!("Remove {} anyway? It deletes {count} with the worktree: {}", w.name(), paths.join(", ")), action: "Delete and Remove" }
}

#[derive(Serialize, Deserialize, Clone, Debug, Default, PartialEq)]
pub struct TileOrder {
    #[serde(default)]
    pub order: Vec<String>,
    #[serde(default)]
    pub pinned: Vec<String>,
    #[serde(default)]
    pub revision: i64,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum TileChange {
    Pin(String, bool),
    Move(String, String),
    Step(String, i32),
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Arrangement {
    pub order: Vec<String>,
    pub pinned: BTreeSet<String>,
}

impl Arrangement {
    fn from(t: &TileOrder) -> Arrangement {
        Arrangement { order: t.order.clone(), pinned: t.pinned.iter().cloned().collect() }
    }

    pub fn is_empty(&self) -> bool {
        self.order.is_empty() && self.pinned.is_empty()
    }

    pub fn applying(&self, change: &TileChange, arranged: &[String]) -> Arrangement {
        let all: Vec<String> = arranged.iter().cloned().chain(self.order.iter().filter(|id| !arranged.contains(id)).cloned()).collect();
        let mut next = self.clone();
        match self.resolving(change, arranged) {
            Some(TileChange::Pin(id, on)) => {
                if on {
                    next.pinned.insert(id);
                } else {
                    next.pinned.remove(&id);
                }
                next.order = all;
            }
            Some(TileChange::Move(id, target)) => {
                if self.pinned.contains(&target) {
                    next.pinned.insert(id.clone());
                } else {
                    next.pinned.remove(&id);
                }
                next.order = moving(&id, &target, &all);
            }
            Some(TileChange::Step(..)) | None => return self.clone(),
        }
        next
    }

    pub fn resolving(&self, change: &TileChange, arranged: &[String]) -> Option<TileChange> {
        let TileChange::Step(id, by) = change else { return Some(change.clone()) };
        let pinned = self.pinned.contains(id);
        let group: Vec<&String> = arranged.iter().filter(|x| self.pinned.contains(*x) == pinned).collect();
        let index = group.iter().position(|x| *x == id)? as i64 + *by as i64;
        let other = group.get(usize::try_from(index).ok()?)?;
        Some(if *by < 0 { TileChange::Move(id.clone(), (*other).clone()) } else { TileChange::Move((*other).clone(), id.clone()) })
    }

    pub fn sort<T>(&self, items: &mut [T], id: impl Fn(&T) -> &str, name: impl Fn(&T) -> &str) {
        if self.is_empty() {
            return;
        }
        let place = |x: &str| self.order.iter().position(|o| o == x).unwrap_or(usize::MAX);
        items.sort_by(|a, b| {
            let (ia, ib) = (id(a), id(b));
            self.pinned.contains(ib).cmp(&self.pinned.contains(ia)).then(place(ia).cmp(&place(ib))).then_with(|| by_name(name(a), name(b)))
        });
    }
}

fn by_name(a: &str, b: &str) -> Ordering {
    a.to_lowercase().cmp(&b.to_lowercase()).then(a.cmp(b))
}

pub fn moving(id: &str, target: &str, order: &[String]) -> Vec<String> {
    if id == target || !order.iter().any(|x| x == target) {
        return order.to_vec();
    }
    let mut next: Vec<String> = order.iter().filter(|x| *x != id).cloned().collect();
    let at = next.iter().position(|x| x == target).unwrap_or(next.len());
    next.insert(at, id.to_string());
    next
}

#[derive(Clone, Debug, PartialEq)]
pub enum TileReply {
    Saved(TileOrder),
    Stale(TileOrder),
    Failed(String),
}

#[derive(Serialize, Deserialize, Clone, Debug, Default, PartialEq)]
pub struct Saved {
    #[serde(default)]
    pub order: Vec<String>,
    #[serde(default)]
    pub pinned: Vec<String>,
    #[serde(default)]
    pub revision: i64,
    #[serde(default)]
    pub unsent: bool,
}

#[derive(Clone, Debug, Default)]
pub struct Tiles {
    pub arrangement: Arrangement,
    revision: i64,
    unsent: bool,
    heard: bool,
    retried: bool,
    flight: Option<Arrangement>,
    seen: Option<TileOrder>,
    pub error: Option<String>,
}

impl Tiles {
    pub fn restore(saved: Saved) -> Tiles {
        Tiles { arrangement: Arrangement { order: saved.order, pinned: saved.pinned.into_iter().collect() }, revision: saved.revision, unsent: saved.unsent, ..Tiles::default() }
    }

    pub fn saved(&self) -> Saved {
        Saved { order: self.arrangement.order.clone(), pinned: self.arrangement.pinned.iter().cloned().collect(), revision: self.revision, unsent: self.unsent }
    }

    pub fn unsent(&self) -> bool {
        self.unsent
    }

    pub fn heard(&self) -> bool {
        self.heard
    }

    pub fn poll(&mut self, remote: Option<&TileOrder>, fresh: bool) -> Option<TileOrder> {
        if self.flight.is_some() {
            return None;
        }
        let retry = fresh && self.unsent && self.flight.is_none();
        if remote == self.seen.as_ref() && !retry {
            return None;
        }
        self.seen = remote.cloned();
        self.adopt(remote)
    }

    pub fn adopt(&mut self, remote: Option<&TileOrder>) -> Option<TileOrder> {
        let remote = remote?;
        self.heard = true;
        if self.flight.is_some() || !(self.unsent || remote.revision >= self.revision) {
            return None;
        }
        if self.unsent || remote.revision == 0 && !self.arrangement.order.is_empty() {
            self.revision = remote.revision;
            return self.start();
        }
        if remote.revision != self.revision || Arrangement::from(remote) != self.arrangement {
            self.take(remote);
        }
        None
    }

    pub fn change(&mut self, change: &TileChange, arranged: &[String]) -> Option<TileOrder> {
        let next = self.arrangement.applying(change, arranged);
        if next == self.arrangement {
            return None;
        }
        self.arrangement = next;
        self.unsent = true;
        self.error = None;
        self.start()
    }

    pub fn answered(&mut self, reply: TileReply) -> Option<TileOrder> {
        let sent = self.flight.take()?;
        match reply {
            TileReply::Saved(saved) => {
                self.revision = saved.revision;
                if self.arrangement != sent {
                    return self.launch();
                }
                self.unsent = false;
                None
            }
            TileReply::Stale(current) if self.retried || !self.unsent => {
                self.take(&current);
                None
            }
            TileReply::Stale(current) => {
                self.retried = true;
                self.revision = current.revision;
                self.launch()
            }
            TileReply::Failed(message) => {
                self.error = Some(format!("The project order didn't save: {message}"));
                None
            }
        }
    }

    pub fn sending(&self) -> bool {
        self.flight.is_some()
    }

    fn start(&mut self) -> Option<TileOrder> {
        if !self.heard || self.flight.is_some() {
            return None;
        }
        self.retried = false;
        self.launch()
    }

    fn launch(&mut self) -> Option<TileOrder> {
        self.flight = Some(self.arrangement.clone());
        Some(TileOrder { order: self.arrangement.order.clone(), pinned: self.arrangement.pinned.iter().cloned().collect(), revision: self.revision })
    }

    fn take(&mut self, remote: &TileOrder) {
        self.revision = remote.revision;
        self.arrangement = Arrangement::from(remote);
        self.unsent = false;
    }
}

impl Client {
    pub fn project_add(&self, path: &str, title: &str, host: &str) -> Outcome<Added> {
        let mut payload = json!({"path": path});
        if !title.is_empty() {
            payload["title"] = json!(title);
        }
        if !host.is_empty() {
            payload["host"] = json!(host);
        }
        let reply = self.action("project-add", payload).and_then(confirmed)?;
        let name = reply["project"]["displayName"].as_str().filter(|n| !n.is_empty()).unwrap_or("This folder").to_string();
        Ok(Added { name, created: reply["created"].as_bool().unwrap_or(true) })
    }

    pub fn project_rename(&self, project: &str, title: &str) -> Outcome<()> {
        self.action("project-rename", json!({"projectId": project, "title": title})).and_then(confirmed).map(drop)
    }

    pub fn project_remove(&self, project: &str) -> Outcome<()> {
        self.action("project-remove", json!({"projectId": project})).and_then(confirmed).map(drop)
    }

    pub fn project_archive(&self, project: &str, archived: bool) -> Outcome<()> {
        self.action(if archived { "project-archive" } else { "project-unarchive" }, json!({"projectId": project})).and_then(confirmed).map(drop)
    }

    pub fn project_tools(&self, project: &str, on: bool) -> Outcome<()> {
        self.action("project-setting", json!({"projectId": project, "setting": "mcp", "value": if on { "on" } else { "off" }})).and_then(confirmed).map(drop)
    }

    pub fn lead_start(&self, surface: &str, folder: &str, host: &str, name: &str, replace: bool) -> Outcome<Lead> {
        let mut payload = json!({"surface": surface, "cwd": folder});
        if !host.is_empty() {
            payload["host"] = json!(host);
        }
        if !name.is_empty() {
            payload["name"] = json!(name);
        }
        if replace {
            payload["replace"] = json!(true);
        }
        lead(self.action("coordinator-new", payload).and_then(confirmed)?)
    }

    pub fn lead_set(&self, session: &str, replace: bool) -> Outcome<Lead> {
        let mut payload = json!({"sessionId": session});
        if replace {
            payload["replace"] = json!(true);
        }
        lead(self.action("coordinator-set", payload).and_then(confirmed)?)
    }

    pub fn lead_send(&self, project: &str, message: &str) -> Outcome<()> {
        self.action("coordinator-send", json!({"project": project, "message": message, "author": "person"})).and_then(confirmed).map(drop)
    }

    pub fn lead_clear(&self, project: &str) -> Outcome<()> {
        self.action("coordinator-clear", json!({"projectId": project})).and_then(confirmed).map(drop)
    }

    pub fn put_tiles(&self, order: &TileOrder) -> TileReply {
        let body = serde_json::to_value(order).unwrap_or(Value::Null);
        match self.put_json("/api/v1/projects/tiles", body) {
            Ok(reply) => serde_json::from_value(reply["tileOrder"].clone()).map(TileReply::Saved).unwrap_or_else(|e| TileReply::Failed(format!("the daemon's answer had no tile order: {e}"))),
            Err(e) if e.status == 409 => serde_json::from_value(e.reply["tileOrder"].clone()).map(TileReply::Stale).unwrap_or(TileReply::Failed(e.message)),
            Err(e) => TileReply::Failed(e.message),
        }
    }

    pub fn work_clean(&self, session: &str, remove_ignored: &[String]) -> Outcome<Worktree> {
        let mut payload = json!({"sessionId": session});
        if !remove_ignored.is_empty() {
            payload["removeIgnored"] = json!(remove_ignored);
        }
        let reply = self.action("work-clean", payload).and_then(confirmed)?;
        Ok(serde_json::from_value(reply["worktree"].clone()).unwrap_or_default())
    }
}

fn lead(reply: Value) -> Outcome<Lead> {
    Ok(serde_json::from_value(reply["coordinator"].clone()).unwrap_or_default())
}

#[cfg(test)]
#[path = "projects_tests.rs"]
mod tests;
