use feed::voice::{ELSEWHERE, MIC_SETTINGS, OCCUPIED, Role};
use rune::{Cx, El, ScrollSpec, Tx, col, el, icon, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize};
use rune_theme_opencode::icons as theme_icons;

use super::{Ask, Win, open_timeline};
use crate::icons;
use crate::theme::{self, BODY, BODY_BOLD, META, META_BOLD, TITLE, accent, failed, faint, kit, line, muted, wash};

pub fn view(w: &Win, _: &mut Cx<Win>) -> El {
    super::trace("view", w);
    col()
        .w_full()
        .h_full()
        .fill(theme::card_deep())
        .child(el().key("voice-peer").absolute(0.0, 0.0).size(1.0, 1.0).opacity(0.01).native_view(&w.slot))
        .child(header(w))
        .child(col().key("voice-body").w_full().grow(1.0).shrink().scroll(ScrollSpec { stick_bottom: true }).child(body(w)))
        .child(controls(w))
}

fn small(label: impl Into<String>, variant: ButtonVariant) -> Button {
    Button::new(label).variant(variant).control_size(ControlSize::Small)
}

fn header(w: &Win) -> El {
    let live = w.call.audio_connected;
    let glyph = col()
        .size(36.0, 36.0)
        .radius(18.0)
        .items_center()
        .justify_center()
        .fill(wash())
        .child(icon(&theme_icons::WAVEFORM, 18.0).color(if live { accent() } else { muted() }));
    let route = match w.call.state.as_ref().and_then(|s| s.target.as_ref()) {
        Some(target) => format!("Routed to {}", if target.name.is_empty() { &target.id } else { &target.name }),
        None if w.call.routed() => format!("Calls {}", shown_name(w)),
        None => "Codex Voice · Agenthail orchestrator".into(),
    };
    let titles = col()
        .gap(2.0)
        .grow(1.0)
        .shrink()
        .child(text(w.call.title()).font(BODY_BOLD).color(theme::text()).key("voice-status"))
        .child(text(route).font(META).color(muted()).truncate());
    let new = small("New conversation", ButtonVariant::Ghost)
        .icon(&icons::PENCIL)
        .help("Start a new orchestrator conversation")
        .disabled(!w.call.can_start_new())
        .on_press(|w: &mut Win| w.ask = Some(Ask::NewConversation))
        .build(kit())
        .key("voice-new");
    let mut head = col().w_full().px(20.0).pt(40.0).pb(14.0).gap(10.0).window_drag().border_sides([0.0, 0.0, 1.0, 0.0], line())
        .child(row().w_full().gap(12.0).items_center().child(glyph).child(titles).child(new));
    if w.ask == Some(Ask::NewConversation) {
        head = head.child(confirm(
            "Start a new orchestrator conversation?",
            "This creates a separate Codex thread. The current conversation stays in Sessions.",
            "Start new conversation",
            |w: &mut Win| {
                w.ask = None;
                let effects = w.call.start_new();
                w.run(effects);
            },
        ));
    }
    head
}

fn confirm(title: &str, detail: &str, yes: &str, act: fn(&mut Win)) -> El {
    col()
        .w_full()
        .gap(8.0)
        .padding(12.0)
        .radius(10.0)
        .fill(wash())
        .border(1.0, line())
        .child(text(title.to_string()).font(BODY_BOLD).color(theme::text()))
        .child(text(detail.to_string()).font(META).color(muted()).wrap().w_full())
        .child(
            row()
                .w_full()
                .gap(8.0)
                .child(el().grow(1.0))
                .child(small("Cancel", ButtonVariant::Ghost).on_press(|w: &mut Win| w.ask = None).build(kit()).key("voice-ask-cancel"))
                .child(small(yes.to_string(), ButtonVariant::Danger).on_press(act).build(kit()).key("voice-ask-yes")),
        )
}

fn shown_name(w: &Win) -> String {
    if w.name.is_empty() { w.call.target.clone() } else { w.name.clone() }
}

fn note(value: impl Into<String>, color: rune::Color) -> El {
    text(value.into()).font(BODY).color(color).wrap().w_full()
}

fn body(w: &Win) -> El {
    let mut b = col().w_full().gap(14.0).padding(20.0);
    if w.call.mic_denied {
        let settings = small("Open System Settings", ButtonVariant::Neutral).on_press(|_: &mut Win| crate::open_settings(MIC_SETTINGS)).build(kit()).key("voice-mic-settings");
        b = b.child(
            col()
                .key("voice-mic-denied")
                .w_full()
                .gap(8.0)
                .padding(12.0)
                .radius(10.0)
                .fill(failed().alpha(0.12))
                .border(1.0, failed().alpha(0.35))
                .child(row().gap(8.0).items_center().child(icon(&icons::MIC_OFF, 15.0).color(failed())).child(text("Microphone access is off for Agenthail").font(BODY_BOLD).color(failed())))
                .child(note("Turn on Agenthail in System Settings, Privacy & Security, Microphone, then call again.", theme::text()))
                .child(row().child(settings)),
        );
    } else if let Some(error) = &w.call.error {
        b = b.child(note(error.clone(), failed()).key("voice-error"));
    }
    if let Some(error) = &w.call.connection_error {
        b = b.child(note(error.clone(), failed()));
    }
    if let Some(message) = w.call.state.as_ref().and_then(|s| s.message.clone()).filter(|m| !m.is_empty() && w.call.error.as_ref() != Some(m)) {
        b = b.child(note(message, failed()));
    }
    if w.call.state.as_ref().is_some_and(|s| s.occupied) {
        b = b.child(note(OCCUPIED, muted()).key("voice-occupied"));
    } else if w.call.elsewhere() {
        b = b.child(note(ELSEWHERE, muted()).key("voice-elsewhere"));
    }
    b = b.child(transcript(w));
    if let Some(brief) = briefing(w) {
        b = b.child(brief);
    }
    if let Some(session) = w.call.state.as_ref().and_then(|s| s.session.clone()) {
        let id = session.id.clone();
        b = b.child(
            row()
                .w_full()
                .items_center()
                .gap(8.0)
                .child(icon(&icons::TERMINAL, 13.0).color(muted()))
                .child(text("Orchestrator").font(META).color(muted()))
                .child(el().grow(1.0))
                .child(small("Open timeline", ButtonVariant::GhostMuted).on_press(move |_: &mut Win| open_timeline(&id)).build(kit()).key("voice-timeline")),
        );
    }
    if let Some(id) = w.call.attempt() {
        b = b.child(text(format!("Call ID {id}")).font(META).color(faint()).selectable(true));
    }
    let toggle = small(if w.details { "Hide voice details" } else { "Voice details" }, ButtonVariant::GhostMuted)
        .on_press(|w: &mut Win| w.details = !w.details)
        .build(kit())
        .key("voice-details-toggle");
    b = b.child(row().child(toggle));
    if w.details {
        b = b.child(details(w));
    }
    b
}

fn briefing(w: &Win) -> Option<El> {
    let state = w.call.state.as_ref()?;
    let brief = state.brief.as_ref()?;
    let target = state.target.as_ref()?;
    let name = if target.name.is_empty() { &target.id } else { &target.name };
    let mut card = col()
        .key("voice-brief")
        .w_full()
        .gap(4.0)
        .padding(12.0)
        .radius(10.0)
        .fill(wash())
        .border(1.0, line())
        .child(text(brief.summary(name, time::OffsetDateTime::now_utc())).font(META).color(muted()).wrap().w_full());
    if let Some(left) = brief.left_out() {
        card = card.child(text(left).font(META).color(faint()).wrap().w_full());
    }
    Some(card)
}

fn details(w: &Win) -> El {
    let mut list = col().key("voice-details").w_full().gap(14.0).padding(12.0).radius(10.0).fill(wash()).border(1.0, line());
    for section in w.call.details() {
        let mut group = col().w_full().gap(4.0).child(text(section.title).font(META_BOLD).color(muted()));
        for (name, value) in section.rows {
            group = group.child(
                row()
                    .w_full()
                    .gap(12.0)
                    .child(text(name).font(META).color(faint()).w(130.0))
                    .child(text(value).font(META).color(theme::text()).wrap().grow(1.0).shrink().min_w(0.0).selectable(true)),
            );
        }
        list = list.child(group);
    }
    list
}

fn route_field(w: &Win) -> El {
    let input = text_input(&w.route_edit, BODY)
        .single_line()
        .key("voice-route")
        .label("Session route")
        .grow(1.0)
        .min_w(0.0)
        .color(theme::text())
        .placeholder("Exact session ID or alias", faint())
        .on_edit(|w: &mut Win| &mut w.route_edit);
    let hint = if w.route_edit.text.trim().is_empty() {
        "Leave blank to use Agenthail's normal orchestration workflow."
    } else {
        "This routes work only to that exact existing session."
    };
    col()
        .w_full()
        .gap(4.0)
        .child(text("Session route (optional)").font(META_BOLD).color(muted()))
        .child(row().w_full().px(10.0).py(7.0).radius(8.0).fill(wash()).border(1.0, line()).child(input))
        .child(text(hint).font(META).color(faint()).wrap().w_full())
}

fn transcript(w: &Win) -> El {
    let lines = w.call.state.as_ref().map(|s| s.lines()).unwrap_or_default();
    if lines.is_empty() {
        return col()
            .w_full()
            .gap(6.0)
            .py(8.0)
            .child(text("What would you like to work on?").font(TITLE).color(theme::text()))
            .child(note("Talk to the Agenthail orchestrator, or route this call to one exact existing Codex or Claude session.", muted()));
    }
    let routed = w.call.state.as_ref().is_some_and(|s| s.target.is_some());
    let mut list = col().w_full().gap(12.0).key("voice-transcript");
    for line in lines {
        let who = match line.role {
            Role::You => "You",
            Role::Agent if routed => "Selected session",
            Role::Agent => "Orchestrator",
        };
        let words = if line.open { format!("{}…", line.text) } else { line.text };
        list = list.child(
            col()
                .key(format!("voice-line-{}", line.id))
                .w_full()
                .gap(4.0)
                .child(text(who).font(META_BOLD).color(muted()))
                .child(text(words).font(BODY).color(theme::text()).wrap().w_full().selectable(true)),
        );
    }
    list
}

fn controls(w: &Win) -> El {
    let mut dock = col().key("voice-controls").w_full().gap(10.0).padding(20.0).fill(theme::card()).border_sides([1.0, 0.0, 0.0, 0.0], line());
    let call = &w.call;
    let occupied = call.state.as_ref().is_some_and(|s| s.occupied);
    if call.hangup_pending {
        return dock.child(note("Audio ended locally. Confirming hangup…", muted()));
    }
    if call.hangup_unconfirmed {
        return dock.child(note("Audio is off locally. Agenthail didn't confirm the hangup.", muted())).child(
            row().child(
                small("Try hangup again", ButtonVariant::Danger)
                    .icon(&icons::PHONE_OFF)
                    .disabled(occupied)
                    .on_press(|w: &mut Win| {
                        let effects = w.call.hangup();
                        w.run(effects);
                    })
                    .build(kit())
                    .key("voice-hangup-again"),
            ),
        );
    }
    dock = dock.child(route_field(w));
    if !call.has_local_call() {
        let label = if call.routed() { format!("Call {}", shown_name(w)) } else { "Talk to orchestrator".to_string() };
        dock = dock.child(
            Button::new(label)
                .variant(ButtonVariant::Contrast)
                .icon(&icons::PHONE)
                .disabled(!call.can_call())
                .on_press(|w: &mut Win| {
                    w.sync_route();
                    let effects = w.call.call();
                    w.run(effects);
                })
                .build(kit())
                .key("voice-call")
                .w_full(),
        );
        if let Some(reason) = call.unavailable().filter(|r| r != OCCUPIED && r != ELSEWHERE && call.error.as_ref() != Some(r) && call.connection_error.as_ref() != Some(r)) {
            let tone = if call.setup_stuck() { failed() } else { faint() };
            dock = dock.child(text(reason).font(META).color(tone).wrap().w_full().key("voice-unavailable"));
        }
        if call.routed() {
            dock = dock.child(
                row().child(small("Talk to the orchestrator instead", ButtonVariant::GhostMuted).on_press(|w: &mut Win| w.route("", "")).build(kit()).key("voice-unroute")),
            );
        }
        return dock;
    }
    if w.ask == Some(Ask::StopTarget) {
        dock = dock.child(confirm(
            "Stop the selected session's active turn?",
            "Audio stays connected. This doesn't hang up or stop any other agent.",
            "Stop selected session",
            |w: &mut Win| {
                w.ask = None;
                let effects = w.call.interrupt_target();
                w.run(effects);
            },
        ));
    }
    let connected = call.connected_call();
    let mut buttons = row().w_full().gap(8.0).items_center();
    if connected {
        if let Some((target, name)) = w.offer.clone() {
            let label = if target.is_empty() { "Return to orchestrator".to_string() } else { format!("Transfer to {}", if name.is_empty() { &target } else { &name }) };
            buttons = buttons.child(
                small(label, ButtonVariant::Neutral)
                    .disabled(call.working())
                    .on_press(|w: &mut Win| {
                        w.sync_route();
                        if let Some((target, name)) = w.offer.take() {
                            w.name = name;
                            let effects = w.call.transfer(&target);
                            w.run(effects);
                        }
                    })
                    .build(kit())
                    .key("voice-transfer"),
            );
        }
        if call.state.as_ref().is_some_and(|s| s.target.is_some()) {
            buttons = buttons.child(
                small("Stop selected session", ButtonVariant::Ghost).icon(&icons::STOP).disabled(call.working()).on_press(|w: &mut Win| w.ask = Some(Ask::StopTarget)).build(kit()).key("voice-stop-target"),
            );
        }
    }
    buttons = buttons
        .child(el().grow(1.0))
        .child(
            small(if call.muted { "Unmute" } else { "Mute" }, ButtonVariant::Neutral)
                .icon(if call.muted { &icons::MIC_OFF } else { &icons::MIC })
                .disabled(!call.audio_connected)
                .on_press(|w: &mut Win| {
                    let effects = w.call.toggle_mute();
                    w.run(effects);
                })
                .build(kit())
                .key("voice-mute"),
        )
        .child(
            small("Hang up", ButtonVariant::Danger)
                .icon(&icons::PHONE_OFF)
                .disabled(occupied)
                .on_press(|w: &mut Win| {
                    let effects = w.call.hangup();
                    w.run(effects);
                })
                .build(kit())
                .key("voice-hangup"),
        );
    dock = dock.child(buttons);
    if connected {
        let input = text_input(&w.typed, BODY)
            .single_line()
            .key("voice-type")
            .label("Type to the call")
            .grow(1.0)
            .min_w(0.0)
            .color(theme::text())
            .placeholder("Type to the call", faint())
            .on_edit(|w: &mut Win| &mut w.typed)
            .on_submit(|w: &mut Win, _: &mut Tx| send_typed(w));
        dock = dock.child(row().w_full().px(10.0).py(7.0).radius(8.0).fill(wash()).border(1.0, line()).child(input));
    }
    dock.child(text("Audio ends. Your agents keep working.").font(META).color(faint()))
}

fn send_typed(w: &mut Win) {
    let words = w.typed.text.clone();
    let effects = w.call.send_text(&words);
    if !effects.is_empty() {
        w.typed = rune::TextEdit::new("");
        w.run(effects);
    }
}
