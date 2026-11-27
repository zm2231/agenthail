use crate::catalog::Session;

use super::text::{first_sentence, plain};
use super::world::{Work, WorkKey, World};

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
    if submitted.is_empty() {
        return Vec::new();
    }
    w.timeline.iter().filter(|t| t.kind == "comment" && t.at > submitted).map(|t| first_sentence(&plain(t.text.as_deref().unwrap_or("")), 260)).collect()
}

impl World {
    pub fn also_holds<'a>(&'a self, s: &Session, except: &WorkKey) -> Vec<&'a Work> {
        s.held_work.iter().map(|h| h.key()).filter(|k| k != except).filter_map(|k| self.work_item(&k)).collect()
    }
}

#[cfg(test)]
mod unchecked_tests {
    use super::unchecked;
    use crate::derive::world::Work;

    fn work(timeline: serde_json::Value) -> Work {
        serde_json::from_value(serde_json::json!({"id": "w", "timeline": timeline})).unwrap()
    }

    #[test]
    fn a_comment_on_work_never_submitted_is_not_raised_after_submission() {
        let w = work(serde_json::json!([{"at": "2026-11-27T01:00:00Z", "kind": "comment", "text": "A note."}]));
        assert!(unchecked(&w).is_empty());
    }

    #[test]
    fn only_comments_after_the_submission_count() {
        let w = work(serde_json::json!([
            {"at": "2026-11-27T01:00:00Z", "kind": "comment", "text": "Before."},
            {"at": "2026-11-27T02:00:00Z", "kind": "state", "state": "in_review"},
            {"at": "2026-11-27T03:00:00Z", "kind": "comment", "text": "After."}
        ]));
        assert_eq!(unchecked(&w), vec!["After".to_string()]);
    }
}
