use std::fs::{File, OpenOptions, TryLockError};
use std::io::{Read, Seek, Write};
use std::path::{Path, PathBuf};
use std::sync::OnceLock;

static HELD: OnceLock<File> = OnceLock::new();

pub enum Claim {
    First(File),
    Running(Option<i32>),
}

pub fn path(name: &str) -> Option<PathBuf> {
    let home = std::env::var_os("HOME").filter(|h| !h.is_empty())?;
    Some(Path::new(&home).join(".agenthail").join(name))
}

pub fn claim(path: &Path) -> std::io::Result<Claim> {
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir)?;
    }
    let mut file = OpenOptions::new().create(true).truncate(false).read(true).write(true).open(path)?;
    match file.try_lock() {
        Ok(()) => {
            file.set_len(0)?;
            writeln!(file, "{}", std::process::id())?;
            Ok(Claim::First(file))
        }
        Err(TryLockError::WouldBlock) => {
            let mut text = String::new();
            file.rewind()?;
            file.read_to_string(&mut text)?;
            Ok(Claim::Running(text.trim().parse().ok()))
        }
        Err(TryLockError::Error(error)) => Err(error),
    }
}

pub fn only_copy(name: &str) -> bool {
    if rune::is_headless() {
        return true;
    }
    let Some(path) = path(name) else { return true };
    match claim(&path) {
        Ok(Claim::First(file)) => {
            let _ = HELD.set(file);
            true
        }
        Ok(Claim::Running(pid)) => {
            front(pid);
            false
        }
        Err(error) => {
            eprintln!("agenthail: couldn't check for another running copy: {error}");
            true
        }
    }
}

#[cfg(target_os = "macos")]
fn front(pid: Option<i32>) {
    use objc2_app_kit::{NSApplicationActivationOptions, NSRunningApplication};
    if let Some(app) = pid.and_then(NSRunningApplication::runningApplicationWithProcessIdentifier) {
        app.activateWithOptions(NSApplicationActivationOptions::ActivateAllWindows);
    }
}

#[cfg(not(target_os = "macos"))]
fn front(_: Option<i32>) {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_second_copy_finds_the_first_and_its_pid() {
        let dir = std::env::temp_dir().join(format!("agenthail-instance-{}", std::process::id()));
        let path = dir.join("nested").join("app.lock");
        let Claim::First(first) = claim(&path).unwrap() else { panic!("the first claim holds the lock") };
        assert!(matches!(claim(&path).unwrap(), Claim::Running(Some(pid)) if pid == std::process::id() as i32));
        first.unlock().unwrap();
        drop(first);
        assert!(matches!(claim(&path).unwrap(), Claim::First(_)));
        std::fs::remove_dir_all(&dir).unwrap();
    }
}
