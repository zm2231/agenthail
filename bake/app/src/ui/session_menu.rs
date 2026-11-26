use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::catalog::{Client, RespondError};
use feed::menu::{self, Act, Patch};
use feed::settings::{self, Chip, Choice, ContextUsage, ModelOption, Pick, SessionSettings};
use rune::{PointerPress, TextEdit};

use crate::{App, Screen, open_link, session};

pub use super::session_dialogs::{button, marks, overlays};

const REFRESH: Duration = Duration::from_secs(30);

pub enum Done {
    Settings(String, u64, Result<SessionSettings, String>),
    Models(String, Vec<ModelOption>),
    Context(String, u64, Result<Option<ContextUsage>, String>),
    Acted(String, Act, String, Result<Option<feed::action::ReadState>, RespondError>),
    Renamed(String, u64, Result<String, RespondError>),
    Set(String, &'static str, String, Result<(), RespondError>),
    Compacted(String, Result<(), RespondError>),
}

pub struct Open {
    pub session: String,
    pub at: Option<PointerPress>,
}

pub struct Confirm {
    pub session: String,
    pub key: &'static str,
    pub choice: Choice,
}

pub struct Menus {
    pub open: Option<Open>,
    pub chip: Option<(String, &'static str)>,
    pub confirm: Option<Confirm>,
    pub renaming: Option<String>,
    pub rename_text: TextEdit,
    pub rename_error: Option<String>,
    pub rename_saving: bool,
    pub settings: HashMap<String, SessionSettings>,
    pub models: HashMap<String, Vec<ModelOption>>,
    pub context: HashMap<String, ContextUsage>,
    pub busy: HashSet<(String, &'static str)>,
    pub note: HashMap<String, String>,
    pub context_error: HashMap<String, String>,
    leaving: HashMap<String, Vec<String>>,
    seq: u64,
    newest: HashMap<(String, &'static str), u64>,
    rename_seq: u64,
    refreshed: Option<(String, Instant)>,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl Menus {
    pub fn new() -> Menus {
        let (tx, rx) = channel();
        Menus {
            open: None,
            chip: None,
            confirm: None,
            renaming: None,
            rename_text: TextEdit::new(""),
            rename_error: None,
            rename_saving: false,
            settings: HashMap::new(),
            models: HashMap::new(),
            context: HashMap::new(),
            busy: HashSet::new(),
            note: HashMap::new(),
            context_error: HashMap::new(),
            leaving: HashMap::new(),
            seq: 0,
            newest: HashMap::new(),
            rename_seq: 0,
            refreshed: None,
            tx,
            rx,
        }
    }

    fn next(&mut self) -> u64 {
        self.seq += 1;
        self.seq
    }

    fn fresh(&mut self, id: &str, what: &'static str, seq: u64) -> bool {
        let newest = self.newest.entry((id.to_string(), what)).or_default();
        if seq <= *newest {
            return false;
        }
        *newest = seq;
        true
    }

    fn supersede(&mut self, id: &str, what: &'static str) {
        let seq = self.next();
        self.newest.insert((id.to_string(), what), seq);
    }
}

pub fn chips_for(s: &App, id: &str) -> Vec<Chip> {
    let (Some(set), Some(x)) = (s.menus.settings.get(id), session(s, id)) else { return Vec::new() };
    let models = s.menus.models.get(id).map(Vec::as_slice).unwrap_or(&[]);
    settings::chips(set, models, x)
}

fn spawn(s: &App, after: Duration, job: impl FnOnce(&Client) -> Option<Done> + Send + 'static) {
    let Some(client) = s.client.clone() else { return };
    let tx = s.menus.tx.clone();
    std::thread::spawn(move || {
        std::thread::sleep(after);
        if let Some(done) = job(&client) {
            let _ = tx.send(done);
        }
    });
}

fn fetch_settings(s: &mut App, id: &str, after: Duration) {
    let (id, seq) = (id.to_string(), s.menus.next());
    spawn(s, after, move |c| Some(Done::Settings(id.clone(), seq, c.session_settings(&id).map_err(|e| e.to_string()))));
}

fn fetch_context(s: &mut App, id: &str, after: Duration) {
    let (id, seq) = (id.to_string(), s.menus.next());
    spawn(s, after, move |c| Some(Done::Context(id.clone(), seq, c.context_usage(&id).map_err(|e| e.to_string()))));
}

fn load_fixture(s: &mut App, id: &str) {
    let Some(f) = &s.fixtures else { return };
    let Ok(raw) = std::fs::read_to_string(format!("{}/settings/{id}.json", f.dir)) else { return };
    let Ok(value) = serde_json::from_str::<serde_json::Value>(&raw) else { return };
    if let Ok(set) = serde_json::from_value(value["settings"].clone()) {
        s.menus.settings.insert(id.to_string(), set);
    }
    if let Ok(models) = serde_json::from_value(value["models"].clone()) {
        s.menus.models.insert(id.to_string(), models);
    }
    if let Ok(usage) = serde_json::from_value(value["context"].clone()) {
        s.menus.context.insert(id.to_string(), usage);
    }
}

pub fn load(s: &mut App, id: &str) {
    if s.fixtures.is_some() {
        load_fixture(s, id);
        return;
    }
    s.menus.refreshed = Some((id.to_string(), Instant::now()));
    fetch_settings(s, id, Duration::ZERO);
    fetch_context(s, id, Duration::ZERO);
    let surface = session(s, id).map(|x| x.surface.clone()).unwrap_or_default();
    if !surface.is_empty() {
        let id = id.to_string();
        spawn(s, Duration::ZERO, move |c| c.session_models(&surface, &id).ok().map(|m| Done::Models(id, m)));
    }
}

fn refresh_shown(s: &mut App) {
    if s.screen != Screen::Session || s.client.is_none() {
        return;
    }
    let Some(id) = s.selected.clone() else { return };
    let due = s.menus.refreshed.as_ref().is_none_or(|(shown, at)| *shown != id || at.elapsed() >= REFRESH);
    if due {
        s.menus.refreshed = Some((id.clone(), Instant::now()));
        fetch_settings(s, &id, Duration::ZERO);
        fetch_context(s, &id, Duration::ZERO);
    }
}

fn not_sent(s: &mut App) -> bool {
    if s.client.is_none() {
        s.ov.notice = Some("Fixture mode: not sent".into());
        return true;
    }
    false
}

fn verb(act: &Act) -> &'static str {
    match act {
        Act::Rename => "Renaming",
        Act::Terminal => "Opening the terminal",
        Act::Pr(_) => "Opening the pull request",
        Act::Pin(true) => "Pinning",
        Act::Pin(false) => "Unpinning",
        Act::Read(true) => "Marking as read",
        Act::Read(false) => "Marking as unread",
        Act::Archive(true) => "Archiving",
        Act::Archive(false) => "Unarchiving",
    }
}

fn failure(what: &str, error: &RespondError) -> String {
    if error.maybe_delivered { format!("{what} may not have gone through: {}", error.message) } else { format!("{what} didn't work: {}", error.message) }
}

pub fn run(s: &mut App, id: &str, act: Act) {
    s.menus.open = None;
    match act {
        Act::Rename => start_rename(s, id),
        Act::Pr(url) => open_link(&url),
        act => {
            if not_sent(s) || !s.menus.busy.insert((id.to_string(), "menu")) {
                return;
            }
            if act == Act::Archive(true) {
                let project = session(s, id).and_then(|x| crate::over::ui::project_for(s, x));
                let order = crate::over::ui::sidebar_order(s, project.as_deref(), Some(id));
                s.menus.leaving.insert(id.to_string(), order);
            }
            let revision = session(s, id).and_then(|x| x.read_state.as_ref()).map(|r| r.revision.clone()).unwrap_or_default();
            let id = id.to_string();
            spawn(s, Duration::ZERO, move |c| {
                let result = match &act {
                    Act::Terminal => c.session_focus(&id).map(|_| None),
                    Act::Pin(on) => c.session_pin(&id, *on).map(|_| None),
                    Act::Read(read) => c.session_read(&id, &revision, *read),
                    Act::Archive(on) => c.session_archive(&id, *on).map(|_| None),
                    Act::Rename | Act::Pr(_) => Ok(None),
                };
                Some(Done::Acted(id, act, revision, result))
            });
        }
    }
}

pub fn open_at(s: &mut App, id: &str, at: PointerPress) {
    s.menus.chip = None;
    s.menus.open = Some(Open { session: id.to_string(), at: Some(at) });
}

fn start_rename(s: &mut App, id: &str) {
    let current = current_alias(s, id).unwrap_or_default();
    s.menus.rename_seq += 1;
    s.menus.renaming = Some(id.to_string());
    s.menus.rename_text = TextEdit::new(current.as_str());
    s.menus.rename_text.focused = true;
    s.composer.focused = false;
    s.menus.rename_error = None;
    s.menus.rename_saving = false;
}

pub fn close_rename(s: &mut App) {
    s.menus.rename_seq += 1;
    s.menus.renaming = None;
    s.menus.rename_error = None;
    s.menus.rename_saving = false;
}

pub fn current_alias(s: &App, id: &str) -> Option<String> {
    session(s, id).and_then(|x| x.alias.clone()).filter(|a| !a.is_empty())
}

pub fn save_rename(s: &mut App) {
    let Some(id) = s.menus.renaming.clone() else { return };
    let current = current_alias(s, &id);
    let text = s.menus.rename_text.text.clone();
    if s.menus.rename_saving || menu::rename_problem(&text, current.as_deref()).is_some() || menu::unchanged(&text, current.as_deref()) {
        return;
    }
    if not_sent(s) {
        close_rename(s);
        return;
    }
    if !s.menus.busy.insert((id.clone(), "menu")) {
        s.menus.rename_error = Some("Another change to this session is still on its way.".into());
        return;
    }
    s.menus.rename_saving = true;
    s.menus.rename_error = None;
    let (name, seq) = (menu::normalized(&text), s.menus.rename_seq);
    spawn(s, Duration::ZERO, move |c| Some(Done::Renamed(id.clone(), seq, c.alias(&id, &name))));
}

pub fn choose(s: &mut App, id: &str, chip: &Chip, choice: &Choice) {
    s.menus.chip = None;
    match chip.pick(&choice.id) {
        Pick::Unchanged => {}
        Pick::Confirm => s.menus.confirm = Some(Confirm { session: id.to_string(), key: chip.key, choice: choice.clone() }),
        Pick::Apply => set(s, id, chip.key, &choice.id),
    }
}

pub fn set(s: &mut App, id: &str, key: &'static str, value: &str) {
    s.menus.confirm = None;
    if not_sent(s) || s.menus.busy.contains(&(id.to_string(), key)) {
        return;
    }
    s.menus.busy.insert((id.to_string(), key));
    let (id, value) = (id.to_string(), value.to_string());
    spawn(s, Duration::ZERO, move |c| {
        let result = c.turn_setting(&id, key, &value);
        Some(Done::Set(id, key, value, result))
    });
}

pub fn compact(s: &mut App, id: &str) {
    if not_sent(s) || s.menus.busy.contains(&(id.to_string(), "compact")) {
        return;
    }
    s.menus.busy.insert((id.to_string(), "compact"));
    let id = id.to_string();
    spawn(s, Duration::ZERO, move |c| Some(Done::Compacted(id.clone(), c.compact(&id))));
}

fn stamp() -> String {
    time::OffsetDateTime::now_utc().format(&time::format_description::well_known::Rfc3339).unwrap_or_default()
}

fn patch_of(act: &Act, read: Option<feed::action::ReadState>, from: String) -> Option<Patch> {
    match act {
        Act::Pin(on) => Some(Patch::Pinned(on.then(stamp))),
        Act::Archive(on) => Some(Patch::Archived(*on)),
        Act::Read(_) => read.map(|receipt| Patch::Read { receipt, from: vec![from] }),
        _ => None,
    }
}

fn leave_archived(s: &mut App, id: &str) {
    let Some(order) = s.menus.leaving.remove(id) else { return };
    let now = feed::composer::current(&s.rekeyed, id).to_string();
    if s.screen != Screen::Session || s.selected.as_deref() != Some(now.as_str()) {
        return;
    }
    let order: Vec<String> = order.iter().map(|x| feed::composer::current(&s.rekeyed, x).to_string()).collect();
    match feed::gone::next_after(&order, &now, |x| session(s, x).is_some_and(|x| !x.archived)) {
        Some(next) => crate::over::open_session(s, &next),
        None => crate::over::go(s, Screen::Now),
    }
}

fn land(s: &mut App, id: &str, patch: Patch) {
    if s.ov.live.patch(id, patch, Instant::now()) {
        s.sessions = s.ov.live.sessions().to_vec();
    }
}

fn settled(s: &mut App, done: Done) {
    match done {
        Done::Settings(id, seq, result) => {
            if !s.menus.fresh(&id, "settings", seq) {
                return;
            }
            match result {
                Ok(set) => {
                    s.menus.note.remove(&id);
                    s.menus.settings.insert(id, set);
                }
                Err(error) => {
                    s.menus.note.insert(id, format!("Couldn't read the settings: {error}"));
                }
            }
        }
        Done::Models(id, models) => {
            s.menus.models.insert(id, models);
        }
        Done::Context(id, seq, result) => {
            if !s.menus.fresh(&id, "context", seq) {
                return;
            }
            match result {
                Ok(Some(usage)) => {
                    s.menus.context_error.remove(&id);
                    s.menus.context.insert(id, usage);
                }
                Ok(None) => {
                    s.menus.context_error.remove(&id);
                    s.menus.context.remove(&id);
                }
                Err(error) => {
                    s.menus.context.remove(&id);
                    s.menus.context_error.insert(id, error);
                }
            }
        }
        Done::Acted(id, act, from, Ok(read)) => {
            s.menus.busy.remove(&(id.clone(), "menu"));
            if let Some(patch) = patch_of(&act, read, from) {
                land(s, &id, patch);
            }
            if act == Act::Archive(true) {
                leave_archived(s, &id);
            }
        }
        Done::Acted(id, act, _, Err(error)) => {
            s.menus.busy.remove(&(id.clone(), "menu"));
            s.menus.leaving.remove(&id);
            s.ov.notice = Some(failure(verb(&act), &error));
        }
        Done::Renamed(id, seq, result) => {
            s.menus.busy.remove(&(id.clone(), "menu"));
            let current = s.menus.renaming.as_deref() == Some(id.as_str()) && s.menus.rename_seq == seq;
            match result {
                Ok(alias) => {
                    land(s, &id, Patch::Alias((!alias.is_empty()).then_some(alias)));
                    if current {
                        close_rename(s);
                    }
                }
                Err(error) if current => {
                    s.menus.rename_saving = false;
                    s.menus.rename_error = Some(error.message);
                }
                Err(error) => s.ov.notice = Some(failure("Renaming", &error)),
            }
        }
        Done::Set(id, key, value, result) => {
            s.menus.busy.remove(&(id.clone(), key));
            match result {
                Ok(()) => {
                    s.menus.supersede(&id, "settings");
                    if let Some(set) = s.menus.settings.get_mut(&id) {
                        settings::apply(set, key, &value);
                    }
                    fetch_settings(s, &id, Duration::from_millis(1500));
                }
                Err(error) => s.ov.notice = Some(failure("Changing the setting", &error)),
            }
        }
        Done::Compacted(id, result) => {
            s.menus.busy.remove(&(id.clone(), "compact"));
            match result {
                Ok(()) => {
                    s.menus.supersede(&id, "context");
                    if let Some(usage) = s.menus.context.get_mut(&id) {
                        usage.compacting = true;
                    }
                    fetch_context(s, &id, Duration::from_secs(8));
                }
                Err(error) => s.ov.notice = Some(failure("Compacting", &error)),
            }
        }
    }
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok(done) = s.menus.rx.try_recv() {
        changed = true;
        settled(s, done);
    }
    if s.ov.live.expire() {
        s.sessions = s.ov.live.sessions().to_vec();
        changed = true;
    }
    if let Some((id, key)) = s.menus.chip.clone()
        && !chips_for(s, &id).iter().any(|c| c.key == key && c.changeable)
    {
        s.menus.chip = None;
        changed = true;
    }
    refresh_shown(s);
    changed
}
