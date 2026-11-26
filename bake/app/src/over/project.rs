use rune::{Cx, El, ScrollSpec, Tx, col, row, text, text_input};
use rune_kit::shell::Toolbar;
use rune_kit::{Badge, BadgeVariant, Button, ButtonVariant, ControlSize, IconButton, IconButtonVariant, SegmentedControl};
use rune_theme_opencode::icons;

use feed::derive::board::{LANES, fold};
use feed::derive::digest::{HAPPENED, count, cut, pick};
use feed::derive::phase::{CHECK, SHIP, phase};
use feed::derive::sessions::busy;
use feed::derive::turn_end::turn_end;

use crate::over::{Line, Work, plain, short_id};
use crate::over::ui::{self, BODY, BODY_MED, HEAD, META, META_MED, base, kit, strong, weak};
use crate::over::go;
use crate::{App, Confirm, Screen};

fn variant(s: &App, key: &str) -> El {
    let k = key.to_string();
    let seg = SegmentedControl::new(&["Lead chat", "Board"], Some(if crate::over::layout::board_for(s, key) { 1 } else { 0 }))
        .label("Project view")
        .fit()
        .on_pick(move |s: &mut App, i, _| {
            s.ov.boards.insert(k.clone(), i == 1);
        })
        .build(kit());
    row().gap(6.0).items_center().child(seg).child(ui::ghost_keys(&["⌥⌘B"]))
}

fn toolbar(s: &App, key: &str) -> Toolbar {
    let name = s.ov.d.project(key).map(|p| p.name.clone()).unwrap_or_default();
    Toolbar::new()
        .leading(row().gap(14.0).items_center().child(ui::avatar(&name, 18.0)).child(ui::crumb(&[("Projects", false), (&name, true)])))
        .trailing(row().gap(10.0).items_center().child(crate::over::decisions::brief_button(s, key)).child(ui::new_session_button(Some(key))).child(variant(s, key)).child(ui::needs_button(s)))
}

pub fn work_card(s: &App, w: &Work, compact: bool) -> El {
    let k = kit();
    let id = w.id.clone();
    let phase = phase(w);
    let whose = s.ov.d.whose_move(w);
    let who = match phase {
        CHECK => "Your move".to_string(),
        SHIP => "Shipped".to_string(),
        _ => format!("{}'s move", ui::short_title(&whose, 22)),
    };
    let mut c = col()
        .key(format!("w-{}", w.id))
        .w_full()
        .gap(8.0)
        .padding(12.0)
        .radius(10.0)
        .fill(k.c.surface_base)
        .border(1.0, if phase == CHECK { k.c.warning.icon.alpha(0.35) } else { ui::edge() })
        .hover(k.c.surface_raised_hover)
        .on_press(move |s: &mut App| go(s, Screen::Work(id.clone())))
        .child(ui::title_fit(s, &format!("card-{}-{compact}", w.id), &plain(&w.text), BODY_MED.lh(19.0), strong(), 2));
    c = c.child(ui::mini_phase(phase));
    let last = w.timeline.last().map(|t| s.ov.d.ago(&t.at)).unwrap_or_default();
    c.child(
        row()
            .w_full()
            .gap(6.0)
            .items_center()
            .child(text(who).font(if phase == CHECK { META_MED } else { META }).color(if phase == CHECK { ui::needs_color() } else { weak() }).truncate().shrink())
            .child(ui::spacer())
            .child(text(last).font(META).color(weak()))
            .child(ui::id_label(&short_id(&w.id))),
    )
}

fn lane(s: &App, key: &str, title: &str, items: &[&Work], compact: bool) -> (Vec<El>, Option<El>) {
    let lane = format!("{key}/{title}");
    let open = s.ov.open_lanes.contains(&lane);
    let f = fold(items, open);
    let cards = f.shown.iter().map(|w| work_card(s, w, compact)).collect();
    (cards, f.foldable.then(|| lane_more(lane, open, f.hidden)))
}

fn lane_more(lane: String, open: bool, hidden: usize) -> El {
    let k = kit();
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
            .child(rune::icon(if open { &icons::CHEVRON_DOWN } else { &icons::CHEVRON_RIGHT }, 12.0).color(weak()))
            .child(text(label).font(META_MED).color(weak()))
}

fn rows_note(s: &App, key: &str) -> Option<El> {
    let rows = *s.ov.d.rows.get(key)?;
    let color = if rows == feed::derive::Rows::Loading { weak() } else { ui::needs_color() };
    Some(row().w_full().gap(6.0).items_center().child(rune::icon(&icons::INFO, 13.0).color(color)).child(text(rows.note().to_string()).font(META).color(color).wrap()))
}

fn group(s: &App, key: &str, title: &str, items: &[&Work], empty: &str) -> El {
    let mut c = col().w_full().gap(8.0).child(ui::section_head(title, Some(items.len()), None));
    if items.is_empty() {
        c = c.child(text(empty.to_string()).font(META).color(weak()));
    }
    let (cards, more) = lane(s, key, title, items, true);
    c.children(cards).children(more)
}

fn crew_panel(s: &App, lead: Option<&str>) -> El {
    let k = kit();
    let crew = lead.map(|l| s.ov.d.crew(l)).unwrap_or_default();
    let mut c = col().w_full().gap(6.0).child(ui::section_head("Crew", Some(crew.len()), Some(text("started by the lead".to_string()).font(META).color(weak()))));
    if crew.is_empty() {
        c = c.child(text("The lead has no workers running today.".to_string()).font(META).color(weak()));
    }
    for (i, x) in crew.iter().take(5).enumerate() {
        let id = x.id.clone();
        let end = turn_end(x, !s.ov.d.asks(&x.id).iter().all(|a| a.answer.is_some()));
        let status = if busy(x) { rune_kit::Spinner::new().size(11.0).build(k) } else { rune::icon(&icons::CIRCLE_CHECK, 12.0).color(weak()) };
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
                        .child(row().gap(6.0).items_center().child(text(ui::short_title(&s.ov.d.title(x), 30)).font(META_MED).color(strong()).truncate().shrink()).child(ui::spacer()).child(text(s.ov.d.ago(&x.last_active)).font(META).color(weak())))
                        .child(text(end.label.clone()).font(META_MED).color(if end.needs_you { ui::needs_color() } else { base() }))
                        .children(Some(if end.headline.is_empty() { s.ov.d.gist(x) } else { crate::over::plain(&end.headline) }).filter(|t| !t.is_empty() && *t != end.label).map(|t| text(t).font(META).color(weak()).max_lines(3).wrap())),
                ),
        );
    }
    c
}

fn waiting_line(s: &App, lead: &str) -> El {
    let k = kit();
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
        .child(rune_kit::Spinner::new().size(11.0).build(k))
        .child(text(label).font(META).color(weak()))
        .child(text(who).font(META_MED).color(k.c.text_accent).truncate().shrink())
}

pub fn lead(s: &App, cx: &mut Cx<App>, key: &str, _layer: &mut Vec<El>) -> El {
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
                .child(crew_panel(s, p.lead.as_deref()))
                .children(rows_note(s, key))
                .child(group(s, key, LANES[0], &cols[0], "Nothing waits on you here."))
                .child(group(s, key, LANES[1], &cols[1], "Nothing is being built."))
                .child(group(s, key, LANES[2], &cols[2], "The queue is empty.")),
        );
    let main = match &p.lead {
        Some(lead) => {
            let head = row()
                .w_full()
                .px(28.0)
                .py(10.0)
                .gap(10.0)
                .items_center()
                .border_sides([0.0, 0.0, 1.0, 0.0], ui::rule())
                .child(ui::avatar(&p.lead_name, 22.0))
                .child(col().gap(1.0).child(text(format!("{}, the lead", p.lead_name)).font(BODY_MED).color(strong())).child(text(s.ov.d.session(lead).map(|x| s.ov.d.gist(x)).unwrap_or_default()).font(META).color(weak()).truncate().max_w(620.0)))
                .child(ui::spacer())
                .child(Button::new("Workers").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).icon(&icons::BULLET_LIST).on_press_tx(|s: &mut App, tx: &mut Tx| crate::over::open_jump(s, tx)).build(kit()));
            crate::ui::attach::drop_target(col()
                .w_full()
                .h_full()
                .items_center()
                .child(head)
                .children(crate::over::decisions::brief_note(s, key).map(|n| row().w_full().px(28.0).py(8.0).border_sides([0.0, 0.0, 1.0, 0.0], ui::rule()).child(n)))
                .child(lead_chat(s, lead))
                .child(waiting_line(s, lead))
                .child(composer(s, &format!("Tell {}…", p.lead_name), lead)))
        }
        None => col().w_full().h_full().center().gap(8.0).child(text("This project has no lead yet.".to_string()).font(BODY_MED).color(weak())).children(crate::over::decisions::brief_note(s, key)),
    };
    ui::frame(cx, s, Some(ui::sidebar(s, Some(key), p.lead.as_deref())), main, Some(panel), toolbar(s, key))
}

pub fn board(s: &App, cx: &mut Cx<App>, key: &str, _layer: &mut Vec<El>) -> El {
    let k = kit();
    let Some(p) = s.ov.d.project(key) else { return col() };
    let cols = s.ov.d.board(key).lanes;
    let looked = s.ov.looked.get(key).map(String::as_str);
    let lines = s.ov.d.digest(p, s.ov.d.window(looked));
    let card = |i: usize, l: &Line| {
        let (glyph, color) = ui::happened_style(l.kind);
        let t = l.target.clone();
        col()
            .key(format!("since-{i}"))
            .grow(1.0)
            .w(1.0)
            .gap(5.0)
            .padding(10.0)
            .radius(10.0)
            .fill(k.c.surface_base)
            .border(1.0, ui::edge())
            .hover(k.c.surface_raised_hover)
            .on_press(move |s: &mut App| crate::over::open_target(s, &t))
            .child(row().gap(6.0).items_center().child(rune::icon(glyph, 13.0).color(color)).child(text(l.kind.label().to_string()).font(META_MED).color(color)).child(ui::spacer()).child(text(s.ov.d.ago(&l.at)).font(META).color(weak())))
            .child(ui::title_fit(s, &format!("since-{i}"), &l.text, META, strong(), 2))
    };
    let (new, old) = cut(pick(&lines, 1), looked);
    let mut since = col().w_full().gap(8.0);
    for (half, start) in [(&new, 0), (&old, new.len())] {
        if half.is_empty() {
            continue;
        }
        if start > 0 {
            since = since.child(ui::new_divider("since-new".into()));
        }
        since = since.child(row().w_full().gap(8.0).items_stretch().children(half.iter().enumerate().map(|(i, l)| card(start + i, l))));
    }
    let counts: Vec<String> = HAPPENED.iter().map(|k| format!("{} {}", count(&lines, *k), k.word())).collect();
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
                .child(text(title.to_string()).font(META_MED).color(if i == 0 { k.c.warning.icon } else { base() }))
                .child(Badge::new(items.len().to_string()).variant(BadgeVariant::Subtle).build(k)),
        );
        let (cards, more) = lane(s, key, title, items, false);
        c = c.children(cards).children(more);
        if items.is_empty() {
            c = c.child(text("Empty".to_string()).font(META).color(weak()).pl(4.0));
        }
        board = board.child(c);
    }
    let body = col()
        .w_full()
        .gap(14.0)
        .px(22.0)
        .pt(16.0)
        .pb(24.0)
        .child(row().w_full().items_center().gap(10.0).child(text("Since you left".to_string()).font(HEAD).color(strong())).child(text(summary).font(META).color(weak()).truncate().shrink()))
        .children(crate::over::decisions::brief_note(s, key))
        .child(since)
        .children(rows_note(s, key))
        .child(board.mt(8.0));
    let hint = row()
        .gap(6.0)
        .items_center()
        .child(ui::ghost_keys(&["L"]))
        .child(text(format!("opens the chat with {}", p.lead_name)).font(META).color(weak()));
    let mut main = col().w_full().h_full().child(col().key("board").w_full().grow(1.0).min_h(0.0).scroll(ScrollSpec { stick_bottom: false }).child(body));
    if let Some(lead) = &p.lead {
        main = main.child(
            col()
                .w_full()
                .items_center()
                .border_sides([1.0, 0.0, 0.0, 0.0], ui::rule())
                .pt(12.0)
                .child(composer(s, &format!("Tell the lead, {}…", p.lead_name), lead))
                .child(row().w_full().max_w(780.0).px(28.0).pb(10.0).child(hint)),
        );
    }
    ui::frame(cx, s, Some(ui::sidebar(s, Some(key), None)), main, None, toolbar(s, key))
}

fn lead_chat(s: &App, lead: &str) -> El {
    let lead = feed::composer::current(&s.rekeyed, lead);
    if s.panes.get(lead).is_some_and(|p| !p.blocks.is_empty()) {
        return crate::transcript::view(s, lead);
    }
    let status = s.panes.get(lead).map(|p| p.status.clone()).unwrap_or_default();
    let note = if matches!(status.as_str(), "" | "loading" | "live") { "Loading the conversation…".to_string() } else { status };
    crate::transcript::loading(note)
}

fn composer(s: &App, placeholder: &str, lead: &str) -> El {
    let lead = feed::composer::current(&s.rekeyed, lead);
    let k = kit();
    let (to, to2) = (lead.to_string(), lead.to_string());
    let input = text_input(&s.composer, BODY.lh(21.0))
        .key("composer")
        .label("Message")
        .w_full()
        .min_h(44.0)
        .color(strong())
        .placeholder(placeholder.to_string(), weak())
        .on_edit(|s: &mut App| &mut s.composer)
        .on_input_paste(|s: &mut App, content, _: &mut Tx| crate::ui::attach::paste(s, content))
        .on_submit(move |s: &mut App, tx: &mut Tx| crate::send_to(s, &to, tx));
    let status = crate::composer_note(s, lead).or_else(|| crate::send_busy(s, lead).map(|b| b.hint().to_string())).unwrap_or_else(|| "Sends as you".into());
    let controls = row()
        .w_full()
        .items_center()
        .gap(6.0)
        .children(crate::ui::attach::button(s, lead))
        .child(ui::spacer())
        .child(text(status).font(META).color(weak()));
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
        controls.child(Button::new("Retry").variant(ButtonVariant::Ghost).build(k).key("send-retry").on_press_tx(move |s: &mut App, tx: &mut Tx| crate::retry(s, &again, tx)))
    } else {
        controls
    };
    let controls = controls
        .child(IconButton::new(&icons::ARROW_UP).variant(IconButtonVariant::Submit).label("Send").on_press_tx(move |s: &mut App, tx: &mut Tx| crate::send_to(s, &to2, tx)).build(k));
    col()
        .w_full()
        .max_w(780.0)
        .px(28.0)
        .pb(16.0)
        .child(col().w_full().gap(8.0).padding(12.0).radius(14.0).fill(k.c.surface_raised).border(1.0, if s.attach.dropping { crate::theme::accent() } else { ui::edge() }).children(crate::ui::attach::chips(s, lead)).child(input).child(controls))
}
