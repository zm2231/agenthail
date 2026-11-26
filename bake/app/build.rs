use std::process::Command;

const SOURCES: [&str; 7] = ["src", "build.rs", "Cargo.toml", "Cargo.lock", "../feed/src", "../feed/Cargo.toml", "../feed/Cargo.lock"];

fn git(args: &[&str]) -> Option<String> {
    let out = Command::new("git").args(args).output().ok()?;
    out.status.success().then(|| String::from_utf8_lossy(&out.stdout).trim().to_string())
}

fn main() {
    for name in ["AGENTHAIL_VERSION", "AGENTHAIL_REVISION", "AGENTHAIL_MODIFIED"] {
        println!("cargo:rerun-if-env-changed={name}");
    }
    for path in SOURCES {
        println!("cargo:rerun-if-changed={path}");
    }
    for path in ["HEAD", "index", "packed-refs"].iter().filter_map(|p| git(&["rev-parse", "--git-path", p])) {
        println!("cargo:rerun-if-changed={path}");
    }
    if let Some(head) = git(&["symbolic-ref", "-q", "HEAD"]).and_then(|r| git(&["rev-parse", "--git-path", &r])) {
        println!("cargo:rerun-if-changed={head}");
    }
    let revision = std::env::var("AGENTHAIL_REVISION").ok().or_else(|| git(&["rev-parse", "HEAD"])).unwrap_or_default();
    let version = std::env::var("AGENTHAIL_VERSION").ok().or_else(|| git(&["describe", "--tags", "--abbrev=0"])).unwrap_or_default();
    let modified = std::env::var("AGENTHAIL_MODIFIED").ok().unwrap_or_else(|| git(&[&["status", "--porcelain", "--untracked-files=no", "--"][..], &SOURCES[..]].concat()).map(|s| (!s.is_empty()).to_string()).unwrap_or_else(|| "false".into()));
    println!("cargo:rustc-env=AGENTHAIL_REVISION={revision}");
    println!("cargo:rustc-env=AGENTHAIL_VERSION={version}");
    println!("cargo:rustc-env=AGENTHAIL_MODIFIED={modified}");
}
