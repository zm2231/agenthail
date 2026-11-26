use std::sync::{Arc, Mutex};

use feed::handoffs::{self, Channel, GOAL_COMPLETED, Handoffs, Relay, RelayDraft, TURN_FINISHED};
use feed::prefs::Failure;
use rune::{El, TextEdit, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, TextArea, TextInput};
use time::OffsetDateTime;

use super::parts::{self, line, section};
use super::{Prefs, Read, Section, busy, explain, fetch, saving, token_verified, write};
use crate::theme::{self, BODY, META, attention, failed, muted};

pub const TAB: usize = 3;

const REREAD: &[Read] = &[Read::Base];

const READ_ONLY: &str = "This app's sign-in doesn't include the control permission, so channels and handoffs show read-only here.";

pub struct Form {
    pub relay_from: TextEdit,
    pub relay_to: TextEdit,
    pub relay_pattern: TextEdit,
    pub relay_handoff: String,
    pub relay_once: bool,
    pub channel_name: TextEdit,
    pub picked: String,
    pub member: TextEdit,
    pub message: TextEdit,
    pub problem: Option<(Section, &'static str)>,
    pub sent: Arc<Mutex<Option<String>>>,
}

impl Default for Form {
    fn default() -> Self {
        Form {
            relay_from: TextEdit::new(""),
            relay_to: TextEdit::new(""),
            relay_pattern: TextEdit::new(".*"),
            relay_handoff: TURN_FINISHED.into(),
            relay_once: false,
            channel_name: TextEdit::new(""),
            picked: String::new(),
            member: TextEdit::new(""),
            message: TextEdit::new(""),
            problem: None,
            sent: Arc::new(Mutex::new(None)),
        }
    }
}

pub fn shown(s: &Prefs) -> bool {
    s.tab == TAB
}

pub fn changed(s: &mut Prefs) {
    if shown(s) && s.client.is_some() && !busy(s) {
        s.settling.insert(Read::Base);
        fetch(s, Read::Base);
    }
}

fn writable(s: &Prefs) -> bool {
    !(matches!(&s.preferences, Some(Err(f)) if f.denied && f.scope == "control") && token_verified(s))
}

fn note(s: &mut Prefs, problem: Option<(Section, &'static str)>) {
    s.handoffs.problem = problem;
    *s.handoffs.sent.lock().unwrap() = None;
}

fn change(s: &mut Prefs, section: Section, key: String, job: impl FnOnce(&feed::catalog::Client) -> Result<(), Failure> + Send + 'static) {
    note(s, None);
    s.notes.remove(&Section::Relays);
    s.notes.remove(&Section::Channels);
    write(s, key, section, REREAD, job);
}

pub fn saved(s: &mut Prefs, key: &str) {
    let f = &mut s.handoffs;
    if key == "relay-add" {
        f.relay_from = TextEdit::new("");
        f.relay_to = TextEdit::new("");
        f.relay_pattern = TextEdit::new(".*");
        f.relay_once = false;
    } else if let Some(name) = key.strip_prefix("channel-create-") {
        f.channel_name = TextEdit::new("");
        f.picked = name.to_string();
    } else if key.starts_with("channel-add-") {
        f.member = TextEdit::new("");
    } else if key.starts_with("channel-send-") {
        f.message = TextEdit::new("");
    } else if key.strip_prefix("channel-delete-") == Some(f.picked.as_str()) {
        f.picked.clear();
    }
}

fn draft(s: &Prefs) -> RelayDraft {
    let f = &s.handoffs;
    RelayDraft { from: f.relay_from.text.clone(), to: f.relay_to.text.clone(), pattern: f.relay_pattern.text.clone(), handoff: f.relay_handoff.clone(), once: f.relay_once }
}

fn add_relay(s: &mut Prefs) {
    let d = draft(s);
    if let Some(p) = d.problem() {
        return note(s, Some((Section::Relays, p)));
    }
    if !busy(s) {
        change(s, Section::Relays, "relay-add".into(), move |c| c.relay_add(&d));
    }
}

fn create_channel(s: &mut Prefs) {
    let name = match handoffs::channel_name(&s.handoffs.channel_name.text) {
        Ok(name) => name,
        Err(p) => return note(s, Some((Section::Channels, p))),
    };
    if !busy(s) {
        change(s, Section::Channels, format!("channel-create-{name}"), move |c| c.channel_create(&name));
    }
}

fn add_member(s: &mut Prefs) {
    let (Some(target), channel) = (handoffs::session_ref(&s.handoffs.member.text), s.handoffs.picked.clone()) else {
        return note(s, Some((Section::Channels, "Name the session to add, by @alias or id.")));
    };
    if !busy(s) && !channel.is_empty() {
        change(s, Section::Channels, format!("channel-add-{channel}"), move |c| c.channel_add(&channel, &target));
    }
}

fn send(s: &mut Prefs) {
    let message = s.handoffs.message.text.trim().to_string();
    let channel = s.handoffs.picked.clone();
    if message.is_empty() || channel.is_empty() || busy(s) {
        return;
    }
    let out = s.handoffs.sent.clone();
    change(s, Section::Channels, format!("channel-send-{channel}"), move |c| {
        let sent = c.channel_send(&channel, &message)?;
        *out.lock().unwrap() = Some(format!("#{channel}: {}", sent.summary()));
        Ok(())
    });
}

fn small(label: &str, key: String, off: bool, press: impl Fn(&mut Prefs) + 'static) -> El {
    Button::new(label).control_size(ControlSize::Small).disabled(off).on_press(press).build(theme::kit()).key(key)
}

fn remove(label: &str, key: String, off: bool, press: impl Fn(&mut Prefs) + 'static) -> El {
    Button::new(label).variant(ButtonVariant::Ghost).control_size(ControlSize::Small).disabled(off).on_press(press).build(theme::kit()).key(key)
}

fn field(key: &str, edit: &TextEdit, label: &str, hint: &str, off: bool, pick: for<'a> fn(&'a mut Prefs) -> &'a mut TextEdit, submit: fn(&mut Prefs)) -> El {
    TextInput::new(edit)
        .label(label.to_string())
        .placeholder(hint.to_string())
        .width(Some(240.0))
        .disabled(off)
        .on_edit(pick)
        .on_change(|s: &mut Prefs, _: &mut Tx| s.handoffs.problem = None)
        .on_submit(move |s: &mut Prefs, _: &mut Tx| submit(s))
        .build(theme::kit())
        .key(format!("settings-{key}"))
}

fn relay_row(s: &Prefs, r: &Relay, now: OffsetDateTime, off: bool) -> El {
    let mut words = col().gap(2.0).grow(1.0).min_w(0.0).child(text(r.route()).font(BODY).color(theme::text()).wrap()).child(text(r.pattern.clone()).font(META).color(muted()).wrap()).child(text(r.state(now)).font(META).color(muted()).wrap());
    if let Some(problem) = r.problem() {
        words = words.child(text(problem.to_string()).font(META).color(attention()).wrap());
    }
    let id = r.id;
    let key = format!("relay-remove-{id}");
    let button = remove("Remove", format!("settings-{key}"), off || saving(s, &key), move |s| change(s, Section::Relays, format!("relay-remove-{id}"), move |c| c.relay_remove(id)));
    row().w_full().gap(16.0).items_center().px(14.0).py(10.0).child(words).child(button)
}

fn relays(s: &Prefs, h: &Handoffs, off: bool, layer: &mut Vec<El>) -> El {
    let now = OffsetDateTime::now_utc();
    let f = &s.handoffs;
    let mut rows: Vec<El> = h.relays.iter().map(|r| relay_row(s, r, now, off)).collect();
    if rows.is_empty() {
        rows.push(parts::words("No automatic handoffs yet."));
    }
    let choices = vec![("Turn finished".to_string(), TURN_FINISHED.to_string()), ("Goal completed".to_string(), GOAL_COMPLETED.to_string())];
    let when = parts::select(s, "settings-relay-handoff", "Hand off when", choices, &f.relay_handoff, off, layer, |s, value| s.handoffs.relay_handoff = value);
    let once = f.relay_once;
    rows.push(line("When", Some("The session whose replies hand off".into()), field("relay-from", &f.relay_from, "When", "@research or a session id", off, |s| &mut s.handoffs.relay_from, add_relay)));
    rows.push(line("Hand off when", None, when));
    rows.push(line("Finishes with text matching", Some("A regular expression; .* matches every reply".into()), field("relay-pattern", &f.relay_pattern, "Finishes with text matching", ".*", off, |s| &mut s.handoffs.relay_pattern, add_relay)));
    rows.push(line("Hand off to", None, field("relay-to", &f.relay_to, "Hand off to", "@builder or a session id", off, |s| &mut s.handoffs.relay_to, add_relay)));
    rows.push(line("Run this handoff once", None, parts::switch("relay-once", "Run this handoff once", once, off, move |s| s.handoffs.relay_once = !once)));
    let ready = draft(s).problem().is_none();
    rows.extend(status(s, Section::Relays));
    rows.push(row().w_full().px(14.0).py(10.0).child(col().grow(1.0)).child(small("Add Handoff", "settings-relay-add".into(), off || !ready, add_relay)));
    section(
        &format!("Automatic handoffs · {}", h.relays.len()),
        rows,
        Some("When one session finishes with matching text, Agenthail sends its reply to another.".into()),
    )
}

fn members(s: &Prefs, c: &Channel, off: bool) -> Vec<El> {
    let people = c.people();
    if people.is_empty() {
        return vec![parts::words(format!("#{} has no members yet.", c.name))];
    }
    people
        .into_iter()
        .map(|m| {
            let (channel, id) = (c.name.clone(), m.id.clone());
            let key = format!("channel-remove-{channel}-{id}");
            let button = remove("Remove", format!("settings-{key}"), off || saving(s, &key), move |s| {
                let (channel, id) = (channel.clone(), id.clone());
                change(s, Section::Channels, format!("channel-remove-{channel}-{id}"), move |c| c.channel_remove(&channel, &id));
            });
            line(&m.display, None, button)
        })
        .collect()
}

fn channel_rows(s: &Prefs, c: &Channel, off: bool) -> Vec<El> {
    let f = &s.handoffs;
    let k = theme::kit();
    let mut rows = members(s, c, off);
    let add = small("Add", "settings-channel-add".into(), off || handoffs::session_ref(&f.member.text).is_none(), add_member);
    rows.push(line("Add a session", None, row().gap(8.0).items_center().child(field("channel-member", &f.member, "Add a session", "@writer or a session id", off, |s| &mut s.handoffs.member, add_member)).child(add)));
    let message = TextArea::new(&f.message)
        .label(format!("Message everyone in #{}", c.name))
        .placeholder(format!("Message everyone in #{}", c.name))
        .width(None)
        .auto_grow(2, 5)
        .disabled(off)
        .on_edit(|s: &mut Prefs| &mut s.handoffs.message)
        .build(k)
        .key("settings-channel-message");
    rows.push(row().w_full().px(14.0).py(10.0).child(message.grow(1.0)));
    let name = c.name.clone();
    let delete_key = format!("channel-delete-{name}");
    let delete = Button::new("Delete Channel")
        .variant(ButtonVariant::Danger)
        .control_size(ControlSize::Small)
        .disabled(off || saving(s, &delete_key))
        .on_press(move |s: &mut Prefs| {
            let name = name.clone();
            change(s, Section::Channels, format!("channel-delete-{name}"), move |c| c.channel_delete(&name));
        })
        .build(k)
        .key("settings-channel-delete");
    let send_button = small("Send", "settings-channel-send".into(), off || f.message.text.trim().is_empty(), send);
    rows.push(row().w_full().gap(8.0).items_center().px(14.0).py(10.0).child(delete).child(col().grow(1.0)).child(send_button));
    rows
}

fn channels(s: &Prefs, h: &Handoffs, off: bool, layer: &mut Vec<El>) -> El {
    let f = &s.handoffs;
    let mut rows = Vec::new();
    if h.channels.is_empty() {
        rows.push(parts::words("No shared channels yet."));
    }
    let create = small("Create", "settings-channel-create".into(), off || handoffs::channel_name(&f.channel_name.text).is_err(), create_channel);
    rows.push(line("New channel", None, row().gap(8.0).items_center().child(field("channel-name", &f.channel_name, "New channel", "launch-review", off, |s| &mut s.handoffs.channel_name, create_channel)).child(create)));
    if !h.channels.is_empty() {
        let choices = h.channels.iter().map(|c| (format!("#{}", c.name), c.name.clone())).collect();
        let pick = parts::select(s, "settings-channel-pick", "Channel", choices, &f.picked, false, layer, |s, value| {
            s.handoffs.picked = value;
            s.handoffs.member = TextEdit::new("");
            s.handoffs.problem = None;
        });
        rows.push(line("Channel", None, pick));
    }
    if let Some(c) = h.channel(&f.picked) {
        rows.extend(channel_rows(s, c, off));
    }
    rows.extend(status(s, Section::Channels));
    section(&format!("Channels · {}", h.channels.len()), rows, Some("Everyone in a channel receives what is sent to it.".into()))
}

fn status(s: &Prefs, section: Section) -> Option<El> {
    let shown = match (s.handoffs.problem, s.notes.get(&section)) {
        (Some((at, p)), _) if at == section => text(p.to_string()).font(META).color(failed()).wrap().px(4.0),
        (_, Some(f)) => parts::failure(s, f),
        _ if section == Section::Channels => text(s.handoffs.sent.lock().unwrap().clone()?).font(META).color(muted()).wrap().px(4.0),
        _ => return None,
    };
    Some(row().w_full().px(10.0).py(8.0).child(shown.grow(1.0)))
}

pub fn page(s: &Prefs, layer: &mut Vec<El>) -> El {
    let mut out = parts::page();
    let h = match &s.base {
        None => return out.child(parts::reading("channels and handoffs")),
        Some(Err(f)) => return out.child(parts::failure(s, f)),
        Some(Ok(b)) => &b.handoffs,
    };
    let writable = writable(s);
    if !writable && let Some(Err(f)) = &s.preferences {
        out = out.child(parts::failure(s, &Failure { message: READ_ONLY.into(), scope: "", ..explain(s, f) }));
    }
    let off = !writable || busy(s);
    out.child(relays(s, h, off, layer)).child(channels(s, h, off, layer))
}
