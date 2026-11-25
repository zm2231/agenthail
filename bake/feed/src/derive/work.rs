use crate::catalog::Session;

use super::text::{first_sentence, plain};
use super::world::{Work, World};

fn is_commit(word: &str) -> bool {
    (7..=12).contains(&word.len()) && word.chars().all(|c| c.is_ascii_hexdigit()) && word.chars().any(|c| c.is_ascii_digit()) && word.chars().any(|c| c.is_ascii_alphabetic())
}

pub fn commit(w: &Work) -> Option<String> {
    w.timeline.iter().rev().filter(|t| t.kind == "log").flat_map(|t| t.text.as_deref().unwrap_or("").split(|c: char| !c.is_ascii_alphanumeric())).find(|w| is_commit(w)).map(String::from)
}

fn digits_after(text: &str, at: usize) -> String {
    text[at..].chars().take_while(|c| c.is_ascii_digit()).collect()
}

pub fn pr(w: &Work) -> Option<String> {
    for t in w.timeline.iter().rev() {
        let text = t.text.as_deref().unwrap_or("");
        if let Some(n) = text.find("pull/").map(|i| digits_after(text, i + 5)).filter(|n| !n.is_empty()) {
            return Some(n);
        }
        if let Some(n) = text.match_indices("PR #").map(|(i, _)| digits_after(text, i + 4)).find(|n| n.len() >= 3) {
            return Some(n);
        }
        if let Some(n) = text.match_indices('#').map(|(i, _)| digits_after(text, i + 1)).find(|n| n.len() == 4) {
            return Some(n);
        }
    }
    None
}

pub fn unchecked(w: &Work) -> Vec<String> {
    let submitted = w.submitted_at();
    w.timeline.iter().filter(|t| t.kind == "comment" && t.at > submitted).map(|t| first_sentence(&plain(t.text.as_deref().unwrap_or("")), 260)).collect()
}

impl World {
    pub fn also_holds<'a>(&'a self, s: &Session, except: &str) -> Vec<&'a Work> {
        s.held_work.iter().filter(|h| h.item_id != except).filter_map(|h| self.work.get(&h.item_id)).collect()
    }
}
