use std::sync::Arc;
use std::time::{Duration, Instant};

use feed::wire::{Outcome, Rebuild, Sse, Wire};
use gpui::{AppContext as _, HeadlessAppContext, Modifiers, MouseButton, MouseDownEvent, MouseMoveEvent, MouseUpEvent, PlatformInput, point, px, size};

use crate::view::TranscriptView;

pub struct Plan {
    pub replay: String,
    pub speed: f64,
    pub seconds: f64,
    pub scroll_at: Option<f64>,
    pub snaps: Vec<f64>,
    pub trees: Vec<f64>,
    pub out: String,
    pub select_at: Option<f64>,
    pub pageup_at: Option<f64>,
}

struct Assets;

impl gpui::AssetSource for Assets {
    fn load(&self, _: &str) -> anyhow::Result<Option<std::borrow::Cow<'static, [u8]>>> {
        Ok(None)
    }

    fn list(&self, _: &str) -> anyhow::Result<Vec<gpui::SharedString>> {
        Ok(vec![])
    }
}

fn pick(sorted: &[f64], q: f64) -> f64 {
    sorted.get(((sorted.len() as f64 - 1.0) * q).round() as usize).copied().unwrap_or(0.0)
}

pub fn run(plan: Plan) {
    let text = std::fs::read_to_string(&plan.replay).expect("read replay");
    let lines: Vec<(f64, String)> = text
        .lines()
        .filter_map(|l| {
            let (ts, rest) = l.split_once('\t')?;
            Some((ts.parse().ok()?, rest.to_string()))
        })
        .collect();
    let first = lines.first().map(|l| l.0).unwrap_or(0.0);
    let text_system = gpui_platform::current_platform(true).text_system();
    let mut cx = HeadlessAppContext::with_platform(text_system, Arc::new(Assets), gpui_platform::current_headless_renderer);
    cx.update(|cx| gpui_component::init(cx));
    let mut view_slot = None;
    let window = cx
        .open_window(size(px(1100.), px(820.)), |window, cx| {
            let view = cx.new(|cx| TranscriptView::manual(window, cx));
            view_slot = Some(view.clone());
            cx.new(|cx| gpui_component::Root::new(view, window, cx))
        })
        .expect("open window");
    let view = view_slot.expect("view");
    let any = window.into();
    let mut sse = Sse::default();
    let mut rebuild = Rebuild::default();
    let mut next = 0;
    let frames = (plan.seconds * 60.0) as usize;
    let mut changed_ms: Vec<f64> = Vec::new();
    let mut idle_ms: Vec<f64> = Vec::new();
    let mut scrolled = false;
    let mut selected = false;
    let mut paged = false;
    let mut snaps = plan.snaps.clone();
    let mut trees = plan.trees.clone();
    for frame in 0..=frames {
        let t = frame as f64 / 60.0;
        let start = Instant::now();
        let mut changed = false;
        let mut batches = Vec::new();
        while let Some((ts, line)) = lines.get(next) {
            if (ts - first) / plan.speed > t {
                break;
            }
            next += 1;
            let Some(json) = sse.line(line) else { continue };
            let Ok(wire) = serde_json::from_str::<Wire>(&json) else { continue };
            if let Outcome::Events(seq, events) = rebuild.apply(wire) {
                batches.push((seq, events));
            }
        }
        if !batches.is_empty() {
            changed |= cx.update(|cx| view.update(cx, |v, cx| v.feed(batches, cx)));
        }
        if let Some(at) = plan.scroll_at {
            if !scrolled && t >= at {
                scrolled = true;
                changed = true;
                cx.update(|cx| view.update(cx, |v, cx| v.scroll_up(900.0, cx)));
            }
        }
        if let Some(at) = plan.select_at {
            if !selected && t >= at {
                selected = true;
                let rows_before = cx.update(|cx| view.read(cx).visible());
                cx.update_window(any, |_, window, cx| {
                    let down = point(px(60.), px(120.));
                    let up = point(px(900.), px(700.));
                    window.dispatch_event(PlatformInput::MouseDown(MouseDownEvent { button: MouseButton::Left, position: down, modifiers: Modifiers::default(), click_count: 1, first_mouse: false }), cx);
                    window.draw(cx).clear(cx);
                    for k in 1..=12 {
                        let f = k as f32 / 12.0;
                        let p = point(px(60. + 840. * f), px(120. + 580. * f));
                        window.dispatch_event(PlatformInput::MouseMove(MouseMoveEvent { position: p, pressed_button: Some(MouseButton::Left), modifiers: Modifiers::default() }), cx);
                        window.draw(cx).clear(cx);
                    }
                    window.dispatch_event(PlatformInput::MouseUp(MouseUpEvent { button: MouseButton::Left, position: up, modifiers: Modifiers::default(), click_count: 1 }), cx);
                    window.draw(cx).clear(cx);
                    let text = gpui_base::TextSelection::selected_text(window, cx);
                    let paragraphs = text.split("\n").filter(|l| !l.trim().is_empty()).count();
                    println!("gpui selection: {} chars, {} non-empty lines, {} visible blocks; starts {:?}", text.len(), paragraphs, rows_before.len(), text.chars().take(60).collect::<String>());
                    let _ = std::fs::write(format!("{}/gpui-selection.txt", plan.out), &text);
                })
                .expect("select");
            }
        }
        if let Some(at) = plan.pageup_at {
            if !paged && t >= at {
                paged = true;
                let before = cx.update(|cx| view.read(cx).visible());
                cx.update_window(any, |_, window, cx| {
                    window.dispatch_action(Box::new(crate::view::PageUp), cx);
                    window.draw(cx).clear(cx);
                })
                .expect("page up");
                let after = cx.update(|cx| view.read(cx).visible());
                let first = |rows: &Vec<(String, f32)>| rows.first().map(|r| r.0.clone()).unwrap_or_default();
                println!("gpui page up: first visible {} -> {} ({} rows before, {} after)", first(&before), first(&after), before.len(), after.len());
            }
        }
        cx.run_until_parked();
        cx.update_window(any, |_, window, cx| {
            window.draw(cx).clear(cx);
        })
        .expect("draw");
        let ms = start.elapsed().as_secs_f64() * 1000.0;
        if changed { changed_ms.push(ms) } else { idle_ms.push(ms) }
        cx.advance_clock(Duration::from_millis(16));
        if let Some(pos) = trees.iter().position(|at| t >= *at) {
            let at = trees.remove(pos);
            let rows = cx.update(|cx| view.read(cx).visible());
            let lines: Vec<String> = rows.iter().map(|(id, y)| format!("{id}\t{y:.1}")).collect();
            std::fs::write(format!("{}/gpui-tree-{at}.tsv", plan.out), lines.join("\n")).expect("write tree");
        }
        if let Some(pos) = snaps.iter().position(|at| t >= *at) {
            let at = snaps.remove(pos);
            match cx.capture_screenshot(any) {
                Ok(image) => image.save(format!("{}/gpui-snap-{at}.png", plan.out)).expect("save png"),
                Err(error) => eprintln!("snapshot failed: {error}"),
            }
        }
    }
    let blocks = cx.update(|cx| view.read(cx).visible().len());
    changed_ms.sort_by(|a, b| a.partial_cmp(b).unwrap());
    idle_ms.sort_by(|a, b| a.partial_cmp(b).unwrap());
    println!(
        "gpui headless: changed frames {} (cpu p50 {:.3} p95 {:.3} max {:.3} ms), idle frames {} (cpu p50 {:.3} p95 {:.3} ms), visible rows {blocks}",
        changed_ms.len(),
        pick(&changed_ms, 0.5),
        pick(&changed_ms, 0.95),
        changed_ms.last().copied().unwrap_or(0.0),
        idle_ms.len(),
        pick(&idle_ms, 0.5),
        pick(&idle_ms, 0.95),
    );
}
