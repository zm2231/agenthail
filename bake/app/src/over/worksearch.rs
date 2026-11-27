use std::collections::HashMap;
use std::sync::mpsc::Receiver;
use std::time::Duration;

use feed::derive::phase::Steps;
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
    steps: HashMap<String, (u64, Steps)>,
    names: HashMap<String, String>,
    opening: HashMap<WorkKey, Option<String>>,
    tx: Sender<Reply>,
    rx: Receiver<Reply>,
}

impl Default for State {
    fn default() -> State {
        let (tx, rx) = channel();
        State { asked: None, typed: None, requests: 0, reads: HashMap::new(), found: HashMap::new(), steps: HashMap::new(), names: HashMap::new(), opening: HashMap::new(), tx, rx }
    }
}

impl State {
    pub fn keep(&mut self, d: &mut World) {
        self.found.retain(|key, _| !d.work.contains_key(key));
        d.found_names.extend(self.names.iter().map(|(k, v)| (k.clone(), v.clone())));
        self.steps.retain(|ledger, _| d.steps_for(ledger).is_none());
        d.steps.extend(self.steps.iter().map(|(k, (_, v))| (k.clone(), v.clone())));
        for (key, w) in &self.found {
            let steps = d.steps_of(&key.ledger);
            d.work.entry(key.clone()).or_insert_with(|| std::sync::Arc::new(Work { steps, ..w.clone() }));
        }
    }

    fn learn(&mut self, d: &mut World, work: Work, seq: u64) -> Work {
        let ledger = work.ledger_key().to_string();
        let ours = match self.steps.get(&ledger) {
            Some((at, _)) => *at < seq,
            None => d.steps_for(&ledger).is_none(),
        };
        if ours {
            self.steps.insert(ledger.clone(), (seq, work.steps.clone()));
            d.steps.insert(ledger.clone(), work.steps.clone());
            for w in d.work.values_mut().filter(|w| w.ledger_key() == ledger) {
                feed::shared::own(w).steps = work.steps.clone();
            }
        }
        let steps = d.steps_of(&ledger);
        Work { steps, ..work }
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
                        let work = s.ov.worksearch.learn(&mut s.ov.d, work, seq);
                        s.ov.worksearch.opening.remove(&key);
                        s.ov.worksearch.found.insert(key.clone(), work.clone());
                        super::jump::keep_highlight(s, |s| {
                            s.ov.d.work.insert(key, work.into());
                        });
                        s.ov.world_changed();
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

#[cfg(test)]
mod tests {
    use super::*;

    fn steps(build: &str) -> Steps {
        serde_json::from_value(serde_json::json!({"build": build})).unwrap()
    }

    fn item(id: &str, project: &str, ledger: &str, build: &str) -> Work {
        let w: Work = serde_json::from_value(serde_json::json!({"id": id, "state": "in_progress", "phase": "build"})).unwrap();
        Work { project: project.into(), ledger_key: ledger.into(), steps: steps(build), ..w }
    }

    fn world() -> World {
        World::new(serde_json::from_value(serde_json::json!({"projects": [{"id": "p-1", "ledgerKey": "project:p-1"}], "phaseLabels": {"p-1": {"build": "Craft"}}})).unwrap())
    }

    fn word(d: &World, key: WorkKey) -> String {
        let w = &d.work[&key];
        feed::derive::phase::step_word(&w.phase, &w.state, &w.steps)
    }

    #[test]
    fn a_search_item_takes_its_projects_ledger_names_over_its_own_reply() {
        let mut state = State::default();
        let mut d = world();
        let w = state.learn(&mut d, item("p-1-W9", "p-1", "project:p-1", "Make"), 1);
        assert_eq!(w.steps.name(1), "Craft", "an older item reply doesn't replace the project's ledger names");
        state.found.insert(w.key(), item("p-1-W9", "p-1", "project:p-1", "Make"));
        let mut d = world();
        state.keep(&mut d);
        assert_eq!(word(&d, WorkKey::new("project:p-1", "p-1-W9")), "Craft", "a rebuild stamps the kept item with the project's names");
    }

    #[test]
    fn a_project_known_only_from_search_names_its_steps_from_its_newest_item_reply() {
        let mut state = State::default();
        let mut d = world();
        let first = state.learn(&mut d, item("z-W1", "shared:z", "shared:z", "Make"), 1);
        state.found.insert(first.key(), first.clone());
        d.work.insert(first.key(), first.into());
        assert_eq!(d.steps_of("shared:z").name(1), "Make", "the reply's names reach the project's other search rows");
        let second = state.learn(&mut d, item("z-W2", "shared:z", "shared:z", "Forge"), 3);
        state.found.insert(second.key(), second.clone());
        d.work.insert(second.key(), second.into());
        assert_eq!(word(&d, WorkKey::new("shared:z", "z-W1")), "Forge", "an item opened earlier takes the newer names at once");
        let late = state.learn(&mut d, item("z-W3", "shared:z", "shared:z", "Old"), 2);
        assert_eq!(late.steps.name(1), "Forge", "a reply to an older read doesn't bring its names back");
        assert_eq!(word(&d, WorkKey::new("shared:z", "z-W1")), "Forge");
        let mut d = world();
        state.keep(&mut d);
        assert_eq!(d.steps_of("shared:z").name(1), "Forge");
        assert_eq!(word(&d, WorkKey::new("shared:z", "z-W1")), "Forge", "every kept item of the project shows the same names");
        assert_eq!(word(&d, WorkKey::new("shared:z", "z-W2")), "Forge");
        let mut d = World::new(serde_json::from_value(serde_json::json!({"projects": [{"id": "shared:z", "ledgerKey": "shared:z"}], "phaseLabels": {"shared:z": {"build": "Craft"}}})).unwrap());
        state.keep(&mut d);
        assert_eq!(word(&d, WorkKey::new("shared:z", "z-W1")), "Craft", "once the project's ledger is read its names win");
        assert!(state.steps.is_empty());
    }

    #[test]
    fn names_learned_from_search_survive_the_project_joining_the_catalog() {
        let mut state = State::default();
        let mut d = world();
        let first = state.learn(&mut d, item("q-W1", "project:q", "project:q", "Make"), 1);
        state.found.insert(first.key(), first.clone());
        d.work.insert(first.key(), first.into());
        let known = || World::new(serde_json::from_value(serde_json::json!({"projects": [{"id": "q", "ledgerKey": "project:q"}]})).unwrap());
        let mut d = known();
        state.keep(&mut d);
        assert_eq!(word(&d, WorkKey::new("project:q", "q-W1")), "Make", "the kept item keeps its names once its project is listed");
        assert_eq!(d.steps_of("q").name(1), "Make");
        assert_eq!(d.steps_of("project:q").name(1), "Make");
        let second = state.learn(&mut d, item("q-W2", "q", "project:q", "Forge"), 2);
        d.work.insert(second.key(), second.into());
        assert_eq!(word(&d, WorkKey::new("project:q", "q-W1")), "Forge", "a newer reply reaches the item opened before the project was listed");
        let mut d = World::new(serde_json::from_value(serde_json::json!({"projects": [{"id": "q", "ledgerKey": "project:q"}], "phaseLabels": {"q": {"build": "Craft"}}})).unwrap());
        state.keep(&mut d);
        assert_eq!(word(&d, WorkKey::new("project:q", "q-W1")), "Craft", "the project's ledger read wins once it lands");
        assert!(state.steps.is_empty());
    }
}
