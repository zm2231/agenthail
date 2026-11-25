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

pub(crate) fn recent_answers(s: &App, x: &Session) -> Vec<String> {
    let Some(pane) = s.panes.get(&x.id) else { return Vec::new() };
    pane.blocks
        .iter()
        .rev()
        .filter_map(|b| match &b.kind {
            feed::Kind::Assistant(body) => Some(body.clone()),
            _ => None,
        })
        .take(2)
        .collect()
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
    s.requests.get(&x.id).is_some_and(|r| r.iter().any(|r| s.is_settled(&r.id)))
}

pub(crate) fn waiting_choices(s: &App, x: &Session) -> (Option<String>, Vec<Choice>, bool) {
    if let Some(request) = open_request(s, x) {
        if let Some(q) = request.questions.as_ref().and_then(|q| q.first()).filter(|q| !q.multi_select && !q.options.is_empty()) {
            let choices = q
                .options
                .iter()
                .enumerate()
                .map(|(i, o)| Choice {
                    label: o.label.replace(" (Recommended)", "").replace(" (recommended)", ""),
                    detail: o.description.clone(),
                    recommended: q.suggested == Some(i) || o.label.to_lowercase().contains("recommended"),
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
    for body in recent_answers(s, x) {
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
                        recommended: label.to_lowercase().contains("recommended"),
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
    (recent_answers(s, x).first().and_then(|b| question_in(b)), Vec::new(), false)
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
        Some((request, choices)) => card_el.on_press(move |s: &mut App| answer_request(s, &request, choices.clone())),
        None => {
            let reply = c.reply.clone();
            card_el.on_press_tx(move |s: &mut App, tx: &mut Tx| {
                send_text(s, reply.clone(), tx);
                if let Some(id) = s.selected.clone() {
                    s.answered.insert(id);
                }
            })
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
        Some("Sends your pick as a reply")
    };
    let mut body = col()
        .grow(1.0)
        .min_w(0.0)
        .gap(8.0)
        .child(row().gap(8.0).items_center().child(text("WAITING ON YOU").font(LABEL).color(attention())).children(source.map(|t| text(t).font(META).color(faint()))))
        .child(text(question).font(TITLE).color(theme::text()).wrap());
    if !choices.is_empty() {
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
