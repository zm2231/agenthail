use std::time::{Duration, Instant};

use feed::catalog::Client;
use feed::devices::{self as dev, Device, Host, Pairing, PairingStatus, Progress, RemoteAccess};
use feed::prefs::Failure;
use rune::{El, ImageFilter, Pixels, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, Kit};
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
    Status(u64, Result<PairingStatus, Failure>),
    Link(u64, Use, Result<String, Failure>),
}

#[derive(Default)]
pub struct Devices {
    pub list: Option<Result<Vec<Device>, Failure>>,
    pairing: Option<(Pairing, Option<Pixels>)>,
    expires: Option<Instant>,
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
    if s.dev.starting || s.dev.pairing.is_some() || busy(s) {
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
    clear(s);
}

fn clear(s: &mut Prefs) {
    s.dev.starting = false;
    s.dev.checking = false;
    s.dev.pairing = None;
    s.dev.expires = None;
    s.dev.next_check = None;
}

fn drop_link(s: &mut Prefs) {
    s.dev.link_attempt += 1;
    s.dev.linking = None;
    s.dev.link_qr = None;
    s.dev.copy = None;
    s.dev.copied_until = None;
}

pub fn settled(s: &mut Prefs) {
    let on = matches!(&s.settings, Some(Ok(st)) if st.remote_access.as_ref().is_some_and(|r| r.enabled));
    if !on {
        cancel(s);
        drop_link(s);
    }
}

fn set_phone(s: &mut Prefs, on: bool) {
    cancel(s);
    drop_link(s);
    write(s, "phone-access", Section::Devices, &[Read::Settings], move |c| c.set_phone_access(on));
}

fn revoke(s: &mut Prefs) {
    let Some(d) = s.dev.revoking.take() else { return };
    let id = d.id;
    write(s, format!("revoke-{id}"), Section::Devices, &[Read::Devices], move |c| c.revoke_device(&id));
}

fn expire(s: &mut Prefs, now: Instant) -> bool {
    if !s.dev.expires.is_some_and(|at| now >= at) {
        return false;
    }
    let id = s.dev.pairing.as_ref().map(|(p, _)| p.id.clone()).unwrap_or_default();
    clear(s);
    s.dev.notice = Progress::Expired.notice();
    let attempt = s.dev.attempt;
    spawn(s, move |c| Got::Status(attempt, c.pairing_status(&id)));
    true
}

fn link(s: &mut Prefs, purpose: Use) {
    if s.dev.linking.is_some() || busy(s) {
        return;
    }
    s.dev.link_attempt += 1;
    s.dev.problem = None;
    let attempt = s.dev.link_attempt;
    if spawn(s, move |c| Got::Link(attempt, purpose, c.login_link())) {
        s.dev.linking = Some(purpose);
    }
}

pub fn hide_link(s: &mut Prefs) {
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

fn reported(result: &Result<PairingStatus, Failure>) -> Progress {
    match result {
        Ok(status) => Progress::of(status),
        Err(_) => Progress::Waiting,
    }
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
                    let left = feed::catalog::parse_time(&p.expires_at).map(|at| at - OffsetDateTime::now_utc());
                    s.dev.expires = left.map(|d| now + Duration::try_from(d).unwrap_or_default());
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
        Got::Status(attempt, result) => {
            if attempt != s.dev.attempt {
                return;
            }
            s.dev.checking = false;
            if let Some(f) = result.as_ref().err().filter(|f| f.denied) {
                let f = f.clone();
                cancel(s);
                s.dev.problem = Some(f);
                super::refused(s, None, true);
                return;
            }
            if s.dev.pairing.is_none() {
                if let Progress::Paired(name) = reported(&result) {
                    s.dev.notice = Progress::Paired(name).notice();
                    super::fetch(s, Read::Devices);
                }
                return;
            }
            match reported(&result) {
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
    changed |= expire(s, now);
    if !s.dev.checking
        && s.dev.next_check.is_some_and(|at| now >= at)
        && let Some((p, _)) = &s.dev.pairing
    {
        let id = p.id.clone();
        let attempt = s.dev.attempt;
        s.dev.next_check = None;
        s.dev.checking = spawn(s, move |c| Got::Status(attempt, c.pairing_status(&id)));
    }
    let next = [s.dev.next_check, s.dev.expires, s.dev.copied_until, s.dev.link_qr.as_ref().map(|(_, at)| *at)].into_iter().flatten().min();
    if let Some(at) = next {
        s.wake.after(tx, at.saturating_duration_since(now).max(Duration::from_millis(16)));
    }
    changed
}

fn qr(pixels: &Pixels) -> El {
    el().size(QR_SIDE, QR_SIDE).radius(4.0).pixels(pixels).image_filter(ImageFilter::Nearest).label("QR code")
}

fn note(s: &Prefs, f: &Failure, k: &Kit) -> El {
    row().w_full().px(10.0).py(10.0).child(parts::failure(s, f, k).grow(1.0))
}

fn phone(s: &Prefs, r: &RemoteAccess, k: &Kit) -> El {
    let on = r.enabled;
    let switch = parts::switch("phone-access", "Phone access", on, busy(s), move |s| set_phone(s, !on), k);
    let saving = saving(s, "phone-access");
    let detail = if saving {
        if on { "Turning phone access off…".to_string() } else { "Turning phone access on…".to_string() }
    } else {
        r.detail()
    };
    let problem = r.problem().is_some() && !saving;
    let mut rows = vec![line("Phone access", (!problem).then_some(detail), switch, k)];
    if problem {
        rows.push(parts::problem(r.detail(), k));
    }
    section("", rows, None, k)
}

fn browser(s: &Prefs, r: &RemoteAccess, k: &Kit) -> El {
    let shown = s.dev.link_qr.as_ref().map(|(p, _)| p);
    let title = match shown {
        Some(_) => r.dns_name.clone().filter(|d| !d.is_empty()).unwrap_or_else(|| "Browser link".into()),
        None => "Browser link hidden".into(),
    };
    let copy_label = if s.dev.copied_until.is_some() { "Copied" } else { "Copy Browser Link" };
    let copy = Button::new(copy_label).control_size(ControlSize::Small).disabled(s.dev.linking.is_some() || busy(s)).on_press(|s: &mut Prefs| link(s, Use::Copy)).build(k).key("settings-browser-copy");
    let show_label = if shown.is_some() { "Hide QR Code" } else { "Show QR Code" };
    let showing = shown.is_some();
    let show = Button::new(show_label)
        .control_size(ControlSize::Small)
        .disabled(!showing && (s.dev.linking.is_some() || busy(s)))
        .on_press(move |s: &mut Prefs| if showing { hide_link(s) } else { link(s, Use::Show) })
        .build(k)
        .key("settings-browser-qr");
    let words = col()
        .gap(6.0)
        .grow(1.0)
        .min_w(0.0)
        .child(text(title).font(k.text(BODY)).color(theme::text()).wrap())
        .child(text(BROWSER_LINK).font(k.text(META)).color(muted()).wrap())
        .child(row().gap(8.0).items_center().pt(2.0).child(copy).child(show));
    let body = row().w_full().gap(14.0).items_start().px(14.0).py(12.0).children(shown.map(qr)).child(words);
    section("Open in a browser", vec![body], None, k)
}

fn pair(s: &Prefs, enabled: bool, k: &Kit) -> El {
    let mut rows = Vec::new();
    if let Some((p, pixels)) = &s.dev.pairing {
        let cancel = Button::new("Cancel").control_size(ControlSize::Small).on_press(cancel).build(k).key("settings-pair-cancel");
        let words = col()
            .gap(6.0)
            .grow(1.0)
            .min_w(0.0)
            .child(text("Waiting for the iPhone").font(k.text(BODY)).color(theme::text()))
            .child(text(format!("Scan with the iPhone's camera, or with Scan pairing code in Agenthail. {}", p.deadline(OffsetDateTime::now_utc()))).font(k.text(META)).color(muted()).wrap())
            .child(text(p.endpoint.clone()).font(k.text(META)).color(muted()).wrap())
            .child(row().pt(2.0).child(cancel));
        rows.push(row().w_full().gap(14.0).items_start().px(14.0).py(12.0).children(pixels.as_ref().map(qr)).child(words));
    } else {
        let label = if s.dev.starting { "Starting…" } else { "Show Pairing Code" };
        let button = Button::new(label).control_size(ControlSize::Small).disabled(!enabled || s.dev.starting || busy(s) || s.client.is_none()).on_press(start).build(k).key("settings-pair");
        rows.push(row().w_full().px(14.0).py(10.0).child(button));
    }
    if let Some(n) = &s.dev.notice {
        rows.push(parts::words(n.clone(), k));
    }
    if let Some(f) = &s.dev.problem {
        rows.push(note(s, f, k));
    }
    section("Pair an iPhone", rows, None, k)
}

fn devices(s: &Prefs, k: &Kit) -> El {
    let rows = match &s.dev.list {
        None => vec![parts::words("Reading paired devices…", k)],
        Some(Err(f)) => vec![note(s, f, k)],
        Some(Ok(list)) if list.is_empty() => vec![parts::words("No devices are paired.", k)],
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
                    line(&d.name, Some(d.detail(now)), revoke, k)
                })
                .collect()
        }
    };
    section("Paired devices", rows, None, k)
}

fn confirm(s: &Prefs, layer: &mut Vec<El>, k: &Kit) {
    let Some(d) = &s.dev.revoking else { return };
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
                .on_press(revoke)
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

fn host_row(h: &Host, k: &Kit) -> El {
    let state = text(h.state_label()).font(k.text(BODY)).color(if h.healthy() { done() } else { failed() });
    let mut c = col().w_full().gap(2.0).px(14.0).py(10.0).child(row().w_full().gap(16.0).items_center().child(text(h.name.clone()).font(k.text(BODY)).color(theme::text()).grow(1.0)).child(state));
    if let Some(detail) = h.detail.clone().filter(|d| !d.is_empty()) {
        c = c.child(text(detail).font(k.text(META)).color(muted()).wrap());
    }
    if let Some(build) = &h.build {
        c = c.child(text(build.label()).font(k.text(META)).color(muted()));
    }
    if let Some(notice) = h.notice.clone().filter(|n| !n.is_empty()) {
        c = c.child(text(notice).font(k.text(META)).color(failed()).wrap());
    }
    c
}

fn hosts(s: &Prefs, k: &Kit) -> Option<El> {
    let daemon = &s.base.as_ref()?.as_ref().ok()?.daemon;
    if daemon.host.is_none() && daemon.hosts.is_none() {
        return None;
    }
    let mut rows = Vec::new();
    if let Some(h) = &daemon.host {
        rows.push(parts::fact("This computer", h.name.clone(), k));
    }
    let paired = daemon.hosts.as_deref().unwrap_or_default();
    if paired.is_empty() {
        rows.push(parts::words(dev::NO_HOSTS, k));
    }
    rows.extend(paired.iter().map(|h| host_row(h, k)));
    Some(section("Hosts", rows, None, k))
}

pub fn page(s: &Prefs, layer: &mut Vec<El>, k: &Kit) -> El {
    let mut out = parts::page();
    if let Some(f) = s.notes.get(&Section::Devices) {
        out = out.child(parts::failure(s, f, k));
    }
    let remote = match &s.settings {
        None => {
            out = out.child(parts::reading("phone access", k));
            None
        }
        Some(Err(f)) => {
            out = out.child(parts::failure(s, f, k));
            None
        }
        Some(Ok(st)) => st.remote_access.as_ref(),
    };
    if let Some(r) = remote {
        out = out.child(phone(s, r, k));
        if r.enabled {
            out = out.child(browser(s, r, k));
        }
    }
    out = out.child(pair(s, remote.is_some_and(|r| r.enabled), k)).child(devices(s, k));
    if let Some(h) = hosts(s, k) {
        out = out.child(h);
    }
    confirm(s, layer, k);
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use feed::prefs::Settings;

    fn prefs() -> Prefs {
        let mut s = super::super::init();
        s.client = Some(Client::new("http://127.0.0.1:9", "t"));
        s
    }

    fn pairing(id: &str, minutes: i64) -> Pairing {
        let at = OffsetDateTime::now_utc() + time::Duration::minutes(minutes);
        Pairing { id: id.into(), expires_at: at.format(&time::format_description::well_known::Rfc3339).unwrap(), pairing_url: "agenthail://pair?secret=s".into(), endpoint: "https://studio:7412".into() }
    }

    fn started(s: &mut Prefs, id: &str, minutes: i64) {
        start(s);
        take(s, Got::Started(s.dev.attempt, Ok(pairing(id, minutes))));
    }

    #[test]
    fn a_start_that_returns_after_cancel_shows_nothing() {
        let mut s = prefs();
        start(&mut s);
        let first = s.dev.attempt;
        cancel(&mut s);
        take(&mut s, Got::Started(first, Ok(pairing("p1", 5))));
        assert!(s.dev.pairing.is_none() && !s.dev.starting);
    }

    #[test]
    fn a_cancelled_check_neither_blocks_nor_ends_the_next_pairing() {
        let mut s = prefs();
        started(&mut s, "p1", 5);
        let old = s.dev.attempt;
        s.dev.checking = true;
        cancel(&mut s);
        assert!(!s.dev.checking);
        started(&mut s, "p2", 5);
        take(&mut s, Got::Status(old, Ok(PairingStatus { state: "expired".into(), device: None })));
        assert_eq!(s.dev.pairing.as_ref().map(|(p, _)| p.id.as_str()), Some("p2"));
        assert!(s.dev.next_check.is_some() && s.dev.expires.is_some());
    }

    #[test]
    fn a_refused_check_stops_pairing_and_says_why() {
        let mut s = prefs();
        started(&mut s, "p1", 5);
        let refused = Failure { denied: true, scope: "settings", ..Failure::new("refused") };
        let current = s.dev.attempt;
        take(&mut s, Got::Status(current, Err(refused)));
        assert!(s.dev.pairing.is_none());
        assert!(s.dev.problem.as_ref().is_some_and(|f| f.denied));
    }

    #[test]
    fn a_failed_check_keeps_waiting_and_pairing_ends_at_its_deadline() {
        let mut s = prefs();
        started(&mut s, "p1", 5);
        let current = s.dev.attempt;
        take(&mut s, Got::Status(current, Err(Failure::new("timed out"))));
        assert!(s.dev.pairing.is_some() && s.dev.next_check.is_some());
        assert!(!expire(&mut s, Instant::now()));
        assert!(expire(&mut s, Instant::now() + Duration::from_secs(301)));
        assert!(s.dev.pairing.is_none());
        assert_eq!(s.dev.notice.as_deref(), Some(dev::EXPIRED));
    }

    #[test]
    fn a_phone_that_pairs_just_before_the_deadline_is_reported_paired() {
        let mut s = prefs();
        started(&mut s, "p1", 5);
        assert!(expire(&mut s, Instant::now() + Duration::from_secs(301)));
        assert_eq!(s.dev.notice.as_deref(), Some(dev::EXPIRED));
        let last = s.dev.attempt;
        take(&mut s, Got::Status(last, Ok(PairingStatus { state: "paired".into(), device: Some(dev::PairedName { name: "Ada's iPhone".into() }) })));
        assert_eq!(s.dev.notice.as_deref(), Some("Paired Ada's iPhone."));
        assert!(s.dev.pairing.is_none());
        take(&mut s, Got::Status(last, Ok(PairingStatus { state: "expired".into(), device: None })));
        assert_eq!(s.dev.notice.as_deref(), Some("Paired Ada's iPhone."));
        start(&mut s);
        take(&mut s, Got::Status(last, Ok(PairingStatus { state: "paired".into(), device: None })));
        assert_eq!(s.dev.notice, None);
    }

    #[test]
    fn a_pending_reply_read_after_the_deadline_leaves_the_end_to_the_final_check() {
        let mut s = prefs();
        started(&mut s, "p1", 5);
        let current = s.dev.attempt;
        s.dev.expires = Some(Instant::now() - Duration::from_secs(1));
        take(&mut s, Got::Status(current, Ok(PairingStatus { state: "pending".into(), device: None })));
        assert!(s.dev.pairing.is_some() && s.dev.notice.is_none());
        take(&mut s, Got::Status(current, Err(Failure::new("timed out"))));
        assert!(s.dev.pairing.is_some());
        assert!(expire(&mut s, Instant::now()));
        let last = s.dev.attempt;
        take(&mut s, Got::Status(last, Ok(PairingStatus { state: "paired".into(), device: Some(dev::PairedName { name: "Ada's iPhone".into() }) })));
        assert_eq!(s.dev.notice.as_deref(), Some("Paired Ada's iPhone."));
    }

    #[test]
    fn a_paired_reply_already_on_its_way_at_the_deadline_still_counts() {
        let mut s = prefs();
        started(&mut s, "p1", 5);
        let inflight = s.dev.attempt;
        s.dev.checking = true;
        assert!(expire(&mut s, Instant::now() + Duration::from_secs(301)));
        take(&mut s, Got::Status(inflight, Ok(PairingStatus { state: "paired".into(), device: Some(dev::PairedName { name: "Ada's iPhone".into() }) })));
        assert_eq!(s.dev.notice.as_deref(), Some("Paired Ada's iPhone."));
        take(&mut s, Got::Status(inflight, Err(Failure::new("timed out"))));
        assert_eq!(s.dev.notice.as_deref(), Some("Paired Ada's iPhone."));
    }

    #[test]
    fn the_daemon_saying_expired_ends_pairing() {
        let mut s = prefs();
        started(&mut s, "p1", 5);
        let current = s.dev.attempt;
        take(&mut s, Got::Status(current, Ok(PairingStatus { state: "expired".into(), device: None })));
        assert!(s.dev.pairing.is_none());
        assert_eq!(s.dev.notice.as_deref(), Some(dev::EXPIRED));
    }

    #[test]
    fn a_paired_phone_ends_pairing_with_its_name() {
        let mut s = prefs();
        started(&mut s, "p1", 5);
        let current = s.dev.attempt;
        take(&mut s, Got::Status(current, Ok(PairingStatus { state: "paired".into(), device: Some(dev::PairedName { name: "Ada's iPhone".into() }) })));
        assert!(s.dev.pairing.is_none());
        assert_eq!(s.dev.notice.as_deref(), Some("Paired Ada's iPhone."));
    }

    #[test]
    fn nothing_starts_while_a_setting_saves() {
        let mut s = prefs();
        s.writing = Some("phone-access".into());
        start(&mut s);
        link(&mut s, Use::Copy);
        assert!(!s.dev.starting && s.dev.linking.is_none());
    }

    #[test]
    fn turning_phone_access_off_drops_the_code_and_the_browser_link() {
        let mut s = prefs();
        started(&mut s, "p1", 5);
        link(&mut s, Use::Show);
        let shown = s.dev.link_attempt;
        set_phone(&mut s, false);
        assert!(s.dev.pairing.is_none() && s.dev.linking.is_none());
        take(&mut s, Got::Link(shown, Use::Show, Ok("https://studio/login?t=1".into())));
        assert!(s.dev.link_qr.is_none());
        assert!(saving(&s, "phone-access"));
    }

    #[test]
    fn a_copy_still_out_when_phone_access_goes_off_never_reaches_the_clipboard() {
        let mut s = prefs();
        link(&mut s, Use::Copy);
        let pending = s.dev.link_attempt;
        set_phone(&mut s, false);
        take(&mut s, Got::Link(pending, Use::Copy, Ok("https://studio/login?t=1".into())));
        assert!(s.dev.copy.is_none() && s.dev.copied_until.is_none());
    }

    #[test]
    fn phone_access_turned_off_elsewhere_ends_pairing_and_drops_links() {
        let mut s = prefs();
        let access = |on: bool| Settings { remote_access: Some(RemoteAccess { enabled: on, ..Default::default() }), ..Default::default() };
        s.settings = Some(Ok(access(true)));
        started(&mut s, "p1", 5);
        link(&mut s, Use::Show);
        let shown = s.dev.link_attempt;
        take(&mut s, Got::Link(shown, Use::Show, Ok("https://studio/login?t=1".into())));
        settled(&mut s);
        assert!(s.dev.pairing.is_some() && s.dev.link_qr.is_some());
        link(&mut s, Use::Copy);
        let pending = s.dev.link_attempt;
        s.settings = Some(Ok(access(false)));
        settled(&mut s);
        assert!(s.dev.pairing.is_none() && s.dev.link_qr.is_none());
        take(&mut s, Got::Link(pending, Use::Copy, Ok("https://studio/login?t=2".into())));
        s.settings = Some(Ok(access(true)));
        settled(&mut s);
        assert!(s.dev.link_qr.is_none() && s.dev.copy.is_none());
    }

    #[test]
    fn a_shown_link_hides_and_a_late_one_never_appears() {
        let mut s = prefs();
        link(&mut s, Use::Show);
        let current = s.dev.link_attempt;
        take(&mut s, Got::Link(current, Use::Show, Ok("https://studio/login?t=1".into())));
        assert!(s.dev.link_qr.is_some());
        hide_link(&mut s);
        assert!(s.dev.link_qr.is_none());
        link(&mut s, Use::Show);
        let late = s.dev.link_attempt;
        hide_link(&mut s);
        take(&mut s, Got::Link(late, Use::Show, Ok("https://studio/login?t=2".into())));
        assert!(s.dev.link_qr.is_none());
    }

    #[test]
    fn revoke_writes_only_after_the_confirm() {
        let mut s = prefs();
        revoke(&mut s);
        assert!(s.writing.is_none());
        s.dev.revoking = Some(Device { id: "d1".into(), name: "Sam's iPhone".into(), ..Default::default() });
        revoke(&mut s);
        assert!(s.dev.revoking.is_none() && saving(&s, "revoke-d1"));
    }
}
