import XCTest
@testable import Agenthail

private final class InboxHostProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var requests: [String] = []
    nonisolated(unsafe) static var failing: Set<String> = []
    nonisolated(unsafe) static var actions: [[String: Any]] = []
    private static let lock = NSLock()

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let url = request.url!
        Self.lock.withLock { Self.requests.append(url.host! + url.path + "?" + (url.query ?? "")) }
        if url.path == "/api/v1/actions" { Self.recordAction(request, host: url.host!) }
        if url.host == "slow.invalid" { return }
        if Self.lock.withLock({ Self.failing.contains(url.host!) }) {
            client?.urlProtocol(self, didFailWithError: URLError(.timedOut))
            return
        }
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let before = query.first { $0.name == "before" }?.value
        if url.path == "/api/v1/queue", let id = query.first(where: { $0.name == "id" })?.value, let row = Self.lookup(id: id, on: url.host!, via: query.first { $0.name == "host" }?.value) {
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(row.utf8))
            client?.urlProtocolDidFinishLoading(self)
            return
        }
        let (status, body) = Self.answer(host: url.host!, path: url.path, before: before)
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: url, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    private static func lookup(id: String, on host: String, via remote: String?) -> String? {
        if id == "9" && remote == "hserver" { return #"{"items":[\#(queued(9, at: "2026-11-02T10:00:00.000000000Z"))]}"# }
        if id == "10" && remote == nil { return #"{"items":[\#(queued(10, at: "2026-11-02T10:00:00.000000000Z").replacingOccurrences(of: #""sessionId":"s1""#, with: #""sessionId":"hserver::s1""#))]}"# }
        if id == "11" && remote == nil { return #"{"items":[\#(queued(11, at: "2026-11-02T10:00:00.000000000Z").replacingOccurrences(of: #""sessionId":"s1""#, with: #""sessionId":"laptop-own""#))]}"# }
        return #"{"items":[]}"#
    }

    private static func recordAction(_ request: URLRequest, host: String) {
        var data = request.httpBody ?? Data()
        if let stream = request.httpBodyStream {
            stream.open()
            defer { stream.close() }
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let count = stream.read(&buffer, maxLength: buffer.count)
                if count <= 0 { break }
                data.append(contentsOf: buffer[0..<count])
            }
        }
        var action = (try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:]
        action["to"] = host
        lock.withLock { actions.append(action) }
    }

    private static func answer(host: String, path: String, before: String?) -> (Int, String) {
        if path == "/api/v1/queue" {
            let rows = host == "older.invalid" || host == "dated.invalid"
                ? [queued(9, at: "2026-11-02T10:00:00.000000000Z"), #"{"id":2,"sessionId":"s1","target":"@s1","message":"done","status":"delivered","attempts":1,"queuedAt":"2026-11-01T10:00:00.000000000Z","historical":true,"evidence":"delivered"}"#]
                : []
            return (200, #"{"items":[\#(rows.joined(separator: ","))]}"#)
        }
        switch host {
        case "dated.invalid":
            return (404, #"{"error":{"code":"unknown_route","message":"This Agenthail has no /api/v1/inbox; it runs an older version."}}"#)
        case "older.invalid":
            return (200, before == nil
                ? #"{"items":[\#(message(20, queue: 9, pending: true, at: "2026-11-02T10:00:00Z")),\#(message(19, at: "2026-11-02T09:00:00Z")),\#(message(18, at: "2026-11-01T10:00:00Z"))],"nextBefore":18}"#
                : #"{"items":[\#(message(3, at: "2026-08-01T10:00:00Z"))]}"#)
        default:
            return (200, #"{"items":[\#(message(5, at: "2026-11-02T08:00:00Z"))]}"#)
        }
    }

    private static func queued(_ id: Int, at: String) -> String {
        #"{"id":\#(id),"sessionId":"s1","target":"@s1","message":"m\#(id)","status":"pending","attempts":0,"queuedAt":"\#(at)","historical":false,"evidence":"queued"}"#
    }

    private static func message(_ id: Int, queue: Int? = nil, pending: Bool = false, at: String) -> String {
        let queued = queue.map { #""queueId":\#($0),"queueStatus":"pending","# } ?? ""
        return #"{"id":\#(id),"sessionId":"s1","target":"@s1","message":"m\#(id)","status":"\#(pending ? "queued" : "delivered")","evidence":"\#(pending ? "queued" : "delivered")","pending":\#(pending),\#(queued)"createdAt":"\#(at)"}"#
    }
}

@MainActor
final class InboxPageTests: XCTestCase {
    private func host(_ name: String, remote: String? = nil) -> InboxPage.Host {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [InboxHostProtocol.self]
        return InboxPage.Host(id: name, name: name, api: AgenthailAPI(baseURL: URL(string: "https://\(name).invalid")!, token: "t", session: URLSession(configuration: configuration)), remote: remote)
    }

    func testAnUnreachableHostDoesNotHoldTheOthersPastItsShortTimeout() async throws {
        InboxHostProtocol.requests = []
        let started = Date()
        var partials: [(TimeInterval, Int)] = []
        let page = try await InboxPage.load(from: [host("slow"), host("older"), host("fast")]) { partial in
            partials.append((Date().timeIntervalSince(started), partial.messages.count))
        }
        let elapsed = Date().timeIntervalSince(started)
        XCTAssertLessThan(elapsed, InboxPage.hostTimeout + 3)
        XCTAssertEqual(partials.count, 2)
        XCTAssertEqual(partials.last?.1, 4)
        XCTAssertLessThan(try XCTUnwrap(partials.last).0, 2, "the reachable hosts show before the unreachable one times out")
        XCTAssertEqual(page.unreachable, ["slow"])
        XCTAssertEqual(page.messages.map(\.listID), ["older/20", "older/19", "fast/5", "older/18"])
        XCTAssertEqual(page.cursors, ["older": 18])
    }

    func testCurrentIsTheActiveQueueAndHistoryLeavesOutWhatCurrentShows() async throws {
        let page = try await InboxPage.load(from: [host("older"), host("fast")])
        XCTAssertEqual(page.active.map(\.listID), ["older/9"], "a finished row an older computer still sends is not current")
        XCTAssertEqual(page.history.map(\.listID), ["older/19", "fast/5", "older/18"])
    }

    func testHistoryKeepsAMessageWhoseQueueIDMatchesARowOnAnotherHost() {
        let message = InboxMessage(id: 1, sessionId: "s", target: "@s", sourceSessionId: nil, source: nil, message: "m", status: "delivered", evidence: "delivered", pending: false, queueId: 9, createdAt: "2026-11-02T08:00:00Z", hostID: "fast")
        var page = InboxPage(messages: [message])
        page.active = [InboxPage.qualified(queueRow(id: 9), host: "older")]
        XCTAssertEqual(page.history.map(\.listID), ["fast/1"])
    }

    private func queueRow(id: Int) -> QueueState {
        try! JSONDecoder().decode(QueueState.self, from: Data(#"{"id":\#(id),"sessionId":"s","target":"@s","message":"m","status":"pending","attempts":0,"queuedAt":"2026-11-02T10:00:00.000000000Z","evidence":"queued"}"#.utf8))
    }

    func testAComputerTooOldForTheInboxIsNamedAndOthersStillList() async throws {
        let page = try await InboxPage.load(from: [host("dated"), host("fast")])
        XCTAssertEqual(page.outdated, ["dated"])
        XCTAssertEqual(page.unreachable, [])
        XCTAssertEqual(page.messages.map(\.listID), ["fast/5"])
        XCTAssertEqual(page.active.map(\.listID), ["dated/9"], "its waiting rows are still its own")

        let alone = try await InboxPage.load(from: [host("dated")])
        XCTAssertEqual(alone.outdated, ["dated"], "being the only computer is not a failure")
    }

    func testARemoteHostBehindAnOlderComputerShowsNoneOfThatComputersRows() async throws {
        InboxHostProtocol.requests = []
        let page = try await InboxPage.load(from: [host("dated", remote: "peer")])
        XCTAssertEqual(page.outdated, ["dated"])
        XCTAssertEqual(page.active, [])
        XCTAssertEqual(Set(InboxHostProtocol.requests), ["dated.invalid/api/v1/queue?host=peer", "dated.invalid/api/v1/inbox?host=peer"])
    }

    func testTheInboxPagesOlderHistory() async throws {
        InboxHostProtocol.requests = []
        let first = try await InboxPage.load(from: [host("older")])
        XCTAssertEqual(Set(InboxHostProtocol.requests), ["older.invalid/api/v1/queue?", "older.invalid/api/v1/inbox?"])
        XCTAssertEqual(first.messages.map(\.sessionId), ["older::s1", "older::s1", "older::s1"])
        XCTAssertTrue(first.hasOlder)

        InboxHostProtocol.requests = []
        let older = try await InboxPage.load(from: [host("older")], before: first.cursors)
        XCTAssertEqual(InboxHostProtocol.requests, ["older.invalid/api/v1/inbox?before=18"])
        let merged = first.appending(older)
        XCTAssertEqual(merged.messages.map(\.listID), ["older/20", "older/19", "older/18", "older/3"])
        XCTAssertFalse(merged.hasOlder)
        XCTAssertEqual(merged.active.map(\.listID), ["older/9"], "an older page leaves the current rows alone")

        let refreshed = merged.refreshed(with: first)
        XCTAssertEqual(refreshed.messages.map(\.listID), ["older/20", "older/19", "older/18", "older/3"], "reloading the first page keeps what was paged in")
        XCTAssertFalse(refreshed.hasOlder)
    }

    func testAHostThatFailsKeepsItsLoadedRowsAndCursor() async throws {
        defer { InboxHostProtocol.failing = [] }
        let first = try await InboxPage.load(from: [host("older"), host("fast")])
        XCTAssertEqual(first.cursors, ["older": 18])

        InboxHostProtocol.failing = ["older.invalid"]
        let refresh = try await InboxPage.load(from: [host("older"), host("fast")])
        XCTAssertEqual(refresh.unreachable, ["older"])
        let refreshed = first.refreshed(with: refresh)
        XCTAssertEqual(refreshed.messages.map(\.listID), ["older/20", "older/19", "fast/5", "older/18"])
        XCTAssertEqual(refreshed.active.map(\.listID), ["older/9"])
        XCTAssertEqual(refreshed.cursors, ["older": 18], "the failed host can still load older history once it answers")
        XCTAssertEqual(refreshed.unreachable, ["older"])

        do {
            _ = try await InboxPage.load(from: [host("older"), host("fast")], before: refreshed.cursors)
            XCTFail("load older from only a failing host throws, leaving the page as it was")
        } catch {}

        var twoHosts = first
        twoHosts.cursors = ["older": 18, "fast": 5]
        let partlyFailed = InboxPage(cursors: ["older": 18], unreachable: ["older"], answered: ["fast"])
        XCTAssertEqual(twoHosts.appending(partlyFailed).cursors, ["older": 18], "a host that failed keeps its cursor; one that answered with no more pages drops it")

        InboxHostProtocol.failing = []
        let back = try await InboxPage.load(from: [host("older"), host("fast")])
        XCTAssertEqual(refreshed.refreshed(with: back).unreachable, [])
    }

    private func modelWithTheOwnerHeld() throws -> AgenthailIOSModel {
        KeychainStore.removeAll()
        addTeardownBlock { KeychainStore.removeAll(); InboxHostProtocol.failing = [] }
        try AgenthailIOSTests.pair("hlaptop", "https://fast.invalid", name: "laptop")
        try AgenthailIOSTests.pair("hserver", "https://older.invalid", name: "server")
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [InboxHostProtocol.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        var row = HostCatalogTests.row("hserver::s", name: "s")
        row.host = SessionHost(id: "hserver", name: "server", state: "connected")
        var laptop = HostCatalogTests.snapshot([row])
        laptop.daemonHosts([HostHealth(id: "hserver", name: "server", state: "connected")])
        model.setHostSnapshot(laptop, host: "hlaptop")
        model.setHostSnapshot(HostCatalogTests.snapshot([]), host: "hserver")
        let owner = try XCTUnwrap(model.connection("hserver"))
        owner.grace.contact()
        InboxHostProtocol.failing = ["older.invalid"]
        owner.recordStreamInterruption(probeError: URLError(.timedOut))
        XCTAssertTrue(owner.holding, "the owner is shown connected during its grace")
        InboxHostProtocol.actions = []
        return model
    }

    func testEditCancelsThroughAMirrorWhileTheOwnerIsHeld() async throws {
        let model = try modelWithTheOwnerHeld()
        try await model.takeBack(InboxPage.qualified(queueRow(id: 9), host: "hserver"))
        XCTAssertEqual(InboxHostProtocol.actions.first?["to"] as? String, "fast.invalid")
        XCTAssertEqual(InboxHostProtocol.actions.first?["host"] as? String, "hserver")
        XCTAssertEqual(InboxHostProtocol.actions.first?["action"] as? String, "queue-cancel")
    }

    func testRetryAndCancelGoThroughAMirrorWhileTheOwnerIsHeld() async throws {
        let model = try modelWithTheOwnerHeld()
        try await model.updateQueue(id: 9, hostID: "hserver", retry: true)
        try await model.updateQueue(InboxPage.qualified(queueRow(id: 10), host: "hserver"), retry: false)
        let actions = InboxHostProtocol.actions
        XCTAssertEqual(actions.compactMap { $0["action"] as? String }, ["queue-retry", "queue-cancel"])
        XCTAssertEqual(actions.compactMap { $0["to"] as? String }, ["fast.invalid", "fast.invalid"])
        XCTAssertEqual(actions.compactMap { $0["host"] as? String }, ["hserver", "hserver"])
    }

    func testSteerWorksThroughAMirrorWhileTheOwnerIsHeld() async throws {
        let model = try modelWithTheOwnerHeld()
        try await model.steerQueued(InboxPage.qualified(queueRow(id: 9), host: "hserver"), capabilities: Capabilities(steer: true))
        let actions = InboxHostProtocol.actions
        XCTAssertEqual(actions.compactMap { $0["action"] as? String }, ["queue-cancel", "steer"])
        XCTAssertEqual(actions.compactMap { $0["to"] as? String }, ["fast.invalid", "fast.invalid"], "both go through the computer that can reach the session")
        XCTAssertEqual(actions.first?["host"] as? String, "hserver", "the row is cancelled on the computer that holds it")
        XCTAssertEqual(actions.last?["sessionId"] as? String, "hserver::s")
    }

    func testTheInboxAsksEveryComputerAndReachesOneThroughAnother() async throws {
        KeychainStore.removeAll()
        defer { KeychainStore.removeAll() }
        try AgenthailIOSTests.pair("hlaptop", "https://fast.invalid", name: "laptop")
        try AgenthailIOSTests.pair("hdesk", "https://older.invalid", name: "desk")
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [InboxHostProtocol.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        var laptop = HostCatalogTests.snapshot([])
        laptop.daemonHosts([HostHealth(id: "hserver", name: "server", state: "connected")])
        model.setHostSnapshot(laptop, host: "hlaptop")
        model.setHostSnapshot(HostCatalogTests.snapshot([]), host: "hdesk")

        InboxHostProtocol.requests = []
        let all = try await model.inboxPage()
        XCTAssertEqual(Set(InboxHostProtocol.requests.filter { $0.contains("/inbox") }), ["fast.invalid/api/v1/inbox?", "older.invalid/api/v1/inbox?", "fast.invalid/api/v1/inbox?host=hserver"])
        XCTAssertTrue(Set(all.messages.map(\.listID)).isSuperset(of: ["hlaptop/5", "hserver/5", "hdesk/19"]))
        XCTAssertTrue(all.messages.contains { $0.sessionId == "hserver::s1" })

        InboxHostProtocol.actions = []
        try await model.updateQueue(id: 9, hostID: "hserver", retry: true)
        let action = try XCTUnwrap(InboxHostProtocol.actions.first)
        XCTAssertEqual(action["to"] as? String, "fast.invalid")
        XCTAssertEqual(action["host"] as? String, "hserver")
        XCTAssertEqual(action["action"] as? String, "queue-retry")
        XCTAssertEqual(action["queueId"] as? Int, 9)

        InboxHostProtocol.actions = []
        try await model.takeBack(InboxPage.qualified(queueRow(id: 9), host: "hserver"))
        XCTAssertEqual(InboxHostProtocol.actions.first?["host"] as? String, "hserver", "Edit cancels the row on the computer that holds it")
        XCTAssertEqual(InboxHostProtocol.actions.first?["to"] as? String, "fast.invalid")

        InboxHostProtocol.requests = []
        let owned = await model.queuedDelivery(id: 9, sessionID: "hserver::s1")
        XCTAssertEqual(InboxHostProtocol.requests, ["fast.invalid/api/v1/queue?host=hserver&id=9"], "a receipt reads the owner's queue")
        XCTAssertEqual(owned?.listID, "hserver/9")

        let held = await model.queuedDelivery(id: 10, sessionID: "hserver::s1")
        XCTAssertEqual(held?.sessionId, "hserver::s1", "a send held for a down owner is on the computer holding it")

        let other = await model.queuedDelivery(id: 11, sessionID: "hserver::s1")
        XCTAssertNil(other, "a row with the same id for another session is not this receipt")
    }

    func testAPageAsTheComputerSendsItDecodes() throws {
        let page = try JSONDecoder().decode(InboxMessagePage.self, from: Data(#"""
        {"items":[{"id":2,"messageId":"c8c3c290-ab80-43ee-921e-750446bf693e","sessionId":"to","target":"to","sourceSessionId":"agenthail","source":"agenthail/Agenthail","message":"pending message","status":"queued","evidence":"queued","pending":true,"queueId":2,"queueStatus":"pending","createdAt":"2026-11-04T07:38:40.000000000Z","updatedAt":"2026-11-04T07:38:40.000000000Z"},{"id":1,"messageId":"8979cf50-f31b-455f-a539-0389db9d857e","sessionId":"from","target":"from","sourceSessionId":"agenthail","source":"agenthail/Agenthail","message":"dead message","status":"unknown","evidence":"unknown","pending":false,"queueId":1,"queueStatus":"dead","attempts":1,"lastError":"delivery outcome is unknown: connection closed; retry explicitly if the target did not receive it","createdAt":"2026-11-04T07:38:40.000000000Z","updatedAt":"2026-11-04T07:38:40.000000000Z"}]}
        """#.utf8))
        XCTAssertEqual(page.items.map(\.pending), [true, false])
        XCTAssertNotNil(page.items[1].createdDate)
        XCTAssertTrue(page.items[1].canSendAgain)
        XCTAssertFalse(page.items[0].canSendAgain)
        XCTAssertEqual(page.items[1].outcomeLabel, "Never confirmed")
        XCTAssertEqual(page.items[1].senderLabel, "Agenthail")
        XCTAssertEqual(InboxPage.qualified(page.items[1], host: "hlaptop").senderLabel, "Agenthail", "the computer's name does not change who sent it")
    }

    func testAnAgentReadsAsItsNameAndThePersonAsYou() {
        func sender(_ id: String, _ source: String) -> String? {
            InboxMessage(id: 1, sessionId: "s", target: "@s", sourceSessionId: id, source: source, message: "m", status: "delivered", evidence: "delivered", pending: false, createdAt: "2026-11-02T08:00:00Z").senderLabel
        }
        XCTAssertEqual(sender("agenthail-person", "agenthail/Person"), "you")
        XCTAssertEqual(sender("agenthail-cli", "agenthail/CLI"), "the command line or a script")
        XCTAssertEqual(sender("worker-id", "@worker"), "@worker")
    }

    func testAPeerEnvelopeReadsAsItsSenderAndBody() {
        let envelope = PeerEnvelope(#"<cross-session-message from="uds:/run/x.sock" from-name="@reviewer" hop-chain="ab">Check the signing step.</cross-session-message>"#)
        XCTAssertEqual(envelope.sender, "@reviewer")
        XCTAssertEqual(envelope.body, "Check the signing step.")
    }
}
