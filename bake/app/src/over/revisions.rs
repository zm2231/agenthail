use std::collections::HashMap;

pub struct Revisions<T> {
    seen: HashMap<String, (T, u64)>,
    next: u64,
}

impl<T> Default for Revisions<T> {
    fn default() -> Self {
        Revisions { seen: HashMap::new(), next: 0 }
    }
}

impl<T: PartialEq + Clone> Revisions<T> {
    pub fn of(&mut self, id: &str, value: &T) -> u64 {
        if let Some((_, rev)) = self.seen.get(id).filter(|(seen, _)| seen == value) {
            return *rev;
        }
        self.next += 1;
        self.seen.insert(id.to_string(), (value.clone(), self.next));
        self.next
    }

    pub fn keep(&mut self, listed: impl Fn(&str) -> bool) {
        self.seen.retain(|id, _| listed(id));
    }
}
