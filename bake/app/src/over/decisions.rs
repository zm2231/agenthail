use rune::{El, TextEdit, Tx, col, el, icon, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, Kit};
use rune_theme_opencode::icons;

use feed::decisions::Decision;
use feed::review::{self, Verdict};

use crate::App;
use crate::over::inbox;
use crate::ui::waiting::{self, Route};
use crate::over::ui::{self, BODY, BODY_MED, META, META_MED, base, strong, weak};

const HISTORY_SHOWN: usize = 6;

#[derive(Hash)]
struct Shown {
    who: String,
    project: String,
    since: String,
    acting: bool,
    unconfirmed: bool,
    note: Option<String>,
    review: Option<ReviewShown>,
    replies: bool,
    not_now: Option<(String, bool)>,
    shift: bool,
    open: bool,
}

impl Shown {
    fn of(s: &App, d: &Decision, id: &str, shift: bool) -> Shown {
        let st = &s.ov.inbox;
        let acting = st.acting.contains(id);
        Shown {
            who: s.ov.d.session(&d.session_id).map(|x| s.ov.d.title(x)).unwrap_or_else(|| crate::over::short_id(&d.session_id)),
            project: s.ov.d.project(&d.project_id).map(|p| p.name.clone()).unwrap_or_default(),
            since: s.ov.d.ago(&d.since),
            acting,
            unconfirmed: st.unconfirmed.contains(&d.hold_key()),
            note: st.notes.get(id).filter(|(hold, _)| d.holds(hold)).map(|(_, n)| n.clone()),
            review: d.is_review().then(|| ReviewShown::of(s, d, false)),
            replies: d.is_parked() && !d.session_id.is_empty() && !crate::closed(s, &d.session_id),
            not_now: (!d.is_review()).then(|| (d.not_now_label(&st.unconfirmed), acting || d.open_dismissals(&st.unconfirmed).is_empty())),
            shift,
            open: st.open.as_deref() == Some(id),
        }
    }
}

fn line(s: &App, d: &Decision, id: &str, shift: bool, k: &Kit, main_kit: &Kit) -> El {
    let shown = Shown::of(s, d, id, shift);
    if shown.open {
        return line_of(s, d, id, &shown, k, main_kit);
    }
    let rev = s.ov.inbox.drawn.borrow_mut().of(id, d);
    crate::over::rows::memo(format!("decision-{id}"), (rev, &shown), k, || line_of(s, d, id, &shown, k, main_kit))
}

fn line_of(s: &App, d: &Decision, id: &str, shown: &Shown, k: &Kit, main_kit: &Kit) -> El {
    let (shift, open) = (shown.shift, shown.open);
    let tag = format!("decision-{id}");
    let (who, project) = (&shown.who, &shown.project);
    let mut meta: Vec<String> = vec![ui::short_title(who, 28)];
    if !project.is_empty() {
        meta.push(project.clone());
    }
    if !d.host_name.is_empty() {
        meta.push(format!("on {}", d.host_name));
    }
    if !shown.since.is_empty() {
        meta.push(shown.since.clone());
    }
    let title = if d.work_title.is_empty() { d.reason_text() } else { d.work_title.clone() };
    let mut body = col().grow(1.0).shrink().min_w(0.0).gap(4.0).child(text(crate::over::plain(&title)).font(k.text(BODY_MED)).color(strong(k)).max_lines(2).wrap());
    if !d.work_title.is_empty() && !d.reason.is_empty() {
        body = body.child(text(crate::over::plain(&d.reason_text())).font(k.text(META)).color(base(k)).max_lines(2).wrap());
    }
    let mut facts = row().gap(8.0).items_center().child(ui::avatar(if project.is_empty() { who } else { project }, 16.0)).child(text(meta.join(" · ")).font(k.text(META)).color(weak(k)).truncate().shrink());
    if !d.holds_up.is_empty() {
        facts = facts.child(text(format!("Holds up {}", d.holds_up.len())).font(k.text(META_MED)).color(ui::needs_color(k)));
    }
    if !d.work_item.is_empty() && d.covers.is_empty() {
        facts = facts.child(ui::id_label(&crate::over::short_id(&d.work_item), k));
    }
    if let Some(p) = d.proof_line() {
        facts = facts.child(text(p).font(k.text(META)).color(weak(k)));
    }
    if !d.covers.is_empty() {
        body = body.child(text(format!("Covers {} items", d.covers.len())).font(k.text(META_MED)).color(base(k)));
        for c in &d.covers {
            let title = if c.work_title.is_empty() { c.reason_text() } else { c.work_title.clone() };
            body = body.child(row().gap(6.0).items_center().child(ui::id_label(&c.work_item, k)).child(text(crate::over::plain(&title)).font(k.text(META)).color(base(k)).truncate().shrink()));
        }
    }
    for (q, answer) in d.recommended_lines() {
        body = body.child(row().gap(6.0).items_center().child(text(crate::over::plain(&q)).font(k.text(META)).color(base(k)).truncate().shrink()).child(text("→".to_string()).font(k.text(META)).color(weak(k))).child(text(answer).font(k.text(META_MED)).color(strong(k))));
    }
    body = body.child(facts);
    if !d.request_error.is_empty() {
        body = body.child(text(d.request_error.clone()).font(k.text(META)).color(ui::needs_color(k)).wrap());
    }
    if let Some(note) = &shown.note {
        body = body.child(text(note.clone()).font(k.text(META)).color(ui::needs_color(k)).wrap());
    }
    let mut actions = row().gap(6.0).items_center();
    if let Some(review) = &shown.review {
        let (da, dc) = (d.clone(), d.clone());
        let approve = move |s: &mut App| inbox::review(s, &da, Verdict::Approve, "");
        let changes = move |s: &mut App| inbox::ask_changes(s, &dc);
        actions = shown_review_buttons(review, ControlSize::Small, &tag, actions, k, approve, changes);
        if let Some(p) = review::pr_link(&d.prs, None) {
            let url = p.url.clone();
            let label = if p.number > 0 { format!("Open PR #{}", p.number) } else { "Open PR".into() };
            actions = actions.child(Button::new(label).variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |_: &mut App| crate::open_link(&url)).build(k).key(format!("{tag}-pr")));
        }
    } else if d.recommended().is_some() {
        let dd = d.clone();
        let (acting, unconfirmed) = (shown.acting, shown.unconfirmed);
        let mut accept = Button::new(if acting { "Answering…" } else if unconfirmed { "Sent, not confirmed" } else { "Accept recommended" }).variant(ButtonVariant::Contrast).control_size(ControlSize::Small).disabled(acting || unconfirmed).on_press(move |s: &mut App| inbox::accept(s, &dd));
        if shift {
            accept = accept.shortcut("⇧↩");
        }
        actions = actions.child(accept.build(k).key(format!("{tag}-accept")));
    }
    let answers = !d.is_review() && d.answerable && !d.requests.is_empty();
    if answers || shown.replies {
        let dd = d.clone();
        let label = match (open, answers) {
            (true, _) => "Hide",
            (false, true) => "Answer…",
            (false, false) => "Reply…",
        };
        actions = actions.child(Button::new(label).variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press_tx(move |s: &mut App, tx: &mut Tx| inbox::toggle_open(s, &dd, tx)).build(k).key(format!("{tag}-open")));
    }
    let session = d.session_id.clone();
    if !session.is_empty() {
        actions = actions.child(Button::new("Open session").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| crate::over::open_session(s, &session)).build(k));
    }
    if let Some((label, disabled)) = &shown.not_now {
        let dd = d.clone();
        actions = actions.child(
            Button::new(label.clone())
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .disabled(*disabled)
                .help("Take this off Needs you without answering")
                .on_press(move |s: &mut App| inbox::not_now(s, &dd))
                .build(k)
                .key(format!("{tag}-not-now")),
        );
    }
    let head = row().w_full().gap(14.0).items_start().child(body).child(actions);
    let mut c = col().key(tag.clone()).w_full().gap(10.0).px(14.0).py(12.0).radius(10.0).child(head);
    if open && answers {
        c = c.child(answer_panel(s, d, k, main_kit));
    } else if open && shown.replies {
        c = c.child(reply_panel(s, &tag, d, who, k));
    }
    c
}

fn answer_panel(s: &App, d: &Decision, k: &Kit, main_kit: &Kit) -> El {
    let open: Vec<&feed::catalog::OpenRequest> = d.requests.iter().collect();
    let Some(request) = waiting::shown_of(s, &d.session_id, &open) else { return el() };
    let busy = inbox::held(s, d, &request.id);
    let who = s.ov.d.session(&d.session_id).map(|x| s.ov.d.title(x)).unwrap_or_else(|| "The agent".into());
    let head = row().w_full().gap(8.0).items_center().child(text(crate::over::plain(&waiting::request_heading(request, &who))).font(k.text(BODY_MED)).color(strong(k)).wrap().grow(1.0).min_w(0.0)).children(waiting::switcher(&d.session_id, &open, &request.id, k));
    let route = Route::Decision(d.clone());
    col()
        .key(format!("decision-answer-{}", d.id()))
        .focusable(true)
        .on_key(move |s: &mut App, ev: &rune::KeyEvent, _: &mut Tx| waiting::pager_key(s, ev, &route))
        .w_full()
        .gap(8.0)
        .pl(4.0)
        .child(head)
        .child(waiting::request_body(s, request, Route::Decision(d.clone()), busy, main_kit))
}

fn reply_panel(s: &App, tag: &str, d: &Decision, who: &str, k: &Kit) -> El {
    let st = &s.ov.inbox;
    let id = d.id();
    let sending = st.replying.contains(&id);
    let empty = TextEdit::new("");
    let edit = st.replies.get(&id).unwrap_or(&empty);
    let ready = !sending && !edit.text.trim().is_empty();
    let (lens, dd, de) = (id.clone(), d.clone(), d.clone());
    let input = text_input(edit, k.text(BODY))
        .key(format!("{tag}-reply"))
        .w_full()
        .min_h(44.0)
        .color(strong(k))
        .placeholder(format!("Answer {who}"), weak(k))
        .on_edit(move |s: &mut App| s.ov.inbox.replies.entry(lens.clone()).or_insert_with(|| TextEdit::new("")))
        .on_submit(move |s: &mut App, _: &mut Tx| inbox::reply(s, &de));
    let field = col().w_full().px(10.0).py(8.0).radius(8.0).fill(k.c.surface_base).border(1.0, ui::rule(k)).child(input);
    let send = Button::new(if sending {
        "Sending…".to_string()
    } else if st.reply_unknown.contains(&id) {
        "Send as new".to_string()
    } else {
        format!("Send to {}", ui::short_title(who, 24))
    }).variant(ButtonVariant::Contrast).control_size(ControlSize::Small).disabled(!ready).on_press(move |s: &mut App| inbox::reply_as_new(s, &dd)).build(k).key(format!("{tag}-reply-send"));
    let mut c = col().key(format!("decision-reply-{id}")).w_full().gap(8.0).pl(4.0);
    if let Some(sent) = st.replied.get(&id) {
        c = c.child(row().gap(6.0).items_center().child(icon(&icons::CIRCLE_CHECK, k.text_size(12.0)).color(base(k))).child(text(sent.clone()).font(k.text(META_MED)).color(base(k))));
    }
    c.child(field).child(row().gap(8.0).items_center().child(send).child(text("It goes to the session holding the work, as your message.".to_string()).font(k.text(META)).color(weak(k)).wrap().shrink()))
}

#[derive(Hash)]
pub struct ReviewShown {
    approve: &'static str,
    ready: bool,
    old: bool,
    note: Option<String>,
}

impl ReviewShown {
    pub fn of(s: &App, d: &Decision, show_note: bool) -> ReviewShown {
        let st = &s.ov.inbox;
        let old = inbox::too_old(st, d);
        let approve = if st.acting.contains(&d.id()) {
            "Sending…"
        } else if st.unconfirmed.contains(&d.hold_key()) {
            "Sent, not confirmed"
        } else {
            "Approve"
        };
        let note = st.notes.get(&d.id()).filter(|(hold, _)| show_note && d.holds(hold)).map(|(_, n)| n.clone()).or_else(|| old.then(review::needs_newer_note));
        ReviewShown { approve, ready: inbox::reviewable(st, d), old, note }
    }
}

pub fn shown_review_buttons(shown: &ReviewShown, size: ControlSize, key: &str, mut r: El, k: &Kit, approve: impl Fn(&mut App) + 'static, changes: impl Fn(&mut App) + 'static) -> El {
    let mut a = Button::new(shown.approve).variant(ButtonVariant::Contrast).control_size(size).disabled(!shown.ready).on_press(approve);
    let mut c = Button::new("Ask for changes").variant(ButtonVariant::Ghost).control_size(size).disabled(!shown.ready).on_press(changes);
    if shown.old {
        a = a.help(review::needs_newer_note());
        c = c.help(review::needs_newer_note());
    }
    r = r.child(a.build(k).key(format!("{key}-approve"))).child(c.build(k).key(format!("{key}-changes")));
    if let Some(n) = &shown.note {
        r = r.child(text(n.clone()).font(k.text(META)).color(ui::needs_color(k)).wrap().shrink());
    }
    r
}

pub fn note_dialog(s: &App, layer: &mut Vec<El>, k: &Kit) {
    let Some(n) = &s.ov.inbox.note else { return };
    let problem = review::note_problem(&n.text.text);
    let input = text_input(&n.text, k.text(BODY))
        .key("review-note-input")
        .label("What it still needs")
        .w_full()
        .min_h(72.0)
        .color(strong(k))
        .placeholder("What should change before you approve?", weak(k))
        .on_edit(|s: &mut App| &mut s.ov.inbox.note.as_mut().expect("the dialog is open").text)
        .on_submit(|s: &mut App, _: &mut Tx| inbox::send_changes(s));
    let field = col().w_full().px(10.0).py(8.0).radius(8.0).fill(k.c.surface_base).border(1.0, ui::rule(k)).child(input);
    let buttons = row()
        .w_full()
        .gap(8.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut App| s.ov.inbox.note = None).build(k).key("review-note-cancel"))
        .child(Button::new("Ask for changes").variant(ButtonVariant::Contrast).control_size(ControlSize::Small).disabled(problem.is_some() || n.gone).on_press(inbox::send_changes).build(k).key("review-note-send"));
    let mut body = col().w_full().gap(10.0).px(16.0).pb(16.0).child(field);
    let why = if n.gone { Some("This review isn't waiting on you any more, or it was resubmitted, so your note wasn't sent. Copy it if you want to send it to the worker yourself.".to_string()) } else { n.why.clone() };
    if let Some(why) = why {
        body = body.child(text(why).font(k.text(META)).color(ui::needs_color(k)).wrap().w_full());
    }
    let body = body.child(buttons);
    Dialog::new("review-note", format!("Ask for changes to {}", ui::short_title(&crate::over::plain(&n.title), 48)))
        .description("The worker holding it gets your note and the item goes back to them.")
        .open(true)
        .fit()
        .width(460.0)
        .on_close(|s: &mut App| s.ov.inbox.note = None)
        .child(body)
        .build(k, layer);
}

pub fn section(s: &App, k: &Kit, main_kit: &Kit) -> El {
    let st = &s.ov.inbox;
    let count = st.inbox.as_ref().map(|i| i.decisions.len());
    let mut c = col().w_full().gap(6.0).child(ui::section_head("Decisions", count, Some(text("from every connected computer".to_string()).font(k.text(META)).color(weak(k))), k));
    let Some(found) = &st.inbox else {
        let note = if s.client.is_some() { "Looking for decisions…" } else { "Decisions show when the app is connected to the daemon." };
        return c.child(text(note.to_string()).font(k.text(META)).color(weak(k)));
    };
    for n in &found.notes {
        c = c.child(row().gap(6.0).items_center().child(icon(&icons::INFO, k.text_size(13.0)).color(weak(k))).child(text(n.clone()).font(k.text(META)).color(weak(k)).wrap()));
    }
    let mut list = col().w_full().gap(2.0).padding(4.0);
    let mut last = "";
    let target = inbox::shift_target(st).map(Decision::id);
    let ids: Vec<String> = found.decisions.iter().map(Decision::id).collect();
    for (d, id) in found.decisions.iter().zip(&ids) {
        let group = d.group_title();
        if group != last {
            let n = found.decisions.iter().filter(|x| x.group_title() == group).count();
            list = list.child(text(format!("{group} · {n}")).font(k.text(META_MED)).color(weak(k)).px(14.0).pt(if last.is_empty() { 6.0 } else { 12.0 }).pb(2.0));
            last = group;
        }
        list = list.child(line(s, d, id, target.as_deref() == Some(id.as_str()), k, main_kit));
    }
    s.ov.inbox.drawn.borrow_mut().keep(|id| ids.iter().any(|x| x == id));
    if found.decisions.is_empty() {
        list = list.child(row().gap(8.0).items_center().px(14.0).py(10.0).child(icon(&icons::CIRCLE_CHECK, k.text_size(14.0)).color(weak(k))).child(text(if found.notes.is_empty() { "Nothing needs you." } else { "Nothing needs you on the computers that answered." }.to_string()).font(k.text(BODY)).color(weak(k))));
    }
    if !found.lead_handling.is_empty() {
        list = list.child(text("Handled by a lead".to_string()).font(k.text(META_MED)).color(weak(k)).px(14.0).pt(12.0).pb(2.0));
        for h in &found.lead_handling {
            let who = s.ov.d.session(&h.session_id).map(|x| s.ov.d.title(x)).unwrap_or_else(|| crate::over::short_id(&h.session_id));
            let holder = s.ov.d.name_of(&h.held_by);
            let key = format!("lead-handled-{}|{}|{}", h.ledger_key, h.work_item, h.session_id);
            list = list.child(crate::over::rows::memo(key.clone(), (&who, &holder, &h.reason, &h.session_id), k, || {
                let id = h.session_id.clone();
                row()
                    .key(key.clone())
                    .w_full()
                    .gap(8.0)
                    .items_center()
                    .px(14.0)
                    .py(6.0)
                    .radius(8.0)
                    .hover(k.c.surface_raised_hover)
                    .on_press(move |s: &mut App| crate::over::open_session(s, &id))
                    .child(text(ui::short_title(&who, 28)).font(k.text(META_MED)).color(strong(k)))
                    .child(text(format!("{holder} has it · {}", crate::over::plain(&h.reason))).font(k.text(META)).color(weak(k)).truncate().shrink())
            }));
        }
    }
    c = c.child(ui::card(list, k));
    if !st.history.is_empty() {
        let mut h = col().w_full().gap(2.0).padding(4.0).child(text("Handled here".to_string()).font(k.text(META_MED)).color(weak(k)).px(14.0).pt(6.0).pb(2.0));
        for (i, e) in st.history.iter().take(HISTORY_SHOWN).enumerate() {
            let id = e.session.clone();
            h = h.child(
                row()
                    .key(format!("handled-{i}"))
                    .w_full()
                    .gap(8.0)
                    .items_center()
                    .px(14.0)
                    .py(6.0)
                    .radius(8.0)
                    .hover(k.c.surface_raised_hover)
                    .on_press(move |s: &mut App| crate::over::open_session(s, &id))
                    .child(icon(&icons::CIRCLE_CHECK, k.text_size(12.0)).color(weak(k)))
                    .child(text(ui::short_title(&e.title, 40)).font(k.text(META_MED)).color(base(k)).truncate().shrink())
                    .child(text(e.outcome.clone()).font(k.text(META)).color(weak(k)))
                    .child(ui::spacer())
                    .child(text(s.ov.d.ago(&e.at)).font(k.text(META)).color(weak(k))),
            );
        }
        c = c.child(ui::card(h, k));
    }
    c
}

pub fn brief_button(s: &App, project: &str, k: &Kit) -> El {
    let asking = s.ov.inbox.briefs.get(project).is_some_and(|b| b.asking);
    let unknown = s.ov.inbox.briefs.get(project).is_some_and(|b| b.unknown);
    let lead = s.ov.d.project(project).filter(|p| p.lead.is_some()).map(|p| p.lead_name.clone()).unwrap_or_else(|| "the lead".into());
    let key = project.to_string();
    Button::new(if asking {
        "Asking…"
    } else if unknown {
        inbox::BRIEF_AS_NEW
    } else {
        "Catch me up"
    })
        .variant(ButtonVariant::Ghost)
        .control_size(ControlSize::Small)
        .disabled(asking)
        .help(format!("Ask {lead} for a brief on everything since you last wrote to this project"))
        .on_press(move |s: &mut App| inbox::ask_brief(s, &key))
        .build(k)
        .key(format!("brief-{project}"))
}

pub fn brief_note(s: &App, project: &str, k: &Kit) -> Option<El> {
    let b = s.ov.inbox.briefs.get(project)?;
    let note = b.notice.clone()?;
    Some(text(note).font(k.text(META)).color(if b.failed { ui::needs_color(k) } else { base(k) }).wrap())
}
