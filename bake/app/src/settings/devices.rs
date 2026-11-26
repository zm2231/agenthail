use std::time::{Duration, Instant};

use feed::catalog::Client;
use feed::devices::{self as dev, Device, Host, Pairing, PairingStatus, Progress, RemoteAccess};
use feed::prefs::Failure;
use rune::{El, ImageFilter, Pixels, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog};
use time::OffsetDateTime;

use super::parts::{self, line, section};
use super::{Done, Prefs, Read, Section, busy, saving, write};
use crate::theme::{self, BODY, META, done, failed, muted};

const CHECK_EVERY: Duration = Duration::from_millis(1500);
const QR_SHOWN: Duration = Duration::from_secs(60);
const COPIED_SHOWN: Duration = Duration::from_secs(2);
const QR_SIDE: f32 = 128.0;
const BROWSER_LINK: &str = "Each link signs one browser in to this Agenthail, once, within 15 minutes. Share it only with your own devices. On iPhone, open it, tap Share, then Add to Home Screen.";

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Use {
    Copy,
    Show,
}

pub enum Got {
    Started(u64, Result<Pairing, Failure>),
    Status(String, Result<PairingStatus, Failure>),
    Link(u64, Use, Result<String, Failure>),
}

#[derive(Default)]
pub struct Devices {
    pub list: Option<Result<Vec<Device>, Failure>>,
    pairing: Option<(Pairing, Option<Pixels>)>,
    starting: bool,
    attempt: u64,
    checking: bool,
    next_check: Option<Instant>,
    notice: Option<String>,
    problem: Option<Failure>,
    revoking: Option<Device>,
    linking: Option<Use>,
    link_attempt: u64,
    link_qr: Option<(Pixels, Instant)>,
    copied_until: Option<Instant>,
    copy: Option<String>,
    seen: bool,
}

fn picture(value: &str) -> Option<Pixels> {
    let m = dev::qr(value)?;
    let pixels = Pixels::new();
    pixels.set(m.size as u32, m.size as u32, m.rgba());
    Some(pixels)
}

fn spawn(s: &mut Prefs, job: impl FnOnce(&Client) -> Got + Send + 'static) -> bool {
    let Some(client) = s.client.clone() else {
        s.dev.problem = Some(super::unread(s));
        return false;
    };
    s.in_flight += 1;
    let tx = s.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(Done::Devices(job(&client)));
    });
    true
}

fn start(s: &mut Prefs) {
    if s.dev.starting || s.dev.pairing.is_some() {
        return;
    }
    s.dev.attempt += 1;
    s.dev.notice = None;
    s.dev.problem = None;
    let attempt = s.dev.attempt;
    s.dev.starting = spawn(s, move |c| Got::Started(attempt, c.start_pairing()));
}

fn cancel(s: &mut Prefs) {
    s.dev.attempt += 1;
    s.dev.starting = false;
    s.dev.pairing = None;
    s.dev.next_check = None;
}

fn link(s: &mut Prefs, purpose: Use) {
    if s.dev.linking.is_some() {
        return;
    }
    s.dev.link_attempt += 1;
    s.dev.problem = None;
    let attempt = s.dev.link_attempt;
    if spawn(s, move |c| Got::Link(attempt, purpose, c.login_link())) {
        s.dev.linking = Some(purpose);
    }
}

fn hide_link(s: &mut Prefs) {
    if s.dev.linking == Some(Use::Show) {
        s.dev.link_attempt += 1;
        s.dev.linking = None;
    }
    s.dev.link_qr = None;
}

pub fn opened(s: &mut Prefs) {
    hide_link(s);
    s.dev.copied_until = None;
}

pub fn take(s: &mut Prefs, got: Got) {
    let now = Instant::now();
    match got {
        Got::Started(attempt, result) => {
            if attempt != s.dev.attempt {
                return;
            }
            s.dev.starting = false;
            match result {
                Ok(p) => {
                    let pixels = picture(&p.pairing_url);
                    s.dev.pairing = Some((p, pixels));
                    s.dev.next_check = Some(now + CHECK_EVERY);
                }
                Err(f) => {
                    let denied = f.denied;
                    s.dev.problem = Some(f);
                    super::refused(s, None, denied);
                }
            }
        }
        Got::Status(id, result) => {
            s.dev.checking = false;
            let Some((p, _)) = s.dev.pairing.as_ref().filter(|(p, _)| p.id == id) else { return };
            match Progress::of(result.as_ref().ok(), p, OffsetDateTime::now_utc()) {
                Progress::Waiting => s.dev.next_check = Some(now + CHECK_EVERY),
                progress => {
                    s.dev.notice = progress.notice();
                    cancel(s);
                    if matches!(progress, Progress::Paired(_)) {
                        super::fetch(s, Read::Devices);
                    }
                }
            }
        }
        Got::Link(attempt, purpose, result) => {
            if attempt != s.dev.link_attempt {
                return;
            }
            s.dev.linking = None;
            match (result, purpose) {
                (Ok(url), Use::Copy) => {
                    s.dev.copy = Some(url);
                    s.dev.copied_until = Some(now + COPIED_SHOWN);
                }
                (Ok(url), Use::Show) => s.dev.link_qr = picture(&url).map(|p| (p, now + QR_SHOWN)),
                (Err(f), _) => {
                    let denied = f.denied;
                    s.dev.problem = Some(f);
                    super::refused(s, None, denied);
                }
            }
        }
    }
}

pub fn poll(s: &mut Prefs, tx: &mut Tx) -> bool {
    let now = Instant::now();
    let mut changed = false;
    if let Some(url) = s.dev.copy.take() {
        tx.copy(url);
    }
    if s.dev.copied_until.is_some_and(|at| now >= at) {
        s.dev.copied_until = None;
        changed = true;
    }
    let shown = super::visible();
    let closed = s.dev.seen && !shown;
    s.dev.seen = shown;
    if (s.dev.link_qr.as_ref().is_some_and(|(_, at)| now >= *at) || closed) && (s.dev.link_qr.is_some() || s.dev.linking == Some(Use::Show)) {
        hide_link(s);
        changed = true;
    }
    if !s.dev.checking
        && s.dev.next_check.is_some_and(|at| now >= at)
        && let Some((p, _)) = &s.dev.pairing
    {
        let id = p.id.clone();
        s.dev.next_check = None;
        s.dev.checking = spawn(s, move |c| Got::Status(id.clone(), c.pairing_status(&id)));
    }
    let next = [s.dev.next_check, s.dev.copied_until, s.dev.link_qr.as_ref().map(|(_, at)| *at)].into_iter().flatten().min();
    if let Some(at) = next {
        tx.wake_after(at.saturating_duration_since(now).max(Duration::from_millis(16)));
    }
    changed
}

fn qr(pixels: &Pixels) -> El {
    el().size(QR_SIDE, QR_SIDE).radius(4.0).pixels(pixels).image_filter(ImageFilter::Nearest).label("QR code")
}

fn note(s: &Prefs, f: &Failure) -> El {
    row().w_full().px(10.0).py(10.0).child(parts::failure(s, f).grow(1.0))
}

fn phone(s: &Prefs, r: &RemoteAccess) -> El {
    let on = r.enabled;
    let switch = parts::switch("phone-access", "Phone access", on, busy(s), move |s| {
        cancel(s);
        write(s, "phone-access", Section::Devices, &[Read::Settings], move |c| c.set_phone_access(!on));
    });
    let saving = saving(s, "phone-access");
    let detail = if saving {
        if on { "Turning phone access off…".to_string() } else { "Turning phone access on…".to_string() }
    } else {
        r.detail()
    };
    let problem = r.problem().is_some() && !saving;
    let mut rows = vec![line("Phone access", (!problem).then_some(detail), switch)];
    if problem {
        rows.push(parts::problem(r.detail()));
    }
    section("", rows, None)
}

fn browser(s: &Prefs, r: &RemoteAccess) -> El {
    let k = theme::kit();
    let shown = s.dev.link_qr.as_ref().map(|(p, _)| p);
    let title = match shown {
        Some(_) => r.dns_name.clone().filter(|d| !d.is_empty()).unwrap_or_else(|| "Browser link".into()),
        None => "Browser link hidden".into(),
    };
    let copy_label = if s.dev.copied_until.is_some() { "Copied" } else { "Copy Browser Link" };
    let copy = Button::new(copy_label).control_size(ControlSize::Small).disabled(s.dev.linking.is_some()).on_press(|s: &mut Prefs| link(s, Use::Copy)).build(k).key("settings-browser-copy");
    let show_label = if shown.is_some() { "Hide QR Code" } else { "Show QR Code" };
    let showing = shown.is_some();
    let show = Button::new(show_label)
        .control_size(ControlSize::Small)
        .disabled(s.dev.linking.is_some() && !showing)
        .on_press(move |s: &mut Prefs| if showing { hide_link(s) } else { link(s, Use::Show) })
        .build(k)
        .key("settings-browser-qr");
    let words = col()
        .gap(6.0)
        .grow(1.0)
        .min_w(0.0)
        .child(text(title).font(BODY).color(theme::text()).wrap())
        .child(text(BROWSER_LINK).font(META).color(muted()).wrap())
        .child(row().gap(8.0).items_center().pt(2.0).child(copy).child(show));
    let body = row().w_full().gap(14.0).items_start().px(14.0).py(12.0).children(shown.map(qr)).child(words);
    section("Open in a browser", vec![body], None)
}

fn pair(s: &Prefs, enabled: bool) -> El {
    let k = theme::kit();
    let mut rows = Vec::new();
    if let Some((p, pixels)) = &s.dev.pairing {
        let cancel = Button::new("Cancel").control_size(ControlSize::Small).on_press(cancel).build(k).key("settings-pair-cancel");
        let words = col()
            .gap(6.0)
            .grow(1.0)
            .min_w(0.0)
            .child(text("Waiting for the iPhone").font(BODY).color(theme::text()))
            .child(text(format!("Scan with the iPhone's camera, or with Scan pairing code in Agenthail. {}", p.deadline(OffsetDateTime::now_utc()))).font(META).color(muted()).wrap())
            .child(text(p.endpoint.clone()).font(META).color(muted()).wrap())
            .child(row().pt(2.0).child(cancel));
        rows.push(row().w_full().gap(14.0).items_start().px(14.0).py(12.0).children(pixels.as_ref().map(qr)).child(words));
    } else {
        let label = if s.dev.starting { "Starting…" } else { "Show Pairing Code" };
        let button = Button::new(label).control_size(ControlSize::Small).disabled(!enabled || s.dev.starting || s.client.is_none()).on_press(start).build(k).key("settings-pair");
        rows.push(row().w_full().px(14.0).py(10.0).child(button));
    }
    if let Some(n) = &s.dev.notice {
        rows.push(parts::words(n.clone()));
    }
    if let Some(f) = &s.dev.problem {
        rows.push(note(s, f));
    }
    section("Pair an iPhone", rows, None)
}

fn devices(s: &Prefs) -> El {
    let k = theme::kit();
    let rows = match &s.dev.list {
        None => vec![parts::words("Reading paired devices…")],
        Some(Err(f)) => vec![note(s, f)],
        Some(Ok(list)) if list.is_empty() => vec![parts::words("No devices are paired.")],
        Some(Ok(list)) => {
            let now = OffsetDateTime::now_utc();
            list.iter()
                .map(|d| {
                    let target = d.clone();
                    let revoke = Button::new("Revoke")
                        .variant(ButtonVariant::Danger)
                        .control_size(ControlSize::Small)
                        .disabled(busy(s))
                        .on_press(move |s: &mut Prefs| s.dev.revoking = Some(target.clone()))
                        .build(k)
                        .key(format!("settings-revoke-{}", d.id));
                    line(&d.name, Some(d.detail(now)), revoke)
                })
                .collect()
        }
    };
    section("Paired devices", rows, None)
}

fn confirm(s: &Prefs, layer: &mut Vec<El>) {
    let Some(d) = &s.dev.revoking else { return };
    let k = theme::kit();
    let id = d.id.clone();
    let close = |s: &mut Prefs| s.dev.revoking = None;
    let buttons = row()
        .w_full()
        .gap(8.0)
        .px(16.0)
        .py(14.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(close).build(k).key("revoke-cancel"))
        .child(
            Button::new("Disconnect")
                .variant(ButtonVariant::Danger)
                .control_size(ControlSize::Small)
                .disabled(busy(s))
                .on_press(move |s: &mut Prefs| {
                    s.dev.revoking = None;
                    let id = id.clone();
                    write(s, format!("revoke-{id}"), Section::Devices, &[Read::Devices], move |c| c.revoke_device(&id));
                })
                .build(k)
                .key("revoke-confirm"),
        );
    Dialog::new("revoke-device", format!("Disconnect {} from this computer?", d.name))
        .description("It stops reaching this computer until you pair it again.")
        .open(true)
        .fit()
        .width(420.0)
        .on_close(close)
        .child(buttons)
        .build(k, layer);
}

fn host_row(h: &Host) -> El {
    let state = text(h.state_label()).font(BODY).color(if h.healthy() { done() } else { failed() });
    let mut c = col().w_full().gap(2.0).px(14.0).py(10.0).child(row().w_full().gap(16.0).items_center().child(text(h.name.clone()).font(BODY).color(theme::text()).grow(1.0)).child(state));
    if let Some(detail) = h.detail.clone().filter(|d| !d.is_empty()) {
        c = c.child(text(detail).font(META).color(muted()).wrap());
    }
    if let Some(build) = &h.build {
        c = c.child(text(build.label()).font(META).color(muted()));
    }
    if let Some(notice) = h.notice.clone().filter(|n| !n.is_empty()) {
        c = c.child(text(notice).font(META).color(failed()).wrap());
    }
    c
}

fn hosts(s: &Prefs) -> Option<El> {
    let daemon = &s.base.as_ref()?.as_ref().ok()?.daemon;
    if daemon.host.is_none() && daemon.hosts.is_none() {
        return None;
    }
    let mut rows = Vec::new();
    if let Some(h) = &daemon.host {
        rows.push(parts::fact("This computer", h.name.clone()));
    }
    let paired = daemon.hosts.as_deref().unwrap_or_default();
    if paired.is_empty() {
        rows.push(parts::words(dev::NO_HOSTS));
    }
    rows.extend(paired.iter().map(host_row));
    Some(section("Hosts", rows, None))
}

pub fn page(s: &Prefs, layer: &mut Vec<El>) -> El {
    let mut out = parts::page();
    if let Some(f) = s.notes.get(&Section::Devices) {
        out = out.child(parts::failure(s, f));
    }
    let remote = match &s.settings {
        None => {
            out = out.child(parts::reading("phone access"));
            None
        }
        Some(Err(f)) => {
            out = out.child(parts::failure(s, f));
            None
        }
        Some(Ok(st)) => st.remote_access.as_ref(),
    };
    if let Some(r) = remote {
        out = out.child(phone(s, r));
        if r.enabled {
            out = out.child(browser(s, r));
        }
    }
    out = out.child(pair(s, remote.is_some_and(|r| r.enabled))).child(devices(s));
    if let Some(h) = hosts(s) {
        out = out.child(h);
    }
    confirm(s, layer);
    out
}
