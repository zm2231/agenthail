use std::cell::RefCell;
use std::rc::Rc;
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::extensions::{Probe, WebSession};
use rune::NativeView;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

const RENEW_BEFORE: time::Duration = time::Duration::hours(1);

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
    probing: Option<String>,
    cleared: Option<String>,
    resigned: bool,
    loaded: Option<String>,
    page: Rc<RefCell<String>>,
    finished: (Sender<String>, Receiver<String>),
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
            page: Rc::new(RefCell::new(String::new())),
            finished: channel(),
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

    pub fn probe_due(&mut self, want: Option<&str>) -> Option<(String, WebSession)> {
        let want = want?;
        if self.failed() || self.probing.is_some() || self.cleared.as_deref() == Some(want) {
            return None;
        }
        let cookie = self.cookie.clone()?;
        self.probing = Some(want.to_string());
        if self.loaded.is_none() {
            self.phase = Phase::Loading;
        }
        Some((want.to_string(), cookie))
    }

    pub fn probed(&mut self, page: String, result: Probe) {
        if self.probing.as_deref() != Some(page.as_str()) {
            return;
        }
        self.probing = None;
        match result {
            Probe::Open => {
                self.cleared = Some(page);
                self.resigned = false;
            }
            Probe::SignIn if !self.resigned => {
                self.resigned = true;
                self.cookie = None;
            }
            Probe::SignIn => self.phase = Phase::Failed("The page turned down this app's sign-in. Reload to try again.".into()),
            Probe::Unreachable(error) => self.phase = Phase::Failed(format!("Couldn't reach the page: {error}")),
        }
    }

    pub fn reload(&mut self) {
        if self.failed() {
            self.phase = Phase::SigningIn;
            self.cleared = None;
            self.loaded = None;
            self.resigned = false;
            return;
        }
        #[cfg(target_os = "macos")]
        if let Some(view) = &self.view
            && view.reload().is_ok()
        {
            self.phase = Phase::Loading;
        }
    }

    pub fn pump(&mut self, want: Option<&str>) -> bool {
        let mut changed = false;
        while self.finished.1.try_recv().is_ok() {
            if self.phase == Phase::Loading {
                self.phase = Phase::Shown;
                changed = true;
            }
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
            self.view = Some(build(slot, self.page.clone(), self.finished.0.clone())?);
        }
        let Some(view) = &self.view else { return Ok(false) };
        if !self.cookie_set {
            view.set_cookie(&web_cookie(cookie, want)?).map_err(|e| format!("Couldn't hand the page its sign-in: {e}"))?;
            self.cookie_set = true;
        }
        if cleared && self.loaded.as_deref() != Some(want) {
            *self.page.borrow_mut() = want.to_string();
            view.load_url(want).map_err(|e| format!("Couldn't open {want}: {e}"))?;
            self.loaded = Some(want.to_string());
            self.phase = Phase::Loading;
            return Ok(true);
        }
        Ok(false)
    }

    #[cfg(not(target_os = "macos"))]
    fn show(&mut self, _want: &str, _cookie: &WebSession, cleared: bool, _in_window: bool) -> Result<bool, String> {
        let _ = (&self.page, &self.finished.0, &self.cookie_set, &self.loaded);
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
fn build(slot: &NativeView, page: Rc<RefCell<String>>, finished: Sender<String>) -> Result<wry::WebView, String> {
    use feed::extensions::{Navigation, navigation};
    wry::WebViewBuilder::new()
        .with_navigation_handler(move |url| match navigation(&url, &page.borrow()) {
            Navigation::Allow => true,
            Navigation::OpenOutside => {
                crate::open_link(&url);
                false
            }
            Navigation::Refuse => false,
        })
        .with_new_window_req_handler(|url, _| {
            crate::open_link(&url);
            wry::NewWindowResponse::Deny
        })
        .with_on_page_load_handler(move |event, url| {
            if matches!(event, wry::PageLoadEvent::Finished) {
                let _ = finished.send(url);
            }
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
        let (url, _) = web.probe_due(Some(page)).unwrap();
        web.probed(url, Probe::Open);
        assert!(web.probe_due(Some(page)).is_none());
        let other = "http://127.0.0.1:48300/";
        let (url, _) = web.probe_due(Some(other)).unwrap();
        web.probed(url, Probe::Unreachable("connection refused".into()));
        assert_eq!(web.phase, Phase::Failed("Couldn't reach the page: connection refused".into()));
    }

    #[test]
    fn renewing_the_cookie_keeps_the_open_page() {
        let page = "http://127.0.0.1:48200/";
        let mut web = Web::new();
        web.sign_in_due(now());
        web.signed(Ok(session("2026-11-26T22:00:00Z")));
        let (url, _) = web.probe_due(Some(page)).unwrap();
        web.probed(url, Probe::Open);
        web.loaded = Some(page.into());
        web.signed(Ok(session("2026-11-27T22:00:00Z")));
        assert_eq!(web.loaded.as_deref(), Some(page));
        assert!(web.probe_due(Some(page)).is_none(), "the same page is not checked again");
    }
}
