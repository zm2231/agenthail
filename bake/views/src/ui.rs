use rune::{Color, Cx, El, Icon, Spring, TextStyle, col, el, icon, oklch, row, text};
use rune_kit::shell::{PaneMotion, RowStatus, Sidebar, SidebarRow, SidebarSection, SidebarToggle, SplitView, Toolbar};
use rune_kit::{Badge, BadgeVariant, Button, ButtonVariant, ControlSize, Keybind, KeybindVariant, Kit, Spinner};
use rune_theme_opencode::icons;

use crate::data::{Happened, Need, NeedKind, PHASES, Target};
use crate::{App, Screen, go, next_need, open_target};

pub const TITLE: TextStyle = TextStyle::new(22.0, 640.0);
pub const HEAD: TextStyle = TextStyle::new(15.0, 600.0);
pub const BODY: TextStyle = TextStyle::new(14.0, 430.0);
pub const BODY_MED: TextStyle = TextStyle::new(14.0, 560.0);
pub const META: TextStyle = TextStyle::new(12.5, 440.0);
pub const META_MED: TextStyle = TextStyle::new(12.5, 560.0);
pub const LABEL: TextStyle = TextStyle::new(11.0, 600.0);

pub fn kit() -> &'static Kit {
    rune_theme_opencode::kit(true)
}

pub fn strong() -> Color {
    kit().c.text_strong
}

pub fn base() -> Color {
    kit().c.text_base
}

pub fn weak() -> Color {
    kit().c.text_weak
}

pub fn rule() -> Color {
    kit().c.border_weak
}

pub fn hue(name: &str) -> f32 {
    let h = name.bytes().fold(2166136261u32, |h, b| (h ^ b as u32).wrapping_mul(16777619));
    (h % 360) as f32
}

pub fn avatar(name: &str, size: f32) -> El {
    let clean = name.trim_start_matches('@');
    let initial = clean.chars().next().unwrap_or('?').to_ascii_uppercase().to_string();
    row()
        .size(size, size)
        .radius(size * 0.26)
        .center()
        .fill(oklch(0.52, 0.13, hue(clean)))
        .child(text(initial).font(TextStyle::new(size * 0.62, 640.0)).color(Color::WHITE))
}

pub fn spacer() -> El {
    el().grow(1.0)
}

pub fn label(value: &str) -> El {
    text(value.to_uppercase()).font(LABEL).color(weak())
}

pub fn ghost_keys(k: &[&str]) -> El {
    Keybind::new(k).variant(KeybindVariant::Ghost).build(kit())
}

pub fn kind_color(kind: NeedKind) -> Color {
    let c = &kit().c;
    match kind {
        NeedKind::Question => c.info.icon,
        NeedKind::Approval => c.success.icon,
        NeedKind::Check => c.warning.icon,
        NeedKind::CiFailing => c.danger.icon,
    }
}

pub fn kind_icon(kind: NeedKind) -> &'static Icon {
    match kind {
        NeedKind::Question => &icons::SPEECH_BUBBLE,
        NeedKind::Approval => &icons::CIRCLE_CHECK,
        NeedKind::Check => &icons::CHECKLIST,
        NeedKind::CiFailing => &icons::WARNING,
    }
}

pub fn pill(glyph: &'static Icon, value: &str, color: Color) -> El {
    row()
        .gap(5.0)
        .items_center()
        .px(8.0)
        .py(3.0)
        .radius(6.0)
        .fill(color.alpha(0.13))
        .child(icon(glyph, 12.0).color(color))
        .child(text(value.to_string()).font(META_MED).color(color))
}

pub fn kind_pill(kind: NeedKind) -> El {
    pill(kind_icon(kind), kind.label(), kind_color(kind))
}

pub fn chip(glyph: &'static Icon, value: &str) -> El {
    row()
        .gap(6.0)
        .items_center()
        .px(9.0)
        .py(4.0)
        .radius(7.0)
        .fill(kit().c.surface_base)
        .border(1.0, rule())
        .child(icon(glyph, 13.0).color(kit().c.icon_base))
        .child(text(value.to_string()).font(META_MED).color(base()))
}

pub fn id_label(id: &str) -> El {
    text(id.to_string()).font(META).color(weak())
}

pub fn happened_style(kind: Happened) -> (&'static Icon, Color) {
    let c = &kit().c;
    match kind {
        Happened::Shipped => (&icons::CIRCLE_CHECK, c.success.icon),
        Happened::Waiting => (&icons::CHECKLIST, c.warning.icon),
        Happened::Failed => (&icons::CIRCLE_X, c.danger.icon),
        Happened::Moved => (&icons::ARROW_RIGHT, c.info.icon),
        Happened::Said => (&icons::SPEECH_BUBBLE, c.icon_base),
    }
}

pub fn stepper(phase: usize, whose: &str) -> El {
    let k = kit();
    let mut r = row().w_full().items_start();
    for (i, name) in PHASES.iter().enumerate() {
        let done = i < phase;
        let current = i == phase;
        let you = current && i == 4;
        let accent = if you { k.c.warning.icon } else { k.c.text_accent };
        let dot = if done {
            row().size(20.0, 20.0).radius(10.0).center().fill(k.c.success.icon.alpha(0.18)).child(icon(&icons::CHECK_SMALL, 14.0).color(k.c.success.icon))
        } else if current {
            row().size(20.0, 20.0).radius(10.0).center().fill(accent.alpha(0.18)).border(1.5, accent).child(el().size(8.0, 8.0).radius(4.0).fill(accent))
        } else {
            row().size(20.0, 20.0).radius(10.0).center().border(1.5, k.c.border_base)
        };
        let line_l = el().h(1.5).grow(1.0).fill(if i == 0 { Color::TRANSPARENT } else if done || current { k.c.success.icon.alpha(0.5) } else { k.c.border_base });
        let line_r = el().h(1.5).grow(1.0).fill(if i + 1 == PHASES.len() { Color::TRANSPARENT } else if done { k.c.success.icon.alpha(0.5) } else { k.c.border_base });
        let mut step = col()
            .grow(1.0)
            .w(1.0)
            .items_center()
            .gap(7.0)
            .child(row().w_full().items_center().child(line_l).child(dot).child(line_r))
            .child(text(name.to_string()).font(if current { META_MED } else { META }).color(if current { strong() } else if done { base() } else { weak() }));
        if you {
            step = step.child(text("Your move".to_string()).font(TextStyle::new(11.5, 600.0)).color(accent));
        }
        r = r.child(step);
    }
    if phase == 4 {
        return r;
    }
    col().w_full().gap(10.0).child(r).child(
        row()
            .w_full()
            .justify_center()
            .gap(6.0)
            .child(text(format!("{}:", PHASES[phase.min(5)])).font(META_MED).color(k.c.text_accent))
            .child(text(format!("{whose}'s move")).font(META).color(base()).truncate().shrink()),
    )
}

pub fn mini_phase(phase: usize) -> El {
    let k = kit();
    let mut r = row().gap(3.0).items_center();
    for i in 0..PHASES.len() {
        let color = if i < phase {
            k.c.success.icon
        } else if i == phase {
            if i == 4 { k.c.warning.icon } else { k.c.text_accent }
        } else {
            k.c.border_base
        };
        r = r.child(el().size(if i == phase { 14.0 } else { 8.0 }, 4.0).radius(2.0).fill(color));
    }
    r.child(text(PHASES[phase.min(5)].to_string()).font(META_MED).color(base()).ml(6.0))
}

pub fn section_head(title: &str, count: Option<usize>, trailing: Option<El>) -> El {
    let mut r = row().w_full().items_center().gap(8.0).child(text(title.to_string()).font(HEAD).color(strong()));
    if let Some(n) = count {
        r = r.child(Badge::new(n.to_string()).variant(BadgeVariant::Subtle).build(kit()));
    }
    r = r.child(spacer());
    if let Some(t) = trailing {
        r = r.child(t);
    }
    r
}

pub fn card(content: El) -> El {
    let k = kit();
    col().w_full().radius(12.0).fill(k.c.surface_base).border(1.0, rule()).child(content)
}

pub fn notice(message: &str) -> El {
    let toast = rune_kit::Toast::new("notice", message.to_string()).icon(kit(), &icons::INFO).on_close(|s: &mut App| s.notice = None).closable().build(kit());
    col().absolute(0.0, 0.0).w_full().h_full().items_end().justify_end().padding(24.0).child(toast)
}

pub fn need_actions(n: &Need, compact: bool) -> El {
    let k = kit();
    let t = n.target.clone();
    let size = if compact { ControlSize::Small } else { ControlSize::Regular };
    let primary = Button::new(n.kind.primary()).variant(ButtonVariant::Contrast).control_size(size).on_press({
        let t = t.clone();
        move |s: &mut App| {
            if matches!(t, Target::Session(_)) {
                open_target(s, &t);
            } else {
                crate::act(s, "Approved");
            }
        }
    });
    let mut r = row().gap(6.0).items_center().child(primary.build(k));
    if matches!(n.kind, NeedKind::Check | NeedKind::Approval) {
        r = r.child(Button::new("Ask for changes").variant(ButtonVariant::Ghost).control_size(size).on_press(|s: &mut App| crate::act(s, "Ask for changes")).build(k));
    }
    if n.kind == NeedKind::CiFailing {
        return r;
    }
    r.child(Button::new("Open").variant(ButtonVariant::Ghost).control_size(size).on_press(move |s: &mut App| open_target(s, &t)).build(k))
}

pub fn need_row(s: &App, i: usize, n: &Need, focused: bool) -> El {
    let k = kit();
    let t = n.target.clone();
    let mut meta = row().gap(8.0).items_center().child(kind_pill(n.kind)).child(text(n.project.clone()).font(META_MED).color(base()));
    let who = if matches!(n.target, Target::Session(_)) { format!("{} · {}", short_title(&n.who, 24), s.d.ago(&n.at)) } else { s.d.ago(&n.at) };
    meta = meta.child(text(who).font(META).color(weak()));
    if let Some(l) = &n.label {
        meta = meta.child(id_label(l));
    }
    let body = col().grow(1.0).shrink().min_w(0.0).gap(6.0).child(text(n.title.clone()).font(BODY_MED).color(strong()).max_lines(2).wrap()).child(meta);
    let mut r = row()
        .key(format!("need-{i}"))
        .w_full()
        .items_center()
        .gap(16.0)
        .px(16.0)
        .py(13.0)
        .radius(10.0)
        .child(avatar(&n.project, 22.0))
        .child(body)
        .child(need_actions(n, true))
        .on_press(move |s: &mut App| {
            s.need_ix = i;
            open_target(s, &t);
        });
    if focused {
        r = r.fill(k.c.surface_raised_hover);
    } else {
        r = r.hover(k.c.surface_raised_hover);
    }
    r
}

pub fn needs_button(s: &App) -> El {
    let n = s.needs.len();
    Button::new(format!("{n} need you"))
        .variant(ButtonVariant::Ghost)
        .icon(&icons::NOTIFICATIONS)
        .shortcut("⌘J")
        .help("Next thing that needs you (⌘J)")
        .on_press(|s: &mut App| next_need(s))
        .build(kit())
}

pub fn strip(s: &App, current: Option<&str>) -> El {
    let k = kit();
    let mut r = row().gap(4.0).items_center().flex_wrap();
    for (i, id) in s.hands_on.iter().enumerate() {
        let Some(x) = s.d.session(id) else { continue };
        let on = current == Some(id.as_str());
        let busy = matches!(x.display.kind.as_str(), "working" | "finishing" | "starting");
        let id2 = id.clone();
        let mut tab = row()
            .key(format!("tab-{i}"))
            .gap(7.0)
            .items_center()
            .px(9.0)
            .py(5.0)
            .radius(8.0)
            .max_w(176.0)
            .child(avatar(&x.project_name(), 16.0))
            .child(text(short_title(&s.d.title(x), 18)).font(if on { META_MED } else { META }).color(if on { strong() } else { base() }).truncate().shrink())
            .on_press(move |s: &mut App| go(s, Screen::Session(id2.clone())));
        if busy {
            tab = tab.child(Spinner::new().size(11.0).build(k));
        }
        tab = tab.child(ghost_keys(&[format!("⌘{}", i + 1).as_str()]));
        tab = if on { tab.fill(k.c.surface_raised_hover).border(1.0, rule()) } else { tab.hover(k.c.surface_raised_hover) };
        r = r.child(tab);
    }
    r
}

pub fn short_title(t: &str, max: usize) -> String {
    if t.chars().count() > max { format!("{}…", t.chars().take(max).collect::<String>().trim_end()) } else { t.to_string() }
}

pub fn sidebar(s: &App, current_project: Option<&str>, selected: Option<&str>) -> El {
    let k = kit();
    let home = SidebarRow::new("Now").icon(&icons::DOT_GRID).detail("⌘0").height(32.0).selected(matches!(s.screen, Screen::Now)).on_select(|s: &mut App, _| go(s, Screen::Now)).build(k).key("home");
    let search = SidebarRow::new("Jump to").icon(&icons::MAGNIFYING_GLASS).detail("⌘K").height(32.0).on_select(|s: &mut App, _| crate::open_jump(s, "")).build(k).key("jump");
    let mut sb = Sidebar::new().label("Navigation").inset(0.0).header_gap(2.0).header_padding(0.0, 12.0).row_gap(2.0).header(home).header(search);
    let mut needs = SidebarSection::new("Needs you").key("sec-needs").count(s.needs.len());
    for (i, n) in s.needs.iter().take(6).enumerate() {
        let t = n.target.clone();
        let sel = match (&n.target, selected) {
            (Target::Session(id), Some(x)) | (Target::Work(id), Some(x)) => id == x,
            _ => false,
        };
        needs = needs.row(
            SidebarRow::new(short_title(&n.title, 60))
                .height(44.0)
                .avatar(row().size(18.0, 18.0).center().child(icon(kind_icon(n.kind), 15.0).color(kind_color(n.kind))))
                .subtitle(format!("{} · {}", n.kind.label(), n.project))
                .selected(sel)
                .on_select(move |s: &mut App, _| open_target(s, &t))
                .build(k)
                .key(format!("need-{i}")),
        );
    }
    sb = sb.section(needs);
    let mut order: Vec<&crate::data::Project> = s.d.projects.iter().collect();
    if let Some(cp) = current_project {
        order.sort_by_key(|p| p.key != cp);
    }
    for (pi, p) in order.iter().take(if current_project.is_some() { 1 } else { 0 }).enumerate() {
        let mut sec = SidebarSection::new(p.name.clone()).key(format!("sec-p{pi}")).count(p.sessions.len());
        for id in p.sessions.iter().take(7) {
            let Some(x) = s.d.session(id) else { continue };
            let idc = id.clone();
            let lead = p.lead.as_deref() == Some(id.as_str());
            let mut row_ = SidebarRow::new(short_title(&s.d.title(x), 40))
                .height(44.0)
                .avatar(avatar(&s.d.title(x), 18.0))
                .subtitle(if lead { "Lead".to_string() } else { short_title(&s.d.gist(x), 48) })
                .selected(selected == Some(id.as_str()))
                .on_select(move |s: &mut App, _| go(s, Screen::Session(idc.clone())));
            if matches!(x.display.kind.as_str(), "working" | "finishing" | "starting") {
                row_ = row_.status(RowStatus::Busy);
            }
            sec = sec.row(row_.build(k).key(format!("s-{id}")));
        }
        sb = sb.section(sec);
    }
    let mut others = SidebarSection::new("Projects").key("sec-projects").count(s.d.projects.len());
    for p in order.iter().skip(if current_project.is_some() { 1 } else { 0 }).take(10) {
        let key = p.key.clone();
        let waiting = s.needs.iter().filter(|n| n.project_key == p.key).count();
        let mut r = SidebarRow::new(p.name.clone())
            .height(32.0)
            .avatar(avatar(&p.name, 18.0))
            .detail(s.d.ago(&p.last_active))
            .selected(matches!(&s.screen, Screen::Project(k2) if *k2 == p.key))
            .on_select(move |s: &mut App, _| go(s, Screen::Project(key.clone())));
        if waiting > 0 {
            r = r.badge(waiting.to_string());
        }
        others = others.row(r.build(k).key(format!("p-{}", p.key)));
    }
    sb = sb.section(others);
    sb.build(k)
}

pub fn frame(cx: &mut Cx<App>, s: &App, sidebar: Option<El>, main: El, inspector: Option<El>, toolbar: Toolbar) -> El {
    let k = kit();
    let mut v = SplitView::new(main);
    if let Some(sb) = sidebar {
        v = v.sidebar(&s.sidebar, |s: &mut App| &mut s.sidebar, sb).sidebar_toggle(SidebarToggle::new().size(ControlSize::Large).past_lights(16.0).edge(18.0).gap(4.0).in_header());
    } else {
        v = v.hide_sidebar_toggle();
    }
    if let Some(i) = inspector {
        v = v.inspector(&s.inspector, |s: &mut App| &mut s.inspector, i);
    }
    v.toolbar(toolbar)
        .titlebar_inset(cx.titlebar_inset())
        .frame_inset(8.0)
        .toolbar_in_panel()
        .sidebar_padding(14.0, 10.0)
        .pane_motion(PaneMotion::new().spring(Spring::new(0.3, 0.0)))
        .reduced_motion(cx.is_reduced_motion())
        .build(k)
}

pub fn crumb(parts: &[(&str, bool)]) -> El {
    let mut r = row().gap(8.0).items_center();
    for (i, (p, strong_)) in parts.iter().enumerate() {
        if i > 0 {
            r = r.child(text("/".to_string()).font(BODY).color(weak()));
        }
        r = r.child(text(p.to_string()).font(if *strong_ { BODY_MED } else { BODY }).color(if *strong_ { strong() } else { weak() }).truncate().max_w(380.0));
    }
    r
}
