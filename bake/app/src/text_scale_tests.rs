use super::*;

const SIZE: (f32, f32) = (1440.0, 900.0);

const LONG_TITLE: &str = "Fix the flaky decisions test for other computers' inbox notes";
const SMALLEST: (f32, f32) = (1100.0, 700.0);

fn laid_out(scale: f32, screen: &str, dir: &Dir) -> Runtime<App> {
    laid_out_in(scale, screen, dir, SIZE, None)
}

fn laid_out_in(scale: f32, screen: &str, dir: &Dir, size: (f32, f32), name: Option<&str>) -> Runtime<App> {
    crate::look::hold_text_scale(Some(scale));
    let mut all = vec![session("a", 0, name.map_or(json!({}), |n| json!({"name": n}))), session("b", 1, json!({}))];
    all.push(session("q", 3, json!({"surface": "codex", "name": "queue"})));
    let mut raw = recorded(1, all);
    if matches!(screen, "project" | "work") {
        raw.projects[0].ledger_key = "shared:p1".into();
        raw.ledgers.insert("p1".into(), serde_json::from_value(json!([
            {"id": "p1-W1", "text": "Make the sidebar rows hold larger text without clipping their second line", "type": "bug", "priority": "P1", "state": "in_progress", "phase": "build", "assignee": "a", "description": "Rows clip at the largest text size."},
            {"id": "p1-W2", "text": "Keep the toolbar title when the window is narrow", "type": "task", "priority": "P2", "state": "open"},
            {"id": "p1-W3", "text": "Ship the text size keys", "type": "task", "priority": "P2", "state": "done"},
        ])).unwrap());
    }
    let mut app = crate::empty(Overview::recorded(raw), false);
    app.sessions = app.ov.d.sessions.clone();
    app.now = app.ov.d.now;
    app.fixtures = Some(Fixtures { dir: dir.path(), streams: HashMap::new(), speed: 1.0 });
    app.status = "fixtures".into();
    let mut rt = runtime(app, size);
    rt.update(|s, tx| match screen {
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
            let page = feed::listing::Page { sessions: s.ov.d.sessions.to_vec(), view, next: None };
            crate::over::listing::show(s, feed::listing::Listing::first(page));
        }
        "queue" => crate::over::open_session(s, "q"),
        "project" => crate::over::go(s, Screen::Project("p1".into())),
        "work" => {
            let key = s.ov.d.work.keys().find(|k| k.id == "p1-W1").cloned().expect("p1-W1 is recorded");
            crate::over::go(s, Screen::Work(key));
        }
        "apps" => crate::over::go(s, Screen::Extensions),
        "brief" => {
            crate::over::go(s, Screen::Now);
            s.ov.now_brief = true;
        }
        "jump" => crate::over::open_jump(s, tx),
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

#[test]
fn sidebar_avatars_fill_their_slot_and_keep_the_label_beside_them_at_every_text_size() {
    if !isolated("text_scale::sidebar_avatars_fill_their_slot_and_keep_the_label_beside_them_at_every_text_size") {
        return;
    }
    let dir = Dir::new("text-scale-avatars");
    for scale in [1.0, 1.6] {
        let rt = laid_out(scale, "now", &dir);
        let (slot, chip, label) = (rect(&rt, "p-p1/avatar"), rect(&rt, "p-p1/avatar/chip"), rect(&rt, "p-p1/label"));
        assert!((slot[2] - 16.0 * scale).abs() < 0.01, "the slot follows the text at {scale}: {slot:?}");
        assert!((chip[2] - (slot[2] + 2.0)).abs() < 0.01, "the chip fills its slot at {scale}: {chip:?} in {slot:?}");
        let gap = label[0] - (chip[0] + chip[2]);
        assert!((gap - 5.0).abs() < 0.01, "the label sits 5 pt past the chip at {scale}: {gap}");
    }
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
    for screen in ["now", "session", "sessions", "queue", "project", "work", "apps", "jump"] {
        let mut rt = laid_out(scale, screen, &dir);
        render(&mut rt, SIZE, &out.join(format!("{screen}-{scale}.png")));
    }
    for (size, tag) in [(SIZE, "wide"), (SMALLEST, "small")] {
        for screen in ["now", "session", "sessions"] {
            let mut rt = laid_out_in(scale, screen, &dir, size, Some(LONG_TITLE));
            render(&mut rt, size, &out.join(format!("long-{tag}-{screen}-{scale}.png")));
            let mut rt = laid_out_in(1.6, screen, &dir, size, Some(LONG_TITLE));
            crate::look::hold_text_scale(Some(scale));
            for _ in 0..4 {
                rt.frame(DT);
            }
            rt.settle_all();
            rt.frame(DT);
            render(&mut rt, size, &out.join(format!("long-{tag}-{screen}-from-1.6-to-{scale}.png")));
        }
    }
    for screen in ["now", "session", "project", "work"] {
        let mut rt = laid_out(1.6, screen, &dir);
        crate::look::hold_text_scale(Some(scale));
        rt.frame(DT);
        rt.settle_all();
        rt.frame(DT);
        render(&mut rt, SIZE, &out.join(format!("{screen}-from-1.6-to-{scale}.png")));
    }
    let agents = crate::settings::TABS.iter().position(|t| *t == "Agents").expect("an Agents tab");
    for (name, tab, page) in [("settings", 0, 0), ("keyboard", 1, 2), ("agents", agents, 0)] {
        let mut rt = settings_on(tab, page);
        render(&mut rt, SETTINGS, &out.join(format!("{name}-{scale}.png")));
    }
    let mut rt = roles_shown();
    render(&mut rt, SETTINGS, &out.join(format!("roles-{scale}.png")));
}

pub(super) const SETTINGS: (f32, f32) = (720.0, 620.0);
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

pub(super) fn settings_on(tab: usize, page: usize) -> Runtime<crate::settings::Prefs> {
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

#[test]
fn command_zero_resets_the_text_size_and_shift_command_n_opens_now() {
    if !isolated("text_scale::command_zero_resets_the_text_size_and_shift_command_n_opens_now") {
        return;
    }
    let dir = Dir::new("text-scale-keys");
    let mut rt = laid_out(1.6, "session", &dir);
    crate::look::take_view(crate::look::ticket(), feed::look::View::default());
    rt.frame(DT);
    crate::look::hold_text_scale(Some(1.6));
    assert_eq!(crate::look::text_scale(), Some(1.6));
    let press = |key: rune::Key, shift: bool| rune::KeyEvent { key, mods: rune::Mods { cmd: true, shift, ..rune::Mods::default() } };

    rt.key(press(rune::Key::Command('0'), false));
    rt.frame(DT);
    assert_eq!(crate::look::text_scale(), Some(1.0), "⌘0 puts the text back to its actual size");
    assert!(rt.state.screen != Screen::Now, "⌘0 leaves the session open");

    for mods in [rune::Mods { alt: true, ..rune::Mods::default() }, rune::Mods { ctrl: true, ..rune::Mods::default() }] {
        rt.key(rune::KeyEvent { key: rune::Key::Command('n'), mods: rune::Mods { cmd: true, shift: true, ..mods } });
        rt.frame(DT);
        assert!(rt.state.screen != Screen::Now, "another modifier with ⇧⌘N leaves Now alone");
    }

    rt.key(press(rune::Key::Command('n'), true));
    rt.frame(DT);
    assert!(rt.state.screen == Screen::Now, "⇧⌘N opens Now");
}

#[test]
fn every_settings_tab_can_be_reached_when_larger_text_makes_the_tabs_scroll() {
    if !isolated("text_scale::every_settings_tab_can_be_reached_when_larger_text_makes_the_tabs_scroll") {
        return;
    }
    crate::look::hold_text_scale(Some(1.6));
    let rt = settings_on(0, 0);
    let [x, _, w, _] = rt.presented("settings-tabs").expect("the tab strip is on screen").0;
    let [sx, _, sw, _] = rt.presented("settings-tabs/segment0").expect("the Overview tab is laid out").0;
    assert!(sx >= x - 0.5 && sx + sw <= x + w + 0.5, "Overview {:?} shows inside the strip {:?} at the start", (sx, sw), (x, w));
    crate::look::hold_text_scale(Some(1.0));
    let rt = settings_on(0, 0);
    let [x, _, w, _] = rt.presented("settings-tabs").unwrap().0;
    let first = rt.presented("settings-tabs/segment0").unwrap().0;
    let last = rt.presented(&format!("settings-tabs/segment{}", crate::settings::TABS.len() - 1)).unwrap().0;
    let (left, right) = (first[0] - x, x + w - (last[0] + last[2]));
    assert!(left > 8.0 && (left - right).abs() < 1.0, "the tabs sit centred when they fit: {left} vs {right}");
}

#[test]
fn the_now_toolbar_keeps_its_title_at_larger_text_and_its_labels_after_going_back() {
    if !isolated("text_scale::the_now_toolbar_keeps_its_title_at_larger_text_and_its_labels_after_going_back") {
        return;
    }
    let dir = Dir::new("text-scale-now-bar");
    let normal = laid_out(1.0, "now", &dir);
    let (plain, plain_title) = (rect(&normal, "talk"), rect(&normal, "now-leading"));
    let mut rt = laid_out(1.6, "now", &dir);
    rt.frame(DT);
    rt.frame(DT);
    let title = rect(&rt, "now-leading");
    let needs = rect(&rt, "now-trailing");
    assert!(title[2] >= plain_title[2] * 1.5, "Now keeps its whole title at 1.6: {title:?} vs {plain_title:?}");
    assert!(title[0] + title[2] <= needs[0], "the title {title:?} sits clear of the buttons {needs:?}");
    crate::look::hold_text_scale(Some(1.0));
    for _ in 0..4 {
        rt.frame(DT);
    }
    rt.settle_all();
    rt.frame(DT);
    let back = rect(&rt, "talk");
    assert!((back[2] - plain[2]).abs() < 0.5, "Talk gets its label back at 1.0: {back:?} vs {plain:?}");
}

#[test]
fn the_rail_shows_how_many_sessions_are_working_at_larger_text() {
    if !isolated("text_scale::the_rail_shows_how_many_sessions_are_working_at_larger_text") {
        return;
    }
    let dir = Dir::new("text-scale-rail");
    let plain = rect(&laid_out(1.0, "now", &dir), "sessions-count");
    let rt = laid_out(1.6, "now", &dir);
    let count = rect(&rt, "sessions-count");
    let rail = rect(&rt, "now-rail");
    assert!(count[2] >= plain[2] * 1.5, "the count shows whole: {count:?} vs {plain:?}");
    assert!(count[0] + count[2] <= rail[0] + rail[2] + 0.5, "inside the rail {rail:?}");
}

#[test]
fn the_jump_palette_holds_its_text_at_larger_sizes() {
    if !isolated("text_scale::the_jump_palette_holds_its_text_at_larger_sizes") {
        return;
    }
    let dir = Dir::new("text-scale-jump");
    let rt = laid_out(1.6, "jump", &dir);
    let panel = rect(&rt, "jump-list");
    let hint = rect(&rt, "jump-hint");
    assert!(hint[0] >= panel[0] && hint[0] + hint[2] <= panel[0] + panel[2] + 0.5, "the hint {hint:?} stays inside the palette {panel:?}");
    assert!(panel[2] > 680.0 * 1.5, "the palette grows with the text: {panel:?}");
    assert!(panel[0] >= 0.0 && panel[0] + panel[2] <= SIZE.0, "and stays in the window: {panel:?}");
}

fn said(rt: &Runtime<App>, starts: &str) -> [f32; 4] {
    rt.elements().into_iter().find(|e| !e.exiting && e.text.as_deref().is_some_and(|t| t.starts_with(starts))).map(|e| e.rect).unwrap_or_else(|| panic!("{starts} is shown"))
}

#[test]
fn a_session_at_larger_text_in_the_smallest_window_keeps_its_controls_and_message_box_inside() {
    if !isolated("text_scale::a_session_at_larger_text_in_the_smallest_window_keeps_its_controls_and_message_box_inside") {
        return;
    }
    let dir = Dir::new("text-scale-small-session");
    let mut rt = laid_out_in(1.6, "session", &dir, SMALLEST, Some(LONG_TITLE));
    rt.frame(DT);
    rt.frame(DT);
    let pane = rect(&rt, "transcript-a");
    let switch = rect(&rt, "focus-switch");
    assert!(switch[0] + switch[2] <= pane[0] + pane[2] + 0.5, "Focus and Show work {switch:?} stay inside the conversation {pane:?}");
    let field = rect(&rt, "composer");
    let k = rune_kit::Kit::neutral(true).with_text_scale(1.6);
    let hint = format!("Message {LONG_TITLE}");
    assert!(rune::text::advance(&hint, k.text(crate::theme::BODY)) > field[2], "the whole placeholder is wider than the message box {field:?}");
    let line = k.text(crate::theme::BODY).size;
    assert!(field[3] >= 2.0 * line, "the message box {field:?} is tall enough for the wrapped placeholder");
    let send = rect(&rt, "send");
    assert!(field[1] + field[3] <= send[1] + 0.5, "the message box {field:?} ends above Send {send:?}");
}

#[test]
fn now_at_larger_text_in_the_smallest_window_keeps_its_title_buttons_and_notes_inside() {
    if !isolated("text_scale::now_at_larger_text_in_the_smallest_window_keeps_its_title_buttons_and_notes_inside") {
        return;
    }
    let dir = Dir::new("text-scale-small-now");
    let mut rt = laid_out_in(1.6, "now", &dir, SMALLEST, Some(LONG_TITLE));
    rt.frame(DT);
    rt.frame(DT);
    let head = rect(&rt, "decisions-head");
    assert!(rect(&rt, "now-leading")[2] > 40.0, "Now keeps its title");
    let trailing = rect(&rt, "now-trailing");
    assert!(trailing[0] + trailing[2] <= head[0] + head[2] + 30.0, "the toolbar buttons {trailing:?} end inside the pane {head:?}");
    for note in ["from every connected computer", "Decisions show when", "Last 12 hours", "Each line opens"] {
        let r = said(&rt, note);
        assert!(r[0] + r[2] <= head[0] + head[2] + 0.5, "{note} {r:?} stays inside the pane {head:?}");
    }
}

#[test]
fn now_keeps_its_title_and_notes_when_more_things_need_you_or_the_brief_shows() {
    if !isolated("text_scale::now_keeps_its_title_and_notes_when_more_things_need_you_or_the_brief_shows") {
        return;
    }
    use crate::over::{Group, Need, NeedKind, Target};
    let dir = Dir::new("text-scale-small-brief");
    let mut rt = laid_out_in(1.6, "now", &dir, SMALLEST, Some(LONG_TITLE));
    rt.frame(DT);
    rt.update(|s, _| {
        s.ov.needs = (0..12).map(|i| Need { kind: NeedKind::Question, group: Group::Waiting, project: "Alpha".into(), project_key: "p1".into(), title: format!("Question {i}"), who: "a".into(), at: "2026-11-27T08:50:00Z".into(), target: Target::Session("a".into()), label: None }).collect();
    });
    for _ in 0..3 {
        rt.frame(DT);
    }
    assert!(rect(&rt, "now-leading")[2] > 40.0, "Now keeps its title with 12 needs");

    let mut rt = laid_out_in(1.6, "brief", &dir, SMALLEST, Some(LONG_TITLE));
    rt.frame(DT);
    rt.frame(DT);
    let pane = rect(&rt, "now-b");
    let note = said(&rt, "From the ledger");
    assert!(note[0] + note[2] <= pane[0] + pane[2] + 0.5, "the brief's note {note:?} stays inside {pane:?}");

    let mut rt = laid_out_in(1.6, "work", &dir, SMALLEST, Some(LONG_TITLE));
    rt.frame(DT);
    rt.frame(DT);
    let rail = said(&rt, "WHO'S WORKING");
    let note = said(&rt, "0 passed");
    assert!(note[0] + note[2] <= rail[0], "the evidence note {note:?} wraps before the rail {rail:?}");
}

#[test]
fn the_sidebar_shows_nows_keys_only_while_no_command_holds_them() {
    if !isolated("text_scale::the_sidebar_shows_nows_keys_only_while_no_command_holds_them") {
        return;
    }
    let dir = Dir::new("text-scale-now-keys");
    let hint = |rt: &Runtime<App>| rt.elements().into_iter().any(|e| !e.exiting && e.path.contains("home") && e.text.as_deref() == Some("⌘⇧N"));
    let rt = laid_out(1.0, "session", &dir);
    assert!(hint(&rt), "the sidebar shows ⌘⇧N for Now");
    crate::ui::bindings::change(|sc| *sc = feed::shortcuts::Shortcuts::load(Some(br#"{"refresh":{"carbonKeyCode":45,"carbonModifiers":768}}"#), None)).unwrap();
    let mut rt = laid_out(1.0, "session", &dir);
    assert!(!hint(&rt), "a Refresh saved on ⇧⌘N takes the hint away");
    rt.key(rune::KeyEvent { key: rune::Key::Command('n'), mods: rune::Mods { cmd: true, shift: true, ..rune::Mods::default() } });
    rt.frame(DT);
    assert!(rt.state.screen != Screen::Now, "and ⇧⌘N does what was saved for it");
}

#[test]
fn the_settings_tabs_show_which_side_has_more_when_larger_text_makes_them_scroll() {
    if !isolated("text_scale::the_settings_tabs_show_which_side_has_more_when_larger_text_makes_them_scroll") {
        return;
    }
    let agents = crate::settings::TABS.iter().position(|t| *t == "Agents").expect("an Agents tab");
    let sides = |scale: f32, tab: usize| {
        crate::look::hold_text_scale(Some(scale));
        let mut rt = settings_on(tab, 0);
        for _ in 0..3 {
            rt.frame(DT);
        }
        rt.settle_all();
        rt.frame(DT);
        (rt.presented("settings-tabs-before").is_some(), rt.presented("settings-tabs-after").is_some())
    };
    assert_eq!(sides(1.0, 0), (false, false), "the tabs fit at 1.0");
    for scale in [1.5, 1.6] {
        assert_eq!(sides(scale, 0), (false, true), "with Overview open at {scale}, more tabs wait to the right");
        assert_eq!(sides(scale, agents), (true, false), "with Agents open at {scale}, more tabs wait to the left");
    }
    crate::look::hold_text_scale(Some(1.6));
    let mut rt = settings_on(0, 0);
    let settle = |rt: &mut Runtime<crate::settings::Prefs>| {
        for _ in 0..40 {
            rt.frame(DT);
        }
        rt.settle_all();
        rt.frame(DT);
    };
    let press = |rt: &mut Runtime<crate::settings::Prefs>, key: &str| {
        let (x, y) = rt.find(key).unwrap_or_else(|| panic!("{key} is shown"));
        rt.pointer_move(x, y);
        rt.pointer_down(x, y);
        rt.pointer_up(x, y);
    };
    let first = |rt: &Runtime<crate::settings::Prefs>| rt.presented("settings-tabs/segment0").expect("Overview is laid out").0[0];
    settle(&mut rt);
    let start = first(&rt);
    press(&mut rt, "settings-tabs-after");
    settle(&mut rt);
    assert!(first(&rt) < start - 50.0, "the right chevron scrolls toward the later tabs: {start} -> {}", first(&rt));
    assert_eq!(rt.state.tab, 0, "and leaves the open tab alone");
    for _ in 0..4 {
        if rt.presented("settings-tabs-after").is_some() {
            press(&mut rt, "settings-tabs-after");
            settle(&mut rt);
        }
    }
    assert!(rt.presented("settings-tabs-after").is_none(), "at the end the right chevron goes");
    let end = first(&rt);
    press(&mut rt, "settings-tabs-before");
    settle(&mut rt);
    assert!(first(&rt) > end + 50.0, "the left chevron scrolls back toward Overview: {end} -> {}", first(&rt));
    assert_eq!(rt.state.tab, 0, "while Overview stays open");

    rt.update(|_, tx| tx.scroll_to_x("settings-tabs", 0.0));
    settle(&mut rt);
    assert!(rt.presented("settings-tabs-before").is_none(), "back at the start the left chevron goes");
    rt.update(|_, tx| tx.scroll_to_x("settings-tabs", 10_000.0));
    settle(&mut rt);
    let scrolled = first(&rt);
    assert!(rt.presented("settings-tabs-after").is_none() && rt.presented("settings-tabs-before").is_some(), "scrolled by hand to the end, only the left chevron shows");
    press(&mut rt, "settings-tabs-before");
    settle(&mut rt);
    assert!(first(&rt) > scrolled + 50.0 && rt.state.tab == 0, "after a scroll by hand the left chevron still moves the strip: {scrolled} -> {}", first(&rt));
}

#[test]
fn now_keeps_every_toolbar_button_clear_of_the_title_and_fade_at_every_width() {
    if !isolated("text_scale::now_keeps_every_toolbar_button_clear_of_the_title_and_fade_at_every_width") {
        return;
    }
    let dir = Dir::new("text-scale-now-widths");
    for scale in [1.0, 1.5, 1.6] {
        let k = rune_kit::Kit::neutral(true).with_text_scale(scale);
        let (floor, fade) = (k.text_size(96.0), k.text_size(16.0).max(16.0));
        for w in (1100..=1440).step_by(20) {
            let mut rt = laid_out_in(scale, "now", &dir, (w as f32, 900.0), None);
            for _ in 0..4 {
                rt.frame(DT);
            }
            let (title, buttons) = (rect(&rt, "now-leading"), rect(&rt, "now-trailing"));
            assert!(buttons[0] >= title[0] + title[2].max(floor) + fade - 0.5, "{scale} at {w}: the buttons {buttons:?} start clear of the title's room {title:?} and the fade");
        }
    }
}

#[test]
fn now_drops_its_button_labels_in_steps_as_room_runs_out() {
    if !isolated("text_scale::now_drops_its_button_labels_in_steps_as_room_runs_out") {
        return;
    }
    let dir = Dir::new("text-scale-now-steps");
    let shown = |scale: f32, size: (f32, f32)| {
        let mut rt = laid_out_in(scale, "now", &dir, size, None);
        for _ in 0..3 {
            rt.frame(DT);
        }
        let labelled = |starts: &str| rt.elements().into_iter().any(|e| !e.exiting && e.path.contains("now-trailing") && e.text.as_deref().is_some_and(|t| t.starts_with(starts)));
        (labelled("Talk"), labelled("Three panes"), labelled("0 need you"), rect(&rt, "now-leading")[2] > 30.0)
    };
    assert_eq!(shown(1.0, SIZE), (true, true, true, true), "at 1.0 every label shows");
    assert_eq!(shown(1.5, SIZE), (false, true, true, true), "at 1.5 Talk and New session go to icons first");
    assert_eq!(shown(1.6, SMALLEST), (false, false, false, true), "in the smallest window everything is an icon and Now keeps its title");
}
