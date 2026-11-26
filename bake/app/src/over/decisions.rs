use rune::{El, col, icon, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize};
use rune_theme_opencode::icons;

use feed::decisions::Decision;

use crate::App;
use crate::over::inbox;
use crate::over::ui::{self, BODY, BODY_MED, META, META_MED, base, kit, strong, weak};

const HISTORY_SHOWN: usize = 6;

fn line(s: &App, i: usize, d: &Decision, shift: bool) -> El {
    let k = kit();
    let id = d.id();
    let acting = s.ov.inbox.acting.contains(&id);
    let unconfirmed = s.ov.inbox.unconfirmed.contains(&d.hold_key());
    let who = s.ov.d.session(&d.session_id).map(|x| s.ov.d.title(x)).unwrap_or_else(|| crate::over::short_id(&d.session_id));
    let project = s.ov.d.project(&d.project_id).map(|p| p.name.clone()).unwrap_or_default();
    let mut meta: Vec<String> = vec![ui::short_title(&who, 28)];
    if !project.is_empty() {
        meta.push(project.clone());
    }
    if !d.host_name.is_empty() {
        meta.push(format!("on {}", d.host_name));
    }
    let since = s.ov.d.ago(&d.since);
    if !since.is_empty() {
        meta.push(since);
    }
    let title = if d.work_title.is_empty() { d.reason_text() } else { d.work_title.clone() };
    let mut body = col().grow(1.0).shrink().min_w(0.0).gap(4.0).child(text(crate::over::plain(&title)).font(BODY_MED).color(strong()).max_lines(2).wrap());
    if !d.work_title.is_empty() && !d.reason.is_empty() {
        body = body.child(text(crate::over::plain(&d.reason_text())).font(META).color(base()).max_lines(2).wrap());
    }
    let mut facts = row().gap(8.0).items_center().child(ui::avatar(if project.is_empty() { &who } else { &project }, 16.0)).child(text(meta.join(" · ")).font(META).color(weak()).truncate().shrink());
    if !d.holds_up.is_empty() {
        facts = facts.child(text(format!("Holds up {}", d.holds_up.len())).font(META_MED).color(ui::needs_color()));
    }
    if !d.work_item.is_empty() && d.covers.is_empty() {
        facts = facts.child(ui::id_label(&crate::over::short_id(&d.work_item)));
    }
    if !d.covers.is_empty() {
        body = body.child(text(format!("Covers {} items", d.covers.len())).font(META_MED).color(base()));
        for c in &d.covers {
            let title = if c.work_title.is_empty() { c.reason_text() } else { c.work_title.clone() };
            body = body.child(row().gap(6.0).items_center().child(ui::id_label(&crate::over::short_id(&c.work_item))).child(text(crate::over::plain(&title)).font(META).color(base()).truncate().shrink()));
        }
    }
    for (q, answer) in d.recommended_lines() {
        body = body.child(row().gap(6.0).items_center().child(text(crate::over::plain(&q)).font(META).color(base()).truncate().shrink()).child(text("→".to_string()).font(META).color(weak())).child(text(answer).font(META_MED).color(strong())));
    }
    body = body.child(facts);
    if !d.request_error.is_empty() {
        body = body.child(text(d.request_error.clone()).font(META).color(ui::needs_color()).wrap());
    }
    if let Some(note) = s.ov.inbox.notes.get(&id) {
        body = body.child(text(note.clone()).font(META).color(ui::needs_color()).wrap());
    }
    let mut actions = row().gap(6.0).items_center();
    if d.is_review() {
        actions = actions
            .child(Button::new("Approve").variant(ButtonVariant::Contrast).control_size(ControlSize::Small).disabled(true).build(k))
            .child(Button::new("Reject").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).disabled(true).build(k))
            .child(text("coming soon".to_string()).font(META).color(weak()));
    } else if d.recommended().is_some() {
        let dd = d.clone();
        let mut accept = Button::new(if acting { "Answering…" } else if unconfirmed { "Sent, not confirmed" } else { "Accept recommended" }).variant(ButtonVariant::Contrast).control_size(ControlSize::Small).disabled(acting || unconfirmed).on_press(move |s: &mut App| inbox::accept(s, &dd));
        if shift {
            accept = accept.shortcut("⇧↩");
        }
        actions = actions.child(accept.build(k).key(format!("decision-{i}-accept")));
    }
    let session = d.session_id.clone();
    if !session.is_empty() {
        actions = actions.child(Button::new("Open session").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| crate::over::open_session(s, &session)).build(k));
    }
    if !d.is_review() {
        let dd = d.clone();
        actions = actions.child(
            Button::new(d.not_now_label(&s.ov.inbox.unconfirmed))
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .disabled(acting || d.open_dismissals(&s.ov.inbox.unconfirmed).is_empty())
                .help("Take this off Needs you without answering")
                .on_press(move |s: &mut App| inbox::not_now(s, &dd))
                .build(k)
                .key(format!("decision-{i}-not-now")),
        );
    }
    row().key(format!("decision-{i}")).w_full().gap(14.0).items_start().px(14.0).py(12.0).radius(10.0).child(body).child(actions)
}

pub fn section(s: &App) -> El {
    let st = &s.ov.inbox;
    let count = st.inbox.as_ref().map(|i| i.decisions.len());
    let mut c = col().w_full().gap(6.0).child(ui::section_head("Decisions", count, Some(text("from every connected computer".to_string()).font(META).color(weak()))));
    let Some(found) = &st.inbox else {
        let note = if s.client.is_some() { "Looking for decisions…" } else { "Decisions show when the app is connected to the daemon." };
        return c.child(text(note.to_string()).font(META).color(weak()));
    };
    for n in &found.notes {
        c = c.child(row().gap(6.0).items_center().child(icon(&icons::INFO, 13.0).color(weak())).child(text(n.clone()).font(META).color(weak()).wrap()));
    }
    let mut list = col().w_full().gap(2.0).padding(4.0);
    let mut last = "";
    let target = inbox::shift_target(st).map(Decision::id);
    for (i, d) in found.decisions.iter().enumerate() {
        let group = d.group_title();
        if group != last {
            let n = found.decisions.iter().filter(|x| x.group_title() == group).count();
            list = list.child(text(format!("{group} · {n}")).font(META_MED).color(weak()).px(14.0).pt(if last.is_empty() { 6.0 } else { 12.0 }).pb(2.0));
            last = group;
        }
        list = list.child(line(s, i, d, target.as_deref() == Some(d.id().as_str())));
    }
    if found.decisions.is_empty() {
        list = list.child(row().gap(8.0).items_center().px(14.0).py(10.0).child(icon(&icons::CIRCLE_CHECK, 14.0).color(weak())).child(text(if found.notes.is_empty() { "Nothing needs you." } else { "Nothing needs you on the computers that answered." }.to_string()).font(BODY).color(weak())));
    }
    if !found.lead_handling.is_empty() {
        list = list.child(text("Handled by a lead".to_string()).font(META_MED).color(weak()).px(14.0).pt(12.0).pb(2.0));
        for (i, h) in found.lead_handling.iter().enumerate() {
            let who = s.ov.d.session(&h.session_id).map(|x| s.ov.d.title(x)).unwrap_or_else(|| crate::over::short_id(&h.session_id));
            let id = h.session_id.clone();
            list = list.child(
                row()
                    .key(format!("lead-handled-{i}"))
                    .w_full()
                    .gap(8.0)
                    .items_center()
                    .px(14.0)
                    .py(6.0)
                    .radius(8.0)
                    .hover(kit().c.surface_raised_hover)
                    .on_press(move |s: &mut App| crate::over::open_session(s, &id))
                    .child(text(ui::short_title(&who, 28)).font(META_MED).color(strong()))
                    .child(text(format!("{} has it · {}", h.held_by, crate::over::plain(&h.reason))).font(META).color(weak()).truncate().shrink()),
            );
        }
    }
    c = c.child(ui::card(list));
    if !st.history.is_empty() {
        let mut h = col().w_full().gap(2.0).padding(4.0).child(text("Handled here".to_string()).font(META_MED).color(weak()).px(14.0).pt(6.0).pb(2.0));
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
                    .hover(kit().c.surface_raised_hover)
                    .on_press(move |s: &mut App| crate::over::open_session(s, &id))
                    .child(icon(&icons::CIRCLE_CHECK, 12.0).color(weak()))
                    .child(text(ui::short_title(&e.title, 40)).font(META_MED).color(base()).truncate().shrink())
                    .child(text(e.outcome.clone()).font(META).color(weak()))
                    .child(ui::spacer())
                    .child(text(s.ov.d.ago(&e.at)).font(META).color(weak())),
            );
        }
        c = c.child(ui::card(h));
    }
    c
}

pub fn brief_button(s: &App, project: &str) -> El {
    let asking = s.ov.inbox.briefs.get(project).is_some_and(|b| b.asking);
    let lead = s.ov.d.project(project).filter(|p| p.lead.is_some()).map(|p| p.lead_name.clone()).unwrap_or_else(|| "the lead".into());
    let key = project.to_string();
    Button::new(if asking { "Asking…" } else { "Catch me up" })
        .variant(ButtonVariant::Ghost)
        .control_size(ControlSize::Small)
        .disabled(asking)
        .help(format!("Ask {lead} for a brief on everything since you last wrote to this project"))
        .on_press(move |s: &mut App| inbox::ask_brief(s, &key))
        .build(kit())
        .key(format!("brief-{project}"))
}

pub fn brief_note(s: &App, project: &str) -> Option<El> {
    let b = s.ov.inbox.briefs.get(project)?;
    let note = b.notice.clone()?;
    Some(text(note).font(META).color(if b.failed { ui::needs_color() } else { base() }).wrap())
}
