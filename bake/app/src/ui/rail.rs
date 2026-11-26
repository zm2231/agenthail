use super::*;

pub(crate) fn rail_section(title: &str, glyph: &'static Icon, body: El) -> El {
    col()
        .w_full()
        .gap(8.0)
        .pt(14.0)
        .child(row().gap(6.0).items_center().px(4.0).child(icon(glyph, 12.0).color(faint())).child(label(title)))
        .child(col().w_full().px(12.0).py(12.0).radius(14.0).fill(Color::BLACK.alpha(0.20)).border(1.0, Color::WHITE.alpha(0.07)).child(body))
}

pub(crate) fn phase_stepper(state: &str) -> El {
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

pub(crate) fn note(title: &str, color: Color, body: String) -> El {
    row()
        .w_full()
        .gap(10.0)
        .child(el().w(3.0).radius(2.0).fill(color).self_stretch())
        .child(col().grow(1.0).min_w(0.0).gap(3.0).child(text(title.to_string()).font(META_BOLD).color(color)).child(text(body).font(META).color(muted()).wrap_anywhere().max_lines(6)))
}

pub(crate) fn priority(p: &str) -> El {
    let color = match p {
        "P0" => failed(),
        "P1" => attention(),
        "P2" => accent(),
        _ => faint(),
    };
    row().px(6.0).py(2.0).radius(6.0).fill(color.alpha(0.15)).child(text(p.to_string()).font(META_BOLD).color(color))
}

pub(crate) fn rail(s: &App, x: &Session) -> El {
    let mut parts: Vec<El> = Vec::new();
    if let Some(work) = x.primary_work(s.work_pick.get(&x.id).map(String::as_str)) {
        let item = s.items.get(&work.item_id);
        let state = item.map(|i| i.state.clone()).unwrap_or_else(|| work.state.clone());
        let mut tags = row().gap(6.0).items_center().child(row().px(7.0).py(2.0).radius(6.0).fill(wash()).child(text(work.item_id.clone()).font(META).color(muted())));
        if let Some(i) = item {
            tags = tags.child(priority(&i.priority)).child(text(i.kind.clone()).font(META).color(faint()));
        }
        let mut body = col().w_full().gap(12.0).child(tags).child(text(work.text.clone()).font(BODY_BOLD).color(theme::text()).wrap_anywhere()).child(phase_stepper(&state));
        if let Some(i) = item.filter(|i| !i.acceptance.is_empty()) {
            body = body.child(note("Expected", done(), i.acceptance.clone()));
        }
        if let Some(latest) = item.and_then(|i| i.timeline.iter().rev().find(|e| e.kind == "log").and_then(|e| e.text.clone())) {
            body = body.child(note("Latest", accent(), latest));
        }
        let others: Vec<_> = x.held_work.iter().filter(|w| w.item_id != work.item_id).collect();
        if !others.is_empty() {
            body = body.child(
                col()
                    .w_full()
                    .gap(2.0)
                    .pt(2.0)
                    .child(text(format!("ALSO HOLDS {}", others.len())).font(LABEL).color(faint()).pb(2.0))
                    .children(others.into_iter().take(6).map(|w| {
                        let item = w.item_id.clone();
                        row()
                            .key(format!("held-{}", w.item_id))
                            .w_full()
                            .gap(8.0)
                            .items_center()
                            .px(6.0)
                            .py(5.0)
                            .radius(7.0)
                            .hover(Color::WHITE.alpha(0.06))
                            .on_press(move |s: &mut App| pick_work(s, &item))
                            .child(text(w.item_id.rsplit('-').next().unwrap_or(&w.item_id).to_string()).font(META_BOLD).color(muted()))
                            .child(text(w.text.clone()).font(META).color(theme::text()).truncate().grow(1.0).min_w(0.0))
                            .child(text(w.state.replace('_', " ")).font(META).color(faint()))
                    })),
            );
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
                let url = p.url.clone();
                row().key(format!("pr-{}", p.number)).w_full().gap(8.0).items_center().radius(6.0).hover(Color::WHITE.alpha(0.06)).on_press(move |_: &mut App| open_link(&url)).child(icon(&icons::PULL, 13.0).color(done())).child(text(format!("#{}", p.number)).font(META_BOLD).color(accent())).child(text(p.title.clone()).font(META).color(theme::text()).truncate().min_w(0.0))
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
