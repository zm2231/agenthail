use std::cell::RefCell;
use std::collections::BTreeSet;
use std::sync::{Condvar, Mutex};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::look::{self as fl, Change};
use feed::prefs::Failure;

use crate::App;

const WRITE_AFTER: Duration = Duration::from_secs(1);

type Wrote = Result<(), Failure>;

#[derive(Default)]
struct State {
    due: Option<(f32, Instant)>,
    writing: bool,
    latest: Option<f32>,
    tx: Option<Sender<Wrote>>,
    rx: Option<Receiver<Wrote>>,
}

thread_local! {
    static STATE: RefCell<State> = RefCell::new(State::default());
}

pub fn step(s: &mut App, steps: Option<i32>) {
    let Some(scale) = STATE.with_borrow(|st| st.latest).or_else(crate::look::text_scale) else {
        s.ov.notice = Some("Text size is still loading. Try again in a moment.".into());
        return;
    };
    let next = fl::text_scale_step(scale, steps);
    crate::look::hold_text_scale(Some(next));
    STATE.with_borrow_mut(|st| {
        st.due = Some((next, Instant::now() + WRITE_AFTER));
        st.latest = Some(next);
    });
}

struct Line {
    taken: u64,
    done: u64,
    finished: BTreeSet<u64>,
}

static LINE: Mutex<Line> = Mutex::new(Line { taken: 0, done: 0, finished: BTreeSet::new() });
static MOVED: Condvar = Condvar::new();

pub struct Turn(u64);

pub fn turn() -> Turn {
    let mut line = LINE.lock().expect("text size line");
    line.taken += 1;
    Turn(line.taken)
}

impl Turn {
    pub fn wait(&self) {
        let mut line = LINE.lock().expect("text size line");
        while line.done + 1 < self.0 {
            line = MOVED.wait(line).expect("text size line");
        }
    }
}

impl Drop for Turn {
    fn drop(&mut self) {
        let mut line = LINE.lock().expect("text size line");
        line.finished.insert(self.0);
        loop {
            let next = line.done + 1;
            if !line.finished.remove(&next) {
                break;
            }
            line.done = next;
        }
        MOVED.notify_all();
    }
}

pub fn supersede() {
    STATE.with_borrow_mut(|st| {
        st.due = None;
        st.latest = None;
    });
    crate::look::hold_text_scale(None);
}

pub fn pump(s: &mut App) -> bool {
    let (due, wrote, latest) = STATE.with_borrow_mut(|st| {
        if st.tx.is_none() {
            let (tx, rx) = channel();
            st.tx = Some(tx);
            st.rx = Some(rx);
        }
        let wrote: Vec<Wrote> = st.rx.as_ref().map(|rx| rx.try_iter().collect()).unwrap_or_default();
        if !wrote.is_empty() {
            st.writing = false;
        }
        let due = st.due.filter(|(_, at)| !st.writing && Instant::now() >= *at).map(|(scale, _)| scale);
        if due.is_some() {
            st.due = None;
            st.writing = true;
        }
        if st.due.is_none() && !st.writing && due.is_none() {
            st.latest = None;
        }
        (due.zip(st.tx.clone()), wrote, st.latest)
    });
    let failure = wrote.into_iter().find_map(Result::err);
    let changed = failure.is_some();
    match (&failure, latest) {
        (Some(_), None) => crate::look::release_text_scale(),
        _ => crate::look::hold_text_scale(latest),
    }
    if let Some(failure) = failure {
        s.ov.notice = Some(format!("Couldn't save the text size: {}", failure.message));
    }
    let Some((scale, tx)) = due else { return changed };
    let Some(client) = s.client.clone() else {
        STATE.with_borrow_mut(|st| st.writing = false);
        return changed;
    };
    let device = crate::look::device();
    let turn = turn();
    std::thread::spawn(move || {
        turn.wait();
        let _ = tx.send(client.ui_change(fl::device_class(), &device, &Change::text_scale(scale, &device)).map(drop));
    });
    changed
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn text_size_writes_go_in_the_order_they_were_asked() {
        let (first, second) = (turn(), turn());
        let base = first.0 - 1;
        let waiting = std::thread::spawn(move || {
            second.wait();
            LINE.lock().unwrap().done
        });
        std::thread::sleep(std::time::Duration::from_millis(50));
        assert!(!waiting.is_finished());
        drop(first);
        assert_eq!(waiting.join().unwrap(), base + 1);
        let (third, fourth) = (turn(), turn());
        drop(fourth);
        assert_eq!(LINE.lock().unwrap().done, base + 2);
        drop(third);
        assert_eq!(LINE.lock().unwrap().done, base + 4);
    }
}
