use super::*;

const SIZE: (f32, f32) = (1440.0, 900.0);

fn laid_out(scale: f32, screen: &str, dir: &Dir) -> Runtime<App> {
    crate::look::hold_text_scale(Some(scale));
    let mut all = sessions(&["a", "b"]);
    all.push(session("q", 3, json!({"surface": "codex", "name": "queue"})));
    let mut rt = runtime(app(all, dir), SIZE);
    rt.update(|s, _| match screen {
        "session" => {
            crate::over::open_session(s, "a");
            crate::load_pane(s, "a");
            let items: Vec<feed::Item> = serde_json::from_value(json!([
                {"id": "u1", "kind": "message", "role": "user", "text": "Make the sidebar rows hold larger text."},
                {"id": "a1", "kind": "message", "role": "assistant", "text": "Done. The row height now follows the text size, see [the change](https://example.com) and `look.rs`.\n\n- Rows grow\n- Layout stays"},
                {"id": "h1", "kind": "message", "role": "assistant", "text": "## Next steps"},
                {"id": "n1", "kind": "notice", "text": "Turn finished"},
            ]))
            .unwrap();
            if let Some(pane) = s.panes.get_mut("a") {
                pane.transcript.prepend(items);
                let mut counter = 0;
                pane.rebuild(&mut counter);
            }
        }
        "sessions" => {
            crate::over::go(s, Screen::Sessions);
            let view = serde_json::from_value(json!({
                "projects": [{"id": "p1", "name": "Alpha", "counts": {"families": 3, "sessions": 3}, "collapsedFamilyIds": ["a", "b", "q"]}],
                "families": [{"id": "a", "rootId": "a", "projectId": "p1", "members": [{"id": "a"}]}, {"id": "b", "rootId": "b", "projectId": "p1", "members": [{"id": "b"}]}, {"id": "q", "rootId": "q", "projectId": "p1", "members": [{"id": "q"}]}],
            }))
            .unwrap();
            let page = feed::listing::Page { sessions: s.ov.d.sessions.clone(), view, next: None };
            crate::over::listing::show(s, feed::listing::Listing::first(page));
        }
        "queue" => crate::over::open_session(s, "q"),
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

#[test]
fn changing_the_text_size_in_an_open_window_grows_the_transcript_rows_already_shown() {
    if !isolated("text_scale::changing_the_text_size_in_an_open_window_grows_the_transcript_rows_already_shown") {
        return;
    }
    let dir = Dir::new("text-scale-live");
    let mut rt = laid_out(1.0, "session", &dir);
    let (reply, heading) = (rect(&rt, "item:a1"), rect(&rt, "item:h1"));
    crate::look::hold_text_scale(Some(1.3));
    rt.frame(DT);
    rt.settle_all();
    rt.frame(DT);
    let after = rect(&rt, "item:a1");
    assert!(after[3] > reply[3] * 1.15, "the shown reply grows with the text: {reply:?} -> {after:?}");
    let after = rect(&rt, "item:h1");
    assert!(after[3] > heading[3] * 1.15, "a heading grows with the text: {heading:?} -> {after:?}");
}

struct Capture {
    gpu: rune::render::Gpu,
    renderer: rune::render::Renderer,
    target: rune::render::Offscreen,
}

impl Capture {
    fn new() -> Capture {
        let gpu = rune::render::Gpu::headless();
        let renderer = rune::render::Renderer::new(&gpu.device, rune::render::OFFSCREEN_FORMAT);
        let target = rune::render::Offscreen::new(&gpu.device, SIZE.0 as u32, SIZE.1 as u32);
        Capture { gpu, renderer, target }
    }

    fn pixels<S: 'static>(&mut self, rt: &mut Runtime<S>) -> Vec<u8> {
        rt.frame(DT);
        self.renderer.render_runtime_to_offscreen(&self.gpu.device, &self.gpu.queue, &self.target, rt);
        self.target.read(&self.gpu.device)
    }
}

fn red(rgba: &[u8]) -> usize {
    rgba.chunks(4).filter(|p| p[0] > 200 && p[1] < 70 && p[2] < 70).count()
}

#[test]
fn a_new_link_colour_reaches_the_transcript_rows_already_shown() {
    if !isolated("text_scale::a_new_link_colour_reaches_the_transcript_rows_already_shown") {
        return;
    }
    let dir = Dir::new("text-roles-live");
    let mut rt = laid_out(1.0, "session", &dir);
    let mut screen = Capture::new();
    assert_eq!(red(&screen.pixels(&mut rt)), 0, "nothing is red before the link colour changes");
    let view: feed::look::View = serde_json::from_value(json!({"colors": {"link": {"dark": "#ff2020", "light": "#ff2020"}}})).unwrap();
    crate::look::take_view(crate::look::ticket(), view);
    rt.frame(DT);
    rt.settle_all();
    assert!(red(&screen.pixels(&mut rt)) > 20, "the shown link takes the new link colour");
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
    for screen in ["now", "session", "sessions", "queue"] {
        let mut rt = laid_out(scale, screen, &dir);
        render(&mut rt, SIZE, &out.join(format!("{screen}-{scale}.png")));
    }
    let agents = crate::settings::TABS.iter().position(|t| *t == "Agents").expect("an Agents tab");
    for (name, tab, page) in [("settings", 0, 0), ("keyboard", 1, 2), ("agents", agents, 0)] {
        let mut rt = settings_on(tab, page);
        render(&mut rt, SETTINGS, &out.join(format!("{name}-{scale}.png")));
    }
    let mut rt = roles_shown();
    render(&mut rt, SETTINGS, &out.join(format!("roles-{scale}.png")));
}

const SETTINGS: (f32, f32) = (720.0, 620.0);
const LONG_ROLE: &str = "release-captain-for-the-weekly-train-42";

fn roles_shown() -> Runtime<crate::settings::Prefs> {
    let agents = crate::settings::TABS.iter().position(|t| *t == "Agents").expect("an Agents tab");
    let mut rt = settings_on(agents, 0);
    rt.update(|s, _| {
        let roles = serde_json::from_value(json!({"roles": [
            {"name": "lead", "builtIn": true},
            {"name": LONG_ROLE, "edited": true, "description": "Runs the weekly release train."},
            {"name": "reviewer", "builtIn": true},
        ]}))
        .unwrap();
        crate::settings::show_agent_roles(s, roles, LONG_ROLE);
    });
    rt.frame(DT);
    rt.settle_all();
    rt.frame(DT);
    rt
}

#[test]
fn a_long_role_name_stays_inside_the_role_list_at_larger_text() {
    if !isolated("text_scale::a_long_role_name_stays_inside_the_role_list_at_larger_text") {
        return;
    }
    crate::look::hold_text_scale(Some(1.6));
    let rt = roles_shown();
    let row = rt.presented(&format!("agent-role-{LONG_ROLE}")).expect("the long role is listed").0;
    let lead = rt.presented("agent-role-lead").expect("lead is listed").0;
    assert!(row[0] >= lead[0] && row[0] + row[2] <= lead[0] + lead[2] + 0.5, "the long role {row:?} keeps the list's width {lead:?}");
    assert!(row[3] > lead[3] * 1.5, "the long name wraps onto more lines: {row:?} vs {lead:?}");
}

fn settings_on(tab: usize, page: usize) -> Runtime<crate::settings::Prefs> {
    let spec = AppSpec::<crate::settings::Prefs> {
        poll: Some(crate::settings::poll),
        fonts: rune_theme_opencode::FONTS,
        families: rune_theme_opencode::FAMILIES,
        ..AppSpec::new("Settings", SETTINGS, crate::settings::init, crate::settings::view)
    };
    let mut rt = rune::App::new(spec).headless_runtime(SETTINGS, Env { system_dark: true, scale: 1.0 });
    rt.update(|s, _| {
        s.tab = tab;
        s.general_page = page;
    });
    rt.frame(DT);
    rt.settle_all();
    rt.frame(DT);
    rt
}

#[test]
fn the_open_settings_tab_stays_in_view_when_larger_text_makes_the_tabs_scroll() {
    if !isolated("text_scale::the_open_settings_tab_stays_in_view_when_larger_text_makes_the_tabs_scroll") {
        return;
    }
    crate::look::hold_text_scale(Some(1.6));
    let agents = crate::settings::TABS.iter().position(|t| *t == "Agents").expect("an Agents tab");
    let rt = settings_on(agents, 0);
    let [x, _, w, _] = rt.presented("settings-tabs").expect("the tab strip is on screen").0;
    let [sx, _, sw, _] = rt.presented(&format!("settings-tabs/segment{agents}")).expect("the Agents tab is laid out").0;
    assert!(sx >= x - 0.5 && sx + sw <= x + w + 0.5, "the Agents tab {:?} shows inside the strip {:?}", (sx, sw), (x, w));
}
