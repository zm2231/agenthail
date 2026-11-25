use super::*;

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

pub(crate) fn open_request<'a>(s: &'a App, x: &Session) -> Option<&'a feed::catalog::OpenRequest> {
    s.requests.get(&x.id).and_then(|r| r.iter().find(|r| !s.is_settled(&r.id)))
}

pub(crate) fn has_settled(s: &App, x: &Session) -> bool {
    settled_request(s, x).is_some()
}

pub(crate) fn settled_request<'a>(s: &'a App, x: &Session) -> Option<&'a feed::catalog::OpenRequest> {
    let settled = s.requests.get(&x.id)?.iter().filter(|r| s.is_settled(&r.id));
    settled.max_by_key(|r| s.settled.get(&r.id).copied().flatten().map_or((1, None), |at| (0, Some(at))))
}

pub(crate) fn is_compound(request: &feed::catalog::OpenRequest) -> bool {
    request.questions.as_ref().is_some_and(|qs| qs.len() > 1 || qs.iter().any(|q| q.multi_select))
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

fn pick_el(request: &str, qi: usize, siblings: Vec<String>, option: &feed::catalog::RequestOption, multi: bool, picked: bool, recommended: bool, busy: bool) -> El {
    let label = option.label.replace(" (Recommended)", "").replace(" (recommended)", "");
    let mut el = row()
        .key(format!("pick-{}", option.id))
        .w_full()
        .gap(10.0)
        .items_center()
        .px(12.0)
        .py(8.0)
        .radius(10.0)
        .fill(if picked { attention().alpha(0.22) } else { Color::WHITE.alpha(0.05) })
        .border(1.0, if picked { attention().alpha(0.6) } else { Color::WHITE.alpha(0.10) })
        .hover(Color::WHITE.alpha(0.10))
        .child(icon(if picked { &icons::CHECK_CIRCLE } else { &icons::TARGET }, 14.0).color(if picked { attention() } else { faint() }))
        .child(col().grow(1.0).min_w(0.0).gap(2.0).child(text(label).font(BODY_BOLD).color(theme::text()).wrap()).children((!option.description.is_empty()).then(|| text(option.description.clone()).font(META).color(muted()).wrap().max_lines(2))))
        .children(recommended.then(|| row().px(7.0).py(2.0).radius(6.0).fill(attention().alpha(0.2)).child(text("Recommended").font(META_BOLD).color(attention()))));
    if busy {
        return el.opacity(0.5);
    }
    let (request, id) = (request.to_string(), option.id.clone());
    el = el.on_press(move |s: &mut App| {
        s.typed.remove(&format!("{request}|{qi}"));
        toggle_pick(s, &request, &siblings, &id, multi)
    });
    el
}

fn compound_el(s: &App, request: &feed::catalog::OpenRequest, busy: bool) -> El {
    let picks = s.picks.get(&request.id).cloned().unwrap_or_default();
    let questions = request.questions.clone().unwrap_or_default();
    let mut list = col().w_full().gap(12.0).pt(4.0);
    let typed_for = |qi: usize| s.typed.get(&format!("{}|{qi}", request.id)).map(|t| t.text.trim().to_string()).unwrap_or_default();
    for (qi, q) in questions.iter().enumerate() {
        let key = format!("{}|{qi}", request.id);
        let empty = rune::TextEdit::new("");
        let edit = s.typed.get(&key).unwrap_or(&empty);
        let lens = key.clone();
        let other = text_input(edit, META)
            .key(format!("other-{key}"))
            .w_full()
            .px(12.0)
            .py(8.0)
            .radius(10.0)
            .fill(Color::WHITE.alpha(0.04))
            .border(1.0, Color::WHITE.alpha(0.10))
            .color(theme::text())
            .placeholder("Or answer in your own words", faint())
            .on_edit(move |s: &mut App| s.typed.entry(lens.clone()).or_insert_with(|| rune::TextEdit::new("")));
        let siblings: Vec<String> = q.options.iter().map(|o| o.id.clone()).collect();
        let hint = if q.multi_select { "Pick any" } else { "Pick one" };
        list = list.child(
            col()
                .w_full()
                .gap(6.0)
                .child(row().gap(8.0).items_center().child(text(q.question.clone()).font(BODY_BOLD).color(theme::text()).wrap()).child(text(hint).font(META).color(faint())))
                .children(question_context(q))
                .children(q.options.iter().enumerate().map(|(i, o)| {
                    let recommended = feed::catalog::suggests(q.suggested, i, &o.label);
                    pick_el(&request.id, qi, siblings.clone(), o, q.multi_select, picks.contains(&o.id) && typed_for(qi).is_empty(), recommended, busy)
                }))
                .child(other),
        );
    }
    let mut chosen = picks.clone();
    let mut answers = std::collections::BTreeMap::new();
    for (qi, q) in questions.iter().enumerate() {
        let own = typed_for(qi);
        if !own.is_empty() {
            chosen.retain(|p| !q.options.iter().any(|o| &o.id == p));
            answers.insert(q.question.clone(), own);
        }
    }
    let answered = |qi: usize, q: &feed::catalog::RequestQuestion| !typed_for(qi).is_empty() || q.options.iter().any(|o| picks.contains(&o.id));
    let partial_ok = request.kind == "ask";
    let ready = !busy && if partial_ok { questions.iter().enumerate().any(|(qi, q)| answered(qi, q)) } else { questions.iter().enumerate().all(|(qi, q)| answered(qi, q)) };
    let id = request.id.clone();
    let submit = Button::new("Send answers").variant(if ready { ButtonVariant::Submit } else { ButtonVariant::Neutral }).icon(&icons::ARROW_UP).build(theme::kit()).key("pick-submit");
    let submit = if ready { submit.on_press(move |s: &mut App| answer_request(s, &id, chosen.clone(), answers.clone())) } else { submit.opacity(0.5) };
    list.child(row().child(submit))
}

pub(crate) fn waiting_choices(s: &App, x: &Session) -> (Option<String>, Vec<Choice>, bool) {
    if let Some(request) = open_request(s, x) {
        if is_compound(request) {
            let count = request.questions.as_ref().map_or(0, Vec::len);
            let heading = if count > 1 { format!("The agent has {count} questions") } else { "The agent has a question".to_string() };
            return (Some(heading), Vec::new(), true);
        }
        if let Some(q) = request.questions.as_ref().and_then(|q| q.first()).filter(|q| !q.multi_select && !q.options.is_empty()) {
            let choices = q
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
            return (Some(q.question.clone()), choices, true);
        }
        let actions: Vec<(String, String)> = if request.actions.is_empty() {
            vec![("allow".into(), "Allow".into()), ("deny".into(), "Deny".into())]
        } else {
            request.actions.iter().map(|a| (a.id.clone(), a.title.clone())).collect()
        };
        let title = request.title.clone().or(request.description.clone()).unwrap_or_else(|| format!("{} wants to use {}", x.title(), request.tool));
        let choices = actions
            .into_iter()
            .map(|(id, label)| Choice { recommended: id == "allow", label, detail: String::new(), request: Some((request.id.clone(), vec![id])), reply: String::new() })
            .collect();
        return (Some(title), choices, true);
    }
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
            return (question, choices, false);
        }
    }
    (recent.first().and_then(|(_, b)| question_in(b)), Vec::new(), false)
}

pub(crate) fn choice_el(i: usize, c: Choice, busy: bool) -> El {
    let accent_on = c.recommended;
    let mut card_el = row()
        .key(format!("choice-{i}"))
        .w_full()
        .gap(12.0)
        .items_center()
        .px(12.0)
        .py(10.0)
        .radius(12.0)
        .fill(if accent_on { attention().alpha(0.14) } else { Color::WHITE.alpha(0.05) })
        .border(1.0, if accent_on { attention().alpha(0.45) } else { Color::WHITE.alpha(0.10) })
        .hover(if accent_on { attention().alpha(0.22) } else { Color::WHITE.alpha(0.10) })
        .press_scale(0.985)
        .motion(SPRING)
        .child(col().size(24.0, 24.0).radius(7.0).items_center().justify_center().fill(Color::WHITE.alpha(0.08)).child(text((i + 1).to_string()).font(META_BOLD).color(theme::text())))
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
        Some((request, choices)) => card_el.on_press(move |s: &mut App| answer_request(s, &request, choices.clone(), Default::default())),
        None => {
            let reply = c.reply.clone();
            card_el.on_press(move |s: &mut App| draft_reply(s, &reply))
        }
    }
}

pub(crate) fn waiting_card(s: &App, x: &Session) -> El {
    let (asked, choices, structured) = waiting_choices(s, x);
    let reason = x.attention.as_ref().map(|a| a.reason.clone()).filter(|r| !r.is_empty());
    let summary = Some(x.summary()).filter(|t| t != "Needs you" && t != "Working" && !t.is_empty());
    let question = asked.or(reason).or(summary).unwrap_or_else(|| "This session is waiting for you.".into());
    let reply = Button::new(if choices.is_empty() { "Reply" } else { "Something else" })
        .variant(if choices.is_empty() { ButtonVariant::Submit } else { ButtonVariant::Neutral })
        .icon(&icons::CORNER)
        .build(theme::kit())
        .key("waiting-reply")
        .on_press_tx(|_: &mut App, tx: &mut Tx| tx.focus("composer", true));
    let next = Button::new("Next waiting").variant(ButtonVariant::Ghost).icon(&icons::ARROW_RIGHT).build(theme::kit()).key("waiting-next").on_press(next_waiting);
    let busy = s.answering.is_some();
    let source = if choices.is_empty() {
        None
    } else if structured {
        Some("Answers the agent's question directly")
    } else {
        Some("Puts your pick in the reply box; press Return to send")
    };
    let mut body = col()
        .grow(1.0)
        .min_w(0.0)
        .gap(8.0)
        .child(row().gap(8.0).items_center().child(text("WAITING ON YOU").font(LABEL).color(attention())).children(source.map(|t| text(t).font(META).color(faint()))))
        .child(text(question).font(TITLE).color(theme::text()).wrap())
        .children(open_request(s, x).filter(|r| !is_compound(r)).and_then(|r| r.questions.as_ref()?.first()).and_then(question_context));
    if let Some(request) = open_request(s, x).filter(|r| is_compound(r)) {
        body = body.child(col().key("compound-scroll").w_full().max_h(420.0).scroll(ScrollSpec { stick_bottom: false }).child(compound_el(s, request, busy)));
    } else if !choices.is_empty() {
        body = body.child(col().w_full().gap(6.0).pt(4.0).children(choices.into_iter().enumerate().map(|(i, c)| choice_el(i, c, busy))));
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
        .child(col().size(34.0, 34.0).radius(10.0).items_center().justify_center().fill(attention().alpha(0.18)).child(icon(&icons::HAND, 17.0).color(attention())))
        .child(body);
    col().w_full().items_center().px(32.0).pb(12.0).child(card_el)
}
