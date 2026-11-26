use super::*;
use feed::queue::{Done, Op, Queued, waiting};
use rune::TextEdit;
use rune_kit::{ControlSize, IconButton, IconButtonVariant};

use crate::Bg;

const SHOWN: usize = 3;
const ROW_HEIGHT: f32 = 46.0;
const READ_FAILED: &str = "Couldn't read the queue";

pub(crate) fn rows(s: &App, id: &str) -> Vec<Queued> {
    s.queue.get(id).map(|rows| waiting(rows, &s.rekeyed, id)).unwrap_or_default()
}

pub(crate) fn wants(s: &App, id: &str) -> bool {
    session(s, id).is_some_and(|x| x.queue_count > 0) || !rows(s, id).is_empty() || s.queued.contains_key(id)
}

pub(crate) fn fetch(s: &mut App, id: &str) {
    let Some(client) = s.client.clone() else { return };
    if !s.queue_fetching.insert(id.to_string()) {
        return;
    }
    let (out, id) = (s.outbox.clone(), id.to_string());
    std::thread::spawn(move || {
        let result = client.queue(&id).map_err(|e| e.to_string());
        let _ = out.send(Bg::Queue(id, result));
    });
}

pub(crate) fn take(s: &mut App, id: String, result: Result<Vec<Queued>, String>) {
    s.queue_fetching.remove(&id);
    let id = feed::composer::current(&s.rekeyed, &id).to_string();
    match result {
        Ok(list) => {
            if s.queue_note.get(&id).is_some_and(|n| n.starts_with(READ_FAILED)) {
                s.queue_note.remove(&id);
            }
            s.queue.insert(id, list);
        }
        Err(error) => {
            s.queue_note.insert(id, format!("{READ_FAILED}: {error}"));
        }
    }
}

fn run(s: &mut App, id: &str, row: Queued, op: Op) {
    let Some(client) = s.client.clone() else { return };
    if !s.queue_working.insert(row.id) {
        return;
    }
    s.queue_note.remove(id);
    let host = session(s, id).map(|x| if x.host.id.is_empty() { x.host.name.clone() } else { x.host.id.clone() }).unwrap_or_default();
    let (out, id) = (s.outbox.clone(), id.to_string());
    std::thread::spawn(move || {
        let done = client.queue_op(op, &row, &host);
        let _ = out.send(Bg::QueueDone(id, row.id, done));
    });
}

fn hand_back(s: &mut App, id: &str, message: &str) {
    let merged = feed::composer::put_back(crate::shown_text(s, id), message);
    if s.composer_for.as_deref() == Some(id) {
        s.composer = TextEdit::new(merged.as_str());
        s.composer.focused = true;
    }
    s.drafts.set(id, &merged);
}

pub(crate) fn done(s: &mut App, id: String, queue_id: i64, done: Done) {
    s.queue_working.remove(&queue_id);
    let id = feed::composer::current(&s.rekeyed, &id).to_string();
    let drop_row = |s: &mut App| {
        if let Some(list) = s.queue.get_mut(&id) {
            list.retain(|r| r.id != queue_id);
        }
    };
    match done {
        Done::Removed => drop_row(s),
        Done::TakenBack(message) => {
            drop_row(s);
            hand_back(s, &id, &message);
        }
        Done::Steered => {
            drop_row(s);
            s.status_line.insert(id.clone(), "Steered".into());
        }
        Done::SteerFailed(message, error) => {
            drop_row(s);
            hand_back(s, &id, &message);
            let note = if error.maybe_delivered {
                format!("Steer may not have reached the agent. The message is back in the composer; check the conversation before resending. {}", error.message)
            } else {
                format!("Couldn't steer: {}. The message is back in the composer.", error.message)
            };
            s.queue_note.insert(id.clone(), note);
        }
        Done::Failed(error) => {
            s.queue_note.insert(id.clone(), format!("Couldn't change the queued message: {}", error.message));
        }
    }
    crate::over::layout::save(s);
    fetch(s, &id);
}

pub(crate) fn reserve(s: &App, id: &str) -> f32 {
    let shown = rows(s, id).len().min(SHOWN);
    let extra = usize::from(rows(s, id).len() > SHOWN) + usize::from(s.queue_note.contains_key(id));
    shown as f32 * ROW_HEIGHT + extra as f32 * 20.0 + if shown + extra > 0 { 12.0 } else { 0.0 }
}

fn row_el(s: &App, x: &Session, item: &Queued) -> El {
    let k = theme::kit();
    let working = s.queue_working.contains(&item.id);
    let mut actions = row().gap(2.0).items_center();
    if crate::busy_now(s, &x.id) && x.capabilities.steer {
        let refusal = item.steer_refusal(&x.capabilities);
        let (id, it) = (x.id.clone(), item.clone());
        actions = actions.child(
            Button::new("Steer")
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .icon(&icons::BOLT)
                .disabled(working || refusal.is_some())
                .help(refusal.unwrap_or("Steer into the current turn"))
                .on_press(move |s: &mut App| run(s, &id, it.clone(), Op::Steer))
                .build(k)
                .key(format!("queue-steer-{}", item.id)),
        );
    }
    let refusal = item.take_back_refusal();
    let (id, it) = (x.id.clone(), item.clone());
    actions = actions.child(
        IconButton::new(&icons::PENCIL)
            .variant(IconButtonVariant::GhostMuted)
            .control_size(ControlSize::Small)
            .label("Edit")
            .disabled(working || refusal.is_some())
            .help(refusal.unwrap_or("Take it back into the composer"))
            .on_press(move |s: &mut App| run(s, &id, it.clone(), Op::TakeBack))
            .build(k)
            .key(format!("queue-edit-{}", item.id)),
    );
    let message = item.message.clone();
    actions = actions.child(
        IconButton::new(&icons::COPY)
            .variant(IconButtonVariant::GhostMuted)
            .control_size(ControlSize::Small)
            .label("Copy text")
            .help("Copy text")
            .on_press_tx(move |_: &mut App, tx: &mut Tx| tx.copy(message.clone()))
            .build(k)
            .key(format!("queue-copy-{}", item.id)),
    );
    let (id, it) = (x.id.clone(), item.clone());
    actions = actions.child(
        IconButton::new(&icons::TRASH)
            .variant(IconButtonVariant::GhostMuted)
            .control_size(ControlSize::Small)
            .label("Remove queued message")
            .disabled(working)
            .help("Remove queued message")
            .on_press(move |s: &mut App| run(s, &id, it.clone(), Op::Remove))
            .build(k)
            .key(format!("queue-remove-{}", item.id)),
    );
    row()
        .key(format!("queued-{}", item.id))
        .w_full()
        .h(ROW_HEIGHT)
        .gap(10.0)
        .items_center()
        .px(12.0)
        .child(icon(&icons::CLOCK, 13.0).color(faint()))
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(2.0)
                .child(text(item.first_line().to_string()).font(META_BOLD).color(theme::text()).truncate())
                .child(text(if working { "Working…".to_string() } else { item.summary(s.now) }).font(META).color(faint()).truncate()),
        )
        .child(actions)
}

pub(crate) fn dock(s: &App, x: &Session) -> Option<El> {
    let list = rows(s, &x.id);
    let note = s.queue_note.get(&x.id);
    if list.is_empty() && note.is_none() {
        return None;
    }
    let mut c = col().key("queue-dock").w_full().pt(6.0).pb(6.0).border_sides([0.0, 0.0, 1.0, 0.0], line());
    c = c.children(list.iter().take(SHOWN).map(|item| row_el(s, x, item)));
    if list.len() > SHOWN {
        c = c.child(row().h(20.0).px(12.0).items_center().child(text(format!("{} more queued", list.len() - SHOWN)).font(META).color(faint())));
    }
    if let Some(note) = note {
        c = c.child(row().h(20.0).px(12.0).items_center().child(text(note.clone()).font(META).color(attention()).truncate()));
    }
    Some(c)
}
