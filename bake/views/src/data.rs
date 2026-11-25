pub use feed::derive::text::{clip_words, plain, short_id};
pub use feed::derive::{Ask, Happened, Line, Need, NeedKind, PHASES, Project, Target, Work, World};

pub const FIXTURE: &str = include_str!("../fixture/data.json");
