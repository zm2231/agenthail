import AVFAudio
import XCTest
@testable import Agenthail

final class VoiceTests: XCTestCase {
    @MainActor
    func testPreOfferAudioFailurePreservesCauseAndDoesNotStopUnknownHostCall() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
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
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        await model.call()
        audio.onMessage?("offer", "v=0 fixture")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "start" }) { await Task.yield() }
        audio.onMessage?("error", "Codex voice data channel failed")
        for _ in 0..<1000 where !api.actions.contains(where: { $0.action == "stop" }) { await Task.yield() }
        XCTAssertEqual(model.error, "Codex voice data channel failed")
    }

    @MainActor
    func testUnavailableAudioAndDisconnectedHostBlockCalls() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio)
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
    func testClosedScreenCannotSendTextOrInterrupt() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        await model.call()
        model.close()
        model.text = "Do not deliver after close"
        await model.sendText()
        await model.interrupt()
        XCTAssertFalse(api.actions.contains { ["text", "interrupt"].contains($0.action) })
    }

    @MainActor
    func testLateTextResponseCannotReplaceClosedScreenState() async throws {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
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
        XCTAssertEqual(model.state?.phase, "ready")
        XCTAssertFalse(model.audioConnected)
    }

    @MainActor
    func testPreviousCallEndingCannotCancelPendingMicrophonePermission() async throws {
        let api = VoiceFixtureAPI()
        api.snapshot = try JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"ended","attemptId":"old-call","events":[],"occupied":false,"truncated":false}"#.utf8))
        let audio = VoiceFixtureAudio(); audio.holdStart = true
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
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
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
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
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
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
    func testOnlyBegunAudioInterruptionEndsActiveCallWithVisibleReason() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        await model.call()
        model.audioInterrupted(Notification(name: AVAudioSession.interruptionNotification,
                                            userInfo: [AVAudioSessionInterruptionTypeKey: AVAudioSession.InterruptionType.ended.rawValue]))
        XCTAssertTrue(model.dialing)
        XCTAssertEqual(audio.ends, 0)
        model.audioInterrupted(Notification(name: AVAudioSession.interruptionNotification,
                                            userInfo: [AVAudioSessionInterruptionTypeKey: AVAudioSession.InterruptionType.began.rawValue]))
        XCTAssertFalse(model.dialing)
        XCTAssertEqual(audio.ends, 1)
        XCTAssertEqual(model.error, "iOS interrupted the microphone. The call was ended; call again after the interruption clears.")
    }

    @MainActor
    func testAudioInterruptionIncludesSystemReasonWhenProvided() async {
        let api = VoiceFixtureAPI(); let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
        await model.call()
        model.audioInterrupted(Notification(name: AVAudioSession.interruptionNotification,
                                            userInfo: [AVAudioSessionInterruptionTypeKey: AVAudioSession.InterruptionType.began.rawValue,
                                                       AVAudioSessionInterruptionReasonKey: AVAudioSession.InterruptionReason.default.rawValue]))
        XCTAssertEqual(model.error, "iOS interrupted the microphone (iOS reason code \(AVAudioSession.InterruptionReason.default.rawValue)). The call was ended; call again after the interruption clears.")
    }

    @MainActor
    func testClosingDuringPreparationCannotRestartMicrophone() async throws {
        let api = VoiceFixtureAPI(); api.holdPrepare = true
        let audio = VoiceFixtureAudio()
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
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
        let model = VoiceOperatorModel(api: api, audio: audio); model.ready = true
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
        for address in ["http://mac.test", "https://user:secret@mac.test", "https://mac.test?token=secret", "https://mac.test/path"] {
            XCTAssertThrowsError(try VoiceAPI(endpoint: URL(string: address)!, token: "fixture"))
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
    var holdText = false
    var pending: CheckedContinuation<VoiceState, Never>?
    var actions: [VoiceAction] = []
    var snapshot: VoiceState?
    var stopError: Error?
    private var ready: VoiceState { try! JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"ready","events":[],"occupied":false,"truncated":false}"#.utf8)) }
    func request(path: String) -> URLRequest { URLRequest(url: URL(string: "https://mac.test/\(path)")!) }
    func state() async throws -> VoiceState { snapshot ?? ready }
    func action(_ action: VoiceAction) async throws -> VoiceState {
        actions.append(action)
        if action.action == "stop", let stopError { throw stopError }
        if (action.action == "prepare" && holdPrepare) || (action.action == "start" && holdStart) || (action.action == "text" && holdText) {
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
    var pending: CheckedContinuation<Void, Never>?
    func load(_ request: URLRequest) {}
    func start() async throws {
        starts += 1
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
