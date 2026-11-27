use feed::notify::{NOTIFICATION_SETTINGS, Permission, daemon_posts};
use feed::prefs::{Notify, NotifyStatus, Preference};
use rune::{El, row};
use rune_kit::{Button, ButtonVariant, ControlSize, Kit};

use super::parts::{self, line, section};
use super::{Prefs, Read, Section, busy, notify, saving, write};

fn desktop(s: &Prefs, status: &NotifyStatus, k: &Kit) -> El {
    let toggling = busy(s);
    let on = status.enabled;
    let switch = parts::switch("desktop-notifications", "Desktop notifications", on, toggling, move |s| notify(s, if on { Notify::Disable } else { Notify::Enable }), k);
    let detail = if saving(s, Notify::Enable.action()) { "Asking macOS for permission…".to_string() } else { status.detail() };
    let test = Button::new("Send Test Notification")
        .control_size(ControlSize::Small)
        .disabled(!on || busy(s))
        .on_press(|s: &mut Prefs| notify(s, Notify::Test))
        .build(k)
        .key("settings-notify-test");
    let mut buttons = row().gap(8.0).items_center().px(14.0).py(10.0).child(test);
    if status.denied() {
        buttons = buttons.child(Button::new("Open System Settings…").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).disabled(busy(s)).on_press(|s: &mut Prefs| notify(s, Notify::OpenSettings)).build(k));
    }
    let problem = status.problem().is_some() && !saving(s, Notify::Enable.action());
    let mut rows = vec![line("Desktop notifications", (!problem).then_some(detail), switch, k)];
    if problem {
        rows.push(parts::problem(status.detail(), k));
    }
    rows.push(buttons);
    section("", rows, Some("Agenthail notifies when an agent finishes or fails, and when a message can't be delivered.".into()), k)
}

fn own(permission: Permission, k: &Kit) -> Option<El> {
    let (head, how) = permission.note()?;
    let control = if permission.warns() {
        Button::new("Open System Settings…").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|_: &mut Prefs| crate::open_settings(NOTIFICATION_SETTINGS)).build(k).key("settings-notify-permission")
    } else {
        row()
    };
    Some(line(head, Some(how.to_string()), control, k))
}

fn preferences(s: &Prefs, list: &[Preference], k: &Kit) -> El {
    let rows = list
        .iter()
        .map(|p| {
            let (id, value) = (p.id.clone(), p.value);
            let key = format!("preference-{}", p.id);
            let control = parts::switch(&key, &p.title, value, busy(s), move |s| {
                let id = id.clone();
                write(s, format!("preference-{id}"), Section::Notify, &[Read::Preferences], move |c| c.set_notification_preference(&id, !value).map(|_| crate::notify::preference_saved(&id, !value)));
            }, k);
            line(&p.title, Some(p.detail.clone()), control, k)
        })
        .collect();
    section("What notifies", rows, None, k)
}

pub fn page(s: &Prefs, k: &Kit) -> El {
    let mut out = parts::page();
    if let Some(f) = s.notes.get(&Section::Notify) {
        out = out.child(parts::failure(s, f, k));
    }
    match &s.settings {
        None => out = out.child(parts::reading("notification access", k)),
        Some(Err(f)) => out = out.child(parts::failure(s, f, k)),
        Some(Ok(st)) => out = out.child(desktop(s, &st.notifications, k)),
    }
    if !std::env::var_os("HOME").is_some_and(|home| daemon_posts(std::path::Path::new(&home)))
        && let Some(note) = s.permission.and_then(|p| own(p, k))
    {
        out = out.child(section("", vec![note], None, k));
    }
    match &s.preferences {
        None => out.child(parts::reading("notification preferences", k)),
        Some(Err(f)) => out.child(parts::failure(s, f, k)),
        Some(Ok(list)) if list.is_empty() => out,
        Some(Ok(list)) => out.child(preferences(s, list, k)),
    }
}
