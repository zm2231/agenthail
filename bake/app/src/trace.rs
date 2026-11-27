use std::path::PathBuf;

use feed::trace::{self, Event};
use rune::{FileDialogOptions, MenuItem, ScrollPos, Tx};

use crate::App;

#[cfg(target_os = "macos")]
const CLOCK: libc::clockid_t = libc::CLOCK_UPTIME_RAW;
#[cfg(all(unix, not(target_os = "macos")))]
const CLOCK: libc::clockid_t = libc::CLOCK_MONOTONIC;

#[cfg(unix)]
fn uptime() -> f64 {
    let mut at = libc::timespec { tv_sec: 0, tv_nsec: 0 };
    unsafe { libc::clock_gettime(CLOCK, &mut at) };
    at.tv_sec as f64 + at.tv_nsec as f64 / 1e9
}

#[cfg(not(unix))]
fn uptime() -> f64 {
    static START: std::sync::OnceLock<std::time::Instant> = std::sync::OnceLock::new();
    START.get_or_init(std::time::Instant::now).elapsed().as_secs_f64()
}

pub fn composer(r: [f32; 4]) {
    trace::shared().composer(f64::from(r[3]), uptime());
}

pub fn scroll(at: ScrollPos) {
    let document = at.max[1] + at.viewport[1];
    trace::shared().record(Event::Scroll, &[f64::from(at.offset[1]), f64::from(document), f64::from(at.viewport[1])], uptime());
}

fn logs() -> Option<PathBuf> {
    Some(PathBuf::from(std::env::var_os("HOME")?).join("Library/Logs/Agenthail"))
}

pub fn save(_: &mut App, tx: &mut Tx) {
    let text = trace::shared().dump(time::OffsetDateTime::now_utc(), uptime());
    let options = FileDialogOptions { title: "Save Transcript Trace".into(), directory: logs().filter(|d| d.is_dir()), file_name: Some("transcript-trace.jsonl".into()), ..Default::default() }.filter("JSON Lines", &["jsonl"]);
    tx.save_file(options, move |s: &mut App, answer, _| {
        let written = match answer {
            Ok(Some(path)) => std::fs::write(&path, &text).map_err(|e| e.to_string()),
            Ok(None) => Ok(()),
            Err(e) => Err(e.to_string()),
        };
        if let Err(error) = written {
            s.ov.notice = Some(format!("Couldn't save the transcript trace: {error}"));
        }
    });
}

pub fn menu_item() -> MenuItem<App> {
    MenuItem::with_action("Save Transcript Trace…", save).id("save-transcript-trace").menu("Help")
}
