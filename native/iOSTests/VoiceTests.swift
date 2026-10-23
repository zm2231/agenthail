import AVFAudio
import XCTest
@testable import Agenthail

final class VoiceTests: XCTestCase {
    @MainActor
    func testDismissingAndReopeningScreenPreservesCallAndCoordinatorIdentity() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        let coordinator = VoiceCallCoordinator(makeModel: { model })
        coordinator.open(targetID: "target-a")
        model.screenAppeared()
        await model.call()
        model.screenDisappeared()
        XCTAssertTrue(model.dialing)
        XCTAssertEqual(audio.ends, 0)
        coordinator.open(targetID: "other-session")
        XCTAssertTrue(coordinator.model === model)
        XCTAssertEqual(model.targetID, "target-a")
        XCTAssertEqual(audio.starts, 1)
        coordinator.disconnect()
        XCTAssertNil(coordinator.model)
        XCTAssertEqual(audio.ends, 1)
    }

    @MainActor
    func testHangupDuringPreparationLetsANewCallStartThatTheOldCallCannotClear() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        api.holdPrepare = true
        let oldCall = Task { await model.call() }
        for _ in 0..<1000 where api.pending == nil { await Task.yield() }
        XCTAssertNotNil(api.pending)
        model.hangup()
        XCTAssertFalse(model.working)
        XCTAssertTrue(model.canCall)
        api.holdPrepare = false
        audio.holdStart = true
        let newCall = Task { await model.call() }
        for _ in 0..<1000 where audio.pending == nil { await Task.yield() }
        XCTAssertNotNil(audio.pending)
        XCTAssertTrue(model.working)
        api.release()
        await oldCall.value
        XCTAssertTrue(model.working)
        audio.release()
        await newCall.value
        XCTAssertFalse(model.working)
        model.close()
    }

    @MainActor
    func testAnEndedCallStopsPollingOnceNoScreenShowsIt() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        model.screenAppeared()
        await model.call()
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "start" }) { await Task.yield() }
        let start = try XCTUnwrap(api.actions.first { $0.action == "start" })
        model.screenDisappeared()
        let polled = api.stateCalls
        api.snapshot = try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"ended","attemptId":"\#(start.attemptId ?? "")","events":[],"occupied":false,"truncated":false}"#.utf8))
        for _ in 0..<300 where model.state?.phase != "ended" { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertEqual(model.state?.phase, "ended")
        XCTAssertGreaterThan(api.stateCalls, polled)
        let ended = api.stateCalls
        try await Task.sleep(for: .milliseconds(1500))
        XCTAssertEqual(api.stateCalls, ended)
        model.screenAppeared()
        for _ in 0..<1000 where api.stateCalls == ended { await Task.yield() }
        XCTAssertGreaterThan(api.stateCalls, ended)
        model.close()
    }

    @MainActor
    func testAFailedPreparationIsRecordedWithItsStage() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let diagnostics = VoiceCallDiagnostics(file: nil)
        let model = VoiceOperatorModel(api: api, audio: audio, diagnostics: diagnostics); model.ready = true
        api.prepareError = NSError(domain: NSURLErrorDomain, code: NSURLErrorNotConnectedToInternet)
        await model.call()
        let attempt = try XCTUnwrap(diagnostics.attempts.last)
        XCTAssertEqual(attempt.failureStage, "operator_prepare")
        XCTAssertEqual(attempt.failureDomain, NSURLErrorDomain)
        XCTAssertEqual(attempt.failureCode, NSURLErrorNotConnectedToInternet)
        XCTAssertEqual(attempt.terminalCause, "operator_prepare_failed")
        XCTAssertEqual(audio.starts, 0)
        XCTAssertTrue(model.canCall)
        XCTAssertNil(model.callUnavailableReason)
        model.ready = false
        XCTAssertEqual(model.callUnavailableReason, "Call audio is not ready.")
        XCTAssertFalse(model.canCall)
        model.close()
    }

    @MainActor
    func testEachFailedCallStepIsRecordedWithItsStage() async throws {
        let failure = NSError(domain: NSURLErrorDomain, code: NSURLErrorTimedOut)
        for (stage, cause) in [("target_select", "target_select_failed"), ("native_audio_start", "native_audio_start_failed"), ("host_start", "host_start_rejected")] {
            let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
            let diagnostics = VoiceCallDiagnostics(file: nil)
            let model = VoiceOperatorModel(api: api, audio: audio, diagnostics: diagnostics); model.ready = true
            switch stage {
            case "target_select": api.selectError = failure
            case "native_audio_start": audio.startError = failure
            default: api.startError = failure
            }
            await model.call()
            if stage == "host_start" {
                audio.onMessage?("offer", "v=0 fixture")
                for _ in 0..<1000 where diagnostics.attempts.last?.terminalCause == nil { await Task.yield() }
            }
            let attempt = try XCTUnwrap(diagnostics.attempts.last)
            XCTAssertEqual(attempt.failureStage, stage)
            XCTAssertEqual(attempt.failureDomain, NSURLErrorDomain)
            XCTAssertEqual(attempt.failureCode, NSURLErrorTimedOut)
            XCTAssertEqual(attempt.terminalCause, cause)
            model.close()
        }
    }

    @MainActor
    func testAHangupOrCloseWhileTheCallIsStartingEndsItsRecord() async throws {
        for closing in [false, true] {
            let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
            let diagnostics = VoiceCallDiagnostics(file: nil)
            let model = VoiceOperatorModel(api: api, audio: audio, diagnostics: diagnostics); model.ready = true
            if closing { api.holdPrepare = true } else { api.holdSelect = true }
            let call = Task { await model.call() }
            for _ in 0..<1000 where api.pending == nil { await Task.yield() }
            XCTAssertNotNil(api.pending)
            if closing { model.close() } else { model.hangup() }
            api.release()
            await call.value
            let attempt = try XCTUnwrap(diagnostics.attempts.last)
            XCTAssertEqual(attempt.terminalCause, "local_hangup")
            XCTAssertNil(attempt.failureStage)
            XCTAssertEqual(audio.starts, 0)
            model.close()
        }
    }

    @MainActor
    func testAHangupWithNothingForTheHostToStopEndsPollingOnceNoScreenShowsIt() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        model.screenAppeared()
        audio.holdStart = true
        let call = Task { await model.call() }
        for _ in 0..<1000 where audio.pending == nil { await Task.yield() }
        model.screenDisappeared()
        let during = api.stateCalls
        try await Task.sleep(for: .milliseconds(1200))
        XCTAssertGreaterThan(api.stateCalls, during)
        model.hangup()
        audio.release()
        await call.value
        try await Task.sleep(for: .milliseconds(100))
        let ended = api.stateCalls
        try await Task.sleep(for: .milliseconds(1500))
        XCTAssertEqual(api.stateCalls, ended)
        XCTAssertFalse(api.actions.contains { $0.action == "stop" })
        model.close()
    }

    @MainActor
    func testFollowingTheLatestActivityMovesOnlyForNewWords() throws {
        let model = VoiceOperatorModel(api: VoiceFixtureAPI(), audio: VoiceFixtureAudio())
        func state(_ events: String) throws -> VoiceState {
            try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"connected","attemptId":"a","events":[\#(events)],"occupied":false,"truncated":false}"#.utf8))
        }
        let spoken = #"{"sequence":1,"method":"thread/realtime/transcript/done","params":{"role":"user","text":"Check the build."}}"#
        model.state = try state(spoken)
        let first = model.latestContentMarker
        model.state = try state(spoken + #",{"sequence":2,"method":"voice/input-state","params":{"muted":"true"}}"#)
        XCTAssertEqual(model.latestContentMarker, first)
        model.state = try state(spoken + #",{"sequence":3,"method":"thread/realtime/transcript/done","params":{"role":"assistant","text":"Checking it now."}}"#)
        XCTAssertNotEqual(model.latestContentMarker, first)
        model.close()
    }

    @MainActor
    func testAnActivityAttachmentLoadsOnlyForTheSessionStillShown() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [VoiceAttachmentURLProtocol.self]
        let timeline = AgenthailAPI(baseURL: URL(string: "https://attach.test")!, token: "token", session: URLSession(configuration: configuration))
        let model = VoiceOperatorModel(api: VoiceFixtureAPI(), timelineAPI: timeline, audio: VoiceFixtureAudio())
        let shown = try JSONDecoder().decode(SessionDetail.self, from: Data(SessionPreview.detailJSON.utf8))
        let other = try JSONDecoder().decode(SessionDetail.self, from: Data(SessionPreview.detailJSON.replacingOccurrences(of: #""id":"demo""#, with: #""id":"other""#).utf8))
        let item = try JSONDecoder().decode(TimelineItem.self, from: Data(#"{"id":"r","kind":"toolResult","title":"Result","text":"","truncated":false,"attachment":{"id":"image-1","mediaType":"image/png"}}"#.utf8))
        model.detail = shown
        let loaded = await model.activityAttachment(item)
        XCTAssertEqual(loaded, Data("image-bytes".utf8))
        XCTAssertEqual(VoiceAttachmentURLProtocol.gate.lastSession, "demo")

        VoiceAttachmentURLProtocol.gate.hold()
        let switched = Task { await model.activityAttachment(item) }
        for _ in 0..<500 where !VoiceAttachmentURLProtocol.gate.isWaiting { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(VoiceAttachmentURLProtocol.gate.isWaiting)
        model.detail = other
        VoiceAttachmentURLProtocol.gate.release()
        let result = await switched.value
        XCTAssertNil(result)
        model.close()
    }

    @MainActor
    func testHeartbeatUsesAcceptedAttemptAndStopsOnHangup() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        await model.call()
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "heartbeat" }) { await Task.yield() }
        let start = try XCTUnwrap(api.actions.first { $0.action == "start" })
        let heartbeat = try XCTUnwrap(api.actions.first { $0.action == "heartbeat" })
        XCTAssertEqual(heartbeat.attemptId, start.attemptId)
        model.hangup()
        let count = api.actions.filter { $0.action == "heartbeat" }.count
        for _ in 0..<10 { await Task.yield() }
        XCTAssertEqual(api.actions.filter { $0.action == "heartbeat" }.count, count)
        XCTAssertEqual(audio.ends, 1)
        model.close()
    }

    @MainActor
    func testSystemMuteAndEndUpdateTheSameCallModel() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        await model.call()
        audio.onMessage?("mute", "true")
        XCTAssertTrue(model.muted)
        audio.onMessage?("ended", "user_hangup")
        XCTAssertFalse(model.dialing)
        XCTAssertEqual(audio.ends, 1)
    }

    @MainActor
    func testMuteAndHoldAreReportedInOrderOnceTheHostHasTheCall() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        await model.call()
        model.toggleMute()
        XCTAssertFalse(api.actions.contains { $0.action == "input-state" })
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "input-state" }) { await Task.yield() }
        audio.onMessage?("held", "true")
        model.toggleMute()
        audio.onMessage?("held", "false")
        for _ in 0..<1000 where api.actions.filter({ $0.action == "input-state" }).count < 2 { await Task.yield() }
        let start = try XCTUnwrap(api.actions.first { $0.action == "start" })
        let reports = api.actions.filter { $0.action == "input-state" }
        XCTAssertEqual(reports.map(\.muted), [true, false])
        XCTAssertEqual(reports.map(\.inputRevision), [1, 2])
        XCTAssertTrue(reports.allSatisfy { $0.attemptId == start.attemptId })
        model.close()
    }

    @MainActor
    func testAMuteTheHostCannotRecordEndsTheCall() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        await model.call()
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "heartbeat" }) { await Task.yield() }
        api.inputStateError = AgenthailAPIError.request(409, "input state requires the current call")
        model.toggleMute()
        for _ in 0..<1000 where audio.ends == 0 { await Task.yield() }
        XCTAssertEqual(audio.ends, 1)
        XCTAssertEqual(model.error, "Your computer could not record whether the microphone is muted, so the call ended.")
    }

    @MainActor
    func testHostThatCannotConfirmTheCurrentCallEndsLocalAudio() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        await model.call()
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "start" }) { await Task.yield() }
        let attempt = try XCTUnwrap(api.actions.first { $0.action == "start" }?.attemptId)
        api.snapshot = try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"unknown","attemptId":"\#(attempt)","message":"The host restarted.","events":[],"occupied":false,"truncated":false}"#.utf8))
        await model.refresh()
        XCTAssertFalse(model.dialing)
        XCTAssertEqual(audio.ends, 1)
        XCTAssertEqual(model.error, "The host restarted.")
    }

    @MainActor
    func testPreOfferAudioFailurePreservesCauseAndDoesNotStopUnknownHostCall() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"; model.ready = true
        await model.call()
        audio.onMessage?("error", "Microphone route unavailable")
        await Task.yield()
        XCTAssertEqual(model.error, "Microphone route unavailable")
        XCTAssertFalse(api.actions.contains { $0.action == "stop" })
    }

    @MainActor
    func testCleanupFailureDoesNotOverwriteAudioFailure() async {
        let api = VoiceFixtureAPI(); api.stopError = AgenthailAPIError.request(409, "call identity does not match")
        let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"; model.ready = true
        await model.call()
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "start" }) { await Task.yield() }
        audio.onMessage?("error", "Codex voice data channel failed")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "stop" }) { await Task.yield() }
        XCTAssertEqual(model.error, "Codex voice data channel failed")
        XCTAssertFalse(model.hostHangupPending)
        XCTAssertTrue(model.hostHangupUnconfirmed)
    }

    @MainActor
    func testHangupEndsLocalAudioBeforeHostStopIsConfirmed() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"; model.ready = true
        await model.call()
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "start" }) { await Task.yield() }
        let attempt = try! XCTUnwrap(api.actions.first { $0.action == "start" }?.attemptId)
        audio.onMessage?("connection", "connected")
        audio.onMessage?("channel", "open")
        api.holdStop = true

        model.hangup()

        XCTAssertEqual(audio.ends, 1)
        XCTAssertFalse(model.audioConnected)
        XCTAssertFalse(model.dialing)
        XCTAssertTrue(model.hostHangupPending)
        XCTAssertFalse(model.hostHangupUnconfirmed)
        XCTAssertFalse(model.canCall)
        for _ in 0..<1000 where api.pending == nil { await Task.yield() }
        XCTAssertEqual(api.actions.last?.action, "stop")

        api.release()
        for _ in 0..<1000 where model.state?.phase != "ready" { await Task.yield() }
        XCTAssertTrue(model.hostHangupPending)

        api.snapshot = try! JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"ended","attemptId":"\#(attempt)","events":[{"sequence":1,"method":"thread/realtime/closed","params":{}}],"occupied":false,"truncated":false}"#.utf8))
        await model.refresh()
        XCTAssertFalse(model.hostHangupPending)
        XCTAssertFalse(model.hostHangupUnconfirmed)
        XCTAssertTrue(model.canCall)
    }

    @MainActor
    func testUnconfirmedHostHangupKeepsLocalAudioOffAndAllowsRetry() async {
        let api = VoiceFixtureAPI(); api.stopError = AgenthailAPIError.request(503, "host unavailable")
        let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"; model.ready = true
        await model.call()
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "start" }) { await Task.yield() }

        model.hangup()
        for _ in 0..<1000 where !model.hostHangupUnconfirmed { await Task.yield() }

        XCTAssertEqual(audio.ends, 1)
        XCTAssertFalse(model.audioConnected)
        XCTAssertFalse(model.hostHangupPending)
        XCTAssertTrue(model.hostHangupUnconfirmed)
        XCTAssertEqual(model.error, "Audio is off locally. Host hangup is unconfirmed: host unavailable")
        api.stopError = nil
        model.hangup()
        for _ in 0..<1000 where api.actions.filter({ $0.action == "stop" }).count < 2 { await Task.yield() }
        XCTAssertTrue(model.hostHangupPending)
        XCTAssertFalse(model.hostHangupUnconfirmed)
    }

    @MainActor
    func testUnavailableAudioAndDisconnectedHostBlockCalls() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"
        audio.onMessage?("ready", "")
        XCTAssertTrue(model.canCall)
        model.connectionError = "Host offline"
        await model.call()
        XCTAssertFalse(model.canCall)
        XCTAssertTrue(api.actions.isEmpty)
        model.connectionError = nil
        audio.onMessage?("unavailable", "Audio page failed")
        await model.call()
        XCTAssertFalse(model.ready)
        XCTAssertEqual(model.error, "Audio page failed")
        XCTAssertTrue(api.actions.isEmpty)
    }

    @MainActor
    func testOrchestratorCallDoesNotRequireASessionTarget() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        XCTAssertTrue(model.canCall)
        await model.call()
        XCTAssertEqual(api.actions.prefix(2).map(\.action), ["select", "prepare"])
        XCTAssertEqual(api.actions.first?.targetId, "")
        XCTAssertEqual(audio.starts, 1)
    }

    @MainActor
    func testConnectedCallCanTransferOrReturnThroughTheSharedAction() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        api.snapshot = try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"connected","events":[],"occupied":false,"truncated":false}"#.utf8))
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "@builder"
        await model.refresh()
        await model.transfer()
        XCTAssertEqual(api.actions.last?.action, "transfer")
        XCTAssertEqual(api.actions.last?.targetId, "@builder")
        model.targetID = ""
        model.state = api.snapshot
        await model.transfer()
        XCTAssertEqual(api.actions.last?.targetId, "")
    }

    @MainActor
    func testConnectedSelectedCallCanRequestTargetInterrupt() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        api.snapshot = try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"connected","attemptId":"call-a","events":[],"occupied":false,"truncated":false,"target":{"id":"target-a","surface":"codex","name":"Build session","status":"busy","lastActive":"2026-10-03T00:00:00Z"}}"#.utf8))
        let model = VoiceOperatorModel(api: api, audio: audio)
        await model.refresh()
        await model.interruptTarget()
        XCTAssertEqual(api.actions.last?.action, "target-interrupt")
        XCTAssertEqual(api.actions.last?.attemptId, "call-a")
    }

    @MainActor
    func testClosedScreenCannotSendTextOrInterrupt() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"; model.ready = true
        await model.call()
        model.close()
        model.text = "Do not deliver after close"
        await model.sendText()
        await model.interrupt()
        XCTAssertFalse(api.actions.contains { ["text", "interrupt"].contains($0.action) })
    }

    @MainActor
    func testNewConversationUsesServerActionAndClearsLoadedTimeline() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        api.snapshot = try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"ready","session":{"id":"old","surface":"codex","name":"Old operator","status":"idle","lastActive":"now","cwd":"/operator","source":"agenthail","transport":"desktop"},"events":[{"sequence":1,"method":"thread/realtime/transcript/done","params":{"role":"assistant","text":"Old history"}}],"occupied":false,"truncated":false}"#.utf8))
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"
        await model.refresh()
        XCTAssertTrue(model.canStartNewConversation)
        await model.startNewConversation()
        XCTAssertEqual(api.actions.last?.action, "new")
        XCTAssertNil(model.detail)
    }

    @MainActor
    func testLateTextResponseCannotReplaceClosedScreenState() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"; model.ready = true
        await model.call()
        api.holdText = true
        model.text = "Check the existing task"
        let send = Task { await model.sendText() }
        for _ in 0..<1000 where api.pending == nil { await Task.yield() }
        XCTAssertNotNil(api.pending)
        model.close()
        let connected = try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"connected","events":[],"occupied":false,"truncated":false}"#.utf8))
        api.release(connected)
        await send.value
        XCTAssertNil(model.state)
        XCTAssertFalse(model.audioConnected)
    }

    @MainActor
    func testPreviousCallEndingCannotCancelPendingMicrophonePermission() async throws {
        let api = VoiceFixtureAPI()
        api.snapshot = try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"ended","attemptId":"old-call","events":[],"occupied":false,"truncated":false}"#.utf8))
        let audio = VoiceFixtureAudio(); audio.holdStart = true
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"; model.ready = true
        let call = Task { await model.call() }
        for _ in 0..<1000 where audio.pending == nil { await Task.yield() }
        XCTAssertNotNil(audio.pending)
        await model.refresh()
        XCTAssertTrue(model.dialing)
        XCTAssertEqual(audio.ends, 0)
        audio.release()
        await call.value
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "start" }) { await Task.yield() }
        let start = try XCTUnwrap(api.actions.first { $0.action == "start" })
        XCTAssertNotEqual(start.attemptId, "old-call")
        model.close()
    }

    @MainActor
    func testCurrentCallEndingIsVisibleInsteadOfSilentlyReturningToReady() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"; model.ready = true
        await model.call()
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "start" }) { await Task.yield() }
        let attempt = try XCTUnwrap(api.actions.first { $0.action == "start" }?.attemptId)
        api.snapshot = try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"ended","attemptId":"\#(attempt)","events":[],"occupied":false,"truncated":false}"#.utf8))
        await model.refresh()
        XCTAssertFalse(model.dialing)
        XCTAssertEqual(audio.ends, 1)
        await model.refresh()
        XCTAssertEqual(audio.ends, 1)
        XCTAssertEqual(model.error, "The host ended this voice call before audio connected. Check Voice details before trying again.")
        XCTAssertFalse(api.actions.contains { $0.action == "stop" })
    }

    @MainActor
    func testOldEndedSnapshotCannotEndNewConnectedCall() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"; model.ready = true
        await model.call()
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "start" }) { await Task.yield() }
        let attempt = try XCTUnwrap(api.actions.first { $0.action == "start" }?.attemptId)
        audio.onMessage?("connection", "connected")
        audio.onMessage?("channel", "open")
        XCTAssertTrue(model.audioConnected)
        XCTAssertFalse(model.canCall)
        api.snapshot = try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"ended","attemptId":"old-call","events":[],"occupied":false,"truncated":false}"#.utf8))
        await model.refresh()
        XCTAssertTrue(model.audioConnected)
        XCTAssertEqual(audio.ends, 0)
        XCTAssertNotEqual(model.state?.phase, "ended")
        XCTAssertNil(model.error)
        XCTAssertFalse(model.canCall)
        api.snapshot = try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"ended","attemptId":"\#(attempt)","events":[],"occupied":false,"truncated":false}"#.utf8))
        await model.refresh()
        XCTAssertFalse(model.audioConnected)
        XCTAssertEqual(model.error, "The host ended this connected voice call. Check Voice details before trying again.")
    }

    @MainActor
    func testSystemAudioSuspensionAndHoldDoNotHangUpCall() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"; model.ready = true
        await model.call()
        audio.onMessage?("suspended", "true")
        audio.onMessage?("held", "true")
        audio.onMessage?("held", "false")
        audio.onMessage?("suspended", "false")
        XCTAssertTrue(model.dialing)
        XCTAssertEqual(audio.ends, 0)
        XCTAssertNil(model.error)
    }

    @MainActor
    func testClosingDuringPreparationCannotRestartMicrophone() async throws {
        let api = VoiceFixtureAPI(); api.holdPrepare = true
        let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"; model.ready = true
        let call = Task { await model.call() }
        for _ in 0..<1000 where api.pending == nil { await Task.yield() }
        XCTAssertNotNil(api.pending)
        model.close()
        api.release()
        await call.value
        XCTAssertEqual(audio.starts, 0)
        XCTAssertFalse(model.audioConnected)
        XCTAssertNil(model.state)
    }

    @MainActor
    func testLateAcceptedOfferIsStoppedAndCannotReviveClosedScreen() async throws {
        let api = VoiceFixtureAPI(); api.holdStart = true
        let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.targetID = "target-a"; model.ready = true
        await model.call()
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where api.pending == nil { await Task.yield() }
        XCTAssertNotNil(api.pending)
        model.close()
        api.release()
        for _ in 0..<1000 where api.actions.filter({ $0.action == "stop" }).count < 2 { await Task.yield() }
        audio.onMessage?("connection", "connected")
        let starts = api.actions.filter { $0.action == "start" }
        let stops = api.actions.filter { $0.action == "stop" }
        XCTAssertEqual(starts.count, 1)
        XCTAssertEqual(stops.count, 2)
        XCTAssertTrue(stops.allSatisfy { $0.attemptId == starts.first?.attemptId })
        XCTAssertFalse(model.audioConnected)
        XCTAssertFalse(model.dialing)
    }
    @MainActor
    func testNativeVoiceRejectsInsecureOrCredentialBearingOrigins() {
        for address in ["http://mac.test", "http://192.168.1.20:7412", "https://user:secret@mac.test", "https://mac.test?token=secret", "https://mac.test/path"] {
            XCTAssertThrowsError(try VoiceAPI(endpoint: URL(string: address)!, token: "fixture"))
        }
        for address in ["https://mac.test", "http://127.0.0.1:7412"] {
            XCTAssertNoThrow(try VoiceAPI(endpoint: URL(string: address)!, token: "fixture"))
        }
    }

    @MainActor
    func testNativeVoiceSendsIdentityAndSDPOnlyThroughAuthenticatedAPI() async throws {
        VoiceURLProtocol.recorder.reset()
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [VoiceURLProtocol.self]
        let api = try VoiceAPI(endpoint: URL(string: "https://mac.test")!, token: "paired-fixture", session: URLSession(configuration: configuration))
        let state = try await api.action(VoiceAction(action: "start", attemptId: "attempt", sdp: "v=0 fixture"))
        XCTAssertEqual(state.phase, "negotiating")
        let request = VoiceURLProtocol.recorder.requests.first!
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer paired-fixture")
        XCTAssertEqual(request.url?.path, "/api/v1/voice")
        XCTAssertEqual(request.httpMethod, "POST")
        let mediaRequest = api.request(path: "api/v1/voice/peer")
        XCTAssertEqual(mediaRequest.url?.query, nil)
        XCTAssertEqual(mediaRequest.value(forHTTPHeaderField: "Authorization"), "Bearer paired-fixture")
    }

    func testDeliveredVoiceTranscriptsKeepRolesAndReplacePartialText() throws {
        let state = try JSONDecoder().decode(VoiceState.self, from: Data(VoiceURLProtocol.state.utf8))
        XCTAssertEqual(state.transcripts.map(\.id), ["3"])
        XCTAssertEqual(state.transcripts.first?.role, "user")
        XCTAssertEqual(state.transcripts.first?.text, "Ask the builder to check the tests.")
        XCTAssertTrue(state.hasCall)
        XCTAssertFalse(state.occupied)
    }

    func testVoiceCloseReasonRemainsAvailableForConnectionDetails() throws {
        let state = try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"ended","attemptId":"call-a","events":[{"sequence":91,"method":"thread/realtime/closed","params":{"reason":"requested"}}],"occupied":false,"truncated":false}"#.utf8))
        XCTAssertEqual(state.attemptId, "call-a")
        XCTAssertEqual(state.events.last?.sequence, 91)
        XCTAssertEqual(state.events.last?.params.reason, "requested")
    }
}

@MainActor
private final class VoiceFixtureAPI: VoiceServiceClient {
    var holdPrepare = false
    var holdStart = false
    var holdStop = false
    var holdText = false
    var pending: CheckedContinuation<VoiceState, Never>?
    var actions: [VoiceAction] = []
    var snapshot: VoiceState?
    var stopError: Error?
    var inputStateError: Error?
    var prepareError: Error?
    var selectError: Error?
    var startError: Error?
    var holdSelect = false
    private var ready: VoiceState { try! JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"ready","events":[],"occupied":false,"truncated":false}"#.utf8)) }
    func request(path: String) -> URLRequest { URLRequest(url: URL(string: "https://mac.test/\(path)")!) }
    var stateCalls = 0
    func state() async throws -> VoiceState { stateCalls += 1; return snapshot ?? ready }
    func action(_ action: VoiceAction) async throws -> VoiceState {
        actions.append(action)
        if action.action == "stop", let stopError { throw stopError }
        if action.action == "input-state", let inputStateError { throw inputStateError }
        if action.action == "prepare", let prepareError { throw prepareError }
        if action.action == "select", let selectError { throw selectError }
        if action.action == "start", let startError { throw startError }
        if action.action == "select" && holdSelect { return await withCheckedContinuation { pending = $0 } }
        if (action.action == "prepare" && holdPrepare) || (action.action == "start" && holdStart) || (action.action == "stop" && holdStop) || (action.action == "text" && holdText) {
            return await withCheckedContinuation { pending = $0 }
        }
        return ready
    }
    func release(_ value: VoiceState? = nil) { let continuation = pending; pending = nil; continuation?.resume(returning: value ?? ready) }
}

@MainActor
private final class VoiceFixtureAudio: VoiceAudioClient {
    var onMessage: ((String, String) -> Void)?
    var starts = 0
    var ends = 0
    var holdStart = false
    var startError: Error?
    var pending: CheckedContinuation<Void, Never>?
    func load(_ request: URLRequest) {}
    func start() async throws {
        starts += 1
        if let startError { throw startError }
        if holdStart { await withCheckedContinuation { pending = $0 } }
    }
    func release() { let continuation = pending; pending = nil; continuation?.resume() }
    func end() { ends += 1 }
    func answer(_ sdp: String) async throws {}
    func mute(_ muted: Bool) {}
    func close() {}
}

private final class VoiceRequestRecorder: @unchecked Sendable {
    private let lock = NSLock()
    private var stored: [URLRequest] = []
    func reset() { lock.lock(); defer { lock.unlock() }; stored = [] }
    func append(_ request: URLRequest) { lock.lock(); defer { lock.unlock() }; stored.append(request) }
    var requests: [URLRequest] { lock.lock(); defer { lock.unlock() }; return stored }
}

private final class VoiceURLProtocol: URLProtocol, @unchecked Sendable {
    static let recorder = VoiceRequestRecorder()
    static let state = #"{"protocol":1,"phase":"negotiating","attemptId":"attempt","sdp":"v=0 answer","events":[{"sequence":3,"method":"thread/realtime/transcript/delta","params":{"role":"user","delta":"Ask the builder"}},{"sequence":4,"method":"thread/realtime/transcript/done","params":{"role":"user","text":"Ask the builder to check the tests."}}],"truncated":false,"occupied":false}"#
    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "mac.test" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.recorder.append(request)
        let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type":"application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(Self.state.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

private final class VoiceAttachmentGate: @unchecked Sendable {
    private let lock = NSLock()
    private var held: DispatchSemaphore?
    private var waiting = false
    private var session: String?
    var isWaiting: Bool { lock.withLock { waiting } }
    var lastSession: String? { lock.withLock { session } }
    func hold() { lock.withLock { held = DispatchSemaphore(value: 0) } }
    func release() { lock.withLock { held }?.signal() }
    func pass(session id: String?) {
        let semaphore = lock.withLock { () -> DispatchSemaphore? in
            session = id
            waiting = held != nil
            return held
        }
        semaphore?.wait()
        lock.withLock { waiting = false; held = nil }
    }
}

private final class VoiceAttachmentURLProtocol: URLProtocol, @unchecked Sendable {
    static let gate = VoiceAttachmentGate()
    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "attach.test" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let session = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "sessionId" }?.value
        Self.gate.pass(session: session)
        let body = Data("image-bytes".utf8)
        let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "image/png", "Content-Length": "\(body.count)"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: body)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
