use crate::item::Item;

fn is_bookkeeping(item: &Item) -> bool {
    matches!(item.kind.as_str(), "done" | "notice") || (item.kind == "event" && item.title == "Turn duration") || (matches!(item.kind.as_str(), "message" | "text") && item.text.trim().is_empty())
}

pub fn open_question(items: &[Item], limit: usize) -> Vec<(String, String)> {
    items
        .iter()
        .rev()
        .filter(|item| !is_bookkeeping(item))
        .map_while(|item| (matches!(item.kind.as_str(), "message" | "text") && !item.role_is("user")).then(|| (item.id.clone(), item.text.clone())))
        .take(limit)
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn item(id: &str, kind: &str, role: &str) -> Item {
        Item { id: id.into(), kind: kind.into(), role: Some(role.into()), text: format!("{id} text"), ..Default::default() }
    }

    #[test]
    fn a_trailing_assistant_message_is_open() {
        let items = [item("q", "message", "assistant"), item("d", "done", "")];
        assert_eq!(open_question(&items, 2), vec![("q".to_string(), "q text".to_string())]);
    }

    #[test]
    fn a_reply_or_later_work_closes_it() {
        assert!(open_question(&[item("q", "message", "assistant"), item("u", "message", "user")], 2).is_empty());
        assert!(open_question(&[item("q", "message", "assistant"), item("t", "toolCall", "assistant")], 2).is_empty());
        assert!(open_question(&[item("q", "message", "assistant"), item("r", "toolResult", "user")], 2).is_empty());
    }

    #[test]
    fn text_items_and_turn_bookkeeping_keep_it_open() {
        let mut duration = item("e", "event", "");
        duration.title = "Turn duration".into();
        let items = [item("q", "text", "assistant"), duration, item("n", "notice", ""), item("d", "done", "")];
        assert_eq!(open_question(&items, 2).len(), 1);
    }

    #[test]
    fn a_new_question_after_an_answer_is_open() {
        let items = [item("q1", "message", "assistant"), item("u", "message", "user"), item("q2", "message", "assistant")];
        assert_eq!(open_question(&items, 2).len(), 1);
        assert_eq!(open_question(&items, 2)[0].0, "q2");
    }
}
