use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::catalog::{Client, RespondError, Session};
use feed::create::{ConfigRoot, RootUsage};
use feed::limit::{Moved, Stop, Target, targets};
use rune::{Color, El, col, el, icon, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog};

use super::spacer;
use crate::icons;
use crate::theme::{self, BODY_BOLD, LABEL, META, TITLE, attention, failed, faint, muted};
use crate::{App, session};

pub enum Done {
    Roots(String, u64, Result<Vec<ConfigRoot>, String>),
    Usage(String, u64, RootUsage),
    Unread(String, u64, String),
    Read(String, u64),
    Resumed(String, bool, Result<(), RespondError>),
    Moved(String, Target, Result<Moved, RespondError>),
}

pub struct Confirm {
    pub session: String,
    pub target: Target,
}

pub struct Consent {
    pub session: String,
    pub target: Target,
    pub message: String,
}

pub struct Limits {
    pub picking: Option<String>,
    pub confirm: Option<Confirm>,
    pub consent: Option<Consent>,
    pub roots: HashMap<String, Result<Vec<ConfigRoot>, String>>,
    pub unread: HashSet<(String, String)>,
    pub reading: HashMap<String, u64>,
    pub refresh_error: HashMap<String, String>,
    generation: u64,
    pub busy: HashSet<String>,
    pub note: HashMap<String, (String, bool)>,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl Limits {
    pub fn new() -> Limits {
        let (tx, rx) = channel();
        Limits { picking: None, confirm: None, consent: None, roots: HashMap::new(), unread: HashSet::new(), reading: HashMap::new(), refresh_error: HashMap::new(), generation: 0, busy: HashSet::new(), note: HashMap::new(), tx, rx }
    }
}

fn host_key(x: &Session) -> String {
    if x.local() { String::new() } else { x.host.id.clone() }
}

fn spawn(s: &App, job: impl FnOnce(&Client, &Sender<Done>) + Send + 'static) {
    let Some(client) = s.client.clone() else { return };
    let tx = s.limits.tx.clone();
    std::thread::spawn(move || job(&client, &tx));
}

fn not_sent(s: &mut App) -> bool {
    if s.client.is_none() {
        s.ov.notice = Some("Fixture mode: not sent".into());
        return true;
    }
    false
}

fn failure(what: &str, error: &RespondError) -> String {
    if error.maybe_delivered { format!("{what} may not have gone through: {}", error.message) } else { format!("{what} didn't work: {}", error.message) }
}

fn resume(s: &mut App, id: &str, now: bool) {
    if not_sent(s) || !s.limits.busy.insert(id.to_string()) {
        return;
    }
    s.limits.note.remove(id);
    let id = id.to_string();
    spawn(s, move |c, tx| {
        let _ = tx.send(Done::Resumed(id.clone(), now, c.limit_resume(&id, now)));
    });
}

fn open_picker(s: &mut App, id: &str) {
    let Some(x) = session(s, id) else { return };
    let host = host_key(x);
    s.limits.picking = Some(id.to_string());
    if s.client.is_none() {
        let recorded = s.fixtures.as_ref().and_then(|f| std::fs::read_to_string(format!("{}/session-options.json", f.dir)).ok());
        if let Some(options) = recorded.and_then(|t| serde_json::from_str::<feed::create::SessionOptions>(&t).ok()) {
            s.limits.roots.insert(host, Ok(options.claude_config_roots));
        }
        return;
    }
    s.limits.generation += 1;
    let generation = s.limits.generation;
    s.limits.reading.insert(host.clone(), generation);
    spawn(s, move |c, tx| {
        let roots = c.session_options(&host).map(|o| o.claude_config_roots).map_err(|e| e.to_string());
        let readable: Vec<String> = roots.as_ref().map(|r| r.iter().filter(|r| r.reads_usage()).map(|r| r.root.clone()).collect()).unwrap_or_default();
        let _ = tx.send(Done::Roots(host.clone(), generation, roots));
        for root in readable {
            let _ = tx.send(match c.claude_usage(&root, &host) {
                Ok(usage) => Done::Usage(host.clone(), generation, usage),
                Err(_) => Done::Unread(host.clone(), generation, root),
            });
        }
        let _ = tx.send(Done::Read(host, generation));
    });
}

fn move_to(s: &mut App, id: &str, target: Target, stop_terminal: bool) {
    s.limits.confirm = None;
    s.limits.consent = None;
    s.limits.picking = None;
    if not_sent(s) || !s.limits.busy.insert(id.to_string()) {
        return;
    }
    s.limits.note.insert(id.to_string(), (format!("Moving to {}…", target.name), false));
    let id = id.to_string();
    spawn(s, move |c, tx| {
        let result = c.limit_move(&id, Some(&target.root), stop_terminal);
        let _ = tx.send(Done::Moved(id, target, result));
    });
}

fn settled(s: &mut App, done: Done) {
    match done {
        Done::Roots(host, generation, _) | Done::Usage(host, generation, _) | Done::Unread(host, generation, _) | Done::Read(host, generation)
            if s.limits.reading.get(&host) != Some(&generation) => {}
        Done::Roots(host, _, roots) => {
            match roots {
                Err(error) if matches!(s.limits.roots.get(&host), Some(Ok(_))) => {
                    s.limits.refresh_error.insert(host, error);
                }
                roots => {
                    s.limits.unread.retain(|(h, _)| *h != host);
                    s.limits.refresh_error.remove(&host);
                    s.limits.roots.insert(host, roots);
                }
            }
        }
        Done::Unread(host, _, root) => {
            s.limits.unread.insert((host, root));
        }
        Done::Read(host, _) => {
            s.limits.reading.remove(&host);
        }
        Done::Usage(host, _, usage) => {
            if let Some(Ok(roots)) = s.limits.roots.get_mut(&host)
                && let Some(root) = roots.iter_mut().find(|r| r.root == usage.root)
            {
                root.take_usage(usage);
            }
        }
        Done::Resumed(id, now, result) => {
            s.limits.busy.remove(&id);
            let note = match result {
                Ok(()) if now => ("Asked it to continue".to_string(), false),
                Ok(()) => ("It stays stopped when the limit resets".to_string(), false),
                Err(error) => (failure(if now { "Resuming" } else { "Keeping it stopped" }, &error), true),
            };
            s.limits.note.insert(id, note);
        }
        Done::Moved(id, target, result) => {
            s.limits.busy.remove(&id);
            match result {
                Ok(Moved::Done) => {
                    s.limits.note.insert(id, (format!("Moved to {}", target.name), false));
                }
                Ok(Moved::NeedsConsent(message)) => {
                    s.limits.note.remove(&id);
                    s.limits.consent = Some(Consent { session: id, target, message });
                }
                Err(error) => {
                    s.limits.note.insert(id, (failure("Moving", &error), true));
                }
            }
        }
    }
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok(done) = s.limits.rx.try_recv() {
        changed = true;
        settled(s, done);
    }
    changed
}

fn button(label: impl Into<String>, variant: ButtonVariant, disabled: bool) -> Button {
    Button::new(label).variant(variant).control_size(ControlSize::Small).disabled(disabled)
}

pub(crate) fn card(s: &App, x: &Session, stop: &Stop) -> El {
    let k = theme::kit();
    let busy = s.limits.busy.contains(&x.id);
    let mut body = col()
        .grow(1.0)
        .min_w(0.0)
        .gap(8.0)
        .child(text("USAGE LIMIT").font(LABEL).color(attention()))
        .child(text(stop.label.clone()).font(TITLE).color(theme::text()).wrap());
    if !x.display.detail.is_empty() && x.display.detail != stop.label {
        body = body.child(text(x.display.detail.clone()).font(META).color(muted()).wrap());
    }
    if stop.held && !x.read_only {
        let mut actions = row().gap(8.0).pt(4.0).items_center().flex_wrap();
        let id = x.id.clone();
        actions = actions.child(button("Resume now", ButtonVariant::Submit, busy).icon(&icons::ROTATE).help("Send the session its queued messages, or ask it to continue, now").on_press(move |s: &mut App| resume(s, &id, true)).build(k).key("limit-resume"));
        if stop.waiting {
            let id = x.id.clone();
            actions = actions.child(button("Don't resume", ButtonVariant::Ghost, busy).help("Keep the session stopped when its usage limit resets").on_press(move |s: &mut App| resume(s, &id, false)).build(k).key("limit-cancel"));
        }
        if let Some(offer) = stop.offer.as_ref().filter(|_| stop.waiting) {
            let target = Target { root: offer.root.clone(), name: offer.name(), detail: offer.reason.clone(), full: false, offered: true };
            let id = x.id.clone();
            let help = match &offer.reason {
                Some(reason) if !reason.is_empty() => format!("Continue this session under {}: {reason}", offer.name()),
                _ => format!("Continue this session under {}", offer.name()),
            };
            actions = actions.child(
                button(format!("Move to {}…", offer.name()), ButtonVariant::Neutral, busy)
                    .icon(&icons::ARROW_RIGHT)
                    .help(help)
                    .on_press(move |s: &mut App| s.limits.confirm = Some(Confirm { session: id.clone(), target: target.clone() }))
                    .build(k)
                    .key("limit-move"),
            );
        }
        let id = x.id.clone();
        actions = actions.children(stop.waiting.then(|| button("Another account…", ButtonVariant::Ghost, busy).help("Pick the Claude account this session continues under").on_press(move |s: &mut App| open_picker(s, &id)).build(k).key("limit-pick")));
        actions = actions.child(spacer()).child(Button::new("Usage").variant(ButtonVariant::GhostMuted).control_size(ControlSize::Small).icon(&icons::ACTIVITY).on_press(super::accounts::open).build(k).key("limit-usage"));
        body = body.child(actions);
    }
    if let Some((note, bad)) = s.limits.note.get(&x.id) {
        body = body.child(text(note.clone()).font(META).color(if *bad { failed() } else { muted() }).wrap());
    }
    let card_el = row()
        .key("limit-card")
        .w_full()
        .max_w(780.0)
        .gap(14.0)
        .items_start()
        .px(18.0)
        .py(16.0)
        .radius(16.0)
        .linear_gradient(135.0, [(0.0, attention().alpha(0.14)), (1.0, attention().alpha(0.03))])
        .border_gradient(1.0, attention().alpha(0.5), attention().alpha(0.12))
        .child(col().size(34.0, 34.0).radius(10.0).items_center().justify_center().fill(attention().alpha(0.18)).child(icon(&icons::GAUGE, 17.0).color(attention())))
        .child(body);
    col().w_full().items_center().px(32.0).pb(12.0).child(card_el)
}

fn target_row(id: &str, t: &Target, busy: bool, unread: bool) -> El {
    let usable = !t.full && !busy;
    let mut name = row().gap(8.0).items_center().child(text(t.name.clone()).font(BODY_BOLD).color(if usable { theme::text() } else { faint() }).truncate());
    if t.offered {
        name = name.child(row().px(7.0).py(2.0).radius(6.0).fill(attention().alpha(0.2)).child(text("Suggested").font(META).color(attention())));
    }
    let detail = match (t.full, t.detail.clone(), unread) {
        (true, _, _) => Some("Out of usage".to_string()),
        (false, Some(detail), true) => Some(format!("{detail} · usage couldn't be read")),
        (false, None, true) => Some("Usage couldn't be read".to_string()),
        (false, detail, false) => detail,
    };
    let mut r = row()
        .key(format!("limit-target-{}", t.root))
        .w_full()
        .gap(12.0)
        .items_center()
        .px(12.0)
        .py(9.0)
        .radius(10.0)
        .fill(Color::WHITE.alpha(0.04))
        .border(1.0, Color::WHITE.alpha(0.08))
        .child(col().grow(1.0).min_w(0.0).gap(2.0).child(name).children(detail.map(|d| text(d).font(META).color(muted()).truncate())));
    if usable {
        let (id, target) = (id.to_string(), t.clone());
        r = r.hover(Color::WHITE.alpha(0.09)).on_press(move |s: &mut App| {
            s.limits.picking = None;
            s.limits.confirm = Some(Confirm { session: id.clone(), target: target.clone() });
        });
        r = r.child(icon(&icons::ARROW_RIGHT, 13.0).color(faint()));
    } else {
        r = r.opacity(0.6);
    }
    r
}

fn picker(s: &App, id: &str, layer: &mut Vec<El>) {
    let k = theme::kit();
    let Some(x) = session(s, id) else { return };
    let Some(stop) = x.limit(s.now) else { return };
    let busy = s.limits.busy.contains(id);
    let current = x.claude_config_root.as_ref().map(|r| r.root.as_str());
    let mut body = col().w_full().gap(6.0).px(16.0).pb(16.0);
    match s.limits.roots.get(&host_key(x)) {
        None if s.client.is_some() => body = body.child(text("Reading this computer's Claude accounts…").font(META).color(muted())),
        Some(Err(error)) => body = body.child(text(format!("Couldn't read the accounts: {error}")).font(META).color(failed()).wrap()),
        _ => {
            let roots = match s.limits.roots.get(&host_key(x)) {
                Some(Ok(roots)) => roots.as_slice(),
                _ => &[],
            };
            let list = targets(&stop, current, roots);
            if list.is_empty() {
                body = body.child(text("No other Claude account is set up on this computer.").font(META).color(muted()).wrap());
            }
            let host = host_key(x);
            body = body.children(list.iter().map(|t| target_row(id, t, busy, s.limits.unread.contains(&(host.clone(), t.root.clone())))));
            if let Some(error) = s.limits.refresh_error.get(&host) {
                body = body.child(text(format!("Couldn't refresh the accounts, so this list may be out of date: {error}")).font(META).color(failed()).wrap());
            } else if s.limits.reading.contains_key(&host) {
                body = body.child(text("Checking for changes…").font(META).color(faint()));
            }
        }
    }
    if let Some(root) = x.claude_config_root.as_ref().filter(|r| !r.name.is_empty()) {
        body = body.child(text(format!("Now under {}", root.name)).font(META).color(faint()).pt(4.0));
    }
    body = body.child(row().w_full().pt(6.0).child(el().grow(1.0)).child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut App| s.limits.picking = None).build(k).key("limit-pick-cancel")));
    Dialog::new("limit-pick", format!("Move {} to another account", x.title()))
        .description("It stops here and continues under the account you pick.")
        .open(true)
        .fit()
        .width(440.0)
        .on_close(|s: &mut App| s.limits.picking = None)
        .child(body)
        .build(k, layer);
}

fn confirm(c: &Confirm, layer: &mut Vec<El>) {
    let k = theme::kit();
    let (id, target) = (c.session.clone(), c.target.clone());
    let reason = c.target.detail.as_ref().filter(|d| c.target.offered && !d.is_empty()).map(|d| format!(" Chosen for {d}."));
    let buttons = row()
        .w_full()
        .gap(8.0)
        .px(16.0)
        .pb(16.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut App| s.limits.confirm = None).build(k).key("limit-confirm-cancel"))
        .child(Button::new(format!("Move to {}", c.target.name)).variant(ButtonVariant::Contrast).control_size(ControlSize::Small).on_press(move |s: &mut App| move_to(s, &id, target.clone(), false)).build(k).key("limit-confirm-move"));
    Dialog::new("limit-confirm", format!("Move this session to {}?", c.target.name))
        .description(format!("It stops here and continues under that account.{}", reason.unwrap_or_default()))
        .open(true)
        .fit()
        .width(420.0)
        .on_close(|s: &mut App| s.limits.confirm = None)
        .child(buttons)
        .build(k, layer);
}

fn consent(c: &Consent, layer: &mut Vec<El>) {
    let k = theme::kit();
    let (id, target) = (c.session.clone(), c.target.clone());
    let buttons = row()
        .w_full()
        .gap(8.0)
        .px(16.0)
        .pb(16.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut App| s.limits.consent = None).build(k).key("limit-consent-cancel"))
        .child(Button::new("Stop the terminal and move").variant(ButtonVariant::Danger).control_size(ControlSize::Small).on_press(move |s: &mut App| move_to(s, &id, target.clone(), true)).build(k).key("limit-consent-move"));
    Dialog::new("limit-consent", format!("Stop its terminal to move it to {}?", c.target.name))
        .description(c.message.clone())
        .open(true)
        .fit()
        .width(420.0)
        .on_close(|s: &mut App| s.limits.consent = None)
        .child(buttons)
        .build(k, layer);
}

pub fn overlays(s: &App, layer: &mut Vec<El>) {
    if let Some(id) = &s.limits.picking {
        picker(s, id, layer);
    }
    if let Some(c) = &s.limits.confirm {
        confirm(c, layer);
    }
    if let Some(c) = &s.limits.consent {
        consent(c, layer);
    }
}
