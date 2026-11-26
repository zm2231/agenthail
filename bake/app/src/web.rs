use std::cell::RefCell;
use std::rc::Rc;
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::extensions::WebSession;
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
    pub signing: bool,
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
            loaded: None,
            page: Rc::new(RefCell::new(String::new())),
            finished: channel(),
        }
    }

    pub fn needs_sign_in(&self, now: OffsetDateTime) -> bool {
        !self.signing && self.cookie.as_ref().is_none_or(|c| expiring(c, now))
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

    pub fn reload(&mut self) {
        if matches!(self.phase, Phase::Failed(_)) {
            self.phase = Phase::SigningIn;
            self.loaded = None;
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
        while let Ok(url) = self.finished.1.try_recv() {
            if self.loaded.as_deref() == Some(url.as_str()) || self.phase == Phase::Loading {
                self.phase = Phase::Shown;
                changed = true;
            }
        }
        let Some(want) = want else { return changed };
        if matches!(self.phase, Phase::Failed(_)) {
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
            self.view = Some(build(slot, self.page.clone(), self.finished.0.clone())?);
        }
        let Some(view) = &self.view else { return Ok(false) };
        let mut changed = false;
        if !self.cookie_set {
            view.set_cookie(&web_cookie(cookie, want)?).map_err(|e| format!("Couldn't hand the page its sign-in: {e}"))?;
            self.cookie_set = true;
            self.loaded = None;
        }
        if self.loaded.as_deref() != Some(want) {
            *self.page.borrow_mut() = want.to_string();
            view.load_url(want).map_err(|e| format!("Couldn't open {want}: {e}"))?;
            self.loaded = Some(want.to_string());
            self.phase = Phase::Loading;
            changed = true;
        }
        Ok(changed)
    }

    #[cfg(not(target_os = "macos"))]
    fn show(&mut self, _want: &str, _cookie: &WebSession, _in_window: bool) -> Result<bool, String> {
        let _ = (&self.page, &self.finished.0, &self.cookie_set, &self.loaded);
        Err("Pages open in the Mac app.".into())
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

    #[test]
    fn a_web_session_is_renewed_an_hour_before_it_ends() {
        let now = OffsetDateTime::parse("2026-11-26T10:00:00Z", &Rfc3339).unwrap();
        assert!(!expiring(&session("2026-11-26T12:00:00Z"), now));
        assert!(expiring(&session("2026-11-26T10:30:00Z"), now));
        assert!(expiring(&session("soon"), now));
    }
}
