use std::cell::RefCell;
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::look::{self as fl, Change, View};
use feed::prefs::Failure;

use crate::App;

const WRITE_AFTER: Duration = Duration::from_secs(1);

type Wrote = (u64, Result<View, Failure>);

#[derive(Default)]
struct State {
    due: Option<(f32, Instant)>,
    tx: Option<Sender<Wrote>>,
    rx: Option<Receiver<Wrote>>,
}

thread_local! {
    static STATE: RefCell<State> = RefCell::new(State::default());
}

pub fn step(s: &mut App, steps: Option<i32>) {
    let Some(scale) = crate::look::text_scale() else {
        s.ov.notice = Some("Text size is still loading. Try again in a moment.".into());
        return;
    };
    let next = fl::text_scale_step(scale, steps);
    crate::look::preview_text_scale(next);
    STATE.with_borrow_mut(|st| st.due = Some((next, Instant::now() + WRITE_AFTER)));
}

pub fn pump(s: &mut App) -> bool {
    let (due, wrote) = STATE.with_borrow_mut(|st| {
        let due = st.due.filter(|(_, at)| Instant::now() >= *at).map(|(scale, _)| scale);
        if due.is_some() {
            st.due = None;
        }
        if st.tx.is_none() {
            let (tx, rx) = channel();
            st.tx = Some(tx);
            st.rx = Some(rx);
        }
        let wrote: Vec<Wrote> = st.rx.as_ref().map(|rx| rx.try_iter().collect()).unwrap_or_default();
        (due.map(|scale| (scale, st.tx.clone())), wrote)
    });
    let changed = !wrote.is_empty();
    for (ticket, result) in wrote {
        match result {
            Ok(view) => crate::look::take_view(ticket, view),
            Err(failure) => s.ov.notice = Some(format!("Couldn't save the text size: {}", failure.message)),
        }
    }
    if let (Some((scale, Some(tx))), Some(client)) = (due, s.client.clone()) {
        let device = crate::look::device();
        let ticket = crate::look::ticket();
        std::thread::spawn(move || {
            let result = client.ui_change(fl::device_class(), &device, &Change::text_scale(scale, &device));
            let _ = tx.send((ticket, result));
        });
    }
    changed
}
