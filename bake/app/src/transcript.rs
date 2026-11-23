use std::collections::HashMap;

use feed::Transcript;
use feed::blocks::{AgentEntry, Block, Kind, Work};
use feed::summary::Entry;
use feed::wire::{Outcome, Rebuild, Sse, Wire};
use rune::tok;
use rune::{El, GrowthSpec, ScrollSpec, TextStyle, col, memo, row, text};
use rune_kit::MarkdownStyle;
use rune_kit::markdown::Markdown;

use crate::App;
use crate::theme::{META, META_BOLD, accent, failed};

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

pub struct Pane {
    pub transcript: Transcript,
    pub blocks: Vec<Block>,
    pub revs: Vec<u64>,
    pub live: Option<std::sync::mpsc::Receiver<feed::source::Message>>,
    pub replay: Option<Replay>,
    pub status: String,
}

impl Pane {
    pub fn new() -> Self {
        Self { transcript: Transcript::default(), blocks: Vec::new(), revs: Vec::new(), live: None, replay: None, status: "loading".into() }
    }

    pub fn rebuild(&mut self, counter: &mut u64) {
        let Some((start, tail)) = self.transcript.tail(true) else { return };
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

fn markdown(body: &str) -> El {
    Markdown::new(body.to_string()).selectable(true).render(&MarkdownStyle::neutral(true)).selection_group(false)
}

fn entry_el(entry: &Entry) -> El {
    let t = tok::theme(true);
    let dim = t.text_secondary.alpha(0.7);
    match entry {
        Entry::Call { call, results } => {
            let mut line = row()
                .gap(8.0)
                .pl(18.0)
                .child(text(call.title.clone()).font(META_BOLD).color(t.text_secondary))
                .child(text(first_line(&call.text, 140)).font(META).color(dim).truncate());
            if results.iter().any(feed::summary::is_failure) {
                line = line.child(text("failed").font(META).color(failed()));
            }
            line
        }
        Entry::Output(item) => row().pl(18.0).child(text(first_line(&item.text, 140)).font(META).color(dim).truncate()),
        Entry::Reasoning(item) => row().pl(18.0).child(text(first_line(&item.text, 160)).font(META).color(dim).truncate()),
    }
}

fn toggle(id: String) -> impl Fn(&mut App) + 'static {
    move |s: &mut App| {
        if !s.open.remove(&id) {
            s.open.insert(id.clone());
        }
        let mut counter = s.counter;
        if let Some(pane) = s.selected.clone().and_then(|sel| s.panes.get_mut(&sel)) {
            pane.bump(&id, &mut counter);
        }
        s.counter = counter;
    }
}

fn work_el(id: &str, work: &Work, open: bool) -> El {
    let t = tok::theme(true);
    let shown = open || !work.folds;
    let header = if work.live { format!("Working · {}", work.label) } else { format!("{} · {}", work.line, work.label) };
    let color = if work.live { accent() } else { t.text_secondary };
    let head = row()
        .key(format!("work-{id}"))
        .gap(8.0)
        .child(text(if shown && work.folds { "▾" } else { "▸" }).font(META).color(color))
        .child(text(header).font(META).color(color))
        .on_press(toggle(id.to_string()));
    let entries: Vec<&Entry> = if shown {
        work.entries.iter().collect()
    } else if work.live {
        work.entries.iter().rev().take(1).collect()
    } else {
        Vec::new()
    };
    col().w_full().gap(3.0).child(head).children(entries.into_iter().map(entry_el))
}

fn agent_el(entry: &AgentEntry) -> El {
    let t = tok::theme(true);
    let lead = if entry.agent.is_incoming() { format!("From {}", entry.agent.chip()) } else { format!("To {}", entry.agent.chip()) };
    row()
        .gap(8.0)
        .child(text(lead).font(META_BOLD).color(t.text_secondary))
        .child(text(first_line(&entry.agent.summary, 200)).font(META).color(t.text_secondary.alpha(0.7)).truncate())
}

fn block_el(block: &Block, open: bool, gap: f32, last: bool) -> El {
    let t = tok::theme(true);
    let body = match &block.kind {
        Kind::User(body) => col().w_full().items_end().child(col().max_w(560.0).fill(t.card).radius(14.0).px(14.0).py(9.0).child(markdown(body))),
        Kind::Assistant(body) => {
            let mut c = col().w_full().gap(4.0).child(markdown(body));
            if let Some(took) = &block.finished {
                c = c.child(text(format!("Took {took}")).font(META).color(t.text_secondary));
            }
            c
        }
        Kind::Work(work) => work_el(&block.id, work, open),
        Kind::Agent(entry) => agent_el(entry),
        Kind::AgentFold { label, members, .. } => {
            let head = row()
                .key(format!("fold-{}", block.id))
                .child(text(format!("{} {label}", if open { "▾" } else { "▸" })).font(META).color(t.text_secondary))
                .on_press(toggle(block.id.clone()));
            let mut c = col().w_full().gap(4.0).child(head);
            if open {
                c = c.children(members.iter().map(agent_el));
            }
            c
        }
        Kind::Annotation(note) | Kind::Notice(note) => col().child(text(note.clone()).font(META).color(t.text_secondary).wrap()),
        Kind::Instructions(_) => col().child(text("Instructions").font(META).color(t.text_secondary)),
        Kind::Unknown { title, .. } => col().child(text(title.clone()).font(META).color(t.text_secondary)),
    };
    let row = col().w_full().max_w(760.0).px(28.0).pt(gap).child(body);
    if last { row.streaming(true) } else { row }
}

fn gap_for(blocks: &[Block], ix: usize) -> f32 {
    match ix.checked_sub(1).and_then(|i| blocks.get(i)) {
        None => 12.0,
        Some(_) if blocks[ix].is_message_in() => 22.0,
        Some(p) if p.is_message_in() => 12.0,
        Some(_) => 8.0,
    }
}

pub fn view(s: &App, id: &str) -> El {
    let count = s.panes.get(id).map(|p| p.blocks.len()).unwrap_or(0);
    let key = id.to_string();
    let stack = col().key(format!("rows-{id}")).w_full().growth(GrowthSpec::default()).selection_group(true).pb(16.0).virtual_rows(
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
            memo(&block.id, (pane.revs[i], open, last, gap.to_bits()), || block_el(block, open, gap, last))
        },
    );
    col().key(format!("transcript-{id}")).w_full().grow(1.0).min_h(0.0).scroll(ScrollSpec { stick_bottom: true }).child(stack)
}

pub fn style() -> TextStyle {
    META
}
