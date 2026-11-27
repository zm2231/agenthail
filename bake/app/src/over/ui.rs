use rune::{Color, Cx, El, Icon, Spring, TextStyle, col, el, icon, oklch, row, text};
use rune_kit::shell::{PaneMotion, RowStatus, Sidebar, SidebarRow, SidebarSection, SidebarToggle, SplitView, Toolbar};
use rune_kit::{Badge, BadgeVariant, Button, ButtonVariant, ControlSize, Keybind, KeybindVariant, Kit, Spinner};
use feed::derive::sessions::busy;
use rune_theme_opencode::icons;

use feed::derive::needs::is_new;

use crate::over::{Group, Happened, Need, NeedKind, PHASES, Target};
use crate::over::{go, next_need, open_target};
use crate::{App, Screen};

pub const TITLE: TextStyle = TextStyle::new(22.0, 640.0);
pub const HEAD: TextStyle = TextStyle::new(15.0, 600.0);
pub const BODY: TextStyle = TextStyle::new(14.0, 430.0);
pub const BODY_MED: TextStyle = TextStyle::new(14.0, 560.0);
pub const META: TextStyle = TextStyle::new(12.5, 440.0);
pub const META_MED: TextStyle = TextStyle::new(12.5, 560.0);
pub const LABEL: TextStyle = TextStyle::new(11.0, 600.0);

pub fn kit() -> &'static Kit {
    crate::theme::chrome_kit()
}

pub fn edge() -> Color {
    crate::theme::paint().border
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
    let body = match s.ov.widths.get(key) {
        Some(w) if *w > 8.0 => text(fit_lines(t, style, w - 1.0, lines)).font(style).color(color),
        _ => text(t.to_string()).font(style).color(color).wrap_anywhere().max_lines(lines),
    };
    col().key(format!("fit-{key}")).w_full().min_w(0.0).shrink().on_layout(move |s: &mut App, r: [f32; 4]| {
        if s.ov.widths.get(&k).is_none_or(|w| (w - r[2]).abs() > 0.5) {
            s.ov.widths.insert(k.clone(), r[2]);
        }
    }).child(body)
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
        NeedKind::Finished => c.icon_base,
    }
}

pub fn needs_color() -> Color {
    kit().c.warning.icon
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
        .border(1.0, edge())
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
    r.child(text(PHASES[phase.min(5)].to_string()).font(META).color(weak()).ml(6.0))
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
    col().w_full().radius(12.0).fill(k.c.surface_base).border(1.0, edge()).child(content)
}

pub fn new_session_button(project: Option<&str>) -> El {
    let project = project.map(str::to_string);
    Button::new("New session")
        .variant(ButtonVariant::Ghost)
        .control_size(ControlSize::Small)
        .icon(&icons::PLUS)
        .shortcut("⌘N")
        .on_press_tx(move |s: &mut App, tx: &mut rune::Tx| {
            let prefill = match &project {
                Some(key) => crate::ui::new_session::project_prefill(s, key),
                None => crate::ui::new_session::Prefill::default(),
            };
            crate::ui::new_session::open(s, prefill, tx);
        })
        .build(kit())
}

pub fn notice(message: &str) -> El {
    let toast = rune_kit::Toast::new("notice", message.to_string()).icon(kit(), &icons::INFO).on_close(|s: &mut App| s.ov.notice = None).closable().build(kit());
    col().absolute(0.0, 0.0).w_full().h_full().items_end().justify_end().padding(24.0).child(toast)
}

pub fn need_actions(s: &App, n: &Need, compact: bool) -> El {
    let k = kit();
    let t = n.target.clone();
    let size = if compact { ControlSize::Small } else { ControlSize::Regular };
    let r = row().gap(6.0).items_center();
    let work = match &t {
        Target::Work(id) => s.ov.d.work.get(id).map(|w| (w, crate::over::work_review(s, id))),
        _ => None,
    };
    let r = match (n.kind, work) {
        (NeedKind::Check, Some((_, Some(d)))) => crate::over::decisions::review_buttons(s, &d, size, &format!("{}-{}", if compact { "need-row" } else { "need-focus" }, d.id()), true, r),
        (NeedKind::Check, _) => {
            return r.child(Button::new("Open").variant(ButtonVariant::Contrast).control_size(size).on_press(move |s: &mut App| open_target(s, &t)).build(k));
        }
        (NeedKind::CiFailing, Some((w, d))) => {
            let url = crate::over::work::pr_of(s, w, &d.unwrap_or_default(), feed::derive::work::pr(w).as_deref()).map(|p| p.url);
            let t = t.clone();
            r.child(
                Button::new(n.kind.primary())
                    .variant(ButtonVariant::Contrast)
                    .control_size(size)
                    .on_press(move |s: &mut App| match &url {
                        Some(url) => crate::open_link(url),
                        None => open_target(s, &t),
                    })
                    .build(k),
            )
        }
        _ => r.child(Button::new(n.kind.primary()).variant(ButtonVariant::Contrast).control_size(size).on_press({
            let t = t.clone();
            move |s: &mut App| open_target(s, &t)
        }).build(k)),
    };
    if n.kind == NeedKind::CiFailing || matches!(t, Target::Session(_)) {
        return r;
    }
    r.child(Button::new("Open").variant(ButtonVariant::Ghost).control_size(size).on_press(move |s: &mut App| open_target(s, &t)).build(k))
}

pub fn need_row(s: &App, i: usize, n: &Need, focused: bool) -> El {
    let k = kit();
    let t = n.target.clone();
    let mut meta = row().gap(8.0).items_center().child(kind_pill(n.kind)).child(text(n.project.clone()).font(META_MED).color(base()));
    let who = if matches!(n.target, Target::Session(_)) { format!("{} · {}", short_title(&n.who, 24), s.ov.d.ago(&n.at)) } else { s.ov.d.ago(&n.at) };
    meta = meta.child(text(who).font(META).color(weak()));
    if let Some(l) = &n.label {
        meta = meta.child(id_label(l));
    }
    let body = col().grow(1.0).shrink().min_w(0.0).gap(6.0).child(title_fit(s, &format!("need-{}", n.title), &n.title, BODY_MED, strong(), 2)).child(meta);
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
        .child(need_actions(s, n, true))
        .on_press(move |s: &mut App| {
            s.ov.need_ix = i;
            open_target(s, &t);
        });
    if focused {
        r = r.fill(k.c.surface_raised_hover);
    } else {
        r = r.hover(k.c.surface_raised_hover);
    }
    r
}

pub fn group_head(g: Group, n: usize) -> El {
    let color = if g == Group::Holding { needs_color() } else { weak() };
    row().w_full().gap(8.0).items_center().px(10.0).pt(8.0).pb(2.0).child(text(g.label().to_uppercase()).font(LABEL).color(color)).child(text(n.to_string()).font(LABEL).color(weak()))
}

pub fn new_divider(key: String) -> El {
    let line = || el().h(1.0).grow(1.0).fill(kit().c.text_accent.alpha(0.4));
    row().key(key).w_full().gap(8.0).items_center().px(10.0).py(4.0).child(line()).child(text("New since you looked".to_string()).font(LABEL).color(kit().c.text_accent)).child(line())
}

pub enum Slot<'a> {
    Head(Group, usize),
    Need(usize, &'a Need),
    Divider,
}

pub fn need_slots<'a>(needs: &'a [Need], looked: Option<&str>, per_group: usize) -> Vec<Slot<'a>> {
    let mut out = Vec::new();
    for g in crate::over::GROUPS {
        let rows: Vec<(usize, &Need)> = needs.iter().enumerate().filter(|(_, n)| n.group == g).take(per_group).collect();
        if rows.is_empty() {
            continue;
        }
        out.push(Slot::Head(g, needs.iter().filter(|n| n.group == g).count()));
        let fresh = rows.iter().take_while(|(_, n)| is_new(&n.at, looked)).count();
        for (j, (i, n)) in rows.into_iter().enumerate() {
            out.push(Slot::Need(i, n));
            if j + 1 == fresh {
                out.push(Slot::Divider);
            }
        }
    }
    out
}

pub fn needs_list(s: &App) -> El {
    let mut c = col().w_full().gap(2.0).padding(6.0);
    for (si, slot) in need_slots(&s.ov.needs, s.ov.looked_now.as_deref(), usize::MAX).into_iter().enumerate() {
        c = c.child(match slot {
            Slot::Head(g, count) => group_head(g, count).key(format!("needs-group-{si}")),
            Slot::Divider => new_divider(format!("needs-new-{si}")),
            Slot::Need(i, n) => need_row(s, i, n, i == s.ov.need_ix),
        });
    }
    card(c)
}

pub fn needs_button(s: &App) -> El {
    let n = s.ov.needs.len();
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
    let mut ids: Vec<String> = s.ov.hands_on.clone();
    if let Some(c) = current.filter(|c| !ids.iter().any(|x| x == c)) {
        ids.push(c.to_string());
    }
    for (i, id) in ids.iter().enumerate() {
        let Some(x) = s.ov.d.session(id) else { continue };
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
            .child(text(short_title(&s.ov.d.title(x), 18)).font(if on { META_MED } else { META }).color(if on { strong() } else { base() }).truncate().shrink())
            .on_press(move |s: &mut App| crate::over::open_session(s, &id2));
        if busy {
            tab = tab.child(Spinner::new().size(11.0).build(k));
        }
        if i < s.ov.hands_on.len() {
            tab = tab.child(ghost_keys(&[format!("⌘{}", i + 1).as_str()]));
        } else {
            tab = tab.child(text("viewing".to_string()).font(META).color(weak()));
        }
        tab = if on { tab.fill(k.c.surface_raised_hover).border(1.0, edge()) } else { tab.hover(k.c.surface_raised_hover) };
        r = r.child(tab);
    }
    r
}

pub use feed::derive::text::short_title;

fn with_menu(row_: El, id: &str) -> El {
    let id = id.to_string();
    row_.on_secondary_press(move |s: &mut App, p, _: &mut rune::Tx| crate::ui::session_menu::open_at(s, &id, p))
}

fn session_row(s: &App, x: &feed::catalog::Session, depth: u8, lead: bool, selected: Option<&str>) -> El {
    let id = x.id.clone();
    let waiting = x.display.label.starts_with("Waiting on");
    let mut r = SidebarRow::new(short_title(&s.ov.d.title(x), 40))
        .height(44.0)
        .depth(depth)
        .avatar(avatar(&s.ov.d.title(x), 18.0))
        .subtitle(if waiting { x.display.label.clone() } else if lead { format!("Lead · {}", short_title(&s.ov.d.gist(x), 40)) } else { short_title(&s.ov.d.gist(x), 48) })
        .selected(selected == Some(x.id.as_str()))
        .on_select(move |s: &mut App, _| crate::over::open_session(s, &id));
    if x.unread() || x.pinned() {
        r = r.trailing(crate::ui::session_menu::marks(x));
    }
    if busy(x) {
        r = r.status(RowStatus::Busy);
    }
    if waiting {
        r = r.subtitle_color(kit().c.warning.icon);
    }
    with_menu(r.build(kit()).key(session_key(&x.id)), &x.id)
}

pub fn project_for(s: &App, x: &feed::catalog::Session) -> Option<String> {
    Some(x.project_key()).filter(|k| s.ov.d.project(k).is_some())
}

pub fn sidebar_order(s: &App, current_project: Option<&str>, selected: Option<&str>) -> Vec<String> {
    let mut out: Vec<String> = Vec::new();
    if !matches!(s.screen, Screen::Now) {
        for slot in need_slots(&s.ov.needs, s.ov.looked_now.as_deref(), 4) {
            if let Slot::Need(_, n) = slot
                && let Target::Session(id) = &n.target
            {
                out.push(id.clone());
            }
        }
    }
    if let Some(p) = current_project.and_then(|k| s.ov.d.project(k)) {
        out.extend(s.ov.d.project_rows(p).into_iter().map(|(id, _)| id));
    }
    if let Some(id) = selected.filter(|id| !out.iter().any(|x| x == id)) {
        out.push(id.to_string());
    }
    let mut seen = std::collections::HashSet::new();
    out.retain(|id| seen.insert(id.clone()));
    out
}

pub fn session_key(id: &str) -> String {
    format!("s-{id}")
}

pub fn sidebar(s: &App, current_project: Option<&str>, selected: Option<&str>) -> El {
    let k = kit();
    let home = SidebarRow::new("Now").icon(&icons::DOT_GRID).detail("⌘0").height(32.0).selected(matches!(s.screen, Screen::Now)).on_select(|s: &mut App, _| go(s, Screen::Now)).build(k).key("home");
    let search = SidebarRow::new("Jump to").icon(&icons::MAGNIFYING_GLASS).detail("⌘K").height(32.0).on_select(|s: &mut App, tx: &mut rune::Tx| crate::over::open_jump(s, tx)).build(k).key("jump");
    let all = SidebarRow::new("Sessions")
        .icon(&icons::BULLET_LIST)
        .detail("⌘⇧S")
        .height(32.0)
        .selected(matches!(s.screen, Screen::Sessions))
        .on_select(|s: &mut App, _| go(s, Screen::Sessions))
        .build(k)
        .key("sessions");
    let apps = SidebarRow::new("Apps")
        .icon(&icons::EXTENSIONS)
        .height(32.0)
        .selected(matches!(s.screen, Screen::Extensions | Screen::Extension(_)))
        .on_select(|s: &mut App, _| go(s, Screen::Extensions))
        .build(k)
        .key("apps");
    let mut sb = Sidebar::new().label("Navigation").inset(0.0).header_gap(2.0).header_padding(0.0, 12.0).row_gap(2.0).header(home).header(search).header(all).header(apps);
    let mut needs = SidebarSection::new("Needs you").key("sec-needs").count(s.ov.needs.len());
    for (si, slot) in need_slots(&s.ov.needs, s.ov.looked_now.as_deref(), 4).into_iter().enumerate() {
        let (i, n) = match slot {
            Slot::Head(g, count) => {
                needs = needs.row(group_head(g, count).key(format!("need-group-{si}")));
                continue;
            }
            Slot::Divider => {
                needs = needs.row(new_divider(format!("need-new-{si}")));
                continue;
            }
            Slot::Need(i, n) => (i, n),
        };
        let t = n.target.clone();
        let sel = match (&n.target, selected) {
            (Target::Session(id), Some(x)) | (Target::Work(id), Some(x)) => id == x,
            _ => false,
        };
        let mut need = SidebarRow::new(short_title(&n.title, 60))
            .height(44.0)
            .avatar(avatar(&n.project, 18.0))
            .subtitle(n.kind.wanted())
            .subtitle_color(needs_color())
            .selected(sel)
            .on_select(move |s: &mut App, _| open_target(s, &t));
        let held = match &n.target {
            Target::Session(id) => s.ov.d.session(id),
            _ => None,
        };
        if let Some(x) = held.filter(|x| x.unread() || x.pinned()) {
            need = need.trailing(crate::ui::session_menu::marks(x));
        }
        let mut need = need.build(k).key(format!("need-{i}"));
        if let Target::Session(id) = &n.target {
            need = with_menu(need, id);
        }
        needs = needs.row(need);
    }
    if !matches!(s.screen, Screen::Now) {
        sb = sb.section(needs);
    }
    let mut order = crate::over::projects::listed(s);
    if let Some(cp) = current_project {
        order.sort_by_key(|p| p.key != cp);
    }
    let mut shown = false;
    for (pi, p) in order.iter().take(if current_project.is_some() { 1 } else { 0 }).enumerate() {
        let mut sec = SidebarSection::new(p.name.clone()).key(format!("sec-p{pi}")).count(p.sessions.len());
        for (_, (id, depth)) in s.ov.d.project_rows(p).iter().enumerate().filter(|(n, (id, _))| *n < 8 || selected == Some(id.as_str())) {
            let Some(x) = s.ov.d.session(id) else { continue };
            shown |= selected == Some(id.as_str());
            sec = sec.row(session_row(s, x, *depth, p.lead.as_deref() == Some(id.as_str()), selected));
        }
        let pname = p.name.clone();
        sec = sec.row(
            SidebarRow::new("All sessions")
                .icon(&icons::ARROW_RIGHT)
                .height(30.0)
                .detail(format!("{}", p.sessions.len()))
                .on_select(move |s: &mut App, _| {
                    s.ov.sessions_project = Some(pname.clone());
                    go(s, Screen::Sessions);
                })
                .build(k)
                .key(format!("all-{}", p.key)),
        );
        sb = sb.section(sec);
    }
    if !shown
        && matches!(s.screen, Screen::Session)
        && let Some(x) = selected.and_then(|id| s.ov.d.session(id).or_else(|| crate::ui::gone::shown(s, id)))
    {
        sb = sb.section(SidebarSection::new(x.project_name()).key("sec-open").row(session_row(s, x, 0, false, selected)));
    }
    let mut others = SidebarSection::new("Projects").key("sec-projects").count(s.ov.d.projects.len());
    for p in order.iter().skip(if current_project.is_some() { 1 } else { 0 }).take(10) {
        let key = p.key.clone();
        let waiting = s.ov.needs.iter().filter(|n| n.project_key == p.key).count();
        let mut r = SidebarRow::new(p.name.clone())
            .height(32.0)
            .avatar(avatar(&p.name, 18.0))
            .detail(s.ov.d.ago(&p.last_active))
            .selected(matches!(&s.screen, Screen::Project(k2) if *k2 == p.key))
            .on_select(move |s: &mut App, _| go(s, Screen::Project(key.clone())));
        if waiting > 0 {
            r = r.badge(waiting.to_string());
        }
        let menu_key = p.key.clone();
        others = others.row(r.build(k).key(format!("p-{}", p.key)).on_secondary_press(move |s: &mut App, at, _: &mut rune::Tx| crate::over::projects::open_menu(s, &menu_key, Some(at))));
    }
    for extra in crate::over::projects::sidebar_rows(s) {
        others = others.row(extra);
    }
    sb = sb.section(others);
    sb.build(k)
}

pub fn frame(cx: &mut Cx<App>, s: &App, sidebar: Option<El>, main: El, inspector: Option<El>, toolbar: Toolbar) -> El {
    let k = kit();
    let mut v = SplitView::new(main);
    if let Some(sb) = sidebar {
        v = v.sidebar(&s.ov.sidebar, |s: &mut App| &mut s.ov.sidebar, sb).sidebar_toggle(SidebarToggle::new().size(ControlSize::Large).past_lights(16.0).edge(18.0).gap(4.0).in_header());
    } else {
        v = v.hide_sidebar_toggle();
    }
    if let Some(i) = inspector {
        v = v.inspector(&s.ov.inspector, |s: &mut App| &mut s.ov.inspector, i);
    }
    let split = v
        .toolbar(toolbar)
        .titlebar_inset(cx.titlebar_inset())
        .frame_inset(8.0)
        .toolbar_in_panel()
        .sidebar_padding(14.0, 10.0)
        .pane_motion(PaneMotion::new().spring(Spring::new(0.3, 0.0)))
        .reduced_motion(cx.is_reduced_motion())
        .build(k);
    col().w_full().h_full().child(col().w_full().grow(1.0).min_h(0.0).child(split)).child(status_bar(s))
}

pub fn frame_now(cx: &mut Cx<App>, s: &App, sidebar: Option<El>, main: El, inspector: Option<El>, toolbar: Toolbar) -> El {
    let k = kit();
    let mut v = SplitView::new(main);
    if let Some(sb) = sidebar {
        v = v.sidebar(&s.ov.now_side, |s: &mut App| &mut s.ov.now_side, sb).sidebar_toggle(SidebarToggle::new().size(ControlSize::Large).past_lights(16.0).edge(18.0).gap(4.0).in_header());
    } else {
        v = v.hide_sidebar_toggle();
    }
    if let Some(i) = inspector {
        v = v.inspector(&s.ov.now_rail, |s: &mut App| &mut s.ov.now_rail, i);
    }
    let split = v
        .toolbar(toolbar)
        .titlebar_inset(cx.titlebar_inset())
        .frame_inset(8.0)
        .toolbar_in_panel()
        .sidebar_padding(14.0, 10.0)
        .pane_motion(PaneMotion::new().spring(Spring::new(0.3, 0.0)))
        .reduced_motion(cx.is_reduced_motion())
        .build(k);
    col().w_full().h_full().child(col().w_full().grow(1.0).min_h(0.0).child(split)).child(status_bar(s))
}

pub fn pane_strip(i: usize, glyph: &'static Icon, count: Option<(usize, Color)>) -> El {
    let k = kit();
    let name = crate::over::layout::PANES[i];
    let len = 140.0;
    let label = el().w(16.0).h(len).child(text(name.to_string()).font(META_MED).color(base()).w(len).h(16.0).absolute(8.0 - len / 2.0, len / 2.0 - 8.0).rotate(std::f32::consts::FRAC_PI_2));
    let mut c = col()
        .key(format!("strip-{i}"))
        .w(40.0)
        .h_full()
        .items_center()
        .gap(10.0)
        .pt(14.0)
        .radius(10.0)
        .fill(k.c.surface_base)
        .border(1.0, edge())
        .hover(k.c.surface_raised_hover)
        .on_press(move |s: &mut App| crate::over::layout::expand_pane(s, i))
        .child(icon(glyph, 15.0).color(k.c.icon_base));
    if let Some((n, color)) = count {
        c = c.child(row().min_w(20.0).h(18.0).px(5.0).radius(9.0).center().fill(color.alpha(0.18)).child(text(n.to_string()).font(LABEL).color(color)));
    }
    c.child(label).child(spacer()).child(ghost_keys(&[format!("⌥⌘{}", i + 1).as_str()]).pb(12.0))
}

pub fn pane_head(s: &App, i: usize, title: &str) -> El {
    let k = kit();
    let focused = s.ov.now_focus.map(|(f, _)| f) == Some(i);
    let focus = rune_kit::IconButton::new(if focused { &icons::COLLAPSE } else { &icons::EXPAND })
        .variant(rune_kit::IconButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .label(if focused { "Restore panes" } else { "Focus this pane" })
        .help(if focused { format!("Restore panes (⌥⇧⌘{} or esc)", i + 1) } else { format!("Focus this pane (⌥⇧⌘{})", i + 1) })
        .on_press(move |s: &mut App| crate::over::layout::focus_pane(s, i))
        .build(k);
    let hide = rune_kit::IconButton::new(if i == 2 { &icons::CHEVRON_DOUBLE_RIGHT } else { &icons::MINUS })
        .variant(rune_kit::IconButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .label("Collapse this pane")
        .help(format!("Collapse to a strip (⌥⌘{})", i + 1))
        .on_press(move |s: &mut App| crate::over::layout::toggle_pane(s, i))
        .build(k);
    let mut r = row().key(format!("pane-head-{i}")).w_full().h(36.0).px(14.0).gap(6.0).items_center().border_sides([0.0, 0.0, 1.0, 0.0], rule()).child(label(title));
    if focused {
        r = r.child(text("focused".to_string()).font(META).color(k.c.text_accent));
    }
    r.child(spacer()).child(focus).child(hide)
}

fn until(s: &App, at: &str) -> String {
    let h = -s.ov.d.hours(at);
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

pub fn status_bar(s: &App) -> El {
    let k = kit();
    let mut meters = row().key("usage-meters").h_full().gap(12.0).px(6.0).radius(6.0).items_center().hover(k.c.surface_raised_hover).label("Usage by account").on_press(crate::ui::accounts::open).child(text("Used".to_string()).font(META).color(weak()));
    for m in &s.ov.d.usage {
        let pct = m.percent.unwrap_or(0.0);
        let color = if pct >= 80.0 { k.c.danger.icon } else if pct >= 60.0 { k.c.warning.icon } else { base() };
        let window = if m.label.ends_with("week") { "wk" } else { "5h" };
        let who = match &m.name {
            Some(n) if n != "default" => format!("{n} {window}"),
            _ => format!("{} {window}", m.provider.chars().next().map(|c| c.to_uppercase().collect::<String>() + &m.provider[1..]).unwrap_or_default()),
        };
        let bar = row().w(28.0).h(4.0).radius(2.0).fill(k.c.border_base).child(el().w((28.0 * pct / 100.0).clamp(2.0, 28.0) as f32).h(4.0).radius(2.0).fill(color));
        meters = meters.child(
            row()
                .gap(6.0)
                .items_center()
                .child(text(who).font(META).color(weak()))
                .child(bar)
                .child(text(format!("{pct:.0}%")).font(META_MED).color(color))
                .child(text(until(s, m.resets_at.as_deref().unwrap_or(""))).font(META).color(weak())),
        );
    }
    let mut r = row().key("status-bar").w_full().h(30.0).px(10.0).gap(12.0).items_center().fill(k.c.bg_deep).child(meters);
    let working = s.ov.d.working();
    r = r.child(spacer());
    if let Some(down) = crate::ui::connect::bar(s) {
        r = r.child(down);
    } else if let Some(v) = &s.ov.inbox.version {
        let restart = Button::new(if s.link.starting { "Restarting…" } else { "Restart for this version" }).variant(ButtonVariant::GhostMuted).control_size(ControlSize::Small).disabled(s.link.starting).on_press(crate::ui::connect::start_daemon).build(k).key("version-restart");
        r = r.child(row().gap(6.0).items_center().shrink().min_w(0.0).child(icon(&rune_theme_opencode::icons::INFO, 12.0).color(k.c.warning.icon)).child(text(v.clone()).font(META).color(k.c.warning.icon).truncate().shrink()).child(restart));
    }
    r = r.child(row().gap(6.0).items_center().child(Spinner::new().size(10.0).build(k)).child(text(format!("{working} working")).font(META).color(base())));
    for h in &s.ov.d.hosts {
        let up = matches!(h.state.as_str(), "local" | "connected");
        r = r.child(
            row()
                .gap(6.0)
                .items_center()
                .child(el().size(7.0, 7.0).radius(3.5).fill(if up { k.c.success.icon } else { k.c.text_weak }))
                .child(text(h.name.clone()).font(META).color(if up { base() } else { weak() })),
        );
    }
    r
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

pub fn sessions_block(s: &App, compact: bool) -> El {
    let k = kit();
    let by = feed::derive::sessions::busy_by_group(&s.ov.d.sessions);
    let total = s.ov.d.working();
    let listed = s.ov.d.listed();
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
                .border(1.0, edge())
                .hover(k.c.surface_raised_hover)
                .on_press(move |s: &mut App| {
                    s.ov.sessions_project = if pname == "No project" { None } else { Some(pname.clone()) };
                    s.ov.sessions_filter = 1;
                    go(s, Screen::Sessions);
                })
                .child(avatar(name, 14.0))
                .child(text(name.clone()).font(META).color(base()))
                .child(text(n.to_string()).font(META_MED).color(k.c.text_accent)),
        );
    }
    let head = row()
        .w_full()
        .gap(8.0)
        .items_center()
        .child(text("Sessions".to_string()).font(HEAD).color(strong()))
        .child(text(format!("{total} working of {listed}")).font(META).color(weak()))
        .child(spacer())
        .child(open);
    let c = col().w_full().gap(8.0).child(head).child(chips);
    if compact { c } else { card(c.padding(14.0)) }
}
