use rune::{Cx, El, ScrollSpec, col, icon, row, text};
use rune_kit::shell::Toolbar;
use rune_kit::{Button, ButtonVariant, ControlSize, Kit, KitCx, SegmentedControl, Spinner};
use rune_theme_opencode::icons;

use feed::derive::digest::{HAPPENED, count, cut, pick};
use feed::derive::sessions::busy;

use crate::over::rows::Part;
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

fn variant(brief: bool, room: Room, k: &Kit) -> El {
    if room == Room::Icons {
        let (glyph, to) = if brief { (&icons::CHECKLIST, "Show three panes") } else { (&icons::FILE_DOCUMENT, "Show the brief") };
        return rune_kit::IconButton::new(glyph)
            .variant(rune_kit::IconButtonVariant::Ghost)
            .control_size(ControlSize::Small)
            .label(to)
            .shortcut("⌥⌘B")
            .on_press(move |s: &mut App| s.ov.now_brief = !brief)
            .build(k)
            .key("now-layout");
    }
    let seg = SegmentedControl::new(&["Three panes", "Brief"], Some(if brief { 1 } else { 0 }))
        .label("Layout")
        .fit()
        .on_pick(|s: &mut App, i, _| s.ov.now_brief = i == 1)
        .build(k);
    let r = row().gap(6.0).items_center().child(seg);
    if room == Room::Labels { r.child(ui::ghost_keys(&["⌥⌘B"], k)) } else { r }
}

const LEADING_START: &str = "now-leading-start";
const TRAILING_END: &str = "now-trailing-end";
const TOOLBAR_TITLE_MIN: f32 = 96.0;
const TOOLBAR_FADE: f32 = 16.0;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
enum Room {
    Labels,
    Fewer,
    Icons,
}

fn trailing_key(room: Room, needs: usize, brief: bool, k: &Kit) -> String {
    format!("now-trailing-{room:?}-{}-{needs}-{brief}", k.text_size(1.0).to_bits())
}

fn room(s: &App, needs: usize, brief: bool, k: &Kit) -> Room {
    let (Some(start), Some(end)) = (ui::fit_width(s, LEADING_START), ui::fit_width(s, TRAILING_END)) else { return Room::Labels };
    let title = k.text_size(TOOLBAR_TITLE_MIN).max(rune::text::advance("Now", k.text(BODY_MED)));
    let free = end - start - title - k.text_size(TOOLBAR_FADE).max(TOOLBAR_FADE);
    for room in [Room::Labels, Room::Fewer] {
        match ui::fit_width(s, &trailing_key(room, needs, brief, k)) {
            Some(w) if w > free => continue,
            _ => return room,
        }
    }
    Room::Icons
}

fn keep(s: &mut App, key: &str, v: f32) {
    if s.ov.widths.get(key).is_none_or(|w| (w - v).abs() > 0.5) {
        s.ov.widths.insert(key.to_string(), v);
    }
}

fn line_part<'a>(s: &App, key: String, l: &'a Line, k: &'a Kit) -> Part<'a> {
    let ago = s.ov.d.ago(&l.at);
    let shows = crate::over::rows::hash(&(l.kind, &l.text, &l.detail, &ago, &l.target));
    Part::row(key.clone(), shows, move || {
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
            .child(text(ago).font(k.text(META)).color(weak(k)))
    })
}

pub fn line_id(project: &str, l: &Line) -> String {
    format!("d-{project}-{:?}-{}", l.kind, crate::over::rows::target_id(&l.target))
}

pub fn digest_block<'a>(s: &'a App, p: &'a Project, lines: &'a [Line], max: usize, looked: Option<&str>, catch_up: bool, k: &'a Kit) -> Part<'a> {
    let summary: Vec<String> = HAPPENED.iter().map(|h| (count(lines, *h), h.word())).filter(|(n, _)| *n > 0).map(|(n, w)| format!("{n} {w}")).collect();
    let summary = summary.join(" · ");
    let brief = catch_up.then(|| crate::over::decisions::brief_shown(s, &p.key));
    let head_shows = crate::over::rows::hash(&(&p.key, &p.name, &summary, &brief));
    let head = Part::row(format!("d-{}-head", p.key), head_shows, move || {
        let key = p.key.clone();
        let mut c = col().w_full().gap(2.0);
        let head = row()
            .w_full()
            .gap(10.0)
            .items_center()
            .child(ui::avatar(&p.name, 22.0))
            .child(text(p.name.clone()).font(k.text(HEAD)).color(strong(k)))
            .child(text(summary).font(k.text(META)).color(weak(k)).truncate().shrink())
            .child(ui::spacer())
            .children(catch_up.then(|| crate::over::decisions::brief_button(s, &p.key, k)))
            .child(Button::new("Open project").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| go(s, Screen::Project(key.clone()))).build(k));
        c = c.child(head.mb(6.0));
        if catch_up && let Some(note) = crate::over::decisions::brief_note(s, &p.key, k) {
            c = c.child(note.pl(32.0).pb(6.0));
        }
        c
    });
    let mut shown = pick(lines, 2);
    shown.truncate(max);
    let (new, old) = cut(shown, looked);
    let divider = !new.is_empty() && !old.is_empty();
    let mut parts = vec![head];
    for l in new {
        parts.push(line_part(s, line_id(&p.key, l), l, k));
    }
    if divider {
        let key = format!("d-{}-new", p.key);
        parts.push(Part::row(key.clone(), (), move || ui::new_divider(key, k)));
    }
    for l in old {
        parts.push(line_part(s, line_id(&p.key, l), l, k));
    }
    if lines.len() > max {
        let more = format!("{} more in {}", lines.len() - max, p.name);
        parts.push(Part::row(format!("d-{}-more", p.key), more.clone(), move || text(more).font(k.text(META)).color(weak(k)).pl(35.0).pt(4.0)));
    }
    Part::group(format!("digest-{}", p.key), crate::over::rows::version((), &parts), move || {
        let mut c = col().w_full().gap(2.0).padding(14.0);
        for part in parts {
            c = c.child(part.el());
        }
        ui::card(c, k)
    })
}

pub fn hands_on_card<'a>(s: &App, i: usize, id: &'a str, k: &'a Kit) -> Part<'a> {
    let Some(x) = s.ov.d.session(id) else { return Part::row(format!("hands-{id}"), (), col) };
    let busy = busy(x);
    let idle = if busy { String::new() } else { s.ov.d.ago(&x.last_active) };
    let title = ui::short_title(&s.ov.d.title(x), 30);
    let gist = s.ov.d.gist(x);
    let gist = if gist == x.display.label { "No summary yet".to_string() } else { gist };
    let project = x.project_name();
    let shows = crate::over::rows::hash(&(i, id, busy, &idle, &title, &gist, &project));
    Part::row(format!("hands-{id}"), shows, move || {
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

pub fn moving_row<'a>(s: &App, id: &'a str, k: &'a Kit) -> Part<'a> {
    let Some(x) = s.ov.d.session(id) else { return Part::row(format!("moving-{id}"), (), col) };
    let what = crate::over::plain(&if !x.display.detail.is_empty() { x.display.detail.clone() } else { s.ov.d.gist(x) });
    let title = ui::short_title(&s.ov.d.title(x), 34);
    let project = x.project_name();
    let shows = crate::over::rows::hash(&(id, &what, &title, &project));
    Part::row(format!("moving-{id}"), shows, move || {
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

fn focus_card<'a>(s: &'a App, k: &'a Kit) -> Part<'a> {
    let Some(n) = s.ov.needs.get(s.ov.need_ix) else { return Part::row("focus-card", (), col) };
    let total = s.ov.needs.len();
    let ix = s.ov.need_ix;
    let ago = s.ov.d.ago(&n.at);
    let acts = crate::over::need_acts::Acts::of(s, n);
    let shows = crate::over::rows::hash(&(n.kind, n.group, ix, total, &n.project, &n.who, &ago, &n.label, &n.title, &n.target, &acts));
    Part::row("focus-card", shows, move || {
        let head = row()
            .w_full()
            .gap(10.0)
            .items_center()
            .child(ui::kind_pill(n.kind, k))
            .child(text(format!("{} · {} of {total} that need you", n.group.label(), ix + 1)).font(k.text(META)).color(weak(k)))
            .child(ui::spacer())
            .child(Button::new("Next").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).shortcut("⌘J").on_press(|s: &mut App| next_need(s)).build(k));
        let mut meta = row().gap(8.0).items_center().child(ui::avatar(&n.project, 16.0)).child(text(n.project.clone()).font(k.text(META_MED)).color(base(k))).child(text(format!("{} · {ago}", n.who)).font(k.text(META)).color(weak(k)));
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
            .child(row().w_full().pt(4.0).child(acts.build(s, n, false, k)));
        col().w_full().radius(14.0).fill(k.c.surface_raised).border(1.0, ui::kind_color(n.kind, k).alpha(0.35)).child(c)
    })
}

fn toolbar<'a>(s: &'a App, strip: bool, k: &'a Kit) -> ui::Bar<'a> {
    let start = |e: El| e.on_layout(|s: &mut App, r: [f32; 4]| keep(s, LEADING_START, r[0]));
    let leading = if strip {
        Part::always("now-leading", move || start(row().key("now-leading").gap(12.0).items_center().shrink().min_w(0.0).child(text("Now".to_string()).font(k.text(BODY_MED)).color(strong(k)).truncate().shrink().min_w(0.0)).child(ui::strip(s, None, k))))
    } else {
        Part::row("now-leading", (), move || start(ui::crumb(&[("Now", true)], k)))
    };
    let center = crate::ui::tabs::strip_part(s, k, false).map(|tabs| Part::group("now-tabs", crate::over::rows::version((), [&tabs]), move || row().max_w(560.0).child(tabs.el())));
    let needs = s.ov.needs.len();
    let brief = s.ov.now_brief;
    let room = room(s, needs, brief, k);
    let measure = trailing_key(room, needs, brief, k);
    let trailing = Part::row("now-trailing", (needs, brief, room), move || {
        let r = row().gap(10.0).items_center();
        let r = if room == Room::Labels { r.child(talk_button(k)).child(ui::new_session_button(None, k)) } else { r.child(talk_icon(k)).child(ui::new_session_icon(None, k)) };
        let r = r.child(variant(brief, room, k)).child(if room == Room::Icons { ui::needs_count_button(s, k) } else { ui::needs_button(s, k) });
        r.on_layout(move |s: &mut App, b: [f32; 4]| {
            keep(s, &measure, b[2]);
            keep(s, TRAILING_END, b[0] + b[2]);
        })
    });
    ui::Bar { leading, center, trailing }
}

fn toolbar_of(bar: ui::Bar) -> Toolbar {
    let mut t = Toolbar::new().leading(bar.leading.el()).trailing(bar.trailing.el());
    if let Some(c) = bar.center {
        t = t.center(c.el());
    }
    t
}

fn talk_icon(k: &Kit) -> El {
    rune_kit::IconButton::new(&icons::WAVEFORM)
        .variant(rune_kit::IconButtonVariant::Ghost)
        .control_size(ControlSize::Small)
        .label("Talk")
        .help("Talk to the orchestrator")
        .on_press(|_: &mut App| crate::voice::open("", ""))
        .build(k)
        .key("talk")
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
    let k = &k;
    let [side_c, centre_c, rail_c] = crate::over::layout::collapsed(s);
    let side_in_split = !(centre_c && rail_c);
    let rail_in_split = !rail_c && !centre_c;

    let looked = s.ov.looked_now.as_deref();
    let digests = s.ov.digests();
    let mut centre = vec![focus_card(s, k)];
    if s.ov.needs.len() > 1 {
        centre.push(ui::needs_part(s, k));
    }
    let decisions_at = centre.len();
    centre.push(crate::over::decisions::section_part(s, k, main_kit));
    let since = format!("{}, from the records", since_note(s, looked));
    centre.push(Part::row("since-head", crate::over::rows::hash(&since), move || {
        col()
            .w_full()
            .gap(3.0)
            .pt(14.0)
            .child(ui::section_head("Since you left", None, Some(ui::section_note(since, k)), k))
            .child(text("Each line opens what it is about.".to_string()).font(k.text(META)).color(weak(k)).wrap())
    }));
    for (p, lines) in digests.iter().take(6) {
        centre.push(digest_block(s, p, lines, 5, looked, false, k));
    }
    let centre_head = ui::pane_head_part(s, 1, crate::over::layout::PANES[1], k);
    let v = crate::over::rows::version((), centre.iter().chain([&centre_head]));
    let centre = Part::group("now-centre", v, move || {
        let mut c = col().w_full().max_w(860.0).gap(16.0).px(28.0).pt(20.0).pb(40.0);
        for (i, p) in centre.into_iter().enumerate() {
            let el = p.el();
            c = c.child(if i == decisions_at { col().w_full().pt(14.0).child(el) } else { el });
        }
        col().w_full().h_full().child(centre_head.el()).child(col().key("now-a").w_full().grow(1.0).min_h(0.0).items_center().scroll(ScrollSpec { stick_bottom: false }).child(c))
    });

    let wide = centre_c;
    let hands_head = ui::head_part("rail-hands-head", "Hands-on", Some(s.ov.hands_on.len()), Some("⌃Tab".into()), k);
    let hands: Vec<Part> = s.ov.hands_on.iter().enumerate().map(|(i, id)| hands_on_card(s, i, id, k)).collect();
    let sessions = ui::sessions_part(s, !wide, k);
    let moving = s.ov.d.moving(&s.ov.hands_on);
    let moving_head = ui::head_part("rail-moving-head", "What's moving", Some(moving.len()), None, k);
    let none = moving.is_empty();
    let moving: Vec<Part> = moving.iter().take(6).map(|x| moving_row(s, &x.id, k)).collect();
    let rail_head = ui::pane_head_part(s, 2, crate::over::layout::PANES[2], k);
    let v = crate::over::rows::version((wide, none), hands.iter().chain(&moving).chain([&hands_head, &sessions, &moving_head, &rail_head]));
    let rail = Part::group("now-rail", v, move || {
        let mut rail = col().w_full().max_w(if wide { 820.0 } else { 10000.0 }).gap(10.0).px(14.0).pt(16.0).pb(24.0).child(hands_head.el());
        if wide {
            let mut grid = row().w_full().gap(10.0).flex_wrap().items_stretch();
            for h in hands {
                grid = grid.child(col().w(300.0).grow(1.0).child(h.el()));
            }
            rail = rail.child(grid);
        } else {
            for h in hands {
                rail = rail.child(h.el());
            }
        }
        rail = rail.child(col().w_full().mt(14.0).child(sessions.el()));
        rail = rail.child(col().w_full().mt(14.0).child(moving_head.el()));
        if none {
            rail = rail.child(text("Nothing else is running.".to_string()).font(k.text(META)).color(weak(k)));
        }
        for m in moving {
            rail = rail.child(m.el());
        }
        col().w_full().h_full().child(rail_head.el()).child(col().key("now-rail").w_full().grow(1.0).min_h(0.0).items_center().scroll(ScrollSpec { stick_bottom: false }).child(rail))
    });

    let (inspector, rail_in_main) = if rail_in_split { (Some(rail), None) } else { (None, (!rail_c).then_some(rail)) };
    let side_in_main = (!side_c && !side_in_split).then(|| (ui::pane_head_part(s, 0, crate::over::layout::PANES[0], k), ui::sidebar_part(s, None, None, k)));
    let centre = (!centre_c).then_some(centre);
    let counts = (s.ov.needs.len(), s.ov.hands_on.len());
    let v = crate::over::rows::version((side_c, centre_c, rail_c, counts), centre.iter().chain(&rail_in_main).chain(side_in_main.iter().flat_map(|(h, b)| [h, b])));
    let main = Part::group("now-main", v, move || {
        let mut main = row().w_full().h_full().gap(8.0);
        let strip_wrap = |e: El| col().h_full().py(8.0).child(e);
        if side_c {
            main = main.child(strip_wrap(ui::pane_strip(0, &icons::SIDEBAR, Some((counts.0, ui::needs_color(k))), k)).pl(8.0));
        } else if let Some((head, body)) = side_in_main {
            main = main.child(col().grow(1.0).w(1.0).h_full().items_center().child(col().w_full().max_w(560.0).h_full().child(head.el()).child(col().w_full().grow(1.0).min_h(0.0).px(10.0).pt(8.0).scroll(ScrollSpec { stick_bottom: false }).child(body.el()))));
        }
        if let Some(c) = centre {
            main = main.child(col().grow(1.0).w(1.0).h_full().child(c.el()));
        } else {
            main = main.child(strip_wrap(ui::pane_strip(1, &icons::CHECKLIST, Some((counts.0, ui::needs_color(k))), k)));
        }
        if rail_c {
            main = main.child(strip_wrap(ui::pane_strip(2, &icons::BULLET_LIST, Some((counts.1, k.c.text_accent)), k)).pr(8.0));
        } else if let Some(r) = rail_in_main {
            main = main.child(col().grow(1.0).w(1.0).h_full().child(r.el()));
        }
        main
    });
    let sidebar = side_in_split.then(|| ui::sidebar_part(s, None, None, k));
    ui::frame_now(cx, s, sidebar, main, inspector, toolbar(s, false, k), k, main_kit)
}

pub fn brief(s: &App, cx: &mut Cx<App>, _layer: &mut Vec<El>, main_kit: &Kit) -> El {
    let k = cx.kit();
    let moving = s.ov.d.moving(&s.ov.hands_on);
    let looked = s.ov.looked_now.as_deref();
    let digests = s.ov.digests();
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
        .child(col().w_full().mt(10.0).child(crate::over::decisions::section(s, &k, main_kit)))
        .child(ui::section_head("By project", None, Some(ui::section_note("From the ledger and each session's last word", &k)), &k).mt(10.0));
    for (p, lines) in digests.iter().take(6) {
        c = c.child(digest_block(s, p, lines, 4, looked, true, &k).el());
    }
    let mut mv = col().w_full().gap(2.0).padding(6.0);
    for x in moving.iter().take(6) {
        mv = mv.child(moving_row(s, &x.id, &k).el());
    }
    c = c.child(ui::section_head("What's moving", Some(moving.len()), None, &k).mt(10.0)).child(ui::card(mv, &k));
    let main = col().key("now-b").w_full().h_full().items_center().scroll(ScrollSpec { stick_bottom: false }).child(c);
    let bar = toolbar_of(toolbar(s, true, &k));
    ui::frame(cx, s, None, main, None, bar, main_kit)
}
