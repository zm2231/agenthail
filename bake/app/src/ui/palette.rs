use super::*;

pub enum Hit<'a> {
    Project { key: String, name: String, count: usize, top: State },
    Session(&'a Session),
}

pub fn palette_matches<'a>(s: &'a App, query: &str) -> Vec<Hit<'a>> {
    let mut hits: Vec<Hit> = Vec::new();
    if !query.is_empty() {
        let mut projects: BTreeMap<String, (String, usize, State)> = BTreeMap::new();
        for x in s.sessions.iter().filter(|x| !x.archived && !x.project_key().is_empty()) {
            let name = x.project_name();
            if name.to_lowercase().contains(query) {
                let e = projects.entry(name).or_insert((x.project_key(), 0, State::Idle));
                if x.visible(s.now) {
                    e.1 += 1;
                    e.2 = e.2.min(x.state(s.now));
                }
            }
        }
        let mut list: Vec<_> = projects.into_iter().collect();
        list.sort_by_key(|(name, (_, count, _))| (!name.to_lowercase().starts_with(query), std::cmp::Reverse(*count)));
        hits.extend(list.into_iter().take(3).map(|(name, (key, count, top))| Hit::Project { key, name, count, top }));
    }
    let room = 9 - hits.len();
    hits.extend(
        s.palette_order
            .iter()
            .filter_map(|id| session(s, id))
            .filter(|x| query.is_empty() || x.title().to_lowercase().contains(query) || x.project_name().to_lowercase().contains(query) || x.summary().to_lowercase().contains(query))
            .take(room)
            .map(Hit::Session),
    );
    hits
}

pub(crate) fn activate(s: &mut App, query: &str) {
    let target = match palette_matches(s, query).into_iter().nth(s.palette_ix) {
        Some(Hit::Project { key, .. }) => Some((true, key)),
        Some(Hit::Session(x)) => Some((false, x.id.clone())),
        None => None,
    };
    if let Some((project, key)) = target {
        s.palette = None;
        if project { open_project(s, &key) } else { select(s, &key) }
    }
}

pub(crate) fn palette(s: &App, edit: &rune::TextEdit) -> El {
    let query = edit.text.to_lowercase();
    let matches = palette_matches(s, &query);
    let pick = s.palette_ix.min(matches.len().saturating_sub(1));
    let sessions_found = matches.iter().filter(|h| matches!(h, Hit::Session(_))).count();
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
            let len = palette_matches(s, &query).len();
            s.palette_ix = s.palette_ix.min(len.saturating_sub(1));
            activate(s, &query);
        });
    let rows = matches.iter().enumerate().map(|(i, hit)| {
        let x = match hit {
            Hit::Session(x) => *x,
            Hit::Project { key, name, count, top } => {
                let key = key.clone();
                let mut r = row()
                    .key(format!("pick-project-{key}"))
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
                        open_project(s, &key);
                    })
                    .child(col().size(28.0, 28.0).radius(8.0).items_center().justify_center().fill(wash()).child(icon(&icons::FOLDER, 14.0).color(state_color(*top))))
                    .child(
                        col()
                            .grow(1.0)
                            .min_w(0.0)
                            .gap(1.0)
                            .child(row().gap(8.0).items_center().child(text(name.clone()).font(BODY_BOLD).color(theme::text()).truncate()).child(text("Project").font(META).color(faint())))
                            .child(text(if *count == 1 { "1 active session".to_string() } else { format!("{count} active sessions") }).font(META).color(muted())),
                    );
                if i == pick {
                    r = r.fill(accent().alpha(0.20)).border(1.0, accent().alpha(0.35)).child(keys(&["↵"]));
                }
                return r;
            }
        };
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
        .child(text(if query.is_empty() { format!("{} sessions", s.palette_order.len()) } else { format!("{sessions_found} sessions") }).font(META).color(faint()));
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
