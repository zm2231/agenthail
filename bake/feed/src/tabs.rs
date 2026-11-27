use serde::{Deserialize, Serialize};

pub const NEW_SESSION: &str = "agenthail.new-session";

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Tab {
    pub id: String,
    pub kept: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub alias: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
}

impl Tab {
    fn new(id: &str) -> Tab {
        Tab { id: id.to_string(), kept: true, alias: None, name: None }
    }
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct Tabs {
    #[serde(default)]
    tabs: Vec<Tab>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    active: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    used: Option<Vec<String>>,
}

fn display_name(name: &str) -> String {
    let parts: Option<(&str, &str)> = name
        .strip_prefix("https://")
        .or_else(|| name.strip_prefix("http://"))
        .and_then(|rest| {
            let mut it = rest.splitn(4, '/');
            let host = it.next()?;
            let (owner, repo) = (it.next()?, it.next()?);
            let ok = |p: &str| !p.is_empty() && !p.contains(char::is_whitespace);
            (ok(host) && ok(owner) && ok(repo)).then_some((owner, repo))
        });
    match parts {
        Some((owner, repo)) => format!("{owner} / {repo}"),
        None => name.to_string(),
    }
}

fn looks_like_uuid(s: &str) -> bool {
    let groups: Vec<&str> = s.split('-').collect();
    groups.iter().map(|g| g.len()).eq([8, 4, 4, 4, 12]) && groups.iter().all(|g| g.chars().all(|c| c.is_ascii_hexdigit()))
}

pub fn title(alias: Option<&str>, name: Option<&str>, id: &str, label: &str) -> String {
    if id == NEW_SESSION {
        return "New Session".into();
    }
    let alias = alias.filter(|a| !a.is_empty()).map(|a| format!("@{a}"));
    let trimmed = name.unwrap_or("").trim();
    let name = (!trimmed.is_empty() && trimmed != id && !looks_like_uuid(trimmed)).then(|| display_name(trimmed));
    let parts = match label {
        "alias" => vec![alias.or(name)],
        "title" => vec![name.or(alias)],
        _ => vec![alias, name],
    };
    let title = parts.into_iter().flatten().collect::<Vec<_>>().join(" ");
    if title.is_empty() { "Untitled conversation".into() } else { title }
}

impl Tabs {
    pub fn tabs(&self) -> &[Tab] {
        &self.tabs
    }

    pub fn active(&self) -> Option<&str> {
        self.active.as_deref()
    }

    pub fn contains(&self, id: &str) -> bool {
        self.tabs.iter().any(|t| t.id == id)
    }

    fn index(&self, id: &str) -> Option<usize> {
        self.tabs.iter().position(|t| t.id == id)
    }

    fn beside_active(&self) -> usize {
        self.active.as_deref().and_then(|a| self.index(a)).map_or(self.tabs.len(), |i| i + 1)
    }

    pub fn open(&mut self, id: &str) {
        if !self.contains(id) {
            let at = self.beside_active();
            self.tabs.insert(at, Tab::new(id));
        }
        self.activate(id);
    }

    pub fn add(&mut self, id: &str) {
        if !self.contains(id) {
            let at = self.beside_active();
            self.tabs.insert(at, Tab::new(id));
        }
    }

    pub fn activate(&mut self, id: &str) {
        if !self.contains(id) {
            return;
        }
        self.active = Some(id.to_string());
        let mut used: Vec<String> = self.used.take().unwrap_or_default().into_iter().filter(|u| u != id).collect();
        used.push(id.to_string());
        self.used = Some(used);
    }

    pub fn close(&mut self, id: &str) {
        let Some(i) = self.index(id) else { return };
        self.tabs.remove(i);
        if let Some(used) = &mut self.used {
            used.retain(|u| u != id);
        }
        if self.active.as_deref() != Some(id) {
            return;
        }
        self.active = self.tabs.get(i.min(self.tabs.len().saturating_sub(1))).map(|t| t.id.clone());
    }

    pub fn close_others(&mut self, id: &str) {
        if !self.contains(id) {
            return;
        }
        self.tabs.retain(|t| t.id == id);
        self.active = Some(id.to_string());
    }

    pub fn replace(&mut self, old: &str, new: &str) {
        if !self.contains(old) {
            self.open(new);
            return;
        }
        if old != new {
            self.tabs.retain(|t| t.id != new);
        }
        let Some(i) = self.index(old) else { return };
        self.tabs[i] = Tab::new(new);
        if let Some(used) = &mut self.used {
            for u in used.iter_mut().filter(|u| *u == old) {
                *u = new.to_string();
            }
        }
        self.activate(new);
    }

    pub fn name(&mut self, id: &str, alias: Option<&str>, name: Option<&str>) {
        let Some(i) = self.index(id) else { return };
        if alias.is_none() && name.is_none() {
            return;
        }
        self.tabs[i].alias = alias.map(str::to_string);
        self.tabs[i].name = name.map(str::to_string);
    }

    pub fn neighbour(&self, offset: isize) -> Option<&str> {
        if self.tabs.is_empty() {
            return None;
        }
        let i = self.active.as_deref().and_then(|a| self.index(a)).unwrap_or(0) as isize;
        let n = self.tabs.len() as isize;
        Some(&self.tabs[((i + offset) % n + n) as usize % n as usize].id)
    }

    pub fn fit(&mut self, capacity: usize, holds: impl Fn(&str) -> bool) -> Vec<String> {
        let mut closed = Vec::new();
        while self.tabs.len() > capacity.max(1) {
            let order: Vec<String> = self.used.clone().unwrap_or_default().into_iter().filter(|u| self.contains(u)).collect();
            let unused = self.tabs.iter().map(|t| t.id.clone()).filter(|id| !order.contains(id));
            let Some(oldest) = unused.chain(order.iter().cloned()).find(|id| self.active.as_deref() != Some(id) && !holds(id)) else { break };
            self.close(&oldest);
            closed.push(oldest);
        }
        closed
    }

    pub fn reconcile(&mut self, resolve: impl Fn(&str) -> String, keeps: impl Fn(&str) -> bool) -> Vec<String> {
        let mut dropped = Vec::new();
        let mut survivors: Vec<(usize, Tab)> = Vec::new();
        let active_index = self.active.as_deref().and_then(|a| self.index(a));
        for (i, tab) in self.tabs.iter().enumerate() {
            let id = resolve(&tab.id);
            if !keeps(&id) || survivors.iter().any(|(_, t)| t.id == id) {
                dropped.push(tab.id.clone());
                continue;
            }
            survivors.push((i, Tab { id, kept: tab.kept, alias: tab.alias.clone(), name: tab.name.clone() }));
        }
        if let (Some(at), Some(current)) = (active_index, self.active.as_deref()) {
            let resolved = resolve(current);
            let kept = survivors
                .iter()
                .find(|(_, t)| t.id == resolved)
                .or_else(|| survivors.iter().find(|(i, _)| *i > at))
                .or(survivors.last());
            self.active = kept.map(|(_, t)| t.id.clone());
        }
        self.tabs = survivors.into_iter().map(|(_, t)| t).collect();
        dropped
    }

    pub fn from_json(v: &serde_json::Value) -> Option<Tabs> {
        serde_json::from_value(v.clone()).ok()
    }

    pub fn to_json(&self) -> serde_json::Value {
        serde_json::to_value(self).unwrap_or(serde_json::Value::Null)
    }
}

#[cfg(test)]
#[path = "tabs_tests.rs"]
mod tests;
