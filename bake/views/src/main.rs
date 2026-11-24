mod data;
mod jump;
mod now;
mod project;
mod session;
mod sessions;
mod ui;
mod work;

use std::collections::HashSet;

use rune::{AppSpec, Chrome, Cx, El, Key, KeyEvent, TextEdit, Tx};
use rune_kit::shell::{self, PaneState};

use crate::data::{Data, Need, Target};

#[derive(Clone, PartialEq, Debug)]
pub enum Screen {
    Now,
    Session(String),
    Project(String),
    Work(String),
    Sessions,
}

pub struct App {
    pub d: Data,
    pub needs: Vec<Need>,
    pub hands_on: Vec<String>,
    pub screen: Screen,
    pub back: Vec<Screen>,
    pub now_b: bool,
    pub board: bool,
    pub sidebar: PaneState,
    pub inspector: PaneState,
    pub jump: Option<TextEdit>,
    pub jump_ix: usize,
    pub composer: TextEdit,
    pub need_ix: usize,
    pub open_folds: HashSet<String>,
    pub to_lead: bool,
    pub to_open: bool,
    pub notice: Option<String>,
    pub ask: Option<data::Ask>,
    pub ask_pick: Option<usize>,
    pub replayed: bool,
    pub sessions_filter: usize,
    pub sessions_project: Option<String>,
    pub sessions_project_open: bool,
    pub sessions_query: TextEdit,
}

fn arg(name: &str) -> Option<String> {
    let args = rune::extra_args();
    args.iter().position(|a| a == name).and_then(|i| args.get(i + 1)).cloned()
}

fn init() -> App {
    let json = arg("--fixture").and_then(|p| std::fs::read_to_string(p).ok());
    let d = Data::load(json.as_deref().unwrap_or(data::FIXTURE));
    let mut needs = d.needs();
    let replay = !rune::extra_args().iter().any(|a| a == "--no-replay");
    let ask = if replay { d.last_ask() } else { None };
    if let Some(a) = &ask {
        if let Some(x) = d.session(&a.session) {
            needs.push(Need { kind: data::NeedKind::Question, project: x.project_name(), project_key: x.project_key(), title: a.question.clone(), who: d.title(x), at: a.at.clone(), target: Target::Session(a.session.clone()), label: None });
        }
    }
    let hands_on = d.hands_on().iter().map(|s| s.id.clone()).collect();
    needs.sort_by(|a, b| a.kind.rank().cmp(&b.kind.rank()).then(a.at.cmp(&b.at)));
    let mut app = App {
        d,
        needs,
        hands_on,
        screen: Screen::Now,
        back: Vec::new(),
        now_b: false,
        board: true,
        sidebar: PaneState::new(300.0).range(200.0, 480.0),
        inspector: PaneState::new(340.0).range(260.0, 520.0),
        jump: None,
        jump_ix: 0,
        composer: TextEdit::new(""),
        need_ix: 0,
        open_folds: HashSet::new(),
        to_lead: true,
        to_open: false,
        notice: None,
        replayed: ask.is_some(),
        ask,
        ask_pick: None,
        sessions_filter: 0,
        sessions_project: None,
        sessions_project_open: false,
        sessions_query: TextEdit::new(""),
    };
    let view = arg("--view").unwrap_or_else(|| "now-b".into());
    match view.as_str() {
        "now-a" => app.now_b = false,
        "now-b" => app.now_b = true,
        "session" => {
            let id = arg("--session").or_else(|| app.hands_on.first().cloned()).unwrap_or_default();
            app.screen = Screen::Session(id);
        }
        "project-a" | "project-b" => {
            app.board = view == "project-b";
            let key = arg("--project").or_else(|| app.d.projects.first().map(|p| p.key.clone())).unwrap_or_default();
            app.screen = Screen::Project(key);
        }
        "sessions" => app.screen = Screen::Sessions,
        "sessions-working" => {
            app.screen = Screen::Sessions;
            app.sessions_filter = 1;
        }
        "work" => app.screen = Screen::Work(arg("--item").unwrap_or_else(|| "agenthail-W595".into())),
        "jump" => {
            app.now_b = true;
            open_jump(&mut app, arg("--query").as_deref().unwrap_or(""));
        }
        _ => {}
    }
    app
}

pub fn go(s: &mut App, to: Screen) {
    if s.screen != to {
        let from = std::mem::replace(&mut s.screen, to);
        s.back.push(from);
    }
    s.jump = None;
    s.composer = TextEdit::new("");
    s.to_lead = true;
    s.notice = None;
}

pub fn open_target(s: &mut App, t: &Target) {
    match t {
        Target::Session(id) => go(s, Screen::Session(id.clone())),
        Target::Work(id) => go(s, Screen::Work(id.clone())),
        Target::Project(key) => go(s, Screen::Project(key.clone())),
    }
}

pub fn next_need(s: &mut App) {
    if s.needs.is_empty() {
        return;
    }
    let current = match &s.screen {
        Screen::Session(id) => s.needs.iter().position(|n| n.target == Target::Session(id.clone())),
        Screen::Work(id) => s.needs.iter().position(|n| n.target == Target::Work(id.clone())),
        _ => None,
    };
    let ix = current.map(|i| (i + 1) % s.needs.len()).unwrap_or(0);
    s.need_ix = ix;
    let t = s.needs[ix].target.clone();
    open_target(s, &t);
}

pub fn open_jump(s: &mut App, query: &str) {
    let mut edit = TextEdit::new(query);
    edit.focused = true;
    s.jump = Some(edit);
    s.jump_ix = 0;
}

pub fn act(s: &mut App, what: &str) {
    s.notice = Some(format!("{what}: preview only, nothing was sent"));
}

fn on_key(s: &mut App, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.is_command('k') {
        if s.jump.is_some() {
            s.jump = None;
        } else {
            open_jump(s, "");
            tx.focus("jump-input", false);
        }
        return true;
    }
    if s.jump.is_some() {
        if !ev.mods.cmd {
            return jump::key(s, ev, tx);
        }
        s.jump = None;
    }
    if ev.is_command('s') && ev.mods.shift {
        go(s, Screen::Sessions);
        return true;
    }
    if ev.is_command('j') {
        next_need(s);
        return true;
    }
    if ev.is_command('0') {
        go(s, Screen::Now);
        return true;
    }
    for (i, c) in ['1', '2', '3', '4', '5'].iter().enumerate() {
        if ev.is_command(*c) {
            if let Some(id) = s.hands_on.get(i).cloned() {
                go(s, Screen::Session(id));
            }
            return true;
        }
    }
    if ev.mods.ctrl && matches!(ev.key, Key::Tab) {
        if !s.hands_on.is_empty() {
            let at = match &s.screen {
                Screen::Session(id) => s.hands_on.iter().position(|x| x == id),
                _ => None,
            };
            let n = s.hands_on.len();
            let ix = match at {
                Some(i) if ev.mods.shift => (i + n - 1) % n,
                Some(i) => (i + 1) % n,
                None => 0,
            };
            let id = s.hands_on[ix].clone();
            go(s, Screen::Session(id));
        }
        return true;
    }
    if shell::is_sidebar_toggle(ev) {
        s.sidebar.toggle();
        return true;
    }
    if s.composer.focused {
        return false;
    }
    match &ev.key {
        Key::Escape => {
            if let Some(prev) = s.back.pop() {
                s.screen = prev;
            }
            true
        }
        Key::Text(t) if !ev.mods.cmd && !ev.mods.ctrl => match t.to_lowercase().as_str() {
            "l" => {
                if let Screen::Project(key) = &s.screen {
                    if let Some(lead) = s.d.project(key).and_then(|p| p.lead.clone()) {
                        go(s, Screen::Session(lead));
                    }
                }
                true
            }
            "v" => {
                match s.screen {
                    Screen::Now => s.now_b = !s.now_b,
                    Screen::Project(_) => s.board = !s.board,
                    _ => {}
                }
                true
            }
            "a" if matches!(s.screen, Screen::Work(_)) => {
                act(s, "Approved outcome");
                true
            }
            "c" if matches!(s.screen, Screen::Work(_)) => {
                act(s, "Ask for changes");
                true
            }
            _ => false,
        },
        Key::Enter => {
            if matches!(s.screen, Screen::Now) {
                if let Some(t) = s.needs.get(s.need_ix).map(|n| n.target.clone()) {
                    open_target(s, &t);
                }
                return true;
            }
            false
        }
        Key::Down if matches!(s.screen, Screen::Now) => {
            s.need_ix = (s.need_ix + 1).min(s.needs.len().saturating_sub(1));
            true
        }
        Key::Up if matches!(s.screen, Screen::Now) => {
            s.need_ix = s.need_ix.saturating_sub(1);
            true
        }
        _ => false,
    }
}

fn view(s: &App, cx: &mut Cx<App>) -> El {
    let mut layer = Vec::new();
    let body = match &s.screen {
        Screen::Now => {
            if s.now_b {
                now::brief(s, cx, &mut layer)
            } else {
                now::panes(s, cx, &mut layer)
            }
        }
        Screen::Session(id) => session::view(s, cx, id, &mut layer),
        Screen::Project(key) => {
            if s.board {
                project::board(s, cx, key, &mut layer)
            } else {
                project::lead(s, cx, key, &mut layer)
            }
        }
        Screen::Work(id) => work::view(s, cx, id, &mut layer),
        Screen::Sessions => sessions::view(s, cx, &mut layer),
    };
    if s.jump.is_some() {
        jump::view(s, &mut layer);
    }
    if let Some(n) = &s.notice {
        layer.push(ui::notice(n));
    }
    rune_kit::root(body, layer)
}

fn main() {
    let chrome = Chrome { full_size_content: true, traffic_lights: Some((16.0, 25.0)), drag_regions: true };
    rune::App::new(AppSpec {
        on_key: Some(on_key),
        fonts: rune_theme_opencode::FONTS,
        families: rune_theme_opencode::FAMILIES,
        min_size: Some((1100.0, 700.0)),
        ..AppSpec::new("Agenthail", (1280.0, 800.0), init, view)
    })
    .chrome(chrome)
    .run();
}
