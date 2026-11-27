use std::collections::HashSet;

use feed::catalog::{Session, State};
use feed::derive::needs::{need_of as find_need, needy as needy_of};
use feed::derive::sessions::{FILTERS, busy, groups, in_filter};
use rune::{Color, Cx, El, ScrollSpec, TextStyle, col, el, icon, row, text, text_input};
use rune_kit::shell::Toolbar;
use rune_kit::{Badge, BadgeVariant, Kit, KitCx, SegmentedControl, Select, Spinner};
use rune_theme_opencode::icons;

use crate::ui::{self, BODY_MED, HEAD, META, META_MED, base, strong, weak};
use crate::{App, Screen, go};

fn replayed(s: &App) -> Option<&str> {
    s.ask.as_ref().filter(|_| s.replayed).map(|a| a.session.as_str())
}

pub fn need_of<'a>(s: &'a App, x: &Session) -> Option<&'a crate::data::Need> {
    find_need(&s.needs, &x.id, replayed(s))
}

fn needy(s: &App) -> HashSet<String> {
    needy_of(&s.needs, replayed(s))
}

fn agent(x: &Session) -> String {
    let mut c = x.surface.chars();
    c.next().map(|f| f.to_uppercase().collect::<String>() + c.as_str()).unwrap_or_default()
}

fn keep(s: &App, x: &Session, needy: &HashSet<String>) -> bool {
    let project = s.sessions_project.as_deref().is_none_or(|p| x.project_name() == p);
    in_filter(s.sessions_filter, x, needy) && s.d.matches_query(x, &s.sessions_query.text) && project
}

pub fn status_line(s: &App, x: &Session, k: &Kit) -> (String, Color, bool) {
    if let Some(n) = need_of(s, x) {
        return (format!("{}: {}", n.kind.wanted(), ui::short_title(&n.title, 90)), ui::needs_color(k), false);
    }
    if busy(x) {
        let verb = crate::session::live_verb(s, &x.id).unwrap_or_else(|| if x.display.detail.is_empty() { "Working".into() } else { x.display.detail.clone() });
        return (verb, k.c.text_accent, true);
    }
    (s.d.gist(x), base(k), false)
}

fn state_dot(s: &App, x: &Session, k: &Kit) -> El {
    let color = if need_of(s, x).is_some() {
        ui::needs_color(k)
    } else if busy(x) {
        k.c.text_accent
    } else if x.state(s.d.now) == State::Done {
        k.c.success.icon
    } else {
        k.c.text_weak
    };
    el().size(10.0, 10.0).radius(5.0).fill(color).border(2.0, k.c.bg_base)
}

pub fn session_row(s: &App, x: &Session, depth: u8, selected: bool, k: &Kit) -> El {
    let id = x.id.clone();
    let branch = x.checkout.branch.clone().unwrap_or_default();
    let mut place = vec![s.d.role(x).to_string(), agent(x), x.host.name.clone()];
    if !branch.is_empty() {
        place.push(branch);
    }
    let (line, color, live) = status_line(s, x, k);
    let trailing = if busy(x) {
        row().gap(5.0).items_center().child(el().size(6.0, 6.0).radius(3.0).fill(k.c.text_accent)).child(text("live".to_string()).font(META).color(k.c.text_accent))
    } else if x.state(s.d.now) == State::Done {
        row().gap(5.0).items_center().child(icon(&icons::CIRCLE_CHECK, 12.0).color(k.c.success.icon)).child(text(format!("done {}", s.d.ago(&x.last_active))).font(META).color(weak(k)))
    } else {
        text(format!("idle {}", s.d.ago(&x.last_active))).font(META).color(weak(k))
    };
    let mut status = row().gap(6.0).items_center();
    if live {
        status = status.child(Spinner::new().size(11.0).build(k));
    }
    status = status.child(text(line).font(META).color(color).truncate().shrink());
    let mut headline = row().w_full().gap(8.0).items_center().child(text(s.d.title(x)).font(BODY_MED).color(strong(k)).truncate().shrink());
    if let Some((h, _)) = feed::derive::needs::headline(x).filter(|_| need_of(s, x).is_some() || busy(x)) {
        headline = headline.child(text(ui::short_title(&crate::data::plain(&h), 60)).font(META).color(weak(k)).truncate().shrink());
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
        .child(col().size(28.0, 28.0).child(ui::avatar(&s.d.title(x), 28.0)).child(state_dot(s, x, k).absolute(20.0, 20.0)))
        .child(
            col()
                .grow(1.0)
                .shrink()
                .min_w(0.0)
                .gap(3.0)
                .child(headline)
                .child(row().gap(4.0).items_center().child(icon(&icons::BRANCH, 11.0).color(weak(k))).child(text(place.join(" · ")).font(META).color(weak(k)).truncate().shrink()))
                .child(status),
        );
    r = if selected { r.fill(k.c.surface_raised_hover).border(1.0, ui::edge(k)) } else { r.hover(k.c.surface_raised_hover) };
    r
}

pub fn view(s: &App, cx: &mut Cx<App>, layer: &mut Vec<El>) -> El {
    let k = cx.kit();
    let needy = needy(s);
    let all: Vec<&Session> = s.d.sessions.iter().filter(|x| keep(s, x, &needy)).collect();
    let counts = s.d.filter_counts(&needy);
    let labels: Vec<String> = FILTERS.iter().zip(counts).map(|(f, n)| format!("{f} {n}")).collect();
    let filters = SegmentedControl::new(&labels, Some(s.sessions_filter)).label("Show").fit().on_pick(|s: &mut App, i, _| s.sessions_filter = i).build(&k);
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
        .build(&k, layer);
    let search = row()
        .w(240.0)
        .gap(8.0)
        .items_center()
        .px(10.0)
        .py(6.0)
        .radius(8.0)
        .fill(k.c.surface_base)
        .border(1.0, ui::edge(&k))
        .child(icon(&icons::MAGNIFYING_GLASS, 14.0).color(k.c.icon_base))
        .child(
            text_input(&s.sessions_query, TextStyle::new(13.0, 440.0).lh(18.0))
                .single_line()
                .key("sessions-search")
                .label("Search sessions")
                .grow(1.0)
                .shrink()
                .color(strong(&k))
                .placeholder("Search sessions".to_string(), weak(&k))
                .on_edit(|s: &mut App| &mut s.sessions_query),
        );
    let bar = row().w_full().gap(10.0).items_center().child(filters).child(ui::spacer()).child(project).child(search);

    let groups = groups(&all, &needy);
    let mut list = col().w_full().gap(18.0).px(24.0).pt(16.0).pb(32.0).child(bar);
    if groups.is_empty() {
        list = list.child(text(if s.sessions_filter == 4 { "No archived sessions in the snapshot." } else { "No sessions match." }.to_string()).font(META).color(weak(&k)).pt(20.0));
    }
    for g in groups {
        let (name, key, rows) = (g.name, g.key, g.sessions);
        let working = rows.iter().filter(|x| busy(x)).count();
        let needs = rows.iter().filter(|x| needy.contains(&x.id)).count();
        let mut head = row()
            .w_full()
            .gap(10.0)
            .items_center()
            .px(12.0)
            .pb(4.0)
            .child(ui::avatar(&name, 20.0))
            .child(text(name.clone()).font(HEAD).color(strong(&k)))
            .child(Badge::new(rows.len().to_string()).variant(BadgeVariant::Subtle).build(&k));
        if needs > 0 {
            head = head.child(text(format!("{needs} need you")).font(META_MED).color(ui::needs_color(&k)));
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
                    .build(&k),
            );
        }
        let mut body = col().w_full().gap(2.0).padding(6.0);
        for (x, depth) in s.d.ordered_with_crew(rows, &needy) {
            body = body.child(session_row(s, x, depth, false, &k));
        }
        list = list.child(col().w_full().gap(6.0).child(head).child(ui::card(body, &k)));
    }
    let main = col().key("sessions").w_full().h_full().scroll(ScrollSpec { stick_bottom: false }).child(list);
    let toolbar = Toolbar::new().leading(ui::crumb(&[("Sessions", true)], &k)).trailing(ui::needs_button(s, &k));
    ui::frame(cx, s, Some(ui::sidebar(s, None, None, &k)), main, None, toolbar)
}
