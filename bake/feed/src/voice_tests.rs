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
    assert_eq!(call.mic(&id, true), vec![Effect::PeerStart]);
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
    assert_eq!(call.mic(&id, false), vec![Effect::PeerEnd]);
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
fn a_start_that_answers_after_hangup_is_stopped() {
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
    assert_eq!(refused, "end the current audio call before selecting a different voice target");
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
    assert_eq!(call.mic(&new, true), vec![Effect::PeerStart]);
}

#[test]
fn a_start_bound_to_another_target_is_hung_up_even_when_the_select_reply_agreed() {
    let mut call = Call::new("s-builder");
    call.peer("ready", "", 0.0);
    let first = only_job(&call.tick(0.0));
    call.reply(first, Ok(state(json!({"phase": "ready"}))), 0.0);
    let select = only_job(&call.call());
    let prepare = only_job(&call.reply(select, Ok(state(json!({"phase": "ready", "target": {"id": "s-reviewer", "name": "Reviewer"}}))), 0.0));
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
    let after = call.reply(start, Err("end the current audio call before starting another".into()), 1.0);
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
