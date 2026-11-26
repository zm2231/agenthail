use std::collections::{HashMap, HashSet};

use feed::catalog::{RespondError, Session};
use feed::complete::{self, Can, Found, History, Input, Item, Prompt, RuntimeCommands, SessionFiles, Slash, Token, Trigger};
use rune::{Color, El, Key, KeyEvent, TextEdit, Tx, col, row, text, text_input};

use crate::theme::{self, BODY, META, META_BOLD, SPRING, accent, failed, faint, muted, wash};
use crate::{App, Bg, session};

const SHOWN: usize = 8;

pub enum Done {
    Commands(String, Result<RuntimeCommands, String>),
    Files(u64, String, String, Result<SessionFiles, String>),
    Prompts(u64, String, Result<Vec<Prompt>, String>),
    Found(u64, Result<Found, String>),
    Forked(String, String, String, Result<String, RespondError>),
    Goal(String, String, Result<feed::composer::Delivered, RespondError>),
}

pub struct Search {
    session: String,
    pub query: TextEdit,
    all: bool,
    ix: usize,
    seq: u64,
    asked: Option<(String, bool)>,
    results: Option<Result<Vec<Prompt>, String>>,
    problems: Vec<String>,
    more: bool,
}

#[derive(Default)]
pub struct Complete {
    session: Option<String>,
    highlighted: usize,
    dismissed: Option<String>,
    commands: HashMap<String, Result<RuntimeCommands, String>>,
    commands_asked: HashSet<String>,
    files: Option<(String, String, Result<SessionFiles, String>)>,
    files_asked: Option<(u64, String, String)>,
    history: HashMap<String, History>,
    recorded: HashMap<String, Vec<Prompt>>,
    prompts_asked: HashMap<String, u64>,
    goal_pending: HashSet<String>,
    local: HashMap<String, Vec<(String, time::OffsetDateTime)>>,
    pub search: Option<Search>,
    seq: u64,
    forking: bool,
    fork_keys: HashMap<String, (String, String)>,
    shown_ids: Vec<String>,
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

fn model_mode<'a>(s: &'a App, x: &Session) -> Option<&'a str> {
    let plain = !super::attach::has(s, &x.id);
    if plain && x.capabilities.model { complete::model_query(&s.composer.text) } else { None }
}

fn picking_model(s: &App) -> bool {
    s.composer_for.as_deref().and_then(|id| session(s, id)).is_some_and(|x| model_mode(s, x).is_some())
}

fn shown(s: &App, x: &Session) -> (Vec<Item>, Option<String>) {
    let text = s.composer.text.as_str();
    if s.complete.dismissed.as_deref() == Some(text) || s.complete.search.as_ref().is_some_and(|q| q.session == x.id) {
        return (Vec::new(), None);
    }
    let plain = !super::attach::has(s, &x.id);
    if let Some(query) = model_mode(s, x) {
        return match models(s, &x.id) {
            Some((list, current)) => (complete::model_items(query, &list, &current), None),
            None => (Vec::new(), Some(models_note(s, &x.id))),
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
                if completed(s, item) {
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
    if let Some(h) = s.complete.history.get_mut(&id).filter(|h| h.browsing() && h.current() != Some(s.composer.text.as_str())) {
        h.stop();
        return false;
    }
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

fn completed(s: &App, item: &Item) -> bool {
    if picking_model(s) {
        return s.composer.text.trim() == item.insertion;
    }
    token(s).is_some_and(|t| complete::completes(item, &s.composer.text, &t))
}

pub fn submit(s: &mut App, id: &str, tx: &mut Tx) {
    let open = session(s, id).filter(|_| s.composer_for.as_deref() == Some(id)).map(|x| shown(s, x).0).unwrap_or_default();
    if let Some(item) = open.get(s.complete.highlighted.min(open.len().saturating_sub(1))).filter(|item| !completed(s, item)).cloned() {
        accept(s, &item);
        return;
    }
    crate::send_to(s, id, tx);
}

fn accept(s: &mut App, item: &Item) {
    s.complete.highlighted = 0;
    s.composer.focused = true;
    if picking_model(s) {
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
    if s.screen != crate::Screen::Session
        && let Some(id) = s.composer_for.clone()
    {
        super::session_menu::ensure_models(s, &id);
    }
    if s.complete.search.as_ref().is_some_and(|q| s.composer_for.as_deref() != Some(q.session.as_str())) {
        s.complete.search = None;
        s.composer.focused = true;
    }
    let Some(id) = s.composer_for.clone() else {
        if let Some(left) = s.complete.session.take() {
            leave_history(s, &left);
        }
        return false;
    };
    let mut changed = false;
    if s.complete.session.as_deref() != Some(id.as_str()) {
        if let Some(left) = s.complete.session.clone() {
            leave_history(s, &left);
        }
        s.complete.session = Some(id.clone());
        s.complete.highlighted = 0;
        s.complete.dismissed = None;
        if matches!(s.complete.commands.get(&id), Some(Err(_))) {
            s.complete.commands.remove(&id);
            s.complete.commands_asked.remove(&id);
        }
        refresh(s, &id);
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
        Some(t) if t.trigger == Trigger::Mention && s.complete.files_asked.as_ref().is_none_or(|(_, i, q)| *i != id || *q != t.query) => {
            s.complete.seq += 1;
            let (seq, asked, query) = (s.complete.seq, id.clone(), t.query);
            s.complete.files_asked = Some((seq, asked.clone(), query.clone()));
            spawn(s, move |c| Done::Files(seq, asked.clone(), query.clone(), c.session_files(&asked, &query)));
        }
        Some(t) if t.trigger == Trigger::Mention => {}
        _ => s.complete.files_asked = None,
    }
    if let Some(search) = s.complete.search.as_mut() {
        let wanted = (search.query.text.trim().to_string(), search.all);
        if search.asked.as_ref() != Some(&wanted) {
            s.complete.seq += 1;
            search.seq = s.complete.seq;
            search.asked = Some(wanted.clone());
            search.ix = 0;
            search.problems.clear();
            search.more = false;
            search.results = s.client.is_none().then(|| Err("Fixture mode: searching needs the daemon".to_string()));
            let (seq, session) = (search.seq, (!wanted.1).then(|| search.session.clone()));
            spawn(s, move |c| {
                let found = match &session {
                    Some(id) => c.search_session(Some(id), &wanted.0),
                    None => c.prompts_everywhere(&wanted.0),
                };
                Done::Found(seq, found)
            });
            changed = true;
        }
    }
    if let Some(x) = session(s, &id).cloned() {
        let ids: Vec<String> = shown(s, &x).0.into_iter().map(|i| i.id).collect();
        if ids != s.complete.shown_ids {
            s.complete.shown_ids = ids;
            s.complete.highlighted = 0;
        }
    }
    changed
}

fn leave_history(s: &mut App, left: &str) {
    let Some(h) = s.complete.history.get_mut(left) else { return };
    let recalled = h.current().map(str::to_string);
    if let Some(draft) = h.leave()
        && recalled.as_deref() == Some(s.drafts.get(left))
    {
        s.drafts.set(left, &draft);
    }
}

pub fn refresh(s: &mut App, id: &str) {
    s.complete.seq += 1;
    let (seq, asked) = (s.complete.seq, id.to_string());
    s.complete.prompts_asked.insert(feed::composer::current(&s.rekeyed, id).to_string(), seq);
    spawn(s, move |c| Done::Prompts(seq, asked.clone(), c.prompts(Some(&asked), "")));
}

fn rebuild(s: &mut App, id: &str) {
    let texts = complete::sent_texts(s.complete.local.get(id).map(Vec::as_slice).unwrap_or_default(), s.complete.recorded.get(id).map(Vec::as_slice).unwrap_or_default());
    s.complete.history.entry(id.to_string()).or_default().replace(texts);
}

pub fn sending(s: &mut App, id: &str) {
    s.complete.dismissed = None;
    s.complete.history.entry(id.to_string()).or_default().stop();
}

pub fn delivered(s: &mut App, id: &str, text: &str) {
    if text.trim().is_empty() {
        return;
    }
    let local = s.complete.local.entry(id.to_string()).or_default();
    local.retain(|(t, _)| t != text);
    local.insert(0, (text.to_string(), time::OffsetDateTime::now_utc()));
    rebuild(s, id);
}

pub fn rekey(s: &mut App, old: &str, new: &str) {
    let c = &mut s.complete;
    if let Some(moving) = c.local.remove(old) {
        let local = c.local.entry(new.to_string()).or_default();
        local.extend(moving);
        local.sort_by(|a, b| b.1.cmp(&a.1));
    }
    if let Some(prompts) = c.recorded.remove(old) {
        c.recorded.entry(new.to_string()).or_insert(prompts);
    }
    if let Some(history) = c.history.remove(old) {
        c.history.insert(new.to_string(), history);
    }
    let prefix = format!("{old}/");
    let moving: Vec<String> = c.fork_keys.keys().filter(|k| k.starts_with(&prefix)).cloned().collect();
    for point in moving {
        if let Some(request) = c.fork_keys.remove(&point) {
            c.fork_keys.insert(format!("{new}/{}", &point[prefix.len()..]), request);
        }
    }
    if c.goal_pending.remove(old) {
        c.goal_pending.insert(new.to_string());
    }
    if let Some(seq) = c.prompts_asked.remove(old) {
        let newest = c.prompts_asked.entry(new.to_string()).or_insert(seq);
        *newest = (*newest).max(seq);
    }
    c.commands.remove(old);
    c.commands_asked.remove(old);
    if c.session.as_deref() == Some(old) {
        c.session = Some(new.to_string());
    }
    if let Some(search) = c.search.as_mut().filter(|q| q.session == old) {
        search.session = new.to_string();
    }
    rebuild(s, new);
}

pub fn done(s: &mut App, done: Done) {
    match done {
        Done::Commands(id, result) => {
            s.complete.commands.insert(id, result);
        }
        Done::Files(seq, id, query, result) => {
            if s.complete.files_asked.as_ref().is_some_and(|(asked, _, _)| *asked == seq) {
                s.complete.files = Some((id, query, result));
            }
        }
        Done::Prompts(seq, asked, result) => {
            let id = feed::composer::current(&s.rekeyed, &asked).to_string();
            if s.complete.prompts_asked.get(&id) != Some(&seq) {
                return;
            }
            match result {
                Ok(prompts) => {
                    s.complete.recorded.insert(id.clone(), prompts);
                    rebuild(s, &id);
                }
                Err(error) => crate::note(s, &id, format!("Couldn't load what you sent before: {error}")),
            }
        }
        Done::Found(seq, result) => {
            if let Some(search) = s.complete.search.as_mut().filter(|x| x.seq == seq) {
                search.ix = 0;
                match result {
                    Ok(found) => {
                        search.results = Some(Ok(if search.all { complete::distinct(found.prompts) } else { found.prompts }));
                        search.problems = found.problems;
                        search.more = found.more;
                    }
                    Err(error) => search.results = Some(Err(error)),
                }
            }
        }
        Done::Forked(from, point, prompt, result) => {
            s.complete.forking = false;
            if !result.as_ref().is_err_and(|e| e.maybe_delivered) {
                s.complete.fork_keys.remove(&point);
                if let Some((sess, item)) = point.split_once('/') {
                    let moved = format!("{}/{item}", feed::composer::current(&s.rekeyed, sess));
                    s.complete.fork_keys.remove(&moved);
                }
            }
            let from = feed::composer::current(&s.rekeyed, &from).to_string();
            match result {
                Ok(id) if !id.is_empty() => {
                    s.drafts.set(&id, &prompt);
                    if s.composer_for.as_deref() == Some(from.as_str()) {
                        crate::over::open_session(s, &id);
                    } else {
                        s.ov.notice = Some("The fork is ready, with your prompt in its composer. Find it in the sidebar.".into());
                    }
                }
                Ok(_) => s.ov.notice = Some("The fork started, but the daemon didn't say which session it made.".into()),
                Err(error) if error.maybe_delivered => crate::note(s, &from, format!("Fork sent, not confirmed: {}. Fork from here again checks the same request; it won't make a second copy.", error.message)),
                Err(error) => s.ov.notice = Some(format!("Couldn't fork: {}", error.message)),
            }
        }
        Done::Goal(asked, typed, result) => {
            let id = feed::composer::current(&s.rekeyed, &asked).to_string();
            s.complete.goal_pending.remove(&id);
            if result.as_ref().is_ok_and(|d| *d != feed::composer::Delivered::Unconfirmed) {
                if s.composer_for.as_deref() == Some(id.as_str()) {
                    if s.composer.text.trim() == typed {
                        replace_composer(s, "");
                    }
                } else if s.drafts.get(&id).trim() == typed {
                    s.drafts.set(&id, "");
                }
            }
            let line = match result {
                Ok(feed::composer::Delivered::Queued(_) | feed::composer::Delivered::QueuedUntilAnswer(_)) => "Goal queued; it's set when this turn ends".to_string(),
                Ok(feed::composer::Delivered::Unconfirmed) => "Goal sent, not confirmed yet".to_string(),
                Ok(_) => "Goal set".to_string(),
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
    if command == Slash::Steer && crate::sending(s, id) {
        crate::note(s, id, "Wait for the message being sent, then steer.".into());
        return true;
    }
    if s.client.is_none() {
        crate::note(s, id, "Fixture mode: not sent".into());
        return true;
    }
    if command == Slash::Goal {
        if !s.complete.goal_pending.insert(id.to_string()) {
            crate::note(s, id, "The goal is still being set.".into());
            return true;
        }
        let (asked, typed) = (id.to_string(), s.composer.text.trim().to_string());
        crate::note(s, id, "Setting the goal…".into());
        spawn(s, move |c| Done::Goal(asked.clone(), typed.clone(), c.goal_set(&asked, &argument)));
        return true;
    }
    let pending = match command {
        Slash::Model => s.menus.busy.contains(&(id.to_string(), "model")).then_some("The last model change is still on its way."),
        Slash::Compact => s.menus.busy.contains(&(id.to_string(), "compact")).then_some("Compacting is already on its way."),
        Slash::Stop => s.stopping.contains(id).then_some("Stopping is already on its way."),
        Slash::Name => (s.menus.rename_saving || super::session_menu::changing(s, id)).then_some("Another change to this session is still on its way."),
        Slash::Steer | Slash::Goal => None,
    };
    if let Some(wait) = pending {
        crate::note(s, id, wait.into());
        return true;
    }
    s.complete.dismissed = None;
    replace_composer(s, "");
    match command {
        Slash::Name => super::session_menu::rename_to(s, id, &argument),
        Slash::Compact => super::session_menu::compact(s, id),
        Slash::Stop => crate::stop(s, id),
        Slash::Steer => crate::steer_text(s, id, argument, tx),
        Slash::Model if argument.is_empty() && s.screen != crate::Screen::Session => crate::note(s, id, "Type a model after /model, or open the session to pick one.".into()),
        Slash::Model => pick_model(s, id, &argument),
        Slash::Goal => {}
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
        (None, true) => {
            let line = models_note(s, id);
            crate::note(s, id, line);
        }
    }
}

fn models_note(s: &App, id: &str) -> String {
    match s.menus.models_error.get(id) {
        Some(error) => format!("Couldn't load this session's models: {error}"),
        None => "This session's models are still loading.".into(),
    }
}

fn open_search(s: &mut App, id: &str) {
    let mut query = TextEdit::new("");
    query.focused = true;
    s.composer.focused = false;
    s.complete.search = Some(Search { session: id.to_string(), query, all: false, ix: 0, seq: 0, asked: None, results: None, problems: Vec::new(), more: false });
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

impl Search {
    fn current(&self) -> Option<&Result<Vec<Prompt>, String>> {
        let asked = self.asked.as_ref()?;
        (asked.0 == self.query.text.trim() && asked.1 == self.all).then_some(self.results.as_ref()).flatten()
    }
}

fn results(s: &App) -> &[Prompt] {
    match s.complete.search.as_ref().and_then(Search::current) {
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
    let point = format!("{from}/{}", prompt.item_id);
    let (key, sent_from) = s.complete.fork_keys.entry(point.clone()).or_insert_with(|| (feed::catalog::request_key(), from.to_string())).clone();
    let (from, turn, text) = (sent_from, prompt.turn_id.clone(), prompt.text.clone());
    spawn(s, move |c| {
        let result = c.fork(&from, turn.as_deref(), &key);
        Done::Forked(from.clone(), point.clone(), text.clone(), result)
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
    match search.current() {
        None => list = list.child(text("Searching…").font(META).color(faint()).px(10.0).py(4.0)),
        Some(Err(error)) => list = list.child(text(format!("Couldn't search what you sent: {error}")).font(META).color(failed()).px(10.0).py(4.0)),
        Some(Ok(found)) if found.is_empty() => {
            let empty = match (search.more, search.query.text.trim().is_empty()) {
                _ if !search.problems.is_empty() => "Nothing found where the search could look.",
                (true, _) => "Nothing among the newest prompts. Type more to narrow the search.",
                (false, true) => "Nothing sent yet.",
                (false, false) => "No sent prompt matches.",
            };
            list = list.child(text(empty).font(META).color(faint()).px(10.0).py(4.0));
        }
        Some(Ok(found)) => {
            let at = search.ix.min(found.len() - 1);
            for (i, p) in found.iter().enumerate().skip(at.saturating_sub(SHOWN - 1)).take(SHOWN) {
                let chosen = p.text.clone();
                let mut body = col().grow(1.0).min_w(0.0).gap(2.0).child(text(p.text.lines().map(str::trim).find(|l| !l.is_empty()).unwrap_or_default().to_string()).font(META).color(theme::text()).truncate());
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
                if forks && p.session_id == x.id && complete::forkable(p) {
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
    if search.more && search.current().is_some_and(|r| r.as_ref().is_ok_and(|l| !l.is_empty())) {
        list = list.child(text("Showing the newest matches. Type more to narrow the search.").font(META).color(faint()).px(10.0).py(2.0));
    }
    for problem in &search.problems {
        list = list.child(text(problem.clone()).font(META).color(failed()).px(10.0).py(2.0));
    }
    Some(col().key("prompt-search-panel").w_full().gap(6.0).px(12.0).pt(10.0).child(head).child(list))
}
