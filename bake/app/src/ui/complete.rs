use std::collections::{HashMap, HashSet};

use feed::catalog::{RespondError, Session};
use feed::complete::{self, Can, History, Input, Item, Prompt, RuntimeCommands, SessionFiles, Slash, Token, Trigger};
use rune::{Color, El, Key, KeyEvent, TextEdit, Tx, col, row, text, text_input};

use crate::theme::{self, BODY, META, META_BOLD, SPRING, accent, failed, faint, muted, wash};
use crate::{App, Bg, session};

const SHOWN: usize = 8;

pub enum Done {
    Commands(String, Result<RuntimeCommands, String>),
    Files(String, String, Result<SessionFiles, String>),
    Prompts(String, Result<Vec<Prompt>, String>),
    Found(u64, Result<Vec<Prompt>, String>),
    Forked(String, String, Result<String, RespondError>),
    Goal(String, Result<(), RespondError>),
}

pub struct Search {
    session: String,
    pub query: TextEdit,
    all: bool,
    ix: usize,
    seq: u64,
    asked: Option<(String, bool)>,
    results: Option<Result<Vec<Prompt>, String>>,
}

#[derive(Default)]
pub struct Complete {
    session: Option<String>,
    highlighted: usize,
    dismissed: Option<String>,
    commands: HashMap<String, Result<RuntimeCommands, String>>,
    commands_asked: HashSet<String>,
    files: Option<(String, String, Result<SessionFiles, String>)>,
    files_asked: Option<(String, String)>,
    history: HashMap<String, History>,
    recorded: HashMap<String, Vec<Prompt>>,
    local: HashMap<String, Vec<String>>,
    pub search: Option<Search>,
    seq: u64,
    forking: bool,
}

fn spawn(s: &App, job: impl FnOnce(&feed::catalog::Client) -> Done + Send + 'static) {
    let Some(client) = s.client.clone() else { return };
    let out = s.outbox.clone();
    std::thread::spawn(move || {
        let _ = out.send(Bg::Complete(job(&client)));
    });
}

fn can(s: &App, x: &Session) -> Can {
    Can { stop: crate::can_stop(s, &x.id), steer: crate::busy_now(s, &x.id) && x.capabilities.steer }
}

fn models(s: &App, id: &str) -> Option<(Vec<(String, String)>, String)> {
    let chip = crate::ui::session_menu::chips_for(s, id).into_iter().find(|c| c.key == "model" && c.changeable)?;
    Some((chip.choices.iter().map(|c| (c.id.clone(), c.label.clone())).collect(), chip.value))
}

fn token(s: &App) -> Option<Token> {
    let edit = &s.composer;
    (edit.anchor == edit.focus).then(|| Token::at(&edit.text, edit.focus)).flatten()
}

fn files_for<'a>(c: &'a Complete, id: &str, query: &str) -> Option<&'a Result<SessionFiles, String>> {
    c.files.as_ref().filter(|(s, q, _)| s == id && q == query).map(|(_, _, r)| r)
}

fn shown(s: &App, x: &Session) -> (Vec<Item>, Option<String>) {
    let text = s.composer.text.as_str();
    if s.complete.dismissed.as_deref() == Some(text) || s.complete.search.is_some() {
        return (Vec::new(), None);
    }
    let plain = !super::attach::has(s, &x.id);
    if plain
        && x.capabilities.model
        && let Some(query) = complete::model_query(text)
    {
        return match models(s, &x.id) {
            Some((list, current)) => (complete::model_items(query, &list, &current), None),
            None => (Vec::new(), Some("This session's models are still loading.".into())),
        };
    }
    let Some(token) = token(s) else { return (Vec::new(), None) };
    let app: Vec<Item> = if plain { Slash::available(&x.capabilities, can(s, x)).into_iter().map(Slash::item).collect() } else { Vec::new() };
    let runtime = s.complete.commands.get(&x.id);
    let commands = runtime.and_then(|r| r.as_ref().ok());
    let mentioning = token.trigger == Trigger::Mention;
    let sessions = if mentioning { complete::sessions(&s.sessions, x) } else { Vec::new() };
    let files = if mentioning { files_for(&s.complete, &x.id, &token.query) } else { None };
    let listed = files.and_then(|r| r.as_ref().ok()).map(|f| f.files.as_slice()).unwrap_or_default();
    let items = complete::items(&token, &app, commands, &sessions, listed);
    let notice = match (token.trigger, runtime, files) {
        (Trigger::Mention, _, Some(Err(error))) => Some(format!("Couldn't list files: {error}")),
        (Trigger::Command | Trigger::Skill, Some(Err(error)), _) => Some(format!("Couldn't list this session's commands: {error}")),
        (Trigger::Command | Trigger::Skill, None, _) if items.is_empty() && s.client.is_some() => Some("Loading this session's commands…".into()),
        _ => complete::notice(&token, commands),
    };
    (items, notice)
}

pub fn menu(s: &App, x: &Session) -> Option<El> {
    let (items, notice) = shown(s, x);
    if items.is_empty() && notice.is_none() {
        return None;
    }
    let at = s.complete.highlighted.min(items.len().saturating_sub(1));
    let first = at.saturating_sub(SHOWN - 1);
    let mut list = col().key("completions").w_full().gap(1.0).px(8.0).pt(8.0).label("Completions");
    for (i, item) in items.iter().enumerate().skip(first).take(SHOWN) {
        let on = i == at;
        let picked = item.clone();
        let mut r = row()
            .key(format!("completion-{}", item.id))
            .w_full()
            .gap(10.0)
            .items_center()
            .px(10.0)
            .py(5.0)
            .radius(8.0)
            .motion(SPRING)
            .hover(Color::WHITE.alpha(0.06))
            .label(format!("{} {}", item.title, item.detail))
            .on_press_tx(move |s: &mut App, _: &mut Tx| accept(s, &picked))
            .child(text(item.title.clone()).font(META_BOLD).color(if on { theme::text() } else { muted() }).truncate().max_w(320.0))
            .child(text(item.detail.clone()).font(META).color(faint()).truncate().grow(1.0).min_w(0.0));
        if on {
            r = r.fill(accent().alpha(0.18));
        }
        list = list.child(r);
    }
    if items.len() > SHOWN {
        list = list.child(text(format!("{} of {}", at + 1, items.len())).font(META).color(faint()).px(10.0));
    }
    if let Some(note) = notice {
        list = list.child(text(note).font(META).color(faint()).px(10.0).py(4.0).key("completion-notice"));
    }
    Some(list)
}

fn line_start(t: &str, caret: usize) -> bool {
    !t[..caret.min(t.len())].contains('\n')
}

fn line_end(t: &str, caret: usize) -> bool {
    !t[caret.min(t.len())..].contains('\n')
}

fn replace_composer(s: &mut App, text: &str) {
    let focused = s.composer.focused;
    s.composer = TextEdit::new(text);
    s.composer.focused = focused;
}

pub fn key(s: &mut App, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(x) = s.composer_for.as_deref().and_then(|id| session(s, id)).cloned() else { return false };
    let (items, notice) = shown(s, &x);
    let bare = !ev.mods.cmd && !ev.mods.ctrl && !ev.mods.alt;
    if !items.is_empty() && bare {
        let n = items.len();
        let at = s.complete.highlighted.min(n - 1);
        match ev.key {
            Key::Up if !ev.mods.shift => {
                s.complete.highlighted = (at + n - 1) % n;
                return true;
            }
            Key::Down if !ev.mods.shift => {
                s.complete.highlighted = (at + 1) % n;
                return true;
            }
            Key::Tab | Key::Enter if !ev.mods.shift => {
                let item = &items[at];
                let done = complete::model_query(&s.composer.text).is_some_and(|_| s.composer.text.trim() == item.insertion) || token(s).is_some_and(|t| complete::completes(item, &s.composer.text, &t));
                if done {
                    return ev.key == Key::Tab;
                }
                accept(s, item);
                return true;
            }
            _ => {}
        }
    }
    if (!items.is_empty() || notice.is_some()) && ev.key == Key::Escape {
        s.complete.dismissed = Some(s.composer.text.clone());
        return true;
    }
    let r = tx.key_base().map(|c| c.to_ascii_lowercase()) == Some('r') || matches!(&ev.key, Key::Command('r')) || matches!(&ev.key, Key::Text(t) if t.eq_ignore_ascii_case("r"));
    if ev.mods.ctrl && r {
        open_search(s, &x.id);
        return true;
    }
    if !bare || ev.mods.shift || s.composer.anchor != s.composer.focus {
        return false;
    }
    let id = x.id.clone();
    match ev.key {
        Key::Up if line_start(&s.composer.text, s.composer.focus) => {
            let current = s.composer.text.clone();
            let history = s.complete.history.entry(id).or_default();
            match history.older(&current) {
                Some(text) => {
                    replace_composer(s, &text);
                    true
                }
                None => history.browsing(),
            }
        }
        Key::Down if line_end(&s.composer.text, s.composer.focus) => {
            let Some(text) = s.complete.history.get_mut(&id).filter(|h| h.browsing()).and_then(History::newer) else { return false };
            replace_composer(s, &text);
            true
        }
        _ => false,
    }
}

fn accept(s: &mut App, item: &Item) {
    s.complete.highlighted = 0;
    s.composer.focused = true;
    if complete::model_query(&s.composer.text).is_some() {
        replace_composer(s, &item.insertion);
        return;
    }
    let Some(token) = token(s) else { return };
    let (range, replacement, caret) = complete::edit(item, &s.composer.text, &token);
    s.composer.anchor = range.start;
    s.composer.focus = range.end;
    s.composer.insert(&replacement);
    s.composer.set_caret(caret);
}

pub fn pump(s: &mut App) -> bool {
    let Some(id) = s.composer_for.clone() else { return false };
    let mut changed = false;
    if s.complete.session.as_deref() != Some(id.as_str()) {
        s.complete.session = Some(id.clone());
        s.complete.highlighted = 0;
        s.complete.dismissed = None;
        let asked = id.clone();
        spawn(s, move |c| Done::Prompts(asked.clone(), c.prompts(Some(&asked), "")));
        changed = true;
    }
    if s.complete.dismissed.as_deref().is_some_and(|d| d != s.composer.text) {
        s.complete.dismissed = None;
        s.complete.highlighted = 0;
    }
    match token(s) {
        Some(t) if t.trigger != Trigger::Mention && s.client.is_some() && s.complete.commands_asked.insert(id.clone()) => {
            let asked = id.clone();
            spawn(s, move |c| Done::Commands(asked.clone(), c.session_commands(&asked)));
        }
        Some(t) if t.trigger == Trigger::Mention && s.complete.files_asked.as_ref() != Some(&(id.clone(), t.query.clone())) => {
            s.complete.files_asked = Some((id.clone(), t.query.clone()));
            let (asked, query) = (id.clone(), t.query);
            spawn(s, move |c| Done::Files(asked.clone(), query.clone(), c.session_files(&asked, &query)));
        }
        _ => {}
    }
    if let Some(search) = s.complete.search.as_mut() {
        let wanted = (search.query.text.trim().to_string(), search.all);
        if search.asked.as_ref() != Some(&wanted) {
            s.complete.seq += 1;
            search.seq = s.complete.seq;
            search.asked = Some(wanted.clone());
            let (seq, session) = (search.seq, (!wanted.1).then(|| search.session.clone()));
            spawn(s, move |c| Done::Found(seq, c.prompts(session.as_deref(), &wanted.0)));
            changed = true;
        }
    }
    changed
}

fn rebuild(s: &mut App, id: &str) {
    let texts = complete::sent_texts(s.complete.local.get(id).map(Vec::as_slice).unwrap_or_default(), s.complete.recorded.get(id).map(Vec::as_slice).unwrap_or_default());
    s.complete.history.entry(id.to_string()).or_default().replace(texts);
}

pub fn sent(s: &mut App, id: &str, text: &str) {
    s.complete.dismissed = None;
    if text.trim().is_empty() {
        return;
    }
    let local = s.complete.local.entry(id.to_string()).or_default();
    local.retain(|t| t != text);
    local.insert(0, text.to_string());
    s.complete.history.entry(id.to_string()).or_default().stop();
    rebuild(s, id);
}

pub fn done(s: &mut App, done: Done) {
    match done {
        Done::Commands(id, result) => {
            s.complete.commands.insert(id, result);
        }
        Done::Files(id, query, result) => s.complete.files = Some((id, query, result)),
        Done::Prompts(id, result) => {
            if let Ok(prompts) = result {
                s.complete.recorded.insert(id.clone(), prompts);
                rebuild(s, &id);
            }
        }
        Done::Found(seq, result) => {
            if let Some(search) = s.complete.search.as_mut().filter(|x| x.seq == seq) {
                search.results = Some(result.map(complete::distinct));
                search.ix = 0;
            }
        }
        Done::Forked(from, prompt, result) => {
            s.complete.forking = false;
            match result {
                Ok(id) if !id.is_empty() => {
                    s.drafts.set(&id, &prompt);
                    crate::over::open_session(s, &id);
                }
                Ok(_) => s.ov.notice = Some("The fork started, but the daemon didn't say which session it made.".into()),
                Err(error) if error.maybe_delivered => crate::note(s, &from, format!("Fork sent, not confirmed: {}", error.message)),
                Err(error) => s.ov.notice = Some(format!("Couldn't fork: {}", error.message)),
            }
        }
        Done::Goal(id, result) => {
            let line = match result {
                Ok(()) => "Goal set".to_string(),
                Err(error) if error.maybe_delivered => format!("Goal sent, not confirmed: {}", error.message),
                Err(error) => format!("Couldn't set the goal: {}", error.message),
            };
            crate::note(s, &id, line);
        }
    }
}

pub fn run(s: &mut App, id: &str, tx: &mut Tx) -> bool {
    if super::attach::has(s, id) {
        return false;
    }
    let Some(x) = session(s, id).cloned() else { return false };
    let input = Input::parse(&s.composer.text, &Slash::available(&x.capabilities, can(s, &x)));
    if let Some(problem) = input.problem() {
        crate::note(s, id, problem);
        return true;
    }
    let Input::Command(command, argument) = input else { return false };
    if command == Slash::Name
        && let Some(problem) = feed::menu::rename_problem(&argument, super::session_menu::current_alias(s, id).as_deref())
    {
        crate::note(s, id, problem.to_string());
        return true;
    }
    s.complete.dismissed = None;
    replace_composer(s, "");
    match command {
        Slash::Name => super::session_menu::rename_to(s, id, &argument),
        Slash::Compact => super::session_menu::compact(s, id),
        Slash::Stop => crate::stop(s, id),
        Slash::Steer => crate::steer_text(s, id, argument, tx),
        Slash::Model => pick_model(s, id, &argument),
        Slash::Goal => {
            if s.client.is_none() {
                crate::note(s, id, "Fixture mode: not sent".into());
            }
            let asked = id.to_string();
            spawn(s, move |c| Done::Goal(asked.clone(), c.goal_set(&asked, &argument)));
        }
    }
    true
}

fn pick_model(s: &mut App, id: &str, query: &str) {
    let chip = super::session_menu::chips_for(s, id).into_iter().find(|c| c.key == "model" && c.changeable);
    match (chip, query.is_empty()) {
        (Some(_), true) => {
            s.menus.open = None;
            s.menus.chip = Some((id.to_string(), "model"));
        }
        (Some(chip), false) => {
            let list: Vec<(String, String)> = chip.choices.iter().map(|c| (c.id.clone(), c.label.clone())).collect();
            let wanted = complete::resolve_model(query, &list);
            match chip.choices.iter().find(|c| c.id == wanted).cloned() {
                Some(choice) => super::session_menu::choose(s, id, &chip, &choice),
                None => super::session_menu::set(s, id, "model", &wanted),
            }
        }
        (None, false) => super::session_menu::set(s, id, "model", query),
        (None, true) => crate::note(s, id, "This session's models are still loading.".into()),
    }
}

fn open_search(s: &mut App, id: &str) {
    let mut query = TextEdit::new("");
    query.focused = true;
    s.composer.focused = false;
    s.complete.search = Some(Search { session: id.to_string(), query, all: false, ix: 0, seq: 0, asked: None, results: None });
}

fn close_search(s: &mut App) {
    s.complete.search = None;
    s.composer.focused = true;
}

fn use_prompt(s: &mut App, text: &str) {
    close_search(s);
    if let Some(id) = s.composer_for.clone() {
        s.complete.history.entry(id).or_default().stop();
    }
    replace_composer(s, text);
}

fn results(s: &App) -> &[Prompt] {
    match s.complete.search.as_ref().and_then(|x| x.results.as_ref()) {
        Some(Ok(list)) => list,
        _ => &[],
    }
}

fn search_key(s: &mut App, ev: &KeyEvent, _: &mut Tx) -> bool {
    let n = results(s).len();
    let Some(search) = s.complete.search.as_mut() else { return false };
    match ev.key {
        Key::Escape => close_search(s),
        Key::Down => search.ix = (search.ix + 1).min(n.saturating_sub(1)),
        Key::Up => search.ix = search.ix.saturating_sub(1),
        Key::Tab => search.all = !search.all,
        Key::Enter => {
            let at = search.ix;
            if let Some(text) = results(s).get(at).map(|p| p.text.clone()) {
                use_prompt(s, &text);
            }
        }
        _ => return false,
    }
    true
}

fn fork(s: &mut App, from: &str, prompt: &Prompt) {
    if s.complete.forking {
        return;
    }
    if s.client.is_none() {
        crate::note(s, from, "Fixture mode: not forked".into());
        return;
    }
    s.complete.forking = true;
    let (from, turn, text) = (from.to_string(), prompt.turn_id.clone(), prompt.text.clone());
    spawn(s, move |c| {
        let result = c.fork(&from, turn.as_deref(), &feed::catalog::request_key());
        Done::Forked(from.clone(), text.clone(), result)
    });
}

pub fn search(s: &App, x: &Session) -> Option<El> {
    let search = s.complete.search.as_ref().filter(|q| q.session == x.id)?;
    let input = text_input(&search.query, BODY)
        .single_line()
        .key("prompt-search")
        .label("Search what you sent")
        .grow(1.0)
        .shrink()
        .color(theme::text())
        .placeholder("Search what you sent".to_string(), faint())
        .on_edit(|s: &mut App| &mut s.complete.search.as_mut().expect("search open").query)
        .on_input_key(search_key);
    let scope = |label: &'static str, all: bool| {
        let on = search.all == all;
        row()
            .key(format!("prompt-scope-{}", if all { "all" } else { "session" }))
            .px(8.0)
            .py(3.0)
            .radius(7.0)
            .fill(if on { wash() } else { Color::TRANSPARENT })
            .hover(Color::WHITE.alpha(0.06))
            .on_press(move |s: &mut App| {
                if let Some(q) = s.complete.search.as_mut() {
                    q.all = all;
                    q.query.focused = true;
                }
            })
            .child(text(label).font(META_BOLD).color(if on { theme::text() } else { faint() }))
    };
    let head = row().w_full().gap(8.0).items_center().child(input).child(scope("This session", false)).child(scope("All sessions", true)).child(text("esc").font(META).color(faint()));
    let mut list = col().key("prompt-results").w_full().gap(1.0);
    let forks = !search.all && complete::can_fork(x);
    match &search.results {
        None => list = list.child(text("Searching…").font(META).color(faint()).px(10.0).py(4.0)),
        Some(Err(error)) => list = list.child(text(format!("Couldn't search what you sent: {error}")).font(META).color(failed()).px(10.0).py(4.0)),
        Some(Ok(found)) if found.is_empty() => {
            let empty = if search.query.text.trim().is_empty() { "Nothing sent yet." } else { "No sent prompt matches." };
            list = list.child(text(empty).font(META).color(faint()).px(10.0).py(4.0));
        }
        Some(Ok(found)) => {
            let at = search.ix.min(found.len() - 1);
            for (i, p) in found.iter().enumerate().skip(at.saturating_sub(SHOWN - 1)).take(SHOWN) {
                let chosen = p.text.clone();
                let mut body = col().grow(1.0).min_w(0.0).gap(2.0).child(text(p.text.lines().next().unwrap_or_default().to_string()).font(META).color(theme::text()).truncate());
                if search.all {
                    let title = session(s, &p.session_id).map(|x| x.title()).unwrap_or_else(|| "Another session".into());
                    body = body.child(text(title).font(META).color(faint()).truncate());
                }
                let mut r = row()
                    .key(format!("prompt-{}-{}", p.session_id, p.item_id))
                    .w_full()
                    .gap(10.0)
                    .items_center()
                    .px(10.0)
                    .py(5.0)
                    .radius(8.0)
                    .hover(Color::WHITE.alpha(0.06))
                    .on_press(move |s: &mut App| use_prompt(s, &chosen))
                    .child(body);
                if forks && complete::forkable(p) {
                    let (from, prompt) = (x.id.clone(), p.clone());
                    let label = if s.complete.forking { "Forking…" } else { "Fork from here" };
                    r = r.child(
                        text(label)
                            .font(META_BOLD)
                            .color(accent())
                            .key(format!("fork-{}", p.item_id))
                            .label("Start a new conversation with everything before this prompt, and this prompt in its composer")
                            .on_press(move |s: &mut App| fork(s, &from, &prompt)),
                    );
                }
                if i == at {
                    r = r.fill(accent().alpha(0.18));
                }
                list = list.child(r);
            }
        }
    }
    Some(col().key("prompt-search-panel").w_full().gap(6.0).px(12.0).pt(10.0).child(head).child(list))
}
