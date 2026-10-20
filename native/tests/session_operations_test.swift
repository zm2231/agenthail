@testable import AgenthailHelpers
import Foundation

final class StubProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var bodies: [[String: Any]] = []
    nonisolated(unsafe) static var keys: [String?] = []
    nonisolated(unsafe) static var replies: [(Int, String)] = []
    nonisolated(unsafe) static var receipts: [String: (body: NSDictionary, reply: (Int, String))] = [:]
    nonisolated(unsafe) static var executed = 0

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var data = request.httpBody ?? Data()
        if data.isEmpty, let stream = request.httpBodyStream {
            stream.open()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let count = stream.read(&buffer, maxLength: buffer.count)
                if count <= 0 { break }
                data.append(buffer, count: count)
            }
            stream.close()
        }
        let action = request.httpMethod == "POST" && request.url?.path == "/api/v1/actions"
        let key = request.value(forHTTPHeaderField: "Idempotency-Key")
        let sent = (try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:]
        if action {
            Self.keys.append(key)
            Self.bodies.append(sent)
        }
        var (status, body) = (200, "{}")
        if action, let key, let receipt = Self.receipts[key] {
            (status, body) = receipt.body.isEqual(to: sent) ? receipt.reply : (409, #"{"error":{"code":"action_idempotency_mismatch","message":"The Idempotency-Key was already used for a different action request."}}"#)
        } else if action, !Self.replies.isEmpty {
            (status, body) = Self.replies.removeFirst()
            if status >= 0 || !body.isEmpty { Self.executed += 1 }
            if let key, status != 202 {
                if status >= 0 {
                    Self.receipts[key] = (sent as NSDictionary, (status, body))
                } else if !body.isEmpty {
                    Self.receipts[key] = (sent as NSDictionary, (200, body))
                }
            }
        }
        if status < 0 {
            client?.urlProtocol(self, didFailWithError: URLError(URLError.Code(rawValue: status)))
            return
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@main
struct SessionOperationsTest {
    @MainActor
    static func main() async {
        checkSlashCommands()
        checkQueueOrder()
        checkLifecycleAvailability()
        await checkRequests()
        await checkNativeQueueController()
        await checkBackgroundController()
        await checkFork()
        await checkRetryIdentity()
        print("session operations tests passed")
    }

    static func checkSlashCommands() {
        var capabilities = Capabilities()
        check(SlashCommand.available(capabilities: capabilities, working: false, waitingOnYou: false) == [.name], "only /name is offered without capabilities")
        capabilities.compact = true
        capabilities.model = true
        capabilities.goal = true
        check(!SlashCommand.available(capabilities: capabilities, working: false, waitingOnYou: false).contains(.goal), "/goal needs a session whose goal is set here")
        capabilities.goalSet = true
        capabilities.interrupt = true
        capabilities.steer = true
        check(SlashCommand.available(capabilities: capabilities, working: false, waitingOnYou: false) == [.name, .compact, .model, .goal], "stop and steer need a running turn")
        let all = SlashCommand.available(capabilities: capabilities, working: true, waitingOnYou: false)
        check(all == SlashCommand.allCases, "a running capable session offers every command")
        check(SlashCommand.available(capabilities: capabilities, working: true, waitingOnYou: true) == [.name, .compact, .stop, .model, .goal], "a turn waiting on its user offers no steer")

        check(SlashCommand.suggestions(for: "/st", available: all) == [.stop, .steer], "suggestions filter by prefix")
        check(SlashCommand.suggestions(for: "  /CO", available: all) == [.compact], "suggestions ignore leading space and case")
        check(SlashCommand.suggestions(for: "/steer now", available: all).isEmpty, "an argument closes the suggestions")
        check(SlashCommand.suggestions(for: "hello", available: all).isEmpty, "plain text has no suggestions")
        check(SlashCommand.modelQuery(for: "/model gpt") == "gpt" && SlashCommand.modelQuery(for: "/model ") == "", "model query follows /model")
        check(SlashCommand.modelQuery(for: "/modelx") == nil, "model query needs the separator")

        check(SlashInput("/compact", available: all) == .command(.compact, argument: ""), "a bare command parses")
        check(SlashInput("/Steer  go left ", available: all) == .command(.steer, argument: "go left"), "an argument is trimmed and case is ignored")
        check(SlashInput("/goal", available: all) == .missingArgument(.goal), "a value command without a value is rejected")
        check(SlashInput("/goal", available: all).problem == "/goal needs a value.", "the missing value is explained")
        check(SlashInput("/stop", available: [.name]) == .unavailable(.stop), "an unavailable command is not sent as text")
        check(SlashInput("/unknown thing", available: all) == .message, "unknown commands send as a message")
        check(SlashInput("see /compact", available: all) == .message, "a command mid-message is plain text")
    }

    static func lifecycleSession(_ runtime: String?, lifecycle: String?, readOnly: Bool = false) -> SessionState? {
        let capabilities = #"{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false"# + (lifecycle.map { #","lifecycle":"# + $0 } ?? "") + "}"
        let runtimeField = runtime.map { #","runtimeKind":"\#($0)""# } ?? ""
        let json = #"{"id":"S","surface":"claude","name":"n","status":"idle","queueCount":0,"open":true,"current":true,"readOnly":\#(readOnly),"capabilities":\#(capabilities)\#(runtimeField)}"#
        return try? JSONDecoder().decode(SessionState.self, from: Data(json.utf8))
    }

    static func checkLifecycleAvailability() {
        let background = lifecycleSession("claude-bg", lifecycle: #"{"status":true,"logs":true,"stop":true,"resume":true}"#)
        check(background.map(SessionOperationAvailability.lifecycle) == SessionLifecycle(status: true, logs: true, stop: true, resume: true), "a background job offers all four operations")
        check(background.map(SessionLifecycleCopy.title) == "Background session", "a background job keeps its label")
        let hosted = lifecycleSession("claude-host", lifecycle: #"{"status":true,"logs":false,"stop":true,"resume":true}"#)
        check(hosted.map(SessionOperationAvailability.lifecycle) == SessionLifecycle(status: true, stop: true, resume: true), "an operation whose flag is off is not offered")
        check(hosted.map(SessionLifecycleCopy.title) == "Session", "a host session is not labelled a background session")
        let interactive = lifecycleSession("claude-cli", lifecycle: #"{"status":false,"logs":false,"stop":false,"resume":false}"#)
        check(interactive.map { SessionOperationAvailability.lifecycle($0).any } == false, "an interactive session offers no lifecycle operation")
        let older = lifecycleSession(nil, lifecycle: nil)
        check(older.map { SessionOperationAvailability.lifecycle($0).any } == false, "a host that reports no lifecycle offers none")
        let extended = lifecycleSession("claude-bg", lifecycle: #"{"status":true,"adopt":true}"#)
        check(extended.map(SessionOperationAvailability.lifecycle) == SessionLifecycle(status: true), "unknown lifecycle operations are ignored and missing ones are off")
        let readOnly = lifecycleSession("claude-bg", lifecycle: #"{"status":true,"logs":true,"stop":true,"resume":true}"#, readOnly: true)
        check(readOnly.map { SessionOperationAvailability.lifecycle($0).any } == false, "a read-only session offers no lifecycle operation")
    }

    static func checkQueueOrder() {
        check(NativeQueueOrder.moving(["a", "b", "c"], id: "b", by: -1) == ["b", "a", "c"], "move up swaps with the previous item")
        check(NativeQueueOrder.moving(["a", "b", "c"], id: "b", by: 1) == ["a", "c", "b"], "move down swaps with the next item")
        check(NativeQueueOrder.moving(["a", "b"], id: "a", by: -1) == nil, "the first item cannot move up")
        check(NativeQueueOrder.moving(["a", "b"], id: "z", by: 1) == nil, "an unknown item cannot move")
    }

    static func api() -> AgenthailAPI {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StubProtocol.self]
        return AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
    }

    static func reset(_ replies: [(Int, String)]) {
        StubProtocol.bodies = []
        StubProtocol.keys = []
        StubProtocol.replies = replies
        StubProtocol.receipts = [:]
        StubProtocol.executed = 0
    }

    @MainActor
    static func checkRequests() async {
        let api = api()
        let keys = ActionRetryKeys()
        reset([(200, #"{"ok":true,"result":{"session":{"id":"fork-1","surface":"codex"}}}"#)])
        let forked = try? await api.forkSession(id: "S", keys: keys)
        check(forked?.id == "fork-1", "fork returns the new session")
        check(StubProtocol.bodies.first?["action"] as? String == "session-fork" && StubProtocol.bodies.first?["sessionId"] as? String == "S", "fork names the action and source")
        check(StubProtocol.bodies.first?.keys.sorted() == ["action", "sessionId"], "fork sends only the action and source: \(String(describing: StubProtocol.bodies.first))")

        reset([(200, #"{"ok":true,"result":{"data":[{"id":"q1","clientUserMessageId":"c1","input":[{"type":"text","text":"first","text_elements":[]},{"type":"localImage","path":"/x.png"}]}],"nextCursor":"next"}}"#)])
        let page: NativeQueuePage? = try? await api.nativeQueue(sessionID: "S", NativeQueueCommand(queueAction: .list), keys: keys)
        check(page?.data.first?.text == "first\nImage" && page?.nextCursor == "next", "a queue page decodes Codex submissions")
        check(page?.data.first?.editableText == nil, "input with an image is not editable as text")
        let queue = StubProtocol.bodies.first?["nativeQueue"] as? [String: Any]
        check(StubProtocol.bodies.first?["action"] as? String == "native-queue" && queue?["queueAction"] as? String == "list" && queue?.count == 1, "list sends only its action")

        reset([(502, #"{"ok":false,"unknown":false,"error":"session is not a Claude background agent"}"#), (502, #"{"ok":false,"unknown":true,"error":"Claude stop: exit 1"}"#)])
        do {
            _ = try await api.sessionLifecycle(sessionID: "C", action: "status", keys: keys)
            check(false, "a failed operation throws")
        } catch {
            check(error.localizedDescription == "session is not a Claude background agent", "operation errors show the daemon's message")
        }
        do {
            _ = try await api.sessionLifecycle(sessionID: "C", action: "stop", keys: keys)
        } catch {
            check(error.localizedDescription.hasSuffix("The outcome is unknown; check the session before retrying."), "an unknown outcome is called out")
        }
        check(StubProtocol.bodies.last?["action"] as? String == "session-lifecycle-stop", "lifecycle actions use the session-lifecycle prefix")

        let settings = TurnSettings(effort: "high", mode: .plan)
        reset([(200, #"{"result":{"status":"delivered"}}"#), (200, #"{"result":{"status":"delivered"}}"#)])
        _ = try? await api.sendInstruction(action: "send", sessionID: "S", message: "hi", turnSettings: settings)
        _ = try? await api.sendInstruction(action: "steer", sessionID: "S", message: "hi", turnSettings: settings)
        let send = StubProtocol.bodies.first ?? [:]
        check(send["effort"] as? String == "high" && send["mode"] as? String == "plan" && send["serviceTier"] == nil && send["outputSchema"] == nil, "a send carries effort and mode only: \(send)")
        let steer = StubProtocol.bodies.last ?? [:]
        check(steer["effort"] == nil && steer["mode"] == nil, "a steer keeps the running turn's settings")
    }

    @MainActor
    static func checkNativeQueueController() async {
        let api = api()
        let controller = NativeQueueController(keys: ActionRetryKeys())
        controller.reset(sessionID: "S")
        let empty = #"{"ok":true,"result":{"data":[],"nextCursor":null}}"#
        let two = #"{"ok":true,"result":{"data":[{"id":"q1","clientUserMessageId":"c1","input":[{"type":"text","text":"one"}]},{"id":"q2","clientUserMessageId":"c2","input":[{"type":"text","text":"two"}]}],"nextCursor":null}}"#

        reset([(200, #"{"ok":true,"result":{"queuedSubmission":{"id":"q1","clientUserMessageId":"x","input":[]}}}"#), (200, empty), (200, #"{"ok":true,"result":{}}"#), (200, empty)])
        let added = await controller.add("next", api: api)
        check(added && controller.loaded && controller.error == nil, "a successful add reloads the queue")
        _ = await controller.add("next", api: api)
        let ids = StubProtocol.bodies.compactMap { ($0["nativeQueue"] as? [String: Any])?["clientUserMessageId"] as? String }
        check(ids.count == 2 && ids[0] != ids[1], "adding the same text after a confirmed add is new work")

        reset([(200, two), (200, #"{"ok":true,"result":{}}"#), (200, two)])
        await controller.reload(api)
        check(controller.items.map(\.id) == ["q1", "q2"] && controller.canReorder, "a complete queue can be reordered")
        await controller.move(controller.items[1], by: -1, api: api)
        let order = (StubProtocol.bodies[1]["nativeQueue"] as? [String: Any])?["queuedSubmissionIds"] as? [String]
        check(order == ["q2", "q1"], "reorder sends the whole new order")

        reset([(200, #"{"ok":true,"result":{"data":[{"id":"q1","clientUserMessageId":"c1","input":[]}],"nextCursor":"more"}}"#)])
        await controller.reload(api)
        check(!controller.canReorder, "a partially loaded queue cannot be reordered")

        reset([(200, #"{"ok":true,"result":{"turn":{"id":"t"}}}"#), (200, empty)])
        let started = await controller.start(nil, api: api)
        let start = StubProtocol.bodies.first?["nativeQueue"] as? [String: Any]
        check(started && start?["queueAction"] as? String == "start" && start?["queuedSubmissionId"] == nil, "start next lets Codex choose")

        controller.reset(sessionID: "T")
        check(controller.items.isEmpty && !controller.loaded && controller.sessionID == "T", "switching sessions clears the queue")
    }

    @MainActor
    static func checkBackgroundController() async {
        let api = api()
        let controller = BackgroundSessionController(keys: ActionRetryKeys())
        controller.reset(sessionID: "C")
        reset([(200, #"{"ok":true,"result":{"id":"bg1","sessionId":"C","action":"stop","output":"stopped"}}"#), (200, #"{"ok":true,"result":{"id":"bg1","sessionId":"C","state":"stopped"}}"#)])
        let stopped = await controller.run("stop", api: api)
        check(stopped && controller.status?.state == "stopped", "stop refreshes the background status")
        reset([(200, #"{"ok":true,"result":{"id":"bg1","sessionId":"C","action":"logs","output":"line one"}}"#)])
        _ = await controller.run("logs", api: api)
        check(controller.logs == "line one", "logs open with their output")
        reset([(200, #"{"ok":true,"result":{"id":"bg1","sessionId":"C","action":"resume","unchanged":true,"state":"working"}}"#), (200, #"{"ok":true,"result":{"id":"bg1","sessionId":"C","state":"working"}}"#)])
        _ = await controller.run("resume", api: api)
        check(controller.resumeUnchanged && controller.status?.state == "working", "resuming a running session says nothing changed")
        reset([(200, #"{"ok":true,"result":{"id":"bg1","sessionId":"C","state":"working"}}"#)])
        _ = await controller.run("status", api: api)
        check(!controller.resumeUnchanged, "the no-op note clears on the next action")
        controller.reset(sessionID: "D")
        check(controller.status == nil && controller.logs == nil && !controller.resumeUnchanged, "switching sessions clears background state")
    }

    static let unknownReply = (502, #"{"ok":false,"unknown":true,"error":"timed out"}"#)
    static let lostReply = (Int(URLError.networkConnectionLost.rawValue), "")
    static let refusedReply = (Int(URLError.cannotConnectToHost.rawValue), "")
    static let submittedReply = (202, #"{"ok":true,"status":"submitted"}"#)
    static let forkedReply = (200, #"{"ok":true,"result":{"session":{"id":"fork-1"}}}"#)
    static let okReply = (200, #"{"ok":true,"result":{}}"#)
    static let emptyQueue = (200, #"{"ok":true,"result":{"data":[],"nextCursor":null}}"#)

    @MainActor
    static func checkFork() async {
        let model = AgenthailModel(connecting: false, api: api())
        guard let pane = model.mainPane else { return check(false, "the model has a main pane") }
        let source = codexSession("S")

        reset([(502, #"{"ok":false,"unknown":false,"error":"thread not found"}"#), forkedReply])
        await pane.forkNow(source)
        check(model.operationError == "thread not found" && model.pendingForks.isEmpty && !pane.forking, "a definite failure shows its error")
        await pane.forkNow(source)
        check(StubProtocol.keys[0] != StubProtocol.keys[1] && StubProtocol.executed == 2, "a retry after a definite failure is a new fork")
        check(StubProtocol.bodies[1].keys.sorted() == ["action", "sessionId"], "a fork sends no options")

        reset([lostReply, forkedReply, forkedReply])
        await pane.forkNow(source)
        check(model.operationError == nil && model.pendingForks == ["S"], "a lost connection after dispatch shows the pending note")
        await pane.forkNow(source)
        check(StubProtocol.keys[0] == StubProtocol.keys[1] && model.pendingForks.isEmpty && model.operationError == nil, "a retry after a lost connection sends the same key and settles")
        await pane.forkNow(source)
        check(StubProtocol.keys[2] != StubProtocol.keys[1] && StubProtocol.executed == 2, "a fork after a confirmed one is new")

        reset([(Int(URLError.networkConnectionLost.rawValue), forkedReply.1)])
        await pane.forkNow(source)
        await pane.forkNow(source)
        check(StubProtocol.executed == 1 && model.pendingForks.isEmpty, "a retry after a lost reply replays the fork the daemon made")

        reset([submittedReply, forkedReply])
        await pane.forkNow(source)
        check(model.pendingForks == ["S"], "a submitted reply shows the pending note")
        await pane.forkNow(source)
        check(StubProtocol.keys[0] == StubProtocol.keys[1] && model.pendingForks.isEmpty, "checking again on a submitted fork uses its key")

        reset([unknownReply, forkedReply])
        await pane.forkNow(source)
        await pane.forkNow(source)
        check(StubProtocol.keys[0] == StubProtocol.keys[1] && StubProtocol.executed == 1 && model.pendingForks == ["S"], "retrying a recorded unknown fork replays it and keeps the note")
        reset([forkedReply])
        pane.forkAgain(source)
        await waitUntil { !pane.forking }
        check(StubProtocol.executed == 1 && model.pendingForks.isEmpty, "Fork again starts a new fork")

        reset([refusedReply, forkedReply])
        await pane.forkNow(source)
        check(model.operationError != nil && model.pendingForks.isEmpty, "a connection that never opened is a definite failure")
        await pane.forkNow(source)
        check(StubProtocol.keys[0] != StubProtocol.keys[1], "a retry after a refused connection sends a new key")

        reset([unknownReply, unknownReply])
        await pane.forkNow(source)
        await pane.forkNow(codexSession("T"))
        check(model.pendingForks == ["S", "T"], "each session keeps its own pending note")

        let readOnly = SessionState(id: "R", surface: "codex", name: "r", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: true, readOnlyReason: nil)
        reset([])
        pane.fork(readOnly)
        check(!pane.forking && StubProtocol.bodies.isEmpty, "a read-only session does not fork")
    }

    @MainActor
    static func checkRetryIdentity() async {
        let api = api()
        let keys = ActionRetryKeys()
        let queue = { () -> NativeQueueController in
            let controller = NativeQueueController(keys: keys)
            controller.reset(sessionID: "S")
            return controller
        }
        let mismatch: () -> Bool = { StubProtocol.receipts.isEmpty == false && StubProtocol.bodies.indices.contains { i in StubProtocol.keys[i] != nil && StubProtocol.receipts[StubProtocol.keys[i]!]?.body.isEqual(to: StubProtocol.bodies[i]) == false } }

        reset([unknownReply, emptyQueue, okReply, emptyQueue])
        _ = await queue().start(nil, api: api)
        _ = await queue().start(nil, api: api)
        check(StubProtocol.keys[0] == StubProtocol.keys[1] && StubProtocol.executed == 1, "retrying Start next after a recorded unknown replays it from a reopened section")
        let observed = queue()
        await observed.reload(api)
        let started = await observed.start(nil, api: api)
        check(started && StubProtocol.keys[3] != StubProtocol.keys[0] && StubProtocol.executed == 4, "after the queue is reloaded, Start next is new work")
        check(StubProtocol.bodies[3]["nativeQueue"].flatMap { ($0 as? [String: Any])?["queuedSubmissionId"] } == nil, "Start next still lets Codex choose")

        reset([submittedReply, emptyQueue, okReply, emptyQueue])
        _ = await queue().start(nil, api: api)
        await queue().reload(api)
        _ = await queue().start(nil, api: api)
        check(StubProtocol.keys[0] == StubProtocol.keys[2] && StubProtocol.executed == 4, "a reload does not settle a start the daemon is still running")

        reset([unknownReply, (200, #"{"ok":true,"result":{"queuedSubmission":{"id":"q1","input":[]}}}"#), emptyQueue])
        _ = await queue().add("same", api: api)
        _ = await queue().add("same", api: api)
        let ids = StubProtocol.bodies.compactMap { ($0["nativeQueue"] as? [String: Any])?["clientUserMessageId"] as? String }
        check(ids.count == 2 && ids[0] == ids[1] && StubProtocol.keys[0] == StubProtocol.keys[1] && ids[0] == StubProtocol.keys[0], "an uncertain add keeps its client message ID when the section reopens")
        check(StubProtocol.executed == 1 && !mismatch(), "the retried add replays without a key mismatch")

        reset([unknownReply, okReply, emptyQueue])
        let item = NativeQueuedSubmission(id: "q1", clientUserMessageId: nil, input: [])
        _ = await queue().update(item, text: "a", api: api)
        _ = await queue().update(item, text: "b", api: api)
        check(StubProtocol.keys[0] != StubProtocol.keys[1] && StubProtocol.executed == 3, "a different edit is a new operation")

        let background = { () -> BackgroundSessionController in
            let controller = BackgroundSessionController(keys: keys)
            controller.reset(sessionID: "C")
            return controller
        }
        let state = { (value: String) in (200, #"{"ok":true,"result":{"id":"bg1","sessionId":"C","state":"\#(value)"}}"#) }
        reset([lostReply, (200, #"{"ok":true,"result":{"id":"bg1","sessionId":"C","action":"resume"}}"#), state("working"), (200, #"{"ok":true,"result":{"id":"bg1","sessionId":"C","action":"stop"}}"#), state("stopped")])
        _ = await background().run("stop", api: api)
        _ = await background().run("resume", api: api)
        _ = await background().run("stop", api: api)
        check(StubProtocol.keys[0] != StubProtocol.keys[3] && StubProtocol.executed == 4, "a confirmed Resume settles an earlier uncertain Stop")

        let logs = (200, #"{"ok":true,"result":{"id":"bg1","sessionId":"C","action":"logs","output":"line"}}"#)
        for action in ["stop", "resume"] {
            reset([unknownReply, logs, state("working"), (200, #"{"ok":true,"result":{"id":"bg1","sessionId":"C","action":"\#(action)"}}"#), state("stopped")])
            _ = await background().run(action, api: api)
            _ = await background().run("logs", api: api)
            _ = await background().run(action, api: api)
            check(StubProtocol.keys[2] == StubProtocol.keys[0] && StubProtocol.executed == 2, "Logs leaves a recorded unknown \(action) in place")
            _ = await background().run("status", api: api)
            _ = await background().run(action, api: api)
            check(StubProtocol.keys[4] != StubProtocol.keys[0] && StubProtocol.keys[3] != StubProtocol.keys[0] && StubProtocol.keys[1] != StubProtocol.keys[0], "Status settles a recorded unknown \(action) and reads never reuse a key")
        }
        check(!mismatch(), "no retry reuses a key with a different body")
    }

    @MainActor
    static func waitUntil(_ condition: () -> Bool) async {
        for _ in 0..<200 where !condition() { try? await Task.sleep(nanoseconds: 5_000_000) }
    }

    static func codexSession(_ id: String) -> SessionState {
        SessionState(id: id, surface: "codex", name: id, alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
