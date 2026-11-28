use rune::{ElementInfo, Runtime};
use serde_json::json;

use super::text_scale::{SETTINGS, settings_on};
use super::{DT, isolated};
use crate::settings::{Prefs, TABS, drafted_agent_role, show_agent_roles};

const REACH: &str = "Reach";
const RENAME: &str = "Rename its own project";

fn roles_with_actions(scale: f32) -> Runtime<Prefs> {
    crate::look::hold_text_scale(Some(scale));
    let agents = TABS.iter().position(|t| *t == "Agents").expect("an Agents tab");
    let mut rt = settings_on(agents, 0);
    rt.update(|s, _| {
        let roles = serde_json::from_value(json!({
            "roles": [{"name": "lead", "builtIn": true, "reach": "project", "definition": {"reach": "project"}, "effectiveDefaults": {"project-rename": "allow"}}],
            "actions": [
                {"name": "session-create-remote", "default": "notify", "detail": "Start a session on a paired computer"},
                {"name": "project-rename", "default": "allow", "detail": RENAME},
            ],
        }))
        .unwrap();
        show_agent_roles(s, roles, "lead");
    });
    settle(&mut rt);
    rt
}

fn settle(rt: &mut Runtime<Prefs>) {
    rt.frame(DT);
    rt.settle_all();
    rt.frame(DT);
}

fn named(rt: &Runtime<Prefs>, role: &str, name: &str) -> Option<ElementInfo> {
    rt.elements().into_iter().find(|e| e.role == role && !e.exiting && e.label.as_deref() == Some(name))
}

fn field(rt: &Runtime<Prefs>, name: &str) -> [f32; 4] {
    named(rt, "combobox", name).unwrap_or_else(|| panic!("the {name} field is on screen")).rect
}

fn press(rt: &mut Runtime<Prefs>, r: [f32; 4]) {
    let (x, y) = (r[0] + r[2] * 0.5, r[1] + r[3] * 0.5);
    rt.pointer_move(x, y);
    rt.pointer_down(x, y);
    rt.pointer_up(x, y);
    settle(rt);
}

fn reveal(rt: &mut Runtime<Prefs>, name: &str) {
    for _ in 0..40 {
        let r = field(rt, name);
        if r[1] >= 0.0 && r[1] + r[3] <= SETTINGS.1 {
            return;
        }
        rt.wheel(SETTINGS.0 * 0.6, SETTINGS.1 * 0.6, 80.0);
        settle(rt);
    }
    panic!("the {name} field scrolls into view: {:?}", field(rt, name));
}

fn open(rt: &mut Runtime<Prefs>, name: &str) {
    reveal(rt, name);
    let r = field(rt, name);
    press(rt, r);
}

fn list(rt: &Runtime<Prefs>, name: &str) -> Option<ElementInfo> {
    named(rt, "listbox", name).filter(|l| l.opacity > 0.01)
}

fn option(rt: &Runtime<Prefs>, name: &str, label: &str) -> [f32; 4] {
    let inside = format!("{}/", list(rt, name).unwrap_or_else(|| panic!("{name} opens its list")).path);
    rt.elements()
        .into_iter()
        .find(|e| e.role == "option" && e.path.starts_with(&inside) && e.label.as_deref() == Some(label))
        .unwrap_or_else(|| panic!("{name} lists {label}"))
        .rect
}

fn options_laid_out(rt: &Runtime<Prefs>, name: &str, labels: &[&str]) {
    let [_, ty, _, th] = field(rt, name);
    let open = named(rt, "listbox", name).unwrap_or_else(|| panic!("{name} opens its list"));
    let l = open.rect;
    assert!(l[2] > 0.0 && l[3] > 0.0 && open.opacity > 0.01, "{name}'s list shows with a size: {l:?} at opacity {}", open.opacity);
    assert!(l[1] >= ty + th - 0.5 || l[1] + l[3] <= ty + 0.5, "{name}'s list {l:?} sits beside its field at y {ty}..{}", ty + th);
    for label in labels {
        let r = option(rt, name, label);
        assert!(r[2] > 0.0 && r[3] > 0.0, "{label} in {name} has a size: {r:?}");
        assert!(r[1] >= l[1] - 0.5 && r[1] + r[3] <= l[1] + l[3] + 0.5, "{label} {r:?} shows inside {name}'s list {l:?}");
    }
}

struct Shots {
    gpu: rune::render::Gpu,
    renderer: rune::render::Renderer,
    target: rune::render::Offscreen,
    dir: std::path::PathBuf,
}

impl Shots {
    fn new() -> Option<Shots> {
        let dir = std::path::PathBuf::from(std::env::var_os("AGENTHAIL_SHOT_DIR")?);
        let gpu = rune::render::Gpu::headless();
        let renderer = rune::render::Renderer::new(&gpu.device, rune::render::OFFSCREEN_FORMAT);
        let target = rune::render::Offscreen::new(&gpu.device, SETTINGS.0 as u32, SETTINGS.1 as u32);
        Some(Shots { gpu, renderer, target, dir })
    }

    fn take(&mut self, rt: &mut Runtime<Prefs>, name: &str, scale: f32) {
        rt.frame(DT);
        self.renderer.render_runtime_to_offscreen(&self.gpu.device, &self.gpu.queue, &self.target, rt);
        let rgba = self.target.read(&self.gpu.device);
        rune::render::write_png(&self.dir.join(format!("{name}-{scale}.png")), SETTINGS.0 as u32, SETTINGS.1 as u32, &rgba).unwrap();
    }
}

fn pick(rt: &mut Runtime<Prefs>, name: &str, label: &str) {
    let r = option(rt, name, label);
    press(rt, r);
    assert!(list(rt, name).is_none(), "picking {label} closes {name}'s list");
}

fn reach_and_an_action_pick_at(scale: f32) {
    let mut rt = roles_with_actions(scale);
    let mut shots = Shots::new();
    open(&mut rt, REACH);
    options_laid_out(&rt, REACH, &["Sessions it started", "Its project", "Every session"]);
    if let Some(shots) = &mut shots {
        shots.take(&mut rt, "reach-open", scale);
    }
    pick(&mut rt, REACH, "Every session");
    assert_eq!(drafted_agent_role(&rt.state).map(|d| d.reach.as_str()), Some("all"));

    open(&mut rt, RENAME);
    options_laid_out(&rt, RENAME, &["Default", "Allow", "Notify", "Ask"]);
    if let Some(shots) = &mut shots {
        shots.take(&mut rt, "action-open", scale);
    }
    pick(&mut rt, RENAME, "Ask");
    let actions = drafted_agent_role(&rt.state).and_then(|d| d.actions.clone()).unwrap_or_default();
    assert_eq!(actions.get("project-rename").map(String::as_str), Some("ask"));
}

#[test]
fn role_reach_and_action_pickers_list_their_options_and_pick_one() {
    if !isolated("role_pickers::role_reach_and_action_pickers_list_their_options_and_pick_one") {
        return;
    }
    reach_and_an_action_pick_at(1.0);
}

#[test]
fn role_pickers_list_their_options_at_larger_text() {
    if !isolated("role_pickers::role_pickers_list_their_options_at_larger_text") {
        return;
    }
    reach_and_an_action_pick_at(1.6);
}
