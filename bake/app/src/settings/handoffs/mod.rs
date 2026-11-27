mod channels;
mod queue;
mod relays;

use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use feed::catalog::Client;
use feed::handoffs::{Pick, RelayDraft, TURN_FINISHED, Unsettled, picks};
use feed::prefs::Failure;
use rune::{El, TextEdit, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Kit, TextInput};

use super::parts;
use super::{Prefs, Read, Section, WINDOW, busy, explain, fetch, token_verified, write};
use crate::theme::{META, failed, muted};

pub const TAB: usize = 3;

const REREAD: &[Read] = &[Read::Base];

const READ_ONLY: &str = "This app's sign-in doesn't include the control permission, so channels and handoffs show read-only here.";

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Who {
    From,
    To,
    Member,
}

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
    pub problems: HashMap<Section, &'static str>,
    pub sent: Arc<Mutex<Option<String>>>,
    pub unsettled: Arc<Mutex<Unsettled>>,
    pub found: Option<(Who, Vec<Pick>)>,
    searching: Arc<AtomicU64>,
    stale: bool,
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
            problems: HashMap::new(),
            sent: Arc::new(Mutex::new(None)),
            unsettled: Arc::new(Mutex::new(Unsettled::default())),
            found: None,
            searching: Arc::new(AtomicU64::new(0)),
            stale: false,
        }
    }
}

pub fn changed(s: &mut Prefs) {
    if s.tab != TAB || s.client.is_none() {
        return;
    }
    if busy(s) {
        s.handoffs.stale = true;
        return;
    }
    s.handoffs.stale = false;
    s.settling.insert(Read::Base);
    fetch(s, Read::Base);
}

pub fn flush(s: &mut Prefs) {
    if busy(s) {
        return;
    }
    if let Some(Ok(base)) = &s.base {
        let listed = &base.handoffs;
        s.handoffs.unsettled.lock().unwrap().keep_channels(listed);
        if !s.handoffs.picked.is_empty() && listed.channel(&s.handoffs.picked).is_none() {
            s.handoffs.picked.clear();
            s.handoffs.message = TextEdit::new("");
            s.handoffs.member = TextEdit::new("");
            edited(s, Section::Channels);
        }
    }
    if s.handoffs.stale {
        changed(s);
    }
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
        f.member = TextEdit::new("");
        f.message = TextEdit::new("");
    } else if key.strip_prefix("channel-add-") == Some(f.picked.as_str()) {
        f.member = TextEdit::new("");
    } else if key.strip_prefix("channel-send-") == Some(f.picked.as_str()) {
        f.message = TextEdit::new("");
    } else if let Some(name) = key.strip_prefix("channel-delete-") {
        f.unsettled.lock().unwrap().forget_channel(name);
        if name == f.picked {
            f.picked.clear();
            f.message = TextEdit::new("");
        }
    }
}

fn refused(f: &Failure) -> bool {
    f.denied && f.scope == "control"
}

pub fn writable(s: &Prefs) -> bool {
    let read = matches!(&s.preferences, Some(Err(f)) if refused(f));
    let wrote = [Section::Relays, Section::Channels].iter().any(|at| s.notes.get(at).is_some_and(refused));
    !((read || wrote) && token_verified(s))
}

fn note(s: &mut Prefs, section: Section, problem: Option<&'static str>) {
    match problem {
        Some(p) => s.handoffs.problems.insert(section, p),
        None => s.handoffs.problems.remove(&section),
    };
    if section == Section::Channels {
        *s.handoffs.sent.lock().unwrap() = None;
    }
}

pub fn edited(s: &mut Prefs, section: Section) {
    note(s, section, None);
    if !s.notes.get(&section).is_some_and(refused) {
        s.notes.remove(&section);
    }
}

fn change(s: &mut Prefs, section: Section, key: String, job: impl FnOnce(&Client) -> Result<(), Failure> + Send + 'static) {
    if busy(s) {
        return;
    }
    note(s, section, None);
    forget(s);
    write(s, key, section, REREAD, job);
}

fn text_of(s: &mut Prefs, who: Who) -> &mut TextEdit {
    match who {
        Who::From => &mut s.handoffs.relay_from,
        Who::To => &mut s.handoffs.relay_to,
        Who::Member => &mut s.handoffs.member,
    }
}

fn search(s: &mut Prefs, who: Who) {
    let mine = s.handoffs.searching.fetch_add(1, Ordering::AcqRel) + 1;
    s.handoffs.found = None;
    let query = text_of(s, who).text.trim().to_string();
    let (Some(client), Some(handle)) = (s.client.clone(), WINDOW.with(|w| w.get().map(|(h, _)| h.clone()))) else { return };
    if query.is_empty() {
        return;
    }
    let current = s.handoffs.searching.clone();
    std::thread::spawn(move || {
        std::thread::sleep(Duration::from_millis(250));
        if current.load(Ordering::Acquire) != mine {
            return;
        }
        let Ok(found) = client.find_sessions(&query) else { return };
        let found = picks(found, who != Who::From);
        handle.update(move |s: &mut Prefs, _| {
            if s.handoffs.searching.load(Ordering::Acquire) == mine {
                s.handoffs.found = Some((who, found));
            }
        });
    });
}

pub fn forget(s: &mut Prefs) {
    s.handoffs.searching.fetch_add(1, Ordering::AcqRel);
    s.handoffs.found = None;
}

fn choose(s: &mut Prefs, who: Who, target: &str) {
    forget(s);
    *text_of(s, who) = TextEdit::new(target);
    edited(s, if who == Who::Member { Section::Channels } else { Section::Relays });
}

pub fn matches(s: &Prefs, who: Who, off: bool, k: &Kit) -> Option<El> {
    let (at, found) = s.handoffs.found.as_ref()?;
    if *at != who || off {
        return None;
    }
    let mut list = col().w_full().gap(2.0).px(10.0).pb(8.0);
    if found.is_empty() {
        list = list.child(text("No session matches.").font(META).color(muted()).px(4.0));
    }
    for (i, p) in found.iter().take(6).enumerate() {
        let target = p.target();
        let label = if p.label() == target { target.clone() } else { format!("{} · {target}", p.label()) };
        let pick = Button::new(label).variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut Prefs| choose(s, who, &target)).build(k).key(format!("settings-match-{i}"));
        list = list.child(row().w_full().child(pick));
    }
    Some(list)
}

pub fn field(key: &str, section: Section, edit: &TextEdit, label: &str, hint: &str, off: bool, who: Option<Who>, pick: for<'a> fn(&'a mut Prefs) -> &'a mut TextEdit, submit: fn(&mut Prefs), k: &Kit) -> El {
    TextInput::new(edit)
        .label(label.to_string())
        .placeholder(hint.to_string())
        .width(Some(240.0))
        .disabled(off)
        .on_edit(pick)
        .on_change(move |s: &mut Prefs, _: &mut Tx| {
            edited(s, section);
            if let Some(who) = who {
                search(s, who);
            }
        })
        .on_submit(move |s: &mut Prefs, _: &mut Tx| submit(s))
        .build(k)
        .key(format!("settings-{key}"))
}

pub fn small(label: &str, key: &str, off: bool, press: impl Fn(&mut Prefs) + 'static, k: &Kit) -> El {
    Button::new(label).control_size(ControlSize::Small).disabled(off).on_press(press).build(k).key(format!("settings-{key}"))
}

pub fn remove(key: &str, off: bool, press: impl Fn(&mut Prefs) + 'static, k: &Kit) -> El {
    Button::new("Remove").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).disabled(off).on_press(press).build(k).key(format!("settings-{key}"))
}

pub fn status(s: &Prefs, section: Section) -> Option<El> {
    let note = s.notes.get(&section).filter(|f| !(refused(f) && !writable(s)));
    let shown = match (s.handoffs.problems.get(&section), note) {
        (Some(p), _) => text(p.to_string()).font(META).color(failed()).wrap().px(4.0),
        (_, Some(f)) => parts::failure(s, f),
        _ if section == Section::Channels => text(s.handoffs.sent.lock().unwrap().clone()?).font(META).color(muted()).wrap().px(4.0),
        _ => return None,
    };
    Some(row().w_full().px(10.0).py(8.0).child(shown.grow(1.0)))
}

pub fn draft(s: &Prefs) -> RelayDraft {
    let f = &s.handoffs;
    RelayDraft { from: f.relay_from.text.clone(), to: f.relay_to.text.clone(), pattern: f.relay_pattern.text.clone(), handoff: f.relay_handoff.clone(), once: f.relay_once }
}

fn read_only(s: &Prefs) -> Failure {
    let why = match &s.preferences {
        Some(Err(f)) if refused(f) => f.clone(),
        _ => [Section::Relays, Section::Channels].iter().find_map(|at| s.notes.get(at).filter(|f| refused(f))).cloned().unwrap_or_else(|| Failure::new("")),
    };
    Failure { message: READ_ONLY.into(), scope: "", ..explain(s, &why) }
}

pub fn page(s: &Prefs, layer: &mut Vec<El>, k: &Kit) -> El {
    let mut out = parts::page();
    let h = match &s.base {
        None => return out.child(parts::reading("channels and handoffs")),
        Some(Err(f)) => return out.child(parts::failure(s, f)),
        Some(Ok(b)) => &b.handoffs,
    };
    let writable = writable(s);
    if !writable {
        out = out.child(parts::failure(s, &read_only(s)));
    }
    let off = !writable || busy(s);
    out.child(queue::waiting(s, h, off, k)).child(queue::recent(h)).child(relays::section(s, h, off, layer, k)).child(channels::section(s, h, off, layer, k))
}
