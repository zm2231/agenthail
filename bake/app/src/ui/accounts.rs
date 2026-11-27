use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::catalog::{Client, RespondError};
use feed::limit::span;
use feed::prefer::{self, Meter, Plan, Prefer, ProxyAccount, ProxyGroup, Set};
use rune::{El, ScrollSpec, col, el, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, Kit, Switch};

use crate::theme::{self, BODY_BOLD, LABEL, META, attention, done, failed, faint, muted};
use crate::{App, select};

pub enum Done {
    Sets(u64, Result<Vec<Set>, String>),
    Plan(u64, Result<Plan, String>),
    Applied(u64, Result<String, RespondError>),
}

#[derive(Clone)]
pub struct Change {
    pub proxy: String,
    pub account: String,
    pub label: String,
    pub set: Prefer,
    pub now: bool,
}

pub struct Planning {
    pub change: Change,
    pub plan: Option<Result<Plan, String>>,
    pub applying: bool,
    pub result: Option<Result<String, String>>,
    seq: u64,
}

pub struct Accounts {
    pub open: bool,
    pub sets: Option<Result<Vec<Set>, String>>,
    pub planning: Option<Planning>,
    seq: u64,
    sets_seq: u64,
    pub refresh_error: Option<String>,
    asked_at: Option<Instant>,
    in_flight: bool,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl Accounts {
    pub fn new() -> Accounts {
        let (tx, rx) = channel();
        Accounts { open: false, sets: None, planning: None, seq: 0, sets_seq: 0, refresh_error: None, asked_at: None, in_flight: false, tx, rx }
    }
}

fn spawn(s: &App, job: impl FnOnce(&Client) -> Done + Send + 'static) {
    let Some(client) = s.client.clone() else { return };
    let tx = s.accounts.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(job(&client));
    });
}

fn fetch(s: &mut App) {
    s.accounts.sets_seq += 1;
    s.accounts.asked_at = Some(Instant::now());
    s.accounts.in_flight = true;
    let seq = s.accounts.sets_seq;
    spawn(s, move |c| Done::Sets(seq, c.usage_sets().map_err(|e| e.to_string())));
}

pub fn open(s: &mut App) {
    s.accounts.open = true;
    if s.client.is_none() {
        let recorded = s.fixtures.as_ref().and_then(|f| std::fs::read_to_string(format!("{}/usage.json", f.dir)).ok()).and_then(|t| serde_json::from_str::<serde_json::Value>(&t).ok());
        s.accounts.sets = Some(match recorded {
            Some(value) => serde_json::from_value(value["sets"].clone()).map_err(|e| e.to_string()),
            None => Err("Fixture mode: usage is read from the daemon".into()),
        });
        return;
    }
    fetch(s);
}

fn close(s: &mut App) {
    if s.accounts.planning.as_ref().is_some_and(|p| p.applying) {
        return;
    }
    s.accounts.open = false;
    s.accounts.planning = None;
}

fn close_sheet(s: &mut App) {
    if !s.accounts.planning.as_ref().is_some_and(|p| p.applying) {
        s.accounts.planning = None;
    }
}

fn preview(s: &mut App, change: Change) {
    if s.accounts.planning.as_ref().is_some_and(|p| p.applying) {
        return;
    }
    s.accounts.seq += 1;
    let seq = s.accounts.seq;
    s.accounts.planning = Some(Planning { change: change.clone(), plan: None, applying: false, result: None, seq });
    if s.client.is_none() {
        let recorded = s.fixtures.as_ref().and_then(|f| std::fs::read_to_string(format!("{}/prefer-plan.json", f.dir)).ok());
        let plan = recorded.map(|t| serde_json::from_str::<Plan>(&t).map_err(|e| e.to_string())).unwrap_or_else(|| Err("Fixture mode: the proxy is read from the daemon".into()));
        if let Some(p) = s.accounts.planning.as_mut() {
            p.plan = Some(plan);
        }
        return;
    }
    spawn(s, move |c| Done::Plan(seq, c.prefer_plan(&change.proxy, &change.account, change.set, change.now).map_err(|e| e.to_string())));
}

fn toggle_now(s: &mut App) {
    let Some(p) = s.accounts.planning.as_ref().filter(|p| !p.applying) else { return };
    let mut change = p.change.clone();
    change.now = !change.now;
    preview(s, change);
}

fn apply(s: &mut App) {
    let Some(p) = s.accounts.planning.as_mut() else { return };
    let Some(Ok(plan)) = &p.plan else { return };
    if p.applying {
        return;
    }
    if s.client.is_none() {
        p.result = Some(Err("Fixture mode: not sent".into()));
        return;
    }
    p.applying = true;
    let (change, plan_id, seq) = (p.change.clone(), plan.plan_id.clone(), p.seq);
    spawn(s, move |c| Done::Applied(seq, c.prefer_apply(&change.proxy, &change.account, change.set, change.now, &plan_id)));
}

fn settled(s: &mut App, done: Done) {
    match done {
        Done::Sets(seq, sets) => {
            if seq == s.accounts.sets_seq {
                s.accounts.in_flight = false;
                s.accounts.asked_at = Some(Instant::now());
                match sets {
                    Err(error) if matches!(s.accounts.sets, Some(Ok(_))) => s.accounts.refresh_error = Some(error),
                    sets => {
                        s.accounts.refresh_error = None;
                        s.accounts.sets = Some(sets);
                    }
                }
            }
        }
        Done::Plan(seq, plan) => {
            if let Some(p) = s.accounts.planning.as_mut().filter(|p| p.seq == seq) {
                p.plan = Some(plan);
            }
        }
        Done::Applied(seq, result) => {
            let Some(p) = s.accounts.planning.as_mut().filter(|p| p.seq == seq) else { return };
            p.applying = false;
            match result {
                Ok(message) => {
                    p.result = Some(Ok(if message.is_empty() { "Done.".into() } else { message }));
                    fetch(s);
                }
                Err(error) if error.code == "plan_stale" => {
                    let change = p.change.clone();
                    preview(s, change);
                    if let Some(p) = s.accounts.planning.as_mut() {
                        p.result = Some(Err("The proxy changed since the preview. Check the new plan.".into()));
                    }
                }
                Err(error) => {
                    let (change, message) = (p.change.clone(), prefer::failure(&error));
                    preview(s, change);
                    if let Some(p) = s.accounts.planning.as_mut() {
                        p.result = Some(Err(format!("{message} Check the plan again before retrying.")));
                    }
                    fetch(s);
                }
            }
        }
    }
}

const USAGE_EVERY: Duration = Duration::from_secs(120);

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok(done) = s.accounts.rx.try_recv() {
        changed = true;
        settled(s, done);
    }
    if s.accounts.open && s.client.is_some() && !s.accounts.in_flight {
        if s.accounts.asked_at.is_none_or(|at| at.elapsed() >= USAGE_EVERY) {
            fetch(s);
        }
    }
    changed
}

fn meter_row(s: &App, m: &Meter, k: &Kit) -> El {
    let pct = m.percent;
    let color = if m.critical() { failed() } else if pct >= 60.0 || m.severity == "warning" { attention() } else { theme::text() };
    let read = m.as_of.as_deref().and_then(feed::catalog::parse_time).map(|at| ((s.now - at).whole_seconds() / 60).max(1));
    let resets = match (m.aged, read) {
        (true, Some(minutes)) => format!("read {} ago", span(minutes)),
        (true, None) => "an old reading".into(),
        (false, _) => m.resets_at.as_deref().and_then(feed::catalog::parse_time).map(|at| ((at - s.now).whole_seconds() as f64 / 60.0).ceil() as i64).filter(|m| *m > 0).map(|m| format!("resets in {}", span(m))).unwrap_or_default(),
    };
    let bar = row().grow(1.0).h(4.0).radius(2.0).fill(k.c.border_base).child(el().w_frac((pct / 100.0).clamp(0.02, 1.0) as f32).h(4.0).radius(2.0).fill(color));
    row()
        .w_full()
        .gap(8.0)
        .items_center()
        .child(text(m.label.clone()).font(META).color(muted()).w(86.0).truncate())
        .child(bar)
        .child(text(format!("{pct:.0}%")).font(META).color(color).w(36.0))
        .child(text(resets).font(META).color(faint()).w(110.0))
}

fn sessions_on(s: &App, set: &Set) -> Vec<El> {
    s.sessions
        .iter()
        .filter(|x| !x.archived && set.runs(x))
        .take(5)
        .map(|x| {
            let id = x.id.clone();
            row()
                .key(format!("usage-session-{}", x.id))
                .gap(6.0)
                .items_center()
                .hover(crate::theme::ink(0.06))
                .radius(6.0)
                .px(4.0)
                .on_press(move |s: &mut App| {
                    close(s);
                    select(s, &id);
                })
                .child(el().size(6.0, 6.0).radius(3.0).fill(if x.limit(s.now).is_some() { attention() } else { faint() }))
                .child(text(x.title()).font(META).color(muted()).truncate())
        })
        .collect()
}

fn account_card(s: &App, set: &Set, k: &Kit) -> El {
    let mut c = col()
        .w_full()
        .gap(6.0)
        .padding(10.0)
        .radius(8.0)
        .fill(crate::theme::ink(0.04))
        .child(row().gap(8.0).items_center().child(text(set.title()).font(BODY_BOLD).color(theme::text()).truncate()).child(el().grow(1.0)).child(text(set.host.clone()).font(META).color(faint())));
    c = c.children(set.meters.iter().map(|m| meter_row(s, m, k)));
    if !set.problem.is_empty() {
        c = c.child(text(set.problem.clone()).font(META).color(attention()).wrap());
    } else if let Some(proxy) = set.via() {
        c = c.child(text(format!("Runs through the {proxy} proxy; its accounts are below")).font(META).color(muted()));
    } else if set.meters.is_empty() {
        c = c.child(text("No current reading").font(META).color(faint()));
    }
    c.children(sessions_on(s, set))
}

fn proxy_account(s: &App, group: &ProxyGroup, a: &ProxyAccount, k: &Kit) -> El {
    let change = |set: Prefer| Change { proxy: group.proxy.id.clone(), account: a.id.clone(), label: a.label.clone(), set, now: false };
    let mut head = row().gap(8.0).items_center().child(text(a.label.clone()).font(BODY_BOLD).color(theme::text()).truncate());
    if a.routed {
        head = head.child(text("in use").font(META).color(done()));
    }
    if !a.status.is_empty() && a.status != "active" {
        head = head.child(text(a.status.clone()).font(META).color(attention()));
    }
    let mut c = col().w_full().gap(6.0).padding(10.0).radius(8.0).fill(crate::theme::ink(0.04)).child(head);
    c = c.children(a.meters.iter().map(|m| meter_row(s, m, k)));
    let on = a.set_to_prefer();
    let toggle = if on { change(Prefer::Off) } else { change(Prefer::On) };
    let help = if on && !a.preferred { "Set to prefer, though it doesn't lead routing now; turning it off changes only this account" } else { "Changes only this account's preference" };
    let mut actions = row().gap(8.0).items_center().child(
        Button::new(a.prefer_label())
            .variant(if on { ButtonVariant::Submit } else { ButtonVariant::Neutral })
            .control_size(ControlSize::Small)
            .help(help)
            .on_press(move |s: &mut App| preview(s, toggle.clone()))
            .build(k)
            .key(format!("prefer-{}-{}", group.proxy.id, a.id)),
    );
    if group.has_peers(a) {
        let only = change(Prefer::Only);
        actions = actions.child(
            Button::new("Prefer only this")
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .help(format!("Prefers this account and turns off the other {} accounts on this proxy", a.provider))
                .on_press(move |s: &mut App| preview(s, only.clone()))
                .build(k)
                .key(format!("prefer-only-{}-{}", group.proxy.id, a.id)),
        );
    }
    if let Some(on) = a.set_on.as_ref().filter(|o| !o.is_empty()) {
        actions = actions.child(text(format!("set on {on}")).font(META).color(faint()));
    }
    c.child(actions)
}

fn panel(s: &App, layer: &mut Vec<El>, k: &Kit) {
    let mut body = col().w_full().gap(10.0);
    if let Some(error) = &s.accounts.refresh_error {
        body = body.child(text(format!("Couldn't refresh usage, so these readings may be out of date: {error}")).font(META).color(failed()).wrap());
    }
    match &s.accounts.sets {
        None => body = body.child(text("Reading usage…").font(META).color(muted())),
        Some(Err(error)) => body = body.child(text(error.clone()).font(META).color(failed()).wrap()),
        Some(Ok(sets)) => {
            let plain = prefer::accounts(sets);
            if !plain.is_empty() {
                body = body.child(text("ACCOUNTS").font(LABEL).color(faint())).children(plain.into_iter().map(|set| account_card(s, set, k)));
            }
            for group in prefer::proxies(sets) {
                let heading = if group.local { format!("{} PROXY", group.proxy.name.to_uppercase()) } else { format!("{} PROXY · READ FROM {}", group.proxy.name.to_uppercase(), group.host.to_uppercase()) };
                body = body.child(text(heading).font(LABEL).color(faint()).pt(4.0));
                body = body.children(group.accounts.iter().map(|a| proxy_account(s, &group, a, k)));
                if !group.problem.is_empty() {
                    body = body.child(text(group.problem.clone()).font(META).color(attention()).wrap());
                }
            }
            if sets.is_empty() {
                body = body.child(text("No usage has been read yet.").font(META).color(muted()));
            }
        }
    }
    let scroll = col().key("usage-scroll").w_full().max_h(520.0).px(16.0).scroll(ScrollSpec { stick_bottom: false }).child(body);
    let buttons = row().w_full().px(16.0).py(12.0).child(el().grow(1.0)).child(Button::new("Done").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(close).build(k).key("usage-done"));
    Dialog::new("usage", "Usage").description("Each Claude account's limits, and which accounts a proxy prefers.").open(true).fit().width(460.0).on_close(close).child(scroll).child(buttons).build(k, layer);
}

fn title(c: &Change) -> String {
    match c.set {
        Prefer::On => format!("Prefer {}?", c.label),
        Prefer::Off => format!("Stop preferring {}?", c.label),
        Prefer::Only => format!("Prefer only {}?", c.label),
    }
}

fn sheet(p: &Planning, layer: &mut Vec<El>, k: &Kit) {
    let mut body = col().w_full().gap(6.0).px(16.0);
    let ready = match &p.plan {
        None => {
            body = body.child(text("Reading the proxy…").font(META).color(muted()));
            false
        }
        Some(Err(error)) => {
            body = body.child(text(format!("Couldn't preview the change: {error}")).font(META).color(failed()).wrap());
            false
        }
        Some(Ok(plan)) => {
            body = body.children(plan.change_lines().into_iter().map(|l| text(l).font(META).color(theme::text()).wrap()));
            let affects = plan.affect_lines();
            if !affects.is_empty() {
                body = body.child(text("AFFECTS").font(LABEL).color(faint()).pt(6.0)).children(affects.into_iter().map(|l| text(l).font(META).color(muted()).wrap()));
            }
            if plan.changes_anything() {
                body = body.children(plan.warnings.iter().map(|w| text(w.clone()).font(META).color(attention()).wrap()));
                body = body.children(plan.confirm.clone().filter(|c| !c.is_empty()).map(|c| text(c).font(META).color(muted()).wrap()));
            }
            plan.changes_anything()
        }
    };
    if p.change.set == Prefer::Only {
        let switch = Switch::new(p.change.now).label("Move running sessions").description("Releases the sessions on the accounts it turns off; each moves on its next request and rebuilds its prompt cache.").on_toggle(toggle_now).build(k);
        body = body.child(el().h(4.0)).child(switch);
    }
    if let Some(result) = &p.result {
        body = match result {
            Ok(message) => body.child(text(message.clone()).font(META).color(done()).wrap()),
            Err(message) => body.child(text(message.clone()).font(META).color(failed()).wrap()),
        };
    }
    let finished = matches!(p.result, Some(Ok(_)));
    let apply_label = if p.applying {
        "Changing…"
    } else {
        match p.change.set {
            Prefer::On => "Prefer",
            Prefer::Off => "Stop preferring",
            Prefer::Only => "Prefer only this",
        }
    };
    let mut buttons = row().w_full().gap(8.0).px(16.0).py(14.0).child(el().grow(1.0));
    buttons = buttons.child(Button::new(if finished { "Done" } else { "Cancel" }).variant(ButtonVariant::Ghost).control_size(ControlSize::Small).disabled(p.applying).on_press(close_sheet).build(k).key("prefer-cancel"));
    if !finished {
        buttons = buttons.child(Button::new(apply_label).variant(ButtonVariant::Contrast).control_size(ControlSize::Small).disabled(!ready || p.applying).on_press(apply).build(k).key("prefer-apply"));
    }
    Dialog::new("prefer", title(&p.change)).open(true).fit().width(440.0).on_close(close_sheet).child(body).child(buttons).build(k, layer);
}

pub fn overlays(s: &App, layer: &mut Vec<El>, k: &Kit) {
    if s.accounts.open {
        panel(s, layer, k);
    }
    if let Some(p) = &s.accounts.planning {
        sheet(p, layer, k);
    }
}
