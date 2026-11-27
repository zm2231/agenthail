use std::collections::HashMap;
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Duration;

use feed::derive::world::{Work, World};
use feed::worksearch::{Search, work_query};

use crate::over::go;
use crate::{App, Screen};

pub const SETTLE: Duration = Duration::from_millis(200);

enum Reply {
    Search(u64, Result<Search, String>),
    Item(String, String, Box<Result<Work, String>>),
}

type Asked = (String, u64, Option<Result<Search, String>>);

pub struct State {
    asked: Option<Asked>,
    typed: Option<(String, f64)>,
    requests: u64,
    found: HashMap<String, Work>,
    opening: HashMap<String, Option<String>>,
    tx: Sender<Reply>,
    rx: Receiver<Reply>,
}

impl Default for State {
    fn default() -> State {
        let (tx, rx) = channel();
        State { asked: None, typed: None, requests: 0, found: HashMap::new(), opening: HashMap::new(), tx, rx }
    }
}

impl State {
    pub fn keep(&self, d: &mut World) {
        for (id, w) in &self.found {
            d.work.entry(id.clone()).or_insert_with(|| w.clone());
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
                if let Some((_, _, slot)) = s.ov.worksearch.asked.as_mut().filter(|(_, asked, _)| *asked == seq) {
                    *slot = Some(found);
                    changed = true;
                }
            }
            Reply::Item(ledger, id, read) => {
                changed = true;
                match *read {
                    Ok(work) => {
                        let work = s.ov.d.place(work, &ledger);
                        s.ov.worksearch.opening.remove(&id);
                        s.ov.d.work.insert(id.clone(), work.clone());
                        s.ov.worksearch.found.insert(id, work);
                    }
                    Err(e) => {
                        s.ov.worksearch.opening.insert(id, Some(e));
                    }
                }
            }
        }
    }
    changed | settle(s)
}

fn loaded(s: &App, id: &str) -> bool {
    s.ov.d.work.contains_key(id) && !s.ov.worksearch.found.contains_key(id)
}

fn read(s: &mut App, ledger: &str, id: &str) {
    let Some(client) = s.client.clone() else {
        s.ov.worksearch.opening.insert(id.into(), Some("Opening this work item needs a running Agenthail.".into()));
        return;
    };
    if !s.ov.d.work.contains_key(id) {
        s.ov.worksearch.opening.insert(id.into(), None);
    }
    let (tx, ledger, id) = (s.ov.worksearch.tx.clone(), ledger.to_string(), id.to_string());
    std::thread::spawn(move || {
        let read = client.found_work(&ledger, &id);
        let _ = tx.send(Reply::Item(ledger, id, Box::new(read)));
    });
}

pub fn open(s: &mut App, ledger: &str, id: &str) {
    if !loaded(s, id) {
        read(s, ledger, id);
    }
    go(s, Screen::Work(id.to_string()));
}

pub fn moved(s: &mut App, id: &str) {
    if let Some(ledger) = s.ov.worksearch.found.get(id).map(|w| w.ledger_key().to_string()) {
        read(s, &ledger, id);
    }
}

pub fn note(s: &App, id: &str) -> Option<String> {
    match s.ov.worksearch.opening.get(id)? {
        None => Some("Loading this work item…".into()),
        Some(e) => Some(format!("Couldn't open this work item: {e}")),
    }
}
