use feed::prefs::{Notify, NotifyStatus, Preference};
use rune::{El, row};
use rune_kit::{Button, ButtonVariant, ControlSize};

use super::parts::{self, line, section};
use super::{Prefs, Read, Section, notify, saving, write};
use crate::theme;

fn desktop(s: &Prefs, status: &NotifyStatus) -> El {
    let k = theme::kit();
    let toggling = saving(s, Notify::Enable.action()) || saving(s, Notify::Disable.action());
    let on = status.enabled;
    let switch = parts::switch("desktop-notifications", "Desktop notifications", on, toggling, move |s| notify(s, if on { Notify::Disable } else { Notify::Enable }));
    let detail = if saving(s, Notify::Enable.action()) { "Asking macOS for permission…".to_string() } else { status.detail() };
    let test = Button::new("Send Test Notification")
        .control_size(ControlSize::Small)
        .disabled(!on || saving(s, Notify::Test.action()))
        .on_press(|s: &mut Prefs| notify(s, Notify::Test))
        .build(k)
        .key("settings-notify-test");
    let mut buttons = row().gap(8.0).items_center().px(14.0).py(10.0).child(test);
    if status.denied() {
        buttons = buttons.child(Button::new("Open System Settings…").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut Prefs| notify(s, Notify::OpenSettings)).build(k));
    }
    let problem = status.problem().is_some() && !saving(s, Notify::Enable.action());
    let mut rows = vec![line("Desktop notifications", (!problem).then_some(detail), switch)];
    if problem {
        rows.push(parts::problem(status.detail()));
    }
    rows.push(buttons);
    section("", rows, Some("Agenthail notifies when an agent finishes or fails, and when a message can't be delivered.".into()))
}

fn preferences(s: &Prefs, list: &[Preference]) -> El {
    let rows = list
        .iter()
        .map(|p| {
            let (id, value) = (p.id.clone(), p.value);
            let key = format!("preference-{}", p.id);
            let control = parts::switch(&key, &p.title, value, saving(s, &key), move |s| {
                let id = id.clone();
                write(s, format!("preference-{id}"), Section::Notify, &[Read::Preferences], move |c| c.set_notification_preference(&id, !value).map(drop));
            });
            line(&p.title, Some(p.detail.clone()), control)
        })
        .collect();
    section("What notifies", rows, None)
}

pub fn page(s: &Prefs) -> El {
    let mut out = parts::page();
    if let Some(f) = s.notes.get(&Section::Notify) {
        out = out.child(parts::failure(f));
    }
    match &s.settings {
        None => out = out.child(parts::reading("notification access")),
        Some(Err(f)) => out = out.child(parts::failure(f)),
        Some(Ok(st)) => out = out.child(desktop(s, &st.notifications)),
    }
    match &s.preferences {
        None => out.child(parts::reading("notification preferences")),
        Some(Err(f)) => out.child(parts::failure(f)),
        Some(Ok(list)) if list.is_empty() => out,
        Some(Ok(list)) => out.child(preferences(s, list)),
    }
}
