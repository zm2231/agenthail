use super::*;

pub(crate) fn board_row(s: &App, r: &BoardRow) -> El {
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

pub(crate) fn tile(glyph: &'static Icon, count: usize, title: &str, color: Color) -> El {
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

pub(crate) fn section(title: &str, glyph: &'static Icon, body: El) -> El {
    col().w_full().gap(10.0).pt(26.0).child(row().gap(7.0).items_center().child(icon(glyph, 13.0).color(faint())).child(label(title))).child(body)
}

pub(crate) fn project_screen(s: &App, key: &str) -> El {
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
