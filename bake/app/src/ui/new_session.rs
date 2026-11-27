use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};

use feed::create::{self, Host, Launch, Launcher, ModelOption, Outcome, RootPick, RootUsage, SessionOptions};
use rune::{El, Key, KeyEvent, TextEdit, Tx, col, row, text, text_input};
use rune_kit::{Button, ButtonVariant, Dialog, Kit, Select, SelectAppearance, Switch};

use crate::theme::{self, BODY, META, failed, faint, muted};
use crate::{App, Bg, Screen, session};

pub enum Loaded {
    Hosts(u64, Result<Vec<Host>, String>),
    Options(u64, Result<SessionOptions, String>),
    Models(u64, String, Result<Vec<ModelOption>, String>),
    Pick(u64, RootPick),
    Usage(u64, RootUsage),
    Created(u64, Outcome),
}

fn ticket() -> u64 {
    static NEXT: AtomicU64 = AtomicU64::new(1);
    NEXT.fetch_add(1, Ordering::Relaxed)
}

enum Next {
    Stay,
    Open { session: String, notice: Option<String> },
}

#[derive(Clone, Default, PartialEq)]
pub struct Prefill {
    pub host: String,
    pub folder: String,
}

#[derive(Clone, Copy, PartialEq)]
enum Choice {
    Host,
    Surface,
    Place,
    Launcher,
    Model,
    Effort,
    Account,
    Permission,
    Mode,
    Tier,
}

pub struct Sheet {
    id: u64,
    launch: Launch,
    hosts: Vec<Host>,
    folder: TextEdit,
    branch: TextEdit,
    base: TextEdit,
    alias: TextEdit,
    agent: TextEdit,
    schema: TextEdit,
    message: TextEdit,
    options: Option<SessionOptions>,
    models: HashMap<String, Vec<ModelOption>>,
    pick: Option<RootPick>,
    load_error: Option<String>,
    model_error: Option<String>,
    generation: u64,
    open: Option<Choice>,
    starting: bool,
    submitted: Option<String>,
    uncertain: bool,
    error: Option<String>,
    existing: Option<String>,
    prefill: Prefill,
}

impl Sheet {
    fn new(prefill: Prefill) -> Sheet {
        let mut message = TextEdit::new("");
        message.focused = true;
        Sheet {
            id: ticket(),
            launch: Launch { host: prefill.host.clone(), ..Default::default() },
            hosts: Vec::new(),
            folder: TextEdit::new(prefill.folder.as_str()),
            branch: TextEdit::new(""),
            base: TextEdit::new(""),
            alias: TextEdit::new(""),
            agent: TextEdit::new(""),
            schema: TextEdit::new(""),
            message,
            options: None,
            models: HashMap::new(),
            pick: None,
            load_error: None,
            model_error: None,
            generation: 0,
            open: None,
            starting: false,
            submitted: None,
            uncertain: false,
            error: None,
            existing: None,
            prefill,
        }
    }

    fn request(&self) -> Launch {
        Launch {
            folder: self.folder.text.clone(),
            branch: self.branch.text.clone(),
            base: self.base.text.clone(),
            alias: self.alias.text.clone(),
            agent: self.agent.text.clone(),
            output_schema: self.schema.text.clone(),
            message: self.message.text.clone(),
            ..self.launch.clone()
        }
    }

    fn host_name(&self) -> String {
        self.hosts.iter().find(|h| h.id == self.launch.host).map(|h| h.name.clone()).unwrap_or_else(|| if self.launch.host.is_empty() { "this computer".into() } else { self.launch.host.clone() })
    }

    fn host_ready(&self) -> bool {
        self.launch.host.is_empty() || self.hosts.iter().any(|h| h.id == self.launch.host && h.available())
    }

    fn listed_models(&self) -> &[ModelOption] {
        self.models.get(&self.launch.surface).map(Vec::as_slice).unwrap_or_default()
    }

    fn model_choices(&self) -> Vec<(String, String)> {
        let listed = self.listed_models();
        if !listed.is_empty() {
            let mut choices = Vec::new();
            if !listed.iter().any(|m| m.default) {
                choices.push(("Runtime default".to_string(), String::new()));
            }
            choices.extend(listed.iter().map(|m| (m.title().to_string(), if m.default { String::new() } else { m.id.clone() })));
            return choices;
        }
        let named = self.options.as_ref().and_then(|o| o.surface(&self.launch.surface)).map(|s| s.models.clone()).unwrap_or_default();
        if named.is_empty() {
            return Vec::new();
        }
        std::iter::once(("Runtime default".to_string(), String::new())).chain(named.into_iter().map(|m| (m.clone(), m))).collect()
    }

    fn efforts(&self) -> Vec<String> {
        if self.launch.in_terminal() || create::is_acp(&self.launch.surface) {
            return Vec::new();
        }
        let listed = self.listed_models();
        let chosen = if self.launch.model.is_empty() { listed.iter().find(|m| m.default) } else { listed.iter().find(|m| m.id == self.launch.model) };
        chosen.map(|m| m.supported_reasoning_efforts.clone()).unwrap_or_default()
    }

    fn agent_modes(&self) -> Vec<(String, String)> {
        if !create::is_acp(&self.launch.surface) || self.launch.in_terminal() {
            return Vec::new();
        }
        let modes = self.options.as_ref().and_then(|o| o.surface(&self.launch.surface)).map(|x| x.modes.clone()).unwrap_or_default();
        if modes.is_empty() {
            return Vec::new();
        }
        std::iter::once(("Agent default".to_string(), String::new())).chain(modes.into_iter().map(|m| (m.clone(), m))).collect()
    }

    fn place_choices(&self) -> Vec<(String, String, &'static str)> {
        let Some(options) = &self.options else { return Vec::new() };
        let mut choices: Vec<(String, String, &'static str)> = Vec::new();
        for p in &options.projects {
            let name = if p.display_name.is_empty() { p.root_path.clone() } else { p.display_name.clone() };
            let taken = choices.iter().any(|(label, _, _)| *label == name);
            choices.push((if taken { format!("{name} · {}", p.root_path) } else { name }, p.root_path.clone(), "Projects"));
        }
        for folder in &options.folders {
            if !choices.iter().any(|(_, path, _)| path == folder) {
                choices.push((folder.clone(), folder.clone(), "Recent folders"));
            }
        }
        choices
    }

    fn account_choices(&self) -> Vec<(String, String)> {
        let Some(options) = &self.options else { return Vec::new() };
        let roots = &options.claude_config_roots;
        if self.launch.surface != create::CLAUDE || roots.len() < 2 {
            return Vec::new();
        }
        let default = match &self.pick {
            Some(pick) => {
                let chosen = roots.iter().find(|r| if pick.root.is_empty() { r.primary } else { r.root == pick.root }).map(|r| r.title()).unwrap_or_else(|| pick.name.clone());
                let reason = pick.reason.split([',', ';']).next().unwrap_or_default().trim().to_string();
                if reason.is_empty() { format!("Default ({chosen})") } else { format!("Default ({chosen}, {reason})") }
            }
            None => "Default".to_string(),
        };
        let mut choices = vec![(default, String::new())];
        let now = time::OffsetDateTime::now_utc();
        for root in roots.iter().filter(|r| r.selectable()) {
            let title = root.title();
            let title = if roots.iter().filter(|r| r.title() == title).count() > 1 { format!("{title} · {}", root.name) } else { title };
            let label = match root.usage_line(now) {
                Some(usage) => format!("{title} · {usage}"),
                None => title,
            };
            choices.push((label, root.root.clone()));
        }
        choices
    }

    fn launchers(&self) -> Vec<Launcher> {
        self.options.as_ref().map(|o| o.launchers_for(&self.launch.surface).into_iter().cloned().collect()).unwrap_or_default()
    }

    fn can_start(&self) -> bool {
        !self.starting && self.submitted.is_none() && self.existing.is_none() && self.options.is_some() && self.host_ready() && self.request().problem().is_none()
    }

    fn restart_loads(&mut self) -> u64 {
        self.generation = ticket();
        self.options = None;
        self.models.clear();
        self.pick = None;
        self.load_error = None;
        self.model_error = None;
        self.generation
    }

    fn settle(&mut self, outcome: Outcome) -> Next {
        self.starting = false;
        match outcome {
            Outcome::Started { session, note } => Next::Open { session, notice: (!note.is_empty()).then_some(note) },
            Outcome::Unknown { session: Some(session), message } => Next::Open { session, notice: Some(message) },
            Outcome::Submitted(note) => {
                self.submitted = Some(note);
                Next::Stay
            }
            Outcome::Halted { session, message } => {
                self.error = Some(format!("The session started, but its first message failed: {message}"));
                self.existing = Some(session);
                Next::Stay
            }
            Outcome::Unknown { session: None, message } => {
                self.submitted = Some(message);
                self.uncertain = true;
                Next::Stay
            }
            Outcome::Failed(message) => {
                self.error = Some(message);
                Next::Stay
            }
        }
    }

    fn again(&mut self) {
        self.submitted = None;
        self.uncertain = false;
        self.existing = None;
        self.error = None;
        self.message = TextEdit::new("");
        self.message.focused = true;
    }
}

pub fn prefill_for(s: &App) -> Prefill {
    match &s.screen {
        Screen::Project(key) => project_prefill(s, key),
        Screen::Session => s.selected.as_deref().and_then(|id| session(s, id)).map(|x| Prefill { host: host_of(x), folder: x.checkout_path().unwrap_or_default() }).unwrap_or_default(),
        _ => Prefill::default(),
    }
}

fn host_of(x: &feed::catalog::Session) -> String {
    if x.host.state == "local" || x.host.state.is_empty() { String::new() } else { x.host.id.clone() }
}

pub fn project_prefill(s: &App, key: &str) -> Prefill {
    let mut members: Vec<&feed::catalog::Session> = s.sessions.iter().filter(|x| x.project_key() == key && x.project.as_ref().is_some_and(|p| !p.root_path.is_empty())).collect();
    members.sort_by_key(|x| !host_of(x).is_empty());
    members
        .first()
        .map(|x| Prefill { host: host_of(x), folder: x.project.as_ref().map(|p| p.root_path.clone()).unwrap_or_default() })
        .or_else(|| s.ov.d.record(key)?.start_place().map(|c| Prefill { host: c.host.target().to_string(), folder: c.root_path.clone() }))
        .unwrap_or_default()
}

pub fn open(s: &mut App, prefill: Prefill, tx: &mut Tx) {
    if !super::window::may_open(s, super::window::Kind::NewSession) {
        return;
    }
    if s.new_session.is_some() {
        tx.focus("new-session-message", false);
        return;
    }
    let mut sheet = Sheet::new(prefill);
    if s.client.is_none() {
        sheet.load_error = Some("Agenthail isn't connected, so nothing can start from here.".into());
    }
    let id = sheet.id;
    s.new_session = Some(sheet);
    if let Some(client) = s.client.clone() {
        let out = s.outbox.clone();
        std::thread::spawn(move || {
            let _ = out.send(Bg::NewSession(Loaded::Hosts(id, client.hosts().map_err(|e| e.to_string()))));
        });
    }
    reload(s);
    tx.focus("new-session-message", false);
}

fn close(s: &mut App) {
    s.new_session = None;
}

fn reload(s: &mut App) {
    let Some(sheet) = s.new_session.as_mut() else { return };
    let generation = sheet.restart_loads();
    let Some(client) = s.client.clone() else { return };
    let (out, host) = (s.outbox.clone(), sheet.launch.host.clone());
    std::thread::spawn(move || {
        let _ = out.send(Bg::NewSession(Loaded::Options(generation, client.session_options(&host).map_err(|e| e.to_string()))));
    });
}

fn load_models(s: &mut App) {
    let Some(sheet) = s.new_session.as_mut() else { return };
    let surface = sheet.launch.surface.clone();
    sheet.model_error = None;
    if surface.is_empty() || surface == create::NOTION || sheet.models.contains_key(&surface) {
        return;
    }
    let Some(client) = s.client.clone() else { return };
    let (out, generation, host) = (s.outbox.clone(), sheet.generation, sheet.launch.host.clone());
    std::thread::spawn(move || {
        let models = client.models(&surface, &host).map_err(|e| e.to_string());
        let _ = out.send(Bg::NewSession(Loaded::Models(generation, surface, models)));
    });
}

fn load_accounts(s: &mut App) {
    let Some(sheet) = s.new_session.as_ref() else { return };
    let Some(client) = s.client.clone() else { return };
    let roots = sheet.options.as_ref().map(|o| o.claude_config_roots.clone()).unwrap_or_default();
    let (generation, host) = (sheet.generation, sheet.launch.host.clone());
    if roots.len() > 1 {
        let (out, client, host) = (s.outbox.clone(), client.clone(), host.clone());
        std::thread::spawn(move || {
            if let Ok(pick) = client.claude_pick(&host) {
                let _ = out.send(Bg::NewSession(Loaded::Pick(generation, pick)));
            }
        });
    }
    for root in roots.into_iter().filter(|r| r.reads_usage()) {
        let (out, client, host) = (s.outbox.clone(), client.clone(), host.clone());
        std::thread::spawn(move || {
            if let Ok(usage) = client.claude_usage(&root.root, &host) {
                let _ = out.send(Bg::NewSession(Loaded::Usage(generation, usage)));
            }
        });
    }
}

pub fn take(s: &mut App, loaded: Loaded) {
    if let Loaded::Created(id, outcome) = loaded {
        return created(s, id, outcome);
    }
    let Some(sheet) = s.new_session.as_mut() else { return };
    match loaded {
        Loaded::Hosts(id, Ok(hosts)) if id == sheet.id => {
            sheet.hosts = hosts;
            if !sheet.host_ready() {
                sheet.launch.host.clear();
                sheet.folder = TextEdit::new("");
                reload(s);
            }
        }
        Loaded::Options(generation, result) if generation == sheet.generation => match result {
            Ok(options) => {
                if options.surface(&sheet.launch.surface).is_none() {
                    sheet.launch = Launch { host: sheet.launch.host.clone(), surface: options.first_surface().map(|x| x.id.clone()).unwrap_or_default(), ..Default::default() };
                }
                if sheet.folder.text.trim().is_empty() {
                    let folder = if sheet.launch.host == sheet.prefill.host && !sheet.prefill.folder.is_empty() {
                        sheet.prefill.folder.clone()
                    } else {
                        options.projects.first().map(|p| p.root_path.clone()).unwrap_or_default()
                    };
                    sheet.folder = TextEdit::new(folder.as_str());
                }
                sheet.options = Some(options);
                load_models(s);
                load_accounts(s);
            }
            Err(error) => sheet.load_error = Some(format!("Couldn't load what {} can start: {error}", sheet.host_name())),
        },
        Loaded::Models(generation, surface, result) if generation == sheet.generation => match result {
            Ok(models) => {
                sheet.models.insert(surface, models);
            }
            Err(error) if surface == sheet.launch.surface => sheet.model_error = Some(format!("The model list isn't available, so the session uses the default model. ({error})")),
            Err(_) => {}
        },
        Loaded::Pick(generation, pick) if generation == sheet.generation => sheet.pick = Some(pick),
        Loaded::Usage(generation, usage) if generation == sheet.generation => {
            if let Some(root) = sheet.options.as_mut().and_then(|o| o.claude_config_roots.iter_mut().find(|r| r.root == usage.root)) {
                root.take_usage(usage);
            }
        }
        _ => {}
    }
}

fn created(s: &mut App, id: u64, outcome: Outcome) {
    let next = match s.new_session.as_mut() {
        Some(sheet) if sheet.id == id => sheet.settle(outcome),
        _ => match outcome {
            Outcome::Started { session, note } => Next::Open { session, notice: Some(if note.is_empty() { "The new session started.".into() } else { note }) },
            Outcome::Unknown { session: Some(session), message } => Next::Open { session, notice: Some(message) },
            Outcome::Submitted(note) => {
                s.ov.notice = Some(note);
                Next::Stay
            }
            Outcome::Halted { message, .. } => {
                s.ov.notice = Some(format!("The new session started, but its first message failed: {message}"));
                Next::Stay
            }
            Outcome::Unknown { session: None, message } => {
                s.ov.notice = Some(message);
                Next::Stay
            }
            Outcome::Failed(message) => {
                s.ov.notice = Some(format!("The new session didn't start: {message}"));
                Next::Stay
            }
        },
    };
    if let Next::Open { session, notice } = next {
        if s.new_session.as_ref().is_some_and(|sheet| sheet.id == id) {
            close(s);
        }
        crate::over::open_session(s, &session);
        if notice.is_some() {
            s.ov.notice = notice;
        }
    }
}

fn start(s: &mut App) {
    let Some(sheet) = s.new_session.as_mut() else { return };
    if !sheet.can_start() {
        return;
    }
    let Some(client) = s.client.clone() else { return };
    sheet.starting = true;
    sheet.error = None;
    sheet.existing = None;
    let (out, launch, id) = (s.outbox.clone(), sheet.request(), sheet.id);
    std::thread::spawn(move || {
        let _ = out.send(Bg::NewSession(Loaded::Created(id, client.create_session(&launch))));
    });
}

fn start_another(s: &mut App, tx: &mut Tx) {
    if let Some(sheet) = s.new_session.as_mut() {
        sheet.again();
        tx.focus("new-session-message", false);
    }
}

fn start_again(s: &mut App) {
    if let Some(sheet) = s.new_session.as_mut() {
        sheet.submitted = None;
        sheet.uncertain = false;
    }
    start(s);
}

fn open_existing(s: &mut App) {
    if let Some(id) = s.new_session.as_ref().and_then(|x| x.existing.clone()) {
        close(s);
        crate::over::open_session(s, &id);
    }
}

fn choose(s: &mut App, which: Choice, value: String) {
    let Some(sheet) = s.new_session.as_mut() else { return };
    sheet.open = None;
    sheet.error = None;
    match which {
        Choice::Host => {
            if value == sheet.launch.host || !sheet.hosts.iter().any(|h| h.id == value && h.available()) {
                return;
            }
            sheet.launch = Launch { host: value, surface: sheet.launch.surface.clone(), ..Default::default() };
            let folder = if sheet.launch.host == sheet.prefill.host { sheet.prefill.folder.clone() } else { String::new() };
            sheet.folder = TextEdit::new(folder.as_str());
            reload(s);
        }
        Choice::Surface => {
            if value != sheet.launch.surface {
                sheet.launch = Launch { host: sheet.launch.host.clone(), surface: value, ..Default::default() };
                sheet.agent = TextEdit::new("");
                sheet.schema = TextEdit::new("");
                load_models(s);
            }
        }
        Choice::Place => sheet.folder = TextEdit::new(value.as_str()),
        Choice::Launcher => {
            let chosen = sheet.launchers().into_iter().find(|l| l.id == value);
            if chosen.as_ref().is_some_and(|l| !l.available) {
                return;
            }
            sheet.launch.launcher = chosen;
            if sheet.launch.launcher.is_some() {
                sheet.launch.worktree = false;
            }
            if sheet.launch.in_terminal() {
                sheet.launch.effort.clear();
                sheet.launch.permission.clear();
            }
        }
        Choice::Model => {
            if value != sheet.launch.model {
                sheet.launch.model = value;
                sheet.launch.effort.clear();
            }
        }
        Choice::Effort => sheet.launch.effort = value,
        Choice::Account => sheet.launch.config_root = value,
        Choice::Permission => sheet.launch.permission = value,
        Choice::Mode => sheet.launch.mode = value,
        Choice::Tier => sheet.launch.service_tier = value,
    }
}

fn toggle(s: &mut App, which: Choice) {
    if let Some(sheet) = s.new_session.as_mut() {
        sheet.open = if sheet.open == Some(which) { None } else { Some(which) };
    }
}

fn sheet_mut(s: &mut App) -> &mut Sheet {
    s.new_session.as_mut().expect("new session sheet open")
}

pub fn on_key(s: &mut App, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if s.new_session.is_none() {
        if super::keys::is(super::keys::Act::NewSession, ev, tx) {
            let prefill = prefill_for(s);
            open(s, prefill, tx);
            return true;
        }
        return false;
    }
    match &ev.key {
        Key::Escape => {
            match s.new_session.as_mut() {
                Some(sheet) if sheet.open.is_some() => sheet.open = None,
                _ => close(s),
            }
            true
        }
        Key::Enter if ev.mods.cmd => {
            start(s);
            true
        }
        Key::Text(_) | Key::Enter | Key::Up | Key::Down => true,
        _ => ev.mods.cmd || ev.mods.ctrl,
    }
}

fn select(key: &'static str, which: Choice, choices: &[(String, String)], current: &str, open: Option<Choice>, popups: &mut Vec<El>, k: &Kit) -> El {
    let labels: Vec<&str> = choices.iter().map(|(label, _)| label.as_str()).collect();
    let values: Vec<(String, String)> = choices.to_vec();
    Select::new(key, &labels)
        .appearance(SelectAppearance::Field)
        .width(268.0)
        .current(choices.iter().find(|(_, value)| value == current).map(|(label, _)| label.as_str()))
        .placeholder("Choose…")
        .open(open == Some(which))
        .on_toggle(move |s: &mut App| toggle(s, which))
        .on_close(|s: &mut App| {
            if let Some(sheet) = s.new_session.as_mut() {
                sheet.open = None;
            }
        })
        .on_choose(move |s: &mut App, label: &str, _: &mut Tx| {
            if let Some((_, value)) = values.iter().find(|(l, _)| l == label) {
                choose(s, which, value.clone());
            }
        })
        .build(k, popups)
}

fn field(name: &str, control: El, k: &Kit) -> El {
    col().gap(5.0).child(text(name.to_string()).font(k.text(META)).color(faint())).child(control)
}

fn line_input(edit: &TextEdit, key: &'static str, placeholder: &str, pick: fn(&mut Sheet) -> &mut TextEdit, k: &Kit) -> El {
    text_input(edit, k.text(BODY))
        .single_line()
        .key(key)
        .label(placeholder.to_string())
        .w(268.0)
        .px(10.0)
        .py(6.0)
        .radius(6.0)
        .fill(theme::wash())
        .border(1.0, theme::line())
        .color(theme::text())
        .placeholder(placeholder.to_string(), faint())
        .on_edit(move |s: &mut App| pick(sheet_mut(s)))
}

fn note(message: &str, color: rune::Color, k: &Kit) -> El {
    text(message.to_string()).font(k.text(META)).color(color).wrap().w_full()
}

pub fn view(s: &App, layer: &mut Vec<El>, k: &Kit) {
    let Some(sheet) = &s.new_session else { return };
    let mut popups = Vec::new();
    let request = sheet.request();
    let mut body = col().w_full().gap(14.0).px(16.0).pt(2.0).pb(16.0);

    let mut first = row().w_full().gap(12.0).flex_wrap();
    if sheet.hosts.len() > 1 {
        let hosts: Vec<(String, String)> = sheet.hosts.iter().filter(|h| h.available()).map(|h| (h.name.clone(), h.id.clone())).collect();
        first = first.child(field("Computer", select("new-session-host", Choice::Host, &hosts, &sheet.launch.host, sheet.open, &mut popups, k), k));
    }
    if let Some(options) = &sheet.options {
        let surfaces: Vec<(String, String)> = options.surfaces.iter().map(|x| (x.title(), x.id.clone())).collect();
        first = first.child(field("Agent", select("new-session-surface", Choice::Surface, &surfaces, &sheet.launch.surface, sheet.open, &mut popups, k), k));
    }
    body = body.child(first);

    if sheet.options.is_some() && request.uses_folder() {
        let places: Vec<(String, String)> = sheet.place_choices().into_iter().map(|(label, value, _)| (label, value)).collect();
        let mut place = row().w_full().gap(12.0).flex_wrap();
        if !places.is_empty() {
            place = place.child(field("Project", select("new-session-place", Choice::Place, &places, sheet.folder.text.trim(), sheet.open, &mut popups, k), k));
        }
        place = place.child(field(&format!("Folder on {}", sheet.host_name()), line_input(&sheet.folder, "new-session-folder", "/path/to/folder", |x| &mut x.folder, k), k));
        body = body.child(place);
    }

    if sheet.options.is_some() {
        let mut tune = row().w_full().gap(12.0).flex_wrap();
        let models = sheet.model_choices();
        if !models.is_empty() {
            tune = tune.child(field("Model", select("new-session-model", Choice::Model, &models, &sheet.launch.model, sheet.open, &mut popups, k), k));
        }
        let efforts = sheet.efforts();
        if !efforts.is_empty() {
            let choices: Vec<(String, String)> = std::iter::once(("Default".to_string(), String::new())).chain(efforts.iter().map(|e| (capitalized(e), e.clone()))).collect();
            tune = tune.child(field("Effort", select("new-session-effort", Choice::Effort, &choices, &sheet.launch.effort, sheet.open, &mut popups, k), k));
        }
        let accounts = sheet.account_choices();
        if !accounts.is_empty() {
            tune = tune.child(field("Account", select("new-session-account", Choice::Account, &accounts, &sheet.launch.config_root, sheet.open, &mut popups, k), k));
        }
        let modes = create::permission_modes(&sheet.launch.surface);
        if !modes.is_empty() && !request.in_terminal() {
            let (name, default) = if sheet.launch.surface == create::CODEX { ("Approvals", "Codex default") } else { ("Permission mode", "Claude default") };
            let choices: Vec<(String, String)> = std::iter::once((default.to_string(), String::new())).chain(modes.iter().map(|(value, label)| (label.to_string(), value.to_string()))).collect();
            tune = tune.child(field(name, select("new-session-permission", Choice::Permission, &choices, &sheet.launch.permission, sheet.open, &mut popups, k), k));
        }
        let agent_modes = sheet.agent_modes();
        if !agent_modes.is_empty() {
            tune = tune.child(field("Mode", select("new-session-agent-mode", Choice::Permission, &agent_modes, &sheet.launch.permission, sheet.open, &mut popups, k), k));
        }
        if request.takes_codex_options() {
            let with_default = |default: &str, pairs: &[(&str, &str)]| -> Vec<(String, String)> {
                std::iter::once((default.to_string(), String::new())).chain(pairs.iter().map(|(value, label)| (label.to_string(), value.to_string()))).collect()
            };
            tune = tune
                .child(field("Mode", select("new-session-mode", Choice::Mode, &with_default("Session default", &create::CODEX_MODES), &sheet.launch.mode, sheet.open, &mut popups, k), k))
                .child(field("Service tier", select("new-session-tier", Choice::Tier, &with_default("Runtime default", &create::CODEX_SERVICE_TIERS), &sheet.launch.service_tier, sheet.open, &mut popups, k), k));
        }
        if sheet.launch.surface == create::CLAUDE && !request.in_terminal() {
            tune = tune.child(field("Claude agent", line_input(&sheet.agent, "new-session-agent", "Optional", |x| &mut x.agent, k), k));
        }
        let launchers = sheet.launchers();
        if !launchers.is_empty() {
            let choices: Vec<(String, String)> = std::iter::once(("Default".to_string(), String::new()))
                .chain(launchers.iter().map(|l| (if l.available { l.title().to_string() } else { format!("{} (unavailable)", l.title()) }, l.id.clone())))
                .collect();
            let current = sheet.launch.launcher.as_ref().map(|l| l.id.clone()).unwrap_or_default();
            tune = tune.child(field("Runs in", select("new-session-launcher", Choice::Launcher, &choices, &current, sheet.open, &mut popups, k), k));
        }
        body = body.child(tune);
    }

    if sheet.options.is_some() && request.takes_codex_options() {
        let schema = text_input(&sheet.schema, k.text(BODY))
            .key("new-session-schema")
            .label("Output JSON schema")
            .w_full()
            .min_h(56.0)
            .max_h(160.0)
            .px(10.0)
            .py(8.0)
            .radius(6.0)
            .fill(theme::wash())
            .border(1.0, theme::line())
            .color(theme::text())
            .placeholder("Optional JSON object".to_string(), faint())
            .on_edit(|s: &mut App| &mut sheet_mut(s).schema);
        body = body.child(field("Output JSON schema", schema, k));
    }

    if sheet.options.is_some() && request.takes_worktree() {
        let toggle = Switch::new(sheet.launch.worktree)
            .label("Start in its own worktree")
            .on_toggle(|s: &mut App| {
                let sheet = sheet_mut(s);
                sheet.launch.worktree = !sheet.launch.worktree;
            })
            .build(k);
        let mut worktree = col().w_full().gap(8.0).child(toggle);
        if sheet.launch.worktree {
            worktree = worktree.child(
                row()
                    .w_full()
                    .gap(12.0)
                    .flex_wrap()
                    .child(field("Branch", line_input(&sheet.branch, "new-session-branch", "ah/<name> by default", |x| &mut x.branch, k), k))
                    .child(field("Base", line_input(&sheet.base, "new-session-base", "The folder's HEAD by default", |x| &mut x.base, k), k)),
            );
        }
        body = body.child(worktree);
    }

    body = body.child(field("Name", line_input(&sheet.alias, "new-session-alias", "Optional", |x| &mut x.alias, k), k));
    let message = text_input(&sheet.message, k.text(BODY))
        .key("new-session-message")
        .label("First message")
        .w_full()
        .min_h(96.0)
        .max_h(240.0)
        .px(10.0)
        .py(8.0)
        .radius(6.0)
        .fill(theme::wash())
        .border(1.0, theme::line())
        .color(theme::text())
        .placeholder("What should the agent do?".to_string(), faint())
        .on_edit(|s: &mut App| &mut sheet_mut(s).message)
        .on_submit(|s: &mut App, _: &mut Tx| start(s));
    body = body.child(field("First message", message, k));

    if sheet.options.is_none() && sheet.load_error.is_none() {
        body = body.child(note(&format!("Loading what {} can start…", sheet.host_name()), muted(), k));
    }
    for line in [&sheet.load_error, &sheet.model_error].into_iter().flatten() {
        body = body.child(note(line, muted(), k));
    }
    if let Some(error) = &sheet.error {
        body = body.child(note(error, failed(), k));
    }

    let mut foot = row().w_full().gap(8.0).items_center().pt(4.0);
    if let Some(submitted) = &sheet.submitted {
        foot = foot
            .child(note(submitted, muted(), k).shrink());
        foot = if sheet.uncertain {
            foot.child(Button::new("Start again").variant(ButtonVariant::Ghost).on_press(start_again).build(k))
        } else {
            foot.child(Button::new("Start another").variant(ButtonVariant::Ghost).on_press_tx(start_another).build(k))
        };
        foot = foot
            .child(Button::new("Done").variant(ButtonVariant::Contrast).on_press(close).build(k));
    } else {
        let hint = if sheet.starting {
            "Starting…".to_string()
        } else if !sheet.host_ready() {
            format!("{} isn't connected.", sheet.host_name())
        } else {
            request.problem().unwrap_or_default().to_string()
        };
        foot = foot.child(text(hint).font(k.text(META)).color(faint()).grow(1.0).shrink().truncate());
        if sheet.existing.is_some() {
            foot = foot
                .child(Button::new("Start another").variant(ButtonVariant::Ghost).on_press_tx(start_another).build(k))
                .child(Button::new("Open the session").variant(ButtonVariant::Contrast).on_press(open_existing).build(k));
        } else {
            foot = foot
                .child(Button::new("Cancel").variant(ButtonVariant::Ghost).on_press(close).build(k))
                .child(Button::new(if sheet.starting { "Starting…" } else { "Start" }).variant(ButtonVariant::Contrast).shortcut("⌘↩").disabled(!sheet.can_start()).on_press(start).build(k));
        }
    }
    body = body.child(foot);

    Dialog::new("new-session", "New session").open(true).fit().width(600.0).click_outside(false).on_close(close).child(body).build(k, layer);
    layer.extend(popups);
}

fn capitalized(value: &str) -> String {
    let mut chars = value.chars();
    chars.next().map(|c| c.to_uppercase().chain(chars).collect()).unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ready() -> Sheet {
        let mut sheet = Sheet::new(Prefill { host: String::new(), folder: "/work/app".into() });
        sheet.restart_loads();
        sheet.options = Some(SessionOptions::default());
        sheet.launch.surface = create::CLAUDE.into();
        sheet.message = TextEdit::new("Fix the build");
        sheet
    }

    #[test]
    fn a_session_whose_first_message_failed_is_not_started_twice() {
        let mut sheet = ready();
        assert!(sheet.can_start());
        assert!(matches!(sheet.settle(Outcome::Halted { session: "s1".into(), message: "model refused".into() }), Next::Stay));
        assert_eq!(sheet.existing.as_deref(), Some("s1"));
        assert!(!sheet.can_start(), "the session exists, so Start would make a second one");
        sheet.again();
        assert!(sheet.message.text.is_empty() && sheet.existing.is_none());
    }

    #[test]
    fn an_unknown_outcome_asks_before_starting_again() {
        let mut sheet = ready();
        assert!(matches!(sheet.settle(Outcome::Unknown { session: None, message: "can't tell".into() }), Next::Stay));
        assert!(sheet.uncertain && !sheet.can_start(), "Start would make a second session if the first one started");
        assert_eq!(sheet.message.text, "Fix the build", "Start again sends the same form");
    }

    #[test]
    fn a_refusal_keeps_the_form_and_a_start_opens_the_session() {
        let mut sheet = ready();
        assert!(matches!(sheet.settle(Outcome::Failed("cwd must be an absolute path".into())), Next::Stay));
        assert_eq!(sheet.error.as_deref(), Some("cwd must be an absolute path"));
        assert_eq!(sheet.message.text, "Fix the build");
        assert!(sheet.can_start());
        assert!(matches!(sheet.settle(Outcome::Started { session: "s2".into(), note: String::new() }), Next::Open { session, notice: None } if session == "s2"));
    }

    #[test]
    fn an_acp_agent_offers_its_own_modes_and_no_effort() {
        let mut sheet = ready();
        let devin = create::Surface { id: "devin".into(), modes: vec!["ask".into(), "bypass".into()], ..Default::default() };
        sheet.options = Some(SessionOptions { surfaces: vec![devin], ..Default::default() });
        assert!(sheet.agent_modes().is_empty(), "Claude keeps its permission modes");
        sheet.launch.surface = "devin".into();
        sheet.models.insert("devin".into(), vec![ModelOption { id: "swe-2".into(), default: true, supported_reasoning_efforts: vec!["high".into()], ..Default::default() }]);
        let modes: Vec<String> = sheet.agent_modes().into_iter().map(|(label, _)| label).collect();
        assert_eq!(modes, ["Agent default", "ask", "bypass"]);
        assert!(sheet.efforts().is_empty());
        sheet.launch.launcher = Some(create::Launcher { id: "tmux".into(), available: true, ..Default::default() });
        assert!(sheet.agent_modes().is_empty(), "a terminal takes no mode");
    }

    #[test]
    fn a_reopened_sheet_never_shares_a_load_with_the_one_it_replaced() {
        let mut closed = ready();
        let old = closed.restart_loads();
        let mut reopened = ready();
        let current = reopened.restart_loads();
        assert_ne!(closed.id, reopened.id);
        assert_ne!(old, current);
        assert_ne!(old, reopened.generation);
    }
}
