use std::collections::{HashMap, HashSet};
use std::sync::mpsc::Receiver;

use feed::blocks::{AgentEntry, Block, Kind, Work};
use feed::source::{Live, Message};
use feed::summary::Entry;
use feed::wire::{Outcome, Rebuild, Sse, Wire};
use feed::Transcript;
use rune::tok;
use rune::{AppSpec, Cx, El, GrowthSpec, KeyEvent, ScrollSpec, TextStyle, Tx, col, memo, row, text};
use rune_kit::{Kit, MarkdownStyle};
use rune_kit::markdown::Markdown;

const META: TextStyle = TextStyle::new(11.5, 400.0);
const META_BOLD: TextStyle = TextStyle::new(11.5, 600.0);
const TITLE: TextStyle = TextStyle::new(15.0, 600.0);

struct Replay {
    lines: Vec<(f64, String)>,
    next: usize,
    first: f64,
    speed: f64,
    sse: Sse,
    rebuild: Rebuild,
}

struct App {
    transcript: Transcript,
    blocks: Vec<Block>,
    revs: Vec<u64>,
    counter: u64,
    open: HashSet<String>,
    focus: bool,
    replay: Option<Replay>,
    live: Option<Receiver<Message>>,
    status: String,
    applied: usize,
    wake: Option<f64>,
}

fn load_replay(path: &str, speed: f64) -> Replay {
    let text = std::fs::read_to_string(path).unwrap_or_default();
    let lines: Vec<(f64, String)> = text
        .lines()
        .filter_map(|l| {
            let (ts, rest) = l.split_once('\t')?;
            Some((ts.parse().ok()?, rest.to_string()))
        })
        .collect();
    let first = lines.first().map(|l| l.0).unwrap_or(0.0);
    Replay { lines, next: 0, first, speed, sse: Sse::default(), rebuild: Rebuild::default() }
}

fn init() -> App {
    let args = rune::extra_args();
    let value = |flag: &str| args.iter().position(|a| a == flag).and_then(|i| args.get(i + 1)).cloned();
    let mut app = App {
        transcript: Transcript::default(),
        blocks: Vec::new(),
        revs: Vec::new(),
        counter: 0,
        open: HashSet::new(),
        focus: true,
        replay: None,
        live: None,
        status: "connecting".into(),
        applied: 0,
        wake: None,
    };
    if let Some(path) = value("--replay").or_else(|| std::env::var("BAKE_REPLAY").ok()) {
        let speed = value("--speed").and_then(|s| s.parse().ok()).unwrap_or(1.0);
        app.replay = Some(load_replay(&path, speed));
        app.status = "replay".into();
    } else if let Some(id) = value("--session") {
        let (tx, rx) = std::sync::mpsc::channel();
        match Live::from_env(id) {
            Ok(live) => {
                std::thread::spawn(move || live.run(tx));
                app.live = Some(rx);
            }
            Err(error) => app.status = error.to_string(),
        }
    } else {
        app.status = "pass --replay <stream.tsv> or --session <id>".into();
    }
    app
}

fn rebuild(s: &mut App) {
    let Some((start, tail)) = s.transcript.tail(s.focus) else { return };
    let start = start.min(s.blocks.len());
    let previous: HashMap<&str, usize> = s.blocks[start..].iter().enumerate().map(|(i, b)| (b.id.as_str(), start + i)).collect();
    let mut revs = Vec::with_capacity(tail.len());
    for block in &tail {
        let rev = match previous.get(block.id.as_str()) {
            Some(&i) if s.blocks[i] == *block => s.revs[i],
            _ => {
                s.counter += 1;
                s.counter
            }
        };
        revs.push(rev);
    }
    s.blocks.truncate(start);
    s.revs.truncate(start);
    s.blocks.extend(tail);
    s.revs.extend(revs);
}

fn soon(wake: &mut Option<f64>, tx: &mut Tx) {
    let due = tx.clock() + 0.016;
    if wake.is_none_or(|armed| tx.clock() >= armed || armed > due + 0.001) {
        *wake = Some(due);
        tx.wake_at(due);
    }
}

fn poll(s: &mut App, t: f64, tx: &mut Tx) -> bool {
    let mut changed = false;
    if let Some(replay) = &mut s.replay {
        while let Some((ts, line)) = replay.lines.get(replay.next) {
            if replay.speed > 0.0 && (ts - replay.first) / replay.speed > t {
                break;
            }
            replay.next += 1;
            let Some(json) = replay.sse.line(line) else { continue };
            let Ok(wire) = serde_json::from_str::<Wire>(&json) else { continue };
            if let Outcome::Events(seq, events) = replay.rebuild.apply(wire) {
                s.applied += 1;
                changed |= s.transcript.apply(seq, events);
            }
        }
        if replay.next >= replay.lines.len() {
            s.status = "replay finished".into();
        } else {
            soon(&mut s.wake, tx);
        }
    }
    if let Some(rx) = &s.live {
        while let Ok(message) = rx.try_recv() {
            match message {
                Message::Batch(b) => {
                    s.applied += 1;
                    changed |= s.transcript.apply(b.seq, b.events);
                }
                Message::Status(status) => s.status = status,
                Message::CaughtUp => {}
            }
        }
        soon(&mut s.wake, tx);
    }
    if changed {
        rebuild(s);
    }
    changed
}

fn first_line(text: &str, max: usize) -> String {
    let line = text.lines().find(|l| !l.trim().is_empty()).unwrap_or("").trim();
    if line.chars().count() <= max { line.to_string() } else { format!("{}…", line.chars().take(max).collect::<String>()) }
}

fn markdown(body: &str, increased: bool) -> El {
    let kit = if increased { Kit::neutral(true).increased_contrast() } else { Kit::neutral(true) };
    Markdown::new(body.to_string()).selectable(true).render(&MarkdownStyle::from_kit(&kit)).selection_group(false)
}

fn entry_el(entry: &Entry) -> El {
    let t = tok::theme(true);
    match entry {
        Entry::Call { call, results } => {
            let failed = results.iter().any(feed::summary::is_failure);
            let mut line = row().gap(8.0).pl(16.0).child(text(call.title.clone()).font(META_BOLD).color(t.text_secondary)).child(
                text(first_line(&call.text, 140)).font(META).color(t.text_secondary.alpha(0.7)).truncate(),
            );
            if failed {
                line = line.child(text("failed").font(META).color(rune::oklch(0.70, 0.17, 25.0)));
            }
            line
        }
        Entry::Output(item) => row().pl(16.0).child(text(first_line(&item.text, 140)).font(META).color(t.text_secondary.alpha(0.7)).truncate()),
        Entry::Reasoning(item) => row().pl(16.0).child(text(first_line(&item.text, 160)).font(META).color(t.text_secondary.alpha(0.7)).truncate()),
    }
}

fn work_el(id: &str, work: &Work, open: bool) -> El {
    let t = tok::theme(true);
    let shown = open || !work.folds;
    let header = if work.live { format!("Working · {}", work.label) } else { format!("{} · {}", work.line, work.label) };
    let arrow = if shown && work.folds { "▾" } else { "▸" };
    let toggle = id.to_string();
    let color = if work.live { rune::oklch(0.72, 0.13, 260.0) } else { t.text_secondary };
    let head = row()
        .key(format!("work-{id}"))
        .gap(8.0)
        .child(text(arrow).font(META).color(color))
        .child(text(header).font(META).color(color))
        .on_press(move |s: &mut App| {
            if !s.open.remove(&toggle) {
                s.open.insert(toggle.clone());
            }
            rebuild_open(s, &toggle);
        });
    let entries: Vec<&Entry> = if shown {
        work.entries.iter().collect()
    } else if work.live {
        work.entries.iter().rev().take(1).collect()
    } else {
        Vec::new()
    };
    col().w_full().gap(2.0).child(head).children(entries.into_iter().map(entry_el))
}

fn rebuild_open(s: &mut App, id: &str) {
    if let Some(ix) = s.blocks.iter().position(|b| b.id == id) {
        s.counter += 1;
        s.revs[ix] = s.counter;
    }
}

fn agent_el(entry: &AgentEntry) -> El {
    let t = tok::theme(true);
    let lead = if entry.agent.is_incoming() { format!("From {}", entry.agent.chip()) } else { format!("To {}", entry.agent.chip()) };
    row()
        .gap(8.0)
        .child(text(lead).font(META_BOLD).color(t.text_secondary))
        .child(text(first_line(&entry.agent.summary, 200)).font(META).color(t.text_secondary.alpha(0.7)).truncate())
}

fn block_el(block: &Block, open: bool, gap: f32, last: bool, increased: bool) -> El {
    let t = tok::theme(true);
    let body = match &block.kind {
        Kind::User(body) => col().w_full().items_end().child(col().max_w(620.0).fill(t.card).radius(14.0).px(14.0).py(9.0).child(markdown(body, increased))),
        Kind::Assistant(body) => {
            let mut c = col().w_full().gap(4.0).child(markdown(body, increased));
            if let Some(took) = &block.finished {
                c = c.child(text(format!("Took {took}")).font(META).color(t.text_secondary));
            }
            c
        }
        Kind::Work(work) => work_el(&block.id, work, open),
        Kind::Agent(entry) => agent_el(entry),
        Kind::AgentFold { label, members, .. } => {
            let toggle = block.id.clone();
            let head = row()
                .key(format!("fold-{}", block.id))
                .child(text(format!("{} {label}", if open { "▾" } else { "▸" })).font(META).color(t.text_secondary))
                .on_press(move |s: &mut App| {
                    if !s.open.remove(&toggle) {
                        s.open.insert(toggle.clone());
                    }
                    rebuild_open(s, &toggle);
                });
            let mut c = col().w_full().gap(4.0).child(head);
            if open {
                c = c.children(members.iter().map(agent_el));
            }
            c
        }
        Kind::Annotation(note) | Kind::Notice(note) => col().child(text(note.clone()).font(META).color(t.text_secondary).wrap()),
        Kind::Instructions(_) => col().child(text("Instructions").font(META).color(t.text_secondary)),
        Kind::Image(_) => col().child(text("Image attached").font(META).color(t.text_secondary)),
        Kind::File(item) => col().child(text(feed::attach::sent_name(item)).font(META).color(t.text_secondary)),
        Kind::Files(files) => col().child(text(files.iter().map(|f| f.name.as_str()).collect::<Vec<_>>().join(", ")).font(META).color(t.text_secondary)),
        Kind::Unknown { title, .. } => col().child(text(title.clone()).font(META).color(t.text_secondary)),
    };
    let row = col().w_full().max_w(860.0).px(20.0).pt(gap).child(body);
    if last { row.streaming(true) } else { row }
}

fn gap_for(blocks: &[Block], ix: usize) -> f32 {
    match ix.checked_sub(1).and_then(|i| blocks.get(i)) {
        None => 0.0,
        Some(_) if blocks[ix].is_message_in() => 20.0,
        Some(p) if p.is_message_in() => 12.0,
        Some(_) => 8.0,
    }
}

fn view(s: &App, cx: &mut Cx<App>) -> El {
    let t = tok::theme(true);
    let increased = cx.system_increased_contrast();
    let working = s.transcript.turn_open;
    let header = row()
        .w_full()
        .h(40.0)
        .gap(12.0)
        .px(16.0)
        .child(text("Rune").font(TITLE).color(t.text))
        .child(text(if working { "Working" } else { "Idle" }).font(META).color(if working { rune::oklch(0.72, 0.13, 260.0) } else { t.text_secondary }))
        .child(text(format!("{} · {} blocks · {} items", s.status, s.blocks.len(), s.transcript.items().len())).font(META).color(t.text_secondary));
    let count = s.blocks.len();
    let stack = col().key("rows").w_full().growth(GrowthSpec::default()).selection_group(true).virtual_rows(
        count,
        60.0,
        |s: &App, i| s.blocks[i].id.as_str().into(),
        move |s: &App, i| {
            let block = &s.blocks[i];
            let open = s.open.contains(&block.id);
            let last = i + 1 == s.blocks.len() && s.transcript.turn_open;
            let gap = gap_for(&s.blocks, i);
            memo(&block.id, (s.revs[i], open, last, gap.to_bits()), || block_el(block, open, gap, last, increased))
        },
    );
    let transcript = col().key("transcript").w_full().grow(1.0).min_h(0.0).scroll(ScrollSpec { stick_bottom: true }).child(stack);
    col().w_full().h_full().fill(t.bg).child(header).child(col().w_full().h(1.0).fill(t.separator)).child(transcript)
}

fn on_key(s: &mut App, key: &KeyEvent, _: &mut Tx) -> bool {
    if key.is_command('f') && key.mods.shift {
        s.focus = !s.focus;
        s.blocks.clear();
        s.revs.clear();
        rebuild(s);
        return true;
    }
    false
}

fn main() {
    rune::run(AppSpec::<App> { poll: Some(poll), on_key: Some(on_key), ..AppSpec::new("Bake: Rune", (1100.0, 820.0), init, view) });
}

#[cfg(test)]
mod tests {
    use super::*;
    use rune::{Env, Key, KeyEvent, Mods, Runtime};

    fn keyed_tops(rt: &Runtime<App>) -> Vec<(f32, String)> {
        let mut rows: Vec<(f32, String)> = rt
            .elements()
            .into_iter()
            .filter_map(|e| {
                let key = e.key.clone()?;
                (key.starts_with("item:") || key.starts_with("tools:")).then(|| (e.rect[1], key))
            })
            .filter(|(y, _)| *y >= 40.0 && *y < 820.0)
            .collect();
        rows.sort_by(|a, b| a.0.partial_cmp(&b.0).unwrap());
        rows
    }

    #[test]
    fn selection_spans_messages_and_page_up_moves() {
        let Ok(path) = std::env::var("BAKE_REPLAY") else { return };
        assert!(std::path::Path::new(&path).exists());
        let spec = AppSpec::<App> { poll: Some(poll), on_key: Some(on_key), ..AppSpec::new("Bake: Rune", (1100.0, 820.0), init, view) };
        let mut rt = rune::App::new(spec).headless_runtime((1100.0, 820.0), Env { system_dark: true, scale: 1.0 });
        let clipboard = rune::clipboard::memory();
        rt.use_clipboard(&clipboard);
        for _ in 0..(60 * 12) {
            rt.frame(1.0 / 60.0);
        }
        let before = keyed_tops(&rt);
        rt.key(KeyEvent { key: Key::PageUp, mods: Mods::default() });
        for _ in 0..30 {
            rt.frame(1.0 / 60.0);
        }
        let after = keyed_tops(&rt);
        println!("page up: first {:?} -> {:?}", before.first(), after.first());
        assert_ne!(before.first().map(|r| r.1.clone()), after.first().map(|r| r.1.clone()));
        let (x, y) = rt
            .elements()
            .into_iter()
            .filter(|e| e.role == "text" && e.rect[1] > 60.0 && e.rect[1] < 300.0 && e.rect[0] < 100.0 && e.rect[2] > 300.0)
            .map(|e| (e.rect[0] + 4.0, e.rect[1] + 6.0))
            .next()
            .unwrap_or((60.0, 120.0));
        let (ex, ey) = rt
            .elements()
            .into_iter()
            .filter(|e| e.role == "text" && e.rect[1] > y + 200.0 && e.rect[1] < 790.0)
            .map(|e| (e.rect[0] + 30.0, e.rect[1] + 6.0))
            .fold((x, y), |acc, p| if p.1 > acc.1 { p } else { acc });
        println!("drag {x},{y} -> {ex},{ey}");
        rt.pointer_move(x, y);
        rt.pointer_down(x, y);
        rt.frame(1.0 / 60.0);
        for k in 1..=20 {
            let f = k as f32 / 20.0;
            rt.pointer_move(x + (ex - x) * f, y + (ey - y) * f);
            rt.frame(1.0 / 60.0);
        }
        rt.pointer_up(ex, ey);
        rt.frame(1.0 / 60.0);
        let selected = rt.selected_text().unwrap_or_default();
        rt.key(KeyEvent { key: Key::Command('c'), mods: Mods { cmd: true, ..Mods::default() } });
        for _ in 0..3 {
            rt.frame(1.0 / 60.0);
        }
        let copied: String = clipboard.text().map(|t| t.as_str().to_owned()).unwrap_or_default();
        let lines = selected.lines().filter(|l| !l.trim().is_empty()).count();
        println!("selection: {} chars, {} lines, copied {} chars; starts {:?}", selected.len(), lines, copied.len(), selected.chars().take(60).collect::<String>());
        std::fs::write("/Volumes/4/caches/agenthail-v2-fixtures/rune-selection.txt", &selected).ok();
        assert!(lines >= 3);
        assert_eq!(copied, selected);
    }

    #[test]
    fn select_all_and_offscreen_copy_reach_the_whole_transcript() {
        let Ok(_) = std::env::var("BAKE_REPLAY") else { return };
        let spec = AppSpec::<App> { poll: Some(poll), on_key: Some(on_key), ..AppSpec::new("Bake: Rune", (1100.0, 820.0), init, view) };
        let mut rt = rune::App::new(spec).headless_runtime((1100.0, 820.0), Env { system_dark: true, scale: 1.0 });
        let clipboard = rune::clipboard::memory();
        rt.use_clipboard(&clipboard);
        for _ in 0..(60 * 9) {
            rt.frame(1.0 / 60.0);
        }
        let (x, y) = rt
            .elements()
            .into_iter()
            .filter(|e| e.role == "text" && e.rect[1] > 60.0 && e.rect[1] < 300.0 && e.rect[0] < 100.0 && e.rect[2] > 300.0)
            .map(|e| (e.rect[0] + 4.0, e.rect[1] + 6.0))
            .next()
            .unwrap();
        rt.pointer_move(x, y);
        rt.pointer_down(x, y);
        rt.frame(1.0 / 60.0);
        rt.pointer_move(x + 40.0, y + 300.0);
        rt.frame(1.0 / 60.0);
        rt.pointer_up(x + 40.0, y + 300.0);
        rt.frame(1.0 / 60.0);
        let picked = rt.selected_text().unwrap_or_default();
        for _ in 0..40 {
            rt.key(KeyEvent { key: Key::PageUp, mods: Mods::default() });
            rt.frame(1.0 / 60.0);
        }
        for _ in 0..30 {
            rt.frame(1.0 / 60.0);
        }
        rt.key(KeyEvent { key: Key::Command('c'), mods: Mods { cmd: true, ..Mods::default() } });
        for _ in 0..3 {
            rt.frame(1.0 / 60.0);
        }
        let after_scroll: String = clipboard.text().map(|t| t.as_str().to_owned()).unwrap_or_default();
        println!("offscreen copy: selected {} chars before scrolling away, copied {} chars after 40 page ups (equal: {})", picked.len(), after_scroll.len(), picked == after_scroll);
        rt.key(KeyEvent { key: Key::Command('a'), mods: Mods { cmd: true, ..Mods::default() } });
        rt.frame(1.0 / 60.0);
        rt.key(KeyEvent { key: Key::Command('c'), mods: Mods { cmd: true, ..Mods::default() } });
        for _ in 0..3 {
            rt.frame(1.0 / 60.0);
        }
        let all: String = clipboard.text().map(|t| t.as_str().to_owned()).unwrap_or_default();
        let first = all.contains("Prompt 0:");
        let last = all.contains("Live prompt");
        println!("select all: copied {} chars; has first prompt {first}, has live prompt {last}", all.len());
    }
}
