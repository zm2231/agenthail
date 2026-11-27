use super::*;

const A: &str = "http://127.0.0.1:48200/";
const B: &str = "http://127.0.0.1:48300/";

fn session(expires_at: &str) -> WebSession {
    WebSession { name: "agenthail_web".into(), value: "v".into(), path: "/".into(), expires_at: expires_at.into(), scopes: vec![] }
}

fn now() -> OffsetDateTime {
    OffsetDateTime::parse("2026-11-26T10:00:00Z", &Rfc3339).unwrap()
}

fn signed_in() -> Web {
    let mut web = Web::new();
    assert!(web.sign_in_due(now()));
    web.signed(Ok(session("2026-11-26T22:00:00Z")), now());
    web
}

fn open(web: &mut Web, url: &str, shown: bool) {
    let mut page = Page::new(url);
    page.opened = true;
    page.arrived = shown;
    page.ready = shown;
    page.phase = if shown { Phase::Shown } else { Phase::Loading };
    web.pages.insert(url.into(), page);
    web.current = Some(url.into());
}

fn send(web: &Web, url: &str, load: Load) {
    web.pages[url].loads.0.send(load).unwrap();
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
    web.signed(Err("refused".into()), now());
    assert!(web.failed());
    assert!(!web.sign_in_due(now()), "no retry loop after a failure");
    web.reload();
    assert_eq!(web.phase(), Phase::SigningIn);
    assert!(web.sign_in_due(now()));
}

#[test]
fn reload_signs_in_afresh_and_opens_the_page_again() {
    let mut web = signed_in();
    open(&mut web, A, true);
    web.reload();
    assert!(web.cookie.is_none());
    assert!(!web.pages[A].opened && !web.pages[A].ready);
    assert!(web.sign_in_due(now()));
}

#[test]
fn a_failed_renewal_keeps_a_page_whose_cookie_still_works() {
    let mut web = Web::new();
    open(&mut web, A, true);
    let soon = (now() + time::Duration::minutes(30)).format(&Rfc3339).unwrap();
    web.cookie = Some(session(&soon));
    assert!(web.sign_in_due(now()));
    web.signed(Err("daemon restarting".into()), now());
    assert_eq!(web.phase(), Phase::Shown);
    assert!(!web.sign_in_due(now()), "waits a minute before trying again");
    web.renew_at = Some(Instant::now());
    assert!(web.sign_in_due(now()));
    web.cookie = Some(session("2026-11-26T09:00:00Z"));
    web.signed(Err("daemon down".into()), now());
    assert!(web.failed(), "an expired cookie can't keep the page");
}

#[test]
fn renewing_the_cookie_keeps_the_open_page() {
    let mut web = signed_in();
    open(&mut web, A, true);
    web.pages.get_mut(A).unwrap().cookie_set = Some("v".into());
    web.signed(Ok(WebSession { value: "v2".into(), ..session("2026-11-27T22:00:00Z") }), now());
    assert!(web.pages[A].opened && web.pages[A].ready);
    assert_ne!(web.pages[A].cookie_set.as_deref(), Some("v2"), "the new cookie is handed over on the next pump, without loading again");
}

#[test]
fn a_page_shows_once_its_own_load_finishes_and_a_stall_is_slow() {
    let mut web = Web::new();
    open(&mut web, A, false);
    web.pages.get_mut(A).unwrap().loading_since = Some(Instant::now() - SLOW);
    assert!(web.slow());
    send(&web, A, Load::Finished(A.into()));
    web.pump(None);
    assert_eq!(web.pages[A].phase, Phase::Loading, "a finish before this load commits belongs to an earlier one");
    send(&web, A, Load::Started(format!("{A}report")));
    web.pump(None);
    assert!(!web.pages[A].slow(), "a commit restarts the clock");
    send(&web, A, Load::Finished(format!("{A}report")));
    web.pump(None);
    assert_eq!(web.pages[A].phase, Phase::Shown);
    assert!(web.pages[A].ready && web.pages[A].loading_since.is_none());
}

#[test]
fn one_pages_events_never_touch_another() {
    let mut web = Web::new();
    open(&mut web, A, false);
    open(&mut web, B, false);
    send(&web, B, Load::Started(B.into()));
    send(&web, A, Load::Started(A.into()));
    send(&web, A, Load::Finished(A.into()));
    web.pump(None);
    assert!(web.pages[A].ready);
    assert!(!web.pages[B].ready, "a's finish can't show b");
    assert!(web.pages[B].left.is_none());
}

#[test]
fn leaving_before_and_after_the_page_arrives() {
    let mut web = Web::new();
    open(&mut web, A, false);
    send(&web, A, Load::Started("https://example.com/".into()));
    web.pump(None);
    assert!(web.pages[A].left.is_none(), "before it arrives, an off-site commit isn't treated as leaving");
    send(&web, A, Load::Started(A.into()));
    send(&web, A, Load::Started("https://example.com/".into()));
    assert!(web.pump(None), "leaving redraws at once");
    assert_eq!(web.pages[A].left.as_deref(), Some("https://example.com/"));
    assert!(!web.pages[A].ready);
}

#[test]
fn a_page_on_another_site_never_marks_this_one_ready() {
    let mut web = Web::new();
    open(&mut web, A, true);
    send(&web, A, Load::Started("https://example.com/".into()));
    send(&web, A, Load::Finished("https://example.com/".into()));
    web.pump(None);
    assert_eq!(web.pages[A].left.as_deref(), Some("https://example.com/"));
    assert!(!web.pages[A].ready);
}

#[test]
fn each_page_opens_only_with_a_cookie_its_port_has_just_accepted() {
    let mut web = signed_in();
    assert!(web.check_due(Some(A)).is_none(), "a cookie just minted needs no check");
    web.verified = None;
    open(&mut web, A, true);
    let (port, cookie) = web.check_due(Some(B)).expect("opening another page checks first, whatever the timer says");
    assert_eq!(port, B);
    assert!(web.check_due(Some(B)).is_none(), "one check at a time");
    web.checked(A, &cookie.value, Ok(true));
    assert!(!web.verified_for("v", B), "a check answers only for its own page");
    web.checking = false;
    web.check_due(Some(B)).unwrap();
    web.checked(B, &cookie.value, Ok(true));
    assert!(web.verified_for("v", B));
}

#[test]
fn an_unclear_check_signs_in_afresh_only_for_a_page_waiting_to_open() {
    let mut web = signed_in();
    web.verified = None;
    open(&mut web, A, true);
    let (_, cookie) = web.check_due(Some(B)).unwrap();
    web.checked(B, &cookie.value, Err("the page's port answered 503".into()));
    assert!(web.cookie.is_some(), "the open page keeps its cookie meanwhile");
    assert!(web.sign_in_due(now()), "a fresh sign-in stands in for the unclear check");
    web.signed(Err("daemon down".into()), now());
    assert_eq!(web.pages[A].phase, Phase::Shown, "a failed sign-in with a working cookie keeps the page");
    web.renew_at = None;
    web.remint = false;
    web.checked_at = None;
    web.check_due(Some(A)).unwrap();
    web.checked(A, &cookie.value, Err("connection refused".into()));
    assert!(!web.remint, "an unclear routine check on an open page changes nothing");
}

#[test]
fn a_forgotten_cookie_signs_in_again_and_reopens_every_page() {
    let mut web = signed_in();
    open(&mut web, B, true);
    open(&mut web, A, true);
    web.checked_at = None;
    let (_, cookie) = web.check_due(Some(A)).unwrap();
    web.checked(A, "someone-else", Ok(false));
    assert!(web.cookie.is_some(), "an answer about an older cookie changes nothing");
    web.checked_at = None;
    web.check_due(Some(A)).unwrap();
    web.checked(A, &cookie.value, Ok(false));
    assert!(web.cookie.is_none());
    assert!(!web.pages[A].opened && !web.pages[B].opened);
    assert_eq!(web.phase(), Phase::SigningIn);
    assert!(web.check_due(Some(A)).is_none(), "no check while there is no cookie");
    assert!(web.sign_in_due(now()));
    web.signed(Ok(session("2026-11-26T23:00:00Z")), now());
    assert!(web.verified_for("v", A), "the fresh cookie opens the page");
}

#[test]
fn the_slow_note_redraws_when_it_appears() {
    let mut web = Web::new();
    open(&mut web, A, false);
    web.pages.get_mut(A).unwrap().loading_since = Some(Instant::now() - SLOW);
    assert!(web.pump(None));
    assert!(!web.pump(None));
}

#[test]
fn only_the_most_recent_pages_are_kept() {
    let mut web = Web::new();
    for port in 0..KEPT + 2 {
        web.pump(Some(&format!("http://127.0.0.1:{}/", 48000 + port)));
    }
    assert_eq!(web.pages.len(), KEPT);
    assert!(!web.pages.contains_key("http://127.0.0.1:48000/"));
    assert!(web.pages.contains_key(&format!("http://127.0.0.1:{}/", 48000 + KEPT + 1)));
}

#[test]
fn a_failed_page_is_tried_again_only_when_opened_again() {
    let mut web = signed_in();
    open(&mut web, A, true);
    web.pages.get_mut(A).unwrap().phase = Phase::Failed("no web view".into());
    web.pump(Some(A));
    assert!(matches!(web.phase(), Phase::Failed(_)), "it stays failed on its own");
    assert!(!web.sign_in_due(now()));
    web.retry(B);
    assert!(matches!(web.pages[A].phase, Phase::Failed(_)));
    web.retry(A);
    assert_eq!(web.pages[A].phase, Phase::Loading);
    assert!(web.cookie.is_some(), "a page failure needs no new sign-in");
}

#[test]
fn a_page_that_never_answers_fails_but_one_that_arrived_only_turns_slow() {
    let mut web = Web::new();
    open(&mut web, A, false);
    web.pages.get_mut(A).unwrap().loading_since = Some(Instant::now() - UNREACHED);
    assert!(web.pump(None));
    assert!(matches!(web.pages[A].phase, Phase::Failed(_)));
    open(&mut web, B, false);
    web.pages.get_mut(B).unwrap().arrived = true;
    web.pages.get_mut(B).unwrap().loading_since = Some(Instant::now() - UNREACHED);
    web.pump(None);
    assert_eq!(web.pages[B].phase, Phase::Loading);
    assert!(web.pages[B].slow());
}

#[test]
fn an_open_page_checks_its_cookie_again_only_after_the_daemon_comes_back() {
    let mut web = signed_in();
    open(&mut web, A, true);
    assert!(web.check_due(Some(A)).is_none());
    web.checked_at = Some(Instant::now() - Duration::from_secs(3600));
    assert!(web.check_due(Some(A)).is_none(), "time alone doesn't ask again");
    web.recheck();
    let (_, cookie) = web.check_due(Some(A)).expect("a reconnect checks the cookie");
    web.checked(A, &cookie.value, Ok(true));
    assert!(web.check_due(Some(A)).is_none());
}

#[test]
fn an_unclear_check_on_an_open_page_is_tried_again_a_minute_later() {
    let mut web = signed_in();
    open(&mut web, A, true);
    web.recheck();
    let (_, cookie) = web.check_due(Some(A)).unwrap();
    web.checked(A, &cookie.value, Err("connection refused".into()));
    assert!(web.check_due(Some(A)).is_none(), "not straight away");
    web.checked_at = Some(Instant::now() - Duration::from_secs(61));
    let (_, cookie) = web.check_due(Some(A)).expect("a minute later");
    web.checked(A, &cookie.value, Ok(true));
    web.checked_at = Some(Instant::now() - Duration::from_secs(3600));
    assert!(web.check_due(Some(A)).is_none(), "a clear answer needs no more checks");
}

#[test]
fn a_page_waiting_to_open_waits_a_minute_after_an_unclear_check_before_checking_again() {
    let mut web = signed_in();
    web.verified = None;
    open(&mut web, A, true);
    let (_, cookie) = web.check_due(Some(B)).unwrap();
    web.checked(B, &cookie.value, Err("the page's port answered 503".into()));
    web.signed(Err("daemon down".into()), now());
    assert!(web.check_due(Some(B)).is_none(), "no second check straight after an unclear one, even for a page still opening");
    web.checked_at = Some(Instant::now() - CHECK_RETRY);
    assert!(web.check_due(Some(B)).is_some(), "a minute later it checks again");
}
