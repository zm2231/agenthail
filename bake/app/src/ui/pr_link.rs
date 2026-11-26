use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Instant;

use feed::action::PrOp;
use feed::catalog::{Pr, RespondError};
use feed::menu::{Patch, names_pr, pr_problem};
use rune::{El, TextEdit, Tx, el, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog};

use crate::theme::{self, BODY, META, failed, faint};
use crate::{App, session};

pub enum Done {
    Changed(String, PrOp, String, u64, Result<Vec<Pr>, RespondError>),
}

pub struct Prs {
    pub linking: Option<String>,
    pub text: TextEdit,
    pub error: Option<String>,
    pub saving: bool,
    seq: u64,
    link_seq: u64,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl Prs {
    pub fn new() -> Prs {
        let (tx, rx) = channel();
        Prs { linking: None, text: TextEdit::new(""), error: None, saving: false, seq: 0, link_seq: 0, tx, rx }
    }
}

fn what(op: PrOp) -> &'static str {
    match op {
        PrOp::Link => "Linking the pull request",
        PrOp::Unlink => "Unlinking the pull request",
        PrOp::Watch => "Watching the pull request",
        PrOp::Unwatch => "Unwatching the pull request",
    }
}

fn send(s: &mut App, id: &str, op: PrOp, pr: &str) -> bool {
    let Some(client) = s.client.clone() else {
        s.ov.notice = Some("Fixture mode: not sent".into());
        return false;
    };
    if !s.menus.busy.insert((id.to_string(), "pr")) {
        s.ov.notice = Some("Another change to this session's pull requests is still on its way.".into());
        return false;
    }
    s.prs.seq += 1;
    let (tx, id, pr, seq) = (s.prs.tx.clone(), id.to_string(), pr.to_string(), s.prs.seq);
    std::thread::spawn(move || {
        let result = client.pr(&id, op, &pr);
        let _ = tx.send(Done::Changed(id, op, pr, seq, result));
    });
    true
}

pub fn change(s: &mut App, id: &str, op: PrOp, pr: &str) {
    send(s, id, op, pr);
}

pub fn start(s: &mut App, id: &str) {
    s.prs.linking = Some(id.to_string());
    s.prs.link_seq = 0;
    s.prs.text = TextEdit::new("");
    s.prs.text.focused = true;
    s.composer.focused = false;
    s.prs.error = None;
    s.prs.saving = false;
}

fn close(s: &mut App) {
    s.prs.linking = None;
    s.prs.error = None;
    s.prs.saving = false;
}

fn save(s: &mut App) {
    let Some(id) = s.prs.linking.clone() else { return };
    let pr = s.prs.text.text.trim().to_string();
    if s.prs.saving || pr_problem(&pr).is_some() {
        return;
    }
    if send(s, &id, PrOp::Link, &pr) {
        s.prs.link_seq = s.prs.seq;
        s.prs.saving = true;
        s.prs.error = None;
    } else if s.client.is_none() {
        close(s);
    }
}

fn settled(s: &mut App, done: Done) {
    let Done::Changed(id, op, asked, seq, result) = done;
    s.menus.busy.remove(&(id.clone(), "pr"));
    let dialog = op == PrOp::Link && s.prs.linking.as_deref() == Some(id.as_str()) && s.prs.link_seq == seq;
    match result {
        Ok(prs) => {
            let changed = match op {
                PrOp::Unlink => Some(Pr { url: asked.clone(), ..Default::default() }),
                _ => prs.iter().find(|p| p.url == asked).or_else(|| prs.iter().find(|p| names_pr(&asked, p))).cloned(),
            };
            if let Some(pr) = changed
                && s.ov.live.patch(&id, Patch::Pr { pr, linked: op != PrOp::Unlink }, Instant::now())
            {
                s.sessions = s.ov.live.sessions().to_vec();
            }
            if dialog {
                close(s);
            }
        }
        Err(error) if dialog => {
            s.prs.saving = false;
            s.prs.error = Some(if error.maybe_delivered { format!("It may have been linked anyway: {}. Check the session's pull requests before trying again.", error.message) } else { error.message });
        }
        Err(error) => {
            s.ov.notice = Some(if error.maybe_delivered { format!("{} may not have gone through: {}", what(op), error.message) } else { format!("{} didn't work: {}", what(op), error.message) });
        }
    }
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok(done) = s.prs.rx.try_recv() {
        changed = true;
        settled(s, done);
    }
    changed
}

fn dialog(s: &App, id: &str, layer: &mut Vec<El>) {
    let k = theme::kit();
    let typed = s.prs.text.text.clone();
    let problem = pr_problem(&typed);
    let input = text_input(&s.prs.text, BODY)
        .single_line()
        .key("pr-link-input")
        .label("Pull request")
        .grow(1.0)
        .min_w(0.0)
        .color(theme::text())
        .placeholder("https://github.com/owner/repo/pull/12, or 12", faint())
        .on_edit(|s: &mut App| &mut s.prs.text)
        .on_submit(|s: &mut App, _: &mut Tx| save(s));
    let field = row().w_full().px(10.0).py(7.0).radius(8.0).fill(theme::wash()).border(1.0, theme::line()).child(input);
    let mut body = rune::col().w_full().gap(10.0).px(16.0).pb(16.0).child(field);
    if let Some(message) = s.prs.error.clone().or_else(|| (!typed.trim().is_empty()).then_some(problem).flatten().map(String::from)) {
        body = body.child(text(message).font(META).color(failed()).wrap().w_full());
    }
    let buttons = row()
        .w_full()
        .gap(8.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(close).build(k).key("pr-link-cancel"))
        .child(Button::new(if s.prs.saving { "Linking…" } else { "Link" }).variant(ButtonVariant::Contrast).control_size(ControlSize::Small).disabled(s.prs.saving || problem.is_some()).on_press(save).build(k).key("pr-link-save"));
    let title = session(s, id).map(|x| x.title()).unwrap_or_default();
    Dialog::new("pr-link", format!("Link a pull request to {title}"))
        .description("A number picks the pull request with that number already linked to this session, or else the one in the session's own repository. Watch it afterwards to wake the session when it changes.")
        .open(true)
        .fit()
        .width(440.0)
        .on_close(close)
        .child(body.child(buttons))
        .build(k, layer);
}

pub fn overlays(s: &App, layer: &mut Vec<El>) {
    if let Some(id) = &s.prs.linking {
        dialog(s, id, layer);
    }
}
