@testable import AgenthailHelpers
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

        UserDefaults.standard.removeObject(forKey: "sessionTabs")
        let tabbedModel = AgenthailModel(connecting: false)
        let tabbed = tabbedModel.mainPane!
        tabbed.select("A", keep: true)
        tabbed.openNewSession()
        let new = SessionTabs.newSession
        check(tabbed.tabs.tabs.map(\.id) == ["A", new] && tabbed.tabs.active == new && tabbed.selectedSessionID == nil && tabbed.newSession != nil, "New Session opens its own tab and clears the selection")
        tabbed.newSession?.form.message = "half written"
        tabbed.activateTab("A")
        check(tabbed.selectedSessionID == "A" && tabbed.newSession?.form.message == "half written", "switching away keeps the New Session draft")
        tabbed.openNewSession()
        check(tabbed.tabs.tabs.count == 2 && tabbed.selectedSessionID == nil, "New Session reuses its open tab")
        tabbed.stepTab(-1)
        tabbed.stepTab(1)
        check(tabbed.selectedSessionID == nil && tabbed.tabs.active == new, "stepping onto the New Session tab shows it")
        tabbed.showStarted("S")
        check(tabbed.tabs.tabs.map(\.id) == ["A", "S"] && tabbed.tabs.active == "S" && tabbed.selectedSessionID == "S" && tabbed.newSession == nil, "a started session takes over the New Session tab")
        tabbed.closeTab("A")
        tabbed.closeTab("S")
        check(tabbed.tabs.tabs.map(\.id) == [new] && tabbed.selectedSessionID == nil && tabbed.newSession?.form.message == "", "closing the last tab leaves a fresh New Session")
        UserDefaults.standard.removeObject(forKey: "sessionTabs")

        let plain = model.openPane()
        plain.select("A")
        plain.openNewSession()
        check(plain.selectedSessionID == nil && plain.newSession != nil && plain.tabs.tabs.isEmpty, "without tabs New Session replaces the selection")
        plain.select("B")
        check(plain.newSession == nil && plain.selectedSessionID == "B", "choosing a session leaves New Session")

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
        watched.composerDraft.attachments = [ComposerAttachment(name: "shot.png", data: Data("synthetic".utf8), mediaType: "image/png")]
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

        let journaledJSON = detailJSON.replacingOccurrences(of: #""readOnly":false"#, with: #""journalSeq":100,"readOnly":false"#)
        var journaledRequests: [String] = []
        let journaled = try! StubServer { line, _ in
            journaledRequests.append(line)
            if line.contains("/api/v1/session-stream?") { return "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n" }
            if line.contains("/api/v1/session?") { return StubServer.reply("200 OK", journaledJSON) }
            return StubServer.reply("404 Not Found", "{}")
        }
        let journaledPort = await journaled.ready()
        let journaledModel = AgenthailModel(connecting: false, api: AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(journaledPort)")!, token: "t", session: URLSession(configuration: .ephemeral)))
        let journaledPane = journaledModel.openPane()
        journaledPane.select("R")
        for _ in 0..<60 where !journaledRequests.contains(where: { $0.contains("/api/v1/session-stream?") }) { try? await Task.sleep(for: .milliseconds(50)) }
        let streamIndex = journaledRequests.firstIndex { $0.contains("/api/v1/session-stream?") }
        let detailIndex = journaledRequests.firstIndex { $0.contains("/api/v1/session?") }
        check(streamIndex != nil && journaledRequests[streamIndex!].contains("after=100"), "the live stream resumes after the loaded journal position: \(journaledRequests)")
        check(detailIndex != nil && detailIndex! < streamIndex!, "the live stream opens after the first load")
        journaledModel.closePane(journaledPane)
        journaled.stop()

        var lateStreamRequests: [String] = []
        let lateStream = try! StubServer { line, _ in
            if line.contains("/api/v1/session-stream?") {
                lateStreamRequests.append(line)
                if lateStreamRequests.count == 1 {
                    return StubServer.reply("409 Conflict", #"{"error":{"code":"stream_unsupported","message":"This session does not expose a live stream."}}"#)
                }
                return "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n"
            }
            if line.contains("/api/v1/session?") { return StubServer.reply("200 OK", journaledJSON) }
            return StubServer.reply("404 Not Found", "{}")
        }
        let lateStreamPort = await lateStream.ready()
        let lateStreamModel = AgenthailModel(connecting: false, api: AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(lateStreamPort)")!, token: "t", session: URLSession(configuration: .ephemeral)))
        func newSession(stream: Bool) -> SessionState {
            var capabilities = Capabilities()
            capabilities.stream = stream
            return SessionState(id: "R", surface: "claude", name: "new", alias: nil, status: "busy", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: capabilities, readOnly: nil, readOnlyReason: nil)
        }
        lateStreamModel.snapshot = DashboardSnapshot(updatedAt: "2026-10-22T08:28:26Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: [newSession(stream: false)], totalSessions: 1, queue: [], channels: [], relays: [], history: [], attention: [], deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "queue")
        let lateStreamPane = lateStreamModel.mainPane!
        lateStreamPane.select("R")
        for _ in 0..<60 where lateStreamRequests.isEmpty { try? await Task.sleep(for: .milliseconds(50)) }
        try? await Task.sleep(for: .milliseconds(300))
        check(lateStreamRequests.count == 1, "a session without a stream yet is asked once: \(lateStreamRequests)")
        let gainedStream = String(decoding: try! JSONEncoder().encode(newSession(stream: true)), as: UTF8.self)
        let upserted = "{\"stream\":\"catalog\",\"seq\":2,\"type\":\"session.upserted\",\"data\":{\"session\":\(gainedStream)}}"
        lateStreamModel.applyCatalogEvent(try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(upserted.utf8)))
        for _ in 0..<60 where lateStreamRequests.count < 2 { try? await Task.sleep(for: .milliseconds(50)) }
        check(lateStreamRequests.count >= 2 && lateStreamRequests[1].contains("after=100"), "the live stream starts once a new session gains it, from the reloaded journal position: \(lateStreamRequests)")
        lateStreamPane.close()
        lateStream.stop()
        func page(_ range: ClosedRange<Int>, before: Int) -> String {
            let items = range.map { #"{"id":"i\#($0)","kind":"message","role":"assistant","title":"assistant","text":"row \#($0)","truncated":false}"# }.joined(separator: ",")
            return detailJSON.replacingOccurrences(of: #""timeline":{"items":[],"nextBefore":null,"truncated":false}"#, with: #""timeline":{"items":["# + items + #"],"nextBefore":\#(before),"truncated":false}"#)
        }
        var pages = [page(1...20, before: 50), page(2...21, before: 51), page(40...59, before: 70)]
        let paging = try! StubServer { line, _ in
            guard line.contains("/api/v1/session?") else { return StubServer.reply("404 Not Found", "{}") }
            return StubServer.reply("200 OK", pages.count > 1 ? pages.removeFirst() : pages[0])
        }
        let pagingPort = await paging.ready()
        let pagingModel = AgenthailModel(connecting: false, api: AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(pagingPort)")!, token: "t", session: URLSession(configuration: .ephemeral)))
        let pagingPane = pagingModel.openPane()
        pagingPane.select("R")
        for _ in 0..<60 where pagingPane.detail == nil { try? await Task.sleep(for: .milliseconds(50)) }
        await pagingPane.loadSession("R")
        check(pagingPane.timelineItems.map(\.id) == (1...21).map { "i\($0)" } && pagingPane.olderCursor == 50, "a refreshed page keeps the rows that fell off its start, in order, with the earlier cursor: \(pagingPane.timelineItems.map(\.id)) \(String(describing: pagingPane.olderCursor))")
        await pagingPane.loadSession("R")
        check(pagingPane.timelineItems.map(\.id) == (40...59).map { "i\($0)" } && pagingPane.olderCursor == 70, "a page that does not overlap the shown rows replaces them rather than joining across a gap: \(pagingPane.timelineItems.map(\.id))")
        pagingModel.closePane(pagingPane)
        paging.stop()

        let offline = AgenthailModel(connecting: false, api: AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:9")!, token: "t", session: URLSession(configuration: .ephemeral)))
        let offlinePane = offline.openPane()
        offline.operationError = "An action failed."
        offlinePane.select("unreachable")
        for _ in 0..<40 where offlinePane.detailLoadError == nil { try? await Task.sleep(for: .milliseconds(50)) }
        check(offlinePane.detailLoadError != nil && offlinePane.detail == nil, "a failed first load is shown on the pane while it retries")
        check(offline.operationError == "An action failed.", "a failed session load leaves another operation's error alone")
        offline.closePane(offlinePane)
        let picture = ComposerAttachment(name: "a picture.png", data: Data("synthetic picture".utf8), mediaType: "image/png")
        let sent = await withCheckedContinuation { done in
            offline.send(" look at this ", attachments: [picture], to: "X") { done.resume(returning: $0) }
        }
        let failed = offline.localSends["X"]?.first
        check(!sent && failed?.failure != nil && failed?.text == "look at this" && failed?.attachments == [picture], "a failed send stays in the transcript with its attachments, ready to retry")
        check(offline.draft(for: "X").text.isEmpty && offline.draft(for: "X").attachments.isEmpty, "a failed send is not also restored to the composer")
        let catalog = AgenthailModel(connecting: false)
        let codexSurface = try! JSONDecoder().decode(SurfaceState.self, from: Data(#"{"name":"codex","connected":true,"health":"healthy","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}}"#.utf8))
        catalog.snapshot = DashboardSnapshot(updatedAt: "2026-10-04T12:00:00Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [codexSurface], sessions: [], totalSessions: 0, queue: [], channels: [], relays: [], history: [], attention: [], deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "queue")
        let healthEvent = try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":2,"type":"surface.health","data":{"surface":"codex","health":"degraded","detail":"bridge closed","runtime":{"name":"Codex Desktop bridge","reachable":false,"durable":false,"problem":"bridge-unavailable","detail":"bridge closed","remediation":"run the launch command"}}}"#.utf8))
        catalog.applyCatalogEvent(healthEvent)
        check(catalog.snapshot?.surfaces.first?.runtime?.advice.isEmpty == false && catalog.snapshot?.surfaces.first?.health == "degraded", "a streamed surface health change keeps its runtime advice")
        let rekeyed = AgenthailModel(connecting: false)
        let retired = SessionState(id: "session_old", surface: "claude", name: "worker", alias: "worker", status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        rekeyed.snapshot = DashboardSnapshot(updatedAt: "2026-10-18T12:00:00Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: [retired], totalSessions: 1, queue: [], channels: [], relays: [], history: [], attention: [], deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "queue")
        let following = rekeyed.openPane()
        following.select("session_old")
        rekeyed.draft(for: "session_old").text = "unsent"
        rekeyed.applyCatalogEvent(try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":2,"type":"session.removed","data":{"sessionId":"session_old","surface":"claude","reason":"rekeyed","rekeyedTo":"conversation-a"}}"#.utf8)))
        check(following.selectedSessionID == "conversation-a" && following.removedSession == nil, "a pane on a rekeyed session follows it to its conversation id")
        check(rekeyed.draft(for: "conversation-a").text == "unsent", "a rekeyed session keeps its draft")
        let restored = rekeyed.openPane()
        restored.select("session_old")
        check(restored.selectedSessionID == "conversation-a", "a window restored with a retired id opens the conversation")
        let lateReply = await rekeyed.reply("late answer", to: "session_old", connectionTimeout: .milliseconds(50))
        check(!lateReply && rekeyed.draft(for: "conversation-a").text.contains("late answer"), "a notification reply to a retired id goes to the conversation")
        let cold = AgenthailModel(connecting: false)
        cold.openSession(reference: "session_old")
        let coldReply = Task { await cold.reply("cold answer", to: "session_old", connectionTimeout: .seconds(5)) }
        try? await Task.sleep(for: .milliseconds(50))
        let conversation = SessionState(id: "conversation-a", surface: "claude", name: "worker", alias: "worker", status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        var hydrated = DashboardSnapshot(updatedAt: "2026-10-18T12:00:00Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: [conversation], totalSessions: 1, queue: [], channels: [], relays: [], history: [], attention: [], deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "queue")
        hydrated.rekeyed = ["session_old": "conversation-a"]
        cold.adopt(hydrated)
        check(cold.mainPane.selectedSessionID == "conversation-a", "a notification opened before the first snapshot opens the rekeyed conversation")
        let coldDelivered = await coldReply.value
        check(!coldDelivered && cold.draft(for: "conversation-a").text.contains("cold answer") && cold.draft(for: "session_old").text.isEmpty, "a reply sent before the first snapshot resolves the rekey once it loads")
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
        let streaming = Task {
            try? await openAPI.streamSession(id: "S", after: 0, onConnected: {}, onEvent: { await received.add($0.seq) })
            await received.finish()
        }
        let deadline = Date().addingTimeInterval(5)
        while await received.values.count < 2, Date() < deadline { try? await Task.sleep(for: .milliseconds(20)) }
        check(await received.values == [1, 2], "events on a stream that stays open arrive as they are sent")
        streaming.cancel()
        let cancelDeadline = Date().addingTimeInterval(3)
        while await !received.finished, Date() < cancelDeadline { try? await Task.sleep(for: .milliseconds(20)) }
        check(await received.finished, "cancelling a silent open stream ends it")
        openServer.stop()

        var actions: [String] = []
        var failUnarchive = false
        let archive = try! StubServer { request, _ in
            guard request.contains("session-unarchive") else { return StubServer.reply("200 OK", "{}") }
            actions.append(request)
            return failUnarchive ? StubServer.reply("500 Internal Server Error", #"{"error":{"message":"unarchive failed"}}"#) : StubServer.reply("200 OK", "{}")
        }
        let archivePort = await archive.ready()
        let archiveModel = AgenthailModel(connecting: false, api: AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(archivePort)")!, token: "t", session: URLSession(configuration: .ephemeral)))
        let archivePane = archiveModel.openPane()
        let noticeA = SessionState(id: "NA", surface: "codex", name: "a", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: false, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        let noticeB = SessionState(id: "NB", surface: "codex", name: "b", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: false, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        archivePane.archivedTab = noticeA
        archivePane.archivedTab = noticeB
        await archivePane.unarchiveTab(noticeA)
        check(actions.count == 1 && actions[0].contains("\"NA\"") && !actions[0].contains("\"NB\""), "Unarchive acts on the notice that was clicked: \(actions)")
        check(archivePane.archivedTab?.id == "NB" && archivePane.archivedTabError == nil, "a newer notice stays after the older one is unarchived")
        failUnarchive = true
        await archivePane.unarchiveTab(noticeB)
        check(archivePane.archivedTab?.id == "NB" && archivePane.archivedTabError?.contains("unarchive failed") == true, "a failed unarchive keeps the notice with the reason: \(String(describing: archivePane.archivedTabError))")
        archivePane.archivedTab = nil
        await archivePane.unarchiveTab(noticeA)
        check(archivePane.archivedTab?.id == "NA" && archivePane.archivedTabError != nil, "a failure after dismissal brings the notice back with the reason")
        archiveModel.closePane(archivePane)
        archive.stop()
        let unreachable = model.openPane()
        unreachable.archivedTab = noticeA
        await unreachable.unarchiveTab(noticeA)
        check(unreachable.archivedTab?.id == "NA" && unreachable.archivedTabError?.contains("connected") == true, "without a connection the notice stays and says why")

        let restoredTabs = { () -> SessionTabs in
            var tabs = SessionTabs()
            tabs.open("A", keep: true)
            tabs.open(SessionTabs.newSession, keep: true)
            return tabs
        }()
        UserDefaults.standard.set(try! JSONEncoder().encode(restoredTabs), forKey: "sessionTabs")
        let restoredModel = AgenthailModel(connecting: false)
        check(restoredModel.mainPane.tabs.active == SessionTabs.newSession && restoredModel.mainPane.newSession != nil, "a restored New Session tab has its draft before any snapshot")
        UserDefaults.standard.removeObject(forKey: "sessionTabs")

        let offlineModel = AgenthailModel(connecting: false)
        offlineModel.openNewSession(message: "while offline")
        let offlineDraft = offlineModel.mainPane.newSession
        offlineDraft?.form.folder = "/work/sample"
        check(offlineDraft?.form.message == "while offline" && offlineDraft?.canStart == false, "New Session opens while disconnected, and Start waits for the connection")
        if let offlineDraft {
            offlineDraft.hosts.receive(options: try! JSONDecoder().decode(SessionCreationOptions.self, from: Data(#"{"surfaces":[{"id":"claude","workspace":true}],"projects":[]}"#.utf8)), for: offlineDraft.hosts.generation)
            await offlineDraft.loadModels()
            check(offlineDraft.modelError == nil && offlineDraft.hosts.models["claude"] == nil, "models wait for the connection instead of failing, so reconnecting loads them")
        }
        UserDefaults.standard.removeObject(forKey: "sessionTabs")

        let launch = try! StubServer { request, _ in
            request.contains("\"surface\"") && request.contains("/api/v1/actions") ? nil : StubServer.reply("200 OK", "{}")
        }
        let launchPort = await launch.ready()
        let launchModel = AgenthailModel(connecting: false, api: AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(launchPort)")!, token: "t", session: URLSession(configuration: .ephemeral)))
        launchModel.snapshot = DashboardSnapshot(updatedAt: "2026-10-22T12:00:00Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: [], totalSessions: 0, queue: [], channels: [], relays: [], history: [], attention: [], deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "queue")
        launchModel.openNewSession()
        let launching = launchModel.mainPane.newSession!
        launching.hosts.receive(options: try! JSONDecoder().decode(SessionCreationOptions.self, from: Data(#"{"surfaces":[{"id":"claude","workspace":true}],"projects":[]}"#.utf8)), for: launching.hosts.generation)
        launching.form.folder = "/work/sample"
        launching.form.message = "first"
        check(launching.canStart, "a complete form can start: \(launching.form)")
        launching.start()
        launchModel.receiveSharedText("shared while starting")
        check(launching.starting && launching.form.message == "first", "text shared while starting waits")
        for _ in 0..<60 where launch.heldCount == 0 { try? await Task.sleep(for: .milliseconds(50)) }
        launch.release(StubServer.reply("200 OK", #"{"ok":true,"accepted":true}"#))
        for _ in 0..<60 where launching.submitted == nil { try? await Task.sleep(for: .milliseconds(50)) }
        launchModel.receiveSharedText("shared after submitting")
        check(launching.submitted != nil && launching.form.message == "first", "text shared after submitting waits: \(launching.form.message)")
        launching.reset()
        check(launching.form.message == "shared while starting\n\nshared after submitting", "held text arrives once the draft is editable, each piece once: \(launching.form.message)")
        launch.stop()
        UserDefaults.standard.removeObject(forKey: "sessionTabs")

        let delivered = await model.reply("  answer from a notification  ", to: "D", connectionTimeout: .milliseconds(50))
        check(!delivered && model.draft(for: "D").text == "answer from a notification", "an undeliverable reply waits in the session's draft")
    }

    actor ReceivedSequences {
        private(set) var values: [UInt64] = []
        private(set) var finished = false
        func add(_ value: UInt64) { values.append(value) }
        func finish() { finished = true }
    }

    final class StubServer: @unchecked Sendable {
        private let listener: NWListener
        private var connections: [NWConnection] = []
        private var held: [NWConnection] = []
        private var requests = 0

        init(respond: @escaping (String, Int) -> String?) throws {
            listener = try NWListener(using: .tcp, on: .any)
            listener.newConnectionHandler = { [self] connection in
                connections.append(connection)
                connection.start(queue: .main)
                read(connection, Data()) { [self] request in
                    requests += 1
                    guard let reply = respond(request, requests) else {
                        held.append(connection)
                        return
                    }
                    connection.send(content: Data(reply.utf8), completion: .contentProcessed { _ in })
                }
            }
        }

        /// Reads one request: the headers, then as much body as they declare.
        private func read(_ connection: NWConnection, _ received: Data, then handle: @escaping (String) -> Void) {
            connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [self] data, _, complete, error in
                let all = received + (data ?? Data())
                let text = String(decoding: all, as: UTF8.self)
                if let end = text.range(of: "\r\n\r\n") {
                    let length = text[..<end.lowerBound].components(separatedBy: "\r\n")
                        .first { $0.lowercased().hasPrefix("content-length:") }
                        .flatMap { Int($0.split(separator: ":")[1].trimmingCharacters(in: .whitespaces)) } ?? 0
                    if text[end.upperBound...].utf8.count >= length || complete || error != nil { return handle(text) }
                } else if complete || error != nil {
                    return handle(text)
                }
                read(connection, all, then: handle)
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

        var heldCount: Int { held.count }

        /// Answers every request the responder left waiting.
        func release(_ reply: String) {
            held.forEach { $0.send(content: Data(reply.utf8), completion: .contentProcessed { _ in }) }
            held = []
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
