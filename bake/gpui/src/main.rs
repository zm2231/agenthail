mod metrics;
mod palette;
mod view;

use gpui::{App, AppContext as _, Bounds, KeyBinding, TitlebarOptions, WindowBounds, WindowOptions, actions, point, px, size};

actions!(bake, [Quit]);

pub enum Source {
    Live(String),
    Replay { path: String, speed: f64 },
}

fn parse_args() -> Source {
    let args: Vec<String> = std::env::args().collect();
    let value = |flag: &str| args.iter().position(|a| a == flag).and_then(|i| args.get(i + 1)).cloned();
    if let Some(path) = value("--replay") {
        let speed = value("--speed").and_then(|s| s.parse().ok()).unwrap_or(1.0);
        return Source::Replay { path, speed };
    }
    if let Some(id) = value("--session") {
        return Source::Live(id);
    }
    eprintln!("usage: bake-gpui --replay <stream.tsv> [--speed N] | --session <id>");
    std::process::exit(2);
}

fn main() {
    let source = parse_args();
    gpui::Application::with_platform(gpui_platform::current_platform(false)).run(move |cx: &mut App| {
        gpui_component::init(cx);
        cx.bind_keys([
            KeyBinding::new("cmd-q", Quit, None),
            KeyBinding::new("pageup", view::PageUp, None),
            KeyBinding::new("pagedown", view::PageDown, None),
            KeyBinding::new("cmd-down", view::JumpToLatest, None),
            KeyBinding::new("cmd-shift-f", view::ToggleFocus, None),
        ]);
        cx.on_action(|_: &Quit, cx| cx.quit());
        let bounds = Bounds::centered(None, size(px(1100.), px(820.)), cx);
        cx.open_window(
            WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                window_min_size: Some(size(px(480.), px(360.))),
                titlebar: Some(TitlebarOptions {
                    title: Some("Bake: GPUI".into()),
                    appears_transparent: true,
                    traffic_light_position: Some(point(px(12.), px(12.))),
                }),
                focus: false,
                show: true,
                ..Default::default()
            },
            |window, cx| {
                let view = cx.new(|cx| view::TranscriptView::new(source, window, cx));
                cx.new(|cx| gpui_component::Root::new(view, window, cx))
            },
        )
        .expect("open window");
    });
}
