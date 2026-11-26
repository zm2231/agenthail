use feed::voice::{Call, Effect, Job, State};
use serde_json::{Value, json};

use super::{Ask, Win};

pub const SCENES: &[(&str, fn(&mut Win))] = &[
    ("ready", ready),
    ("connected", connected),
    ("speaking", speaking),
    ("muted", muted),
    ("routed", routed),
    ("transfer", transfer),
    ("stop-target", stop_target),
    ("denied", denied),
    ("occupied", occupied),
    ("elsewhere", elsewhere),
    ("unconfirmed", unconfirmed),
    ("host-ended", host_ended),
    ("new-conversation", new_conversation),
];

fn state(mut value: Value) -> State {
    value["protocol"] = json!(1);
    serde_json::from_value(value).unwrap_or_default()
}

fn job(effects: Vec<Effect>) -> Option<Job> {
    effects.into_iter().find_map(|e| if let Effect::Fetch(job) = e { Some(job) } else { None })
}

fn answer(call: &mut Call, effects: Vec<Effect>, value: Value) -> Vec<Effect> {
    match job(effects) {
        Some(job) => call.reply(job, Ok(state(value)), 0.0),
        None => Vec::new(),
    }
}

fn fresh(w: &mut Win, target: &str, name: &str) {
    w.call = Call::new(target);
    w.name = name.to_string();
    w.offer = None;
    w.ask = None;
    w.call.peer("ready", "", 0.0);
    let refresh = w.call.tick(0.0);
    answer(&mut w.call, refresh, json!({"phase": "ready", "session": {"id": "operator", "name": "Agenthail orchestrator"}}));
}

fn dial(w: &mut Win) -> String {
    let target = (!w.call.target.is_empty()).then(|| json!({"id": w.call.target, "name": w.name}));
    let base = json!({"phase": "ready", "session": {"id": "operator", "name": "Agenthail orchestrator"}, "target": target});
    let select = w.call.call();
    let prepare = answer(&mut w.call, select, base.clone());
    answer(&mut w.call, prepare, base);
    w.call.mic(true);
    w.call.attempt().unwrap_or_default().to_string()
}

fn talk(w: &mut Win, events: Value) {
    let id = dial(w);
    let target = (!w.call.target.is_empty()).then(|| json!({"id": w.call.target, "name": w.name}));
    let start = w.call.peer("offer", "v=0", 0.0);
    answer(&mut w.call, start, json!({"phase": "negotiating", "attemptId": id}));
    w.call.peer("connection", "connected", 0.0);
    let connected = w.call.peer("channel", "open", 0.0);
    answer(&mut w.call, connected, json!({"phase": "connected", "attemptId": id, "session": {"id": "operator", "name": "Agenthail orchestrator"}, "target": target, "events": events}));
}

fn lines(open: bool) -> Value {
    let mut events = vec![
        json!({"sequence": 1, "method": "thread/realtime/transcript/done", "params": {"role": "user", "text": "What is the builder working on?"}}),
        json!({"sequence": 2, "method": "thread/realtime/transcript/done", "params": {"role": "assistant", "text": "The builder is running the fixture test suite. Two of five packages have passed so far."}}),
        json!({"sequence": 3, "method": "thread/realtime/transcript/done", "params": {"role": "user", "text": "Tell me when it finishes."}}),
    ];
    if open {
        events.push(json!({"sequence": 4, "method": "thread/realtime/transcript/delta", "params": {"role": "assistant", "delta": "I'll tell you as soon as the last package"}}));
    }
    Value::Array(events)
}

fn ready(w: &mut Win) {
    fresh(w, "", "");
}

fn connected(w: &mut Win) {
    fresh(w, "", "");
    talk(w, lines(false));
}

fn speaking(w: &mut Win) {
    fresh(w, "", "");
    talk(w, lines(true));
}

fn muted(w: &mut Win) {
    connected(w);
    w.call.toggle_mute();
}

fn routed(w: &mut Win) {
    fresh(w, "builder", "Builder");
    talk(w, lines(false));
}

fn transfer(w: &mut Win) {
    connected(w);
    w.offer = Some(("builder".into(), "Builder".into()));
}

fn stop_target(w: &mut Win) {
    routed(w);
    w.ask = Some(Ask::StopTarget);
}

fn denied(w: &mut Win) {
    fresh(w, "", "");
    let select = w.call.call();
    let prepare = answer(&mut w.call, select, json!({"phase": "ready"}));
    answer(&mut w.call, prepare, json!({"phase": "ready"}));
    w.call.mic(false);
}

fn other(w: &mut Win, value: Value) {
    w.call = Call::new("");
    w.call.peer("ready", "", 0.0);
    let refresh = w.call.tick(0.0);
    answer(&mut w.call, refresh, value);
}

fn occupied(w: &mut Win) {
    other(w, json!({"phase": "connected", "occupied": true}));
}

fn elsewhere(w: &mut Win) {
    other(w, json!({"phase": "connected", "attemptId": "another-window", "session": {"id": "operator", "name": "Agenthail orchestrator"}}));
}

fn unconfirmed(w: &mut Win) {
    connected(w);
    if let Some(stop) = job(w.call.hangup()) {
        w.call.reply(stop, Err("the daemon didn't answer in time".into()), 1.0);
    }
}

fn host_ended(w: &mut Win) {
    fresh(w, "", "");
    let id = dial(w);
    let start = w.call.peer("offer", "v=0", 0.0);
    answer(&mut w.call, start, json!({"phase": "negotiating", "attemptId": id}));
    let refresh = w.call.tick(5.0);
    answer(&mut w.call, refresh, json!({"phase": "ended", "attemptId": id, "message": "Codex can't start a realtime call with this login. Sign in again with codex login."}));
}

fn new_conversation(w: &mut Win) {
    fresh(w, "", "");
    w.ask = Some(Ask::NewConversation);
}
