@testable import AgenthailHelpers
import Foundation

final class MoveStub: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var reply: (Int, String) = (200, "{}")
    nonisolated(unsafe) static var failure: URLError.Code?
    nonisolated(unsafe) static var sent: [String: Any] = [:]
    nonisolated(unsafe) static var path = ""
    nonisolated(unsafe) static var timeout: TimeInterval = 0
    nonisolated(unsafe) static var count = 0

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
        guard request.url?.path == "/api/v1/session-moves" else {
            let response = HTTPURLResponse(url: request.url!, statusCode: 503, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(#"{"error":{"message":"not part of this test"}}"#.utf8))
            client?.urlProtocolDidFinishLoading(self)
            return
        }
        Self.sent = (try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:]
        Self.path = "\(request.httpMethod ?? "") \(request.url?.path ?? "")"
        Self.timeout = request.timeoutInterval
        Self.count += 1
        if let failure = Self.failure {
            client?.urlProtocol(self, didFailWithError: URLError(failure))
            return
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: Self.reply.0, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(Self.reply.1.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@main
struct SessionMovesTest {
    @MainActor
    static func main() async {
        checkRowField()
        checkAvailability()
        checkRequestBody()
        checkOutcomes()
        checkNotices()
        await checkAPI()
        await checkMacModel()
        print("session moves tests passed")
    }

    static let snapshot = """
    {"updatedAt":"now","daemon":{"running":true,"host":{"id":"hself","name":"laptop","state":"local"},"hosts":[{"id":"hpeer","name":"peer","state":"connected"},{"id":"hdown","name":"spare","state":"unreachable"}]},"surfaces":[],"sessions":[
      {"id":"old","surface":"claude","name":"task","status":"idle","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}},
      {"id":"hpeer::m2","surface":"claude","name":"remote","status":"idle","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"host":{"id":"hpeer","name":"peer","state":"connected"}}],
     "totalSessions":2,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5}
    """

    @MainActor
    static func checkMacModel() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [MoveStub.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        guard let decoded = try? JSONDecoder().decode(DashboardSnapshot.self, from: Data(snapshot.utf8)) else { return check(false, "the fixture snapshot decodes") }
        model.snapshot = decoded
        guard let local = decoded.sessions.first(where: { $0.name == "task" }), let remote = decoded.sessions.first(where: { $0.name == "remote" }) else { return check(false, "fixture sessions") }
        check(model.canMove(local), "the Mac offers a move of its own session")
        check(!model.canMove(remote), "the Mac leaves a paired host's session to that host")
        check(model.moveTargets.map(\.name) == ["peer", "spare"], "the Mac lists its paired hosts")

        MoveStub.count = 0
        MoveStub.reply = (409, #"{"error":{"code":"move_needs_consent","message":"a terminal holds it"}}"#)
        model.move(local, to: model.moveTargets[0])
        check(!model.canMove(local), "a move in flight cannot be sent twice")
        await waitForNotice(model)
        guard let consent = model.moveNotice else { return check(false, "consent is asked") }
        check(MoveStub.sent["sessionId"] as? String == "old" && MoveStub.sent["host"] as? String == "hpeer" && MoveStub.sent["stopTerminal"] as? Bool == false, "the move goes to the picked host: \(MoveStub.sent)")
        check(consent.retry?.stopTerminal == true && consent.subject == "old", "consent can resend")
        check(model.canMove(local), "an answered move can be asked again")

        MoveStub.reply = (200, #"{"ok":true,"result":{"host":{"id":"hpeer","name":"peer","state":"connected"}}}"#)
        model.moveNotice = nil
        model.retryMove(consent)
        await waitForNotice(model)
        check(MoveStub.count == 2 && MoveStub.sent["stopTerminal"] as? Bool == true, "the resend stops the terminal")
        check(model.moveNotice?.title == "Moved to peer", "the Mac reports the move: \(String(describing: model.moveNotice?.title))")
    }

    @MainActor
    static func waitForNotice(_ model: AgenthailModel) async {
        for _ in 0..<500 where model.moveNotice == nil { try? await Task.sleep(for: .milliseconds(10)) }
    }

    static func session(surface: String = "claude", move: SessionMove? = nil) -> SessionState {
        var state = SessionState(id: "s1", surface: surface, name: "builder", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        state.move = move
        return state
    }

    static func checkRowField() {
        let row = #"{"id":"s1","surface":"claude","name":"builder","status":"idle","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"move":{"state":"settling","host":"h1","hostName":"server","detail":"no answer"}}"#
        let decoded = try? JSONDecoder().decode(SessionState.self, from: Data(row.utf8))
        check(decoded?.move == SessionMove(state: "settling", host: "h1", hostName: "server", detail: "no answer"), "a row decodes its move")
        let plain = #"{"id":"s1","surface":"claude","name":"builder","status":"idle","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}}"#
        let plainRow = try? JSONDecoder().decode(SessionState.self, from: Data(plain.utf8))
        check(plainRow != nil && plainRow?.move == nil, "a row without a move decodes with none")
        check(SessionMove(state: "holding", host: "h1", hostName: "server").label == "Moving to server: holding", "holding")
        check(SessionMove(state: "copying", host: "h1", hostName: "server").label == "Moving to server: copying", "copying")
        check(SessionMove(state: "settling", host: "h1", hostName: "server").label == "Move to server unsettled", "settling")
        check(SessionMove(state: "holding", host: "h1").label == "Moving to h1: holding", "a host without a name shows its id")
        check(SessionMove(state: "holding", host: "self", hostName: "laptop", root: "work").label == "Moving to account work: holding", "an account move names the account")
    }

    static func checkAvailability() {
        let hosts = [HostHealth(id: "h1", name: "server", state: "connected"), HostHealth(id: "h2", name: "spare", state: "unreachable")]
        check(SessionMoveAvailability.offered(session(), local: true, hosts: hosts), "a local idle Claude session with paired hosts can move")
        check(SessionMoveAvailability.offered(session(surface: "codex"), local: true, hosts: hosts), "Codex sessions can move")
        check(!SessionMoveAvailability.offered(session(surface: "notion"), local: true, hosts: hosts), "other surfaces cannot move")
        check(!SessionMoveAvailability.offered(session(), local: false, hosts: hosts), "a paired host's row moves on that host, not here")
        check(!SessionMoveAvailability.offered(session(), local: true, hosts: []), "no paired host, no move")
        check(!SessionMoveAvailability.offered(session(move: SessionMove(state: "copying", host: "h1")), local: true, hosts: hosts), "a session already moving cannot move again")
        var subagent = session()
        subagent.subagent = SubagentIdentity(parentId: "p", rootId: "p", depth: 1)
        check(!SessionMoveAvailability.offered(subagent, local: true, hosts: hosts), "a subagent moves with its parent, not alone")
        let readOnly = SessionState(id: "s1", surface: "claude", name: "builder", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: true, readOnlyReason: "read only")
        check(!SessionMoveAvailability.offered(readOnly, local: true, hosts: hosts), "a read-only session cannot move")
        let targets = SessionMoveAvailability.targets(hosts)
        check(targets.map(\.id) == ["h1", "h2"] && targets.map(\.isAvailable) == [true, false], "every paired host is listed and only connected ones can be picked")
    }

    static func checkRequestBody() {
        let body = (try? JSONSerialization.jsonObject(with: JSONEncoder().encode(SessionMoveRequest(sessionId: "s1", host: "h1")))) as? [String: Any]
        check(body?["sessionId"] as? String == "s1" && body?["host"] as? String == "h1" && body?["stopTerminal"] as? Bool == false && body?.count == 3, "the request names the session and the host: \(String(describing: body))")
    }

    static func outcome(_ status: Int, _ body: String) -> SessionMoveOutcome? {
        SessionMoveOutcome(status: status, body: Data(body.utf8), hostName: "server")
    }

    static func checkOutcomes() {
        let moved = outcome(200, #"{"ok":true,"result":{"move":{"id":"m1"},"host":{"id":"h1","name":"vps","state":"connected"},"stopped":"terminal process 41","mcpServersWithoutCredentials":["linear","sentry"],"droppedRelays":[{"moveId":"m1","sessionId":"s1","relayId":7,"toSession":"s9","at":"2026-11-10T00:00:00Z"}]}}"#)
        check(moved == .moved(SessionMoveSummary(hostName: "vps", stopped: "terminal process 41", mcpServers: ["linear", "sentry"], droppedRelays: ["Relay #7 was not taken over: session s9, which it sends to, left this computer's view during the move."])), "a move reads its summary: \(String(describing: moved))")
        check(outcome(202, #"{"error":{"code":"move_outcome_unknown","message":"move m1 is unresolved"},"result":{}}"#) == .unsettled, "202 is unsettled")
        check(outcome(409, #"{"error":{"code":"move_busy","message":"session s1 is busy"},"busy":"a turn is running"}"#) == .busy("a turn is running"), "409 move_busy carries why")
        check(outcome(409, #"{"error":{"code":"move_needs_consent","message":"a terminal holds it"}}"#) == .needsConsent, "409 move_needs_consent asks to stop the terminal")
        check(outcome(409, #"{"error":{"code":"move_refused","message":"commit and push them first"}}"#) == .refused("commit and push them first"), "409 move_refused keeps the daemon's words")
        check(outcome(502, #"{"error":{"code":"move_failed","message":"the archive failed"}}"#) == .refused("the archive failed"), "502 move_failed keeps the daemon's words")
        check(outcome(409, #"{"error":{"code":"other","message":"x"}}"#) == nil, "an answer that is not a move outcome is left to the generic error")
        check(outcome(401, #"{"error":{"code":"unauthorized","message":"x"}}"#) == nil, "an auth failure is left to the generic error")
    }

    static func checkNotices() {
        let request = SessionMoveRequest(sessionId: "s1", host: "h1")
        let moved = SessionMoveNotice.of(.moved(SessionMoveSummary(hostName: "vps", stopped: "host", mcpServers: ["linear"], droppedRelays: ["Relay #7 was not taken over."])), request: request, session: "@builder", hostName: "vps", here: "laptop")
        check(moved.title == "Moved to vps" && moved.retry == nil, "a move says where it went")
        check(moved.message == "Stopped the Claude process Agenthail ran for it.\n\nThese MCP servers moved without their environment and headers. Add their credentials again on vps:\nlinear\n\nRelay #7 was not taken over.", "the summary lists what to redo: \(moved.message)")
        let consent = SessionMoveNotice.of(.needsConsent, request: request, session: "@builder", hostName: "vps", here: "laptop")
        check(consent.retry == SessionMoveRequest(sessionId: "s1", host: "h1", stopTerminal: true), "consent resends with stopTerminal")
        let refused = SessionMoveNotice.of(.refused("commit and push them first"), request: request, session: "@builder", hostName: "vps", here: "laptop")
        check(refused.title == "Couldn't move @builder" && refused.message == "commit and push them first\n\nIt stays on laptop." && refused.retry == nil, "a refusal keeps the session here")
        let busy = SessionMoveNotice.of(.busy("a turn is running"), request: request, session: "@builder", hostName: "vps", here: "laptop")
        check(busy.message == "@builder is busy (a turn is running). Move it once it's idle." && busy.retry == nil, "busy does not retry by itself")
        let unsettled = SessionMoveNotice.of(.unsettled, request: request, session: "@builder", hostName: "vps", here: "laptop")
        check(unsettled.title == "The move to vps hasn't settled" && unsettled.message.contains("settles when vps reconnects"), "unsettled says when it settles")
        let lost = SessionMoveNotice.of(.lost, request: request, session: "@builder", hostName: "vps", here: "laptop")
        check(lost.message.contains("carries on without this app"), "a lost connection says the daemon carries on")
    }

    @MainActor
    static func checkAPI() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [MoveStub.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let request = SessionMoveRequest(sessionId: "s1", host: "h1")

        MoveStub.reply = (200, #"{"ok":true,"result":{"host":{"id":"h1","name":"vps","state":"connected"}}}"#)
        let moved = await api.moveSession(request, hostName: "vps")
        check(moved == .moved(SessionMoveSummary(hostName: "vps")), "the API returns the move: \(moved)")
        check(MoveStub.path == "POST /api/v1/session-moves" && MoveStub.sent["host"] as? String == "h1" && MoveStub.sent["sessionId"] as? String == "s1", "the API posts the move: \(MoveStub.path) \(MoveStub.sent)")
        check(MoveStub.timeout >= 30 * 60, "the request waits as long as a move can take: \(MoveStub.timeout)")

        MoveStub.reply = (409, #"{"error":{"code":"move_needs_consent","message":"a terminal holds it"}}"#)
        check(await api.moveSession(request, hostName: "vps") == .needsConsent, "the API reads a 409")
        MoveStub.reply = (202, #"{"error":{"code":"move_outcome_unknown","message":"unresolved"}}"#)
        check(await api.moveSession(request, hostName: "vps") == .unsettled, "the API reads a 202")
        MoveStub.reply = (500, #"{"error":{"code":"moves_unavailable","message":"database is locked"}}"#)
        check(await api.moveSession(request, hostName: "vps") == .refused("database is locked"), "another failure shows its message")

        MoveStub.failure = .timedOut
        check(await api.moveSession(request, hostName: "vps") == .lost, "a request that may have reached the daemon is lost, not refused")
        MoveStub.failure = .cannotConnectToHost
        if case .refused = await api.moveSession(request, hostName: "vps") {} else { check(false, "a request that never left is refused") }
        MoveStub.failure = nil
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
