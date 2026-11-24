use std::collections::HashSet;

use feed::Item;
use rune::{Cx, El, ScrollSpec, col, el, icon, row, text, text_input};
use rune_kit::markdown::Markdown;
use rune_kit::shell::Toolbar;
use rune_kit::{IconButton, IconButtonVariant, Select, Spinner};
use rune_theme_opencode::icons;
use time::OffsetDateTime;

use crate::data::plain;
use crate::ui::{self, BODY, BODY_MED, META, META_MED, base, kit, strong, weak};
use crate::{App, Screen, go};

enum Block<'a> {
    User(&'a Item),
    Peer(&'a Item),
    Said(&'a Item),
    Run(Vec<&'a Item>),
}

fn blocks(items: &[Item]) -> Vec<Block<'_>> {
    let mut out = Vec::new();
    let mut run: Vec<&Item> = Vec::new();
    for i in items {
        let tool = matches!(i.kind.as_str(), "toolCall" | "toolResult" | "reasoning" | "command" | "tool");
        if tool {
            run.push(i);
            continue;
        }
        let block = match (i.kind.as_str(), i.role.as_deref()) {
            ("message", Some("user")) if !i.text.trim().is_empty() => Some(Block::User(i)),
            ("message", Some("peer")) => Some(Block::Peer(i)),
            ("message", Some("assistant")) if !i.text.trim().is_empty() => Some(Block::Said(i)),
            _ => None,
        };
        if let Some(b) = block {
            if !run.is_empty() {
                out.push(Block::Run(std::mem::take(&mut run)));
            }
            out.push(b);
        }
    }
    if !run.is_empty() {
        out.push(Block::Run(run));
    }
    out
}

fn arg_line(call: &Item) -> String {
    let v: serde_json::Value = serde_json::from_str(&call.text).unwrap_or_default();
    let pick = ["description", "command", "file_path", "path", "pattern", "headline", "target", "message", "query"].iter().find_map(|k| v.get(*k).and_then(|x| x.as_str()).map(String::from));
    let line = pick.unwrap_or_else(|| call.text.clone());
    line.lines().next().unwrap_or("").chars().take(140).collect()
}

fn tool_name(t: &str) -> String {
    match feed::summary::mcp_parts(t) {
        Some((_, tool)) => tool.replace("agenthail_", "").replace('_', " "),
        None => t.to_string(),
    }
}

fn run_line(items: &[&Item]) -> String {
    let owned: Vec<Item> = items.iter().map(|i| (*i).clone()).collect();
    let steps = owned.iter().filter(|i| feed::summary::is_call(i)).count();
    let times: Vec<OffsetDateTime> = owned.iter().filter_map(|i| i.timestamp.as_deref().and_then(feed::catalog::parse_time)).collect();
    let span = match (times.iter().min(), times.iter().max()) {
        (Some(a), Some(b)) if b > a => Some(feed::summary::span((*b - *a).as_seconds_f64())),
        _ => None,
    };
    let edits = feed::edits::edits(&owned);
    let files: HashSet<&str> = edits.iter().map(|e| e.path.as_str()).collect();
    let (mut add, mut del) = (0usize, 0usize);
    for e in &edits {
        let old: HashSet<&str> = e.old.lines().collect();
        let new: HashSet<&str> = e.new.lines().collect();
        add += e.new.lines().filter(|l| !old.contains(l)).count();
        del += e.old.lines().filter(|l| !new.contains(l)).count();
    }
    let mut parts = Vec::new();
    parts.push(match span {
        Some(sp) => format!("Worked {sp}"),
        None => "Worked".into(),
    });
    parts.push(if steps == 1 { "1 step".into() } else { format!("{steps} steps") });
    if !files.is_empty() {
        parts.push(format!("{} {} +{add} −{del}", files.len(), if files.len() == 1 { "file" } else { "files" }));
    }
    parts.join(" · ")
}

fn run_block(s: &App, key: String, items: &[&Item], last: bool, busy: bool) -> El {
    let k = kit();
    let open = s.open_folds.contains(&key);
    let calls: Vec<&&Item> = items.iter().filter(|i| feed::summary::is_call(i)).collect();
    let answered: HashSet<&str> = items.iter().filter(|i| i.kind == "toolResult").filter_map(|i| i.call_id.as_deref()).collect();
    let k2 = key.clone();
    let head = row()
        .key(format!("{key}-head"))
        .gap(8.0)
        .items_center()
        .py(4.0)
        .on_press(move |s: &mut App| {
            if !s.open_folds.remove(&k2) {
                s.open_folds.insert(k2.clone());
            }
        })
        .child(icon(&icons::CHECKLIST, 14.0).color(weak()))
        .child(text(run_line(items)).font(META_MED).color(base()))
        .child(icon(if open { &icons::CHEVRON_DOWN } else { &icons::CHEVRON_RIGHT }, 12.0).color(weak()));
    let mut c = col().key(key.clone()).w_full().gap(4.0).child(head);
    if open {
        let mut list = col().w_full().gap(2.0).pl(22.0);
        for call in calls.iter().take(14) {
            list = list.child(
                row()
                    .w_full()
                    .gap(10.0)
                    .items_center()
                    .child(text(tool_name(&call.title)).font(META_MED).color(base()).w(110.0).truncate())
                    .child(text(arg_line(call)).font(META.mono()).color(weak()).truncate().shrink()),
            );
        }
        c = c.child(list);
    }
    let pending = calls.last().filter(|c| c.call_id.as_deref().is_some_and(|id| !answered.contains(id)));
    if last && busy {
        if let Some(call) = pending {
            c = c.child(
                row()
                    .gap(8.0)
                    .items_center()
                    .px(10.0)
                    .py(7.0)
                    .radius(8.0)
                    .fill(k.c.surface_base)
                    .border(1.0, ui::rule())
                    .child(Spinner::new().size(12.0).build(k))
                    .child(text(format!("Running {}", tool_name(&call.title))).font(META_MED).color(k.c.text_accent))
                    .child(text(arg_line(call)).font(META.mono()).color(base()).truncate().shrink()),
            );
        } else {
            c = c.child(row().gap(8.0).items_center().child(Spinner::new().size(12.0).build(k)).child(text("Thinking".to_string()).font(META_MED).color(k.c.text_accent)));
        }
    }
    c
}

fn clip(t: &str, n: usize) -> String {
    if t.chars().count() > n { format!("{}…", t.chars().take(n).collect::<String>()) } else { t.to_string() }
}

pub fn transcript(s: &App, id: &str, max_blocks: usize) -> El {
    let k = kit();
    let busy = s.d.session(id).is_some_and(|x| matches!(x.display.kind.as_str(), "working" | "finishing" | "starting"));
    let empty: Vec<Item> = Vec::new();
    let items = s.d.transcripts.get(id).unwrap_or(&empty);
    let all = blocks(items);
    let start = all.len().saturating_sub(max_blocks);
    let n = all.len();
    let mut c = col().w_full().max_w(780.0).gap(18.0).px(28.0).pt(20.0).pb(28.0);
    if items.is_empty() {
        c = c.child(text("No transcript in the snapshot for this session.".to_string()).font(BODY).color(weak()));
    }
    for (i, b) in all.iter().enumerate().skip(start) {
        let el = match b {
            Block::User(m) => row().w_full().justify_end().child(
                col()
                    .max_w(560.0)
                    .px(16.0)
                    .py(12.0)
                    .radius(14.0)
                    .fill(k.c.surface_raised)
                    .child(text(clip(&plain(&m.text), 420)).font(BODY).color(strong()).wrap()),
            ),
            Block::Peer(m) => {
                let from = m.agent.as_ref().map(|a| a.chip()).unwrap_or_else(|| "another agent".into());
                let body = m.agent.as_ref().map(|a| a.summary.clone()).filter(|x| !x.is_empty()).unwrap_or_else(|| m.text.clone());
                col()
                    .w_full()
                    .gap(4.0)
                    .pl(12.0)
                    .border_sides([0.0, 0.0, 0.0, 2.0], k.c.border_base)
                    .child(row().gap(6.0).items_center().child(icon(&icons::SPEECH_BUBBLE, 12.0).color(weak())).child(text(format!("From {from}")).font(META_MED).color(weak())))
                    .child(text(clip(&plain(&body), 300)).font(META).color(base()).wrap())
            }
            Block::Said(m) => col().w_full().child(Markdown::new(plain(&m.text)).build(k)),
            Block::Run(items) => run_block(s, format!("run-{id}-{i}"), items, i + 1 == n, busy),
        };
        c = c.child(el.key(format!("b-{i}")));
    }
    if busy && !matches!(all.last(), Some(Block::Run(_))) {
        let detail = s.d.session(id).map(|x| x.display.detail.clone()).filter(|d| !d.is_empty()).unwrap_or_else(|| "Working on the next step".into());
        c = c.child(row().key("live").gap(8.0).items_center().child(Spinner::new().size(12.0).build(k)).child(text(detail).font(META_MED).color(k.c.text_accent)));
    }
    col().key(format!("transcript-{id}")).w_full().grow(1.0).min_h(0.0).items_center().scroll(ScrollSpec { stick_bottom: true }).child(c)
}

pub fn composer(s: &App, placeholder: &str, to: Option<(String, String)>, layer: &mut Vec<El>) -> El {
    let k = kit();
    let input = text_input(&s.composer, BODY.lh(21.0))
        .key("composer")
        .label("Message")
        .w_full()
        .min_h(44.0)
        .color(strong())
        .placeholder(placeholder.to_string(), weak())
        .on_edit(|s: &mut App| &mut s.composer);
    let mut controls = row().w_full().items_center().gap(6.0);
    if let Some((lead, this)) = to {
        let options = [lead.clone(), this.clone()];
        let current = if s.to_lead { lead.clone() } else { this.clone() };
        let lead2 = lead.clone();
        controls = controls.child(text("To".to_string()).font(META).color(weak())).child(
            Select::new("to", &options)
                .current(Some(&current))
                .open(s.to_open)
                .on_toggle(|s: &mut App| s.to_open = !s.to_open)
                .on_close(|s: &mut App| s.to_open = false)
                .on_choose(move |s: &mut App, v: &str, _| {
                    s.to_lead = v == lead2;
                    s.to_open = false;
                })
                .build(k, layer),
        );
    }
    controls = controls
        .child(ui::spacer())
        .child(text("Sends as you".to_string()).font(META).color(weak()))
        .child(IconButton::new(&icons::ARROW_UP).variant(IconButtonVariant::Submit).label("Send").on_press(|s: &mut App| crate::act(s, "Send")).build(k));
    col()
        .w_full()
        .max_w(780.0)
        .px(28.0)
        .pb(16.0)
        .child(col().w_full().gap(8.0).padding(12.0).radius(14.0).fill(k.c.surface_raised).border(1.0, ui::rule()).child(input).child(controls))
}

pub fn work_rail(s: &App, id: &str) -> El {
    let k = kit();
    let Some(x) = s.d.session(id) else { return col() };
    let mut c = col().w_full().gap(14.0).px(16.0).pt(14.0).pb(24.0);
    let primary = x.primary_work(None).and_then(|h| s.d.work.get(&h.item_id));
    match primary {
        Some(w) => {
            let item = w.id.clone();
            c = c
                .child(row().w_full().items_center().child(ui::label("On this work")).child(ui::spacer()).child(ui::id_label(&crate::data::short_id(&w.id))))
                .child(text(plain(&w.text)).font(BODY_MED).color(strong()).wrap_anywhere().max_lines(4))
                .child(ui::stepper(s.d.phase(w), &s.d.whose_move(w)).mt(4.0));
            let mut chips = row().gap(6.0).flex_wrap();
            if let Some(pr) = s.d.pr(w) {
                chips = chips.child(ui::chip(&icons::BRANCH, &format!("PR #{pr}")));
            }
            chips = chips.child(ui::chip(&icons::TASK, &format!("{} · {}", w.priority, w.kind)));
            c = c.child(chips).child(
                rune_kit::Button::new("Open work page")
                    .variant(rune_kit::ButtonVariant::Outline)
                    .icon(&icons::ARROW_UP_RIGHT)
                    .on_press(move |s: &mut App| go(s, Screen::Work(item.clone())))
                    .build(k),
            );
            let also = s.d.also_holds(x, &w.id);
            if !also.is_empty() {
                c = c.child(ui::label("Also holds").mt(6.0));
                for a in also {
                    let aid = a.id.clone();
                    c = c.child(
                        row()
                            .w_full()
                            .gap(8.0)
                            .items_center()
                            .py(4.0)
                            .on_press(move |s: &mut App| go(s, Screen::Work(aid.clone())))
                            .child(ui::mini_phase(s.d.phase(a)))
                            .child(text(plain(&a.text)).font(META).color(base()).truncate().shrink()),
                    );
                }
            }
            c = c.child(ui::label("Timeline").mt(6.0)).child(timeline(s, w, 7));
        }
        None => {
            c = c.child(ui::label("On this work")).child(text("This session holds no work item. Its last word:".to_string()).font(META).color(weak())).child(text(s.d.gist(x)).font(BODY).color(base()).wrap());
        }
    }
    c = c.child(el().h(1.0).w_full().fill(ui::rule()).mt(4.0)).child(ui::label("Session")).child(fact("Project", &x.project_name())).child(fact("Host", &x.host.name)).child(fact("Branch", x.checkout.branch.as_deref().unwrap_or("none"))).child(fact("Last active", &match s.d.ago(&x.last_active).as_str() { "now" => "just now".to_string(), a => format!("{a} ago") }));
    col().key(format!("rail-{id}")).w_full().h_full().scroll(ScrollSpec { stick_bottom: false }).child(c)
}

fn fact(name: &str, value: &str) -> El {
    row().w_full().gap(8.0).child(text(name.to_string()).font(META).color(weak()).w(84.0)).child(text(value.to_string()).font(META).color(base()).truncate().shrink())
}

pub fn timeline(s: &App, w: &crate::data::Work, max: usize) -> El {
    let k = kit();
    let mut c = col().w_full().gap(0.0);
    let entries: Vec<_> = w.timeline.iter().rev().filter(|t| t.kind != "dep" && t.kind != "imported").take(max).collect();
    let n = entries.len();
    for (i, t) in entries.iter().enumerate() {
        let what = match (t.kind.as_str(), t.state.as_deref()) {
            ("state", Some(st)) => format!("Moved to {}", state_word(st)),
            ("created", _) => "Created".into(),
            ("comment", _) => format!("Comment: {}", t.text.as_deref().unwrap_or("")),
            _ => t.text.clone().unwrap_or_default(),
        };
        let dot = el().size(7.0, 7.0).radius(3.5).fill(if t.kind == "state" { k.c.text_accent } else { k.c.border_strong });
        let rail = col().w(7.0).h_full().items_center().child(dot.mt(5.0)).child(el().w(1.0).grow(1.0).fill(if i + 1 < n { ui::rule() } else { rune::Color::TRANSPARENT }));
        c = c.child(
            row()
                .w_full()
                .gap(10.0)
                .items_stretch()
                .child(rail)
                .child(
                    col()
                        .grow(1.0)
                        .shrink()
                        .min_w(0.0)
                        .gap(2.0)
                        .pb(12.0)
                        .child(row().gap(6.0).child(text(s.d.name_of(&t.actor)).font(META_MED).color(base()).truncate().shrink()).child(text(s.d.ago(&t.at)).font(META).color(weak())))
                        .child(text(clip(&plain(&what), 180)).font(META).color(weak()).wrap().max_lines(3)),
                ),
        );
    }
    c
}

pub fn state_word(st: &str) -> &'static str {
    match st {
        "in_progress" => "Build",
        "in_review" => "Check",
        "done" => "Shipped",
        "open" | "ready" => "Up next",
        "parked" => "Parked",
        "blocked" => "Blocked",
        "closed" => "Closed",
        _ => "a new state",
    }
}

pub fn lead_choice(s: &App, id: &str) -> Option<(String, String)> {
    let x = s.d.session(id)?;
    let p = s.d.project(&x.project_key())?;
    if p.lead.as_deref() == Some(id) {
        return None;
    }
    Some((format!("{} (lead)", p.lead_name), format!("{} (this session)", s.d.title(x))))
}

pub fn view(s: &App, cx: &mut Cx<App>, id: &str, layer: &mut Vec<El>) -> El {
    let Some(x) = s.d.session(id) else { return col() };
    let to = lead_choice(s, id);
    let placeholder = match (&to, s.to_lead) {
        (Some((lead, _)), true) => format!("Tell {}", lead.replace(" (lead)", ", the lead…")),
        _ => format!("Message {}…", s.d.title(x)),
    };
    let top = row().w_full().px(16.0).py(6.0).items_center().border_sides([0.0, 0.0, 1.0, 0.0], ui::rule()).child(ui::strip(s, Some(id))).child(ui::spacer()).child(ui::ghost_keys(&["⌃Tab"]));
    let main = col().w_full().h_full().items_center().child(top).child(transcript(s, id, 26)).child(composer(s, &placeholder, to, layer));
    let pk = x.project_key();
    let pname = x.project_name();
    let title = ui::short_title(&s.d.title(x), 48);
    let toolbar = Toolbar::new()
        .leading(row().gap(14.0).items_center().child(ui::avatar(&pname, 18.0)).child(ui::crumb(&[(&pname, false), (&title, true)])))
        .trailing(ui::needs_button(s));
    ui::frame(cx, s, Some(ui::sidebar(s, Some(&pk), Some(id))), main, Some(work_rail(s, id)), toolbar)
}
