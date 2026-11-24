use std::collections::BTreeSet;

use rune::{Cx, El, ScrollSpec, col, el, icon, row, text};
use rune_kit::shell::Toolbar;
use rune_kit::{Button, ButtonVariant};
use rune_theme_opencode::icons;

use crate::data::{plain, short_id};
use crate::ui::{self, BODY, BODY_MED, HEAD, META, META_MED, TITLE, base, kit, strong, weak};
use crate::{App, Screen, go};

fn proof_row(i: usize, expected: &str, observed: &str, passed: Option<bool>) -> El {
    let k = kit();
    let (label, glyph, color) = match passed {
        Some(true) => ("Passed", &icons::CIRCLE_CHECK, k.c.success.icon),
        Some(false) => ("Failed", &icons::CIRCLE_X, k.c.danger.icon),
        None => ("Not run", &icons::DASH, k.c.text_weak),
    };
    row()
        .key(format!("proof-{i}"))
        .w_full()
        .gap(16.0)
        .items_start()
        .px(16.0)
        .py(12.0)
        .border_sides([if i == 0 { 0.0 } else { 1.0 }, 0.0, 0.0, 0.0], ui::rule())
        .child(text(expected.to_string()).font(BODY).color(strong()).wrap().grow(1.0).w(1.0))
        .child(text(observed.to_string()).font(BODY).color(if passed.is_some() { base() } else { weak() }).wrap().grow(1.0).w(1.0))
        .child(row().w(96.0).justify_end().child(ui::pill(glyph, label, color)))
}

pub fn view(s: &App, cx: &mut Cx<App>, id: &str, _layer: &mut Vec<El>) -> El {
    let k = kit();
    let Some(w) = s.d.work.get(id) else { return col() };
    let project = s.d.project(&w.project).map(|p| p.name.clone()).unwrap_or_default();
    let phase = s.d.phase(w);
    let pr = s.d.pr(w);
    let steps = s.d.proof(w);
    let unchecked = s.d.unchecked(w);
    let passed = steps.iter().filter(|p| p.passed == Some(true)).count();
    let not_run = steps.iter().filter(|p| p.passed.is_none()).count();

    let goal = col()
        .w_full()
        .gap(8.0)
        .child(row().gap(8.0).items_center().child(ui::label("Goal")).child(ui::id_label(&short_id(&w.id))).child(ui::chip(&icons::TASK, &format!("{} · {}", w.priority, w.kind))))
        .child(text(plain(&w.text)).font(TITLE.lh(30.0)).color(strong()).wrap())
        .child(text(plain(&w.description)).font(BODY).color(base()).wrap().max_lines(3));

    let mut links = row().gap(4.0).items_center();
    if let Some(n) = &pr {
        links = links.child(Button::new(format!("Open PR #{n}")).variant(ButtonVariant::Ghost).icon(&icons::GITHUB).on_press(|s: &mut App| crate::act(s, "Open PR")).build(k));
    }
    links = links.child(Button::new("Open in px0").variant(ButtonVariant::Ghost).icon(&icons::ARROW_UP_RIGHT).on_press(|s: &mut App| crate::act(s, "Open in px0")).build(k));

    let waiting = if phase == 4 {
        let c = col()
            .w_full()
            .gap(14.0)
            .padding(20.0)
            .child(row().gap(8.0).items_center().child(ui::pill(&icons::CHECKLIST, "Waiting on you", k.c.warning.icon)).child(text(format!("submitted {} ago", s.d.ago(&w.timeline.iter().rev().find(|t| t.state.as_deref() == Some("in_review")).map(|t| t.at.clone()).unwrap_or_default()))).font(META).color(weak())))
            .child(text("Does this do what was asked?".to_string()).font(HEAD.lh(22.0)).color(strong()))
            .child(
                col()
                    .w_full()
                    .gap(4.0)
                    .pl(12.0)
                    .border_sides([0.0, 0.0, 0.0, 2.0], k.c.border_strong)
                    .child(text("What was asked".to_string()).font(META_MED).color(weak()))
                    .child(text(plain(&w.acceptance)).font(BODY).color(base()).wrap()),
            )
            .child(
                row()
                    .w_full()
                    .gap(8.0)
                    .items_center()
                    .child(Button::new("Approve outcome").variant(ButtonVariant::Contrast).shortcut("A").on_press(|s: &mut App| crate::act(s, "Approved outcome")).build(k))
                    .child(ui::ghost_keys(&["A"]))
                    .child(Button::new("Ask for changes").variant(ButtonVariant::Outline).shortcut("C").on_press(|s: &mut App| crate::act(s, "Ask for changes")).build(k))
                    .child(ui::ghost_keys(&["C"])),
            )
            .child(links)
            .child(row().gap(6.0).items_center().child(icon(&icons::INFO, 13.0).color(weak())).child(text("Approving accepts the outcome. Merging is a separate step the lead takes after.".to_string()).font(META).color(weak())));
        col().w_full().radius(14.0).fill(k.c.surface_raised).border(1.0, k.c.warning.icon.alpha(0.4)).child(c)
    } else {
        ui::card(col().w_full().padding(16.0).gap(8.0).child(text(format!("{}'s move. Nothing waits on you yet.", s.d.whose_move(w))).font(BODY_MED).color(strong())).child(links))
    };

    let mut table = col().w_full().child(
        row()
            .w_full()
            .gap(16.0)
            .px(16.0)
            .py(10.0)
            .fill(k.c.surface_raised)
            .child(text("Expected".to_string()).font(META_MED).color(weak()).grow(1.0).w(1.0))
            .child(text("Observed".to_string()).font(META_MED).color(weak()).grow(1.0).w(1.0))
            .child(text("Result".to_string()).font(META_MED).color(weak()).w(96.0)),
    );
    for (i, p) in steps.iter().enumerate() {
        table = table.child(proof_row(i, &p.expected, &p.observed, p.passed));
    }
    let proof = col()
        .w_full()
        .gap(10.0)
        .child(ui::section_head("Proof", None, Some(text(format!("{passed} passed · {not_run} not run · from the workers' logged results")).font(META).color(weak()))))
        .child(col().w_full().radius(12.0).fill(k.c.surface_base).border(1.0, ui::rule()).child(table));

    let mut gaps = col().w_full().gap(8.0).child(ui::section_head("What wasn't checked", Some(unchecked.len() + not_run), None));
    for p in steps.iter().filter(|p| p.passed.is_none()) {
        gaps = gaps.child(gap_line(&format!("No recorded run shows: {}", p.expected)));
    }
    for u in &unchecked {
        gaps = gaps.child(gap_line(&format!("Raised after it was submitted: {u}")));
    }
    if unchecked.is_empty() && not_run == 0 {
        gaps = gaps.child(text("Every expected step has a recorded result.".to_string()).font(BODY).color(weak()));
    }

    let body = col()
        .w_full()
        .max_w(860.0)
        .gap(26.0)
        .px(28.0)
        .pt(22.0)
        .pb(48.0)
        .child(goal)
        .child(ui::card(col().w_full().padding(18.0).child(ui::stepper(phase, &s.d.whose_move(w)))))
        .child(waiting)
        .child(proof)
        .child(gaps);
    let main = col().key(format!("work-{id}")).w_full().h_full().items_center().scroll(ScrollSpec { stick_bottom: false }).child(body);

    let mut people: Vec<String> = Vec::new();
    let mut seen = BTreeSet::new();
    for t in w.timeline.iter().rev() {
        let n = s.d.name_of(&t.actor);
        if seen.insert(n.clone()) {
            people.push(n);
        }
    }
    let mut who = col().w_full().gap(8.0).child(ui::label("Who's working"));
    for (i, n) in people.iter().take(4).enumerate() {
        let role = if Some(n) == w.assignee.as_ref().map(|a| s.d.name_of(a)).as_ref() { "Holds it" } else if i == 0 { "Last to act" } else { "Worked on it" };
        who = who.child(row().w_full().gap(8.0).items_center().child(ui::avatar(n, 20.0)).child(text(n.clone()).font(META_MED).color(strong()).truncate().shrink()).child(ui::spacer()).child(text(role.to_string()).font(META).color(weak())));
    }
    let rail = col()
        .key("work-rail")
        .w_full()
        .h_full()
        .scroll(ScrollSpec { stick_bottom: false })
        .child(col().w_full().gap(14.0).px(16.0).pt(14.0).pb(24.0).child(who).child(el().h(1.0).w_full().fill(ui::rule())).child(ui::label("Timeline")).child(crate::session::timeline(s, w, 12)));

    let pkey = w.project.clone();
    let toolbar = Toolbar::new()
        .leading(
            row()
                .gap(14.0)
                .items_center()
                .child(ui::avatar(&project, 18.0))
                .child(row().on_press(move |s: &mut App| go(s, Screen::Project(pkey.clone()))).child(ui::crumb(&[(&project, false), ("Work", false), (&ui::short_title(&plain(&w.text), 34), true)]))),
        )
        .trailing(ui::needs_button(s));
    ui::frame(cx, s, Some(ui::sidebar(s, Some(&w.project), Some(id))), main, Some(rail), toolbar)
}

fn gap_line(t: &str) -> El {
    let k = kit();
    row().w_full().gap(10.0).items_start().child(row().pt(3.0).child(icon(&icons::WARNING, 14.0).color(k.c.warning.icon))).child(text(t.to_string()).font(BODY).color(base()).wrap().grow(1.0).w(1.0))
}
