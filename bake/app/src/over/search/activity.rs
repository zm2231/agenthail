use std::collections::HashSet;
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Instant;

use feed::search::{Activity, Filter, Page, kind_label};
use rune::{Color, El, Key, KeyEvent, ScrollSpec, TextEdit, TextStyle, Tx, col, el, icon, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize, Select, Spinner};
use rune_theme_opencode::icons;

use super::SETTLE;
use crate::App;
use crate::over::ui::{self, BODY, BODY_MED, META, kit, strong, weak};

const ALL_EVENTS: &str = "All events";

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
    pub panel: Option<Panel>,
    requests: u64,
    tx: Sender<(u64, bool, Result<Page, String>)>,
    rx: Receiver<(u64, bool, Result<Page, String>)>,
}

impl Default for State {
    fn default() -> State {
        let (tx, rx) = channel();
        State { panel: None, requests: 0, tx, rx }
    }
}

fn filter(p: &Panel) -> Filter {
    Filter::new(&p.kind, &p.query.text)
}

fn load(s: &mut App, reset: bool) {
    let client = s.client.clone();
    let st = &mut s.ov.search.activity;
    let tx = st.tx.clone();
    st.requests += 1;
    let generation = st.requests;
    let Some(p) = st.panel.as_mut() else { return };
    let f = filter(p);
    let reset = reset || p.asked.as_ref() != Some(&f);
    let before = if reset { 0 } else { p.list.next_before };
    p.generation = generation;
    p.asked = Some(f.clone());
    p.loading = true;
    p.error = None;
    if reset {
        p.list = Activity { kinds: std::mem::take(&mut p.list.kinds), ..Activity::default() };
        p.expanded.clear();
    }
    let Some(client) = client else {
        p.loading = false;
        p.error = Some("Activity history needs a running Agenthail.".into());
        return;
    };
    std::thread::spawn(move || {
        let page = client.history(&f, before);
        let _ = tx.send((generation, reset, page));
    });
}

fn invalidate(p: &mut Panel) {
    p.generation = 0;
    p.asked = None;
    p.loading = false;
    p.error = None;
}

fn edited(s: &mut App) -> &mut TextEdit {
    let p = s.ov.search.activity.panel.as_mut().expect("activity open");
    invalidate(p);
    &mut p.query
}

fn pending(p: &Panel) -> bool {
    p.asked.as_ref() != Some(&filter(p))
}

fn settle(s: &mut App) -> bool {
    let Some(p) = s.ov.search.activity.panel.as_mut() else { return false };
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
    while let Ok((generation, reset, page)) = s.ov.search.activity.rx.try_recv() {
        let Some(p) = s.ov.search.activity.panel.as_mut().filter(|p| p.generation == generation) else { continue };
        changed = true;
        p.loading = false;
        if p.asked.as_ref() != Some(&filter(p)) {
            continue;
        }
        match page {
            Ok(page) => {
                p.list.take(page, reset);
                p.error = None;
            }
            Err(e) => p.error = Some(e),
        }
    }
    changed | settle(s)
}

pub fn open(s: &mut App, tx: &mut Tx) {
    s.ov.jump = None;
    let mut query = TextEdit::new("");
    query.focused = true;
    s.composer.focused = false;
    let f = Filter::default();
    s.ov.search.activity.panel = Some(Panel { query, kind: String::new(), kind_open: false, list: Activity::default(), expanded: HashSet::new(), error: None, loading: false, asked: None, typed: (f, Instant::now()), generation: 0 });
    load(s, true);
    tx.focus("activity-input", false);
}

fn close(s: &mut App) {
    s.ov.search.activity.panel = None;
}

pub fn key(s: &mut App, ev: &KeyEvent) -> Option<bool> {
    let p = s.ov.search.activity.panel.as_mut()?;
    if matches!(ev.key, Key::Escape) {
        if p.kind_open {
            p.kind_open = false;
        } else {
            close(s);
        }
        return Some(true);
    }
    if matches!(ev.key, Key::Enter) && p.query.focused {
        load(s, true);
        return Some(true);
    }
    if ev.is_command('k') {
        close(s);
        return None;
    }
    if ev.mods.cmd {
        return None;
    }
    Some(false)
}

fn entry_row(s: &App, e: &feed::search::Entry) -> El {
    let k = kit();
    let failed = e.is_error();
    let open = s.ov.search.activity.panel.as_ref().is_some_and(|p| p.expanded.contains(&e.id));
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
            if let Some(p) = s.ov.search.activity.panel.as_mut()
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

pub fn view(s: &App, layer: &mut Vec<El>) {
    let Some(p) = &s.ov.search.activity.panel else { return };
    let k = kit();
    let input = text_input(&p.query, TextStyle::new(15.0, 450.0).lh(20.0))
        .single_line()
        .key("activity-input")
        .label("Search activity")
        .grow(1.0)
        .shrink()
        .color(strong())
        .placeholder("Search activity".to_string(), weak())
        .on_edit(edited)
        .on_input_key(|s: &mut App, ev: &KeyEvent, _: &mut Tx| key(s, ev).unwrap_or(false));
    let mut pops = Vec::new();
    let mut labels = vec![ALL_EVENTS.to_string()];
    labels.extend(p.list.kinds.iter().map(|x| kind_label(x)));
    let current = if p.kind.is_empty() { ALL_EVENTS.to_string() } else { kind_label(&p.kind) };
    let kinds = Select::new("activity-kind", &labels)
        .current(Some(&current))
        .open(p.kind_open)
        .on_toggle(|s: &mut App| {
            if let Some(p) = s.ov.search.activity.panel.as_mut() {
                p.kind_open = !p.kind_open;
            }
        })
        .on_close(|s: &mut App| {
            if let Some(p) = s.ov.search.activity.panel.as_mut() {
                p.kind_open = false;
            }
        })
        .on_choose(|s: &mut App, v: &str, _| {
            let Some(p) = s.ov.search.activity.panel.as_mut() else { return };
            p.kind_open = false;
            p.kind = p.list.kinds.iter().find(|k| kind_label(k) == v).cloned().unwrap_or_default();
            invalidate(p);
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
    if p.list.items.is_empty() && !p.loading && !pending(p) && p.error.is_none() {
        list = list.child(text("No activity matches these filters.".to_string()).font(META).color(weak()).px(12.0).py(10.0));
    }
    if !pending(p) {
        for e in &p.list.items {
            list = list.child(entry_row(s, e));
        }
    }
    let mut foot = row().w_full().gap(12.0).items_center().px(16.0).py(10.0).border_sides([1.0, 0.0, 0.0, 0.0], ui::rule());
    let shown = match p.list.items.len() {
        _ if pending(p) => "Searching…".to_string(),
        1 => "1 event shown".to_string(),
        n => format!("{n} events shown"),
    };
    foot = foot.child(text(shown).font(META).color(weak()));
    if let Some(e) = p.error.as_ref().filter(|_| !pending(p)) {
        foot = foot.child(text(e.clone()).font(META).color(crate::theme::failed()).wrap().shrink());
    }
    foot = foot.child(ui::spacer());
    if p.loading || pending(p) {
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
            .on_press(close)
            .child(panel.on_press(|_: &mut App| {})),
    );
    layer.extend(pops);
}
