use std::collections::HashMap;
use std::sync::mpsc::{Receiver, Sender, channel};

use feed::catalog::{RespondError, Session};
use feed::inspect::{self, GoalAct, LifecycleAct, LifecycleResult, Metadata};
use rune::{El, ScrollSpec, TextEdit, Tx, col, el, icon, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog};
use time::OffsetDateTime;

use super::rail::rail_section;
use crate::icons;
use crate::theme::{self, BODY, META, META_BOLD, accent, attention, failed, faint, muted, wash};
use crate::{App, session};

pub enum Done {
    Goal(String, GoalAct, Result<bool, RespondError>),
    Life(String, u64, LifecycleAct, bool, Result<LifecycleResult, RespondError>),
    Status(String, u64, Result<LifecycleResult, RespondError>),
}

#[derive(Default)]
struct Process {
    generation: u64,
    status: Option<LifecycleResult>,
    busy: Option<LifecycleAct>,
    unchanged: bool,
    error: Option<String>,
}

pub struct Editing {
    pub session: String,
    pub act: GoalAct,
    pub text: TextEdit,
}

pub struct Inspect {
    meta: HashMap<String, Metadata>,
    process: HashMap<String, Process>,
    goal_busy: HashMap<String, GoalAct>,
    goal_note: HashMap<String, (bool, String)>,
    expanded: Option<String>,
    pub editing: Option<Editing>,
    pub logs: Option<(String, String)>,
    seq: u64,
    tx: Sender<Done>,
    rx: Receiver<Done>,
}

impl Inspect {
    pub fn new() -> Inspect {
        let (tx, rx) = channel();
        Inspect { meta: HashMap::new(), process: HashMap::new(), goal_busy: HashMap::new(), goal_note: HashMap::new(), expanded: None, editing: None, logs: None, seq: 0, tx, rx }
    }
}

fn spawn(s: &App, job: impl FnOnce(&feed::catalog::Client) -> Done + Send + 'static) -> bool {
    let Some(client) = s.client.clone() else { return false };
    let tx = s.inspect.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(job(&client));
    });
    true
}

pub fn load(s: &mut App, id: &str) {
    s.inspect.meta.retain(|k, _| k == id);
    s.inspect.process.retain(|k, _| k == id);
    if s.inspect.expanded.as_deref() != Some(id) {
        s.inspect.expanded = None;
    }
}

pub(crate) fn read(s: &mut App, id: &str, result: Result<Metadata, String>) {
    let old = s.inspect.meta.get(id);
    let data = match result {
        Ok(data) => data.after(old),
        Err(error) => Metadata::failed(old, error),
    };
    s.inspect.meta.insert(id.to_string(), data);
}

pub(crate) fn goal_moved(s: &mut App, id: &str) {
    if s.selected.as_deref() == Some(id) {
        super::session_menu::reread(s, id);
    }
}

fn goal_act(s: &mut App, id: &str, act: GoalAct, typed: &str) -> bool {
    if s.client.is_none() {
        s.ov.notice = Some("Fixture mode: not sent".into());
        return false;
    }
    if s.inspect.goal_busy.contains_key(id) || !session(s, id).is_some_and(|x| inspect::goal_controls(x).editable) {
        return false;
    }
    s.inspect.goal_busy.insert(id.to_string(), act);
    s.inspect.goal_note.remove(id);
    let (id, typed) = (id.to_string(), typed.to_string());
    spawn(s, move |c| Done::Goal(id.clone(), act, c.goal(&id, act, &typed)))
}

fn press_goal(s: &mut App, id: &str, act: GoalAct) {
    if !act.takes_text() {
        goal_act(s, id, act, "");
        return;
    }
    let goal = s.inspect.meta.get(id).and_then(|d| d.goal.clone());
    let start = match act {
        GoalAct::Edit => goal.map(|g| g.objective).unwrap_or_default(),
        GoalAct::Budget => goal.and_then(|g| g.token_budget).map(|b| b.to_string()).unwrap_or_default(),
        _ => String::new(),
    };
    let mut text = TextEdit::new(&start);
    text.focused = true;
    s.composer.focused = false;
    s.inspect.editing = Some(Editing { session: id.to_string(), act, text });
}

fn close_editor(s: &mut App) {
    s.inspect.editing = None;
}

fn budget_of(s: &App, id: &str) -> Option<i64> {
    s.inspect.meta.get(id).and_then(|d| d.goal.as_ref()).and_then(|g| g.token_budget)
}

fn save_editor(s: &mut App) {
    let Some(editing) = s.inspect.editing.as_ref() else { return };
    let (id, act, typed) = (editing.session.clone(), editing.act, editing.text.text.clone());
    if s.selected.as_deref() != Some(id.as_str()) || !inspect::can_save(act, &typed, budget_of(s, &id)) {
        if s.selected.as_deref() != Some(id.as_str()) {
            close_editor(s);
        }
        return;
    }
    goal_act(s, &id, act, &typed);
    close_editor(s);
}

pub(crate) fn run(s: &mut App, id: &str, act: LifecycleAct, tell: bool) {
    if s.client.is_none() {
        s.ov.notice = Some("Fixture mode: not sent".into());
        return;
    }
    if !session(s, id).is_some_and(|x| act.offered(inspect::lifecycle(x))) {
        return;
    }
    s.inspect.seq += 1;
    let fresh = s.inspect.seq;
    let p = s.inspect.process.entry(id.to_string()).or_insert_with(|| Process { generation: fresh, ..Process::default() });
    if p.busy.is_some() {
        return;
    }
    p.busy = Some(act);
    let generation = p.generation;
    let id = id.to_string();
    spawn(s, move |c| Done::Life(id.clone(), generation, act, tell, c.lifecycle(&id, act)));
}

fn failure(error: &RespondError) -> String {
    if error.maybe_delivered { format!("It may have gone through: {}", error.message) } else { error.message.clone() }
}

fn settled(s: &mut App, done: Done) {
    match done {
        Done::Goal(id, act, result) => {
            s.inspect.goal_busy.remove(&id);
            let note = match result {
                Ok(true) => None,
                Ok(false) => Some((false, format!("{} was sent, not confirmed yet.", act.doing()))),
                Err(error) => Some((true, format!("{} didn't work: {}", act.doing(), failure(&error)))),
            };
            if let Some(note) = note {
                s.inspect.goal_note.insert(id.clone(), note);
            }
            goal_moved(s, &id);
        }
        Done::Life(id, generation, act, tell, result) => {
            let Some(p) = s.inspect.process.get_mut(&id).filter(|p| p.generation == generation) else { return };
            p.busy = None;
            if tell {
                let line = match &result {
                    Ok(r) if act == LifecycleAct::Status => Some(r.line()).filter(|l| !l.is_empty()),
                    Ok(r) if act == LifecycleAct::Resume && r.unchanged == Some(true) => Some(inspect::RESUME_UNCHANGED.to_string()),
                    Err(error) => Some(format!("{} didn't work: {}", act.label(), failure(error))),
                    _ => None,
                };
                if let Some(line) = line {
                    s.ov.notice = Some(line);
                }
            }
            match result {
                Ok(result) => {
                    p.error = None;
                    p.unchanged = act == LifecycleAct::Resume && result.unchanged == Some(true);
                    match act {
                        LifecycleAct::Status => p.status = Some(result),
                        LifecycleAct::Logs => s.inspect.logs = Some((id.clone(), result.output.unwrap_or_default())),
                        _ if session(s, &id).is_some_and(|x| inspect::lifecycle(x).status) => {
                            if let Some(p) = s.inspect.process.get_mut(&id) {
                                p.busy = Some(LifecycleAct::Status);
                            }
                            spawn(s, move |c| Done::Status(id.clone(), generation, c.lifecycle(&id, LifecycleAct::Status)));
                        }
                        _ => {}
                    }
                }
                Err(error) => p.error = Some(failure(&error)),
            }
        }
        Done::Status(id, generation, result) => {
            let Some(p) = s.inspect.process.get_mut(&id).filter(|p| p.generation == generation) else { return };
            p.busy = None;
            if let Ok(result) = result {
                p.status = Some(result);
            }
        }
    }
}

pub fn drain(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok(done) = s.inspect.rx.try_recv() {
        changed = true;
        settled(s, done);
    }
    if let Some(editing) = &s.inspect.editing
        && (s.selected.as_deref() != Some(editing.session.as_str()) || !session(s, &editing.session).is_some_and(|x| inspect::goal_controls(x).editable))
    {
        close_editor(s);
        changed = true;
    }
    changed
}

fn small(label: &str, key: String, variant: ButtonVariant, disabled: bool, on: impl Fn(&mut App) + 'static) -> El {
    Button::new(label).variant(variant).control_size(ControlSize::Small).disabled(disabled).on_press(on).build(theme::kit()).key(key)
}

fn goal_section(s: &App, x: &Session) -> Option<El> {
    let can = inspect::goal_controls(x);
    let data = s.inspect.meta.get(&x.id);
    let goal = data.and_then(|d| d.goal.as_ref());
    if goal.is_none() && (!can.editable || data.is_none_or(|d| d.goal_failed)) {
        return None;
    }
    let busy = s.inspect.goal_busy.contains_key(&x.id);
    let buttons = row().w_full().flex_wrap().gap(6.0).children(inspect::goal_buttons(goal, can).into_iter().map(|(act, label)| {
        let id = x.id.clone();
        let variant = if act == GoalAct::Clear { ButtonVariant::Ghost } else { ButtonVariant::Neutral };
        small(label, format!("goal-{}", act.action()), variant, busy, move |s: &mut App| press_goal(s, &id, act))
    }));
    let mut body = col().w_full().gap(8.0);
    if let Some(goal) = goal {
        let tint = if goal.needs_attention() { attention() } else { muted() };
        let status = row().gap(5.0).items_center().px(7.0).py(2.0).radius(9.0).fill(wash()).child(icon(&icons::TARGET, 11.0).color(tint)).child(text(goal.display_status()).font(META).color(tint));
        let long = goal.objective.len() > 240 || goal.objective.lines().count() > 4;
        let expanded = s.inspect.expanded.as_deref() == Some(x.id.as_str());
        let objective = text(goal.objective.clone()).font(BODY).color(theme::text()).wrap().w_full();
        body = body.child(row().w_full().child(el().grow(1.0)).child(status)).child(if expanded || !long { objective } else { objective.max_lines(4) });
        if long {
            let id = x.id.clone();
            body = body.child(
                text(if expanded { "Less" } else { "More" })
                    .font(META)
                    .color(accent())
                    .key("goal-more")
                    .help(if expanded { "Show less of the goal" } else { "Show the whole goal" })
                    .on_press(move |s: &mut App| s.inspect.expanded = if s.inspect.expanded.as_deref() == Some(id.as_str()) { None } else { Some(id.clone()) }),
            );
        }
        let now = OffsetDateTime::now_utc();
        let clock = super::background::clock();
        let times = goal.times(now, clock, local_offset);
        for line in [goal.reason().map(str::to_string), Some(goal.usage()), Some(times)].into_iter().flatten().filter(|l| !l.is_empty()) {
            body = body.child(text(line).font(META).color(muted()).wrap());
        }
    }
    body = body.child(buttons);
    if let Some((bad, note)) = s.inspect.goal_note.get(&x.id) {
        body = body.child(text(note.clone()).font(META).color(if *bad { failed() } else { muted() }).wrap());
    }
    Some(rail_section("Goal", &icons::TARGET, body))
}

fn local_offset(at: OffsetDateTime) -> i32 {
    super::session_ops::offset(at).whole_seconds()
}

fn process_section(s: &App, x: &Session) -> Option<El> {
    let lifecycle = inspect::lifecycle(x);
    if !inspect::lifecycle_any(lifecycle) {
        return None;
    }
    let p = s.inspect.process.get(&x.id);
    let busy = p.and_then(|p| p.busy).is_some();
    let mut body = col().w_full().gap(8.0);
    match p.and_then(|p| p.status.as_ref()) {
        Some(status) => {
            body = body.child(text(status.line()).font(META).color(theme::text()).wrap());
            if p.is_some_and(|p| p.unchanged) {
                body = body.child(text(inspect::RESUME_UNCHANGED).font(META).color(muted()));
            }
        }
        None => body = body.child(text(inspect::lifecycle_detail(x)).font(META).color(muted()).wrap()),
    }
    let buttons = row().w_full().flex_wrap().gap(6.0).items_center().children(LifecycleAct::ALL.into_iter().filter(|a| a.offered(lifecycle)).map(|act| {
        let id = x.id.clone();
        small(act.label(), format!("process-{}", act.name()), ButtonVariant::Neutral, busy, move |s: &mut App| run(s, &id, act, false))
    }));
    body = body.child(if busy { buttons.child(text("Working…").font(META).color(faint())) } else { buttons });
    if let Some(error) = p.and_then(|p| p.error.clone()) {
        body = body.child(text(error).font(META).color(failed()).wrap());
    }
    Some(rail_section(inspect::lifecycle_title(x), &icons::TERMINAL, body))
}

fn runs_section(s: &App, x: &Session) -> Vec<El> {
    let Some(data) = s.inspect.meta.get(&x.id) else { return Vec::new() };
    let mut out = Vec::new();
    if let Some(runs) = &data.runs {
        let now = OffsetDateTime::now_utc();
        let clock = super::background::clock();
        let mut body = col().w_full().gap(8.0);
        if runs.is_empty() {
            body = body.child(text(inspect::NO_RUNS).font(META).color(muted()));
        }
        for run in runs {
            let facts = run.lines(now, clock, local_offset);
            let mut entry = col().w_full().gap(2.0).child(text(format!("Job: {}", run.job_id)).font(META_BOLD).color(theme::text()).wrap_anywhere());
            if !facts.is_empty() {
                entry = entry.child(text(facts.join(" · ")).font(META).color(muted()).wrap_anywhere());
            }
            body = body.child(entry);
        }
        out.push(rail_section("Claude runs", &icons::ACTIVITY, body));
    }
    if !data.warnings.is_empty() {
        out.push(rail_section("Not available right now", &icons::CLOCK, col().w_full().gap(4.0).children(data.warnings.iter().map(|w| text(w.clone()).font(META).color(muted()).wrap_anywhere()))));
    }
    out
}

pub(crate) fn above(s: &App, x: &Session) -> Vec<El> {
    goal_section(s, x).into_iter().chain(process_section(s, x)).collect()
}

pub(crate) fn below(s: &App, x: &Session) -> Vec<El> {
    runs_section(s, x)
}

fn editor(s: &App, editing: &Editing, layer: &mut Vec<El>) {
    let k = theme::kit();
    let budget = editing.act == GoalAct::Budget;
    let ok = inspect::can_save(editing.act, &editing.text.text, budget_of(s, &editing.session));
    let mut input = text_input(&editing.text, BODY).key("goal-editor-input").grow(1.0).min_w(0.0).color(theme::text()).on_edit(|s: &mut App| &mut s.inspect.editing.as_mut().expect("goal editor is open").text);
    input = if budget { input.single_line().label("Tokens").placeholder("Tokens", faint()).on_submit(|s: &mut App, _: &mut Tx| save_editor(s)) } else { input.label("Goal").min_h(100.0) };
    let field = row().w_full().px(10.0).py(7.0).radius(8.0).fill(wash()).border(1.0, theme::line()).child(input);
    let mut body = col().w_full().gap(10.0).px(16.0).pb(16.0).child(field);
    if budget {
        body = body.child(text("Leave empty to remove the budget.").font(META).color(muted()));
    }
    let buttons = row()
        .w_full()
        .gap(8.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(close_editor).build(k).key("goal-editor-cancel"))
        .child(Button::new("Save").variant(ButtonVariant::Contrast).control_size(ControlSize::Small).disabled(!ok).on_press(save_editor).build(k).key("goal-editor-save"));
    Dialog::new("goal-editor", editing.act.title()).open(true).fit().width(400.0).on_close(close_editor).child(body.child(buttons)).build(k, layer);
}

fn logs(s: &App, id: &str, output: &str, layer: &mut Vec<El>) {
    let k = theme::kit();
    let title = session(s, id).map(|x| x.title()).unwrap_or_default();
    let shown = if output.is_empty() { inspect::NO_LOGS.to_string() } else { output.to_string() };
    let copy = output.to_string();
    let body = col()
        .w_full()
        .gap(10.0)
        .px(16.0)
        .pb(16.0)
        .child(col().key("logs-scroll").w_full().h(300.0).px(10.0).py(8.0).radius(8.0).fill(wash()).border(1.0, theme::line()).scroll(ScrollSpec { stick_bottom: true }).child(text(shown).font(META.mono()).color(theme::text()).wrap_anywhere().w_full()))
        .child(
            row()
                .w_full()
                .gap(8.0)
                .child(Button::new("Copy").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press_tx(move |_: &mut App, tx: &mut Tx| tx.copy(copy.clone())).build(k).key("logs-copy"))
                .child(el().grow(1.0))
                .child(Button::new("Done").variant(ButtonVariant::Contrast).control_size(ControlSize::Small).on_press(|s: &mut App| s.inspect.logs = None).build(k).key("logs-done")),
        );
    Dialog::new("session-logs", format!("Logs for {title}")).open(true).fit().width(560.0).on_close(|s: &mut App| s.inspect.logs = None).child(body).build(k, layer);
}

pub fn overlays(s: &App, layer: &mut Vec<El>) {
    if let Some(editing) = &s.inspect.editing {
        editor(s, editing, layer);
    }
    if let Some((id, output)) = &s.inspect.logs {
        logs(s, id, output, layer);
    }
}
