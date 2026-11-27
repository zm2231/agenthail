use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

static COPIES: AtomicU64 = AtomicU64::new(0);

pub fn own<T: Clone>(shared: &mut Arc<T>) -> &mut T {
    if Arc::get_mut(shared).is_none() {
        COPIES.fetch_add(1, Ordering::Relaxed);
    }
    Arc::make_mut(shared)
}

pub fn copies() -> u64 {
    COPIES.load(Ordering::Relaxed)
}
