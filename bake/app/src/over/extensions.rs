use std::collections::{BTreeSet, HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::{Duration, Instant};

use feed::catalog::RespondError;
use feed::create::Host;
use feed::extension_changes::build_blocked;
use feed::extensions::{Approval, Entry, Extension, Kind, List, Tone, WebSession, approval, entries, pin_help, pins_host, problems, status};
use rune::{Color, Cx, El, ScrollSpec, col, el, icon, row, text};
use rune_kit::shell::Toolbar;
use rune_kit::{Button, ButtonVariant, ControlSize, Kit, KitCx, Spinner};
use rune_theme_opencode::icons;

use crate::over::extension_sheets::{self, Sheets};
use crate::over::ui::{self, BODY, BODY_MED, HEAD, META, META_MED, strong, weak};
use crate::over::{go, open_session};
use crate::web::{Phase, Web};
use crate::{App, Screen};

const PAIRED_EVERY: Duration = Duration::from_secs(30);
const FAILED_AT_LEAST: Duration = Duration::from_secs(10);

pub enum Done {
    Hosts(Result<Vec<Host>, String>),
    Listed(u64, String, Result<List, String>),
    Acted(String, Result<List, RespondError>),
    Signed(Result<WebSession, String>),
    Checked(String, String, Result<bool, String>),
}

pub struct Catalog {
    pub host: Host,
    pub list: Option<List>,
    pub error: Option<String>,
    pub fetched: Option<Instant>,
    failed_at: Option<Instant>,
}

impl Catalog {
    fn key(&self) -> String {
        host_key(&self.host)
    }

    fn entries(&self, origin: &str) -> Vec<Entry> {
        self.list.as_ref().map(|l| entries(l, &self.key(), &self.host.name, origin)).unwrap_or_default()
    }
}

pub struct State {
    pub catalogs: Vec<Catalog>,
    pub hosts_error: Option<String>,
    pub settings: bool,
    pub acting: Option<String>,
    pub web: Web,
    pub sheets: Sheets,
    pub pins: BTreeSet<String>,
    fetched_at: Option<Instant>,
    ages: Vec<String>,
    hosts_pending: bool,
    hosts_failed_at: Option<Instant>,
    again: bool,
    listing: HashSet<String>,
    stale: HashSet<String>,
    generation: HashMap<String, u64>,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl State {
    pub fn new() -> State {
        let (tx, rx) = channel();
        State { catalogs: Vec::new(), hosts_error: None, settings: false, acting: None, web: Web::new(), sheets: Sheets::new(), pins: BTreeSet::new(), fetched_at: None, ages: Vec::new(), hosts_pending: false, hosts_failed_at: None, again: false, listing: HashSet::new(), stale: HashSet::new(), generation: HashMap::new(), tx, rx }
    }
}

fn local_host() -> Host {
    Host { id: String::new(), name: "This computer".into(), state: "connected".into() }
}

fn host_key(host: &Host) -> String {
    if host.is_local() { "local".into() } else { host.id.clone() }
}

fn shown(s: &App) -> bool {
    matches!(s.screen, Screen::Extensions | Screen::Extension(_))
}

fn wanted(s: &App, host: &Host) -> bool {
    shown(s) || pins_host(&s.ov.ext.pins, &host_key(host))
}

pub fn relist(s: &mut App, host: &Host) {
    let key = host_key(host);
    if s.ov.ext.listing.contains(&key) {
        *s.ov.ext.generation.entry(key.clone()).or_default() += 1;
        s.ov.ext.stale.insert(key);
    } else {
        list(s, host);
    }
}

pub fn pinned(s: &App) -> Vec<Entry> {
    if s.ov.ext.pins.is_empty() {
        return Vec::new();
    }
    let origin = s.client.as_ref().map(|c| c.origin().to_string()).unwrap_or_default();
    feed::extensions::pinned(s.ov.ext.catalogs.iter().flat_map(|c| c.entries(&origin)).collect(), &s.ov.ext.pins)
}

fn toggle_pin(s: &mut App, id: String) {
    if !s.ov.ext.pins.remove(&id) {
        s.ov.ext.pins.insert(id);
    }
}

pub fn refresh(s: &mut App) {
    let Some(client) = s.client.clone() else { return };
    s.ov.ext.settings = feed::extensions::holds_settings(&client);
    s.ov.ext.fetched_at = Some(Instant::now());
    if !s.ov.ext.catalogs.iter().any(|c| c.host.is_local()) {
        s.ov.ext.catalogs.insert(0, Catalog { host: local_host(), list: None, error: None, fetched: None, failed_at: None });
    }
    list(s, &local_host());
    read_hosts(s, client);
}

fn read_hosts(s: &mut App, client: feed::catalog::Client) {
    if s.ov.ext.hosts_pending {
        return;
    }
    s.ov.ext.hosts_pending = true;
    let tx = s.ov.ext.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(Done::Hosts(client.hosts().map_err(|e| e.to_string())));
    });
}

fn list(s: &mut App, host: &Host) {
    let Some(client) = s.client.clone() else { return };
    let key = host_key(host);
    if !s.ov.ext.listing.insert(key.clone()) {
        return;
    }
    let generation = s.ov.ext.generation.entry(key.clone()).or_default();
    *generation += 1;
    let (tx, generation, host) = (s.ov.ext.tx.clone(), *generation, host.clone());
    std::thread::spawn(move || {
        let result = client.extensions((!host.is_local()).then_some(host.id.as_str())).map_err(|e| e.to_string());
        let _ = tx.send(Done::Listed(generation, key, result));
    });
}

fn act(s: &mut App, what: &str, call: impl FnOnce(&feed::catalog::Client) -> Result<List, RespondError> + Send + 'static) {
    let Some(client) = s.client.clone() else {
        s.ov.notice = Some("Fixture mode: not sent".into());
        return;
    };
    if s.ov.ext.acting.is_some() {
        return;
    }
    s.ov.ext.acting = Some(what.to_string());
    let (tx, what) = (s.ov.ext.tx.clone(), what.to_string());
    std::thread::spawn(move || {
        let _ = tx.send(Done::Acted(what, call(&client)));
    });
}

fn approve(s: &mut App, id: String) {
    act(s, &format!("Approving {id}"), move |c| c.approve_extension(&id));
}

fn reload_all(s: &mut App) {
    act(s, "Reloading extensions", |c| c.reload_extensions());
}

fn take(s: &mut App, done: Done) {
    match &done {
        Done::Hosts(result) => {
            s.ov.ext.hosts_pending = false;
            let resting = |h: &Host| s.ov.ext.catalogs.iter().any(|c| c.key() == host_key(h) && c.failed_at.is_some_and(|t| t.elapsed() < FAILED_AT_LEAST));
            let hosts: Vec<Host> = result.iter().flatten().filter(|h| h.available() && !h.is_local() && wanted(s, h) && !resting(h)).cloned().collect();
            for host in &hosts {
                list(s, host);
            }
        }
        Done::Listed(_, key, _) => {
            s.ov.ext.listing.remove(key);
            if s.ov.ext.stale.remove(key)
                && let Some(host) = s.ov.ext.catalogs.iter().find(|c| c.key() == *key).map(|c| c.host.clone())
            {
                list(s, &host);
            }
        }
        _ => {}
    }
    let ext = &mut s.ov.ext;
    match done {
        Done::Listed(generation, key, _) if ext.generation.get(&key) != Some(&generation) => {}
        Done::Hosts(Ok(hosts)) => {
            ext.hosts_error = None;
            ext.hosts_failed_at = None;
            let mut old: Vec<Catalog> = std::mem::take(&mut ext.catalogs);
            for host in hosts {
                let at = old.iter().position(|c| c.host.id == host.id);
                let mut catalog = at.map(|i| old.remove(i)).unwrap_or(Catalog { host: host.clone(), list: None, error: None, fetched: None, failed_at: None });
                if !host.available() {
                    catalog.error = Some(format!("{} is {}.", host.name, if host.state.is_empty() { "unreachable" } else { &host.state }));
                }
                catalog.host = host;
                ext.catalogs.push(catalog);
            }
        }
        Done::Hosts(Err(error)) => {
            ext.hosts_error = Some(format!("Couldn't read the computers: {error}"));
            ext.hosts_failed_at = Some(Instant::now());
        }
        Done::Listed(_, key, result) => {
            if let Some(catalog) = ext.catalogs.iter_mut().find(|c| c.key() == key) {
                match result {
                    Ok(list) => {
                        if catalog.host.available() {
                            catalog.error = list.host_problem(&catalog.host.name);
                        }
                        catalog.list = Some(list);
                        catalog.fetched = Some(Instant::now());
                    }
                    Err(error) => catalog.error = Some(error),
                }
                catalog.failed_at = catalog.error.as_ref().map(|_| Instant::now());
            }
        }
        Done::Acted(what, result) => {
            ext.acting = None;
            match result {
                Ok(list) => {
                    *ext.generation.entry("local".into()).or_default() += 1;
                    ext.fetched_at = Some(Instant::now());
                    if let Some(local) = ext.catalogs.iter_mut().find(|c| c.host.is_local()) {
                        local.error = list.host_problem(&local.host.name);
                        local.list = Some(list);
                        local.fetched = Some(Instant::now());
                        local.failed_at = local.error.as_ref().map(|_| Instant::now());
                    }
                }
                Err(error) if error.maybe_delivered => s.ov.notice = Some(format!("{what} may not have finished: {}", error.message)),
                Err(error) => s.ov.notice = Some(format!("{what} didn't work: {}", error.message)),
            }
        }
        Done::Signed(result) => ext.web.signed(result, time::OffsetDateTime::now_utc()),
        Done::Checked(page, value, known) => ext.web.checked(&page, &value, known),
    }
}

pub fn current_entry(s: &App) -> Option<Entry> {
    let Screen::Extension(id) = &s.screen else { return None };
    find(s, id)
}

fn find(s: &App, id: &str) -> Option<Entry> {
    let origin = s.client.as_ref().map(|c| c.origin().to_string()).unwrap_or_default();
    s.ov.ext.catalogs.iter().flat_map(|c| c.entries(&origin)).find(|e| e.id() == id)
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = extension_sheets::drain(s);
    while let Ok(done) = s.ov.ext.rx.try_recv() {
        take(s, done);
        changed = true;
    }
    if !shown(s) && s.ov.ext.pins.is_empty() {
        return changed;
    }
    if s.ov.ext.again && s.ov.ext.listing.is_empty() && !s.ov.ext.hosts_pending {
        s.ov.ext.again = false;
        refresh(s);
    }
    match s.ov.ext.fetched_at {
        None => refresh(s),
        Some(at) => {
            let retry = |since: Option<Instant>, after: Duration| since.is_some_and(|t| t.elapsed() >= after);
            if retry(s.ov.ext.hosts_failed_at, PAIRED_EVERY)
                && let Some(client) = s.client.clone()
            {
                read_hosts(s, client);
            }
            let poll = at.elapsed() >= PAIRED_EVERY && s.ov.ext.catalogs.iter().any(|c| !c.host.is_local() && wanted(s, &c.host));
            if poll {
                s.ov.ext.fetched_at = Some(Instant::now());
            }
            let due = |c: &Catalog| {
                wanted(s, &c.host) && (c.host.is_local() || c.host.available()) && (retry(c.failed_at, PAIRED_EVERY) || (poll && !c.host.is_local() && (c.failed_at.is_none() || retry(c.failed_at, FAILED_AT_LEAST))))
            };
            let hosts: Vec<Host> = s.ov.ext.catalogs.iter().filter(|c| due(c)).map(|c| c.host.clone()).collect();
            for host in hosts {
                list(s, &host);
            }
        }
    }
    let ages: Vec<String> = s.ov.ext.catalogs.iter().filter_map(|c| Some(feed::extensions::age(c.fetched?.elapsed().as_secs(), s.ov.ext.listing.contains(&c.key())))).collect();
    if ages != s.ov.ext.ages {
        s.ov.ext.ages = ages;
        changed = true;
    }
    let want = current_entry(s).and_then(|e| e.url);
    if let Some(client) = s.client.clone()
        && want.is_some()
        && s.ov.ext.web.sign_in_due(time::OffsetDateTime::now_utc())
    {
        let tx = s.ov.ext.tx.clone();
        std::thread::spawn(move || {
            let _ = tx.send(Done::Signed(client.web_session().map_err(|e| e.message)));
        });
    }
    if let Some((page, cookie)) = s.ov.ext.web.check_due(want.as_deref()) {
        let tx = s.ov.ext.tx.clone();
        std::thread::spawn(move || {
            let known = feed::extensions::session_known(&page, &cookie);
            let _ = tx.send(Done::Checked(page, cookie.value, known));
        });
    }
    changed | s.ov.ext.web.pump(want.as_deref())
}

pub fn changed(s: &mut App) {
    if shown(s) || !s.ov.ext.pins.is_empty() {
        s.ov.ext.again = !s.ov.ext.listing.is_empty() || s.ov.ext.hosts_pending;
        refresh(s);
    } else {
        s.ov.ext.fetched_at = None;
    }
}

pub fn reopened(s: &mut App) {
    s.ov.ext.web.recheck();
    changed(s);
}

pub fn arrived(s: &mut App) {
    if s.ov.ext.fetched_at.is_none_or(|at| at.elapsed() >= Duration::from_secs(2)) {
        refresh(s);
    }
}

fn covered(s: &App) -> bool {
    let menus = &s.menus;
    s.ov.ext.sheets.is_open()
        || s.ov.jump.is_some()
        || s.ov.notice.is_some()
        || s.new_session.is_some()
        || s.ov.inbox.note.is_some()
        || s.ov.work.words.is_some()
        || menus.open.is_some()
        || menus.chip.is_some()
        || menus.confirm.is_some()
        || menus.renaming.is_some()
        || s.accounts.open
        || s.accounts.planning.is_some()
        || !s.limits.warnings.is_empty()
        || s.limits.picking.is_some()
        || s.limits.confirm.is_some()
        || s.limits.consent.is_some()
        || s.prs.linking.is_some()
}

pub fn open(s: &mut App, entry: &Entry) {
    match &entry.approval_session {
        Some(session) => open_session(s, session),
        None => {
            if let Some(url) = &entry.url {
                s.ov.ext.web.retry(url);
            }
            go(s, Screen::Extension(entry.id()))
        }
    }
}

fn tone_color(tone: Tone, k: &Kit) -> Color {
    match tone {
        Tone::Good => k.c.success.icon,
        Tone::Waiting => ui::needs_color(k),
        Tone::Failed => crate::theme::failed(),
        Tone::Quiet => weak(k),
    }
}

const OFFERED: [(&str, &rune::Icon); 16] = [
    ("terminal", &icons::CONSOLE),
    ("server", &icons::SERVER),
    ("globe", &icons::GLOBE),
    ("network", &icons::GLOBE),
    ("chart", &icons::WINDOW_ANALYTICS),
    ("gauge", &icons::WINDOW_ANALYTICS),
    ("checklist", &icons::CHECKLIST),
    ("list", &icons::BULLET_LIST),
    ("grid", &icons::DOT_GRID),
    ("doc", &icons::FILE_DOCUMENT),
    ("note", &icons::FILE_DOCUMENT),
    ("folder", &icons::FOLDER),
    ("gear", &icons::SETTINGS_GEAR),
    ("lock", &icons::LOCK),
    ("bubble", &icons::SPEECH_BUBBLE),
    ("photo", &icons::PHOTO),
];

fn offered_icon(name: &str) -> Option<&'static rune::Icon> {
    let words: Vec<&str> = name.split('.').collect();
    OFFERED.iter().find(|(word, _)| words.iter().any(|w| w.contains(word))).map(|(_, icon)| *icon)
}

pub(crate) fn entry_icon(entry: &Entry) -> &'static rune::Icon {
    offered_icon(&entry.icon).unwrap_or(match entry.kind {
        Kind::Mount(_) => &icons::GLOBE,
        Kind::Page => &icons::WINDOW_CURSOR,
    })
}

fn pin_button(entry: &Entry, pinned: bool, k: &Kit) -> El {
    let id = entry.id();
    let label = if pinned { format!("Unpin {}", entry.label) } else { format!("Pin {}", entry.label) };
    rune_kit::IconButton::new(&crate::icons::PIN)
        .label(label)
        .help(pin_help(pinned, entry.offered))
        .control_size(ControlSize::Small)
        .on_press(move |s: &mut App| toggle_pin(s, id.clone()))
        .build(k)
        .opacity(if pinned { 1.0 } else if entry.offered { 0.85 } else { 0.5 })
        .key(format!("ext-pin-{}", entry.id()))
}

fn entry_row(entry: &Entry, pinned: bool, k: &Kit) -> El {
    let mut r = row()
        .key(format!("ext-entry-{}", entry.id()))
        .w_full()
        .gap(10.0)
        .items_center()
        .px(10.0)
        .py(8.0)
        .radius(8.0)
        .hover(k.c.surface_raised_hover)
        .child(col().size(28.0, 28.0).radius(7.0).items_center().justify_center().fill(k.c.surface_raised_hover).child(icon(entry_icon(entry), k.text_size(14.0)).color(strong(k))))
        .child(
            col()
                .grow(1.0)
                .shrink()
                .min_w(0.0)
                .gap(2.0)
                .child(row().gap(6.0).items_center().child(text(entry.label.clone()).font(k.text(BODY_MED)).color(strong(k)).truncate().shrink()).children(entry.badge.clone().map(|b| text(b).font(k.text(META_MED)).color(ui::needs_color(k)))))
                .child(text(entry.subtitle()).font(k.text(META)).color(if entry.awaits_approval { ui::needs_color(k) } else { weak(k) }).truncate()),
        );
    r = r.child(pin_button(entry, pinned, k)).child(icon(&icons::CHEVRON_RIGHT, k.text_size(12.0)).color(weak(k)));
    let e = entry.clone();
    r.on_press(move |s: &mut App| open(s, &e))
}

fn approval_line(s: &App, ext: &Extension, host_local: bool, host_name: &str, k: &Kit) -> Option<El> {
    let busy = s.ov.ext.acting.is_some();
    match approval(ext) {
        Approval::Nothing => None,
        Approval::Ask(session) => Some(
            Button::new("Open the request")
                .variant(ButtonVariant::Neutral)
                .control_size(ControlSize::Small)
                .icon(&icons::ARROW_RIGHT)
                .help(format!("A session asked you to approve {}; answer it there.", ext.label))
                .on_press(move |s: &mut App| open_session(s, &session))
                .build(k)
                .key(format!("ext-ask-{}", ext.id)),
        ),
        Approval::Direct if host_local && s.ov.ext.settings => {
            let id = ext.id.clone();
            Some(
                Button::new(if busy { "Working…" } else { "Approve" })
                    .variant(ButtonVariant::Contrast)
                    .control_size(ControlSize::Small)
                    .disabled(busy)
                    .help(format!("Grant what {}/extension.json declares now", ext.id))
                    .on_press(move |s: &mut App| approve(s, id.clone()))
                    .build(k)
                    .key(format!("ext-approve-{}", ext.id)),
            )
        }
        Approval::Direct => Some(text(format!("Nothing has asked for it yet. {}, approve it with agenthail extensions approve {}.", if host_local { "On this computer".to_string() } else { format!("On {host_name}") }, ext.id)).font(k.text(META)).color(weak(k)).wrap().w_full()),
    }
}

fn capabilities(ext: &Extension, k: &Kit) -> El {
    let mut line = row().w_full().gap(6.0).items_center().wrap().child(text("Can".to_string()).font(k.text(META_MED)).color(weak(k)));
    if ext.capabilities.is_empty() {
        line = line.child(text("nothing yet".to_string()).font(k.text(META)).color(weak(k)));
    }
    for cap in &ext.capabilities {
        line = line.child(row().px(7.0).py(2.0).radius(6.0).fill(k.c.surface_raised_hover).child(text(cap.clone()).font(k.text(META)).color(strong(k))));
    }
    for cap in &ext.pending_capabilities {
        line = line.child(row().px(7.0).py(2.0).radius(6.0).border(1.0, ui::needs_color(k).alpha(0.6)).child(text(cap.clone()).font(k.text(META)).color(ui::needs_color(k))));
    }
    line
}

fn extension_card(s: &App, ext: &Extension, own: &[&Entry], host: &Host, k: &Kit) -> El {
    let host_local = host.is_local();
    let (state, tone) = status(ext);
    let mut head = row()
        .w_full()
        .gap(10.0)
        .items_center()
        .child(icon(&icons::EXTENSIONS, k.text_size(16.0)).color(strong(k)))
        .child(text(ext.label.clone()).font(k.text(HEAD)).color(strong(k)).truncate().shrink())
        .child(ui::pill(&icons::STATUS, &state, tone_color(tone, k), k));
    if !ext.badge.is_empty() {
        head = head.child(text(ext.badge.clone()).font(k.text(META_MED)).color(ui::needs_color(k)));
    }
    head = head.child(ui::spacer()).child(text(ext.id.clone()).font(k.text(META)).color(weak(k)));
    let mut body = col().w_full().gap(8.0).padding(14.0).child(head);
    if !ext.description.is_empty() {
        body = body.child(text(ext.description.clone()).font(k.text(META)).color(ui::base(k)).wrap().w_full());
    }
    body = body.child(capabilities(ext, k));
    for p in problems(ext) {
        body = body.child(text(p.text).font(k.text(META)).color(if p.failed { crate::theme::failed() } else { ui::needs_color(k) }).wrap().w_full());
    }
    body = body.children(approval_line(s, ext, host_local, &host.name, k).map(|line| row().w_full().child(line)));
    if own.is_empty() {
        body = body.child(text("No page. Its jobs and actions run in the background.".to_string()).font(k.text(META)).color(weak(k)));
    } else {
        body = body.child(col().w_full().gap(2.0).children(own.iter().map(|e| entry_row(e, s.ov.ext.pins.contains(&e.id()), k))));
    }
    col().key(format!("ext-card-{}", ext.id)).w_full().radius(12.0).fill(k.c.surface_base).border(1.0, ui::edge()).child(body)
}

fn sections(s: &App) -> Vec<&Catalog> {
    s.ov.ext.catalogs.iter().collect()
}

fn host_tools(s: &App, catalog: &Catalog, k: &Kit) -> Option<El> {
    s.client.as_ref()?;
    let key = catalog.key();
    let blocked = build_blocked(catalog.list.is_some(), catalog.error.as_deref(), &catalog.host.name);
    let (history_host, build_host) = (catalog.host.clone(), catalog.host.clone());
    let history = Button::new("History")
        .variant(ButtonVariant::Ghost)
        .control_size(ControlSize::Small)
        .icon(&crate::icons::CLOCK)
        .help(if catalog.host.is_local() { "Every change to the extensions on this computer, with Undo".to_string() } else { format!("Every change to the extensions on {}, with Undo", catalog.host.name) })
        .on_press(move |s: &mut App| extension_sheets::open_history(s, history_host.clone()))
        .build(k)
        .key(format!("ext-history-{key}"));
    let build = Button::new("New App…")
        .variant(ButtonVariant::Neutral)
        .control_size(ControlSize::Small)
        .icon(&icons::PLUS)
        .disabled(blocked.is_some())
        .help(blocked.unwrap_or_else(|| "Describe an app and a builder session makes it".into()))
        .on_press(move |s: &mut App| extension_sheets::open_build(s, build_host.clone()))
        .build(k)
        .key(format!("ext-new-{key}"));
    Some(row().gap(6.0).items_center().child(history).child(build))
}

fn catalog_block(s: &App, catalog: &Catalog, titled: bool, origin: &str, k: &Kit) -> El {
    let mut block = col().w_full().gap(10.0);
    let mut head = row().w_full().gap(8.0).items_center().pt(6.0);
    if titled {
        let title = if catalog.host.is_local() { "On this computer".to_string() } else { format!("On {}", catalog.host.name) };
        head = head.child(text(title).font(k.text(HEAD)).color(strong(k)));
    }
    head = head.child(ui::spacer());
    if let Some(at) = catalog.fetched {
        let refreshing = s.ov.ext.listing.contains(&catalog.key());
        head = head.child(text(feed::extensions::age(at.elapsed().as_secs(), refreshing)).font(k.text(META)).color(weak(k)).key(format!("ext-age-{}", catalog.key())));
    }
    block = block.child(head.children(host_tools(s, catalog, k)));
    if let Some(error) = &catalog.error {
        block = block.child(text(error.clone()).font(k.text(META)).color(crate::theme::failed()).wrap().w_full());
    }
    let Some(list) = &catalog.list else {
        if catalog.error.is_none() {
            block = block.child(row().gap(8.0).items_center().child(Spinner::new().size(k.text_size(12.0)).build(k)).child(text("Looking for extensions…".to_string()).font(k.text(META)).color(weak(k))));
        }
        return block;
    };
    if list.extensions.is_empty() && catalog.error.is_none() {
        let empty = if catalog.host.is_local() { "No extensions on this computer yet. Use New App to describe one; each one is a folder in ~/.agenthail/extensions.".to_string() } else { format!("No extensions on {}.", catalog.host.name) };
        return block.child(text(empty).font(k.text(BODY)).color(weak(k)));
    }
    let all = catalog.entries(origin);
    for ext in &list.extensions {
        let own: Vec<&Entry> = all.iter().filter(|e| e.extension_id == ext.id).collect();
        block = block.child(extension_card(s, ext, &own, &catalog.host, k));
    }
    block
}

fn reload_button(s: &App, k: &Kit) -> Option<El> {
    let has_local = s.ov.ext.catalogs.iter().any(|c| c.host.is_local() && c.list.is_some());
    if !s.ov.ext.settings {
        return has_local.then(|| text("Reload them with agenthail extensions reload".to_string()).font(k.text(META)).color(weak(k)));
    }
    has_local.then(|| {
        let busy = s.ov.ext.acting.is_some();
        Button::new(if busy { "Working…" } else { "Reload extensions" })
            .variant(ButtonVariant::Ghost)
            .control_size(ControlSize::Small)
            .icon(&icons::REFRESH)
            .disabled(busy)
            .help("Read every extension folder on this computer again")
            .on_press(reload_all)
            .build(k)
            .key("ext-reload-all")
    })
}

pub fn hub(s: &App, cx: &mut Cx<App>, main_kit: &Kit) -> El {
    let k = cx.kit();
    let origin = s.client.as_ref().map(|c| c.origin().to_string()).unwrap_or_default();
    let mut list = col().w_full().max_w(880.0).gap(16.0).px(24.0).pt(16.0).pb(32.0);
    list = list.child(row().w_full().items_center().child(text("Apps".to_string()).font(k.text(ui::TITLE)).color(strong(&k))).child(ui::spacer()).children(reload_button(s, &k)));
    if s.client.is_none() {
        list = list.child(text("Fixture mode: no daemon to list extensions from.".to_string()).font(k.text(BODY)).color(weak(&k)));
    }
    if let Some(error) = &s.ov.ext.hosts_error {
        list = list.child(text(error.clone()).font(k.text(META)).color(crate::theme::failed()).wrap().w_full());
    }
    let shown = sections(s);
    let titled = shown.len() > 1 || shown.iter().any(|c| !c.host.is_local());
    for catalog in shown {
        list = list.child(catalog_block(s, catalog, titled, &origin, &k));
    }
    let main = col().key("extensions").w_full().h_full().items_center().scroll(ScrollSpec { stick_bottom: false }).child(list);
    let toolbar = Toolbar::new().leading(ui::crumb(&[("Apps", true)], &k)).trailing(ui::needs_button(s, &k));
    ui::frame(cx, s, Some(ui::sidebar(s, None, None, &k)), main, None, toolbar, main_kit)
}

fn closed(title: &str, detail: String, session: Option<String>, k: &Kit) -> El {
    let mut actions = row().gap(8.0).child(Button::new("All apps").variant(ButtonVariant::Neutral).control_size(ControlSize::Small).on_press(|s: &mut App| go(s, Screen::Extensions)).build(k).key("ext-all"));
    if let Some(session) = session {
        actions = actions.child(Button::new("Open the request").variant(ButtonVariant::Contrast).control_size(ControlSize::Small).on_press(move |s: &mut App| open_session(s, &session)).build(k).key("ext-open-ask"));
    }
    col()
        .w_full()
        .h_full()
        .items_center()
        .justify_center()
        .gap(10.0)
        .padding(32.0)
        .child(icon(&icons::EXTENSIONS, 28.0).color(weak(k)))
        .child(text(title.to_string()).font(k.text(HEAD)).color(strong(k)))
        .child(text(detail).font(k.text(BODY)).color(weak(k)).wrap().max_w(520.0))
        .child(actions)
}

fn phase_note(web: &Web, host: &str, k: &Kit) -> Option<El> {
    let label = match web.phase() {
        _ if web.slow() => format!("Still loading. If it doesn't open, check that {host} is serving it, then Reload."),
        Phase::SigningIn => "Signing in…".into(),
        Phase::Loading => "Loading…".into(),
        _ => return None,
    };
    Some(row().gap(6.0).items_center().child(Spinner::new().size(k.text_size(11.0)).build(k)).child(text(label).font(k.text(META)).color(weak(k)).truncate().max_w(420.0)))
}

fn page_tools(s: &App, k: &Kit) -> El {
    let fill = !s.ov.sidebar.is_open();
    row()
        .gap(4.0)
        .items_center()
        .child(rune_kit::IconButton::new(&icons::REFRESH).label("Reload").help("Reload").control_size(ControlSize::Small).on_press(|s: &mut App| s.ov.ext.web.reload()).build(k).key("ext-page-reload"))
        .child(
            rune_kit::IconButton::new(if fill { &icons::COLLAPSE } else { &icons::EXPAND })
                .label(if fill { "Show the sidebar" } else { "Fill the window" })
                .help(if fill { "Show the sidebar" } else { "Fill the window" })
                .control_size(ControlSize::Small)
                .on_press(|s: &mut App| s.ov.sidebar.toggle())
                .build(k)
                .key("ext-page-fill"),
        )
}

pub fn page(s: &App, cx: &mut Cx<App>, id: &str, main_kit: &Kit) -> El {
    let k = cx.kit();
    let entry = find(s, id);
    let label = entry.as_ref().map(|e| e.label.clone()).unwrap_or_else(|| "App".into());
    let main = match &entry {
        Some(entry) => match (&entry.url, &s.ov.ext.web.phase(), s.ov.ext.web.slot()) {
            (Some(_), Phase::Failed(message), _) => {
                col()
                    .w_full()
                    .h_full()
                    .items_center()
                    .justify_center()
                    .gap(10.0)
                    .padding(32.0)
                    .child(icon(&icons::WARNING, 28.0).color(crate::theme::failed()))
                    .child(text(entry.label.clone()).font(k.text(HEAD)).color(strong(&k)))
                    .child(text(message.clone()).font(k.text(BODY)).color(weak(&k)).wrap().max_w(520.0))
                    .child(Button::new("Reload").variant(ButtonVariant::Contrast).control_size(ControlSize::Small).on_press(|s: &mut App| s.ov.ext.web.reload()).build(&k).key("ext-retry"))
            }
            (Some(url), _, Some(_)) if covered(s) || !s.ov.ext.web.shows(url) => el().w_full().h_full(),
            (Some(_), _, Some(slot)) => el().key("ext-page").w_full().h_full().radius(10.0).native_view(slot),
            (Some(_), _, None) => el().w_full().h_full(),
            (None, _, _) => closed(&entry.label, entry.closed_reason(), entry.approval_session.clone(), &k),
        },
        None if s.ov.ext.catalogs.iter().all(|c| c.list.is_none()) => col().w_full().h_full().items_center().justify_center().child(Spinner::new().size(k.text_size(16.0)).build(&k)),
        None => closed("This extension was removed", "It is no longer on any computer this app can reach.".into(), None, &k),
    };
    let host = entry.as_ref().map(|e| format!("on {}", e.host_name)).unwrap_or_default();
    let crumb = row().gap(8.0).items_center().child(row().on_press(|s: &mut App| go(s, Screen::Extensions)).child(ui::crumb(&[("Apps", false)], &k))).child(text("/".to_string()).font(k.text(BODY)).color(weak(&k))).child(text(label).font(k.text(BODY_MED)).color(strong(&k)).truncate().max_w(380.0)).child(text(host).font(k.text(META)).color(weak(&k)));
    let mut trailing = row().gap(10.0).items_center();
    if entry.as_ref().is_some_and(|e| e.url.is_some()) {
        trailing = trailing.children(phase_note(&s.ov.ext.web, entry.as_ref().map_or("the computer", |e| e.host_name.as_str()), &k)).child(page_tools(s, &k));
    }
    let toolbar = Toolbar::new().leading(crumb).trailing(trailing.child(ui::needs_button(s, &k)));
    ui::frame(cx, s, Some(ui::sidebar(s, None, None, &k)), col().w_full().h_full().padding(6.0).child(main), None, toolbar, main_kit)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_offered_symbol_name_picks_the_nearest_icon() {
        assert!(std::ptr::eq(offered_icon("server.rack").unwrap(), &icons::SERVER));
        assert!(std::ptr::eq(offered_icon("rectangle.grid.2x2").unwrap(), &icons::DOT_GRID));
        assert!(std::ptr::eq(offered_icon("chart.bar.xaxis").unwrap(), &icons::WINDOW_ANALYTICS));
        assert!(std::ptr::eq(offered_icon("checklist").unwrap(), &icons::CHECKLIST));
        assert!(offered_icon("").is_none());
        assert!(offered_icon("puzzlepiece.extension").is_none());
    }
}
