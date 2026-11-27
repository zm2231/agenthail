use std::cell::RefCell;
use std::sync::mpsc::Receiver;
use std::time::{Duration, Instant};

use feed::wake::{Bell, Sender};
use rune::Tx;

pub static MAIN: Bell = Bell::new();
pub static SETTINGS: Bell = Bell::new();
pub static VOICE: Bell = Bell::new();

pub fn channel<T>() -> (Sender<T>, Receiver<T>) {
    feed::wake::channel(&MAIN)
}

pub fn settings_channel<T>() -> (Sender<T>, Receiver<T>) {
    feed::wake::channel(&SETTINGS)
}

pub fn voice_channel<T>() -> (Sender<T>, Receiver<T>) {
    feed::wake::channel(&VOICE)
}

pub use feed::wake::to_thread;

pub fn poke() {
    MAIN.ring();
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Window {
    Main,
    Settings,
}

thread_local! {
    static DUE: RefCell<(Window, Vec<Instant>, Vec<Instant>)> = const { RefCell::new((Window::Main, Vec::new(), Vec::new())) };
}

fn record(at: Instant) {
    DUE.with_borrow_mut(|(window, main, settings)| {
        let list = if *window == Window::Main { main } else { settings };
        if !list.contains(&at) {
            list.push(at);
        }
    });
}

pub fn due(at: Instant) -> bool {
    if Instant::now() >= at {
        return true;
    }
    record(at);
    false
}

pub fn later(wait: time::Duration) {
    record(Instant::now() + Duration::try_from(wait).unwrap_or_default());
}

pub fn due_after(since: Instant, wait: Duration) -> bool {
    due(since + wait)
}

pub fn due_clock(at: f64, clock: f64) -> bool {
    if clock >= at {
        return true;
    }
    if let Ok(wait) = Duration::try_from_secs_f64(at - clock) {
        record(Instant::now() + wait);
    }
    false
}

pub fn begin(window: Window) {
    DUE.with_borrow_mut(|(current, main, settings)| {
        *current = window;
        if window == Window::Main { main } else { settings }.clear();
    });
}

pub fn next(window: Window) -> Option<Instant> {
    DUE.with_borrow_mut(|(current, main, settings)| {
        *current = Window::Main;
        let list = if window == Window::Main { main } else { settings };
        let now = Instant::now();
        list.retain(|at| *at > now);
        list.iter().min().copied()
    })
}

#[derive(Default)]
pub struct Wake(Option<f64>);

impl Wake {
    pub fn at(&mut self, tx: &mut Tx, due: f64) {
        if self.0.is_none_or(|armed| tx.clock() >= armed || armed > due + 0.001) {
            self.0 = Some(due);
            tx.wake_at(due);
        }
    }

    pub fn after(&mut self, tx: &mut Tx, step: Duration) {
        let due = tx.clock() + step.as_secs_f64();
        self.at(tx, due);
    }

    pub fn until(&mut self, tx: &mut Tx, at: Instant) {
        self.after(tx, at.saturating_duration_since(Instant::now()));
    }
}
