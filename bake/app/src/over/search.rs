use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::catalog::Session;
use feed::search::{Activity, Codex, Command, Filter, Page, codex_query, kind_label};
use rune::{Color, El, Key, KeyEvent, ScrollSpec, TextEdit, TextStyle, Tx, col, el, icon, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize, Select, Spinner};
use rune_theme_opencode::icons;

use crate::over::ui::{self, BODY, BODY_MED, META, kit, strong, weak};
use crate::{App, Screen};

const SETTLE: Duration = Duration::from_millis(250);
const RECHECK: Duration = Duration::from_secs(20);
const ALL_EVENTS: &str = "All events";

pub enum Got {
    Codex(String, Result<Codex, String>),
    History(u64, bool, Result<Page, String>),
    Looked(String, u64, Result<feed::gone::Gone, String>),
}

pub struct Panel {
    pub query: TextEdit,
    pub kind: String,
    pub kind_open: bool,
    pub list: Activity,
    pub expanded: HashSet<i64>,
    pub error: Option<String>,
    pub loading: bool,
    asked: Option<Filter>,
    typed: (Filter, Instant),
    generation: u64,
}

pub struct State {
    pub codex: Option<(String, Option<Result<Codex, String>>)>,
    pub pinned: Vec<Session>,
    pub panel: Option<Panel>,
    typed: Option<(String, Instant)>,
    requests: u64,
    checking: Option<(String, u64)>,
    checked: HashMap<String, Instant>,
    tx: Sender<Got>,
    rx: Receiver<Got>,
}

impl Default for State {
    fn default() -> State {
        let (tx, rx) = channel();
        State { codex: None, pinned: Vec::new(), panel: None, typed: None, requests: 0, checking: None, checked: HashMap::new(), tx, rx }
    }
}

pub fn pinned<'a>(s: &'a App, id: &str) -> Option<&'a Session> {
    s.ov.search.pinned.iter().find(|x| x.id == id)
}

pub fn open_codex(s: &mut App, row: Session) {
    let id = row.id.clone();
    let pins = &mut s.ov.search.pinned;
    pins.retain(|x| x.id != id);
    pins.push(row);
    s.ov.search.checked.remove(&id);
    crate::over::open_session(s, &id);
}

pub fn patch_pinned(s: &mut App, id: &str, patch: &feed::menu::Patch) {
    if let Some(x) = s.ov.search.pinned.iter_mut().find(|x| x.id == id) {
        patch.apply(x);
    }
}

fn unpin(s: &mut App, id: &str) {
    s.ov.search.pinned.retain(|x| x.id != id);
    s.ov.search.checked.remove(id);
}

fn recheck_pinned(s: &mut App) -> bool {
    let listed: HashSet<&str> = s.sessions.iter().map(|x| x.id.as_str()).collect();
    let before = s.ov.search.pinned.len();
    s.ov.search.pinned.retain(|x| !listed.contains(x.id.as_str()));
    let dropped = s.ov.search.pinned.len() != before;
    let Some(id) = selected(s) else { return dropped };
    let Some(row) = pinned(s, &id).cloned() else { return dropped };
    let st = &mut s.ov.search;
    if st.checking.is_some() || st.checked.get(&id).is_some_and(|at| at.elapsed() < RECHECK) {
        return dropped;
    }
    let Some(client) = s.client.clone() else { return dropped };
    let st = &mut s.ov.search;
    st.requests += 1;
    let seq = st.requests;
    st.checking = Some((id.clone(), seq));
    let tx = st.tx.clone();
    std::thread::spawn(move || {
        let result = client.look_up(&id, Some(&row));
        let _ = tx.send(Got::Looked(id, seq, result));
    });
    dropped
}

fn looked(s: &mut App, id: String, seq: u64, result: Result<feed::gone::Gone, String>) {
    if s.ov.search.checking.as_ref() != Some(&(id.clone(), seq)) {
        return;
    }
    s.ov.search.checking = None;
    match result {
        Ok(g) if matches!(g.why, feed::gone::Why::Unlisted | feed::gone::Why::Unchecked(_)) => {
            if let Some(x) = s.ov.search.pinned.iter_mut().find(|x| x.id == id) {
                *x = g.row;
            }
            s.ov.search.checked.insert(id, Instant::now());
        }
        _ => unpin(s, &id),
    }
}

fn jump_query(s: &App) -> Option<String> {
    s.ov.jump.as_ref().and_then(|e| codex_query(&e.text))
}

pub fn codex_for(s: &App) -> Option<&Option<Result<Codex, String>>> {
    let q = jump_query(s)?;
    match &s.ov.search.codex {
        Some((asked, found)) if *asked == q => Some(found),
        _ => Some(&None),
    }
}

fn ask_codex(s: &mut App, q: String) {
    s.ov.search.codex = Some((q.clone(), None));
    let Some(client) = s.client.clone() else {
        s.ov.search.codex = Some((q, Some(Err("Codex history search needs a running Agenthail.".into()))));
        return;
    };
    let tx = s.ov.search.tx.clone();
    std::thread::spawn(move || {
        let found = client.search_codex(&q);
        let _ = tx.send(Got::Codex(q, found));
    });
}

fn settle_codex(s: &mut App) -> bool {
    let Some(q) = jump_query(s) else {
        s.ov.search.typed = None;
        return false;
    };
    let st = &mut s.ov.search;
    match &st.typed {
        Some((typed, _)) if *typed == q => {}
        _ => {
            st.typed = Some((q, Instant::now()));
            return false;
        }
    }
    let due = st.typed.as_ref().is_some_and(|(_, at)| at.elapsed() >= SETTLE);
    let asked = st.codex.as_ref().is_some_and(|(a, _)| *a == q);
    if due && !asked {
        ask_codex(s, q);
        return true;
    }
    false
}

fn filter(p: &Panel) -> Filter {
    Filter::new(&p.kind, &p.query.text)
}

fn load(s: &mut App, reset: bool) {
    let client = s.client.clone();
    let tx = s.ov.search.tx.clone();
    s.ov.search.requests += 1;
    let generation = s.ov.search.requests;
    let Some(p) = s.ov.search.panel.as_mut() else { return };
    let f = filter(p);
    let reset = reset || p.asked.as_ref() != Some(&f);
    let before = if reset { 0 } else { p.list.next_before };
    p.generation = generation;
    p.asked = Some(f.clone());
    p.loading = true;
    let Some(client) = client else {
        p.loading = false;
        p.error = Some("Activity history needs a running Agenthail.".into());
        return;
    };
    std::thread::spawn(move || {
        let page = client.history(&f, before);
        let _ = tx.send(Got::History(generation, reset, page));
    });
}

fn settle_panel(s: &mut App) -> bool {
    let Some(p) = s.ov.search.panel.as_mut() else { return false };
    let f = filter(p);
    if p.typed.0 != f {
        p.typed = (f, Instant::now());
        return false;
    }
    if p.asked.as_ref() != Some(&f) && p.typed.1.elapsed() >= SETTLE {
        load(s, true);
        return true;
    }
    false
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok(got) = s.ov.search.rx.try_recv() {
        match got {
            Got::Codex(q, found) => {
                if let Some((_, slot)) = s.ov.search.codex.as_mut().filter(|(a, _)| *a == q) {
                    *slot = Some(found);
                    changed = true;
                }
            }
            Got::History(generation, reset, page) => {
                let Some(p) = s.ov.search.panel.as_mut().filter(|p| p.generation == generation) else { continue };
                p.loading = false;
                if p.asked.as_ref() != Some(&filter(p)) {
                    changed = true;
                    continue;
                }
                match page {
                    Ok(page) => {
                        p.list.take(page, reset);
                        p.error = None;
                    }
                    Err(e) => p.error = Some(e),
                }
                changed = true;
            }
            Got::Looked(id, seq, result) => {
                looked(s, id, seq, result);
                changed = true;
            }
        }
    }
    changed |= recheck_pinned(s);
    changed |= settle_codex(s);
    changed |= settle_panel(s);
    changed
}

pub fn open_activity(s: &mut App, tx: &mut Tx) {
    s.ov.jump = None;
    let mut query = TextEdit::new("");
    query.focused = true;
    s.composer.focused = false;
    let f = Filter::default();
    s.ov.search.panel = Some(Panel { query, kind: String::new(), kind_open: false, list: Activity::default(), expanded: HashSet::new(), error: None, loading: false, asked: None, typed: (f, Instant::now()), generation: 0 });
    load(s, true);
    tx.focus("activity-input", false);
}

fn close_activity(s: &mut App) {
    s.ov.search.panel = None;
}

pub fn activity_key(s: &mut App, ev: &KeyEvent) -> Option<bool> {
    let p = s.ov.search.panel.as_mut()?;
    if matches!(ev.key, Key::Escape) {
        if p.kind_open {
            p.kind_open = false;
        } else {
            close_activity(s);
        }
        return Some(true);
    }
    if matches!(ev.key, Key::Enter) && p.query.focused {
        load(s, true);
        return Some(true);
    }
    if ev.is_command('k') {
        close_activity(s);
        return None;
    }
    if ev.mods.cmd {
        return None;
    }
    Some(false)
}

pub fn selected(s: &App) -> Option<String> {
    (s.screen == Screen::Session).then(|| s.selected.clone()).flatten()
}

pub fn selected_title(s: &App, id: &str) -> String {
    crate::ui::gone::shown(s, id).map(|x| x.title()).unwrap_or_else(|| "this session".into())
}

pub fn can_stop(s: &App, id: &str) -> bool {
    crate::can_stop(s, id) && !s.stopping.contains(id)
}

pub fn run(s: &mut App, command: Command, tx: &mut Tx) {
    s.ov.jump = None;
    match command {
        Command::NewSession => {
            let prefill = crate::ui::new_session::prefill_for(s);
            crate::ui::new_session::open(s, prefill, tx);
        }
        Command::Stop => {
            if let Some(id) = selected(s).filter(|id| can_stop(s, id)) {
                crate::stop(s, &id);
            }
        }
        Command::Rename => {
            if let Some(id) = selected(s) {
                crate::ui::session_menu::run(s, &id, feed::menu::Act::Rename);
            }
        }
        Command::Refresh => refresh(s),
        Command::Activity => open_activity(s, tx),
    }
}

fn refresh(s: &mut App) {
    if let Some(client) = s.client.clone() {
        crate::fetch_overview(&client, &s.outbox);
        crate::over::inbox::refresh(&client, &s.ov.inbox);
    }
    match selected(s) {
        Some(id) => {
            crate::fetch_requests(s, &id);
            crate::ui::session_menu::load(s, &id);
            crate::ui::queue::fetch(s, &id);
        }
        None => crate::over::arrive(s),
    }
}

fn entry_row(s: &App, e: &feed::search::Entry) -> El {
    let k = kit();
    let failed = e.is_error();
    let open = s.ov.search.panel.as_ref().is_some_and(|p| p.expanded.contains(&e.id));
    let id = e.id;
    let dot = el().w(6.0).h(6.0).radius(3.0).fill(if failed { crate::theme::failed() } else { weak() });
    let head = row()
        .w_full()
        .gap(8.0)
        .items_center()
        .child(dot)
        .child(text(e.heading()).font(BODY_MED).color(strong()).truncate().shrink())
        .child(ui::spacer())
        .child(text(feed::catalog::ago(&e.created_at, s.now)).font(META).color(weak()));
    let toggle = row()
        .key(format!("activity-more-{id}"))
        .gap(4.0)
        .items_center()
        .on_press(move |s: &mut App| {
            if let Some(p) = s.ov.search.panel.as_mut()
                && !p.expanded.remove(&id)
            {
                p.expanded.insert(id);
            }
        })
        .child(icon(if open { &icons::CHEVRON_DOWN } else { &icons::CHEVRON_RIGHT }, 12.0).color(k.c.text_accent))
        .child(text(if open { "Hide details" } else { "View details" }.to_string()).font(META).color(k.c.text_accent));
    let mut c = col().key(format!("activity-{id}")).w_full().gap(4.0).px(12.0).py(10.0).radius(8.0).hover(k.c.surface_raised_hover).child(head).child(text(e.route()).font(META).color(weak()).truncate()).child(toggle);
    if open {
        c = c.child(text(e.content().to_string()).font(BODY).color(if failed { crate::theme::failed() } else { strong() }).wrap().pt(4.0));
    }
    c
}

pub fn activity_view(s: &App, layer: &mut Vec<El>) {
    let Some(p) = &s.ov.search.panel else { return };
    let k = kit();
    let input = text_input(&p.query, TextStyle::new(15.0, 450.0).lh(20.0))
        .single_line()
        .key("activity-input")
        .label("Search activity")
        .grow(1.0)
        .shrink()
        .color(strong())
        .placeholder("Search activity".to_string(), weak())
        .on_edit(|s: &mut App| &mut s.ov.search.panel.as_mut().expect("activity open").query)
        .on_input_key(|s: &mut App, ev: &KeyEvent, _: &mut Tx| activity_key(s, ev).unwrap_or(false));
    let mut pops = Vec::new();
    let mut labels = vec![ALL_EVENTS.to_string()];
    labels.extend(p.list.kinds.iter().map(|x| kind_label(x)));
    let current = if p.kind.is_empty() { ALL_EVENTS.to_string() } else { kind_label(&p.kind) };
    let kinds = Select::new("activity-kind", &labels)
        .current(Some(&current))
        .open(p.kind_open)
        .on_toggle(|s: &mut App| {
            if let Some(p) = s.ov.search.panel.as_mut() {
                p.kind_open = !p.kind_open;
            }
        })
        .on_close(|s: &mut App| {
            if let Some(p) = s.ov.search.panel.as_mut() {
                p.kind_open = false;
            }
        })
        .on_choose(|s: &mut App, v: &str, _| {
            let Some(p) = s.ov.search.panel.as_mut() else { return };
            p.kind_open = false;
            p.kind = p.list.kinds.iter().find(|k| kind_label(k) == v).cloned().unwrap_or_default();
            p.typed = (filter(p), Instant::now().checked_sub(SETTLE).unwrap_or_else(Instant::now));
        })
        .build(k, &mut pops);
    let search = row()
        .w_full()
        .gap(10.0)
        .items_center()
        .px(16.0)
        .py(12.0)
        .border_sides([0.0, 0.0, 1.0, 0.0], ui::rule())
        .child(icon(&icons::MAGNIFYING_GLASS, 16.0).color(k.c.icon_base))
        .child(input)
        .child(kinds)
        .child(ui::ghost_keys(&["esc"]));
    let mut list = col().w_full().gap(2.0).padding(8.0);
    if p.list.items.is_empty() && !p.loading && p.error.is_none() {
        list = list.child(text("No activity matches these filters.".to_string()).font(META).color(weak()).px(12.0).py(10.0));
    }
    for e in &p.list.items {
        list = list.child(entry_row(s, e));
    }
    let mut foot = row().w_full().gap(12.0).items_center().px(16.0).py(10.0).border_sides([1.0, 0.0, 0.0, 0.0], ui::rule());
    let shown = match p.list.items.len() {
        1 => "1 event shown".to_string(),
        n => format!("{n} events shown"),
    };
    foot = foot.child(text(shown).font(META).color(weak()));
    if let Some(e) = &p.error {
        foot = foot.child(text(e.clone()).font(META).color(crate::theme::failed()).wrap().shrink());
    }
    foot = foot.child(ui::spacer());
    if p.loading {
        foot = foot.child(Spinner::new().build(k));
    } else if p.list.has_more {
        foot = foot.child(Button::new("Load more").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).build(k).key("activity-more").on_press(|s: &mut App| load(s, false)));
    }
    let title = row().w_full().gap(10.0).items_center().px(16.0).pt(14.0).child(text("Activity history".to_string()).font(BODY_MED).color(strong())).child(ui::spacer());
    let panel = col()
        .key("activity")
        .w(720.0)
        .radius(16.0)
        .fill(k.c.surface_raised)
        .border(1.0, k.c.border_base)
        .shadow(0.0, 24.0, Color::BLACK.alpha(0.55))
        .child(title)
        .child(search)
        .child(col().key("activity-list").w_full().max_h(560.0).scroll(ScrollSpec { stick_bottom: false }).child(list))
        .child(foot);
    layer.push(
        col()
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .items_center()
            .pt(72.0)
            .fill(Color::BLACK.alpha(0.45))
            .on_press(close_activity)
            .child(panel.on_press(|_: &mut App| {})),
    );
    layer.extend(pops);
}
