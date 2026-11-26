use super::*;
use feed::queue::{Done, Op, Queued, READ_FAILED, Taking, waiting};
use rune::TextEdit;
use rune_kit::{ControlSize, IconButton, IconButtonVariant};

use crate::Bg;

const SHOWN: usize = 3;
const ROW_HEIGHT: f32 = 46.0;
const NOTE_HEIGHT: f32 = 34.0;


pub(crate) fn rows(s: &App, id: &str) -> Vec<Queued> {
    s.queue.get(id).map(|rows| waiting(rows, &s.rekeyed, id)).unwrap_or_default()
}

pub(crate) fn wants(s: &App, id: &str) -> bool {
    session(s, id).is_some_and(|x| x.queue_count > 0) || !rows(s, id).is_empty() || s.queued.contains_key(id) || s.queue_note.contains_key(id)
}

fn valid_from(s: &App, id: &str) -> u64 {
    s.queue_gen.get(id).copied().unwrap_or_default()
}

pub(crate) fn invalidate(s: &mut App, id: &str) {
    s.queue_epoch += 1;
    s.queue_gen.insert(id.to_string(), s.queue_epoch);
}

pub(crate) fn fetch(s: &mut App, id: &str) {
    let Some(client) = s.client.clone() else { return };
    if s.queue_fetching.get(id).is_some_and(|&asked| asked >= valid_from(s, id)) {
        return;
    }
    let generation = s.queue_epoch;
    s.queue_fetching.insert(id.to_string(), generation);
    let remote = session(s, id).filter(|x| x.host.state != "local" && !x.host.state.is_empty()).map(|x| if x.host.id.is_empty() { x.host.name.clone() } else { x.host.id.clone() });
    let (out, id) = (s.outbox.clone(), id.to_string());
    std::thread::spawn(move || {
        let result = client.queue(&id, remote.as_deref()).map_err(|e| e.to_string());
        let _ = out.send(Bg::Queue(id, generation, result));
    });
}

pub(crate) fn take(s: &mut App, id: String, asked: u64, result: Result<feed::queue::Read, String>) {
    if s.queue_fetching.get(&id) == Some(&asked) {
        s.queue_fetching.remove(&id);
    }
    let id = feed::composer::current(&s.rekeyed, &id).to_string();
    if asked < valid_from(s, &id) {
        return;
    }
    match result {
        Ok(read) => {
            if s.queue_note.get(&id).is_some_and(|n| n.starts_with(READ_FAILED)) {
                s.queue_note.remove(&id);
            }
            if let Some(partial) = read.partial {
                s.queue_note.insert(id.clone(), partial);
            }
            s.queue.insert(id, read.rows);
        }
        Err(error) => {
            s.queue_note.insert(id, format!("{READ_FAILED}: {error}"));
        }
    }
}

fn run(s: &mut App, id: &str, row: Queued, op: Op) {
    let Some(client) = s.client.clone() else { return };
    let working = (row.host.clone(), row.id);
    if !s.queue_working.insert(working.clone()) {
        return;
    }
    s.queue_note.remove(id);
    if op != Op::Remove {
        if let Some(open) = s.composer_for.clone() {
            s.drafts.set(&open, &s.composer.text);
        }
        s.queue_taking.push(Taking { session: id.to_string(), host: row.host.clone(), id: row.id, text: row.message.clone() });
        if let Err(error) = crate::over::layout::save_now(s) {
            s.queue_taking.retain(|t| (t.host.as_str(), t.id) != (row.host.as_str(), row.id));
            s.queue_working.remove(&working);
            s.queue_note.insert(id.to_string(), format!("Nothing was changed: the message couldn't be saved first ({error})"));
            return;
        }
    }
    invalidate(s, id);
    let (out, id) = (s.outbox.clone(), id.to_string());
    std::thread::spawn(move || {
        let done = client.queue_op(op, &row);
        let _ = out.send(Bg::QueueDone(id, working, done));
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

pub(crate) fn done(s: &mut App, id: String, working: (String, i64), done: Done) {
    s.queue_working.remove(&working);
    s.queue_taking.retain(|t| (t.host.as_str(), t.id) != (working.0.as_str(), working.1));
    let (host, queue_id) = working;
    let id = feed::composer::current(&s.rekeyed, &id).to_string();
    invalidate(s, &id);
    let drop_row = |s: &mut App| {
        if let Some(list) = s.queue.get_mut(&id) {
            list.retain(|r| !(r.id == queue_id && r.host == host));
        }
    };
    match done {
        Done::Removed => drop_row(s),
        Done::TakenBack(message) => {
            drop_row(s);
            hand_back(s, &id, &message);
        }
        Done::Steered(receipt) => {
            drop_row(s);
            s.status_line.insert(id.clone(), receipt.note(1));
        }
        Done::SteerUnconfirmed(message) => {
            drop_row(s);
            hand_back(s, &id, &message);
            s.queue_note.insert(id.clone(), "The steer wasn't confirmed. The message is back in the composer; check the conversation before resending.".into());
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
        Done::Unsure(message, error) => {
            let note = match message {
                Some(message) => {
                    hand_back(s, &id, &message);
                    format!("The daemon didn't confirm taking it off the queue, so it may still send. Its text is in the composer; check the queue before sending. {}", error.message)
                }
                None => format!("The daemon didn't confirm the removal; check the queue. {}", error.message),
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

pub(crate) fn recover(s: &mut App) {
    for taking in std::mem::take(&mut s.queue_taking) {
        hand_back(s, &taking.session, &taking.text);
        s.queue_note.insert(taking.session.clone(), "A queued message you were taking back or steering when the app closed is back in the composer; check the queue before sending.".into());
    }
}

pub(crate) fn reserve(s: &App, id: &str) -> f32 {
    let shown = rows(s, id).len().min(SHOWN);
    let note = usize::from(s.queue_note.contains_key(id));
    shown as f32 * ROW_HEIGHT + note as f32 * NOTE_HEIGHT + if shown + note > 0 { 12.0 } else { 0.0 }
}

fn row_el(s: &App, x: &Session, item: &Queued) -> El {
    let k = theme::kit();
    let working = s.queue_working.contains(&(item.host.clone(), item.id));
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
                .key(format!("queue-steer-{}-{}", item.host, item.id)),
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
            .key(format!("queue-edit-{}-{}", item.host, item.id)),
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
            .key(format!("queue-copy-{}-{}", item.host, item.id)),
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
            .key(format!("queue-remove-{}-{}", item.host, item.id)),
    );
    row()
        .key(format!("queued-{}-{}", item.host, item.id))
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
    if !list.is_empty() {
        let rows_el = col().w_full().children(list.iter().map(|item| row_el(s, x, item)));
        c = c.child(col().key("queue-dock-rows").w_full().max_h(SHOWN as f32 * ROW_HEIGHT).scroll(ScrollSpec { stick_bottom: false }).child(rows_el));
    }
    if let Some(note) = note {
        c = c.child(row().w_full().h(NOTE_HEIGHT).px(12.0).items_center().child(text(note.clone()).font(META).color(attention()).wrap().max_lines(2)));
    }
    Some(c)
}
