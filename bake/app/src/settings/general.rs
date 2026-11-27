use feed::prefs::{self, Failure, Person};
use rune::{El, Tx, row};
use rune_kit::{Button, ButtonVariant, ControlSize, RadioGroup, RadioItem, TextInput};

use super::parts::{self, line, section};
use super::{Prefs, Read, Section, busy, write};
use crate::theme;

const READ_ONLY: &str = "This app's sign-in doesn't include the settings permission, so these show read-only. Usage limit and Claude root settings need that permission to be read.";

const GENERAL: &[Read] = &[Read::Settings, Read::Base];

fn save_name(s: &mut Prefs, current: &Person) {
    if let Some(name) = prefs::name_submission(&s.name.text, current) {
        write(s, "person-name", Section::General, &[Read::Base], move |c| c.set_person_name(&name));
    }
}

fn name_section(s: &Prefs, person: &Person, writable: bool) -> El {
    let k = theme::kit();
    let busy = busy(s);
    let current = person.clone();
    let input = TextInput::new(&s.name)
        .label("Your name")
        .placeholder(person.name.clone())
        .width(None)
        .disabled(!writable || busy)
        .on_edit(|s: &mut Prefs| &mut s.name)
        .on_submit(move |s: &mut Prefs, _: &mut Tx| save_name(s, &current))
        .build(k);
    let current = person.clone();
    let save = Button::new("Save")
        .control_size(ControlSize::Small)
        .disabled(!writable || busy || prefs::name_submission(&s.name.text, person).is_none())
        .on_press(move |s: &mut Prefs| save_name(s, &current))
        .build(k)
        .key("settings-name-save");
    let mut buttons = row().gap(8.0).items_center().child(save);
    if person.set {
        let reset = Button::new("Use This Account's Name")
            .variant(ButtonVariant::Ghost)
            .control_size(ControlSize::Small)
            .disabled(!writable || busy)
            .on_press(|s: &mut Prefs| {
                s.name = rune::TextEdit::new("");
                write(s, "person-name", Section::General, &[Read::Base], |c| c.set_person_name(""));
            })
            .build(k)
            .key("settings-name-reset");
        buttons = buttons.child(reset);
    }
    let field = rune::col().w_full().gap(10.0).px(14.0).py(12.0).child(input).child(buttons);
    section("Your name", vec![field], Some(prefs::name_footer(person)))
}

fn composer() -> El {
    let shown = !feed::notice::shared().hidden;
    let flip = parts::switch("turn-notice", "Show the terminal session note above the composer", shown, false, move |_: &mut Prefs| feed::notice::change(|n| n.set_shown(!shown)));
    section("Composer", vec![line("Show the terminal session note above the composer", None, flip)], None)
}

fn delivery(s: &Prefs, current: &str, writable: bool) -> El {
    let k = theme::kit();
    let off = !writable || super::busy(s);
    let item = |label: &str, mode: &'static str| {
        RadioItem::new(label, current == mode).disabled(off).on_select(move |s: &mut Prefs| {
            write(s, "delivery", Section::General, GENERAL, move |c| c.change_delivery(prefs::Delivery::Busy(mode.into())));
        })
    };
    let group = RadioGroup::new().item(item("Queue the message (default)", "queue")).item(item("Steer the running turn", "steer")).disabled(off).build(k);
    section(
        "Sending while an agent works",
        vec![row().w_full().px(14.0).py(12.0).child(group).key("settings-delivery")],
        Some("The Steer now / After this turn chip by the composer switches one message the other way. Agents that can't be steered mid-turn send queued messages after the turn. This setting is shared with your iPhone and other Agenthail apps.".into()),
    )
}

fn recent(s: &Prefs, hours: i64, writable: bool, layer: &mut Vec<El>) -> El {
    let choices = prefs::codex_recent_choices(hours).into_iter().map(|h| (prefs::hours_label(h), h.to_string())).collect();
    let pick = parts::select(s, "settings-codex-recent", "Codex recent window", choices, &hours.to_string(), !writable || super::busy(s), layer, move |s, value| {
        let Ok(h) = value.parse::<i64>() else { return };
        if h != hours {
            write(s, "delivery", Section::General, GENERAL, move |c| c.change_delivery(prefs::Delivery::Hours(h)));
        }
    });
    section("Current work", vec![line("Codex recent window", None, pick)], Some("Codex threads stay in Recent this long after their last activity.".into()))
}

fn limits(s: &Prefs, d: &prefs::Dashboard, writable: bool) -> El {
    let resumes = d.resumes_after_limit();
    let moves = d.moves_after_limit();
    let resume = parts::switch("resume-after-limit", "Resume sessions after a usage limit resets", resumes, !writable || busy(s), move |s| {
        write(s, "resume", Section::General, GENERAL, move |c| c.set_resume_after_limit(!resumes));
    });
    let mv = parts::switch("move-after-limit", "Move stopped sessions to a proxy with room", moves, !writable || !resumes || busy(s), move |s| {
        write(s, "move", Section::General, GENERAL, move |c| c.set_move_after_limit(!moves));
    });
    section(
        "After a usage limit",
        vec![line("Resume sessions after a usage limit resets", None, resume), line("Move stopped sessions to a proxy with room", None, mv)],
        Some("A Claude session a usage limit stopped is sent its queued messages once the limit resets, or asked to continue when it has none. Turned off, queued messages still wait for the reset. A session Agenthail runs can instead move to a Claude account proxy that has room and resume there.".into()),
    )
}

fn default_root(s: &Prefs, settings: &prefs::Settings, layer: &mut Vec<El>) -> Option<El> {
    if settings.claude_roots.len() < 2 {
        return None;
    }
    let mut choices = vec![("The primary root".to_string(), String::new())];
    choices.extend(settings.claude_roots.iter().map(|r| (r.clone(), r.clone())));
    let current = settings.claude_default_root.clone();
    let shown = current.clone();
    let pick = parts::select(s, "settings-default-root", "Claude root for sessions you start", choices, &shown, busy(s), layer, move |s, value| {
        if value != current {
            write(s, "default-root", Section::General, GENERAL, move |c| c.set_claude_default_root(&value));
        }
    });
    Some(section(
        "Claude root",
        vec![line("Claude root for sessions you start", None, pick)],
        Some("A session an agent starts stays in the agent's root, and a replacement lead in the old lead's root. Sessions never move to another root on their own.".into()),
    ))
}

pub fn page(s: &Prefs, layer: &mut Vec<El>) -> El {
    let mut out = parts::page();
    let settings = s.settings.as_ref().and_then(|r| r.as_ref().ok());
    let base = s.base.as_ref().and_then(|r| r.as_ref().ok());
    let writable = settings.is_some();
    match &s.settings {
        None => out = out.child(parts::reading("settings")),
        Some(Err(f)) if f.denied && super::token_verified(s) => out = out.child(parts::failure(s, &Failure { message: READ_ONLY.into(), scope: "", ..f.clone() })),
        Some(Err(f)) => out = out.child(parts::failure(s, f)),
        Some(Ok(_)) => {}
    }
    if let Some(f) = s.notes.get(&Section::General) {
        out = out.child(parts::failure(s, f));
    }
    if let Some(person) = base.and_then(|b| b.person.as_ref()) {
        out = out.child(name_section(s, person, writable));
    }
    out = out.child(composer());
    let (busy, hours) = match (settings, base) {
        (Some(st), _) => (st.dashboard.busy_delivery.clone(), st.dashboard.codex_recent_hours),
        (None, Some(b)) => (b.busy_delivery.clone(), b.codex_recent_hours),
        (None, None) => return out,
    };
    let busy = if busy.is_empty() { "queue".to_string() } else { busy };
    out = out.child(delivery(s, &busy, writable)).child(recent(s, hours, writable, layer));
    if let Some(st) = settings {
        out = out.child(limits(s, &st.dashboard, writable));
        if let Some(root) = default_root(s, st, layer) {
            out = out.child(root);
        }
    }
    out
}
