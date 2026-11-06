@testable import AgenthailHelpers
import Foundation

final class EventsDaemon: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var requests: [String] = []
    static let lock = NSLock()

    static func count(_ path: String) -> Int { lock.withLock { requests.filter { $0 == path }.count } }

    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "events.test" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let url = request.url!
        let scope = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "scope" }?.value
        Self.lock.withLock { Self.requests.append(url.path + (scope.map { "?" + $0 } ?? "")) }
        let view = #"{"counts":{},"surfaces":[],"projects":[],"families":[]}"#
        let body: String
        switch (url.path, scope) {
        case ("/api/v1/projects/activity", _): body = #"{"projects":[]}"#
        case ("/api/v1/history", _): body = #"{"items":[],"hasMore":false,"nextBefore":0,"kinds":[]}"#
        case ("/api/v1/devices", _): body = #"{"devices":[]}"#
        case ("/api/v1/snapshot", nil): body = #"{"updatedAt":"2026-11-03T12:00:00Z","hostEpoch":"e1","catalogSeq":5,"daemon":{"running":true,"pid":1,"stale":false},"surfaces":[],"queue":[],"channels":[],"relays":[],"attention":[],"codexRecentHours":5}"#
        case ("/api/v1/snapshot", _): body = #"{"sessions":[],"hostEpoch":"e1","catalogSeq":5,"view":\#(view)}"#
        default: body = "{}"
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}

@main
struct MacEventRefreshTest {
    static func check(_ condition: Bool, _ message: String) {
        guard !condition else { return }
        print("FAIL: \(message)")
        exit(1)
    }

    @MainActor
    static func settle(_ condition: () -> Bool) async -> Bool {
        let deadline = Date().addingTimeInterval(3)
        while !condition() && Date() < deadline { try? await Task.sleep(for: .milliseconds(10)) }
        return condition()
    }

    static func event(_ id: UInt64, _ type: String) -> AgenthailEvent {
        AgenthailEvent(id: id, type: type, timestamp: "2026-11-03T12:00:00Z", entityId: "s1")
    }

    @MainActor
    static func main() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [EventsDaemon.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://events.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        check(await model.refresh(), "the Mac reads its first snapshot")
        let base = { EventsDaemon.count("/api/v1/snapshot") }

        let history = { EventsDaemon.count("/api/v1/history") }

        for (index, type) in ["ledger.check.started", "ledger.gate", "session.updated", "turn.completed", "task.settled"].enumerated() {
            model.receive(event(UInt64(10 + index), type))
            check(model.pendingRefresh.isEmpty, "\(type) schedules no reread")
        }
        check(model.lastEventID == 14, "every event still moves the cursor: \(model.lastEventID)")

        for type in ["state.changed", "settings.updated", "signin.changed", "host.state", "host.removed", "stream.reset"] {
            let (read, served) = (base(), history())
            model.receive(event(20, type))
            check(!model.pendingRefresh.isEmpty, "\(type) schedules a reread")
            check(await settle { base() == read + 1 && history() > served }, "\(type) rereads the snapshot")
            check(base() == read + 1, "\(type) rereads it once")
        }

        let (read, served) = (base(), history())
        let devices = EventsDaemon.count("/api/v1/devices")
        model.receive(event(30, "device.paired"))
        check(!model.pendingRefresh.snapshot && model.pendingRefresh.operations, "a device event waits to reload Operations only")
        model.receive(event(31, "state.changed"))
        check(await settle { base() == read + 1 && history() > served && EventsDaemon.count("/api/v1/devices") == devices + 1 }, "a later event inside the wait keeps what an earlier one asked for")
        print("ok   mac_event_refresh_test")
    }
}
