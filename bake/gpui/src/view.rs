use std::collections::HashSet;
use std::time::Instant;

use feed::blocks::{AgentEntry, Block, Kind, Work};
use feed::source::{Live, Message, Replay};
use feed::summary::Entry;
use feed::Transcript;
use gpui::{
    AnyElement, Context, FocusHandle, FollowMode, InteractiveElement, IntoElement, ListAlignment, ListState, ParentElement, Render,
    SharedString, StatefulInteractiveElement, Styled, Window, div, list, prelude::FluentBuilder as _, px,
};
use gpui_component::text::TextView;

use crate::Source;
use crate::metrics::Metrics;
use crate::palette::{Palette, radius, space, ty};

gpui::actions!(transcript, [PageUp, PageDown, JumpToLatest, ToggleFocus]);

pub struct TranscriptView {
    transcript: Transcript,
    blocks: Vec<Block>,
    focus_mode: bool,
    open: HashSet<String>,
    list: ListState,
    focus: FocusHandle,
    status: String,
    pending_paint: Option<Instant>,
    metrics: Metrics,
    finished: bool,
}

fn start(source: Source) -> async_channel::Receiver<(Instant, Message)> {
    let (tx, rx) = async_channel::unbounded();
    let (std_tx, std_rx) = std::sync::mpsc::channel::<Message>();
    std::thread::spawn(move || {
        for message in std_rx {
            if tx.send_blocking((Instant::now(), message)).is_err() {
                return;
            }
        }
    });
    std::thread::spawn(move || match source {
        Source::Live(id) => match Live::from_env(id) {
            Ok(live) => live.run(std_tx),
            Err(error) => {
                let _ = std_tx.send(Message::Status(error.to_string()));
            }
        },
        Source::Replay { path, speed } => {
            if let Err(error) = (Replay { path, speed }).run(std_tx.clone()) {
                let _ = std_tx.send(Message::Status(error.to_string()));
            }
        }
    });
    rx
}

impl TranscriptView {
    pub fn manual(window: &mut Window, cx: &mut Context<Self>) -> Self {
        let list = ListState::new(0, ListAlignment::Bottom, px(1200.));
        list.set_follow_mode(FollowMode::Tail);
        let focus = cx.focus_handle();
        window.focus(&focus, cx);
        Self {
            transcript: Transcript::default(),
            blocks: Vec::new(),
            focus_mode: true,
            open: HashSet::new(),
            list,
            focus,
            status: "headless".into(),
            pending_paint: None,
            metrics: Metrics::new(),
            finished: false,
        }
    }

    pub fn feed(&mut self, seq: u64, events: Vec<feed::wire::Event>, cx: &mut Context<Self>) -> bool {
        let changed = self.transcript.apply(seq, events);
        if changed {
            self.rebuild();
            cx.notify();
        }
        changed
    }

    pub fn scroll_up(&mut self, distance: f32, cx: &mut Context<Self>) {
        self.list.scroll_by(px(-distance));
        self.list.pause_following_tail();
        cx.notify();
    }

    pub fn visible(&self) -> Vec<(String, f32)> {
        let viewport = self.list.viewport_bounds();
        let top = self.list.logical_scroll_top().item_ix.min(self.blocks.len());
        let from = top.saturating_sub(80);
        let to = (top + 80).min(self.blocks.len());
        (from..to)
            .filter_map(|ix| {
                let b = self.list.bounds_for_item(ix)?;
                let y = f32::from(b.origin.y - viewport.origin.y);
                let bottom = y + f32::from(b.size.height);
                (bottom > 0.0 && y < f32::from(viewport.size.height)).then(|| (self.blocks[ix].id.clone(), y))
            })
            .collect()
    }

    pub fn new(source: Source, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let list = ListState::new(0, ListAlignment::Bottom, px(1200.));
        list.set_follow_mode(FollowMode::Tail);
        let focus = cx.focus_handle();
        window.focus(&focus, cx);
        let rx = start(source);
        cx.spawn(async move |this, cx| {
            while let Ok(first) = rx.recv().await {
                let mut batch = vec![first];
                while let Ok(next) = rx.try_recv() {
                    batch.push(next);
                }
                if this.update(cx, |this, cx| this.receive(batch, cx)).is_err() {
                    return;
                }
            }
        })
        .detach();
        let env_secs = |name: &str| std::env::var(name).ok().and_then(|v| v.parse::<f64>().ok());
        if let Some(at) = env_secs("BAKE_SCROLL_AT") {
            cx.spawn(async move |this, cx| {
                cx.background_executor().timer(std::time::Duration::from_secs_f64(at)).await;
                let _ = this.update(cx, |this, cx| {
                    let height = this.list.viewport_bounds().size.height;
                    this.list.scroll_by(-height * 1.5);
                    this.list.pause_following_tail();
                    cx.notify();
                });
                loop {
                    cx.background_executor().timer(std::time::Duration::from_secs(2)).await;
                    let alive = this.update(cx, |this, _| {
                        let top = this.list.logical_scroll_top();
                        let y = this.list.bounds_for_item(top.item_ix).map(|b| f32::from(b.origin.y)).unwrap_or(f32::NAN);
                        let id = this.blocks.get(top.item_ix).map(|b| b.id.clone()).unwrap_or_default();
                        this.metrics.note(&format!("anchor\t{}\t{}\t{:.1}\t{:.1}\tblocks={}", top.item_ix, id, f32::from(top.offset_in_item), y, this.blocks.len()));
                    });
                    if alive.is_err() {
                        return;
                    }
                }
            })
            .detach();
        }
        if let Some(at) = env_secs("BAKE_QUIT_AT") {
            cx.spawn(async move |this, cx| {
                cx.background_executor().timer(std::time::Duration::from_secs_f64(at)).await;
                let _ = this.update(cx, |this, cx| {
                    eprintln!("{}", this.metrics.summary("gpui"));
                    cx.quit();
                });
            })
            .detach();
        }
        Self {
            transcript: Transcript::default(),
            blocks: Vec::new(),
            focus_mode: true,
            open: HashSet::new(),
            list,
            focus,
            status: "connecting".into(),
            pending_paint: None,
            metrics: Metrics::new(),
            finished: false,
        }
    }

    fn receive(&mut self, batch: Vec<(Instant, Message)>, cx: &mut Context<Self>) {
        let mut changed = false;
        let mut first: Option<Instant> = None;
        for (at, message) in batch {
            match message {
                Message::Batch(b) => {
                    self.metrics.batch();
                    if self.transcript.apply(b.seq, b.events) {
                        changed = true;
                        first.get_or_insert(at);
                    }
                }
                Message::Status(status) => {
                    if status == "replay finished" {
                        self.finished = true;
                        eprintln!("{}", self.metrics.summary("gpui"));
                    }
                    self.status = status;
                    cx.notify();
                }
            }
        }
        if changed {
            self.rebuild();
            self.pending_paint = self.pending_paint.or(first);
            cx.notify();
        }
    }

    fn rebuild(&mut self) {
        let next = self.transcript.blocks(self.focus_mode);
        let same = self.blocks.iter().zip(next.iter()).take_while(|(a, b)| a == b).count();
        let old_len = self.blocks.len();
        self.blocks = next;
        if same == old_len && same == self.blocks.len() {
            return;
        }
        self.list.splice(same..old_len, self.blocks.len() - same);
    }

    fn toggle(&mut self, id: String, cx: &mut Context<Self>) {
        if !self.open.remove(&id) {
            self.open.insert(id.clone());
        }
        if let Some(ix) = self.blocks.iter().position(|b| b.id == id) {
            self.list.splice(ix..ix + 1, 1);
        }
        cx.notify();
    }

    fn page(&mut self, up: bool, cx: &mut Context<Self>) {
        let height = self.list.viewport_bounds().size.height * 0.9;
        self.list.scroll_by(if up { -height } else { height });
        cx.notify();
    }

    fn jump(&mut self, cx: &mut Context<Self>) {
        self.list.scroll_to_end();
        self.list.set_follow_mode(FollowMode::Tail);
        cx.notify();
    }

    fn render_block(&self, ix: usize, palette: &Palette, cx: &mut Context<Self>) -> AnyElement {
        let Some(block) = self.blocks.get(ix) else { return div().into_any_element() };
        let previous = ix.checked_sub(1).and_then(|i| self.blocks.get(i));
        let gap = match previous {
            None => 0.,
            Some(_) if block.is_message_in() => space::XL,
            Some(p) if p.is_message_in() => space::M,
            Some(_) => space::S,
        };
        let row = div().id(SharedString::from(block.id.clone())).w_full().max_w(px(860.)).mx_auto().px(px(space::XL)).pt(px(gap));
        let body: AnyElement = match &block.kind {
            Kind::User(text) => div()
                .flex()
                .justify_end()
                .child(
                    div()
                        .max_w(px(620.))
                        .bg(palette.bubble)
                        .rounded(px(radius::CARD + 4.))
                        .px(px(space::M + 2.))
                        .py(px(space::S + 1.))
                        .text_size(px(ty::PROSE))
                        .child(TextView::markdown(SharedString::from(format!("md-{}", block.id)), text.clone()).selectable(true)),
                )
                .into_any_element(),
            Kind::Assistant(text) => div()
                .text_size(px(ty::PROSE))
                .line_height(px(ty::PROSE * 1.55))
                .child(TextView::markdown(SharedString::from(format!("md-{}", block.id)), text.clone()).selectable(true))
                .when_some(block.finished.clone(), |d, took| d.child(div().pt(px(space::XS)).text_size(px(ty::META)).text_color(palette.muted).child(format!("Took {took}"))))
                .into_any_element(),
            Kind::Work(work) => self.render_work(&block.id, work, palette, cx),
            Kind::Agent(entry) => render_agent(entry, palette),
            Kind::AgentFold { label, members, .. } => {
                let open = self.open.contains(&block.id);
                let id = block.id.clone();
                div()
                    .flex()
                    .flex_col()
                    .gap(px(space::XS))
                    .child(
                        div()
                            .id(SharedString::from(format!("fold-{}", block.id)))
                            .cursor_pointer()
                            .text_size(px(ty::META))
                            .text_color(palette.text2)
                            .child(format!("{} {label}", if open { "▾" } else { "▸" }))
                            .on_click(cx.listener(move |this, _, _, cx| this.toggle(id.clone(), cx))),
                    )
                    .when(open, |d| d.children(members.iter().map(|m| render_agent(m, palette))))
                    .into_any_element()
            }
            Kind::Annotation(text) | Kind::Notice(text) => {
                div().text_size(px(ty::META)).text_color(palette.muted).child(text.clone()).into_any_element()
            }
            Kind::Instructions(_) => div().text_size(px(ty::META)).text_color(palette.muted).child("Instructions").into_any_element(),
            Kind::Unknown { title, .. } => div().text_size(px(ty::META)).text_color(palette.muted).child(title.clone()).into_any_element(),
        };
        row.child(body).into_any_element()
    }

    fn render_work(&self, id: &str, work: &Work, palette: &Palette, cx: &mut Context<Self>) -> AnyElement {
        let open = self.open.contains(id) || !work.folds;
        let toggle_id = id.to_string();
        let header = if work.live { format!("Working · {}", work.label) } else { format!("{} · {}", work.line, work.label) };
        let entries: Vec<&Entry> = if open {
            work.entries.iter().collect()
        } else if work.live {
            work.entries.iter().rev().take(1).collect()
        } else {
            Vec::new()
        };
        div()
            .flex()
            .flex_col()
            .gap(px(2.))
            .child(
                div()
                    .id(SharedString::from(format!("work-{id}")))
                    .cursor_pointer()
                    .flex()
                    .gap(px(space::S))
                    .text_size(px(ty::META))
                    .text_color(if work.live { palette.working } else { palette.text2 })
                    .child(if open && work.folds { "▾" } else { "▸" })
                    .child(header)
                    .on_click(cx.listener(move |this, _, _, cx| this.toggle(toggle_id.clone(), cx))),
            )
            .children(entries.into_iter().map(|entry| render_entry(entry, palette)))
            .into_any_element()
    }
}

fn first_line(text: &str, max: usize) -> String {
    let line = text.lines().find(|l| !l.trim().is_empty()).unwrap_or("").trim();
    if line.chars().count() <= max { line.to_string() } else { format!("{}…", line.chars().take(max).collect::<String>()) }
}

fn render_entry(entry: &Entry, palette: &Palette) -> AnyElement {
    let row = div().pl(px(space::L)).flex().gap(px(space::S)).text_size(px(ty::META)).text_color(palette.text2);
    match entry {
        Entry::Call { call, results } => row
            .child(div().font_weight(gpui::FontWeight::MEDIUM).flex_none().child(call.title.clone()))
            .child(div().font_family("Menlo").truncate().text_color(palette.muted).child(first_line(&call.text, 140)))
            .when(results.iter().any(feed::summary::is_failure), |d| d.child(div().text_color(palette.failed).child("failed")))
            .into_any_element(),
        Entry::Output(item) => row.child(div().truncate().text_color(palette.muted).child(first_line(&item.text, 140))).into_any_element(),
        Entry::Reasoning(item) => row.italic().text_color(palette.muted).child(first_line(&item.text, 160)).into_any_element(),
    }
}

fn render_agent(entry: &AgentEntry, palette: &Palette) -> AnyElement {
    let incoming = entry.agent.is_incoming();
    let lead = if incoming { format!("From {}", entry.agent.chip()) } else { format!("To {}", entry.agent.chip()) };
    div()
        .flex()
        .gap(px(space::S))
        .text_size(px(ty::META))
        .child(div().flex_none().font_weight(gpui::FontWeight::SEMIBOLD).text_color(palette.text2).child(lead))
        .child(div().truncate().text_color(palette.muted).child(first_line(&entry.agent.summary, 200)))
        .into_any_element()
}

impl Render for TranscriptView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.metrics.render();
        let palette = Palette::for_appearance(window.appearance());
        if let Some(received) = self.pending_paint.take() {
            let view = cx.entity().downgrade();
            let blocks = self.blocks.len();
            window.on_next_frame(move |_, cx| {
                if let Some(view) = view.upgrade() {
                    view.update(cx, |this, _| this.metrics.painted(received, blocks));
                }
            });
        }
        let view = cx.entity().downgrade();
        let following = self.list.is_following_tail() || self.list.is_scrolled_to_end() != Some(false);
        let working = self.transcript.turn_open;
        let header = div()
            .h(px(40.))
            .flex_none()
            .flex()
            .items_center()
            .gap(px(space::M))
            .pl(px(84.))
            .pr(px(space::L))
            .border_b_1()
            .border_color(palette.line)
            .child(div().text_size(px(ty::TITLE)).font_weight(gpui::FontWeight::SEMIBOLD).child("GPUI"))
            .child(div().text_size(px(ty::META)).text_color(if working { palette.working } else { palette.muted }).child(if working { "Working" } else { "Idle" }))
            .child(div().text_size(px(ty::META)).text_color(palette.muted).child(format!("{} · {} blocks · {} items", self.status, self.blocks.len(), self.transcript.items().len())))
            .child(div().flex_1())
            .child(
                div()
                    .id("focus-toggle")
                    .cursor_pointer()
                    .text_size(px(ty::META))
                    .text_color(palette.text2)
                    .child(if self.focus_mode { "Focus" } else { "Full" })
                    .on_click(cx.listener(|this, _, _, cx| this.toggle_focus(cx))),
            );
        let transcript = list(self.list.clone(), move |ix, window, cx| {
            let palette = Palette::for_appearance(window.appearance());
            view.upgrade()
                .map(|view| view.update(cx, |this, cx| this.render_block(ix, &palette, cx)))
                .unwrap_or_else(|| div().into_any_element())
        })
        .size_full();
        let pill = (!following).then(|| {
            div()
                .absolute()
                .bottom(px(space::L))
                .right(px(space::XL))
                .id("jump")
                .cursor_pointer()
                .bg(palette.raised)
                .border_1()
                .border_color(palette.line)
                .rounded(px(radius::CARD))
                .px(px(space::M))
                .py(px(space::XS + 2.))
                .text_size(px(ty::META))
                .child("↓ Jump to latest")
                .on_click(cx.listener(|this, _, _, cx| this.jump(cx)))
        });
        div()
            .track_focus(&self.focus)
            .on_action(cx.listener(|this, _: &PageUp, _, cx| this.page(true, cx)))
            .on_action(cx.listener(|this, _: &PageDown, _, cx| this.page(false, cx)))
            .on_action(cx.listener(|this, _: &JumpToLatest, _, cx| this.jump(cx)))
            .on_action(cx.listener(|this, _: &ToggleFocus, _, cx| this.toggle_focus(cx)))
            .size_full()
            .flex()
            .flex_col()
            .bg(palette.window)
            .text_color(palette.text)
            .child(header)
            .child(div().relative().flex_1().min_h_0().child(transcript).children(pill))
    }
}

impl TranscriptView {
    fn toggle_focus(&mut self, cx: &mut Context<Self>) {
        self.focus_mode = !self.focus_mode;
        self.blocks.clear();
        let next = self.transcript.blocks(self.focus_mode);
        self.list.reset(next.len());
        self.blocks = next;
        cx.notify();
    }
}
