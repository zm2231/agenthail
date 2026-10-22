use std::collections::{BTreeMap, HashSet};
use std::time::{Duration, Instant};

use gpui::{
    AppContext as _,
    AnyElement, Context, Entity, InteractiveElement, IntoElement, ParentElement, Render, Role, SharedString,
    StatefulInteractiveElement, Styled, Window, div, prelude::FluentBuilder as _, px,
};
use serde_json::Value;

use crate::api::{Api, StreamError};
use crate::model::{Session, Snapshot};
use crate::palette::{Palette, radius, space, ty};
use crate::pane::SessionPane;
use crate::probe;

enum CatalogMessage {
    Event(Value),
    Reset,
}

pub struct AppView {
    api: Api,
    sessions: Vec<Session>,
    attention: HashSet<String>,
    catalog_seq: u64,
    selected: Option<String>,
    pane: Option<Entity<SessionPane>>,
    error: Option<String>,
    focus: gpui::FocusHandle,
    _tasks: Vec<gpui::Task<()>>,
}

impl AppView {
    pub fn new(api: Api, window: &mut Window, cx: &mut Context<Self>) -> Self {
        cx.observe_window_appearance(window, |_, window, cx| {
            sync_theme(window, cx);
            cx.notify();
        })
        .detach();
        sync_theme(window, cx);
        cx.intercept_keystrokes(|event, window, _| {
            let key = event.keystroke.key.clone();
            let pressed = Instant::now();
            window.on_next_frame(move |window, _| {
                let frame = pressed.elapsed().as_secs_f64() * 1000.;
                window.on_next_frame(move |_, _| {
                    probe::record("key", &format!("{key}\tframe_start={frame:.2}ms\tpresented={:.2}ms", pressed.elapsed().as_secs_f64() * 1000.));
                });
            });
        })
        .detach();
        let mut this = Self {
            api,
            sessions: Vec::new(),
            attention: HashSet::new(),
            catalog_seq: 0,
            selected: None,
            pane: None,
            error: None,
            focus: cx.focus_handle(),
            _tasks: Vec::new(),
        };
        this.load_snapshot(cx);
        this
    }

    fn load_snapshot(&mut self, cx: &mut Context<Self>) {
        let api = self.api.clone();
        let task = cx.spawn(async move |this, cx| {
            let snapshot = cx.background_spawn(async move { api.get::<Snapshot>("/api/v1/snapshot") }).await;
            let _ = this.update(cx, |this, cx| {
                match snapshot {
                    Ok(snapshot) => {
                        this.catalog_seq = snapshot.catalog_seq.unwrap_or(0);
                        this.attention = snapshot
                            .attention
                            .iter()
                            .filter_map(|a| a["sessionId"].as_str().map(str::to_string))
                            .collect();
                        this.sessions = snapshot.sessions;
                        this.error = None;
                        this.start_catalog(cx);
                    }
                    Err(error) => this.error = Some(error.to_string()),
                }
                cx.notify();
            });
        });
        self._tasks.push(task);
    }

    fn start_catalog(&mut self, cx: &mut Context<Self>) {
        let (tx, rx) = async_channel::unbounded::<CatalogMessage>();
        let api = self.api.clone();
        let mut after = self.catalog_seq;
        std::thread::spawn(move || {
            let mut delay = Duration::from_millis(500);
            loop {
                let result = api.stream(&format!("/api/v1/catalog-events?after={after}"), after, |event| {
                    if let Some(seq) = event["seq"].as_u64() {
                        after = after.max(seq);
                    }
                    delay = Duration::from_millis(500);
                    tx.send_blocking(CatalogMessage::Event(event)).is_ok()
                });
                if tx.is_closed() {
                    return;
                }
                if let Err(StreamError::Gap) = result {
                    after = 0;
                    if tx.send_blocking(CatalogMessage::Reset).is_err() {
                        return;
                    }
                }
                std::thread::sleep(delay);
                delay = (delay * 2).min(Duration::from_secs(10));
            }
        });
        let task = cx.spawn(async move |this, cx| {
            while let Ok(message) = rx.recv().await {
                let alive = this.update(cx, |this, cx| {
                    match message {
                        CatalogMessage::Event(event) => this.apply_catalog(event, cx),
                        CatalogMessage::Reset => this.load_snapshot(cx),
                    }
                    cx.notify();
                });
                if alive.is_err() {
                    return;
                }
            }
        });
        self._tasks.push(task);
    }

    fn apply_catalog(&mut self, event: Value, cx: &mut Context<Self>) {
        if event["stream"] != "catalog" {
            return;
        }
        let seq = event["seq"].as_u64().unwrap_or(0);
        if seq <= self.catalog_seq {
            return;
        }
        self.catalog_seq = seq;
        match event["type"].as_str() {
            Some("session.upserted") => {
                let Ok(session) = serde_json::from_value::<Session>(event["data"]["session"].clone()) else { return };
                if let Some(pane) = &self.pane {
                    if self.selected.as_deref() == Some(&session.id) {
                        let changed = session.clone();
                        pane.update(cx, |pane, cx| pane.session_changed(changed, cx));
                    }
                }
                match self.sessions.iter_mut().find(|s| s.id == session.id) {
                    Some(existing) => *existing = session,
                    None => self.sessions.push(session),
                }
            }
            Some("session.removed") => {
                if let Some(id) = event["data"]["sessionId"].as_str() {
                    self.sessions.retain(|s| s.id != id);
                }
            }
            _ => {}
        }
    }

    fn select(&mut self, id: String, window: &mut Window, cx: &mut Context<Self>) {
        let clicked = Instant::now();
        probe::record("select", &id);
        if self.selected.as_deref() == Some(&id) {
            return;
        }
        let Some(session) = self.sessions.iter().find(|s| s.id == id).cloned() else { return };
        self.selected = Some(id);
        let api = self.api.clone();
        self.pane = Some(cx.new(|cx| SessionPane::new(api, session, clicked, window, cx)));
        window.focus(&self.focus, cx);
        cx.notify();
    }

    fn with_pane(
        &mut self,
        cx: &mut Context<Self>,
        f: impl FnOnce(&mut SessionPane, Option<&mut Window>, &mut Context<SessionPane>),
        window: Option<&mut Window>,
    ) {
        if let Some(pane) = &self.pane {
            pane.update(cx, |pane, cx| f(pane, window, cx));
        }
    }

    fn needs_you(&self, session: &Session) -> bool {
        session.waiting_on.is_some() || self.attention.contains(&session.id)
    }

    fn render_sidebar(&self, palette: &Palette, cx: &mut Context<Self>) -> AnyElement {
        let mut ordered: Vec<&Session> = self.sessions.iter().filter(|s| !s.archived).collect();
        ordered.sort_by(|a, b| b.last_active.cmp(&a.last_active));
        let needs: Vec<&Session> = ordered.iter().copied().filter(|s| self.needs_you(s)).collect();
        let working: Vec<&Session> = ordered.iter().copied().filter(|s| s.is_working() && !self.needs_you(s)).collect();
        let mut projects: BTreeMap<(usize, String), Vec<&Session>> = BTreeMap::new();
        let mut project_order: Vec<String> = Vec::new();
        for session in ordered.iter().copied().filter(|s| s.current || s.is_working()) {
            let name = session.project.as_ref().map(|p| p.display_name.clone()).unwrap_or_else(|| "No project".into());
            let index = match project_order.iter().position(|p| *p == name) {
                Some(index) => index,
                None => {
                    project_order.push(name.clone());
                    project_order.len() - 1
                }
            };
            projects.entry((index, name)).or_default().push(session);
        }

        let mut list = div().flex().flex_col().gap(px(2.)).px(px(space::S)).pb(px(space::L));
        if !needs.is_empty() {
            list = list.child(caption("needs-you", "Needs you", palette));
            for session in &needs {
                list = list.child(self.row("needs", session, true, palette, cx));
            }
        }
        if !working.is_empty() {
            list = list.child(caption("working", "Working", palette));
            for session in &working {
                list = list.child(self.row("working", session, false, palette, cx));
            }
        }
        list = list.child(caption("projects", "Projects", palette));
        for ((_, name), sessions) in projects {
            list = list.child(
                div()
                    .id(SharedString::from(format!("project-{name}")))
                    .role(Role::Heading)
                    .aria_label(name.clone())
                    .pt(px(space::S))
                    .px(px(space::S))
                    .text_size(px(ty::BODY))
                    .font_weight(gpui::FontWeight::SEMIBOLD)
                    .text_color(palette.text2)
                    .child(name.clone()),
            );
            for session in sessions.iter().take(5) {
                list = list.child(self.row("project", session, false, palette, cx));
            }
        }
        div()
            .id("sidebar")
            .role(Role::Navigation)
            .aria_label("Sessions")
            .w(px(280.))
            .h_full()
            .flex_none()
            .bg(palette.side)
            .border_r_1()
            .border_color(palette.line)
            .flex()
            .flex_col()
            .child(div().h(px(40.)).flex_none())
            .child(div().id("sidebar-scroll").flex_1().overflow_y_scroll().child(list))
            .into_any_element()
    }

    fn row(&self, section: &str, session: &Session, show_reason: bool, palette: &Palette, cx: &mut Context<Self>) -> AnyElement {
        let selected = self.selected.as_deref() == Some(&session.id);
        let needs = self.needs_you(session);
        let dot = if needs {
            palette.attention
        } else if session.is_working() {
            palette.working
        } else {
            palette.muted
        };
        let status = if needs {
            "Needs you"
        } else if session.is_working() {
            "Working"
        } else if session.open {
            "Idle"
        } else {
            "Closed"
        };
        let reason = if show_reason {
            session.waiting_kind().map(|k| match k {
                "approval" => "Approval".to_string(),
                "question" => "Question".to_string(),
                other => other.to_string(),
            })
        } else {
            None
        };
        let id = session.id.clone();
        let title = session.title();
        let label = format!("{title}, {status}");
        div()
            .id(SharedString::from(format!("row-{section}-{}", session.id)))
            .role(Role::Button)
            .aria_label(label)
            .flex()
            .items_center()
            .gap(px(space::S))
            .px(px(space::S))
            .py(px(5.))
            .rounded(px(7.))
            .when(selected, |d| d.bg(palette.selection))
            .when(!selected && needs, |d| d.bg(palette.attention_background))
            .hover(|d| d.bg(palette.fill))
            .cursor_pointer()
            .on_click(cx.listener(move |this, _, window, cx| this.select(id.clone(), window, cx)))
            .child(div().size(px(7.)).rounded_full().bg(dot).flex_none())
            .child(
                div()
                    .flex()
                    .flex_col()
                    .min_w_0()
                    .child(div().text_size(px(ty::BODY)).text_color(palette.text).truncate().child(title))
                    .when_some(reason, |d, reason| {
                        d.child(div().text_size(px(ty::META)).text_color(palette.attention).child(reason))
                    }),
            )
            .into_any_element()
    }
}

fn caption(id: &'static str, text: &'static str, palette: &Palette) -> AnyElement {
    div()
        .id(id)
        .role(Role::Heading)
        .aria_label(text)
        .pt(px(space::M))
        .pb(px(space::XS))
        .px(px(space::S))
        .text_size(px(ty::CAPTION))
        .font_weight(gpui::FontWeight::SEMIBOLD)
        .text_color(palette.muted)
        .child(text.to_uppercase())
        .into_any_element()
}

fn sync_theme(window: &mut Window, cx: &mut gpui::App) {
    let dark = matches!(window.appearance(), gpui::WindowAppearance::Dark | gpui::WindowAppearance::VibrantDark);
    let mode = if dark { gpui_component::ThemeMode::Dark } else { gpui_component::ThemeMode::Light };
    gpui_component::Theme::change(mode, Some(window), cx);
}

impl Render for AppView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let palette = Palette::for_appearance(window.appearance());
        let detail: AnyElement = match (&self.pane, &self.error) {
            (Some(pane), _) => pane.clone().into_any_element(),
            (None, Some(error)) => div().p(px(space::XL)).text_color(palette.failed).child(error.clone()).into_any_element(),
            (None, None) => div()
                .size_full()
                .flex()
                .items_center()
                .justify_center()
                .text_color(palette.muted)
                .text_size(px(ty::TITLE))
                .child("Choose a session")
                .into_any_element(),
        };
        div()
            .size_full()
            .flex()
            .track_focus(&self.focus)
            .on_action(cx.listener(|this, _: &crate::pane::PageUp, _, cx| this.with_pane(cx, |pane, _, cx| pane.page(true, cx), None)))
            .on_action(cx.listener(|this, _: &crate::pane::PageDown, _, cx| this.with_pane(cx, |pane, _, cx| pane.page(false, cx), None)))
            .on_action(cx.listener(|this, _: &crate::pane::ScrollBench, window, cx| {
                this.with_pane(cx, |pane, window, cx| pane.scroll_bench(window.unwrap(), cx), Some(window))
            }))
            .bg(palette.window)
            .text_color(palette.text)
            .font_family(".SystemUIFont")
            .child(self.render_sidebar(&palette, cx))
            .child(div().flex_1().min_w_0().h_full().rounded(px(radius::CARD)).child(detail))
    }
}
