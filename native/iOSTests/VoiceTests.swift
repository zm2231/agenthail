import XCTest
@testable import Agenthail

final class VoiceTests: XCTestCase {
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
}

@MainActor
private final class VoiceFixtureAPI: VoiceServiceClient {
    var holdPrepare = false
    var holdStart = false
    var pending: CheckedContinuation<VoiceState, Never>?
    var actions: [VoiceAction] = []
    private var ready: VoiceState { try! JSONDecoder().decode(VoiceState.self, from: Data(#"{"protocol":1,"phase":"ready","events":[],"occupied":false,"truncated":false}"#.utf8)) }
    func request(path: String) -> URLRequest { URLRequest(url: URL(string: "https://mac.test/\(path)")!) }
    func state() async throws -> VoiceState { ready }
    func action(_ action: VoiceAction) async throws -> VoiceState {
        actions.append(action)
        if (action.action == "prepare" && holdPrepare) || (action.action == "start" && holdStart) {
            return await withCheckedContinuation { pending = $0 }
        }
        return ready
    }
    func release() { let continuation = pending; pending = nil; continuation?.resume(returning: ready) }
}

@MainActor
private final class VoiceFixtureAudio: VoiceAudioClient {
    var onMessage: ((String, String) -> Void)?
    var starts = 0
    func load(_ request: URLRequest) {}
    func start() async throws { starts += 1 }
    func end() {}
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
