use feed::catalog::{Session, State};
use feed::derive::needs::need_of as find_need;
use feed::derive::sessions::busy;
use feed::listing::{SCOPES, STATUSES, TreeCheckout, TreeProject, project_choices, status_label, tree};
use rune::{Color, Cx, El, ScrollSpec, TextStyle, col, el, icon, row, text, text_input};
use rune_kit::shell::Toolbar;
use rune_kit::{Badge, BadgeVariant, Button, ButtonVariant, ControlSize, Kit, KitCx, SegmentedControl, Select, Spinner};
use rune_theme_opencode::icons;

use crate::over::ui::{self, BODY_MED, HEAD, META, META_MED, base, strong, weak};
use crate::over::go;
use crate::{App, Screen};

pub fn need_of<'a>(s: &'a App, x: &Session) -> Option<&'a crate::over::Need> {
    find_need(&s.ov.needs, &x.id, None)
}

fn agent(x: &Session) -> String {
    let mut c = x.surface.chars();
    c.next().map(|f| f.to_uppercase().collect::<String>() + c.as_str()).unwrap_or_default()
}

pub fn status_line(s: &App, x: &Session, k: &Kit) -> (String, Color, bool) {
    if let Some(n) = need_of(s, x) {
        return (format!("{}: {}", n.kind.wanted(), ui::short_title(&n.title, 90)), ui::needs_color(k), false);
    }
    if let Some(m) = &x.moving {
        return (m.label(), k.c.text_accent, true);
    }
    let offset = |t: time::OffsetDateTime| crate::ui::session_ops::offset(t).whole_seconds();
    if let Some(note) = feed::snooze::note(x, time::OffsetDateTime::now_utc(), crate::ui::background::clock(), offset) {
        return (note, weak(k), false);
    }
    if busy(x) {
        let verb = if x.display.detail.is_empty() { "Working".to_string() } else { x.display.detail.clone() };
        return (verb, k.c.text_accent, true);
    }
    (s.ov.d.gist(x), base(k), false)
}

fn state_dot(s: &App, x: &Session, k: &Kit) -> El {
    let color = if need_of(s, x).is_some() {
        ui::needs_color(k)
    } else if busy(x) {
        k.c.text_accent
    } else if x.state(s.ov.d.now) == State::Done {
        k.c.success.icon
    } else {
        k.c.text_weak
    };
    el().size(10.0, 10.0).radius(5.0).fill(color).border(2.0, k.c.bg_base)
}

pub fn session_row(s: &App, x: &Session, depth: u8, selected: bool, k: &Kit) -> El {
    let id = x.id.clone();
    let menu_id = x.id.clone();
    let paged = s.ov.d.session(&x.id).is_none().then(|| x.clone());
    let menu_paged = paged.clone();
    let branch = x.checkout.branch.clone().unwrap_or_default();
    let mut place = vec![s.ov.d.role(x).to_string(), agent(x), x.host.name.clone()];
    if !branch.is_empty() {
        place.push(branch);
    }
    let (line, color, live) = status_line(s, x, k);
    let trailing = if busy(x) {
        row().gap(5.0).items_center().child(el().size(6.0, 6.0).radius(3.0).fill(k.c.text_accent)).child(text("live".to_string()).font(META).color(k.c.text_accent))
    } else if x.state(s.ov.d.now) == State::Done {
        row().gap(5.0).items_center().child(icon(&icons::CIRCLE_CHECK, 12.0).color(k.c.success.icon)).child(text(format!("done {}", s.ov.d.ago(&x.last_active))).font(META).color(weak(k)))
    } else {
        text(format!("idle {}", s.ov.d.ago(&x.last_active))).font(META).color(weak(k))
    };
    let mut status = row().gap(6.0).items_center();
    if live {
        status = status.child(Spinner::new().size(11.0).build(k));
    }
    status = status.child(text(line).font(META).color(color).truncate().shrink());
    let mut headline = row().w_full().gap(8.0).items_center().child(text(s.ov.d.title(x)).font(BODY_MED).color(strong(k)).truncate().shrink()).child(crate::ui::session_menu::marks(x));
    if let Some((h, _)) = feed::derive::needs::headline(x).filter(|_| need_of(s, x).is_some() || busy(x)) {
        headline = headline.child(text(ui::short_title(&crate::over::plain(&h), 60)).font(META).color(weak(k)).truncate().shrink());
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
        .on_press(move |s: &mut App| {
            if let Some(row) = &paged {
                crate::over::search::pin_read(s, row.clone());
            }
            crate::over::open_session(s, &id)
        })
        .on_secondary_press(move |s: &mut App, p, _: &mut rune::Tx| {
            if let Some(row) = &menu_paged {
                crate::over::search::pin_read(s, row.clone());
            }
            crate::ui::session_menu::open_at(s, &menu_id, p)
        })
        .child(col().size(28.0, 28.0).child(ui::avatar(&s.ov.d.title(x), 28.0)).child(state_dot(s, x, k).absolute(20.0, 20.0)))
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
    r = if selected { r.fill(k.c.surface_raised_hover).border(1.0, ui::edge()) } else { r.hover(k.c.surface_raised_hover) };
    r
}

const ALL_PROJECTS: &str = "All projects";
const ALL_STATUSES: &str = "All statuses";
const ALL_SURFACES: &str = "All surfaces";

fn checkout_line(c: &TreeCheckout, k: &Kit) -> El {
    let mut line = row().gap(6.0).items_center().pl(14.0).pt(6.0).pb(2.0).child(icon(&icons::BRANCH, 11.0).color(weak(k))).child(text(c.label.clone()).font(META.mono()).color(weak(k)).truncate().shrink());
    if c.dirty {
        line = line.child(el().size(5.0, 5.0).radius(2.5).fill(weak(k)).label("Uncommitted changes"));
    }
    if let Some(host) = &c.host {
        line = line.child(text(format!("on {host}")).font(META).color(weak(k)));
    }
    line.key(format!("checkout-{}", c.id))
}

fn project_group(s: &App, p: &TreeProject, k: &Kit) -> El {
    let expanded = s.ov.list.expanded.contains(&p.id);
    let keeping = s.selected.as_deref();
    let shown = p.shown(expanded, keeping);
    let rows: Vec<&Session> = shown.checkouts.iter().flat_map(|c| c.families.iter().flat_map(|f| f.rows.iter().map(|r| r.session))).collect();
    let working = rows.iter().filter(|x| busy(x)).count();
    let needs = rows.iter().filter(|x| need_of(s, x).is_some()).count();
    let mut head = row()
        .w_full()
        .gap(10.0)
        .items_center()
        .px(12.0)
        .pb(4.0)
        .child(ui::avatar(&p.name, 20.0))
        .child(text(p.name.clone()).font(HEAD).color(strong(k)))
        .child(Badge::new(p.sessions_label()).variant(BadgeVariant::Subtle).build(k));
    if needs > 0 {
        head = head.child(text(format!("{needs} need you")).font(META_MED).color(ui::needs_color(k)));
    }
    if working > 0 {
        head = head.child(text(format!("{working} working")).font(META_MED).color(k.c.text_accent));
    }
    head = head.child(ui::spacer());
    if s.ov.d.project(&p.id).is_some() {
        let key = p.id.clone();
        head = head.child(Button::new("Open project").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(move |s: &mut App| go(s, Screen::Project(key.clone()))).build(k));
    }
    let branches = p.shows_checkouts();
    let mut body = col().w_full().gap(2.0).padding(6.0);
    for c in &shown.checkouts {
        if branches {
            body = body.child(checkout_line(c, k));
        }
        for f in &c.families {
            for r in &f.rows {
                body = body.child(session_row(s, r.session, r.depth, false, k));
            }
        }
    }
    if let Some(more) = p.more_label(expanded, keeping) {
        let id = p.id.clone();
        body = body.child(
            Button::new(more)
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .on_press(move |s: &mut App| {
                    if !s.ov.list.expanded.remove(&id) {
                        s.ov.list.expanded.insert(id.clone());
                    }
                })
                .build(k)
                .key(format!("more-{}", p.id)),
        );
    }
    col().key(format!("group-{}", p.id)).w_full().gap(6.0).child(head).child(ui::card(body, k))
}

fn bar(s: &App, layer: &mut Vec<El>, k: &Kit) -> El {
    let view = s.ov.list.listing.as_ref().filter(|_| crate::over::listing::current(s)).map(|l| &l.view);
    let labels: Vec<String> = SCOPES.iter().map(|(label, scope)| view.and_then(|v| v.count(scope)).map_or(label.to_string(), |n| format!("{label} {n}"))).collect();
    let scopes = SegmentedControl::new(&labels, Some(s.ov.list.scope)).label("Show").fit().on_pick(|s: &mut App, i, _| s.ov.list.scope = i).build(k);

    let mut statuses: Vec<String> = vec![ALL_STATUSES.into()];
    statuses.extend(STATUSES.iter().map(|(_, label)| label.to_string()));
    let status = s.ov.list.status.as_deref().map_or(ALL_STATUSES.to_string(), status_label);
    let status = Select::new("status-filter", &statuses)
        .current(Some(&status))
        .open(s.ov.list.status_open)
        .on_toggle(|s: &mut App| s.ov.list.status_open = !s.ov.list.status_open)
        .on_close(|s: &mut App| s.ov.list.status_open = false)
        .on_choose(|s: &mut App, v: &str, _| {
            s.ov.list.status = STATUSES.iter().find(|(_, label)| *label == v).map(|(kind, _)| kind.to_string());
            s.ov.list.status_open = false;
        })
        .build(k, layer);

    let mut surfaces: Vec<String> = vec![ALL_SURFACES.into()];
    surfaces.extend(s.ov.list.surfaces().iter().map(|x| feed::projects::surface_label(x)));
    let surface = s.ov.list.surface.as_deref().map_or(ALL_SURFACES.to_string(), feed::projects::surface_label);
    let surface = Select::new("surface-filter", &surfaces)
        .current(Some(&surface))
        .open(s.ov.list.surface_open)
        .on_toggle(|s: &mut App| s.ov.list.surface_open = !s.ov.list.surface_open)
        .on_close(|s: &mut App| s.ov.list.surface_open = false)
        .on_choose(|s: &mut App, v: &str, _| {
            s.ov.list.surface = s.ov.list.surfaces().iter().find(|x| feed::projects::surface_label(x) == v).cloned();
            s.ov.list.surface_open = false;
        })
        .build(k, layer);

    let known: Vec<(String, String)> = s.ov.d.projects.iter().map(|p| (p.key.clone(), p.name.clone())).chain(s.ov.list.projects().iter().cloned()).collect();
    let choices = project_choices(&known);
    let mut names: Vec<String> = vec![ALL_PROJECTS.into()];
    names.extend(choices.iter().map(|(_, label)| label.clone()));
    let current = s.ov.sessions_project.as_deref().and_then(|key| choices.iter().find(|(id, _)| id == key)).map_or(ALL_PROJECTS.to_string(), |(_, label)| label.clone());
    let project = Select::new("project-filter", &names)
        .current(Some(&current))
        .open(s.ov.sessions_project_open)
        .on_toggle(|s: &mut App| s.ov.sessions_project_open = !s.ov.sessions_project_open)
        .on_close(|s: &mut App| s.ov.sessions_project_open = false)
        .on_choose(move |s: &mut App, v: &str, _| {
            s.ov.sessions_project = choices.iter().find(|(_, label)| label == v).map(|(id, _)| id.clone());
            s.ov.sessions_project_open = false;
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
        .border(1.0, ui::edge())
        .child(icon(&icons::MAGNIFYING_GLASS, 14.0).color(k.c.icon_base))
        .child(
            text_input(&s.ov.sessions_query, TextStyle::new(13.0, 440.0).lh(18.0))
                .single_line()
                .key("sessions-search")
                .label("Search sessions")
                .grow(1.0)
                .shrink()
                .color(strong(k))
                .placeholder("Search sessions".to_string(), weak(k))
                .on_edit(|s: &mut App| &mut s.ov.sessions_query),
        );
    let mut bar = row().w_full().gap(10.0).items_center().flex_wrap().child(scopes).child(ui::spacer()).child(status).child(surface);
    if s.ov.list.status.is_some() || s.ov.list.surface.is_some() {
        bar = bar.child(Button::new("Clear Filters").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut App| s.ov.list.clear_refinement()).build(k));
    }
    bar.child(project).child(search)
}

fn empty_note(s: &App) -> &'static str {
    if s.client.is_none() {
        "Connect to Agenthail to list sessions."
    } else if s.ov.list.loading() || !crate::over::listing::current(s) && s.ov.list.error.is_none() {
        "Loading…"
    } else if s.ov.list.error.is_some() {
        ""
    } else if SCOPES[s.ov.list.scope.min(SCOPES.len() - 1)].1 == "archived" {
        "No archived sessions."
    } else {
        "No sessions match."
    }
}

pub fn view(s: &App, cx: &mut Cx<App>, layer: &mut Vec<El>, main_kit: &Kit) -> El {
    let k = cx.kit();
    let mut list = col().w_full().gap(18.0).px(24.0).pt(16.0).pb(32.0).child(bar(s, layer, &k));
    let listing = s.ov.list.listing.as_ref().filter(|_| crate::over::listing::current(s));
    let projects = listing.map(|l| tree(&l.view, |id| s.ov.d.session(id).or_else(|| l.rows.get(id)))).unwrap_or_default();
    if projects.is_empty() {
        let note = empty_note(s);
        if !note.is_empty() {
            list = list.child(text(note.to_string()).font(META).color(weak(&k)).pt(20.0));
        }
    }
    for p in &projects {
        list = list.child(project_group(s, p, &k));
    }
    if let Some(error) = &s.ov.list.error {
        list = list.child(
            row()
                .w_full()
                .gap(10.0)
                .items_center()
                .child(text(error.clone()).font(META).color(ui::needs_color(&k)).wrap().shrink())
                .child(Button::new("Try again").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(crate::over::listing::retry).build(&k).key("sessions-retry")),
        );
    } else if s.ov.list.loading_more() {
        list = list.child(text("Loading…".to_string()).font(META).color(weak(&k)));
    } else if listing.is_some_and(|l| l.next.is_some()) {
        list = list.child(Button::new("Show more").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(crate::over::listing::more).build(&k).key("sessions-more"));
    }
    let main = col().key("sessions").w_full().h_full().scroll(ScrollSpec { stick_bottom: false }).child(list);
    let toolbar = Toolbar::new().leading(ui::crumb(&[("Sessions", true)], &k)).trailing(ui::needs_button(s, &k));
    ui::frame(cx, s, Some(ui::sidebar(s, None, None, &k)), main, None, toolbar, main_kit)
}
