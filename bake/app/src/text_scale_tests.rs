use super::*;

const SIZE: (f32, f32) = (1440.0, 900.0);

fn laid_out(scale: f32, screen: &str, dir: &Dir) -> Runtime<App> {
    crate::look::hold_text_scale(Some(scale));
    let mut rt = runtime(app(sessions(&["a", "b"]), dir), SIZE);
    rt.update(|s, _| match screen {
        "session" => {
            crate::over::open_session(s, "a");
            crate::load_pane(s, "a");
            let items: Vec<feed::Item> = serde_json::from_value(json!([
                {"id": "u1", "kind": "message", "role": "user", "text": "Make the sidebar rows hold larger text."},
                {"id": "a1", "kind": "message", "role": "assistant", "text": "Done. The row height now follows the text size, see [the change](https://example.com) and `look.rs`.\n\n- Rows grow\n- Layout stays"},
                {"id": "n1", "kind": "notice", "text": "Turn finished"},
            ]))
            .unwrap();
            if let Some(pane) = s.panes.get_mut("a") {
                pane.transcript.prepend(items);
                let mut counter = 0;
                pane.rebuild(&mut counter);
            }
        }
        "sessions" => crate::over::go(s, Screen::Sessions),
        _ => {}
    });
    rt.settle_all();
    rt.frame(DT);
    rt.frame(DT);
    rt
}

fn rect(rt: &Runtime<App>, key: &str) -> [f32; 4] {
    rt.presented(key).unwrap_or_else(|| panic!("{key} is on screen")).0
}

#[test]
fn a_larger_text_size_grows_the_text_and_keeps_the_window_layout_at_1440_by_900() {
    if !isolated("text_scale::a_larger_text_size_grows_the_text_and_keeps_the_window_layout_at_1440_by_900") {
        return;
    }
    let dir = Dir::new("text-scale");
    let normal = laid_out(1.0, "now", &dir);
    let larger = laid_out(1.2, "now", &dir);

    for key in ["home", "p-p1", "status-bar"] {
        let (before, after) = (rect(&normal, key), rect(&larger, key));
        assert!(after[3] > before[3] * 1.1, "{key} holds larger text: {before:?} -> {after:?}");
    }
    for key in ["sidebar-scroll", "now-a", "now-rail"] {
        let (before, after) = (rect(&normal, key), rect(&larger, key));
        assert_eq!((before[0], before[2]), (after[0], after[2]), "{key} keeps its place and width");
    }
}

fn render<S: 'static>(rt: &mut Runtime<S>, size: (f32, f32), path: &std::path::Path) {
    let gpu = rune::render::Gpu::headless();
    let mut renderer = rune::render::Renderer::new(&gpu.device, rune::render::OFFSCREEN_FORMAT);
    let target = rune::render::Offscreen::new(&gpu.device, size.0 as u32, size.1 as u32);
    rt.frame(DT);
    renderer.render_runtime_to_offscreen(&gpu.device, &gpu.queue, &target, rt);
    let rgba = target.read(&gpu.device);
    rune::render::write_png(path, size.0 as u32, size.1 as u32, &rgba).unwrap();
}

#[test]
fn shot() {
    let Some(dir_out) = std::env::var_os("AGENTHAIL_SHOT_DIR") else { return };
    if !isolated("text_scale::shot") {
        return;
    }
    let out = std::path::PathBuf::from(dir_out);
    let scale: f32 = std::env::var("AGENTHAIL_SHOT_SCALE").ok().and_then(|s| s.parse().ok()).unwrap_or(1.0);
    let dir = Dir::new("shot");
    for screen in ["now", "session", "sessions"] {
        let mut rt = laid_out(scale, screen, &dir);
        render(&mut rt, SIZE, &out.join(format!("{screen}-{scale}.png")));
    }
    let size = (720.0, 620.0);
    let spec = AppSpec::<crate::settings::Prefs> {
        fonts: rune_theme_opencode::FONTS,
        families: rune_theme_opencode::FAMILIES,
        ..AppSpec::new("Settings", size, crate::settings::init, crate::settings::view)
    };
    let mut rt = rune::App::new(spec).headless_runtime(size, Env { system_dark: true, scale: 1.0 });
    rt.frame(DT);
    render(&mut rt, size, &out.join(format!("settings-{scale}.png")));
}
