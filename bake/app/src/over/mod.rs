pub mod decisions;
pub mod earlier;
pub mod extension_sheets;
pub mod extensions;
pub mod inbox;
pub mod jump;
pub mod layout;
pub mod listing;
mod now;
mod project;
pub mod projects;
pub mod rows;
pub mod search;
pub mod sessions;
pub mod ui;
pub mod work;
pub mod worksearch;

use rune_kit::Kit;
use std::cell::RefCell;
use std::collections::{HashMap, HashSet};
use std::rc::Rc;

use rune::{Cx, El, Key, KeyEvent, TextEdit, Tx};
use rune_kit::shell::{self, PaneState};

pub use feed::derive::text::{clip_words, plain, short_id};
pub use feed::derive::live::Overview;
pub use feed::derive::{GROUPS, Group, Happened, Line, Need, NeedKind, PHASES, Project, Target, Work, WorkKey, World};

use crate::{App, Screen};

pub struct Over {
    pub live: Overview,
    pub d: World,
    pub needs: Vec<Need>,
    pub hands_on: Vec<String>,
    pub back: Vec<Screen>,
    pub now_brief: bool,
    pub now_side: PaneState,
    pub now_rail: PaneState,
    pub now_centre_hidden: bool,
    pub now_focus: Option<(usize, [bool; 3])>,
    pub boards: HashMap<String, bool>,
    pub looked_now: Option<String>,
    pub looked: HashMap<String, String>,
    pub persist: bool,
    pub sidebar: PaneState,
    pub inspector: PaneState,
    pub jump: Option<TextEdit>,
    pub jump_ix: (String, usize),
    pub need_ix: usize,
    pub open_lanes: HashSet<String>,
    pub widths: HashMap<String, f32>,
    pub notice: Option<String>,
    pub list: listing::State,
    pub sessions_project: Option<String>,
    pub sessions_project_open: bool,
    pub sessions_query: TextEdit,
    pub inbox: inbox::State,
    pub ext: extensions::State,
    pub search: search::State,
    pub projects: projects::State,
    pub work: work::State,
    pub worksearch: worksearch::State,
    digests: RefCell<Option<(Option<String>, Digests)>>,
}

pub type Digests = Rc<Vec<(Project, Vec<Line>)>>;

impl Over {
    pub fn new(mut live: Overview, persist: bool) -> Over {
        let d = live.world(&[]);
        let needs = d.needs();
        Over {
            hands_on: d.hands_on(&[]).iter().map(|x| x.id.clone()).collect(),
            needs,
            live,
            d,
            back: Vec::new(),
            now_brief: false,
            now_side: PaneState::new(300.0).range(200.0, 480.0),
            now_rail: PaneState::new(340.0).range(260.0, 520.0),
            now_centre_hidden: false,
            now_focus: None,
            boards: HashMap::new(),
            looked_now: None,
            looked: HashMap::new(),
            persist,
            sidebar: PaneState::new(300.0).range(200.0, 480.0),
            inspector: PaneState::new(340.0).range(260.0, 520.0),
            jump: None,
            jump_ix: (String::new(), 0),
            need_ix: 0,
            open_lanes: HashSet::new(),
            widths: HashMap::new(),
            notice: None,
            list: listing::State::default(),
            sessions_project: None,
            sessions_project_open: false,
            sessions_query: TextEdit::new(""),
            inbox: inbox::State::default(),
            ext: extensions::State::new(),
            search: search::State::default(),
            projects: projects::State::default(),
            work: work::State::default(),
            worksearch: worksearch::State::default(),
            digests: RefCell::new(None),
        }
    }

    pub fn rebuild(&mut self, panes: &[(&str, &[feed::Item])]) {
        let d = self.live.world(panes);
        let focused = self.needs.get(self.need_ix).map(|n| n.target.clone());
        self.hands_on = d.hands_on(&[]).iter().map(|x| x.id.clone()).collect();
        self.needs = d.needs();
        self.d = d;
        self.worksearch.keep(&mut self.d);
        self.world_changed();
        feed::derive::needs::arrange(&mut self.needs, self.looked_now.as_deref());
        self.refocus(focused);
    }

    pub fn world_changed(&mut self) {
        self.digests.get_mut().take();
    }

    pub fn digests(&self) -> Digests {
        let looked = self.looked_now.clone();
        let mut held = self.digests.borrow_mut();
        if let Some((_, digests)) = held.as_ref().filter(|(at, _)| *at == looked) {
            return digests.clone();
        }
        let digests: Digests = Rc::new(self.d.digests(self.d.window(looked.as_deref())).into_iter().map(|(p, lines)| (p.clone(), lines)).collect());
        *held = Some((looked, digests.clone()));
        digests
    }

    pub fn arrange(&mut self) {
        let focused = self.needs.get(self.need_ix).map(|n| n.target.clone());
        feed::derive::needs::arrange(&mut self.needs, self.looked_now.as_deref());
        self.refocus(focused);
    }

    fn refocus(&mut self, focused: Option<Target>) {
        self.need_ix = match focused.and_then(|t| self.needs.iter().position(|n| n.target == t)) {
            Some(at) => at,
            None => self.need_ix.min(self.needs.len().saturating_sub(1)),
        };
    }

    pub fn looked_at(&self, screen: &Screen) -> Option<&str> {
        match screen {
            Screen::Now => self.looked_now.as_deref(),
            Screen::Project(key) => self.looked.get(key).map(String::as_str),
            _ => None,
        }
    }
}

fn leave(s: &mut App) {
    let Ok(at) = s.ov.d.now.format(&time::format_description::well_known::Rfc3339) else { return };
    match &s.screen {
        Screen::Now => {
            s.ov.looked_now = Some(at);
            s.ov.arrange();
        }
        Screen::Project(key) => {
            s.ov.looked.insert(key.clone(), at);
        }
        _ => {}
    }
}

fn lead_of(s: &App, key: &str) -> Option<String> {
    s.ov.d.project(key).and_then(|p| p.lead.clone())
}

pub fn arrive(s: &mut App) {
    match s.screen.clone() {
        Screen::Project(key) => {
            crate::fetch_ledger(s, &key);
            if let Some(lead) = lead_of(s, &key) {
                crate::load_pane(s, &lead);
            }
        }
        Screen::Session => {
            if let Some(id) = s.selected.clone() {
                crate::load_pane(s, &id);
            }
        }
        Screen::Extensions | Screen::Extension(_) => extensions::arrived(s),
        _ => {}
    }
}

pub fn go(s: &mut App, to: Screen) {
    crate::link::moved();
    if s.screen != to {
        leave(s);
        let from = std::mem::replace(&mut s.screen, to);
        s.ov.back.push(from);
    }
    left_overlays(s);
    crate::switch_composer(s);
    arrive(s);
    landed(s);
}

pub fn close_palette(s: &mut App) {
    if crate::ui::window::shows(s, crate::ui::window::Kind::Jump) {
        s.ov.jump = None;
    }
}

fn left_overlays(s: &mut App) {
    close_palette(s);
    if s.windows.current() == crate::ui::window::MAIN {
        s.ov.notice = None;
    }
}

fn landed(s: &mut App) {
    if let Screen::Work(key) = s.screen.clone() {
        worksearch::reread(s, &key);
    }
}

pub fn composer_target(s: &App) -> Option<String> {
    match &s.screen {
        Screen::Session => s.selected.clone(),
        Screen::Project(key) => lead_of(s, key).map(|lead| feed::composer::current(&s.rekeyed, &lead).to_string()),
        _ => None,
    }
}

pub fn open_session(s: &mut App, id: &str) {
    if s.screen != Screen::Session {
        leave(s);
        s.ov.back.push(s.screen.clone());
    }
    left_overlays(s);
    crate::select(s, id);
}

pub fn open_target(s: &mut App, t: &Target) {
    match t {
        Target::Session(id) => open_session(s, id),
        Target::Work(key) => go(s, Screen::Work(key.clone())),
        Target::Project(key) => go(s, Screen::Project(key.clone())),
    }
}

fn current_session(s: &App) -> Option<&str> {
    match s.screen {
        Screen::Session => s.selected.as_deref(),
        _ => None,
    }
}

pub fn next_need(s: &mut App) {
    if s.ov.needs.is_empty() {
        return;
    }
    let current = match (&s.screen, current_session(s)) {
        (_, Some(id)) => s.ov.needs.iter().position(|n| n.target == Target::Session(id.to_string())),
        (Screen::Work(key), _) => s.ov.needs.iter().position(|n| matches!(&n.target, Target::Work(k) if k == key)),
        _ => None,
    };
    let ix = current.map(|i| (i + 1) % s.ov.needs.len()).unwrap_or(0);
    s.ov.need_ix = ix;
    let t = s.ov.needs[ix].target.clone();
    open_target(s, &t);
}

pub fn open_jump(s: &mut App, tx: &mut Tx) {
    if s.ov.jump.is_some() && s.windows.current() != crate::ui::window::MAIN {
        crate::ui::window::bring_forward(s, crate::ui::window::MAIN);
        return;
    }
    let mut edit = TextEdit::new("");
    edit.focused = true;
    s.ov.jump = Some(edit);
    s.ov.jump_ix = (String::new(), 0);
    search::forget_codex(s);
    worksearch::forget(s);
    crate::ui::window::give(s, crate::ui::window::Kind::Jump, crate::ui::window::MAIN);
    if s.windows.current() == crate::ui::window::MAIN {
        tx.focus("jump-input", false);
    } else {
        crate::ui::window::bring_forward(s, crate::ui::window::MAIN);
    }
}

pub fn note_open(s: &App) -> bool {
    s.ov.inbox.note.is_some() || s.ov.work.words.is_some()
}

pub fn work_review(s: &App, key: &WorkKey) -> Option<feed::decisions::Decision> {
    let w = s.ov.d.work.get(key)?;
    let inbox = s.ov.inbox.inbox.as_ref()?;
    feed::review::of_work(&inbox.decisions, w)
}

fn review_key(s: &mut App, key: &WorkKey, verdict: feed::review::Verdict) {
    let Some(d) = work_review(s, key) else { return };
    match verdict {
        feed::review::Verdict::Approve => inbox::review(s, &d, verdict, ""),
        feed::review::Verdict::Reject => inbox::ask_changes(s, &d),
    }
}

pub fn view(s: &App, cx: &mut Cx<App>, layer: &mut Vec<El>, main_kit: &Kit) -> El {
    match &s.screen {
        Screen::Now if s.ov.now_brief => now::brief(s, cx, layer, main_kit),
        Screen::Now => now::panes(s, cx, layer, main_kit),
        Screen::Project(key) if layout::board_for(s, key) => project::board(s, cx, key, layer, main_kit),
        Screen::Project(key) => project::lead(s, cx, key, layer, main_kit),
        Screen::Work(key) => work::view(s, cx, key, layer, main_kit),
        Screen::Sessions => sessions::view(s, cx, layer, main_kit),
        Screen::Extensions => extensions::hub(s, cx, main_kit),
        Screen::Extension(id) => extensions::page(s, cx, id, main_kit),
        Screen::Session => rune::col(),
    }
}

pub fn overlays(s: &App, layer: &mut Vec<El>, k: &Kit) {
    use crate::ui::window::{Kind, shows};
    if s.ov.jump.is_some() && shows(s, Kind::Jump) {
        jump::view(s, layer, k);
    }
    if shows(s, Kind::Activity) {
        search::activity_view(s, layer, k);
    }
    if shows(s, Kind::Note) {
        decisions::note_dialog(s, layer, k);
        work::words_dialog(s, layer, k);
    }
    if shows(s, Kind::Projects) {
        projects::overlays(s, layer, k);
    }
    if shows(s, Kind::Sheets) {
        extension_sheets::overlays(s, layer, k);
    }
}

pub fn on_key(s: &mut App, ev: &KeyEvent, tx: &mut Tx) -> bool {
    use crate::ui::window::{Kind, shows};
    if s.ov.ext.sheets.is_open() && shows(s, Kind::Sheets) {
        return matches!(ev.key, Key::Escape) && extension_sheets::escape(s);
    }
    if projects::open(s) && shows(s, Kind::Projects) {
        if matches!(ev.key, Key::Escape) {
            projects::close_top(s);
            return true;
        }
        return false;
    }
    if s.ov.inbox.note.is_some() && shows(s, Kind::Note) {
        if matches!(ev.key, Key::Escape) {
            s.ov.inbox.note = None;
            return true;
        }
        return false;
    }
    if s.ov.work.words.is_some() && shows(s, Kind::Note) {
        if matches!(ev.key, Key::Escape) {
            s.ov.work.words = None;
            return true;
        }
        return false;
    }
    if shows(s, Kind::Activity)
        && let Some(done) = search::activity_key(s, ev)
    {
        return done;
    }
    if crate::ui::keys::is(crate::ui::keys::Act::GoToSession, ev, tx) {
        if s.ov.jump.is_some() && shows(s, Kind::Jump) {
            s.ov.jump = None;
        } else {
            open_jump(s, tx);
        }
        return true;
    }
    if s.ov.jump.is_some() && shows(s, Kind::Jump) {
        if !ev.mods.cmd {
            return jump::key(s, ev, tx);
        }
        s.ov.jump = None;
    }
    ordinary(s, ev, tx)
}

fn ordinary(s: &mut App, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if crate::ui::keys::on_key(s, ev, tx) {
        return true;
    }
    if ev.mods.cmd && ev.mods.alt {
        let base = tx.key_base().map(|c| c.to_ascii_lowercase());
        match (&s.screen, base) {
            (Screen::Now, Some(c @ ('1' | '2' | '3'))) => {
                let i = c as usize - '1' as usize;
                if ev.mods.shift { layout::focus_pane(s, i) } else { layout::toggle_pane(s, i) }
                return true;
            }
            (Screen::Now, Some('b')) => {
                s.ov.now_brief = !s.ov.now_brief;
                return true;
            }
            (Screen::Project(key), Some('b')) => {
                let key = key.clone();
                layout::toggle_board(s, &key);
                return true;
            }
            _ => {}
        }
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
        if ev.is_command(*c) && !ev.mods.alt {
            if let Some(id) = s.ov.hands_on.get(i).cloned() {
                open_session(s, &id);
            }
            return true;
        }
    }
    if ev.mods.ctrl && matches!(ev.key, Key::Tab) {
        if !s.ov.hands_on.is_empty() {
            let at = current_session(s).and_then(|id| s.ov.hands_on.iter().position(|x| x == id));
            let n = s.ov.hands_on.len();
            let ix = match at {
                Some(i) if ev.mods.shift => (i + n - 1) % n,
                Some(i) => (i + 1) % n,
                None => 0,
            };
            let id = s.ov.hands_on[ix].clone();
            open_session(s, &id);
        }
        return true;
    }
    if shell::is_sidebar_toggle(ev) {
        if s.screen == Screen::Now && !s.ov.now_brief {
            layout::toggle_pane(s, 0);
        } else {
            s.ov.sidebar.toggle();
        }
        return true;
    }
    if s.composer.focused {
        return false;
    }
    match &ev.key {
        Key::Escape => {
            if s.screen == Screen::Now && !s.ov.now_brief && layout::restore(s) {
                return true;
            }
            if let Some(prev) = s.ov.back.pop() {
                crate::link::moved();
                leave(s);
                s.screen = prev;
                crate::switch_composer(s);
                arrive(s);
                landed(s);
            }
            true
        }
        Key::Text(t) if !ev.mods.cmd && !ev.mods.ctrl => match t.to_lowercase().as_str() {
            "l" if matches!(s.screen, Screen::Project(_)) => {
                if let Screen::Project(key) = &s.screen
                    && let Some(lead) = lead_of(s, key)
                {
                    open_session(s, &lead);
                }
                true
            }
            "v" => match s.screen.clone() {
                Screen::Now => {
                    s.ov.now_brief = !s.ov.now_brief;
                    true
                }
                Screen::Project(key) => {
                    layout::toggle_board(s, &key);
                    true
                }
                _ => false,
            },
            "a" | "c" if matches!(s.screen, Screen::Work(_)) => {
                if let Screen::Work(key) = s.screen.clone()
                    && s.ov.d.work.get(&key).is_some_and(|w| feed::derive::phase(w) == feed::derive::phase::CHECK)
                {
                    let verdict = if t.eq_ignore_ascii_case("a") { feed::review::Verdict::Approve } else { feed::review::Verdict::Reject };
                    review_key(s, &key, verdict);
                }
                true
            }
            _ => false,
        },
        Key::Enter if s.screen == Screen::Now && ev.mods.shift => inbox::accept_first(s),
        Key::Enter if s.screen == Screen::Now => {
            if let Some(t) = s.ov.needs.get(s.ov.need_ix).map(|n| n.target.clone()) {
                open_target(s, &t);
            }
            true
        }
        Key::Down if s.screen == Screen::Now => {
            s.ov.need_ix = (s.ov.need_ix + 1).min(s.ov.needs.len().saturating_sub(1));
            true
        }
        Key::Up if s.screen == Screen::Now => {
            s.ov.need_ix = s.ov.need_ix.saturating_sub(1);
            true
        }
        _ => false,
    }
}
