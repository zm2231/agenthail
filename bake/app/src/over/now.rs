use rune::{Cx, El, ScrollSpec, col, icon, row, text};
use rune_kit::shell::Toolbar;
use rune_kit::{Button, ButtonVariant, ControlSize, Kit, KitCx, SegmentedControl, Spinner};
use rune_theme_opencode::icons;

use feed::derive::digest::{HAPPENED, count, cut, pick};
use feed::derive::sessions::busy;

use crate::over::{Happened, Line, Project};
use crate::over::ui::{self, BODY, BODY_MED, HEAD, META, META_MED, TITLE, base, strong, weak};
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

fn variant(s: &App, k: &Kit) -> El {
    let seg = SegmentedControl::new(&["Three panes", "Brief"], Some(if s.ov.now_brief { 1 } else { 0 }))
        .label("Layout")
        .fit()
        .on_pick(|s: &mut App, i, _| s.ov.now_brief = i == 1)
        .build(k);
    row().gap(6.0).items_center().child(seg).child(ui::ghost_keys(&["⌥⌘B"], k))
}

pub fn digest_line(s: &App, key: String, l: &Line, k: &Kit) -> El {
    let ago = s.ov.d.ago(&l.at);
    let shows = (l.kind, &l.text, &l.detail, &ago, &l.target);
    crate::over::rows::memo(key.clone(), shows, k, || {
        let (glyph, color) = ui::happened_style(l.kind, k);
        let t = l.target.clone();
        let mut body = col().grow(1.0).shrink().min_w(0.0).gap(2.0).child(
            row()
                .gap(8.0)
                .items_center()
                .child(text(l.kind.label().to_string()).font(k.text(META_MED)).color(color))
                .child(text(l.text.clone()).font(k.text(BODY)).color(strong(k)).truncate().shrink()),
        );
        if !l.detail.is_empty() {
            body = body.child(text(l.detail.clone()).font(k.text(META)).color(weak(k)).truncate());
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
            .child(row().pt(2.0).child(icon(glyph, k.text_size(15.0)).color(color)))
            .child(body)
            .child(text(ago.clone()).font(k.text(META)).color(weak(k)))
    })
}

pub fn line_id(project: &str, l: &Line) -> String {
    format!("d-{project}-{:?}-{}", l.kind, crate::over::rows::target_id(&l.target))
}

pub fn digest_block(s: &App, p: &Project, lines: &[Line], max: usize, looked: Option<&str>, catch_up: bool, k: &Kit) -> El {
    let key = p.key.clone();
    let summary: Vec<String> = HAPPENED.iter().map(|h| (count(lines, *h), h.word())).filter(|(n, _)| *n > 0).map(|(n, w)| format!("{n} {w}")).collect();
    let head = row()
        .w_full()
        .gap(10.0)
        .items_center()
        .child(ui::avatar(&p.name, 22.0))
        .child(text(p.name.clone()).font(k.text(HEAD)).color(strong(k)))
        .child(text(summary.join(" · ")).font(k.text(META)).color(weak(k)).truncate().shrink())
        .child(ui::spacer())
        .children(catch_up.then(|| crate::over::decisions::brief_button(s, &p.key, k)))
        .child(Button::new("Open project").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| go(s, Screen::Project(key.clone()))).build(k));
    let mut c = col().w_full().gap(2.0).padding(14.0).child(head.mb(6.0));
    if catch_up && let Some(note) = crate::over::decisions::brief_note(s, &p.key, k) {
        c = c.child(note.pl(32.0).pb(6.0));
    }
    let mut shown = pick(lines, 2);
    shown.truncate(max);
    let (new, old) = cut(shown, looked);
    let divider = !new.is_empty() && !old.is_empty();
    for l in &new {
        c = c.child(digest_line(s, line_id(&p.key, l), l, k));
    }
    if divider {
        c = c.child(ui::new_divider(format!("d-{}-new", p.key), k));
    }
    for l in &old {
        c = c.child(digest_line(s, line_id(&p.key, l), l, k));
    }
    if lines.len() > max {
        c = c.child(text(format!("{} more in {}", lines.len() - max, p.name)).font(k.text(META)).color(weak(k)).pl(35.0).pt(4.0));
    }
    ui::card(c, k)
}

pub fn hands_on_card(s: &App, i: usize, id: &str, k: &Kit) -> El {
    let Some(x) = s.ov.d.session(id) else { return col() };
    let busy = busy(x);
    let idle = if busy { String::new() } else { s.ov.d.ago(&x.last_active) };
    let title = ui::short_title(&s.ov.d.title(x), 30);
    let gist = s.ov.d.gist(x);
    let gist = if gist == x.display.label { "No summary yet".to_string() } else { gist };
    let project = x.project_name();
    let shows = (i, id, busy, &idle, &title, &gist, &project);
    crate::over::rows::memo(format!("hands-{id}"), shows, k, || {
        let idc = id.to_string();
        let status = if busy {
            row().gap(6.0).items_center().child(Spinner::new().size(k.text_size(11.0)).build(k)).child(text("Working".to_string()).font(k.text(META_MED)).color(k.c.text_accent))
        } else {
            row().gap(6.0).items_center().child(icon(&icons::CIRCLE_CHECK, k.text_size(12.0)).color(weak(k))).child(text(format!("Idle {idle}")).font(k.text(META)).color(weak(k)))
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
                    .child(ui::avatar(&project, 18.0))
                    .child(text(title.clone()).font(k.text(BODY_MED)).color(strong(k)).truncate().shrink())
                    .child(ui::spacer())
                    .child(ui::ghost_keys(&[format!("⌘{}", i + 1).as_str()], k)),
            )
            .child(text(gist.clone()).font(k.text(META)).color(weak(k)).max_lines(2).wrap())
            .child(row().w_full().items_center().child(status).child(ui::spacer()).child(text(project.clone()).font(k.text(META)).color(weak(k))));
        ui::card(c, k).key(format!("hands-{id}")).on_press(move |s: &mut App| crate::over::open_session(s, &idc))
    })
}

pub fn moving_row(s: &App, id: &str, k: &Kit) -> El {
    let Some(x) = s.ov.d.session(id) else { return col() };
    let what = crate::over::plain(&if !x.display.detail.is_empty() { x.display.detail.clone() } else { s.ov.d.gist(x) });
    let title = ui::short_title(&s.ov.d.title(x), 34);
    let project = x.project_name();
    let shows = (id, &what, &title, &project);
    crate::over::rows::memo(format!("moving-{id}"), shows, k, || {
        let idc = id.to_string();
        row()
            .key(format!("moving-{id}"))
            .w_full()
            .gap(10.0)
            .items_start()
            .px(10.0)
            .py(8.0)
            .radius(8.0)
            .hover(k.c.surface_raised_hover)
            .on_press(move |s: &mut App| crate::over::open_session(s, &idc))
            .child(row().pt(3.0).child(Spinner::new().size(k.text_size(12.0)).build(k)))
            .child(
                col()
                    .grow(1.0)
                    .shrink()
                    .min_w(0.0)
                    .gap(2.0)
                    .child(row().gap(6.0).items_center().child(text(title.clone()).font(k.text(META_MED)).color(strong(k)).truncate().shrink()).child(text(project.clone()).font(k.text(META)).color(weak(k))))
                    .child(text(what.clone()).font(k.text(META)).color(weak(k)).max_lines(2).wrap()),
            )
    })
}

fn focus_card(s: &App, k: &Kit) -> El {
    let Some(n) = s.ov.needs.get(s.ov.need_ix) else { return col() };
    let total = s.ov.needs.len();
    let head = row()
        .w_full()
        .gap(10.0)
        .items_center()
        .child(ui::kind_pill(n.kind, k))
        .child(text(format!("{} · {} of {total} that need you", n.group.label(), s.ov.need_ix + 1)).font(k.text(META)).color(weak(k)))
        .child(ui::spacer())
        .child(Button::new("Next").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).shortcut("⌘J").on_press(|s: &mut App| next_need(s)).build(k));
    let mut meta = row().gap(8.0).items_center().child(ui::avatar(&n.project, 16.0)).child(text(n.project.clone()).font(k.text(META_MED)).color(base(k))).child(text(format!("{} · {}", n.who, s.ov.d.ago(&n.at))).font(k.text(META)).color(weak(k)));
    if let Some(l) = &n.label {
        meta = meta.child(ui::id_label(l, k));
    }
    let c = col()
        .w_full()
        .gap(12.0)
        .padding(18.0)
        .child(head)
        .child(text(n.title.clone()).font(k.text(TITLE.lh(30.0))).color(strong(k)).wrap())
        .child(text(n.kind.ask().to_string()).font(k.text(BODY)).color(base(k)))
        .child(meta)
        .child(row().w_full().pt(4.0).child(ui::need_actions(s, n, false, k)));
    col().w_full().radius(14.0).fill(k.c.surface_raised).border(1.0, ui::kind_color(n.kind, k).alpha(0.35)).child(c)
}

fn toolbar(s: &App, strip: bool, k: &Kit) -> Toolbar {
    let mut t = Toolbar::new();
    t = if strip { t.leading(row().gap(12.0).items_center().shrink().min_w(0.0).child(text("Now".to_string()).font(k.text(BODY_MED)).color(strong(k)).truncate().shrink().min_w(0.0)).child(ui::strip(s, None, k))) } else { t.leading(ui::crumb(&[("Now", true)], k)) };
    if let Some(tabs) = crate::ui::tabs::strip(s, k, false) {
        t = t.center(row().max_w(560.0).child(tabs));
    }
    t.trailing(row().gap(10.0).items_center().child(talk_button(k)).child(ui::new_session_button(None, k)).child(variant(s, k)).child(ui::needs_button(s, k)))
}

fn talk_button(k: &Kit) -> El {
    Button::new("Talk")
        .variant(ButtonVariant::Ghost)
        .control_size(ControlSize::Small)
        .icon(&icons::WAVEFORM)
        .help("Talk to the orchestrator")
        .on_press(|_: &mut App| crate::voice::open("", ""))
        .build(k)
        .key("talk")
}

pub fn panes(s: &App, cx: &mut Cx<App>, _layer: &mut Vec<El>, main_kit: &Kit) -> El {
    let k = cx.kit();
    let [side_c, centre_c, rail_c] = crate::over::layout::collapsed(s);
    let side_in_split = !(centre_c && rail_c);
    let rail_in_split = !rail_c && !centre_c;

    let looked = s.ov.looked_now.as_deref();
    let mut centre = col().w_full().max_w(860.0).gap(16.0).px(28.0).pt(20.0).pb(40.0).child(focus_card(s, &k));
    if s.ov.needs.len() > 1 {
        centre = centre.child(ui::needs_list(s, &k));
    }
    centre = centre.child(crate::over::decisions::section(s, &k, main_kit).pt(14.0));
    centre = centre.child(
        col()
            .w_full()
            .gap(3.0)
            .pt(14.0)
            .child(ui::section_head("Since you left", None, Some(text(format!("{}, from the records", since_note(s, looked))).font(k.text(META)).color(weak(&k))), &k))
            .child(text("Each line opens what it is about.".to_string()).font(k.text(META)).color(weak(&k))),
    );
    for (p, lines) in s.ov.d.digests(s.ov.d.window(looked)).iter().take(6) {
        centre = centre.child(digest_block(s, p, lines, 5, looked, false, &k));
    }
    let centre = col()
        .w_full()
        .h_full()
        .child(ui::pane_head(s, 1, crate::over::layout::PANES[1], &k))
        .child(col().key("now-a").w_full().grow(1.0).min_h(0.0).items_center().scroll(ScrollSpec { stick_bottom: false }).child(centre));

    let wide = centre_c;
    let mut rail = col().w_full().max_w(if wide { 820.0 } else { 10000.0 }).gap(10.0).px(14.0).pt(16.0).pb(24.0).child(ui::section_head("Hands-on", Some(s.ov.hands_on.len()), Some(text("⌃Tab".to_string()).font(k.text(META)).color(weak(&k))), &k));
    if wide {
        let mut grid = row().w_full().gap(10.0).flex_wrap().items_stretch();
        for (i, id) in s.ov.hands_on.iter().enumerate() {
            grid = grid.child(col().w(300.0).grow(1.0).child(hands_on_card(s, i, id, &k)));
        }
        rail = rail.child(grid);
    } else {
        for (i, id) in s.ov.hands_on.iter().enumerate() {
            rail = rail.child(hands_on_card(s, i, id, &k));
        }
    }
    rail = rail.child(ui::sessions_block(s, !wide, &k).mt(14.0));
    let moving = s.ov.d.moving(&s.ov.hands_on);
    rail = rail.child(ui::section_head("What's moving", Some(moving.len()), None, &k).mt(14.0));
    if moving.is_empty() {
        rail = rail.child(text("Nothing else is running.".to_string()).font(k.text(META)).color(weak(&k)));
    }
    for x in moving.iter().take(6) {
        rail = rail.child(moving_row(s, &x.id, &k));
    }
    let rail = col()
        .w_full()
        .h_full()
        .child(ui::pane_head(s, 2, crate::over::layout::PANES[2], &k))
        .child(col().key("now-rail").w_full().grow(1.0).min_h(0.0).items_center().scroll(ScrollSpec { stick_bottom: false }).child(rail));

    let mut rail = Some(rail);
    let mut main = row().w_full().h_full().gap(8.0);
    let strip_wrap = |e: El| col().h_full().py(8.0).child(e);
    if side_c {
        main = main.child(strip_wrap(ui::pane_strip(0, &icons::SIDEBAR, Some((s.ov.needs.len(), ui::needs_color(&k))), &k)).pl(8.0));
    } else if !side_in_split {
        main = main.child(col().grow(1.0).w(1.0).h_full().items_center().child(col().w_full().max_w(560.0).h_full().child(ui::pane_head(s, 0, crate::over::layout::PANES[0], &k)).child(col().w_full().grow(1.0).min_h(0.0).px(10.0).pt(8.0).scroll(ScrollSpec { stick_bottom: false }).child(ui::sidebar(s, None, None, &k)))));
    }
    if centre_c {
        main = main.child(strip_wrap(ui::pane_strip(1, &icons::CHECKLIST, Some((s.ov.needs.len(), ui::needs_color(&k))), &k)));
    } else {
        main = main.child(col().grow(1.0).w(1.0).h_full().child(centre));
    }
    if rail_c {
        main = main.child(strip_wrap(ui::pane_strip(2, &icons::BULLET_LIST, Some((s.ov.hands_on.len(), k.c.text_accent)), &k)).pr(8.0));
    } else if !rail_in_split {
        main = main.child(col().grow(1.0).w(1.0).h_full().children(rail.take()));
    }
    let sidebar = side_in_split.then(|| ui::sidebar(s, None, None, &k));
    let inspector = rail.filter(|_| rail_in_split);
    ui::frame_now(cx, s, sidebar, main, inspector, toolbar(s, false, &k), main_kit)
}

pub fn brief(s: &App, cx: &mut Cx<App>, _layer: &mut Vec<El>, main_kit: &Kit) -> El {
    let k = cx.kit();
    let moving = s.ov.d.moving(&s.ov.hands_on);
    let looked = s.ov.looked_now.as_deref();
    let digests = s.ov.d.digests(s.ov.d.window(looked));
    let shipped: usize = digests.iter().map(|(_, l)| count(l, Happened::Shipped)).sum();
    let intro = col()
        .w_full()
        .gap(6.0)
        .child(text("Since you left".to_string()).font(k.text(TITLE)).color(strong(&k)))
        .child(text(format!("{} need you · {} shipped · {} working · {} across {} projects", s.ov.needs.len(), shipped, s.ov.d.working(), since_note(s, looked).to_lowercase(), digests.len())).font(k.text(BODY)).color(base(&k)));
    let mut c = col()
        .w_full()
        .max_w(820.0)
        .gap(14.0)
        .px(28.0)
        .pt(26.0)
        .pb(48.0)
        .child(intro)
        .child(ui::sessions_block(s, false, &k))
        .child(ui::section_head("Needs you", Some(s.ov.needs.len()), Some(row().gap(6.0).items_center().child(text("Next".to_string()).font(k.text(META)).color(weak(&k))).child(ui::ghost_keys(&["⌘J"], &k))), &k).mt(10.0))
        .child(ui::needs_list(s, &k))
        .child(crate::over::decisions::section(s, &k, main_kit).mt(10.0))
        .child(ui::section_head("By project", None, Some(text("From the ledger and each session's last word".to_string()).font(k.text(META)).color(weak(&k))), &k).mt(10.0));
    for (p, lines) in digests.iter().take(6) {
        c = c.child(digest_block(s, p, lines, 4, looked, true, &k));
    }
    let mut mv = col().w_full().gap(2.0).padding(6.0);
    for x in moving.iter().take(6) {
        mv = mv.child(moving_row(s, &x.id, &k));
    }
    c = c.child(ui::section_head("What's moving", Some(moving.len()), None, &k).mt(10.0)).child(ui::card(mv, &k));
    let main = col().key("now-b").w_full().h_full().items_center().scroll(ScrollSpec { stick_bottom: false }).child(c);
    ui::frame(cx, s, None, main, None, toolbar(s, true, &k), main_kit)
}
