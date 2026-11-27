use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::mpsc::{Sender, channel};
use std::sync::{Mutex, OnceLock};

use serde_json::{Value, json};

use crate::App;

pub const PANES: [&str; 3] = ["Sidebar", "Needs you", "Sessions"];

static LAST: Mutex<String> = Mutex::new(String::new());
static FAILED: Mutex<Option<String>> = Mutex::new(None);

pub fn failure() -> Option<String> {
    FAILED.lock().expect("layout lock").clone()
}

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
    s.ov.boards.get(key).copied().unwrap_or_else(|| s.ov.d.project(key).is_none_or(|p| p.lead.is_none() && p.lead_pending.is_none()))
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

fn unsent(s: &App) -> Value {
    let mut all = s.unsent.clone();
    for (id, pending) in s.in_flight.iter().flat_map(|(id, list)| list.iter().map(move |p| (id, p))) {
        all.add(id, pending.clone());
    }
    all.to_json()
}

fn drafts(s: &App) -> feed::composer::Drafts {
    let mut drafts = s.drafts.clone();
    for (id, origin) in crate::ui::complete::browse_origins(s) {
        drafts.set(&id, &origin);
    }
    drafts
}

fn pasted(s: &App, drafts: &feed::composer::Drafts) -> Value {
    let unsent: Vec<&str> = s.unsent.iter().map(|(_, u)| u).chain(s.in_flight.values().flatten()).filter_map(|u| u.draft.as_deref()).collect();
    Value::Object(feed::paste::shared().saved(drafts.texts().chain(s.complete.keys.texts()).chain(unsent)))
}

fn encode(s: &App) -> String {
    let drafts = drafts(s);
    let boards: serde_json::Map<String, Value> = s.ov.boards.iter().map(|(k, v)| (k.clone(), Value::Bool(*v))).collect();
    json!({
        "brief": s.ov.now_brief,
        "transcriptFocus": crate::transcript::focused(),
        "collapsed": collapsed(s),
        "focus": s.ov.now_focus.map(|(i, before)| json!({ "pane": i, "before": before })),
        "boards": boards,
        "looked": { "now": s.ov.looked_now, "projects": s.ov.looked },
        "drafts": drafts.to_json(),
        "pasted": pasted(s, &drafts),
        "attachments": crate::ui::attach::saved(s),
        "stashes": s.complete.keys.to_json(),
        "stashedAttachments": crate::ui::attach::stashed_saved(s),
        "unsent": unsent(s),
        "handledHere": feed::decisions::saved_history(&s.ov.inbox.history),
        "queueTaking": feed::queue::Taking::to_json(&s.queue_taking),
        "backgroundDock": s.dock.to_json(),
        "turnNotice": feed::notice::shared().to_json(),
        "projectTiles": serde_json::to_value(s.ov.projects.tiles.saved()).unwrap_or(Value::Null),
        "leadDrafts": s.ov.projects.lead_drafts.iter().filter(|(_, d)| !d.text.trim().is_empty()).map(|(k, d)| (k.clone(), Value::String(d.text.clone()))).collect::<serde_json::Map<String, Value>>(),
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
    crate::transcript::restore_focus(v["transcriptFocus"].as_bool().unwrap_or(false));
    if let Some(c) = flags(&v["collapsed"]).filter(|c| !c.iter().all(|x| *x)) {
        set_collapsed(s, c);
    }
    if let (Some(i), Some(before)) = (v["focus"]["pane"].as_u64(), flags(&v["focus"]["before"])) {
        s.ov.now_focus = Some(((i as usize).min(2), before));
    }
    if let Some(m) = v["boards"].as_object() {
        s.ov.boards = m.iter().filter_map(|(k, b)| b.as_bool().map(|b| (k.clone(), b))).collect::<HashMap<_, _>>();
    }
    s.ov.looked_now = v["looked"]["now"].as_str().map(str::to_string);
    if let Some(m) = v["looked"]["projects"].as_object() {
        s.ov.looked = m.iter().filter_map(|(k, t)| t.as_str().map(|t| (k.clone(), t.to_string()))).collect();
    }
    if let Some(pasted) = v["pasted"].as_object() {
        let mut store = feed::paste::shared();
        for (marker, text) in pasted {
            store.restore(marker, text.as_str().unwrap_or_default());
        }
    }
    s.drafts = feed::composer::Drafts::from_json(&v["drafts"]);
    s.unsent = feed::composer::Unconfirmed::from_json(&v["unsent"]);
    s.ov.inbox.history = feed::decisions::restored_history(&v["handledHere"]);
    s.attach.tray = feed::attach::Tray::from_json(&v["attachments"]);
    s.attach.tray.load_stashed(&v["stashedAttachments"]);
    s.complete.keys = crate::ui::composer_keys::State::from_json(&v["stashes"]);
    let held: Vec<(String, feed::composer::Unsent)> = s.unsent.iter().map(|(id, u)| (id.clone(), u.clone())).collect();
    for (id, unsent) in held {
        if s.drafts.get(&id).trim().is_empty() && !s.attach.tray.has(&id) {
            s.drafts.set(&id, &crate::ui::paste::unsent_draft(&unsent));
            crate::ui::attach::restore(s, &id, &unsent);
        }
    }
    s.queue_taking = feed::queue::Taking::from_json(&v["queueTaking"]);
    s.dock = feed::background::DockChoices::from_json(&v["backgroundDock"]);
    *feed::notice::shared() = feed::notice::TurnNotice::from_json(&v["turnNotice"]);
    if let Ok(saved) = serde_json::from_value(v["projectTiles"].clone()) {
        s.ov.projects.tiles = feed::projects::Tiles::restore(saved);
    }
    if let Some(m) = v["leadDrafts"].as_object() {
        s.ov.projects.lead_drafts = m.iter().filter_map(|(k, t)| t.as_str().map(|t| (k.clone(), rune::TextEdit::new(t)))).collect();
    }
    crate::ui::queue::recover(s);
    s.ov.arrange();
    *LAST.lock().expect("layout lock") = encode(s);
}

type Write = (String, Option<Sender<Result<(), String>>>);

fn writer() -> &'static Sender<Write> {
    static WRITER: OnceLock<Sender<Write>> = OnceLock::new();
    WRITER.get_or_init(|| {
        let (tx, rx) = channel::<Write>();
        std::thread::spawn(move || {
            for (text, done) in rx {
                let Some(p) = path() else { continue };
                let next = p.with_extension("json.next");
                let written = p
                    .parent()
                    .map_or(Ok(()), std::fs::create_dir_all)
                    .and_then(|()| std::fs::write(&next, &text))
                    .and_then(|()| std::fs::rename(&next, &p))
                    .map_err(|e| e.to_string());
                *FAILED.lock().expect("layout lock") = written.clone().err();
                if written.is_err() {
                    let mut last = LAST.lock().expect("layout lock");
                    if *last == text {
                        last.clear();
                    }
                }
                if let Some(done) = done {
                    let _ = done.send(written);
                }
            }
        });
        tx
    })
}

pub fn save(s: &App) {
    if !s.ov.persist {
        return;
    }
    if path().is_none() {
        *FAILED.lock().expect("layout lock") = Some("HOME is not set".into());
        return;
    }
    let now = encode(s);
    let mut last = LAST.lock().expect("layout lock");
    if *last != now {
        let _ = writer().send((now.clone(), None));
        *last = now;
    }
}

pub fn save_now(s: &App) -> Result<(), String> {
    if !s.ov.persist {
        return Ok(());
    }
    if path().is_none() {
        *FAILED.lock().expect("layout lock") = Some("HOME is not set".into());
        return Err("HOME is not set".into());
    }
    let now = encode(s);
    let (done, wait) = channel();
    *LAST.lock().expect("layout lock") = now.clone();
    let result = writer()
        .send((now, Some(done)))
        .map_err(|_| "the layout writer stopped".to_string())
        .and_then(|()| wait.recv_timeout(std::time::Duration::from_secs(5)).map_err(|_| "saving took too long".to_string())?);
    if let Err(error) = &result {
        *FAILED.lock().expect("layout lock") = Some(error.clone());
    }
    result
}
