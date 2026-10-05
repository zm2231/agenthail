import Foundation
import Network

@main
struct SessionPaneTest {
    @MainActor
    static func main() async {
        let model = AgenthailModel(connecting: false)
        let first = model.openPane()
        let second = model.openPane()
        first.select("A")
        second.select("A")

        first.composer = "shared draft"
        check(second.composer == "shared draft", "panes on one session share its draft")

        second.select("B")
        check(second.composer.isEmpty, "another session starts with its own draft")
        second.composer = "only B"
        second.select("A")
        check(first.composer == "shared draft" && second.composer == "shared draft", "returning to a session keeps its draft")
        check(model.draft(for: "B").text == "only B", "leaving a session keeps its draft")

        model.draft(for: "A").restore("restored")
        check(first.composer == "shared draft\n\nrestored" && second.composer == first.composer, "restored text reaches every pane on the session")

        model.closePane(second)
        second.select("C")
        check(second.selectedSessionID == "A", "a closed pane ignores selection")
        check(first.selectedSessionID == "A", "closing one pane leaves the others")

        model.setTurnSettings(TurnSettings(effort: "high", mode: .plan), for: "A")
        check(model.turnSettings(for: "A") == TurnSettings(effort: "high", mode: .plan) && model.turnSettings(for: "B").isEmpty, "next-turn settings belong to one session")
        model.setTurnSettings(TurnSettings(), for: "A")
        check(model.turnSettings(for: "A").isEmpty, "resetting removes a session's next-turn settings")

        let working = SessionState(id: "W", surface: "codex", name: "w", alias: nil, status: "busy", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        check(SessionPane.stopAvailable(working, removed: false, draftEmpty: true), "an empty draft lets Stop interrupt a working session")
        check(!SessionPane.stopAvailable(working, removed: false, draftEmpty: false), "a draft turns Stop into queue or steer")
        check(!SessionPane.stopAvailable(working, removed: true, draftEmpty: true), "a removed session cannot be stopped")
        let readOnly = SessionState(id: "R", surface: "codex", name: "r", alias: nil, status: "busy", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: true, readOnlyReason: nil)
        check(!SessionPane.stopAvailable(readOnly, removed: false, draftEmpty: true), "a read-only session cannot be stopped")
        let idle = SessionState(id: "I", surface: "codex", name: "i", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        check(!SessionPane.stopAvailable(idle, removed: false, draftEmpty: true) && !SessionPane.stopAvailable(nil, removed: false, draftEmpty: true), "nothing to stop when idle or unselected")

        let watched = model.openPane()
        watched.select("E")
        var changes = 0
        let observation = watched.objectWillChange.sink { changes += 1 }
        watched.composer = "a"
        check(!watched.draftIsEmpty && changes == 1, "typing the first character republishes the pane once")
        watched.composer = "ab"
        watched.composer = "abc"
        check(changes == 1, "further typing does not republish the pane")
        watched.composer = "  "
        check(watched.draftIsEmpty && changes == 2, "clearing the draft republishes the pane")
        model.draft(for: "E").text = "from another pane"
        check(!watched.draftIsEmpty, "edits from a pane sharing the draft are observed")
        watched.select("F")
        check(watched.draftIsEmpty, "a new session's empty draft is observed")
        watched.composerDraft.attachments = [URL(fileURLWithPath: "/tmp/shot.png")]
        check(!watched.draftIsEmpty && !watched.composerDraft.isEmpty, "an attachment alone makes the draft sendable")
        watched.composerDraft.attachments = []
        check(watched.draftIsEmpty, "removing the last attachment empties the draft")
        observation.cancel()

        func conflict(_ json: String) -> AgenthailAPIError { AgenthailAPI.streamConflict(Data(json.utf8)) }
        if case .streamGap = conflict(#"{"error":{"code":"stream_gap","message":"gone"}}"#) {} else { check(false, "a retained-cursor conflict is a gap") }
        if case .streamUnsupported = conflict(#"{"error":{"code":"stream_unsupported","message":"no stream"}}"#) {} else { check(false, "a session without a live stream is not a gap") }
        if case .request(409, "Codex is unavailable") = conflict(#"{"error":{"code":"transport_unavailable","message":"Codex is unavailable"}}"#) {} else { check(false, "other conflicts keep their message and are not gaps") }
        if case .request(409, _) = conflict("not json") {} else { check(false, "an unreadable conflict is not a gap") }

        let detailJSON = #"{"session":{"id":"R","surface":"codex","name":"r","status":"idle","lastActive":"2026-10-04T12:00:00Z"},"exchanges":[],"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"readOnly":false,"readOnlyReason":"","timeline":{"items":[],"nextBefore":null,"truncated":false}}"#
        var detailRequests = 0
        let flaky = try! StubServer { line, _ in
            guard line.contains("/api/v1/session?") else { return StubServer.reply("404 Not Found", "{}") }
            detailRequests += 1
            return detailRequests == 1 ? StubServer.reply("200 OK", detailJSON) : StubServer.reply("500 Internal Server Error", #"{"error":{"message":"refresh failed"}}"#)
        }
        let flakyPort = await flaky.ready()
        let live = AgenthailModel(connecting: false, api: AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(flakyPort)")!, token: "t", session: URLSession(configuration: .ephemeral)))
        let livePane = live.openPane()
        livePane.select("R")
        for _ in 0..<60 where livePane.detail == nil { try? await Task.sleep(for: .milliseconds(50)) }
        check(livePane.detail?.session.id == "R" && !livePane.detailStale, "the first load shows the session")
        livePane.sessionChanged("R")
        for _ in 0..<60 where !livePane.detailRefreshFailed { try? await Task.sleep(for: .milliseconds(50)) }
        check(livePane.detail?.session.id == "R" && livePane.detailStale && livePane.detailRefreshFailed, "a failed refresh keeps the last content and shows that it could not refresh")
        live.closePane(livePane)
        flaky.stop()

        let offline = AgenthailModel(connecting: false, api: AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:9")!, token: "t", session: URLSession(configuration: .ephemeral)))
        let offlinePane = offline.openPane()
        offline.operationError = "An action failed."
        offlinePane.select("unreachable")
        for _ in 0..<40 where offlinePane.detailLoadError == nil { try? await Task.sleep(for: .milliseconds(50)) }
        check(offlinePane.detailLoadError != nil && offlinePane.detail == nil, "a failed first load is shown on the pane while it retries")
        check(offline.operationError == "An action failed.", "a failed session load leaves another operation's error alone")
        offline.closePane(offlinePane)
        let picture = URL(fileURLWithPath: "/Volumes/shared/a picture.png")
        let sent = await withCheckedContinuation { done in
            offline.send(" look at this ", attachments: [picture], to: "X") { done.resume(returning: $0) }
        }
        check(!sent && offline.draft(for: "X").text == "look at this" && offline.draft(for: "X").attachments == [picture], "a failed send returns the text and the attachments to the draft")
        let onlyFile = await withCheckedContinuation { done in
            offline.send("", attachments: [picture], to: "Y") { done.resume(returning: $0) }
        }
        check(!onlyFile && offline.draft(for: "Y").text.isEmpty && offline.draft(for: "Y").attachments == [picture], "a failed attachment-only send keeps the attachment")
        let catalog = AgenthailModel(connecting: false)
        let codexSurface = try! JSONDecoder().decode(SurfaceState.self, from: Data(#"{"name":"codex","connected":true,"health":"healthy","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}}"#.utf8))
        catalog.snapshot = DashboardSnapshot(updatedAt: "2026-10-04T12:00:00Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [codexSurface], sessions: [], totalSessions: 0, queue: [], channels: [], relays: [], history: [], attention: [], deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "queue")
        let healthEvent = try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":2,"type":"surface.health","data":{"surface":"codex","health":"degraded","detail":"bridge closed","runtime":{"name":"Codex Desktop bridge","reachable":false,"durable":false,"problem":"bridge-unavailable","detail":"bridge closed","remediation":"run the launch command"}}}"#.utf8))
        catalog.applyCatalogEvent(healthEvent)
        check(catalog.snapshot?.surfaces.first?.runtime?.advice.isEmpty == false && catalog.snapshot?.surfaces.first?.health == "degraded", "a streamed surface health change keeps its runtime advice")
        let disconnected = AgenthailModel(connecting: false)
        var rejected: Bool?
        disconnected.send("look", attachments: [picture], to: "Z") { rejected = !$0 }
        check(rejected == true && disconnected.draft(for: "Z").text == "look" && disconnected.draft(for: "Z").attachments == [picture], "a send with no connection keeps the draft")

        let stalledServer = try! StubServer { _, _ in "HTTP/1.1 409 Conflict\r\nContent-Type: application/json\r\nContent-Length: 200\r\n\r\n{\"error\":{\"code\":\"stream_un" }
        let port = await stalledServer.ready()
        let stalledAPI = AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(port)")!, token: "t", session: URLSession(configuration: .ephemeral))
        let started = Date()
        do {
            try await stalledAPI.streamSession(id: "S", after: 0, onConnected: {}, onEvent: { _ in })
            check(false, "a stalled conflict ends the stream with an error")
        } catch AgenthailAPIError.request(409, _) {
        } catch {
            check(false, "a stalled conflict body is reported as a plain conflict, got \(error)")
        }
        check(Date().timeIntervalSince(started) < 5, "a stalled conflict body stops waiting within a few seconds")
        stalledServer.stop()

        func item(_ seq: Int) -> String {
            "id: \(seq)\nevent: item\ndata: {\"stream\":\"session\",\"sessionId\":\"S\",\"seq\":\(seq),\"type\":\"item\",\"data\":{\"itemId\":\"i\(seq)\",\"version\":1,\"op\":\"upsert\",\"kind\":\"message\",\"ts\":\"2026-10-04T12:00:00Z\",\"truncated\":false}}\n\n"
        }
        let openServer = try! StubServer { _, _ in "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n" + item(1) + item(2) }
        let openPort = await openServer.ready()
        let openAPI = AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(openPort)")!, token: "t", session: URLSession(configuration: .ephemeral))
        let received = ReceivedSequences()
        let streaming = Task { try? await openAPI.streamSession(id: "S", after: 0, onConnected: {}, onEvent: { await received.add($0.seq) }) }
        let deadline = Date().addingTimeInterval(5)
        while await received.values.count < 2, Date() < deadline { try? await Task.sleep(for: .milliseconds(20)) }
        check(await received.values == [1, 2], "events on a stream that stays open arrive as they are sent")
        streaming.cancel()
        openServer.stop()

        let delivered = await model.reply("  answer from a notification  ", to: "D", connectionTimeout: .milliseconds(50))
        check(!delivered && model.draft(for: "D").text == "answer from a notification", "an undeliverable reply waits in the session's draft")
    }

    actor ReceivedSequences {
        private(set) var values: [UInt64] = []
        func add(_ value: UInt64) { values.append(value) }
    }

    final class StubServer: @unchecked Sendable {
        private let listener: NWListener
        private var connections: [NWConnection] = []
        private var requests = 0

        init(respond: @escaping (String, Int) -> String?) throws {
            listener = try NWListener(using: .tcp, on: .any)
            listener.newConnectionHandler = { [self] connection in
                connections.append(connection)
                connection.start(queue: .main)
                connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [self] data, _, _, _ in
                    let line = data.flatMap { String(data: $0, encoding: .utf8) }?.components(separatedBy: "\r\n").first ?? ""
                    requests += 1
                    guard let reply = respond(line, requests) else { return }
                    connection.send(content: Data(reply.utf8), completion: .contentProcessed { _ in })
                }
            }
        }

        static func reply(_ status: String, _ body: String) -> String {
            "HTTP/1.1 \(status)\r\nContent-Type: application/json\r\nContent-Length: \(body.utf8.count)\r\nConnection: close\r\n\r\n\(body)"
        }

        func ready() async -> UInt16 {
            await withCheckedContinuation { continuation in
                listener.stateUpdateHandler = { [listener] state in
                    if case .ready = state { continuation.resume(returning: listener.port!.rawValue) }
                }
                listener.start(queue: .main)
            }
        }

        func stop() {
            connections.forEach { $0.cancel() }
            listener.cancel()
        }
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
