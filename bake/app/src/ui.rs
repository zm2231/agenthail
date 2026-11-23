use std::collections::BTreeMap;

use feed::catalog::{self, PHASES, Session, State};
use rune::tok;
use rune::{Color, Cx, El, ScrollSpec, Tx, col, el, row, text, text_input};

use crate::theme::{BODY, BODY_BOLD, HEADING, LABEL, META, META_BOLD, TITLE, accent, attention, done, side};
use crate::{App, Screen, open_project, select, send, session, transcript};

pub fn palette_matches<'a>(s: &'a App, query: &str) -> Vec<&'a Session> {
    s.palette_order
        .iter()
        .filter_map(|id| session(s, id))
        .filter(|x| query.is_empty() || x.title().to_lowercase().contains(query) || x.project_name().to_lowercase().contains(query) || x.summary().to_lowercase().contains(query))
        .take(10)
        .collect()
}

pub fn ordered(s: &App) -> Vec<&Session> {
    let mut list: Vec<&Session> = s.sessions.iter().filter(|x| x.visible(s.now)).collect();
    list.sort_by(|a, b| a.state(s.now).cmp(&b.state(s.now)).then(b.last_active.cmp(&a.last_active)));
    list
}

fn glyph(state: State) -> (&'static str, Color) {
    let t = tok::theme(true);
    match state {
        State::NeedsYou => ("●", attention()),
        State::Working => ("◐", accent()),
        State::Done => ("✓", done()),
        State::Idle => ("○", t.text_secondary.alpha(0.6)),
    }
}

fn label(text_value: &str) -> El {
    let t = tok::theme(true);
    text(text_value.to_uppercase()).font(LABEL).color(t.text_secondary.alpha(0.8))
}

fn spacer() -> El {
    el().grow(1.0)
}

fn session_row(s: &App, x: &Session) -> El {
    let t = tok::theme(true);
    let state = x.state(s.now);
    let (g, color) = glyph(state);
    let selected = s.selected.as_deref() == Some(x.id.as_str()) && s.screen == Screen::Session;
    let id = x.id.clone();
    let branch = x.checkout.branch.clone().unwrap_or_default();
    let place = if branch.is_empty() { format!("{} · {}", x.project_name(), x.host.name) } else { format!("{} · {}", x.project_name(), branch) };
    let summary_color = if state == State::NeedsYou { attention() } else { t.text_secondary };
    let mut card = col()
        .key(format!("row-{}", x.id))
        .w_full()
        .px(12.0)
        .py(8.0)
        .gap(3.0)
        .radius(8.0)
        .on_press(move |s: &mut App| select(s, &id))
        .child(
            row()
                .w_full()
                .gap(8.0)
                .child(text(g).font(BODY).color(color))
                .child(text(x.title()).font(BODY_BOLD).color(t.text).truncate().grow(1.0))
                .child(text(catalog::ago(&x.last_active, s.now)).font(META).color(t.text_secondary)),
        )
        .child(text(place).font(META).color(t.text_secondary.alpha(0.75)).truncate().pl(20.0))
        .child(text(x.summary()).font(META).color(summary_color).wrap().max_lines(2).pl(20.0));
    if selected {
        card = card.fill(t.card);
    }
    card
}

fn group(title: &str, count: usize, rows: Vec<El>, project: Option<String>) -> El {
    let t = tok::theme(true);
    let mut head = row().w_full().px(12.0).pt(14.0).pb(4.0).gap(6.0).child(label(title)).child(text(count.to_string()).font(LABEL).color(t.text_secondary.alpha(0.6))).child(spacer());
    if let Some(key) = project {
        head = head.key(format!("project-{key}")).on_press(move |s: &mut App| open_project(s, &key)).child(text("Overview ›").font(META).color(accent()));
    }
    col().w_full().child(head).children(rows)
}

fn sidebar(s: &App) -> El {
    let t = tok::theme(true);
    let list = ordered(s);
    let needs = list.iter().filter(|x| x.state(s.now) == State::NeedsYou).count();
    let working = list.iter().filter(|x| x.state(s.now) == State::Working).count();
    let mode = if s.by_project { "By project" } else { "By state" };
    let header = col()
        .w_full()
        .px(14.0)
        .pt(38.0)
        .pb(10.0)
        .gap(8.0)
        .child(row().w_full().child(text("Agenthail").font(TITLE).color(t.text)).child(spacer()).child(
            text(mode).font(META).color(accent()).key("mode").on_press(|s: &mut App| s.by_project = !s.by_project),
        ))
        .child(
            row()
                .w_full()
                .gap(8.0)
                .px(10.0)
                .py(6.0)
                .radius(8.0)
                .fill(t.card)
                .key("search")
                .on_press_tx(|s: &mut App, tx: &mut Tx| crate::open_palette(s, tx))
                .child(text("Jump to a session").font(META).color(t.text_secondary))
                .child(spacer())
                .child(text("⌘K").font(META).color(t.text_secondary.alpha(0.6))),
        )
        .child(
            row()
                .gap(12.0)
                .child(text(format!("● {needs} need you")).font(META_BOLD).color(if needs > 0 { attention() } else { t.text_secondary }))
                .child(text(format!("◐ {working} working")).font(META).color(accent()))
                .child(spacer())
                .child(text("⌘J next").font(META).color(t.text_secondary.alpha(0.6))),
        );
    let mut groups: Vec<El> = Vec::new();
    if s.by_project {
        let mut by: BTreeMap<String, (String, Vec<&Session>)> = BTreeMap::new();
        for x in &list {
            by.entry(x.project_name()).or_insert_with(|| (x.project_key(), Vec::new())).1.push(x);
        }
        let mut projects: Vec<(String, String, Vec<&Session>)> = by.into_iter().map(|(n, (k, v))| (n, k, v)).collect();
        projects.sort_by_key(|(_, _, v)| v.iter().map(|x| x.state(s.now)).min());
        for (name, key, rows) in projects {
            let count = rows.len();
            groups.push(group(&name, count, rows.into_iter().map(|x| session_row(s, x)).collect(), (!key.is_empty()).then_some(key)));
        }
    } else {
        for state in [State::NeedsYou, State::Working, State::Done, State::Idle] {
            let rows: Vec<El> = list.iter().filter(|x| x.state(s.now) == state).map(|x| session_row(s, x)).collect();
            if !rows.is_empty() {
                groups.push(group(state.label(), rows.len(), rows, None));
            }
        }
    }
    let scroller = col().key("sidebar-list").w_full().grow(1.0).min_h(0.0).scroll(ScrollSpec { stick_bottom: false }).child(col().w_full().px(6.0).pb(12.0).children(groups));
    let footer = row().w_full().px(14.0).py(8.0).child(text(s.status.clone()).font(META).color(t.text_secondary.alpha(0.6)));
    col().w(300.0).h_full().fill(side()).child(header).child(scroller).child(footer)
}

fn pill(value: &str, color: Color) -> El {
    row().px(8.0).py(3.0).radius(10.0).fill(color.alpha(0.16)).child(text(value.to_string()).font(META_BOLD).color(color))
}

fn session_header(s: &App, x: &Session) -> El {
    let t = tok::theme(true);
    let state = x.state(s.now);
    let (_, color) = glyph(state);
    let label_value = if state == State::NeedsYou || x.display.label.is_empty() { state.label().to_string() } else { x.display.label.clone() };
    let branch = x.checkout.branch.clone().unwrap_or_else(|| "no branch".into());
    col()
        .w_full()
        .px(28.0)
        .pt(30.0)
        .pb(12.0)
        .gap(6.0)
        .child(row().w_full().gap(10.0).child(text(x.title()).font(HEADING).color(t.text)).child(pill(&label_value, color)).child(spacer()).child(text(format!("{} · {}", x.surface, x.host.name)).font(META).color(t.text_secondary)))
        .child(text(format!("{} · {}{}", x.project_name(), branch, if x.checkout.dirty { " · uncommitted changes" } else { "" })).font(META).color(t.text_secondary))
}

fn waiting_card(x: &Session) -> El {
    let t = tok::theme(true);
    let question = x.attention.as_ref().map(|a| a.reason.clone()).filter(|r| !r.is_empty()).unwrap_or_else(|| x.summary());
    let card = col()
        .w_full()
        .max_w(760.0)
        .px(16.0)
        .py(12.0)
        .gap(6.0)
        .radius(12.0)
        .fill(attention().alpha(0.10))
        .border(1.0, attention().alpha(0.35))
        .child(label("Waiting on you"))
        .child(text(question).font(BODY_BOLD).color(t.text).wrap())
        .child(text("Reply in the box below, or press ⌘J for the next session that needs you.").font(META).color(t.text_secondary));
    col().w_full().px(28.0).pb(8.0).child(card)
}

fn composer(s: &App, x: &Session) -> El {
    let t = tok::theme(true);
    let input = text_input(&s.composer, BODY)
        .key("composer")
        .w_full()
        .min_h(44.0)
        .color(t.text)
        .placeholder(format!("Message {}", x.title()), t.text_secondary.alpha(0.6))
        .on_edit(|s: &mut App| &mut s.composer)
        .on_submit(|s: &mut App, tx: &mut Tx| send(s, tx));
    col()
        .w_full()
        .px(20.0)
        .pb(16.0)
        .pt(6.0)
        .gap(4.0)
        .child(col().w_full().px(12.0).py(8.0).radius(12.0).fill(t.card).border(1.0, t.card_border).child(input))
        .child(row().w_full().px(6.0).child(text(s.send_status.clone()).font(META).color(t.text_secondary)).child(spacer()).child(text("Return sends · Shift-Return new line").font(META).color(t.text_secondary.alpha(0.6))))
}

fn phase_strip(state: &str) -> El {
    let t = tok::theme(true);
    let current = catalog::phase(state);
    row().gap(4.0).children(PHASES.iter().enumerate().map(|(i, name)| {
        let (fg, bg) = if i == current { (t.text, accent().alpha(0.28)) } else if i < current { (t.text_secondary, t.card) } else { (t.text_secondary.alpha(0.5), t.bg) };
        row().px(7.0).py(3.0).radius(6.0).fill(bg).child(text(name.to_string()).font(META).color(fg))
    }))
}

fn section(title: &str, body: El) -> El {
    col().w_full().gap(8.0).pt(16.0).child(label(title)).child(body)
}

fn rail(s: &App, x: &Session) -> El {
    let t = tok::theme(true);
    let mut parts: Vec<El> = Vec::new();
    if let Some(work) = x.held_work.first() {
        let item = s.items.get(&work.item_id);
        let state = item.map(|i| i.state.clone()).unwrap_or_else(|| work.state.clone());
        let mut body = col()
            .w_full()
            .gap(8.0)
            .child(text(format!("{} · {}", work.item_id, item.map(|i| format!("{} {}", i.priority, i.kind)).unwrap_or_default())).font(META).color(t.text_secondary))
            .child(text(work.text.clone()).font(BODY_BOLD).color(t.text).wrap())
            .child(phase_strip(&state));
        if let Some(i) = item.filter(|i| !i.acceptance.is_empty()) {
            body = body.child(col().gap(3.0).child(text("Expected").font(META_BOLD).color(done())).child(text(i.acceptance.clone()).font(META).color(t.text_secondary).wrap().max_lines(6)));
        }
        let latest = item.and_then(|i| i.timeline.iter().rev().find(|e| e.kind == "log").and_then(|e| e.text.clone()));
        if let Some(observed) = latest {
            body = body.child(col().gap(3.0).child(text("Latest").font(META_BOLD).color(accent())).child(text(observed).font(META).color(t.text_secondary).wrap().max_lines(6)));
        }
        parts.push(section("Work", body));
        if let Some(i) = item {
            let entries = i.timeline.iter().rev().take(7).map(|e| {
                let what = match (&e.kind[..], &e.state) {
                    ("state", Some(st)) => format!("Moved to {}", st.replace('_', " ")),
                    ("created", _) => "Created".to_string(),
                    _ => e.text.clone().unwrap_or_default(),
                };
                row()
                    .w_full()
                    .gap(8.0)
                    .child(text("•").font(META).color(t.text_secondary))
                    .child(text(what).font(META).color(t.text).truncate().grow(1.0))
                    .child(text(catalog::ago(&e.at, s.now)).font(META).color(t.text_secondary))
            });
            parts.push(section("Timeline", col().w_full().gap(6.0).children(entries)));
        }
    }
    if let Some(plan) = &x.plan {
        let total = plan.total.max(1) as f32;
        let bar = row()
            .w_full()
            .h(6.0)
            .radius(3.0)
            .fill(t.card)
            .child(el().w(248.0 * plan.done as f32 / total).h(6.0).radius(3.0).fill(done()));
        parts.push(section("Progress", col().w_full().gap(6.0).child(text(format!("{} of {} tasks done", plan.done, plan.total)).font(META).color(t.text)).child(bar)));
    }
    if !x.prs.is_empty() {
        parts.push(section(
            "Pull requests",
            col().w_full().gap(6.0).children(x.prs.iter().map(|p| row().w_full().gap(8.0).child(text(format!("#{}", p.number)).font(META_BOLD).color(accent())).child(text(p.title.clone()).font(META).color(t.text).truncate()))),
        ));
    }
    let facts = [
        ("Agent", x.surface.clone()),
        ("Host", x.host.name.clone()),
        ("Branch", x.checkout.branch.clone().unwrap_or_else(|| "none".into())),
        ("Queued", x.queue_count.to_string()),
    ];
    parts.push(section(
        "Session",
        col().w_full().gap(5.0).children(facts.into_iter().map(|(k, v)| row().w_full().child(text(k).font(META).color(t.text_secondary)).child(spacer()).child(text(v).font(META).color(t.text).truncate()))),
    ));
    col().key("rail").w(320.0).h_full().fill(side()).px(16.0).pt(30.0).scroll(ScrollSpec { stick_bottom: false }).children(parts)
}

fn session_screen(s: &App) -> El {
    let t = tok::theme(true);
    let Some(id) = s.selected.clone() else { return col().grow(1.0).h_full().child(text("No session selected").color(t.text_secondary)) };
    let Some(x) = session(s, &id) else { return col().grow(1.0).h_full() };
    let mut center = col().grow(1.0).h_full().min_w(0.0).child(session_header(s, x));
    if x.state(s.now) == State::NeedsYou {
        center = center.child(waiting_card(x));
    }
    center = center.child(col().w_full().h(1.0).fill(t.separator));
    let empty = s.panes.get(&id).is_none_or(|p| p.blocks.is_empty());
    if empty {
        let status = s.panes.get(&id).map(|p| p.status.clone()).unwrap_or_default();
        let note = if matches!(status.as_str(), "" | "loading" | "live") { "Loading the transcript…".to_string() } else { status };
        center = center.child(col().w_full().grow(1.0).items_center().pt(60.0).child(text(note).font(META).color(t.text_secondary)));
    } else {
        center = center.child(transcript::view(s, &id));
    }
    center = center.child(composer(s, x));
    row().grow(1.0).h_full().child(center).child(rail(s, x))
}

fn board_row(s: &App, r: &catalog::BoardRow) -> El {
    let t = tok::theme(true);
    let holder = s.sessions.iter().find(|x| x.held_work.iter().any(|w| w.item_id == r.id)).map(|x| (x.id.clone(), x.title(), x.state(s.now)));
    let mut line = row()
        .w_full()
        .gap(10.0)
        .px(12.0)
        .py(8.0)
        .radius(8.0)
        .fill(t.card)
        .child(text(r.text.clone()).font(BODY).color(t.text).truncate().grow(1.0))
        .child(text(format!("{} {}", r.priority, r.kind)).font(META).color(t.text_secondary));
    if let Some((id, title, state)) = holder {
        let (g, color) = glyph(state);
        line = line.key(format!("board-{}", r.id)).on_press(move |s: &mut App| select(s, &id)).child(text(format!("{g} {title}")).font(META_BOLD).color(color));
    }
    line
}

fn project_screen(s: &App, key: &str) -> El {
    let t = tok::theme(true);
    let sessions: Vec<&Session> = ordered(s).into_iter().filter(|x| x.project_key() == key).collect();
    let name = sessions.first().map(|x| x.project_name()).unwrap_or_else(|| key.to_string());
    let needs: Vec<&Session> = sessions.iter().copied().filter(|x| x.state(s.now) == State::NeedsYou).collect();
    let mut body = col().w_full().max_w(900.0).px(32.0).pt(34.0).pb(32.0).gap(6.0).child(text(name).font(HEADING).color(t.text)).child(text(format!("{} sessions · {} need you", sessions.len(), needs.len())).font(META).color(t.text_secondary));
    let cards = needs.iter().map(|x| {
        let id = x.id.clone();
        col()
            .key(format!("needs-{}", x.id))
            .w_full()
            .px(14.0)
            .py(10.0)
            .gap(4.0)
            .radius(10.0)
            .fill(attention().alpha(0.10))
            .border(1.0, attention().alpha(0.3))
            .on_press(move |s: &mut App| select(s, &id))
            .child(row().w_full().child(text(x.title()).font(BODY_BOLD).color(t.text)).child(spacer()).child(text("Open ›").font(META).color(accent())))
            .child(text(x.summary()).font(META).color(t.text_secondary).wrap().max_lines(3))
    });
    body = body.child(section(&format!("Needs your attention · {}", needs.len()), col().w_full().gap(8.0).children(cards)));
    let active = sessions.iter().filter(|x| x.state(s.now) == State::Working).map(|x| session_row(s, x));
    body = body.child(section("Working now", col().w_full().gap(2.0).children(active)));
    match s.boards.get(key) {
        Some(rows) => {
            for (title, states) in [("In review", &["in_review"][..]), ("In progress", &["in_progress"][..]), ("Ready to start", &["open", "ready"][..])] {
                let picked: Vec<El> = rows.iter().filter(|r| states.contains(&r.state.as_str())).take(12).map(|r| board_row(s, r)).collect();
                if !picked.is_empty() {
                    let count = picked.len();
                    body = body.child(section(&format!("{title} · {count}"), col().w_full().gap(6.0).children(picked)));
                }
            }
        }
        None => body = body.child(section("Work", text("Reading this project's ledger…").font(META).color(t.text_secondary))),
    }
    col().key(format!("project-page-{key}")).grow(1.0).h_full().scroll(ScrollSpec { stick_bottom: false }).child(body)
}

fn palette(s: &App, edit: &rune::TextEdit) -> El {
    let t = tok::theme(true);
    let query = edit.text.to_lowercase();
    let matches = palette_matches(s, &query);
    let pick = s.palette_ix.min(matches.len().saturating_sub(1));
    let input = text_input(edit, BODY)
        .key("palette-input")
        .w_full()
        .color(t.text)
        .placeholder("Jump to a session, project or question", t.text_secondary.alpha(0.6))
        .on_edit(|s: &mut App| s.palette.as_mut().expect("palette open"))
        .on_input_key(|s: &mut App, key: &rune::KeyEvent, _: &mut Tx| match key.key {
            rune::Key::Down => {
                s.palette_ix += 1;
                true
            }
            rune::Key::Up => {
                s.palette_ix = s.palette_ix.saturating_sub(1);
                true
            }
            rune::Key::Escape => {
                s.palette = None;
                true
            }
            _ => false,
        })
        .on_submit(|s: &mut App, _: &mut Tx| {
            let query = s.palette.as_ref().map(|e| e.text.to_lowercase()).unwrap_or_default();
            let found: Vec<String> = palette_matches(s, &query).into_iter().map(|x| x.id.clone()).collect();
            if let Some(id) = found.get(s.palette_ix.min(found.len().saturating_sub(1))).cloned() {
                s.palette = None;
                select(s, &id);
            }
        });
    let rows = matches.iter().enumerate().map(|(i, x)| {
        let (g, color) = glyph(x.state(s.now));
        let id = x.id.clone();
        let mut r = row()
            .key(format!("pick-{}", x.id))
            .w_full()
            .gap(10.0)
            .px(12.0)
            .py(7.0)
            .radius(8.0)
            .on_press(move |s: &mut App| {
                s.palette = None;
                select(s, &id);
            })
            .child(text(g).font(BODY).color(color))
            .child(text(x.title()).font(BODY_BOLD).color(t.text).truncate().max_w(300.0))
            .child(text(x.project_name()).font(META).color(t.text_secondary).truncate().max_w(160.0))
            .child(text(x.summary()).font(META).color(t.text_secondary.alpha(0.7)).truncate().grow(1.0));
        if i == pick {
            r = r.fill(accent().alpha(0.18));
        }
        r
    });
    col()
        .w(640.0)
        .mt(70.0)
        .px(10.0)
        .py(10.0)
        .gap(4.0)
        .radius(14.0)
        .fill(t.card)
        .border(1.0, t.card_border)
        .child(col().w_full().px(8.0).py(6.0).child(input))
        .child(col().w_full().h(1.0).fill(t.separator))
        .children(rows)
}

pub fn view(s: &App, _: &mut Cx<App>) -> El {
    let t = tok::theme(true);
    let main = match &s.screen {
        Screen::Session => session_screen(s),
        Screen::Project(key) => project_screen(s, key),
    };
    let body = row().w_full().h_full().fill(t.bg).child(sidebar(s)).child(col().w(1.0).h_full().fill(t.separator)).child(main);
    match &s.palette {
        Some(edit) => col().w_full().h_full().fill(t.bg).child(row().w_full().h_full().child(col().w_full().h_full().items_center().fill(t.bg.alpha(0.6)).child(palette(s, edit)))),
        None => body,
    }
}
