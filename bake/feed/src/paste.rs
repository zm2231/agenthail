use std::collections::HashMap;
use std::ops::{Range, RangeInclusive};
use std::sync::{LazyLock, Mutex, MutexGuard};

pub const LINE_LIMIT: usize = 8;
pub const CHARACTER_LIMIT: usize = 600;

const LEADS: RangeInclusive<u32> = 0xFDD0..=0xFDEF;
const IDS: RangeInclusive<u32> = 0xE000..=0xF6FF;

pub fn is_long(text: &str) -> bool {
    text.chars().count() > CHARACTER_LIMIT || lines(text) > LINE_LIMIT
}

pub fn lines(text: &str) -> usize {
    let trimmed = text.trim_end_matches('\n');
    if trimmed.is_empty() { 0 } else { trimmed.split('\n').count() }
}

pub fn label(text: &str) -> String {
    match lines(text) {
        n if n > 1 => format!("Pasted text · {n} lines"),
        _ => format!("Pasted text · {} characters", text.chars().count()),
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Piece {
    Plain(String),
    Token(String),
}

// A marker is a noncharacter, which Unicode keeps out of interchanged text, then a private-use id.
#[derive(Default)]
pub struct Store {
    texts: HashMap<String, String>,
    issued: u32,
}

pub fn shared() -> MutexGuard<'static, Store> {
    static SHARED: LazyLock<Mutex<Store>> = LazyLock::new(Mutex::default);
    SHARED.lock().unwrap_or_else(|e| e.into_inner())
}

impl Store {
    pub fn add(&mut self, text: &str) -> Option<String> {
        let per_lead = IDS.end() - IDS.start() + 1;
        let leads = LEADS.end() - LEADS.start() + 1;
        if self.issued >= leads * per_lead {
            return None;
        }
        let lead = char::from_u32(LEADS.start() + self.issued / per_lead)?;
        let id = char::from_u32(IDS.start() + self.issued % per_lead)?;
        self.issued += 1;
        let marker = format!("{lead}{id}");
        self.texts.insert(marker.clone(), text.to_string());
        Some(marker)
    }

    pub fn text(&self, marker: &str) -> Option<&str> {
        self.texts.get(marker).map(String::as_str)
    }

    pub fn pieces(&self, draft: &str) -> Vec<Piece> {
        if !draft.chars().any(|c| LEADS.contains(&(c as u32))) {
            return vec![Piece::Plain(draft.to_string())];
        }
        let mut out = Vec::new();
        let mut plain = String::new();
        let mut chars = draft.chars().peekable();
        while let Some(c) = chars.next() {
            let marker = chars.peek().filter(|_| LEADS.contains(&(c as u32))).map(|id| format!("{c}{id}")).filter(|m| self.texts.contains_key(m));
            match marker {
                Some(marker) => {
                    chars.next();
                    if !plain.is_empty() {
                        out.push(Piece::Plain(std::mem::take(&mut plain)));
                    }
                    out.push(Piece::Token(marker));
                }
                None => plain.push(c),
            }
        }
        if !plain.is_empty() {
            out.push(Piece::Plain(plain));
        }
        out
    }

    pub fn contains_marker(&self, draft: &str) -> bool {
        self.pieces(draft).iter().any(|p| matches!(p, Piece::Token(_)))
    }

    pub fn expand(&self, draft: &str) -> String {
        self.pieces(draft).into_iter().fold(String::new(), |mut out, piece| {
            match piece {
                Piece::Plain(plain) => out.push_str(&plain),
                Piece::Token(marker) => out.push_str(self.text(&marker).unwrap_or_default()),
            }
            out
        })
    }

    pub fn draft<'a>(&self, text: &str, pills: impl IntoIterator<Item = (Range<usize>, &'a str)>) -> String {
        let mut out = String::with_capacity(text.len());
        let mut at = 0;
        for (range, id) in pills {
            if range.start < at || range.end > text.len() || !self.texts.contains_key(id) {
                continue;
            }
            out.push_str(&text[at..range.start]);
            out.push_str(id);
            at = range.end;
        }
        out.push_str(&text[at..]);
        out
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_paste_is_long_past_six_hundred_characters_or_eight_lines() {
        assert!(!is_long(&"a".repeat(600)));
        assert!(is_long(&"a".repeat(601)));
        assert!(!is_long(&"line\n".repeat(8)));
        assert!(is_long(&"line\n".repeat(9)));
        assert!(!is_long(&"é".repeat(600)), "characters, not bytes");
    }

    #[test]
    fn trailing_newlines_are_not_lines() {
        assert_eq!(lines(""), 0);
        assert_eq!(lines("\n\n"), 0);
        assert_eq!(lines("a\n\n"), 1);
        assert_eq!(lines("a\n\nb"), 3);
    }

    #[test]
    fn the_label_counts_lines_or_characters() {
        assert_eq!(label("a\nb\nc\n"), "Pasted text · 3 lines");
        assert_eq!(label(&"x".repeat(700)), "Pasted text · 700 characters");
    }

    #[test]
    fn a_draft_with_tokens_expands_to_the_pasted_text() {
        let mut store = Store::default();
        let pasted = "fn main() {\n}\n".repeat(10);
        let marker = store.add(&pasted).unwrap();
        let draft = format!("look at this: {marker} thanks");
        assert!(store.contains_marker(&draft));
        assert_eq!(store.pieces(&draft), vec![Piece::Plain("look at this: ".into()), Piece::Token(marker.clone()), Piece::Plain(" thanks".into())]);
        assert_eq!(store.expand(&draft), format!("look at this: {pasted} thanks"));
    }

    #[test]
    fn markers_are_distinct_and_unknown_ones_stay_plain() {
        let mut store = Store::default();
        let a = store.add("one").unwrap();
        let b = store.add("two").unwrap();
        assert_ne!(a, b);
        assert_eq!(a.chars().count(), 2);
        assert_eq!(store.expand(&format!("{a}{b}")), "onetwo");
        let other = Store::default();
        assert_eq!(other.expand(&a), a, "a marker another store issued is just text");
        assert!(!other.contains_marker(&a));
    }

    #[test]
    fn a_fields_pills_turn_back_into_markers() {
        let mut store = Store::default();
        let marker = store.add("long text").unwrap();
        let label = label("long text");
        let text = format!("see {label} ok");
        let pills = [(4..4 + label.len(), marker.as_str()), (0..3, "not a paste")];
        assert_eq!(store.draft(&text, pills), format!("see {marker} ok"));
        assert_eq!(store.expand(&store.draft(&text, [(4..4 + label.len(), marker.as_str())])), "see long text ok");
    }

    #[test]
    fn the_shared_store_hands_out_markers() {
        let marker = shared().add("kept").unwrap();
        assert_eq!(shared().text(&marker), Some("kept"));
    }
}
