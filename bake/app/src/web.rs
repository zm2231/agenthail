use std::collections::HashMap;
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::extensions::WebSession;
use rune::NativeView;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

const RENEW_BEFORE: time::Duration = time::Duration::hours(1);
const RENEW_RETRY: Duration = Duration::from_secs(60);
const CHECK_RETRY: Duration = Duration::from_secs(60);
const SLOW: Duration = Duration::from_secs(10);
const KEPT: usize = 6;
const UNREACHED: Duration = Duration::from_secs(20);

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

struct Page {
    url: String,
    slot: Option<NativeView>,
    #[cfg(target_os = "macos")]
    view: Option<wry::WebView>,
    phase: Phase,
    cookie_set: Option<String>,
    opened: bool,
    ready: bool,
    committed: bool,
    arrived: bool,
    loading_since: Option<Instant>,
    slow_shown: bool,
    left: Option<String>,
    loads: (Sender<Load>, Receiver<Load>),
    follow: (Sender<String>, Receiver<String>),
}

impl Page {
    fn new(url: &str) -> Page {
        Page {
            url: url.to_string(),
            slot: None,
            #[cfg(target_os = "macos")]
            view: None,
            phase: Phase::Loading,
            cookie_set: None,
            opened: false,
            ready: false,
            committed: false,
            arrived: false,
            loading_since: None,
            slow_shown: false,
            left: None,
            loads: channel(),
            follow: channel(),
        }
    }

    fn slow(&self) -> bool {
        self.loading_since.is_some_and(|at| at.elapsed() >= SLOW)
    }

    fn reopen(&mut self) {
        self.opened = false;
        self.ready = false;
        self.committed = false;
        self.arrived = false;
        self.loading_since = None;
        self.left = None;
        self.phase = Phase::Loading;
    }

    fn take_loads(&mut self) -> bool {
        let mut changed = false;
        while let Ok(load) = self.loads.1.try_recv() {
            if !self.opened {
                continue;
            }
            let home = |url: &str| feed::extensions::same_origin(url, &self.url);
            match load {
                Load::Started(url) if !self.arrived && !home(&url) => {}
                Load::Started(url) if leaves(&url, &self.url) => {
                    self.left = Some(url);
                    self.committed = false;
                    self.ready = false;
                    changed = true;
                }
                Load::Started(_) => {
                    self.committed = true;
                    self.arrived = true;
                    self.loading_since = Some(Instant::now());
                }
                Load::Finished(url) if self.committed && self.left.is_none() && home(&url) => {
                    self.loading_since = None;
                    if self.phase == Phase::Loading {
                        self.phase = Phase::Shown;
                        self.ready = true;
                    }
                    changed = true;
                }
                Load::Finished(_) => {}
            }
        }
        if self.opened && !self.arrived && self.phase == Phase::Loading && self.loading_since.is_some_and(|at| at.elapsed() >= UNREACHED) {
            self.phase = Phase::Failed(format!("Nothing came back from the page in {} seconds. Check that it is serving, then Reload.", UNREACHED.as_secs()));
            self.loading_since = None;
            changed = true;
        }
        if self.slow() != self.slow_shown {
            self.slow_shown = self.slow();
            changed = true;
        }
        changed
    }

    #[cfg(target_os = "macos")]
    fn show(&mut self, cookie: &WebSession, may_open: bool) -> Result<bool, String> {
        if self.view.is_none() {
            let Some(slot) = self.slot.as_ref().filter(|s| s.in_window()) else { return Ok(false) };
            if !may_open {
                return Ok(false);
            }
            self.view = Some(build(slot, &self.url, self.loads.0.clone(), self.follow.0.clone())?);
        }
        let Some(view) = &self.view else { return Ok(false) };
        if self.cookie_set.as_deref() != Some(cookie.value.as_str()) {
            view.set_cookie(&web_cookie(cookie, &self.url)?).map_err(|e| format!("Couldn't hand the page its sign-in: {e}"))?;
            self.cookie_set = Some(cookie.value.clone());
        }
        if let Some(url) = self.left.take() {
            crate::open_link(&url);
            view.load_url(&self.url).map_err(|e| format!("Couldn't open {}: {e}", self.url))?;
            self.ready = false;
            self.committed = false;
            self.arrived = false;
            self.phase = Phase::Loading;
            self.loading_since = Some(Instant::now());
        }
        while let Ok(url) = self.follow.1.try_recv() {
            if self.opened {
                view.load_url(&url).map_err(|e| format!("Couldn't open {url}: {e}"))?;
            }
        }
        if !self.opened {
            if !may_open {
                return Ok(false);
            }
            view.load_url(&self.url).map_err(|e| format!("Couldn't open {}: {e}", self.url))?;
            self.opened = true;
            self.ready = false;
            self.committed = false;
            self.arrived = false;
            self.loading_since = Some(Instant::now());
            self.phase = Phase::Loading;
            return Ok(true);
        }
        Ok(false)
    }

    #[cfg(not(target_os = "macos"))]
    fn show(&mut self, _cookie: &WebSession, may_open: bool) -> Result<bool, String> {
        let _ = (&self.loads.0, &self.follow.0, &self.cookie_set, &self.left);
        if may_open { Err("Pages open in the Mac app.".into()) } else { Ok(false) }
    }
}

pub struct Web {
    pages: HashMap<String, Page>,
    recent: Vec<String>,
    current: Option<String>,
    failure: Option<String>,
    cookie: Option<WebSession>,
    signing: bool,
    renew_at: Option<Instant>,
    checking: bool,
    opening: bool,
    checked_at: Option<Instant>,
    unclear: bool,
    verified: Option<(String, Option<String>)>,
    remint: bool,
}

impl Web {
    pub fn new() -> Web {
        Web { pages: HashMap::new(), recent: Vec::new(), current: None, failure: None, cookie: None, signing: false, renew_at: None, checking: false, opening: false, checked_at: None, unclear: false, verified: None, remint: false }
    }

    fn page(&self) -> Option<&Page> {
        self.current.as_ref().and_then(|url| self.pages.get(url))
    }

    pub fn phase(&self) -> Phase {
        if let Some(failure) = &self.failure {
            return Phase::Failed(failure.clone());
        }
        match self.page() {
            Some(page) if page.opened || matches!(page.phase, Phase::Failed(_)) => page.phase.clone(),
            _ if self.cookie.is_none() || self.signing => Phase::SigningIn,
            _ => Phase::Loading,
        }
    }

    pub fn slot(&self) -> Option<&NativeView> {
        self.page().and_then(|p| p.slot.as_ref())
    }

    pub fn slow(&self) -> bool {
        self.page().is_some_and(Page::slow)
    }

    pub fn shows(&self, want: &str) -> bool {
        let Some(page) = self.pages.get(want) else { return false };
        #[cfg(target_os = "macos")]
        if page.view.is_some() {
            return page.ready;
        }
        let _ = page;
        true
    }

    fn failed(&self) -> bool {
        matches!(self.phase(), Phase::Failed(_))
    }

    pub fn sign_in_due(&mut self, now: OffsetDateTime) -> bool {
        let waiting = self.renew_at.is_some_and(|at| at > Instant::now());
        let due = self.failure.is_none() && !self.signing && !waiting && (self.remint || self.cookie.as_ref().is_none_or(|c| expiring(c, now)));
        self.signing |= due;
        due
    }

    pub fn signed(&mut self, result: Result<WebSession, String>, now: OffsetDateTime) {
        self.signing = false;
        match result {
            Ok(cookie) => {
                self.verified = Some((cookie.value.clone(), None));
                self.remint = false;
                self.checked_at = Some(Instant::now());
                self.cookie = Some(cookie);
                self.renew_at = None;
            }
            Err(_) if self.cookie.as_ref().is_some_and(|c| !expired(c, now)) => self.renew_at = Some(Instant::now() + RENEW_RETRY),
            Err(error) => self.failure = Some(format!("Couldn't sign in to the page: {error}")),
        }
    }

    fn verified_for(&self, value: &str, page: &str) -> bool {
        self.verified.as_ref().is_some_and(|(v, p)| v == value && p.as_deref().is_none_or(|p| p == page))
    }

    pub fn recheck(&mut self) {
        self.checked_at = None;
    }

    pub fn check_due(&mut self, want: Option<&str>) -> Option<(String, WebSession)> {
        let want = want?;
        if self.failed() || self.signing || self.checking {
            return None;
        }
        let cookie = self.cookie.clone()?;
        let opening = !self.pages.get(want).is_some_and(|p| p.opened) && !self.verified_for(&cookie.value, want);
        if self.checked_at.is_some_and(|at| if self.unclear { at.elapsed() < CHECK_RETRY } else { !opening }) {
            return None;
        }
        self.checking = true;
        self.opening = opening;
        self.checked_at = Some(Instant::now());
        Some((want.to_string(), cookie))
    }

    pub fn checked(&mut self, page: &str, value: &str, known: Result<bool, String>) {
        self.checking = false;
        if self.cookie.as_ref().is_none_or(|c| c.value != value) {
            return;
        }
        self.unclear = known.is_err();
        match known {
            Ok(true) => self.verified = Some((value.to_string(), Some(page.to_string()))),
            Ok(false) => {
                self.cookie = None;
                self.renew_at = None;
                self.verified = None;
                self.pages.values_mut().for_each(Page::reopen);
            }
            Err(_) if self.opening => self.remint = true,
            Err(_) => {}
        }
    }

    pub fn retry(&mut self, url: &str) {
        if self.failure.is_some() {
            self.reload();
        }
        if let Some(page) = self.pages.get_mut(url).filter(|p| matches!(p.phase, Phase::Failed(_))) {
            page.reopen();
        }
    }

    pub fn reload(&mut self) {
        self.failure = None;
        self.cookie = None;
        self.renew_at = None;
        self.verified = None;
        self.remint = false;
        if let Some(page) = self.current.as_ref().and_then(|url| self.pages.get_mut(url)) {
            page.reopen();
        }
    }

    pub fn pump(&mut self, want: Option<&str>) -> bool {
        let mut changed = false;
        for page in self.pages.values_mut() {
            changed |= page.take_loads();
        }
        self.current = want.map(str::to_string);
        let Some(want) = want else { return changed };
        self.recent.retain(|u| u != want);
        self.recent.push(want.to_string());
        if !self.pages.contains_key(want) {
            self.pages.insert(want.to_string(), Page::new(want));
            while self.recent.len() > KEPT {
                let old = self.recent.remove(0);
                self.pages.remove(&old);
            }
        }
        if self.failure.is_some() {
            return changed;
        }
        let cookie = self.cookie.clone();
        let may_open = cookie.as_ref().is_some_and(|c| !self.checking && self.verified_for(&c.value, want));
        let Some(page) = self.pages.get_mut(want) else { return changed };
        if matches!(page.phase, Phase::Failed(_)) {
            return changed;
        }
        if page.slot.is_none() {
            page.slot = Some(NativeView::new());
            return true;
        }
        let Some(cookie) = cookie else { return changed };
        let was_open = page.opened;
        match page.show(&cookie, may_open) {
            Ok(moved) => changed |= moved,
            Err(error) => {
                page.phase = Phase::Failed(error);
                changed = true;
            }
        }
        if !was_open && page.opened {
            self.verified = None;
        }
        changed
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
fn build(slot: &NativeView, page: &str, loads: Sender<Load>, follow: Sender<String>) -> Result<wry::WebView, String> {
    use feed::extensions::{Navigation, navigation};
    let (home, opener) = (page.to_string(), page.to_string());
    wry::WebViewBuilder::new()
        .with_initialization_script_for_main_only(OUTSIDE_LINKS, true)
        .with_navigation_handler(move |url| navigation(&url, &home) != Navigation::Refuse)
        .with_new_window_req_handler(move |url, _| {
            match navigation(&url, &opener) {
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
#[path = "web_tests.rs"]
mod tests;
