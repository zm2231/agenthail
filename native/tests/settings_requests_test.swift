@testable import AgenthailHelpers
import Foundation

@main
struct SettingsRequestsTest {
    @MainActor
    static func main() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [RecordingProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        let repairable = try! JSONDecoder().decode(SurfaceState.self, from: Data(#"{"name":"codex","connected":false,"health":"degraded","repairAction":"runtime-ensure","repairLabel":"Start managed runtime","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"runtime":{"name":"Codex app server","reachable":false,"durable":false,"problem":"runtime-stopped"}}"#.utf8))
        model.snapshot = DashboardSnapshot(updatedAt: "2026-10-05T12:00:00Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [repairable], sessions: [], totalSessions: 0, queue: [], channels: [], relays: [], history: [], attention: [], deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "steer")

        await model.repairSurface(repairable)
        let repair = RecordingProtocol.requests.first { $0.path == "/api/v1/actions" }
        check(repair?.method == "POST" && repair?.body["action"] as? String == "runtime-ensure" && repair?.body.count == 1, "the repair posts only the surface's action: \(String(describing: repair?.body))")
        check(repair?.idempotencyKey?.isEmpty == false, "the repair carries an idempotency key")
        check(RecordingProtocol.requests.contains { $0.path == "/api/v1/snapshot" }, "a repair refreshes the snapshot")
        check(model.operationError == nil, "a successful repair clears the error")

        RecordingProtocol.reset(failing: "/api/v1/actions")
        await model.repairSurface(repairable)
        check(model.operationError == "codex launch failed", "a failed repair shows the daemon's message: \(String(describing: model.operationError))")

        RecordingProtocol.reset()
        model.setCodexRecentHours(12)
        let config = await waitFor { $0.path == "/api/v1/settings" && $0.method == "POST" }
        check(config?.body["action"] as? String == "dashboard-config" && config?.body["codexRecentHours"] as? Int == 12 && config?.body["busyDelivery"] as? String == "steer", "the window change keeps the busy delivery: \(String(describing: config?.body))")
        RecordingProtocol.reset()
        model.setCodexRecentHours(5)
        try? await Task.sleep(for: .milliseconds(200))
        check(RecordingProtocol.requests.isEmpty, "choosing the current window sends nothing")

        RecordingProtocol.reset()
        model.performOperation(action: "relay-add", fromID: "@research", toID: "@builder", pattern: "READY", once: true)
        let relay = await waitFor { $0.path == "/api/v1/actions" }
        check(relay?.body["once"] as? Bool == true && relay?.body["fromId"] as? String == "@research" && relay?.body["pattern"] as? String == "READY", "a one-time handoff sends once: \(String(describing: relay?.body))")

        RecordingProtocol.reset()
        model.send("typed by a person", to: "claude-target")
        let composed = await waitFor { $0.path == "/api/v1/actions" && $0.body["action"] as? String == "send" }
        check(composed?.body["author"] as? String == "person" && composed?.body["message"] as? String == "typed by a person", "the composer declares a person's message: \(String(describing: composed?.body))")
        RecordingProtocol.reset()
        _ = await model.reply("notification reply", to: "claude-target", connectionTimeout: .seconds(1))
        let replied = await waitFor { $0.path == "/api/v1/actions" && $0.body["message"] as? String == "notification reply" }
        check(replied?.body["author"] as? String == "person", "a notification reply is a person's message: \(String(describing: replied?.body))")
        RecordingProtocol.reset()
        _ = try? await api.sendInstruction(action: "send", sessionID: "claude-target", message: "unmarked")
        let unmarked = await waitFor { $0.path == "/api/v1/actions" && $0.body["message"] as? String == "unmarked" }
        check(unmarked != nil && unmarked?.body["author"] == nil, "an unmarked API send declares no author: \(String(describing: unmarked?.body))")

        RecordingProtocol.reset()
        await model.loadSignin()
        check(model.signin?.consumers.map(\.id) == ["notion", "claude"] && model.signin?.addableBrowsers.first?.addableProfiles.map(\.profile) == ["Default"], "sign-in settings decode: \(String(describing: model.signin))")
        RecordingProtocol.reset()
        model.addSigninProfile(browser: "chrome", profile: "Default", label: "")
        let added = await waitFor { $0.path == "/api/v1/signin/profiles" && $0.method == "POST" }
        check(added?.body["browser"] as? String == "chrome" && added?.body["profile"] as? String == "Default", "adding sends the chosen browser profile: \(String(describing: added?.body))")
        RecordingProtocol.reset()
        model.bindSignin(consumer: "notion", profileID: "sp_0123456789ab")
        let bound = await waitFor { $0.path == "/api/v1/signin/bindings" }
        check(bound?.method == "PUT" && bound?.body["consumer"] as? String == "notion" && bound?.body["profileId"] as? String == "sp_0123456789ab", "binding names the consumer and profile: \(String(describing: bound?.body))")
        RecordingProtocol.reset()
        model.bindSignin(consumer: "claude", profileID: nil)
        let cleared = await waitFor { $0.path == "/api/v1/signin/bindings" }
        check(cleared?.body["profileId"] is NSNull, "choosing Not set up sends a null profile: \(String(describing: cleared?.body))")
        RecordingProtocol.reset()
        model.removeSigninProfile("sp_0123456789ab")
        let removed = await waitFor { $0.path == "/api/v1/signin/profiles" && $0.method == "DELETE" }
        check(removed?.query == "id=sp_0123456789ab", "removing names the profile: \(String(describing: removed?.query))")
        check(await waitFor { $0.path == "/api/v1/signin" } != nil, "a sign-in change reloads the settings")

        let catalog = AgenthailModel(connecting: false)
        catalog.snapshot = model.snapshot.map { snapshot in
            var copy = snapshot
            copy.surfaces = [repairable]
            return copy
        }
        catalog.applyCatalogEvent(event(seq: 2, problem: "runtime-stopped"))
        check(catalog.snapshot?.surfaces.first?.repairAction == "runtime-ensure" && catalog.snapshot?.surfaces.first?.repairLabel == "Start managed runtime", "a health event with the same runtime problem keeps the repair")
        catalog.applyCatalogEvent(event(seq: 3, problem: "bridge-unavailable"))
        check(catalog.snapshot?.surfaces.first?.repairAction == nil && catalog.snapshot?.surfaces.first?.repairLabel == nil, "a different runtime problem drops the stale repair until the snapshot names the new one")
        print("settings requests tests passed")
    }

    private static func event(seq: Int, problem: String) -> CatalogStreamEvent {
        try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":\#(seq),"type":"surface.health","data":{"surface":"codex","health":"degraded","detail":"runtime down","runtime":{"name":"Codex app server","reachable":false,"durable":false,"problem":"\#(problem)"}}}"#.utf8))
    }

    @MainActor
    private static func waitFor(_ match: (RecordingProtocol.Request) -> Bool) async -> RecordingProtocol.Request? {
        for _ in 0..<40 {
            if let found = RecordingProtocol.requests.first(where: match) { return found }
            try? await Task.sleep(for: .milliseconds(50))
        }
        return nil
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}

final class RecordingProtocol: URLProtocol {
    struct Request {
        let method: String
        let path: String
        let query: String?
        let body: [String: Any]
        let idempotencyKey: String?
    }

    private static let lock = NSLock()
    nonisolated(unsafe) private static var recorded: [Request] = []
    nonisolated(unsafe) private static var failingPath: String?

    static var requests: [Request] { lock.withLock { recorded } }

    static func reset(failing path: String? = nil) {
        lock.withLock {
            recorded = []
            failingPath = path
        }
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let path = request.url?.path ?? ""
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
        let body = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        let failing = Self.lock.withLock {
            Self.recorded.append(Request(method: request.httpMethod ?? "GET", path: path, query: request.url?.query, body: body, idempotencyKey: request.value(forHTTPHeaderField: "Idempotency-Key")))
            return Self.failingPath == path
        }
        let (status, reply): (Int, String) = failing ? (502, "codex launch failed") : (200, Self.reply(for: path))
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": failing ? "text/plain" : "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(reply.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}

    private static func reply(for path: String) -> String {
        switch path {
        case "/api/v1/snapshot":
            return #"{"updatedAt":"2026-10-05T12:00:01Z","daemon":{"running":true,"pid":1},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5,"busyDelivery":"steer"}"#
        case "/api/v1/signin":
            return #"{"profiles":[{"id":"sp_0123456789ab","label":"Work Chrome","browser":"chrome","profile":"Profile 2","createdAt":"2026-10-18T00:00:00Z"}],"consumers":[{"id":"notion","label":"Notion","purpose":"p","profileId":null},{"id":"claude","label":"Claude Remote Control","purpose":"p","profileId":"sp_0123456789ab"}],"browsers":[{"browser":"chrome","label":"Google Chrome","available":true,"profiles":[{"profile":"Default","name":"Personal"},{"profile":"Profile 2","name":"Work","profileId":"sp_0123456789ab"}]},{"browser":"edge","label":"Microsoft Edge","available":false,"detail":"reading this browser's profiles did not finish in time","profiles":[]}]}"#
        case "/api/v1/settings":
            return #"{"dashboard":{"listen":"127.0.0.1:7412","codexRecentHours":12,"busyDelivery":"steer"},"remoteAccess":{"enabled":false,"desired":false,"provider":"tailscale","port":7412},"notifications":{"enabled":false,"available":true,"authorization":"unknown","authorized":false,"alerts":false,"sounds":false}}"#
        default:
            return #"{"ok":true}"#
        }
    }
}
