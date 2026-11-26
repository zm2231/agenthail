use std::cell::RefCell;
use std::rc::Rc;
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::extensions::WebSession;
use rune::NativeView;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

const RENEW_BEFORE: time::Duration = time::Duration::hours(1);
const RENEW_RETRY: Duration = Duration::from_secs(60);
const SLOW: Duration = Duration::from_secs(10);
const CHECK_EVERY: Duration = Duration::from_secs(20);

#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
enum Load {
    Started(String),
    Finished(String),
}

#[derive(Clone, Debug, PartialEq)]
pub enum Phase {
    SigningIn,
    Loading,
    Shown,
    Failed(String),
}

pub struct Web {
    pub slot: Option<NativeView>,
    #[cfg(target_os = "macos")]
    view: Option<wry::WebView>,
    pub phase: Phase,
    cookie: Option<WebSession>,
    cookie_set: bool,
    signing: bool,
    renew_at: Option<Instant>,
    checking: bool,
    checked_at: Option<Instant>,
    verified: Option<String>,
    loaded: Option<String>,
    ready: Option<String>,
    loading_since: Option<Instant>,
    committed: bool,
    arrived: bool,
    slow_shown: bool,
    left: Option<String>,
    page: Rc<RefCell<String>>,
    loads: (Sender<Load>, Receiver<Load>),
    follow: (Sender<String>, Receiver<String>),
}

impl Web {
    pub fn new() -> Web {
        Web {
            slot: None,
            #[cfg(target_os = "macos")]
            view: None,
            phase: Phase::SigningIn,
            cookie: None,
            cookie_set: false,
            signing: false,
            renew_at: None,
            checking: false,
            checked_at: None,
            verified: None,
            loaded: None,
            ready: None,
            loading_since: None,
            committed: false,
            arrived: false,
            slow_shown: false,
            left: None,
            page: Rc::new(RefCell::new(String::new())),
            loads: channel(),
            follow: channel(),
        }
    }

    fn failed(&self) -> bool {
        matches!(self.phase, Phase::Failed(_))
    }

    pub fn sign_in_due(&mut self, now: OffsetDateTime) -> bool {
        let waiting = self.renew_at.is_some_and(|at| at > Instant::now());
        let due = !self.failed() && !self.signing && !waiting && self.cookie.as_ref().is_none_or(|c| expiring(c, now));
        self.signing |= due;
        due
    }

    pub fn signed(&mut self, result: Result<WebSession, String>) {
        self.signing = false;
        match result {
            Ok(cookie) => {
                self.verified = Some(cookie.value.clone());
                self.checked_at = Some(Instant::now());
                self.cookie = Some(cookie);
                self.cookie_set = false;
                self.renew_at = None;
            }
            Err(_) if self.cookie.as_ref().is_some_and(|c| !expired(c, OffsetDateTime::now_utc())) => self.renew_at = Some(Instant::now() + RENEW_RETRY),
            Err(error) => self.phase = Phase::Failed(format!("Couldn't sign in to the page: {error}")),
        }
    }

    pub fn check_due(&mut self, want: Option<&str>) -> Option<(String, WebSession)> {
        let want = want?;
        if self.failed() || self.signing || self.checking {
            return None;
        }
        let cookie = self.cookie.clone()?;
        let opening = self.loaded.as_deref() != Some(want) && self.verified.as_deref() != Some(cookie.value.as_str());
        if !opening && self.checked_at.is_some_and(|at| at.elapsed() < CHECK_EVERY) {
            return None;
        }
        let page = want.to_string();
        self.checking = true;
        self.checked_at = Some(Instant::now());
        Some((page, cookie))
    }

    pub fn checked(&mut self, value: &str, known: Result<bool, String>) {
        self.checking = false;
        if self.cookie.as_ref().is_none_or(|c| c.value != value) {
            return;
        }
        if known == Ok(false) {
            self.cookie = None;
            self.renew_at = None;
            self.verified = None;
            if self.phase == Phase::Loading {
                self.loaded = None;
                self.ready = None;
            }
        } else {
            self.verified = Some(value.to_string());
        }
    }

    pub fn reload(&mut self) {
        self.phase = Phase::SigningIn;
        self.cookie = None;
        self.cookie_set = false;
        self.renew_at = None;
        self.verified = None;
        self.loaded = None;
        self.ready = None;
        self.committed = false;
        self.arrived = false;
        self.loading_since = None;
    }

    pub fn slow(&self) -> bool {
        self.loading_since.is_some_and(|at| at.elapsed() >= SLOW)
    }

    pub fn shows(&self, want: &str) -> bool {
        #[cfg(target_os = "macos")]
        if self.view.is_some() {
            return self.ready.as_deref() == Some(want);
        }
        let _ = want;
        true
    }

    pub fn pump(&mut self, want: Option<&str>) -> bool {
        let mut changed = false;
        while let Ok(load) = self.loads.1.try_recv() {
            let Some(page) = self.loaded.clone() else { continue };
            match load {
                Load::Started(url) if leaves(&url, &page) => {
                    self.left = Some(url);
                    self.committed = false;
                    self.ready = None;
                    changed = true;
                }
                Load::Started(url) => {
                    let home = feed::extensions::same_origin(&url, &page);
                    self.committed |= home;
                    self.arrived |= home;
                    self.loading_since = Some(Instant::now());
                }
                Load::Finished(url) if self.committed && self.left.is_none() && feed::extensions::same_origin(&url, &page) => {
                    self.loading_since = None;
                    if self.phase == Phase::Loading {
                        self.phase = Phase::Shown;
                        self.ready = Some(page);
                    }
                    changed = true;
                }
                Load::Finished(_) => {}
            }
        }
        if self.slow() != self.slow_shown {
            self.slow_shown = self.slow();
            changed = true;
        }
        let Some(want) = want else { return changed };
        if self.failed() {
            return changed;
        }
        if self.slot.is_none() {
            self.slot = Some(NativeView::new());
            return true;
        }
        let Some(cookie) = self.cookie.clone() else { return changed };
        let in_window = self.slot.as_ref().is_some_and(NativeView::in_window);
        match self.show(want, &cookie, in_window) {
            Ok(moved) => changed |= moved,
            Err(error) => {
                self.phase = Phase::Failed(error);
                changed = true;
            }
        }
        changed
    }

    #[cfg(target_os = "macos")]
    fn show(&mut self, want: &str, cookie: &WebSession, in_window: bool) -> Result<bool, String> {
        if self.view.is_none() {
            if !in_window {
                return Ok(false);
            }
            let Some(slot) = &self.slot else { return Ok(false) };
            self.view = Some(build(slot, self.page.clone(), self.loads.0.clone(), self.follow.0.clone())?);
        }
        let Some(view) = &self.view else { return Ok(false) };
        if !self.cookie_set {
            view.set_cookie(&web_cookie(cookie, want)?).map_err(|e| format!("Couldn't hand the page its sign-in: {e}"))?;
            self.cookie_set = true;
        }
        if let Some(url) = self.left.take() {
            crate::open_link(&url);
            if !self.arrived {
                return Err(format!("This page sent you to {url}, so it opened in your browser."));
            }
            if let Some(page) = &self.loaded {
                view.load_url(page).map_err(|e| format!("Couldn't open {page}: {e}"))?;
                self.ready = None;
                self.committed = false;
                self.arrived = false;
                self.phase = Phase::Loading;
                self.loading_since = Some(Instant::now());
            }
        }
        while let Ok(url) = self.follow.1.try_recv() {
            if self.loaded.as_deref() == Some(want) {
                view.load_url(&url).map_err(|e| format!("Couldn't open {url}: {e}"))?;
            }
        }
        if self.loaded.as_deref() != Some(want) {
            if self.verified.as_deref() != Some(cookie.value.as_str()) {
                return Ok(false);
            }
            *self.page.borrow_mut() = want.to_string();
            view.load_url(want).map_err(|e| format!("Couldn't open {want}: {e}"))?;
            self.loaded = Some(want.to_string());
            self.ready = None;
            self.committed = false;
            self.arrived = false;
            self.loading_since = Some(Instant::now());
            self.phase = Phase::Loading;
            return Ok(true);
        }
        Ok(false)
    }

    #[cfg(not(target_os = "macos"))]
    fn show(&mut self, _want: &str, _cookie: &WebSession, _in_window: bool) -> Result<bool, String> {
        let _ = (&self.page, &self.loads.0, &self.follow.0, &self.cookie_set, &self.left);
        Err("Pages open in the Mac app.".into())
    }
}

fn leaves(url: &str, page: &str) -> bool {
    (url.starts_with("http://") || url.starts_with("https://")) && !feed::extensions::same_origin(url, page)
}

fn expired(cookie: &WebSession, now: OffsetDateTime) -> bool {
    OffsetDateTime::parse(&cookie.expires_at, &Rfc3339).map_or(true, |at| at <= now)
}

fn expiring(cookie: &WebSession, now: OffsetDateTime) -> bool {
    OffsetDateTime::parse(&cookie.expires_at, &Rfc3339).map_or(true, |at| at - now < RENEW_BEFORE)
}

#[cfg(target_os = "macos")]
fn web_cookie(session: &WebSession, page: &str) -> Result<wry::cookie::Cookie<'static>, String> {
    let host = feed::extensions::page_host(page).ok_or_else(|| format!("{page} has no host"))?;
    let mut cookie = wry::cookie::Cookie::build((session.name.clone(), session.value.clone()))
        .domain(host)
        .path(if session.path.is_empty() { "/".to_string() } else { session.path.clone() })
        .http_only(true)
        .same_site(wry::cookie::SameSite::Strict)
        .secure(page.starts_with("https://"));
    if let Ok(at) = OffsetDateTime::parse(&session.expires_at, &Rfc3339) {
        cookie = cookie.expires(at);
    }
    Ok(cookie.build())
}

#[cfg(target_os = "macos")]
const OUTSIDE_LINKS: &str = r#"document.addEventListener("click", (event) => {
  const link = event.target instanceof Element ? event.target.closest("a[href]") : null;
  if (!link || event.defaultPrevented || !["", "_self", "_top", "_parent"].includes(link.target)) return;
  const url = new URL(link.href, location.href);
  if ((url.protocol === "http:" || url.protocol === "https:") && url.origin !== location.origin) {
    event.preventDefault();
    window.open(url.href, "_blank");
  }
}, true);"#;

#[cfg(target_os = "macos")]
fn build(slot: &NativeView, page: Rc<RefCell<String>>, loads: Sender<Load>, follow: Sender<String>) -> Result<wry::WebView, String> {
    use feed::extensions::{Navigation, navigation};
    let opener = page.clone();
    wry::WebViewBuilder::new()
        .with_initialization_script_for_main_only(OUTSIDE_LINKS, true)
        .with_navigation_handler(move |url| navigation(&url, &page.borrow()) != Navigation::Refuse)
        .with_new_window_req_handler(move |url, _| {
            match navigation(&url, &opener.borrow()) {
                Navigation::Allow => {
                    let _ = follow.send(url);
                }
                Navigation::Elsewhere => crate::open_link(&url),
                Navigation::Refuse => {}
            }
            wry::NewWindowResponse::Deny
        })
        .with_on_page_load_handler(move |event, url| {
            let _ = loads.send(match event {
                wry::PageLoadEvent::Started => Load::Started(url),
                wry::PageLoadEvent::Finished => Load::Finished(url),
            });
        })
        .build_as_child(slot)
        .map_err(|e| format!("No web view: {e}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn session(expires_at: &str) -> WebSession {
        WebSession { name: "agenthail_web".into(), value: "v".into(), path: "/".into(), expires_at: expires_at.into(), scopes: vec![] }
    }

    fn now() -> OffsetDateTime {
        OffsetDateTime::parse("2026-11-26T10:00:00Z", &Rfc3339).unwrap()
    }

    #[test]
    fn a_web_session_is_renewed_an_hour_before_it_ends() {
        assert!(!expiring(&session("2026-11-26T12:00:00Z"), now()));
        assert!(expiring(&session("2026-11-26T10:30:00Z"), now()));
        assert!(expiring(&session("soon"), now()));
    }

    #[test]
    fn a_failed_sign_in_waits_for_reload_and_then_tries_again() {
        let mut web = Web::new();
        assert!(web.sign_in_due(now()));
        assert!(!web.sign_in_due(now()), "one request at a time");
        web.signed(Err("refused".into()));
        assert!(web.failed());
        assert!(!web.sign_in_due(now()), "no retry loop after a failure");
        web.reload();
        assert_eq!(web.phase, Phase::SigningIn);
        assert!(web.sign_in_due(now()));
    }

    #[test]
    fn reload_signs_in_afresh_and_opens_the_entry_again() {
        let mut web = Web::new();
        web.cookie = Some(session("2026-11-26T22:00:00Z"));
        web.loaded = Some("http://127.0.0.1:48200/".into());
        web.ready = web.loaded.clone();
        web.phase = Phase::Shown;
        web.reload();
        assert!(web.cookie.is_none() && web.loaded.is_none() && web.ready.is_none());
        assert!(web.sign_in_due(now()));
    }

    #[test]
    fn a_failed_renewal_keeps_a_page_whose_cookie_still_works() {
        let mut web = Web::new();
        web.phase = Phase::Shown;
        let soon = (OffsetDateTime::now_utc() + time::Duration::minutes(30)).format(&Rfc3339).unwrap();
        web.cookie = Some(session(&soon));
        assert!(web.sign_in_due(OffsetDateTime::now_utc()));
        web.signed(Err("daemon restarting".into()));
        assert_eq!(web.phase, Phase::Shown);
        assert!(!web.sign_in_due(OffsetDateTime::now_utc()), "waits a minute before trying again");
        web.renew_at = Some(Instant::now());
        assert!(web.sign_in_due(OffsetDateTime::now_utc()));
        web.cookie = Some(session("2026-11-26T09:00:00Z"));
        web.signed(Err("daemon down".into()));
        assert!(web.failed(), "an expired cookie can't keep the page");
    }

    #[test]
    fn renewing_the_cookie_keeps_the_open_page() {
        let page = "http://127.0.0.1:48200/";
        let mut web = Web::new();
        web.cookie = Some(session("2026-11-26T22:00:00Z"));
        web.loaded = Some(page.into());
        web.ready = Some(page.into());
        web.signed(Ok(session("2026-11-27T22:00:00Z")));
        assert_eq!(web.loaded.as_deref(), Some(page));
        assert_eq!(web.ready.as_deref(), Some(page));
        assert!(!web.cookie_set, "the new cookie is handed over without loading again");
    }

    #[test]
    fn a_page_shows_once_it_finishes_and_a_stalled_load_is_marked_slow() {
        let page = "http://127.0.0.1:48200/";
        let mut web = Web::new();
        web.loaded = Some(page.into());
        web.phase = Phase::Loading;
        web.loading_since = Some(Instant::now() - SLOW);
        assert!(web.slow());
        web.loads.0.send(Load::Finished(page.into())).unwrap();
        web.pump(None);
        assert_eq!(web.phase, Phase::Loading, "a finish before this load commits belongs to an earlier one");
        web.loads.0.send(Load::Started(format!("{page}report"))).unwrap();
        web.pump(None);
        assert!(!web.slow(), "a commit restarts the clock");
        web.loads.0.send(Load::Finished(format!("{page}report"))).unwrap();
        web.pump(None);
        assert_eq!(web.phase, Phase::Shown);
        assert_eq!(web.ready.as_deref(), Some(page));
        assert!(web.loading_since.is_none());
    }

    #[test]
    fn leaving_before_and_after_the_page_arrives() {
        let page = "http://127.0.0.1:48200/";
        let mut web = Web::new();
        web.loaded = Some(page.into());
        web.phase = Phase::Loading;
        web.loads.0.send(Load::Started("https://example.com/".into())).unwrap();
        web.pump(None);
        assert!(!web.arrived, "an entry that redirects away never arrived, so it fails instead of reopening");
        web.left = None;
        web.loads.0.send(Load::Started(page.into())).unwrap();
        web.loads.0.send(Load::Started("https://example.com/".into())).unwrap();
        assert!(web.pump(None), "leaving redraws at once");
        assert!(web.arrived, "it arrived first, so the entry is reopened");
        assert!(web.ready.is_none());
    }

    #[test]
    fn a_cookie_the_daemon_forgot_is_replaced_without_reloading() {
        let page = "http://127.0.0.1:48200/";
        let mut web = Web::new();
        web.cookie = Some(session("2026-11-26T22:00:00Z"));
        assert!(web.check_due(None).is_none(), "nothing to check before a page is chosen");
        web.loaded = Some(page.into());
        web.ready = Some(page.into());
        web.phase = Phase::Shown;
        let (checked, cookie) = web.check_due(Some(page)).unwrap();
        assert_eq!(checked, page);
        assert!(web.check_due(Some(page)).is_none(), "one check at a time");
        web.checked("someone-else", Ok(false));
        assert!(web.cookie.is_some(), "an answer about an older cookie changes nothing");
        web.checked_at = None;
        web.check_due(Some(page)).unwrap();
        web.checked(&cookie.value, Ok(false));
        assert!(web.cookie.is_none());
        assert!(web.sign_in_due(now()));
        assert_eq!(web.loaded.as_deref(), Some(page), "a page that already loaded stays");
        assert_eq!(web.phase, Phase::Shown);
        web.checked_at = None;
        assert!(web.check_due(Some(page)).is_none(), "no check while signing in");
    }

    #[test]
    fn a_new_page_opens_only_with_a_cookie_its_port_accepts() {
        let (a, b) = ("http://127.0.0.1:48200/", "http://127.0.0.1:48300/");
        let mut web = Web::new();
        web.sign_in_due(now());
        web.signed(Ok(session("2026-11-26T22:00:00Z")));
        assert!(web.check_due(Some(a)).is_none(), "a cookie just minted needs no check");
        web.loaded = Some(a.into());
        web.ready = Some(a.into());
        web.phase = Phase::Shown;
        web.checked_at = Some(Instant::now());
        web.verified = None;
        let (port, cookie) = web.check_due(Some(b)).expect("opening another page checks first, whatever the timer says");
        assert_eq!(port, b);
        web.loaded = Some(b.into());
        web.phase = Phase::Loading;
        web.checked(&cookie.value, Ok(false));
        assert!(web.loaded.is_none(), "a page still loading opens again after the new sign-in");
        assert!(web.sign_in_due(now()));
        web.signed(Ok(session("2026-11-26T23:00:00Z")));
        assert_eq!(web.verified.as_deref(), Some("v"));
    }

    #[test]
    fn the_slow_note_redraws_when_it_appears() {
        let mut web = Web::new();
        web.loaded = Some("http://127.0.0.1:48200/".into());
        web.loading_since = Some(Instant::now() - SLOW);
        assert!(web.pump(None));
        assert!(!web.pump(None));
    }

    #[test]
    fn a_page_that_leaves_for_another_site_is_sent_back() {
        let page = "http://127.0.0.1:48200/";
        let mut web = Web::new();
        web.loaded = Some(page.into());
        web.phase = Phase::Shown;
        web.loads.0.send(Load::Started("https://example.com/".into())).unwrap();
        web.loads.0.send(Load::Finished("https://example.com/".into())).unwrap();
        web.pump(None);
        assert_eq!(web.left.as_deref(), Some("https://example.com/"));
        assert_eq!(web.phase, Phase::Shown);
        assert!(web.ready.is_none(), "another site's finish never marks the page ready");
        web.left = None;
        web.loads.0.send(Load::Started("about:blank".into())).unwrap();
        web.loads.0.send(Load::Started("http://127.0.0.1:48300/".into())).unwrap();
        web.pump(None);
        assert_eq!(web.left.as_deref(), Some("http://127.0.0.1:48300/"));
    }
}
