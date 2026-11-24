use std::collections::{BTreeMap, BTreeSet};

use feed::catalog::{self, BoardRow, PHASES, Session, State};
use rune::{Color, Cx, El, Icon, ScrollSpec, SystemMaterial, TextStyle, Transition, Tx, col, el, icon, row, text, text_input};
use rune_kit::{Button, ButtonVariant, Keybind, KeybindVariant};

use crate::icons;
use crate::theme::{
    self, BODY, BODY_BOLD, HEADING, HERO, LABEL, META, META_BOLD, NUMBER, SPRING, TITLE, accent, accent_deep, attention, card, done, failed, faint, line, muted, raised, wash,
};
use crate::transcript::{self, session_avatar};
use crate::{App, Screen, Tab, answer_request, session_edits, short_path, show_changes, open_palette, open_project, select, send, send_text, session};

pub fn palette_matches<'a>(s: &'a App, query: &str) -> Vec<&'a Session> {
    s.palette_order
        .iter()
        .filter_map(|id| session(s, id))
        .filter(|x| query.is_empty() || x.title().to_lowercase().contains(query) || x.project_name().to_lowercase().contains(query) || x.summary().to_lowercase().contains(query))
        .take(9)
        .collect()
}

pub fn ordered(s: &App) -> Vec<&Session> {
    let mut list: Vec<&Session> = s.sessions.iter().filter(|x| x.visible(s.now)).collect();
    list.sort_by(|a, b| a.state(s.now).cmp(&b.state(s.now)).then(b.last_active.cmp(&a.last_active)));
    list
}

pub fn next_waiting(s: &mut App) {
    let waiting: Vec<String> = ordered(s).into_iter().filter(|x| x.state(s.now) == State::NeedsYou).map(|x| x.id.clone()).collect();
    let next = waiting.iter().skip_while(|id| Some(*id) != s.selected.as_ref()).nth(1).or(waiting.first()).cloned();
    if let Some(id) = next {
        select(s, &id);
    }
}

fn state_color(state: State) -> Color {
    match state {
        State::NeedsYou => attention(),
        State::Working => accent(),
        State::Done => done(),
        State::Idle => faint(),
    }
}

fn state_icon(state: State) -> &'static Icon {
    match state {
        State::NeedsYou => &icons::HAND,
        State::Working => &icons::BOLT,
        State::Done => &icons::CHECK_CIRCLE,
        State::Idle => &icons::CLOCK,
    }
}

fn spacer() -> El {
    el().grow(1.0)
}

fn label(value: &str) -> El {
    text(value.to_uppercase()).font(LABEL).color(faint())
}

fn chip(glyph: &'static Icon, value: impl Into<String>) -> El {
    row()
        .gap(5.0)
        .items_center()
        .px(8.0)
        .py(3.0)
        .radius(7.0)
        .fill(wash())
        .border(1.0, line())
        .child(icon(glyph, 12.0).color(faint()))
        .child(text(value.into()).font(META).color(muted()).truncate().max_w(220.0))
}

fn pill(glyph: &'static Icon, value: impl Into<String>, color: Color) -> El {
    row()
        .gap(5.0)
        .items_center()
        .px(10.0)
        .py(5.0)
        .radius(20.0)
        .fill(color.alpha(0.14))
        .border(1.0, color.alpha(0.30))
        .child(icon(glyph, 12.0).color(color))
        .child(text(value.into()).font(META_BOLD).color(color))
}

fn keys(k: &[&str]) -> El {
    Keybind::new(k).variant(KeybindVariant::Ghost).build(theme::kit())
}

fn status_mark(state: State) -> El {
    let color = state_color(state);
    let dot = el().size(10.0, 10.0).radius(5.0).fill(color).border(2.0, theme::card_deep());
    if state == State::NeedsYou { dot.shadow(0.0, 8.0, color.alpha(0.8)) } else { dot }
}

fn avatar_with_state(name: &str, state: State, size: f32) -> El {
    col().size(size, size).child(session_avatar(name, size)).child(status_mark(state).absolute(size - 9.0, size - 9.0))
}

fn session_row(s: &App, x: &Session) -> El {
    let state = x.state(s.now);
    let selected = s.selected.as_deref() == Some(x.id.as_str()) && s.screen == Screen::Session;
    let id = x.id.clone();
    let branch = x.checkout.branch.clone().unwrap_or_default();
    let place = if branch.is_empty() { format!("{} · {}", x.project_name(), x.host.name) } else { format!("{} · {}", x.project_name(), branch) };
    let summary_color = if state == State::NeedsYou { attention() } else { muted() };
    let summary = match x.summary() {
        t if state == State::NeedsYou && (t == "Working" || t == "Needs you") => "Waiting on you".to_string(),
        t => t,
    };
    let trailing = if state == State::Working {
        row().gap(5.0).items_center().child(el().size(6.0, 6.0).radius(3.0).fill(accent()).shadow(0.0, 7.0, accent().alpha(0.9))).child(text("live").font(META).color(accent()))
    } else {
        text(catalog::ago(&x.last_active, s.now)).font(META).color(faint())
    };
    let mut card_el = row()
        .key(format!("row-{}", x.id))
        .w_full()
        .gap(10.0)
        .items_start()
        .px(10.0)
        .py(9.0)
        .radius(12.0)
        .hover(Color::WHITE.alpha(0.06))
        .press_scale(0.985)
        .motion(SPRING)
        .on_press(move |s: &mut App| select(s, &id))
        .child(avatar_with_state(&x.title(), state, 30.0))
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(2.0)
                .child(row().w_full().gap(6.0).items_center().child(text(x.title()).font(BODY_BOLD).color(theme::text()).truncate().grow(1.0).min_w(0.0)).child(trailing))
                .child(row().gap(4.0).items_center().child(icon(&icons::BRANCH, 11.0).color(faint())).child(text(place).font(META).color(faint()).truncate().min_w(0.0)))
                .child(text(summary).font(META).color(summary_color).wrap().max_lines(2).pt(2.0)),
        );
    if selected {
        card_el = card_el.fill(Color::WHITE.alpha(0.11)).border(1.0, Color::WHITE.alpha(0.10)).shadow(4.0, 14.0, Color::BLACK.alpha(0.25));
    }
    card_el
}

fn group(title: &str, count: usize, glyph: &'static Icon, color: Color, rows: Vec<El>, project: Option<String>) -> El {
    let mut head = row()
        .w_full()
        .px(10.0)
        .pt(16.0)
        .pb(6.0)
        .gap(7.0)
        .items_center()
        .child(icon(glyph, 12.0).color(color))
        .child(label(title))
        .child(row().px(6.0).py(1.0).radius(8.0).fill(wash()).child(text(count.to_string()).font(LABEL).color(muted())))
        .child(spacer());
    if let Some(key) = project {
        head = head
            .key(format!("project-{key}"))
            .on_press(move |s: &mut App| open_project(s, &key))
            .child(row().gap(3.0).items_center().px(6.0).py(2.0).radius(6.0).hover(wash()).child(text("Overview").font(META).color(accent())).child(icon(&icons::CHEVRON_RIGHT, 11.0).color(accent())));
    }
    col().w_full().child(head).child(col().w_full().gap(2.0).children(rows))
}

fn segmented(s: &App) -> El {
    let option = |title: &'static str, glyph: &'static Icon, on: bool, project: bool| {
        let mut o = row()
            .key(format!("seg-{title}"))
            .grow(1.0)
            .gap(6.0)
            .items_center()
            .justify_center()
            .py(5.0)
            .radius(7.0)
            .motion(SPRING)
            .on_press(move |s: &mut App| s.by_project = project)
            .child(icon(glyph, 12.0).color(if on { theme::text() } else { faint() }))
            .child(text(title).font(META_BOLD).color(if on { theme::text() } else { faint() }));
        if on {
            o = o.fill(Color::WHITE.alpha(0.13)).shadow(1.0, 4.0, Color::BLACK.alpha(0.3));
        }
        o
    };
    row()
        .w_full()
        .padding(3.0)
        .radius(10.0)
        .fill(Color::BLACK.alpha(0.18))
        .child(option("Attention", &icons::INBOX, !s.by_project, false))
        .child(option("Projects", &icons::FOLDER, s.by_project, true))
}

fn stat(glyph: &'static Icon, count: usize, title: &str, color: Color) -> El {
    row()
        .grow(1.0)
        .gap(8.0)
        .items_center()
        .px(10.0)
        .py(8.0)
        .radius(10.0)
        .fill(color.alpha(0.10))
        .border(1.0, color.alpha(0.18))
        .child(col().size(24.0, 24.0).radius(7.0).items_center().justify_center().fill(color.alpha(0.18)).child(icon(glyph, 13.0).color(color)))
        .child(col().child(text(count.to_string()).font(TITLE).color(theme::text())).child(text(title.to_string()).font(META).color(muted())))
}

fn sidebar(s: &App) -> El {
    let list = ordered(s);
    let needs = list.iter().filter(|x| x.state(s.now) == State::NeedsYou).count();
    let working = list.iter().filter(|x| x.state(s.now) == State::Working).count();
    let search = row()
        .key("search")
        .w_full()
        .gap(8.0)
        .items_center()
        .px(10.0)
        .py(7.0)
        .radius(10.0)
        .fill(Color::BLACK.alpha(0.20))
        .border(1.0, Color::WHITE.alpha(0.07))
        .hover(Color::BLACK.alpha(0.28))
        .on_press_tx(|s: &mut App, tx: &mut Tx| open_palette(s, tx))
        .child(icon(&icons::SEARCH, 14.0).color(muted()))
        .child(text("Jump to a session").font(META).color(muted()))
        .child(spacer())
        .child(keys(&["⌘", "K"]));
    let header = col()
        .w_full()
        .px(12.0)
        .gap(10.0)
        .child(el().w_full().h(40.0).window_drag())
        .child(search)
        .child(row().w_full().gap(8.0).child(stat(&icons::HAND, needs, "need you", attention())).child(stat(&icons::BOLT, working, "working", accent())))
        .child(segmented(s));
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
            let top = rows.iter().map(|x| x.state(s.now)).min().unwrap_or(State::Idle);
            groups.push(group(&name, count, &icons::FOLDER, state_color(top), rows.into_iter().map(|x| session_row(s, x)).collect(), (!key.is_empty()).then_some(key)));
        }
    } else {
        for state in [State::NeedsYou, State::Working, State::Done, State::Idle] {
            let rows: Vec<El> = list.iter().filter(|x| x.state(s.now) == state).map(|x| session_row(s, x)).collect();
            if !rows.is_empty() {
                groups.push(group(state.label(), rows.len(), state_icon(state), state_color(state), rows, None));
            }
        }
    }
    let scroller = col().key("sidebar-list").w_full().grow(1.0).min_h(0.0).clip_fade(0.0, 10.0, 0.0, 18.0).scroll(ScrollSpec { stick_bottom: false }).child(col().w_full().px(6.0).pb(16.0).children(groups));
    let live = s.status == "live";
    let footer = row()
        .w_full()
        .px(16.0)
        .py(10.0)
        .gap(6.0)
        .items_center()
        .child(el().size(6.0, 6.0).radius(3.0).fill(if live { done() } else { attention() }))
        .child(text(if live { "Connected".to_string() } else { s.status.clone() }).font(META).color(faint()).truncate());
    col().w(300.0).h_full().child(header).child(scroller).child(footer)
}

fn header(s: &App, x: &Session) -> El {
    let state = x.state(s.now);
    let label_value = if state == State::NeedsYou || x.display.label.is_empty() { state.label().to_string() } else { x.display.label.clone() };
    let mut chips = row().gap(6.0).items_center().child(chip(&icons::FOLDER, x.project_name()));
    if let Some(branch) = &x.checkout.branch {
        chips = chips.child(chip(&icons::BRANCH, branch.clone()));
    }
    chips = chips.child(chip(&icons::SERVER, x.host.name.clone())).child(chip(&icons::SPARKLES, x.surface.clone()));
    if x.checkout.dirty {
        chips = chips.child(row().gap(5.0).items_center().px(8.0).py(3.0).radius(7.0).fill(attention().alpha(0.10)).child(el().size(6.0, 6.0).radius(3.0).fill(attention())).child(text("uncommitted").font(META).color(attention())));
    }
    row()
        .w_full()
        .gap(14.0)
        .items_center()
        .px(32.0)
        .pt(22.0)
        .pb(16.0)
        .window_drag()
        .child(avatar_with_state(&x.title(), state, 40.0))
        .child(col().grow(1.0).min_w(0.0).gap(7.0).child(text(x.title()).font(HEADING).color(theme::text()).truncate()).child(chips))
        .child(pill(state_icon(state), label_value, state_color(state)))
}

struct Choice {
    label: String,
    detail: String,
    recommended: bool,
    request: Option<(String, Vec<String>)>,
    reply: String,
}

fn plain(text: &str, max: usize) -> String {
    let t = text.replace("**", "").replace('`', "");
    if t.chars().count() > max { format!("{}…", t.chars().take(max).collect::<String>()) } else { t }
}

fn recent_answers(s: &App, x: &Session) -> Vec<String> {
    let Some(pane) = s.panes.get(&x.id) else { return Vec::new() };
    pane.blocks
        .iter()
        .rev()
        .filter_map(|b| match &b.kind {
            feed::Kind::Assistant(body) => Some(body.clone()),
            _ => None,
        })
        .take(2)
        .collect()
}

fn question_in(body: &str) -> Option<String> {
    let paragraphs: Vec<&str> = body.split("\n\n").map(str::trim).filter(|p| !p.is_empty()).collect();
    let pick = paragraphs.iter().rev().find(|p| p.contains('?')).or(paragraphs.last())?;
    Some(plain(pick, 320))
}

fn split_option(label: &str) -> (String, String) {
    let cut = [" — ", ", ", ": ", ". "].iter().filter_map(|sep| label.find(sep).map(|i| (i, sep.len()))).min_by_key(|(i, _)| *i);
    match cut {
        Some((i, len)) if i > 0 && i < 70 => (label[..i].trim().to_string(), label[i + len..].trim().to_string()),
        _ => (plain(label, 70), String::new()),
    }
}

fn waiting_choices(s: &App, x: &Session) -> (Option<String>, Vec<Choice>, bool) {
    if let Some(request) = s.requests.get(&x.id).and_then(|r| r.first()) {
        if let Some(q) = request.questions.as_ref().and_then(|q| q.first()).filter(|q| !q.multi_select && !q.options.is_empty()) {
            let choices = q
                .options
                .iter()
                .enumerate()
                .map(|(i, o)| Choice {
                    label: o.label.replace(" (Recommended)", "").replace(" (recommended)", ""),
                    detail: o.description.clone(),
                    recommended: q.suggested == Some(i) || o.label.to_lowercase().contains("recommended"),
                    request: Some((request.id.clone(), vec![o.id.clone()])),
                    reply: String::new(),
                })
                .collect();
            return (Some(q.question.clone()), choices, true);
        }
        let actions: Vec<(String, String)> = if request.actions.is_empty() {
            vec![("allow".into(), "Allow".into()), ("deny".into(), "Deny".into())]
        } else {
            request.actions.iter().map(|a| (a.id.clone(), a.title.clone())).collect()
        };
        let title = request.title.clone().or(request.description.clone()).unwrap_or_else(|| format!("{} wants to use {}", x.title(), request.tool));
        let choices = actions
            .into_iter()
            .map(|(id, label)| Choice { recommended: id == "allow", label, detail: String::new(), request: Some((request.id.clone(), vec![id])), reply: String::new() })
            .collect();
        return (Some(title), choices, true);
    }
    for body in recent_answers(s, x) {
        let options = feed::catalog::numbered_options(&body);
        if !options.is_empty() {
            let question = body.split("\n\n").map(str::trim).find(|p| p.contains('?') && feed::catalog::numbered_options(p).is_empty()).map(|p| plain(p, 320));
            let choices = options
                .iter()
                .enumerate()
                .map(|(i, label)| {
                    let (head, tail) = split_option(label);
                    let tail = tail.trim_start_matches("recommended").trim_start_matches(',').trim().to_string();
                    Choice {
                        recommended: label.to_lowercase().contains("recommended"),
                        reply: format!("Option {}: {}", i + 1, head),
                        label: head.replace(" — recommended", "").replace(" (recommended)", ""),
                        detail: tail,
                        request: None,
                    }
                })
                .collect();
            return (question, choices, false);
        }
    }
    (recent_answers(s, x).first().and_then(|b| question_in(b)), Vec::new(), false)
}

fn choice_el(i: usize, c: Choice, busy: bool) -> El {
    let accent_on = c.recommended;
    let mut card_el = row()
        .key(format!("choice-{i}"))
        .w_full()
        .gap(12.0)
        .items_center()
        .px(12.0)
        .py(10.0)
        .radius(12.0)
        .fill(if accent_on { attention().alpha(0.14) } else { Color::WHITE.alpha(0.05) })
        .border(1.0, if accent_on { attention().alpha(0.45) } else { Color::WHITE.alpha(0.10) })
        .hover(if accent_on { attention().alpha(0.22) } else { Color::WHITE.alpha(0.10) })
        .press_scale(0.985)
        .motion(SPRING)
        .child(col().size(24.0, 24.0).radius(7.0).items_center().justify_center().fill(Color::WHITE.alpha(0.08)).child(text((i + 1).to_string()).font(META_BOLD).color(theme::text())))
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(2.0)
                .child(text(c.label.clone()).font(BODY_BOLD).color(theme::text()).wrap())
                .children((!c.detail.is_empty()).then(|| text(c.detail.clone()).font(META).color(muted()).wrap().max_lines(2))),
        );
    if accent_on {
        card_el = card_el.child(row().px(7.0).py(2.0).radius(6.0).fill(attention().alpha(0.2)).child(text("Recommended").font(META_BOLD).color(attention())));
    }
    card_el = card_el.child(icon(&icons::ARROW_RIGHT, 13.0).color(faint()));
    if busy {
        return card_el.opacity(0.5);
    }
    match c.request {
        Some((request, choices)) => card_el.on_press(move |s: &mut App| answer_request(s, &request, choices.clone())),
        None => {
            let reply = c.reply.clone();
            card_el.on_press_tx(move |s: &mut App, tx: &mut Tx| {
                send_text(s, reply.clone(), tx);
                if let Some(id) = s.selected.clone() {
                    s.answered.insert(id);
                }
            })
        }
    }
}

fn waiting_card(s: &App, x: &Session) -> El {
    let (asked, choices, structured) = waiting_choices(s, x);
    let reason = x.attention.as_ref().map(|a| a.reason.clone()).filter(|r| !r.is_empty());
    let summary = Some(x.summary()).filter(|t| t != "Needs you" && t != "Working" && !t.is_empty());
    let question = asked.or(reason).or(summary).unwrap_or_else(|| "This session is waiting for you.".into());
    let reply = Button::new(if choices.is_empty() { "Reply" } else { "Something else" })
        .variant(if choices.is_empty() { ButtonVariant::Submit } else { ButtonVariant::Neutral })
        .icon(&icons::CORNER)
        .build(theme::kit())
        .key("waiting-reply")
        .on_press_tx(|_: &mut App, tx: &mut Tx| tx.focus("composer", true));
    let next = Button::new("Next waiting").variant(ButtonVariant::Ghost).icon(&icons::ARROW_RIGHT).build(theme::kit()).key("waiting-next").on_press(next_waiting);
    let busy = s.answering.is_some();
    let source = if choices.is_empty() {
        None
    } else if structured {
        Some("Answers the agent's question directly")
    } else {
        Some("Sends your pick as a reply")
    };
    let mut body = col()
        .grow(1.0)
        .min_w(0.0)
        .gap(8.0)
        .child(row().gap(8.0).items_center().child(text("WAITING ON YOU").font(LABEL).color(attention())).children(source.map(|t| text(t).font(META).color(faint()))))
        .child(text(question).font(TITLE).color(theme::text()).wrap());
    if !choices.is_empty() {
        body = body.child(col().w_full().gap(6.0).pt(4.0).children(choices.into_iter().enumerate().map(|(i, c)| choice_el(i, c, busy))));
    }
    body = body.child(row().gap(8.0).pt(4.0).items_center().child(reply).child(next).child(spacer()).child(keys(&["⌘", "J"])));
    let card_el = row()
        .w_full()
        .max_w(780.0)
        .gap(14.0)
        .items_start()
        .px(18.0)
        .py(16.0)
        .radius(16.0)
        .linear_gradient(135.0, [(0.0, attention().alpha(0.16)), (1.0, attention().alpha(0.04))])
        .border_gradient(1.0, attention().alpha(0.55), attention().alpha(0.12))
        .shadow(8.0, 28.0, attention().alpha(0.10))
        .transition(Transition::scale(0.97))
        .child(col().size(34.0, 34.0).radius(10.0).items_center().justify_center().fill(attention().alpha(0.18)).child(icon(&icons::HAND, 17.0).color(attention())))
        .child(body);
    col().w_full().items_center().px(32.0).pb(12.0).child(card_el)
}

fn composer(s: &App, x: &Session) -> El {
    let input = text_input(&s.composer, BODY)
        .key("composer")
        .w_full()
        .min_h(26.0)
        .color(theme::text())
        .placeholder(format!("Message {}", x.title()), faint())
        .on_edit(|s: &mut App| &mut s.composer)
        .on_submit(|s: &mut App, tx: &mut Tx| send(s, tx));
    let ready = !s.composer.text.trim().is_empty();
    let send_button = col()
        .key("send")
        .size(30.0, 30.0)
        .radius(15.0)
        .items_center()
        .justify_center()
        .linear_gradient(150.0, [(0.0, if ready { accent() } else { raised() }), (1.0, if ready { accent_deep() } else { raised() })])
        .shadow(2.0, 10.0, if ready { accent().alpha(0.45) } else { Color::TRANSPARENT })
        .press_scale(0.92)
        .motion(SPRING)
        .on_press_tx(|s: &mut App, tx: &mut Tx| send(s, tx))
        .child(icon(&icons::ARROW_UP, 15.0).color(if ready { Color::WHITE } else { faint() }));
    let status = if s.send_status.is_empty() { text("Return to send · Shift-Return for a new line").font(META).color(faint()) } else { text(s.send_status.clone()).font(META).color(muted()) };
    let box_el = col()
        .w_full()
        .max_w(780.0)
        .radius(18.0)
        .backdrop_blur(22.0)
        .fill(card().alpha(0.78))
        .border(1.0, Color::WHITE.alpha(0.10))
        .shadow(10.0, 34.0, Color::BLACK.alpha(0.45))
        .child(col().w_full().px(16.0).pt(13.0).pb(4.0).child(input))
        .child(row().w_full().px(10.0).pb(10.0).gap(8.0).items_center().child(chip(&icons::SPARKLES, x.surface.clone())).child(status).child(spacer()).child(send_button));
    col().w_full().items_center().px(28.0).pb(18.0).mt(-120.0).child(box_el)
}

fn counts(diff: &rune_kit::diff::FileDiff) -> (usize, usize) {
    let mut added = 0;
    let mut removed = 0;
    for hunk in &diff.hunks {
        for line in &hunk.lines {
            match line.kind {
                rune_kit::diff::LineKind::Added => added += 1,
                rune_kit::diff::LineKind::Removed => removed += 1,
                rune_kit::diff::LineKind::Context => {}
            }
        }
    }
    (added, removed)
}

struct FileTotal {
    path: String,
    added: usize,
    removed: usize,
    edits: usize,
}

fn file_totals(s: &App, id: &str) -> Vec<FileTotal> {
    let mut out: Vec<FileTotal> = Vec::new();
    for e in session_edits(s, id) {
        let path = short_path(s, id, &e.path);
        let (a, r) = counts(&rune_kit::diff::FileDiff::from_texts(path.clone(), &e.old, &e.new));
        match out.iter_mut().find(|f| f.path == path) {
            Some(f) => {
                f.added += a;
                f.removed += r;
                f.edits += 1;
            }
            None => out.push(FileTotal { path, added: a, removed: r, edits: 1 }),
        }
    }
    out
}

fn tabs(s: &App, id: &str) -> El {
    let files = file_totals(s, id).len();
    let tab = |key: &'static str, glyph: &'static Icon, title: String, on: bool| {
        let mut t = row()
            .key(key)
            .gap(6.0)
            .items_center()
            .px(11.0)
            .py(5.0)
            .radius(8.0)
            .motion(SPRING)
            .hover(Color::WHITE.alpha(0.06))
            .child(icon(glyph, 13.0).color(if on { theme::text() } else { faint() }))
            .child(text(title).font(META_BOLD).color(if on { theme::text() } else { faint() }));
        if on {
            t = t.fill(Color::WHITE.alpha(0.10)).border(1.0, Color::WHITE.alpha(0.08));
        }
        t
    };
    row()
        .w_full()
        .gap(4.0)
        .px(28.0)
        .pb(10.0)
        .child(tab("tab-conversation", &icons::MESSAGE, "Conversation".into(), s.tab == Tab::Conversation).on_press(|s: &mut App| s.tab = Tab::Conversation))
        .child(tab("tab-changes", &icons::BRANCH, if files == 0 { "Changes".into() } else { format!("Changes · {files} files") }, s.tab == Tab::Changes).on_press(show_changes))
}

fn changes_view(s: &App, id: &str) -> El {
    if session_edits(s, id).is_empty() {
        return col()
            .w_full()
            .grow(1.0)
            .items_center()
            .justify_center()
            .gap(10.0)
            .child(col().size(44.0, 44.0).radius(14.0).items_center().justify_center().fill(wash()).child(icon(&icons::BRANCH, 20.0).color(faint())))
            .child(text("No file edits in this conversation yet").font(BODY_BOLD).color(muted()))
            .child(text("Edits the agent makes show here as diffs, newest first.").font(META).color(faint()))
            .child(el().h(80.0));
    }
    col()
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .px(20.0)
        .pt(10.0)
        .pb(120.0)
        .child(rune_kit::diff::DiffView::new(|s: &App| &s.diff, |s: &mut App| &mut s.diff).key("diff").build(s, theme::kit()))
}

fn session_screen(s: &App) -> El {
    let Some(id) = s.selected.clone() else { return transcript::loading("Pick a session".into()) };
    let Some(x) = session(s, &id) else { return transcript::loading("Loading sessions…".into()) };
    let mut center = col().grow(1.0).h_full().min_w(0.0).child(header(s, x));
    if x.state(s.now) == State::NeedsYou && !s.answered.contains(&x.id) {
        center = center.child(waiting_card(s, x));
    } else if s.answered.contains(&x.id) {
        center = center.child(col().w_full().items_center().px(32.0).pb(12.0).child(pill(&icons::CHECK_CIRCLE, "Answer sent, waiting for the agent", done())));
    }
    center = center.child(tabs(s, &id)).child(el().w_full().h(1.0).fill(line()));
    let empty = s.panes.get(&id).is_none_or(|p| p.blocks.is_empty());
    if s.tab == Tab::Changes && !empty {
        center = center.child(changes_view(s, &id));
    } else if empty {
        let status = s.panes.get(&id).map(|p| p.status.clone()).unwrap_or_default();
        let note = if matches!(status.as_str(), "" | "loading" | "live") { "Loading the transcript…".to_string() } else { status };
        center = center.child(transcript::loading(note));
    } else {
        center = center.child(transcript::view(s, &id));
    }
    center.child(composer(s, x))
}

fn rail_section(title: &str, glyph: &'static Icon, body: El) -> El {
    col()
        .w_full()
        .gap(8.0)
        .pt(14.0)
        .child(row().gap(6.0).items_center().px(4.0).child(icon(glyph, 12.0).color(faint())).child(label(title)))
        .child(col().w_full().px(12.0).py(12.0).radius(14.0).fill(Color::BLACK.alpha(0.20)).border(1.0, Color::WHITE.alpha(0.07)).child(body))
}

fn phase_stepper(state: &str) -> El {
    let current = catalog::phase(state);
    let mut nodes = row().w_full().items_center();
    for i in 0..PHASES.len() {
        let reached = i <= current;
        let size = if i == current { 12.0 } else { 8.0 };
        let mut dot = el().size(size, size).radius(size / 2.0).fill(if reached { accent() } else { Color::WHITE.alpha(0.15) });
        if i == current {
            dot = dot.shadow(0.0, 10.0, accent().alpha(0.8)).border(2.0, Color::WHITE.alpha(0.9));
        }
        nodes = nodes.child(dot);
        if i + 1 < PHASES.len() {
            nodes = nodes.child(el().grow(1.0).h(2.0).radius(1.0).fill(if i < current { accent().alpha(0.7) } else { Color::WHITE.alpha(0.10) }));
        }
    }
    let names = row().w_full().children(PHASES.iter().enumerate().map(|(i, name)| {
        let t = text(name.to_string()).font(META).color(if i == current { theme::text() } else { faint() });
        let cell = col().grow(1.0);
        match i {
            0 => cell.items_start().child(t),
            n if n + 1 == PHASES.len() => cell.items_end().child(t),
            _ => cell.items_center().child(t),
        }
    }));
    col().w_full().gap(6.0).child(nodes).child(names)
}

fn note(title: &str, color: Color, body: String) -> El {
    row()
        .w_full()
        .gap(10.0)
        .child(el().w(3.0).radius(2.0).fill(color).self_stretch())
        .child(col().grow(1.0).min_w(0.0).gap(3.0).child(text(title.to_string()).font(META_BOLD).color(color)).child(text(body).font(META).color(muted()).wrap().max_lines(6)))
}

fn priority(p: &str) -> El {
    let color = match p {
        "P0" => failed(),
        "P1" => attention(),
        "P2" => accent(),
        _ => faint(),
    };
    row().px(6.0).py(2.0).radius(6.0).fill(color.alpha(0.15)).child(text(p.to_string()).font(META_BOLD).color(color))
}

fn rail(s: &App, x: &Session) -> El {
    let mut parts: Vec<El> = Vec::new();
    if let Some(work) = x.held_work.first() {
        let item = s.items.get(&work.item_id);
        let state = item.map(|i| i.state.clone()).unwrap_or_else(|| work.state.clone());
        let mut tags = row().gap(6.0).items_center().child(row().px(7.0).py(2.0).radius(6.0).fill(wash()).child(text(work.item_id.clone()).font(META).color(muted())));
        if let Some(i) = item {
            tags = tags.child(priority(&i.priority)).child(text(i.kind.clone()).font(META).color(faint()));
        }
        let mut body = col().w_full().gap(12.0).child(tags).child(text(work.text.clone()).font(BODY_BOLD).color(theme::text()).wrap()).child(phase_stepper(&state));
        if let Some(i) = item.filter(|i| !i.acceptance.is_empty()) {
            body = body.child(note("Expected", done(), i.acceptance.clone()));
        }
        if let Some(latest) = item.and_then(|i| i.timeline.iter().rev().find(|e| e.kind == "log").and_then(|e| e.text.clone())) {
            body = body.child(note("Latest", accent(), latest));
        }
        parts.push(rail_section("Work", &icons::TARGET, body));
        if let Some(i) = item {
            let entries: Vec<_> = i.timeline.iter().rev().take(6).collect();
            let count = entries.len();
            let list = col().w_full().children(entries.into_iter().enumerate().map(|(n, e)| {
                let what = match (&e.kind[..], &e.state) {
                    ("state", Some(st)) => format!("Moved to {}", st.replace('_', " ")),
                    ("created", _) => "Created".to_string(),
                    _ => e.text.clone().unwrap_or_default(),
                };
                let rail_line = col()
                    .w(10.0)
                    .items_center()
                    .self_stretch()
                    .child(el().size(8.0, 8.0).radius(4.0).mt(5.0).fill(if n == 0 { accent() } else { Color::WHITE.alpha(0.25) }))
                    .child(el().w(1.5).grow(1.0).fill(if n + 1 < count { Color::WHITE.alpha(0.10) } else { Color::TRANSPARENT }));
                row()
                    .w_full()
                    .gap(10.0)
                    .child(rail_line)
                    .child(col().grow(1.0).min_w(0.0).pb(10.0).gap(1.0).child(text(what).font(META).color(theme::text()).wrap().max_lines(2)).child(text(catalog::ago(&e.at, s.now)).font(META).color(faint())))
            }));
            parts.push(rail_section("Timeline", &icons::ACTIVITY, list));
        }
    }
    let files = file_totals(s, &x.id);
    if !files.is_empty() {
        let total_add: usize = files.iter().map(|f| f.added).sum();
        let total_del: usize = files.iter().map(|f| f.removed).sum();
        let list = col()
            .w_full()
            .gap(2.0)
            .child(row().w_full().pb(6.0).gap(8.0).items_center().child(text(format!("{} files", files.len())).font(META_BOLD).color(theme::text())).child(spacer()).child(text(format!("+{total_add}")).font(META_BOLD).color(done())).child(text(format!("−{total_del}")).font(META_BOLD).color(failed())))
            .children(files.iter().take(8).map(|f| {
                let name = f.path.rsplit('/').next().unwrap_or(&f.path).to_string();
                row()
                    .key(format!("file-{}", f.path))
                    .w_full()
                    .gap(8.0)
                    .items_center()
                    .px(6.0)
                    .py(5.0)
                    .radius(7.0)
                    .hover(Color::WHITE.alpha(0.06))
                    .on_press(show_changes)
                    .child(icon(&icons::LAYERS, 12.0).color(faint()))
                    .child(col().grow(1.0).min_w(0.0).child(text(name).font(META_BOLD).color(theme::text()).truncate()).child(text(f.path.clone()).font(META).color(faint()).truncate()))
                    .child(text(format!("+{}", f.added)).font(META).color(done()))
                    .child(text(format!("−{}", f.removed)).font(META).color(failed()))
                    .children((f.edits > 1).then(|| text(format!("×{}", f.edits)).font(META).color(faint())))
            }))
            .children((files.len() > 8).then(|| text(format!("and {} more", files.len() - 8)).font(META).color(faint()).pt(4.0)));
        parts.push(rail_section("Changes", &icons::BRANCH, list));
    }
    if let Some(plan) = &x.plan {
        let total = plan.total.max(1) as f32;
        let bar = row()
            .w_full()
            .h(6.0)
            .radius(3.0)
            .fill(Color::WHITE.alpha(0.10))
            .child(el().w(240.0 * plan.done as f32 / total).h(6.0).radius(3.0).linear_gradient(0.0, [(0.0, done()), (1.0, accent())]).motion(SPRING));
        parts.push(rail_section("Progress", &icons::LIST, col().w_full().gap(8.0).child(text(format!("{} of {} tasks done", plan.done, plan.total)).font(META).color(theme::text())).child(bar)));
    }
    if !x.prs.is_empty() {
        parts.push(rail_section(
            "Pull requests",
            &icons::PULL,
            col().w_full().gap(8.0).children(x.prs.iter().map(|p| {
                row().w_full().gap(8.0).items_center().child(icon(&icons::PULL, 13.0).color(done())).child(text(format!("#{}", p.number)).font(META_BOLD).color(accent())).child(text(p.title.clone()).font(META).color(theme::text()).truncate().min_w(0.0))
            })),
        ));
    }
    let facts: [(&'static Icon, &str, String); 4] = [
        (&icons::SPARKLES, "Agent", x.surface.clone()),
        (&icons::SERVER, "Host", x.host.name.clone()),
        (&icons::BRANCH, "Branch", x.checkout.branch.clone().unwrap_or_else(|| "none".into())),
        (&icons::INBOX, "Queued", x.queue_count.to_string()),
    ];
    parts.push(rail_section(
        "Session",
        &icons::LAYERS,
        col().w_full().gap(9.0).children(facts.into_iter().map(|(g, k, v)| {
            row().w_full().gap(8.0).items_center().child(icon(g, 12.0).color(faint())).child(text(k.to_string()).font(META).color(muted())).child(spacer()).child(text(v).font(META_BOLD).color(theme::text()).truncate().max_w(150.0))
        })),
    ));
    col().key("rail").w(300.0).h_full().px(12.0).child(el().w_full().h(36.0).window_drag()).child(col().w_full().grow(1.0).min_h(0.0).scroll(ScrollSpec { stick_bottom: false }).child(col().w_full().pb(16.0).children(parts)))
}

fn board_row(s: &App, r: &BoardRow) -> El {
    let holder = s.sessions.iter().find(|x| x.held_work.iter().any(|w| w.item_id == r.id)).map(|x| (x.id.clone(), x.title(), x.state(s.now)));
    let mut line_el = row()
        .w_full()
        .gap(10.0)
        .items_center()
        .px(14.0)
        .py(10.0)
        .radius(12.0)
        .fill(wash())
        .border(1.0, line())
        .hover(Color::WHITE.alpha(0.08))
        .child(priority(&r.priority))
        .child(text(r.text.clone()).font(BODY).color(theme::text()).truncate().grow(1.0).min_w(0.0))
        .child(text(r.kind.clone()).font(META).color(faint()));
    if let Some((id, title, state)) = holder {
        line_el = line_el
            .key(format!("board-{}", r.id))
            .press_scale(0.99)
            .on_press(move |s: &mut App| select(s, &id))
            .child(row().gap(6.0).items_center().child(avatar_with_state(&title, state, 20.0)).child(text(title).font(META_BOLD).color(state_color(state))));
    }
    line_el
}

fn tile(glyph: &'static Icon, count: usize, title: &str, color: Color) -> El {
    col()
        .grow(1.0)
        .gap(10.0)
        .padding(16.0)
        .radius(16.0)
        .linear_gradient(150.0, [(0.0, color.alpha(0.12)), (1.0, color.alpha(0.03))])
        .border(1.0, color.alpha(0.20))
        .child(col().size(30.0, 30.0).radius(9.0).items_center().justify_center().fill(color.alpha(0.18)).child(icon(glyph, 16.0).color(color)))
        .child(col().child(text(count.to_string()).font(NUMBER).color(theme::text())).child(text(title.to_string()).font(META).color(muted())))
}

fn section(title: &str, glyph: &'static Icon, body: El) -> El {
    col().w_full().gap(10.0).pt(26.0).child(row().gap(7.0).items_center().child(icon(glyph, 13.0).color(faint())).child(label(title))).child(body)
}

fn project_screen(s: &App, key: &str) -> El {
    let sessions: Vec<&Session> = ordered(s).into_iter().filter(|x| x.project_key() == key).collect();
    let name = sessions.first().map(|x| x.project_name()).unwrap_or_else(|| key.to_string());
    let needs: Vec<&Session> = sessions.iter().copied().filter(|x| x.state(s.now) == State::NeedsYou).collect();
    let working: Vec<&Session> = sessions.iter().copied().filter(|x| x.state(s.now) == State::Working).collect();
    let hosts = sessions.iter().map(|x| x.host.name.clone()).collect::<BTreeSet<_>>().len();
    let board = s.boards.get(key);
    let count_of = |states: &[&str]| board.map(|rows| rows.iter().filter(|r| states.contains(&r.state.as_str())).count()).unwrap_or(0);
    let tiles = row()
        .w_full()
        .gap(12.0)
        .child(tile(&icons::HAND, needs.len(), "need you", attention()))
        .child(tile(&icons::BOLT, working.len(), "working now", accent()))
        .child(tile(&icons::PULL, count_of(&["in_review"]), "in review", done()))
        .child(tile(&icons::LAYERS, count_of(&["open", "ready"]), "ready to start", muted()));
    let hero = row()
        .w_full()
        .gap(14.0)
        .items_center()
        .window_drag()
        .child(session_avatar(&name, 46.0))
        .child(col().gap(2.0).child(text(name.clone()).font(HERO).color(theme::text())).child(text(format!("{} sessions on {} hosts", sessions.len(), hosts)).font(META).color(muted())));
    let mut body = col().w_full().max_w(980.0).px(36.0).pt(30.0).pb(40.0).child(hero).child(el().h(20.0)).child(tiles);
    if !needs.is_empty() {
        let cards = needs.iter().map(|x| {
            let id = x.id.clone();
            row()
                .key(format!("needs-{}", x.id))
                .w_full()
                .gap(12.0)
                .items_start()
                .px(16.0)
                .py(14.0)
                .radius(14.0)
                .linear_gradient(135.0, [(0.0, attention().alpha(0.12)), (1.0, attention().alpha(0.03))])
                .border(1.0, attention().alpha(0.28))
                .press_scale(0.99)
                .on_press(move |s: &mut App| select(s, &id))
                .child(avatar_with_state(&x.title(), State::NeedsYou, 30.0))
                .child(col().grow(1.0).min_w(0.0).gap(4.0).child(text(x.title()).font(BODY_BOLD).color(theme::text())).child(text(x.summary()).font(META).color(muted()).wrap().max_lines(3)))
                .child(icon(&icons::CHEVRON_RIGHT, 14.0).color(attention()))
        });
        body = body.child(section("Needs your attention", &icons::HAND, col().w_full().gap(8.0).children(cards)));
    }
    if !working.is_empty() {
        body = body.child(section("Working now", &icons::BOLT, col().w_full().padding(6.0).radius(16.0).fill(Color::WHITE.alpha(0.025)).border(1.0, line()).children(working.iter().map(|x| session_row(s, x)))));
    }
    match board {
        Some(rows) => {
            for (title, glyph, states) in [("In review", &icons::PULL, &["in_review"][..]), ("In progress", &icons::ACTIVITY, &["in_progress"][..]), ("Ready to start", &icons::LAYERS, &["open", "ready"][..])] {
                let picked: Vec<El> = rows.iter().filter(|r| states.contains(&r.state.as_str())).take(12).map(|r| board_row(s, r)).collect();
                if !picked.is_empty() {
                    body = body.child(section(title, glyph, col().w_full().gap(6.0).children(picked)));
                }
            }
        }
        None => {
            body = body.child(section(
                "Work",
                &icons::LAYERS,
                row().gap(10.0).items_center().child(el().size(7.0, 7.0).radius(4.0).fill(accent())).child(text("Reading this project's ledger…").font(META).color(faint())),
            ))
        }
    }
    col().key(format!("project-page-{key}")).w_full().grow(1.0).min_h(0.0).items_center().scroll(ScrollSpec { stick_bottom: false }).child(body)
}

fn palette(s: &App, edit: &rune::TextEdit) -> El {
    let query = edit.text.to_lowercase();
    let matches = palette_matches(s, &query);
    let pick = s.palette_ix.min(matches.len().saturating_sub(1));
    let input = text_input(edit, TextStyle::new(17.0, 450.0))
        .key("palette-input")
        .grow(1.0)
        .color(theme::text())
        .placeholder("Jump to a session, project or question", faint())
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
        let state = x.state(s.now);
        let id = x.id.clone();
        let mut r = row()
            .key(format!("pick-{}", x.id))
            .w_full()
            .gap(12.0)
            .items_center()
            .px(12.0)
            .py(8.0)
            .radius(10.0)
            .motion(SPRING)
            .hover(Color::WHITE.alpha(0.05))
            .on_press(move |s: &mut App| {
                s.palette = None;
                select(s, &id);
            })
            .child(avatar_with_state(&x.title(), state, 28.0))
            .child(
                col()
                    .grow(1.0)
                    .min_w(0.0)
                    .gap(1.0)
                    .child(row().gap(8.0).items_center().child(text(x.title()).font(BODY_BOLD).color(theme::text()).truncate().max_w(320.0)).child(text(x.project_name()).font(META).color(faint()).truncate().max_w(160.0)))
                    .child(text(x.summary()).font(META).color(muted()).truncate()),
            );
        if i == pick {
            r = r.fill(accent().alpha(0.20)).border(1.0, accent().alpha(0.35)).child(keys(&["↵"]));
        }
        r
    });
    let footer = row()
        .w_full()
        .gap(14.0)
        .items_center()
        .px(14.0)
        .py(9.0)
        .border_sides([1.0, 0.0, 0.0, 0.0], line())
        .child(row().gap(5.0).items_center().child(keys(&["↑", "↓"])).child(text("move").font(META).color(faint())))
        .child(row().gap(5.0).items_center().child(keys(&["↵"])).child(text("open").font(META).color(faint())))
        .child(row().gap(5.0).items_center().child(keys(&["esc"])).child(text("close").font(META).color(faint())))
        .child(spacer())
        .child(text(format!("{} sessions", s.palette_order.len())).font(META).color(faint()));
    let card_el = col()
        .w(660.0)
        .radius(20.0)
        .backdrop_blur(30.0)
        .fill(card().alpha(0.80))
        .border(1.0, Color::WHITE.alpha(0.12))
        .shadow(24.0, 60.0, Color::BLACK.alpha(0.55))
        .transition(Transition::scale(0.96))
        .child(row().w_full().gap(12.0).items_center().px(18.0).py(15.0).child(icon(&icons::SEARCH, 18.0).color(muted())).child(input))
        .child(el().w_full().h(1.0).fill(line()))
        .child(col().w_full().padding(6.0).gap(2.0).children(rows))
        .child(footer);
    col().absolute(0.0, 0.0).w_full().h_full().items_center().pt(96.0).backdrop_blur(4.0).fill(Color::BLACK.alpha(0.32)).transition(Transition::opacity()).child(card_el)
}

pub fn view(s: &App, _: &mut Cx<App>) -> El {
    let main = match &s.screen {
        Screen::Session => session_screen(s),
        Screen::Project(key) => project_screen(s, key),
    };
    let content = col()
        .grow(1.0)
        .h_full()
        .min_w(0.0)
        .my(8.0)
        .radius(16.0)
        .continuous_corners()
        .fill(card())
        .border(1.0, Color::WHITE.alpha(0.08))
        .shadow(10.0, 40.0, Color::BLACK.alpha(0.40))
        .clip_rounded(16.0)
        .child(main);
    let mut body = row().w_full().h_full().child(sidebar(s)).child(content);
    match (&s.screen, s.selected.as_deref().and_then(|id| session(s, id))) {
        (Screen::Session, Some(x)) => body = body.child(rail(s, x)),
        _ => body = body.child(el().w(8.0)),
    }
    let mut root = col()
        .w_full()
        .h_full()
        .child(el().absolute(0.0, 0.0).w_full().h_full().system_material(SystemMaterial::Glass).fill(theme::card_deep()))
        .child(el().absolute(0.0, 0.0).w_full().h_full().fill(theme::glass_tint()))
        .child(body);
    if let Some(edit) = &s.palette {
        root = root.child(palette(s, edit));
    }
    root
}
