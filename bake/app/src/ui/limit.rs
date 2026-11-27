use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::catalog::{Client, RespondError, Session};
use feed::create::{ConfigRoot, RootUsage};
use feed::limit::{Moved, Stop, Target, targets};
use rune::{El, col, el, icon, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, Kit};

use super::spacer;
use crate::icons;
use crate::theme::{self, BODY_BOLD, LABEL, META, TITLE, attention, failed, faint, muted};
use crate::{App, session};

pub enum Done {
    Roots(String, u64, Result<Vec<ConfigRoot>, String>),
    Usage(String, u64, RootUsage),
    Unread(String, u64, String),
    Read(String, u64),
    Resumed(String, String, bool, Result<(), RespondError>),
    Moved(String, String, Target, super::window::Id, Result<Moved, RespondError>),
}

pub struct Confirm {
    pub session: String,
    pub since: String,
    pub target: Target,
}

pub struct Consent {
    pub session: String,
    pub since: String,
    pub target: Target,
    pub message: String,
}

pub struct Limits {
    pub picking: Option<(String, String)>,
    pub confirm: Option<Confirm>,
    pub consent: Option<Consent>,
    pub roots: HashMap<String, Result<Vec<ConfigRoot>, String>>,
    pub unread: HashSet<(String, String)>,
    pub reading: HashMap<String, u64>,
    pub refresh_error: HashMap<String, String>,
    generation: u64,
    pub busy: HashSet<String>,
    pub note: HashMap<String, (String, bool, String)>,
    pub warnings: Vec<String>,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl Limits {
    pub fn new() -> Limits {
        let (tx, rx) = channel();
        Limits { picking: None, confirm: None, consent: None, roots: HashMap::new(), unread: HashSet::new(), reading: HashMap::new(), refresh_error: HashMap::new(), generation: 0, busy: HashSet::new(), note: HashMap::new(), warnings: Vec::new(), tx, rx }
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

fn since(s: &App, id: &str) -> String {
    session(s, id).and_then(|x| x.limit(s.now)).map(|stop| stop.since).unwrap_or_default()
}

fn waiting_since(s: &App, id: &str) -> Option<String> {
    session(s, id).filter(|x| !x.read_only).and_then(|x| x.limit(s.now)).filter(|stop| stop.held && stop.waiting).map(|stop| stop.since)
}

fn current(s: &App, id: &str, since: &str) -> bool {
    waiting_since(s, id).as_deref() == Some(since)
}

fn target_spent(s: &App, id: &str, root: &str) -> bool {
    let Some(x) = session(s, id) else { return false };
    matches!(s.limits.roots.get(&host_key(x)), Some(Ok(roots)) if roots.iter().any(|r| r.root == root && r.spent(s.now)))
}

fn note(s: &mut App, id: &str, since: String, text: String, bad: bool) {
    if bad {
        s.limits.note.remove(id);
        let title = session(s, id).map(|x| x.title()).unwrap_or_default();
        s.limits.warnings.push(format!("{title}: {text}"));
        return;
    }
    s.limits.note.insert(id.to_string(), (text, bad, since));
}

fn resume(s: &mut App, id: &str, now: bool) {
    if not_sent(s) || !s.limits.busy.insert(id.to_string()) {
        return;
    }
    s.limits.note.remove(id);
    let (id, stop) = (id.to_string(), since(s, id));
    spawn(s, move |c, tx| {
        let _ = tx.send(Done::Resumed(id.clone(), stop, now, c.limit_resume(&id, now)));
    });
}

fn open_picker(s: &mut App, id: &str) {
    let Some(x) = session(s, id) else { return };
    let host = host_key(x);
    let Some(stop) = waiting_since(s, id) else { return };
    if !super::window::may_open(s, super::window::Kind::LimitPick) {
        return;
    }
    s.limits.picking = Some((id.to_string(), stop));
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

fn ask_confirm(s: &mut App, c: Confirm) {
    if super::window::may_open(s, super::window::Kind::LimitConfirm) {
        s.limits.confirm = Some(c);
    }
}

fn move_to(s: &mut App, id: &str, since: &str, target: Target, stop_terminal: bool) {
    s.limits.confirm = None;
    s.limits.consent = None;
    s.limits.picking = None;
    if !current(s, id, since) {
        s.ov.notice = Some("The session's usage stop changed, so nothing was moved. Check it again.".into());
        return;
    }
    if target_spent(s, id, &target.root) {
        s.ov.notice = Some(format!("{} is out of usage now, so nothing was moved.", target.name));
        return;
    }
    if not_sent(s) || !s.limits.busy.insert(id.to_string()) {
        return;
    }
    let asker = s.windows.current();
    let stop = since.to_string();
    note(s, id, stop.clone(), format!("Moving to {}…", target.name), false);
    let id = id.to_string();
    spawn(s, move |c, tx| {
        let result = c.limit_move(&id, Some(&target.root), stop_terminal);
        let _ = tx.send(Done::Moved(id, stop, target, asker, result));
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
                mut roots => {
                    if let (Ok(fresh), Some(Ok(known))) = (roots.as_mut(), s.limits.roots.get(&host)) {
                        feed::limit::carry_usage(fresh, known);
                    }
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
                let root_path = root.root.clone();
                if !root.take_reading(usage) {
                    s.limits.unread.insert((host, root_path));
                }
            }
        }
        Done::Resumed(id, stop, now, result) => {
            s.limits.busy.remove(&id);
            if result.is_ok() {
                super::session_menu::changed(s, &id);
            }
            let (text, bad) = match result {
                Ok(()) if now => ("Asked it to continue".to_string(), false),
                Ok(()) => ("It stays stopped when the limit resets".to_string(), false),
                Err(error) => (failure(if now { "Resuming" } else { "Keeping it stopped" }, &error), true),
            };
            note(s, &id, stop, text, bad);
        }
        Done::Moved(id, stop, target, asker, result) => {
            s.limits.busy.remove(&id);
            match result {
                Ok(Moved::Done(None)) => {
                    super::session_menu::changed(s, &id);
                    note(s, &id, stop, format!("Moved to {}", target.name), false);
                }
                Ok(Moved::Done(Some(detail))) => {
                    super::session_menu::changed(s, &id);
                    note(s, &id, stop, format!("Moved to {}, but {detail}", target.name), true);
                }
                Ok(Moved::NeedsConsent(message)) => {
                    s.limits.note.remove(&id);
                    let to = super::window::asker_or_showing(s, asker, &id);
                    if !current(s, &id, &stop) {
                    } else if to.is_some() && super::window::free_for(s, super::window::Kind::LimitConsent, to) {
                        s.limits.consent = Some(Consent { session: id.clone(), since: stop, target, message });
                        if let Some(w) = to {
                            super::window::give(s, super::window::Kind::LimitConsent, w);
                        }
                    } else {
                        note(s, &id, stop, format!("Moving to {} needs your OK. Try it again from its window.", target.name), true);
                    }
                }
                Err(error) => {
                    note(s, &id, stop, failure("Moving", &error), true);
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
    let stale = |s: &App, id: &str, since: &str| !current(s, id, since);
    if s.limits.picking.as_ref().is_some_and(|(id, since)| stale(s, id, since)) {
        s.limits.picking = None;
        changed = true;
    }
    let spent = |s: &App, session: &str, target: &Target| target_spent(s, session, &target.root).then(|| format!("{} ran out of usage, so it can't take this session now.", target.name));
    if let Some(c) = &s.limits.confirm {
        let out = spent(s, &c.session, &c.target);
        if out.is_some() || stale(s, &c.session, &c.since) {
            s.limits.confirm = None;
            s.ov.notice = out.or(s.ov.notice.take());
            changed = true;
        }
    }
    if let Some(c) = &s.limits.consent {
        let out = spent(s, &c.session, &c.target);
        if out.is_some() || stale(s, &c.session, &c.since) {
            s.limits.consent = None;
            s.ov.notice = out.or(s.ov.notice.take());
            changed = true;
        }
    }
    changed
}

fn button(label: impl Into<String>, variant: ButtonVariant, disabled: bool) -> Button {
    Button::new(label).variant(variant).control_size(ControlSize::Small).disabled(disabled)
}

pub(crate) fn card(s: &App, x: &Session, stop: &Stop, k: &Kit) -> El {
    let busy = s.limits.busy.contains(&x.id);
    let mut body = col()
        .grow(1.0)
        .min_w(0.0)
        .gap(8.0)
        .child(text("USAGE LIMIT").font(k.text(LABEL)).color(attention()))
        .child(text(stop.label.clone()).font(k.text(TITLE)).color(theme::text()).wrap());
    if !x.display.detail.is_empty() && x.display.detail != stop.label {
        body = body.child(text(x.display.detail.clone()).font(k.text(META)).color(muted()).wrap());
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
            let full = matches!(s.limits.roots.get(&host_key(x)), Some(Ok(roots)) if roots.iter().any(|r| r.root == offer.root && r.spent(s.now)));
            let target = Target { root: offer.root.clone(), name: offer.name(), detail: offer.reason.clone(), full, offered: true };
            let (id, since) = (x.id.clone(), stop.since.clone());
            let help = match &offer.reason {
                _ if full => format!("{} is out of usage now", offer.name()),
                Some(reason) if !reason.is_empty() => format!("Continue this session under {}: {reason}", offer.name()),
                _ => format!("Continue this session under {}", offer.name()),
            };
            actions = actions.child(
                button(format!("Move to {}…", offer.name()), ButtonVariant::Neutral, busy || full)
                    .icon(&icons::ARROW_RIGHT)
                    .help(help)
                    .on_press(move |s: &mut App| ask_confirm(s, Confirm { session: id.clone(), since: since.clone(), target: target.clone() }))
                    .build(k)
                    .key("limit-move"),
            );
        }
        let id = x.id.clone();
        actions = actions.children(stop.waiting.then(|| button("Another account…", ButtonVariant::Ghost, busy).help("Pick the Claude account this session continues under").on_press(move |s: &mut App| open_picker(s, &id)).build(k).key("limit-pick")));
        actions = actions.child(spacer()).child(Button::new("Usage").variant(ButtonVariant::GhostMuted).control_size(ControlSize::Small).icon(&icons::ACTIVITY).on_press(super::accounts::open).build(k).key("limit-usage"));
        body = body.child(actions);
    }
    if let Some((note, bad, _)) = s.limits.note.get(&x.id).filter(|n| n.2 == stop.since) {
        body = body.child(text(note.clone()).font(k.text(META)).color(if *bad { failed() } else { muted() }).wrap());
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
        .child(col().size(34.0, 34.0).radius(10.0).items_center().justify_center().fill(attention().alpha(0.18)).child(icon(&icons::GAUGE, k.text_size(17.0)).color(attention())))
        .child(body);
    col().w_full().items_center().px(32.0).pb(12.0).child(card_el)
}

fn target_row(id: &str, since: &str, t: &Target, busy: bool, unread: bool, k: &Kit) -> El {
    let usable = !t.full && !busy;
    let mut name = row().gap(8.0).items_center().child(text(t.name.clone()).font(k.text(BODY_BOLD)).color(if usable { theme::text() } else { faint() }).truncate());
    if t.offered {
        name = name.child(row().px(7.0).py(2.0).radius(6.0).fill(attention().alpha(0.2)).child(text("Suggested").font(k.text(META)).color(attention())));
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
        .fill(crate::theme::ink(0.04))
        .border(1.0, crate::theme::ink(0.08))
        .child(col().grow(1.0).min_w(0.0).gap(2.0).child(name).children(detail.map(|d| text(d).font(k.text(META)).color(muted()).truncate())));
    if usable {
        let (id, since, target) = (id.to_string(), since.to_string(), t.clone());
        r = r.hover(crate::theme::ink(0.09)).on_press(move |s: &mut App| {
            s.limits.picking = None;
            ask_confirm(s, Confirm { session: id.clone(), since: since.clone(), target: target.clone() });
        });
        r = r.child(icon(&icons::ARROW_RIGHT, k.text_size(13.0)).color(faint()));
    } else {
        r = r.opacity(0.6);
    }
    r
}

fn picker(s: &App, id: &str, since: &str, layer: &mut Vec<El>, k: &Kit) {
    let Some(x) = session(s, id) else { return };
    let Some(stop) = x.limit(s.now) else { return };
    let busy = s.limits.busy.contains(id);
    let current = x.claude_config_root.as_ref().map(|r| r.root.as_str());
    let mut body = col().w_full().gap(6.0).px(16.0).pb(16.0);
    match s.limits.roots.get(&host_key(x)) {
        None if s.client.is_some() => body = body.child(text("Reading this computer's Claude accounts…").font(k.text(META)).color(muted())),
        Some(Err(error)) => body = body.child(text(format!("Couldn't read the accounts: {error}")).font(k.text(META)).color(failed()).wrap()),
        _ => {
            let roots = match s.limits.roots.get(&host_key(x)) {
                Some(Ok(roots)) => roots.as_slice(),
                _ => &[],
            };
            let list = targets(&stop, current, roots, s.now);
            if list.is_empty() {
                body = body.child(text("No other Claude account is set up on this computer.").font(k.text(META)).color(muted()).wrap());
            }
            let host = host_key(x);
            body = body.children(list.iter().map(|t| target_row(id, since, t, busy, s.limits.unread.contains(&(host.clone(), t.root.clone())), k)));
            if let Some(error) = s.limits.refresh_error.get(&host) {
                body = body.child(text(format!("Couldn't refresh the accounts, so this list may be out of date: {error}")).font(k.text(META)).color(failed()).wrap());
            } else if s.limits.reading.contains_key(&host) {
                body = body.child(text("Checking for changes…").font(k.text(META)).color(faint()));
            }
        }
    }
    if let Some(root) = x.claude_config_root.as_ref().filter(|r| !r.name.is_empty()) {
        body = body.child(text(format!("Now under {}", root.name)).font(k.text(META)).color(faint()).pt(4.0));
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

fn confirm(c: &Confirm, layer: &mut Vec<El>, k: &Kit) {
    let (id, since, target) = (c.session.clone(), c.since.clone(), c.target.clone());
    let reason = c.target.detail.as_ref().filter(|d| c.target.offered && !d.is_empty()).map(|d| format!(" Chosen for {d}."));
    let buttons = row()
        .w_full()
        .gap(8.0)
        .px(16.0)
        .pb(16.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut App| s.limits.confirm = None).build(k).key("limit-confirm-cancel"))
        .child(Button::new(format!("Move to {}", c.target.name)).variant(ButtonVariant::Contrast).control_size(ControlSize::Small).on_press(move |s: &mut App| move_to(s, &id, &since, target.clone(), false)).build(k).key("limit-confirm-move"));
    Dialog::new("limit-confirm", format!("Move this session to {}?", c.target.name))
        .description(format!("It stops here and continues under that account.{}", reason.unwrap_or_default()))
        .open(true)
        .fit()
        .width(420.0)
        .on_close(|s: &mut App| s.limits.confirm = None)
        .child(buttons)
        .build(k, layer);
}

fn consent(c: &Consent, layer: &mut Vec<El>, k: &Kit) {
    let (id, since, target) = (c.session.clone(), c.since.clone(), c.target.clone());
    let buttons = row()
        .w_full()
        .gap(8.0)
        .px(16.0)
        .pb(16.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut App| s.limits.consent = None).build(k).key("limit-consent-cancel"))
        .child(Button::new("Stop the terminal and move").variant(ButtonVariant::Danger).control_size(ControlSize::Small).on_press(move |s: &mut App| move_to(s, &id, &since, target.clone(), true)).build(k).key("limit-consent-move"));
    Dialog::new("limit-consent", format!("Stop its terminal to move it to {}?", c.target.name))
        .description(c.message.clone())
        .open(true)
        .fit()
        .width(420.0)
        .on_close(|s: &mut App| s.limits.consent = None)
        .child(buttons)
        .build(k, layer);
}

fn warnings(s: &App, layer: &mut Vec<El>, k: &Kit) {
    let mut stack = col().gap(8.0).w(420.0);
    for (i, warning) in s.limits.warnings.iter().enumerate() {
        let toast = rune_kit::Toast::new(format!("move-warning-{i}"), warning.clone()).icon(k, &icons::GAUGE).on_close(move |s: &mut App| {
            if i < s.limits.warnings.len() {
                s.limits.warnings.remove(i);
            }
        });
        stack = stack.child(toast.closable().build(k));
    }
    layer.push(col().absolute(0.0, 0.0).w_full().h_full().items_end().justify_start().padding(24.0).child(stack));
}

pub fn overlays(s: &App, layer: &mut Vec<El>, k: &Kit, main: bool) {
    use super::window::{Kind, shows};
    if main && !s.limits.warnings.is_empty() {
        warnings(s, layer, k);
    }
    if let Some((id, since)) = s.limits.picking.as_ref().filter(|_| shows(s, Kind::LimitPick)) {
        picker(s, id, since, layer, k);
    }
    if let Some(c) = s.limits.confirm.as_ref().filter(|_| shows(s, Kind::LimitConfirm)) {
        confirm(c, layer, k);
    }
    if let Some(c) = s.limits.consent.as_ref().filter(|_| shows(s, Kind::LimitConsent)) {
        consent(c, layer, k);
    }
}
