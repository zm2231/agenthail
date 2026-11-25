use std::collections::HashSet;

use super::text::{capital, first_sentence, plain};
use super::world::{Entry, Work};

pub struct Proof {
    pub expected: String,
    pub observed: String,
    pub passed: Option<bool>,
}

fn clauses(acceptance: &str) -> Vec<String> {
    acceptance
        .split("; ")
        .flat_map(|c| c.split(". "))
        .map(|c| c.rsplit(": ").next().unwrap_or(c).trim().trim_end_matches('.').to_string())
        .filter(|c| c.len() > 12)
        .collect()
}

pub fn proof(w: &Work) -> Vec<Proof> {
    let results: Vec<&Entry> = w.timeline.iter().filter(|t| t.kind == "log" && t.log_type.as_deref() == Some("result")).collect();
    let mut steps = Vec::new();
    let mut used = HashSet::new();
    for clause in clauses(&w.acceptance) {
        let words: Vec<String> = clause.to_lowercase().split(|c: char| !c.is_alphanumeric()).filter(|x| x.len() > 3).map(String::from).collect();
        let best = results
            .iter()
            .enumerate()
            .rev()
            .filter(|(i, _)| !used.contains(i))
            .map(|(i, r)| {
                let body = r.text.as_deref().unwrap_or("").to_lowercase();
                (i, words.iter().filter(|w| body.contains(w.as_str())).count(), r)
            })
            .find(|(_, n, _)| *n >= 2);
        let expected = capital(&plain(&clause));
        match best {
            Some((i, _, r)) => {
                used.insert(i);
                let body = r.text.clone().unwrap_or_default();
                let l = body.to_lowercase();
                let pass = !(l.contains("failed") && !l.contains("since fixed")) || l.contains("exit 0");
                steps.push(Proof { expected, observed: first_sentence(&plain(&body), 220), passed: Some(pass) });
            }
            None => steps.push(Proof { expected, observed: "Nothing in the record checks this.".into(), passed: None }),
        }
    }
    if let Some(last) = results.last().filter(|_| !used.contains(&(results.len() - 1))) {
        let body = last.text.clone().unwrap_or_default();
        let l = body.to_lowercase();
        steps.push(Proof { expected: "The full test suite passes on the final commit".into(), observed: first_sentence(&plain(&body), 220), passed: Some(l.contains("exit 0") || l.contains("ok")) });
    }
    steps
}
