use feed::history::{Miss, Older};
use rune::{TextEdit, Tx};

use crate::transcript::Pane;
use crate::{App, Bg};


pub fn batch_arrived(pane: &mut Pane, t: f64) {
    if pane.paged_first {
        let (live, status) = (pane.live.take(), std::mem::take(&mut pane.status));
        *pane = Pane::new();
        (pane.live, pane.status) = (live, status);
    }
    pane.last_batch = t;
}

pub fn caught_up(s: &mut App, id: &str) {
    let Some(pane) = s.panes.get_mut(id) else { return };
    pane.caught_up = true;
    pane.paged_first = pane.transcript.items().is_empty();
    if pane.earlier.at_top || pane.blocks.is_empty() {
        load_older(s, id);
    }
}

pub fn seen_rows(s: &mut App, id: &str, start: usize) -> bool {
    let Some(pane) = s.panes.get_mut(id) else { return false };
    pane.earlier.at_top = start == 0;
    pane.earlier.at_top && load_older(s, id)
}

pub fn can_load(s: &App, id: &str) -> bool {
    s.client.is_some() && s.panes.get(id).is_some_and(|p| p.caught_up && p.earlier.more())
}

pub fn load_older(s: &mut App, id: &str) -> bool {
    if !can_load(s, id) {
        return false;
    }
    let Some(client) = s.client.clone() else { return false };
    let Some(pane) = s.panes.get_mut(id) else { return false };
    if pane.earlier.loading || pane.earlier.note.is_some() {
        return false;
    }
    pane.earlier.loading = true;
    let oldest = if pane.earlier.anchored { None } else { pane.transcript.items().first().map(|i| i.id.clone()) };
    let (from, known, generation) = (pane.earlier.next.unwrap_or(0), pane.transcript.ids(), pane.generation);
    let (out, id) = (s.outbox.clone(), id.to_string());
    std::thread::spawn(move || {
        let result = client.older(&id, from, oldest.as_deref(), &known);
        let _ = out.send(Bg::Older(id, generation, result));
    });
    true
}

pub fn retry_older(s: &mut App, id: &str) {
    if let Some(pane) = s.panes.get_mut(id) {
        pane.earlier.note = None;
    }
    load_older(s, id);
}

pub fn take_older(s: &mut App, id: String, generation: u64, result: Result<Older, Miss>) {
    let mut counter = s.counter;
    let Some(pane) = s.panes.get_mut(&id).filter(|p| p.generation == generation) else { return };
    pane.earlier.loading = false;
    match result {
        Ok(older) if !older.anchored && older.next == 0 => {
            pane.earlier.note = Some("Couldn't line up earlier messages with this conversation".into());
        }
        Ok(older) => {
            pane.earlier.next = Some(older.next);
            pane.earlier.anchored |= older.anchored;
            if pane.transcript.prepend(older.items) {
                pane.rebuild(&mut counter);
                s.panes_moved = true;
            }
            if pane.blocks.is_empty() {
                s.counter = counter;
                load_older(s, &id);
                return;
            }
        }
        Err(Miss::Gone) => {
            pane.earlier.next = Some(0);
            pane.earlier.note = Some(Miss::Gone.note());
        }
        Err(miss) => pane.earlier.note = Some(miss.note()),
    }
    s.counter = counter;
}

pub fn load_body(s: &mut App, id: &str, item: &str) {
    let Some(client) = s.client.clone() else { return };
    let Some(pane) = s.panes.get_mut(id) else { return };
    let Some((body_ref, version)) = pane.transcript.item(item).filter(|i| i.truncated).and_then(|i| Some((i.body_ref.clone()?, i.version))) else { return };
    if pane.expanding.get(item).is_some_and(|note| note.is_none()) {
        return;
    }
    pane.expanding.insert(item.to_string(), None);
    let mut counter = s.counter;
    pane.bump_item(item, &mut counter);
    s.counter = counter;
    let (out, id, item, generation) = (s.outbox.clone(), id.to_string(), item.to_string(), pane.generation);
    std::thread::spawn(move || {
        let result = client.full_body(&id, &body_ref);
        let _ = out.send(Bg::Body(id, generation, item, version, result));
    });
}

pub fn take_body(s: &mut App, id: String, generation: u64, item: String, version: u64, result: Result<String, String>) {
    let mut counter = s.counter;
    let Some(pane) = s.panes.get_mut(&id).filter(|p| p.generation == generation) else { return };
    match result {
        Ok(text) => {
            pane.expanding.remove(&item);
            if pane.transcript.expand(&item, version, text) {
                pane.rebuild(&mut counter);
                s.panes_moved = true;
            } else {
                pane.bump_item(&item, &mut counter);
            }
        }
        Err(error) => {
            pane.expanding.insert(item.clone(), Some(format!("Couldn't load the full message: {error}")));
            pane.bump_item(&item, &mut counter);
        }
    }
    s.counter = counter;
}

pub fn show_more(s: &mut App, block: &str, item: &str, limit: usize) {
    let mut counter = s.counter;
    for pane in s.panes.values_mut() {
        if pane.transcript.item(item).is_some() {
            pane.shown.insert(item.to_string(), limit);
            pane.bump(block, &mut counter);
        }
    }
    s.counter = counter;
}

pub fn copy(text: String) -> impl Fn(&mut App, &mut Tx) + 'static {
    move |_: &mut App, tx: &mut Tx| tx.copy(text.clone())
}

pub fn edit(text: String) -> impl Fn(&mut App) + 'static {
    move |s: &mut App| {
        let merged = feed::composer::put_back(&s.composer.text, &text);
        s.composer = TextEdit::new(merged.as_str());
        s.composer.focused = true;
    }
}
