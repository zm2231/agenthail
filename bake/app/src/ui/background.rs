use super::*;
use std::sync::OnceLock;

use feed::background::{BackgroundTasks, Clock};
use rune_kit::{ControlSize, IconButton, IconButtonVariant};

const HEADER_HEIGHT: f32 = 34.0;
const LINE_HEIGHT: f32 = 20.0;
const LINES_SHOWN: usize = 5;
const NOTE_LINES: usize = 2;
const LINES_PAD: f32 = 6.0;

fn global_default(key: &str) -> Option<String> {
    let out = std::process::Command::new("/usr/bin/defaults").args(["read", "-g", key]).output().ok()?;
    out.status.success().then(|| String::from_utf8_lossy(&out.stdout).trim().to_string())
}

fn clock() -> Clock {
    static CLOCK: OnceLock<Clock> = OnceLock::new();
    *CLOCK.get_or_init(|| {
        let set = |key: &str| global_default(key).is_some_and(|v| v == "1" || v.eq_ignore_ascii_case("true"));
        let locale = global_default("AppleLocale").or_else(|| std::env::var("LC_TIME").ok()).or_else(|| std::env::var("LANG").ok()).unwrap_or_default();
        Clock::from_settings(set("AppleICUForce24HourTime"), set("AppleICUForce12HourTime"), &locale)
    })
}

fn shown<'a>(s: &App, x: &'a Session) -> Option<&'a BackgroundTasks> {
    x.background_tasks.as_ref().filter(|b| s.dock.shows(b, &x.id))
}

fn lines(b: &BackgroundTasks) -> usize {
    if b.source_note().is_some() { LINES_SHOWN } else { b.tasks.len() }
}

pub(crate) fn reserve(s: &App, id: &str) -> f32 {
    let Some(b) = session(s, id).and_then(|x| shown(s, x)) else { return 0.0 };
    let open = if s.dock.is_open(b, id) { lines(b).min(LINES_SHOWN) as f32 * LINE_HEIGHT + LINES_PAD } else { 0.0 };
    HEADER_HEIGHT + open
}

fn toggle(s: &mut App, id: &str) {
    let Some(b) = session(s, id).and_then(|x| x.background_tasks.clone()) else { return };
    s.dock.toggle(&b, id);
    crate::over::layout::save(s);
}

fn dismiss(s: &mut App, id: &str) {
    let Some(b) = session(s, id).and_then(|x| x.background_tasks.clone()) else { return };
    s.dock.dismiss(&b, id);
    crate::over::layout::save(s);
}

pub(crate) fn dock(s: &App, x: &Session) -> Option<El> {
    let b = shown(s, x)?;
    let open = s.dock.is_open(b, &x.id);
    let k = theme::kit();
    let (on, off) = (x.id.clone(), x.id.clone());
    let header = row()
        .key("background-tasks-toggle")
        .grow(1.0)
        .min_w(0.0)
        .h(HEADER_HEIGHT)
        .gap(8.0)
        .items_center()
        .on_press(move |s: &mut App| toggle(s, &on))
        .child(icon(&icons::COG, 13.0).color(faint()))
        .child(text(b.summary()).font(META_BOLD).color(theme::text()).truncate())
        .child(spacer())
        .child(icon(if open { &icons::CHEVRON_UP } else { &icons::CHEVRON_DOWN }, 12.0).color(faint()));
    let close = IconButton::new(&icons::CLOSE)
        .variant(IconButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .label("Hide until the background tasks change")
        .help("Hide until the background tasks change")
        .on_press(move |s: &mut App| dismiss(s, &off))
        .build(k)
        .key("background-tasks-dismiss");
    let mut c = col().key("background-tasks").w_full().px(12.0).border_sides([0.0, 0.0, 1.0, 0.0], line()).child(row().w_full().gap(6.0).items_center().child(header).child(close));
    if open {
        let offset = |t: time::OffsetDateTime| rune::time::LocalTime::at(t.unix_timestamp() * 1000).offset;
        let list = col()
            .w_full()
            .pl(21.0)
            .pb(6.0)
            .children(b.tasks.iter().map(|t| {
                let line = t.timed_line(s.now, clock(), offset);
                row().key(format!("background-task-{}", t.id)).w_full().h(LINE_HEIGHT).items_center().help(line.clone()).child(text(line).font(META).color(muted()).truncate())
            }))
            .children(b.source_note().map(|n| row().w_full().min_h(NOTE_LINES as f32 * LINE_HEIGHT).items_center().child(text(n).font(META).color(faint()).wrap())));
        c = c.child(col().key("background-tasks-lines").w_full().max_h(LINES_SHOWN as f32 * LINE_HEIGHT + LINES_PAD).scroll(ScrollSpec { stick_bottom: false }).child(list));
    }
    Some(c)
}
