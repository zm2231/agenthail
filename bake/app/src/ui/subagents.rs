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
    in_flight: bool,
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
    seq: u64,
    in_flight: bool,
}

#[derive(Default)]
pub struct Crew {
    pages: HashMap<String, Vec<Session>>,
    asked: HashMap<String, PageAsk>,
    seq: u64,
    errors: HashMap<String, String>,
    claude: HashMap<String, ClaudeSubagents>,
    claude_errors: HashMap<String, String>,
    claude_asked: HashMap<String, ClaudeAsk>,
    rows: HashMap<String, Session>,
    seen: feed::subagents::Seen,
    row_asked: HashMap<String, RowAsk>,
    expanded: HashSet<String>,
    recent: Vec<String>,
}

pub enum Done {
    Page(String, u64, Result<Vec<Session>, String>),
    Claude(String, u64, Result<ClaudeSubagents, String>),
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

fn lineage(s: &App, id: &str) -> Vec<String> {
    let mut line = vec![id.to_string()];
    while let Some(parent) = line.last().and_then(|at| parent_of(s, at)).filter(|p| !line.contains(p)) {
        line.push(parent);
    }
    line
}

fn held(s: &App) -> HashSet<String> {
    s.selected.as_deref().map(|sel| lineage(s, sel).into_iter().collect()).unwrap_or_default()
}

fn touch(s: &mut App, id: &str) {
    s.crew.recent.retain(|x| x != id);
    s.crew.recent.push(id.to_string());
    let held = held(s);
    while s.crew.recent.len() > KEPT {
        let Some(at) = s.crew.recent.iter().position(|x| !held.contains(x)) else { break };
        let old = s.crew.recent.remove(at);
        s.crew.pages.remove(&old);
        if !s.crew.asked.get(&old).is_some_and(|a| a.in_flight) {
            s.crew.asked.remove(&old);
        }
        s.crew.errors.remove(&old);
        s.crew.claude.remove(&old);
        s.crew.claude_errors.remove(&old);
        if !s.crew.claude_asked.get(&old).is_some_and(|a| a.in_flight) {
            s.crew.claude_asked.remove(&old);
        }
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
    if s.crew.claude_asked.get(id).is_some_and(|a| !a.in_flight) {
        s.crew.claude_asked.remove(id);
    }
    if s.crew.errors.remove(id).is_some() && !loading(s, id) {
        s.crew.asked.remove(id);
    }
    if let Some(parent) = parent_of(s, id) {
        s.crew.row_asked.retain(|p, a| *p != parent || a.in_flight);
    }
    refresh(s, id);
}

pub(crate) fn refresh(s: &mut App, id: &str) {
    if s.client.is_none() || s.screen != Screen::Session {
        return;
    }
    let line = lineage(s, id);
    for at in line.iter().rev() {
        touch(s, at);
    }
    let unresolved_subagent = session(s, id).is_none() && known(s, id).and_then(crew::parent_of).is_some();
    if (s.crew.rows.contains_key(id) || unresolved_subagent) && outside(s, id) {
        read_row(s, id);
    }
    for ancestor in &line[1..] {
        if outside(s, ancestor) {
            read_row(s, ancestor);
        }
    }
    for at in &line {
        page(s, at);
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

fn loading(s: &App, id: &str) -> bool {
    s.crew.asked.get(id).is_some_and(|a| a.in_flight)
}

fn page(s: &mut App, id: &str) {
    let Some(x) = known(s, id) else { return };
    if !crew::page_wanted(x, listed(s, id)) {
        if !loading(s, id) {
            keep_open_lineage(s, id, &[]);
            s.crew.pages.remove(id);
            s.crew.asked.remove(id);
            s.crew.errors.remove(id);
        }
        return;
    }
    let key = crew::page_key(x);
    let held = held(s);
    let holds_open = s.crew.pages.get(id).is_some_and(|rows| rows.iter().any(|x| held.contains(&x.id)));
    if s.crew.asked.get(id).is_some_and(|a| a.in_flight || !crew::page_due(a.key == key, a.at.elapsed(), holds_open)) {
        return;
    }
    s.crew.seq += 1;
    let seq = s.crew.seq;
    s.crew.asked.insert(id.to_string(), PageAsk { key, at: Instant::now(), seq, in_flight: true });
    let id = id.to_string();
    spawn(s, move |c| Done::Page(id.clone(), seq, c.subagent_sessions(&id)));
}

fn claude(s: &mut App, id: &str) {
    let Some(stamp) = known(s, id).filter(|x| x.surface == "claude").map(|x| x.last_active.clone()) else { return };
    if s.crew.claude_asked.get(id).is_some_and(|a| a.in_flight) {
        return;
    }
    let working = s.crew.claude.get(id).is_some_and(ClaudeSubagents::working);
    let asked = s.crew.claude_asked.get(id).map(|a| Asked { stamp: &a.stamp, since: a.at.elapsed(), failed: a.failed });
    if !crew::claude_due(asked, &stamp, working) {
        return;
    }
    s.crew.seq += 1;
    let seq = s.crew.seq;
    s.crew.claude_asked.insert(id.to_string(), ClaudeAsk { stamp, at: Instant::now(), failed: false, seq, in_flight: true });
    let id = id.to_string();
    spawn(s, move |c| Done::Claude(id.clone(), seq, c.claude_subagents(&id)));
}

pub(crate) fn done(s: &mut App, done: Done) {
    take(s, done);
    settle(s);
}

fn take(s: &mut App, done: Done) {
    match done {
        Done::Page(id, seq, result) => {
            let Some(asked) = s.crew.asked.get_mut(&id).filter(|a| a.seq == seq) else { return };
            asked.in_flight = false;
            if !s.crew.recent.contains(&id) {
                s.crew.asked.remove(&id);
                return;
            }
            match result {
                Ok(mut rows) => {
                    let seen = &mut s.crew.seen;
                    rows.retain(|x| seen.read(&x.id, seq, true) || !seen.absent(&x.id));
                    s.crew.errors.remove(&id);
                    keep_open_lineage(s, &id, &rows);
                    s.crew.pages.insert(id, rows);
                }
                Err(error) => {
                    s.crew.errors.insert(id, error);
                }
            }
        }
        Done::Claude(id, seq, result) => {
            let Some(asked) = s.crew.claude_asked.get_mut(&id).filter(|a| a.seq == seq) else { return };
            asked.in_flight = false;
            if !s.crew.recent.contains(&id) {
                s.crew.claude_asked.remove(&id);
                return;
            }
            match result {
                Ok(list) => {
                    s.crew.claude_errors.remove(&id);
                    s.crew.claude.insert(id, list);
                }
                Err(error) => {
                    asked.failed = true;
                    s.crew.claude.remove(&id);
                    s.crew.claude_errors.insert(id, error);
                }
            }
        }
        Done::Row(id, seq, result) => {
            let Some(asked) = s.crew.row_asked.get_mut(&id).filter(|a| a.seq == seq) else { return };
            asked.in_flight = false;
            asked.at = Instant::now();
            match result {
                RowRead::Found(row) => {
                    if s.crew.seen.read(&id, seq, true) {
                        s.crew.rows.insert(id, row);
                    }
                }
                RowRead::Gone => {
                    if s.crew.seen.read(&id, seq, false) {
                        evict(s, &id);
                    }
                }
                RowRead::Failed(_) => {}
            }
        }
    }
}

pub(crate) fn forget(s: &mut App, id: &str) {
    let asked = s.crew.seq;
    s.crew.seen.gone(id, asked);
    evict(s, id);
    settle(s);
}

fn evict(s: &mut App, id: &str) {
    s.crew.rows.remove(id);
    for rows in s.crew.pages.values_mut() {
        rows.retain(|x| x.id != id);
    }
}

fn settle(s: &mut App) {
    let pages = s.crew.asked.values().filter(|a| a.in_flight).map(|a| a.seq);
    let rows = s.crew.row_asked.values().filter(|a| a.in_flight).map(|a| a.seq);
    let oldest = pages.chain(rows).min();
    s.crew.seen.settle(oldest);
}

fn keep_open_lineage(s: &mut App, parent: &str, rows: &[Session]) {
    let Some(old) = s.crew.pages.get(parent) else { return };
    let held = held(s);
    let kept: Vec<Session> = old.iter().filter(|x| held.contains(&x.id) && !rows.iter().any(|r| r.id == x.id) && !listed(s, &x.id) && !s.crew.seen.absent(&x.id)).cloned().collect();
    for row in kept {
        s.crew.rows.insert(row.id.clone(), row);
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
    crew::in_daemon_order(&mut children, &s.sessions);
    let loading = loading(s, &x.id);
    let error = s.crew.errors.get(&x.id);
    let observed = s.crew.claude.get(&x.id).map(|c| c.list(s.crew.expanded.contains(&x.id)));
    let total = observed.as_ref().map_or(0, |o| o.total);
    let claude_error = s.crew.claude_errors.get(&x.id);
    if parent.is_none() && children.is_empty() && total == 0 && !loading && error.is_none() && claude_error.is_none() {
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
    if let Some(e) = claude_error {
        body = body.child(text(format!("Couldn't read Claude's subagents: {e}")).font(META).color(attention()).wrap().px(6.0).py(4.0));
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
