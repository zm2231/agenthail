pub fn plain(text: &str) -> String {
    text.replace(" — ", ", ").replace(" – ", ", ").replace('—', "--").replace('–', "-")
}

pub fn first_sentence(text: &str, max: usize) -> String {
    let t = text.trim();
    let cut = t.find(". ").map(|i| &t[..i]).unwrap_or(t);
    let cut = cut.trim_end_matches('.');
    if cut.chars().count() > max {
        let s: String = cut.chars().take(max).collect();
        format!("{}…", s.trim_end())
    } else {
        cut.to_string()
    }
}

pub fn clip_words(t: &str, max: usize) -> String {
    let t = t.split([':', ';', '.']).next().unwrap_or(t).trim();
    if t.chars().count() <= max {
        return t.to_string();
    }
    let mut out = String::new();
    for w in t.split_whitespace() {
        if out.chars().count() + w.chars().count() + 1 > max {
            break;
        }
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(w);
    }
    format!("{}…", out.trim_end_matches([',', ' ']))
}

pub fn short_title(t: &str, max: usize) -> String {
    if t.chars().count() > max { format!("{}…", t.chars().take(max).collect::<String>().trim_end()) } else { t.to_string() }
}

pub fn capital(t: &str) -> String {
    let mut c = t.chars();
    c.next().map(|f| f.to_uppercase().collect::<String>() + c.as_str()).unwrap_or_default()
}

pub fn short_id(id: &str) -> String {
    id.rsplit('-').next().unwrap_or(id).to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn clip_words_stops_at_a_word_and_the_first_clause() {
        assert_eq!(clip_words("Ship the board: lanes and caps", 40), "Ship the board");
        assert_eq!(clip_words("alpha beta gamma delta", 12), "alpha beta…");
    }

    #[test]
    fn short_id_keeps_the_ledger_number() {
        assert_eq!(short_id("agenthail-W595"), "W595");
    }
}
