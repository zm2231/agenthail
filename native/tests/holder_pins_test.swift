@testable import AgenthailHelpers
import Foundation

final class PinStubProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var bodies: [[String: Any]] = []
    nonisolated(unsafe) static var keys: [String?] = []
    nonisolated(unsafe) static var status = 200

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
        if request.httpMethod == "POST", request.url?.path == "/api/v1/actions" {
            Self.keys.append(request.value(forHTTPHeaderField: "Idempotency-Key"))
            Self.bodies.append((try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:])
        }
        let body = Self.status == 200 ? #"{"ok":true,"result":{"status":"delivered"}}"# : #"{"ok":false,"error":"open in 2 terminals; pin one with claude:4101"}"#
        let response = HTTPURLResponse(url: request.url!, statusCode: Self.status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@main
struct HolderPinsTest {
    static func main() async {
        checkDecoding()
        checkStartTimes()
        checkPinnedBodies()
        checkKeys()
        checkPrune()
        checkRefusal()
        await checkRequests()
        print("holder pins tests passed")
    }

    static let holders = #"[{"pid":4101,"tty":"ttys002","status":"idle","startedAt":"2026-01-02T03:04:05.678Z"},{"pid":4102,"tty":"pts/1","status":"busy","startedAt":"2026-01-02T03:04:06Z"}]"#

    static func session(_ id: String = "S", surface: String = "claude", holders: String? = Self.holders) -> SessionState {
        let field = holders.map { #","holders":\#($0)"# } ?? ""
        let json = #"{"id":"\#(id)","surface":"\#(surface)","name":"\#(id)","status":"idle","queueCount":0,"open":true,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":true,"compact":true,"model":true,"interrupt":true,"steer":true}\#(field)}"#
        return try! JSONDecoder().decode(SessionState.self, from: Data(json.utf8))
    }

    static func checkDecoding() {
        let shared = session()
        check(shared.holders?.map(\.pid) == [4101, 4102], "holders decode")
        check(shared.sharedHolders.count == 2, "a Claude conversation with two holders is shared")
        check(shared.sharedHolders[0].label == "pid 4101, ttys002, idle", "label names pid, tty and status")
        check(SessionHolder(pid: 4103, status: "idle").label == "pid 4103, idle", "label skips a missing tty")
        check(session(holders: nil).sharedHolders.isEmpty, "no holders is not shared")
        check(session(holders: #"[{"pid":4101,"status":"idle"}]"#).sharedHolders.isEmpty, "one holder is not shared")
        check(session(surface: "codex").sharedHolders.isEmpty, "only Claude conversations are shared")
    }

    static func checkStartTimes() {
        let parsed = session().holders!.map(\.startedAtMilliseconds)
        check(parsed == [1_767_323_045_678, 1_767_323_046_000], "start times parse with and without fractions")
        check(SessionHolder(pid: 1, status: "idle", startedAt: "2026-01-02T05:04:05+02:00").startedAtMilliseconds == 1_767_323_045_000, "offsets parse")
        check(SessionHolder(pid: 1, status: "idle", startedAt: "yesterday").startedAtMilliseconds == nil, "garbage parses to nil")
        check(SessionHolder(pid: 1, status: "idle").startedAtMilliseconds == nil, "missing start is nil")
    }

    static func body(_ fields: [String: Any]) -> Data {
        try! JSONSerialization.data(withJSONObject: fields)
    }

    static func decode(_ data: Data) -> [String: Any] {
        (try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:]
    }

    static func checkPinnedBodies() {
        let pins = HolderPins()
        let pin = HolderPin(pid: 4102, startedAt: 1_767_323_046_000)
        let unpinned = pins.pinned(body: body(["action": "send", "sessionId": "S", "message": "hi"]), key: "k")
        check(decode(unpinned.body)["pid"] == nil && unpinned.key == "k", "no pick leaves the request alone")

        pins.pick(pin, for: "S")
        for action in ["send", "steer", "interrupt", "model", "compact", "goal-set", "session-lifecycle-stop", "session-lifecycle-resume"] {
            let pinned = pins.pinned(body: body(["action": action, "sessionId": "S"]), key: "k")
            let sent = decode(pinned.body)
            check(sent["pid"] as? Int == 4102 && (sent["pidStartedAt"] as? NSNumber)?.int64Value == 1_767_323_046_000, "\(action) carries the pick")
            check(sent["action"] as? String == action && sent["sessionId"] as? String == "S", "\(action) keeps its fields")
            check(pinned.key == HolderPins.key("k", pinnedTo: pin), "\(action) key follows the pick")
        }
        for action in ["session-create", "notion-create", "queue-retry", "queue-cancel", "delivery-dismiss", "channel-join", "relay-add", "project-add"] {
            let pinned = pins.pinned(body: body(["action": action, "sessionId": "S"]), key: "k")
            check(decode(pinned.body)["pid"] == nil && pinned.key == "k", "\(action) is never pinned")
        }
        let other = pins.pinned(body: body(["action": "send", "sessionId": "T"]), key: "k")
        check(decode(other.body)["pid"] == nil, "a pick applies to its own conversation only")
        let raw = Data("not json".utf8)
        check(pins.pinned(body: raw, key: "k") == (raw, "k"), "a body that is not JSON passes through")

        pins.pick(HolderPin(pid: 4101, startedAt: nil), for: "S")
        let noStart = decode(pins.pinned(body: body(["action": "send", "sessionId": "S"]), key: "k").body)
        check(noStart["pid"] as? Int == 4101 && noStart["pidStartedAt"] == nil, "a pick without a start time sends the pid alone")
        pins.pick(nil, for: "S")
        check(pins.pin(for: "S") == nil, "clearing a pick removes it")
    }

    static func checkKeys() {
        let first = HolderPin(pid: 4101, startedAt: 1)
        let key = HolderPins.key("k", pinnedTo: first)
        check(key == HolderPins.key("k", pinnedTo: first), "the derived key is stable for a retry")
        check(UUID(uuidString: key) != nil, "the derived key is a UUID")
        check(key != HolderPins.key("k", pinnedTo: HolderPin(pid: 4102, startedAt: 1)), "another terminal is another request")
        check(key != HolderPins.key("k", pinnedTo: HolderPin(pid: 4101, startedAt: 2)), "a reused pid is another request")
        check(key != HolderPins.key("j", pinnedTo: first), "another base key is another request")
    }

    static func checkPrune() {
        let pins = HolderPins()
        let holder = session().holders![1]
        pins.pick(HolderPin(holder), for: "S")
        pins.pick(HolderPin(pid: 4101, startedAt: 1), for: "T")
        pins.pick(HolderPin(holder), for: "U")
        pins.prune(to: [session("S"), session("T"), session("U", holders: #"[{"pid":4102,"status":"idle","startedAt":"2026-01-02T03:04:06Z"}]"#)])
        check(pins.pin(for: "S") == HolderPin(holder), "a pick on a listed holder stays")
        check(pins.pin(for: "T") == nil, "a pick whose process restarted is dropped")
        check(pins.pin(for: "U") == nil, "a pick on a conversation one process holds again is dropped")
        pins.prune(to: [])
        check(pins.all.isEmpty, "a conversation that is gone drops its pick")
    }

    static func checkRefusal() {
        let refused = AgenthailAPIError.request(409, "open in 2 terminals")
        let message = session().holderRefusal(refused, picked: false)
        check(message == "Open in 2 terminals. Choose a terminal, then send again.", "a refusal points at the picker")
        check(session().holderRefusal(refused, picked: true) == nil, "a refusal after a pick keeps the daemon's reason")
        check(session().holderRefusal(AgenthailAPIError.request(502, "x"), picked: false) == nil, "other failures keep their reason")
        check(session(holders: nil).holderRefusal(refused, picked: false) == nil, "an unshared conversation keeps the daemon's reason")
    }

    static func checkRequests() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [PinStubProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let pins = HolderPins()
        api.holderPins = pins

        PinStubProtocol.status = 409
        do {
            _ = try await api.sendInstruction(action: "send", sessionID: "S", message: "hi", idempotencyKey: "k")
            check(false, "an unpinned send to a shared conversation fails")
        } catch {
            check(session().holderRefusal(error, picked: false) != nil, "the refusal surfaces as a 409")
        }
        check(PinStubProtocol.bodies.last?["pid"] == nil && PinStubProtocol.keys.last == "k", "an unpinned send goes out as is")

        let pin = HolderPin(pid: 4102, startedAt: 1_767_323_046_000)
        pins.pick(pin, for: "S")
        PinStubProtocol.status = 200
        _ = try? await api.sendInstruction(action: "send", sessionID: "S", message: "hi", idempotencyKey: "k")
        let sent = PinStubProtocol.bodies.last ?? [:]
        check(sent["pid"] as? Int == 4102 && (sent["pidStartedAt"] as? NSNumber)?.int64Value == 1_767_323_046_000, "a picked send names the terminal")
        check(sent["message"] as? String == "hi", "a picked send keeps its message")
        check(PinStubProtocol.keys.last == HolderPins.key("k", pinnedTo: pin), "the retry after a pick is a new request")

        _ = try? await api.control("interrupt", sessionID: "S")
        check(PinStubProtocol.bodies.last?["pid"] as? Int == 4102, "controls carry the pick")
        try? await api.action("session-lifecycle-stop", sessionID: "S")
        check(PinStubProtocol.bodies.last?["pid"] as? Int == 4102, "lifecycle carries the pick")
        try? await api.action("queue-cancel", sessionID: "S", queueID: 7)
        check(PinStubProtocol.bodies.last?["pid"] == nil, "queue cancel is not pinned")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
