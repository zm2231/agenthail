use std::collections::BTreeSet;

use rune::{Cx, El, ScrollSpec, col, el, icon, row, text};
use rune_kit::shell::Toolbar;
use rune_kit::{Button, ButtonVariant};
use rune_theme_opencode::icons;

use crate::data::{plain, short_id};
use crate::ui::{self, BODY, BODY_MED, HEAD, META, META_MED, TITLE, base, kit, strong, weak};
use crate::{App, Screen, go};

fn phase_badge(phase: usize) -> El {
    let k = kit();
    let color = if phase == 4 { k.c.warning.icon } else if phase == 5 { k.c.success.icon } else { k.c.text_accent };
    ui::pill(&icons::STATUS, crate::data::PHASES[phase.min(5)], color)
}

fn tile(i: usize, expected: &str, observed: &str, passed: Option<bool>) -> El {
    let k = kit();
    let (label, glyph, color) = match passed {
        Some(true) => ("Passed", &icons::CIRCLE_CHECK, k.c.success.icon),
        Some(false) => ("Failed", &icons::CIRCLE_X, k.c.danger.icon),
        None => ("Not run", &icons::DASH, k.c.text_weak),
    };
    let section = |t: &str| text(t.to_uppercase()).font(rune::TextStyle::new(10.5, 600.0)).color(weak());
    col()
        .key(format!("tile-{i}"))
        .w(380.0)
        .grow(1.0)
        .gap(8.0)
        .padding(14.0)
        .radius(12.0)
        .fill(k.c.surface_base)
        .border(1.0, if passed.is_none() { ui::rule() } else { color.alpha(0.3) })
        .child(row().w_full().items_center().child(text(format!("Check {}", i + 1)).font(META_MED).color(base())).child(ui::spacer()).child(ui::pill(glyph, label, color)))
        .child(section("Expected"))
        .child(text(expected.to_string()).font(BODY).color(strong()).wrap())
        .child(section("Observed").mt(4.0))
        .child(text(observed.to_string()).font(META.lh(18.0)).color(if passed.is_some() { base() } else { weak() }).wrap())
}

pub fn view(s: &App, cx: &mut Cx<App>, id: &str, _layer: &mut Vec<El>) -> El {
    let k = kit();
    let Some(w) = s.d.work.get(id) else { return col() };
    let project = s.d.project(&w.project).map(|p| p.name.clone()).unwrap_or_default();
    let phase = s.d.phase(w);
    let pr = s.d.pr(w);
    let commit = s.d.commit(w);
    let revision = commit.clone().map(|c| c.chars().take(9).collect::<String>()).unwrap_or_else(|| "the latest revision".into());
    let steps = s.d.proof(w);
    let unchecked = s.d.unchecked(w);
    let passed = steps.iter().filter(|p| p.passed == Some(true)).count();
    let not_run = steps.iter().filter(|p| p.passed.is_none()).count();

    let goal = col()
        .w_full()
        .gap(8.0)
        .child({
            let mut r = row().gap(8.0).items_center().child(phase_badge(phase)).child(ui::id_label(&short_id(&w.id))).child(ui::chip(&icons::TASK, &format!("{} · {}", w.priority, w.kind)));
            if let Some(c) = &commit {
                r = r.child(ui::chip(&icons::BRANCH, c));
            }
            if let Some(n) = &pr {
                r = r.child(ui::chip(&icons::GITHUB, &format!("PR #{n}")));
            }
            r
        })
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
            .child(text("Does this revision do what was agreed?".to_string()).font(HEAD.lh(22.0)).color(strong()))
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
                    .child(Button::new(format!("Approve outcome for {revision}")).variant(ButtonVariant::Contrast).shortcut("A").on_press(|s: &mut App| crate::act(s, "Approved outcome")).build(k))
                    .child(ui::ghost_keys(&["A"]))
                    .child(Button::new("Ask for changes").variant(ButtonVariant::Outline).shortcut("C").on_press(|s: &mut App| crate::act(s, "Ask for changes")).build(k))
                    .child(ui::ghost_keys(&["C"])),
            )
            .child(links)
            .child(row().gap(6.0).items_center().child(icon(&icons::INFO, 13.0).color(weak())).child(text(format!("Approval covers {revision} only. A new commit asks again. Merging is a separate step.").to_string()).font(META).color(weak())));
        col().w_full().radius(14.0).fill(k.c.surface_raised).border(1.0, k.c.warning.icon.alpha(0.4)).child(c)
    } else {
        ui::card(col().w_full().padding(16.0).gap(8.0).child(text(format!("{}'s move. Nothing waits on you yet.", s.d.whose_move(w))).font(BODY_MED).color(strong())).child(links))
    };

    let first_result = w.timeline.iter().rev().find(|t| t.kind == "log" && t.log_type.as_deref() == Some("result")).and_then(|t| t.text.clone()).unwrap_or_default();
    let brief_rows = [
        ("What was asked", plain(w.acceptance.split(": ").next().unwrap_or(&w.acceptance))),
        ("What was built", crate::data::clip_words(&plain(&first_result), 150)),
        ("Before you approve", format!("{passed} of {} checks have a recorded pass. {} raised after it was submitted.", steps.len(), if unchecked.is_empty() { "Nothing was".to_string() } else { format!("{} thing{} {}", unchecked.len(), if unchecked.len() == 1 { "" } else { "s" }, if unchecked.len() == 1 { "was" } else { "were" }) })),
    ];
    let mut brief = col().w_full().gap(2.0).padding(8.0);
    for (i, (t, b)) in brief_rows.iter().enumerate() {
        brief = brief.child(
            row()
                .w_full()
                .gap(14.0)
                .items_start()
                .padding(10.0)
                .radius(8.0)
                .child(text(format!("{}", i + 1)).font(META_MED).color(weak()).w(14.0))
                .child(col().grow(1.0).w(1.0).gap(3.0).child(text(t.to_uppercase()).font(rune::TextStyle::new(11.0, 600.0)).color(k.c.text_accent)).child(text(b.clone()).font(BODY).color(strong()).wrap())),
        );
    }
    let brief = col().w_full().gap(10.0).child(ui::section_head("Review brief", None, Some(text("read this before the evidence".to_string()).font(META).color(weak())))).child(ui::card(brief));
    let mut tiles = row().w_full().gap(12.0).flex_wrap().items_stretch();
    for (i, p) in steps.iter().enumerate() {
        tiles = tiles.child(tile(i, &p.expected, &p.observed, p.passed));
    }
    let proof = col()
        .w_full()
        .gap(10.0)
        .child(ui::section_head("Evidence", Some(steps.len()), Some(text(format!("{passed} passed · {not_run} not run · from the workers' logged results")).font(META).color(weak()))))
        .child(tiles);

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
        .child(brief)
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
