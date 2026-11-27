use rune::{Cx, El, ScrollSpec, col, row, text};
use rune_kit::shell::Toolbar;
use rune_kit::{Badge, BadgeVariant, Button, ButtonVariant, ControlSize, Kit, KitCx, SegmentedControl};
use rune_theme_opencode::icons;

use feed::derive::board::{LANES, fold};
use feed::derive::digest::{HAPPENED, count, pick};
use feed::derive::phase::{CHECK, SHIP, phase};
use feed::derive::sessions::busy;

use crate::data::{Work, plain, short_id};
use crate::ui::{self, BODY_MED, HEAD, META, META_MED, base, strong, weak};
use crate::{App, Screen, go};

fn variant(s: &App, key: &str, k: &Kit) -> El {
    let id = key.to_string();
    let seg = SegmentedControl::new(&["Lead chat", "Board"], Some(if crate::layout::board_for(s, key) { 1 } else { 0 }))
        .label("Project view")
        .fit()
        .on_pick(move |s: &mut App, i, _| {
            s.boards.insert(id.clone(), i == 1);
        })
        .build(k);
    row().gap(6.0).items_center().child(seg).child(ui::ghost_keys(&["⌥⌘B"], k))
}

fn toolbar(s: &App, key: &str, k: &Kit) -> Toolbar {
    let name = s.d.project(key).map(|p| p.name.clone()).unwrap_or_default();
    Toolbar::new()
        .leading(row().gap(14.0).items_center().child(ui::avatar(&name, 18.0)).child(ui::crumb(&[("Projects", false), (&name, true)], k)))
        .trailing(row().gap(10.0).items_center().child(variant(s, key, k)).child(ui::needs_button(s, k)))
}

pub fn work_card(s: &App, w: &Work, compact: bool, k: &Kit) -> El {
    let key = w.key();
    let phase = phase(w);
    let whose = s.d.whose_move(w);
    let who = match phase {
        CHECK => "Your move".to_string(),
        SHIP => "Shipped".to_string(),
        _ => format!("{}'s move", ui::short_title(&whose, 22)),
    };
    let mut c = col()
        .key(format!("w-{}-{}", w.ledger_key(), w.id))
        .w_full()
        .gap(8.0)
        .padding(12.0)
        .radius(10.0)
        .fill(k.c.surface_base)
        .border(1.0, if phase == CHECK { k.c.warning.icon.alpha(0.35) } else { ui::edge(k) })
        .hover(k.c.surface_raised_hover)
        .on_press(move |s: &mut App| go(s, Screen::Work(key.clone())))
        .child(ui::title_fit(s, &format!("card-{}-{compact}", w.id), &plain(&w.text), BODY_MED.lh(19.0), strong(k), 2));
    c = c.child(ui::mini_phase(w, k));
    let last = w.timeline.last().map(|t| s.d.ago(&t.at)).unwrap_or_default();
    c.child(
        row()
            .w_full()
            .gap(6.0)
            .items_center()
            .child(text(who).font(if phase == CHECK { META_MED } else { META }).color(if phase == CHECK { ui::needs_color(k) } else { weak(k) }).truncate().shrink())
            .child(ui::spacer())
            .child(text(last).font(META).color(weak(k)))
            .child(ui::id_label(&short_id(&w.id), k)),
    )
}

fn lane(s: &App, key: &str, title: &str, items: &[&Work], compact: bool, k: &Kit) -> (Vec<El>, Option<El>) {
    let lane = format!("{key}/{title}");
    let open = s.open_lanes.contains(&lane);
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
                if !s.open_lanes.remove(&lane) {
                    s.open_lanes.insert(lane.clone());
                }
            })
            .child(rune::icon(if open { &icons::CHEVRON_DOWN } else { &icons::CHEVRON_RIGHT }, 12.0).color(weak(k)))
            .child(text(label).font(META_MED).color(weak(k)))
}

fn group(s: &App, key: &str, title: &str, items: &[&Work], empty: &str, k: &Kit) -> El {
    let mut c = col().w_full().gap(8.0).child(ui::section_head(title, Some(items.len()), None, k));
    if items.is_empty() {
        c = c.child(text(empty.to_string()).font(META).color(weak(k)));
    }
    let (cards, more) = lane(s, key, title, items, true, k);
    c.children(cards).children(more)
}

fn crew_panel(s: &App, lead: Option<&str>, k: &Kit) -> El {
    let crew = lead.map(|l| s.d.crew(l)).unwrap_or_default();
    let mut c = col().w_full().gap(6.0).child(ui::section_head("Crew", Some(crew.len()), Some(text("started by the lead".to_string()).font(META).color(weak(k))), k));
    if crew.is_empty() {
        c = c.child(text("The lead has no workers running today.".to_string()).font(META).color(weak(k)));
    }
    for (i, x) in crew.iter().take(5).enumerate() {
        let id = x.id.clone();
        let status = if busy(x) { rune_kit::Spinner::new().size(11.0).build(k) } else { rune::icon(&icons::CIRCLE_CHECK, 12.0).color(weak(k)) };
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
                .on_press(move |s: &mut App| go(s, Screen::Session(id.clone())))
                .child(row().pt(3.0).child(status))
                .child(
                    col()
                        .grow(1.0)
                        .shrink()
                        .min_w(0.0)
                        .gap(2.0)
                        .child(row().gap(6.0).items_center().child(text(ui::short_title(&s.d.title(x), 30)).font(META_MED).color(strong(k)).truncate().shrink()).child(ui::spacer()).child(text(s.d.ago(&x.last_active)).font(META).color(weak(k))))
                        .child(text(s.d.gist(x)).font(META).color(weak(k)).max_lines(2).wrap()),
                ),
        );
    }
    c
}

fn waiting_line(s: &App, lead: &str, k: &Kit) -> El {
    let Some(x) = s.d.session(lead) else { return col() };
    let crew = s.d.crew(lead);
    let busy: Vec<String> = crew.iter().filter(|c| busy(c)).map(|c| s.d.title(c)).collect();
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
        .child(text(label).font(META).color(weak(k)))
        .child(text(who).font(META_MED).color(k.c.text_accent).truncate().shrink())
}

pub fn lead(s: &App, cx: &mut Cx<App>, key: &str, layer: &mut Vec<El>) -> El {
    let k = cx.kit();
    let Some(p) = s.d.project(key) else { return col() };
    let cols = s.d.board(key).lanes;
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
                .child(group(s, key, LANES[0], &cols[0], "Nothing waits on you here.", &k))
                .child(group(s, key, LANES[1], &cols[1], "Nothing is being built.", &k))
                .child(group(s, key, LANES[2], &cols[2], "The queue is empty.", &k)),
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
                .child(col().gap(1.0).child(text(format!("{}, the lead", p.lead_name)).font(BODY_MED).color(strong(&k))).child(text(s.d.session(lead).map(|x| s.d.gist(x)).unwrap_or_default()).font(META).color(weak(&k)).truncate().max_w(620.0)))
                .child(ui::spacer())
                .child(Button::new("Workers").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).icon(&icons::BULLET_LIST).on_press(|s: &mut App| crate::open_jump(s, "")).build(&k));
            col()
                .w_full()
                .h_full()
                .items_center()
                .child(head)
                .child(crate::session::transcript(s, lead, 22, &k))
                .child(waiting_line(s, lead, &k))
                .child(crate::session::composer(s, &format!("Tell {}…", p.lead_name), None, layer, &k))
        }
        None => col().w_full().h_full().center().child(text("This project has no lead yet.".to_string()).font(BODY_MED).color(weak(&k))),
    };
    ui::frame(cx, s, Some(ui::sidebar(s, Some(key), p.lead.as_deref(), &k)), main, Some(panel), toolbar(s, key, &k))
}

pub fn board(s: &App, cx: &mut Cx<App>, key: &str, layer: &mut Vec<El>) -> El {
    let k = cx.kit();
    let Some(p) = s.d.project(key) else { return col() };
    let cols = s.d.board(key).lanes;
    let lines = s.d.digest(p, crate::now::WINDOW);
    let mut since = row().w_full().gap(8.0).items_stretch();
    for (i, l) in pick(&lines, 1).iter().enumerate() {
        let (glyph, color) = ui::happened_style(l.kind, &k);
        let t = l.target.clone();
        since = since.child(
            col()
                .key(format!("since-{i}"))
                .grow(1.0)
                .w(1.0)
                .gap(5.0)
                .padding(10.0)
                .radius(10.0)
                .fill(k.c.surface_base)
                .border(1.0, ui::edge(&k))
                .hover(k.c.surface_raised_hover)
                .on_press(move |s: &mut App| crate::open_target(s, &t))
                .child(row().gap(6.0).items_center().child(rune::icon(glyph, 13.0).color(color)).child(text(l.kind.label().to_string()).font(META_MED).color(color)).child(ui::spacer()).child(text(s.d.ago(&l.at)).font(META).color(weak(&k))))
                .child(ui::title_fit(s, &format!("since-{i}"), &l.text, META, strong(&k), 2)),
        );
    }
    let counts: Vec<String> = HAPPENED.iter().map(|h| format!("{} {}", count(&lines, *h), h.word())).collect();
    let summary = format!("{}, last {:.0} hours", counts.join(" · "), crate::now::WINDOW);
    let mut board = row().w_full().gap(12.0).items_start();
    for (i, title) in LANES.iter().enumerate() {
        let items = &cols[i];
        let mut c = col().grow(1.0).w(1.0).min_w(0.0).gap(8.0).child(
            row()
                .w_full()
                .gap(8.0)
                .items_center()
                .pb(2.0)
                .child(text(title.to_string()).font(META_MED).color(if i == 0 { k.c.warning.icon } else { base(&k) }))
                .child(Badge::new(items.len().to_string()).variant(BadgeVariant::Subtle).build(&k)),
        );
        let (cards, more) = lane(s, key, title, items, false, &k);
        c = c.children(cards).children(more);
        if items.is_empty() {
            c = c.child(text("Empty".to_string()).font(META).color(weak(&k)).pl(4.0));
        }
        board = board.child(c);
    }
    let body = col()
        .w_full()
        .gap(14.0)
        .px(22.0)
        .pt(16.0)
        .pb(24.0)
        .child(row().w_full().items_center().gap(10.0).child(text("Since you left".to_string()).font(HEAD).color(strong(&k))).child(text(summary).font(META).color(weak(&k)).truncate().shrink()))
        .child(since)
        .child(board.mt(8.0));
    let hint = row()
        .gap(6.0)
        .items_center()
        .child(ui::ghost_keys(&["L"], &k))
        .child(text(format!("opens the chat with {}", p.lead_name)).font(META).color(weak(&k)));
    let composer = col()
        .w_full()
        .items_center()
        .border_sides([1.0, 0.0, 0.0, 0.0], ui::rule(&k))
        .pt(12.0)
        .child(crate::session::composer(s, &format!("Tell the lead, {}…", p.lead_name), None, layer, &k))
        .child(row().w_full().max_w(780.0).px(28.0).pb(10.0).child(hint));
    let main = col().w_full().h_full().child(col().key("board").w_full().grow(1.0).min_h(0.0).scroll(ScrollSpec { stick_bottom: false }).child(body)).child(composer);
    ui::frame(cx, s, Some(ui::sidebar(s, Some(key), None, &k)), main, None, toolbar(s, key, &k))
}
