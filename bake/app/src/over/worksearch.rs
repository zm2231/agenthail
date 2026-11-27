use std::collections::HashMap;
use std::sync::mpsc::Receiver;
use std::time::Duration;

use feed::derive::world::{Work, WorkKey, World};
use feed::wake::Sender;
use feed::worksearch::{Search, work_query};

use crate::over::go;
use crate::{App, Screen};
use crate::wake::channel;

pub const SETTLE: Duration = Duration::from_millis(200);

enum Reply {
    Search(u64, Result<Search, String>),
    Item(u64, WorkKey, Box<Result<Work, String>>),
}

type Asked = (String, u64, Option<Result<Search, String>>);

pub struct State {
    asked: Option<Asked>,
    typed: Option<(String, f64)>,
    requests: u64,
    reads: HashMap<WorkKey, u64>,
    found: HashMap<WorkKey, Work>,
    names: HashMap<String, String>,
    opening: HashMap<WorkKey, Option<String>>,
    tx: Sender<Reply>,
    rx: Receiver<Reply>,
}

impl Default for State {
    fn default() -> State {
        let (tx, rx) = channel();
        State { asked: None, typed: None, requests: 0, reads: HashMap::new(), found: HashMap::new(), names: HashMap::new(), opening: HashMap::new(), tx, rx }
    }
}

impl State {
    pub fn keep(&mut self, d: &mut World) {
        self.found.retain(|key, _| !d.work.contains_key(key));
        d.found_names.extend(self.names.iter().map(|(k, v)| (k.clone(), v.clone())));
        for (key, w) in &self.found {
            d.work.entry(key.clone()).or_insert_with(|| w.clone());
        }
    }
}

fn query(s: &App) -> Option<String> {
    s.ov.jump.as_ref().and_then(|e| work_query(&e.text))
}

pub fn forget(s: &mut App) {
    s.ov.worksearch.asked = None;
    s.ov.worksearch.typed = None;
}

pub fn found_for(s: &App) -> Option<&Option<Result<Search, String>>> {
    let q = query(s)?;
    match &s.ov.worksearch.asked {
        Some((asked, _, found)) if *asked == q => Some(found),
        _ => Some(&None),
    }
}

fn ask(s: &mut App, q: String) {
    let st = &mut s.ov.worksearch;
    st.requests += 1;
    let seq = st.requests;
    let Some(client) = s.client.clone() else {
        s.ov.worksearch.asked = Some((q, seq, Some(Err("Work search needs a running Agenthail.".into()))));
        return;
    };
    let st = &mut s.ov.worksearch;
    st.asked = Some((q.clone(), seq, None));
    let tx = st.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(Reply::Search(seq, client.search_work(&q)));
    });
}

fn settle(s: &mut App) -> bool {
    let Some(q) = query(s) else {
        let st = &mut s.ov.worksearch;
        let had = st.asked.take().is_some();
        st.typed = None;
        return had;
    };
    let clock = s.clock;
    let st = &mut s.ov.worksearch;
    if st.typed.as_ref().is_none_or(|(typed, _)| *typed != q) {
        if st.asked.as_ref().is_some_and(|(asked, _, _)| *asked != q) {
            st.asked = None;
        }
        st.typed = Some((q, clock));
        return false;
    }
    let due = st.typed.as_ref().is_some_and(|(_, at)| clock - at >= SETTLE.as_secs_f64());
    let asked = st.asked.as_ref().is_some_and(|(a, _, _)| *a == q);
    if due && !asked {
        ask(s, q);
        return true;
    }
    false
}

pub fn wait(s: &App) -> Option<Duration> {
    let st = &s.ov.worksearch;
    let (typed, at) = st.typed.as_ref()?;
    let asked = st.asked.as_ref().is_some_and(|(a, _, _)| a == typed);
    (!asked).then(|| SETTLE.saturating_sub(Duration::from_secs_f64((s.clock - at).max(0.0))))
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok(reply) = s.ov.worksearch.rx.try_recv() {
        match reply {
            Reply::Search(seq, found) => {
                let current = query(s);
                if s.ov.worksearch.asked.as_ref().is_some_and(|(q, asked, _)| *asked == seq && current.as_ref() == Some(q)) {
                    if let Ok(search) = &found {
                        for f in search.items.iter().filter(|f| !f.project_name.is_empty()) {
                            s.ov.worksearch.names.insert(f.project_key.clone(), f.project_name.clone());
                            s.ov.d.found_names.insert(f.project_key.clone(), f.project_name.clone());
                        }
                    }
                    super::jump::keep_highlight(s, |s| {
                        if let Some((_, _, slot)) = s.ov.worksearch.asked.as_mut() {
                            *slot = Some(found);
                        }
                    });
                    changed = true;
                }
            }
            Reply::Item(seq, key, read) => {
                if s.ov.worksearch.reads.get(&key) != Some(&seq) {
                    continue;
                }
                s.ov.worksearch.reads.remove(&key);
                changed = true;
                if loaded(s, &key) {
                    s.ov.worksearch.opening.remove(&key);
                    continue;
                }
                match *read {
                    Ok(work) => {
                        let work = s.ov.d.place(work, &key.ledger);
                        s.ov.worksearch.opening.remove(&key);
                        s.ov.worksearch.found.insert(key.clone(), work.clone());
                        super::jump::keep_highlight(s, |s| {
                            s.ov.d.work.insert(key, work);
                        });
                    }
                    Err(e) => {
                        s.ov.worksearch.opening.insert(key, Some(e));
                    }
                }
            }
        }
    }
    changed | settle(s)
}

fn loaded(s: &App, key: &WorkKey) -> bool {
    s.ov.d.work.contains_key(key) && !s.ov.worksearch.found.contains_key(key)
}

fn read(s: &mut App, key: &WorkKey) {
    let Some(client) = s.client.clone() else {
        s.ov.worksearch.opening.insert(key.clone(), Some("Opening this work item needs a running Agenthail.".into()));
        return;
    };
    s.ov.worksearch.opening.insert(key.clone(), None);
    let st = &mut s.ov.worksearch;
    st.requests += 1;
    let seq = st.requests;
    st.reads.insert(key.clone(), seq);
    let (tx, key) = (st.tx.clone(), key.clone());
    std::thread::spawn(move || {
        let read = client.found_work(&key.ledger, &key.id);
        let _ = tx.send(Reply::Item(seq, key, Box::new(read)));
    });
}

pub fn open(s: &mut App, key: &WorkKey) {
    go(s, Screen::Work(key.clone()));
}

pub fn reread(s: &mut App, key: &WorkKey) {
    if !loaded(s, key) {
        read(s, key);
    }
}

pub fn ledger_changed(s: &mut App, ledger: Option<&str>) {
    let Screen::Work(key) = s.screen.clone() else { return };
    if ledger.is_none_or(|l| l == key.ledger) {
        reread(s, &key);
    }
}

pub fn note(s: &App, key: &WorkKey) -> Option<String> {
    let shown = s.ov.d.work.contains_key(key);
    match s.ov.worksearch.opening.get(key)? {
        None if shown => None,
        None => Some("Loading this work item…".into()),
        Some(e) if shown => Some(format!("Couldn't refresh this work item, so it may be out of date: {e}")),
        Some(e) => Some(format!("Couldn't open this work item: {e}")),
    }
}
