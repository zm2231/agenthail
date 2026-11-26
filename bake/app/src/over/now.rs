use rune::{Cx, El, ScrollSpec, col, icon, row, text};
use rune_kit::shell::Toolbar;
use rune_kit::{Button, ButtonVariant, ControlSize, SegmentedControl, Spinner};
use rune_theme_opencode::icons;

use feed::derive::digest::{HAPPENED, count, cut, pick};
use feed::derive::sessions::busy;

use crate::over::{Happened, Line, Project};
use crate::over::ui::{self, BODY, BODY_MED, HEAD, META, META_MED, TITLE, base, kit, strong, weak};
use crate::over::{go, next_need, open_target};
use crate::{App, Screen};

pub const WINDOW: f64 = feed::derive::WINDOW_HOURS;

pub fn since_note(s: &App, looked: Option<&str>) -> String {
    match looked {
        Some(t) => match s.ov.d.ago(t).as_str() {
            "now" => "Since you looked just now".to_string(),
            ago => format!("Since you looked, {ago} ago"),
        },
        None => format!("Last {WINDOW:.0} hours"),
    }
}

fn variant(s: &App) -> El {
    let seg = SegmentedControl::new(&["Three panes", "Brief"], Some(if s.ov.now_brief { 1 } else { 0 }))
        .label("Layout")
        .fit()
        .on_pick(|s: &mut App, i, _| s.ov.now_brief = i == 1)
        .build(kit());
    row().gap(6.0).items_center().child(seg).child(ui::ghost_keys(&["⌥⌘B"]))
}

pub fn digest_line(s: &App, key: String, l: &Line) -> El {
    let k = kit();
    let (glyph, color) = ui::happened_style(l.kind);
    let t = l.target.clone();
    let mut body = col().grow(1.0).shrink().min_w(0.0).gap(2.0).child(
        row()
            .gap(8.0)
            .items_center()
            .child(text(l.kind.label().to_string()).font(META_MED).color(color))
            .child(text(l.text.clone()).font(BODY).color(strong()).truncate().shrink()),
    );
    if !l.detail.is_empty() {
        body = body.child(text(l.detail.clone()).font(META).color(weak()).truncate());
    }
    row()
        .key(key)
        .w_full()
        .gap(10.0)
        .items_start()
        .px(10.0)
        .py(7.0)
        .radius(8.0)
        .hover(k.c.surface_raised_hover)
        .on_press(move |s: &mut App| open_target(s, &t))
        .child(row().pt(2.0).child(icon(glyph, 15.0).color(color)))
        .child(body)
        .child(text(s.ov.d.ago(&l.at)).font(META).color(weak()))
}

pub fn digest_block(s: &App, p: &Project, lines: &[Line], max: usize, looked: Option<&str>, catch_up: bool) -> El {
    let k = kit();
    let key = p.key.clone();
    let summary: Vec<String> = HAPPENED.iter().map(|k| (count(lines, *k), k.word())).filter(|(n, _)| *n > 0).map(|(n, w)| format!("{n} {w}")).collect();
    let head = row()
        .w_full()
        .gap(10.0)
        .items_center()
        .child(ui::avatar(&p.name, 22.0))
        .child(text(p.name.clone()).font(HEAD).color(strong()))
        .child(text(summary.join(" · ")).font(META).color(weak()).truncate().shrink())
        .child(ui::spacer())
        .children(catch_up.then(|| crate::over::decisions::brief_button(s, &p.key)))
        .child(Button::new("Open project").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| go(s, Screen::Project(key.clone()))).build(k));
    let mut c = col().w_full().gap(2.0).padding(14.0).child(head.mb(6.0));
    if catch_up && let Some(note) = crate::over::decisions::brief_note(s, &p.key) {
        c = c.child(note.pl(32.0).pb(6.0));
    }
    let mut shown = pick(lines, 2);
    shown.truncate(max);
    let (new, old) = cut(shown, looked);
    let divider = !new.is_empty() && !old.is_empty();
    for (i, l) in new.iter().enumerate() {
        c = c.child(digest_line(s, format!("d-{}-{i}", p.key), l));
    }
    if divider {
        c = c.child(ui::new_divider(format!("d-{}-new", p.key)));
    }
    for (i, l) in old.iter().enumerate() {
        c = c.child(digest_line(s, format!("d-{}-{}", p.key, new.len() + i), l));
    }
    if lines.len() > max {
        c = c.child(text(format!("{} more in {}", lines.len() - max, p.name)).font(META).color(weak()).pl(35.0).pt(4.0));
    }
    ui::card(c)
}

pub fn hands_on_card(s: &App, i: usize, id: &str) -> El {
    let k = kit();
    let Some(x) = s.ov.d.session(id) else { return col() };
    let busy = busy(x);
    let idc = id.to_string();
    let status = if busy {
        row().gap(6.0).items_center().child(Spinner::new().size(11.0).build(k)).child(text("Working".to_string()).font(META_MED).color(k.c.text_accent))
    } else {
        row().gap(6.0).items_center().child(icon(&icons::CIRCLE_CHECK, 12.0).color(weak())).child(text(format!("Idle {}", s.ov.d.ago(&x.last_active))).font(META).color(weak()))
    };
    let c = col()
        .w_full()
        .gap(8.0)
        .padding(12.0)
        .child(
            row()
                .w_full()
                .gap(8.0)
                .items_center()
                .child(ui::avatar(&x.project_name(), 18.0))
                .child(text(ui::short_title(&s.ov.d.title(x), 30)).font(BODY_MED).color(strong()).truncate().shrink())
                .child(ui::spacer())
                .child(ui::ghost_keys(&[format!("⌘{}", i + 1).as_str()])),
        )
        .child(text(if s.ov.d.gist(x) == x.display.label { "No summary yet".to_string() } else { s.ov.d.gist(x) }).font(META).color(weak()).max_lines(2).wrap())
        .child(row().w_full().items_center().child(status).child(ui::spacer()).child(text(x.project_name()).font(META).color(weak())));
    ui::card(c).key(format!("hands-{i}")).on_press(move |s: &mut App| crate::over::open_session(s, &idc))
}

pub fn moving_row(s: &App, i: usize, id: &str) -> El {
    let k = kit();
    let Some(x) = s.ov.d.session(id) else { return col() };
    let idc = id.to_string();
    let what = if !x.display.detail.is_empty() { x.display.detail.clone() } else { s.ov.d.gist(x) };
    row()
        .key(format!("moving-{i}"))
        .w_full()
        .gap(10.0)
        .items_start()
        .px(10.0)
        .py(8.0)
        .radius(8.0)
        .hover(k.c.surface_raised_hover)
        .on_press(move |s: &mut App| crate::over::open_session(s, &idc))
        .child(row().pt(3.0).child(Spinner::new().size(12.0).build(k)))
        .child(
            col()
                .grow(1.0)
                .shrink()
                .min_w(0.0)
                .gap(2.0)
                .child(row().gap(6.0).items_center().child(text(ui::short_title(&s.ov.d.title(x), 34)).font(META_MED).color(strong()).truncate().shrink()).child(text(x.project_name()).font(META).color(weak())))
                .child(text(crate::over::plain(&what)).font(META).color(weak()).max_lines(2).wrap()),
        )
}

fn focus_card(s: &App) -> El {
    let k = kit();
    let Some(n) = s.ov.needs.get(s.ov.need_ix) else { return col() };
    let total = s.ov.needs.len();
    let head = row()
        .w_full()
        .gap(10.0)
        .items_center()
        .child(ui::kind_pill(n.kind))
        .child(text(format!("{} · {} of {total} that need you", n.group.label(), s.ov.need_ix + 1)).font(META).color(weak()))
        .child(ui::spacer())
        .child(Button::new("Next").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).shortcut("⌘J").on_press(|s: &mut App| next_need(s)).build(k));
    let mut meta = row().gap(8.0).items_center().child(ui::avatar(&n.project, 16.0)).child(text(n.project.clone()).font(META_MED).color(base())).child(text(format!("{} · {}", n.who, s.ov.d.ago(&n.at))).font(META).color(weak()));
    if let Some(l) = &n.label {
        meta = meta.child(ui::id_label(l));
    }
    let c = col()
        .w_full()
        .gap(12.0)
        .padding(18.0)
        .child(head)
        .child(text(n.title.clone()).font(TITLE.lh(30.0)).color(strong()).wrap())
        .child(text(n.kind.ask().to_string()).font(BODY).color(base()))
        .child(meta)
        .child(row().w_full().pt(4.0).child(ui::need_actions(s, n, false)));
    col().w_full().radius(14.0).fill(k.c.surface_raised).border(1.0, ui::kind_color(n.kind).alpha(0.35)).child(c)
}

fn toolbar(s: &App, strip: bool) -> Toolbar {
    let mut t = Toolbar::new();
    t = if strip { t.leading(row().gap(12.0).items_center().child(text("Now".to_string()).font(BODY_MED).color(strong())).child(ui::strip(s, None))) } else { t.leading(ui::crumb(&[("Now", true)])) };
    t.trailing(row().gap(10.0).items_center().child(talk_button()).child(ui::new_session_button(None)).child(variant(s)).child(ui::needs_button(s)))
}

fn talk_button() -> El {
    Button::new("Talk")
        .variant(ButtonVariant::Ghost)
        .control_size(ControlSize::Small)
        .icon(&icons::WAVEFORM)
        .help("Talk to the orchestrator")
        .on_press(|_: &mut App| crate::voice::open("", ""))
        .build(kit())
        .key("talk")
}

pub fn panes(s: &App, cx: &mut Cx<App>, _layer: &mut Vec<El>) -> El {
    let k = kit();
    let [side_c, centre_c, rail_c] = crate::over::layout::collapsed(s);
    let side_in_split = !(centre_c && rail_c);
    let rail_in_split = !rail_c && !centre_c;

    let looked = s.ov.looked_now.as_deref();
    let mut centre = col().w_full().max_w(860.0).gap(16.0).px(28.0).pt(20.0).pb(40.0).child(focus_card(s));
    if s.ov.needs.len() > 1 {
        centre = centre.child(ui::needs_list(s));
    }
    centre = centre.child(crate::over::decisions::section(s).pt(14.0));
    centre = centre.child(
        col()
            .w_full()
            .gap(3.0)
            .pt(14.0)
            .child(ui::section_head("Since you left", None, Some(text(format!("{}, from the records", since_note(s, looked))).font(META).color(weak()))))
            .child(text("Each line opens what it is about.".to_string()).font(META).color(weak())),
    );
    for (p, lines) in s.ov.d.digests(s.ov.d.window(looked)).iter().take(6) {
        centre = centre.child(digest_block(s, p, lines, 5, looked, false));
    }
    let centre = col()
        .w_full()
        .h_full()
        .child(ui::pane_head(s, 1, crate::over::layout::PANES[1]))
        .child(col().key("now-a").w_full().grow(1.0).min_h(0.0).items_center().scroll(ScrollSpec { stick_bottom: false }).child(centre));

    let wide = centre_c;
    let mut rail = col().w_full().max_w(if wide { 820.0 } else { 10000.0 }).gap(10.0).px(14.0).pt(16.0).pb(24.0).child(ui::section_head("Hands-on", Some(s.ov.hands_on.len()), Some(text("⌃Tab".to_string()).font(META).color(weak()))));
    if wide {
        let mut grid = row().w_full().gap(10.0).flex_wrap().items_stretch();
        for (i, id) in s.ov.hands_on.iter().enumerate() {
            grid = grid.child(col().w(300.0).grow(1.0).child(hands_on_card(s, i, id)));
        }
        rail = rail.child(grid);
    } else {
        for (i, id) in s.ov.hands_on.iter().enumerate() {
            rail = rail.child(hands_on_card(s, i, id));
        }
    }
    rail = rail.child(ui::sessions_block(s, !wide).mt(14.0));
    let moving = s.ov.d.moving(&s.ov.hands_on);
    rail = rail.child(ui::section_head("What's moving", Some(moving.len()), None).mt(14.0));
    if moving.is_empty() {
        rail = rail.child(text("Nothing else is running.".to_string()).font(META).color(weak()));
    }
    for (i, x) in moving.iter().take(6).enumerate() {
        rail = rail.child(moving_row(s, i, &x.id));
    }
    let rail = col()
        .w_full()
        .h_full()
        .child(ui::pane_head(s, 2, crate::over::layout::PANES[2]))
        .child(col().key("now-rail").w_full().grow(1.0).min_h(0.0).items_center().scroll(ScrollSpec { stick_bottom: false }).child(rail));

    let mut rail = Some(rail);
    let mut main = row().w_full().h_full().gap(8.0);
    let strip_wrap = |e: El| col().h_full().py(8.0).child(e);
    if side_c {
        main = main.child(strip_wrap(ui::pane_strip(0, &icons::SIDEBAR, Some((s.ov.needs.len(), ui::needs_color())))).pl(8.0));
    } else if !side_in_split {
        main = main.child(col().grow(1.0).w(1.0).h_full().items_center().child(col().w_full().max_w(560.0).h_full().child(ui::pane_head(s, 0, crate::over::layout::PANES[0])).child(col().w_full().grow(1.0).min_h(0.0).px(10.0).pt(8.0).scroll(ScrollSpec { stick_bottom: false }).child(ui::sidebar(s, None, None)))));
    }
    if centre_c {
        main = main.child(strip_wrap(ui::pane_strip(1, &icons::CHECKLIST, Some((s.ov.needs.len(), ui::needs_color())))));
    } else {
        main = main.child(col().grow(1.0).w(1.0).h_full().child(centre));
    }
    if rail_c {
        main = main.child(strip_wrap(ui::pane_strip(2, &icons::BULLET_LIST, Some((s.ov.hands_on.len(), k.c.text_accent)))).pr(8.0));
    } else if !rail_in_split {
        main = main.child(col().grow(1.0).w(1.0).h_full().children(rail.take()));
    }
    let sidebar = side_in_split.then(|| ui::sidebar(s, None, None));
    let inspector = rail.filter(|_| rail_in_split);
    ui::frame_now(cx, s, sidebar, main, inspector, toolbar(s, false))
}

pub fn brief(s: &App, cx: &mut Cx<App>, _layer: &mut Vec<El>) -> El {
    let moving = s.ov.d.moving(&s.ov.hands_on);
    let looked = s.ov.looked_now.as_deref();
    let digests = s.ov.d.digests(s.ov.d.window(looked));
    let shipped: usize = digests.iter().map(|(_, l)| count(l, Happened::Shipped)).sum();
    let intro = col()
        .w_full()
        .gap(6.0)
        .child(text("Since you left".to_string()).font(TITLE).color(strong()))
        .child(text(format!("{} need you · {} shipped · {} working · {} across {} projects", s.ov.needs.len(), shipped, s.ov.d.working(), since_note(s, looked).to_lowercase(), digests.len())).font(BODY).color(base()));
    let mut c = col()
        .w_full()
        .max_w(820.0)
        .gap(14.0)
        .px(28.0)
        .pt(26.0)
        .pb(48.0)
        .child(intro)
        .child(ui::sessions_block(s, false))
        .child(ui::section_head("Needs you", Some(s.ov.needs.len()), Some(row().gap(6.0).items_center().child(text("Next".to_string()).font(META).color(weak())).child(ui::ghost_keys(&["⌘J"])))).mt(10.0))
        .child(ui::needs_list(s))
        .child(crate::over::decisions::section(s).mt(10.0))
        .child(ui::section_head("By project", None, Some(text("From the ledger and each session's last word".to_string()).font(META).color(weak()))).mt(10.0));
    for (p, lines) in digests.iter().take(6) {
        c = c.child(digest_block(s, p, lines, 4, looked, true));
    }
    let mut mv = col().w_full().gap(2.0).padding(6.0);
    for (i, x) in moving.iter().take(6).enumerate() {
        mv = mv.child(moving_row(s, i, &x.id));
    }
    c = c.child(ui::section_head("What's moving", Some(moving.len()), None).mt(10.0)).child(ui::card(mv));
    let main = col().key("now-b").w_full().h_full().items_center().scroll(ScrollSpec { stick_bottom: false }).child(c);
    ui::frame(cx, s, None, main, None, toolbar(s, true))
}
