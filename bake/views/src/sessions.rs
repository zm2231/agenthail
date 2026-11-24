use feed::catalog::{Session, State};
use rune::{Color, Cx, El, ScrollSpec, TextStyle, col, el, icon, row, text, text_input};
use rune_kit::shell::Toolbar;
use rune_kit::{Badge, BadgeVariant, SegmentedControl, Select, Spinner};
use rune_theme_opencode::icons;

use crate::data::{NeedKind, Target};
use crate::ui::{self, BODY_MED, HEAD, META, META_MED, base, kit, strong, weak};
use crate::{App, Screen, go};

pub const FILTERS: [&str; 5] = ["All", "Working", "Needs you", "Idle", "Archived"];

pub fn hidden(x: &Session) -> bool {
    x.name.trim_start().starts_with('<') && x.alias.is_none() || x.display.label == "Closed" && !matches!(x.display.kind.as_str(), "working" | "finishing" | "starting")
}

pub fn busy(x: &Session) -> bool {
    matches!(x.display.kind.as_str(), "working" | "finishing" | "starting")
}

pub fn need_of<'a>(s: &'a App, x: &Session) -> Option<&'a crate::data::Need> {
    let replay = s.ask.as_ref().filter(|_| s.replayed).map(|a| a.session.as_str());
    s.needs.iter().find(|n| n.target == Target::Session(x.id.clone()) && !(n.kind == NeedKind::Question && replay == Some(x.id.as_str())))
}

fn rank(s: &App, x: &Session) -> u8 {
    if need_of(s, x).is_some() {
        0
    } else if busy(x) {
        1
    } else {
        2
    }
}

pub fn role(s: &App, x: &Session) -> &'static str {
    let lead = s.d.projects.iter().any(|p| p.lead.as_deref() == Some(x.id.as_str()));
    let t = s.d.title(x).to_lowercase();
    if lead {
        "Lead"
    } else if t.contains("review") {
        "Reviewer"
    } else if s.d.spawned.contains(&x.id) || s.d.started_by.contains_key(&x.id) {
        "Worker"
    } else {
        "Session"
    }
}

fn agent(x: &Session) -> String {
    let mut c = x.surface.chars();
    c.next().map(|f| f.to_uppercase().collect::<String>() + c.as_str()).unwrap_or_default()
}

fn keep(s: &App, x: &Session) -> bool {
    let f = s.sessions_filter;
    let ok = match f {
        4 => x.archived,
        _ if x.archived || hidden(x) => false,
        1 => busy(x),
        2 => need_of(s, x).is_some(),
        3 => !busy(x) && need_of(s, x).is_none(),
        _ => true,
    };
    let q = s.sessions_query.text.trim().to_lowercase();
    let matches = q.is_empty() || s.d.title(x).to_lowercase().contains(&q) || x.project_name().to_lowercase().contains(&q) || s.d.gist(x).to_lowercase().contains(&q);
    let project = s.sessions_project.as_deref().is_none_or(|p| x.project_name() == p);
    ok && matches && project
}

pub fn status_line(s: &App, x: &Session) -> (String, Color, bool) {
    let k = kit();
    if let Some(n) = need_of(s, x) {
        return (format!("{}: {}", n.kind.wanted(), ui::short_title(&n.title, 90)), ui::kind_color(n.kind), false);
    }
    if busy(x) {
        let verb = crate::session::live_verb(s, &x.id).unwrap_or_else(|| if x.display.detail.is_empty() { "Working".into() } else { x.display.detail.clone() });
        return (verb, k.c.text_accent, true);
    }
    (s.d.gist(x), base(), false)
}

fn state_dot(s: &App, x: &Session) -> El {
    let k = kit();
    let color = if let Some(n) = need_of(s, x) {
        ui::kind_color(n.kind)
    } else if busy(x) {
        k.c.text_accent
    } else if x.state(s.d.now) == State::Done {
        k.c.success.icon
    } else {
        k.c.text_weak
    };
    el().size(10.0, 10.0).radius(5.0).fill(color).border(2.0, k.c.bg_base)
}

pub fn session_row(s: &App, x: &Session, depth: u8, selected: bool) -> El {
    let k = kit();
    let id = x.id.clone();
    let branch = x.checkout.branch.clone().unwrap_or_default();
    let mut place = vec![role(s, x).to_string(), agent(x), x.host.name.clone()];
    if !branch.is_empty() {
        place.push(branch);
    }
    let (line, color, live) = status_line(s, x);
    let trailing = if busy(x) {
        row().gap(5.0).items_center().child(el().size(6.0, 6.0).radius(3.0).fill(k.c.text_accent)).child(text("live".to_string()).font(META).color(k.c.text_accent))
    } else if x.state(s.d.now) == State::Done {
        row().gap(5.0).items_center().child(icon(&icons::CIRCLE_CHECK, 12.0).color(k.c.success.icon)).child(text(format!("done {}", s.d.ago(&x.last_active))).font(META).color(weak()))
    } else {
        text(format!("idle {}", s.d.ago(&x.last_active))).font(META).color(weak())
    };
    let mut status = row().gap(6.0).items_center();
    if live {
        status = status.child(Spinner::new().size(11.0).build(k));
    }
    status = status.child(text(line).font(META).color(color).truncate().shrink());
    let mut headline = row().w_full().gap(8.0).items_center().child(text(s.d.title(x)).font(BODY_MED).color(strong()).truncate().shrink());
    if let Some((h, _)) = crate::data::Data::headline(x).filter(|_| need_of(s, x).is_some() || busy(x)) {
        headline = headline.child(text(ui::short_title(&crate::data::plain(&h), 60)).font(META).color(weak()).truncate().shrink());
    }
    headline = headline.child(ui::spacer()).child(trailing);
    let mut r = row()
        .key(format!("srow-{}", x.id))
        .w_full()
        .gap(12.0)
        .items_start()
        .pl(12.0 + 28.0 * depth as f32)
        .pr(12.0)
        .py(9.0)
        .radius(10.0)
        .on_press(move |s: &mut App| go(s, Screen::Session(id.clone())))
        .child(col().size(28.0, 28.0).child(ui::avatar(&s.d.title(x), 28.0)).child(state_dot(s, x).absolute(20.0, 20.0)))
        .child(
            col()
                .grow(1.0)
                .shrink()
                .min_w(0.0)
                .gap(3.0)
                .child(headline)
                .child(row().gap(4.0).items_center().child(icon(&icons::BRANCH, 11.0).color(weak())).child(text(place.join(" · ")).font(META).color(weak()).truncate().shrink()))
                .child(status),
        );
    r = if selected { r.fill(k.c.surface_raised_hover).border(1.0, ui::rule()) } else { r.hover(k.c.surface_raised_hover) };
    r
}

pub fn ordered_with_crew<'a>(s: &'a App, list: Vec<&'a Session>) -> Vec<(&'a Session, u8)> {
    let mut list = list;
    list.sort_by(|a, b| rank(s, a).cmp(&rank(s, b)).then(b.last_active.cmp(&a.last_active)));
    let ids: Vec<&str> = list.iter().map(|x| x.id.as_str()).collect();
    let parent = |x: &Session| s.d.started_by.get(&x.id).filter(|p| ids.contains(&p.as_str())).cloned();
    let mut out = Vec::new();
    for x in list.iter().filter(|x| parent(x).is_none()) {
        out.push((*x, 0u8));
        for c in list.iter().filter(|c| parent(c).as_deref() == Some(x.id.as_str())) {
            out.push((*c, 1u8));
        }
    }
    out
}

pub fn view(s: &App, cx: &mut Cx<App>, layer: &mut Vec<El>) -> El {
    let k = kit();
    let all: Vec<&Session> = s.d.sessions.iter().filter(|x| keep(s, x)).collect();
    let count = |f: usize| s.d.sessions.iter().filter(|x| match f {
        4 => x.archived,
        _ if x.archived || hidden(x) => false,
        1 => busy(x),
        2 => need_of(s, x).is_some(),
        3 => !busy(x) && need_of(s, x).is_none(),
        _ => true,
    }).count();
    let labels: Vec<String> = FILTERS.iter().enumerate().map(|(i, f)| format!("{f} {}", count(i))).collect();
    let filters = SegmentedControl::new(&labels, Some(s.sessions_filter)).label("Show").fit().on_pick(|s: &mut App, i, _| s.sessions_filter = i).build(k);
    let mut names: Vec<String> = vec!["All projects".into()];
    for p in &s.d.projects {
        names.push(p.name.clone());
    }
    let current = s.sessions_project.clone().unwrap_or_else(|| "All projects".into());
    let project = Select::new("project-filter", &names)
        .current(Some(&current))
        .open(s.sessions_project_open)
        .on_toggle(|s: &mut App| s.sessions_project_open = !s.sessions_project_open)
        .on_close(|s: &mut App| s.sessions_project_open = false)
        .on_choose(|s: &mut App, v: &str, _| {
            s.sessions_project = if v == "All projects" { None } else { Some(v.to_string()) };
            s.sessions_project_open = false;
        })
        .build(k, layer);
    let search = row()
        .w(240.0)
        .gap(8.0)
        .items_center()
        .px(10.0)
        .py(6.0)
        .radius(8.0)
        .fill(k.c.surface_base)
        .border(1.0, ui::rule())
        .child(icon(&icons::MAGNIFYING_GLASS, 14.0).color(k.c.icon_base))
        .child(
            text_input(&s.sessions_query, TextStyle::new(13.0, 440.0).lh(18.0))
                .single_line()
                .key("sessions-search")
                .label("Search sessions")
                .grow(1.0)
                .shrink()
                .color(strong())
                .placeholder("Search sessions".to_string(), weak())
                .on_edit(|s: &mut App| &mut s.sessions_query),
        );
    let bar = row().w_full().gap(10.0).items_center().child(filters).child(ui::spacer()).child(project).child(search);

    let mut groups: Vec<(String, Option<String>, Vec<&Session>)> = Vec::new();
    for x in &all {
        let name = if x.project.is_some() { x.project_name() } else { "No project".into() };
        match groups.iter_mut().find(|g| g.0 == name) {
            Some(g) => g.2.push(x),
            None => groups.push((name, x.project.as_ref().map(|_| x.project_key()), vec![*x])),
        }
    }
    let order = |g: &(String, Option<String>, Vec<&Session>)| {
        let last = g.2.iter().map(|x| x.last_active.as_str()).max().unwrap_or("").to_string();
        (g.1.is_none(), g.2.iter().map(|x| rank(s, x)).min().unwrap_or(9), std::cmp::Reverse(last))
    };
    groups.sort_by_key(|g| order(g));
    let mut list = col().w_full().gap(18.0).px(24.0).pt(16.0).pb(32.0).child(bar);
    if groups.is_empty() {
        list = list.child(text(if s.sessions_filter == 4 { "No archived sessions in the snapshot." } else { "No sessions match." }.to_string()).font(META).color(weak()).pt(20.0));
    }
    for (name, key, rows) in groups {
        let working = rows.iter().filter(|x| busy(x)).count();
        let needs = rows.iter().filter(|x| need_of(s, x).is_some()).count();
        let mut head = row()
            .w_full()
            .gap(10.0)
            .items_center()
            .px(12.0)
            .pb(4.0)
            .child(ui::avatar(&name, 20.0))
            .child(text(name.clone()).font(HEAD).color(strong()))
            .child(Badge::new(rows.len().to_string()).variant(BadgeVariant::Subtle).build(k));
        if needs > 0 {
            head = head.child(text(format!("{needs} need you")).font(META_MED).color(ui::kind_color(NeedKind::Check)));
        }
        if working > 0 {
            head = head.child(text(format!("{working} working")).font(META_MED).color(k.c.text_accent));
        }
        head = head.child(ui::spacer());
        if let Some(key) = key {
            head = head.child(
                rune_kit::Button::new("Open project")
                    .variant(rune_kit::ButtonVariant::Ghost)
                    .control_size(rune_kit::ControlSize::Small)
                    .on_press(move |s: &mut App| go(s, Screen::Project(key.clone())))
                    .build(k),
            );
        }
        let mut body = col().w_full().gap(2.0).padding(6.0);
        for (x, depth) in ordered_with_crew(s, rows) {
            body = body.child(session_row(s, x, depth, false));
        }
        list = list.child(col().w_full().gap(6.0).child(head).child(ui::card(body)));
    }
    let main = col().key("sessions").w_full().h_full().scroll(ScrollSpec { stick_bottom: false }).child(list);
    let toolbar = Toolbar::new().leading(ui::crumb(&[("Sessions", true)])).trailing(ui::needs_button(s));
    ui::frame(cx, s, Some(ui::sidebar(s, None, None)), main, None, toolbar)
}
