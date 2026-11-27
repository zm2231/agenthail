use std::cell::RefCell;
use std::collections::HashSet;

use feed::catalog::Session;
use feed::health::{self, Card, Fix, Grant, Permissions, Tally, Tone};
use feed::prefs::Failure;
use rune::{Color, El, col, el, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Kit, Spinner};

use super::parts::{self, section};
use super::{Prefs, Read, Section, WINDOW, busy, fetch, saving, write};
use crate::theme::{self, BODY, BODY_BOLD, LABEL, META, TITLE, done, failed, faint, muted};

pub const TAB: usize = 6;

#[derive(Default)]
pub struct Overview {
    pub permissions: Option<Result<Permissions, Failure>>,
    tally: Tally,
    needs: usize,
    shown: HashSet<String>,
}

thread_local! {
    static LIVE: RefCell<(Tally, usize)> = RefCell::new((Tally::new(), 0));
}

pub fn initial() -> Overview {
    let (tally, needs) = LIVE.with(|l| l.borrow().clone());
    Overview { tally, needs, ..Overview::default() }
}

pub fn sessions_changed(sessions: &[Session], needs: usize) {
    let fresh = (health::tally(sessions), needs);
    let moved = LIVE.with(|l| {
        let mut held = l.borrow_mut();
        let moved = *held != fresh;
        *held = fresh.clone();
        moved
    });
    if !moved {
        return;
    }
    WINDOW.with(|w| {
        if let Some((handle, _)) = w.get() {
            handle.update(move |s: &mut Prefs, _| (s.overview.tally, s.overview.needs) = fresh);
        }
    });
}

pub fn opened(s: &mut Prefs) {
    if s.tab == TAB && s.client.is_some() {
        fetch(s, Read::Permissions);
    }
}

pub fn focused(s: &mut Prefs) {
    if s.tab == TAB && super::visible() {
        opened(s);
    }
}

pub fn changed(s: &mut Prefs) {
    if s.tab != TAB || s.client.is_none() || busy(s) {
        return;
    }
    s.settling.insert(Read::Base);
    fetch(s, Read::Base);
}

fn repair(s: &mut Prefs, surface: String, action: String) {
    write(s, format!("repair-{surface}"), Section::Overview, &[Read::Base], move |c| c.repair(&action));
}

fn ask(s: &mut Prefs) {
    write(s, "permissions-ask", Section::Overview, &[], |c| c.request_permissions());
}

fn fix(s: &mut Prefs, card: &Card, fix: &Fix) {
    match fix {
        Fix::BrowserSignIn => super::signin::show(s),
        Fix::Repair { action, .. } => repair(s, card.surface.clone(), action.clone()),
    }
}

fn dot(label: String, color: Color) -> El {
    row().gap(5.0).items_center().child(el().size(7.0, 7.0).radius(3.5).fill(color)).child(text(label).font(META).color(color))
}

fn grant_row(g: &Grant, k: &Kit) -> El {
    let color = if g.granted() { done() } else { failed() };
    let mut line = row().w_full().gap(10.0).items_center().px(14.0).py(9.0).child(text(g.title()).font(BODY).color(theme::text())).child(dot(g.state_label(), color)).child(el().grow(1.0));
    if !g.granted() && !g.url.is_empty() {
        let url = g.url.clone();
        line = line.child(
            Button::new("Open Settings")
                .control_size(ControlSize::Small)
                .help(g.pane.clone())
                .on_press(move |_: &mut Prefs| crate::open_settings(&url))
                .build(k)
                .key(format!("settings-grant-{}", g.id())),
        );
    }
    line
}

fn permissions(s: &Prefs, k: &Kit) -> Option<El> {
    let state = match s.overview.permissions.as_ref()? {
        Ok(p) if p.supported => p,
        Ok(_) => return None,
        Err(f) => return Some(section("Privacy Permissions", vec![row().w_full().px(10.0).py(10.0).child(parts::failure(s, f).grow(1.0))], None)),
    };
    let mut rows = Vec::new();
    if let Some(problem) = state.problem.clone().filter(|p| !p.is_empty()) {
        rows.push(parts::problem(problem));
    }
    rows.extend(state.grants.iter().map(|g| grant_row(g, k)));
    if state.askable() {
        let asking = saving(s, "permissions-ask");
        let button = Button::new("Ask macOS").control_size(ControlSize::Small).disabled(asking || busy(s)).on_press(ask).build(k).key("settings-permissions-ask");
        rows.push(
            row()
                .w_full()
                .gap(10.0)
                .items_center()
                .px(14.0)
                .py(9.0)
                .child(text(health::ASK_NOTE).font(META).color(muted()).wrap().grow(1.0).min_w(0.0))
                .children(asking.then(|| Spinner::new().size(11.0).build(k)))
                .child(button),
        );
    }
    let footer = state.holder.clone().filter(|h| !h.is_empty()).map(|h| format!("Held by {h}"));
    Some(section("Privacy Permissions", rows, footer))
}

fn stat(title: &str, value: i64, note: Option<&str>) -> El {
    col()
        .gap(1.0)
        .child(text(title.to_string()).font(LABEL).color(muted()))
        .child(text(value.to_string()).font(TITLE).color(theme::text()))
        .children(note.map(|n| text(n.to_string()).font(LABEL).color(faint())))
}

fn details(s: &Prefs, card: &Card, lines: &[String], k: &Kit) -> Option<El> {
    if lines.is_empty() {
        return None;
    }
    let open = s.overview.shown.contains(&card.surface);
    let id = card.surface.clone();
    let toggle = Button::new("Details")
        .variant(ButtonVariant::Ghost)
        .control_size(ControlSize::Small)
        .on_press(move |s: &mut Prefs| {
            if !s.overview.shown.remove(&id) {
                s.overview.shown.insert(id.clone());
            }
        })
        .build(k)
        .key(format!("settings-details-{}", card.surface));
    let mut out = col().w_full().gap(4.0).child(row().child(toggle));
    if open {
        for line in lines {
            out = out.child(text(line.clone()).font(META).color(muted()).wrap());
        }
    }
    Some(out)
}

fn card_view(s: &Prefs, card: &Card, k: &Kit) -> El {
    let color = match card.health.tone() {
        Tone::Done => done(),
        Tone::Failed => failed(),
        Tone::Quiet => muted(),
    };
    let mut head = row().w_full().gap(10.0).items_center().child(text(card.name.clone()).font(BODY_BOLD).color(theme::text())).child(dot(card.health.label().into(), color)).child(el().grow(1.0));
    if let Some(f) = card.problem.as_ref().and_then(|p| p.fix.clone()) {
        let key = format!("repair-{}", card.surface);
        let repairing = saving(s, &key);
        if repairing {
            head = head.child(Spinner::new().size(11.0).build(k));
        }
        let (c, label) = (card.clone(), f.label().to_string());
        head = head.child(Button::new(label).control_size(ControlSize::Small).disabled(repairing || busy(s)).on_press(move |s: &mut Prefs| fix(s, &c, &f)).build(k).key(format!("settings-fix-{}", card.surface)));
    }
    let stats = row().gap(22.0).items_start().child(stat("Working", card.working, None)).child(stat("Current", card.current, Some(&card.current_window))).child(stat("Queued", card.queued, None));
    let mut body = col().w_full().gap(8.0).px(14.0).py(12.0).child(head).child(stats);
    match &card.problem {
        Some(p) => {
            body = body.child(text(p.sentence.clone()).font(META).color(if card.quiet() { muted() } else { failed() }).wrap());
            body = body.children(details(s, card, &p.details, k));
        }
        None => {
            body = body.child(text(card.detail.clone()).font(META).color(muted()).wrap());
            if let Some(a) = &card.availability {
                body = body.child(text(a.clone()).font(META).color(muted()).wrap());
            }
            body = body.children(details(s, card, &card.advice, k));
        }
    }
    body
}

pub fn page(s: &Prefs, k: &Kit) -> El {
    let mut out = parts::page();
    let base = match &s.base {
        None => return out.child(parts::reading("this computer's agent apps")),
        Some(Err(f)) => return out.child(parts::failure(s, f)),
        Some(Ok(b)) => b,
    };
    let (line, stale) = health::summary(&base.daemon, &base.surfaces, &s.overview.tally, s.overview.needs);
    out = out.child(section("", vec![row().w_full().px(14.0).py(10.0).child(text(line).font(BODY).color(if stale { failed() } else { muted() }).wrap().grow(1.0))], None));
    out = out.children(permissions(s, k));
    let cards = health::cards(&base.surfaces, &s.overview.tally, base.codex_recent_hours);
    let rows = if cards.is_empty() { vec![parts::words(health::NO_APPS)] } else { cards.iter().map(|c| card_view(s, c, k)).collect() };
    out = out.child(section("Agent apps", rows, None));
    if let Some(f) = s.notes.get(&Section::Overview) {
        out = out.child(parts::failure(s, f));
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use feed::catalog::Client;

    fn prefs() -> Prefs {
        let mut s = super::super::init();
        s.client = Some(Client::new("http://127.0.0.1:9", "t"));
        s
    }

    #[test]
    fn a_repair_writes_once_and_rereads_the_surfaces() {
        let mut s = prefs();
        repair(&mut s, "codex".into(), "runtime-ensure".into());
        assert!(saving(&s, "repair-codex"));
        repair(&mut s, "codex".into(), "codex-launch".into());
        ask(&mut s);
        assert!(saving(&s, "repair-codex") && !saving(&s, "permissions-ask"));
    }

    #[test]
    fn the_sign_in_fix_opens_the_sign_in_tab() {
        let mut s = prefs();
        s.tab = TAB;
        let card = Card { surface: "notion".into(), name: "Notion".into(), health: health::Health::SigninOff, detail: String::new(), availability: None, advice: vec![], problem: None, working: 0, current: 0, current_window: String::new(), queued: 0 };
        fix(&mut s, &card, &Fix::BrowserSignIn);
        assert_eq!((s.tab, s.signin.page), (super::super::signin::TAB, super::super::signin::PAGE));
        assert!(s.writing.is_none());
    }

    #[test]
    fn permissions_are_read_only_while_the_tab_shows() {
        let mut s = prefs();
        opened(&mut s);
        assert_eq!(s.in_flight, 0);
        s.tab = TAB;
        opened(&mut s);
        assert_eq!(s.in_flight, 1);
    }
}
