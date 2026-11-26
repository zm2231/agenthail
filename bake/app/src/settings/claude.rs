use feed::prefs::{self, CompactDraft, CompactRoot, Compaction, Hold, RemoteControl};
use rune::{El, col, row, text};
use rune_kit::{Button, Collapsible, ControlSize, SegmentedControl, TextInput};
use time::OffsetDateTime;

use super::parts::{self, fact, line, section};
use super::{Prefs, Read, Section, busy, send_compaction, write};
use crate::theme::{self, META, failed, muted};

fn remote(s: &Prefs, rc: &RemoteControl) -> El {
    let rows = rc
        .roots
        .iter()
        .map(|root| {
            let control = match root.problem.as_deref().filter(|p| !p.is_empty()) {
                Some(problem) => text(problem.to_string()).font(META).color(failed()).wrap().max_w(260.0),
                None => {
                    let on = root.at_startup == Some(true);
                    let path = root.root.clone();
                    let key = format!("remote-{}", root.root);
                    parts::switch(&key, &format!("Remote Control at startup for {}", root.name), on, busy(s), move |s| {
                        let path = path.clone();
                        write(s, format!("remote-{path}"), Section::Remote, &[Read::Remote], move |c| c.set_remote_control(&path, !on).map(drop));
                    })
                }
            };
            let detail = (root.problem.is_none() && root.at_startup.is_none()).then(|| "Not set; Claude's default applies".to_string());
            line(&root.name, detail, control)
        })
        .collect();
    section(
        "Claude Remote Control at startup",
        rows,
        Some("New Claude sessions on an account with this on start Remote Control, so what you write here reaches them as your own turn. Sessions already running keep what they started with.".into()),
    )
}

fn holds(s: &Prefs, root: &CompactRoot, all: &[Hold]) -> Option<El> {
    let lines = prefs::hold_lines(root, all, OffsetDateTime::now_utc());
    if lines.is_empty() {
        return None;
    }
    let open = s.holds_open.contains(&root.root);
    let key = root.root.clone();
    let mut c = Collapsible::new(format!("Not compacted recently · {}", lines.len()), open).ghost().on_toggle(move |s: &mut Prefs| {
        if !s.holds_open.remove(&key) {
            s.holds_open.insert(key.clone());
        }
    });
    for l in lines {
        c = c.child(text(l).font(META).color(muted()).wrap());
    }
    Some(row().w_full().px(6.0).py(4.0).child(c.build(theme::kit()).w_full()))
}

fn titles(s: &Prefs, c: &Compaction) -> Vec<String> {
    prefs::root_titles(&c.roots.iter().map(|r| r.root.as_str()).collect::<Vec<_>>(), &s.home)
}

fn root_card(s: &Prefs, title: &str, root: &CompactRoot, all: &[Hold]) -> El {
    let idle = &root.agenthail_idle_compaction;
    let mut rows = vec![fact("Window", prefs::window_label(root.auto_compact_window.as_ref()))];
    rows.extend(prefs::model_lines(root).into_iter().map(|(model, window)| fact(&model, window)));
    rows.push(fact("Autocompact", prefs::toggle_label(root.auto_compact_enabled).into()));
    rows.push(fact("Idle compaction", prefs::toggle_label(root.idle_compaction).into()));
    rows.push(fact("Agenthail idle compaction", prefs::agenthail_idle_label(idle)));
    if let Some(pre) = prefs::pre_limit_label(idle) {
        rows.push(fact("Before a usage limit", pre));
    }
    rows.extend(prefs::override_lines(root).into_iter().map(parts::words));
    rows.extend(holds(s, root, all));
    section(title, rows, Some(root.settings_path.clone()))
}

fn tri(key: &'static str, value: Option<bool>, set: fn(&mut CompactDraft, Option<bool>)) -> El {
    let picked = match value {
        None => 0,
        Some(true) => 1,
        Some(false) => 2,
    };
    SegmentedControl::new(&["Leave as is", "On", "Off"], Some(picked))
        .label(key)
        .fit()
        .on_pick(move |s: &mut Prefs, i, _| {
            if busy(s) {
                return;
            }
            s.saved_notice = false;
            set(&mut s.draft, [None, Some(true), Some(false)][i.min(2)]);
        })
        .build(theme::kit())
        .key(format!("settings-{key}"))
}

pub fn draft(s: &Prefs) -> CompactDraft {
    CompactDraft { model: s.model.text.clone(), window: s.window.text.clone(), min_tokens: s.min_tokens.text.clone(), pre_limit: s.pre_limit.text.clone(), ..s.draft.clone() }
}

fn save(s: &mut Prefs) {
    let d = draft(s);
    if d.problem().is_some() || !d.changes() || d.root.is_empty() {
        return;
    }
    send_compaction(s, d);
}

fn input(s: &Prefs, edit: &rune::TextEdit, label: &str, hint: &str, pick: for<'a> fn(&'a mut Prefs) -> &'a mut rune::TextEdit) -> El {
    TextInput::new(edit).label(label.to_string()).placeholder(hint.to_string()).width(Some(220.0)).disabled(busy(s)).on_edit(pick).on_change(|s: &mut Prefs, _| s.saved_notice = false).build(theme::kit())
}

fn change(s: &Prefs, c: &Compaction, layer: &mut Vec<El>) -> El {
    let choices = titles(s, c).into_iter().zip(&c.roots).map(|(t, r)| (t, r.root.clone())).collect();
    let root = parts::select(s, "settings-compact-root", "Account folder", choices, &s.draft.root, busy(s), layer, |s, value| {
        s.draft.root = value;
        s.draft_orphaned = false;
    });
    let d = draft(s);
    let busy = busy(s);
    let status = match (d.problem(), s.notes.get(&Section::Compaction)) {
        _ if s.draft_orphaned => text("The account folder this change was for is gone. Choose one to save it to.").font(META).color(failed()).wrap(),
        (Some(p), _) => text(p).font(META).color(failed()).wrap(),
        (None, Some(f)) => parts::failure(s, f),
        (None, None) if s.saved_notice => text("Saved.").font(META).color(muted()),
        _ => text(""),
    };
    let save_button = Button::new("Save").control_size(ControlSize::Small).disabled(busy || d.problem().is_some() || !d.changes() || d.root.is_empty()).on_press(save).build(theme::kit()).key("settings-compact-save");
    let rows = vec![
        line("Account folder", None, root),
        line("Model", None, input(s, &s.model, "Model", "Blank for every model", |s| &mut s.model)),
        line("Window", None, input(s, &s.window, "Window", "tokens, auto, or unset", |s| &mut s.window)),
        line("Autocompact", None, tri("autocompact", s.draft.enabled, |d, v| d.enabled = v)),
        line("Idle compaction", None, tri("idle-compaction", s.draft.idle, |d, v| d.idle = v)),
        line("Agenthail idle compaction", None, tri("agenthail-idle", s.draft.agenthail_idle, |d, v| d.agenthail_idle = v)),
        line("From context size", None, input(s, &s.min_tokens, "From context size", "tokens, 200000 by default", |s| &mut s.min_tokens)),
        line("Before a usage limit, from", None, input(s, &s.pre_limit, "Before a usage limit", "percent, 90 by default, or off", |s| &mut s.pre_limit)),
        row().w_full().gap(12.0).items_center().px(14.0).py(10.0).child(col().grow(1.0).min_w(0.0).child(status)).child(save_button),
    ];
    section("Change", rows, None)
}

pub fn page(s: &Prefs, layer: &mut Vec<El>) -> El {
    let mut out = parts::page();
    match &s.remote {
        None => out = out.child(parts::reading("Remote Control")),
        Some(Err(f)) => out = out.child(parts::failure(s, f)),
        Some(Ok(rc)) if rc.shown() => out = out.child(remote(s, rc)),
        Some(Ok(_)) => {}
    }
    if let Some(f) = s.notes.get(&Section::Remote) {
        out = out.child(parts::failure(s, f));
    }
    out = out.child(section(
        "Compaction",
        vec![parts::words("When Claude compacts a conversation, for each Claude account folder. Claude's settings apply to sessions started after a change. Agenthail's idle compaction compacts a large idle conversation before its cache expires, and applies at once.")],
        None,
    ));
    match &s.compaction {
        None => out.child(parts::reading("Claude settings")),
        Some(Err(f)) => out.child(parts::failure(s, f)),
        Some(Ok(c)) if c.roots.is_empty() => out.child(text("No Claude accounts.").font(META).color(muted()).px(4.0)),
        Some(Ok(c)) => out.children(titles(s, c).iter().zip(&c.roots).map(|(t, r)| root_card(s, t, r, c.holds()))).child(change(s, c, layer)),
    }
}
