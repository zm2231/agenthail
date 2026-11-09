@testable import AgenthailHelpers
import Foundation

final class EmptyDecisionsDaemon: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let found = request.url?.path == "/api/v1/decisions"
        let body = found ? #"{"decisions":[],"leadHandling":[]}"# : #"{"error":{"code":"unknown_route","message":"not in this test"}}"#
        let response = HTTPURLResponse(url: request.url!, statusCode: found ? 200 : 404, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@main
struct DecisionsKeyTest {
    static func row(_ id: String, lastActive: String) -> String {
        #"{"id":"\#(id)","surface":"claude","name":"\#(id)","status":"busy","lastActive":"\#(lastActive)","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":true,"steer":true}}"#
    }

    static func event(_ seq: UInt64, _ type: String, _ data: String) -> CatalogStreamEvent {
        try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":\#(seq),"type":"\#(type)","data":\#(data)}"#.utf8))
    }

    static func snapshot(rev: String?) -> DashboardSnapshot {
        DashboardSnapshot(updatedAt: "2026-11-08T08:00:00Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: [], totalSessions: 0, queue: [], channels: [], relays: [], history: [], attention: [], deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "queue", decisionsRev: rev)
    }

    @MainActor
    static func main() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [EmptyDecisionsDaemon.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)

        model.snapshot = snapshot(rev: "r1")
        let quiet = model.decisionsKey
        for second in 1...5 {
            let session = row("streaming", lastActive: "2026-11-08T08:00:0" + String(second) + "Z")
            model.applyCatalogEvents([event(UInt64(1 + second), "session.upserted", #"{"session":\#(session)}"#)])
        }
        check(model.snapshot?.sessions.first?.lastActive == "2026-11-08T08:00:05Z", "the progress landed")
        check(model.decisionsKey == quiet, "a session's progress does not ask for decisions again: \(quiet) then \(model.decisionsKey)")

        model.applyCatalogEvents([event(7, "decisions.changed", #"{"rev":"r2","own":"o2"}"#)])
        check(model.decisionsKey != quiet && model.decisionsKey.hasPrefix("r2"), "a decisions change does: \(model.decisionsKey)")

        let current = await model.loadDecisions()
        check(current.outdated.isEmpty, "a host that names its revision is current: \(current.outdated)")
        model.snapshot = snapshot(rev: nil)
        let older = await model.loadDecisions()
        check(older.outdated == ["This Mac"], "a daemon without a revision is shown as needing an update: \(older.outdated)")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
