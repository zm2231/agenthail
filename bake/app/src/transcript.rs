use std::collections::HashMap;
use std::rc::Rc;

use feed::Transcript;
use feed::blocks::{AgentEntry, Block, Kind, Work};
use feed::summary::Entry;
use feed::wire::{Outcome, Rebuild, Sse, Wire};
use rune::{Color, El, GrowthSpec, ScrollSpec, col, el, icon, memo, row, text};
use rune_kit::{Kit, MarkdownStyle};
use rune_kit::markdown::Markdown;

use crate::App;
use crate::icons;
use crate::theme::{self, BODY_BOLD, META, META_BOLD, accent, failed, faint, line, muted, wash};

pub struct Replay {
    lines: Vec<(f64, String)>,
    next: usize,
    first: f64,
    speed: f64,
    started: Option<f64>,
    sse: Sse,
    rebuild: Rebuild,
}

impl Replay {
    pub fn load(path: &str, speed: f64) -> Self {
        let text = std::fs::read_to_string(path).unwrap_or_default();
        let lines: Vec<(f64, String)> = text
            .lines()
            .filter_map(|l| {
                let (ts, rest) = l.split_once('\t')?;
                Some((ts.parse().ok()?, rest.to_string()))
            })
            .collect();
        let first = lines.first().map(|l| l.0).unwrap_or(0.0);
        Self { lines, next: 0, first, speed, started: None, sse: Sse::default(), rebuild: Rebuild::default() }
    }

    pub fn pump(&mut self, t: f64, transcript: &mut Transcript) -> (bool, bool) {
        let start = *self.started.get_or_insert(t);
        let mut changed = false;
        while let Some((ts, line)) = self.lines.get(self.next) {
            if (ts - self.first) / self.speed > t - start {
                break;
            }
            self.next += 1;
            let Some(json) = self.sse.line(line) else { continue };
            let Ok(wire) = serde_json::from_str::<Wire>(&json) else { continue };
            if let Outcome::Events(seq, events) = self.rebuild.apply(wire) {
                changed |= transcript.apply(seq, events);
            }
        }
        (changed, self.next < self.lines.len())
    }
}

#[derive(Default)]
pub struct Earlier {
    pub next: Option<i64>,
    pub anchored: bool,
    pub loading: bool,
    pub note: Option<String>,
    pub at_top: bool,
}

impl Earlier {
    pub fn more(&self) -> bool {
        self.next != Some(0)
    }
}

pub struct Pane {
    pub transcript: Transcript,
    pub blocks: Vec<Block>,
    pub revs: Vec<u64>,
    pub live: Option<std::sync::mpsc::Receiver<feed::source::Message>>,
    pub replay: Option<Replay>,
    pub status: String,
    pub caught_up: bool,
    pub paged_first: bool,
    pub reload: usize,
    pub last_batch: f64,
    pub earlier: Earlier,
    pub expanding: HashMap<String, Option<String>>,
    pub shown: HashMap<String, usize>,
    pub generation: u64,
}

static PANES: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(1);
static FOCUS: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);
const BEHIND_PX: f32 = 120.0;

thread_local! {
    static BEHIND: std::cell::RefCell<(String, bool, bool)> = const { std::cell::RefCell::new((String::new(), false, false)) };
}

pub fn focused() -> bool {
    FOCUS.load(std::sync::atomic::Ordering::Relaxed)
}

pub fn restore_focus(on: bool) {
    FOCUS.store(on, std::sync::atomic::Ordering::Relaxed);
}

pub fn set_focus(s: &mut App, on: bool) {
    if focused() == on {
        return;
    }
    restore_focus(on);
    let mut counter = s.counter;
    for pane in s.panes.values_mut() {
        pane.rebuild(&mut counter);
    }
    s.counter = counter;
    s.panes_moved = true;
    crate::over::layout::save(s);
}

fn watch_end(id: String) -> impl Fn(rune::ScrollPos) + 'static {
    move |at| {
        crate::trace::scroll(at);
        let hidden = at.max[1] - at.offset[1] > BEHIND_PX;
        BEHIND.with_borrow_mut(|b| {
            if b.0 != id || b.1 != hidden {
                *b = (id.clone(), hidden, true);
            }
        });
    }
}

fn behind(id: &str) -> bool {
    BEHIND.with_borrow(|b| b.0 == id && b.1)
}

pub fn take_behind_moved() -> bool {
    BEHIND.with_borrow_mut(|b| std::mem::take(&mut b.2))
}

impl Pane {
    pub fn new() -> Self {
        Self {
            transcript: Transcript::default(),
            blocks: Vec::new(),
            revs: Vec::new(),
            live: None,
            replay: None,
            status: "loading".into(),
            caught_up: false,
            paged_first: false,
            reload: 0,
            last_batch: f64::MAX,
            earlier: Earlier::default(),
            expanding: HashMap::new(),
            shown: HashMap::new(),
            generation: PANES.fetch_add(1, std::sync::atomic::Ordering::Relaxed),
        }
    }

    pub fn rebuild(&mut self, counter: &mut u64) {
        let Some((start, tail)) = self.transcript.tail(focused()) else { return };
        let start = start.min(self.blocks.len());
        let previous: HashMap<&str, usize> = self.blocks[start..].iter().enumerate().map(|(i, b)| (b.id.as_str(), start + i)).collect();
        let mut revs = Vec::with_capacity(tail.len());
        for block in &tail {
            let rev = match previous.get(block.id.as_str()) {
                Some(&i) if self.blocks[i] == *block => self.revs[i],
                _ => {
                    *counter += 1;
                    *counter
                }
            };
            revs.push(rev);
        }
        self.blocks.truncate(start);
        self.revs.truncate(start);
        self.blocks.extend(tail);
        self.revs.extend(revs);
    }

    pub fn bump_item(&mut self, item: &str, counter: &mut u64) {
        let holder = self.blocks.iter().position(|b| b.id.strip_prefix("item:") == Some(item) || matches!(&b.kind, Kind::Work(w) if w.items.iter().any(|i| i.id == item)));
        if let Some(ix) = holder {
            *counter += 1;
            self.revs[ix] = *counter;
        }
    }

    pub fn bump(&mut self, id: &str, counter: &mut u64) {
        if let Some(ix) = self.blocks.iter().position(|b| b.id == id) {
            *counter += 1;
            self.revs[ix] = *counter;
        }
    }
}

fn first_line(text: &str, max: usize) -> String {
    let line = text.lines().find(|l| !l.trim().is_empty()).unwrap_or("").trim();
    if line.chars().count() <= max { line.to_string() } else { format!("{}…", line.chars().take(max).collect::<String>()) }
}

fn markdown(body: &str, k: &Kit) -> El {
    let p = crate::theme::paint();
    Markdown::new(body.to_string()).selectable(true).render(&MarkdownStyle::from_kit(&Kit { roles: k.roles, ..Kit::neutral(p.dark).with_text_scale(k.text_scale) }).text(p.text).muted(p.muted)).selection_group(false)
}

pub struct Cx<'a> {
    pub session: &'a str,
    pub block: &'a str,
    pub pane: &'a Pane,
    pub open: &'a std::collections::HashSet<String>,
    pub app: &'a App,
}

const SHOWN_BODY: usize = 20_000;

fn body_tools(cx: &Cx, item: &feed::Item, mut head: El, k: &Kit) -> El {
    if item.truncated && item.body_ref.is_some() {
        let (sid, iid) = (cx.session.to_string(), item.id.clone());
        head = match cx.pane.expanding.get(&item.id) {
            Some(None) => head.child(text("Loading all of it…").font(k.text(META)).color(faint())),
            Some(Some(note)) => head
                .child(text(note.clone()).font(k.text(META)).color(failed()).truncate().max_w(320.0))
                .child(action(format!("full-{}", item.id), &icons::ROTATE, "Try again", k).on_press(move |s: &mut App| crate::history::load_body(s, &sid, &iid))),
            None => head.child(action(format!("full-{}", item.id), &icons::CHEVRON_DOWN, "Show all", k).on_press(move |s: &mut App| crate::history::load_body(s, &sid, &iid))),
        };
    }
    head.child(action(format!("copy-{}", item.id), &icons::COPY, "Copy", k).on_press_tx(crate::history::copy(item.text.clone())))
}

fn entry_body(cx: &Cx, label: &str, item: &feed::Item, k: &Kit) -> El {
    let limit = cx.pane.shown.get(&item.id).copied().unwrap_or(SHOWN_BODY);
    let (shown, clipped) = feed::summary::clip(&item.text, limit);
    let head = body_tools(cx, item, row().w_full().gap(6.0).items_center().child(text(label.to_string()).font(k.text(META_BOLD)).color(muted())).child(el().grow(1.0)), k);
    let mut c = col().w_full().gap(4.0).child(head).child(
        col().w_full().px(10.0).py(8.0).radius(8.0).fill(Color::BLACK.alpha(0.18)).child(text(shown.to_string()).font(k.text(META)).color(theme::text()).wrap().selectable(true)),
    );
    if clipped {
        let (iid, more) = (item.id.clone(), limit + SHOWN_BODY * 5);
        let block = cx.block.to_string();
        c = c.child(
            row()
                .gap(8.0)
                .items_center()
                .child(text(format!("Showing {} of {} KB", shown.len() / 1024, item.text.len() / 1024)).font(k.text(META)).color(faint()))
                .child(action(format!("more-{}", item.id), &icons::CHEVRON_DOWN, "Show more", k).on_press(move |s: &mut App| crate::history::show_more(s, &block, &iid, more))),
        );
    }
    c
}

fn entry_el(cx: &Cx, block: &str, entry: &Entry, k: &Kit) -> El {
    let (glyph, title, detail, failure) = match entry {
        Entry::Call { call, results } => (&icons::TERMINAL, call.title.clone(), first_line(&call.text, 140), results.iter().any(feed::summary::is_failure)),
        Entry::Output(item) => (&icons::CORNER, "Output".to_string(), first_line(&item.text, 140), false),
        Entry::Reasoning(item) => (&icons::SPARKLES, "Thinking".to_string(), first_line(&item.text, 160), false),
    };
    let key = format!("entry:{}", entry.id());
    let expanded = cx.open.contains(&key);
    let mut line_el = row()
        .key(format!("entry-{}", entry.id()))
        .w_full()
        .gap(8.0)
        .items_center()
        .px(10.0)
        .py(5.0)
        .radius(7.0)
        .hover(wash())
        .child(icon(glyph, k.text_size(13.0)).color(if failure { failed() } else { faint() }))
        .child(text(title).font(k.text(META_BOLD)).color(muted()))
        .child(text(detail).font(k.text(META)).color(faint()).truncate().grow(1.0).min_w(0.0));
    if failure {
        line_el = line_el.child(row().px(6.0).py(1.0).radius(5.0).fill(failed().alpha(0.15)).child(text("failed").font(k.text(META_BOLD)).color(failed())));
    }
    let line_el = line_el.child(icon(if expanded { &icons::CHEVRON_DOWN } else { &icons::CHEVRON_RIGHT }, k.text_size(11.0)).color(faint())).on_press(toggle_in(key, block.to_string()));
    if !expanded {
        return line_el;
    }
    let call_id = match entry {
        Entry::Call { call, results } => std::iter::once(call).chain(results).find_map(|i| i.call_id.clone()),
        Entry::Output(item) => item.call_id.clone(),
        Entry::Reasoning(_) => None,
    }
    .filter(|c| !c.is_empty());
    let copy_id = call_id.map(|call| row().child(action(format!("copy-call-{}", entry.id()), &icons::TERMINAL, "Copy call ID", k).help(call.clone()).on_press_tx(crate::history::copy(call))));
    col().w_full().gap(4.0).child(line_el).child(col().w_full().pl(10.0).pb(6.0).gap(8.0).children(copy_id).children(entry.bodies().into_iter().map(|(label, item)| entry_body(cx, label, item, k))))
}

fn toggle_in(id: String, block: String) -> impl Fn(&mut App) + 'static {
    move |s: &mut App| {
        if !s.open.remove(&id) {
            s.open.insert(id.clone());
        }
        let mut counter = s.counter;
        for pane in s.panes.values_mut() {
            pane.bump(&block, &mut counter);
        }
        s.counter = counter;
    }
}

fn toggle(id: String) -> impl Fn(&mut App) + 'static {
    toggle_in(id.clone(), id)
}

fn work_el(cx: &Cx, id: &str, work: &Work, open: bool, k: &Kit) -> El {
    let shown = open || !work.folds;
    let label = if work.live {
        format!("Working · {}", work.label)
    } else if work.line == work.label || work.label == "Thought" && work.line.ends_with("Thought") {
        work.line.clone()
    } else {
        format!("{} · {}", work.line, work.label)
    };
    let tint = if work.live { accent() } else { muted() };
    let lead = if work.live {
        el().size(7.0, 7.0).radius(4.0).fill(accent()).shadow(0.0, 8.0, accent().alpha(0.9))
    } else {
        icon(&icons::TERMINAL, k.text_size(13.0)).color(faint())
    };
    let head = row()
        .key(format!("work-{id}"))
        .gap(8.0)
        .items_center()
        .px(10.0)
        .py(6.0)
        .radius(9.0)
        .fill(if work.live { accent().alpha(0.08) } else { wash() })
        .border(1.0, if work.live { accent().alpha(0.22) } else { line() })
        .hover(crate::theme::ink(0.09))
        .press_scale(0.98)
        .child(lead)
        .child(text(label).font(k.text(META)).color(tint))
        .child(icon(if shown && work.folds { &icons::CHEVRON_DOWN } else { &icons::CHEVRON_RIGHT }, k.text_size(12.0)).color(faint()))
        .on_press(toggle(id.to_string()));
    let entries: Vec<&Entry> = if shown {
        work.entries.iter().collect()
    } else if work.live {
        work.entries.iter().rev().take(1).collect()
    } else {
        Vec::new()
    };
    let mut c = col().w_full().gap(4.0).items_start().child(head);
    if !entries.is_empty() {
        c = c.child(
            col()
                .w_full()
                .ml(14.0)
                .pl(8.0)
                .border_sides([0.0, 0.0, 0.0, 1.5], line())
                .gap(1.0)
                .children(entries.into_iter().map(|e| entry_el(cx, id, e, k))),
        );
    }
    c
}

pub fn avatar(name: &str, size: f32) -> El {
    let hue = theme::hue_for(name);
    let letter: String = name.trim_start_matches('@').chars().next().map(|c| c.to_uppercase().collect()).unwrap_or_default();
    col()
        .size(size, size)
        .radius(size / 2.0)
        .items_center()
        .justify_center()
        .linear_gradient(135.0, [(0.0, rune::oklch(0.62, 0.14, hue)), (1.0, rune::oklch(0.45, 0.14, hue + 40.0))])
        .child(text(letter).font(rune::TextStyle::new(size * 0.45, 700.0)).color(Color::WHITE))
}

fn agent_el(entry: &AgentEntry, k: &Kit) -> El {
    let incoming = entry.agent.is_incoming();
    let chip = entry.agent.chip();
    row()
        .w_full()
        .gap(10.0)
        .items_start()
        .px(12.0)
        .py(9.0)
        .radius(12.0)
        .fill(wash())
        .border(1.0, line())
        .child(avatar(&chip, 22.0))
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(2.0)
                .child(
                    row()
                        .gap(6.0)
                        .items_center()
                        .child(icon(if incoming { &icons::CORNER } else { &icons::ARROW_RIGHT }, k.text_size(11.0)).color(faint()))
                        .child(text(if incoming { format!("From {chip}") } else { format!("To {chip}") }).font(k.text(META_BOLD)).color(muted())),
                )
                .child(text(first_line(&entry.agent.summary, 220)).font(k.text(META)).color(faint()).wrap().max_lines(2)),
        )
}

fn block_el(cx: &Cx, block: &Block, open: bool, gap: f32, last: bool, k: &Kit) -> El {
    let body = match &block.kind {
        Kind::User(body) if body.trim_start().starts_with("<hook_prompt") => {
            let inner = body.split_once('>').map(|(_, rest)| rest).unwrap_or(body).replace("</hook_prompt>", "");
            row()
                .w_full()
                .gap(8.0)
                .items_start()
                .px(12.0)
                .py(8.0)
                .radius(10.0)
                .border(1.0, line())
                .child(icon(&icons::BOLT, k.text_size(12.0)).color(faint()).mt(2.0))
                .child(col().grow(1.0).min_w(0.0).gap(2.0).child(text("Hook").font(k.text(META_BOLD)).color(muted())).child(text(first_line(&inner, 400)).font(k.text(META)).color(faint()).wrap().max_lines(3)))
        }
        Kind::User(body) => col().w_full().items_end().child(
            col()
                .max_w(560.0)
                .corners([18.0, 18.0, 6.0, 18.0])
                .linear_gradient(160.0, if crate::theme::paint().dark { [(0.0, rune::oklch(0.33, 0.06, 268.0)), (1.0, rune::oklch(0.27, 0.05, 275.0))] } else { [(0.0, rune::oklch(0.93, 0.03, 268.0)), (1.0, rune::oklch(0.90, 0.035, 275.0))] })
                .border(1.0, crate::theme::ink(0.08))
                .px(15.0)
                .py(10.0)
                .child(markdown(body, k)),
        ),
        Kind::Assistant(body) => {
            let mut c = col().w_full().gap(6.0).child(markdown(body, k));
            if let Some(took) = &block.finished {
                c = c.child(row().gap(5.0).items_center().child(icon(&icons::CLOCK, k.text_size(11.0)).color(faint())).child(text(format!("Took {took}")).font(k.text(META)).color(faint())));
            }
            c
        }
        Kind::Work(work) => work_el(cx, &block.id, work, open, k),
        Kind::Agent(entry) => agent_el(entry, k),
        Kind::AgentFold { label, members, .. } => {
            let head = row()
                .key(format!("fold-{}", block.id))
                .gap(8.0)
                .items_center()
                .px(10.0)
                .py(6.0)
                .radius(9.0)
                .fill(wash())
                .hover(crate::theme::ink(0.09))
                .child(icon(&icons::MESSAGE, k.text_size(13.0)).color(faint()))
                .child(text(label.clone()).font(k.text(META)).color(muted()))
                .child(icon(if open { &icons::CHEVRON_DOWN } else { &icons::CHEVRON_RIGHT }, k.text_size(12.0)).color(faint()))
                .on_press(toggle(block.id.clone()));
            let mut c = col().w_full().gap(6.0).items_start().child(head);
            if open {
                c = c.children(members.iter().map(|e| agent_el(e, k)));
            }
            c
        }
        Kind::Annotation(note) | Kind::Notice(note) => row().gap(6.0).child(text(note.clone()).font(k.text(META)).color(faint()).wrap()),
        Kind::Instructions(_) => row().gap(6.0).items_center().child(icon(&icons::LAYERS, k.text_size(12.0)).color(faint())).child(text("Instructions").font(k.text(META)).color(faint())),
        Kind::Image(attachment) => crate::ui::attach::image_el(cx.app, cx.session, attachment, k),
        Kind::File(item) => crate::ui::attach::file_el(cx.app, cx.session, item, k),
        Kind::Files(files) => crate::ui::attach::files_el(cx.app, &block.id, files, k),
        Kind::Unknown { title, kind, text: body } => {
            let head = row().w_full().gap(8.0).items_center().child(text(title.clone()).font(k.text(META_BOLD)).color(theme::text()).truncate().min_w(0.0)).child(row().px(5.0).py(1.0).radius(8.0).fill(wash()).child(text(kind.clone()).font(k.text(META.mono())).color(muted())));
            let content = if body.trim().is_empty() { text("No recorded content.").font(k.text(META)).color(faint()) } else { markdown(body, k) };
            col().key(format!("unknown-{}", block.id)).w_full().gap(4.0).px(12.0).py(9.0).radius(12.0).fill(crate::theme::ink(0.04)).border(1.0, line()).child(head).child(content)
        }
    };
    let mut row_el = col().w_full().max_w(780.0).px(36.0).pt(gap).child(body);
    if let Some(footer) = footer(cx, block, k) {
        row_el = row_el.hover_group().child(footer);
    }
    if last { row_el.streaming(true) } else { row_el }
}

fn action(key: String, glyph: &'static rune::Icon, label: &str, k: &Kit) -> El {
    row()
        .key(key)
        .gap(4.0)
        .items_center()
        .px(6.0)
        .py(3.0)
        .radius(6.0)
        .hover(wash())
        .press_scale(0.96)
        .child(icon(glyph, k.text_size(12.0)).color(faint()))
        .child(text(label.to_string()).font(k.text(META)).color(faint()))
}

fn footer(cx: &Cx, block: &Block, k: &Kit) -> Option<El> {
    let item = block.id.strip_prefix("item:")?;
    let (body, user) = match &block.kind {
        Kind::User(body) if !body.trim_start().starts_with("<hook_prompt") => (body.clone(), true),
        Kind::Assistant(body) => (body.clone(), false),
        _ => return None,
    };
    let mut tools = row().gap(2.0).items_center().group_opacity(0.0, 1.0);
    tools = tools.child(action(format!("copy-{item}"), &icons::COPY, "Copy", k).on_press_tx(crate::history::copy(body.clone())));
    tools = tools.child(action(format!("share-{item}"), &icons::SHARE, "Share", k).on_press_tx(crate::history::share(cx.session.to_string(), format!("share-{item}"), body.clone())));
    let images = cx.pane.transcript.images_with(item);
    if user && images > 0 {
        tools = tools.child(action(format!("edit-{item}"), &icons::PENCIL, "Edit", k).opacity(0.45).help("Edit would resend this message without its images"));
    } else if user {
        tools = tools.child(action(format!("edit-{item}"), &icons::PENCIL, "Edit", k).on_press(crate::history::edit(body.clone())));
    }
    let mut line = row().gap(6.0).items_center().pt(2.0);
    if let Some(full) = cx.pane.transcript.item(item).filter(|i| i.truncated && i.body_ref.is_some()) {
        line = body_tools(cx, full, line, k).child(
            action(format!("share-{item}"), &icons::SHARE, "Share", k)
                .help("Shares what has loaded so far; Show all loads the rest first")
                .on_press_tx(crate::history::share(cx.session.to_string(), format!("share-{item}"), full.text.clone())),
        );
        return Some(if user { col().w_full().items_end().child(line) } else { line });
    }
    let line = line.child(tools);
    Some(if user { col().w_full().items_end().child(line) } else { line })
}

pub fn empty(s: &App, id: &str, note: String, k: &Kit) -> El {
    let settled = s.panes.get(id).is_some_and(|p| p.caught_up && !p.earlier.loading && p.earlier.next == Some(0));
    let note = if settled && note == "Loading the transcript…" { "Nothing to show in this conversation yet".to_string() } else { note };
    let mut c = col().w_full().grow(1.0).items_center().justify_center().gap(12.0);
    if !settled {
        c = c.child(rune_kit::Loader::new().size(22.0).color(accent()).build(k));
    }
    c = c.child(text(note).font(k.text(BODY_BOLD)).color(faint()));
    if let Some(earlier) = earlier_el(s, id, k) {
        c = c.child(earlier);
    }
    c.child(el().h(80.0))
}

fn earlier_el(s: &App, id: &str, k: &Kit) -> Option<El> {
    let pane = s.panes.get(id)?;
    let key = id.to_string();
    let body = if pane.earlier.loading {
        row().gap(8.0).items_center().child(rune_kit::Loader::new().size(k.text_size(12.0)).color(faint()).build(k)).child(text("Loading earlier messages…").font(k.text(META)).color(faint()))
    } else if let Some(note) = &pane.earlier.note {
        let mut r = row().gap(8.0).items_center().child(text(note.clone()).font(k.text(META)).color(faint()));
        if pane.earlier.next != Some(0) {
            r = r.child(action("earlier-retry".into(), &icons::ROTATE, "Try again", k).on_press(move |s: &mut App| crate::history::retry_older(s, &key)));
        }
        r
    } else if crate::history::can_load(s, id) {
        row().child(action("earlier".into(), &icons::CHEVRON_DOWN, "Load earlier", k).on_press(move |s: &mut App| {
            crate::history::load_older(s, &key);
        }))
    } else {
        return None;
    };
    Some(col().key(format!("earlier-{id}")).w_full().items_center().pt(14.0).child(body))
}

fn gap_for(blocks: &[Block], ix: usize) -> f32 {
    match ix.checked_sub(1).and_then(|i| blocks.get(i)) {
        None => 20.0,
        Some(_) if blocks[ix].is_message_in() => 26.0,
        Some(p) if p.is_message_in() => 16.0,
        Some(_) => 10.0,
    }
}

pub fn view(s: &App, id: &str, k: &Kit) -> El {
    let row_kit = Rc::new(k.clone());
    let count = s.panes.get(id).map(|p| p.blocks.len()).unwrap_or(0);
    let key = id.to_string();
    let stack = col().key(format!("rows-{id}")).w_full().items_center().growth(GrowthSpec::default()).selection_group(true).pb(s.cover.reserve()).virtual_rows(
        count,
        60.0,
        {
            let key = key.clone();
            move |s: &App, i| s.panes.get(&key).map(|p| p.blocks[i].id.clone().into()).unwrap_or_default()
        },
        move |s: &App, i| {
            let Some(pane) = s.panes.get(&key) else { return col() };
            let block = &pane.blocks[i];
            let open = s.open.contains(&block.id);
            let last = i + 1 == pane.blocks.len() && pane.transcript.turn_open;
            let gap = gap_for(&pane.blocks, i);
            let cx = Cx { session: &key, block: &block.id, pane, open: &s.open, app: s };
            let images = match (&block.kind, block.id.strip_prefix("item:")) {
                (Kind::User(_), Some(item)) => pane.transcript.images_with(item),
                (Kind::Image(_) | Kind::File(_) | Kind::Files(_), _) => s.attach.rev as usize,
                _ => 0,
            };
            memo(&block.id, (pane.revs[i], open, last, gap.to_bits(), images, crate::theme::paint_key(), row_kit.text_scale.to_bits()), || block_el(&cx, block, open, gap, last, &row_kit))
        },
    );
    let watch = id.to_string();
    let stack = stack.on_visible_rows(move |s: &mut App, rows| crate::history::seen_rows(s, &watch, rows.start));
    let mut scroll = col().key(format!("transcript-{id}")).w_full().grow(1.0).min_h(0.0).scroll(ScrollSpec { stick_bottom: true }).on_scroll_offset(watch_end(id.to_string())).on_layout(|s: &mut App, r: [f32; 4]| s.cover.set_view(r));
    if let Some(earlier) = earlier_el(s, id, k) {
        scroll = scroll.child(earlier);
    }
    let scroll = scroll.child(stack);
    let frame = col().w_full().grow(1.0).min_h(0.0).child(scroll);
    if !behind(id) {
        return frame;
    }
    let target = format!("transcript-{id}");
    let jump = row()
        .key("jump-to-latest")
        .gap(6.0)
        .items_center()
        .px(10.0)
        .py(6.0)
        .radius(14.0)
        .fill(crate::theme::raised())
        .border(1.0, line())
        .hover(crate::theme::ink(0.08))
        .role("button")
        .label("Jump to latest")
        .on_press_tx(move |_: &mut App, tx: &mut rune::Tx| tx.scroll_to_bottom(target.clone()))
        .child(icon(&icons::ARROW_DOWN, k.text_size(12.0)).color(muted()))
        .child(text("Jump to latest").font(k.text(META)).color(muted()));
    frame.child(col().absolute(0.0, 0.0).w_full().h_full().items_end().justify_end().px(28.0).pb(s.cover.reserve() + 12.0).child(jump))
}

pub fn loading(note: String, k: &Kit) -> El {
    col()
        .w_full()
        .grow(1.0)
        .items_center()
        .justify_center()
        .gap(12.0)
        .child(rune_kit::Loader::new().size(22.0).color(accent()).build(k))
        .child(text(note).font(k.text(BODY_BOLD)).color(faint()))
        .child(el().h(80.0))
}

pub use avatar as session_avatar;
