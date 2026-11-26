use std::collections::{HashMap, HashSet};
use std::time::{Duration, Instant};

use super::*;
use feed::subagents::{self as crew, ClaudeSubagent, ClaudeSubagents};
use rune_kit::ControlSize;

const KEPT: usize = 6;
const CLAUDE_EVERY: Duration = Duration::from_secs(15);

#[derive(Default)]
pub struct Crew {
    pages: HashMap<String, Vec<Session>>,
    keys: HashMap<String, String>,
    loading: HashSet<String>,
    errors: HashMap<String, String>,
    claude: HashMap<String, ClaudeSubagents>,
    claude_asked: HashMap<String, (String, Instant)>,
    claude_loading: HashSet<String>,
    expanded: HashSet<String>,
    recent: Vec<String>,
}

pub enum Done {
    Page(String, String, Result<Vec<Session>, String>),
    Claude(String, Result<ClaudeSubagents, String>),
}

pub(crate) fn fetched<'a>(s: &'a App, id: &str) -> Option<&'a Session> {
    s.crew.pages.values().flatten().find(|x| x.id == id)
}

fn spawn(s: &App, job: impl FnOnce(&feed::catalog::Client) -> Done + Send + 'static) {
    let Some(client) = s.client.clone() else { return };
    let out = s.outbox.clone();
    std::thread::spawn(move || {
        let _ = out.send(Bg::Crew(job(&client)));
    });
}

fn touch(s: &mut App, id: &str) {
    s.crew.recent.retain(|x| x != id);
    s.crew.recent.push(id.to_string());
    let held: HashSet<String> = s.selected.iter().flat_map(|sel| std::iter::once(sel.clone()).chain(parent_of(s, sel))).collect();
    while s.crew.recent.len() > KEPT {
        let Some(at) = s.crew.recent.iter().position(|x| !held.contains(x)) else { break };
        let old = s.crew.recent.remove(at);
        s.crew.pages.remove(&old);
        s.crew.keys.remove(&old);
        s.crew.errors.remove(&old);
        s.crew.loading.remove(&old);
        s.crew.claude.remove(&old);
        s.crew.claude_asked.remove(&old);
        s.crew.expanded.remove(&old);
    }
}

pub(crate) fn load(s: &mut App, id: &str) {
    s.crew.claude_asked.remove(id);
    if s.crew.errors.remove(id).is_some() {
        s.crew.keys.remove(id);
    }
    refresh(s, id);
}

pub(crate) fn refresh(s: &mut App, id: &str) {
    if s.client.is_none() {
        return;
    }
    let parent = parent_of(s, id);
    if let Some(parent) = &parent {
        touch(s, parent);
    }
    touch(s, id);
    page(s, id);
    if let Some(parent) = parent {
        page(s, &parent);
    }
    claude(s, id);
}

fn parent_of(s: &App, id: &str) -> Option<String> {
    session(s, id).and_then(crew::parent_of).or_else(|| match &s.missing {
        Some(super::gone::Missing { id: m, look: super::gone::Look::Found(g), .. }) if m == id => crew::parent_of(&g.row),
        _ => None,
    })
}

fn page(s: &mut App, id: &str) {
    let Some(x) = session(s, id) else { return };
    if !crew::wants_page(x) {
        if !s.crew.loading.contains(id) && fetched_parent_of_selected(s) != Some(id) {
            s.crew.pages.remove(id);
            s.crew.keys.remove(id);
            s.crew.errors.remove(id);
        }
        return;
    }
    let key = crew::page_key(x);
    if s.crew.keys.get(id) == Some(&key) {
        return;
    }
    s.crew.keys.insert(id.to_string(), key.clone());
    s.crew.loading.insert(id.to_string());
    let id = id.to_string();
    spawn(s, move |c| Done::Page(id.clone(), key, c.subagent_sessions(&id)));
}

fn fetched_parent_of_selected(s: &App) -> Option<&str> {
    let sel = s.selected.as_deref()?;
    if s.sessions.iter().any(|x| x.id == sel) {
        return None;
    }
    s.crew.pages.iter().find(|(_, rows)| rows.iter().any(|x| x.id == sel)).map(|(p, _)| p.as_str())
}

fn claude(s: &mut App, id: &str) {
    let Some(stamp) = session(s, id).map(|x| x.last_active.clone()) else { return };
    if s.crew.claude_loading.contains(id) || s.crew.claude_asked.get(id).is_some_and(|(seen, at)| *seen == stamp || at.elapsed() < CLAUDE_EVERY) {
        return;
    }
    s.crew.claude_asked.insert(id.to_string(), (stamp, Instant::now()));
    s.crew.claude_loading.insert(id.to_string());
    let id = id.to_string();
    spawn(s, move |c| Done::Claude(id.clone(), c.claude_subagents(&id).map_err(|e| e.to_string())));
}

pub(crate) fn done(s: &mut App, done: Done) {
    match done {
        Done::Page(id, key, result) => {
            if s.crew.keys.get(&id) != Some(&key) {
                return;
            }
            s.crew.loading.remove(&id);
            if !s.crew.recent.contains(&id) {
                return;
            }
            match result {
                Ok(rows) => {
                    s.crew.errors.remove(&id);
                    s.crew.pages.insert(id, rows);
                }
                Err(error) => {
                    s.crew.errors.insert(id, error);
                }
            }
        }
        Done::Claude(id, result) => {
            s.crew.claude_loading.remove(&id);
            if let (Ok(list), true) = (result, s.crew.recent.contains(&id)) {
                s.crew.claude.insert(id, list);
            }
        }
    }
}

fn child_row(s: &App, c: &Session) -> El {
    let state = c.state(s.now);
    let id = c.id.clone();
    row()
        .key(format!("subagent-{}", c.id))
        .w_full()
        .gap(8.0)
        .items_center()
        .px(6.0)
        .py(5.0)
        .radius(7.0)
        .hover(Color::WHITE.alpha(0.06))
        .help(state.label())
        .on_press(move |s: &mut App| crate::select(s, &id))
        .child(el().size(7.0, 7.0).radius(3.5).fill(state_color(state)))
        .child(text(crew::label(c)).font(META_BOLD).color(theme::text()).truncate().grow(1.0).min_w(0.0))
        .child(text(catalog::ago(&c.last_active, s.now)).font(META).color(faint()))
}

fn claude_row(s: &App, link: &ClaudeSubagent) -> El {
    let mut top = row()
        .w_full()
        .gap(8.0)
        .child(el().size(7.0, 7.0).radius(3.5).mt(5.0).fill(if link.working { accent() } else { faint() }))
        .child(text(link.title()).font(META).color(theme::text()).wrap().max_lines(2).grow(1.0).min_w(0.0));
    if !link.last_active.is_empty() {
        top = top.child(text(catalog::ago(&link.last_active, s.now)).font(META).color(faint()));
    }
    let mut c = col().key(format!("claude-subagent-{}", link.key())).w_full().gap(1.0).px(6.0).py(4.0).help(if link.working { "Working" } else { "Idle" }).child(top);
    if let Some(kind) = link.kind_label() {
        c = c.child(text(kind.to_string()).font(META).color(faint()).pl(15.0));
    }
    c
}

pub(crate) fn section(s: &App, x: &Session) -> Option<El> {
    let parent = crew::parent_of(x).and_then(|p| session(s, &p));
    let mut children = crew::children(&x.id, &s.sessions, s.crew.pages.get(&x.id).map(Vec::as_slice).unwrap_or_default());
    children.sort_by(|a, b| b.last_active.cmp(&a.last_active));
    let loading = s.crew.loading.contains(&x.id);
    let error = s.crew.errors.get(&x.id);
    let observed = s.crew.claude.get(&x.id).map(|c| c.list(s.crew.expanded.contains(&x.id)));
    let total = observed.as_ref().map_or(0, |o| o.total);
    if parent.is_none() && children.is_empty() && total == 0 && !loading && error.is_none() {
        return None;
    }
    let mut body = col().w_full().gap(2.0);
    if let Some(p) = parent {
        let id = p.id.clone();
        body = body.child(
            row()
                .key("subagent-parent")
                .w_full()
                .gap(8.0)
                .items_center()
                .px(6.0)
                .py(5.0)
                .radius(7.0)
                .hover(Color::WHITE.alpha(0.06))
                .on_press(move |s: &mut App| crate::select(s, &id))
                .child(icon(&icons::CORNER, 12.0).color(accent()))
                .child(text(format!("Spawned by {}", p.title())).font(META_BOLD).color(accent()).truncate().min_w(0.0)),
        );
    }
    body = body.children(children.iter().map(|c| child_row(s, c)));
    if loading || error.is_some() {
        let note = error.map_or_else(|| "Loading subagents".to_string(), |e| format!("Couldn't load subagents: {e}"));
        body = body.child(text(note).font(META).color(if error.is_some() { attention() } else { faint() }).wrap().px(6.0).py(4.0));
    }
    if let Some(o) = observed.filter(|o| o.total > 0) {
        body = body.child(text(o.summary()).font(META).color(faint()).px(6.0).pt(if children.is_empty() && parent.is_none() { 0.0 } else { 8.0 }).pb(2.0));
        body = body.children(o.shown.iter().map(|l| claude_row(s, l)));
        if o.earlier > 0 {
            let id = x.id.clone();
            body = body.child(
                Button::new(o.earlier_label())
                    .variant(ButtonVariant::Ghost)
                    .control_size(ControlSize::Small)
                    .on_press(move |s: &mut App| {
                        s.crew.expanded.insert(id.clone());
                    })
                    .build(theme::kit())
                    .key("claude-subagents-earlier"),
            );
        }
    }
    Some(rail_section("Subagents", &icons::USERS, body))
}
