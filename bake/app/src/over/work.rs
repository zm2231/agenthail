use std::collections::{BTreeSet, HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};

use rune::{Cx, El, ScrollSpec, TextEdit, Tx, col, el, icon, row, text, text_input};
use rune_kit::shell::Toolbar;
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog};
use rune_theme_opencode::icons;
use serde_json::Value;

use feed::catalog::RespondError;
use feed::ledger::{self, Move};

use feed::derive::phase::{CHECK, SHIP, off_board, phase};
use feed::derive::proof::proof;
use feed::derive::work::{commit, pr, unchecked};

use crate::over::{PHASES, plain, short_id};
use crate::over::ui::{self, BODY, BODY_MED, HEAD, META, META_MED, TITLE, base, kit, strong, weak};
use crate::over::go;
use crate::{App, Screen};

fn phase_badge(phase: usize) -> El {
    let k = kit();
    let color = if phase == CHECK { k.c.warning.icon } else if phase == SHIP { k.c.success.icon } else { k.c.text_accent };
    ui::pill(&icons::STATUS, PHASES[phase.min(SHIP)], color)
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
    let Some(w) = s.ov.d.work.get(id) else { return col() };
    let project = s.ov.d.project(&w.project).map(|p| p.name.clone()).unwrap_or_default();
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
            let mut r = row().gap(8.0).items_center().child(match off_board(&w.state) { Some(word) => ui::pill(&icons::STATUS, word, weak()), None => phase_badge(phase) }).child(ui::id_label(&short_id(&w.id))).child(ui::chip(&icons::TASK, &format!("{} · {}", w.priority, w.kind)));
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

    let listed = crate::over::work_review(s, id);
    let review = listed.clone().unwrap_or_default();
    let mut links = row().gap(4.0).items_center();
    if let Some(p) = pr_of(s, w, &review, pr.as_deref()) {
        let label = if p.number > 0 { format!("Open PR #{}", p.number) } else { "Open PR".into() };
        links = links.child(Button::new(label).variant(ButtonVariant::Ghost).icon(&icons::GITHUB).on_press(move |_: &mut App| crate::open_link(&p.url)).build(k).key("work-open-pr"));
    }

    let waiting = if phase == CHECK {
        let held = listed.is_none().then(|| s.ov.inbox.inbox.as_ref().and_then(|i| feed::review::lead_of(&i.lead_handling, w))).flatten();
        let heading = held.map(|h| format!("With {}", s.ov.d.name_of(&h.held_by))).unwrap_or_else(|| "Waiting on you".into());
        let c = col()
            .w_full()
            .gap(14.0)
            .padding(20.0)
            .child(row().gap(8.0).items_center().child(ui::pill(&icons::CHECKLIST, &heading, if held.is_some() { k.c.text_accent } else { k.c.warning.icon })).child(text(format!("submitted {} ago", s.ov.d.ago(&w.submitted_at()))).font(META).color(weak())))
            .child(text("Does this revision do what was agreed?".to_string()).font(HEAD.lh(22.0)).color(strong()))
            .children((!w.acceptance.trim().is_empty()).then(|| {
                col()
                    .w_full()
                    .gap(4.0)
                    .pl(12.0)
                    .border_sides([0.0, 0.0, 0.0, 2.0], k.c.border_strong)
                    .child(text("What was asked".to_string()).font(META_MED).color(weak()))
                    .child(text(plain(&w.acceptance)).font(BODY).color(base()).wrap())
            }))
            .child(verdict_row(s, w, listed.as_ref(), &revision))
            .child(links)
            .child(row().gap(6.0).items_center().child(icon(&icons::INFO, 13.0).color(weak())).child(text(format!("Approval covers {revision} only. A new commit asks again. Merging is a separate step.").to_string()).font(META).color(weak())));
        col().w_full().radius(14.0).fill(k.c.surface_raised).border(1.0, k.c.warning.icon.alpha(0.4)).child(c)
    } else {
        ui::card(col().w_full().padding(16.0).gap(8.0).child(text(match s.ov.d.whose_move(w).as_str() {
            _ if w.state == "parked" => "Parked. It's off the board until someone unparks it.".to_string(),
            _ if w.state == "closed" => "Closed without shipping. Reopen it to put it back on the board.".to_string(),
            "You" => "Your move. You hold it.".to_string(),
            whose => format!("{whose}'s move. Nothing waits on you yet."),
        }).font(BODY_MED).color(strong())).child(links))
    };

    let first_result = w.timeline.iter().rev().find(|t| t.kind == "log" && t.log_type.as_deref() == Some("result")).and_then(|t| t.text.clone()).unwrap_or_default();
    let asked = plain(w.acceptance.split(": ").next().unwrap_or(&w.acceptance));
    let brief_rows: Vec<(&str, String)> = [
        ("What was asked", asked),
        ("What was built", crate::over::clip_words(&plain(&first_result), 150)),
        ("Before you approve", format!("{passed} of {} checks have a recorded pass. {} raised after it was submitted.", steps.len(), if unchecked.is_empty() { "Nothing was".to_string() } else { format!("{} thing{} {}", unchecked.len(), if unchecked.len() == 1 { "" } else { "s" }, if unchecked.len() == 1 { "was" } else { "were" }) })),
    ]
    .into_iter()
    .filter(|(_, b)| !b.trim().is_empty())
    .collect();
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
        .children(moves(s, w))
        .children(off_board(&w.state).is_none().then(|| ui::card(col().w_full().padding(18.0).child(ui::stepper(phase, &s.ov.d.whose_move(w))))))
        .child(waiting)
        .child(brief)
        .child(proof)
        .child(gaps);
    let main = col().key(format!("work-{id}")).w_full().h_full().items_center().scroll(ScrollSpec { stick_bottom: false }).child(body);

    let mut people: Vec<String> = Vec::new();
    let mut seen = BTreeSet::new();
    for t in w.timeline.iter().rev() {
        let n = s.ov.d.name_of(&t.actor);
        if seen.insert(n.clone()) {
            people.push(n);
        }
    }
    let mut who = col().w_full().gap(8.0).child(ui::label("Who's working"));
    for (i, n) in people.iter().take(4).enumerate() {
        let role = if Some(n) == w.assignee.as_ref().map(|a| s.ov.d.name_of(a)).as_ref() { "Holds it" } else if i == 0 { "Last to act" } else { "Worked on it" };
        who = who.child(row().w_full().gap(8.0).items_center().child(ui::avatar(n, 20.0)).child(text(n.clone()).font(META_MED).color(strong()).truncate().shrink()).child(ui::spacer()).child(text(role.to_string()).font(META).color(weak())));
    }
    let rail = col()
        .key("work-rail")
        .w_full()
        .h_full()
        .scroll(ScrollSpec { stick_bottom: false })
        .child(col().w_full().gap(14.0).px(16.0).pt(14.0).pb(24.0).child(who).child(el().h(1.0).w_full().fill(ui::rule())).child(ui::label("Timeline")).child(timeline(s, w, 12)));

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

pub fn pr_of(s: &App, w: &crate::over::Work, review: &feed::decisions::Decision, number: Option<&str>) -> Option<feed::catalog::Pr> {
    let holder = w.assignee.as_deref().and_then(|a| s.ov.d.session(a)).map(|x| x.prs.as_slice()).unwrap_or_default();
    let timeline = feed::review::timeline_pr(w);
    feed::review::pr_link(review.prs.iter().chain(timeline.iter()).chain(holder), number).cloned()
}

fn verdict_row(s: &App, w: &crate::over::Work, d: Option<&feed::decisions::Decision>, revision: &str) -> El {
    let k = kit();
    let missing = d.is_none();
    let fallback = feed::decisions::Decision::default();
    let d = d.unwrap_or(&fallback);
    let st = &s.ov.inbox;
    let ready = crate::over::inbox::reviewable(st, d);
    let busy = st.acting.contains(&d.id());
    let unconfirmed = st.unconfirmed.contains(&d.hold_key());
    let approve = if busy { "Sending…".to_string() } else if unconfirmed { "Sent, not confirmed".to_string() } else { format!("Approve outcome for {revision}") };
    let (da, dc) = (d.clone(), d.clone());
    let mut r = row()
        .w_full()
        .gap(8.0)
        .items_center()
        .flex_wrap()
        .child(Button::new(approve).variant(ButtonVariant::Contrast).shortcut("A").disabled(!ready).on_press(move |s: &mut App| crate::over::inbox::review(s, &da, feed::review::Verdict::Approve, "")).build(k).key("work-approve"))
        .child(ui::ghost_keys(&["A"]))
        .child(Button::new("Ask for changes").variant(ButtonVariant::Outline).control_size(ControlSize::Regular).shortcut("C").disabled(!ready).on_press(move |s: &mut App| crate::over::inbox::ask_changes(s, &dc)).build(k).key("work-changes"))
        .child(ui::ghost_keys(&["C"]));
    let note = if missing {
        Some(feed::review::unlisted_note(st.inbox.as_ref(), w, |id| s.ov.d.name_of(id)))
    } else {
        st.notes.get(&d.id()).filter(|(hold, _)| d.holds(hold)).map(|(_, n)| n.clone()).or_else(|| crate::over::inbox::too_old(st, d).then(feed::review::needs_newer_note))
    };
    if let Some(n) = note {
        r = r.child(text(n).font(META).color(ui::needs_color()).wrap());
    }
    r
}

fn gap_line(t: &str) -> El {
    let k = kit();
    row().w_full().gap(10.0).items_start().child(row().pt(3.0).child(icon(&icons::WARNING, 14.0).color(k.c.warning.icon))).child(text(t.to_string()).font(BODY).color(base()).wrap().grow(1.0).w(1.0))
}

fn clip(t: &str, n: usize) -> String {
    if t.chars().count() > n { format!("{}…", t.chars().take(n).collect::<String>()) } else { t.to_string() }
}

fn timeline(s: &App, w: &crate::over::Work, max: usize) -> El {
    let k = kit();
    let mut c = col().w_full().gap(0.0);
    let steps = feed::derive::timeline::timeline(w, max);
    let n = steps.len();
    for (i, step) in steps.iter().enumerate() {
        let t = step.entry;
        let dot = el().size(7.0, 7.0).radius(3.5).fill(if step.moved_back { k.c.warning.icon } else if t.kind == "state" { k.c.text_accent } else if t.kind == "comment" { k.c.info.icon } else { k.c.border_strong });
        let rail = col().w(7.0).h_full().items_center().child(dot.mt(5.0)).child(el().w(1.0).grow(1.0).fill(if i + 1 < n { ui::rule() } else { rune::Color::TRANSPARENT }));
        c = c.child(
            row()
                .w_full()
                .gap(10.0)
                .items_stretch()
                .child(rail)
                .child(
                    col()
                        .grow(1.0)
                        .shrink()
                        .min_w(0.0)
                        .gap(2.0)
                        .pb(12.0)
                        .child(row().gap(6.0).child(text(s.ov.d.name_of(&t.actor)).font(META_MED).color(base()).truncate().shrink()).child(text(s.ov.d.ago(&t.at)).font(META).color(weak())))
                        .child(text(clip(&step.what, 180)).font(META).color(weak()).wrap().max_lines(3)),
                ),
        );
    }
    c
}

type Done = (String, Move, Result<Value, RespondError>);

pub struct Words {
    item: String,
    title: String,
    m: Move,
    text: TextEdit,
}

pub struct State {
    tx: Sender<Done>,
    rx: Receiver<Done>,
    acting: HashSet<String>,
    notes: HashMap<String, (bool, String)>,
    sends: ledger::Sends,
    pub words: Option<Words>,
}

impl Default for State {
    fn default() -> Self {
        let (tx, rx) = channel();
        State { tx, rx, acting: HashSet::new(), notes: HashMap::new(), sends: ledger::Sends::default(), words: None }
    }
}

fn start(s: &mut App, id: &str, m: Move) {
    let Some(w) = s.ov.d.work.get(id) else { return };
    if !m.needs_words() {
        send(s, id, m, "");
        return;
    }
    let mut text = TextEdit::new(s.ov.work.sends.held_words(id, m).unwrap_or_default().to_string());
    text.focused = true;
    s.composer.focused = false;
    s.ov.work.words = Some(Words { item: id.into(), title: plain(&w.text), m, text });
}

fn changed_while_held(s: &App, n: &Words) -> bool {
    s.ov.work.sends.held_words(&n.item, n.m).is_some_and(|said| said != n.text.text.trim())
}

fn send_words(s: &mut App) {
    let Some(n) = &s.ov.work.words else { return };
    if ledger::words_problem(n.m, &n.text.text).is_some() || changed_while_held(s, n) {
        return;
    }
    let (id, m, words) = (n.item.clone(), n.m, n.text.text.trim().to_string());
    s.ov.work.words = None;
    send(s, &id, m, &words);
}

fn send(s: &mut App, id: &str, m: Move, words: &str) {
    let Some(w) = s.ov.d.work.get(id) else { return };
    let asking = s.ov.work.sends.held_words(id, m) == Some(words);
    if s.ov.work.acting.contains(id) || !(asking || ledger::offered(w).contains(&m)) || s.ov.work.sends.held(id).is_some_and(|h| h != m) {
        return;
    }
    post(s, id, m, words);
}

fn post(s: &mut App, id: &str, m: Move, words: &str) {
    let Some(w) = s.ov.d.work.get(id).cloned() else { return };
    let Some(client) = s.client.clone() else {
        s.ov.work.notes.insert(id.into(), (true, "Fixture mode: not sent".into()));
        return;
    };
    let key = s.ov.work.sends.key(id, m, words);
    s.ov.work.acting.insert(id.into());
    s.ov.work.notes.remove(id);
    let (tx, id, words) = (s.ov.work.tx.clone(), id.to_string(), words.to_string());
    std::thread::spawn(move || {
        let result = client.work_move(&w, m, &words, &key);
        let _ = tx.send((id, m, result));
    });
}

fn send_new(s: &mut App, id: &str, m: Move) {
    if s.ov.work.acting.contains(id) || !s.ov.d.work.get(id).is_some_and(|w| ledger::offered(w).contains(&m)) {
        return;
    }
    let Some(p) = s.ov.work.sends.forget(id, m) else { return };
    send(s, id, m, &p.words);
}

pub fn reopened(s: &mut App) {
    let clock = s.clock;
    s.ov.work.sends.wake(clock);
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    let clock = s.clock;
    while let Ok((id, m, result)) = s.ov.work.rx.try_recv() {
        changed = true;
        s.ov.work.acting.remove(&id);
        let note = s.ov.work.sends.answered(&id, m, &result, clock);
        s.ov.work.notes.insert(id, note);
        if result.as_ref().map_or_else(|e| e.maybe_delivered, |_| true) {
            crate::ask_overview(s, crate::Ask::Refresh);
        }
    }
    for (id, m, words) in s.ov.work.sends.due(clock) {
        if !s.ov.work.acting.contains(&id) {
            post(s, &id, m, &words);
            changed = true;
        }
    }
    changed
}

fn moves(s: &App, w: &crate::over::Work) -> Option<El> {
    let offered = ledger::offered(w);
    let note = s.ov.work.notes.get(&w.id);
    if offered.is_empty() && note.is_none() {
        return None;
    }
    let k = kit();
    let busy = s.ov.work.acting.contains(&w.id);
    let held = s.ov.work.sends.held(&w.id);
    let mut r = row().w_full().gap(8.0).items_center().flex_wrap();
    for m in offered {
        let id = w.id.clone();
        let variant = if matches!(m, Move::Claim | Move::Submit | Move::Reopen) { ButtonVariant::Outline } else { ButtonVariant::Ghost };
        r = r.child(Button::new(m.label()).variant(variant).control_size(ControlSize::Small).disabled(busy || held.is_some_and(|h| h != m)).on_press(move |s: &mut App| start(s, &id, m)).build(k).key(format!("work-move-{m:?}")));
    }
    if let Some(m) = held.filter(|m| !busy && !ledger::offered(w).contains(m)) {
        let (id, words) = (w.id.clone(), s.ov.work.sends.held_words(&w.id, m).unwrap_or_default().to_string());
        r = r.child(Button::new("Ask again").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).help("Asks Agenthail about the same send").on_press(move |s: &mut App| send(s, &id, m, &words)).build(k).key("work-ask-again"));
    }
    if let Some(m) = held.filter(|m| !busy && ledger::offered(w).contains(m)) {
        let id = w.id.clone();
        r = r.child(Button::new("Send as new").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).help("Sends it again as a fresh request, so it can show up twice if the first one landed").on_press(move |s: &mut App| send_new(s, &id, m)).build(k).key("work-send-new"));
    }
    if busy {
        r = r.child(text("Sending…".to_string()).font(META).color(weak()));
    } else if let Some((failed, n)) = note {
        r = r.child(text(n.clone()).font(META).color(if *failed { ui::needs_color() } else { base() }).wrap().shrink());
    }
    Some(r)
}

pub fn words_dialog(s: &App, layer: &mut Vec<El>) {
    let Some(n) = &s.ov.work.words else { return };
    let k = kit();
    let held = changed_while_held(s, n);
    let problem = ledger::words_problem(n.m, &n.text.text).or(held.then_some(ledger::CHANGED_WORDS));
    let (title, label, placeholder, yes, about) = match n.m {
        Move::Park => ("Park", "Why it waits", "What should happen before anyone picks this up?", "Park", "It leaves the board until someone unparks it."),
        _ => ("Comment on", "Comment", "Your note for whoever works on this", "Add comment", "It goes on the item's timeline, where its workers read it."),
    };
    let input = text_input(&n.text, BODY)
        .key("work-words-input")
        .label(label)
        .w_full()
        .min_h(72.0)
        .color(strong())
        .placeholder(placeholder, weak())
        .on_edit(|s: &mut App| &mut s.ov.work.words.as_mut().expect("the dialog is open").text)
        .on_submit(|s: &mut App, _: &mut Tx| send_words(s));
    let field = col().w_full().px(10.0).py(8.0).radius(8.0).fill(k.c.surface_base).border(1.0, ui::rule()).child(input);
    let buttons = row()
        .w_full()
        .gap(8.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut App| s.ov.work.words = None).build(k).key("work-words-cancel"))
        .child(Button::new(yes).variant(ButtonVariant::Contrast).control_size(ControlSize::Small).disabled(problem.is_some()).on_press(send_words).build(k).key("work-words-send"));
    let mut body = col().w_full().gap(10.0).px(16.0).pb(16.0).child(field);
    if held {
        body = body.child(text(ledger::CHANGED_WORDS.to_string()).font(META).color(ui::needs_color()).wrap().w_full());
    }
    let body = body.child(buttons);
    Dialog::new("work-words", format!("{title} {}", ui::short_title(&n.title, 48)))
        .description(about)
        .open(true)
        .fit()
        .width(460.0)
        .on_close(|s: &mut App| s.ov.work.words = None)
        .child(body)
        .build(k, layer);
}
