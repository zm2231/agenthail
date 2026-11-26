use std::collections::{HashMap, HashSet};
use std::time::{Duration, Instant};

use super::*;
use feed::subagents::{self as crew, Asked, ClaudeSubagent, ClaudeSubagents, RowRead};
use rune_kit::ControlSize;

const KEPT: usize = 6;
const ROW_EVERY: Duration = Duration::from_secs(15);

struct PageAsk {
    key: String,
    at: Instant,
    seq: u64,
}

struct RowAsk {
    at: Instant,
    seq: u64,
    in_flight: bool,
}

struct ClaudeAsk {
    stamp: String,
    at: Instant,
    failed: bool,
}

#[derive(Default)]
pub struct Crew {
    pages: HashMap<String, Vec<Session>>,
    asked: HashMap<String, PageAsk>,
    seq: u64,
    loading: HashSet<String>,
    errors: HashMap<String, String>,
    claude: HashMap<String, ClaudeSubagents>,
    claude_asked: HashMap<String, ClaudeAsk>,
    claude_loading: HashSet<String>,
    rows: HashMap<String, Session>,
    row_asked: HashMap<String, RowAsk>,
    expanded: HashSet<String>,
    recent: Vec<String>,
}

pub enum Done {
    Page(String, u64, Result<Vec<Session>, String>),
    Claude(String, Result<ClaudeSubagents, String>),
    Row(String, u64, RowRead),
}

pub(crate) fn fetched<'a>(s: &'a App, id: &str) -> Option<&'a Session> {
    s.crew.pages.values().flatten().find(|x| x.id == id).or_else(|| s.crew.rows.get(id))
}

fn spawn(s: &App, job: impl FnOnce(&feed::catalog::Client) -> Done + Send + 'static) {
    let Some(client) = s.client.clone() else { return };
    let out = s.outbox.clone();
    std::thread::spawn(move || {
        let _ = out.send(Bg::Crew(job(&client)));
    });
}

fn held(s: &App) -> HashSet<String> {
    let Some(sel) = s.selected.clone() else { return HashSet::new() };
    let mut held = HashSet::from([sel.clone()]);
    let mut at = sel;
    while let Some(parent) = parent_of(s, &at).filter(|p| !held.contains(p)) {
        held.insert(parent.clone());
        at = parent;
    }
    held
}

fn touch(s: &mut App, id: &str) {
    s.crew.recent.retain(|x| x != id);
    s.crew.recent.push(id.to_string());
    let held = held(s);
    while s.crew.recent.len() > KEPT {
        let Some(at) = s.crew.recent.iter().position(|x| !held.contains(x)) else { break };
        let old = s.crew.recent.remove(at);
        s.crew.pages.remove(&old);
        s.crew.asked.remove(&old);
        s.crew.errors.remove(&old);
        s.crew.loading.remove(&old);
        s.crew.claude.remove(&old);
        s.crew.claude_asked.remove(&old);
        s.crew.expanded.remove(&old);
    }
    if s.crew.rows.len() > KEPT {
        s.crew.rows.retain(|id, _| held.contains(id));
    }
    if s.crew.row_asked.len() > KEPT {
        s.crew.row_asked.retain(|id, a| a.in_flight || held.contains(id));
    }
}

pub(crate) fn load(s: &mut App, id: &str) {
    s.crew.claude_asked.remove(id);
    if s.crew.errors.remove(id).is_some() {
        s.crew.asked.remove(id);
    }
    if let Some(parent) = parent_of(s, id) {
        s.crew.row_asked.retain(|p, a| *p != parent || a.in_flight);
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
    if s.crew.rows.contains_key(id) && outside(s, id) {
        read_row(s, id);
    }
    if let Some(parent) = parent {
        if outside(s, &parent) {
            read_row(s, &parent);
        }
        page(s, &parent);
    }
    claude(s, id);
}

fn outside(s: &App, id: &str) -> bool {
    !listed(s, id) && !s.crew.pages.values().flatten().any(|x| x.id == id)
}

fn known<'a>(s: &'a App, id: &str) -> Option<&'a Session> {
    super::gone::shown(s, id)
}

fn listed(s: &App, id: &str) -> bool {
    s.sessions.iter().any(|x| x.id == id)
}

fn parent_of(s: &App, id: &str) -> Option<String> {
    known(s, id).and_then(crew::parent_of)
}

fn read_row(s: &mut App, id: &str) {
    if s.crew.row_asked.get(id).is_some_and(|a| a.in_flight || a.at.elapsed() < ROW_EVERY) {
        return;
    }
    s.crew.seq += 1;
    let seq = s.crew.seq;
    s.crew.row_asked.insert(id.to_string(), RowAsk { at: Instant::now(), seq, in_flight: true });
    let id = id.to_string();
    spawn(s, move |c| Done::Row(id.clone(), seq, c.session_row(&id)));
}

fn page(s: &mut App, id: &str) {
    let Some(x) = known(s, id) else { return };
    if !crew::page_wanted(x, listed(s, id)) {
        if !s.crew.loading.contains(id) {
            keep_selected(s, id, &[]);
            s.crew.pages.remove(id);
            s.crew.asked.remove(id);
            s.crew.errors.remove(id);
        }
        return;
    }
    let key = crew::page_key(x);
    if s.crew.loading.contains(id) || s.crew.asked.get(id).is_some_and(|a| !crew::page_due(a.key == key, a.at.elapsed())) {
        return;
    }
    s.crew.seq += 1;
    let seq = s.crew.seq;
    s.crew.asked.insert(id.to_string(), PageAsk { key, at: Instant::now(), seq });
    s.crew.loading.insert(id.to_string());
    let id = id.to_string();
    spawn(s, move |c| Done::Page(id.clone(), seq, c.subagent_sessions(&id)));
}

fn claude(s: &mut App, id: &str) {
    let Some(stamp) = known(s, id).filter(|x| x.surface == "claude").map(|x| x.last_active.clone()) else { return };
    if s.crew.claude_loading.contains(id) {
        return;
    }
    let working = s.crew.claude.get(id).is_some_and(ClaudeSubagents::working);
    let asked = s.crew.claude_asked.get(id).map(|a| Asked { stamp: &a.stamp, since: a.at.elapsed(), failed: a.failed });
    if !crew::claude_due(asked, &stamp, working) {
        return;
    }
    s.crew.claude_asked.insert(id.to_string(), ClaudeAsk { stamp, at: Instant::now(), failed: false });
    s.crew.claude_loading.insert(id.to_string());
    let id = id.to_string();
    spawn(s, move |c| Done::Claude(id.clone(), c.claude_subagents(&id)));
}

pub(crate) fn done(s: &mut App, done: Done) {
    match done {
        Done::Page(id, seq, result) => {
            if s.crew.asked.get(&id).is_none_or(|a| a.seq != seq) {
                return;
            }
            s.crew.loading.remove(&id);
            match result {
                Ok(rows) => {
                    s.crew.errors.remove(&id);
                    keep_selected(s, &id, &rows);
                    s.crew.pages.insert(id, rows);
                }
                Err(error) => {
                    s.crew.errors.insert(id, error);
                }
            }
        }
        Done::Claude(id, result) => {
            s.crew.claude_loading.remove(&id);
            if !s.crew.recent.contains(&id) {
                return;
            }
            match result {
                Ok(list) => {
                    s.crew.claude.insert(id, list);
                }
                Err(_) => {
                    if let Some(a) = s.crew.claude_asked.get_mut(&id) {
                        a.failed = true;
                    }
                }
            }
        }
        Done::Row(id, seq, result) => {
            let Some(asked) = s.crew.row_asked.get_mut(&id).filter(|a| a.seq == seq) else { return };
            asked.in_flight = false;
            asked.at = Instant::now();
            match result {
                RowRead::Found(row) => {
                    s.crew.rows.insert(id, row);
                }
                RowRead::Gone => {
                    s.crew.rows.remove(&id);
                }
                RowRead::Failed(_) => {}
            }
        }
    }
}

fn keep_selected(s: &mut App, parent: &str, rows: &[Session]) {
    let Some(sel) = s.selected.clone() else { return };
    if rows.iter().any(|x| x.id == sel) || s.sessions.iter().any(|x| x.id == sel) {
        return;
    }
    if let Some(row) = s.crew.pages.get(parent).and_then(|old| old.iter().find(|x| x.id == sel)).cloned() {
        s.crew.rows.insert(sel, row);
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
