use super::*;
use std::sync::atomic::AtomicUsize;

fn bell() -> (&'static Bell, Arc<AtomicUsize>) {
    let bell: &'static Bell = Box::leak(Box::new(Bell::new()));
    let rings = Arc::new(AtomicUsize::new(0));
    let counted = rings.clone();
    bell.install(move || {
        counted.fetch_add(1, Ordering::Relaxed);
    });
    (bell, rings)
}

#[test]
fn ages_move_on_at_the_start_of_each_minute() {
    let at = |s: &str| time::OffsetDateTime::parse(s, &time::format_description::well_known::Rfc3339).unwrap();
    assert_eq!(next_minute(at("2026-11-27T10:04:37.5Z")), at("2026-11-27T10:05:00Z"));
    assert_eq!(next_minute(at("2026-11-27T10:05:00Z")), at("2026-11-27T10:06:00Z"));
}

#[test]
fn a_burst_of_sends_rings_once_until_the_poll_rearms() {
    let (bell, rings) = bell();
    let (tx, rx) = channel(bell);
    for n in 0..5 {
        tx.send(n).unwrap();
    }
    assert_eq!(rings.load(Ordering::Relaxed), 1);
    assert!(bell.rearm());
    assert_eq!(rx.try_iter().count(), 5);
    assert!(!bell.rearm(), "nothing rang since");
    tx.send(5).unwrap();
    assert_eq!(rings.load(Ordering::Relaxed), 2);
}

#[test]
fn a_send_while_the_poll_drains_rings_again() {
    let (bell, rings) = bell();
    let (tx, rx) = channel(bell);
    tx.send(1).unwrap();
    assert!(bell.rearm());
    assert_eq!(rx.try_recv().ok(), Some(1));
    tx.send(2).unwrap();
    assert_eq!(rings.load(Ordering::Relaxed), 2, "a post after the rearm wakes the next poll");
    assert!(rx.try_recv().is_ok());
}

#[test]
fn a_send_from_another_thread_rings_with_no_timer() {
    let (bell, rings) = bell();
    let (tx, rx) = channel(bell);
    std::thread::spawn(move || tx.send("done").unwrap()).join().unwrap();
    assert_eq!(rings.load(Ordering::Relaxed), 1);
    assert_eq!(rx.try_recv().ok(), Some("done"));
}

#[test]
fn a_send_before_the_ring_is_installed_rings_when_it_is() {
    let bell: &'static Bell = Box::leak(Box::new(Bell::new()));
    let (tx, _rx) = channel(bell);
    tx.send(()).unwrap();
    let rings = Arc::new(AtomicUsize::new(0));
    let counted = rings.clone();
    bell.install(move || {
        counted.fetch_add(1, Ordering::Relaxed);
    });
    assert_eq!(rings.load(Ordering::Relaxed), 1);
}

#[test]
fn a_send_to_a_closed_poll_does_not_ring() {
    let (bell, rings) = bell();
    let (tx, rx) = channel::<u8>(bell);
    drop(rx);
    assert!(tx.send(1).is_err());
    assert_eq!(rings.load(Ordering::Relaxed), 0);
}

fn sources(dir: &std::path::Path, out: &mut Vec<std::path::PathBuf>) {
    for entry in std::fs::read_dir(dir).unwrap().flatten() {
        let path = entry.path();
        if path.is_dir() {
            sources(&path, out);
        } else if path.extension().is_some_and(|e| e == "rs") {
            out.push(path);
        }
    }
}

fn outside_tests(text: &str) -> Vec<(usize, &str)> {
    let lines: Vec<&str> = text.lines().collect();
    let mut kept = Vec::new();
    let mut i = 0;
    while i < lines.len() {
        let line = lines[i].trim();
        let next = lines.get(i + 1).map(|l| l.trim()).unwrap_or_default();
        if line == "#[cfg(test)]" && next.starts_with("mod ") && next.ends_with('{') {
            let mut depth = 0i32;
            i += 1;
            while i < lines.len() {
                depth += lines[i].matches('{').count() as i32 - lines[i].matches('}').count() as i32;
                i += 1;
                if depth <= 0 {
                    break;
                }
            }
            continue;
        }
        kept.push((i + 1, lines[i]));
        i += 1;
    }
    kept
}

fn std_channels(lines: &[(usize, &str)]) -> Vec<usize> {
    let mut found = Vec::new();
    let mut importing: Option<usize> = None;
    for (n, line) in lines {
        let code = line.split("//").next().unwrap_or_default();
        let names = |text: &str| text.split(|c: char| !(c.is_alphanumeric() || c == '_')).any(|w| w == "channel" || w == "sync_channel");
        if let Some(start) = importing {
            if names(code) {
                found.push(start);
            }
            if code.contains('}') {
                importing = None;
            }
            continue;
        }
        if code.contains("mpsc::channel") || code.contains("sync_channel") || code.contains("mpsc::*") || code.contains("mpsc as ") {
            found.push(*n);
        } else if let Some(rest) = code.trim_start().strip_prefix("use std::sync::mpsc::{") {
            if names(rest.split('}').next().unwrap_or_default()) {
                found.push(*n);
            } else if !rest.contains('}') {
                importing = Some(*n);
            }
        }
    }
    found
}

#[test]
fn the_check_finds_every_way_to_make_a_plain_channel() {
    let text = "use std::sync::mpsc::{Receiver, Sender, channel};\nlet (a, b) = std::sync::mpsc::channel();\nuse std::sync::mpsc::{\n    Receiver,\n    sync_channel,\n};\nuse std::sync::mpsc::Receiver;\nlet (c, d) = crate::wake::channel();\n#[cfg(test)]\nmod tests {\n    let x = mpsc::channel();\n}\nuse std::sync::mpsc::channel;\nuse std::sync::mpsc::channel as plain;\nuse std::sync::{mpsc, Arc};\nlet (e, f) = mpsc::channel::<u8>();\nuse std::sync::mpsc::*;\nuse std::sync::mpsc as queue;\n";
    assert_eq!(std_channels(&outside_tests(text)), vec![1, 2, 3, 13, 14, 16, 17, 18]);
}

#[test]
fn every_channel_in_the_app_and_feed_is_made_here() {
    let root = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).parent().unwrap().to_path_buf();
    let mut files = Vec::new();
    for dir in ["app/src", "feed/src"] {
        sources(&root.join(dir), &mut files);
    }
    assert!(files.len() > 50, "the sources were found");
    let mut found = Vec::new();
    for file in files {
        let name = file.file_name().unwrap().to_string_lossy().to_string();
        if name == "wake.rs" || name.ends_with("_tests.rs") || file.components().any(|c| c.as_os_str() == "tests") {
            continue;
        }
        let text = std::fs::read_to_string(&file).unwrap();
        for n in std_channels(&outside_tests(&text)) {
            found.push(format!("{}:{n}", file.strip_prefix(&root).unwrap().display()));
        }
    }
    assert!(found.is_empty(), "make channels through wake.rs so a post wakes the window that drains it:\n{}", found.join("\n"));
}
