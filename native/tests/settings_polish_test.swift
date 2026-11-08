@testable import AgenthailHelpers
import Foundation
import Network

@main
@MainActor
struct SettingsPolishTest {
    static func main() async throws {
        let pairing = try decode(PairingResponse.self, #"{"id":"p1","expiresAt":"2026-11-08T12:05:00Z","pairingURL":"agenthail://pair","endpoint":"https://h","secret":"s","scopes":["read"]}"#)
        let before = TurnClock.date("2026-11-08T12:04:00Z")!
        let after = TurnClock.date("2026-11-08T12:05:01Z")!

        let paired = try decode(PairingStatusResponse.self, #"{"id":"p1","state":"paired","expiresAt":"2026-11-08T12:05:00Z","device":{"id":"d1","name":"Ana's iPhone","scopes":["read"],"hostId":"h","createdAt":"x","lastSeenAt":"","revokedAt":""}}"#)
        check(PairingProgress.of(paired, pairing: pairing, now: before) == .paired("Ana's iPhone"), "a paired code names the device")
        check(PairingProgress.paired("Ana's iPhone").notice == "Paired Ana's iPhone.", "pairing reads as a notice")
        let anonymous = try decode(PairingStatusResponse.self, #"{"id":"p1","state":"paired","expiresAt":"2026-11-08T12:05:00Z"}"#)
        check(PairingProgress.of(anonymous, pairing: pairing, now: before) == .paired("the iPhone"), "a paired code without a device still pairs")
        let expired = try decode(PairingStatusResponse.self, #"{"id":"p1","state":"expired","expiresAt":"2026-11-08T12:05:00Z"}"#)
        check(PairingProgress.of(expired, pairing: pairing, now: before) == .expired, "the daemon's expiry wins")
        check(PairingProgress.expired.notice == "The pairing code expired. Pair again to show a new one.", "expiry says how to recover")
        let pending = try decode(PairingStatusResponse.self, #"{"id":"p1","state":"pending","expiresAt":"2026-11-08T12:05:00Z"}"#)
        check(PairingProgress.of(pending, pairing: pairing, now: after) == .waiting, "a pending answer keeps waiting")
        check(PairingProgress.of(nil, pairing: pairing, now: before) == .waiting, "an unanswered check keeps waiting before the deadline")
        check(PairingProgress.of(nil, pairing: pairing, now: after) == .expired, "an unanswered check past the deadline expires the code")
        check(PairingProgress.deadline(pairing).hasPrefix("The code works once, before "), "the code shows its deadline: \(PairingProgress.deadline(pairing))")

        try await watches(pairing)
        try await searches()

        let session = try decode(SessionState.self, #"{"id":"S","surface":"codex","name":"s","status":"idle","queueCount":1,"open":true,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":true,"interrupt":true,"steer":true}}"#)
        let readOnly = try decode(SessionState.self, #"{"id":"S","surface":"codex","name":"s","status":"idle","queueCount":1,"open":true,"current":false,"readOnly":true,"capabilities":{"send":false,"stream":true,"reply":false,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false}}"#)
        let message = try queued(#""status":"pending""#)
        check(QueueFacts.canTakeBack(message, target: session), "a pending message to a known session can be taken back")
        check(!QueueFacts.canTakeBack(message, target: nil), "a message to an unknown session can't be taken back")
        check(!QueueFacts.canTakeBack(message, target: readOnly), "a read-only session has no composer to take the message back into")
        check(!QueueFacts.canTakeBack(try queued(#""status":"dead""#), target: session), "a dead message is retried or cancelled, not taken back")
        check(!QueueFacts.canTakeBack(try queued(#""status":"pending","operation":"goal-set""#), target: session), "a queued operation isn't a message to take back")
        check(QueueFacts.canTakeBack(try queued(#""status":"pending","operation":"message""#), target: session), "an explicit message operation can be taken back")

        let described = LauncherOption(id: "tmux", label: "tmux", detail: " Opens a tmux window. ")
        check(NewSessionLabels.launcherDetail(described) == "Opens a tmux window.", "a launcher shows its description")
        check(NewSessionLabels.launcherHelp(described) == "Runs in tmux: Opens a tmux window.", "the launcher chip explains where it runs")
        let bare = LauncherOption(id: "app", label: "App", detail: "  ")
        check(NewSessionLabels.launcherDetail(bare) == nil && NewSessionLabels.launcherHelp(bare) == "Where the agent runs", "a launcher without a description shows none")
        print("ok")
    }

    static func watches(_ pairing: PairingResponse) async throws {
        var checks = 0
        let server = try StubServer { line in
            if line.contains("/api/v1/pairings?id=p1") {
                checks += 1
                return checks < 2 ? reply(#"{"id":"p1","state":"pending","expiresAt":"2026-11-08T12:05:00Z"}"#) : reply(#"{"id":"p1","state":"paired","expiresAt":"2026-11-08T12:05:00Z","device":{"name":"Ana's iPhone"}}"#)
            }
            if line.contains("/api/v1/devices") {
                return reply(#"{"devices":[{"id":"d1","name":"Ana's iPhone","scopes":["read"],"createdAt":"2026-11-08T12:04:30Z","pushEnabled":false}]}"#)
            }
            return "HTTP/1.1 404 Not Found\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}"
        }
        let port = await server.ready()
        let api = AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(port)")!, token: "t", session: URLSession(configuration: .ephemeral))
        let model = AgenthailModel(connecting: false, api: api)

        model.pairing = pairing
        await model.watchPairing(pairing, api: api, interval: .milliseconds(10))
        check(checks == 2 && model.pairing == nil && model.pairingNotice == "Paired Ana's iPhone." && model.devices.map(\.name) == ["Ana's iPhone"], "pairing clears the code, says so and reloads devices")

        model.pairingNotice = nil
        model.pairing = pairing
        let watch = Task { await model.watchPairing(pairing, api: api, interval: .milliseconds(10)) }
        model.cancelPairing()
        await watch.value
        check(model.pairing == nil && model.pairingNotice == nil, "Cancel drops the code without a notice")

        let newer = try decode(PairingResponse.self, #"{"id":"p2","expiresAt":"2026-11-08T12:10:00Z","pairingURL":"agenthail://pair","endpoint":"https://h","secret":"s2","scopes":["read"]}"#)
        model.pairing = newer
        let before = checks
        await model.watchPairing(pairing, api: api, interval: .milliseconds(10))
        check(model.pairing?.id == "p2" && model.pairingNotice == nil && checks == before, "a watch for a replaced code stops without touching the new one")
        server.stop()

        var posts = 0
        let racing = try StubServer(delay: { $0.hasPrefix("POST /api/v1/pairings") && posts == 0 ? 0.3 : 0 }) { line in
            if line.hasPrefix("POST /api/v1/pairings") {
                posts += 1
                let id = posts == 1 ? "slow" : "fast"
                return reply(#"{"id":"\#(id)","expiresAt":"2099-01-01T00:00:00Z","pairingURL":"agenthail://pair","endpoint":"https://h","secret":"s","scopes":["read"]}"#)
            }
            return reply(#"{"id":"x","state":"pending","expiresAt":"2099-01-01T00:00:00Z"}"#)
        }
        let racingPort = await racing.ready()
        let racingModel = AgenthailModel(connecting: false, api: AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(racingPort)")!, token: "t", session: URLSession(configuration: .ephemeral)))
        racingModel.createPairing(interval: .milliseconds(10))
        for _ in 0..<40 where posts < 1 { try? await Task.sleep(for: .milliseconds(10)) }
        racingModel.createPairing(interval: .milliseconds(10))
        for _ in 0..<100 where racingModel.pairing == nil { try? await Task.sleep(for: .milliseconds(10)) }
        try? await Task.sleep(for: .milliseconds(500))
        check(posts == 2 && racingModel.pairing?.id == "fast", "a slow first code never replaces the one asked for after it: \(posts) \(racingModel.pairing?.id ?? "none")")
        racingModel.cancelPairing()
        racing.stop()
    }

    static func searches() async throws {
        var requests: [String] = []
        let server = try StubServer(delay: { $0.contains("q=old") ? 0.3 : 0 }) { line in
            requests.append(line)
            if line.contains("q=old") {
                return "HTTP/1.1 500 Internal Server Error\r\nContent-Type: application/json\r\nContent-Length: 40\r\nConnection: close\r\n\r\n{\"error\":{\"message\":\"old search broke\"}}"
            }
            return reply(#"{"items":[{"id":7,"createdAt":"2026-11-08T12:00:00Z","kind":"send"}],"hasMore":false,"nextBefore":0,"kinds":["send"]}"#)
        }
        let port = await server.ready()
        let model = AgenthailModel(connecting: false, api: AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(port)")!, token: "t", session: URLSession(configuration: .ephemeral)))
        model.auditQuery = "old"
        let old = Task { await model.loadAudit(reset: true) }
        for _ in 0..<40 where requests.isEmpty { try? await Task.sleep(for: .milliseconds(10)) }
        model.auditQuery = "new"
        await model.loadAudit(reset: true)
        await old.value
        check(requests.count == 2 && model.audit.map(\.id) == [7] && model.operationError == nil, "a failed older search leaves the newer results and shows no error: \(model.operationError ?? "")")

        await model.loadAudit(reset: true)
        model.auditQuery = "old"
        await model.loadAudit(reset: true)
        check(model.operationError?.contains("old search broke") == true, "a failed current search still says why: \(model.operationError ?? "nil")")
        server.stop()
    }

    static func queued(_ fields: String) throws -> QueueState {
        try decode(QueueState.self, #"{"id":1,"sessionId":"S","target":"s",\#(fields),"message":"hi","attempts":0,"queuedAt":"2026-11-08T12:00:00Z","evidence":""}"#)
    }

    static func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
        try JSONDecoder().decode(type, from: Data(json.utf8))
    }

    static func reply(_ body: String) -> String {
        "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: \(body.utf8.count)\r\nConnection: close\r\n\r\n\(body)"
    }

    final class StubServer: @unchecked Sendable {
        private let listener: NWListener
        private var connections: [NWConnection] = []

        init(delay: @escaping (String) -> Double = { _ in 0 }, respond: @escaping (String) -> String) throws {
            listener = try NWListener(using: .tcp, on: .any)
            listener.newConnectionHandler = { [self] connection in
                connections.append(connection)
                connection.start(queue: .main)
                connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { data, _, _, _ in
                    let request = String(decoding: data ?? Data(), as: UTF8.self)
                    let wait = delay(request)
                    let reply = respond(request)
                    DispatchQueue.main.asyncAfter(deadline: .now() + wait) {
                        connection.send(content: Data(reply.utf8), completion: .contentProcessed { _ in })
                    }
                }
            }
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

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
