use std::collections::HashMap;
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::role::SessionRole;

use crate::App;

type Read = (String, u64, Result<Option<SessionRole>, String>);

const RETRY: Duration = Duration::from_secs(30);

pub struct Roles {
    shown: HashMap<String, SessionRole>,
    seq: u64,
    newest: HashMap<String, u64>,
    failed: HashMap<String, Instant>,
    tx: Sender<Read>,
    rx: Receiver<Read>,
}

impl Roles {
    pub fn new() -> Roles {
        let (tx, rx) = channel();
        Roles { shown: HashMap::new(), seq: 0, newest: HashMap::new(), failed: HashMap::new(), tx, rx }
    }
}

pub fn load(s: &mut App, id: &str) {
    let keep: Vec<String> = super::window::shown(s).into_iter().map(|(_, x)| x).chain(std::iter::once(id.to_string())).collect();
    s.roles.shown.retain(|k, _| keep.contains(k));
    s.roles.newest.retain(|k, _| keep.contains(k));
    s.roles.failed.retain(|k, _| keep.contains(k) && k != id);
    refresh(s, id);
}

pub fn refresh(s: &mut App, id: &str) {
    let Some(client) = s.client.clone() else { return };
    s.roles.seq += 1;
    let (id, seq, tx) = (id.to_string(), s.roles.seq, s.roles.tx.clone());
    s.roles.newest.insert(id.clone(), seq);
    std::thread::spawn(move || {
        let read = client.session_role(&id).map_err(|e| e.to_string());
        let _ = tx.send((id, seq, read));
    });
}

#[cfg(test)]
pub fn failed_a_while_ago(s: &mut App, id: &str) {
    s.roles.failed.insert(id.to_string(), Instant::now() - RETRY);
}

#[cfg(test)]
pub fn forget(s: &mut App) {
    s.roles.newest.clear();
}

#[cfg(test)]
pub fn asked(s: &App, id: &str) -> bool {
    s.roles.newest.contains_key(id)
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok((id, seq, read)) = s.roles.rx.try_recv() {
        if s.roles.newest.get(&id) != Some(&seq) {
            continue;
        }
        match read {
            Ok(role) => {
                s.roles.failed.remove(&id);
                match role {
                    Some(role) => s.roles.shown.insert(id, role),
                    None => s.roles.shown.remove(&id),
                };
                changed = true;
            }
            Err(_) => {
                s.roles.failed.insert(id, Instant::now());
            }
        }
    }
    let shown = super::window::shown_sessions(s);
    let due: Vec<String> = s.roles.failed.iter().filter(|(id, at)| at.elapsed() >= RETRY && shown.contains(id)).map(|(id, _)| id.clone()).collect();
    for id in due {
        s.roles.failed.remove(&id);
        refresh(s, &id);
    }
    changed
}

pub fn shown<'a>(s: &'a App, id: &str) -> Option<(&'a str, &'a str)> {
    s.roles.shown.get(id).and_then(SessionRole::shown)
}
