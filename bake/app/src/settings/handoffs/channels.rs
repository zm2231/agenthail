use feed::handoffs::{self, Channel, Handoffs, session_ref};
use rune::{El, TextEdit, col, row};
use rune_kit::{Button, ButtonVariant, ControlSize, Kit, TextArea};

use super::super::parts::{self, line};
use super::super::{Prefs, Section, busy, saving};
use super::{Who, change, edited, field, forget, matches, note, refused, remove, small, status};

fn create(s: &mut Prefs) {
    let name = match handoffs::channel_name(&s.handoffs.channel_name.text) {
        Ok(name) => name,
        Err(p) => return note(s, Section::Channels, Some(p)),
    };
    change(s, Section::Channels, format!("channel-create-{name}"), move |c| c.channel_create(&name));
}

fn add_member(s: &mut Prefs) {
    let channel = s.handoffs.picked.clone();
    let Some(target) = session_ref(&s.handoffs.member.text) else {
        return note(s, Section::Channels, Some("Name the session to add."));
    };
    if !channel.is_empty() {
        change(s, Section::Channels, format!("channel-add-{channel}"), move |c| c.channel_add(&channel, &target));
    }
}

fn send(s: &mut Prefs) {
    let message = s.handoffs.message.text.trim().to_string();
    let channel = s.handoffs.picked.clone();
    if message.is_empty() || channel.is_empty() {
        return;
    }
    let (out, open) = (s.handoffs.sent.clone(), s.handoffs.unsettled.clone());
    let key = open.lock().unwrap().key_for(&channel, &message);
    change(s, Section::Channels, format!("channel-send-{channel}"), move |c| {
        let result = c.channel_send(&channel, &message, &key);
        open.lock().unwrap().record(&channel, &message, &key, &result);
        *out.lock().unwrap() = Some(format!("#{channel}: {}", result?.summary()));
        Ok(())
    });
}

fn members(s: &Prefs, c: &Channel, off: bool, k: &Kit) -> Vec<El> {
    let people = c.people();
    if people.is_empty() {
        return vec![parts::words(format!("#{} has no members yet.", c.name))];
    }
    people
        .into_iter()
        .map(|m| {
            let (channel, id) = (c.name.clone(), m.id.clone());
            let key = format!("channel-remove-{channel}-{id}");
            let button = remove(&key, off || saving(s, &key), move |s| {
                let (channel, id) = (channel.clone(), id.clone());
                change(s, Section::Channels, format!("channel-remove-{channel}-{id}"), move |c| c.channel_remove(&channel, &id));
            }, k);
            line(&m.display, None, button)
        })
        .collect()
}

fn picked(s: &Prefs, c: &Channel, off: bool, k: &Kit) -> Vec<El> {
    let f = &s.handoffs;
    let mut rows = members(s, c, off, k);
    let add = small("Add", "channel-add", off || session_ref(&f.member.text).is_none(), add_member, k);
    let input = field("channel-member", Section::Channels, &f.member, "Add a session", "Find a session or @alias", off, Some(Who::Member), |s| &mut s.handoffs.member, add_member, k);
    rows.push(line("Add a session", None, row().gap(8.0).items_center().child(input).child(add)));
    rows.extend(matches(s, Who::Member, off, k));
    let message = TextArea::new(&f.message)
        .label(format!("Message everyone in #{}", c.name))
        .placeholder(format!("Message everyone in #{}", c.name))
        .width(None)
        .auto_grow(2, 5)
        .disabled(off)
        .on_edit(|s: &mut Prefs| &mut s.handoffs.message)
        .on_change(|s: &mut Prefs, _: &mut rune::Tx| edited(s, Section::Channels))
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
    let send_button = small("Send", "channel-send", off || f.message.text.trim().is_empty(), send, k);
    rows.push(row().w_full().gap(8.0).items_center().px(14.0).py(10.0).child(delete).child(col().grow(1.0)).child(send_button));
    rows
}

fn pick(s: &mut Prefs, value: String) {
    if value != s.handoffs.picked {
        s.handoffs.picked = value;
        s.handoffs.member = TextEdit::new("");
        s.handoffs.message = TextEdit::new("");
        if !s.notes.get(&Section::Channels).is_some_and(refused) {
            s.notes.remove(&Section::Channels);
        }
        forget(s);
        note(s, Section::Channels, None);
    }
}

pub fn section(s: &Prefs, h: &Handoffs, off: bool, layer: &mut Vec<El>, k: &Kit) -> El {
    let f = &s.handoffs;
    let mut rows = Vec::new();
    if h.channels.is_empty() {
        rows.push(parts::words("No shared channels yet."));
    }
    let create_button = small("Create", "channel-create", off || handoffs::channel_name(&f.channel_name.text).is_err(), create, k);
    let input = field("channel-name", Section::Channels, &f.channel_name, "New channel", "launch-review", off, None, |s| &mut s.handoffs.channel_name, create, k);
    rows.push(line("New channel", None, row().gap(8.0).items_center().child(input).child(create_button)));
    if !h.channels.is_empty() {
        let choices = h.channels.iter().map(|c| (format!("#{}", c.name), c.name.clone())).collect();
        rows.push(line("Channel", None, parts::select(s, "settings-channel-pick", "Channel", choices, &f.picked, busy(s), layer, pick, k)));
    }
    if let Some(c) = h.channel(&f.picked) {
        rows.extend(picked(s, c, off, k));
    }
    rows.extend(status(s, Section::Channels));
    parts::section(&format!("Channels · {}", h.channels.len()), rows, Some("Everyone in a channel receives what is sent to it.".into()))
}
