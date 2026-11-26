use super::*;

pub fn ordered(s: &App) -> Vec<&Session> {
    let mut list: Vec<&Session> = s.sessions.iter().filter(|x| x.visible(s.now)).collect();
    list.sort_by(|a, b| a.state(s.now).cmp(&b.state(s.now)).then(b.last_active.cmp(&a.last_active)));
    list
}

pub(crate) fn session_row(s: &App, x: &Session) -> El {
    let state = x.state(s.now);
    let selected = s.selected.as_deref() == Some(x.id.as_str());
    let id = x.id.clone();
    let menu_id = x.id.clone();
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
        .on_secondary_press(move |s: &mut App, p, _: &mut Tx| session_menu::open_at(s, &menu_id, p))
        .child(avatar_with_state(&x.title(), state, 30.0))
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(2.0)
                .child(row().w_full().gap(6.0).items_center().child(text(x.title()).font(BODY_BOLD).color(theme::text()).truncate().grow(1.0).min_w(0.0)).child(session_menu::marks(x)).child(trailing))
                .child(row().gap(4.0).items_center().child(icon(&icons::BRANCH, 11.0).color(faint())).child(text(place).font(META).color(faint()).truncate().min_w(0.0)))
                .child(text(summary).font(META).color(summary_color).wrap().max_lines(2).pt(2.0)),
        );
    if selected {
        card_el = card_el.fill(Color::WHITE.alpha(0.11)).border(1.0, Color::WHITE.alpha(0.10)).shadow(4.0, 14.0, Color::BLACK.alpha(0.25));
    }
    card_el
}

pub(crate) fn group(title: &str, count: usize, glyph: &'static Icon, color: Color, rows: Vec<El>, project: Option<String>) -> El {
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
            .on_press(move |s: &mut App| crate::over::go(s, Screen::Project(key.clone())))
            .child(row().gap(3.0).items_center().px(6.0).py(2.0).radius(6.0).hover(wash()).child(text("Overview").font(META).color(accent())).child(icon(&icons::CHEVRON_RIGHT, 11.0).color(accent())));
    }
    col().w_full().child(head).child(col().w_full().gap(2.0).children(rows))
}

pub(crate) fn segmented(s: &App) -> El {
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

pub(crate) fn stat(glyph: &'static Icon, count: usize, title: &str, color: Color) -> El {
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

pub(crate) fn sidebar(s: &App) -> El {
    let list = ordered(s);
    let needs = list.iter().filter(|x| x.state(s.now) == State::NeedsYou).count();
    let working = list.iter().filter(|x| x.state(s.now) == State::Working).count();
    let home = row()
        .key("home")
        .w_full()
        .gap(8.0)
        .items_center()
        .px(10.0)
        .py(7.0)
        .radius(10.0)
        .hover(Color::WHITE.alpha(0.06))
        .on_press(|s: &mut App| crate::over::go(s, Screen::Now))
        .child(icon(&icons::INBOX, 14.0).color(muted()))
        .child(text("Now").font(META_BOLD).color(theme::text()))
        .child(spacer())
        .child(keys(&["⌘", "0"]));
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
        .on_press_tx(crate::over::open_jump)
        .child(icon(&icons::SEARCH, 14.0).color(muted()))
        .child(text("Jump to").font(META).color(muted()))
        .child(spacer())
        .child(keys(&["⌘", "K"]));
    let header = col()
        .w_full()
        .px(12.0)
        .gap(10.0)
        .child(el().w_full().h(40.0).window_drag())
        .child(home)
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
        let current = s.selected.as_deref().and_then(|id| session(s, id)).map(|x| x.project_key()).filter(|k| !k.is_empty());
        projects.sort_by_key(|(_, key, v)| (key.is_empty(), current.as_deref() != Some(key.as_str()), v.iter().map(|x| x.state(s.now)).min(), std::cmp::Reverse(v.len())));
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
