use std::collections::BTreeSet;

use rune::{Cx, El, ScrollSpec, col, el, icon, row, text};
use rune_kit::shell::Toolbar;
use rune_kit::{Button, ButtonVariant, Kit, KitCx};
use rune_theme_opencode::icons;

use feed::derive::phase::{CHECK, SHIP, phase};
use feed::derive::proof::proof;
use feed::derive::work::{commit, pr, unchecked};

use crate::data::{PHASES, plain, short_id};
use crate::ui::{self, BODY, BODY_MED, HEAD, META, META_MED, TITLE, base, strong, weak};
use crate::{App, Screen, go};

fn phase_badge(phase: usize, k: &Kit) -> El {
    let color = if phase == CHECK { k.c.warning.icon } else if phase == SHIP { k.c.success.icon } else { k.c.text_accent };
    ui::pill(&icons::STATUS, PHASES[phase.min(SHIP)], color)
}

fn tile(i: usize, expected: &str, observed: &str, passed: Option<bool>, k: &Kit) -> El {
    let (label, glyph, color) = match passed {
        Some(true) => ("Passed", &icons::CIRCLE_CHECK, k.c.success.icon),
        Some(false) => ("Failed", &icons::CIRCLE_X, k.c.danger.icon),
        None => ("Not run", &icons::DASH, k.c.text_weak),
    };
    let section = |t: &str| text(t.to_uppercase()).font(rune::TextStyle::new(10.5, 600.0)).color(weak(k));
    col()
        .key(format!("tile-{i}"))
        .w(380.0)
        .grow(1.0)
        .gap(8.0)
        .padding(14.0)
        .radius(12.0)
        .fill(k.c.surface_base)
        .border(1.0, if passed.is_none() { ui::rule(k) } else { color.alpha(0.3) })
        .child(row().w_full().items_center().child(text(format!("Check {}", i + 1)).font(META_MED).color(base(k))).child(ui::spacer()).child(ui::pill(glyph, label, color)))
        .child(section("Expected"))
        .child(text(expected.to_string()).font(BODY).color(strong(k)).wrap())
        .child(section("Observed").mt(4.0))
        .child(text(observed.to_string()).font(META.lh(18.0)).color(if passed.is_some() { base(k) } else { weak(k) }).wrap())
}

pub fn view(s: &App, cx: &mut Cx<App>, key: &feed::derive::WorkKey, _layer: &mut Vec<El>) -> El {
    let k = cx.kit();
    let Some(w) = s.d.work.get(key) else { return col() };
    let project = s.d.project(&w.project).map(|p| p.name.clone()).unwrap_or_default();
    let phase = phase(w);
    let pr = pr(w);
    let commit = commit(w);
    let revision = commit.clone().map(|c| c.chars().take(9).collect::<String>()).unwrap_or_else(|| "the latest revision".into());
    let steps = proof(w);
    let unchecked = unchecked(w);
    let passed = steps.iter().filter(|p| p.passed == Some(true)).count();
    let not_run = steps.iter().filter(|p| p.passed.is_none()).count();

    let goal = col()
        .w_full()
        .gap(8.0)
        .child({
            let mut r = row().gap(8.0).items_center().child(phase_badge(phase, &k)).child(ui::id_label(&short_id(&w.id), &k)).child(ui::chip(&icons::TASK, &format!("{} · {}", w.priority, w.kind), &k));
            if let Some(c) = &commit {
                r = r.child(ui::chip(&icons::BRANCH, c, &k));
            }
            if let Some(n) = &pr {
                r = r.child(ui::chip(&icons::GITHUB, &format!("PR #{n}"), &k));
            }
            r
        })
        .child(text(plain(&w.text)).font(TITLE.lh(30.0)).color(strong(&k)).wrap())
        .child(text(plain(&w.description)).font(BODY).color(base(&k)).wrap().max_lines(3));

    let mut links = row().gap(4.0).items_center();
    if let Some(n) = &pr {
        links = links.child(Button::new(format!("Open PR #{n}")).variant(ButtonVariant::Ghost).icon(&icons::GITHUB).on_press(|s: &mut App| crate::act(s, "Open PR")).build(&k));
    }
    links = links.child(Button::new("Open in px0").variant(ButtonVariant::Ghost).icon(&icons::ARROW_UP_RIGHT).on_press(|s: &mut App| crate::act(s, "Open in px0")).build(&k));

    let waiting = if phase == CHECK {
        let c = col()
            .w_full()
            .gap(14.0)
            .padding(20.0)
            .child(row().gap(8.0).items_center().child(ui::pill(&icons::CHECKLIST, "Waiting on you", k.c.warning.icon)).child(text(format!("submitted {} ago", s.d.ago(&w.submitted_at()))).font(META).color(weak(&k))))
            .child(text("Does this revision do what was agreed?".to_string()).font(HEAD.lh(22.0)).color(strong(&k)))
            .child(
                col()
                    .w_full()
                    .gap(4.0)
                    .pl(12.0)
                    .border_sides([0.0, 0.0, 0.0, 2.0], k.c.border_strong)
                    .child(text("What was asked".to_string()).font(META_MED).color(weak(&k)))
                    .child(text(plain(&w.acceptance)).font(BODY).color(base(&k)).wrap()),
            )
            .child(
                row()
                    .w_full()
                    .gap(8.0)
                    .items_center()
                    .child(Button::new(format!("Approve outcome for {revision}")).variant(ButtonVariant::Contrast).shortcut("A").on_press(|s: &mut App| crate::act(s, "Approved outcome")).build(&k))
                    .child(ui::ghost_keys(&["A"], &k))
                    .child(Button::new("Ask for changes").variant(ButtonVariant::Outline).shortcut("C").on_press(|s: &mut App| crate::act(s, "Ask for changes")).build(&k))
                    .child(ui::ghost_keys(&["C"], &k)),
            )
            .child(links)
            .child(row().gap(6.0).items_center().child(icon(&icons::INFO, 13.0).color(weak(&k))).child(text(format!("Approval covers {revision} only. A new commit asks again. Merging is a separate step.").to_string()).font(META).color(weak(&k))));
        col().w_full().radius(14.0).fill(k.c.surface_raised).border(1.0, k.c.warning.icon.alpha(0.4)).child(c)
    } else {
        ui::card(col().w_full().padding(16.0).gap(8.0).child(text(format!("{}'s move. Nothing waits on you yet.", s.d.whose_move(w))).font(BODY_MED).color(strong(&k))).child(links), &k)
    };

    let first_result = w.timeline.iter().rev().find(|t| t.kind == "log" && t.log_type.as_deref() == Some("result")).and_then(|t| t.text.clone()).unwrap_or_default();
    let brief_rows = [
        ("What was asked", plain(w.acceptance.split(": ").next().unwrap_or(&w.acceptance))),
        ("What was built", crate::data::clip_words(&plain(&first_result), 150)),
        ("Before you approve", if w.submitted_at().is_empty() { format!("{passed} of {} checks have a recorded pass. It hasn't been submitted yet.", steps.len()) } else { format!("{passed} of {} checks have a recorded pass. {} raised after it was submitted.", steps.len(), if unchecked.is_empty() { "Nothing was".to_string() } else { format!("{} thing{} {}", unchecked.len(), if unchecked.len() == 1 { "" } else { "s" }, if unchecked.len() == 1 { "was" } else { "were" }) }) }),
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
                .child(text(format!("{}", i + 1)).font(META_MED).color(weak(&k)).w(14.0))
                .child(col().grow(1.0).w(1.0).gap(3.0).child(text(t.to_uppercase()).font(rune::TextStyle::new(11.0, 600.0)).color(k.c.text_accent)).child(text(b.clone()).font(BODY).color(strong(&k)).wrap())),
        );
    }
    let brief = col().w_full().gap(10.0).child(ui::section_head("Review brief", None, Some(text("read this before the evidence".to_string()).font(META).color(weak(&k))), &k)).child(ui::card(brief, &k));
    let mut tiles = row().w_full().gap(12.0).flex_wrap().items_stretch();
    for (i, p) in steps.iter().enumerate() {
        tiles = tiles.child(tile(i, &p.expected, &p.observed, p.passed, &k));
    }
    let proof = col()
        .w_full()
        .gap(10.0)
        .child(ui::section_head("Evidence", Some(steps.len()), Some(text(format!("{passed} passed · {not_run} not run · from the workers' logged results")).font(META).color(weak(&k))), &k))
        .child(tiles);

    let mut gaps = col().w_full().gap(8.0).child(ui::section_head("What wasn't checked", Some(unchecked.len() + not_run), None, &k));
    for p in steps.iter().filter(|p| p.passed.is_none()) {
        gaps = gaps.child(gap_line(&format!("No recorded run shows: {}", p.expected), &k));
    }
    for u in &unchecked {
        gaps = gaps.child(gap_line(&format!("Raised after it was submitted: {u}"), &k));
    }
    if unchecked.is_empty() && not_run == 0 {
        gaps = gaps.child(text("Every expected step has a recorded result.".to_string()).font(BODY).color(weak(&k)));
    }

    let body = col()
        .w_full()
        .max_w(860.0)
        .gap(26.0)
        .px(28.0)
        .pt(22.0)
        .pb(48.0)
        .child(goal)
        .child(ui::card(col().w_full().padding(18.0).child(ui::stepper(phase, &s.d.whose_move(w), &k)), &k))
        .child(waiting)
        .child(brief)
        .child(proof)
        .child(gaps);
    let main = col().key(format!("work-{}-{}", key.ledger, key.id)).w_full().h_full().items_center().scroll(ScrollSpec { stick_bottom: false }).child(body);

    let mut people: Vec<String> = Vec::new();
    let mut seen = BTreeSet::new();
    for t in w.timeline.iter().rev() {
        let n = s.d.name_of(&t.actor);
        if seen.insert(n.clone()) {
            people.push(n);
        }
    }
    let mut who = col().w_full().gap(8.0).child(ui::label("Who's working", &k));
    for (i, n) in people.iter().take(4).enumerate() {
        let role = if Some(n) == w.assignee.as_ref().map(|a| s.d.name_of(a)).as_ref() { "Holds it" } else if i == 0 { "Last to act" } else { "Worked on it" };
        who = who.child(row().w_full().gap(8.0).items_center().child(ui::avatar(n, 20.0)).child(text(n.clone()).font(META_MED).color(strong(&k)).truncate().shrink()).child(ui::spacer()).child(text(role.to_string()).font(META).color(weak(&k))));
    }
    let rail = col()
        .key("work-rail")
        .w_full()
        .h_full()
        .scroll(ScrollSpec { stick_bottom: false })
        .child(col().w_full().gap(14.0).px(16.0).pt(14.0).pb(24.0).child(who).child(el().h(1.0).w_full().fill(ui::rule(&k))).child(ui::label("Timeline", &k)).child(crate::session::timeline(s, w, 12, &k)));

    let pkey = w.project.clone();
    let toolbar = Toolbar::new()
        .leading(
            row()
                .gap(14.0)
                .items_center()
                .child(ui::avatar(&project, 18.0))
                .child(row().on_press(move |s: &mut App| go(s, Screen::Project(pkey.clone()))).child(ui::crumb(&[(&project, false), ("Work", false), (&ui::short_title(&plain(&w.text), 34), true)], &k))),
        )
        .trailing(ui::needs_button(s, &k));
    ui::frame(cx, s, Some(ui::sidebar(s, Some(&w.project), None, &k)), main, Some(rail), toolbar)
}

fn gap_line(t: &str, k: &Kit) -> El {
    row().w_full().gap(10.0).items_start().child(row().pt(3.0).child(icon(&icons::WARNING, 14.0).color(k.c.warning.icon))).child(text(t.to_string()).font(BODY).color(base(k)).wrap().grow(1.0).w(1.0))
}
