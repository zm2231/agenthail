use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::mpsc::{Sender, channel};
use std::sync::{Mutex, OnceLock};

use serde_json::{Value, json};

use crate::App;

pub const PANES: [&str; 3] = ["Sidebar", "Needs you", "Sessions"];

static LAST: Mutex<String> = Mutex::new(String::new());

fn path() -> Option<PathBuf> {
    let home = std::env::var_os("HOME")?;
    Some(PathBuf::from(home).join("Library/Application Support/Agenthail/layout.json"))
}

pub fn collapsed(s: &App) -> [bool; 3] {
    [!s.ov.now_side.is_open(), s.ov.now_centre_hidden, !s.ov.now_rail.is_open()]
}

pub fn set_collapsed(s: &mut App, c: [bool; 3]) {
    s.ov.now_side.set_open(!c[0]);
    s.ov.now_centre_hidden = c[1];
    s.ov.now_rail.set_open(!c[2]);
}

pub fn board_for(s: &App, key: &str) -> bool {
    s.ov.boards.get(key).copied().unwrap_or_else(|| s.ov.d.project(key).is_none_or(|p| p.lead.is_none()))
}

pub fn toggle_board(s: &mut App, key: &str) {
    let now = board_for(s, key);
    s.ov.boards.insert(key.to_string(), !now);
}

pub fn toggle_pane(s: &mut App, i: usize) {
    s.ov.now_brief = false;
    s.ov.now_focus = None;
    let mut c = collapsed(s);
    c[i] = !c[i];
    if c.iter().all(|x| *x) {
        return;
    }
    set_collapsed(s, c);
}

pub fn expand_pane(s: &mut App, i: usize) {
    s.ov.now_focus = None;
    let mut c = collapsed(s);
    c[i] = false;
    set_collapsed(s, c);
}

pub fn focus_pane(s: &mut App, i: usize) {
    s.ov.now_brief = false;
    if let Some((f, before)) = s.ov.now_focus {
        s.ov.now_focus = None;
        set_collapsed(s, before);
        if f == i {
            return;
        }
    }
    let before = collapsed(s);
    s.ov.now_focus = Some((i, before));
    set_collapsed(s, [i != 0, i != 1, i != 2]);
}

pub fn restore(s: &mut App) -> bool {
    let Some((_, before)) = s.ov.now_focus.take() else { return false };
    set_collapsed(s, before);
    true
}

fn encode(s: &App) -> String {
    let boards: serde_json::Map<String, Value> = s.ov.boards.iter().map(|(k, v)| (k.clone(), Value::Bool(*v))).collect();
    json!({
        "brief": s.ov.now_brief,
        "collapsed": collapsed(s),
        "focus": s.ov.now_focus.map(|(i, before)| json!({ "pane": i, "before": before })),
        "boards": boards,
    })
    .to_string()
}

pub fn load(s: &mut App) {
    let Some(p) = path() else { return };
    let Ok(raw) = std::fs::read_to_string(p) else { return };
    let Ok(v) = serde_json::from_str::<Value>(&raw) else { return };
    let flags = |v: &Value| -> Option<[bool; 3]> {
        let a = v.as_array()?;
        Some([a.first()?.as_bool()?, a.get(1)?.as_bool()?, a.get(2)?.as_bool()?])
    };
    s.ov.now_brief = v["brief"].as_bool().unwrap_or(false);
    if let Some(c) = flags(&v["collapsed"]).filter(|c| !c.iter().all(|x| *x)) {
        set_collapsed(s, c);
    }
    if let (Some(i), Some(before)) = (v["focus"]["pane"].as_u64(), flags(&v["focus"]["before"])) {
        s.ov.now_focus = Some(((i as usize).min(2), before));
    }
    if let Some(m) = v["boards"].as_object() {
        s.ov.boards = m.iter().filter_map(|(k, b)| b.as_bool().map(|b| (k.clone(), b))).collect::<HashMap<_, _>>();
    }
    *LAST.lock().expect("layout lock") = encode(s);
}

fn writer() -> &'static Sender<String> {
    static WRITER: OnceLock<Sender<String>> = OnceLock::new();
    WRITER.get_or_init(|| {
        let (tx, rx) = channel::<String>();
        std::thread::spawn(move || {
            for text in rx {
                let Some(p) = path() else { continue };
                let written = p.parent().is_none_or(|dir| std::fs::create_dir_all(dir).is_ok()) && std::fs::write(&p, &text).is_ok();
                if !written {
                    let mut last = LAST.lock().expect("layout lock");
                    if *last == text {
                        last.clear();
                    }
                }
            }
        });
        tx
    })
}

pub fn save(s: &App) {
    if !s.ov.persist || path().is_none() {
        return;
    }
    let now = encode(s);
    let mut last = LAST.lock().expect("layout lock");
    if *last != now {
        let _ = writer().send(now.clone());
        *last = now;
    }
}
