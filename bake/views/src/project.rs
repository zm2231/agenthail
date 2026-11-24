use rune::{Cx, El, ScrollSpec, col, row, text};
use rune_kit::shell::Toolbar;
use rune_kit::{Badge, BadgeVariant, Button, ButtonVariant, ControlSize, SegmentedControl};
use rune_theme_opencode::icons;

use crate::data::{Work, plain, short_id};
use crate::ui::{self, BODY_MED, HEAD, META, META_MED, base, kit, strong, weak};
use crate::{App, Screen, go};

fn variant(s: &App) -> El {
    SegmentedControl::new(&["Lead chat", "Board"], Some(if s.board { 1 } else { 0 }))
        .label("Layout")
        .fit()
        .on_pick(|s: &mut App, i, _| s.board = i == 1)
        .build(kit())
}

fn toolbar(s: &App, key: &str) -> Toolbar {
    let name = s.d.project(key).map(|p| p.name.clone()).unwrap_or_default();
    Toolbar::new()
        .leading(row().gap(14.0).items_center().child(ui::avatar(&name, 18.0)).child(ui::crumb(&[("Projects", false), (&name, true)])))
        .trailing(row().gap(10.0).items_center().child(variant(s)).child(ui::needs_button(s)))
}

pub fn work_card(s: &App, w: &Work, compact: bool) -> El {
    let k = kit();
    let id = w.id.clone();
    let phase = s.d.phase(w);
    let whose = s.d.whose_move(w);
    let who = match phase {
        4 => "Your move".to_string(),
        5 => "Shipped".to_string(),
        _ => format!("{}'s move", ui::short_title(&whose, 22)),
    };
    let mut c = col()
        .key(format!("w-{}", w.id))
        .w_full()
        .gap(8.0)
        .padding(12.0)
        .radius(10.0)
        .fill(k.c.surface_base)
        .border(1.0, if phase == 4 { k.c.warning.icon.alpha(0.35) } else { ui::rule() })
        .hover(k.c.surface_raised_hover)
        .on_press(move |s: &mut App| go(s, Screen::Work(id.clone())))
        .child(text(plain(&w.text)).font(BODY_MED.lh(19.0)).color(strong()).wrap_anywhere().max_lines(if compact { 2 } else { 3 }));
    c = c.child(ui::mini_phase(phase));
    let last = w.timeline.last().map(|t| s.d.ago(&t.at)).unwrap_or_default();
    c.child(
        row()
            .w_full()
            .gap(6.0)
            .items_center()
            .child(text(who).font(META_MED).color(if phase == 4 { k.c.warning.icon } else { base() }).truncate().shrink())
            .child(ui::spacer())
            .child(text(last).font(META).color(weak()))
            .child(ui::id_label(&short_id(&w.id))),
    )
}

fn group(s: &App, title: &str, items: &[&Work], max: usize, empty: &str) -> El {
    let mut c = col().w_full().gap(8.0).child(ui::section_head(title, Some(items.len()), None));
    if items.is_empty() {
        c = c.child(text(empty.to_string()).font(META).color(weak()));
    }
    for w in items.iter().take(max) {
        c = c.child(work_card(s, w, true));
    }
    if items.len() > max {
        c = c.child(text(format!("{} more", items.len() - max)).font(META).color(weak()));
    }
    c
}

fn crew_panel(s: &App, lead: Option<&str>) -> El {
    let k = kit();
    let crew = lead.map(|l| s.d.crew(l)).unwrap_or_default();
    let mut c = col().w_full().gap(6.0).child(ui::section_head("Crew", Some(crew.len()), Some(text("started by the lead".to_string()).font(META).color(weak()))));
    if crew.is_empty() {
        c = c.child(text("The lead has no workers running today.".to_string()).font(META).color(weak()));
    }
    for (i, x) in crew.iter().take(5).enumerate() {
        let id = x.id.clone();
        let busy = matches!(x.display.kind.as_str(), "working" | "finishing" | "starting");
        let status = if busy { rune_kit::Spinner::new().size(11.0).build(k) } else { rune::icon(&icons::CIRCLE_CHECK, 12.0).color(weak()) };
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
                        .child(row().gap(6.0).items_center().child(text(ui::short_title(&s.d.title(x), 30)).font(META_MED).color(strong()).truncate().shrink()).child(ui::spacer()).child(text(s.d.ago(&x.last_active)).font(META).color(weak())))
                        .child(text(s.d.gist(x)).font(META).color(base()).max_lines(2).wrap()),
                ),
        );
    }
    c
}

fn waiting_line(s: &App, lead: &str) -> El {
    let k = kit();
    let Some(x) = s.d.session(lead) else { return col() };
    let crew = s.d.crew(lead);
    let busy: Vec<String> = crew.iter().filter(|c| matches!(c.display.kind.as_str(), "working" | "finishing" | "starting")).map(|c| s.d.title(c)).collect();
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

pub fn lead(s: &App, cx: &mut Cx<App>, key: &str, layer: &mut Vec<El>) -> El {
    let Some(p) = s.d.project(key) else { return col() };
    let cols = s.d.board(key);
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
                .child(group(s, "Waiting on you", &cols[0], 3, "Nothing waits on you here."))
                .child(group(s, "Now", &cols[1], 3, "Nothing is being built."))
                .child(group(s, "Up next", &cols[2], 3, "The queue is empty.")),
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
                .child(col().gap(1.0).child(text(format!("{}, the lead", p.lead_name)).font(BODY_MED).color(strong())).child(text(s.d.session(lead).map(|x| s.d.gist(x)).unwrap_or_default()).font(META).color(weak()).truncate().max_w(620.0)))
                .child(ui::spacer())
                .child(Button::new("Workers").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).icon(&icons::BULLET_LIST).on_press(|s: &mut App| crate::open_jump(s, "")).build(kit()));
            col()
                .w_full()
                .h_full()
                .items_center()
                .child(head)
                .child(crate::session::transcript(s, lead, 22))
                .child(waiting_line(s, lead))
                .child(crate::session::composer(s, &format!("Tell {}…", p.lead_name), None, layer))
        }
        None => col().w_full().h_full().center().child(text("This project has no lead yet.".to_string()).font(BODY_MED).color(weak())),
    };
    ui::frame(cx, s, Some(ui::sidebar(s, Some(key), p.lead.as_deref())), main, Some(panel), toolbar(s, key))
}

pub fn board(s: &App, cx: &mut Cx<App>, key: &str, layer: &mut Vec<El>) -> El {
    let k = kit();
    let Some(p) = s.d.project(key) else { return col() };
    let cols = s.d.board(key);
    let lines = s.d.digest(p, crate::now::WINDOW);
    let mut since = row().w_full().gap(8.0).items_stretch();
    let mut picked: Vec<&crate::data::Line> = Vec::new();
    for kind in [crate::data::Happened::Shipped, crate::data::Happened::Waiting, crate::data::Happened::Failed, crate::data::Happened::Moved, crate::data::Happened::Said] {
        if let Some(l) = lines.iter().find(|l| l.kind == kind) {
            picked.push(l);
        }
    }
    for (i, l) in picked.iter().enumerate() {
        let (glyph, color) = ui::happened_style(l.kind);
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
                .border(1.0, ui::rule())
                .hover(k.c.surface_raised_hover)
                .on_press(move |s: &mut App| crate::open_target(s, &t))
                .child(row().gap(6.0).items_center().child(rune::icon(glyph, 13.0).color(color)).child(text(l.kind.label().to_string()).font(META_MED).color(color)).child(ui::spacer()).child(text(s.d.ago(&l.at)).font(META).color(weak())))
                .child(text(l.text.clone()).font(META).color(strong()).wrap_anywhere().max_lines(2)),
        );
    }
    let count = |kind| lines.iter().filter(|l| l.kind == kind).count();
    use crate::data::Happened::*;
    let summary = format!("{} shipped · {} waiting on you · {} moved · {} said, last {:.0} hours", count(Shipped), count(Waiting), count(Moved), count(Said), crate::now::WINDOW);
    let titles = ["Waiting on you", "Now", "Up next", "Shipped"];
    let mut board = row().w_full().gap(12.0).items_start();
    for (i, title) in titles.iter().enumerate() {
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
        for w in items.iter().take(6) {
            c = c.child(work_card(s, w, false));
        }
        if items.len() > 6 {
            c = c.child(text(format!("{} more", items.len() - 6)).font(META).color(weak()).pl(4.0));
        }
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
        .child(since)
        .child(board.mt(8.0));
    let hint = row()
        .gap(6.0)
        .items_center()
        .child(ui::ghost_keys(&["L"]))
        .child(text(format!("opens the chat with {}", p.lead_name)).font(META).color(weak()));
    let composer = col()
        .w_full()
        .items_center()
        .border_sides([1.0, 0.0, 0.0, 0.0], ui::rule())
        .pt(12.0)
        .child(crate::session::composer(s, &format!("Tell the lead, {}…", p.lead_name), None, layer))
        .child(row().w_full().max_w(780.0).px(28.0).pb(10.0).child(hint));
    let main = col().w_full().h_full().child(col().key("board").w_full().grow(1.0).min_h(0.0).scroll(ScrollSpec { stick_bottom: false }).child(body)).child(composer);
    ui::frame(cx, s, Some(ui::sidebar(s, Some(key), None)), main, None, toolbar(s, key))
}
