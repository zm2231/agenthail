use rune::{Color, El, Key, KeyEvent, TextStyle, Tx, col, icon, row, text, text_input};
use rune_theme_opencode::icons;

use crate::data::{Target, plain, short_id};
use crate::ui::{self, BODY_MED, META, META_MED, base, kit, strong, weak};
use crate::{App, open_target};

pub struct Hit {
    pub group: &'static str,
    pub title: String,
    pub status: String,
    pub badge: String,
    pub avatar: String,
    pub target: Target,
}

pub fn hits(s: &App) -> Vec<Hit> {
    let q = s.jump.as_ref().map(|e| e.text.trim().to_lowercase()).unwrap_or_default();
    let has = |t: &str| q.is_empty() || t.to_lowercase().contains(&q);
    let mut out = Vec::new();
    let mut projects = 0;
    for p in &s.d.projects {
        if !has(&p.name) || projects >= 3 {
            continue;
        }
        projects += 1;
        let needs = s.needs.iter().filter(|n| n.project_key == p.key).count();
        let moving = p.sessions.iter().filter_map(|id| s.d.session(id)).filter(|x| matches!(x.display.kind.as_str(), "working" | "finishing" | "starting")).count();
        let mut status = Vec::new();
        if needs > 0 {
            status.push(format!("{needs} need you"));
        }
        status.push(if moving > 0 { format!("{moving} moving") } else { "quiet".into() });
        status.push(if p.lead.is_some() { format!("lead {}", p.lead_name) } else { "no lead yet".into() });
        out.push(Hit { group: "Projects", title: p.name.clone(), status: status.join(" · "), badge: s.d.ago(&p.last_active), avatar: p.name.clone(), target: Target::Project(p.key.clone()) });
    }
    let mut work: Vec<&crate::data::Work> = s.d.work.values().filter(|w| matches!(w.state.as_str(), "in_progress" | "in_review")).filter(|w| has(&w.text) || has(&w.id)).collect();
    work.sort_by(|a, b| s.d.phase(b).cmp(&s.d.phase(a)).then(b.timeline.last().map(|t| t.at.as_str()).cmp(&a.timeline.last().map(|t| t.at.as_str()))));
    for w in work.into_iter().take(3) {
        let phase = s.d.phase(w);
        let whose = if phase == 4 { "your move".to_string() } else { format!("{}'s move", ui::short_title(&s.d.whose_move(w), 24)) };
        let project = s.d.project(&w.project).map(|p| p.name.clone()).unwrap_or_default();
        out.push(Hit {
            group: "Work",
            title: plain(&w.text),
            status: format!("{} · {whose} · {project}", crate::data::PHASES[phase]),
            badge: short_id(&w.id),
            avatar: project,
            target: Target::Work(w.id.clone()),
        });
    }
    let mut sessions: Vec<&feed::catalog::Session> = s.hands_on.iter().filter_map(|id| s.d.session(id)).collect();
    let mut rest: Vec<&feed::catalog::Session> = s.d.sessions.iter().filter(|x| x.project.is_some() && !s.hands_on.contains(&x.id)).collect();
    rest.sort_by(|a, b| b.last_active.cmp(&a.last_active));
    sessions.extend(rest);
    for x in sessions.into_iter().filter(|x| has(&s.d.title(x)) || has(&x.project_name())).take(3) {
        let busy = matches!(x.display.kind.as_str(), "working" | "finishing" | "starting");
        out.push(Hit {
            group: "Sessions",
            title: s.d.title(x),
            status: {
                let gist = s.d.gist(x);
                let state = if busy { "Working" } else { "Idle" };
                if gist == x.display.label { state.to_string() } else { format!("{state} · {}", ui::short_title(&gist, 70)) }
            },
            badge: x.project_name(),
            avatar: x.project_name(),
            target: Target::Session(x.id.clone()),
        });
    }
    out
}

pub fn key(s: &mut App, ev: &KeyEvent, _tx: &mut Tx) -> bool {
    match ev.key {
        Key::Escape => {
            s.jump = None;
            true
        }
        Key::Down => {
            s.jump_ix = (s.jump_ix + 1).min(hits(s).len().saturating_sub(1));
            true
        }
        Key::Up => {
            s.jump_ix = s.jump_ix.saturating_sub(1);
            true
        }
        Key::Enter => {
            let found = hits(s);
            let ix = s.jump_ix.min(found.len().saturating_sub(1));
            if let Some(h) = found.into_iter().nth(ix) {
                open_target(s, &h.target);
            }
            true
        }
        _ => false,
    }
}

pub fn view(s: &App, layer: &mut Vec<El>) {
    let k = kit();
    let Some(edit) = &s.jump else { return };
    let input = text_input(edit, TextStyle::new(16.0, 450.0).lh(22.0))
        .single_line()
        .key("jump-input")
        .label("Jump to")
        .grow(1.0)
        .shrink()
        .color(strong())
        .placeholder("Jump to a project, work item or session".to_string(), weak())
        .on_edit(|s: &mut App| s.jump.as_mut().expect("jump open"))
        .on_input_key(|s: &mut App, ev: &KeyEvent, tx: &mut Tx| key(s, ev, tx));
    let search = row().w_full().gap(10.0).items_center().px(16.0).py(14.0).border_sides([0.0, 0.0, 1.0, 0.0], ui::rule()).child(icon(&icons::MAGNIFYING_GLASS, 18.0).color(k.c.icon_base)).child(input).child(ui::ghost_keys(&["esc"]));
    let mut list = col().w_full().gap(2.0).padding(8.0);
    let mut last = "";
    let found = hits(s);
    let ix = s.jump_ix.min(found.len().saturating_sub(1));
    for (i, h) in found.into_iter().enumerate() {
        if h.group != last {
            list = list.child(text(h.group.to_string()).font(META_MED).color(weak()).px(10.0).pt(if last.is_empty() { 4.0 } else { 12.0 }).pb(4.0));
            last = h.group;
        }
        let active = i == ix;
        let t = h.target.clone();
        let mut r = row()
            .key(format!("hit-{i}"))
            .w_full()
            .gap(12.0)
            .items_center()
            .px(10.0)
            .py(8.0)
            .radius(8.0)
            .on_press(move |s: &mut App| open_target(s, &t))
            .child(ui::avatar(&h.avatar, 22.0))
            .child(col().grow(1.0).shrink().min_w(0.0).gap(2.0).child(text(h.title.clone()).font(BODY_MED).color(strong()).truncate()).child(text(h.status.clone()).font(META).color(base()).truncate()))
            .child(text(h.badge.clone()).font(META).color(weak()));
        if active {
            r = r.fill(k.c.surface_raised_hover).child(icon(&icons::ENTER, 14.0).color(k.c.icon_base));
        } else {
            r = r.hover(k.c.surface_raised_hover);
        }
        list = list.child(r);
    }
    let foot = row()
        .w_full()
        .gap(14.0)
        .items_center()
        .px(16.0)
        .py(10.0)
        .border_sides([1.0, 0.0, 0.0, 0.0], ui::rule())
        .child(row().gap(6.0).items_center().child(ui::ghost_keys(&["↑", "↓"])).child(text("move".to_string()).font(META).color(weak())))
        .child(row().gap(6.0).items_center().child(ui::ghost_keys(&["⏎"])).child(text("open".to_string()).font(META).color(weak())))
        .child(ui::spacer())
        .child(text("Projects first, then work, then sessions".to_string()).font(META).color(weak()));
    let panel = col()
        .key("jump")
        .w(680.0)
        .radius(16.0)
        .fill(k.c.surface_raised)
        .border(1.0, k.c.border_base)
        .shadow(0.0, 24.0, Color::BLACK.alpha(0.55))
        .child(search)
        .child(list)
        .child(foot);
    layer.push(
        col()
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .items_center()
            .pt(84.0)
            .fill(Color::BLACK.alpha(0.45))
            .on_press(|s: &mut App| s.jump = None)
            .child(panel.on_press(|_: &mut App| {})),
    );
}
