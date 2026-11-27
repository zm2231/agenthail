use rune::{Cx, El, ScrollSpec, TextEdit, Tx, col, row, text, text_input};
use rune_kit::shell::Toolbar;
use rune_kit::{Badge, BadgeVariant, Button, ButtonVariant, ControlSize, IconButton, IconButtonVariant, Kit, KitCx, SegmentedControl};
use rune_theme_opencode::icons;

use feed::derive::board::{LANES, fold};
use feed::derive::digest::{HAPPENED, count, cut, pick};
use feed::derive::phase::{CHECK, SHIP, phase};
use feed::derive::sessions::busy;
use feed::derive::turn_end::turn_end;

use crate::over::{Line, Work, plain, short_id};
use crate::over::ui::{self, BODY, BODY_MED, HEAD, META, META_MED, base, strong, weak};
use crate::over::go;
use crate::{App, Confirm, Screen};

fn variant(s: &App, key: &str, k: &Kit) -> El {
    let id = key.to_string();
    let seg = SegmentedControl::new(&["Lead chat", "Board"], Some(if crate::over::layout::board_for(s, key) { 1 } else { 0 }))
        .label("Project view")
        .fit()
        .on_pick(move |s: &mut App, i, _| {
            s.ov.boards.insert(id.clone(), i == 1);
        })
        .build(k);
    row().gap(6.0).items_center().child(seg).child(ui::ghost_keys(&["⌥⌘B"], k))
}

fn toolbar(s: &App, key: &str, k: &Kit) -> Toolbar {
    let name = s.ov.d.project(key).map(|p| p.name.clone()).unwrap_or_default();
    Toolbar::new()
        .leading(row().gap(14.0).items_center().child(ui::avatar(&name, 18.0)).child(ui::crumb(&[("Projects", false), (&name, true)], k)))
        .trailing(row().gap(10.0).items_center().child(crate::over::decisions::brief_button(s, key, k)).child(ui::new_session_button(Some(key), k)).child(variant(s, key, k)).child(crate::over::projects::menu_button(key, k)).child(ui::needs_button(s, k)))
}

pub fn work_card(s: &App, w: &Work, compact: bool, k: &Kit) -> El {
    let key = w.key();
    let phase = phase(w);
    let who = match phase {
        CHECK => "Your move".to_string(),
        SHIP => "Shipped".to_string(),
        _ => feed::derive::phase::move_of(&ui::short_title(&s.ov.d.whose_move(w), 22)),
    };
    let fit = format!("card-{}-{compact}", w.id);
    let width = ui::fit_width(s, &fit);
    let title = plain(&w.text);
    let mini = ui::mini_shown(w);
    let last = w.timeline.last().map(|t| s.ov.d.ago(&t.at)).unwrap_or_default();
    let id = short_id(&w.id);
    let card = format!("w-{}-{}", w.ledger_key(), w.id);
    let shows = (&key, compact, phase, &who, width.map(f32::to_bits), &title, &mini, &last, &id);
    crate::over::rows::memo(card.clone(), shows, k, || {
        let open = key.clone();
        col()
            .key(card)
            .w_full()
            .gap(8.0)
            .padding(12.0)
            .radius(10.0)
            .fill(k.c.surface_base)
            .border(1.0, if phase == CHECK { k.c.warning.icon.alpha(0.35) } else { ui::edge() })
            .hover(k.c.surface_raised_hover)
            .on_press(move |s: &mut App| go(s, Screen::Work(open.clone())))
            .child(ui::fitted(&fit, &title, width, BODY_MED.lh(19.0), strong(k), 2, k))
            .child(ui::mini_phase(mini.0, &mini.1, k))
            .child(
                row()
                    .w_full()
                    .gap(6.0)
                    .items_center()
                    .child(text(who.clone()).font(k.text(if phase == CHECK { META_MED } else { META })).color(if phase == CHECK { ui::needs_color(k) } else { weak(k) }).truncate().shrink())
                    .child(ui::spacer())
                    .child(text(last.clone()).font(k.text(META)).color(weak(k)))
                    .child(ui::id_label(&id, k)),
            )
    })
}

fn lane(s: &App, key: &str, title: &str, items: &[&Work], compact: bool, k: &Kit) -> (Vec<El>, Option<El>) {
    let lane = format!("{key}/{title}");
    let open = s.ov.open_lanes.contains(&lane);
    let f = fold(items, open);
    let cards = f.shown.iter().map(|w| work_card(s, w, compact, k)).collect();
    (cards, f.foldable.then(|| lane_more(lane, open, f.hidden, k)))
}

fn lane_more(lane: String, open: bool, hidden: usize, k: &Kit) -> El {
    let label = if open { "Show fewer".to_string() } else { format!("{hidden} more") };
    row()
            .key(format!("more-{lane}"))
            .w_full()
            .gap(6.0)
            .items_center()
            .px(10.0)
            .py(8.0)
            .radius(8.0)
            .hover(k.c.surface_raised_hover)
            .on_press(move |s: &mut App| {
                if !s.ov.open_lanes.remove(&lane) {
                    s.ov.open_lanes.insert(lane.clone());
                }
            })
            .child(rune::icon(if open { &icons::CHEVRON_DOWN } else { &icons::CHEVRON_RIGHT }, k.text_size(12.0)).color(weak(k)))
            .child(text(label).font(k.text(META_MED)).color(weak(k)))
}

fn rows_note(s: &App, key: &str, k: &Kit) -> Option<El> {
    let rows = *s.ov.d.rows.get(key)?;
    let color = if rows == feed::derive::Rows::Loading { weak(k) } else { ui::needs_color(k) };
    Some(row().w_full().gap(6.0).items_center().child(rune::icon(&icons::INFO, k.text_size(13.0)).color(color)).child(text(rows.note().to_string()).font(k.text(META)).color(color).wrap()))
}

fn group(s: &App, key: &str, title: &str, items: &[&Work], empty: &str, k: &Kit) -> El {
    let mut c = col().w_full().gap(8.0).child(ui::section_head(title, Some(items.len()), None, k));
    if items.is_empty() {
        c = c.child(text(empty.to_string()).font(k.text(META)).color(weak(k)));
    }
    let (cards, more) = lane(s, key, title, items, true, k);
    c.children(cards).children(more)
}

fn crew_panel(s: &App, lead: Option<&str>, k: &Kit) -> El {
    let crew = lead.map(|l| s.ov.d.crew(l)).unwrap_or_default();
    let mut c = col().w_full().gap(6.0).child(ui::section_head("Crew", Some(crew.len()), Some(text("started by the lead".to_string()).font(k.text(META)).color(weak(k))), k));
    if crew.is_empty() {
        c = c.child(text("The lead has no workers running today.".to_string()).font(k.text(META)).color(weak(k)));
    }
    for (i, x) in crew.iter().take(5).enumerate() {
        let id = x.id.clone();
        let end = turn_end(x, !s.ov.d.asks(&x.id).iter().all(|a| a.answer.is_some()));
        let status = if busy(x) { rune_kit::Spinner::new().size(k.text_size(11.0)).build(k) } else { rune::icon(&icons::CIRCLE_CHECK, k.text_size(12.0)).color(weak(k)) };
        c = c.child(
            row()
                .key(format!("crew-{i}"))
                .w_full()
                .gap(10.0)
                .items_start()
                .px(8.0)
                .py(7.0)
                .radius(8.0)
                .hover(k.c.surface_raised_hover)
                .on_press(move |s: &mut App| crate::over::open_session(s, &id))
                .child(row().pt(3.0).child(status))
                .child(
                    col()
                        .grow(1.0)
                        .shrink()
                        .min_w(0.0)
                        .gap(2.0)
                        .child(row().gap(6.0).items_center().child(text(ui::short_title(&s.ov.d.title(x), 30)).font(k.text(META_MED)).color(strong(k)).truncate().shrink()).child(ui::spacer()).child(text(s.ov.d.ago(&x.last_active)).font(k.text(META)).color(weak(k))))
                        .child(text(end.label.clone()).font(k.text(META_MED)).color(if end.needs_you { ui::needs_color(k) } else { base(k) }))
                        .children(Some(if end.headline.is_empty() { s.ov.d.gist(x) } else { crate::over::plain(&end.headline) }).filter(|t| !t.is_empty() && *t != end.label).map(|t| text(t).font(k.text(META)).color(weak(k)).max_lines(3).wrap())),
                ),
        );
    }
    c
}

fn waiting_line(s: &App, lead: &str, k: &Kit) -> El {
    let Some(x) = s.ov.d.session(lead) else { return col() };
    let crew = s.ov.d.crew(lead);
    let busy: Vec<String> = crew.iter().filter(|c| busy(c)).map(|c| s.ov.d.title(c)).collect();
    let (label, who) = if x.display.label.starts_with("Waiting on") {
        ("Waiting on".to_string(), x.display.label.trim_start_matches("Waiting on").trim().to_string())
    } else if !busy.is_empty() {
        ("Crew at work:".to_string(), busy.join(", "))
    } else {
        return col();
    };
    row()
        .w_full()
        .max_w(780.0)
        .px(28.0)
        .pb(8.0)
        .gap(6.0)
        .justify_center()
        .items_center()
        .child(rune_kit::Spinner::new().size(k.text_size(11.0)).build(k))
        .child(text(label).font(k.text(META)).color(weak(k)))
        .child(text(who).font(k.text(META_MED)).color(k.c.text_accent).truncate().shrink())
}

pub fn lead(s: &App, cx: &mut Cx<App>, key: &str, _layer: &mut Vec<El>, main_kit: &Kit) -> El {
    let k = cx.kit();
    let Some(p) = s.ov.d.project(key) else { return col() };
    let cols = s.ov.d.board(key).lanes;
    let panel = col()
        .key("work-panel")
        .w_full()
        .h_full()
        .scroll(ScrollSpec { stick_bottom: false })
        .child(
            col()
                .w_full()
                .gap(22.0)
                .px(14.0)
                .pt(14.0)
                .pb(24.0)
                .child(crew_panel(s, p.lead.as_deref(), &k))
                .children(crate::over::projects::merged(s, key, &k))
                .children(rows_note(s, key, &k))
                .child(group(s, key, LANES[0], &cols[0], "Nothing waits on you here.", &k))
                .child(group(s, key, LANES[1], &cols[1], "Nothing is being built.", &k))
                .child(group(s, key, LANES[2], &cols[2], "The queue is empty.", &k))
                .child(crate::over::earlier::section(s, key, &k))
                .children(crate::over::earlier::folders(s, key, &k)),
        );
    let main = match &p.lead {
        Some(lead) => {
            let head = row()
                .w_full()
                .px(28.0)
                .py(10.0)
                .gap(10.0)
                .items_center()
                .border_sides([0.0, 0.0, 1.0, 0.0], ui::rule(&k))
                .child(ui::avatar(&p.lead_name, 22.0))
                .child(col().gap(1.0).child(text(format!("{}, the lead", p.lead_name)).font(k.text(BODY_MED)).color(strong(&k))).child(text(s.ov.d.session(lead).map(|x| s.ov.d.gist(x)).unwrap_or_default()).font(k.text(META)).color(weak(&k)).truncate().max_w(620.0)))
                .child(ui::spacer())
                .children(crate::ui::ready::link(s, key, &k))
                .child(Button::new("Workers").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).icon(&icons::BULLET_LIST).on_press_tx(|s: &mut App, tx: &mut Tx| crate::over::open_jump(s, tx)).build(&k));
            crate::ui::attach::drop_target(col()
                .w_full()
                .h_full()
                .items_center()
                .child(head)
                .children(crate::over::decisions::brief_note(s, key, &k).map(|n| row().w_full().px(28.0).py(8.0).border_sides([0.0, 0.0, 1.0, 0.0], ui::rule(&k)).child(n)))
                .child(lead_chat(s, lead, main_kit))
                .child(waiting_line(s, lead, &k))
                .child(composer(s, &format!("Tell {}…", p.lead_name), lead, &k, main_kit)))
        }
        None => {
            let pk = key.to_string();
            let label = if p.lead_pending.is_some() { "Change Lead…" } else { "Set Lead…" };
            let start = Button::new(label).variant(if p.lead_pending.is_some() { ButtonVariant::Ghost } else { ButtonVariant::Contrast }).control_size(ControlSize::Small).on_press_tx(move |s: &mut App, tx: &mut Tx| crate::over::projects::set_lead(s, &pk, tx)).build(&k).key("set-lead");
            match &p.lead_pending {
                Some((surface, note)) => col()
                    .w_full()
                    .h_full()
                    .center()
                    .gap(10.0)
                    .child(ui::avatar(&p.lead_name, 28.0))
                    .child(text(format!("{}, the lead", p.lead_name)).font(k.text(BODY_MED)).color(strong(&k)))
                    .child(text(format!("{surface} · {note}")).font(k.text(META)).color(weak(&k)))
                    .children(crate::over::decisions::brief_note(s, key, &k))
                    .child(pending_composer(s, key, &p.lead_name, &k))
                    .child(start),
                None => col().w_full().h_full().center().gap(8.0).child(text("This project has no lead yet.".to_string()).font(k.text(BODY_MED)).color(weak(&k))).child(start).children(crate::over::decisions::brief_note(s, key, &k)),
            }
        }
    };
    ui::frame(cx, s, Some(ui::sidebar(s, Some(key), p.lead.as_deref(), &k)), main, Some(panel), toolbar(s, key, &k), main_kit)
}

pub fn board(s: &App, cx: &mut Cx<App>, key: &str, _layer: &mut Vec<El>, main_kit: &Kit) -> El {
    let k = cx.kit();
    let Some(p) = s.ov.d.project(key) else { return col() };
    let cols = s.ov.d.board(key).lanes;
    let looked = s.ov.looked.get(key).map(String::as_str);
    let lines = s.ov.d.digest(p, s.ov.d.window(looked));
    let card = |l: &Line| {
        let fit = format!("since-{:?}-{}", l.kind, crate::over::rows::target_id(&l.target));
        let width = ui::fit_width(s, &fit);
        let ago = s.ov.d.ago(&l.at);
        let shows = (l.kind, &l.target, &l.text, &ago, width.map(f32::to_bits));
        crate::over::rows::memo(fit.clone(), shows, &k, || {
            let (glyph, color) = ui::happened_style(l.kind, &k);
            let t = l.target.clone();
            col()
                .key(fit.clone())
                .grow(1.0)
                .w(1.0)
                .gap(5.0)
                .padding(10.0)
                .radius(10.0)
                .fill(k.c.surface_base)
                .border(1.0, ui::edge())
                .hover(k.c.surface_raised_hover)
                .on_press(move |s: &mut App| crate::over::open_target(s, &t))
                .child(row().gap(6.0).items_center().child(rune::icon(glyph, k.text_size(13.0)).color(color)).child(text(l.kind.label().to_string()).font(k.text(META_MED)).color(color)).child(ui::spacer()).child(text(ago.clone()).font(k.text(META)).color(weak(&k))))
                .child(ui::fitted(&fit, &l.text, width, META, strong(&k), 2, &k))
        })
    };
    let (new, old) = cut(pick(&lines, 1), looked);
    let mut since = col().w_full().gap(8.0);
    for (half, start) in [(&new, 0), (&old, new.len())] {
        if half.is_empty() {
            continue;
        }
        if start > 0 {
            since = since.child(ui::new_divider("since-new".into(), &k));
        }
        since = since.child(row().w_full().gap(8.0).items_stretch().children(half.iter().map(|l| card(l))));
    }
    let counts: Vec<String> = HAPPENED.iter().map(|h| format!("{} {}", count(&lines, *h), h.word())).collect();
    let summary = format!("{}, {}", counts.join(" · "), crate::over::now::since_note(s, looked).to_lowercase());
    let mut board = row().w_full().gap(12.0).items_start();
    for (i, title) in LANES.iter().enumerate() {
        let items = &cols[i];
        let mut c = col().grow(1.0).w(1.0).min_w(0.0).gap(8.0).child(
            row()
                .w_full()
                .gap(8.0)
                .items_center()
                .pb(2.0)
                .child(text(title.to_string()).font(k.text(META_MED)).color(if i == 0 { k.c.warning.icon } else { base(&k) }))
                .child(Badge::new(items.len().to_string()).variant(BadgeVariant::Subtle).build(&k)),
        );
        let (cards, more) = lane(s, key, title, items, false, &k);
        c = c.children(cards).children(more);
        if items.is_empty() {
            c = c.child(text("Empty".to_string()).font(k.text(META)).color(weak(&k)).pl(4.0));
        }
        board = board.child(c);
    }
    let body = col()
        .w_full()
        .gap(14.0)
        .px(22.0)
        .pt(16.0)
        .pb(24.0)
        .child(row().w_full().items_center().gap(10.0).child(text("Since you left".to_string()).font(k.text(HEAD)).color(strong(&k))).child(text(summary).font(k.text(META)).color(weak(&k)).truncate().shrink()))
        .children(crate::over::decisions::brief_note(s, key, &k))
        .child(since)
        .children(rows_note(s, key, &k))
        .child(board.mt(8.0))
        .children(crate::over::projects::merged(s, key, &k).map(|m| m.mt(8.0)))
        .child(crate::over::earlier::section(s, key, &k).mt(8.0))
        .children(crate::over::earlier::folders(s, key, &k).map(|f| f.mt(8.0)));
    let hint = row()
        .gap(6.0)
        .items_center()
        .child(ui::ghost_keys(&["L"], &k))
        .child(text(format!("opens the chat with {}", p.lead_name)).font(k.text(META)).color(weak(&k)));
    let mut main = col().w_full().h_full().child(col().key("board").w_full().grow(1.0).min_h(0.0).scroll(ScrollSpec { stick_bottom: false }).child(body));
    if let Some(lead) = &p.lead {
        main = main.child(
            col()
                .w_full()
                .items_center()
                .border_sides([1.0, 0.0, 0.0, 0.0], ui::rule(&k))
                .pt(12.0)
                .child(composer(s, &format!("Tell the lead, {}…", p.lead_name), lead, &k, main_kit))
                .child(row().w_full().max_w(780.0).px(28.0).pb(10.0).child(hint)),
        );
    }
    ui::frame(cx, s, Some(ui::sidebar(s, Some(key), None, &k)), main, None, toolbar(s, key, &k), main_kit)
}

fn pending_composer(s: &App, key: &str, lead: &str, k: &Kit) -> El {
    let (to, to2) = (key.to_string(), key.to_string());
    let sending = crate::over::projects::messaging(s, key);
    let empty = TextEdit::new("");
    let draft = s.ov.projects.lead_drafts.get(key).unwrap_or(&empty);
    let edit_key = key.to_string();
    let input = text_input(draft, k.text(BODY.lh(21.0)))
        .key("lead-first-message")
        .label("Message")
        .w_full()
        .min_h(44.0)
        .color(strong(k))
        .placeholder(format!("Tell {lead}…"), weak(k))
        .on_edit(move |s: &mut App| crate::over::projects::lead_draft(s, &edit_key))
        .on_submit(move |s: &mut App, _: &mut Tx| crate::over::projects::message_lead(s, &to));
    let send = IconButton::new(&icons::ARROW_UP).variant(IconButtonVariant::Submit).label("Send").on_press(move |s: &mut App| crate::over::projects::message_lead(s, &to2)).build(k);
    col()
        .w_full()
        .max_w(620.0)
        .px(28.0)
        .child(
            col()
                .w_full()
                .gap(8.0)
                .padding(12.0)
                .radius(14.0)
                .fill(k.c.surface_raised)
                .border(1.0, ui::edge())
                .child(input)
                .child(row().w_full().items_center().child(text(if sending { "Sending…" } else { "The first message starts its conversation" }.to_string()).font(k.text(META)).color(weak(k))).child(ui::spacer()).child(send)),
        )
}

fn lead_chat(s: &App, lead: &str, main_kit: &Kit) -> El {
    let lead = feed::composer::current(&s.rekeyed, lead);
    if s.panes.get(lead).is_some_and(|p| !p.blocks.is_empty()) {
        return crate::transcript::view(s, lead, main_kit);
    }
    let status = s.panes.get(lead).map(|p| p.status.clone()).unwrap_or_default();
    let note = if matches!(status.as_str(), "" | "loading" | "live") { "Loading the conversation…".to_string() } else { status };
    crate::transcript::loading(note, main_kit)
}

fn composer(s: &App, placeholder: &str, lead: &str, k: &Kit, main_kit: &Kit) -> El {
    let lead = feed::composer::current(&s.rekeyed, lead);
    if let Some(why) = crate::session(s, lead).and_then(feed::notice::read_only) {
        return col().w_full().max_w(780.0).px(28.0).pb(16.0).child(col().w_full().radius(14.0).fill(k.c.surface_raised).border(1.0, ui::edge()).child(crate::ui::session::closed_note(why, k))).on_layout(|s: &mut App, r: [f32; 4]| s.cover.set_composer(r));
    }
    let (to, to2) = (lead.to_string(), lead.to_string());
    let input = crate::ui::paste::field(text_input(&s.composer, k.text(BODY.lh(21.0)))
        .key("composer")
        .label("Message")
        .w_full()
        .min_h(44.0)
        .color(strong(k))
        .placeholder(placeholder.to_string(), weak(k))
        .on_edit(|s: &mut App| &mut s.composer))
        .on_input_key(crate::ui::complete::key)
        .on_submit(move |s: &mut App, tx: &mut Tx| crate::send_to(s, &to, tx));
    let as_note = crate::session(s, lead).is_some_and(|x| x.capabilities.reaches_as_note).then(|| feed::notice::SENDS_AS_NOTE.to_string());
    let moving = crate::session(s, lead).is_some_and(|x| x.moving.is_some());
    let status = moving.then(|| feed::moves::COMPOSER_NOTE.to_string()).or_else(|| crate::composer_note(s, lead)).or(as_note).or_else(|| crate::send_busy(s, lead).map(|b| b.hint().to_string())).unwrap_or_else(|| "Sends as you".into());
    let controls = row()
        .w_full()
        .items_center()
        .gap(6.0)
        .children(crate::ui::attach::button(s, lead, k))
        .children(crate::session(s, lead).and_then(|x| crate::ui::holders::picker(s, x, k)))
        .child(ui::spacer())
        .child(match crate::composer_note(s, lead).filter(|_| moving) {
            Some(note) => text(status).font(k.text(META)).color(weak(k)).help(note),
            None => text(status).font(k.text(META)).color(weak(k)),
        });
    let controls = match crate::busy_delivery(s, lead) {
        Some(choice) => {
            let to = lead.to_string();
            controls.child(Button::new(choice.label()).variant(ButtonVariant::Ghost).control_size(ControlSize::Small).build(k).key("busy-delivery").on_press(move |s: &mut App| crate::pick_busy(s, &to, choice.other())))
        }
        None => controls,
    };
    let controls = if crate::can_stop(s, lead) {
        let to = lead.to_string();
        controls.child(Button::new("Stop").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).build(k).key("stop").on_press(move |s: &mut App| crate::stop(s, &to)))
    } else {
        controls
    };
    let controls = if s.unsent.has(lead) {
        let (again, back, drop, new) = (lead.to_string(), lead.to_string(), lead.to_string(), lead.to_string());
        let mut controls = controls;
        if crate::unsent_held(s, lead) {
            controls = controls.child(Button::new(crate::confirm_label(s, lead, Confirm::PutBack)).variant(ButtonVariant::Ghost).build(k).key("send-put-back").on_press(move |s: &mut App| crate::put_back_unsent(s, &back)));
        }
        controls = controls.child(Button::new(crate::confirm_label(s, lead, Confirm::Discard)).variant(ButtonVariant::Ghost).build(k).key("send-dismiss").on_press(move |s: &mut App| crate::dismiss_unsent(s, &drop)));
        if crate::offers_new_send(s, lead) {
            controls = controls.child(Button::new(crate::confirm_label(s, lead, Confirm::SendNew)).variant(ButtonVariant::Ghost).build(k).key("send-new").on_press_tx(move |s: &mut App, tx: &mut Tx| crate::send_as_new(s, &new, tx)));
        }
        if crate::can_retry(s, lead) {
            controls.child(Button::new("Retry").variant(ButtonVariant::Ghost).build(k).key("send-retry").on_press_tx(move |s: &mut App, tx: &mut Tx| crate::retry(s, &again, tx)))
        } else {
            controls
        }
    } else {
        controls
    };
    let lead_session = crate::session(s, lead);
    let controls = controls
        .child(IconButton::new(&icons::ARROW_UP).variant(IconButtonVariant::Submit).label("Send").on_press_tx(move |s: &mut App, tx: &mut Tx| crate::ui::complete::submit(s, &to2, tx)).build(k));
    col()
        .w_full()
        .max_w(780.0)
        .px(28.0)
        .pb(16.0)
        .on_layout(|s: &mut App, r: [f32; 4]| {
            s.cover.set_composer(r);
            crate::trace::composer(r);
        })
        .children(lead_session.and_then(|x| crate::ui::notice::above(s, x, main_kit)))
        .child(
            col()
                .w_full()
                .gap(8.0)
                .padding(12.0)
                .radius(14.0)
                .fill(k.c.surface_raised)
                .border(1.0, if s.attach.dropping { crate::theme::accent() } else { ui::edge() })
                .children(lead_session.and_then(|x| crate::ui::complete::search(s, x, k)))
                .children(lead_session.and_then(|x| crate::ui::complete::menu(s, x, k)))
                .children(crate::ui::attach::chips(s, lead, main_kit))
                .children(crate::ui::paste::editor(s, main_kit))
                .child(input)
                .child(controls),
        )
}
