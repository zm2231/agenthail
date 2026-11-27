use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{self, Receiver, SendError};
use std::sync::{Arc, Mutex};

type Ring = Arc<dyn Fn() + Send + Sync>;

pub struct Bell {
    rung: AtomicBool,
    ring: Mutex<Option<Ring>>,
}

impl Bell {
    pub const fn new() -> Bell {
        Bell { rung: AtomicBool::new(false), ring: Mutex::new(None) }
    }

    pub fn install(&self, ring: impl Fn() + Send + Sync + 'static) {
        let ring: Ring = Arc::new(ring);
        *self.ring.lock().unwrap_or_else(|e| e.into_inner()) = Some(ring.clone());
        if self.rung.load(Ordering::Acquire) {
            ring();
        }
    }

    pub fn uninstall(&self) {
        *self.ring.lock().unwrap_or_else(|e| e.into_inner()) = None;
    }

    pub fn ring(&self) {
        if self.rung.swap(true, Ordering::AcqRel) {
            return;
        }
        let ring = self.ring.lock().unwrap_or_else(|e| e.into_inner()).clone();
        if let Some(ring) = ring {
            ring();
        }
    }

    pub fn rearm(&self) -> bool {
        self.rung.swap(false, Ordering::AcqRel)
    }

    pub fn rung(&self) -> bool {
        self.rung.load(Ordering::Acquire)
    }
}

impl Default for Bell {
    fn default() -> Bell {
        Bell::new()
    }
}

pub struct Sender<T> {
    tx: mpsc::Sender<T>,
    bell: &'static Bell,
}

impl<T> Clone for Sender<T> {
    fn clone(&self) -> Self {
        Sender { tx: self.tx.clone(), bell: self.bell }
    }
}

impl<T> Sender<T> {
    pub fn send(&self, value: T) -> Result<(), SendError<T>> {
        self.tx.send(value)?;
        self.bell.ring();
        Ok(())
    }
}

pub trait Post<T> {
    fn post(&self, value: T) -> Result<(), SendError<T>>;
}

impl<T> Post<T> for Sender<T> {
    fn post(&self, value: T) -> Result<(), SendError<T>> {
        self.send(value)
    }
}

impl<T> Post<T> for mpsc::Sender<T> {
    fn post(&self, value: T) -> Result<(), SendError<T>> {
        self.send(value)
    }
}

impl<T> std::fmt::Debug for Sender<T> {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("wake::Sender")
    }
}

pub fn channel<T>(bell: &'static Bell) -> (Sender<T>, Receiver<T>) {
    let (tx, rx) = mpsc::channel();
    (Sender { tx, bell }, rx)
}

pub fn to_thread<T>() -> (mpsc::Sender<T>, Receiver<T>) {
    mpsc::channel()
}

pub fn next_minute(at: time::OffsetDateTime) -> time::OffsetDateTime {
    let start = at.replace_nanosecond(0).and_then(|t| t.replace_second(0)).unwrap_or(at);
    start + time::Duration::MINUTE
}

#[cfg(test)]
#[path = "wake_tests.rs"]
mod tests;
