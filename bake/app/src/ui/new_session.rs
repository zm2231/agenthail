use std::collections::HashMap;

use feed::create::{self, Host, Launch, Launcher, ModelOption, Outcome, RootPick, RootUsage, SessionOptions};
use rune::{El, Key, KeyEvent, TextEdit, Tx, col, row, text, text_input};
use rune_kit::{Button, ButtonVariant, Dialog, Select, SelectAppearance, Switch};

use crate::theme::{self, BODY, META, failed, faint, muted};
use crate::{App, Bg, Screen, session};

pub enum Loaded {
    Hosts(Result<Vec<Host>, String>),
    Options(u64, Result<SessionOptions, String>),
    Models(u64, String, Result<Vec<ModelOption>, String>),
    Pick(u64, RootPick),
    Usage(u64, RootUsage),
    Created(Outcome),
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
}

pub struct Sheet {
    launch: Launch,
    hosts: Vec<Host>,
    folder: TextEdit,
    branch: TextEdit,
    base: TextEdit,
    alias: TextEdit,
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
    error: Option<String>,
    existing: Option<String>,
    prefill: Prefill,
}

impl Sheet {
    fn new(prefill: Prefill) -> Sheet {
        let mut message = TextEdit::new("");
        message.focused = true;
        Sheet {
            launch: Launch { host: prefill.host.clone(), ..Default::default() },
            hosts: Vec::new(),
            folder: TextEdit::new(prefill.folder.as_str()),
            branch: TextEdit::new(""),
            base: TextEdit::new(""),
            alias: TextEdit::new(""),
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
        if self.launch.in_terminal() {
            return Vec::new();
        }
        let listed = self.listed_models();
        let chosen = if self.launch.model.is_empty() { listed.iter().find(|m| m.default) } else { listed.iter().find(|m| m.id == self.launch.model) };
        chosen.map(|m| m.supported_reasoning_efforts.clone()).unwrap_or_default()
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
        for root in roots.iter().filter(|r| r.selectable()) {
            let title = root.title();
            let title = if roots.iter().filter(|r| r.title() == title).count() > 1 { format!("{title} · {}", root.name) } else { title };
            let label = match root.usage_line() {
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
        !self.starting && self.submitted.is_none() && self.options.is_some() && self.host_ready() && self.request().problem().is_none()
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
    members.first().map(|x| Prefill { host: host_of(x), folder: x.project.as_ref().map(|p| p.root_path.clone()).unwrap_or_default() }).unwrap_or_default()
}

pub fn open(s: &mut App, prefill: Prefill, tx: &mut Tx) {
    if s.new_session.is_some() {
        tx.focus("new-session-message", false);
        return;
    }
    let mut sheet = Sheet::new(prefill);
    if s.client.is_none() {
        sheet.load_error = Some("Agenthail isn't connected, so nothing can start from here.".into());
    }
    s.new_session = Some(sheet);
    if let Some(client) = s.client.clone() {
        let out = s.outbox.clone();
        std::thread::spawn(move || {
            let _ = out.send(Bg::NewSession(Loaded::Hosts(client.hosts().map_err(|e| e.to_string()))));
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
    sheet.generation += 1;
    sheet.options = None;
    sheet.models.clear();
    sheet.pick = None;
    sheet.load_error = None;
    sheet.model_error = None;
    let Some(client) = s.client.clone() else { return };
    let (out, generation, host) = (s.outbox.clone(), sheet.generation, sheet.launch.host.clone());
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
    let Some(sheet) = s.new_session.as_mut() else { return };
    match loaded {
        Loaded::Hosts(Ok(hosts)) => {
            sheet.hosts = hosts;
            if !sheet.host_ready() {
                sheet.launch.host.clear();
                sheet.folder = TextEdit::new("");
                reload(s);
            }
        }
        Loaded::Hosts(Err(_)) => {}
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
        Loaded::Created(outcome) => created(s, outcome),
        _ => {}
    }
}

fn created(s: &mut App, outcome: Outcome) {
    let Some(sheet) = s.new_session.as_mut() else { return };
    sheet.starting = false;
    match outcome {
        Outcome::Started { session, note } => {
            close(s);
            if !note.is_empty() {
                s.ov.notice = Some(note);
            }
            crate::over::open_session(s, &session);
        }
        Outcome::Unknown { session: Some(session), message } => {
            close(s);
            s.ov.notice = Some(message);
            crate::over::open_session(s, &session);
        }
        Outcome::Submitted(note) => sheet.submitted = Some(note),
        Outcome::Halted { session, message } => {
            sheet.error = Some(format!("The session started, but its first message failed: {message}"));
            sheet.existing = Some(session);
        }
        Outcome::Unknown { session: None, message } => sheet.error = Some(message),
        Outcome::Failed(message) => sheet.error = Some(message),
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
    let (out, launch) = (s.outbox.clone(), sheet.request());
    std::thread::spawn(move || {
        let _ = out.send(Bg::NewSession(Loaded::Created(client.create_session(&launch))));
    });
}

fn start_another(s: &mut App, tx: &mut Tx) {
    if let Some(sheet) = s.new_session.as_mut() {
        sheet.submitted = None;
        sheet.message = TextEdit::new("");
        sheet.message.focused = true;
        tx.focus("new-session-message", false);
    }
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
        if ev.is_command('n') {
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
        _ => ev.mods.cmd,
    }
}

fn select(key: &'static str, which: Choice, choices: &[(String, String)], current: &str, open: Option<Choice>, popups: &mut Vec<El>) -> El {
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
        .build(theme::kit(), popups)
}

fn field(name: &str, control: El) -> El {
    col().gap(5.0).child(text(name.to_string()).font(META).color(faint())).child(control)
}

fn line_input(edit: &TextEdit, key: &'static str, placeholder: &str, pick: fn(&mut Sheet) -> &mut TextEdit) -> El {
    text_input(edit, BODY)
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

fn note(message: &str, color: rune::Color) -> El {
    text(message.to_string()).font(META).color(color).wrap().w_full()
}

pub fn view(s: &App, layer: &mut Vec<El>) {
    let Some(sheet) = &s.new_session else { return };
    let k = theme::kit();
    let mut popups = Vec::new();
    let request = sheet.request();
    let mut body = col().w_full().gap(14.0).px(16.0).pt(2.0).pb(16.0);

    let mut first = row().w_full().gap(12.0).flex_wrap();
    if sheet.hosts.len() > 1 {
        let hosts: Vec<(String, String)> = sheet.hosts.iter().filter(|h| h.available()).map(|h| (h.name.clone(), h.id.clone())).collect();
        first = first.child(field("Computer", select("new-session-host", Choice::Host, &hosts, &sheet.launch.host, sheet.open, &mut popups)));
    }
    if let Some(options) = &sheet.options {
        let surfaces: Vec<(String, String)> = options.surfaces.iter().map(|x| (x.title(), x.id.clone())).collect();
        first = first.child(field("Agent", select("new-session-surface", Choice::Surface, &surfaces, &sheet.launch.surface, sheet.open, &mut popups)));
    }
    body = body.child(first);

    if sheet.options.is_some() && request.uses_folder() {
        let places: Vec<(String, String)> = sheet.place_choices().into_iter().map(|(label, value, _)| (label, value)).collect();
        let mut place = row().w_full().gap(12.0).flex_wrap();
        if !places.is_empty() {
            place = place.child(field("Project", select("new-session-place", Choice::Place, &places, sheet.folder.text.trim(), sheet.open, &mut popups)));
        }
        place = place.child(field(&format!("Folder on {}", sheet.host_name()), line_input(&sheet.folder, "new-session-folder", "/path/to/folder", |x| &mut x.folder)));
        body = body.child(place);
    }

    if sheet.options.is_some() {
        let mut tune = row().w_full().gap(12.0).flex_wrap();
        let models = sheet.model_choices();
        if !models.is_empty() {
            tune = tune.child(field("Model", select("new-session-model", Choice::Model, &models, &sheet.launch.model, sheet.open, &mut popups)));
        }
        let efforts = sheet.efforts();
        if !efforts.is_empty() {
            let choices: Vec<(String, String)> = std::iter::once(("Default".to_string(), String::new())).chain(efforts.iter().map(|e| (capitalized(e), e.clone()))).collect();
            tune = tune.child(field("Effort", select("new-session-effort", Choice::Effort, &choices, &sheet.launch.effort, sheet.open, &mut popups)));
        }
        let accounts = sheet.account_choices();
        if !accounts.is_empty() {
            tune = tune.child(field("Account", select("new-session-account", Choice::Account, &accounts, &sheet.launch.config_root, sheet.open, &mut popups)));
        }
        let modes = create::permission_modes(&sheet.launch.surface);
        if !modes.is_empty() && !request.in_terminal() {
            let (name, default) = if sheet.launch.surface == create::CODEX { ("Approvals", "Codex default") } else { ("Permission mode", "Claude default") };
            let choices: Vec<(String, String)> = std::iter::once((default.to_string(), String::new())).chain(modes.iter().map(|(value, label)| (label.to_string(), value.to_string()))).collect();
            tune = tune.child(field(name, select("new-session-permission", Choice::Permission, &choices, &sheet.launch.permission, sheet.open, &mut popups)));
        }
        let launchers = sheet.launchers();
        if !launchers.is_empty() {
            let choices: Vec<(String, String)> = std::iter::once(("Default".to_string(), String::new()))
                .chain(launchers.iter().map(|l| (if l.available { l.title().to_string() } else { format!("{} (unavailable)", l.title()) }, l.id.clone())))
                .collect();
            let current = sheet.launch.launcher.as_ref().map(|l| l.id.clone()).unwrap_or_default();
            tune = tune.child(field("Runs in", select("new-session-launcher", Choice::Launcher, &choices, &current, sheet.open, &mut popups)));
        }
        body = body.child(tune);
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
                    .child(field("Branch", line_input(&sheet.branch, "new-session-branch", "ah/<name> by default", |x| &mut x.branch)))
                    .child(field("Base", line_input(&sheet.base, "new-session-base", "The folder's HEAD by default", |x| &mut x.base))),
            );
        }
        body = body.child(worktree);
    }

    body = body.child(field("Name", line_input(&sheet.alias, "new-session-alias", "Optional", |x| &mut x.alias)));
    let message = text_input(&sheet.message, BODY)
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
    body = body.child(field("First message", message));

    if sheet.options.is_none() && sheet.load_error.is_none() {
        body = body.child(note(&format!("Loading what {} can start…", sheet.host_name()), muted()));
    }
    for line in [&sheet.load_error, &sheet.model_error].into_iter().flatten() {
        body = body.child(note(line, muted()));
    }
    if let Some(error) = &sheet.error {
        body = body.child(note(error, failed()));
    }

    let mut foot = row().w_full().gap(8.0).items_center().pt(4.0);
    if let Some(submitted) = &sheet.submitted {
        foot = foot
            .child(note(submitted, muted()).shrink())
            .child(Button::new("Start another").variant(ButtonVariant::Ghost).on_press_tx(start_another).build(k))
            .child(Button::new("Done").variant(ButtonVariant::Contrast).on_press(close).build(k));
    } else {
        let hint = if sheet.starting {
            "Starting…".to_string()
        } else if !sheet.host_ready() {
            format!("{} isn't connected.", sheet.host_name())
        } else {
            request.problem().unwrap_or_default().to_string()
        };
        foot = foot.child(text(hint).font(META).color(faint()).grow(1.0).shrink().truncate());
        if sheet.existing.is_some() {
            foot = foot.child(Button::new("Open the session").variant(ButtonVariant::Ghost).on_press(open_existing).build(k));
        }
        foot = foot
            .child(Button::new("Cancel").variant(ButtonVariant::Ghost).on_press(close).build(k))
            .child(Button::new(if sheet.starting { "Starting…" } else { "Start" }).variant(ButtonVariant::Contrast).shortcut("⌘↩").disabled(!sheet.can_start()).on_press(start).build(k));
    }
    body = body.child(foot);

    Dialog::new("new-session", "New session").open(true).fit().width(600.0).click_outside(false).on_close(close).child(body).build(k, layer);
    layer.extend(popups);
}

fn capitalized(value: &str) -> String {
    let mut chars = value.chars();
    chars.next().map(|c| c.to_uppercase().chain(chars).collect()).unwrap_or_default()
}
