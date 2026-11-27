use std::collections::HashMap;
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::role::SessionRole;

use crate::App;

type Read = (String, u64, Result<Option<SessionRole>, String>);

pub struct Roles {
    shown: HashMap<String, SessionRole>,
    seq: u64,
    newest: HashMap<String, u64>,
    tx: Sender<Read>,
    rx: Receiver<Read>,
}

impl Roles {
    pub fn new() -> Roles {
        let (tx, rx) = channel();
        Roles { shown: HashMap::new(), seq: 0, newest: HashMap::new(), tx, rx }
    }
}

pub fn load(s: &mut App, id: &str) {
    s.roles.shown.retain(|k, _| k == id);
    s.roles.newest.retain(|k, _| k == id);
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

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok((id, seq, read)) = s.roles.rx.try_recv() {
        if s.roles.newest.get(&id) != Some(&seq) {
            continue;
        }
        match read {
            Ok(Some(role)) => {
                s.roles.shown.insert(id, role);
            }
            Ok(None) => {
                s.roles.shown.remove(&id);
            }
            Err(_) => continue,
        }
        changed = true;
    }
    changed
}

pub fn shown<'a>(s: &'a App, id: &str) -> Option<(&'a str, &'a str)> {
    s.roles.shown.get(id).and_then(SessionRole::shown)
}
