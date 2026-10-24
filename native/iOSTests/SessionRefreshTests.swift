import Combine
import Foundation
import XCTest
@testable import Agenthail

final class SessionRefreshTests: XCTestCase {
    @MainActor
    func testDetailRefreshKeepsMetadata() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        await model.loadSession("computer::demo")
        try await waitUntil(timeout: 2) { model.selectedDetail?.model == "Claude Opus" }

        await model.refreshSession("computer::demo")

        XCTAssertEqual(model.selectedDetail?.model, "Claude Opus")
        XCTAssertEqual(model.selectedDetail?.context?.usedTokens, 1234)
        XCTAssertEqual(model.selectedDetail?.goal?.objective, "Keep the metadata")
        XCTAssertEqual(model.selectedDetail?.models?.count, 1)
    }

    @MainActor
    func testARefreshMovesOnlyRowsBeforeThePageIntoOlderActivity() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        model.selectedSessionID = "computer::demo"
        await model.refreshSession("computer::demo")
        var detail = try XCTUnwrap(model.selectedDetail)
        let page = try XCTUnwrap(detail.timeline?.items)
        func row(_ id: String) -> TimelineItem {
            TimelineItem(id: id, kind: "message", role: "assistant", title: "assistant", text: id, timestamp: nil, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
        }
        detail.timeline?.items = [row("fell-off")] + page + [row("streamed")]
        model.selectedDetail = detail
        model.olderActivity = [row("earlier")]

        await model.refreshSession("computer::demo")

        XCTAssertEqual(model.olderActivity.map(\.id), ["earlier", "fell-off"])
    }

    @MainActor
    func testARowStreamedWhileARefreshIsInFlightSurvivesIt() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        SessionRefreshProtocol.state.set(journalSeq: 10)
        let model = makeModel()
        model.selectedSessionID = "computer::demo"
        await model.refreshSession("computer::demo")
        SessionRefreshProtocol.state.holdDetail()
        let refresh = Task { await model.refreshSession("computer::demo") }
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.detailHeld }
        model.applySessionStreamEvent(try JSONDecoder().decode(SessionStreamEvent.self, from: Data(#"{"stream":"session","sessionId":"demo","seq":11,"type":"item","data":{"itemId":"live","version":1,"kind":"message","op":"upsert","role":"assistant","title":"assistant","ts":"2026-10-03T12:00:00Z","body":"arrived during the read","truncated":false}}"#.utf8)))
        SessionRefreshProtocol.state.releaseDetail()
        _ = await refresh.value

        XCTAssertEqual(model.selectedDetail?.timeline?.items.filter { $0.id == "live" }.count, 1)
        XCTAssertEqual(model.sessionStreamCursor, 11)
    }

    @MainActor
    func testARefreshThatAddsNoEarlierRowsLeavesOlderActivityAlone() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        model.selectedSessionID = "computer::demo"
        await model.refreshSession("computer::demo")
        model.olderActivity = [TimelineItem(id: "earlier", kind: "message", role: "assistant", title: "assistant", text: "earlier", timestamp: nil, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)]
        var emissions = 0
        let watching = model.$olderActivity.dropFirst().sink { _ in emissions += 1 }
        defer { watching.cancel() }

        await model.refreshSession("computer::demo")

        XCTAssertEqual(emissions, 0)
    }

    @MainActor
    func testSwitchingBackToACachedSessionResumesItsStreamWithoutReadingAPage() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        SessionRefreshProtocol.state.set(journalSeq: 10)
        let model = makeModel()
        await model.refresh()
        await model.loadSession("computer::demo")
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.streamURLs.contains { $0.contains("id=demo") } }
        await model.loadSession("computer::other")
        let pages = SessionRefreshProtocol.state.detailFetches
        let streams = SessionRefreshProtocol.state.streamURLs.count

        await model.loadSession("computer::demo")
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.streamURLs.dropFirst(streams).contains { $0.contains("id=demo") } }

        XCTAssertEqual(SessionRefreshProtocol.state.detailFetches, pages)
        let resumed = try XCTUnwrap(SessionRefreshProtocol.state.streamURLs.dropFirst(streams).first { $0.contains("id=demo") })
        XCTAssertTrue(resumed.contains("after=10"), resumed)
        XCTAssertEqual(model.selectedDetail?.session.id, "computer::demo")
        XCTAssertFalse(model.loadingSession)
    }

    @MainActor
    func testACachedSessionReopensListingEverySubagentItShowed() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        SessionRefreshProtocol.state.set(journalSeq: 10)
        let model = makeModel()
        await model.refresh()
        await model.loadSession("computer::demo")
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.streamURLs.contains { $0.contains("id=demo") } }
        model.showAllClaudeSubagents()
        await model.loadSession("computer::other")
        let metadataReads = SessionRefreshProtocol.state.metadataURLs.count

        await model.loadSession("computer::demo")
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.metadataURLs.dropFirst(metadataReads).contains { $0.contains("id=demo") } }

        XCTAssertTrue(model.showsAllClaudeSubagents)
        let reopened = SessionRefreshProtocol.state.metadataURLs.dropFirst(metadataReads).filter { $0.contains("id=demo") }
        XCTAssertTrue(reopened.allSatisfy { $0.contains("subagents=all") }, "\(reopened)")
    }

    @MainActor
    func testOpeningASessionThatCannotAnswerRequestsPublishesNothing() async {
        let model = AgenthailIOSModel(autoConnect: false)
        let session = SessionState(id: "computer::demo", surface: "claude", name: "demo", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: false, readOnlyReason: nil)
        var published = 0
        let watch = model.objectWillChange.sink { published += 1 }
        await model.syncRequests(for: session)
        watch.cancel()
        XCTAssertNil(model.sessionRequests)
        XCTAssertEqual(published, 0)
    }

    @MainActor
    func testRequestStateIsSetOnlyWhenItChanges() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        await model.refresh()
        await model.loadSession("computer::demo")
        var capabilities = Capabilities()
        capabilities.respond = true
        var session = SessionState(id: "computer::demo", surface: "claude", name: "demo", alias: nil, status: "busy", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: capabilities, readOnly: false, readOnlyReason: nil)
        var sets: [SessionRequests?] = []
        let watch = model.$sessionRequests.dropFirst().sink { sets.append($0) }
        defer { watch.cancel() }

        await model.syncRequests(for: session)
        XCTAssertEqual(sets.map { $0?.permissionMode }, ["default"])
        session.waitingOn = WaitingState(kind: "question", id: "q")
        await model.syncRequests(for: session)
        XCTAssertEqual(sets.count, 1, "rereading the same requests sets nothing")

        await model.loadSession("computer::other")
        XCTAssertEqual(sets.count, 2)
        XCTAssertNil(model.sessionRequests)
        await model.loadSession("computer::demo")
        XCTAssertEqual(sets.count, 2, "opening a session with no request state sets nothing")
    }

    @MainActor
    func testASessionWhoseSourceResetWhileShownReadsAPageWhenReopened() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        SessionRefreshProtocol.state.set(journalSeq: 10)
        let model = makeModel()
        await model.refresh()
        await model.loadSession("computer::demo")
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.streamURLs.contains { $0.contains("id=demo") } }
        model.applySessionStreamEvent(try JSONDecoder().decode(SessionStreamEvent.self, from: Data(#"{"stream":"session","sessionId":"demo","seq":11,"type":"item","data":{"itemId":"source","version":1,"kind":"source-error","op":"reset","ts":"2026-10-03T12:00:00Z","reason":"upstream unavailable","truncated":false}}"#.utf8)))
        await model.loadSession("computer::other")
        let pages = SessionRefreshProtocol.state.detailFetches

        await model.loadSession("computer::demo")

        XCTAssertEqual(SessionRefreshProtocol.state.detailFetches, pages + 1)
    }

    @MainActor
    func testACachedSessionItsComputerNoLongerListsIsDropped() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        SessionRefreshProtocol.state.set(journalSeq: 10)
        let model = makeModel()
        await model.refresh()
        await model.loadSession("computer::other")
        await model.loadSession("computer::demo")
        model.store.commandsStreamed(RuntimeCommands(commands: [], unavailable: ""), for: "computer::other")
        XCTAssertNotNil(model.store.peek("computer::other"))

        await model.refresh()

        XCTAssertNil(model.store.peek("computer::other"))
        XCTAssertNil(model.store.commands["computer::other"])
        XCTAssertNotNil(model.store.peek("computer::demo") ?? model.selectedDetail)
    }

    @MainActor
    func testTheSessionListFollowsEachCatalogChange() async {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        await model.refresh()
        func running(_ search: String = "") -> [String] { model.sessionTree(scope: .running, search: search).projects.flatMap { $0.checkouts.flatMap { $0.families.map(\.root.id) } } }
        XCTAssertTrue(running().contains("computer::demo"))
        for search in ["", "demo", "zzz-no-match"] {
            XCTAssertEqual(model.sessionTree(scope: .recent, search: search).counts[.running], running(search).count, "the Running chip counts what Running lists for \"\(search)\"")
        }

        SessionRefreshProtocol.state.set(snapshotStatus: "idle")
        await model.refresh()

        XCTAssertFalse(running().contains("computer::demo"))
        XCTAssertEqual(model.sessionTree(scope: .running, search: "zzz-no-match").projects.count, 0)
    }

    @MainActor
    func testSnapshotStatusReachesOpenSession() async {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        model.selectedSessionID = "computer::demo"
        await model.refreshSession("computer::demo")
        XCTAssertEqual(model.selectedDetail?.session.status, "busy")

        SessionRefreshProtocol.state.set(snapshotStatus: "idle")
        await model.refresh()

        XCTAssertEqual(model.selectedDetail?.session.status, "idle")
    }

    @MainActor
    func testSnapshotStateReachesOpenSessionLoadedBeforeIt() async {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        model.selectedSessionID = "computer::demo"
        await model.refreshSession("computer::demo")
        XCTAssertEqual(model.selectedDetail?.capabilities.respond, false)

        SessionRefreshProtocol.state.set(snapshotFields: Self.liveFields)
        await model.refresh()

        assertLiveFields(model.selectedDetail)
    }

    private static let liveFields = #"{"alias":"renamed","name":"Renamed","cwd":"/moved","transport":"uds","runtimeKind":"claude-cli","lastActive":"2026-10-22T03:00:00Z","readOnly":true,"readOnlyReason":"Opened elsewhere","runtime":{"launcher":"cmux"},"waitingOn":{"kind":"question","id":"demo:question:held:r-1","answerable":true},"backgroundTasks":{"running":true,"source":"transcript"},"usageLimit":{"resetsAt":"2026-10-22T08:00:00Z"},"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false,"respond":true}}"#

    private func assertLiveFields(_ detail: SessionDetail?, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertEqual(detail?.capabilities.respond, true, file: file, line: line)
        XCTAssertEqual(detail?.capabilities.goal, false, file: file, line: line)
        XCTAssertEqual(detail?.readOnly, true, file: file, line: line)
        XCTAssertEqual(detail?.readOnlyReason, "Opened elsewhere", file: file, line: line)
        XCTAssertEqual(detail?.alias, "renamed", file: file, line: line)
        XCTAssertEqual(detail?.session.name, "Renamed", file: file, line: line)
        XCTAssertEqual(detail?.session.cwd, "/moved", file: file, line: line)
        XCTAssertEqual(detail?.session.transport, "uds", file: file, line: line)
        XCTAssertEqual(detail?.session.runtimeKind, "claude-cli", file: file, line: line)
        XCTAssertEqual(detail?.session.runtime?.launcher, "cmux", file: file, line: line)
        XCTAssertEqual(detail?.session.lastActive, "2026-10-22T03:00:00Z", file: file, line: line)
        XCTAssertEqual(detail?.session.waitingOn?.id, "demo:question:held:r-1", file: file, line: line)
        XCTAssertNotNil(detail?.session.backgroundTasks, file: file, line: line)
        XCTAssertNotNil(detail?.session.usageLimit, file: file, line: line)
    }

    @MainActor
    func testOlderSnapshotDoesNotOverwriteNewerDetail() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        model.selectedSessionID = "computer::demo"
        await model.refreshSession("computer::demo")
        SessionRefreshProtocol.state.holdSnapshot()
        let snapshot = Task { await model.refresh() }
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.snapshotHeld }

        SessionRefreshProtocol.state.set(detailStatus: "idle")
        await model.refreshSession("computer::demo")
        SessionRefreshProtocol.state.releaseSnapshot()
        _ = await snapshot.value

        XCTAssertEqual(model.selectedDetail?.session.status, "idle")
    }

    @MainActor
    func testOlderSnapshotDoesNotOverwriteNewerMatchingSnapshot() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        model.selectedSessionID = "computer::demo"
        await model.refreshSession("computer::demo")
        SessionRefreshProtocol.state.set(snapshotStatus: "idle")
        SessionRefreshProtocol.state.holdSnapshot()
        let older = Task { await model.refresh() }
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.snapshotHeld }

        SessionRefreshProtocol.state.set(snapshotStatus: "busy")
        SessionRefreshProtocol.state.passNextSnapshot()
        await model.refresh()
        SessionRefreshProtocol.state.releaseSnapshot()
        _ = await older.value

        XCTAssertEqual(model.selectedDetail?.session.status, "busy")
    }

    @MainActor
    func testOlderDetailDoesNotOverwriteNewerSnapshot() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        model.selectedSessionID = "computer::demo"
        await model.refreshSession("computer::demo")
        SessionRefreshProtocol.state.holdDetail()
        let detail = Task { await model.refreshSession("computer::demo") }
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.detailHeld }

        SessionRefreshProtocol.state.set(snapshotStatus: "idle")
        SessionRefreshProtocol.state.set(snapshotFields: Self.liveFields)
        await model.refresh()
        SessionRefreshProtocol.state.releaseDetail()
        await detail.value

        XCTAssertEqual(model.selectedDetail?.session.status, "idle")
        assertLiveFields(model.selectedDetail)
    }

    @MainActor
    func testLiveStreamReplacesPolling() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        await model.loadSession("computer::demo")
        try await Task.sleep(for: .seconds(5))
        XCTAssertEqual(SessionRefreshProtocol.state.detailFetches, 1)
        model.selectedSessionID = nil
    }

    @MainActor
    func testUnsupportedStreamResubscribesOnceTheSessionCanStream() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: true)
        SessionRefreshProtocol.state.set(snapshotCanStream: false)
        let model = makeModel()
        await model.refresh()
        await model.loadSession("computer::demo")
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.streamRequests >= 1 }
        try await Task.sleep(for: .seconds(5))
        XCTAssertEqual(SessionRefreshProtocol.state.detailFetches, 1)
        XCTAssertEqual(SessionRefreshProtocol.state.streamRequests, 1)

        SessionRefreshProtocol.state.set(streamUnsupported: false)
        SessionRefreshProtocol.state.set(snapshotCanStream: true)
        await model.refresh()
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.detailFetches >= 2 && SessionRefreshProtocol.state.streamRequests >= 2 }
        try await Task.sleep(for: .seconds(1))
        XCTAssertEqual(SessionRefreshProtocol.state.detailFetches, 2)
        XCTAssertEqual(SessionRefreshProtocol.state.streamRequests, 2)
        model.selectedSessionID = nil
    }

    @MainActor
    func testReconnectResubscribesAnUnsupportedSessionOnce() async throws {
        let model = try await openUnsupportedSession()
        SessionRefreshProtocol.state.set(streamUnsupported: false)
        SessionRefreshProtocol.state.set(snapshotCanStream: true)
        await model.connection("computer")!.eventStreamConnected()
        try await Task.sleep(for: .seconds(1))
        XCTAssertEqual(SessionRefreshProtocol.state.detailFetches, 2)
        XCTAssertEqual(SessionRefreshProtocol.state.streamRequests, 2)
        model.selectedSessionID = nil
    }

    @MainActor
    func testReconnectJoinsAPendingResume() async throws {
        let model = try await openUnsupportedSession()
        SessionRefreshProtocol.state.set(streamUnsupported: false)
        SessionRefreshProtocol.state.set(snapshotCanStream: true)
        SessionRefreshProtocol.state.holdDetail()
        await model.refresh()
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.detailHeld }
        let reconnect = Task { await model.connection("computer")!.eventStreamConnected() }
        try await Task.sleep(for: .milliseconds(300))
        SessionRefreshProtocol.state.releaseDetail()
        await reconnect.value
        try await Task.sleep(for: .milliseconds(500))
        XCTAssertEqual(SessionRefreshProtocol.state.detailFetches, 2)
        XCTAssertEqual(SessionRefreshProtocol.state.streamRequests, 2)
        model.selectedSessionID = nil
    }

    @MainActor
    func testReconnectJoinsAnOpeningSession() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        await model.refresh()
        SessionRefreshProtocol.state.holdDetail()
        let load = Task { await model.loadSession("computer::demo") }
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.detailHeld }
        let reconnect = Task { await model.connection("computer")!.eventStreamConnected() }
        try await Task.sleep(for: .milliseconds(300))
        SessionRefreshProtocol.state.releaseDetail()
        await load.value
        await reconnect.value
        try await Task.sleep(for: .milliseconds(500))
        XCTAssertEqual(SessionRefreshProtocol.state.detailFetches, 1)
        XCTAssertEqual(SessionRefreshProtocol.state.streamRequests, 1)
        model.selectedSessionID = nil
    }

    @MainActor
    func testEventsRefreshAnUnsupportedSessionOnce() async throws {
        let model = try await openUnsupportedSession()
        for id in 1...3 {
            await model.connection("computer")!.receive(AgenthailEvent(id: UInt64(id), type: "session.updated", timestamp: "", entityId: "demo"))
        }
        await model.connection("computer")!.receive(AgenthailEvent(id: 4, type: "session.updated", timestamp: "", entityId: "other"))
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.detailFetches >= 2 }
        try await Task.sleep(for: .seconds(1))
        XCTAssertEqual(SessionRefreshProtocol.state.detailFetches, 2)
        XCTAssertEqual(SessionRefreshProtocol.state.streamRequests, 1)
        model.selectedSessionID = nil
    }

    @MainActor
    private func openUnsupportedSession() async throws -> AgenthailIOSModel {
        SessionRefreshProtocol.state.reset(streamUnsupported: true)
        SessionRefreshProtocol.state.set(snapshotCanStream: false)
        let model = makeModel()
        await model.refresh()
        await model.loadSession("computer::demo")
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.streamRequests >= 1 }
        try await Task.sleep(for: .milliseconds(300))
        XCTAssertEqual(SessionRefreshProtocol.state.detailFetches, 1)
        return model
    }

    @MainActor
    private func makeModel() -> AgenthailIOSModel {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [SessionRefreshProtocol.self]
        return AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: URLSession(configuration: configuration)))
    }

    @MainActor
    private func waitUntil(timeout: TimeInterval, _ condition: () -> Bool) async throws {
        let deadline = Date().addingTimeInterval(timeout)
        while !condition(), Date() < deadline { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertTrue(condition(), "Timed out")
    }
}

private final class SessionRefreshProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let condition = NSCondition()
        private var fetches = 0
        private var unsupported = false
        private var detail = "busy"
        private var snapshot = "busy"
        private var fields = "{}"
        private var holdingSnapshot = false
        private var holdingDetail = false
        private var snapshotWaiting = false
        private var detailWaiting = false
        private var seq: Int?
        var journalSeq: Int? { condition.withLock { seq } }
        func set(journalSeq value: Int?) { condition.withLock { seq = value } }

        var detailFetches: Int { condition.withLock { fetches } }
        var streamUnsupported: Bool { condition.withLock { unsupported } }
        var snapshotHeld: Bool { condition.withLock { snapshotWaiting } }
        var detailHeld: Bool { condition.withLock { detailWaiting } }
        func reset(streamUnsupported: Bool) {
            condition.withLock {
                fetches = 0; unsupported = streamUnsupported; detail = "busy"; snapshot = "busy"
                holdingSnapshot = false; holdingDetail = false; snapshotWaiting = false; detailWaiting = false; passing = 0; seq = nil
                canStream = true; streams = 0; fields = "{}"; streamPaths = []; metadataPaths = []
                condition.broadcast()
            }
        }
        func set(detailStatus: String) { condition.withLock { detail = detailStatus } }
        func set(snapshotStatus: String) { condition.withLock { snapshot = snapshotStatus } }
        func set(snapshotFields: String) { condition.withLock { fields = snapshotFields } }
        private var passing = 0
        private var canStream = true
        private var streams = 0
        var snapshotCanStream: Bool { condition.withLock { canStream } }
        var streamRequests: Int { condition.withLock { streams } }
        func set(snapshotCanStream value: Bool) { condition.withLock { canStream = value } }
        func set(streamUnsupported value: Bool) { condition.withLock { unsupported = value } }
        private var streamPaths: [String] = []
        var streamURLs: [String] { condition.withLock { streamPaths } }
        func streamRequested(_ url: URL?) { condition.withLock { streams += 1; streamPaths.append(url?.absoluteString ?? "") } }
        private var metadataPaths: [String] = []
        var metadataURLs: [String] { condition.withLock { metadataPaths } }
        func metadataRequested(_ url: URL?) { condition.withLock { metadataPaths.append(url?.absoluteString ?? "") } }
        func holdSnapshot() { condition.withLock { holdingSnapshot = true } }
        func passNextSnapshot() { condition.withLock { passing += 1 } }
        func holdDetail() { condition.withLock { holdingDetail = true } }
        func releaseSnapshot() { condition.withLock { holdingSnapshot = false; condition.broadcast() } }
        func releaseDetail() { condition.withLock { holdingDetail = false; condition.broadcast() } }

        func detailResponse() -> String {
            condition.lock(); defer { condition.unlock() }
            fetches += 1
            let status = detail
            let journalSeq = seq
            if holdingDetail {
                detailWaiting = true
                while holdingDetail { condition.wait() }
                detailWaiting = false
            }
            return SessionRefreshProtocol.detail(status: status, journalSeq: journalSeq)
        }

        func snapshotResponse() -> String {
            condition.lock(); defer { condition.unlock() }
            let status = snapshot, overrides = fields
            if passing > 0 {
                passing -= 1
            } else if holdingSnapshot {
                snapshotWaiting = true
                while holdingSnapshot { condition.wait() }
                snapshotWaiting = false
            }
            return SessionRefreshProtocol.snapshot(status: status, canStream: canStream, fields: overrides)
        }
    }

    static let state = State()
    static func detail(status: String, journalSeq: Int? = nil) -> String {
        var object = try! JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as! [String: Any]
        if let journalSeq { object["journalSeq"] = journalSeq }
        for key in ["context", "goal", "model", "models"] { object.removeValue(forKey: key) }
        var session = object["session"] as! [String: Any]
        session["status"] = status
        object["session"] = session
        return String(decoding: try! JSONSerialization.data(withJSONObject: object), as: UTF8.self)
    }
    static func snapshot(status: String, canStream: Bool, fields: String) -> String {
        var object = try! JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as! [String: Any]
        var sessions = object["sessions"] as! [[String: Any]]
        if let index = sessions.firstIndex(where: { $0["id"] as? String == "demo" }) {
            sessions[index]["status"] = status
            let overrides = try! JSONSerialization.jsonObject(with: Data(fields.utf8)) as! [String: Any]
            sessions[index].merge(overrides) { _, new in new }
            var capabilities = sessions[index]["capabilities"] as! [String: Any]
            capabilities["stream"] = canStream
            sessions[index]["capabilities"] = capabilities
        }
        object["sessions"] = sessions
        return String(decoding: try! JSONSerialization.data(withJSONObject: object), as: UTF8.self)
    }
    static let metadata = #"{"model":"Claude Opus","models":[{"id":"opus","displayName":"Claude Opus"}],"context":{"usedTokens":1234,"contextWindow":200000,"compacting":false,"compactionCount":0},"goal":{"objective":"Keep the metadata","status":"active"}}"#

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var status = 200
        var body = #"{"items":[]}"#
        switch request.url?.path {
        case "/api/v1/session", "/api/v1/snapshot":
            let detail = request.url?.path == "/api/v1/session"
            DispatchQueue.global().async { [self] in
                finish(status: 200, body: detail ? Self.state.detailResponse() : Self.state.snapshotResponse())
            }
            return
        case "/api/v1/session-metadata":
            Self.state.metadataRequested(request.url)
            body = Self.metadata
        case "/api/v1/requests":
            body = #"{"permissionMode":"default","requests":[]}"#
        case "/api/v1/session-stream":
            Self.state.streamRequested(request.url)
            if Self.state.streamUnsupported {
                status = 409
                body = #"{"error":{"code":"stream_unsupported","message":"no stream"}}"#
            } else {
                client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "text/event-stream"])!, cacheStoragePolicy: .notAllowed)
                client?.urlProtocol(self, didLoad: Data(": open\n\n".utf8))
                return
            }
        default:
            break
        }
        finish(status: status, body: body)
    }

    private func finish(status: Int, body: String) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
