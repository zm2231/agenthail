use std::io::Write as _;
use std::time::{SystemTime, UNIX_EPOCH};

pub fn record(event: &str, detail: &str) {
    let Ok(path) = std::env::var("AGENTHAIL_GPUI_PROBE") else { return };
    let now = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs_f64()).unwrap_or(0.);
    if let Ok(mut file) = std::fs::OpenOptions::new().create(true).append(true).open(path) {
        let _ = writeln!(file, "{now:.4}\t{event}\t{detail}");
    }
}
