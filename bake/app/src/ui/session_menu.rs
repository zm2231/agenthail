use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Duration;

use feed::action::ReadState;
use feed::catalog::{Client, RespondError, Session};
use feed::menu::{self, Act};
use feed::settings::{self, Choice, ContextUsage, ModelOption, Pick, SessionSettings};
use rune::{El, PointerPress, TextEdit, Tx, col, el, icon, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, Menu, MenuItem, Placement};

use crate::icons;
use crate::theme::{self, BODY, META, accent, failed, faint, muted};
use crate::{App, open_link, session};

pub enum Done {
    Settings(String, SessionSettings),
    Models(String, Vec<ModelOption>),
    Context(String, ContextUsage),
    Acted(String, Act, Result<Option<ReadState>, RespondError>),
    Renamed(String, Result<String, RespondError>),
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
            tx,
            rx,
        }
    }
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

fn fetch_settings(s: &App, id: &str, after: Duration) {
    let id = id.to_string();
    spawn(s, after, move |c| c.session_settings(&id).ok().map(|set| Done::Settings(id, set)));
}

fn fetch_context(s: &App, id: &str, after: Duration) {
    let id = id.to_string();
    spawn(s, after, move |c| c.context_usage(&id).ok().flatten().map(|u| Done::Context(id, u)));
}

pub fn load(s: &mut App, id: &str) {
    if let Some(f) = &s.fixtures {
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
        return;
    }
    let surface = session(s, id).map(|x| x.surface.clone()).unwrap_or_default();
    fetch_settings(s, id, Duration::ZERO);
    fetch_context(s, id, Duration::ZERO);
    if !surface.is_empty() {
        let id = id.to_string();
        spawn(s, Duration::ZERO, move |c| c.models(&surface, &id).ok().map(|m| Done::Models(id, m)));
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
            if not_sent(s) {
                return;
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
                Some(Done::Acted(id, act, result))
            });
        }
    }
}

fn start_rename(s: &mut App, id: &str) {
    let current = session(s, id).and_then(|x| x.alias.clone()).unwrap_or_default();
    s.menus.renaming = Some(id.to_string());
    s.menus.rename_text = TextEdit::new(current.as_str());
    s.menus.rename_text.focused = true;
    s.composer.focused = false;
    s.menus.rename_error = None;
    s.menus.rename_saving = false;
}

fn close_rename(s: &mut App) {
    s.menus.renaming = None;
    s.menus.rename_error = None;
    s.menus.rename_saving = false;
}

fn current_alias(s: &App, id: &str) -> Option<String> {
    session(s, id).and_then(|x| x.alias.clone()).filter(|a| !a.is_empty())
}

fn save_rename(s: &mut App) {
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
    s.menus.rename_saving = true;
    s.menus.rename_error = None;
    let name = menu::normalized(&text);
    spawn(s, Duration::ZERO, move |c| Some(Done::Renamed(id.clone(), c.alias(&id, &name))));
}

pub fn choose(s: &mut App, id: &str, chip: &settings::Chip, choice: &Choice) {
    s.menus.chip = None;
    match chip.pick(&choice.id) {
        Pick::Unchanged => {}
        Pick::Confirm => s.menus.confirm = Some(Confirm { session: id.to_string(), key: chip.key, choice: choice.clone() }),
        Pick::Apply => set(s, id, chip.key, &choice.id),
    }
}

fn set(s: &mut App, id: &str, key: &'static str, value: &str) {
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

fn landed(s: &mut App, id: &str, act: &Act, read: Option<ReadState>) {
    let Some(x) = s.sessions.iter_mut().find(|x| x.id == id) else { return };
    match act {
        Act::Pin(on) => x.pinned_at = on.then(stamp),
        Act::Archive(on) => x.archived = *on,
        Act::Read(_) => x.read_state = read.or(x.read_state.take()),
        _ => {}
    }
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok(done) = s.menus.rx.try_recv() {
        changed = true;
        match done {
            Done::Settings(id, set) => {
                s.menus.settings.insert(id, set);
            }
            Done::Models(id, models) => {
                s.menus.models.insert(id, models);
            }
            Done::Context(id, usage) => {
                s.menus.note.remove(&id);
                s.menus.context.insert(id, usage);
            }
            Done::Acted(id, act, Ok(read)) => landed(s, &id, &act, read),
            Done::Acted(_, act, Err(error)) => s.ov.notice = Some(failure(verb(&act), &error)),
            Done::Renamed(id, Ok(alias)) => {
                if let Some(x) = s.sessions.iter_mut().find(|x| x.id == id) {
                    x.alias = (!alias.is_empty()).then_some(alias);
                }
                close_rename(s);
            }
            Done::Renamed(_, Err(error)) => {
                s.menus.rename_saving = false;
                s.menus.rename_error = Some(error.message);
            }
            Done::Set(id, key, value, result) => {
                s.menus.busy.remove(&(id.clone(), key));
                match result {
                    Ok(()) => {
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
                        s.menus.note.insert(id.clone(), "Compacting the context".into());
                        fetch_context(s, &id, Duration::from_secs(8));
                    }
                    Err(error) => s.ov.notice = Some(failure("Compacting", &error)),
                }
            }
        }
    }
    changed
}

pub fn open_at(s: &mut App, id: &str, at: PointerPress) {
    s.menus.chip = None;
    s.menus.open = Some(Open { session: id.to_string(), at: Some(at) });
}

pub fn marks(x: &Session) -> El {
    let mut r = row().gap(4.0).items_center();
    if x.unread() {
        r = r.child(el().size(7.0, 7.0).radius(3.5).fill(accent()));
    }
    if x.pinned() {
        r = r.child(icon(&icons::PIN, 11.0).color(faint()));
    }
    r
}

pub fn button(x: &Session) -> El {
    let id = x.id.clone();
    col()
        .key("session-menu-trigger")
        .size(30.0, 30.0)
        .radius(9.0)
        .items_center()
        .justify_center()
        .hover(rune::Color::WHITE.alpha(0.08))
        .label("Session actions")
        .on_press(move |s: &mut App| {
            s.menus.chip = None;
            s.menus.open = match &s.menus.open {
                Some(o) if o.session == id && o.at.is_none() => None,
                _ => Some(Open { session: id.clone(), at: None }),
            };
        })
        .child(icon(&icons::MORE, 16.0).color(muted()))
}

fn act_icon(act: &Act) -> &'static rune::Icon {
    match act {
        Act::Rename => &icons::CORNER,
        Act::Terminal => &icons::TERMINAL,
        Act::Pr(_) => &icons::PULL,
        Act::Pin(_) => &icons::PIN,
        Act::Read(_) => &icons::CHECK_CIRCLE,
        Act::Archive(_) => &icons::INBOX,
    }
}

fn session_menu(s: &App, open: &Open, layer: &mut Vec<El>) {
    let Some(x) = session(s, &open.session) else { return };
    let mut m = Menu::new("session-menu").open(true).min_width(220.0).on_close(|s: &mut App| s.menus.open = None);
    m = match open.at {
        Some(p) => m.at(p.x, p.y),
        None => m.placement(Placement::BottomEnd),
    };
    for (i, group) in menu::entries(x).into_iter().enumerate() {
        if i > 0 {
            m = m.separator();
        }
        for entry in group {
            let (id, act) = (x.id.clone(), entry.act.clone());
            let mut item = MenuItem::new(entry.label).icon(act_icon(&entry.act)).disabled(!entry.enabled);
            if let Some(note) = entry.note {
                item = item.description(note);
            }
            m = m.item(item.on_select(move |s: &mut App| run(s, &id, act.clone())));
        }
    }
    let _ = m.build(theme::kit(), layer);
}

fn chip_menu(s: &App, id: &str, key: &'static str, layer: &mut Vec<El>) {
    let Some(set) = s.menus.settings.get(id) else { return };
    let models = s.menus.models.get(id).map(Vec::as_slice).unwrap_or(&[]);
    let Some(chip) = settings::chips(set, models).into_iter().find(|c| c.key == key) else { return };
    let mut m = Menu::new(format!("chip-{key}")).open(true).min_width(200.0).placement(Placement::BottomStart).on_close(|s: &mut App| s.menus.chip = None).group_label(chip.name);
    for choice in &chip.choices {
        let (id, chip_, choice_) = (id.to_string(), chip.clone(), choice.clone());
        m = m.item(MenuItem::new(choice.label.clone()).checked(choice.id == chip.value).on_select(move |s: &mut App| choose(s, &id, &chip_, &choice_)));
    }
    if chip.next_turn {
        m = m.separator().group_label("Applies from the next turn");
    }
    let _ = m.build(theme::kit(), layer);
}

fn rename_dialog(s: &App, id: &str, layer: &mut Vec<El>) {
    let k = theme::kit();
    let current = current_alias(s, id);
    let text_now = s.menus.rename_text.text.clone();
    let problem = menu::rename_problem(&text_now, current.as_deref());
    let unchanged = menu::unchanged(&text_now, current.as_deref());
    let clearing = menu::clears(&text_now, current.as_deref());
    let title = session(s, id).map(|x| x.title()).unwrap_or_default();
    let input = text_input(&s.menus.rename_text, BODY)
        .single_line()
        .key("rename-input")
        .label("Name")
        .grow(1.0)
        .min_w(0.0)
        .color(theme::text())
        .placeholder("name", faint())
        .on_edit(|s: &mut App| &mut s.menus.rename_text)
        .on_submit(|s: &mut App, _: &mut Tx| save_rename(s));
    let field = row().w_full().gap(6.0).items_center().px(10.0).py(7.0).radius(8.0).fill(theme::wash()).border(1.0, theme::line()).child(text("@").font(BODY).color(muted())).child(input);
    let mut body = col().w_full().gap(10.0).px(16.0).pb(16.0).child(field);
    if let Some(message) = s.menus.rename_error.clone().or_else(|| (!text_now.is_empty()).then_some(problem).flatten().map(String::from)) {
        body = body.child(text(message).font(META).color(failed()).wrap().w_full());
    }
    let save_label = if s.menus.rename_saving { "Saving…" } else if clearing { "Clear name" } else { "Save" };
    let buttons = row()
        .w_full()
        .gap(8.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(close_rename).build(k).key("rename-cancel"))
        .child(
            Button::new(save_label)
                .variant(ButtonVariant::Contrast)
                .control_size(ControlSize::Small)
                .disabled(s.menus.rename_saving || problem.is_some() || unchanged)
                .on_press(save_rename)
                .build(k)
                .key("rename-save"),
        );
    let description = if current.is_some() { "Other agents and links use this name to reach the session. Leave it empty to clear it." } else { "Other agents and links use this name to reach the session." };
    Dialog::new("rename", format!("Rename {title}")).description(description).open(true).fit().width(400.0).on_close(close_rename).child(body.child(buttons)).build(k, layer);
}

fn confirm_dialog(c: &Confirm, layer: &mut Vec<El>) {
    let k = theme::kit();
    let (id, key, value) = (c.session.clone(), c.key, c.choice.id.clone());
    let buttons = row()
        .w_full()
        .gap(8.0)
        .px(16.0)
        .pb(16.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut App| s.menus.confirm = None).build(k).key("confirm-cancel"))
        .child(Button::new(c.choice.label.clone()).variant(ButtonVariant::Danger).control_size(ControlSize::Small).on_press(move |s: &mut App| set(s, &id, key, &value)).build(k).key("confirm-switch"));
    Dialog::new("confirm-mode", format!("Switch to {}?", c.choice.label))
        .description("The agent will run tools without asking you first.")
        .open(true)
        .fit()
        .width(400.0)
        .on_close(|s: &mut App| s.menus.confirm = None)
        .child(buttons)
        .build(k, layer);
}

pub fn overlays(s: &App, layer: &mut Vec<El>) {
    if let Some(open) = &s.menus.open {
        session_menu(s, open, layer);
    }
    if let Some((id, key)) = &s.menus.chip {
        chip_menu(s, id, key, layer);
    }
    if let Some(id) = &s.menus.renaming {
        rename_dialog(s, id, layer);
    }
    if let Some(c) = &s.menus.confirm {
        confirm_dialog(c, layer);
    }
}
