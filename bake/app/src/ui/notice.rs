use std::collections::HashSet;

use feed::catalog::{LeadReplacement, Session};
use rune::{Color, El, col, icon, row, text};
use rune_kit::{Button, ButtonVariant, Kit};

use crate::theme::{self, BODY, BODY_BOLD, META, accent, attention, faint, muted};
use crate::{App, Bg, icons};

#[derive(Default)]
pub struct Notices {
    replacements: Vec<LeadReplacement>,
    acking: HashSet<i64>,
    acked: HashSet<i64>,
    error: Option<String>,
    seen: u64,
}

pub fn poll(s: &mut App) -> bool {
    let now = feed::notice::changes();
    let changed = s.notices.seen != now;
    s.notices.seen = now;
    changed
}

pub enum Done {
    Acked(i64, Result<(), String>),
}

pub fn take_replacements(s: &mut App, list: Vec<LeadReplacement>) {
    let n = &mut s.notices;
    n.acked.retain(|id| list.iter().any(|r| r.id == *id));
    n.replacements = list.into_iter().filter(|r| !n.acked.contains(&r.id)).collect();
}

pub fn take(s: &mut App, done: Done) {
    match done {
        Done::Acked(id, Ok(())) => {
            s.notices.replacements.retain(|r| r.id != id);
            s.notices.acking.remove(&id);
            s.notices.acked.insert(id);
        }
        Done::Acked(id, Err(error)) => {
            s.notices.acking.remove(&id);
            s.notices.error = Some(format!("Couldn't acknowledge: {error}"));
        }
    }
}

fn acknowledge(s: &mut App, id: i64) {
    s.notices.error = None;
    let Some(client) = s.client.clone() else {
        s.notices.error = Some("Fixture mode: not acknowledged".into());
        return;
    };
    s.notices.acking.insert(id);
    let out = s.outbox.clone();
    std::thread::spawn(move || {
        let result = client.acknowledge_lead_replacement(id).map_err(|e| e.message);
        let _ = out.send(Bg::Notice(Done::Acked(id, result)));
    });
}

fn banner(r: &LeadReplacement, k: &Kit) -> El {
    let id = r.id;
    let ack = Button::new("Acknowledge").variant(ButtonVariant::Ghost).build(k).key(format!("lead-replacement-ack-{id}")).on_press(move |s: &mut App| acknowledge(s, id));
    row()
        .key(format!("lead-replacement-{id}"))
        .w_full()
        .gap(10.0)
        .items_start()
        .px(14.0)
        .py(9.0)
        .radius(10.0)
        .fill(attention().alpha(0.10))
        .border(1.0, attention().alpha(0.35))
        .child(icon(&icons::ROTATE, k.text_size(14.0)).color(attention()))
        .child(
            col()
                .grow(1.0)
                .w(1.0)
                .min_w(0.0)
                .gap(3.0)
                .child(text(format!("@{} runs in this new conversation", r.name)).font(k.text(BODY_BOLD)).color(theme::text()).wrap().w_full())
                .child(text(r.reason.clone()).font(k.text(BODY)).color(muted()).wrap().w_full().max_lines(2)),
        )
        .child(ack)
}

fn turn(x: &Session, k: &Kit) -> Option<El> {
    let notice = feed::notice::turn_notice(x);
    if notice.is_empty() || !feed::notice::shared().shows(&x.id) {
        return None;
    }
    let id = x.id.clone();
    let never = text("Don\u{2019}t show again")
        .font(k.text(META))
        .color(accent())
        .key("turn-notice-hide")
        .role("button")
        .help("Hide this note for every terminal session. Turn it back on in Settings > General.")
        .on_press(|s: &mut App| {
            feed::notice::change(|n| n.set_shown(false));
            crate::settings::redraw();
            crate::over::layout::save(s);
        });
    let close = col()
        .key("turn-notice-close")
        .size(k.text_size(18.0), k.text_size(18.0))
        .radius(5.0)
        .items_center()
        .justify_center()
        .hover(Color::WHITE.alpha(0.08))
        .label("Hide for this session")
        .help("Hide for this session")
        .on_press(move |s: &mut App| {
            feed::notice::change(|n| n.dismiss(&id));
            crate::settings::redraw();
            crate::over::layout::save(s);
        })
        .child(icon(&icons::CLOSE, k.text_size(11.0)).color(faint()));
    Some(row().key("turn-notice").w_full().gap(8.0).items_start().px(15.0).pb(8.0).child(text(notice).font(k.text(META)).color(muted()).wrap().grow(1.0).w(1.0).min_w(0.0)).child(never).child(close))
}

pub fn above(s: &App, x: &Session, k: &Kit) -> Option<El> {
    let banners: Vec<El> = s.notices.replacements.iter().filter(|r| r.session_id == x.id && !s.notices.acking.contains(&r.id)).map(|r| banner(r, k)).collect();
    let error = s.notices.error.as_ref().filter(|_| !banners.is_empty()).map(|e| text(e.clone()).font(k.text(META)).color(attention()).wrap().w_full());
    let turn = turn(x, k);
    if banners.is_empty() && turn.is_none() {
        return None;
    }
    Some(col().key("composer-notices").w_full().gap(8.0).px(10.0).pt(10.0).children(banners).children(error).children(turn))
}
