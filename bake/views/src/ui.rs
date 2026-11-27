use rune::{Color, Cx, El, Icon, Spring, TextStyle, col, el, icon, oklch, row, text};
use rune_kit::shell::{PaneMotion, RowStatus, Sidebar, SidebarRow, SidebarSection, SidebarToggle, SplitView, Toolbar};
use rune_kit::{Badge, BadgeVariant, Button, ButtonVariant, ControlSize, Keybind, KeybindVariant, Kit, KitCx, KitTheme, Spinner};
use feed::derive::sessions::busy;
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

static DARK: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(true);

pub fn set_dark(dark: bool) {
    DARK.store(dark, std::sync::atomic::Ordering::Relaxed);
}

pub fn dark() -> bool {
    DARK.load(std::sync::atomic::Ordering::Relaxed)
}

pub fn theme() -> KitTheme {
    KitTheme::new(|env| {
        let mut k = rune_theme_opencode::kit(&env.increased_contrast(false));
        if env.dark {
            k.c.bg_deep = Color::TRANSPARENT;
            k.c.border_muted = Color::WHITE.alpha(0.09);
        }
        k
    })
}

pub fn glass_tint() -> Color {
    oklch(0.13, 0.012, 275.0).alpha(0.58)
}

pub fn card_deep() -> Color {
    oklch(0.16, 0.008, 275.0)
}

pub fn edge(k: &Kit) -> Color {
    if dark() { Color::WHITE.alpha(0.07) } else { k.c.border_weak }
}

pub fn backdrop(body: El, k: &Kit) -> El {
    if !dark() {
        return el().w_full().h_full().fill(k.c.bg_deep).child(body);
    }
    el().w_full()
        .h_full()
        .child(el().absolute(0.0, 0.0).w_full().h_full().system_material(rune::SystemMaterial::Glass).fill(card_deep()))
        .child(el().absolute(0.0, 0.0).w_full().h_full().fill(glass_tint()))
        .child(body)
}

pub fn strong(k: &Kit) -> Color {
    k.c.text_strong
}

pub fn base(k: &Kit) -> Color {
    k.c.text_base
}

pub fn weak(k: &Kit) -> Color {
    k.c.text_weak
}

pub fn rule(k: &Kit) -> Color {
    k.c.border_weak
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

fn cut_to(word: &str, style: TextStyle, width: f32) -> String {
    let chars: Vec<char> = word.chars().collect();
    let mut n = chars.len();
    while n > 0 {
        let t: String = chars[..n].iter().collect::<String>() + "…";
        if rune::text::advance(&t, style) <= width {
            return t;
        }
        n -= 1;
    }
    "…".into()
}

pub fn fit_lines(t: &str, style: TextStyle, width: f32, max: usize) -> String {
    let fits = |x: &str| rune::text::advance(x, style) <= width;
    let mut lines: Vec<String> = Vec::new();
    let mut cur = String::new();
    let mut words = t.split_whitespace().peekable();
    while let Some(w) = words.next() {
        let next = if cur.is_empty() { w.to_string() } else { format!("{cur} {w}") };
        if fits(&next) {
            cur = next;
            continue;
        }
        if !cur.is_empty() {
            lines.push(std::mem::take(&mut cur));
            if lines.len() == max {
                let last = lines.pop().unwrap_or_default();
                lines.push(end_line(&last, style, width));
                return lines.join("\n");
            }
        }
        if fits(w) {
            cur = w.to_string();
        } else {
            lines.push(cut_to(w, style, width));
            return lines.join("\n");
        }
    }
    if !cur.is_empty() {
        lines.push(cur);
    }
    lines.join("\n")
}

fn end_line(line: &str, style: TextStyle, width: f32) -> String {
    let mut words: Vec<&str> = line.split(' ').collect();
    while !words.is_empty() {
        let t = format!("{}…", words.join(" "));
        if rune::text::advance(&t, style) <= width {
            return t;
        }
        if words.len() == 1 {
            return cut_to(words[0], style, width);
        }
        words.pop();
    }
    "…".into()
}

pub fn title_fit(s: &App, key: &str, t: &str, style: TextStyle, color: Color, lines: usize) -> El {
    let k = key.to_string();
    let body = match s.widths.get(key) {
        Some(w) if *w > 8.0 => text(fit_lines(t, style, w - 1.0, lines)).font(style).color(color),
        _ => text(t.to_string()).font(style).color(color).wrap_anywhere().max_lines(lines),
    };
    col().key(format!("fit-{key}")).w_full().min_w(0.0).shrink().on_layout(move |s: &mut App, r: [f32; 4]| {
        if s.widths.get(&k).is_none_or(|w| (w - r[2]).abs() > 0.5) {
            s.widths.insert(k.clone(), r[2]);
        }
    }).child(body)
}

pub fn spacer() -> El {
    el().grow(1.0)
}

pub fn label(value: &str, k: &Kit) -> El {
    text(value.to_uppercase()).font(LABEL).color(weak(k))
}

pub fn ghost_keys(keys: &[&str], k: &Kit) -> El {
    Keybind::new(keys).variant(KeybindVariant::Ghost).build(k)
}

pub fn kind_color(kind: NeedKind, k: &Kit) -> Color {
    let c = &k.c;
    match kind {
        NeedKind::Question => c.info.icon,
        NeedKind::Approval => c.success.icon,
        NeedKind::Check => c.warning.icon,
        NeedKind::CiFailing => c.danger.icon,
        NeedKind::Finished => c.icon_base,
    }
}

pub fn needs_color(k: &Kit) -> Color {
    k.c.warning.icon
}

pub fn kind_icon(kind: NeedKind) -> &'static Icon {
    match kind {
        NeedKind::Question => &icons::SPEECH_BUBBLE,
        NeedKind::Approval => &icons::CIRCLE_CHECK,
        NeedKind::Check => &icons::CHECKLIST,
        NeedKind::CiFailing => &icons::WARNING,
        NeedKind::Finished => &icons::CIRCLE_CHECK,
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

pub fn kind_pill(kind: NeedKind, k: &Kit) -> El {
    pill(kind_icon(kind), kind.label(), kind_color(kind, k))
}

pub fn chip(glyph: &'static Icon, value: &str, k: &Kit) -> El {
    row()
        .gap(6.0)
        .items_center()
        .px(9.0)
        .py(4.0)
        .radius(7.0)
        .fill(k.c.surface_base)
        .border(1.0, edge(k))
        .child(icon(glyph, 13.0).color(k.c.icon_base))
        .child(text(value.to_string()).font(META_MED).color(base(k)))
}

pub fn id_label(id: &str, k: &Kit) -> El {
    text(id.to_string()).font(META).color(weak(k))
}

pub fn happened_style(kind: Happened, k: &Kit) -> (&'static Icon, Color) {
    let c = &k.c;
    match kind {
        Happened::Shipped => (&icons::CIRCLE_CHECK, c.success.icon),
        Happened::Waiting => (&icons::CHECKLIST, c.warning.icon),
        Happened::Failed => (&icons::CIRCLE_X, c.danger.icon),
        Happened::Moved => (&icons::ARROW_RIGHT, c.info.icon),
        Happened::Said => (&icons::SPEECH_BUBBLE, c.icon_base),
    }
}

pub fn stepper(w: &feed::derive::Work, whose: &str, k: &Kit) -> El {
    match feed::derive::phase::shown_step(&w.phase, &w.state) {
        Some(phase) => steps(phase, &w.steps, whose, k),
        None => step_word(w, k),
    }
}

pub fn step_word(w: &feed::derive::Work, k: &Kit) -> El {
    text(feed::derive::phase::step_word(&w.phase, &w.state, &w.steps)).font(META).color(weak(k))
}

fn steps(phase: usize, names: &feed::derive::phase::Steps, whose: &str, k: &Kit) -> El {
    let mut r = row().w_full().items_start();
    for (i, name) in names.names().enumerate() {
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
            .child(text(name.to_string()).font(if current { META_MED } else { META }).color(if current { strong(k) } else if done { base(k) } else { weak(k) }));
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
            .child(text(format!("{}:", names.name(phase))).font(META_MED).color(k.c.text_accent))
            .child(text(format!("{whose}'s move")).font(META).color(base(k)).truncate().shrink()),
    )
}

pub fn mini_phase(w: &feed::derive::Work, k: &Kit) -> El {
    match feed::derive::phase::shown_step(&w.phase, &w.state) {
        Some(phase) => mini_steps(phase, &w.steps, k),
        None => step_word(w, k),
    }
}

fn mini_steps(phase: usize, names: &feed::derive::phase::Steps, k: &Kit) -> El {
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
    r.child(text(names.name(phase).to_string()).font(META).color(weak(k)).ml(6.0))
}

pub fn section_head(title: &str, count: Option<usize>, trailing: Option<El>, k: &Kit) -> El {
    let mut r = row().w_full().items_center().gap(8.0).child(text(title.to_string()).font(HEAD).color(strong(k)));
    if let Some(n) = count {
        r = r.child(Badge::new(n.to_string()).variant(BadgeVariant::Subtle).build(k));
    }
    r = r.child(spacer());
    if let Some(t) = trailing {
        r = r.child(t);
    }
    r
}

pub fn card(content: El, k: &Kit) -> El {
    col().w_full().radius(12.0).fill(k.c.surface_base).border(1.0, edge(k)).child(content)
}

pub fn notice(message: &str, k: &Kit) -> El {
    let toast = rune_kit::Toast::new("notice", message.to_string()).icon(k, &icons::INFO).on_close(|s: &mut App| s.notice = None).closable().build(k);
    col().absolute(0.0, 0.0).w_full().h_full().items_end().justify_end().padding(24.0).child(toast)
}

pub fn need_actions(n: &Need, compact: bool, k: &Kit) -> El {
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

pub fn need_row(s: &App, i: usize, n: &Need, focused: bool, k: &Kit) -> El {
    let t = n.target.clone();
    let mut meta = row().gap(8.0).items_center().child(kind_pill(n.kind, k)).child(text(n.project.clone()).font(META_MED).color(base(k)));
    let who = if matches!(n.target, Target::Session(_)) { format!("{} · {}", short_title(&n.who, 24), s.d.ago(&n.at)) } else { s.d.ago(&n.at) };
    meta = meta.child(text(who).font(META).color(weak(k)));
    if let Some(l) = &n.label {
        meta = meta.child(id_label(l, k));
    }
    let body = col().grow(1.0).shrink().min_w(0.0).gap(6.0).child(title_fit(s, &format!("need-{}", n.title), &n.title, BODY_MED, strong(k), 2)).child(meta);
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
        .child(need_actions(n, true, k))
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

pub fn needs_button(s: &App, k: &Kit) -> El {
    let n = s.needs.len();
    Button::new(format!("{n} need you"))
        .variant(ButtonVariant::Ghost)
        .icon(&icons::NOTIFICATIONS)
        .shortcut("⌘J")
        .help("Next thing that needs you (⌘J)")
        .on_press(|s: &mut App| next_need(s))
        .build(k)
}

pub fn strip(s: &App, current: Option<&str>, k: &Kit) -> El {
    let mut r = row().gap(4.0).items_center().flex_wrap();
    let mut ids: Vec<String> = s.hands_on.clone();
    if let Some(c) = current.filter(|c| !ids.iter().any(|x| x == c)) {
        ids.push(c.to_string());
    }
    for (i, id) in ids.iter().enumerate() {
        let Some(x) = s.d.session(id) else { continue };
        let on = current == Some(id.as_str());
        let busy = busy(x);
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
            .child(text(short_title(&s.d.title(x), 18)).font(if on { META_MED } else { META }).color(if on { strong(k) } else { base(k) }).truncate().shrink())
            .on_press(move |s: &mut App| go(s, Screen::Session(id2.clone())));
        if busy {
            tab = tab.child(Spinner::new().size(11.0).build(k));
        }
        if i < s.hands_on.len() {
            tab = tab.child(ghost_keys(&[format!("⌘{}", i + 1).as_str()], k));
        } else {
            tab = tab.child(text("viewing".to_string()).font(META).color(weak(k)));
        }
        tab = if on { tab.fill(k.c.surface_raised_hover).border(1.0, edge(k)) } else { tab.hover(k.c.surface_raised_hover) };
        r = r.child(tab);
    }
    r
}

pub use feed::derive::text::short_title;

pub fn sidebar(s: &App, current_project: Option<&str>, selected: Option<&str>, k: &Kit) -> El {
    let home = SidebarRow::new("Now").icon(&icons::DOT_GRID).detail("⌘0").height(32.0).selected(matches!(s.screen, Screen::Now)).on_select(|s: &mut App, _| go(s, Screen::Now)).build(k).key("home");
    let search = SidebarRow::new("Jump to").icon(&icons::MAGNIFYING_GLASS).detail("⌘K").height(32.0).on_select(|s: &mut App, _| crate::open_jump(s, "")).build(k).key("jump");
    let all = SidebarRow::new("Sessions")
        .icon(&icons::BULLET_LIST)
        .detail("⌘⇧S")
        .height(32.0)
        .selected(matches!(s.screen, Screen::Sessions))
        .on_select(|s: &mut App, _| go(s, Screen::Sessions))
        .build(k)
        .key("sessions");
    let mut sb = Sidebar::new().label("Navigation").inset(0.0).header_gap(2.0).header_padding(0.0, 12.0).row_gap(2.0).header(home).header(search).header(all);
    let mut needs = SidebarSection::new("Needs you").key("sec-needs").count(s.needs.len());
    for (i, n) in s.needs.iter().take(6).enumerate() {
        let t = n.target.clone();
        let sel = match &n.target {
            Target::Session(id) => selected == Some(id.as_str()),
            Target::Work(key) => matches!(&s.screen, Screen::Work(open) if open == key),
            Target::Project(_) => false,
        };
        needs = needs.row(
            SidebarRow::new(short_title(&n.title, 60))
                .height(44.0)
                .avatar(avatar(&n.project, 18.0))
                .subtitle(n.kind.wanted())
                .subtitle_color(needs_color(k))
                .selected(sel)
                .on_select(move |s: &mut App, _| open_target(s, &t))
                .build(k)
                .key(format!("need-{i}")),
        );
    }
    if !matches!(s.screen, Screen::Now) {
        sb = sb.section(needs);
    }
    let mut order: Vec<&crate::data::Project> = s.d.projects.iter().collect();
    if let Some(cp) = current_project {
        order.sort_by_key(|p| p.key != cp);
    }
    for (pi, p) in order.iter().take(if current_project.is_some() { 1 } else { 0 }).enumerate() {
        let mut sec = SidebarSection::new(p.name.clone()).key(format!("sec-p{pi}")).count(p.sessions.len());
        for (id, depth) in s.d.project_rows(p).iter().take(8) {
            let Some(x) = s.d.session(id) else { continue };
            let idc = id.clone();
            let lead = p.lead.as_deref() == Some(id.as_str());
            let waiting = x.display.label.starts_with("Waiting on");
            let mut row_ = SidebarRow::new(short_title(&s.d.title(x), 40))
                .height(44.0)
                .depth(*depth)
                .avatar(avatar(&s.d.title(x), 18.0))
                .subtitle(if waiting { x.display.label.clone() } else if lead { format!("Lead · {}", short_title(&s.d.gist(x), 40)) } else { short_title(&s.d.gist(x), 48) })
                .selected(selected == Some(id.as_str()))
                .on_select(move |s: &mut App, _| go(s, Screen::Session(idc.clone())));
            if busy(x) {
                row_ = row_.status(RowStatus::Busy);
            }
            if waiting {
                row_ = row_.subtitle_color(k.c.warning.icon);
            }
            sec = sec.row(row_.build(k).key(format!("s-{id}")));
        }
        let pname = p.name.clone();
        sec = sec.row(
            SidebarRow::new("All sessions")
                .icon(&icons::ARROW_RIGHT)
                .height(30.0)
                .detail(format!("{}", p.sessions.len()))
                .on_select(move |s: &mut App, _| {
                    s.sessions_project = Some(pname.clone());
                    go(s, Screen::Sessions);
                })
                .build(k)
                .key(format!("all-{}", p.key)),
        );
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
    let k = cx.kit();
    let mut v = SplitView::new(main);
    if let Some(sb) = sidebar {
        v = v.sidebar(&s.sidebar, |s: &mut App| &mut s.sidebar, sb).sidebar_toggle(SidebarToggle::new().size(ControlSize::Large).past_lights(16.0).edge(18.0).gap(4.0).in_header());
    } else {
        v = v.hide_sidebar_toggle();
    }
    if let Some(i) = inspector {
        v = v.inspector(&s.inspector, |s: &mut App| &mut s.inspector, i);
    }
    let split = v
        .toolbar(toolbar)
        .titlebar_inset(cx.titlebar_inset())
        .frame_inset(8.0)
        .toolbar_in_panel()
        .sidebar_padding(14.0, 10.0)
        .pane_motion(PaneMotion::new().spring(Spring::new(0.3, 0.0)))
        .reduced_motion(cx.is_reduced_motion())
        .build(&k);
    col().w_full().h_full().child(col().w_full().grow(1.0).min_h(0.0).child(split)).child(status_bar(s, &k))
}

pub fn frame_now(cx: &mut Cx<App>, s: &App, sidebar: Option<El>, main: El, inspector: Option<El>, toolbar: Toolbar) -> El {
    let k = cx.kit();
    let mut v = SplitView::new(main);
    if let Some(sb) = sidebar {
        v = v.sidebar(&s.now_side, |s: &mut App| &mut s.now_side, sb).sidebar_toggle(SidebarToggle::new().size(ControlSize::Large).past_lights(16.0).edge(18.0).gap(4.0).in_header());
    } else {
        v = v.hide_sidebar_toggle();
    }
    if let Some(i) = inspector {
        v = v.inspector(&s.now_rail, |s: &mut App| &mut s.now_rail, i);
    }
    let split = v
        .toolbar(toolbar)
        .titlebar_inset(cx.titlebar_inset())
        .frame_inset(8.0)
        .toolbar_in_panel()
        .sidebar_padding(14.0, 10.0)
        .pane_motion(PaneMotion::new().spring(Spring::new(0.3, 0.0)))
        .reduced_motion(cx.is_reduced_motion())
        .build(&k);
    col().w_full().h_full().child(col().w_full().grow(1.0).min_h(0.0).child(split)).child(status_bar(s, &k))
}

pub fn pane_strip(i: usize, glyph: &'static Icon, count: Option<(usize, Color)>, k: &Kit) -> El {
    let name = crate::layout::PANES[i];
    let len = 140.0;
    let label = el().w(16.0).h(len).child(text(name.to_string()).font(META_MED).color(base(k)).w(len).h(16.0).absolute(8.0 - len / 2.0, len / 2.0 - 8.0).rotate(std::f32::consts::FRAC_PI_2));
    let mut c = col()
        .key(format!("strip-{i}"))
        .w(40.0)
        .h_full()
        .items_center()
        .gap(10.0)
        .pt(14.0)
        .radius(10.0)
        .fill(k.c.surface_base)
        .border(1.0, edge(k))
        .hover(k.c.surface_raised_hover)
        .on_press(move |s: &mut App| crate::layout::expand_pane(s, i))
        .child(icon(glyph, 15.0).color(k.c.icon_base));
    if let Some((n, color)) = count {
        c = c.child(row().min_w(20.0).h(18.0).px(5.0).radius(9.0).center().fill(color.alpha(0.18)).child(text(n.to_string()).font(LABEL).color(color)));
    }
    c.child(label).child(spacer()).child(ghost_keys(&[format!("⌥⌘{}", i + 1).as_str()], k).pb(12.0))
}

pub fn pane_head(s: &App, i: usize, title: &str, k: &Kit) -> El {
    let focused = s.now_focus.map(|(f, _)| f) == Some(i);
    let focus = rune_kit::IconButton::new(if focused { &icons::COLLAPSE } else { &icons::EXPAND })
        .variant(rune_kit::IconButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .label(if focused { "Restore panes" } else { "Focus this pane" })
        .help(if focused { format!("Restore panes (⌥⇧⌘{} or esc)", i + 1) } else { format!("Focus this pane (⌥⇧⌘{})", i + 1) })
        .on_press(move |s: &mut App| crate::layout::focus_pane(s, i))
        .build(k);
    let hide = rune_kit::IconButton::new(if i == 2 { &icons::CHEVRON_DOUBLE_RIGHT } else { &icons::MINUS })
        .variant(rune_kit::IconButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .label("Collapse this pane")
        .help(format!("Collapse to a strip (⌥⌘{})", i + 1))
        .on_press(move |s: &mut App| crate::layout::toggle_pane(s, i))
        .build(k);
    let mut r = row().key(format!("pane-head-{i}")).w_full().h(36.0).px(14.0).gap(6.0).items_center().border_sides([0.0, 0.0, 1.0, 0.0], rule(k)).child(label(title, k));
    if focused {
        r = r.child(text("focused".to_string()).font(META).color(k.c.text_accent));
    }
    r.child(spacer()).child(focus).child(hide)
}

fn until(s: &App, at: &str) -> String {
    let h = -s.d.hours(at);
    if h <= 0.0 || h > 1e8 {
        return String::new();
    }
    let m = (h * 60.0).round() as i64;
    match m {
        0..=59 => format!("{m}m"),
        60..=1439 => format!("{}h {}m", m / 60, m % 60),
        _ => format!("{}d {}h", m / 1440, (m % 1440) / 60),
    }
}

pub fn status_bar(s: &App, k: &Kit) -> El {
    let mut r = row().key("status-bar").w_full().h(30.0).px(16.0).gap(12.0).items_center().child(text("Used".to_string()).font(META).color(weak(k)));
    for m in &s.d.usage {
        let pct = m.percent.unwrap_or(0.0);
        let color = if pct >= 80.0 { k.c.danger.icon } else if pct >= 60.0 { k.c.warning.icon } else { base(k) };
        let window = if m.label.ends_with("week") { "wk" } else { "5h" };
        let who = match &m.name {
            Some(n) if n != "default" => format!("{n} {window}"),
            _ => format!("{} {window}", m.provider.chars().next().map(|c| c.to_uppercase().collect::<String>() + &m.provider[1..]).unwrap_or_default()),
        };
        let bar = row().w(28.0).h(4.0).radius(2.0).fill(k.c.border_base).child(el().w((28.0 * pct / 100.0).clamp(2.0, 28.0) as f32).h(4.0).radius(2.0).fill(color));
        r = r.child(
            row()
                .gap(6.0)
                .items_center()
                .child(text(who).font(META).color(weak(k)))
                .child(bar)
                .child(text(format!("{pct:.0}%")).font(META_MED).color(color))
                .child(text(until(s, m.resets_at.as_deref().unwrap_or(""))).font(META).color(weak(k))),
        );
    }
    let working = s.d.working();
    r = r.child(spacer()).child(row().gap(6.0).items_center().child(Spinner::new().size(10.0).build(k)).child(text(format!("{working} working")).font(META).color(base(k))));
    for h in &s.d.hosts {
        let up = matches!(h.state.as_str(), "local" | "connected");
        r = r.child(
            row()
                .gap(6.0)
                .items_center()
                .child(el().size(7.0, 7.0).radius(3.5).fill(if up { k.c.success.icon } else { k.c.text_weak }))
                .child(text(h.name.clone()).font(META).color(if up { base(k) } else { weak(k) })),
        );
    }
    r
}

pub fn question_card(s: &App, ask: &crate::data::Ask, compact: bool, k: &Kit) -> El {
    let who = s.d.session(&ask.session).map(|x| s.d.title(x)).unwrap_or_default();
    let pick = s.ask_pick.or_else(|| ask.options.iter().position(|o| o.recommended)).unwrap_or(0);
    let mut head = row()
        .w_full()
        .gap(8.0)
        .items_center()
        .child(pill(&icons::HELP, "Question", k.c.info.icon))
        .child(pill(&icons::OUTLINE_HEXAGONAL_WARNING, "Waits", k.c.warning.icon))
        .child(text(format!("{who} · {}", ask.header)).font(META).color(weak(k)).truncate().shrink())
        .child(spacer());
    if s.replayed {
        head = head.child(text(format!("Replayed: asked {} ago, already answered", s.d.ago(&ask.at))).font(META).color(weak(k)));
    }
    let mut c = col().w_full().gap(12.0).padding(if compact { 16.0 } else { 20.0 }).child(head).child(text(ask.question.clone()).font(if compact { HEAD.lh(22.0) } else { TITLE.lh(30.0) }).color(strong(k)).wrap());
    for (i, o) in ask.options.iter().enumerate() {
        let on = i == pick;
        let mark = if on {
            row().size(16.0, 16.0).radius(8.0).center().border(1.5, k.c.text_accent).child(el().size(8.0, 8.0).radius(4.0).fill(k.c.text_accent))
        } else {
            row().size(16.0, 16.0).radius(8.0).border(1.5, k.c.border_strong)
        };
        let mut title = row().gap(8.0).items_center().child(text(o.label.clone()).font(BODY_MED).color(strong(k)));
        if o.recommended {
            title = title.child(pill(&icons::BRAIN, "Recommended", k.c.text_accent));
        }
        let mut body = col().grow(1.0).shrink().min_w(0.0).gap(4.0).child(title);
        if !o.detail.is_empty() {
            body = body.child(text(o.detail.clone()).font(META.lh(18.0)).color(weak(k)).wrap().max_lines(if compact { 2 } else { 3 }));
        }
        let mut opt = row()
            .key(format!("opt-{i}"))
            .w_full()
            .gap(12.0)
            .items_start()
            .padding(12.0)
            .radius(10.0)
            .border(1.0, if on { k.c.text_accent.alpha(0.5) } else { rule(k) })
            .on_press(move |s: &mut App| s.ask_pick = Some(i))
            .child(row().pt(2.0).child(mark))
            .child(body);
        if on {
            opt = opt.fill(k.c.text_accent.alpha(0.07));
        } else {
            opt = opt.hover(k.c.surface_raised_hover);
        }
        c = c.child(opt);
    }
    c = c.child(text(format!("If unanswered: {who} keeps waiting and nothing on this moves until you choose.")).font(META).color(weak(k)).wrap());
    c = c.child(
        row()
            .w_full()
            .gap(6.0)
            .items_center()
            .child(Button::new("Later").variant(ButtonVariant::Ghost).on_press(|s: &mut App| crate::act(s, "Later")).build(k))
            .child(Button::new("Dismiss").variant(ButtonVariant::Ghost).on_press(|s: &mut App| crate::act(s, "Dismiss")).build(k))
            .child(spacer())
            .child(Button::new("Send answer").variant(ButtonVariant::Contrast).shortcut("⌘↵").on_press(|s: &mut App| crate::act(s, "Send answer")).build(k))
            .child(ghost_keys(&["⌘↵"], k)),
    );
    col().w_full().radius(14.0).fill(k.c.surface_raised).border(1.0, k.c.info.icon.alpha(0.35)).child(c)
}

pub fn crumb(parts: &[(&str, bool)], k: &Kit) -> El {
    let mut r = row().gap(8.0).items_center();
    for (i, (p, strong_)) in parts.iter().enumerate() {
        if i > 0 {
            r = r.child(text("/".to_string()).font(BODY).color(weak(k)));
        }
        r = r.child(text(p.to_string()).font(if *strong_ { BODY_MED } else { BODY }).color(if *strong_ { strong(k) } else { weak(k) }).truncate().max_w(380.0));
    }
    r
}

pub fn sessions_block(s: &App, compact: bool, k: &Kit) -> El {
    let by = feed::derive::sessions::busy_by_group(&s.d.sessions);
    let total = s.d.working();
    let listed = s.d.listed();
    let open = Button::new("All sessions").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).icon(&icons::ARROW_RIGHT).shortcut("⌘⇧S").on_press(|s: &mut App| go(s, Screen::Sessions)).build(k);
    let mut chips = row().gap(6.0).flex_wrap().items_center();
    for (name, n) in &by {
        let pname = name.clone();
        chips = chips.child(
            row()
                .gap(6.0)
                .items_center()
                .px(8.0)
                .py(4.0)
                .radius(7.0)
                .fill(k.c.surface_base)
                .border(1.0, edge(k))
                .hover(k.c.surface_raised_hover)
                .on_press(move |s: &mut App| {
                    s.sessions_project = if pname == "No project" { None } else { Some(pname.clone()) };
                    s.sessions_filter = 1;
                    go(s, Screen::Sessions);
                })
                .child(avatar(name, 14.0))
                .child(text(name.clone()).font(META).color(base(k)))
                .child(text(n.to_string()).font(META_MED).color(k.c.text_accent)),
        );
    }
    let head = row()
        .w_full()
        .gap(8.0)
        .items_center()
        .child(text("Sessions".to_string()).font(HEAD).color(strong(k)))
        .child(text(format!("{total} working of {listed}")).font(META).color(weak(k)))
        .child(spacer())
        .child(open);
    let c = col().w_full().gap(8.0).child(head).child(chips);
    if compact { c } else { card(c.padding(14.0), k) }
}
