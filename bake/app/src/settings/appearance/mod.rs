mod page;

use std::collections::{HashMap, HashSet};

use feed::catalog::Client;
use feed::look::{self as fl, Change, Entry, Preset, Scope, View};
use feed::prefs::Failure;
use rune::{FileDialogOptions, Pixels, Tx};

use super::{Done, Prefs};

pub use page::page;

pub const TAB: usize = 5;
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
}

pub enum Got {
    History(Result<Vec<Entry>, Failure>),
    Presets(Result<Vec<Preset>, Failure>),
    Thumb(String, Result<(u32, u32, Vec<u8>), String>),
    Wrote(Result<View, Failure>),
}

fn spawn(s: &mut Prefs, job: impl FnOnce(&Client) -> Got + Send + 'static) -> bool {
    let Some(client) = s.client.clone() else {
        s.appearance.error = Some(super::unread(s));
        return false;
    };
    s.in_flight += 1;
    let tx = s.tx.clone();
    std::thread::spawn(move || {
        let _ = tx.send(Done::Look(job(&client)));
    });
    true
}

pub fn opened(s: &mut Prefs) {
    if s.tab != TAB {
        return;
    }
    spawn(s, |c| Got::History(c.ui_log(50)));
    if !matches!(s.appearance.presets, Some(Ok(_))) {
        spawn(s, |c| Got::Presets(c.ui_presets()));
    }
}

pub fn changed(s: &mut Prefs) {
    if !s.appearance.writing {
        s.appearance.drafts.clear();
    }
    if s.tab == TAB && !s.appearance.writing {
        spawn(s, |c| Got::History(c.ui_log(50)));
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
    let device = crate::look::device();
    s.appearance.error = None;
    s.appearance.confirm_reset = false;
    if spawn(s, move |c| Got::Wrote(c.ui_change(fl::device_class(), &device, &change))) {
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
    if s.appearance.thumbs.contains_key(path) || !s.appearance.asked.insert(path.to_string()) {
        return;
    }
    let path = path.to_string();
    spawn(s, move |c| {
        let got = c.ui_background(&path).and_then(|data| fl::decode(&data, THUMB_SIDE));
        Got::Thumb(path, got)
    });
}

pub fn choose_image(s: &mut Prefs, tx: &mut Tx) {
    if s.appearance.writing || s.client.is_none() {
        return;
    }
    let options = FileDialogOptions { title: "Choose a background image".into(), ..Default::default() }.filter("Images", &["png", "jpg", "jpeg", "heic", "webp", "gif"]);
    tx.open_file(options, |s: &mut Prefs, result, _| match result {
        Ok(Some(path)) => upload(s, path),
        Ok(None) => {}
        Err(e) => s.appearance.error = Some(Failure::new(format!("Couldn't open the file picker: {e}"))),
    });
}

fn upload(s: &mut Prefs, path: std::path::PathBuf) {
    let name = path.file_name().map(|n| n.to_string_lossy().to_string()).unwrap_or_default();
    let scope = scope(s);
    let device = crate::look::device();
    s.appearance.error = None;
    if spawn(s, move |c| {
        let data = match std::fs::read(&path) {
            Ok(data) => data,
            Err(e) => return Got::Wrote(Err(Failure::new(format!("Couldn't read {name}: {e}")))),
        };
        let stored = match c.ui_upload(&name, &data) {
            Ok(stored) => stored,
            Err(f) => return Got::Wrote(Err(f)),
        };
        Got::Wrote(c.ui_change(fl::device_class(), &device, &Change::set(&scope, "background", &format!("image: {stored}"), &format!("background to {name}"))))
    }) {
        s.appearance.writing = true;
    }
}

pub fn take(s: &mut Prefs, got: Got) {
    match got {
        Got::History(h) => s.appearance.history = Some(h),
        Got::Presets(p) => {
            s.appearance.presets = Some(p);
            thumbs(s);
        }
        Got::Thumb(path, Ok((w, h, rgba))) => {
            let pixels = Pixels::new();
            pixels.set(w, h, rgba);
            s.appearance.thumbs.insert(path, pixels);
        }
        Got::Thumb(..) => {}
        Got::Wrote(result) => {
            s.appearance.writing = false;
            match result {
                Ok(view) => crate::look::take_view(view),
                Err(f) => {
                    s.appearance.drafts.clear();
                    s.appearance.error = Some(f);
                }
            }
        }
    }
}
