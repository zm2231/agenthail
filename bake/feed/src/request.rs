use std::collections::BTreeMap;

use serde::Deserialize;
use serde_json::{Value, json};
use time::{OffsetDateTime, UtcOffset};

use crate::background::Clock;
use crate::catalog::{Client, OpenRequest, RequestQuestion, RespondError, confirmed, parse_time};

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Elicitation {
    #[serde(default)]
    pub server: String,
    #[serde(default)]
    pub message: String,
    #[serde(default)]
    pub mode: String,
    #[serde(default)]
    pub url: String,
    #[serde(default)]
    pub fields: Vec<Field>,
}

impl Elicitation {
    pub fn asker(&self) -> String {
        if self.server.trim().is_empty() { "An MCP server".into() } else { self.server.clone() }
    }

    pub fn is_link(&self) -> bool {
        self.mode == "url"
    }

    pub fn web_url(&self) -> Option<String> {
        let (scheme, rest) = self.url.split_once("://")?;
        let scheme = scheme.to_ascii_lowercase();
        matches!(scheme.as_str(), "https" | "http").then(|| format!("{scheme}://{rest}"))
    }
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Field {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub description: String,
    #[serde(default, rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub required: bool,
    #[serde(default)]
    pub multi_select: bool,
    #[serde(default)]
    pub options: Vec<FieldOption>,
    #[serde(default)]
    pub default: Option<Value>,
}

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
pub struct FieldOption {
    #[serde(default)]
    pub value: String,
    #[serde(default)]
    pub label: String,
}

impl Field {
    pub fn label(&self) -> String {
        let name = if self.title.trim().is_empty() { self.name.as_str() } else { self.title.as_str() };
        if self.required { format!("{name} *") } else { name.to_string() }
    }

    pub fn is_switch(&self) -> bool {
        self.kind == "boolean" && self.options.is_empty()
    }

    pub fn is_choice(&self) -> bool {
        !self.options.is_empty()
    }

    pub fn default_text(&self) -> String {
        match &self.default {
            Some(Value::String(s)) => s.clone(),
            Some(Value::Number(n)) => match n.as_f64() {
                Some(f) if f.fract() == 0.0 && f.abs() < 1e15 => format!("{}", f as i64),
                _ => n.to_string(),
            },
            _ => String::new(),
        }
    }

    fn default_on(&self) -> bool {
        matches!(self.default, Some(Value::Bool(true)))
    }

    fn default_picks(&self) -> Vec<String> {
        match &self.default {
            Some(Value::Array(values)) => values.iter().filter_map(|v| v.as_str().map(String::from)).collect(),
            Some(Value::String(s)) if !s.is_empty() => vec![s.clone()],
            _ => Vec::new(),
        }
    }
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Form {
    on: BTreeMap<String, bool>,
    picks: BTreeMap<String, Vec<String>>,
}

impl Form {
    pub fn is_on(&self, field: &Field) -> bool {
        self.on.get(&field.name).copied().unwrap_or_else(|| field.default_on())
    }

    pub fn flip(&mut self, field: &Field) {
        let next = !self.is_on(field);
        self.on.insert(field.name.clone(), next);
    }

    pub fn picked(&self, field: &Field) -> Vec<String> {
        self.picks.get(&field.name).cloned().unwrap_or_else(|| field.default_picks())
    }

    pub fn pick(&mut self, field: &Field, value: &str) {
        let mut picked = self.picked(field);
        if picked.iter().any(|v| v == value) {
            picked.retain(|v| v != value);
        } else if field.multi_select {
            picked.push(value.to_string());
        } else {
            picked = vec![value.to_string()];
        }
        self.picks.insert(field.name.clone(), picked);
    }

    pub fn content(&self, fields: &[Field], text: impl Fn(&Field) -> String) -> Option<Value> {
        let mut content = serde_json::Map::new();
        for field in fields {
            if field.multi_select {
                let picked = self.picked(field);
                let values: Vec<String> = field.options.iter().map(|o| o.value.clone()).filter(|v| picked.contains(v)).collect();
                if field.required && values.is_empty() {
                    return None;
                }
                content.insert(field.name.clone(), json!(values));
                continue;
            }
            if field.is_switch() {
                content.insert(field.name.clone(), json!(self.is_on(field)));
                continue;
            }
            let words = if field.is_choice() { self.picked(field).into_iter().next().unwrap_or_default() } else { text(field) };
            let words = words.trim();
            if words.is_empty() {
                if field.required {
                    return None;
                }
                continue;
            }
            let value = match field.kind.as_str() {
                "integer" => json!(words.parse::<i64>().ok()?),
                "number" => json!(words.parse::<f64>().ok().filter(|f| f.is_finite())?),
                "boolean" => json!(words == "true"),
                _ => json!(words),
            };
            content.insert(field.name.clone(), value);
        }
        Some(Value::Object(content))
    }
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Response {
    pub choices: Vec<String>,
    pub answers: BTreeMap<String, String>,
    pub message: String,
    pub content: Option<Value>,
}

impl Response {
    pub fn choose(id: &str) -> Response {
        Response { choices: vec![id.to_string()], ..Default::default() }
    }

    pub fn picks(choices: Vec<String>, answers: BTreeMap<String, String>) -> Response {
        Response { choices, answers, ..Default::default() }
    }

    pub fn with_message(id: &str, message: &str) -> Response {
        Response { choices: vec![id.to_string()], message: message.trim().to_string(), ..Default::default() }
    }

    pub fn form(content: Value) -> Response {
        Response { choices: vec!["allow".into()], content: Some(content), ..Default::default() }
    }

    fn body(&self) -> Value {
        let mut decision = json!({"choices": self.choices, "answers": self.answers});
        if !self.message.is_empty() {
            decision["message"] = json!(self.message);
        }
        if let Some(content) = &self.content {
            decision["content"] = content.clone();
        }
        decision
    }
}

pub const REPLY: &str = "reply";
const DENYING: [&str; 3] = ["deny", "never", "stop"];

pub fn is_denial(id: &str) -> bool {
    DENYING.contains(&id)
}

impl OpenRequest {
    pub fn is_input(&self) -> bool {
        self.kind == "input" && self.elicitation.is_some()
    }

    pub fn uses_pager(&self) -> bool {
        self.is_compound() || self.kind == "ask" && self.questions.as_ref().is_some_and(|q| !q.is_empty())
    }

    pub fn takes_note(&self) -> bool {
        self.kind == "ask" && !self.options_only
    }

    pub fn shown_detail(&self) -> Option<String> {
        if !self.detail.trim().is_empty() {
            return Some(self.detail.clone());
        }
        match &self.input {
            None | Some(Value::Null) => None,
            Some(Value::Object(fields)) if fields.is_empty() => None,
            Some(input) => Some(serde_json::to_string_pretty(input).unwrap_or_default()),
        }
    }

    pub fn takes_reply(&self) -> bool {
        self.actions.iter().any(|a| a.id == REPLY)
    }

    pub fn deadline_label(&self, now: OffsetDateTime, clock: Clock, offset_at: impl Fn(OffsetDateTime) -> i32) -> Option<String> {
        let at = parse_time(self.deadline.as_deref()?)?;
        let local = |t: OffsetDateTime| t.to_offset(UtcOffset::from_whole_seconds(offset_at(t)).unwrap_or(UtcOffset::UTC));
        let (at, now) = (local(at), local(now));
        let time = clock.show(at.hour(), at.minute());
        if at.date() == now.date() {
            return Some(format!("Answer by {time}"));
        }
        let month = at.month().to_string();
        Some(format!("Answer by {} {}, {time}", &month[..3], at.day()))
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PagerKey {
    Pick(usize),
    Back,
    Forward,
    Send,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Paged {
    pub step: usize,
    pub send: bool,
}

pub fn page(questions: &[RequestQuestion], step: usize, key: PagerKey, picks: &mut Vec<String>, one_tap: bool) -> Paged {
    let single = questions.len() <= 1;
    let review = if single { 0 } else { questions.len() };
    let step = step.min(review);
    match key {
        PagerKey::Back => Paged { step: step.saturating_sub(1), send: false },
        PagerKey::Forward => Paged { step: (step + 1).min(review), send: false },
        PagerKey::Send if single || step == review => Paged { step, send: true },
        PagerKey::Send => Paged { step: step + 1, send: false },
        PagerKey::Pick(n) => {
            let Some(q) = questions.get(step) else { return Paged { step, send: false } };
            let Some(option) = n.checked_sub(1).and_then(|i| q.options.get(i)) else { return Paged { step, send: false } };
            if q.multi_select {
                if picks.contains(&option.id) {
                    picks.retain(|p| p != &option.id);
                } else {
                    picks.push(option.id.clone());
                }
                return Paged { step, send: false };
            }
            picks.retain(|p| !q.options.iter().any(|o| &o.id == p));
            picks.push(option.id.clone());
            Paged { step: (step + 1).min(review), send: one_tap && single }
        }
    }
}

impl crate::decisions::Decision {
    pub fn parked_reply(&self, words: &str) -> String {
        let covered: Vec<&str> = if self.covers.is_empty() { vec![self.work_item.as_str()] } else { self.covers.iter().map(|c| c.work_item.as_str()).collect() };
        let ids: Vec<&str> = covered.into_iter().filter(|i| !i.is_empty()).collect();
        if ids.is_empty() { words.to_string() } else { format!("Answer for {}: {words}", ids.join(", ")) }
    }
}

impl Client {
    pub fn answer(&self, session: &str, request: &str, response: &Response) -> Result<(), RespondError> {
        self.action("respond", json!({"sessionId": session, "requestId": request, "decision": response.body()})).and_then(confirmed).map(drop)
    }

    pub fn withdraw_request(&self, session: &str, request: &str) -> Result<(), RespondError> {
        self.action("request-withdraw", json!({"sessionId": session, "requestId": request})).and_then(confirmed).map(drop)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::RequestOption;

    fn field(value: Value) -> Field {
        serde_json::from_value(value).unwrap()
    }

    fn request(value: Value) -> OpenRequest {
        serde_json::from_value(value).unwrap()
    }

    #[test]
    fn a_form_request_reads_its_fields_deadline_and_replacement() {
        let r = request(json!({
            "id": "e1", "kind": "input", "tool": "mcp", "createdAt": "2026-11-26T15:00:00Z", "deadline": "2026-11-26T16:30:00Z", "replacedBy": "lead-2",
            "elicitation": {"server": "github", "message": "Which repo?", "mode": "form", "fields": [
                {"name": "repo", "title": "Repository", "type": "string", "required": true},
                {"name": "count", "type": "integer", "default": 3},
                {"name": "private", "type": "boolean", "default": true},
                {"name": "tags", "type": "string", "multiSelect": true, "options": [{"value": "a", "label": "A"}, {"value": "b", "label": "B"}]}
            ]},
            "actions": [{"id": "deny", "title": "Decline"}]
        }));
        assert!(r.is_input());
        assert_eq!(r.replaced_by, "lead-2");
        let e = r.elicitation.as_ref().unwrap();
        assert_eq!(e.asker(), "github");
        assert_eq!(e.fields.len(), 4);
        assert_eq!(e.fields[0].label(), "Repository *");
        assert_eq!(e.fields[1].kind, "integer");
        assert_eq!(e.fields[1].default_text(), "3");
        assert!(e.fields[2].is_switch());
        assert!(e.fields[3].multi_select && e.fields[3].is_choice());
        let utc = |_: OffsetDateTime| 0;
        let now = parse_time("2026-11-26T15:10:00Z").unwrap();
        assert_eq!(r.deadline_label(now, Clock::Twelve, utc).as_deref(), Some("Answer by 4:30 PM"));
        assert_eq!(r.deadline_label(now, Clock::TwentyFour, utc).as_deref(), Some("Answer by 16:30"));
        let next_day = parse_time("2026-11-25T15:10:00Z").unwrap();
        assert_eq!(r.deadline_label(next_day, Clock::TwentyFour, utc).as_deref(), Some("Answer by Nov 26, 16:30"));
        let west = |_: OffsetDateTime| -8 * 3600;
        assert_eq!(r.deadline_label(now, Clock::Twelve, west).as_deref(), Some("Answer by 8:30 AM"));
        assert_eq!(request(json!({"id": "p"})).deadline_label(now, Clock::Twelve, utc), None);
    }

    #[test]
    fn a_link_request_opens_only_web_links_whatever_their_case() {
        let link = |url: &str| Elicitation { mode: "url".into(), url: url.into(), ..Default::default() };
        assert_eq!(link("HTTPS://Example.com/Auth?X=1").web_url().as_deref(), Some("https://Example.com/Auth?X=1"));
        assert_eq!(link("http://a.test").web_url().as_deref(), Some("http://a.test"));
        assert_eq!(link("file:///etc/passwd").web_url(), None);
        assert_eq!(link("example.com").web_url(), None);
    }

    #[test]
    fn a_form_sends_typed_values_and_holds_back_until_required_ones_are_set() {
        let fields = vec![
            field(json!({"name": "repo", "type": "string", "required": true})),
            field(json!({"name": "count", "type": "integer", "default": 3})),
            field(json!({"name": "ratio", "type": "number"})),
            field(json!({"name": "private", "type": "boolean", "default": true})),
            field(json!({"name": "tags", "type": "string", "multiSelect": true, "required": true, "options": [{"value": "a", "label": "A"}, {"value": "b", "label": "B"}]})),
            field(json!({"name": "size", "type": "string", "options": [{"value": "s", "label": "Small"}, {"value": "l", "label": "Large"}]})),
        ];
        let mut form = Form::default();
        let mut words: BTreeMap<String, String> = BTreeMap::new();
        let text = |w: &BTreeMap<String, String>| {
            let w = w.clone();
            move |f: &Field| w.get(&f.name).cloned().unwrap_or_else(|| f.default_text())
        };
        assert_eq!(form.content(&fields, text(&words)), None, "repo and tags are required");
        words.insert("repo".into(), "  agenthail ".into());
        assert_eq!(form.content(&fields, text(&words)), None, "tags still empty");
        form.pick(&fields[4], "b");
        form.pick(&fields[4], "a");
        let sent = form.content(&fields, text(&words)).unwrap();
        assert_eq!(sent, json!({"repo": "agenthail", "count": 3, "private": true, "tags": ["a", "b"]}), "options keep their listed order; empty optional fields are left out");
        form.flip(&fields[3]);
        form.pick(&fields[5], "l");
        words.insert("ratio".into(), "0.5".into());
        let sent = form.content(&fields, text(&words)).unwrap();
        assert_eq!(sent["private"], json!(false));
        assert_eq!(sent["size"], json!("l"));
        assert_eq!(sent["ratio"], json!(0.5));
        form.pick(&fields[5], "l");
        assert!(form.content(&fields, text(&words)).unwrap().get("size").is_none(), "picking a choice again clears it");
        words.insert("count".into(), "three".into());
        assert_eq!(form.content(&fields, text(&words)), None, "an integer field must hold a whole number");
        words.insert("count".into(), "".into());
        assert!(form.content(&fields, text(&words)).unwrap().get("count").is_none(), "a cleared optional field is left out");
    }

    fn question(id: &str, options: &[&str], multi: bool) -> RequestQuestion {
        RequestQuestion {
            question: format!("{id}?"),
            multi_select: multi,
            options: options.iter().map(|o| RequestOption { id: format!("{id}-{o}"), label: o.to_string(), ..Default::default() }).collect(),
            ..Default::default()
        }
    }

    #[test]
    fn pager_keys_pick_move_and_send() {
        let qs = vec![question("a", &["x", "y"], false), question("b", &["p", "q", "r"], true), question("c", &["m"], false)];
        let mut picks = Vec::new();
        assert_eq!(page(&qs, 0, PagerKey::Pick(2), &mut picks, true), Paged { step: 1, send: false }, "a single choice moves on");
        assert_eq!(picks, vec!["a-y"]);
        assert_eq!(page(&qs, 1, PagerKey::Pick(1), &mut picks, true), Paged { step: 1, send: false }, "a multiple choice stays to take more");
        page(&qs, 1, PagerKey::Pick(3), &mut picks, true);
        page(&qs, 1, PagerKey::Pick(1), &mut picks, true);
        assert_eq!(picks, vec!["a-y", "b-r"], "picking again unpicks");
        assert_eq!(page(&qs, 1, PagerKey::Pick(9), &mut picks, true), Paged { step: 1, send: false }, "a number past the options does nothing");
        assert_eq!(page(&qs, 1, PagerKey::Back, &mut picks, true).step, 0);
        assert_eq!(page(&qs, 0, PagerKey::Back, &mut picks, true).step, 0);
        assert_eq!(page(&qs, 2, PagerKey::Forward, &mut picks, true).step, 3, "past the last question is the review");
        assert_eq!(page(&qs, 3, PagerKey::Forward, &mut picks, true).step, 3);
        page(&qs, 0, PagerKey::Pick(1), &mut picks, true);
        assert_eq!(picks, vec!["b-r", "a-x"], "a new single pick replaces the old one");
        assert_eq!(page(&qs, 2, PagerKey::Pick(1), &mut picks, true), Paged { step: 3, send: false }, "the last pick goes to the review, never sends");
        assert_eq!(page(&qs, 0, PagerKey::Send, &mut picks, true), Paged { step: 1, send: false }, "Return moves on");
        assert_eq!(page(&qs, 2, PagerKey::Send, &mut picks, true), Paged { step: 3, send: false }, "Return on the last question goes to the review");
        assert_eq!(page(&qs, 3, PagerKey::Send, &mut picks, true), Paged { step: 3, send: true }, "Return on the review sends");
        assert_eq!(page(&qs, 3, PagerKey::Pick(1), &mut picks, true), Paged { step: 3, send: false }, "the review has no options");
        let one = vec![question("solo", &["yes", "no"], false)];
        let mut picks = Vec::new();
        assert!(page(&one, 0, PagerKey::Pick(1), &mut picks, true).send, "one question answers on a tap");
        assert!(!page(&one, 0, PagerKey::Pick(1), &mut picks, false).send, "an ask that takes a note waits for Return");
        assert!(page(&one, 0, PagerKey::Send, &mut picks, false).send, "one question sends on Return");
        assert!(!page(&[], 0, PagerKey::Pick(1), &mut Vec::new(), true).send);
    }

    #[test]
    fn a_parked_reply_names_the_work_it_answers() {
        use crate::decisions::Decision;
        let one = Decision { work_item: "W7".into(), ..Default::default() };
        assert_eq!(one.parked_reply("use the cache"), "Answer for W7: use the cache");
        let both = Decision { work_item: "W7".into(), covers: vec![Decision { work_item: "W7".into(), ..Default::default() }, Decision { work_item: "W9".into(), ..Default::default() }], ..Default::default() };
        assert_eq!(both.parked_reply("yes"), "Answer for W7, W9: yes");
        assert_eq!(Decision::default().parked_reply("hi"), "hi");
    }

    #[test]
    fn an_approval_shows_its_detail_or_else_its_input() {
        assert_eq!(request(json!({"id": "p", "detail": "npm publish", "input": {"command": "npm publish"}})).shown_detail().as_deref(), Some("npm publish"));
        assert_eq!(request(json!({"id": "p", "input": {"command": "ls"}})).shown_detail().as_deref(), Some("{\n  \"command\": \"ls\"\n}"));
        assert_eq!(request(json!({"id": "p", "input": {}})).shown_detail(), None);
        assert_eq!(request(json!({"id": "p"})).shown_detail(), None);
    }

    #[test]
    fn an_agents_ask_waits_for_send_and_takes_a_note() {
        let ask = request(json!({"id": "a", "kind": "ask", "questions": [{"question": "q?", "options": [{"id": "o", "label": "o"}]}]}));
        assert!(ask.uses_pager() && ask.takes_note());
        let fixed = request(json!({"id": "a", "kind": "ask", "optionsOnly": true, "questions": [{"question": "q?", "options": [{"id": "o", "label": "o"}]}]}));
        assert!(fixed.uses_pager() && !fixed.takes_note());
        let tool = request(json!({"id": "q", "kind": "question", "questions": [{"question": "q?", "options": [{"id": "o", "label": "o"}]}]}));
        assert!(!tool.uses_pager() && !tool.takes_note(), "a provider's single question answers on a tap");
        assert!(!request(json!({"id": "a", "kind": "ask", "title": "t"})).uses_pager());
    }

    #[test]
    fn replies_and_denials_are_told_apart() {
        let r = request(json!({"id": "p", "actions": [{"id": "allow"}, {"id": "deny"}, {"id": "reply", "title": "Deny with message…"}]}));
        assert!(r.takes_reply());
        assert!(!r.is_input());
        assert!(is_denial("never") && is_denial("stop") && !is_denial("allow"));
        assert_eq!(Response::with_message("reply", "  use the staging db ").body(), json!({"choices": ["reply"], "answers": {}, "message": "use the staging db"}));
        assert_eq!(Response::form(json!({"repo": "a"})).body(), json!({"choices": ["allow"], "answers": {}, "content": {"repo": "a"}}));
    }
}
