pub mod action;
pub mod activity;
pub mod attach;
pub mod background;
pub mod blocks;
pub mod brief;
pub mod question;
pub mod request;
pub mod review;
pub mod search;
pub mod adopt;
pub mod snooze;
pub mod settings;
pub mod catalog;
pub mod complete;
pub mod connect;
pub mod create;
pub mod decisions;
pub mod delivery;
pub mod composer;
pub mod derive;
pub mod devices;
pub mod edits;
pub mod extension_changes;
pub mod extensions;
pub mod focus;
pub mod gone;
pub mod handoffs;
pub mod history;
pub mod holders;
pub mod inspect;
pub mod item;
pub mod limit;
pub mod link;
pub mod look;
pub mod menu;
pub mod notice;
pub mod moves;
pub mod notify;
#[cfg(test)]
mod mock;
pub mod queue;
pub mod prefer;
pub mod paste;
pub mod prefs;
pub mod project_message;
pub mod projects;
pub mod source;
pub mod store;
pub mod subagents;
pub mod summary;
pub mod trace;
pub mod version;
pub mod voice;
pub mod watch;
pub mod wire;

pub use blocks::{Block, Kind};
pub use item::Item;
pub use store::Transcript;

pub fn daemon() -> anyhow::Result<(String, String)> {
    let base = std::env::var("AGENTHAIL_API_URL").unwrap_or_else(|_| "http://127.0.0.1:7412".into());
    let token = match std::env::var("AGENTHAIL_API_TOKEN") {
        Ok(token) => token,
        Err(_) => {
            let home = std::env::var("HOME")?;
            std::fs::read_to_string(format!("{home}/.agenthail/dashboard.token"))?.trim().to_string()
        }
    };
    Ok((base, token))
}

pub fn dashboard_token() -> Option<String> {
    let home = std::env::var("HOME").ok()?;
    let token = std::fs::read_to_string(format!("{home}/.agenthail/dashboard.token")).ok()?;
    Some(token.trim().to_string()).filter(|t| !t.is_empty())
}
