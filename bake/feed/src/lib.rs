pub mod action;
pub mod blocks;
pub mod question;
pub mod settings;
pub mod catalog;
pub mod create;
pub mod composer;
pub mod derive;
pub mod edits;
pub mod focus;
pub mod item;
pub mod limit;
pub mod menu;
pub mod prefer;
pub mod source;
pub mod store;
pub mod summary;
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
