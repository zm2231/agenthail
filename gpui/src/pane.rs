use std::collections::{HashMap, HashSet};
use std::time::{Duration, Instant};

use gpui::{
    AppContext as _,
    AnyElement, Context, Entity, FollowMode, InteractiveElement, IntoElement, ListAlignment, ListState,
    ParentElement, Render, Role, SharedString, StatefulInteractiveElement, Styled, Task, Window, div, list,
    prelude::FluentBuilder as _, px,
};
use gpui_component::input::{InputEvent, Textarea, TextareaState};
use gpui_component::text::TextView;
use serde_json::{Value, json};

use crate::api::{Api, StreamError, query};
use crate::model::{Detail, Request, Requests, Session, TimelineItem};
use crate::palette::{Palette, radius, space, ty};
use crate::probe;

enum StreamMessage {
    Changed,
    Gap,
}

pub struct SessionPane {
    api: Api,
    session: Session,
    rows: Vec<TimelineItem>,
    older: Vec<TimelineItem>,
    latest: Vec<TimelineItem>,
    older_cursor: Option<i64>,
    loading_older: bool,
    loading: bool,
    list: ListState,
    composer: Entity<TextareaState>,
    requests: Vec<Request>,
    answers: HashMap<String, String>,
    notice: Option<String>,
    opened_at: Instant,
    paint_pending: bool,
    stream_started: bool,
    reload_task: Option<Task<()>>,
    _tasks: Vec<Task<()>>,
}

fn shown(item: &TimelineItem) -> bool {
    match item.kind.as_str() {
        "message" | "reasoning" | "toolCall" | "attachment" | "notice" | "error" => true,
        "event" => item.role.as_deref() != Some("system") && !item.text.is_empty(),
        _ => false,
    }
}

impl SessionPane {
    pub fn new(api: Api, session: Session, opened_at: Instant, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let list = ListState::new(0, ListAlignment::Bottom, px(800.));
        list.set_follow_mode(FollowMode::Tail);
        let composer = cx.new(|cx| {
            TextareaState::new(window, cx)
                .auto_grow(1, 8)
                .submit_on_enter(true)
                .placeholder("Message the agent")
        });
        cx.subscribe_in(&composer, window, |this, _, event: &InputEvent, window, cx| {
            if let InputEvent::PressEnter { shift: false, .. } = event {
                this.submit(window, cx);
            }
        })
        .detach();
        let weak = cx.entity().downgrade();
        list.set_scroll_handler(move |event, _, cx| {
            if event.visible_range.start < 4 {
                if let Some(this) = weak.upgrade() {
                    this.update(cx, |this, cx| this.load_older(cx));
                }
            }
        });
        let mut this = Self {
            api,
            session,
            rows: Vec::new(),
            older: Vec::new(),
            latest: Vec::new(),
            older_cursor: None,
            loading_older: false,
            loading: true,
            list,
            composer,
            requests: Vec::new(),
            answers: HashMap::new(),
            notice: None,
            opened_at,
            paint_pending: true,
            stream_started: false,
            reload_task: None,
            _tasks: Vec::new(),
        };
        this.load(cx);
        this.load_requests(cx);
        this
    }

    pub fn session_changed(&mut self, session: Session, cx: &mut Context<Self>) {
        let waiting_changed = session.waiting_on != self.session.waiting_on;
        self.session = session;
        if waiting_changed {
            self.load_requests(cx);
        }
        cx.notify();
    }

    fn load(&mut self, cx: &mut Context<Self>) {
        let api = self.api.clone();
        let path = format!("/api/v1/session?id={}&limit=20&timeline=1", query(&self.session.id));
        let started = Instant::now();
        let id = self.session.id.clone();
        let task = cx.spawn(async move |this, cx| {
            let detail = cx.background_spawn(async move { api.get::<Detail>(&path) }).await;
            probe::record("detail-loaded", &format!("{id}\t{:.1}ms", started.elapsed().as_secs_f64() * 1000.));
            let _ = this.update(cx, |this, cx| {
                this.loading = false;
                match detail {
                    Ok(detail) => this.apply_detail(detail, cx),
                    Err(error) => this.notice = Some(error.to_string()),
                }
                cx.notify();
            });
        });
        self.reload_task = Some(task);
    }

    fn apply_detail(&mut self, detail: Detail, cx: &mut Context<Self>) {
        let timeline = detail.timeline.clone();
        let fresh: Vec<TimelineItem> = timeline.as_ref().map(|t| t.items.clone()).unwrap_or_default();
        let fresh_ids: HashSet<&str> = fresh.iter().map(|i| i.id.as_str()).collect();
        if self.latest.iter().any(|i| fresh_ids.contains(i.id.as_str())) {
            let known: HashSet<String> = self.older.iter().map(|i| i.id.clone()).collect();
            let carried: Vec<TimelineItem> = self
                .latest
                .iter()
                .filter(|i| !fresh_ids.contains(i.id.as_str()) && !known.contains(&i.id))
                .cloned()
                .collect();
            if self.older.is_empty() && self.older_cursor.is_none() {
                self.older_cursor = timeline.as_ref().and_then(|t| t.next_before);
            }
            self.older.extend(carried);
        } else {
            self.older.clear();
            self.older_cursor = timeline.as_ref().and_then(|t| t.next_before);
        }
        self.latest = fresh;
        self.rebuild_rows();
        if !self.stream_started {
            self.stream_started = true;
            self.start_stream(detail.journal_seq.unwrap_or(0), cx);
        }
    }

    fn rebuild_rows(&mut self) {
        let rows: Vec<TimelineItem> = self.older.iter().chain(self.latest.iter()).filter(|i| shown(i)).cloned().collect();
        let prefix = self.rows.iter().zip(rows.iter()).take_while(|(a, b)| a == b).count();
        let old_len = self.rows.len();
        self.rows = rows;
        if old_len == 0 {
            self.list.reset(self.rows.len());
        } else {
            self.list.splice(prefix..old_len, self.rows.len() - prefix);
        }
    }

    fn load_older(&mut self, cx: &mut Context<Self>) {
        let Some(cursor) = self.older_cursor.filter(|c| *c > 0) else { return };
        if self.loading_older || self.loading {
            return;
        }
        self.loading_older = true;
        let api = self.api.clone();
        let id = self.session.id.clone();
        let task = cx.spawn(async move |this, cx| {
            let mut next = Some(cursor);
            let mut collected: Vec<TimelineItem> = Vec::new();
            let mut error = None;
            for _ in 0..8 {
                let Some(before) = next.filter(|b| *b > 0) else { break };
                let path = format!("/api/v1/session?id={}&limit=20&timeline=1&timelineBefore={before}", query(&id));
                let api = api.clone();
                match cx.background_spawn(async move { api.get::<Detail>(&path) }).await {
                    Ok(page) => {
                        let Some(timeline) = page.timeline else { break };
                        if let Some(reason) = timeline.unavailable_reason {
                            error = Some(reason);
                            next = None;
                            break;
                        }
                        next = timeline.next_before;
                        let any_shown = timeline.items.iter().any(shown);
                        collected = timeline.items.into_iter().chain(collected).collect();
                        if any_shown {
                            break;
                        }
                    }
                    Err(e) => {
                        error = Some(e.to_string());
                        break;
                    }
                }
            }
            let _ = this.update(cx, |this, cx| {
                this.loading_older = false;
                this.older_cursor = next;
                if let Some(error) = error {
                    this.notice = Some(error);
                }
                let known: HashSet<String> = this.older.iter().chain(this.latest.iter()).map(|i| i.id.clone()).collect();
                let fresh: Vec<TimelineItem> = collected.into_iter().filter(|i| !known.contains(&i.id)).collect();
                let added = fresh.iter().filter(|i| shown(i)).count();
                this.older = fresh.into_iter().chain(this.older.drain(..)).collect();
                this.rows = this.older.iter().chain(this.latest.iter()).filter(|i| shown(i)).cloned().collect();
                this.list.splice(0..0, added);
                cx.notify();
            });
        });
        self._tasks.push(task);
    }

    fn start_stream(&mut self, after: u64, cx: &mut Context<Self>) {
        let (tx, rx) = async_channel::unbounded::<StreamMessage>();
        let api = self.api.clone();
        let id = self.session.id.clone();
        std::thread::spawn(move || {
            let mut cursor = after;
            let mut delay = Duration::from_millis(500);
            loop {
                let path = format!("/api/v1/session-stream?id={}&after={cursor}", query(&id));
                let result = api.stream(&path, cursor, |event| {
                    if let Some(seq) = event["seq"].as_u64() {
                        cursor = cursor.max(seq);
                    }
                    delay = Duration::from_millis(500);
                    tx.send_blocking(StreamMessage::Changed).is_ok()
                });
                if tx.is_closed() {
                    return;
                }
                match result {
                    Err(StreamError::Gap) => {
                        let _ = tx.send_blocking(StreamMessage::Gap);
                        return;
                    }
                    Err(StreamError::Unsupported) => return,
                    _ => {}
                }
                std::thread::sleep(delay);
                delay = (delay * 2).min(Duration::from_secs(10));
            }
        });
        let task = cx.spawn(async move |this, cx| {
            while let Ok(message) = rx.recv().await {
                let alive = this.update(cx, |this, cx| match message {
                    StreamMessage::Changed => this.schedule_reload(cx),
                    StreamMessage::Gap => {
                        this.stream_started = false;
                        this.load(cx);
                    }
                });
                if alive.is_err() {
                    return;
                }
            }
        });
        self._tasks.push(task);
    }

    fn schedule_reload(&mut self, cx: &mut Context<Self>) {
        if self.reload_task.is_some() {
            return;
        }
        let task = cx.spawn(async move |this, cx| {
            cx.background_executor().timer(Duration::from_millis(250)).await;
            let _ = this.update(cx, |this, cx| {
                this.reload_task = None;
                this.load(cx);
            });
        });
        self.reload_task = Some(task);
    }

    fn load_requests(&mut self, cx: &mut Context<Self>) {
        let api = self.api.clone();
        let path = format!("/api/v1/requests?sessionId={}", query(&self.session.id));
        let task = cx.spawn(async move |this, cx| {
            let requests = cx.background_spawn(async move { api.get::<Requests>(&path) }).await;
            let _ = this.update(cx, |this, cx| {
                this.requests = requests.map(|r| r.requests).unwrap_or_default();
                cx.notify();
            });
        });
        self._tasks.push(task);
    }

    fn submit(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let text = self.composer.read(cx).value().trim().to_string();
        if text.is_empty() {
            return;
        }
        self.composer.update(cx, |state, cx| state.set_value("", window, cx));
        let mut body = json!({"action": "send", "sessionId": self.session.id, "message": text, "author": "person"});
        let steering = self.session.is_working();
        if steering {
            body["busyDelivery"] = json!("steer");
        }
        self.notice = Some(if steering { "Steering…".into() } else { "Sending…".into() });
        let api = self.api.clone();
        let task = cx.spawn(async move |this, cx| {
            let result = cx.background_spawn(async move { api.post_action(body) }).await;
            let _ = this.update(cx, |this, cx| {
                this.notice = Some(match result {
                    Ok(receipt) => {
                        let status = receipt["result"]["status"].as_str().unwrap_or("sent").to_string();
                        format!("{} ({status})", if steering { "Steered" } else { "Sent" })
                    }
                    Err(error) => error.to_string(),
                });
                cx.notify();
            });
        });
        self._tasks.push(task);
        cx.notify();
    }

    fn respond(&mut self, request: Request, decision: Value, cx: &mut Context<Self>) {
        let api = self.api.clone();
        let body = json!({"action": "respond", "sessionId": self.session.id, "requestId": request.id, "decision": decision});
        let task = cx.spawn(async move |this, cx| {
            let result = cx.background_spawn(async move { api.post_action(body) }).await;
            let _ = this.update(cx, |this, cx| {
                match result {
                    Ok(reply) => {
                        this.notice = Some(format!("Answered ({})", reply["result"]["outcome"].as_str().unwrap_or("ok")));
                        this.requests.retain(|r| r.id != request.id);
                        this.answers.clear();
                    }
                    Err(error) => this.notice = Some(error.to_string()),
                }
                cx.notify();
            });
        });
        self._tasks.push(task);
    }

    fn render_item(&self, ix: usize, palette: &Palette) -> AnyElement {
        let Some(item) = self.rows.get(ix) else { return div().into_any_element() };
        let id = SharedString::from(format!("item-{}", item.id));
        let row = div().id(id.clone()).w_full().px(px(space::XL + 12.)).py(px(space::S));
        match (item.kind.as_str(), item.role.as_deref()) {
            ("message", Some("user")) | ("message", Some("peer")) => {
                let label = if item.role.as_deref() == Some("peer") {
                    format!("From {}", item.sender.clone().unwrap_or_else(|| "another agent".into()))
                } else {
                    "You".into()
                };
                row.role(Role::Article)
                    .aria_label(format!("{label}: {}", clip(&item.text, 400)))
                    .flex()
                    .flex_col()
                    .items_end()
                    .child(div().text_size(px(ty::META)).text_color(palette.muted).pb(px(space::XS)).child(label))
                    .child(
                        div()
                            .max_w(px(620.))
                            .bg(palette.bubble)
                            .rounded(px(radius::CARD + 4.))
                            .px(px(space::M + 2.))
                            .py(px(space::S + 1.))
                            .text_size(px(ty::PROSE))
                            .child(clip(&item.text, 8000)),
                    )
                    .into_any_element()
            }
            ("message", _) => row
                .role(Role::Article)
                .aria_label(clip(&item.text, 400))
                .text_size(px(ty::PROSE))
                .line_height(px(ty::PROSE * 1.58))
                .child(TextView::markdown(SharedString::from(format!("md-{}", item.id)), item.text.clone()).selectable(true))
                .into_any_element(),
            ("reasoning", _) => row
                .text_size(px(ty::BODY))
                .text_color(palette.muted)
                .italic()
                .child(clip(item.text.lines().next().unwrap_or("Thinking"), 200))
                .into_any_element(),
            ("toolCall", _) => row
                .role(Role::Group)
                .aria_label(format!("Tool {}", item.title))
                .flex()
                .gap(px(space::S))
                .text_size(px(ty::META))
                .text_color(palette.text2)
                .child(div().font_weight(gpui::FontWeight::MEDIUM).child(item.title.clone()))
                .child(div().font_family("Menlo").truncate().text_color(palette.muted).child(clip(item.text.lines().next().unwrap_or(""), 160)))
                .into_any_element(),
            _ => row
                .text_size(px(ty::META))
                .text_color(palette.muted)
                .child(format!("{} {}", item.title, clip(&item.text, 200)))
                .into_any_element(),
        }
    }

    fn render_requests(&self, palette: &Palette, cx: &mut Context<Self>) -> AnyElement {
        let mut column = div().flex().flex_col().gap(px(space::S));
        for request in &self.requests {
            let mut card = div()
                .id(SharedString::from(format!("request-{}", request.id)))
                .role(Role::Group)
                .aria_label(format!("Request: {}", request.title.clone().unwrap_or_else(|| request.tool.clone())))
                .bg(palette.attention_background)
                .border_1()
                .border_color(palette.attention_line)
                .rounded(px(radius::CARD))
                .p(px(space::M))
                .flex()
                .flex_col()
                .gap(px(space::S))
                .text_size(px(ty::BODY));
            if let Some(questions) = request.questions.clone().filter(|_| request.kind == "question") {
                for question in &questions {
                    card = card.child(div().font_weight(gpui::FontWeight::SEMIBOLD).child(question.question.clone()));
                    let mut options = div().flex().flex_wrap().gap(px(space::S));
                    for option in &question.options {
                        let chosen = self.answers.get(&question.question) == Some(&option.label);
                        let q = question.question.clone();
                        let label = option.label.clone();
                        options = options.child(
                            div()
                                .id(SharedString::from(format!("opt-{}-{}-{}", request.id, q, label)))
                                .role(Role::Button)
                                .aria_label(label.clone())
                                .px(px(space::M))
                                .py(px(space::XS + 1.))
                                .rounded(px(radius::CONTROL))
                                .border_1()
                                .border_color(palette.line)
                                .when(chosen, |d| d.bg(palette.accent).text_color(palette.on_accent))
                                .when(!chosen, |d| d.bg(palette.raised))
                                .cursor_pointer()
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.answers.insert(q.clone(), label.clone());
                                    cx.notify();
                                }))
                                .child(option.label.clone()),
                        );
                    }
                    card = card.child(options);
                }
                let complete = questions.iter().all(|q| self.answers.contains_key(&q.question));
                let request_for_submit = request.clone();
                card = card.child(
                    button(format!("submit-{}", request.id), "Submit", complete, palette).on_click(cx.listener(move |this, _, _, cx| {
                        if questions.iter().all(|q| this.answers.contains_key(&q.question)) {
                            let answers: serde_json::Map<String, Value> =
                                this.answers.iter().map(|(k, v)| (k.clone(), json!(v))).collect();
                            this.respond(request_for_submit.clone(), json!({"behavior": "allow", "answers": answers}), cx);
                        }
                    })),
                );
            } else {
                card = card.child(div().font_weight(gpui::FontWeight::SEMIBOLD).child(
                    request.title.clone().unwrap_or_else(|| format!("{} wants permission", request.tool)),
                ));
                let allow = request.clone();
                let deny = request.clone();
                card = card.child(
                    div()
                        .flex()
                        .gap(px(space::S))
                        .child(button(format!("allow-{}", request.id), "Allow", true, palette).on_click(cx.listener(move |this, _, _, cx| {
                            this.respond(allow.clone(), json!({"behavior": "allow"}), cx)
                        })))
                        .child(button(format!("deny-{}", request.id), "Deny", false, palette).on_click(cx.listener(move |this, _, _, cx| {
                            this.respond(deny.clone(), json!({"behavior": "deny"}), cx)
                        }))),
                );
            }
            column = column.child(card);
        }
        column.into_any_element()
    }
}

fn button(id: String, label: &'static str, primary: bool, palette: &Palette) -> gpui::Stateful<gpui::Div> {
    div()
        .id(id)
        .role(Role::Button)
        .aria_label(label)
        .px(px(space::M))
        .py(px(space::XS + 1.))
        .rounded(px(radius::CONTROL))
        .cursor_pointer()
        .when(primary, |d| d.bg(palette.accent).text_color(palette.on_accent))
        .when(!primary, |d| d.bg(palette.fill).text_color(palette.text))
        .child(label)
}

fn clip(text: &str, max: usize) -> String {
    if text.len() <= max {
        return text.to_string();
    }
    let mut end = max;
    while !text.is_char_boundary(end) {
        end -= 1;
    }
    format!("{}…", &text[..end])
}

impl Render for SessionPane {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let palette = Palette::for_appearance(window.appearance());
        if self.paint_pending && !self.rows.is_empty() {
            self.paint_pending = false;
            let opened = self.opened_at;
            let id = self.session.id.clone();
            let rows = self.rows.len();
            window.on_next_frame(move |_, _| {
                probe::record("first-paint", &format!("{id}\t{:.1}ms\t{rows} rows", opened.elapsed().as_secs_f64() * 1000.));
            });
        }
        let status = if self.session.is_working() { "Working" } else if self.session.open { "Idle" } else { "Closed" };
        let header = div()
            .h(px(40.))
            .flex_none()
            .flex()
            .items_center()
            .gap(px(space::S))
            .px(px(space::L))
            .border_b_1()
            .border_color(palette.line)
            .child(div().text_size(px(ty::TITLE)).font_weight(gpui::FontWeight::SEMIBOLD).child(self.session.title()))
            .child(div().text_size(px(ty::META)).text_color(if self.session.is_working() { palette.working } else { palette.muted }).child(status));
        let view = cx.entity().downgrade();
        let transcript = if self.loading && self.rows.is_empty() {
            div().flex_1().flex().items_center().justify_center().text_color(palette.muted).child("Loading…").into_any_element()
        } else {
            list(self.list.clone(), move |ix, window, cx| {
                let palette = Palette::for_appearance(window.appearance());
                view.upgrade()
                    .map(|view| view.read(cx).render_item(ix, &palette))
                    .unwrap_or_else(|| div().into_any_element())
            })
            .flex_1()
            .size_full()
            .into_any_element()
        };
        let older = self.loading_older.then(|| {
            div().text_size(px(ty::META)).text_color(palette.muted).px(px(space::L)).py(px(space::XS)).child("Loading older activity…")
        });
        let dock = div()
            .flex_none()
            .bg(palette.dock)
            .border_t_1()
            .border_color(palette.line)
            .p(px(space::M))
            .flex()
            .flex_col()
            .gap(px(space::S))
            .child(self.render_requests(&palette, cx))
            .when_some(self.notice.clone(), |d, notice| d.child(div().text_size(px(ty::META)).text_color(palette.text2).child(notice)))
            .child(
                div()
                    .id("composer")
                    .role(Role::Group)
                    .aria_label("Composer")
                    .child(Textarea::new(&self.composer)),
            );
        div()
            .size_full()
            .flex()
            .flex_col()
            .bg(palette.window)
            .child(header)
            .children(older)
            .child(div().flex_1().min_h_0().flex().flex_col().child(transcript))
            .child(dock)
    }
}
