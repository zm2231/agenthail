import Foundation

final class StubProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var bodies: [[String: Any]] = []
    nonisolated(unsafe) static var replies: [(Int, String)] = []

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
        Self.bodies.append((try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:])
        let (status, body) = Self.replies.isEmpty ? (200, "{}") : Self.replies.removeFirst()
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
        checkOutputSchema()
        await checkRequests()
        await checkNativeQueueController()
        await checkBackgroundController()
        print("session operations tests passed")
    }

    static func checkSlashCommands() {
        var capabilities = Capabilities()
        check(SlashCommand.available(capabilities: capabilities, working: false) == [.name], "only /name is offered without capabilities")
        capabilities.compact = true
        capabilities.model = true
        capabilities.goal = true
        capabilities.interrupt = true
        capabilities.steer = true
        check(SlashCommand.available(capabilities: capabilities, working: false) == [.name, .compact, .model, .goal], "stop and steer need a running turn")
        let all = SlashCommand.available(capabilities: capabilities, working: true)
        check(all == SlashCommand.allCases, "a running capable session offers every command")

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

    static func checkQueueOrder() {
        check(NativeQueueOrder.moving(["a", "b", "c"], id: "b", by: -1) == ["b", "a", "c"], "move up swaps with the previous item")
        check(NativeQueueOrder.moving(["a", "b", "c"], id: "b", by: 1) == ["a", "c", "b"], "move down swaps with the next item")
        check(NativeQueueOrder.moving(["a", "b"], id: "a", by: -1) == nil, "the first item cannot move up")
        check(NativeQueueOrder.moving(["a", "b"], id: "z", by: 1) == nil, "an unknown item cannot move")
    }

    static func checkOutputSchema() {
        check((try? TurnSettings.outputSchema(from: "  ")) == .some(nil), "an empty schema clears the setting")
        check((try? TurnSettings.outputSchema(from: #"{"type":"object"}"#)) == .object(["type": .string("object")]), "an object schema parses")
        do {
            _ = try TurnSettings.outputSchema(from: "[1]")
            check(false, "an array schema is rejected")
        } catch {
            check(error as? TurnSettings.SchemaError == .notObject, "an array schema is rejected as not an object")
        }
        do {
            _ = try TurnSettings.outputSchema(from: "{" + String(repeating: " ", count: 70_000) + "}")
            check(false, "an oversized schema is rejected")
        } catch {
            check(error as? TurnSettings.SchemaError == .tooLarge, "an oversized schema is rejected as too large")
        }
        check(!TurnSettings(serviceTier: .fast).isEmpty && !TurnSettings(outputSchema: .object([:])).isEmpty, "tier and schema count as settings")
    }

    static func api() -> AgenthailAPI {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StubProtocol.self]
        return AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
    }

    static func reset(_ replies: [(Int, String)]) {
        StubProtocol.bodies = []
        StubProtocol.replies = replies
    }

    static func checkRequests() async {
        let api = api()
        reset([(200, #"{"ok":true,"result":{"session":{"id":"fork-1","surface":"codex"}}}"#)])
        let fork = try? await api.forkSession(id: "S", cwd: "/work")
        check(fork?.session.id == "fork-1", "fork returns the new session")
        check(StubProtocol.bodies.first?["action"] as? String == "session-fork" && StubProtocol.bodies.first?["sessionId"] as? String == "S", "fork names the action and source")
        check((StubProtocol.bodies.first?["fork"] as? [String: Any])?["cwd"] as? String == "/work", "fork sends the folder")

        reset([(200, #"{"ok":true,"result":{"data":[{"id":"q1","clientUserMessageId":"c1","input":[{"type":"text","text":"first","text_elements":[]},{"type":"localImage","path":"/x.png"}]}],"nextCursor":"next"}}"#)])
        let page: NativeQueuePage? = try? await api.nativeQueue(sessionID: "S", NativeQueueCommand(queueAction: .list))
        check(page?.data.first?.text == "first\nImage" && page?.nextCursor == "next", "a queue page decodes Codex submissions")
        check(page?.data.first?.editableText == nil, "input with an image is not editable as text")
        let queue = StubProtocol.bodies.first?["nativeQueue"] as? [String: Any]
        check(StubProtocol.bodies.first?["action"] as? String == "native-queue" && queue?["queueAction"] as? String == "list" && queue?.count == 1, "list sends only its action")

        reset([(502, #"{"ok":false,"unknown":false,"error":"session is not a Claude background agent"}"#), (502, #"{"ok":false,"unknown":true,"error":"Claude stop: exit 1"}"#)])
        do {
            _ = try await api.sessionLifecycle(sessionID: "C", action: "status")
            check(false, "a failed operation throws")
        } catch {
            check(error.localizedDescription == "session is not a Claude background agent", "operation errors show the daemon's message")
        }
        do {
            _ = try await api.sessionLifecycle(sessionID: "C", action: "stop")
        } catch {
            check(error.localizedDescription.hasSuffix("The outcome is unknown; check the session before retrying."), "an unknown outcome is called out")
        }
        check(StubProtocol.bodies.last?["action"] as? String == "session-lifecycle-stop", "lifecycle actions use the session-lifecycle prefix")

        let settings = TurnSettings(effort: "high", serviceTier: .flex, outputSchema: .object(["type": .string("object")]))
        reset([(200, #"{"result":{"status":"delivered"}}"#), (200, #"{"result":{"status":"delivered"}}"#)])
        _ = try? await api.sendInstruction(action: "send", sessionID: "S", message: "hi", turnSettings: settings)
        _ = try? await api.sendInstruction(action: "steer", sessionID: "S", message: "hi", turnSettings: settings)
        let send = StubProtocol.bodies.first ?? [:]
        check(send["serviceTier"] as? String == "flex" && (send["outputSchema"] as? [String: Any])?["type"] as? String == "object", "a send carries tier and schema")
        let steer = StubProtocol.bodies.last ?? [:]
        check(steer["serviceTier"] == nil && steer["outputSchema"] == nil && steer["effort"] == nil, "a steer keeps the running turn's settings")
    }

    @MainActor
    static func checkNativeQueueController() async {
        let api = api()
        let controller = NativeQueueController()
        controller.reset(sessionID: "S")
        let empty = #"{"ok":true,"result":{"data":[],"nextCursor":null}}"#
        let two = #"{"ok":true,"result":{"data":[{"id":"q1","clientUserMessageId":"c1","input":[{"type":"text","text":"one"}]},{"id":"q2","clientUserMessageId":"c2","input":[{"type":"text","text":"two"}]}],"nextCursor":null}}"#

        reset([(502, #"{"ok":false,"unknown":true,"error":"timed out"}"#), (200, #"{"ok":true,"result":{"queuedSubmission":{"id":"q1","clientUserMessageId":"x","input":[]}}}"#), (200, empty), (200, #"{"ok":true,"result":{}}"#), (200, empty)])
        let failed = await controller.add("next", api: api)
        let firstID = (StubProtocol.bodies.last?["nativeQueue"] as? [String: Any])?["clientUserMessageId"] as? String
        check(!failed && controller.error?.contains("timed out") == true, "a failed add reports its error")
        _ = await controller.add("next", api: api)
        let retryID = (StubProtocol.bodies[1]["nativeQueue"] as? [String: Any])?["clientUserMessageId"] as? String
        check(firstID != nil && firstID == retryID, "retrying an uncertain add reuses its client message ID")
        check(controller.loaded && controller.error == nil, "a successful add reloads the queue")
        _ = await controller.add("other", api: api)
        let freshID = (StubProtocol.bodies[3]["nativeQueue"] as? [String: Any])?["clientUserMessageId"] as? String
        check(freshID != nil && freshID != firstID, "new work after a successful add gets a fresh client message ID")

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
        let controller = BackgroundSessionController()
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

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
