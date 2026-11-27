use std::hash::Hash;

use rune::El;
use rune_kit::Kit;

pub fn memo(id: impl Into<String>, shows: impl Hash, k: &Kit, build: impl FnOnce() -> El) -> El {
    let id = id.into();
    #[cfg(test)]
    let named = id.clone();
    let look = (crate::theme::paint_key(), k.dark, k.c, k.ty, k.radius, k.roles, k.text_scale.to_bits());
    rune::memo(id, (shows, look), move || {
        #[cfg(test)]
        built::note(named);
        build()
    })
}

#[cfg(test)]
pub mod built {
    use std::cell::RefCell;

    thread_local! {
        static BUILT: RefCell<Vec<String>> = const { RefCell::new(Vec::new()) };
    }

    pub fn note(id: String) {
        BUILT.with_borrow_mut(|b| b.push(id));
    }

    pub fn take() -> Vec<String> {
        BUILT.with_borrow_mut(std::mem::take)
    }
}
