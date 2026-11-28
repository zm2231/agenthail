use serde_json::json;

use super::*;
use crate::mock::{daemon, replies};

fn state(value: Value) -> State {
    let mut value = value;
    value["protocol"] = json!(1);
    serde_json::from_value(value).unwrap()
}

fn jobs(effects: &[Effect]) -> Vec<Job> {
    effects.iter().filter_map(|e| if let Effect::Fetch(job) = e { Some(job.clone()) } else { None }).collect()
}

fn only_job(effects: &[Effect]) -> Job {
    let mut list = jobs(effects);
    assert_eq!(list.len(), 1, "{effects:?}");
    list.remove(0)
}

fn refresh(effects: &[Effect]) -> Job {
    jobs(effects).into_iter().find(|j| j.req == Req::Refresh).unwrap()
}

fn beat(effects: &[Effect]) -> Option<Job> {
    jobs(effects).into_iter().find(|j| matches!(j.req, Req::Heartbeat(_)))
}

fn named(job: &Job) -> &str {
    job.action.as_ref().map(|a| a.action.as_str()).unwrap_or("GET")
}

fn ready() -> Call {
    let mut call = Call::new("");
    call.peer("ready", "", 0.0);
    let refresh = only_job(&call.tick(0.0));
    call.reply(refresh, Ok(state(json!({"phase": "ready"}))), 0.0);
    call
}

fn dialed(call: &mut Call) -> String {
    let select = only_job(&call.call());
    assert_eq!(named(&select), "select");
    assert_eq!(select.action.as_ref().unwrap().target_id.as_deref(), Some(""));
    let prepare = only_job(&call.reply(select, Ok(state(json!({"phase": "ready"}))), 0.0));
    assert_eq!(named(&prepare), "prepare");
    let asked = call.reply(prepare, Ok(state(json!({"phase": "ready", "session": {"id": "op", "name": "Agenthail orchestrator"}}))), 0.0);
    let id = call.attempt().unwrap().to_string();
    assert_eq!(asked, vec![Effect::AskMic(id.clone())]);
    assert_eq!(call.mic(&id, true), vec![Effect::PeerStart(id.clone())]);
    assert!(call.mic(&id, true).is_empty(), "one answer per ask");
    assert!(!call.working(), "the call button frees once the peer starts");
    id
}

fn started(call: &mut Call, now: f64) -> String {
    let id = dialed(call);
    let start = only_job(&call.peer("offer", "v=0 offer", now));
    assert_eq!(start.req, Req::Start(id.clone()));
    assert_eq!(start.action.as_ref().unwrap().sdp.as_deref(), Some("v=0 offer"));
    call.reply(start, Ok(state(json!({"phase": "negotiating", "attemptId": id}))), now);
    id
}

#[test]
fn transcript_lines_join_deltas_and_take_the_final_text() {
    let s = state(json!({"phase": "connected", "events": [
        {"sequence": 1, "method": "thread/realtime/transcript/delta", "params": {"role": "user", "delta": "What is "}},
        {"sequence": 2, "method": "thread/realtime/transcript/delta", "params": {"role": "user", "delta": "running"}},
        {"sequence": 3, "method": "thread/realtime/transcript/done", "params": {"role": "user", "text": "What is running?"}},
        {"sequence": 4, "method": "thread/realtime/transcript/delta", "params": {"role": "assistant", "delta": "Two builds"}},
        {"sequence": 5, "method": "voice/heartbeat", "params": {"count": 3}},
    ]}));
    let lines = s.lines();
    assert_eq!(lines.len(), 2);
    assert_eq!((lines[0].role, lines[0].text.as_str(), lines[0].open), (Role::You, "What is running?", false));
    assert_eq!((lines[1].role, lines[1].text.as_str(), lines[1].open), (Role::Agent, "Two builds", true));
    assert_eq!(s.talk(), Talk::Speaking);
}

#[test]
fn a_new_realtime_session_closes_open_lines() {
    let s = state(json!({"phase": "connected", "events": [
        {"sequence": 1, "method": "thread/realtime/transcript/delta", "params": {"role": "user", "delta": "Hel"}},
        {"sequence": 2, "method": "thread/realtime/started", "params": {}},
        {"sequence": 3, "method": "thread/realtime/transcript/delta", "params": {"role": "user", "delta": "Again"}},
    ]}));
    let lines = s.lines();
    assert_eq!(lines.iter().map(|l| (l.text.as_str(), l.open)).collect::<Vec<_>>(), vec![("Hel", false), ("Again", true)]);
    assert_eq!(s.talk(), Talk::Hearing);
}

#[test]
fn a_whole_call_connects_mutes_and_hangs_up() {
    let mut call = ready();
    assert!(call.can_call());
    let id = started(&mut call, 0.0);
    let ticked = call.tick(1.0);
    let first = beat(&ticked).unwrap();
    assert_eq!(first.req, Req::Heartbeat(id.clone()), "the first heartbeat goes out as soon as the host has the call");
    call.reply(first, Ok(state(json!({"phase": "negotiating", "attemptId": id}))), 1.0);
    let answer = call.reply(refresh(&ticked), Ok(state(json!({"phase": "negotiating", "attemptId": id, "sdp": "v=0 answer"}))), 1.0);
    assert_eq!(answer, vec![Effect::PeerAnswer("v=0 answer".into())]);
    let again = refresh(&call.tick(2.0));
    assert!(call.reply(again, Ok(state(json!({"phase": "negotiating", "attemptId": id, "sdp": "v=0 answer"}))), 2.0).is_empty(), "an answer is applied once");
    assert!(call.peer("connection", "connected", 2.0).is_empty());
    let connected = only_job(&call.peer("channel", "open", 2.0));
    assert_eq!(connected.req, Req::Connected(id.clone()));
    call.reply(connected, Ok(state(json!({"phase": "connected", "attemptId": id}))), 2.0);
    assert!(call.connected_call());
    assert_eq!(call.title(), "Listening");

    let muted = call.toggle_mute();
    assert_eq!(muted[0], Effect::PeerMute(true));
    let input = only_job(&muted);
    let report = input.action.clone().unwrap();
    assert_eq!((report.action.as_str(), report.muted, report.input_revision, report.attempt_id.as_deref()), ("input-state", Some(true), Some(1), Some(id.as_str())));
    assert_eq!(call.title(), "Microphone muted");

    let stop = only_job(&call.hangup());
    assert_eq!(named(&stop), "stop");
    assert_eq!(stop.action.as_ref().unwrap().attempt_id.as_deref(), Some(id.as_str()));
    assert!(call.hangup_pending);
    call.reply(stop, Ok(state(json!({"phase": "stopping", "attemptId": id}))), 3.0);
    let ended = refresh(&call.tick(4.0));
    call.reply(ended, Ok(state(json!({"phase": "ended", "attemptId": id}))), 4.0);
    assert!(!call.hangup_pending && !call.active());
    assert!(call.can_call());
    assert_eq!(call.error, None);
}

#[test]
fn a_denied_microphone_ends_the_attempt_before_any_start() {
    let mut call = ready();
    let select = only_job(&call.call());
    let prepare = only_job(&call.reply(select, Ok(state(json!({"phase": "ready"}))), 0.0));
    call.reply(prepare, Ok(state(json!({"phase": "ready"}))), 0.0);
    let id = call.attempt().unwrap().to_string();
    assert!(call.mic(&id, false).is_empty(), "no audio was started, so there is none to end");
    assert_eq!(call.trail.last().unwrap().failed_at.as_deref(), Some("native_audio_start"));
    assert!(call.mic_denied);
    assert_eq!(call.error.as_deref(), Some(MIC_DENIED));
    assert!(!call.active() && call.can_call());
}

#[test]
fn a_call_this_window_did_not_start_is_never_heartbeat_or_stopped() {
    let mut call = Call::new("");
    call.peer("ready", "", 0.0);
    let refresh = only_job(&call.tick(0.0));
    call.reply(refresh, Ok(state(json!({"phase": "connected", "attemptId": "theirs"}))), 0.0);
    assert!(call.elsewhere());
    assert_eq!(call.unavailable().as_deref(), Some(ELSEWHERE));
    assert!(call.call().is_empty());
    assert!(jobs(&call.hangup()).is_empty());
    assert!(jobs(&call.tick(20.0)).iter().all(|j| j.req == Req::Refresh));
}

#[test]
fn another_device_owns_the_call() {
    let mut call = Call::new("");
    call.peer("ready", "", 0.0);
    let refresh = only_job(&call.tick(0.0));
    call.reply(refresh, Ok(state(json!({"phase": "connected", "occupied": true}))), 0.0);
    assert!(!call.elsewhere());
    assert_eq!(call.unavailable().as_deref(), Some(OCCUPIED));
}

#[test]
fn heartbeats_run_every_ten_seconds_and_a_stalled_host_ends_the_call_at_the_deadline() {
    let mut call = ready();
    let id = started(&mut call, 0.0);
    let first = beat(&call.tick(0.0)).unwrap();
    assert!(beat(&call.tick(0.5)).is_none(), "one heartbeat at a time");
    call.reply(first, Ok(state(json!({"phase": "negotiating", "attemptId": id}))), 0.5);
    assert!(beat(&call.tick(10.0)).is_none());
    let stalled = beat(&call.tick(10.5));
    assert!(stalled.is_some());
    assert!(!call.tick(30.4).contains(&Effect::PeerEnd));
    let after = call.tick(30.6);
    assert!(after.contains(&Effect::PeerEnd), "the deadline holds while the heartbeat is still waiting");
    assert!(jobs(&after).iter().any(|j| named(j) == "stop"));
    assert!(call.error.as_deref().unwrap().starts_with("The connection to Agenthail was lost"));
    call.reply(stalled.unwrap(), Err("timed out".into()), 40.0);
    assert!(!call.active() || call.hangup_pending);
}

#[test]
fn failed_heartbeats_inside_the_grace_keep_the_call() {
    let mut call = ready();
    let id = started(&mut call, 0.0);
    let first = beat(&call.tick(0.0)).unwrap();
    call.reply(first, Err("connection refused".into()), 5.0);
    let second = beat(&call.tick(15.0)).unwrap();
    assert_eq!(second.req, Req::Heartbeat(id.clone()));
    call.reply(second, Ok(state(json!({"phase": "negotiating", "attemptId": id}))), 15.0);
    assert!(!call.tick(40.0).contains(&Effect::PeerEnd), "a heartbeat that landed restarts the grace");
    assert!(call.attempt().is_some());
}

#[test]
fn a_heartbeat_that_finds_the_host_stopping_hangs_up() {
    let mut call = ready();
    let id = started(&mut call, 0.0);
    let first = beat(&call.tick(10.0)).unwrap();
    let after = call.reply(first, Ok(state(json!({"phase": "stopping", "attemptId": id, "message": "Codex closed the call"}))), 10.0);
    assert!(after.contains(&Effect::PeerEnd));
    assert_eq!(call.error.as_deref(), Some("Codex closed the call"));
    assert!(call.attempt().is_none());
}

#[test]
fn a_start_that_answers_after_hangup_is_stopped_without_touching_the_hangup() {
    let mut call = ready();
    let id = dialed(&mut call);
    let start = only_job(&call.peer("offer", "v=0 offer", 0.0));
    call.hangup();
    let stop = only_job(&call.reply(start, Ok(state(json!({"phase": "negotiating", "attemptId": id}))), 1.0));
    assert_eq!(named(&stop), "stop");
    assert_eq!(stop.action.unwrap().attempt_id, Some(id));
}

#[test]
fn an_unconfirmed_hangup_can_be_retried_for_the_same_attempt() {
    let mut call = ready();
    let id = started(&mut call, 0.0);
    let stop = only_job(&call.hangup());
    call.reply(stop, Err("timed out".into()), 1.0);
    assert!(call.hangup_unconfirmed && !call.hangup_pending);
    assert_eq!(call.title(), "Audio off; hangup unconfirmed");
    assert!(!call.can_call());
    let again = only_job(&call.hangup());
    assert_eq!(again.action.unwrap().attempt_id, Some(id));
    assert!(call.hangup_pending);
}

#[test]
fn a_failed_refresh_shows_reconnecting_until_the_next_one_lands() {
    let mut call = ready();
    let failed = only_job(&call.tick(1.0));
    call.reply(failed, Err("connection refused".into()), 1.0);
    assert_eq!(call.title(), "Reconnecting to Agenthail");
    assert!(!call.can_call());
    let next = only_job(&call.tick(2.0));
    call.reply(next, Ok(state(json!({"phase": "ready"}))), 2.0);
    assert_eq!(call.title(), "Ready to talk");
}

#[test]
fn audio_that_stays_disconnected_for_ten_seconds_ends_the_call() {
    let mut call = ready();
    started(&mut call, 0.0);
    call.peer("connection", "connected", 1.0);
    call.peer("connection", "disconnected", 2.0);
    assert!(!call.tick(11.0).contains(&Effect::PeerEnd));
    assert!(call.tick(12.0).contains(&Effect::PeerEnd));
    assert!(call.error.as_deref().unwrap().starts_with("Audio could not reconnect"));
}

#[test]
fn the_host_ending_a_dialing_call_says_so() {
    let mut call = ready();
    let id = started(&mut call, 0.0);
    let ended = refresh(&call.tick(1.0));
    let after = call.reply(ended, Ok(state(json!({"phase": "ended", "attemptId": id}))), 1.0);
    assert!(after.contains(&Effect::PeerEnd));
    assert_eq!(call.error.as_deref(), Some("The host ended this voice call before audio connected."));
    assert!(!call.active());
}

#[test]
fn a_closed_window_stops_polling_once_no_call_is_left() {
    let mut call = ready();
    call.visible = false;
    assert!(jobs(&call.tick(5.0)).is_empty());
    call.visible = true;
    assert_eq!(only_job(&call.tick(5.0)).req, Req::Refresh);
}

#[test]
fn typing_transfer_and_new_conversation_need_the_right_phase() {
    let mut call = ready();
    assert!(call.transfer("builder").is_empty(), "transfer needs a connected call");
    let new = only_job(&call.start_new());
    assert_eq!(named(&new), "new");
    assert!(call.start_new().is_empty(), "one action at a time");
    call.reply(new, Ok(state(json!({"phase": "ready"}))), 0.0);
    let id = started(&mut call, 0.0);
    call.peer("connection", "connected", 1.0);
    let connected = only_job(&call.peer("channel", "open", 1.0));
    call.reply(connected, Ok(state(json!({"phase": "connected", "attemptId": id}))), 1.0);
    assert!(call.start_new().is_empty(), "no new conversation mid-call");
    let typed = only_job(&call.send_text("  check the build  "));
    let action = typed.action.clone().unwrap();
    assert_eq!((action.text.as_deref(), action.attempt_id.as_deref()), (Some("check the build"), Some(id.as_str())));
    assert!(action.message_id.is_some());
    call.reply(typed, Err("timed out".into()), 2.0);
    assert!(call.error.as_deref().unwrap().starts_with("Text outcome is unknown"));
    let moved = only_job(&call.transfer("builder"));
    assert_eq!(moved.action.unwrap().target_id.as_deref(), Some("builder"));
    assert!(call.routed());
}

#[test]
fn the_client_reads_state_posts_actions_and_reports_refusals() {
    let (client, rx) = daemon(replies(&[
        (200, r#"{"protocol":1,"phase":"ready","events":[],"truncated":false,"occupied":false}"#),
        (200, r#"{"protocol":1,"phase":"negotiating","attemptId":"a1","sdp":"v=0","events":[],"truncated":false,"occupied":false}"#),
        (409, r#"{"error":{"code":"voice_blocked","message":"end the current audio call before selecting a different voice target"},"state":{"protocol":1,"phase":"connected"}}"#),
        (200, r#"{"protocol":2,"phase":"ready"}"#),
    ]));
    assert_eq!(client.voice_state().unwrap().phase, "ready");
    assert_eq!(rx.recv().unwrap().0, "/api/v1/voice");
    let start = Action { sdp: Some("v=0 offer".into()), ..Action::new("start").attempt("a1") };
    let next = client.voice(&start).unwrap();
    assert_eq!((next.phase.as_str(), next.sdp.as_deref()), ("negotiating", Some("v=0")));
    let (path, body) = rx.recv().unwrap();
    assert_eq!(path, "/api/v1/voice");
    assert_eq!(body, json!({"action": "start", "attemptId": "a1", "sdp": "v=0 offer"}));
    let refused = client.voice(&Action { target_id: Some("x".into()), ..Action::new("select") }).unwrap_err();
    assert_eq!(refused.message, "end the current audio call before selecting a different voice target");
    assert_eq!(refused.host.map(|h| h.phase).as_deref(), Some("connected"), "a refusal carries the host's state");
    assert!(client.voice_state().unwrap_err().contains("voice protocol 2"));
}

#[test]
fn the_peer_page_is_loopback_or_https_and_locked_to_its_path() {
    let peer = Client::new("http://0.0.0.0:7412", "secret").voice_peer().unwrap();
    assert_eq!(peer.url, "http://127.0.0.1:7412/api/v1/voice/peer");
    assert_eq!(peer.authorization, "Bearer secret");
    assert!(peer_allowed("http://127.0.0.1:7412/api/v1/voice/peer", &peer));
    assert!(!peer_allowed("http://127.0.0.1:7412/api/v1/snapshot", &peer));
    assert!(!peer_allowed("http://127.0.0.1:7413/api/v1/voice/peer", &peer));
    assert!(!peer_allowed("https://example.com/api/v1/voice/peer", &peer));
    assert!(Client::new("http://10.0.0.5:7412", "t").voice_peer().is_err());
    assert!(Client::new("https://mac.tail.ts.net", "t").voice_peer().is_ok());
}

#[test]
fn attempt_ids_are_unique() {
    let a = fresh_id();
    assert_eq!(a.len(), 36);
    assert_ne!(a, fresh_id());
}

#[test]
fn a_late_start_for_an_old_attempt_never_touches_the_new_one() {
    let mut call = ready();
    let old = dialed(&mut call);
    let late = only_job(&call.peer("offer", "v=0 offer", 0.0));
    let stop = only_job(&call.hangup());
    call.reply(stop, Ok(state(json!({"phase": "stopping", "attemptId": old}))), 0.5);
    let ended = refresh(&call.tick(1.0));
    call.reply(ended, Ok(state(json!({"phase": "ended", "attemptId": old}))), 1.0);
    let new = started(&mut call, 1.0);
    let cleanup = only_job(&call.reply(late, Ok(state(json!({"phase": "negotiating", "attemptId": old}))), 2.0));
    assert_eq!((cleanup.req.clone(), cleanup.action.clone().unwrap().attempt_id), (Req::Abandon, Some(old)));
    assert!(call.reply(cleanup, Err("stop requires the current call".into()), 2.5).is_empty());
    assert!(!call.hangup_unconfirmed && !call.hangup_pending);
    assert_eq!(call.attempt(), Some(new.as_str()));
}

#[test]
fn a_microphone_answer_for_an_earlier_attempt_is_ignored() {
    let mut call = ready();
    let select = only_job(&call.call());
    let prepare = only_job(&call.reply(select, Ok(state(json!({"phase": "ready"}))), 0.0));
    call.reply(prepare, Ok(state(json!({"phase": "ready"}))), 0.0);
    let old = call.attempt().unwrap().to_string();
    call.hangup();
    let select = only_job(&call.call());
    assert!(call.mic(&old, true).is_empty(), "the new attempt hasn't asked yet");
    let prepare = only_job(&call.reply(select, Ok(state(json!({"phase": "ready"}))), 1.0));
    call.reply(prepare, Ok(state(json!({"phase": "ready"}))), 1.0);
    assert!(call.mic(&old, true).is_empty());
    let new = call.attempt().unwrap().to_string();
    assert_eq!(call.mic(&new, true), vec![Effect::PeerStart(new.clone())]);
}

#[test]
fn a_start_bound_to_another_target_than_the_select_resolved_is_hung_up() {
    let mut call = Call::new("s-builder");
    call.peer("ready", "", 0.0);
    let first = only_job(&call.tick(0.0));
    call.reply(first, Ok(state(json!({"phase": "ready"}))), 0.0);
    let select = only_job(&call.call());
    let prepare = only_job(&call.reply(select, Ok(state(json!({"phase": "ready", "target": {"id": "s-builder", "name": "Builder"}}))), 0.0));
    call.reply(prepare, Ok(state(json!({"phase": "ready"}))), 0.0);
    let id = call.attempt().unwrap().to_string();
    call.mic(&id, true);
    let start = only_job(&call.peer("offer", "v=0", 0.0));
    let after = call.reply(start, Ok(state(json!({"phase": "negotiating", "attemptId": id, "target": {"id": "s-reviewer", "name": "Reviewer"}}))), 1.0);
    assert!(after.contains(&Effect::PeerEnd));
    assert_eq!(named(&only_job(&after)), "stop");
    assert!(call.error.as_deref().unwrap().starts_with("Another Agenthail window changed"));
}

#[test]
fn a_start_bound_to_the_requested_session_or_to_none_for_the_orchestrator_keeps_going() {
    let mut call = Call::new("s-builder");
    call.peer("ready", "", 0.0);
    let first = only_job(&call.tick(0.0));
    call.reply(first, Ok(state(json!({"phase": "ready"}))), 0.0);
    let id = dialed_routed(&mut call);
    let start = only_job(&call.peer("offer", "v=0", 0.0));
    let after = call.reply(start, Ok(state(json!({"phase": "negotiating", "attemptId": id, "target": {"id": "s-builder", "name": "Builder"}}))), 1.0);
    assert!(!after.contains(&Effect::PeerEnd));
    assert_eq!(call.error, None);
    let mut orchestrator = ready();
    let id = dialed(&mut orchestrator);
    let start = only_job(&orchestrator.peer("offer", "v=0", 0.0));
    let after = orchestrator.reply(start, Ok(state(json!({"phase": "negotiating", "attemptId": id, "target": {"id": "s-other", "name": "Other"}}))), 1.0);
    assert!(after.contains(&Effect::PeerEnd), "an orchestrator call that comes back routed is hung up");
}

#[test]
fn a_call_to_an_alias_keeps_going_when_the_start_binds_the_session_it_names() {
    let mut call = Call::new("builder");
    call.peer("ready", "", 0.0);
    let first = only_job(&call.tick(0.0));
    call.reply(first, Ok(state(json!({"phase": "ready"}))), 0.0);
    let select = only_job(&call.call());
    let prepare = only_job(&call.reply(select, Ok(state(json!({"phase": "ready", "target": {"id": "s-builder", "name": "Builder"}}))), 0.0));
    call.reply(prepare, Ok(state(json!({"phase": "ready"}))), 0.0);
    let id = call.attempt().unwrap().to_string();
    call.mic(&id, true);
    let start = only_job(&call.peer("offer", "v=0", 0.0));
    let after = call.reply(start, Ok(state(json!({"phase": "negotiating", "attemptId": id, "target": {"id": "s-builder", "name": "Builder"}}))), 1.0);
    assert!(!after.contains(&Effect::PeerEnd));
    assert_eq!(call.error, None);
}

#[test]
fn a_failed_prepare_is_logged_with_its_step() {
    let mut call = ready();
    let select = only_job(&call.call());
    let prepare = only_job(&call.reply(select, Ok(state(json!({"phase": "ready"}))), 0.0));
    call.reply(prepare, Err("the orchestrator couldn't start".into()), 0.0);
    let logged = call.trail.last().unwrap();
    assert_eq!((logged.cause.as_deref(), logged.failed_at.as_deref()), (Some("operator_prepare_failed"), Some("operator_prepare")));
}

#[test]
fn a_failed_select_is_logged_as_its_own_step() {
    let mut call = ready();
    let select = only_job(&call.call());
    call.reply(select, Err("no such session".into()), 0.0);
    let logged = call.trail.last().unwrap();
    assert_eq!((logged.cause.as_deref(), logged.failed_at.as_deref()), (Some("target_select_failed"), Some("target_select")));
}

#[test]
fn page_events_from_an_earlier_call_are_ignored() {
    let mut call = ready();
    let old = dialed(&mut call);
    call.hangup();
    let mut call = ready();
    let id = dialed(&mut call);
    assert_ne!(old, id);
    assert!(call.heard(&old, "offer", "v=0", 0.0).is_empty());
    assert!(call.heard(&old, "error", "The microphone capture ended.", 0.0).is_empty());
    assert!(call.heard("", "connection", "failed", 0.0).is_empty());
    assert_eq!(call.error, None);
    assert!(call.trail.last().unwrap().milestones.iter().all(|(name, _)| name != "offer_submitted"));
    assert_eq!(named(&only_job(&call.heard(&id, "offer", "v=0", 0.0))), "start");
    assert!(call.heard("", "ready", "", 0.0).is_empty());
}

fn connected(call: &mut Call) -> String {
    let id = started(call, 0.0);
    call.peer("connection", "connected", 0.0);
    let up = only_job(&call.peer("channel", "open", 0.0));
    call.reply(up, Ok(state(json!({"phase": "connected", "attemptId": id}))), 0.0);
    assert!(call.connected_call());
    id
}

#[test]
fn a_failed_transfer_keeps_the_route_the_call_is_on() {
    let mut call = ready();
    let id = connected(&mut call);
    let to = only_job(&call.transfer("s-builder"));
    assert_eq!(call.target, "s-builder");
    call.reply(to, Err("Agenthail couldn't reach that session.".into()), 1.0);
    assert_eq!(call.target, "", "the call stays with the orchestrator");
    assert!(!call.working());
    let again = only_job(&call.transfer("s-builder"));
    call.reply(again, Ok(state(json!({"phase": "connected", "attemptId": id, "target": {"id": "s-builder", "name": "Builder"}}))), 2.0);
    assert_eq!(call.target, "s-builder");
}

#[test]
fn a_brief_shows_only_for_the_session_it_was_made_for() {
    let host = state(json!({
        "phase": "ready",
        "target": {"id": "s-reviewer", "name": "Reviewer"},
        "brief": {"sessionId": "s-builder", "asOf": "2026-11-26T11:55:00Z", "exchanges": 3}
    }));
    assert!(host.shown_brief().is_none());
    let matching = state(json!({
        "phase": "ready",
        "target": {"id": "s-builder", "name": "Builder"},
        "brief": {"sessionId": "s-builder", "asOf": "2026-11-26T11:55:00Z", "exchanges": 3}
    }));
    let (brief, target) = matching.shown_brief().unwrap();
    assert_eq!((brief.exchanges, target.name.as_str()), (3, "Builder"));
}

#[test]
fn a_transfer_reply_after_its_call_ended_leaves_the_next_route_alone() {
    let mut call = ready();
    connected(&mut call);
    let to = only_job(&call.transfer("s-builder"));
    call.hangup();
    assert_eq!(call.target, "s-builder");
    call.reply(to, Err("Agenthail couldn't reach that session.".into()), 1.0);
    assert_eq!(call.target, "s-builder", "the route the window shows is the one the next call uses");
}

#[test]
fn an_older_transfer_reply_does_not_undo_a_newer_one() {
    let mut call = ready();
    let id = connected(&mut call);
    let first = only_job(&call.transfer("s-builder"));
    call.reply(first.clone(), Ok(state(json!({"phase": "connected", "attemptId": id, "target": {"id": "s-builder", "name": "Builder"}}))), 1.0);
    let second = only_job(&call.transfer("s-reviewer"));
    call.reply(first, Err("late".into()), 1.0);
    assert_eq!(call.target, "s-reviewer");
    call.reply(second, Err("Agenthail couldn't reach that session.".into()), 2.0);
    assert_eq!(call.target, "s-builder");
}

#[test]
fn a_readiness_check_counts_only_while_the_page_is_being_checked() {
    let mut call = Call::new("");
    call.peer("loading", "", 0.0);
    assert!(call.checked(true, 0.0).is_empty(), "no check was asked for yet");
    call.setup(Setup::Check, 0.0);
    call.checked(true, 1.0);
    assert!(call.ready);
    assert!(call.checked(false, 2.0).is_empty(), "a late failed check can't undo a ready page");
    assert!(call.ready && call.error.is_none());
    let mut failing = Call::new("");
    failing.peer("loading", "", 0.0);
    failing.setup(Setup::Check, 0.0);
    failing.checked(false, 1.0);
    assert!(failing.error.as_deref().unwrap().starts_with("The audio page didn't start"));
}

#[test]
fn an_oversized_saved_log_is_trimmed_and_saved_again() {
    let mut trail = Trail::default();
    let attempts: Vec<_> = (0..30)
        .map(|k| {
            trail.begin(&format!("call-{k}"), 0.0);
            trail.end(&format!("call-{k}"), "local_hangup", 1.0);
            trail.attempts.last().unwrap().clone()
        })
        .collect();
    let saved = serde_json::to_string(&attempts).unwrap();
    let loaded = Trail::load(&saved, 7, |_| true).unwrap();
    assert_eq!(loaded.attempts.len(), KEPT_CALLS);
    assert!(loaded.changed);
}

#[test]
fn a_call_the_host_moved_by_voice_offers_the_route_in_the_field() {
    let mut call = ready();
    let id = connected(&mut call);
    assert!(!call.can_transfer_to(""), "already on the orchestrator");
    assert!(call.can_transfer_to("s-builder"));
    let moved = refresh(&call.tick(1.0));
    call.reply(moved, Ok(state(json!({"phase": "connected", "attemptId": id, "target": {"id": "s-builder", "name": "Builder"}}))), 1.0);
    assert_eq!(call.target, "", "the window's own route is unchanged");
    assert!(call.can_transfer_to(""), "the empty field returns the call to the orchestrator");
    assert!(!call.can_transfer_to("s-builder"));
    let back = only_job(&call.transfer(""));
    assert_eq!(back.action.as_ref().unwrap().target_id.as_deref(), Some(""));
}

#[test]
fn only_the_readiness_check_makes_a_reloaded_page_ready() {
    let mut call = ready();
    connected(&mut call);
    call.hangup();
    call.peer("loading", "", 1.0);
    call.setup(Setup::Page, 0.0);
    assert!(call.heard("", "ready", "", 1.0).is_empty());
    assert!(!call.ready, "a ready message the page sent itself doesn't count");
    call.setup(Setup::Check, 0.0);
    call.checked(true, 2.0);
    assert!(call.ready);
}

#[test]
fn a_page_that_fails_its_check_without_a_call_ends_no_audio() {
    let mut call = Call::new("");
    call.peer("loading", "", 0.0);
    call.setup(Setup::Check, 0.0);
    let effects = call.checked(false, 1.0);
    assert!(!effects.contains(&Effect::PeerEnd), "ending audio would reload the page and check it again");
    assert!(!call.ready);
}

#[test]
fn two_owners_of_one_call_log_keep_each_others_calls() {
    let both = |pid: u32| pid == 1 || pid == 2;
    let mut first = Trail::owned_by(1);
    first.begin("a-done", 0.0);
    first.end("a-done", "local_hangup", 1.0);
    let mut second = Trail::load(&first.save(), 2, both).unwrap();
    second.begin("b-live", 0.0);
    second.mark("b-live", "prepared", 1.0);
    let disk = second.save();
    first.begin("a-live", 2.0);
    first.combine(Trail::parse(&disk).unwrap(), both);
    let ids: Vec<_> = first.attempts.iter().map(|a| a.id.as_str()).collect();
    assert_eq!(ids.len(), 3);
    assert!(ids.contains(&"b-live") && ids.contains(&"a-live") && ids.contains(&"a-done"));
    let theirs = first.attempts.iter().find(|a| a.id == "b-live").unwrap();
    assert_eq!(theirs.cause, None, "another running owner's call is never written as ended");
    assert_eq!(first.last().unwrap().id, "a-live", "this window's details show its own call");
    let disk = first.save();
    second.end("b-live", "local_hangup", 3.0);
    second.combine(Trail::parse(&disk).unwrap(), both);
    let mine = second.attempts.iter().find(|a| a.id == "b-live").unwrap();
    assert_eq!(mine.cause.as_deref(), Some("local_hangup"));
    assert!(second.attempts.iter().any(|a| a.id == "a-live"));
}

#[test]
fn a_call_cut_off_when_its_app_quit_stays_unfinished_through_later_saves() {
    let mut cut_off = Trail::owned_by(1);
    cut_off.begin("cut-off", 0.0);
    let disk = cut_off.save();
    let only_me = |pid: u32| pid == 2;
    let mut next = Trail::load(&disk, 2, only_me).unwrap();
    assert!(next.changed);
    next.begin("next", 1.0);
    next.combine(Trail::parse(&disk).unwrap(), only_me);
    let cut = next.attempts.iter().find(|a| a.id == "cut-off").unwrap();
    assert_eq!(cut.cause.as_deref(), Some(UNFINISHED));
    let written = Trail::parse(&next.save()).unwrap();
    assert_eq!(written.attempts.iter().find(|a| a.id == "cut-off").unwrap().cause.as_deref(), Some(UNFINISHED));
}

#[test]
fn a_live_call_survives_twenty_newer_calls_from_another_owner() {
    let both = |pid: u32| pid == 1 || pid == 2;
    let mut mine = Trail::owned_by(1);
    mine.begin("mine-live", 0.0);
    mine.attempts[0].started_ms -= 3_600_000;
    let mut theirs = Trail::owned_by(2);
    for k in 0..KEPT_CALLS {
        theirs.begin(&format!("theirs-{k}"), 0.0);
        theirs.end(&format!("theirs-{k}"), "local_hangup", 1.0);
    }
    mine.combine(Trail::parse(&theirs.save()).unwrap(), both);
    assert_eq!(mine.attempts.len(), KEPT_CALLS);
    assert!(mine.attempts.iter().any(|a| a.id == "mine-live"));
    mine.end("mine-live", "local_hangup", 5.0);
    assert_eq!(mine.last().unwrap().cause.as_deref(), Some("local_hangup"));
    mine.combine(Trail::parse(&theirs.save()).unwrap(), both);
    assert!(mine.attempts.iter().all(|a| a.id != "mine-live"), "once ended it ages out like any other call");
}

#[test]
fn another_owner_saving_many_calls_never_evicts_a_running_owners_call() {
    let mut a = Trail::owned_by(1);
    a.begin("a-live", 0.0);
    a.attempts[0].started_ms -= 3_600_000;
    let mut disk = a.save();
    let mut b = Trail::owned_by(2);
    for k in 0..KEPT_CALLS + 5 {
        let id = format!("b-{k}");
        b.begin(&id, 0.0);
        b.end(&id, "local_hangup", 1.0);
        b.combine(Trail::parse(&disk).unwrap(), |pid| pid == 1 || pid == 2);
        disk = b.save();
    }
    let kept = Trail::parse(&disk).unwrap();
    assert_eq!(kept.attempts.len(), KEPT_CALLS);
    assert!(kept.attempts.iter().any(|a| a.id == "a-live" && a.cause.is_none()));
    let after = Trail::load(&disk, 3, |pid| pid == 2).unwrap();
    let cut = after.attempts.iter().find(|a| a.id == "a-live").unwrap();
    assert_eq!(cut.cause.as_deref(), Some(UNFINISHED), "once its app is gone the call is closed, not lost");
}

#[test]
fn the_log_keeps_the_newest_calls_from_both_owners_within_one_second() {
    let mut a = Trail::owned_by(1);
    let mut b = Trail::owned_by(2);
    for k in 0..15 {
        for (trail, name) in [(&mut a, "a"), (&mut b, "b")] {
            let id = format!("{name}-{k}");
            trail.begin(&id, 0.0);
            trail.end(&id, "local_hangup", 1.0);
            trail.attempts.last_mut().unwrap().started_ms = 1_000_000 + k * 20 + i64::from(name == "b") * 10;
        }
    }
    a.combine(Trail::parse(&b.save()).unwrap(), |_| true);
    let ids: Vec<_> = a.attempts.iter().map(|x| x.id.as_str()).collect();
    assert_eq!(ids.len(), KEPT_CALLS);
    assert_eq!(ids[0], "a-5");
    assert_eq!(ids[KEPT_CALLS - 1], "b-14");
}

#[test]
fn calls_started_in_the_same_millisecond_are_kept_the_same_whichever_owner_saves() {
    let log = |pid: u32, name: &str| {
        let mut trail = Trail::owned_by(pid);
        for k in 0..KEPT_CALLS {
            let id = format!("{name}-{k:02}");
            trail.begin(&id, 0.0);
            trail.end(&id, "local_hangup", 1.0);
            trail.attempts.last_mut().unwrap().started_ms = 5_000;
        }
        trail
    };
    let mut a = log(1, "a");
    let mut b = log(2, "b");
    let (a_disk, b_disk) = (a.save(), b.save());
    a.combine(Trail::parse(&b_disk).unwrap(), |_| true);
    b.combine(Trail::parse(&a_disk).unwrap(), |_| true);
    let ids = |t: &Trail| t.attempts.iter().map(|x| x.id.clone()).collect::<Vec<_>>();
    assert_eq!(ids(&a), ids(&b));
    assert_eq!(ids(&a).len(), KEPT_CALLS);
}

#[test]
fn calls_logged_before_the_saved_log_could_be_read_join_it() {
    let mut saved = Trail::default();
    for k in 0..19 {
        saved.begin(&format!("old-{k}"), 0.0);
        saved.end(&format!("old-{k}"), "local_hangup", 1.0);
    }
    let mut fresh = Trail::owned_by(2);
    fresh.begin("new-0", 0.0);
    fresh.end("new-0", "local_hangup", 1.0);
    fresh.begin("new-1", 2.0);
    fresh.combine(Trail::parse(&saved.save()).unwrap(), |_| false);
    let ids: Vec<_> = fresh.attempts.iter().map(|a| a.id.as_str()).collect();
    assert_eq!(ids.len(), KEPT_CALLS);
    assert!(ids.contains(&"new-0") && ids.contains(&"new-1") && !ids.contains(&"old-0"));
    fresh.mark("new-1", "prepared", 3.0);
    assert_eq!(fresh.last().unwrap().milestones, vec![("prepared".to_string(), 1.0)]);
}

fn dialed_routed(call: &mut Call) -> String {
    let select = only_job(&call.call());
    let prepare = only_job(&call.reply(select, Ok(state(json!({"phase": "ready"}))), 0.0));
    call.reply(prepare, Ok(state(json!({"phase": "ready"}))), 0.0);
    let id = call.attempt().unwrap().to_string();
    call.mic(&id, true);
    id
}

#[test]
fn an_old_heartbeat_reply_does_not_let_a_second_heartbeat_out() {
    let mut call = ready();
    let old = started(&mut call, 0.0);
    let stale = beat(&call.tick(0.0)).unwrap();
    let stop = only_job(&call.hangup());
    call.reply(stop, Ok(state(json!({"phase": "stopping", "attemptId": old}))), 0.5);
    let ended = refresh(&call.tick(1.0));
    call.reply(ended, Ok(state(json!({"phase": "ended", "attemptId": old}))), 1.0);
    let new = started(&mut call, 1.0);
    let pending = beat(&call.tick(1.0)).unwrap();
    assert_eq!(pending.req, Req::Heartbeat(new.clone()));
    call.reply(stale, Err("heartbeat requires the current active voice call".into()), 2.0);
    assert!(beat(&call.tick(2.5)).is_none(), "the new attempt's heartbeat is still out");
    call.reply(pending, Ok(state(json!({"phase": "negotiating", "attemptId": new}))), 3.0);
    assert!(beat(&call.tick(13.0)).is_some());
}

#[test]
fn a_start_refused_because_another_window_called_first_leaves_this_window_free() {
    let mut call = ready();
    let id = dialed(&mut call);
    let start = only_job(&call.peer("offer", "v=0", 0.0));
    let refusal = Failure { message: "end the current audio call before starting another".into(), host: Some(state(json!({"phase": "connected", "attemptId": "another-window"}))) };
    let after = call.reply(start, Err(refusal), 1.0);
    assert!(after.contains(&Effect::PeerEnd));
    let cleanup = only_job(&after);
    assert_eq!((cleanup.req.clone(), cleanup.action.clone().unwrap().attempt_id), (Req::Abandon, Some(id)));
    assert!(call.reply(cleanup, Err("stop requires the current call".into()), 1.5).is_empty());
    assert!(!call.hangup_unconfirmed && !call.hangup_pending && call.attempt().is_none());
    let seen = refresh(&call.tick(2.0));
    call.reply(seen, Ok(state(json!({"phase": "connected", "attemptId": "another-window"}))), 2.0);
    assert_eq!(call.unavailable().as_deref(), Some(ELSEWHERE));
    assert!(jobs(&call.hangup()).is_empty(), "the other window's call is never stopped from here");
    let seen = refresh(&call.tick(3.0));
    call.reply(seen, Ok(state(json!({"phase": "ended", "attemptId": "another-window"}))), 3.0);
    assert!(call.can_call());
}

#[test]
fn a_start_whose_outcome_is_unknown_keeps_a_hangup_that_can_be_retried() {
    for host in [None, Some(state(json!({"phase": "unknown"})))] {
        let mut call = ready();
        let id = dialed(&mut call);
        let start = only_job(&call.peer("offer", "v=0", 0.0));
        let host = host.map(|mut h| {
            h.attempt_id = Some(id.clone());
            h
        });
        let after = call.reply(start, Err(Failure { message: "Codex refused the realtime start".into(), host }), 1.0);
        assert!(after.contains(&Effect::PeerEnd));
        let stop = only_job(&after);
        assert_eq!((stop.req.clone(), stop.action.clone().unwrap().attempt_id), (Req::Stop, Some(id.clone())));
        call.reply(stop, Err("timed out".into()), 2.0);
        assert!(call.hangup_unconfirmed);
        let seen = refresh(&call.tick(3.0));
        call.reply(seen, Ok(state(json!({"phase": "unknown", "attemptId": id}))), 3.0);
        assert!(!call.elsewhere(), "this window's own unknown call is never shown as another window's");
        assert!(call.hangup_unconfirmed);
        let again = only_job(&call.hangup());
        assert_eq!(again.action.unwrap().attempt_id, Some(id));
    }
}

#[test]
fn a_stop_error_after_the_host_confirmed_the_end_changes_nothing() {
    let mut call = ready();
    let id = started(&mut call, 0.0);
    let stop = only_job(&call.hangup());
    let ended = refresh(&call.tick(1.0));
    call.reply(ended, Ok(state(json!({"phase": "ended", "attemptId": id}))), 1.0);
    assert!(!call.hangup_pending && call.can_call());
    call.reply(stop, Err("timed out".into()), 2.0);
    assert!(!call.hangup_unconfirmed && call.error.is_none());
    assert!(call.can_call());
}

#[test]
fn logged_addresses_leave_out_credentials_queries_and_fragments() {
    assert_eq!(shown_address("https://user:secret@example.com/path?token=abc#frag"), "https://example.com/path");
    assert_eq!(shown_address("http://127.0.0.1:7777/api/v1/voice/peer?x=1"), "http://127.0.0.1:7777/api/v1/voice/peer");
    assert_eq!(shown_address("not a url user:secret"), "an unreadable address");
}

#[test]
fn each_audio_setup_stage_says_what_it_waits_for_and_when_it_is_stuck() {
    let mut call = Call::new("");
    assert_eq!(call.unavailable().as_deref(), Some("Call audio is not ready."));
    call.tick(1.0);
    let said = call.setup(Setup::Window, 1.0);
    assert_eq!(said, vec![Effect::Note(Setup::Window.waiting().into())]);
    assert!(call.setup(Setup::Window, 1.5).is_empty(), "the same stage is said once");
    assert_eq!(call.unavailable().as_deref(), Some(Setup::Window.waiting()));
    call.tick(3.9);
    assert!(!call.setup_stuck());
    let stuck = call.tick(4.0);
    assert!(stuck.contains(&Effect::Note(Setup::Window.stuck().into())));
    assert!(call.setup_stuck());
    assert_eq!(call.unavailable().as_deref(), Some(Setup::Window.stuck()));
    assert!(!call.tick(9.0).iter().any(|e| matches!(e, Effect::Note(_))), "stuck is said once");
    call.setup(Setup::Page, 9.0);
    assert!(!call.setup_stuck(), "a later stage starts its own wait");
    call.tick(23.9);
    assert_eq!(call.unavailable().as_deref(), Some(Setup::Page.waiting()));
    call.tick(24.0);
    assert_eq!(call.unavailable().as_deref(), Some(Setup::Page.stuck()));
    call.setup(Setup::Check, 24.0);
    call.peer("ready", "", 25.0);
    assert!(!call.setup_stuck());
    assert_ne!(call.unavailable().as_deref(), Some(Setup::Check.waiting()));
    assert!(call.setup(Setup::Page, 26.0).is_empty(), "a ready page needs no setup");
}

#[test]
fn a_stage_started_after_a_long_gap_waits_its_full_time() {
    let mut call = Call::new("");
    call.tick(1.0);
    assert!(call.setup(Setup::Check, 60.0).contains(&Effect::Note(Setup::Check.waiting().into())));
    assert!(!call.tick(60.1).contains(&Effect::Note(Setup::Check.stuck().into())));
    assert!(!call.setup_stuck());
    assert!(call.tick(65.0).contains(&Effect::Note(Setup::Check.stuck().into())));
}

#[test]
fn an_unavailable_page_replaces_the_wait_with_its_error() {
    let mut call = Call::new("");
    call.setup(Setup::Page, 0.0);
    let effects = call.peer("unavailable", "The audio page opened at an unexpected address: http://127.0.0.1:1/x", 1.0);
    assert!(effects.contains(&Effect::Note("The audio page opened at an unexpected address: http://127.0.0.1:1/x".into())));
    assert_eq!(call.error.as_deref(), Some("The audio page opened at an unexpected address: http://127.0.0.1:1/x"));
    assert_eq!(call.unavailable().as_deref(), Some("Call audio is not ready."));
    assert!(!call.setup_stuck());
}

fn milestones(call: &Call) -> Vec<String> {
    call.trail.last().unwrap().milestones.iter().map(|(n, _)| n.clone()).collect()
}

#[test]
fn the_call_log_follows_one_call_from_prepare_to_hangup() {
    let mut call = ready();
    let id = started(&mut call, 1.0);
    let ticked = call.tick(2.0);
    call.reply(refresh(&ticked), Ok(state(json!({"phase": "negotiating", "attemptId": id, "sdp": "v=0 answer"}))), 2.0);
    call.peer("connection", "connected", 2.5);
    let connected = only_job(&call.peer("channel", "open", 2.5));
    call.reply(connected, Ok(state(json!({"phase": "connected", "attemptId": id}))), 3.0);
    call.hangup();
    let attempt = call.trail.last().unwrap();
    assert_eq!(attempt.id, id);
    assert_eq!(attempt.cause.as_deref(), Some("local_hangup"));
    assert_eq!(
        milestones(&call),
        ["prepared", "microphone_granted", "offer_submitted", "host_start_accepted", "sdp_answer_sent", "peer_connected", "data_channel_open", "host_connected", "local_ended"]
    );
    let at: Vec<f64> = attempt.milestones.iter().map(|(_, s)| *s).collect();
    assert!(at.windows(2).all(|w| w[0] <= w[1]), "{at:?}");
    assert!(call.trail.changed);
}

#[test]
fn the_call_log_names_why_a_call_ended() {
    let mut call = ready();
    call.tick(0.0);
    let select = only_job(&call.call());
    let prepare = only_job(&call.reply(select, Ok(state(json!({"phase": "ready"}))), 0.0));
    call.reply(prepare, Ok(state(json!({"phase": "ready"}))), 0.0);
    let id = call.attempt().unwrap().to_string();
    call.mic(&id, false);
    assert_eq!(call.trail.last().unwrap().cause.as_deref(), Some("microphone_denied"));

    let mut call = ready();
    let id = started(&mut call, 0.0);
    let ticked = call.tick(5.0);
    call.reply(refresh(&ticked), Ok(state(json!({"phase": "ended", "attemptId": id}))), 5.0);
    assert_eq!(call.trail.last().unwrap().cause.as_deref(), Some("host_ended"));

    let mut call = ready();
    let id = dialed(&mut call);
    let start = only_job(&call.peer("offer", "v=0", 0.0));
    call.reply(start, Err(Failure { message: "refused".into(), host: Some(state(json!({"phase": "ready"}))) }), 0.0);
    let attempt = call.trail.last().unwrap();
    assert_eq!((attempt.id.as_str(), attempt.cause.as_deref(), attempt.failed_at.as_deref()), (id.as_str(), Some("host_start_rejected"), Some("host_start")));
}

#[test]
fn a_saved_call_log_keeps_the_last_twenty_and_closes_unfinished_calls() {
    let mut trail = Trail::default();
    for k in 0..25 {
        trail.begin(&format!("call-{k}"), 0.0);
        if k != 24 {
            trail.end(&format!("call-{k}"), "local_hangup", 1.0);
        }
    }
    assert_eq!(trail.attempts.len(), KEPT_CALLS);
    assert_eq!(trail.attempts[0].id, "call-5");
    let loaded = Trail::load(&trail.save(), 7, |_| false).unwrap();
    assert_eq!(loaded.attempts.len(), KEPT_CALLS);
    assert_eq!(loaded.last().unwrap().cause.as_deref(), Some(UNFINISHED));
    assert!(loaded.changed, "an unfinished call is written as unfinished");
    assert_eq!(loaded.attempts[0].milestones, vec![("local_ended".to_string(), 1.0)]);
    assert!(Trail::load("not json", 7, |_| true).is_err());
}

#[test]
fn the_brief_says_who_was_briefed_how_much_and_how_long_ago() {
    let now = crate::catalog::parse_time("2026-11-26T12:00:00Z").unwrap();
    let brief = Brief { session_id: "s-builder".into(), as_of: "2026-11-26T11:55:00Z".into(), bytes: 900, exchanges: 3, omitted: vec![] };
    assert_eq!(brief.summary("Builder", now), "The call was briefed on Builder: 3 recent exchanges, read 5m ago.");
    assert_eq!(brief.left_out(), None);
    let fresh = Brief { as_of: "2026-11-26T11:59:40Z".into(), exchanges: 1, omitted: vec!["tool output".into(), "images".into()], ..brief.clone() };
    assert_eq!(fresh.summary("Builder", now), "The call was briefed on Builder: 1 recent exchange, read just now.");
    assert_eq!(fresh.left_out().as_deref(), Some("Left out of the brief: tool output, images."));
    let undated = Brief { as_of: String::new(), ..brief };
    assert_eq!(undated.summary("Builder", now), "The call was briefed on Builder: 3 recent exchanges.");
}

#[test]
fn voice_details_list_the_connection_call_log_and_orchestrator() {
    let mut call = Call::new("s-builder");
    call.peer("ready", "", 0.0);
    let first = only_job(&call.tick(0.0));
    let host = json!({
        "phase": "ready",
        "audioProvider": "openai",
        "codingProvider": "codex",
        "skillDigest": "abc123",
        "target": {"id": "s-builder", "name": "Builder"},
        "session": {"id": "s-operator", "name": "Operator"}
    });
    call.reply(first, Ok(state(host.clone())), 0.0);
    let select = only_job(&call.call());
    let prepare = only_job(&call.reply(select, Ok(state(host.clone())), 0.0));
    call.reply(prepare, Ok(state(host)), 0.0);
    let id = call.attempt().unwrap().to_string();
    call.mic(&id, true);
    let sections = call.details();
    let titles: Vec<_> = sections.iter().map(|s| s.title).collect();
    assert_eq!(titles, ["Connection", "Call log", "Persistent orchestrator"]);
    let rows = |title: &str| sections.iter().find(|s| s.title == title).unwrap().rows.clone();
    let connection = rows("Connection");
    assert!(connection.contains(&("Coding target".into(), "Builder".into())));
    assert!(connection.contains(&("Coding provider".into(), "codex".into())));
    assert!(connection.contains(&("Audio provider".into(), "openai".into())));
    assert!(connection.iter().all(|(name, value)| !value.trim().is_empty() && name != "Host close"));
    assert_eq!(rows("Call log")[0], ("Call".to_string(), id));
    assert!(rows("Call log").iter().any(|(name, _)| name == "microphone_granted"));
    let orchestrator = rows("Persistent orchestrator");
    assert!(orchestrator.contains(&("Session".into(), "Operator".into())));
    assert!(orchestrator.contains(&("Skill digest".into(), "abc123".into())));
}

fn typed(call: &mut Call, words: &str) -> Job {
    let job = only_job(&call.send_text(words));
    assert_eq!(named(&job), "text");
    job
}

fn sent_words(call: &mut Call) -> Vec<String> {
    call.take_sent().into_iter().map(|(_, words)| words).collect()
}

fn message_id(job: &Job) -> String {
    job.action.as_ref().and_then(|a| a.message_id.clone()).unwrap()
}

#[test]
fn a_retried_text_carries_the_message_id_of_the_failed_try() {
    let mut call = ready();
    connected(&mut call);
    let first = typed(&mut call, "check the build");
    let id = message_id(&first);
    call.reply(first, Err("timed out".into()), 1.0);
    assert_eq!(call.unsent(), ["check the build"]);
    assert!(sent_words(&mut call).is_empty());
    let again = typed(&mut call, "  check the build ");
    assert_eq!(message_id(&again), id, "the same words keep their id");
    call.reply(again, Ok(state(json!({"phase": "connected", "textReceipt": format!("accepted:{id}")}))), 2.0);
    assert_eq!(call.unsent(), Vec::<&str>::new());
    assert_eq!(sent_words(&mut call), ["check the build"]);
    let next = typed(&mut call, "check the build");
    assert_ne!(message_id(&next), id, "a sent message's id is never reused");
}

#[test]
fn edited_text_after_a_failure_is_a_new_message() {
    let mut call = ready();
    connected(&mut call);
    let first = typed(&mut call, "check the build");
    let id = message_id(&first);
    call.reply(first, Err("timed out".into()), 1.0);
    let edited = typed(&mut call, "check the tests");
    assert_ne!(message_id(&edited), id);
}

#[test]
fn a_failed_text_the_host_accepted_counts_as_sent() {
    let mut call = ready();
    let attempt = connected(&mut call);
    let first = typed(&mut call, "check the build");
    let id = message_id(&first);
    let host = state(json!({"phase": "connected", "attemptId": attempt, "textReceipt": format!("accepted:{id}")}));
    call.reply(first, Err(Failure { message: "message already submitted".into(), host: Some(host) }), 1.0);
    assert_eq!(call.unsent(), Vec::<&str>::new());
    assert_eq!(sent_words(&mut call), ["check the build"]);
    assert!(call.error.is_none());
}

#[test]
fn a_refresh_showing_the_text_accepted_clears_the_unknown_outcome() {
    let mut call = ready();
    let attempt = connected(&mut call);
    let first = typed(&mut call, "check the build");
    let id = message_id(&first);
    call.reply(first, Err("timed out".into()), 1.0);
    assert!(call.error.as_deref().unwrap().starts_with("Text outcome is unknown"));
    let poll = refresh(&call.tick(5.0));
    call.reply(poll, Ok(state(json!({"phase": "connected", "attemptId": attempt, "textReceipt": format!("unknown:{id}")}))), 5.0);
    assert_eq!(call.unsent(), ["check the build"], "an unknown receipt settles nothing");
    let poll = refresh(&call.tick(10.0));
    call.reply(poll, Ok(state(json!({"phase": "connected", "attemptId": attempt, "textReceipt": format!("accepted:{id}")}))), 10.0);
    assert_eq!(call.unsent(), Vec::<&str>::new());
    assert_eq!(sent_words(&mut call), ["check the build"]);
    assert!(call.error.is_none());
}

#[test]
fn a_new_call_starts_texts_afresh() {
    let mut call = ready();
    connected(&mut call);
    let attempt = call.attempt().unwrap().to_string();
    let first = typed(&mut call, "check the build");
    call.reply(first, Err("timed out".into()), 1.0);
    let stop = only_job(&call.hangup());
    call.reply(stop, Ok(state(json!({"phase": "ended", "attemptId": attempt}))), 2.0);
    let poll = refresh(&call.tick(3.0));
    call.reply(poll, Ok(state(json!({"phase": "ended", "attemptId": attempt}))), 3.0);
    assert!(call.can_call(), "{:?}", call.unavailable());
    dialed(&mut call);
    assert_eq!(call.unsent(), Vec::<&str>::new());
}

#[test]
fn the_orchestrator_turn_can_be_interrupted_with_or_without_a_call() {
    let mut call = Call::new("");
    assert!(!call.can_interrupt(), "nothing to interrupt before the host names an orchestrator");
    assert!(call.interrupt().is_empty());
    call.peer("ready", "", 0.0);
    let poll = only_job(&call.tick(0.0));
    call.reply(poll, Ok(state(json!({"phase": "ready", "session": {"id": "op", "name": "Orchestrator"}}))), 0.0);
    assert!(call.can_interrupt());
    let stop = only_job(&call.interrupt());
    assert_eq!(named(&stop), "interrupt");
    assert!(call.working());
    assert!(call.interrupt().is_empty(), "one action at a time");
    call.reply(stop, Err("there is no turn to interrupt".into()), 1.0);
    assert!(!call.working());
    assert_eq!(call.error.as_deref(), Some("there is no turn to interrupt"));
}

#[test]
fn another_devices_call_cannot_be_interrupted_from_here() {
    let mut call = Call::new("");
    call.peer("ready", "", 0.0);
    let poll = only_job(&call.tick(0.0));
    call.reply(poll, Ok(state(json!({"phase": "connected", "occupied": true, "session": {"id": "op", "name": "Orchestrator"}}))), 0.0);
    assert!(!call.can_interrupt());
}

fn world() -> crate::derive::World {
    let session = |id: &str, extra: Value| {
        let mut s = json!({"id": id, "name": id, "surface": "codex", "lastActive": "2026-11-24T11:00:00Z", "project": {"id": "p", "name": "Shop"}, "display": {"kind": "idle", "label": "Idle"}});
        s.as_object_mut().unwrap().extend(extra.as_object().unwrap().clone());
        s
    };
    let raw = json!({
        "updatedAt": "2026-11-24T12:00:00Z",
        "projects": [{"id": "p", "displayName": "Shop", "coordinators": [{"name": "lead", "sessionId": "lead-1", "surface": "claude"}]}],
        "sessions": [
            session("lead-1", json!({"surface": "claude", "alias": "lead", "lastActive": "2026-11-24T10:00:00Z"})),
            session("builder", json!({"alias": "builder", "lastActive": "2026-11-24T11:30:00Z", "status": "busy", "display": {"kind": "working", "label": "Working"}})),
            session("tester", json!({"lastActive": "2026-11-24T11:20:00Z"})),
            session("away", json!({"host": {"id": "h2", "name": "Studio", "state": "connected"}})),
            session("notes", json!({"surface": "notion"})),
            session("watcher", json!({"readOnly": true})),
            session("old", json!({"archived": true})),
            session("helper", json!({"subagent": {}})),
        ],
        "transcripts": {"builder": [{"id": "m1", "kind": "message", "role": "assistant", "text": "Running the checkout tests now. Then the cart.", "timestamp": "2026-11-24T11:29:00Z"}]}
    });
    crate::derive::World::from_json(&raw.to_string()).unwrap()
}

#[test]
fn transfer_picks_list_project_leads_then_local_writable_sessions() {
    let picks = world().voice_picks();
    let rows: Vec<(&str, &str)> = picks.iter().map(|p| (p.group, p.id.as_str())).collect();
    assert_eq!(rows, [(LEADS, "lead-1"), (SESSIONS, "builder"), (SESSIONS, "tester")]);
    assert_eq!(picks[0].label(), "Shop's lead");
    assert_eq!(picks[0].status, "@lead");
    assert_eq!(picks[1].status, "Working · Shop");
}

#[test]
fn transfer_picks_match_title_alias_or_id_and_leave_out_the_current_route() {
    let picks = world().voice_picks();
    let ids = |q: &str, skip: &[&str]| matching(&picks, q, skip).iter().map(|p| p.id.clone()).collect::<Vec<_>>();
    assert_eq!(ids("BUILD", &[]), ["builder"]);
    assert_eq!(ids("lead", &[]), ["lead-1"]);
    assert_eq!(ids("shop", &["lead-1"]), ["builder", "tester"]);
    assert_eq!(ids("", &["builder"]), ["lead-1", "tester"]);
    assert!(ids("nobody", &[]).is_empty());
}

#[test]
fn operator_activity_names_a_busy_state_and_the_latest_words() {
    let w = world();
    let busy = w.voice_activity("builder").unwrap();
    assert!(busy.busy);
    assert_eq!(busy.state.as_deref(), Some("Working"));
    assert_eq!(busy.latest.as_deref(), Some("Running the checkout tests now"));
    let idle = w.voice_activity("tester").unwrap();
    assert_eq!((idle.busy, idle.state, idle.latest), (false, None, None));
    assert!(w.voice_activity("missing").is_none());
}

#[test]
fn the_tools_notice_is_split_from_a_failure_it_follows() {
    let notice = "This voice conversation was created before Agenthail's current tools: it still works sessions through the agenthail command line. Choose New conversation to use Agenthail's MCP tools; Codex cannot change an existing conversation's tools.";
    assert_eq!(without_tools_notice(notice), (None, Some(notice.to_string())));
    assert_eq!(without_tools_notice(&format!("Realtime audio failed.\n\n{notice}")), (Some("Realtime audio failed.".into()), Some(notice.to_string())));
    assert_eq!(without_tools_notice("Realtime audio failed."), (Some("Realtime audio failed.".into()), None));
    assert_eq!(without_tools_notice("  "), (None, None));
}

#[test]
fn a_project_with_leads_on_two_hosts_offers_no_lead_pick() {
    let raw = json!({
        "updatedAt": "2026-11-24T12:00:00Z",
        "projects": [{"id": "p", "displayName": "Shop", "coordinators": [{"name": "lead", "sessionId": "lead-1"}, {"name": "lead", "sessionId": "lead-2", "host": {"id": "h2", "state": "connected"}}]}],
        "sessions": [{"id": "lead-1", "name": "lead-1", "surface": "claude", "project": {"id": "p", "name": "Shop"}, "display": {"kind": "idle"}}]
    });
    let picks = crate::derive::World::from_json(&raw.to_string()).unwrap().voice_picks();
    let rows: Vec<(&str, &str)> = picks.iter().map(|p| (p.group, p.id.as_str())).collect();
    assert_eq!(rows, [(SESSIONS, "lead-1")], "the conflict is the daemon's to resolve, never a pick");
}

#[test]
fn a_routed_text_counts_as_sent_once_its_delegation_shows_up() {
    let mut call = ready();
    let attempt = connected(&mut call);
    let moved = only_job(&call.transfer("builder"));
    call.reply(moved, Ok(state(json!({"phase": "connected", "attemptId": attempt, "target": {"id": "builder"}}))), 0.5);
    let first = typed(&mut call, "rerun the tests");
    let id = message_id(&first);
    call.reply(first, Err("timed out".into()), 1.0);
    let poll = refresh(&call.tick(5.0));
    let event = json!({"sequence": 9, "method": "voice/delegation/dispatched", "params": {"messageId": id, "targetId": "builder"}});
    call.reply(poll, Ok(state(json!({"phase": "connected", "attemptId": attempt, "target": {"id": "builder"}, "events": [event]}))), 5.0);
    assert_eq!(call.unsent(), Vec::<&str>::new());
    assert_eq!(sent_words(&mut call), ["rerun the tests"]);
    assert!(call.error.is_none());
}

#[test]
fn a_text_reply_after_hangup_neither_confirms_nor_clears_the_words() {
    let mut call = ready();
    connected(&mut call);
    let first = typed(&mut call, "check the build");
    call.hangup();
    call.reply(first, Ok(state(json!({"phase": "connected"}))), 1.0);
    assert!(sent_words(&mut call).is_empty());
    assert_eq!(call.unsent(), ["check the build"]);
}

#[test]
fn an_earlier_unsettled_text_keeps_its_id_after_another_is_typed() {
    let mut call = ready();
    connected(&mut call);
    let first = typed(&mut call, "check the build");
    let a = message_id(&first);
    call.reply(first, Err("timed out".into()), 1.0);
    let second = typed(&mut call, "check the tests");
    let b = message_id(&second);
    call.reply(second, Err("timed out".into()), 2.0);
    assert_ne!(a, b);
    assert_eq!(message_id(&typed(&mut call, "check the build")), a);
    assert_eq!(call.unsent(), ["check the build", "check the tests"]);
}

#[test]
fn a_failed_reply_follow_up_is_not_proof_the_text_was_sent() {
    let mut call = ready();
    let attempt = connected(&mut call);
    let first = typed(&mut call, "rerun the tests");
    let id = message_id(&first);
    call.reply(first, Err("timed out".into()), 1.0);
    let poll = refresh(&call.tick(5.0));
    let event = json!({"sequence": 9, "method": "voice/delegation/failed", "params": {"messageId": id, "targetId": "builder"}});
    call.reply(poll, Ok(state(json!({"phase": "connected", "attemptId": attempt, "target": {"id": "builder"}, "events": [event]}))), 5.0);
    assert_eq!(call.unsent(), ["rerun the tests"]);
    assert!(sent_words(&mut call).is_empty());
}

#[test]
fn two_texts_settled_in_one_refresh_are_both_reported_sent() {
    let mut call = ready();
    let attempt = connected(&mut call);
    let moved = only_job(&call.transfer("builder"));
    call.reply(moved, Ok(state(json!({"phase": "connected", "attemptId": attempt, "target": {"id": "builder"}}))), 0.5);
    let first = typed(&mut call, "check the build");
    let a = message_id(&first);
    call.reply(first, Err("timed out".into()), 1.0);
    let second = typed(&mut call, "check the tests");
    let b = message_id(&second);
    call.reply(second, Err("timed out".into()), 2.0);
    let poll = refresh(&call.tick(5.0));
    let events = json!([
        {"sequence": 8, "method": "voice/delegation/dispatched", "params": {"messageId": a}},
        {"sequence": 9, "method": "voice/delegation/held", "params": {"messageId": b}},
    ]);
    call.reply(poll, Ok(state(json!({"phase": "connected", "attemptId": attempt, "target": {"id": "builder"}, "events": events}))), 5.0);
    assert_eq!(sent_words(&mut call), ["check the build", "check the tests"]);
    assert_eq!(call.unsent(), Vec::<&str>::new());
    assert!(call.error.is_none());
}

#[test]
fn a_successful_retry_clears_the_unknown_outcome() {
    let mut call = ready();
    connected(&mut call);
    let first = typed(&mut call, "check the build");
    call.reply(first, Err("timed out".into()), 1.0);
    assert!(call.error.is_some());
    let again = typed(&mut call, "check the build");
    let id = message_id(&again);
    call.reply(again, Ok(state(json!({"phase": "connected", "textReceipt": format!("accepted:{id}")}))), 2.0);
    assert!(call.error.is_none());
    assert_eq!(sent_words(&mut call), ["check the build"]);
}

#[test]
fn a_text_reply_without_acceptance_keeps_the_words_unsettled() {
    let mut call = ready();
    connected(&mut call);
    let first = typed(&mut call, "check the build");
    let id = message_id(&first);
    call.reply(first, Ok(state(json!({"phase": "connected", "textReceipt": format!("unknown:{id}")}))), 1.0);
    assert_eq!(call.unsent(), ["check the build"]);
    assert!(sent_words(&mut call).is_empty());
    assert!(call.error.as_deref().unwrap().starts_with("Text outcome is unknown"));
}

#[test]
fn operator_activity_prefers_newer_words_over_an_earlier_headline() {
    let raw = json!({
        "updatedAt": "2026-11-24T12:00:00Z",
        "sessions": [{"id": "op", "name": "op", "surface": "codex", "status": "busy", "display": {"kind": "working", "label": "Working"},
            "turnCompletion": {"headline": "Shipped the cart fix", "at": "2026-11-24T10:00:00Z"}}],
        "transcripts": {"op": [{"id": "m1", "kind": "message", "role": "assistant", "text": "Asking the builder to rerun checkout. More soon.", "timestamp": "2026-11-24T11:59:00Z"}]}
    });
    let w = crate::derive::World::from_json(&raw.to_string()).unwrap();
    assert_eq!(w.voice_activity("op").unwrap().latest.as_deref(), Some("Asking the builder to rerun checkout"));
}

#[test]
fn operator_activity_shows_a_headline_newer_than_the_last_words() {
    let raw = json!({
        "updatedAt": "2026-11-24T12:00:00Z",
        "sessions": [{"id": "op", "name": "op", "surface": "codex", "display": {"kind": "idle", "label": "Idle"},
            "turnCompletion": {"headline": "Shipped the cart fix", "at": "2026-11-24T11:59:00Z"}}],
        "transcripts": {"op": [{"id": "m1", "kind": "message", "role": "assistant", "text": "Asking the builder to rerun checkout.", "timestamp": "2026-11-24T10:00:00Z"}]}
    });
    let w = crate::derive::World::from_json(&raw.to_string()).unwrap();
    assert_eq!(w.voice_activity("op").unwrap().latest.as_deref(), Some("Shipped the cart fix"));
}

#[test]
fn unsettled_words_retried_after_a_transfer_are_a_new_message() {
    let mut call = ready();
    let attempt = connected(&mut call);
    let first = typed(&mut call, "rerun the tests");
    let id = message_id(&first);
    call.reply(first, Err("timed out".into()), 1.0);
    let moved = only_job(&call.transfer("builder"));
    call.reply(moved, Ok(state(json!({"phase": "connected", "attemptId": attempt, "target": {"id": "builder"}}))), 2.0);
    assert_ne!(message_id(&typed(&mut call, "rerun the tests")), id);
}






#[test]
fn a_settled_text_reports_its_own_id_not_a_later_retry_on_another_route() {
    let mut call = ready();
    let attempt = connected(&mut call);
    let first = typed(&mut call, "rerun the tests");
    let a = message_id(&first);
    call.reply(first, Err("timed out".into()), 1.0);
    let stale = refresh(&call.tick(5.0));
    let moved = only_job(&call.transfer("builder"));
    call.reply(moved, Ok(state(json!({"phase": "connected", "attemptId": attempt, "target": {"id": "builder"}}))), 5.0);
    let retry = typed(&mut call, "rerun the tests");
    let b = message_id(&retry);
    assert_ne!(a, b, "the same words on another route are another message");
    call.reply(stale, Ok(state(json!({"phase": "connected", "attemptId": attempt, "textReceipt": format!("accepted:{a}")}))), 6.0);
    assert_eq!(call.take_sent(), [(a, "rerun the tests".to_string())]);
    assert_eq!(call.unsent(), ["rerun the tests"], "the retry is still waiting");
}

#[test]
fn a_refresh_sent_before_a_transfer_lands_does_not_bring_the_old_route_back() {
    let mut call = ready();
    let attempt = connected(&mut call);
    let first = typed(&mut call, "rerun the tests");
    let a = message_id(&first);
    call.reply(first, Err("timed out".into()), 1.0);
    let stale = refresh(&call.tick(5.0));
    let moved = only_job(&call.transfer("builder"));
    call.reply(moved, Ok(state(json!({"phase": "connected", "attemptId": attempt, "target": {"id": "builder"}}))), 5.0);
    call.reply(stale, Ok(state(json!({"phase": "connected", "attemptId": attempt}))), 6.0);
    assert_eq!(call.state.as_ref().and_then(|s| s.target.as_ref()).map(|t| t.id.as_str()), Some("builder"));
    assert_ne!(message_id(&typed(&mut call, "rerun the tests")), a);
    let fresh = refresh(&call.tick(10.0));
    call.reply(fresh, Ok(state(json!({"phase": "connected", "attemptId": attempt}))), 10.0);
    assert!(call.state.as_ref().is_some_and(|s| s.target.is_none()), "a later refresh is believed");
}

#[test]
fn a_call_is_woken_at_each_deadline_its_tick_acts_on_and_not_before() {
    let mut call = ready();
    let id = started(&mut call, 0.0);
    let answer = |call: &mut Call, effects: &[Effect], now: f64| {
        for job in jobs(effects) {
            call.reply(job, Ok(state(json!({"phase": "negotiating", "attemptId": id}))), now);
        }
    };
    let first = call.tick(0.0);
    answer(&mut call, &first, 0.0);
    let mut now = 0.0;
    let mut acted = 0;
    while let Some(due) = call.next_due() {
        if due > 60.0 {
            break;
        }
        let due = due.max(now);
        if due - 0.01 > now {
            assert!(call.tick(due - 0.01).is_empty(), "nothing is due before {due}");
        }
        let effects = call.tick(due);
        assert!(!effects.is_empty(), "the tick acts at {due}");
        acted += 1;
        answer(&mut call, &effects, due);
        now = due;
    }
    assert!(acted >= 5, "refreshes and heartbeats each came due: {acted}");
}

