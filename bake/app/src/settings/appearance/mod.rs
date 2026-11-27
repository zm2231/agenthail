mod backgrounds;
mod controls;
mod custom;
mod history;
mod page;

use std::collections::{HashMap, HashSet};

use feed::catalog::Client;
use feed::look::{self as fl, Change, Entry, Preset, Scope, View};
use feed::prefs::Failure;
use rune::{FileDialogOptions, Pixels, Tx};

use super::{Done, Prefs};

pub use page::page;

pub const TAB: usize = 2;
const THUMB_SIDE: u32 = 192;

#[derive(Default)]
pub struct Form {
    pub here: bool,
    pub drafts: HashMap<&'static str, f32>,
    writing: bool,
    pub error: Option<Failure>,
    pub history: Option<Result<Vec<Entry>, Failure>>,
    pub presets: Option<Result<Vec<Preset>, Failure>>,
    pub thumbs: HashMap<String, Pixels>,
    asked: HashSet<String>,
    pub history_open: bool,
    pub confirm_reset: bool,
    seen_rev: String,
    accent_edit: Option<custom::Pick>,
    mesh_edit: Option<custom::Mesh>,
    history_asked: u64,
    history_shown: u64,
}

pub enum Got {
    History(u64, Result<Vec<Entry>, Failure>),
    Presets(Result<Vec<Preset>, Failure>),
    Thumb(String, Result<(u32, u32, Vec<u8>), String>),
    Wrote(u64, Result<View, Failure>),
}

fn spawn(s: &mut Prefs, job: impl FnOnce(&Client) -> Got + Send + 'static) -> bool {
    if s.client.is_none() {
        (s.client, s.no_client) = super::connect();
    }
    let Some(client) = s.client.clone() else {
        s.appearance.error = Some(super::unread(s));
        return false;
    };
    let client = Client::from_env().unwrap_or(client);
    s.in_flight += 1;
    let tx = s.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(Done::Look(job(&client)));
    });
    true
}

fn history(s: &mut Prefs) {
    s.appearance.history_asked += 1;
    let asked = s.appearance.history_asked;
    spawn(s, move |c| Got::History(asked, c.ui_log(50)));
}

pub fn opened(s: &mut Prefs) {
    if s.tab != TAB {
        return;
    }
    history(s);
    if matches!(s.appearance.presets, Some(Ok(_))) {
        thumbs(s);
    } else {
        spawn(s, |c| Got::Presets(c.ui_presets()));
    }
}

pub fn changed(s: &mut Prefs) {
    let rev = crate::look::view().map(|v| v.rev).unwrap_or_default();
    if s.appearance.writing || rev == s.appearance.seen_rev {
        return;
    }
    s.appearance.seen_rev = rev;
    s.appearance.drafts.clear();
    thumbs(s);
    if s.tab == TAB {
        history(s);
    }
}

pub fn scope(s: &Prefs) -> Scope {
    let device = crate::look::device();
    if s.appearance.here && !device.is_empty() { Scope::ThisComputer(device) } else { Scope::Everywhere }
}

pub fn writing(s: &Prefs) -> bool {
    s.appearance.writing
}

pub fn write(s: &mut Prefs, change: Change) {
    if s.appearance.writing {
        return;
    }
    let turn = change.touches("text.scale").then(|| {
        crate::ui::text_size::supersede();
        crate::ui::text_size::turn()
    });
    let device = crate::look::device();
    s.appearance.error = None;
    s.appearance.confirm_reset = false;
    if spawn(s, move |c| {
        if let Some(turn) = &turn {
            turn.wait();
        }
        let ticket = crate::look::ticket();
        Got::Wrote(ticket, c.ui_change(fl::device_class(), &device, &change))
    }) {
        s.appearance.writing = true;
    }
}

pub fn set(s: &mut Prefs, key: &str, value: &str, label: &str) {
    let change = Change::set(&scope(s), key, value, label);
    write(s, change);
}

pub fn number(n: f32) -> String {
    let r = (n * 100.0).round() / 100.0;
    if r == r.round() { format!("{}", r as i64) } else { format!("{r}") }
}

pub fn slide(s: &mut Prefs, key: &'static str, label: &str, value: f32, done: bool) {
    s.appearance.drafts.insert(key, value);
    if done {
        set(s, key, &number(value), &format!("{label} to {}", number(value)));
    }
}

fn thumbs(s: &mut Prefs) {
    if let Some(view) = crate::look::view() {
        for path in fl::Look::from_view(&view).images() {
            want_thumb(s, &path);
        }
    }
    let Some(Ok(presets)) = s.appearance.presets.clone() else { return };
    for preset in presets {
        for value in [&preset.dark, &preset.light] {
            match fl::parse_background(value) {
                Some(fl::Wallpaper::Image(path)) => want_thumb(s, &path),
                Some(fl::Wallpaper::Mesh { columns, rows, colors }) if !s.appearance.thumbs.contains_key(value.as_str()) => {
                    let (w, h, rgba) = fl::mesh(columns, rows, &colors, 1.6);
                    let pixels = Pixels::new();
                    pixels.set(w, h, rgba);
                    s.appearance.thumbs.insert(value.clone(), pixels);
                }
                _ => {}
            }
        }
    }
}

fn want_thumb(s: &mut Prefs, path: &str) {
    if s.appearance.thumbs.contains_key(path) || s.appearance.asked.contains(path) {
        return;
    }
    let asked = path.to_string();
    let started = spawn(s, move |c| {
        let got = c.ui_background(&asked).and_then(|data| fl::thumbnail(&data, THUMB_SIDE));
        Got::Thumb(asked, got)
    });
    if started {
        s.appearance.asked.insert(path.to_string());
    }
}

pub fn choose_image(s: &mut Prefs, tx: &mut Tx) {
    if s.appearance.writing {
        return;
    }
    if s.client.is_none() {
        (s.client, s.no_client) = super::connect();
    }
    if s.client.is_none() {
        s.appearance.error = Some(super::unread(s));
        changed(s);
        return;
    }
    s.appearance.writing = true;
    let options = FileDialogOptions { title: "Choose a background image".into(), ..Default::default() }.filter("Images", &["png", "jpg", "jpeg", "webp"]);
    tx.open_file(options, |s: &mut Prefs, result, _| match result {
        Ok(Some(path)) => upload(s, path),
        Ok(None) => {
            s.appearance.writing = false;
            changed(s);
        }
        Err(e) => {
            s.appearance.writing = false;
            s.appearance.error = Some(Failure::new(format!("Couldn't open the file picker: {e}")));
            changed(s);
        }
    });
}

fn upload(s: &mut Prefs, path: std::path::PathBuf) {
    let name = path.file_name().map(|n| n.to_string_lossy().to_string()).unwrap_or_default();
    let scope = scope(s);
    let device = crate::look::device();
    s.appearance.error = None;
    let started = spawn(s, move |c| {
        let data = match std::fs::read(&path) {
            Ok(data) => data,
            Err(e) => return Got::Wrote(0, Err(Failure::new(format!("Couldn't read {name}: {e}")))),
        };
        let stored = match c.ui_upload(&name, &data) {
            Ok(stored) => stored,
            Err(f) => return Got::Wrote(0, Err(f)),
        };
        let ticket = crate::look::ticket();
        Got::Wrote(ticket, c.ui_change(fl::device_class(), &device, &Change::set(&scope, "background", &format!("image: {stored}"), &format!("background to {name}"))))
    });
    s.appearance.writing = started;
    if !started {
        changed(s);
    }
}

pub fn take(s: &mut Prefs, got: Got) {
    match got {
        Got::History(asked, h) => {
            if asked > s.appearance.history_shown {
                s.appearance.history_shown = asked;
                s.appearance.history = Some(h);
            }
        }
        Got::Presets(p) => {
            s.appearance.presets = Some(p);
            thumbs(s);
        }
        Got::Thumb(path, Ok((w, h, rgba))) => {
            let pixels = Pixels::new();
            pixels.set(w, h, rgba);
            s.appearance.thumbs.insert(path, pixels);
        }
        Got::Thumb(path, Err(_)) => {
            s.appearance.asked.remove(&path);
        }
        Got::Wrote(ticket, result) => {
            s.appearance.writing = false;
            s.appearance.drafts.clear();
            match result {
                Ok(view) => {
                    s.appearance.seen_rev = view.rev.clone();
                    for path in fl::Look::from_view(&view).images() {
                        want_thumb(s, &path);
                    }
                    crate::look::take_view(ticket, view);
                }
                Err(f) => s.appearance.error = Some(f),
            }
            history(s);
        }
    }
}
