use super::*;
use feed::composer::Busy;
use crate::Confirm;

pub(crate) fn header(s: &App, x: &Session) -> El {
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
        .child(col().grow(1.0).min_w(0.0).gap(7.0).child(text(x.title()).font(HEADING).color(theme::text()).truncate()).child(chips).child(crate::ui::chips::turn(s, x)))
        .child(pill(state_icon(state), label_value, state_color(state)))
        .child(session_menu::button(x))
}

pub(crate) fn composer(s: &App, x: &Session) -> El {
    let input = text_input(&s.composer, BODY)
        .key("composer")
        .w_full()
        .min_h(26.0)
        .color(theme::text())
        .placeholder(format!("Message {}", x.title()), faint())
        .on_edit(|s: &mut App| &mut s.composer)
        .on_input_paste(|s: &mut App, content, _: &mut Tx| super::attach::paste(s, content))
        .on_input_key(super::complete::key)
        .on_submit(|s: &mut App, tx: &mut Tx| send(s, tx));
    let ready = !s.composer.text.trim().is_empty() || super::attach::has(s, &x.id);
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
        .on_press_tx(|s: &mut App, tx: &mut Tx| {
            if let Some(id) = s.selected.clone() {
                super::complete::submit(s, &id, tx);
            }
        })
        .child(icon(&icons::ARROW_UP, 15.0).color(if ready { Color::WHITE } else { faint() }));
    let busy = crate::busy_delivery(s, &x.id);
    let unsent = s.unsent.has(&x.id);
    let status = match crate::composer_note(s, &x.id) {
        Some(note) => text(note).font(META).color(if unsent { attention() } else { muted() }),
        None => text(crate::send_busy(s, &x.id).map(|b| b.hint()).unwrap_or("Return to send · Shift-Return for a new line")).font(META).color(faint()),
    };
    let mut controls = row().w_full().px(10.0).pb(10.0).gap(8.0).items_center().children(super::attach::button(s, &x.id)).child(chip(&icons::SPARKLES, x.surface.clone()));
    if let Some(choice) = busy {
        let id = x.id.clone();
        controls = controls.child(
            row()
                .key("busy-delivery")
                .gap(5.0)
                .items_center()
                .px(8.0)
                .py(3.0)
                .radius(7.0)
                .fill(wash())
                .hover(Color::WHITE.alpha(0.08))
                .motion(SPRING)
                .on_press(move |s: &mut App| crate::pick_busy(s, &id, choice.other()))
                .child(icon(if choice == Busy::Steer { &icons::BOLT } else { &icons::CLOCK }, 12.0).color(accent()))
                .child(text(choice.label()).font(META_BOLD).color(theme::text())),
        );
    }
    controls = controls.child(status.truncate()).child(spacer());
    if unsent {
        let k = theme::kit();
        let (id, back, drop, new) = (x.id.clone(), x.id.clone(), x.id.clone(), x.id.clone());
        if crate::unsent_held(s, &x.id) {
            controls = controls.child(Button::new(crate::confirm_label(s, &x.id, Confirm::PutBack)).variant(ButtonVariant::Ghost).build(k).key("send-put-back").on_press(move |s: &mut App| crate::put_back_unsent(s, &back)));
        }
        controls = controls.child(Button::new(crate::confirm_label(s, &x.id, Confirm::Discard)).variant(ButtonVariant::Ghost).build(k).key("send-dismiss").on_press(move |s: &mut App| crate::dismiss_unsent(s, &drop)));
        if crate::offers_new_send(s, &x.id) {
            controls = controls.child(Button::new(crate::confirm_label(s, &x.id, Confirm::SendNew)).variant(ButtonVariant::Ghost).build(k).key("send-new").on_press_tx(move |s: &mut App, tx: &mut Tx| crate::send_as_new(s, &new, tx)));
        }
        if crate::can_retry(s, &x.id) {
            controls = controls.child(Button::new("Retry").variant(ButtonVariant::Ghost).icon(&icons::ROTATE).build(k).key("send-retry").on_press_tx(move |s: &mut App, tx: &mut Tx| crate::retry(s, &id, tx)));
        }
    }
    if crate::can_stop(s, &x.id) {
        let id = x.id.clone();
        controls = controls.child(
            col()
                .key("stop")
                .size(30.0, 30.0)
                .radius(15.0)
                .items_center()
                .justify_center()
                .fill(failed().alpha(if s.stopping.contains(&x.id) { 0.35 } else { 0.85 }))
                .press_scale(0.92)
                .motion(SPRING)
                .on_press(move |s: &mut App| crate::stop(s, &id))
                .child(icon(&icons::STOP, 13.0).color(Color::WHITE)),
        );
    }
    let box_el = col()
        .w_full()
        .max_w(780.0)
        .radius(18.0)
        .backdrop_blur(22.0)
        .fill(card().alpha(0.78))
        .border(if s.attach.dropping { 1.5 } else { 1.0 }, if s.attach.dropping { accent() } else { Color::WHITE.alpha(0.10) })
        .shadow(10.0, 34.0, Color::BLACK.alpha(0.45))
        .children(crate::ui::queue::dock(s, x))
        .children(super::complete::search(s, x))
        .children(super::complete::menu(s, x))
        .children(super::attach::chips(s, &x.id).map(|c| col().w_full().px(12.0).pt(10.0).child(c)))
        .child(col().w_full().px(16.0).pt(13.0).pb(4.0).child(input))
        .child(controls.child(send_button));
    col().w_full().items_center().px(28.0).pb(18.0).mt(-120.0).child(box_el)
}

pub(crate) fn counts(diff: &rune_kit::diff::FileDiff) -> (usize, usize) {
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

pub(crate) struct FileTotal {
    pub(crate) path: String,
    pub(crate) added: usize,
    pub(crate) removed: usize,
    pub(crate) edits: usize,
}

pub(crate) fn file_totals(s: &App, id: &str) -> Vec<FileTotal> {
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

pub(crate) fn tabs(s: &App, id: &str) -> El {
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

pub(crate) fn changes_view(s: &App, id: &str) -> El {
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

pub(crate) fn session_screen(s: &App) -> El {
    let Some(id) = s.selected.clone() else { return transcript::loading("Pick a session".into()) };
    let Some(x) = session(s, &id).filter(|x| !x.archived) else { return super::gone::screen(s, &id) };
    let mut center = col().grow(1.0).h_full().min_w(0.0).child(header(s, x));
    let open = open_request(s, x).is_some();
    let sent = has_settled(s, x);
    let stop = x.limit(s.now);
    if let Some(stop) = stop.as_ref().filter(|_| !open) {
        center = center.child(super::limit::card(s, x, stop));
    } else if open || (x.state(s.now) == State::NeedsYou && !sent) {
        center = center.child(waiting_card(s, x));
    } else if sent {
        let request = settled_request(s, x).map(|r| r.id.clone()).unwrap_or_default();
        let in_flight = s.answering.as_deref() == Some(request.as_str());
        let (glyph, label, color) = match (in_flight, s.answer_status.get(&request)) {
            (true, _) => (&icons::CLOCK, "Sending your answer…", muted()),
            (_, Some(status)) if status.starts_with("Sent, not confirmed") => (&icons::CLOCK, "Answer sent, not confirmed yet", attention()),
            _ => (&icons::CHECK_CIRCLE, "Answer sent, waiting for the agent", done()),
        };
        center = center.child(col().w_full().items_center().px(32.0).pb(12.0).child(pill(glyph, label, color)));
    }
    super::attach::drop_target(conversation(s, &id, center).child(composer(s, x)))
}

pub(crate) fn conversation(s: &App, id: &str, center: El) -> El {
    let center = center.child(tabs(s, id)).child(el().w_full().h(1.0).fill(line()));
    let empty = s.panes.get(id).is_none_or(|p| p.blocks.is_empty());
    if s.tab == Tab::Changes && !empty {
        center.child(changes_view(s, id))
    } else if empty {
        let status = s.panes.get(id).map(|p| p.status.clone()).unwrap_or_default();
        let note = if matches!(status.as_str(), "" | "loading" | "live") { "Loading the transcript…".to_string() } else { status };
        center.child(transcript::empty(s, id, note))
    } else {
        center.child(transcript::view(s, id))
    }
}
