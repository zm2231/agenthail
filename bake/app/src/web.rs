use std::cell::RefCell;
use std::rc::Rc;
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::extensions::{Probe, WebSession};
use rune::NativeView;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

const RENEW_BEFORE: time::Duration = time::Duration::hours(1);
const LOAD_LIMIT: Duration = Duration::from_secs(20);

#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
enum Load {
    Started,
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
    probing: Option<(String, String)>,
    cleared: Option<String>,
    resigned: bool,
    loaded: Option<String>,
    ready: Option<String>,
    recheck: Option<String>,
    loading_since: Option<Instant>,
    want: Option<String>,
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
            probing: None,
            cleared: None,
            resigned: false,
            loaded: None,
            ready: None,
            recheck: None,
            loading_since: None,
            want: None,
            page: Rc::new(RefCell::new(String::new())),
            loads: channel(),
            follow: channel(),
        }
    }

    fn failed(&self) -> bool {
        matches!(self.phase, Phase::Failed(_))
    }

    pub fn sign_in_due(&mut self, now: OffsetDateTime) -> bool {
        let due = !self.failed() && !self.signing && self.cookie.as_ref().is_none_or(|c| expiring(c, now));
        self.signing |= due;
        due
    }

    pub fn signed(&mut self, result: Result<WebSession, String>) {
        self.signing = false;
        match result {
            Ok(cookie) => {
                self.cookie = Some(cookie);
                self.cookie_set = false;
            }
            Err(error) => self.phase = Phase::Failed(format!("Couldn't sign in to the page: {error}")),
        }
    }

    pub fn select(&mut self, want: Option<&str>) {
        if self.want.as_deref() != want {
            self.resigned = false;
        }
        self.want = want.map(str::to_string);
    }

    pub fn probe_due(&mut self, want: Option<&str>) -> Option<(String, WebSession)> {
        let want = want?;
        self.select(Some(want));
        if self.failed() || self.probing.is_some() {
            return None;
        }
        let cookie = self.cookie.clone()?;
        if self.cleared.as_deref() == Some(want) {
            let shown = self.recheck.take().filter(|_| self.ready.as_deref() == Some(want))?;
            self.probing = Some((shown.clone(), want.to_string()));
            return Some((shown, cookie));
        }
        self.probing = Some((want.to_string(), want.to_string()));
        if self.loaded.is_none() {
            self.phase = Phase::Loading;
        }
        Some((want.to_string(), cookie))
    }

    pub fn probed(&mut self, page: String, result: Probe) {
        let Some((url, entry)) = self.probing.take_if(|(url, _)| *url == page) else { return };
        if self.want.as_deref() != Some(entry.as_str()) {
            return;
        }
        match result {
            Probe::Open if url == entry => self.cleared = Some(entry),
            Probe::Open => {}
            Probe::SignIn if !self.resigned => {
                self.resigned = true;
                self.cookie = None;
                self.cleared = None;
                self.loaded = None;
                self.ready = None;
            }
            Probe::SignIn => self.phase = Phase::Failed("The page turned down this app's sign-in. Reload to try again.".into()),
            Probe::Unreachable(error) => self.phase = Phase::Failed(format!("Couldn't reach the page: {error}")),
        }
    }

    pub fn reload(&mut self) {
        self.phase = if self.cookie.is_some() { Phase::Loading } else { Phase::SigningIn };
        self.cleared = None;
        self.loaded = None;
        self.ready = None;
        self.probing = None;
        self.recheck = None;
        self.loading_since = None;
        self.resigned = false;
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
            if self.loaded.is_none() {
                continue;
            }
            match load {
                Load::Started => self.loading_since = Some(Instant::now()),
                Load::Finished(url) => {
                    self.loading_since = None;
                    if self.phase == Phase::Loading {
                        self.phase = Phase::Shown;
                        self.ready = self.loaded.clone();
                        changed = true;
                    }
                    self.recheck = Some(url);
                }
            }
        }
        if !self.failed() && self.loading_since.is_some_and(|at| at.elapsed() >= LOAD_LIMIT) {
            self.phase = Phase::Failed(format!("The page didn't finish loading in {} seconds.", LOAD_LIMIT.as_secs()));
            self.loading_since = None;
            changed = true;
        }
        self.want = want.map(str::to_string);
        let Some(want) = want else { return changed };
        if self.failed() {
            return changed;
        }
        if self.slot.is_none() {
            self.slot = Some(NativeView::new());
            return true;
        }
        let Some(cookie) = self.cookie.clone() else { return changed };
        let cleared = self.cleared.as_deref() == Some(want);
        let in_window = self.slot.as_ref().is_some_and(NativeView::in_window);
        match self.show(want, &cookie, cleared, in_window) {
            Ok(moved) => changed |= moved,
            Err(error) => {
                self.phase = Phase::Failed(error);
                changed = true;
            }
        }
        changed
    }

    #[cfg(target_os = "macos")]
    fn show(&mut self, want: &str, cookie: &WebSession, cleared: bool, in_window: bool) -> Result<bool, String> {
        if self.view.is_none() {
            if !in_window || !cleared {
                return Ok(false);
            }
            let Some(slot) = &self.slot else { return Ok(false) };
            self.view = Some(build(slot, self.page.clone(), self.loads.0.clone(), self.follow.0.clone())?);
        }
        let Some(view) = &self.view else { return Ok(false) };
        while let Ok(url) = self.follow.1.try_recv() {
            if self.loaded.as_deref() == Some(want) {
                view.load_url(&url).map_err(|e| format!("Couldn't open {url}: {e}"))?;
            }
        }
        if !self.cookie_set {
            view.set_cookie(&web_cookie(cookie, want)?).map_err(|e| format!("Couldn't hand the page its sign-in: {e}"))?;
            self.cookie_set = true;
        }
        if cleared && self.loaded.as_deref() != Some(want) {
            *self.page.borrow_mut() = want.to_string();
            view.load_url(want).map_err(|e| format!("Couldn't open {want}: {e}"))?;
            self.loaded = Some(want.to_string());
            self.ready = None;
            self.loading_since = Some(Instant::now());
            self.phase = Phase::Loading;
            return Ok(true);
        }
        Ok(false)
    }

    #[cfg(not(target_os = "macos"))]
    fn show(&mut self, _want: &str, _cookie: &WebSession, cleared: bool, _in_window: bool) -> Result<bool, String> {
        let _ = (&self.page, &self.loads.0, &self.follow.0, &self.cookie_set, &self.loaded, &self.loading_since);
        if cleared { Err("Pages open in the Mac app.".into()) } else { Ok(false) }
    }
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
fn build(slot: &NativeView, page: Rc<RefCell<String>>, loads: Sender<Load>, follow: Sender<String>) -> Result<wry::WebView, String> {
    use feed::extensions::{Navigation, navigation};
    let opener = page.clone();
    wry::WebViewBuilder::new()
        .with_navigation_handler(move |url| match navigation(&url, &page.borrow()) {
            Navigation::Allow => true,
            Navigation::OpenOutside => {
                crate::open_link(&url);
                false
            }
            Navigation::Refuse => false,
        })
        .with_new_window_req_handler(move |url, _| {
            match navigation(&url, &opener.borrow()) {
                Navigation::Allow => {
                    let _ = follow.send(url);
                }
                Navigation::OpenOutside => crate::open_link(&url),
                Navigation::Refuse => {}
            }
            wry::NewWindowResponse::Deny
        })
        .with_on_page_load_handler(move |event, url| {
            let _ = loads.send(match event {
                wry::PageLoadEvent::Started => Load::Started,
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
        assert!(matches!(web.phase, Phase::Failed(_)));
        assert!(!web.sign_in_due(now()), "no retry loop after a failure");
        web.reload();
        assert_eq!(web.phase, Phase::SigningIn);
        assert!(web.sign_in_due(now()));
    }

    #[test]
    fn a_refused_cookie_is_replaced_once_then_reported() {
        let page = "http://127.0.0.1:48200/";
        let mut web = Web::new();
        assert!(web.sign_in_due(now()));
        web.signed(Ok(session("2026-11-26T22:00:00Z")));
        web.want = Some(page.into());
        let (url, _) = web.probe_due(Some(page)).unwrap();
        assert!(web.probe_due(Some(page)).is_none(), "one probe at a time");
        web.probed(url, Probe::SignIn);
        assert!(web.sign_in_due(now()), "a refused cookie is minted again");
        web.signed(Ok(session("2026-11-26T22:00:00Z")));
        let (url, _) = web.probe_due(Some(page)).unwrap();
        web.probed(url, Probe::SignIn);
        assert!(matches!(web.phase, Phase::Failed(_)));
        assert!(web.probe_due(Some(page)).is_none());
    }

    #[test]
    fn an_open_page_is_probed_once_and_an_unreachable_one_fails() {
        let page = "http://127.0.0.1:48200/";
        let mut web = Web::new();
        web.sign_in_due(now());
        web.signed(Ok(session("2026-11-26T22:00:00Z")));
        web.want = Some(page.into());
        let (url, _) = web.probe_due(Some(page)).unwrap();
        web.probed(url, Probe::Open);
        assert!(web.probe_due(Some(page)).is_none());
        let other = "http://127.0.0.1:48300/";
        web.want = Some(other.into());
        let (url, _) = web.probe_due(Some(other)).unwrap();
        web.probed(url, Probe::Unreachable("connection refused".into()));
        assert_eq!(web.phase, Phase::Failed("Couldn't reach the page: connection refused".into()));
    }

    #[test]
    fn a_probe_for_a_page_left_behind_changes_nothing() {
        let (a, b) = ("http://127.0.0.1:48200/", "http://127.0.0.1:48300/");
        let mut web = Web::new();
        web.sign_in_due(now());
        web.signed(Ok(session("2026-11-26T22:00:00Z")));
        web.want = Some(a.into());
        let (url, _) = web.probe_due(Some(a)).unwrap();
        web.want = Some(b.into());
        web.probed(url, Probe::Unreachable("connection refused".into()));
        assert!(!web.failed());
        assert!(web.probe_due(Some(b)).is_some());
    }

    #[test]
    fn reload_checks_the_page_again() {
        let page = "http://127.0.0.1:48200/";
        let mut web = Web::new();
        web.sign_in_due(now());
        web.signed(Ok(session("2026-11-26T22:00:00Z")));
        web.want = Some(page.into());
        let (url, _) = web.probe_due(Some(page)).unwrap();
        web.probed(url, Probe::Open);
        web.loaded = Some(page.into());
        web.reload();
        assert_eq!(web.phase, Phase::Loading);
        assert!(web.probe_due(Some(page)).is_some());
    }

    #[test]
    fn a_loaded_page_is_checked_again_and_a_refusal_signs_in_and_reloads_once() {
        let page = "http://127.0.0.1:48200/";
        let mut web = Web::new();
        web.sign_in_due(now());
        web.signed(Ok(session("2026-11-26T22:00:00Z")));
        let (url, _) = web.probe_due(Some(page)).unwrap();
        web.probed(url, Probe::Open);
        web.loaded = Some(page.into());
        web.phase = Phase::Loading;
        let landed = "http://127.0.0.1:48200/login";
        web.loads.0.send(Load::Started).unwrap();
        web.loads.0.send(Load::Finished(landed.into())).unwrap();
        web.pump(None);
        assert_eq!(web.phase, Phase::Shown);
        assert!(web.shows(page));
        let (url, _) = web.probe_due(Some(page)).expect("a finished load is checked");
        assert_eq!(url, landed, "the page that loaded is checked, after any redirect");
        assert!(web.probe_due(Some(page)).is_none());
        web.probed(url, Probe::SignIn);
        assert_eq!(web.loaded, None, "the page loads again after a fresh sign-in");
        assert!(web.sign_in_due(now()));
    }

    #[test]
    fn a_navigation_inside_a_shown_page_is_watched_too() {
        let mut web = Web::new();
        web.phase = Phase::Shown;
        web.loaded = Some("http://127.0.0.1:48200/".into());
        web.loads.0.send(Load::Started).unwrap();
        web.pump(None);
        web.loading_since = Some(Instant::now() - LOAD_LIMIT);
        web.pump(None);
        assert!(web.failed());
    }

    #[test]
    fn reload_ignores_a_check_already_on_its_way() {
        let page = "http://127.0.0.1:48200/";
        let mut web = Web::new();
        web.sign_in_due(now());
        web.signed(Ok(session("2026-11-26T22:00:00Z")));
        let (url, _) = web.probe_due(Some(page)).unwrap();
        web.reload();
        web.probed(url, Probe::Unreachable("old".into()));
        assert!(!web.failed());
        assert!(web.probe_due(Some(page)).is_some());
    }

    #[test]
    fn each_page_gets_its_own_fresh_sign_in() {
        let (a, b) = ("http://127.0.0.1:48200/", "http://127.0.0.1:48300/");
        let mut web = Web::new();
        web.sign_in_due(now());
        web.signed(Ok(session("2026-11-26T22:00:00Z")));
        let (url, _) = web.probe_due(Some(a)).unwrap();
        web.probed(url, Probe::SignIn);
        web.sign_in_due(now());
        web.signed(Ok(session("2026-11-26T22:00:00Z")));
        web.select(Some(b));
        let (url, _) = web.probe_due(Some(b)).unwrap();
        web.probed(url, Probe::SignIn);
        assert!(!web.failed(), "b gets its own retry");
    }

    #[test]
    fn a_page_that_never_finishes_fails_with_reload() {
        let mut web = Web::new();
        web.phase = Phase::Loading;
        web.loaded = Some("http://127.0.0.1:48200/".into());
        web.loading_since = Some(Instant::now() - LOAD_LIMIT);
        web.pump(None);
        assert_eq!(web.phase, Phase::Failed("The page didn't finish loading in 20 seconds.".into()));
    }

    #[test]
    fn renewing_the_cookie_keeps_the_open_page() {
        let page = "http://127.0.0.1:48200/";
        let mut web = Web::new();
        web.sign_in_due(now());
        web.signed(Ok(session("2026-11-26T22:00:00Z")));
        web.want = Some(page.into());
        let (url, _) = web.probe_due(Some(page)).unwrap();
        web.probed(url, Probe::Open);
        web.loaded = Some(page.into());
        web.signed(Ok(session("2026-11-27T22:00:00Z")));
        assert_eq!(web.loaded.as_deref(), Some(page));
        assert!(web.probe_due(Some(page)).is_none(), "the same page is not checked again");
    }
}
