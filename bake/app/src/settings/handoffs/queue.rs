use feed::delivery::{Tone, outcomes};
use feed::handoffs::Handoffs;
use feed::queue::Queued;
use rune::{El, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize};
use time::OffsetDateTime;

use super::super::parts;
use super::super::{Prefs, Section, saving};
use super::{change, status};
use crate::theme::{self, BODY, META, attention, failed, muted};

fn take_back(s: &mut Prefs, row: &Queued) {
    crate::ui::delivery::ask(crate::ui::delivery::Asked::TakeBack(row.clone()));
    super::forget(s);
}

fn item(s: &Prefs, q: &Queued, now: OffsetDateTime, off: bool) -> El {
    let k = theme::kit();
    let mut words = col()
        .gap(2.0)
        .grow(1.0)
        .min_w(0.0)
        .child(text(if q.target.is_empty() { q.session_id.clone() } else { q.target.clone() }).font(BODY).color(theme::text()).truncate())
        .child(text(q.headline()).font(META).color(muted()).wrap().max_lines(2))
        .child(text(q.summary(now)).font(META).color(if q.last_error.as_deref().is_some_and(|e| !e.trim().is_empty()) { failed() } else { muted() }).wrap().max_lines(3).selectable(true));
    words = words.child(text(q.status_label()).font(META).color(if q.stuck() { failed() } else { muted() }));
    let mut actions = row().gap(4.0).items_center();
    if q.can_take_back() {
        let it = q.clone();
        actions = actions.child(
            Button::new("Take back")
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .disabled(off)
                .help("Cancel this queued message and put its text back in the conversation's composer")
                .on_press(move |s: &mut Prefs| take_back(s, &it))
                .build(k)
                .key(format!("settings-queue-take-{}", q.id)),
        );
    }
    if !q.ask_id.is_empty() {
        let (session, ask) = (q.session_id.clone(), q.ask_id.clone());
        actions = actions.child(
            Button::new("Open")
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .help("Open what this message is waiting on: its decision, or the conversation")
                .on_press(move |_: &mut Prefs| crate::ui::delivery::ask(crate::ui::delivery::Asked::Answer { session: session.clone(), ask: ask.clone() }))
                .build(k)
                .key(format!("settings-queue-open-{}", q.id)),
        );
    }
    if q.can_cancel() {
        let (id, host) = (q.id, q.host.clone());
        let key = format!("queue-cancel-{id}");
        actions = actions.child(
            Button::new("Cancel")
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .disabled(off || saving(s, &key))
                .on_press(move |s: &mut Prefs| change(s, Section::Queue, format!("queue-cancel-{id}"), {
                    let host = host.clone();
                    move |c| c.queue_remove(id, &host)
                }))
                .build(k)
                .key(format!("settings-{key}")),
        );
    }
    row().w_full().gap(16.0).items_center().px(14.0).py(10.0).child(words).child(actions)
}

pub fn waiting(s: &Prefs, h: &Handoffs, off: bool) -> El {
    let now = OffsetDateTime::now_utc();
    let mut rows: Vec<El> = h.queue.iter().map(|q| item(s, q, now, off)).collect();
    if rows.is_empty() {
        rows.push(parts::words("Nothing is waiting to be delivered."));
    }
    rows.extend(status(s, Section::Queue));
    parts::section(&format!("Waiting to go out · {}", h.queue.len()), rows, Some("Messages Agenthail is holding for every session, including ones that need a decision.".into()))
}

pub fn recent(h: &Handoffs) -> El {
    let now = OffsetDateTime::now_utc();
    let mut rows: Vec<El> = Vec::new();
    match &h.history {
        None => rows.push(parts::words("Reading delivery history…")),
        Some(Err(error)) => rows.push(text(error.clone()).font(META).color(attention()).wrap().px(14.0).py(10.0)),
        Some(Ok(history)) => {
            let list = outcomes(history);
            if list.is_empty() {
                rows.push(parts::words("No delivery outcomes have been recorded yet."));
            }
            for o in list {
                rows.push(
                    col()
                        .w_full()
                        .gap(2.0)
                        .px(14.0)
                        .py(10.0)
                        .child(
                            row()
                                .w_full()
                                .gap(8.0)
                                .items_center()
                                .child(text(format!("{} · {}", o.label, o.target)).font(BODY).color(if o.tone == Tone::Problem { failed() } else { theme::text() }).truncate().grow(1.0).min_w(0.0))
                                .child(text(feed::catalog::ago(&o.created_at, now)).font(META).color(muted())),
                        )
                        .child(text(o.detail).font(META).color(if o.is_error { failed() } else { muted() }).wrap().max_lines(3).selectable(true)),
                );
            }
        }
    }
    parts::section("Recent outcomes", rows, None)
}
