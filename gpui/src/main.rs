mod api;
mod app;
mod model;
mod native;
mod palette;
mod pane;
mod probe;

use gpui::{App, AppContext as _, Bounds, KeyBinding, TitlebarOptions, WindowBounds, WindowOptions, actions, point, px, size};

actions!(agenthail, [Quit]);

fn main() {
    let api = match api::Api::from_env() {
        Ok(api) => api,
        Err(error) => {
            eprintln!("{error}");
            std::process::exit(2);
        }
    };
    gpui::Application::with_platform(gpui_platform::current_platform(false)).run(move |cx: &mut App| {
        gpui_component::init(cx);
        cx.bind_keys([
            KeyBinding::new("cmd-q", Quit, None),
            KeyBinding::new("pageup", pane::PageUp, None),
            KeyBinding::new("pagedown", pane::PageDown, None),
            KeyBinding::new("cmd-alt-shift-b", pane::ScrollBench, None),
        ]);
        cx.on_action(|_: &Quit, cx| cx.quit());
        let bounds = match std::env::var("AGENTHAIL_GPUI_FRAME").ok().and_then(|f| parse_frame(&f)) {
            Some((x, y, w, h)) => Bounds::new(point(px(x), px(y)), size(px(w), px(h))),
            None => Bounds::centered(None, size(px(1180.), px(800.)), cx),
        };
        cx.open_window(
            WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                window_min_size: Some(size(px(720.), px(480.))),
                titlebar: Some(TitlebarOptions {
                    title: Some("Agenthail GPUI".into()),
                    appears_transparent: true,
                    traffic_light_position: Some(point(px(12.), px(12.))),
                }),
                ..Default::default()
            },
            |window, cx| {
                let view = cx.new(|cx| app::AppView::new(api, window, cx));
                cx.new(|cx| gpui_component::Root::new(view, window, cx))
            },
        )
        .expect("open window");
        cx.activate(true);
    });
}

fn parse_frame(frame: &str) -> Option<(f32, f32, f32, f32)> {
    let parts: Vec<f32> = frame.split(',').filter_map(|p| p.trim().parse().ok()).collect();
    match parts.as_slice() {
        [x, y, w, h] => Some((*x, *y, *w, *h)),
        _ => None,
    }
}
