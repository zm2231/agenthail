use std::collections::{HashMap, HashSet};

use feed::catalog::OpenRequest;
use feed::request::{Field, Form, PagerKey, Response};
use rune::{Key, KeyEvent};
use rune_kit::Switch;

use super::*;

#[derive(Default)]
pub struct Asks {
    shown: HashMap<String, String>,
    step: HashMap<String, usize>,
    forms: HashMap<String, Form>,
    replying: HashSet<String>,
}

#[derive(Clone)]
pub(crate) enum Route {
    Session,
    Decision(feed::decisions::Decision),
}

impl Route {
    fn answer(&self, s: &mut App, request: &str, response: Response) {
        match self {
            Route::Session => answer_request(s, request, response),
            Route::Decision(d) => crate::over::inbox::answer(s, d, request, response),
        }
    }
}

pub(crate) struct Choice {
    label: String,
    detail: String,
    recommended: bool,
    request: Option<(String, Vec<String>)>,
    reply: String,
}

pub(crate) fn plain(text: &str, max: usize) -> String {
    let t = text.replace("**", "").replace('`', "");
    if t.chars().count() > max { format!("{}…", t.chars().take(max).collect::<String>()) } else { t }
}

pub(crate) fn recent_answers(s: &App, x: &Session) -> Vec<(String, String)> {
    s.panes.get(&x.id).map(|p| feed::question::open_question(p.transcript.items(), 2)).unwrap_or_default()
}

pub(crate) fn question_in(body: &str) -> Option<String> {
    let paragraphs: Vec<&str> = body.split("\n\n").map(str::trim).filter(|p| !p.is_empty()).collect();
    let pick = paragraphs.iter().rev().find(|p| p.contains('?')).or(paragraphs.last())?;
    Some(plain(pick, 320))
}

pub(crate) fn split_option(label: &str) -> (String, String) {
    let cut = [" — ", ", ", ": ", ". "].iter().filter_map(|sep| label.find(sep).map(|i| (i, sep.len()))).min_by_key(|(i, _)| *i);
    match cut {
        Some((i, len)) if i > 0 && i < 70 => (label[..i].trim().to_string(), label[i + len..].trim().to_string()),
        _ => (plain(label, 70), String::new()),
    }
}

pub(crate) fn open_requests<'a>(s: &'a App, x: &Session) -> Vec<&'a OpenRequest> {
    s.requests.get(&x.id).map(|r| r.iter().filter(|r| !s.is_settled(&r.id)).collect()).unwrap_or_default()
}

pub(crate) fn shown_of<'a>(s: &App, owner: &str, open: &[&'a OpenRequest]) -> Option<&'a OpenRequest> {
    let shown = s.asks.shown.get(owner);
    open.iter().find(|r| shown == Some(&r.id)).or(open.first()).copied()
}

pub(crate) fn open_request<'a>(s: &'a App, x: &Session) -> Option<&'a OpenRequest> {
    shown_of(s, &x.id, &open_requests(s, x))
}

pub(crate) fn switcher(owner: &str, open: &[&OpenRequest], shown: &str) -> Option<El> {
    if open.len() < 2 {
        return None;
    }
    let at = open.iter().position(|r| r.id == shown).unwrap_or(0);
    let step = |by: isize, glyph: &'static rune::Icon, key: &str| {
        let target = at.checked_add_signed(by).and_then(|i| open.get(i)).map(|r| r.id.clone());
        let owner = owner.to_string();
        let el = col().key(format!("ask-switch-{key}")).size(22.0, 22.0).radius(6.0).items_center().justify_center().fill(crate::theme::ink(0.06)).child(icon(glyph, 12.0).color(if target.is_some() { theme::text() } else { faint() }));
        match target {
            Some(id) => el.hover(crate::theme::ink(0.12)).on_press(move |s: &mut App| {
                s.asks.shown.insert(owner.clone(), id.clone());
            }),
            None => el.opacity(0.5),
        }
    };
    Some(row().key("ask-switcher").gap(6.0).items_center().child(step(-1, &icons::CHEVRON_LEFT, "back")).child(text(format!("{} of {}", at + 1, open.len())).font(META_BOLD).color(muted())).child(step(1, &icons::CHEVRON_RIGHT, "next")))
}

fn clock_line(s: &App, request: &OpenRequest) -> Option<El> {
    let offset = |t: time::OffsetDateTime| rune::time::LocalTime::at(t.unix_timestamp() * 1000).offset;
    request.deadline_label(s.now, super::background::clock(), offset).map(|t| row().key("request-deadline").gap(5.0).items_center().child(icon(&icons::CLOCK, 12.0).color(attention())).child(text(t).font(META_BOLD).color(attention())))
}

fn replaced_note(s: &App, request: &OpenRequest, route: &Route, busy: bool) -> Option<El> {
    if request.replaced_by.is_empty() {
        return None;
    }
    let lead = session(s, &request.replaced_by).map(|x| x.title()).unwrap_or_else(|| crate::over::short_id(&request.replaced_by));
    let mut note = row().key("request-replaced").w_full().gap(10.0).items_center().child(text(format!("From a replaced lead. {lead} leads this project now, so this request no longer needs you.")).font(META).color(muted()).wrap().grow(1.0).min_w(0.0));
    if matches!(route, Route::Session) {
        let id = request.id.clone();
        let withdraw = Button::new("Withdraw").variant(ButtonVariant::Neutral).build(theme::kit()).key("request-withdraw");
        note = note.child(if busy { withdraw.opacity(0.5) } else { withdraw.on_press(move |s: &mut App| crate::withdraw_request(s, &id)) });
    }
    Some(note)
}

pub(crate) fn has_settled(s: &App, x: &Session) -> bool {
    settled_request(s, x).is_some()
}

pub(crate) fn settled_request<'a>(s: &'a App, x: &Session) -> Option<&'a feed::catalog::OpenRequest> {
    let settled = s.requests.get(&x.id)?.iter().filter(|r| s.is_settled(&r.id));
    settled.max_by_key(|r| s.settled.get(&r.id).copied().flatten().map_or((1, None), |at| (0, Some(at))))
}

pub(crate) fn question_context(q: &feed::catalog::RequestQuestion) -> Option<El> {
    let links: Vec<&feed::catalog::RequestLink> = q.links.iter().filter(|l| l.is_web() && !l.title.trim().is_empty()).collect();
    if q.detail.trim().is_empty() && links.is_empty() {
        return None;
    }
    Some(
        col()
            .w_full()
            .gap(4.0)
            .children((!q.detail.trim().is_empty()).then(|| text(q.detail.trim().to_string()).font(META).color(muted()).wrap()))
            .children((!links.is_empty()).then(|| {
                row().gap(10.0).items_center().children(links.into_iter().enumerate().map(|(i, l)| {
                    let url = l.url.clone();
                    row()
                        .key(format!("link-{i}-{}", l.url))
                        .gap(4.0)
                        .items_center()
                        .child(icon(&icons::ARROW_RIGHT, 11.0).color(accent()))
                        .child(text(l.title.clone()).font(META_BOLD).color(accent()))
                        .on_press(move |_: &mut App| open_link(&url))
                }))
            })),
    )
}

fn pick_el(request: &str, (qi, count): (usize, usize), siblings: Vec<String>, option: &feed::catalog::RequestOption, multi: bool, picked: bool, recommended: bool, busy: bool) -> El {
    let label = option.label.replace(" (Recommended)", "").replace(" (recommended)", "");
    let mut el = row()
        .key(format!("pick-{}", option.id))
        .w_full()
        .gap(10.0)
        .items_center()
        .px(12.0)
        .py(8.0)
        .radius(10.0)
        .fill(if picked { attention().alpha(0.22) } else { crate::theme::ink(0.05) })
        .border(1.0, if picked { attention().alpha(0.6) } else { crate::theme::ink(0.10) })
        .hover(crate::theme::ink(0.10))
        .child(icon(if picked { &icons::CHECK_CIRCLE } else { &icons::TARGET }, 14.0).color(if picked { attention() } else { faint() }))
        .child(col().grow(1.0).min_w(0.0).gap(2.0).child(text(label).font(BODY_BOLD).color(theme::text()).wrap()).children((!option.description.is_empty()).then(|| text(option.description.clone()).font(META).color(muted()).wrap().max_lines(2))))
        .children(recommended.then(|| row().px(7.0).py(2.0).radius(6.0).fill(attention().alpha(0.2)).child(text("Recommended").font(META_BOLD).color(attention()))));
    if busy {
        return el.opacity(0.5);
    }
    let (request, id) = (request.to_string(), option.id.clone());
    el = el.on_press_tx(move |s: &mut App, tx: &mut Tx| {
        put_down_composer(s, tx);
        s.asks.step.insert(request.clone(), if multi || count < 2 { qi } else { qi + 1 });
        let was_typed = s.typed.remove(&format!("{request}|{qi}")).is_some_and(|t| !t.text.trim().is_empty());
        if was_typed {
            s.picks.entry(request.clone()).or_default().retain(|p| !siblings.contains(p));
        }
        if multi || !s.picks.get(&request).is_some_and(|p| p.contains(&id)) {
            toggle_pick(s, &request, &siblings, &id, multi)
        }
    });
    el
}

fn own_words(s: &App, key: String, placeholder: &str, submit: Option<Box<dyn Fn(&mut App, String)>>) -> El {
    let empty = rune::TextEdit::new("");
    let edit = s.typed.get(&key).unwrap_or(&empty);
    let lens = key.clone();
    let input = text_input(edit, META)
        .key(format!("other-{key}"))
        .w_full()
        .px(12.0)
        .py(8.0)
        .radius(10.0)
        .fill(crate::theme::ink(0.04))
        .border(1.0, crate::theme::ink(0.10))
        .color(theme::text())
        .placeholder(placeholder.to_string(), faint())
        .on_edit(move |s: &mut App| s.typed.entry(lens.clone()).or_insert_with(|| rune::TextEdit::new("")));
    match submit {
        Some(send) => input.on_submit(move |s: &mut App, _: &mut Tx| {
            let words = s.typed.get(&key).map(|t| t.text.trim().to_string()).unwrap_or_default();
            if !words.is_empty() {
                send(s, words);
            }
        }),
        None => input,
    }
}

pub(crate) fn compound_answer(s: &App, request: &OpenRequest) -> Option<Response> {
    let picks = s.picks.get(&request.id).cloned().unwrap_or_default();
    let questions = request.questions.clone().unwrap_or_default();
    let typed_for = |qi: usize| if request.options_only { String::new() } else { s.typed.get(&format!("{}|{qi}", request.id)).map(|t| t.text.trim().to_string()).unwrap_or_default() };
    let mut chosen: Vec<String> = picks.iter().filter(|p| questions.iter().any(|q| q.options.iter().any(|o| &o.id == *p))).cloned().collect();
    let mut answers = std::collections::BTreeMap::new();
    for (qi, q) in questions.iter().enumerate() {
        let own = typed_for(qi);
        if !own.is_empty() {
            chosen.retain(|p| !q.options.iter().any(|o| &o.id == p));
            answers.insert(q.question.clone(), own);
        }
    }
    let answered = |qi: usize, q: &feed::catalog::RequestQuestion| !typed_for(qi).is_empty() || q.options.iter().any(|o| picks.contains(&o.id));
    let ready = !questions.is_empty() && questions.iter().enumerate().all(|(qi, q)| answered(qi, q));
    let note = if request.takes_note() { s.typed.get(&format!("{}|note", request.id)).map(|t| t.text.trim().to_string()).unwrap_or_default() } else { String::new() };
    ready.then(|| Response { message: note, ..Response::picks(chosen, answers) })
}

fn compound_el(s: &App, request: &OpenRequest, route: &Route, busy: bool) -> El {
    let picks = s.picks.get(&request.id).cloned().unwrap_or_default();
    let questions = request.questions.clone().unwrap_or_default();
    let step = s.asks.step.get(&request.id).copied().unwrap_or(0);
    let reviewing = questions.len() > 1 && step >= questions.len();
    let mut list = col().w_full().gap(12.0).pt(4.0);
    let typed_for = |qi: usize| s.typed.get(&format!("{}|{qi}", request.id)).map(|t| t.text.trim().to_string()).unwrap_or_default();
    for (qi, q) in questions.iter().enumerate() {
        let key = format!("{}|{qi}", request.id);
        let siblings: Vec<String> = q.options.iter().map(|o| o.id.clone()).collect();
        let hint = if q.multi_select { "Pick any" } else { "Pick one" };
        let current = questions.len() > 1 && qi == step;
        let (rid, at) = (request.id.clone(), qi);
        list = list.child(
            col()
                .key(format!("question-{key}"))
                .w_full()
                .gap(6.0)
                .px(if questions.len() > 1 { 10.0 } else { 0.0 })
                .py(if questions.len() > 1 { 8.0 } else { 0.0 })
                .radius(12.0)
                .border(1.0, if current { attention().alpha(0.45) } else { rune::Color::TRANSPARENT })
                .on_press_tx(move |s: &mut App, tx: &mut Tx| {
                    put_down_composer(s, tx);
                    s.asks.step.insert(rid.clone(), at);
                })
                .child(row().gap(8.0).items_center().children((questions.len() > 1).then(|| text(format!("{}/{}", qi + 1, questions.len())).font(META_BOLD).color(if current { attention() } else { faint() }))).child(text(q.question.clone()).font(BODY_BOLD).color(theme::text()).wrap()).child(text(hint).font(META).color(faint())))
                .children(question_context(q))
                .children(q.options.iter().enumerate().map(|(i, o)| {
                    let recommended = feed::catalog::suggests(q.suggested, i, &o.label);
                    pick_el(&request.id, (qi, questions.len()), siblings.clone(), o, q.multi_select, picks.contains(&o.id) && typed_for(qi).is_empty(), recommended, busy)
                }))
                .children((!request.options_only).then(|| own_words(s, key.clone(), "Or answer in your own words", None))),
        );
    }
    if request.takes_note() {
        list = list.child(own_words(s, format!("{}|note", request.id), "Add a note for the agent (optional)", None));
    }
    let response = if busy { None } else { compound_answer(s, request) };
    let submit = Button::new("Send answers").variant(if response.is_some() { ButtonVariant::Submit } else { ButtonVariant::Neutral }).icon(&icons::ARROW_UP).build(theme::kit()).key("pick-submit");
    let submit = if reviewing { row().radius(10.0).border(1.0, attention().alpha(0.6)).child(submit) } else { submit };
    let (id, to) = (request.id.clone(), route.clone());
    let submit = match response {
        Some(response) => submit.on_press(move |s: &mut App| to.answer(s, &id, response.clone())),
        None => submit.opacity(0.5),
    };
    list.child(row().gap(10.0).items_center().child(submit).children(decline(request, route, busy)).child(spacer()).child(pager_hint()))
}

fn decline(request: &OpenRequest, route: &Route, busy: bool) -> Option<El> {
    if request.kind == "ask" {
        return None;
    }
    let b = Button::new("Decline").variant(ButtonVariant::Neutral).build(theme::kit()).key("question-decline");
    let (id, route) = (request.id.clone(), route.clone());
    Some(if busy { b.opacity(0.5) } else { b.on_press(move |s: &mut App| route.answer(s, &id, Response::choose("deny"))) })
}

pub(crate) fn put_down_composer(s: &mut App, _: &mut Tx) {
    s.composer.focused = false;
}

fn pager_hint() -> El {
    row().gap(4.0).items_center().child(keys(&["1–9"])).child(keys(&["←", "→"])).child(keys(&["↩"]))
}

pub(crate) fn request_heading(request: &OpenRequest, who: &str) -> String {
    if let Some(e) = request.elicitation.as_ref().filter(|_| request.is_input()) {
        return if e.message.trim().is_empty() { format!("{} asks for input", e.asker()) } else { e.message.clone() };
    }
    if request.kind == "ask"
        && let Some(title) = request.title.as_ref().filter(|t| !t.trim().is_empty())
    {
        return title.clone();
    }
    if request.uses_pager() {
        let count = request.questions.as_ref().map_or(0, Vec::len);
        return if count > 1 { format!("The agent has {count} questions") } else { "The agent has a question".to_string() };
    }
    if let Some(q) = single_question(request) {
        return q.question.clone();
    }
    request.title.clone().or(request.description.clone()).unwrap_or_else(|| format!("{who} wants to use {}", request.tool))
}

fn single_question(request: &OpenRequest) -> Option<&feed::catalog::RequestQuestion> {
    request.questions.as_ref().and_then(|q| q.first()).filter(|q| !q.multi_select && !q.options.is_empty() && !request.uses_pager())
}

fn request_choices(request: &OpenRequest) -> Vec<Choice> {
    if let Some(q) = single_question(request) {
        return q
            .options
            .iter()
            .enumerate()
            .map(|(i, o)| Choice {
                label: o.label.replace(" (Recommended)", "").replace(" (recommended)", ""),
                detail: o.description.clone(),
                recommended: feed::catalog::suggests(q.suggested, i, &o.label),
                request: Some((request.id.clone(), vec![o.id.clone()])),
                reply: String::new(),
            })
            .collect();
    }
    let actions: Vec<(String, String)> = if request.actions.is_empty() {
        vec![("allow".into(), "Allow".into()), ("deny".into(), "Deny".into())]
    } else {
        request.actions.iter().map(|a| (a.id.clone(), a.title.clone())).collect()
    };
    actions.into_iter().map(|(id, label)| Choice { recommended: id == "allow", label, detail: String::new(), request: Some((request.id.clone(), vec![id])), reply: String::new() }).collect()
}

fn field_key(request: &str, field: &Field) -> String {
    format!("{request}|field|{}", field.name)
}

fn form_content(s: &App, request: &OpenRequest, fields: &[Field]) -> Option<serde_json::Value> {
    let form = s.asks.forms.get(&request.id).cloned().unwrap_or_default();
    form.content(fields, |f| s.typed.get(&field_key(&request.id, f)).map(|t| t.text.clone()).unwrap_or_else(|| f.default_text()))
}

fn field_el(s: &App, request: &str, field: &Field, busy: bool) -> El {
    let form = s.asks.forms.get(request).cloned().unwrap_or_default();
    let mut c = col().key(format!("field-{}", field.name)).w_full().gap(5.0);
    if field.is_switch() {
        let (rid, f) = (request.to_string(), field.clone());
        c = c.child(Switch::new(form.is_on(field)).label(field.label()).disabled(busy).on_toggle(move |s: &mut App| s.asks.forms.entry(rid.clone()).or_default().flip(&f)).build(theme::kit()));
    } else if field.is_choice() {
        let picked = form.picked(field);
        c = c.child(text(field.label()).font(META_BOLD).color(muted())).child(row().w_full().flex_wrap().gap(6.0).children(field.options.iter().map(|o| {
            let on = picked.contains(&o.value);
            let pill = row()
                .key(format!("field-{}-{}", field.name, o.value))
                .gap(6.0)
                .items_center()
                .px(10.0)
                .py(6.0)
                .radius(8.0)
                .fill(if on { attention().alpha(0.22) } else { crate::theme::ink(0.05) })
                .border(1.0, if on { attention().alpha(0.6) } else { crate::theme::ink(0.10) })
                .child(icon(if on { &icons::CHECK_CIRCLE } else { &icons::TARGET }, 12.0).color(if on { attention() } else { faint() }))
                .child(text(if o.label.is_empty() { o.value.clone() } else { o.label.clone() }).font(META_BOLD).color(theme::text()));
            if busy {
                return pill.opacity(0.5);
            }
            let (rid, f, value) = (request.to_string(), field.clone(), o.value.clone());
            pill.hover(crate::theme::ink(0.10)).on_press(move |s: &mut App| s.asks.forms.entry(rid.clone()).or_default().pick(&f, &value))
        })));
    } else {
        let key = field_key(request, field);
        let fresh = rune::TextEdit::new(field.default_text());
        let edit = s.typed.get(&key).unwrap_or(&fresh);
        let (lens, initial) = (key.clone(), field.default_text());
        c = c.child(text(field.label()).font(META_BOLD).color(muted())).child(
            text_input(edit, META)
                .key(format!("field-input-{key}"))
                .w_full()
                .px(12.0)
                .py(8.0)
                .radius(10.0)
                .fill(crate::theme::ink(0.04))
                .border(1.0, crate::theme::ink(0.10))
                .color(theme::text())
                .placeholder(if matches!(field.kind.as_str(), "integer" | "number") { "A number" } else { "" }.to_string(), faint())
                .on_edit(move |s: &mut App| s.typed.entry(lens.clone()).or_insert_with(|| rune::TextEdit::new(initial.clone()))),
        );
    }
    c.children((!field.description.trim().is_empty()).then(|| text(field.description.clone()).font(META).color(muted()).wrap()))
}

fn input_el(s: &App, request: &OpenRequest, e: &feed::request::Elicitation, route: &Route, busy: bool) -> El {
    let mut c = col().w_full().gap(10.0).pt(4.0).child(row().gap(6.0).items_center().child(icon(&icons::MESSAGE, 12.0).color(muted())).child(text(format!("{} asks for input", e.asker())).font(META_BOLD).color(muted())));
    let answer = |label: &str, variant: ButtonVariant, response: Option<Response>, key: &str| {
        let b = Button::new(label.to_string()).variant(variant).build(theme::kit()).key(format!("input-{key}"));
        match response.filter(|_| !busy) {
            Some(response) => {
                let (id, route) = (request.id.clone(), route.clone());
                b.on_press(move |s: &mut App| route.answer(s, &id, response.clone()))
            }
            None => b.opacity(0.5),
        }
    };
    if e.is_link() {
        let link = match e.web_url() {
            Some(url) => {
                text(url.clone()).font(META_BOLD).color(accent()).wrap().on_press(move |_: &mut App| open_link(&url))
            }
            None => text(e.url.clone()).font(META).color(muted()).wrap(),
        };
        return c.child(link).child(row().gap(8.0).child(answer("Opened, continue", ButtonVariant::Submit, Some(Response::choose("allow")), "continue")).child(spacer()).child(answer("Decline", ButtonVariant::Neutral, Some(Response::choose("deny")), "decline")));
    }
    for field in &e.fields {
        c = c.child(field_el(s, &request.id, field, busy));
    }
    let content = form_content(s, request, &e.fields);
    let submit = if e.fields.is_empty() { Some(Response::choose("allow")) } else { content.map(Response::form) };
    c.child(row().gap(8.0).pt(2.0).child(answer("Submit", ButtonVariant::Submit, submit, "submit")).child(spacer()).child(answer("Decline", ButtonVariant::Neutral, Some(Response::choose("deny")), "decline")))
}

fn reply_box(s: &App, request: &OpenRequest, route: &Route, busy: bool) -> El {
    let key = format!("{}|reply", request.id);
    let words = s.typed.get(&key).map(|t| t.text.trim().to_string()).unwrap_or_default();
    let send = {
        let (id, route) = (request.id.clone(), route.clone());
        move |s: &mut App, words: String| route.answer(s, &id, Response::with_message(feed::request::REPLY, &words))
    };
    let button = Button::new("Send").variant(ButtonVariant::Neutral).build(theme::kit()).key("request-reply-send");
    let button = if busy || words.is_empty() {
        button.opacity(0.5)
    } else {
        let send = send.clone();
        button.on_press(move |s: &mut App| send(s, words.clone()))
    };
    let input = own_words(s, key, "Tell the agent why, or what to do instead", if busy { None } else { Some(Box::new(send)) });
    row().w_full().gap(8.0).items_center().child(col().grow(1.0).min_w(0.0).child(input)).child(button)
}

pub(crate) fn request_body(s: &App, request: &OpenRequest, route: Route, busy: bool) -> El {
    let mut c = col().w_full().gap(8.0).children(clock_line(s, request)).children(replaced_note(s, request, &route, busy));
    if let Some(e) = request.elicitation.as_ref().filter(|_| request.is_input()) {
        return c.child(input_el(s, request, e, &route, busy));
    }
    if request.uses_pager() {
        return c.child(col().key("compound-scroll").w_full().max_h(420.0).scroll(ScrollSpec { stick_bottom: false }).child(compound_el(s, request, &route, busy)));
    }
    let question = single_question(request);
    if let Some(q) = question {
        c = c.children(question_context(q));
    } else {
        c = c
            .children(request.description.as_ref().filter(|d| !d.trim().is_empty() && request.title.is_some()).map(|d| text(d.clone()).font(META).color(muted()).wrap()))
            .children(request.reason.as_ref().filter(|r| !r.trim().is_empty()).map(|r| text(r.clone()).font(META).color(muted()).wrap()))
            .children(request.shown_detail().map(|d| col().key("request-detail").w_full().max_h(140.0).scroll(ScrollSpec { stick_bottom: false }).px(10.0).py(8.0).radius(8.0).fill(crate::theme::ink(0.06)).child(text(d).font(META.mono()).color(theme::text()).wrap())))
            .children((!request.blocked_path.trim().is_empty()).then(|| text(request.blocked_path.clone()).font(META.mono()).color(muted()).wrap()));
    }
    let replying = s.asks.replying.contains(&request.id);
    c = c.child(col().w_full().gap(6.0).pt(4.0).children(request_choices(request).into_iter().enumerate().map(|(i, choice)| choice_el(i, choice, busy, &route))));
    if let Some(q) = question.filter(|_| !request.options_only) {
        let (id, to, asked) = (request.id.clone(), route.clone(), q.question.clone());
        let send = move |s: &mut App, words: String| to.answer(s, &id, Response::picks(Vec::new(), [(asked.clone(), words)].into_iter().collect()));
        c = c.child(own_words(s, format!("{}|0", request.id), "Or answer in your own words, then press Return", if busy { None } else { Some(Box::new(send)) })).child(row().gap(10.0).items_center().children(decline(request, &route, busy)).child(spacer()).child(pager_hint()));
    } else if replying {
        c = c.child(reply_box(s, request, &route, busy));
    }
    c
}

pub(crate) fn waiting_choices(s: &App, x: &Session) -> (Option<String>, Vec<Choice>) {
    let recent = recent_answers(s, x);
    for (_, body) in &recent {
        let body = body.clone();
        let options = feed::catalog::numbered_options(&body);
        if !options.is_empty() {
            let question = body.split("\n\n").map(str::trim).find(|p| p.contains('?') && feed::catalog::numbered_options(p).is_empty()).map(|p| plain(p, 320));
            let choices = options
                .iter()
                .enumerate()
                .map(|(i, label)| {
                    let (head, tail) = split_option(label);
                    let tail = tail.trim_start_matches("recommended").trim_start_matches(',').trim().to_string();
                    Choice {
                        recommended: feed::catalog::suggests(None, i, label),
                        reply: format!("Option {}: {}", i + 1, head),
                        label: head.replace(" — recommended", "").replace(" (recommended)", ""),
                        detail: tail,
                        request: None,
                    }
                })
                .collect();
            return (question, choices);
        }
    }
    (recent.first().and_then(|(_, b)| question_in(b)), Vec::new())
}

pub(crate) fn choice_el(i: usize, c: Choice, busy: bool, route: &Route) -> El {
    let accent_on = c.recommended;
    let mut card_el = row()
        .key(format!("choice-{i}"))
        .w_full()
        .gap(12.0)
        .items_center()
        .px(12.0)
        .py(10.0)
        .radius(12.0)
        .fill(if accent_on { attention().alpha(0.14) } else { crate::theme::ink(0.05) })
        .border(1.0, if accent_on { attention().alpha(0.45) } else { crate::theme::ink(0.10) })
        .hover(if accent_on { attention().alpha(0.22) } else { crate::theme::ink(0.10) })
        .press_scale(0.985)
        .motion(SPRING)
        .child(col().size(24.0, 24.0).radius(7.0).items_center().justify_center().fill(crate::theme::ink(0.08)).child(text((i + 1).to_string()).font(META_BOLD).color(theme::text())))
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(2.0)
                .child(text(c.label.clone()).font(BODY_BOLD).color(theme::text()).wrap())
                .children((!c.detail.is_empty()).then(|| text(c.detail.clone()).font(META).color(muted()).wrap().max_lines(2))),
        );
    if accent_on {
        card_el = card_el.child(row().px(7.0).py(2.0).radius(6.0).fill(attention().alpha(0.2)).child(text("Recommended").font(META_BOLD).color(attention())));
    }
    card_el = card_el.child(icon(&icons::ARROW_RIGHT, 13.0).color(faint()));
    if busy {
        return card_el.opacity(0.5);
    }
    match c.request {
        Some((request, choices)) if choices == [feed::request::REPLY] => card_el.on_press(move |s: &mut App| {
            if !s.asks.replying.remove(&request) {
                s.asks.replying.insert(request.clone());
            }
        }),
        Some((request, choices)) => {
            let route = route.clone();
            card_el.on_press(move |s: &mut App| route.answer(s, &request, Response::picks(choices.clone(), Default::default())))
        }
        None => {
            let reply = c.reply.clone();
            card_el.on_press(move |s: &mut App| draft_reply(s, &reply))
        }
    }
}

fn pager_target(s: &App, route: &Route) -> Option<(Route, OpenRequest, bool)> {
    let (route, request, busy) = match route {
        Route::Session => {
            let x = session(s, s.selected.as_deref()?).filter(|x| !x.archived)?;
            (Route::Session, open_request(s, x)?.clone(), s.answering.is_some())
        }
        Route::Decision(d) => {
            let d = s.ov.inbox.inbox.as_ref()?.decisions.iter().find(|x| x.id() == d.id())?.clone();
            let open: Vec<&OpenRequest> = d.requests.iter().collect();
            let request = shown_of(s, &d.session_id, &open)?.clone();
            let busy = crate::over::inbox::held(s, &d, &request.id);
            (Route::Decision(d), request, busy)
        }
    };
    let asks = request.questions.as_ref().is_some_and(|q| q.iter().any(|q| !q.options.is_empty()));
    (asks && !request.is_input()).then_some((route, request, busy))
}

pub(crate) fn pager_key(s: &mut App, ev: &KeyEvent, route: &Route) -> bool {
    if ev.mods.cmd || ev.mods.ctrl || ev.mods.alt || ev.mods.shift || ev.mods.logo {
        return false;
    }
    let key = match &ev.key {
        Key::Text(t) => t.parse::<usize>().ok().filter(|n| (1..=9).contains(n)).map(PagerKey::Pick),
        Key::Left => Some(PagerKey::Back),
        Key::Right => Some(PagerKey::Forward),
        Key::Enter => Some(PagerKey::Send),
        _ => None,
    };
    let Some(key) = key else { return false };
    let Some((route, request, busy)) = pager_target(s, route) else { return false };
    if busy {
        return true;
    }
    let questions = request.questions.clone().unwrap_or_default();
    let step = s.asks.step.get(&request.id).copied().unwrap_or(0);
    let mut picks = s.picks.get(&request.id).cloned().unwrap_or_default();
    if let PagerKey::Pick(n) = key
        && let Some(q) = questions.get(step).filter(|q| n <= q.options.len())
        && s.typed.remove(&format!("{}|{step}", request.id)).is_some_and(|t| !t.text.trim().is_empty())
    {
        picks.retain(|p| !q.options.iter().any(|o| &o.id == p));
    }
    let paged = feed::request::page(&questions, step, key, &mut picks, !request.uses_pager());
    s.picks.insert(request.id.clone(), picks);
    s.asks.step.insert(request.id.clone(), paged.step);
    if paged.send
        && let Some(response) = compound_answer(s, &request)
    {
        route.answer(s, &request.id, response);
    }
    true
}

pub(crate) fn waiting_card(s: &App, x: &Session) -> El {
    let open = open_requests(s, x);
    let shown = shown_of(s, &x.id, &open);
    let busy = s.answering.is_some();
    let (asked, choices) = if shown.is_some() { (None, Vec::new()) } else { waiting_choices(s, x) };
    let reason = x.attention.as_ref().map(|a| a.reason.clone()).filter(|r| !r.is_empty());
    let summary = Some(x.summary()).filter(|t| t != "Needs you" && t != "Working" && !t.is_empty());
    let question = shown.map(|r| request_heading(r, &x.title())).or(asked).or(reason).or(summary).unwrap_or_else(|| "This session is waiting for you.".into());
    let offers = shown.is_some() || !choices.is_empty();
    let reply = Button::new(if offers { "Something else" } else { "Reply" })
        .variant(if offers { ButtonVariant::Neutral } else { ButtonVariant::Submit })
        .icon(&icons::CORNER)
        .build(theme::kit())
        .key("waiting-reply")
        .on_press_tx(|_: &mut App, tx: &mut Tx| tx.focus("composer", true));
    let next = Button::new("Next waiting").variant(ButtonVariant::Ghost).icon(&icons::ARROW_RIGHT).build(theme::kit()).key("waiting-next").on_press(crate::over::next_need);
    let source = match shown {
        Some(_) => Some("Answers the agent's question directly"),
        None if !choices.is_empty() => Some("Puts your pick in the reply box; press Return to send"),
        None => None,
    };
    let mut body = col()
        .grow(1.0)
        .min_w(0.0)
        .gap(8.0)
        .child(row().w_full().gap(8.0).items_center().child(text("WAITING ON YOU").font(LABEL).color(attention())).children(source.map(|t| text(t).font(META).color(faint()))).child(spacer()).children(shown.and_then(|r| switcher(&x.id, &open, &r.id))))
        .child(text(question).font(TITLE).color(theme::text()).wrap());
    if let Some(request) = shown {
        body = body.child(request_body(s, request, Route::Session, busy));
    } else if !choices.is_empty() {
        body = body.child(col().w_full().gap(6.0).pt(4.0).children(choices.into_iter().enumerate().map(|(i, c)| choice_el(i, c, busy, &Route::Session))));
    }
    body = body.child(row().gap(8.0).pt(4.0).items_center().child(reply).child(next).child(spacer()).child(keys(&["⌘", "J"])));
    let card_el = row()
        .w_full()
        .max_w(780.0)
        .gap(14.0)
        .items_start()
        .px(18.0)
        .py(16.0)
        .radius(16.0)
        .linear_gradient(135.0, [(0.0, attention().alpha(0.16)), (1.0, attention().alpha(0.04))])
        .border_gradient(1.0, attention().alpha(0.55), attention().alpha(0.12))
        .shadow(8.0, 28.0, attention().alpha(0.10))
        .transition(Transition::scale(0.97))
        .on_press_tx(put_down_composer)
        .key("waiting-card")
        .focusable(true)
        .on_key(|s: &mut App, ev: &KeyEvent, _: &mut Tx| pager_key(s, ev, &Route::Session))
        .child(col().size(34.0, 34.0).radius(10.0).items_center().justify_center().fill(attention().alpha(0.18)).child(icon(&icons::HAND, 17.0).color(attention())))
        .child(body);
    col().w_full().items_center().px(32.0).pb(12.0).child(card_el)
}
