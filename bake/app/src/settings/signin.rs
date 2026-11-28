use feed::prefs::Failure;
use feed::signin::{self as si, Consumer, Signin};
use rune::{El, TextEdit, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Kit, SegmentedControl, TextInput};

use super::parts::{self, line, section};
use super::{Prefs, Read, Section, busy, fetch, write};
use crate::theme::{self, BODY, META, failed, muted};

pub const TAB: usize = 3;
pub const PAGE: usize = 1;
const PAGES: [&str; 2] = ["Devices", "Browser Sign-in"];

const CONSUMER_KEYS: [&str; 12] = ["signin-use-0", "signin-use-1", "signin-use-2", "signin-use-3", "signin-use-4", "signin-use-5", "signin-use-6", "signin-use-7", "signin-use-8", "signin-use-9", "signin-use-10", "signin-use-11"];
const THEN: &[Read] = &[Read::Signin];

pub struct Form {
    pub page: usize,
    pub data: Option<Result<Signin, Failure>>,
    browser: String,
    profile: String,
    label: TextEdit,
}

impl Default for Form {
    fn default() -> Form {
        Form { page: 0, data: None, browser: String::new(), profile: String::new(), label: TextEdit::new("") }
    }
}

pub fn opened(s: &mut Prefs) {
    if s.tab == TAB && s.signin.page == PAGE && s.client.is_some() {
        fetch(s, Read::Signin);
    }
}

pub fn changed(s: &mut Prefs) {
    opened(s);
}

pub fn settled(s: &mut Prefs) {
    if let Some(Ok(data)) = &s.signin.data {
        (s.signin.browser, s.signin.profile) = data.pick(&s.signin.browser, &s.signin.profile);
    }
}

fn bind(s: &mut Prefs, consumer: String, choice: String) {
    write(s, format!("signin-bind-{consumer}"), Section::Signin, THEN, move |c| c.bind_signin(&consumer, &choice));
}

fn add(s: &mut Prefs) {
    let (browser, profile, label) = (s.signin.browser.clone(), s.signin.profile.clone(), s.signin.label.text.clone());
    if !matches!(&s.signin.data, Some(Ok(d)) if d.can_add(&browser, &profile)) {
        return;
    }
    s.signin.label = TextEdit::new("");
    write(s, "signin-add", Section::Signin, THEN, move |c| c.add_signin_profile(&browser, &profile, &label));
}

fn remove(s: &mut Prefs, id: String) {
    write(s, format!("signin-remove-{id}"), Section::Signin, THEN, move |c| c.remove_signin_profile(&id));
}

fn pick_browser(s: &mut Prefs, browser: String) {
    s.signin.browser = browser;
    settled(s);
}

fn consumer_row(s: &Prefs, data: &Signin, i: usize, c: &Consumer, layer: &mut Vec<El>, k: &Kit) -> El {
    let id = c.id.clone();
    let picker = match CONSUMER_KEYS.get(i) {
        Some(key) => parts::select(s, key, &c.label, data.choices(), &c.choice(), busy(s) || s.client.is_none(), layer, move |s, choice| bind(s, id.clone(), choice), k),
        None => text(c.choice()).font(k.text(META)).color(muted()),
    };
    let mut words = col().gap(3.0).grow(1.0).min_w(0.0).child(text(c.label.clone()).font(k.text(BODY)).color(theme::text())).child(text(c.resolved()).font(k.text(META)).color(theme::text()).wrap());
    if let Some(p) = c.problem.clone().filter(|p| !p.is_empty()) {
        words = words.child(text(p).font(k.text(META)).color(failed()).wrap());
    }
    if let Some(u) = c.unavailable.clone().filter(|u| !u.is_empty()) {
        words = words.child(text(u).font(k.text(META)).color(muted()).wrap());
    }
    if !c.purpose.is_empty() {
        words = words.child(text(c.purpose.clone()).font(k.text(META)).color(muted()).wrap());
    }
    row().w_full().gap(16.0).items_start().px(14.0).py(10.0).child(words).child(picker)
}

fn pinned(s: &Prefs, data: &Signin, k: &Kit) -> El {
    let mut rows = Vec::new();
    if data.profiles.is_empty() {
        rows.push(parts::words(si::NO_PROFILES, k));
    }
    for p in &data.profiles {
        let id = p.id.clone();
        let button = Button::new("Remove")
            .variant(ButtonVariant::Danger)
            .control_size(ControlSize::Small)
            .disabled(busy(s))
            .on_press(move |s: &mut Prefs| remove(s, id.clone()))
            .build(k)
            .key(format!("settings-signin-remove-{}", p.id));
        rows.push(line(&p.label, Some(data.pinned_detail(p)), button, k));
    }
    section("Pinned profiles", rows, None, k)
}

fn adder(s: &Prefs, data: &Signin, layer: &mut Vec<El>, k: &Kit) -> El {
    let mut rows = Vec::new();
    let browsers = data.addable_browsers();
    if browsers.is_empty() {
        rows.push(parts::words(si::ALL_ADDED, k));
    } else {
        let disabled = busy(s) || s.client.is_none();
        let browser_choices = browsers.iter().map(|b| (b.label.clone(), b.browser.clone())).collect();
        rows.push(line("Browser", None, parts::select(s, "signin-browser", "Browser", browser_choices, &s.signin.browser, disabled, layer, pick_browser, k), k));
        let profiles = browsers.iter().find(|b| b.browser == s.signin.browser).map(|b| b.addable().into_iter().map(|p| (p.choice_label(), p.profile.clone())).collect()).unwrap_or_default();
        rows.push(line("Profile", None, parts::select(s, "signin-profile", "Profile", profiles, &s.signin.profile, disabled, layer, |s, p| s.signin.profile = p, k), k));
        let input = TextInput::new(&s.signin.label).label("Label (optional)").placeholder("Label (optional)").width(None).disabled(disabled).on_edit(|s: &mut Prefs| &mut s.signin.label).on_submit(|s: &mut Prefs, _: &mut Tx| add(s)).build(k);
        let button = Button::new("Add Profile").control_size(ControlSize::Small).disabled(disabled || !data.can_add(&s.signin.browser, &s.signin.profile)).on_press(add).build(k).key("settings-signin-add");
        rows.push(col().w_full().gap(10.0).px(14.0).py(12.0).child(input).child(row().child(button)));
    }
    for b in data.unavailable_browsers() {
        rows.push(parts::words(b.unavailable_line(), k));
    }
    section("Add a browser profile", rows, None, k)
}

pub fn show(s: &mut Prefs) {
    s.tab = TAB;
    s.signin.page = PAGE;
    s.open_select = None;
    super::devices::hide_link(s);
    opened(s);
}

pub fn pages(s: &Prefs, layer: &mut Vec<El>, k: &Kit) -> El {
    let pick = SegmentedControl::new(&PAGES, Some(s.signin.page))
        .label("Devices")
        .fit()
        .on_pick(|s: &mut Prefs, i, _| {
            s.signin.page = i;
            s.open_select = None;
            super::devices::hide_link(s);
            opened(s);
        })
        .build(k)
        .key("devices-pages");
    let body = if s.signin.page == PAGE { page(s, layer, k) } else { super::devices::page(s, layer, k) };
    col().w_full().gap(16.0).items_center().child(pick).child(body.w_full())
}

fn page(s: &Prefs, layer: &mut Vec<El>, k: &Kit) -> El {
    let mut out = parts::page().child(section("", vec![parts::words(si::ABOUT, k)], None, k));
    match &s.signin.data {
        None => out = out.child(parts::reading("browser sign-in", k)),
        Some(Err(f)) => out = out.child(parts::failure(s, f, k)),
        Some(Ok(data)) => {
            let rows = data.consumers.iter().enumerate().map(|(i, c)| consumer_row(s, data, i, c, layer, k)).collect();
            out = out.child(section("Used by", rows, None, k)).child(pinned(s, data, k)).child(adder(s, data, layer, k));
        }
    }
    if let Some(f) = s.notes.get(&Section::Signin) {
        out = out.child(parts::failure(s, f, k));
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use feed::catalog::Client;
    use serde_json::json;

    fn prefs() -> Prefs {
        let mut s = super::super::init();
        s.client = Some(Client::new("http://127.0.0.1:9", "t"));
        s.tab = TAB;
        s.signin.page = PAGE;
        s
    }

    fn data() -> Signin {
        feed::reply::parse(json!({"profiles": [], "consumers": [{"id": "notion", "label": "Notion", "mode": "auto"}],
            "browsers": [{"browser": "chrome", "label": "Chrome", "available": true, "profiles": [{"profile": "Default", "name": "Personal"}, {"profile": "Profile 2", "name": "Work"}]}]}))
        .unwrap()
    }

    #[test]
    fn a_read_picks_the_first_addable_profile_and_keeps_a_valid_choice() {
        let mut s = prefs();
        s.signin.data = Some(Ok(data()));
        settled(&mut s);
        assert_eq!((s.signin.browser.as_str(), s.signin.profile.as_str()), ("chrome", "Default"));
        s.signin.profile = "Profile 2".into();
        settled(&mut s);
        assert_eq!(s.signin.profile, "Profile 2");
    }

    #[test]
    fn adding_needs_an_addable_profile_and_clears_the_label() {
        let mut s = prefs();
        s.signin.data = Some(Ok(data()));
        s.signin.browser = "chrome".into();
        s.signin.profile = "Missing".into();
        add(&mut s);
        assert!(s.writing.is_none());
        s.signin.profile = "Default".into();
        s.signin.label = TextEdit::new("Home");
        add(&mut s);
        assert!(super::super::saving(&s, "signin-add") && s.signin.label.text.is_empty());
    }

    #[test]
    fn a_binding_waits_for_the_write_in_flight() {
        let mut s = prefs();
        bind(&mut s, "notion".into(), "none".into());
        assert!(super::super::saving(&s, "signin-bind-notion"));
        remove(&mut s, "p1".into());
        assert!(!super::super::saving(&s, "signin-remove-p1"));
    }

    #[test]
    fn a_reload_rereads_the_page_only_while_it_shows() {
        let mut s = prefs();
        super::super::reload(&mut s);
        assert_eq!(s.seq.get(&Read::Signin), Some(&1));
        s.signin.page = 0;
        super::super::reload(&mut s);
        assert_eq!(s.seq.get(&Read::Signin), Some(&1));
    }

    #[test]
    fn a_change_during_the_first_read_starts_a_newer_read() {
        let mut s = prefs();
        opened(&mut s);
        let first = s.seq[&Read::Signin];
        changed(&mut s);
        assert_eq!(s.seq[&Read::Signin], first + 1);
        s.signin.page = 0;
        changed(&mut s);
        assert_eq!(s.seq[&Read::Signin], first + 1);
    }
}
