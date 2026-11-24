use rune::{Cx, El, ScrollSpec, col, icon, row, text};
use rune_kit::shell::Toolbar;
use rune_kit::{Button, ButtonVariant, ControlSize, SegmentedControl, Spinner};
use rune_theme_opencode::icons;

use crate::data::{Line, Project};
use crate::ui::{self, BODY, BODY_MED, HEAD, META, META_MED, TITLE, base, kit, strong, weak};
use crate::{App, Screen, go, next_need, open_target};

pub const WINDOW: f64 = 12.0;

fn variant(s: &App) -> El {
    SegmentedControl::new(&["Three panes", "Brief"], Some(if s.now_b { 1 } else { 0 }))
        .label("Layout")
        .fit()
        .on_pick(|s: &mut App, i, _| s.now_b = i == 1)
        .build(kit())
}

pub fn digests(s: &App) -> Vec<(&Project, Vec<Line>)> {
    let mut out: Vec<(&Project, Vec<Line>)> = s.d.projects.iter().map(|p| (p, s.d.digest(p, WINDOW))).filter(|(_, l)| !l.is_empty()).collect();
    out.sort_by(|a, b| {
        let la = a.1.iter().map(|l| l.at.as_str()).max().unwrap_or("");
        let lb = b.1.iter().map(|l| l.at.as_str()).max().unwrap_or("");
        let wa = a.1.iter().filter(|l| l.kind != crate::data::Happened::Said).count();
        let wb = b.1.iter().filter(|l| l.kind != crate::data::Happened::Said).count();
        wb.cmp(&wa).then(lb.cmp(la))
    });
    out
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
        .child(text(s.d.ago(&l.at)).font(META).color(weak()))
}

pub fn digest_block(s: &App, p: &Project, lines: &[Line], max: usize) -> El {
    let k = kit();
    let key = p.key.clone();
    let counts = |kind| lines.iter().filter(|l| l.kind == kind).count();
    use crate::data::Happened::*;
    let mut summary = Vec::new();
    for (kind, word) in [(Shipped, "shipped"), (Waiting, "waiting on you"), (Failed, "failed"), (Moved, "moved"), (Said, "said")] {
        let n = counts(kind);
        if n > 0 {
            summary.push(format!("{n} {word}"));
        }
    }
    let head = row()
        .w_full()
        .gap(10.0)
        .items_center()
        .child(ui::avatar(&p.name, 22.0))
        .child(text(p.name.clone()).font(HEAD).color(strong()))
        .child(text(summary.join(" · ")).font(META).color(weak()).truncate().shrink())
        .child(ui::spacer())
        .child(Button::new("Open project").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| go(s, Screen::Project(key.clone()))).build(k));
    let mut c = col().w_full().gap(2.0).padding(14.0).child(head.mb(6.0));
    let mut shown: Vec<&Line> = Vec::new();
    for kind in [Shipped, Waiting, Failed, Moved, Said] {
        shown.extend(lines.iter().filter(|l| l.kind == kind).take(2));
    }
    shown.truncate(max);
    for (i, l) in shown.into_iter().enumerate() {
        c = c.child(digest_line(s, format!("d-{}-{i}", p.key), l));
    }
    if lines.len() > max {
        c = c.child(text(format!("{} more in {}", lines.len() - max, p.name)).font(META).color(weak()).pl(35.0).pt(4.0));
    }
    ui::card(c)
}

pub fn hands_on_card(s: &App, i: usize, id: &str) -> El {
    let k = kit();
    let Some(x) = s.d.session(id) else { return col() };
    let busy = matches!(x.display.kind.as_str(), "working" | "finishing" | "starting");
    let idc = id.to_string();
    let status = if busy {
        row().gap(6.0).items_center().child(Spinner::new().size(11.0).build(k)).child(text("Working".to_string()).font(META_MED).color(k.c.text_accent))
    } else {
        row().gap(6.0).items_center().child(icon(&icons::CIRCLE_CHECK, 12.0).color(weak())).child(text(format!("Idle {}", s.d.ago(&x.last_active))).font(META).color(weak()))
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
                .child(text(ui::short_title(&s.d.title(x), 30)).font(BODY_MED).color(strong()).truncate().shrink())
                .child(ui::spacer())
                .child(ui::ghost_keys(&[format!("⌘{}", i + 1).as_str()])),
        )
        .child(text(if s.d.gist(x) == x.display.label { "No summary yet".to_string() } else { s.d.gist(x) }).font(META).color(base()).max_lines(2).wrap())
        .child(row().w_full().items_center().child(status).child(ui::spacer()).child(text(x.project_name()).font(META).color(weak())));
    ui::card(c).key(format!("hands-{i}")).on_press(move |s: &mut App| go(s, Screen::Session(idc.clone())))
}

pub fn moving_row(s: &App, i: usize, id: &str) -> El {
    let k = kit();
    let Some(x) = s.d.session(id) else { return col() };
    let idc = id.to_string();
    let what = if !x.display.detail.is_empty() { x.display.detail.clone() } else { s.d.gist(x) };
    row()
        .key(format!("moving-{i}"))
        .w_full()
        .gap(10.0)
        .items_start()
        .px(10.0)
        .py(8.0)
        .radius(8.0)
        .hover(k.c.surface_raised_hover)
        .on_press(move |s: &mut App| go(s, Screen::Session(idc.clone())))
        .child(row().pt(3.0).child(Spinner::new().size(12.0).build(k)))
        .child(
            col()
                .grow(1.0)
                .shrink()
                .min_w(0.0)
                .gap(2.0)
                .child(row().gap(6.0).items_center().child(text(ui::short_title(&s.d.title(x), 34)).font(META_MED).color(strong()).truncate().shrink()).child(text(x.project_name()).font(META).color(weak())))
                .child(text(crate::data::plain(&what)).font(META).color(base()).max_lines(2).wrap()),
        )
}

fn focus_card(s: &App) -> El {
    let k = kit();
    let Some(n) = s.needs.get(s.need_ix) else { return col() };
    if let Some(a) = s.ask.as_ref().filter(|a| n.kind == crate::data::NeedKind::Question && n.target == crate::data::Target::Session(a.session.clone())) {
        return ui::question_card(s, a, false);
    }
    let total = s.needs.len();
    let head = row()
        .w_full()
        .gap(10.0)
        .items_center()
        .child(ui::kind_pill(n.kind))
        .child(text(format!("{} of {total} that need you", s.need_ix + 1)).font(META).color(weak()))
        .child(ui::spacer())
        .child(Button::new("Next").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).shortcut("⌘J").on_press(|s: &mut App| next_need(s)).build(k));
    let mut meta = row().gap(8.0).items_center().child(ui::avatar(&n.project, 16.0)).child(text(n.project.clone()).font(META_MED).color(base())).child(text(format!("{} · {}", n.who, s.d.ago(&n.at))).font(META).color(weak()));
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
        .child(row().w_full().pt(4.0).child(ui::need_actions(n, false)));
    col().w_full().radius(14.0).fill(k.c.surface_raised).border(1.0, ui::kind_color(n.kind).alpha(0.35)).child(c)
}

fn toolbar(s: &App, strip: bool) -> Toolbar {
    let mut t = Toolbar::new();
    t = if strip { t.leading(row().gap(12.0).items_center().child(text("Now".to_string()).font(BODY_MED).color(strong())).child(ui::strip(s, None))) } else { t.leading(ui::crumb(&[("Now", true)])) };
    if strip {
        return t.trailing(ui::needs_button(s));
    }
    t.trailing(row().gap(10.0).items_center().child(variant(s)).child(ui::needs_button(s)))
}

pub fn panes(s: &App, cx: &mut Cx<App>, _layer: &mut Vec<El>) -> El {
    let mut centre = col().w_full().max_w(860.0).gap(16.0).px(28.0).pt(20.0).pb(40.0).child(focus_card(s)).child(
        col()
            .w_full()
            .gap(3.0)
            .pt(14.0)
            .child(ui::section_head("Since you left", None, Some(text(format!("Last {WINDOW:.0} hours, from the records")).font(META).color(weak()))))
            .child(text("Each line opens what it is about.".to_string()).font(META).color(weak())),
    );
    for (p, lines) in digests(s).iter().take(6) {
        centre = centre.child(digest_block(s, p, lines, 5));
    }
    let main = col().key("now-a").w_full().h_full().items_center().scroll(ScrollSpec { stick_bottom: false }).child(centre);
    let mut rail = col().w_full().gap(10.0).px(14.0).pt(16.0).pb(24.0).child(ui::section_head("Hands-on", Some(s.hands_on.len()), Some(text("⌃Tab".to_string()).font(META).color(weak()))));
    for (i, id) in s.hands_on.iter().enumerate() {
        rail = rail.child(hands_on_card(s, i, id));
    }
    rail = rail.child(ui::sessions_block(s, true).mt(14.0));
    let skip: Vec<&str> = s.hands_on.iter().map(String::as_str).collect();
    let moving = s.d.moving(&skip);
    rail = rail.child(ui::section_head("What's moving", Some(moving.len()), None).mt(14.0));
    if moving.is_empty() {
        rail = rail.child(text("Nothing else is running.".to_string()).font(META).color(weak()));
    }
    for (i, x) in moving.iter().take(6).enumerate() {
        rail = rail.child(moving_row(s, i, &x.id));
    }
    let inspector = col().key("now-rail").w_full().h_full().scroll(ScrollSpec { stick_bottom: false }).child(rail);
    ui::frame(cx, s, Some(ui::sidebar(s, None, None)), main, Some(inspector), toolbar(s, false))
}

pub fn brief(s: &App, cx: &mut Cx<App>, _layer: &mut Vec<El>) -> El {
    let skip: Vec<&str> = s.hands_on.iter().map(String::as_str).collect();
    let moving = s.d.moving(&skip);
    let digests = digests(s);
    let shipped: usize = digests.iter().map(|(_, l)| l.iter().filter(|x| x.kind == crate::data::Happened::Shipped).count()).sum();
    let intro = col()
        .w_full()
        .gap(6.0)
        .child(row().w_full().items_center().child(text("Since you left".to_string()).font(TITLE).color(strong())).child(ui::spacer()).child(variant(s)))
        .child(text(format!("{} need you · {} shipped · {} working · last {WINDOW:.0} hours across {} projects", s.needs.len(), shipped, s.d.sessions.iter().filter(|x| !x.archived && crate::sessions::busy(x)).count(), digests.len())).font(BODY).color(base()));
    let mut needs = col().w_full().gap(2.0).padding(6.0);
    for (i, n) in s.needs.iter().enumerate() {
        match s.ask.as_ref().filter(|a| n.kind == crate::data::NeedKind::Question && n.target == crate::data::Target::Session(a.session.clone())) {
            Some(a) => needs = needs.child(col().w_full().pb(6.0).child(ui::question_card(s, a, true))),
            None => needs = needs.child(ui::need_row(s, i, n, i == s.need_ix)),
        }
    }
    let mut c = col()
        .w_full()
        .max_w(820.0)
        .gap(14.0)
        .px(28.0)
        .pt(26.0)
        .pb(48.0)
        .child(intro)
        .child(ui::sessions_block(s, false))
        .child(ui::section_head("Needs you", Some(s.needs.len()), Some(row().gap(6.0).items_center().child(text("Next".to_string()).font(META).color(weak())).child(ui::ghost_keys(&["⌘J"])))).mt(10.0))
        .child(ui::card(needs))
        .child(ui::section_head("By project", None, Some(text("From the ledger and each session's last word".to_string()).font(META).color(weak()))).mt(10.0));
    for (p, lines) in digests.iter().take(6) {
        c = c.child(digest_block(s, p, lines, 4));
    }
    let mut mv = col().w_full().gap(2.0).padding(6.0);
    for (i, x) in moving.iter().take(6).enumerate() {
        mv = mv.child(moving_row(s, i, &x.id));
    }
    c = c.child(ui::section_head("What's moving", Some(moving.len()), None).mt(10.0)).child(ui::card(mv));
    let main = col().key("now-b").w_full().h_full().items_center().scroll(ScrollSpec { stick_bottom: false }).child(c);
    ui::frame(cx, s, None, main, None, toolbar(s, true))
}
