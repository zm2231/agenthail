use rune_kit::Kit;
use rune::{Color, El, Icon, Key, KeyEvent, ScrollSpec, TextStyle, Tx, col, icon, row, text, text_input};
use rune_theme_opencode::icons;

use feed::catalog::Session;
use feed::search::{Command, Selected, commands, commands_lead, one_line};
use feed::worksearch::Found;

use crate::icons as ah;
use crate::over::search;
use crate::over::ui::{self, BODY_MED, META, META_MED, strong, weak};
use crate::over::{Target, WorkKey, open_target};
use crate::App;

#[derive(Clone)]
pub enum Pick {
    Target(Target),
    Found(WorkKey),
    Codex(Box<Session>),
    Command(Command),
}

enum Look {
    Avatar(String),
    Icon(&'static Icon),
}

struct Entry {
    group: &'static str,
    title: String,
    status: String,
    badge: String,
    look: Look,
    pick: Option<Pick>,
}

fn command_icon(c: Command) -> &'static Icon {
    match c {
        Command::NewSession => &icons::PLUS,
        Command::Stop => &ah::STOP,
        Command::Rename => &ah::PENCIL,
        Command::Window => &ah::LAYERS,
        Command::Inspector => &ah::PANEL_RIGHT,
        Command::Refresh => &ah::ROTATE,
        Command::Restart => &ah::ROTATE_CW,
        Command::Activity => &ah::ACTIVITY,
    }
}

fn entries(s: &App) -> Vec<Entry> {
    let q = query(s);
    let hits = s.ov.d.jump(q, &s.ov.needs, &s.ov.hands_on).into_iter().map(|h| Entry { group: h.group, title: h.title, status: h.status, badge: h.badge, look: Look::Avatar(h.avatar), pick: Some(Pick::Target(h.target)) });
    let selected = search::selected(s);
    let title = selected.as_deref().map(|id| search::selected_title(s, id)).unwrap_or_default();
    let current = selected.as_deref().map(|id| Selected { title: &title, can_stop: search::can_stop(s, id), inspector: s.ov.inspector.is_open() });
    let rows = commands(q, current);
    let lead = commands_lead(q, &rows);
    let cmds = rows.into_iter().map(|c| Entry { group: "Commands", title: c.title, status: String::new(), badge: c.shortcut.to_string(), look: Look::Icon(command_icon(c.command)), pick: Some(Pick::Command(c.command)) });
    let mut out: Vec<Entry> = if lead { cmds.chain(hits).collect() } else { hits.chain(cmds).collect() };
    work_found(s, &mut out, lead);
    if let Some(found) = search::codex_for(s) {
        let group = "Codex history";
        let note = |text: String| Entry { group, title: text, status: String::new(), badge: String::new(), look: Look::Icon(&ah::CLOCK), pick: None };
        match found {
            None => out.push(note("Searching older Codex threads…".into())),
            Some(Err(e)) => out.push(note(e.clone())),
            Some(Ok(codex)) => {
                let listed = |id: &str| out.iter().any(|e| matches!(&e.pick, Some(Pick::Target(Target::Session(x))) if x == id));
                let fresh: Vec<Entry> = codex
                    .fresh(listed)
                    .map(|h| {
                        let x = &h.session;
                        let place = x.cwd.as_deref().and_then(|c| c.rsplit('/').find(|p| !p.is_empty())).unwrap_or("Codex").to_string();
                        let status = if h.snippet.is_empty() { place } else { format!("{place} · {}", h.snippet) };
                        Entry { group, title: one_line(&x.title()), status, badge: feed::catalog::ago(&x.last_active, s.now), look: Look::Icon(&ah::CLOCK), pick: Some(Pick::Codex(Box::new(x.clone()))) }
                    })
                    .collect();
                let shown = fresh.len();
                out.extend(fresh);
                if let Some(n) = codex.note(shown) {
                    out.push(note(n));
                }
            }
        }
    }
    out
}

fn lists(e: &Entry, f: &Found) -> bool {
    match &e.pick {
        Some(Pick::Target(Target::Work(k)) | Pick::Found(k)) => *k == f.key(),
        _ => false,
    }
}

fn work_found(s: &App, out: &mut Vec<Entry>, lead: bool) {
    let Some(Some(found)) = crate::over::worksearch::found_for(s) else { return };
    let note = |text: String| Entry { group: "Work", title: text, status: String::new(), badge: String::new(), look: Look::Icon(&icons::TASK), pick: None };
    let rows: Vec<Entry> = match found {
        Err(e) => vec![note(e.clone())],
        Ok(search) => {
            for f in &search.items {
                let extra = s.ov.d.found_extra(f);
                if let Some(e) = out.iter_mut().find(|e| lists(e, f))
                    && !extra.is_empty()
                {
                    e.status = std::iter::once(e.status.clone()).filter(|t| !t.is_empty()).chain(extra).collect::<Vec<_>>().join(" · ");
                }
            }
            let listed = |f: &Found| out.iter().any(|e| lists(e, f));
            let mut rows: Vec<Entry> = s.ov.d.found_rows(search, listed).into_iter().map(|r| Entry { group: "Work", title: r.title, status: r.status, badge: r.badge, look: Look::Avatar(r.avatar), pick: Some(Pick::Found(r.key)) }).collect();
            rows.extend(search.note().map(note));
            rows
        }
    };
    let at = feed::worksearch::work_slot(out.iter().map(|e| e.group), lead);
    out.splice(at..at, rows);
}

#[cfg(test)]
pub fn shown(s: &App) -> Vec<(String, String)> {
    entries(s).into_iter().filter(|e| e.group == "Work").map(|e| (e.title, e.status)).collect()
}

fn query(s: &App) -> &str {
    s.ov.jump.as_ref().map(|e| e.text.as_str()).unwrap_or_default()
}

fn highlighted(s: &App) -> usize {
    let (at, ix) = &s.ov.jump_ix;
    if at == query(s) { *ix } else { 0 }
}

fn same(a: &Pick, b: &Pick) -> bool {
    match (a, b) {
        (Pick::Found(x) | Pick::Target(Target::Work(x)), Pick::Found(y) | Pick::Target(Target::Work(y))) => x == y,
        (Pick::Target(x), Pick::Target(y)) => x == y,
        (Pick::Codex(x), Pick::Codex(y)) => x.id == y.id,
        (Pick::Command(x), Pick::Command(y)) => x == y,
        _ => false,
    }
}

pub fn highlighted_pick(s: &App) -> Option<Pick> {
    s.ov.jump.as_ref()?;
    let picks: Vec<Pick> = entries(s).into_iter().filter_map(|e| e.pick).collect();
    let ix = highlighted(s).min(picks.len().checked_sub(1)?);
    picks.into_iter().nth(ix)
}

pub fn keep_highlight(s: &mut App, change: impl FnOnce(&mut App)) {
    let before = highlighted_pick(s);
    change(s);
    if let Some(p) = before
        && let Some(ix) = entries(s).into_iter().filter_map(|e| e.pick).position(|q| same(&p, &q))
    {
        s.ov.jump_ix = (query(s).to_string(), ix);
    }
}

fn highlight(s: &mut App, ix: usize, tx: &mut Tx) {
    s.ov.jump_ix = (query(s).to_string(), ix);
    tx.scroll_into_view_in("jump-list", format!("hit-{ix}"));
}

fn picks(s: &App) -> usize {
    entries(s).iter().filter(|e| e.pick.is_some()).count()
}

fn run(s: &mut App, pick: Pick, tx: &mut Tx) {
    match pick {
        Pick::Target(t) => open_target(s, &t),
        Pick::Found(key) => crate::over::worksearch::open(s, &key),
        Pick::Codex(x) => search::open_pinned(s, *x),
        Pick::Command(c) => search::run(s, c, tx),
    }
}

fn run_at(s: &mut App, ix: usize, tx: &mut Tx) {
    if let Some(pick) = entries(s).into_iter().filter_map(|e| e.pick).nth(ix) {
        run(s, pick, tx);
    }
}

pub fn key(s: &mut App, ev: &KeyEvent, tx: &mut Tx) -> bool {
    match ev.key {
        Key::Escape => {
            s.ov.jump = None;
            true
        }
        Key::Down => {
            let ix = (highlighted(s) + 1).min(picks(s).saturating_sub(1));
            highlight(s, ix, tx);
            true
        }
        Key::Up => {
            let ix = highlighted(s).saturating_sub(1);
            highlight(s, ix, tx);
            true
        }
        Key::Enter => {
            let ix = highlighted(s).min(picks(s).saturating_sub(1));
            run_at(s, ix, tx);
            true
        }
        _ => false,
    }
}

pub fn view(s: &App, layer: &mut Vec<El>, k: &Kit) {
    let Some(edit) = &s.ov.jump else { return };
    let input = text_input(edit, k.text(TextStyle::new(16.0, 450.0).lh(22.0)))
        .single_line()
        .key("jump-input")
        .label("Jump to")
        .grow(1.0)
        .shrink()
        .color(strong(k))
        .placeholder("Jump to a project, work item or session, or run a command".to_string(), weak(k))
        .on_edit(|s: &mut App| {
            if s.ov.jump_ix.0 != query(s) {
                s.ov.jump_ix = (query(s).to_string(), 0);
            }
            s.ov.jump.as_mut().expect("jump open")
        })
        .on_input_key(|s: &mut App, ev: &KeyEvent, tx: &mut Tx| key(s, ev, tx));
    let search = row().w_full().gap(10.0).items_center().px(16.0).py(14.0).border_sides([0.0, 0.0, 1.0, 0.0], ui::rule(k)).child(icon(&icons::MAGNIFYING_GLASS, k.text_size(18.0)).color(k.c.icon_base)).child(input).child(ui::ghost_keys(&["esc"], k));
    let mut list = col().w_full().gap(2.0).padding(8.0);
    let mut last = "";
    let found = entries(s);
    let ix = highlighted(s).min(found.iter().filter(|e| e.pick.is_some()).count().saturating_sub(1));
    let mut i = 0;
    for h in found {
        if h.group != last {
            list = list.child(text(h.group.to_string()).font(k.text(META_MED)).color(weak(k)).px(10.0).pt(if last.is_empty() { 4.0 } else { 12.0 }).pb(4.0));
            last = h.group;
        }
        let mark = |look: &Look| match look {
            Look::Avatar(name) => ui::avatar(name, k.text_size(22.0)),
            Look::Icon(glyph) => row().w(k.text_size(22.0)).h(k.text_size(22.0)).items_center().justify_center().child(icon(glyph, k.text_size(15.0)).color(k.c.icon_base)),
        };
        let look = match &h.look {
            Look::Avatar(name) => (name.as_str(), 0),
            Look::Icon(glyph) => ("", *glyph as *const Icon as usize),
        };
        if h.pick.is_none() {
            list = list.child(row().w_full().gap(12.0).items_center().px(10.0).py(6.0).child(mark(&h.look)).child(text(h.title).font(k.text(META)).color(weak(k)).wrap().shrink()));
            continue;
        }
        let active = i == ix;
        let at = i;
        i += 1;
        let shows = (at, active, &h.title, &h.status, &h.badge, look);
        list = list.child(crate::over::rows::memo(format!("hit-{at}"), shows, k, || {
            let mut body = col().grow(1.0).shrink().min_w(0.0).gap(2.0).child(text(h.title.clone()).font(k.text(BODY_MED)).color(strong(k)).truncate());
            if !h.status.is_empty() {
                body = body.child(text(h.status.clone()).font(k.text(META)).color(weak(k)).truncate());
            }
            let r = row()
                .key(format!("hit-{at}"))
                .w_full()
                .gap(12.0)
                .items_center()
                .px(10.0)
                .py(8.0)
                .radius(8.0)
                .on_press_tx(move |s: &mut App, tx: &mut Tx| run_at(s, at, tx))
                .child(mark(&h.look))
                .child(body)
                .child(text(h.badge.clone()).font(k.text(META)).color(weak(k)));
            if active { r.fill(k.c.surface_raised_hover).child(icon(&icons::ENTER, k.text_size(14.0)).color(k.c.icon_base)) } else { r.hover(k.c.surface_raised_hover) }
        }));
    }
    let foot = row()
        .w_full()
        .gap(14.0)
        .items_center()
        .px(16.0)
        .py(10.0)
        .border_sides([1.0, 0.0, 0.0, 0.0], ui::rule(k))
        .child(row().gap(6.0).items_center().child(ui::ghost_keys(&["↑", "↓"], k)).child(text("move".to_string()).font(k.text(META)).color(weak(k))))
        .child(row().gap(6.0).items_center().child(ui::ghost_keys(&["⏎"], k)).child(text("open".to_string()).font(k.text(META)).color(weak(k))))
        .child(ui::spacer())
        .child(text("Projects, work, sessions and commands, then older Codex threads".to_string()).font(k.text(META)).color(weak(k)));
    let panel = col()
        .key("jump")
        .w(680.0)
        .radius(16.0)
        .fill(k.c.surface_raised)
        .border(1.0, k.c.border_base)
        .shadow(0.0, 24.0, Color::BLACK.alpha(0.55))
        .child(search)
        .child(col().key("jump-list").w_full().max_h(520.0).scroll(ScrollSpec { stick_bottom: false }).child(list))
        .child(foot);
    layer.push(
        col()
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .items_center()
            .pt(84.0)
            .fill(Color::BLACK.alpha(0.45))
            .on_press(|s: &mut App| s.ov.jump = None)
            .child(panel.on_press(|_: &mut App| {})),
    );
}
