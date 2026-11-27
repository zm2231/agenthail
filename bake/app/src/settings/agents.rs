use std::collections::HashMap;

use feed::agents::{self, ActionView, Assignment, Change, Decision, Draft, Kind, Loaded, Request, RoleView, Roles};
use feed::prefs::Failure;
use rune::{El, TextEdit, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, Kit, SegmentedControl, Select, SelectAppearance, Switch, TextArea, TextInput};

use super::parts::{self, line, section};
use super::{Done, Prefs};
use crate::theme::{self, BODY, META, accent, failed, faint, muted};

pub const TAB: usize = 5;

pub enum Got {
    Read(u64, Result<Loaded, Failure>),
    Sessions(u64, Vec<(String, String)>),
    Wrote(After, Result<Roles, Change>),
}

pub enum After {
    Saved(String),
    Removed(String),
    Created(String),
    Assigned,
    Unassigned,
}

#[derive(Default)]
pub struct Form {
    read: Option<Result<Loaded, Failure>>,
    read_only: Option<String>,
    sessions: Vec<(String, String)>,
    seq: u64,
    sessions_seq: u64,
    page: usize,
    selected: Option<String>,
    draft: Option<Draft>,
    kept: HashMap<String, Draft>,
    description: TextEdit,
    creating: Option<TextEdit>,
    removing: bool,
    save_error: Option<Failure>,
    busy: bool,
    open: Option<String>,
    kind: usize,
    session: String,
    typed: TextEdit,
    role: String,
}

fn roles(s: &Prefs) -> Option<&Roles> {
    s.agents.read.as_ref()?.as_ref().ok().map(|l| &l.roles)
}

fn editable(s: &Prefs) -> bool {
    roles(s).is_some() && s.agents.read_only.is_none() && !s.agents.busy
}

fn kind(s: &Prefs) -> Kind {
    Kind::ALL[s.agents.kind.min(Kind::ALL.len() - 1)]
}

pub fn escape(s: &mut Prefs) -> bool {
    s.agents.open.take().is_some() || s.agents.creating.take().is_some() || std::mem::take(&mut s.agents.removing)
}

pub fn opened(s: &mut Prefs) {
    if s.tab == TAB {
        reload(s);
    }
}

pub fn reload(s: &mut Prefs) {
    if s.tab == TAB {
        read(s);
    }
}

fn read(s: &mut Prefs) {
    let Some(client) = s.client.clone() else {
        s.agents.read = Some(Err(super::unread(s)));
        return;
    };
    s.agents.seq += 1;
    s.agents.sessions_seq += 1;
    let (seq, sessions_seq) = (s.agents.seq, s.agents.sessions_seq);
    s.in_flight += 2;
    let (tx, more) = (s.tx.clone(), s.tx.clone());
    let other = client.clone();
    std::thread::spawn(move || {
        let _ = tx.send(Done::Agents(Got::Read(seq, client.agent_roles())));
    });
    std::thread::spawn(move || {
        let list = other.snapshot().map(|snap| session_choices(&snap.sessions)).unwrap_or_default();
        let _ = more.send(Done::Agents(Got::Sessions(sessions_seq, list)));
    });
}

fn session_choices(sessions: &[feed::catalog::Session]) -> Vec<(String, String)> {
    let mut out: Vec<(String, String)> = sessions.iter().filter(|x| !x.archived).map(|x| (x.id.clone(), x.alias.as_ref().filter(|a| !a.is_empty()).map_or_else(|| x.name.clone(), |a| format!("@{a}")))).collect();
    out.sort_by_key(|(_, label)| label.to_lowercase());
    out
}

pub fn take(s: &mut Prefs, got: Got) {
    match got {
        Got::Read(seq, result) => {
            if seq != s.agents.seq {
                return;
            }
            if let Ok(l) = &result {
                s.agents.read_only.clone_from(&l.read_only);
            }
            s.agents.read = Some(result);
            settle(s);
        }
        Got::Sessions(seq, list) => {
            if seq == s.agents.sessions_seq {
                s.agents.sessions = list;
            }
        }
        Got::Wrote(after, result) => {
            s.agents.busy = false;
            match result {
                Ok(updated) => {
                    s.agents.seq += 1;
                    let read_only = s.agents.read_only.clone();
                    s.agents.read = Some(Ok(Loaded { roles: updated, read_only }));
                    match after {
                        After::Saved(name) | After::Removed(name) => {
                            s.agents.kept.remove(&name);
                            let shown = s.agents.selected.as_deref() == Some(name.as_str());
                            if let Some(role) = roles(s).and_then(|r| r.role(&name)).cloned().filter(|_| shown) {
                                set_draft(s, Draft::of(&role));
                            }
                        }
                        After::Created(name) => {
                            s.agents.creating = None;
                            select(s, name);
                        }
                        After::Assigned => {
                            s.agents.typed = TextEdit::new("");
                            s.agents.session.clear();
                        }
                        After::Unassigned => {}
                    }
                    settle(s);
                }
                Err(Change::ReadOnly) => {
                    s.agents.read_only = Some(agents::READ_ONLY.into());
                    s.agents.save_error = Some(Failure::new(agents::READ_ONLY));
                }
                Err(Change::Failed(f)) => {
                    let unsure = f.uncertain;
                    s.agents.save_error = Some(f);
                    if unsure {
                        read(s);
                    }
                }
            }
        }
    }
}

fn settle(s: &mut Prefs) {
    let Some(r) = roles(s) else { return };
    let names: Vec<String> = r.roles.iter().map(|x| x.name.clone()).collect();
    if s.agents.selected.as_ref().is_none_or(|n| !names.contains(n)) {
        if let Some(d) = s.agents.draft.take().filter(Draft::changed) {
            s.agents.kept.insert(d.name.clone(), d);
        }
        s.agents.selected = names.first().cloned();
    }
    let Some(role) = s.agents.selected.as_ref().and_then(|n| roles(s)?.role(n)).cloned() else { return };
    match &mut s.agents.draft {
        Some(d) if d.name == role.name => {
            let before = d.definition.description.clone();
            d.follow(&role);
            if d.definition.description != before {
                s.agents.description = TextEdit::new(d.definition.description.as_deref().unwrap_or(""));
            }
        }
        _ => {
            let mut d = s.agents.kept.remove(&role.name).unwrap_or_else(|| Draft::of(&role));
            d.follow(&role);
            set_draft(s, d);
        }
    }
}

fn select(s: &mut Prefs, name: String) {
    if s.agents.selected.as_deref() == Some(name.as_str()) {
        return;
    }
    if let Some(d) = s.agents.draft.take().filter(Draft::changed) {
        s.agents.kept.insert(d.name.clone(), d);
    }
    s.agents.selected = Some(name);
    s.agents.save_error = None;
    s.agents.removing = false;
    settle(s);
}

fn set_draft(s: &mut Prefs, d: Draft) {
    s.agents.description = TextEdit::new(d.definition.description.as_deref().unwrap_or(""));
    s.agents.draft = Some(d);
}

fn send(s: &mut Prefs, request: Request, after: After) {
    write(s, after, move |c| c.change_agent_role(&request));
}

fn write(s: &mut Prefs, after: After, job: impl FnOnce(&feed::catalog::Client) -> Result<Roles, Change> + Send + 'static) {
    if !editable(s) {
        return;
    }
    let Some(client) = s.client.clone() else { return };
    s.agents.busy = true;
    s.agents.save_error = None;
    s.in_flight += 1;
    let tx = s.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(Done::Agents(Got::Wrote(after, job(&client))));
    });
}

fn edit(s: &mut Prefs, f: impl FnOnce(&mut Draft)) {
    if let Some(d) = &mut s.agents.draft {
        f(d);
    }
}

fn toggle(s: &mut Prefs, key: &str) {
    s.agents.open = if s.agents.open.as_deref() == Some(key) { None } else { Some(key.to_string()) };
}

fn picker(s: &Prefs, key: String, label: &str, choices: Vec<String>, current: Option<&str>, width: f32, disabled: bool, layer: &mut Vec<El>, choose: impl Fn(&mut Prefs, usize) + Clone + 'static, k: &Kit) -> El {
    let open = s.agents.open.as_deref() == Some(key.as_str());
    let values = choices.clone();
    let toggled = key.clone();
    Select::new(key.clone(), &choices)
        .appearance(SelectAppearance::Field)
        .width(width)
        .current(current)
        .placeholder("Choose…")
        .label(label.to_string())
        .disabled(disabled)
        .open(open)
        .on_toggle(move |s: &mut Prefs| toggle(s, &toggled))
        .on_close(|s: &mut Prefs| s.agents.open = None)
        .on_choose(move |s: &mut Prefs, picked: &str, _: &mut Tx| {
            s.agents.open = None;
            if let Some(i) = values.iter().position(|v| v == picked) {
                choose(s, i);
            }
        })
        .build(k, layer)
        .key(format!("settings-{key}"))
}

fn small(label: &str, key: &str, disabled: bool, press: impl Fn(&mut Prefs) + 'static, k: &Kit) -> El {
    Button::new(label).control_size(ControlSize::Small).disabled(disabled).on_press(press).build(k).key(format!("settings-{key}"))
}

fn notices(s: &Prefs) -> Vec<El> {
    let mut out = Vec::new();
    if let Some(reason) = &s.agents.read_only {
        out.push(text(reason.clone()).font(META).color(muted()).wrap().px(4.0).key("agent-roles-read-only"));
    }
    if let Some(Err(f)) = &s.agents.read {
        out.push(parts::failure(s, f));
    }
    out
}

fn role_list(s: &Prefs, r: &Roles) -> El {
    let mut list = col().w(170.0).radius(10.0).fill(theme::card()).border(1.0, theme::line()).py(4.0);
    for role in &r.roles {
        let chosen = s.agents.selected.as_deref() == Some(role.name.as_str());
        let name = role.name.clone();
        let kind = if s.agents.kept.contains_key(&role.name) { format!("{} · not saved", agents::role_kind(role)) } else { agents::role_kind(role).to_string() };
        let note = text(kind).font(META).color(if role.edited { accent() } else { muted() });
        let mut item = col().w_full().gap(2.0).px(12.0).py(7.0).radius(7.0).child(text(role.name.clone()).font(BODY).color(theme::text())).child(note);
        if chosen {
            item = item.fill(theme::wash());
        }
        list = list.child(
            item.role("button")
                .label(role.name.clone())
                .on_press(move |s: &mut Prefs| select(s, name.clone()))
                .key(format!("agent-role-{}", role.name)),
        );
    }
    list
}

fn decision_row(s: &Prefs, d: &Draft, role: &RoleView, a: &ActionView, locked: bool, layer: &mut Vec<El>, k: &Kit) -> El {
    let chosen = d.decision(&a.name);
    let mut choices = vec![agents::default_title(a, role, &d.definition.reach)];
    choices.extend(Decision::ALL.iter().map(|x| x.title().to_string()));
    let current = chosen.map_or(choices[0].clone(), |x| x.title().to_string());
    let name = a.name.clone();
    let control = picker(s, format!("agent-decision-{}", a.name), &a.detail, choices, Some(&current), 150.0, locked, layer, move |s, i| edit(s, |d| d.set_decision(&name, i.checked_sub(1).map(|i| Decision::ALL[i]))), k);
    let dot = el().w(6.0).h(6.0).radius(3.0).fill(if chosen.is_some() { accent() } else { theme::line() });
    let words = col().gap(1.0).grow(1.0).min_w(0.0).child(text(a.detail.clone()).font(BODY).color(theme::text()).wrap()).child(text(a.name.clone()).font(META).color(faint()));
    row().w_full().gap(10.0).items_center().px(14.0).py(8.0).child(dot).child(words).child(control)
}

fn limit_row(label: &str, key: &str, value: i64, fallback: i64, range: std::ops::RangeInclusive<i64>, locked: bool, set: fn(&mut Draft, i64), k: &Kit) -> El {
    let shown = text(agents::limit_label(value, fallback)).font(BODY).color(muted());
    let less = small("−", &format!("{key}-less"), locked || value <= *range.start(), move |s| edit(s, |d| set(d, value - 1)), k);
    let more = small("+", &format!("{key}-more"), locked || value >= *range.end(), move |s| edit(s, |d| set(d, value + 1)), k);
    line(label, None, row().gap(8.0).items_center().child(shown).child(less).child(more))
}

fn detail(s: &Prefs, r: &Roles, role: &RoleView, d: &Draft, layer: &mut Vec<El>, k: &Kit) -> El {
    let locked = !editable(s);
    let description = TextArea::new(&s.agents.description)
        .label("Description")
        .placeholder("What this role is for")
        .width(None)
        .auto_grow(1, 4)
        .disabled(locked)
        .on_edit(|s: &mut Prefs| &mut s.agents.description)
        .on_change(|s: &mut Prefs, _: &mut Tx| {
            let t = s.agents.description.text.clone();
            edit(s, |d| d.definition.description = Some(t).filter(|t| !t.is_empty()));
        })
        .build(k)
        .key("settings-agent-role-description");
    let reaches: Vec<String> = agents::REACHES.iter().map(|(_, t)| t.to_string()).collect();
    let reach = picker(s, "agent-role-reach".into(), "Reach", reaches, Some(agents::reach_title(&d.definition.reach)), 200.0, locked, layer, |s, i| edit(s, |d| d.definition.reach = agents::REACHES[i].0.into()), k);
    let mut out = col().w_full().gap(18.0);
    out = out.child(section(&role.name, vec![row().w_full().px(14.0).py(10.0).child(description.grow(1.0)), line("Reach", None, reach)], None));
    let actions = r.actions.iter().map(|a| decision_row(s, d, role, a, locked, layer, k)).collect();
    out = out.child(section("Actions", actions, None));
    out = out.child(section(
        "Limits",
        vec![
            limit_row("Spawn depth", "agent-role-depth", d.depth(), agents::DEFAULT_DEPTH, agents::DEPTH_RANGE, locked, Draft::set_depth, k),
            limit_row("Running sessions", "agent-role-running", d.running(), agents::DEFAULT_RUNNING, agents::RUNNING_RANGE, locked, Draft::set_running, k),
        ],
        None,
    ));
    let seen = role.tools.clone().unwrap_or_else(|| r.tools.clone());
    let on = d.default_tools();
    let mut tools = vec![line("Default tools", None, parts::switch("agent-role-default-tools", "Default tools", on, locked, move |s| edit(s, |d| d.set_default_tools(!on, &seen)), k))];
    if on {
        tools.push(parts::words(agents::DEFAULT_TOOLS_HINT));
    } else {
        for tool in &r.tools {
            let sees = d.sees(tool);
            let (name, every) = (tool.clone(), r.tools.clone());
            let switch = Switch::new(sees).label(tool.clone()).hide_label().disabled(locked).on_toggle(move |s: &mut Prefs| edit(s, |d| d.set_tool(&name, !sees, &every))).build(k).key(format!("settings-agent-tool-{tool}"));
            tools.push(row().w_full().gap(16.0).items_center().px(14.0).py(6.0).child(text(tool.clone()).font(META).color(theme::text()).grow(1.0)).child(switch));
        }
    }
    out = out.child(section("Agenthail tools", tools, None));
    out.child(footer(s, role, d, locked, k))
}

fn footer(s: &Prefs, role: &RoleView, d: &Draft, locked: bool, k: &Kit) -> El {
    let mut out = col().w_full().gap(8.0);
    if d.conflicts(role) {
        out = out.child(text(format!("{} changed elsewhere while you edited it. Save replaces that change; Revert shows it.", role.name)).font(META).color(muted()).wrap().key("agent-role-conflict"));
    }
    if let Some(f) = &s.agents.save_error {
        out = out.child(parts::failure(s, f).key("agent-role-error"));
    }
    let changed = d.changed();
    let remove = if role.built_in {
        small("Reset", "agent-role-reset", locked || !role.edited, |s| s.agents.removing = true, k)
    } else {
        small("Delete", "agent-role-delete", locked, |s| s.agents.removing = true, k)
    };
    let save = Button::new("Save")
        .control_size(ControlSize::Small)
        .disabled(locked || !changed)
        .on_press(|s: &mut Prefs| {
            if let Some(d) = s.agents.draft.clone() {
                send(s, d.request(), After::Saved(d.name));
            }
        })
        .build(k)
        .key("settings-agent-role-save");
    let buttons = row()
        .w_full()
        .gap(8.0)
        .items_center()
        .child(el().grow(1.0))
        .child(small("New from This Role…", "agent-role-new", locked, |s| s.agents.creating = Some(TextEdit::new("")), k))
        .child(remove)
        .child(small("Revert", "agent-role-revert", !changed, |s| {
            s.agents.save_error = None;
            s.agents.draft = None;
            if let Some(name) = s.agents.selected.clone() {
                s.agents.kept.remove(&name);
            }
            settle(s);
        }, k))
        .child(save);
    out.child(buttons)
}

fn remove_dialog(s: &Prefs, role: &RoleView, layer: &mut Vec<El>, k: &Kit) {
    if !s.agents.removing {
        return;
    }
    let (title, label, about) = if role.built_in {
        (format!("Reset {} to how Agenthail ships it?", role.name), "Reset", "Its actions, tools and limits go back to their defaults.")
    } else {
        (format!("Delete {}?", role.name), "Delete", "Sessions assigned this role go back to their usual role.")
    };
    let name = role.name.clone();
    let close = |s: &mut Prefs| s.agents.removing = false;
    let buttons = row()
        .w_full()
        .gap(8.0)
        .px(16.0)
        .py(14.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(close).build(k).key("agent-role-remove-cancel"))
        .child(
            Button::new(label)
                .variant(ButtonVariant::Danger)
                .control_size(ControlSize::Small)
                .disabled(!editable(s))
                .on_press(move |s: &mut Prefs| {
                    s.agents.removing = false;
                    send(s, Request::Delete(name.clone()), After::Removed(name.clone()));
                })
                .build(k)
                .key("agent-role-remove-confirm"),
        );
    Dialog::new("agent-role-remove", title).description(about).open(true).fit().width(420.0).on_close(close).child(buttons).build(k, layer);
}

fn create(s: &mut Prefs) {
    let Some(name) = s.agents.creating.as_ref().map(|e| e.text.trim().to_string()).filter(|n| !n.is_empty()) else { return };
    let taken: Vec<&str> = roles(s).map(|r| r.roles.iter().map(|x| x.name.as_str()).collect()).unwrap_or_default();
    if agents::name_hint(&name, &taken).is_some() {
        return;
    }
    let Some(from) = s.agents.selected.as_ref().and_then(|n| roles(s)?.role(n)).map(|r| r.definition.clone()) else { return };
    let named = name.clone();
    write(s, After::Created(name), move |c| c.create_agent_role(&named, &from));
}

fn new_dialog(s: &Prefs, r: &Roles, from: &RoleView, layer: &mut Vec<El>, k: &Kit) {
    let Some(edit) = &s.agents.creating else { return };
    let taken: Vec<&str> = r.roles.iter().map(|x| x.name.as_str()).collect();
    let mut body = col().w_full().gap(8.0).px(16.0).pt(4.0);
    body = body.child(
        TextInput::new(edit)
            .label("Name")
            .placeholder("reviewer")
            .width(None)
            .on_edit(|s: &mut Prefs| s.agents.creating.as_mut().expect("the new role sheet is open"))
            .on_submit(|s: &mut Prefs, _: &mut Tx| create(s))
            .build(k)
            .key("agent-role-new-name"),
    );
    if let Some(hint) = agents::name_hint(&edit.text, &taken) {
        body = body.child(text(hint).font(META).color(muted()).wrap());
    }
    if let Some(f) = &s.agents.save_error {
        body = body.child(parts::failure(s, f));
    }
    let close = |s: &mut Prefs| {
        s.agents.creating = None;
        s.agents.save_error = None;
    };
    let blocked = edit.text.trim().is_empty() || agents::name_hint(&edit.text, &taken).is_some();
    let buttons = row()
        .w_full()
        .gap(8.0)
        .px(16.0)
        .py(14.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(close).build(k).key("agent-role-new-cancel"))
        .child(Button::new("Create").control_size(ControlSize::Small).disabled(blocked || !editable(s)).on_press(create).build(k).key("agent-role-create"));
    Dialog::new("agent-role-new", format!("New role from {}", from.name)).open(true).fit().width(360.0).on_close(close).child(body).child(buttons).build(k, layer);
}

fn roles_page(s: &Prefs, r: &Roles, layer: &mut Vec<El>, k: &Kit) -> El {
    let chosen = s.agents.selected.as_ref().and_then(|n| r.role(n));
    let right = match (chosen, &s.agents.draft) {
        (Some(role), Some(d)) if d.name == role.name => {
            remove_dialog(s, role, layer, k);
            new_dialog(s, r, role, layer, k);
            detail(s, r, role, d, layer, k)
        }
        _ => col().w_full().py(20.0).items_center().child(text("Choose a role.").font(BODY).color(muted())),
    };
    row().w_full().gap(16.0).items_start().child(role_list(s, r)).child(col().grow(1.0).min_w(0.0).child(right))
}

fn session_name(s: &Prefs, id: &str) -> Option<String> {
    s.agents.sessions.iter().find(|(x, _)| x == id).map(|(_, label)| label.clone())
}

fn assignment_row(s: &Prefs, a: &Assignment, locked: bool, k: &Kit) -> El {
    let mut words = col().gap(2.0).grow(1.0).min_w(0.0).child(text(agents::assignment_line(a, |id| session_name(s, id))).font(BODY).color(theme::text())).child(text(agents::assignment_kind(a)).font(META).color(muted()));
    if let Some(stale) = a.stale.as_deref().filter(|x| !x.is_empty()) {
        words = words.child(text(format!("Applies to no session: {stale}")).font(META).color(failed()).wrap());
    }
    let gone = a.clone();
    let unassign = small("Unassign", &format!("agent-unassign-{}", a.target), locked, move |s| send(s, Request::Unassign(gone.clone()), After::Unassigned), k);
    row().w_full().gap(12.0).items_center().px(14.0).py(10.0).child(words).child(text(a.role.clone()).font(BODY).color(muted())).child(unassign).key(format!("agent-assignment-{}", a.target))
}

fn target(s: &Prefs) -> String {
    let typed = s.agents.typed.text.trim().to_string();
    if kind(s) == Kind::Session && typed.is_empty() { s.agents.session.clone() } else { typed }
}

fn assign(s: &mut Prefs) {
    let (role, target) = (s.agents.role.clone(), target(s));
    if role.is_empty() || target.is_empty() {
        return;
    }
    send(s, Request::Assign { role, kind: kind(s), target }, After::Assigned);
}

fn assignments_page(s: &Prefs, r: &Roles, layer: &mut Vec<El>, k: &Kit) -> El {
    let locked = !editable(s);
    let mut assigned: Vec<El> = r.assignments.iter().map(|a| assignment_row(s, a, locked, k)).collect();
    if assigned.is_empty() {
        assigned.push(parts::words("No session, identity or alias has an assigned role. Each acts under its usual role."));
    }
    let kinds: Vec<&str> = Kind::ALL.iter().map(|x| x.title()).collect();
    let picker_kind = SegmentedControl::new(&kinds, Some(s.agents.kind)).label("Kind").fit().on_pick(|s: &mut Prefs, i, _| {
        if s.agents.kind != i {
            s.agents.kind = i;
            s.agents.typed = TextEdit::new("");
        }
    });
    let mut rows = vec![line("Kind", None, picker_kind.build(k).key("settings-agent-assign-kind"))];
    if kind(s) == Kind::Session {
        let labels: Vec<String> = s.agents.sessions.iter().map(|(_, l)| l.clone()).collect();
        let current = session_name(s, &s.agents.session);
        rows.push(line("Session", None, picker(s, "agent-assign-session".into(), "Session", labels, current.as_deref(), 220.0, locked, layer, |s, i| {
            if let Some((id, _)) = s.agents.sessions.get(i) {
                s.agents.session = id.clone();
            }
        }, k)));
    }
    let (label, hint) = kind(s).field();
    let typed = TextInput::new(&s.agents.typed).label(label).placeholder(hint).width(Some(220.0)).disabled(locked).on_edit(|s: &mut Prefs| &mut s.agents.typed).build(k).key("settings-agent-assign-target");
    rows.push(line(label, None, typed));
    let names: Vec<String> = r.roles.iter().map(|x| x.name.clone()).collect();
    let current = (!s.agents.role.is_empty()).then_some(s.agents.role.as_str());
    rows.push(line("Role", None, picker(s, "agent-assign-role".into(), "Role", names.clone(), current, 220.0, locked, layer, move |s, i| s.agents.role = names[i].clone(), k)));
    let status = match &s.agents.save_error {
        Some(f) => parts::failure(s, f),
        None => text(""),
    };
    let ready = !target(s).is_empty() && !s.agents.role.is_empty();
    rows.push(row().w_full().gap(12.0).items_center().px(14.0).py(10.0).child(col().grow(1.0).min_w(0.0).child(status)).child(small("Assign", "agent-assign", locked || !ready, assign, k)));
    col().w_full().gap(22.0).child(section("Assigned", assigned, None)).child(section("Assign a role", rows, Some(kind(s).footer().into())))
}

pub fn page(s: &Prefs, layer: &mut Vec<El>, k: &Kit) -> El {
    let pages = SegmentedControl::new(&["Roles", "Assignments"], Some(s.agents.page)).label("Agents").fit().on_pick(|s: &mut Prefs, i, _| {
        s.agents.page = i;
        s.agents.open = None;
        s.agents.save_error = None;
    });
    let mut out = parts::page().child(row().w_full().justify_center().child(pages.build(k).key("settings-agents-page")));
    out = out.children(notices(s));
    let Some(r) = roles(s) else {
        return match &s.agents.read {
            None => out.child(parts::reading("agent roles")),
            Some(_) => out,
        };
    };
    if s.agents.page == 1 { out.child(assignments_page(s, r, layer, k)) } else { out.child(roles_page(s, r, layer, k)) }
}
