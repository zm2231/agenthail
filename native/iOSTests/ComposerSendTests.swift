import Foundation
import XCTest
@testable import Agenthail

@MainActor
final class ComposerSendTests: XCTestCase {
    private let queuedReceipt = #"{"ok":true,"result":{"evidence":"queued","status":"queued","queueId":7,"detail":"Queued for fixture."}}"#
    private let sentReceipt = #"{"ok":true,"result":{"evidence":"transport_accepted","status":"sent"}}"#

    override func setUp() async throws {
        ComposerSendProtocol.state.reset()
    }

    func testASentMessageClearsTheDraftAtOnceAndStaysClear() async throws {
        ComposerSendProtocol.state.script([.answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        model.composer = "Sent"
        model.send(to: session)
        XCTAssertEqual(model.composer, "")
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(model.composer, "")
        XCTAssertEqual(model.deliveryStatus[session.id], "Sent")
        XCTAssertNil(model.failedSends[session.id])
    }

    func testAQueuedMessageClearsTheDraftAndSaysItIsQueued() async throws {
        ComposerSendProtocol.state.script([.answer(200, queuedReceipt)])
        let (model, session) = try makeModel()
        model.composer = "Queued"
        model.send(to: session)
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(model.composer, "")
        XCTAssertEqual(model.deliveryStatus[session.id], "Queued for fixture.")
        XCTAssertNil(model.failedSends[session.id])
    }

    func testARefusedMessageReturnsWithAReasonAndRetryUsesANewKey() async throws {
        ComposerSendProtocol.state.script([.answer(409, #"{"error":{"message":"Session ended"}}"#), .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        model.composer = "Refused"
        model.send(to: session)
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(model.composer, "Refused")
        XCTAssertNotNil(model.failedSends[session.id])
        XCTAssertNil(model.operationError, "the reason stays on the composer of the session on screen")

        model.send(to: session)
        XCTAssertNil(model.failedSends[session.id])
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 2 }
        let attempts = ComposerSendProtocol.state.attempts
        XCTAssertNotEqual(attempts[0].key, attempts[1].key)
        XCTAssertEqual(model.composer, "")
        XCTAssertEqual(model.deliveryStatus[session.id], "Sent")
    }

    func testARefusalForASessionOffScreenRaisesAnAlert() async throws {
        ComposerSendProtocol.state.script([.answer(409, #"{"error":{"message":"Session ended"}}"#)])
        let (model, session) = try makeModel()
        model.composer = "Refused"
        model.send(to: session)
        model.selectedSessionID = "computer::other"
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertNotNil(model.operationError)
        XCTAssertNotNil(model.failedSends[session.id])
    }

    func testALostResponseKeepsTheDraftClearAndRetriesWithTheSameKey() async throws {
        ComposerSendProtocol.state.script([.lostResponse, .answer(200, queuedReceipt)])
        let (model, session) = try makeModel()
        model.composer = "Arrived, answer lost"
        model.send(to: session)
        try await waitUntil { model.pendingSends[session.id]?.first?.unconfirmed == true }
        XCTAssertEqual(model.composer, "", "the message may have arrived, so it does not come back")
        XCTAssertEqual(model.deliveryStatus[session.id], "Not confirmed yet · retrying")
        XCTAssertNil(model.failedSends[session.id])
        try await waitUntil(seconds: 5) { model.pendingSends.isEmpty }
        let attempts = ComposerSendProtocol.state.attempts
        XCTAssertEqual(attempts.count, 2)
        XCTAssertEqual(attempts[0].key, attempts[1].key)
        XCTAssertEqual(attempts[0].body["message"] as? String, attempts[1].body["message"] as? String)
        XCTAssertEqual(model.composer, "")
        XCTAssertEqual(model.deliveryStatus[session.id], "Queued for fixture.")
    }

    func testOfflineMessagesWaitInOrderWhileTheComposerStaysUsable() async throws {
        ComposerSendProtocol.state.script([.offline, .answer(200, sentReceipt), .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        model.composer = "First"
        model.send(to: session)
        try await waitUntil { model.pendingSends[session.id]?.first?.unconfirmed == true }
        model.composer = "Second"
        model.send(to: session)
        XCTAssertEqual(model.composer, "")
        XCTAssertEqual(model.pendingSends[session.id]?.map(\.message), ["First", "Second"])
        try await waitUntil(seconds: 5) { model.pendingSends.isEmpty }
        XCTAssertEqual(ComposerSendProtocol.state.attempts.map { $0.body["message"] as? String }, ["First", "First", "Second"])
    }

    func testAWaitingMessageCanBePutBack() async throws {
        ComposerSendProtocol.state.script([.offline, .offline, .offline, .offline])
        let (model, session) = try makeModel()
        model.composer = "Take it back"
        model.send(to: session)
        try await waitUntil { model.pendingSends[session.id]?.first?.unconfirmed == true }
        model.composer = "newer"
        model.putBack(try XCTUnwrap(model.pendingSends[session.id]?.first))
        XCTAssertEqual(model.composer, "Take it back\n\nnewer")
        XCTAssertTrue(model.pendingSends.isEmpty)
        XCTAssertEqual(model.deliveryStatus[session.id], "Put back. It may already have arrived.")
    }

    func testASendThatCannotStartSaysSoAndKeepsTheDraft() throws {
        let (model, session) = try makeModel()
        model.selectedDetail = nil
        model.composer = "Keep me"
        model.send(to: session)
        XCTAssertEqual(model.composer, "Keep me")
        XCTAssertNotNil(model.failedSends[session.id])
        XCTAssertTrue(model.pendingSends.isEmpty)
    }

    func testAPendingMessageResumesAfterTheAppIsKilledWithItsKey() async throws {
        let store = temporaryStore()
        ComposerSendProtocol.state.script([.offline, .offline, .offline, .offline])
        var first: AgenthailIOSModel? = try makeModel(store: store).model
        let session = try makeSession()
        first?.composer = "Survives"
        first?.send(to: session)
        try await waitUntil { ComposerSendProtocol.state.attempts.count >= 1 }
        let key = ComposerSendProtocol.state.attempts[0].key
        let raw = ComposerSendProtocol.state.attempts[0].raw
        first = nil
        XCTAssertEqual(store.load().saved.sends.map(\.message), ["Survives"])
        XCTAssertEqual(store.load().saved.drafts, [:], "a message that left the composer is not also a draft")

        ComposerSendProtocol.state.reset()
        ComposerSendProtocol.state.script([.answer(200, sentReceipt)])
        let second = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        second.selectedSessionID = session.id
        try await waitUntil(seconds: 5) { second.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(ComposerSendProtocol.state.attempts[0].key, key)
        XCTAssertEqual(ComposerSendProtocol.state.attempts[0].raw, raw, "the resumed message is the same request, byte for byte")
        XCTAssertEqual(store.load().saved.sends, [])
    }

    func testADraftSurvivesTheAppBeingKilled() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        model.composer = "Typed, not sent"
        model.saveComposerState()
        XCTAssertEqual(store.load().saved.drafts[session.id], "Typed, not sent")

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        await reopened.loadSession(session.id)
        XCTAssertEqual(reopened.composer, "Typed, not sent")
    }

    func testTypingReachesTheStoreWithoutAnExplicitSave() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        model.composer = "Typed"
        try await waitUntil(seconds: 3) { store.load().saved.drafts[session.id] == "Typed" }
    }

    func testAKilledSendWhoseAttachmentNeverUploadedComesBackAsADraft() async throws {
        let store = temporaryStore()
        store.save(.init(sends: [.init(id: UUID(), sessionID: "computer::codex-flow", message: "With a file", turnSettings: TurnSettings(), idempotencyKey: "k", uploaded: nil, wire: nil, hadAttachments: true)]))
        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertTrue(model.pendingSends.isEmpty)
        XCTAssertNotNil(model.failedSends["computer::codex-flow"])
        await model.loadSession("computer::codex-flow")
        XCTAssertEqual(model.composer, "With a file")
    }

    func testARekeyWhileWaitingRetriesTheSameRequest() async throws {
        ComposerSendProtocol.state.script([.lostResponse, .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        model.composer = "Rekeyed while waiting"
        model.send(to: session)
        try await waitUntil { model.pendingSends[session.id]?.first?.unconfirmed == true }
        model.host(try XCTUnwrap(model.connection("computer")), rekeyed: ["codex-flow": "codex-moved"])
        XCTAssertNotNil(model.pendingSends["computer::codex-moved"])
        try await waitUntil(seconds: 5) { model.pendingSends.isEmpty }
        let attempts = ComposerSendProtocol.state.attempts
        XCTAssertEqual(attempts.count, 2)
        XCTAssertEqual(attempts[0].key, attempts[1].key)
        XCTAssertEqual(attempts[0].raw, attempts[1].raw, "a changed body under the same key is refused as a mismatch")
    }

    func testChangingTheTerminalWhileWaitingRetriesTheFirstRequest() async throws {
        ComposerSendProtocol.state.script([.lostResponse, .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        let route = "codex-flow"
        defer { HolderPins.shared.pick(nil, for: route) }
        HolderPins.shared.pick(HolderPin(SessionHolder(pid: 11, status: "idle")), for: route)
        model.composer = "To the first terminal"
        model.send(to: session)
        try await waitUntil { model.pendingSends[session.id]?.first?.unconfirmed == true }
        HolderPins.shared.pick(HolderPin(SessionHolder(pid: 22, status: "idle")), for: route)
        try await waitUntil(seconds: 5) { model.pendingSends.isEmpty }
        let attempts = ComposerSendProtocol.state.attempts
        XCTAssertEqual(attempts.count, 2)
        XCTAssertEqual(attempts[0].body["pid"] as? Int, 11)
        XCTAssertEqual(attempts[1].body["pid"] as? Int, 11, "a retry never moves to another terminal: the first may already have it")
        XCTAssertEqual(attempts[0].key, attempts[1].key)
    }

    func testARetryAfterARefusalPicksUpTheNewTerminal() async throws {
        ComposerSendProtocol.state.script([.answer(409, #"{"error":{"message":"That terminal is gone"}}"#), .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        let route = "codex-flow"
        defer { HolderPins.shared.pick(nil, for: route) }
        HolderPins.shared.pick(HolderPin(SessionHolder(pid: 11, status: "idle")), for: route)
        model.composer = "Refused"
        model.send(to: session)
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        HolderPins.shared.pick(HolderPin(SessionHolder(pid: 22, status: "idle")), for: route)
        model.send(to: session)
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 2 }
        XCTAssertEqual(ComposerSendProtocol.state.attempts[1].body["pid"] as? Int, 22)
    }

    func testAFileThatCannotBeReadIsKeptAndAnnouncedOnce() throws {
        let store = temporaryStore()
        let directory = store.file.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let unreadable = Data("not the format".utf8)
        try unreadable.write(to: store.file)

        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNotNil(model.operationError)
        let names = try FileManager.default.contentsOfDirectory(atPath: directory.path)
        let aside = try XCTUnwrap(names.first { $0.hasPrefix("composer.json.unreadable-") })
        XCTAssertFalse(names.contains("composer.json"))

        model.selectedSessionID = "computer::codex-flow"
        model.composer = "Typed after"
        model.saveComposerState()
        XCTAssertEqual(store.load().saved.drafts["computer::codex-flow"], "Typed after")
        XCTAssertEqual(try Data(contentsOf: directory.appendingPathComponent(aside)), unreadable, "a later save does not touch the kept file")

        let next = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNil(next.operationError, "the notice appears once")
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path).filter { $0.hasPrefix("composer.json.unreadable-") }, [aside])
    }

    func testAFileThatCannotBeReadYetIsLeftAloneAndRestoredLater() throws {
        let store = temporaryStore()
        let first = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        first.selectedSessionID = "computer::codex-flow"
        first.composer = "Typed before the lock"
        first.saveComposerState()
        let directory = store.file.deletingLastPathComponent()
        try FileManager.default.setAttributes([.posixPermissions: 0o000], ofItemAtPath: store.file.path)
        addTeardownBlock { try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: store.file.path) }
        try XCTSkipIf(FileManager.default.isReadableFile(atPath: store.file.path), "this runner can read any file")

        let locked = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNotNil(locked.operationError)
        locked.selectedSessionID = "computer::codex-flow"
        locked.composer = "Typed while locked"
        locked.saveComposerState()
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path), ["composer.json"], "a file that may be fine is not moved")

        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: store.file.path)
        XCTAssertEqual(store.load().saved.drafts["computer::codex-flow"], "Typed before the lock", "nothing overwrote it")
        let unlocked = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNil(unlocked.operationError)
    }

    func testAFileThatCannotBeMovedAsideIsNeverOverwritten() throws {
        let store = temporaryStore()
        let directory = store.file.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let unreadable = Data("not the format".utf8)
        try unreadable.write(to: store.file)
        try FileManager.default.setAttributes([.posixPermissions: 0o500], ofItemAtPath: directory.path)
        addTeardownBlock { try? FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path) }
        try XCTSkipIf(FileManager.default.isWritableFile(atPath: directory.path), "this runner can write any directory")

        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNotNil(model.operationError)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)
        model.selectedSessionID = "computer::codex-flow"
        model.composer = "Typed after"
        model.saveComposerState()
        XCTAssertEqual(try Data(contentsOf: store.file), unreadable)
    }

    func testAMissingFileIsNotAProblem() {
        let store = temporaryStore()
        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNil(model.operationError)
    }

    func testAttachmentsLostWithTheAppAreNamedOnReturn() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        model.composer = "With a file"
        model.composerAttachments = [ComposerAttachment(name: "plan.pdf", data: Data([1]), mediaType: "application/pdf")]
        model.saveComposerState()

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        await reopened.loadSession(session.id)
        XCTAssertEqual(reopened.composer, "With a file")
        XCTAssertEqual(reopened.composerAttachments, [])
        XCTAssertTrue(reopened.attachmentProblem?.contains("plan.pdf") == true, "\(String(describing: reopened.attachmentProblem))")
    }

    func testAStashedAttachmentLostWithTheAppIsNamedOnReturn() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        model.composer = "Stashed"
        model.composerAttachments = [ComposerAttachment(name: "stashed.png", data: Data([1]), mediaType: "image/png")]
        model.toggleStash()
        model.saveComposerState()

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertEqual(reopened.stashes[session.id]?.text, "Stashed")
        await reopened.loadSession(session.id)
        XCTAssertTrue(reopened.attachmentProblem?.contains("stashed.png") == true, "\(String(describing: reopened.attachmentProblem))")
    }

    func testAMessageFollowsItsSessionToANewID() async throws {
        let queue = SendQueue()
        var delivered: [String] = []
        var online = false
        queue.deliver = { send in
            delivered.append(send.sessionID)
            return online ? .answered : .unconfirmed
        }
        queue.add(PendingSend(sessionID: "old", message: "Moves", attachments: [], turnSettings: TurnSettings()))
        try await waitUntil { queue.pending["old"]?.first?.unconfirmed == true }
        var rekeys = SessionRekeys()
        rekeys.record(["old": "new"])
        online = true
        queue.carry(rekeys)
        XCTAssertNil(queue.pending["old"])
        XCTAssertEqual(queue.pending["new"]?.first?.sessionID, "new")
        queue.wake()
        try await waitUntil(seconds: 5) { queue.pending.isEmpty }
        XCTAssertEqual(delivered, ["old", "new"], "one worker keeps delivering, under the new id")
    }

    func testAReleasedQueueStopsRetrying() async throws {
        var attempts = 0
        var queue: SendQueue? = SendQueue()
        queue?.deliver = { _ in
            attempts += 1
            return .unconfirmed
        }
        queue?.add(PendingSend(sessionID: "a", message: "m", attachments: [], turnSettings: TurnSettings()))
        try await waitUntil { attempts == 1 }
        queue = nil
        try await Task.sleep(for: .milliseconds(2500))
        XCTAssertLessThanOrEqual(attempts, 1, "a queue nobody holds makes no further attempts")
    }

    func testAnAnswerIsDefiniteAndNoAnswerIsNot() {
        XCTAssertEqual(SendOutcome.of(URLError(.networkConnectionLost)), .unconfirmed)
        XCTAssertEqual(SendOutcome.of(URLError(.timedOut)), .unconfirmed)
        XCTAssertEqual(SendOutcome.of(URLError(.notConnectedToInternet)), .unconfirmed)
        XCTAssertEqual(SendOutcome.of(AgenthailAPIError.invalidResponse), .unconfirmed)
        XCTAssertEqual(SendOutcome.of(AgenthailAPIError.request(409, "ended")), .refused("ended"))
        XCTAssertEqual(SendOutcome.of(AgenthailAPIError.request(502, "down")), .refused("down"))
    }

    private func temporaryStore() -> ComposerStore {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
        return ComposerStore(file: directory.appendingPathComponent("composer.json"))
    }

    private func makeAPI() -> AgenthailAPI {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ComposerSendProtocol.self]
        return AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: URLSession(configuration: configuration))
    }

    private func makeDetail() throws -> SessionDetail {
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as? [String: Any])
        var session = try XCTUnwrap(object["session"] as? [String: Any])
        session["id"] = "codex-flow"
        session["surface"] = "codex"
        session["status"] = "idle"
        object["session"] = session
        var capabilities = try XCTUnwrap(object["capabilities"] as? [String: Any])
        capabilities["send"] = true
        object["capabilities"] = capabilities
        var detail = try JSONDecoder().decode(SessionDetail.self, from: JSONSerialization.data(withJSONObject: object))
        detail.session.id = "computer::codex-flow"
        return detail
    }

    private func makeSession() throws -> SessionState {
        let detail = try makeDetail()
        return SessionState(id: detail.session.id, surface: detail.session.surface, name: detail.session.name, alias: detail.alias,
                            status: detail.session.status, lastActive: nil, queueCount: 0, open: true, current: true,
                            currentReason: nil, capabilities: detail.capabilities, readOnly: false,
                            readOnlyReason: "", cwd: detail.session.cwd)
    }

    private func makeModel(store: ComposerStore? = nil) throws -> (model: AgenthailIOSModel, session: SessionState) {
        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        let session = try makeSession()
        model.selectedSessionID = session.id
        model.selectedDetail = try makeDetail()
        return (model, session)
    }

    private func waitUntil(seconds: Double = 2, _ condition: () -> Bool) async throws {
        let deadline = Date().addingTimeInterval(seconds)
        while !condition(), Date() < deadline { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(condition(), "timed out")
    }
}

private final class ComposerSendProtocol: URLProtocol, @unchecked Sendable {
    enum Step {
        case answer(Int, String)
        case lostResponse
        case offline
    }

    struct Attempt {
        let key: String?
        let raw: Data
        var body: [String: Any] { (try? JSONSerialization.jsonObject(with: raw) as? [String: Any]) ?? [:] }
    }

    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var steps: [Step] = []
        private var recorded: [Attempt] = []

        var attempts: [Attempt] { lock.withLock { recorded } }

        func reset() { lock.withLock { steps = []; recorded = [] } }
        func script(_ steps: [Step]) { lock.withLock { self.steps = steps } }

        func next(_ attempt: Attempt) -> Step {
            lock.withLock {
                recorded.append(attempt)
                return steps.isEmpty ? .offline : steps.removeFirst()
            }
        }
    }

    static let state = State()

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        guard request.url?.path == "/api/v1/actions" else { return respond(200, "{}") }
        switch Self.state.next(Attempt(key: request.value(forHTTPHeaderField: "Idempotency-Key"), raw: requestBody() ?? Data())) {
        case .answer(let status, let text): respond(status, text)
        case .lostResponse: client?.urlProtocol(self, didFailWithError: URLError(.networkConnectionLost))
        case .offline: client?.urlProtocol(self, didFailWithError: URLError(.notConnectedToInternet))
        }
    }

    private func respond(_ status: Int, _ text: String) {
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(text.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    private func requestBody() -> Data? {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            guard count > 0 else { break }
            data.append(buffer, count: count)
        }
        return data
    }
}
