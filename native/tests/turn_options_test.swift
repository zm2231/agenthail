@testable import AgenthailHelpers
import Foundation

@main
struct TurnOptionsTest {
    @MainActor
    static func main() async throws {
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: CaptureSends.session)
        try checkPlainWireUnchanged(api)
        try checkSavedSendFromCurrentBuild()
        try checkOptionsOnTheWire(api)
        checkSchemaInput()
        checkDraft()
        await checkSendCarriesOptions(api)
        print("ok")
    }

    private static func checkPlainWireUnchanged(_ api: AgenthailAPI) throws {
        let wire = try api.instructionWire(sessionID: "S", message: "hi", busyDelivery: "queue", author: "person", attachments: ["u1"], idempotencyKey: "k")
        let plain = try JSONSerialization.jsonObject(with: wire.body) as? [String: Any]
        check(Set(plain?.keys ?? [:].keys) == ["action", "sessionId", "message", "busyDelivery", "author", "attachments"], "a send without turn options carries only the fields it did before: \(String(decoding: wire.body, as: UTF8.self))")
        let empty = try api.instructionWire(sessionID: "S", message: "hi", busyDelivery: "queue", author: "person", attachments: ["u1"], turnOptions: MessageTurnOptions(serviceTier: ""), idempotencyKey: "k")
        let emptyObject = try JSONSerialization.jsonObject(with: empty.body) as? [String: Any]
        check(Set(emptyObject?.keys ?? [:].keys) == Set(plain?.keys ?? [:].keys), "empty turn options add nothing to the body")
    }

    private static func checkSavedSendFromCurrentBuild() throws {
        let body = #"{"action":"send","sessionId":"S","message":"queued before the upgrade","author":"person"}"#
        let saved = #"{"drafts":{},"stashes":{},"sends":[{"id":"6F1B8B2E-3B0B-4C38-9E3E-0F6E5D1C2A10","sessionID":"S","message":"queued before the upgrade","idempotencyKey":"k-saved","uploaded":[],"wire":{"body":"\#(Data(body.utf8).base64EncodedString())","key":"k-saved"},"hadAttachments":false,"sentAt":784000000,"busyDelivery":null,"files":[]}],"attachmentNames":{}}"#
        let decoded = try JSONDecoder().decode(ComposerStore.Saved.self, from: Data(saved.utf8))
        let resumed = decoded.sends.first?.resumable
        check(resumed?.idempotencyKey == "k-saved" && resumed?.wire?.key == "k-saved", "a send saved by the current build keeps its key")
        check(resumed?.wire?.body == Data(body.utf8), "a send saved by the current build replays the exact bytes it first sent")
        check(resumed?.turnOptions == nil, "a send saved without turn options resumes without any")
    }

    private static func checkOptionsOnTheWire(_ api: AgenthailAPI) throws {
        let options = MessageTurnOptions(serviceTier: "fast", outputSchema: .object(["type": .string("object")]))
        let wire = try api.instructionWire(sessionID: "S", message: "hi", author: "person", attachments: [], turnOptions: options, idempotencyKey: "k")
        let object = try JSONSerialization.jsonObject(with: wire.body) as? [String: Any]
        check(object?["serviceTier"] as? String == "fast", "the service tier is sent")
        check((object?["outputSchema"] as? [String: Any])?["type"] as? String == "object", "the schema is sent as a JSON object")
        let send = PendingSend(sessionID: "S", message: "hi", attachments: [], turnOptions: options)
        let record = try JSONDecoder().decode(ComposerStore.Saved.Send.self, from: JSONEncoder().encode(ComposerStore.Saved.Send(send)))
        check(record.turnOptions == options && record.resumable?.turnOptions == options, "turn options survive a save and a relaunch")
        check(PendingSend(sessionID: "S", message: "hi", attachments: [], turnOptions: MessageTurnOptions()).turnOptions == nil, "empty turn options aren't kept")
    }

    private static func checkSchemaInput() {
        check(OutputSchemaInput.problem("") == nil && OutputSchemaInput.problem(#"{"type":"object"}"#) == nil, "an empty or object schema is fine")
        check(OutputSchemaInput.problem("[1]") != nil && OutputSchemaInput.problem("nope") != nil, "a schema must be a JSON object")
        check(OutputSchemaInput.problem(#"{"d":"\#(String(repeating: "x", count: OutputSchemaInput.limit))"}"#) != nil, "a schema over 64 KiB is refused")
        guard case .success(let value) = OutputSchemaInput.recorded(#"{"type":"object","required":["a"]}"#) else {
            check(false, "a valid schema is recorded")
            return
        }
        check(value == .object(["type": .string("object"), "required": .array([.string("a")])]), "the recorded schema keeps its content: \(String(describing: value))")
    }

    private static func checkDraft() {
        let draft = ComposerDraft()
        check(draft.turnOptions == nil && draft.turnOptionsProblem == nil, "a fresh draft carries no turn options")
        draft.serviceTier = "flex"
        check(draft.turnOptions == MessageTurnOptions(serviceTier: "flex"), "a chosen tier is carried")
        draft.outputSchema = "{"
        check(draft.turnOptionsProblem != nil && draft.turnOptions == nil, "a broken schema is a problem and carries nothing")
        check(TurnOptionsChip.label(serviceTier: "flex", outputSchema: #"{"type":"object"}"#) == "Flex · Schema" && TurnOptionsChip.label(serviceTier: "", outputSchema: " ") == "Turn options", "the chip names what's set")
    }

    @MainActor
    private static func checkSendCarriesOptions(_ api: AgenthailAPI) async {
        let model = AgenthailModel(connecting: false, api: api)
        model.send("tiered", to: "S", busyDelivery: "queue", turnOptions: MessageTurnOptions(serviceTier: "fast", outputSchema: .object(["type": .string("object")])))
        for _ in 0..<100 where CaptureSends.bodies.isEmpty { try? await Task.sleep(for: .milliseconds(20)) }
        let body = CaptureSends.bodies.first.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }
        check(body?["serviceTier"] as? String == "fast" && body?["busyDelivery"] as? String == "queue" && body?["outputSchema"] != nil, "a send from the model carries its turn options: \(String(describing: body))")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}

final class CaptureSends: URLProtocol {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var captured: [Data] = []
    static var bodies: [Data] { lock.withLock { captured } }

    static let session: URLSession = {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [CaptureSends.self]
        return URLSession(configuration: configuration)
    }()

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody ?? Data()
        if body.isEmpty, let stream = request.httpBodyStream {
            stream.open()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let count = stream.read(&buffer, maxLength: buffer.count)
                if count <= 0 { break }
                body.append(buffer, count: count)
            }
            stream.close()
        }
        if request.url?.path == "/api/v1/actions" { Self.lock.withLock { Self.captured.append(body) } }
        let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(#"{"ok":true,"result":{"status":"sent"}}"#.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
