use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::catalog::{Client, RespondError};
use feed::create::Host;
use feed::derive::world::ProjectRecord;
use feed::projects::{self, Confirm, Entry, Item, Offer, TileChange, TileOrder, TileReply, Tiles, Worktree};
use rune::{El, PointerPress, TextEdit, Tx, col, el, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, IconButton, IconButtonVariant, Menu, MenuItem, Placement, SegmentedControl, Select, SelectAppearance};
use rune_theme_opencode::icons;

use crate::App;
use crate::over::ui::{self, BODY, META, META_MED, base, kit, strong, weak};

const SURFACES: [(&str, &str); 2] = [("claude", "Claude"), ("codex", "Codex")];

#[derive(Clone, Debug)]
pub enum Job {
    Add { path: String, title: String, host: String },
    Rename { project: String, title: String },
    Remove { project: String },
    Archive { project: String, name: String, archived: bool },
    Tools { project: String, name: String, on: bool },
    ClearLead { project: String, name: String },
    StartLead { surface: String, folder: String, host: String, name: String, project: String, replace: bool },
    SetLead { session: String, replace: bool },
    Clean { session: String, ignored: Vec<String> },
}

impl Job {
    fn run(&self, c: &Client) -> Result<(String, bool), RespondError> {
        if let Job::Clean { session, ignored } = self {
            return c.work_clean(session, ignored).map(|w| (w.outcome(), w.cleanup != "removed"));
        }
        let message = match self {
            Job::Add { path, title, host } => c.project_add(path, title, host).map(|a| a.notice()),
            Job::Rename { project, title } => c.project_rename(project, title).map(|_| format!("Renamed to {title}.")),
            Job::Remove { project } => c.project_remove(project).map(|_| "Project removed.".into()),
            Job::Archive { project, name, archived } => c.project_archive(project, *archived).map(|_| format!("{name} {}.", if *archived { "is archived" } else { "is back in Projects" })),
            Job::Tools { project, name, on } => c.project_tools(project, *on).map(|_| projects::tools_changed(name, *on)),
            Job::ClearLead { project, name } => c.lead_clear(project).map(|_| format!("{name} has no lead now.")),
            Job::StartLead { surface, folder, host, name, project, replace } => c.lead_start(surface, folder, host, name, *replace).map(|l| format!("@{} leads {project}; its first message starts it.", l.name)),
            Job::SetLead { session, replace } => c.lead_set(session, *replace).map(|l| format!("@{} leads {}.", l.name, l.project_root)),
            Job::Clean { .. } => Ok(String::new()),
        }?;
        Ok((message, false))
    }

    fn replacing(&self) -> Option<Job> {
        match self {
            Job::StartLead { replace: false, .. } | Job::SetLead { replace: false, .. } => {
                let mut again = self.clone();
                if let Job::StartLead { replace, .. } | Job::SetLead { replace, .. } = &mut again {
                    *replace = true;
                }
                Some(again)
            }
            _ => None,
        }
    }

    fn what(&self) -> &'static str {
        match self {
            Job::Add { .. } => "Adding the project",
            Job::Rename { .. } => "The rename",
            Job::Remove { .. } => "Removing the project",
            Job::Archive { archived: true, .. } => "Archiving the project",
            Job::Archive { .. } => "Unarchiving the project",
            Job::Tools { .. } => "Changing the tools setting",
            Job::ClearLead { .. } => "Clearing the lead",
            Job::StartLead { .. } | Job::SetLead { .. } => "The lead change",
            Job::Clean { .. } => "Removing the worktree",
        }
    }
}

pub enum Done {
    Changed(Job, bool, Result<(String, bool), RespondError>),
    Tiles(TileReply),
    Hosts(Result<Vec<Host>, String>),
    Folders(String, Vec<String>),
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Pick {
    Host,
    Recent,
    Checkout,
}

pub enum Sheet {
    Add { host: String, folder: TextEdit, title: TextEdit },
    Rename { project: String, name: TextEdit },
    Lead { project: String, checkout: usize, surface: &'static str, name: TextEdit },
}

pub struct State {
    pub tiles: Tiles,
    seen: Option<TileOrder>,
    menu: Option<(String, Option<PointerPress>)>,
    archived_menu: bool,
    sheet: Option<Sheet>,
    picking: Option<Pick>,
    saving: bool,
    error: Option<String>,
    confirm: Option<(Confirm, Job, bool)>,
    hosts: Vec<Host>,
    folders: HashMap<String, Vec<String>>,
    cleaning: HashSet<String>,
    kept: HashMap<String, String>,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl Default for State {
    fn default() -> State {
        let (tx, rx) = channel();
        State {
            tiles: Tiles::default(),
            seen: None,
            menu: None,
            archived_menu: false,
            sheet: None,
            picking: None,
            saving: false,
            error: None,
            confirm: None,
            hosts: Vec::new(),
            folders: HashMap::new(),
            cleaning: HashSet::new(),
            kept: HashMap::new(),
            tx,
            rx,
        }
    }
}

pub fn listed(s: &App) -> Vec<&crate::over::Project> {
    let mut list: Vec<&crate::over::Project> = s.ov.d.projects.iter().collect();
    s.ov.projects.tiles.arrangement.sort(&mut list, |p| p.key.as_str(), |p| p.name.as_str());
    list
}

fn arranged(s: &App) -> Vec<String> {
    listed(s).into_iter().map(|p| p.key.clone()).collect()
}

fn record<'a>(s: &'a App, key: &str) -> Option<&'a ProjectRecord> {
    s.ov.d.record(key)
}

fn run(s: &mut App, job: Job, from_sheet: bool) {
    let Some(client) = s.client.clone() else {
        s.ov.notice = Some("Fixture mode: not sent".into());
        return;
    };
    if let Job::Clean { session, .. } = &job {
        if !s.ov.projects.cleaning.insert(session.clone()) {
            return;
        }
        s.ov.projects.kept.remove(session);
    }
    if from_sheet {
        s.ov.projects.saving = true;
        s.ov.projects.error = None;
    }
    let tx = s.ov.projects.tx.clone();
    std::thread::spawn(move || {
        let result = job.run(&client);
        let _ = tx.send(Done::Changed(job, from_sheet, result));
    });
}

fn ask(s: &mut App, confirm: Confirm, job: Job, from_sheet: bool) {
    s.ov.projects.menu = None;
    s.ov.projects.confirm = Some((confirm, job, from_sheet));
}

fn change_tiles(s: &mut App, change: TileChange) {
    let arranged = arranged(s);
    if let Some(order) = s.ov.projects.tiles.change(&change, &arranged) {
        send_tiles(s, order);
    }
}

fn send_tiles(s: &mut App, order: TileOrder) {
    let Some(client) = s.client.clone() else {
        let _ = s.ov.projects.tiles.answered(TileReply::Saved(order));
        return;
    };
    let tx = s.ov.projects.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(Done::Tiles(client.put_tiles(&order)));
    });
}

fn choose(s: &mut App, key: &str, item: Item) {
    s.ov.projects.menu = None;
    let Some(p) = record(s, key).cloned() else { return };
    let name = p.name().to_string();
    match item {
        Item::Rename => open_sheet(s, Sheet::Rename { project: p.id.clone(), name: focused(&name) }),
        Item::Lead { .. } => {
            let first = p.lead_places().first().map(|c| p.checkouts.iter().position(|x| x == *c).unwrap_or(0)).unwrap_or(0);
            open_sheet(s, Sheet::Lead { project: p.id.clone(), checkout: first, surface: "claude", name: focused("") });
        }
        Item::ClearLead => ask(s, projects::confirm_clear_lead(&name), Job::ClearLead { project: p.id.clone(), name }, false),
        Item::Pin(on) => change_tiles(s, TileChange::Pin(p.id.clone(), on)),
        Item::Earlier => change_tiles(s, TileChange::Step(p.id.clone(), -1)),
        Item::Later => change_tiles(s, TileChange::Step(p.id.clone(), 1)),
        Item::Tools { off } => run(s, Job::Tools { project: p.id.clone(), name, on: off }, false),
        Item::Archive(true) => ask(s, projects::confirm_archive(&name), Job::Archive { project: p.id.clone(), name, archived: true }, false),
        Item::Archive(false) => run(s, Job::Archive { project: p.id.clone(), name, archived: false }, false),
        Item::Remove { project_id, place } => ask(s, projects::confirm_remove(&name, &place), Job::Remove { project: project_id }, false),
    }
}

fn focused(value: &str) -> TextEdit {
    let mut edit = TextEdit::new(value);
    edit.focused = true;
    edit
}

fn open_sheet(s: &mut App, sheet: Sheet) {
    s.composer.focused = false;
    s.ov.projects.menu = None;
    s.ov.projects.archived_menu = false;
    s.ov.projects.error = None;
    s.ov.projects.saving = false;
    s.ov.projects.picking = None;
    let adding = matches!(sheet, Sheet::Add { .. });
    s.ov.projects.sheet = Some(sheet);
    if adding {
        load_hosts(s);
        load_folders(s, "");
    }
}

pub fn add(s: &mut App) {
    open_sheet(s, Sheet::Add { host: String::new(), folder: focused(""), title: TextEdit::new("") });
}

pub fn set_lead(s: &mut App, key: &str) {
    choose(s, key, Item::Lead { change: false });
}

pub fn make_lead(s: &mut App, session: &str) {
    run(s, Job::SetLead { session: session.to_string(), replace: false }, false);
}

pub fn leads(s: &App, session: &str) -> bool {
    s.ov.d.records.iter().any(|p| p.coordinators.iter().any(|c| c.session_id.as_deref() == Some(session)))
}

fn load_hosts(s: &mut App) {
    let Some(client) = s.client.clone() else { return };
    let tx = s.ov.projects.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(Done::Hosts(client.hosts().map_err(|e| e.to_string())));
    });
}

fn load_folders(s: &mut App, host: &str) {
    let Some(client) = s.client.clone() else { return };
    if s.ov.projects.folders.contains_key(host) {
        return;
    }
    let (tx, host) = (s.ov.projects.tx.clone(), host.to_string());
    std::thread::spawn(move || {
        let Ok(options) = client.session_options(&host) else { return };
        let mut seen = HashSet::new();
        let folders = options.projects.into_iter().map(|p| p.root_path).chain(options.folders).filter(|f| seen.insert(f.clone())).collect();
        let _ = tx.send(Done::Folders(host, folders));
    });
}

fn close_sheet(s: &mut App) {
    s.ov.projects.sheet = None;
    s.ov.projects.picking = None;
    s.ov.projects.error = None;
    s.ov.projects.saving = false;
}

fn sheet_job(s: &App) -> Option<Job> {
    match s.ov.projects.sheet.as_ref()? {
        Sheet::Add { host, folder, title } => {
            let path = folder.text.trim();
            (!path.is_empty()).then(|| Job::Add { path: path.to_string(), title: title.text.trim().to_string(), host: host.clone() })
        }
        Sheet::Rename { project, name } => {
            let current = record(s, project).map(|p| p.name().to_string()).unwrap_or_default();
            projects::rename_problem(&name.text, &current).is_none().then(|| Job::Rename { project: project.clone(), title: name.text.trim().to_string() })
        }
        Sheet::Lead { project, checkout, surface, name } => {
            let p = record(s, project)?;
            let c = p.checkouts.get(*checkout).filter(|c| p.lead_places().contains(c))?;
            Some(Job::StartLead { surface: surface.to_string(), folder: c.root_path.clone(), host: c.host.target().to_string(), name: name.text.trim().trim_start_matches('@').to_string(), project: p.name().to_string(), replace: false })
        }
    }
}

fn save(s: &mut App) {
    if s.ov.projects.saving {
        return;
    }
    if let Some(job) = sheet_job(s) {
        run(s, job, true);
    }
}

fn settled(s: &mut App, job: Job, from_sheet: bool, result: Result<(String, bool), RespondError>) {
    if from_sheet {
        s.ov.projects.saving = false;
    }
    if let Job::Clean { session, .. } = &job {
        s.ov.projects.cleaning.remove(session);
    }
    let sheet_open = from_sheet && s.ov.projects.sheet.is_some();
    match result {
        Ok((message, kept)) => {
            if let Job::Clean { session, .. } = &job
                && kept
            {
                s.ov.projects.kept.insert(session.clone(), message.clone());
            }
            if sheet_open {
                close_sheet(s);
            }
            s.ov.notice = Some(message);
            if let Some(poke) = &s.overview_poke {
                let _ = poke.send(());
            }
        }
        Err(error) if projects::needs_replace(&error) && job.replacing().is_some() => {
            let again = job.replacing().unwrap_or(job);
            s.ov.projects.confirm = Some((projects::confirm_replace(&error), again, from_sheet));
        }
        Err(error) => {
            let message = if error.maybe_delivered { format!("{} may have gone through anyway: {}. Check before trying again.", job.what(), error.message) } else { format!("{} didn't work: {}", job.what(), error.message) };
            if let Job::Clean { session, .. } = &job {
                s.ov.projects.kept.insert(session.clone(), message.clone());
            }
            if sheet_open {
                s.ov.projects.error = Some(message);
            } else {
                s.ov.notice = Some(message);
            }
        }
    }
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    if s.ov.d.tile_order != s.ov.projects.seen {
        s.ov.projects.seen = s.ov.d.tile_order.clone();
        let seen = s.ov.projects.seen.clone();
        if let Some(order) = s.ov.projects.tiles.adopt(seen.as_ref()) {
            send_tiles(s, order);
        }
        changed = true;
    }
    if let Some((key, _)) = &s.ov.projects.menu
        && s.ov.d.record(key).is_none()
    {
        s.ov.projects.menu = None;
        changed = true;
    }
    while let Ok(done) = s.ov.projects.rx.try_recv() {
        changed = true;
        match done {
            Done::Changed(job, from_sheet, result) => settled(s, job, from_sheet, result),
            Done::Tiles(reply) => {
                if let Some(order) = s.ov.projects.tiles.answered(reply) {
                    send_tiles(s, order);
                }
                if let Some(error) = s.ov.projects.tiles.error.take() {
                    s.ov.notice = Some(error);
                }
            }
            Done::Hosts(Ok(hosts)) => s.ov.projects.hosts = hosts.into_iter().filter(Host::available).collect(),
            Done::Hosts(Err(_)) => s.ov.projects.hosts.clear(),
            Done::Folders(host, folders) => {
                s.ov.projects.folders.insert(host, folders);
            }
        }
    }
    changed
}

pub fn open_menu(s: &mut App, key: &str, at: Option<PointerPress>) {
    s.ov.projects.archived_menu = false;
    s.ov.projects.menu = match &s.ov.projects.menu {
        Some((k, None)) if k == key && at.is_none() => None,
        _ => Some((key.to_string(), at)),
    };
}

pub fn menu_button(key: &str) -> El {
    let key = key.to_string();
    IconButton::new(&crate::icons::MORE)
        .variant(IconButtonVariant::Ghost)
        .label("Project actions")
        .on_press(move |s: &mut App| open_menu(s, &key, None))
        .build(kit())
        .key("project-menu-trigger")
}

fn entry_item(key: &str, entry: Entry, busy: bool) -> MenuItem {
    let key = key.to_string();
    let item = entry.item.clone();
    let mut m = MenuItem::new(entry.label).disabled(!entry.enabled || busy);
    if let Some(note) = entry.note {
        m = m.description(note);
    }
    m.on_select(move |s: &mut App| choose(s, &key, item.clone()))
}

fn project_menu(s: &App, key: &str, at: Option<PointerPress>, layer: &mut Vec<El>) {
    let Some(p) = record(s, key) else { return };
    let mut m = Menu::new("project-menu").open(true).min_width(240.0).on_close(|s: &mut App| s.ov.projects.menu = None);
    m = match at {
        Some(p) => m.at(p.x, p.y),
        None => m.placement(Placement::BottomEnd),
    };
    let busy = s.ov.projects.tiles.sending();
    for (i, group) in projects::entries(p, &s.ov.projects.tiles.arrangement, &arranged(s)).into_iter().enumerate() {
        if i > 0 {
            m = m.separator();
        }
        for entry in group {
            let tiles = matches!(entry.item, Item::Pin(_) | Item::Earlier | Item::Later);
            m = m.item(entry_item(key, entry, tiles && busy));
        }
    }
    let _ = m.build(kit(), layer);
}

pub fn archived(s: &App) -> Vec<&ProjectRecord> {
    let mut list: Vec<&ProjectRecord> = s.ov.d.records.iter().filter(|p| p.archived).collect();
    list.sort_by_key(|p| p.name().to_lowercase());
    list
}

pub fn open_archived(s: &mut App) {
    s.ov.projects.menu = None;
    s.ov.projects.archived_menu = !s.ov.projects.archived_menu;
}

fn archived_menu(s: &App, layer: &mut Vec<El>) {
    let mut m = Menu::new("archived-projects").open(true).min_width(240.0).placement(Placement::BottomStart).group_label("Archived projects").on_close(|s: &mut App| s.ov.projects.archived_menu = false);
    for p in archived(s) {
        let (id, name) = (p.id.clone(), p.name().to_string());
        m = m.item(MenuItem::new(format!("Unarchive {name}")).on_select(move |s: &mut App| {
            s.ov.projects.archived_menu = false;
            run(s, Job::Archive { project: id.clone(), name: name.clone(), archived: false }, false);
        }));
    }
    let _ = m.build(kit(), layer);
}

pub fn sidebar_rows(s: &App) -> Vec<El> {
    let k = kit();
    let mut rows = vec![rune_kit::SidebarRow::new("Add project…").icon(&icons::PLUS).height(30.0).on_select(|s: &mut App, _| add(s)).build(k).key("add-project")];
    let count = archived(s).len();
    if count > 0 {
        rows.push(
            rune_kit::SidebarRow::new("Archived projects")
                .icon(&crate::icons::INBOX)
                .height(30.0)
                .detail(count.to_string())
                .on_select(|s: &mut App, _| open_archived(s))
                .build(k)
                .key("archived-menu-trigger"),
        );
    }
    rows
}

fn line_input(edit: &TextEdit, key: &'static str, label: &str, pick: fn(&mut Sheet) -> Option<&mut TextEdit>) -> El {
    text_input(edit, BODY)
        .single_line()
        .key(key)
        .label(label.to_string())
        .w_full()
        .px(10.0)
        .py(7.0)
        .radius(8.0)
        .fill(crate::theme::wash())
        .border(1.0, crate::theme::line())
        .color(strong())
        .placeholder(label.to_string(), weak())
        .on_edit(move |s: &mut App| match s.ov.projects.sheet.as_mut().and_then(pick) {
            Some(edit) => edit,
            None => &mut s.composer,
        })
        .on_submit(|s: &mut App, _: &mut Tx| save(s))
}

fn field(name: &str, control: El) -> El {
    col().w_full().gap(5.0).child(text(name.to_string()).font(META).color(weak())).child(control)
}

fn select(key: &'static str, pick: Pick, choices: Vec<(String, String)>, current: &str, placeholder: &str, open: bool, on: fn(&mut App, String), popups: &mut Vec<El>) -> El {
    let labels: Vec<&str> = choices.iter().map(|(label, _)| label.as_str()).collect();
    let values = choices.clone();
    Select::new(key, &labels)
        .appearance(SelectAppearance::Field)
        .width(400.0)
        .current(choices.iter().find(|(_, v)| v == current).map(|(l, _)| l.as_str()))
        .placeholder(placeholder)
        .open(open)
        .on_toggle(move |s: &mut App| s.ov.projects.picking = if s.ov.projects.picking == Some(pick) { None } else { Some(pick) })
        .on_close(|s: &mut App| s.ov.projects.picking = None)
        .on_choose(move |s: &mut App, label: &str, _: &mut Tx| {
            s.ov.projects.picking = None;
            if let Some((_, v)) = values.iter().find(|(l, _)| l == label) {
                on(s, v.clone());
            }
        })
        .build(kit(), popups)
}

fn pick_host(s: &mut App, host: String) {
    if let Some(Sheet::Add { host: h, .. }) = s.ov.projects.sheet.as_mut() {
        *h = host.clone();
    }
    load_folders(s, &host);
}

fn pick_recent(s: &mut App, folder: String) {
    if let Some(Sheet::Add { folder: f, .. }) = s.ov.projects.sheet.as_mut() {
        *f = TextEdit::new(folder.as_str());
    }
}

fn pick_checkout(s: &mut App, index: String) {
    if let Some(Sheet::Lead { checkout, .. }) = s.ov.projects.sheet.as_mut() {
        *checkout = index.parse().unwrap_or(0);
    }
}

fn buttons(s: &App, label: &str, can_save: bool) -> El {
    let k = kit();
    let saving = s.ov.projects.saving;
    row()
        .w_full()
        .gap(8.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(close_sheet).build(k).key("project-sheet-cancel"))
        .child(
            Button::new(if saving { "Saving…" } else { label })
                .variant(ButtonVariant::Contrast)
                .control_size(ControlSize::Small)
                .disabled(saving || !can_save)
                .on_press(save)
                .build(k)
                .key("project-sheet-save"),
        )
}

fn sheet_view(s: &App, sheet: &Sheet, layer: &mut Vec<El>) {
    let k = kit();
    let mut popups = Vec::new();
    let can_save = sheet_job(s).is_some();
    let open = s.ov.projects.picking;
    let (title, note, mut body, save_label) = match sheet {
        Sheet::Add { host, folder, title } => {
            let mut body = col().w_full().gap(12.0);
            if s.ov.projects.hosts.len() > 1 {
                let choices = s.ov.projects.hosts.iter().map(|h| (if h.is_local() { format!("{} · this computer", h.name) } else { h.name.clone() }, h.id.clone())).collect();
                body = body.child(field("Computer", select("project-host", Pick::Host, choices, host, "This computer", open == Some(Pick::Host), pick_host, &mut popups)));
            }
            body = body.child(field("Folder", line_input(folder, "project-folder", "Folder", |sh| if let Sheet::Add { folder, .. } = sh { Some(folder) } else { None })));
            let recent = s.ov.projects.folders.get(host).cloned().unwrap_or_default();
            if !recent.is_empty() {
                let choices = recent.into_iter().map(|f| (f.clone(), f)).collect();
                body = body.child(field("Recent", select("project-recent", Pick::Recent, choices, "", "Pick a recent folder…", open == Some(Pick::Recent), pick_recent, &mut popups)));
            }
            body = body.child(field("Name (optional)", line_input(title, "project-title", "Name", |sh| if let Sheet::Add { title, .. } = sh { Some(title) } else { None })));
            ("Add Project".to_string(), None, body, "Add")
        }
        Sheet::Rename { project, name } => {
            let current = record(s, project).map(|p| p.name().to_string()).unwrap_or_default();
            let mut body = col().w_full().gap(12.0).child(line_input(name, "project-name", "Name", |sh| if let Sheet::Rename { name, .. } = sh { Some(name) } else { None }));
            if let Some(problem) = projects::rename_problem(&name.text, &current).filter(|p| !p.is_empty()) {
                body = body.child(text(problem.to_string()).font(META).color(crate::theme::failed()));
            }
            (format!("Rename {current}"), Some("The name applies on every computer where this project was added.".to_string()), body, "Save")
        }
        Sheet::Lead { project, checkout, surface, name } => {
            let Some(p) = record(s, project) else { return };
            let places = p.lead_places();
            let mut body = col().w_full().gap(12.0);
            if places.len() > 1 {
                let choices = places.iter().map(|c| (format!("{}: {}", c.host.name, c.root_path), p.checkouts.iter().position(|x| x == *c).unwrap_or(0).to_string())).collect();
                body = body.child(field("Checkout", select("lead-checkout", Pick::Checkout, choices, &checkout.to_string(), "Choose…", open == Some(Pick::Checkout), pick_checkout, &mut popups)));
            }
            let at = SURFACES.iter().position(|(id, _)| id == surface);
            let agents = SegmentedControl::new(&SURFACES.map(|(_, l)| l), at)
                .label("Agent")
                .fit()
                .on_pick(|s: &mut App, i, _| {
                    if let Some(Sheet::Lead { surface, .. }) = s.ov.projects.sheet.as_mut() {
                        *surface = SURFACES[i].0;
                    }
                })
                .build(k);
            body = body.child(field("Agent", agents)).child(field("Name (optional)", line_input(name, "lead-name", "Name", |sh| if let Sheet::Lead { name, .. } = sh { Some(name) } else { None })));
            if places.is_empty() {
                body = body.child(text("No computer with this project is connected.".to_string()).font(META).color(crate::theme::failed()).wrap().w_full());
            }
            (format!("Lead of {}", p.name()), Some(p.lead_note()), body, "Start Lead")
        }
    };
    if let Some(error) = &s.ov.projects.error {
        body = body.child(text(error.clone()).font(META).color(crate::theme::failed()).wrap().w_full());
    }
    let mut dialog = Dialog::new("project-sheet", title).open(true).fit().width(440.0).on_close(close_sheet);
    if let Some(note) = note {
        dialog = dialog.description(note);
    }
    dialog.child(col().w_full().gap(14.0).px(16.0).pb(16.0).child(body).child(buttons(s, save_label, can_save))).build(k, layer);
    layer.extend(popups);
}

fn confirm_view(c: &Confirm, layer: &mut Vec<El>) {
    let k = kit();
    let buttons = row()
        .w_full()
        .gap(8.0)
        .px(16.0)
        .pb(16.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut App| s.ov.projects.confirm = None).build(k).key("project-confirm-cancel"))
        .child(
            Button::new(c.action)
                .variant(ButtonVariant::Danger)
                .control_size(ControlSize::Small)
                .on_press(|s: &mut App| {
                    if let Some((_, job, from_sheet)) = s.ov.projects.confirm.take() {
                        run(s, job, from_sheet);
                    }
                })
                .build(k)
                .key("project-confirm-go"),
        );
    Dialog::new("project-confirm", format!("{}?", c.action)).description(c.message.clone()).open(true).fit().width(420.0).on_close(|s: &mut App| s.ov.projects.confirm = None).child(buttons).build(k, layer);
}

pub fn overlays(s: &App, layer: &mut Vec<El>) {
    if let Some((key, at)) = &s.ov.projects.menu {
        project_menu(s, key, *at, layer);
    }
    if s.ov.projects.archived_menu {
        archived_menu(s, layer);
    }
    if let Some(sheet) = &s.ov.projects.sheet {
        sheet_view(s, sheet, layer);
    }
    if let Some((c, ..)) = &s.ov.projects.confirm {
        confirm_view(c, layer);
    }
}

pub fn open(s: &App) -> bool {
    s.ov.projects.sheet.is_some() || s.ov.projects.confirm.is_some()
}

pub fn close_top(s: &mut App) {
    if s.ov.projects.confirm.take().is_none() {
        close_sheet(s);
    }
}

fn merged_row(s: &App, i: usize, x: &feed::catalog::Session, w: &Worktree) -> El {
    let k = kit();
    let id = x.id.clone();
    let cleaning = s.ov.projects.cleaning.contains(&id);
    let mut c = col()
        .key(format!("merged-{i}"))
        .w_full()
        .gap(6.0)
        .padding(10.0)
        .radius(10.0)
        .fill(k.c.surface_base)
        .border(1.0, ui::edge())
        .child(row().w_full().gap(8.0).items_center().child(text(ui::short_title(&s.ov.d.title(x), 30)).font(META_MED).color(strong()).truncate().shrink()).child(ui::spacer()).child(text(s.ov.d.ago(&x.last_active)).font(META).color(weak())))
        .child(text(w.name().to_string()).font(META).color(base()).truncate());
    let offer = w.offer();
    if offer == Offer::Remove {
        c = c.child(text("Remove deletes its folder and branch if nothing in them would be lost.".to_string()).font(META).color(weak()).wrap());
    } else if let Some(h) = &w.hint {
        c = c.child(text(h.reason.clone()).font(META).color(weak()).wrap());
        if !h.command.is_empty() {
            c = c.child(text(h.command.clone()).font(META).color(base()).wrap());
        }
    }
    if let Some(note) = s.ov.projects.kept.get(&id) {
        c = c.child(text(note.clone()).font(META).color(ui::needs_color()).wrap());
    }
    let button = |label: &str, key: String, job: Job, confirm: Confirm| {
        Button::new(if cleaning { "Removing…" } else { label })
            .variant(ButtonVariant::Ghost)
            .control_size(ControlSize::Small)
            .disabled(cleaning)
            .on_press(move |s: &mut App| ask(s, confirm.clone(), job.clone(), false))
            .build(k)
            .key(key)
    };
    let clean = Job::Clean { session: id.clone(), ignored: Vec::new() };
    let mut actions = row().gap(6.0).items_center();
    actions = match &offer {
        Offer::Remove => actions.child(button("Remove…", format!("merged-{i}-remove"), clean, projects::confirm_worktree(w))),
        Offer::Check | Offer::Ignored(_) => actions.child(button("Check again…", format!("merged-{i}-check"), clean, projects::confirm_worktree(w))),
    };
    if let Offer::Ignored(paths) = &offer {
        actions = actions.child(button("Remove anyway…", format!("merged-{i}-ignored"), Job::Clean { session: id.clone(), ignored: paths.clone() }, projects::confirm_ignored(w, paths)));
    }
    c.child(actions)
}

pub fn merged(s: &App, key: &str) -> Option<El> {
    let rows: Vec<(&feed::catalog::Session, &Worktree)> = s.ov.d.sessions.iter().filter(|x| x.project_key() == key).filter_map(|x| Some((x, x.worktree.as_ref().filter(|w| w.merged())?))).collect();
    if rows.is_empty() {
        return None;
    }
    let mut c = col().w_full().gap(8.0).child(ui::section_head("Merged", Some(rows.len()), Some(text("worktrees left behind".to_string()).font(META).color(weak()))));
    for (i, (x, w)) in rows.into_iter().enumerate() {
        c = c.child(merged_row(s, i, x, w));
    }
    Some(c)
}
