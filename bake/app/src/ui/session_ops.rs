use std::collections::{HashSet, VecDeque};
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::adopt::{self, Adopted, Phase};
use feed::catalog::{RespondError, Session};
use feed::create::Host;
use feed::menu::Offer;
use feed::moves::{self, Notice};
use rune::{El, col, el, icon, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog};
use time::{OffsetDateTime, UtcOffset};

use crate::App;
use crate::icons;
use crate::theme::{self, BODY, META, failed, faint, muted};

pub enum Done {
    Adopted(String, Result<Adopted, RespondError>),
    Hosts(u64, Result<Vec<Host>, String>),
    Moved(Notice),
}

pub struct Adopting {
    pub session: String,
    pub title: String,
    pub phase: Phase,
}

pub struct Moving {
    pub seq: u64,
    pub session: String,
    pub title: String,
    pub hosts: Option<Result<Vec<Host>, String>>,
    pub picked: Option<String>,
}

pub struct Ops {
    pub adopting: Option<Adopting>,
    pub moving: Option<Moving>,
    pub notices: VecDeque<Notice>,
    sent: HashSet<String>,
    here: String,
    seq: u64,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl Ops {
    pub fn new() -> Ops {
        let (tx, rx) = channel();
        Ops { adopting: None, moving: None, notices: VecDeque::new(), sent: HashSet::new(), here: String::new(), seq: 0, tx, rx }
    }
}

pub fn offset(at: OffsetDateTime) -> UtcOffset {
    UtcOffset::from_whole_seconds(rune::time::LocalTime::at(at.unix_timestamp() * 1000).offset).unwrap_or(UtcOffset::UTC)
}

pub fn offer(s: &App, id: &str) -> Offer {
    Offer { paired_hosts: s.ov.d.hosts.iter().any(|h| h.state != "local"), moving: s.menus.ops.sent.contains(id), now: Some(OffsetDateTime::now_utc()) }
}

fn spawn(s: &App, job: impl FnOnce(&feed::catalog::Client) -> Done + Send + 'static) {
    let Some(client) = s.client.clone() else { return };
    let tx = s.menus.ops.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(job(&client));
    });
}

fn title(s: &App, id: &str) -> String {
    super::gone::shown(s, id).map(Session::title).unwrap_or_default()
}

pub fn open_adopt(s: &mut App, id: &str) {
    if super::session_menu::not_sent(s) {
        return;
    }
    s.menus.ops.adopting = Some(Adopting { session: id.to_string(), title: title(s, id), phase: Phase::Ready });
}

fn adopt(s: &mut App, stop_terminal: bool) {
    let Some(a) = s.menus.ops.adopting.as_mut() else { return };
    if !matches!(a.phase, Phase::Ready | Phase::Confirm(_)) {
        return;
    }
    a.phase = Phase::Adopting;
    let id = a.session.clone();
    spawn(s, move |c| Done::Adopted(id.clone(), c.session_adopt(&id, stop_terminal)));
}

fn close_adopt(s: &mut App) {
    if !matches!(s.menus.ops.adopting.as_ref().map(|a| &a.phase), Some(Phase::Adopting)) {
        s.menus.ops.adopting = None;
    }
}

pub fn open_move(s: &mut App, id: &str) {
    if super::session_menu::not_sent(s) || s.menus.ops.sent.contains(id) {
        return;
    }
    s.menus.ops.seq += 1;
    let seq = s.menus.ops.seq;
    s.menus.ops.moving = Some(Moving { seq, session: id.to_string(), title: title(s, id), hosts: None, picked: None });
    spawn(s, move |c| Done::Hosts(seq, c.hosts().map_err(|e| e.to_string())));
}

fn send_move(s: &mut App, session: &str, title: &str, host: &str, host_name: &str, stop_terminal: bool) {
    if !s.menus.ops.sent.insert(session.to_string()) {
        return;
    }
    let here = if s.menus.ops.here.is_empty() { "this computer".to_string() } else { s.menus.ops.here.clone() };
    let (session, title, host, host_name) = (session.to_string(), title.to_string(), host.to_string(), host_name.to_string());
    spawn(s, move |c| Done::Moved(c.move_session(&session, &host, &host_name, stop_terminal).notice(&session, &title, &host, &host_name, &here)));
}

fn confirm_move(s: &mut App) {
    let Some(m) = s.menus.ops.moving.take() else { return };
    let Some(host) = targets(&m).into_iter().find(|h| Some(&h.id) == m.picked.as_ref() && h.available()) else {
        s.menus.ops.moving = Some(m);
        return;
    };
    send_move(s, &m.session, &m.title, &host.id, &host.name, false);
}

fn retry(s: &mut App) {
    let Some(n) = s.menus.ops.notices.pop_front() else { return };
    let title = title(s, &n.session);
    send_move(s, &n.session, if title.is_empty() { &n.session } else { &title }, &n.host, &n.host_name, true);
}

fn paired(hosts: &[Host]) -> Vec<Host> {
    hosts.iter().filter(|h| !h.is_local()).cloned().collect()
}

fn targets(m: &Moving) -> Vec<Host> {
    match &m.hosts {
        Some(Ok(hosts)) => paired(hosts),
        _ => Vec::new(),
    }
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok(done) = s.menus.ops.rx.try_recv() {
        changed = true;
        match done {
            Done::Adopted(id, result) => {
                let reopen = match &result {
                    Ok(Adopted::Opened { session: Some(new), .. }) if *new != id => Some(new.clone()),
                    _ => None,
                };
                if let Some(a) = s.menus.ops.adopting.as_mut().filter(|a| a.session == id) {
                    a.phase = Phase::after(result);
                } else if let Err(error) = &result {
                    s.ov.notice = Some(format!("Opening it in Agenthail didn't work: {}", error.message));
                }
                super::gone::recheck(s, &id);
                if let Some(new) = reopen.filter(|_| s.selected.as_deref() == Some(id.as_str())) {
                    crate::over::open_session(s, &new);
                }
            }
            Done::Hosts(seq, result) => {
                if let Ok(hosts) = &result
                    && let Some(local) = hosts.iter().find(|h| h.is_local())
                {
                    s.menus.ops.here = local.name.clone();
                }
                if let Some(m) = s.menus.ops.moving.as_mut().filter(|m| m.seq == seq) {
                    m.hosts = Some(result);
                }
            }
            Done::Moved(notice) => {
                s.menus.ops.sent.remove(&notice.session);
                super::gone::recheck(s, &notice.session);
                s.menus.ops.notices.push_back(notice);
            }
        }
    }
    changed
}

fn small(label: &str, variant: ButtonVariant) -> Button {
    Button::new(label).variant(variant).control_size(ControlSize::Small)
}

fn buttons(children: Vec<El>) -> El {
    let mut r = row().w_full().gap(8.0).child(el().grow(1.0));
    for c in children {
        r = r.child(c);
    }
    r
}

fn adopt_dialog(a: &Adopting, layer: &mut Vec<El>) {
    let k = theme::kit();
    let mut body = col().w_full().gap(10.0).px(16.0).pb(16.0);
    let line = |t: &str| text(t.to_string()).font(META).color(muted()).wrap().w_full();
    match &a.phase {
        Phase::Ready | Phase::Adopting => {}
        Phase::Confirm(message) | Phase::Done(message) => body = body.child(line(message)),
        Phase::Failed(message) => body = body.child(text(message.clone()).font(META).color(failed()).wrap().w_full()),
    }
    let cancel = small("Cancel", ButtonVariant::Ghost).on_press(close_adopt).build(k).key("adopt-cancel");
    let actions = match &a.phase {
        Phase::Done(_) => vec![small("Done", ButtonVariant::Contrast).on_press(|s: &mut App| s.menus.ops.adopting = None).build(k).key("adopt-done")],
        Phase::Confirm(_) => vec![
            small("Cancel", ButtonVariant::Ghost)
                .on_press(|s: &mut App| {
                    if let Some(a) = s.menus.ops.adopting.as_mut() {
                        a.phase = Phase::Ready;
                    }
                })
                .build(k)
                .key("adopt-cancel"),
            small("Stop Terminal and Open", ButtonVariant::Danger).on_press(|s: &mut App| adopt(s, true)).build(k).key("adopt-stop-terminal"),
        ],
        phase => vec![
            cancel,
            small(if *phase == Phase::Adopting { "Opening…" } else { "Open" }, ButtonVariant::Contrast)
                .disabled(*phase != Phase::Ready)
                .on_press(|s: &mut App| adopt(s, false))
                .build(k)
                .key("adopt-open"),
        ],
    };
    Dialog::new("adopt", format!("Open {} in Agenthail", a.title)).description(adopt::ABOUT).open(true).fit().width(460.0).on_close(close_adopt).child(body.child(buttons(actions))).build(k, layer);
}

fn host_row(h: &Host, picked: bool) -> El {
    let id = h.id.clone();
    let up = h.available();
    let mut r = row()
        .key(format!("move-target-{}", h.name))
        .w_full()
        .gap(8.0)
        .items_center()
        .px(10.0)
        .py(7.0)
        .radius(8.0)
        .label(h.name.clone())
        .child(el().size(12.0, 12.0).radius(6.0).border(if picked { 4.0 } else { 1.5 }, if picked { theme::accent() } else { faint() }))
        .child(icon(&icons::SERVER, 13.0).color(if up { muted() } else { faint() }))
        .child(text(h.name.clone()).font(BODY).color(if up { theme::text() } else { faint() }).truncate().shrink())
        .child(el().grow(1.0));
    if up {
        r = r.hover(theme::ink(0.06)).on_press(move |s: &mut App| {
            if let Some(m) = s.menus.ops.moving.as_mut() {
                m.picked = Some(id.clone());
            }
        });
    } else {
        r = r.child(text(feed::devices::Host { state: h.state.clone(), ..Default::default() }.state_label()).font(META).color(faint()));
    }
    if picked {
        r = r.fill(theme::wash());
    }
    r
}

fn move_dialog(m: &Moving, layer: &mut Vec<El>) {
    let k = theme::kit();
    let mut body = col().w_full().gap(4.0).px(16.0).pb(16.0);
    match &m.hosts {
        None => body = body.child(text("Looking for your other computers…".to_string()).font(META).color(muted())),
        Some(Err(error)) => body = body.child(text(format!("Couldn't list your other computers: {error}")).font(META).color(failed()).wrap().w_full()),
        Some(Ok(_)) => {
            let hosts = targets(m);
            if hosts.is_empty() {
                body = body.child(text(feed::devices::NO_HOSTS.to_string()).font(META).color(muted()).wrap().w_full());
            }
            for h in &hosts {
                body = body.child(host_row(h, m.picked.as_deref() == Some(h.id.as_str())));
            }
        }
    }
    let ready = targets(m).iter().any(|h| Some(&h.id) == m.picked.as_ref() && h.available());
    let actions = vec![
        small("Cancel", ButtonVariant::Ghost).on_press(|s: &mut App| s.menus.ops.moving = None).build(k).key("move-cancel"),
        small("Move", ButtonVariant::Contrast).disabled(!ready).on_press(confirm_move).build(k).key("move-confirm"),
    ];
    Dialog::new("move", format!("Move {}", m.title))
        .description(moves::ABOUT)
        .open(true)
        .fit()
        .width(380.0)
        .on_close(|s: &mut App| s.menus.ops.moving = None)
        .child(body.child(col().w_full().pt(8.0).child(buttons(actions))))
        .build(k, layer);
}

fn notice_dialog(n: &Notice, layer: &mut Vec<El>) {
    let k = theme::kit();
    let dismiss = |s: &mut App| {
        s.menus.ops.notices.pop_front();
    };
    let actions = if n.retry {
        vec![small("Cancel", ButtonVariant::Ghost).on_press(dismiss).build(k).key("move-notice-cancel"), small("Stop and Move", ButtonVariant::Danger).on_press(retry).build(k).key("move-notice-retry")]
    } else {
        vec![small("OK", ButtonVariant::Contrast).on_press(dismiss).build(k).key("move-notice-ok")]
    };
    let mut body = col().w_full().gap(10.0).px(16.0).pb(16.0);
    if !n.message.is_empty() {
        body = body.child(text(n.message.clone()).font(META).color(muted()).wrap().w_full());
    }
    Dialog::new("move-notice", n.title.clone()).open(true).fit().width(420.0).on_close(dismiss).child(body.child(buttons(actions))).build(k, layer);
}

pub fn overlays(s: &App, layer: &mut Vec<El>) {
    if let Some(a) = &s.menus.ops.adopting {
        adopt_dialog(a, layer);
    }
    if let Some(m) = &s.menus.ops.moving {
        move_dialog(m, layer);
    }
    if let Some(n) = s.menus.ops.notices.front() {
        notice_dialog(n, layer);
    }
}
