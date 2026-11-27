use rune_kit::Kit;
use std::collections::HashMap;
use std::sync::mpsc::Receiver;
use std::time::{Duration, Instant};

use feed::activity::{Ready, ready_label};
use feed::wake::Sender;
use rune::{El, row, text};

use crate::theme::{META_BOLD, accent};
use crate::{App, Screen};
use crate::wake::channel;

pub const RETRY: Duration = Duration::from_secs(10);

#[derive(Default)]
struct Count {
    ready: Option<Ready>,
    asked: bool,
    again: bool,
    stale: bool,
    retry_at: Option<Instant>,
    seq: u64,
}

pub struct Readies {
    counts: HashMap<String, Count>,
    seq: u64,
    tx: Sender<(String, u64, Result<Ready, String>)>,
    rx: Receiver<(String, u64, Result<Ready, String>)>,
}

impl Default for Readies {
    fn default() -> Self {
        let (tx, rx) = channel();
        Readies { counts: HashMap::new(), seq: 0, tx, rx }
    }
}

pub fn led_by<'a>(s: &'a App, session: &str) -> Option<&'a crate::over::Project> {
    s.ov.d.projects.iter().find(|p| p.lead.as_deref() == Some(session))
}

fn wanted(s: &App) -> Option<String> {
    let lead = match &s.screen {
        Screen::Session => s.selected.clone()?,
        Screen::Project(key) => s.ov.d.project(key)?.lead.clone()?,
        _ => return None,
    };
    led_by(s, &lead).map(|p| p.key.clone())
}

fn read(s: &mut App, project: &str) {
    let Some(client) = s.client.clone() else { return };
    s.readies.seq += 1;
    let seq = s.readies.seq;
    let count = s.readies.counts.entry(project.to_string()).or_default();
    if count.asked {
        count.again = true;
        return;
    }
    count.asked = true;
    count.again = false;
    count.stale = false;
    count.retry_at = None;
    count.seq = seq;
    let (tx, project) = (s.readies.tx.clone(), project.to_string());
    std::thread::spawn(move || {
        let got = client.ready(&project);
        let _ = tx.send((project, seq, got));
    });
}

pub fn ledger_changed(s: &mut App, key: &str) {
    let hit: Vec<String> = s.readies.counts.iter().filter(|(project, c)| project.as_str() == key || c.ready.as_ref().is_some_and(|r| r.ledger_key == key) || (c.asked && c.ready.is_none())).map(|(p, _)| p.clone()).collect();
    refresh(s, hit);
}

pub fn counted(s: &App) -> Vec<String> {
    let mut keys: Vec<String> = s.readies.counts.keys().cloned().collect();
    keys.sort();
    keys
}

pub fn moved(s: &mut App, projects: &[String]) {
    for project in projects {
        s.readies.counts.remove(project);
    }
}

pub fn reopened(s: &mut App) {
    let all: Vec<String> = s.readies.counts.keys().cloned().collect();
    refresh(s, all);
}

fn refresh(s: &mut App, projects: Vec<String>) {
    let shown = wanted(s);
    for project in projects {
        if shown.as_deref() == Some(project.as_str()) {
            read(s, &project);
        } else if let Some(c) = s.readies.counts.get_mut(&project) {
            if c.asked {
                c.stale = true;
            } else {
                s.readies.counts.remove(&project);
            }
        }
    }
}

pub fn poll(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok((project, seq, got)) = s.readies.rx.try_recv() {
        let stale = {
            let Some(count) = s.readies.counts.get_mut(&project).filter(|c| c.asked && c.seq == seq) else { continue };
            count.asked = false;
            std::mem::take(&mut count.stale)
        };
        if stale && wanted(s).as_deref() != Some(project.as_str()) {
            s.readies.counts.remove(&project);
            changed = true;
            continue;
        }
        let count = s.readies.counts.entry(project.clone()).or_default();
        count.again |= stale;
        match got {
            Ok(ready) => {
                count.ready = Some(ready);
                count.retry_at = None;
            }
            Err(_) => {
                count.ready = None;
                count.retry_at = Some(Instant::now() + RETRY);
            }
        }
        if count.again {
            read(s, &project);
        }
        changed = true;
    }
    let Some(project) = wanted(s) else { return changed };
    let due = match s.readies.counts.get(&project) {
        None => true,
        Some(c) => !c.asked && c.retry_at.is_some_and(crate::wake::due),
    };
    if due {
        read(s, &project);
    }
    changed
}

pub fn count(s: &App, project: &str) -> i64 {
    s.readies.counts.get(project).and_then(|c| c.ready.as_ref()).map_or(0, |r| r.count)
}

pub fn link(s: &App, project: &str, k: &Kit) -> Option<El> {
    let label = ready_label(count(s, project))?;
    let key = project.to_string();
    Some(
        row()
            .key(format!("ready-{project}"))
            .px(8.0)
            .py(3.0)
            .radius(6.0)
            .hover(crate::theme::ink(0.06))
            .help("Open this project's work list".to_string())
            .on_press(move |s: &mut App| {
                s.ov.boards.insert(key.clone(), true);
                crate::over::go(s, Screen::Project(key.clone()));
            })
            .child(text(label).font(k.text(META_BOLD)).color(accent())),
    )
}

#[cfg(test)]
pub(crate) fn asking(s: &mut App, project: &str) -> u64 {
    s.readies.seq += 1;
    let count = s.readies.counts.entry(project.to_string()).or_default();
    count.asked = true;
    count.seq = s.readies.seq;
    s.readies.seq
}

#[cfg(test)]
pub(crate) fn answer(s: &mut App, project: &str, seq: u64, ready: Ready) {
    let _ = s.readies.tx.send((project.to_string(), seq, Ok(ready)));
    poll(s);
}
