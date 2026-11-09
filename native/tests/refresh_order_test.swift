@testable import AgenthailHelpers
import Foundation

final class OrderDaemon: URLProtocol, @unchecked Sendable {
    struct Reply {
        var hours: Int?
        var hold = false
    }

    nonisolated(unsafe) static var plan: [Reply] = []
    nonisolated(unsafe) static var held: [(OrderDaemon, Reply)] = []
    static let lock = NSLock()

    static func queue(_ replies: Reply...) { lock.withLock { plan = replies } }
    static var holding: Int { lock.withLock { held.count } }

    static func release() {
        let waiting = lock.withLock { defer { held = [] }; return held }
        for (request, reply) in waiting { request.answer(reply) }
    }

    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "order.test" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() { Self.lock.withLock { Self.held.removeAll { $0.0 === self } } }

    override func startLoading() {
        let url = request.url!
        let scope = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "scope" }?.value
        switch (url.path, scope) {
        case ("/api/v1/snapshot", nil):
            let reply = Self.lock.withLock { () -> Reply? in
                guard !Self.plan.isEmpty else { return nil }
                let reply = Self.plan.removeFirst()
                if reply.hold { Self.held.append((self, reply)) }
                return reply
            }
            if let reply, !reply.hold { answer(reply) }
            if reply == nil { send(500, #"{"error":{"message":"no reply planned"}}"#) }
        case ("/api/v1/snapshot", _):
            send(200, #"{"sessions":[],"hostEpoch":"e1","catalogSeq":5,"view":{"counts":{},"surfaces":[],"projects":[],"families":[]}}"#)
        case ("/api/v1/projects/activity", _):
            send(200, #"{"projects":[]}"#)
        case ("/api/v1/history", _):
            send(200, #"{"items":[],"hasMore":false,"nextBefore":0,"kinds":[]}"#)
        default:
            send(200, "{}")
        }
    }

    func answer(_ reply: Reply) {
        guard let hours = reply.hours else { return send(500, #"{"error":{"message":"base failed"}}"#) }
        send(200, #"{"updatedAt":"2026-11-09T12:00:00Z","hostEpoch":"e1","catalogSeq":5,"daemon":{"running":true,"pid":1,"stale":false},"surfaces":[],"queue":[],"channels":[],"relays":[],"attention":[],"codexRecentHours":\#(hours)}"#)
    }

    private func send(_ status: Int, _ body: String) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}

@main
struct RefreshOrderTest {
    static func check(_ condition: Bool, _ message: String) {
        guard !condition else { return }
        print("FAIL: \(message)")
        exit(1)
    }

    @MainActor
    static func held() async -> Bool {
        let deadline = Date().addingTimeInterval(5)
        while OrderDaemon.holding == 0 && Date() < deadline { try? await Task.sleep(for: .milliseconds(5)) }
        return OrderDaemon.holding == 1
    }

    @MainActor
    static func main() async {
        var order = RefreshOrder()
        let first = order.begin(), second = order.begin(), third = order.begin()
        check(order.adopts(second) && !order.adopts(first), "an older response is not shown over a newer one")
        check(order.settles(third) && !order.settles(second), "an older outcome does not replace a newer one's")
        check(order.adopts(third), "a newer response is shown")

        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [OrderDaemon.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://order.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        let hours = { model.snapshot?.codexRecentHours }

        OrderDaemon.queue(.init(hours: 1))
        check(await model.refresh(), "the Mac reads its first snapshot")
        check(hours() == 1, "the first snapshot is shown")

        OrderDaemon.queue(.init(hours: 2, hold: true), .init(hours: 3))
        var older = Task { await model.refresh() }
        check(await held(), "the older refresh is waiting")
        check(await model.refresh() && hours() == 3, "the newer refresh is shown")
        OrderDaemon.release()
        _ = await older.value
        check(hours() == 3, "an older refresh that answered later replaced the newer snapshot: \(String(describing: hours()))")

        OrderDaemon.queue(.init(hours: nil, hold: true), .init(hours: 4))
        older = Task { await model.refresh() }
        check(await held(), "the older failing refresh is waiting")
        check(await model.refresh() && hours() == 4 && model.connectionError == nil, "the newer refresh succeeds")
        OrderDaemon.release()
        check(await !older.value, "the older refresh fails")
        check(model.connectionError == nil, "an older refresh that failed later showed an error over a newer success: \(model.connectionError ?? "")")

        OrderDaemon.queue(.init(hours: 5, hold: true), .init(hours: nil))
        older = Task { await model.refresh() }
        check(await held(), "the older succeeding refresh is waiting")
        check(await !model.refresh() && model.connectionError != nil, "the newer refresh fails and says so")
        OrderDaemon.release()
        check(await older.value, "the older refresh succeeds")
        check(hours() == 5, "an older success newer than what is shown is shown: \(String(describing: hours()))")
        check(model.connectionError != nil, "an older success cleared the newer failure")

        OrderDaemon.queue(.init(hours: 6))
        check(await model.refresh() && model.connectionError == nil, "a newer success clears the failure")
        OrderDaemon.queue(.init(hours: 7, hold: true))
        older = Task { await model.refresh() }
        check(await held(), "the refresh to cancel is waiting")
        older.cancel()
        check(await !older.value, "the cancelled refresh reports no snapshot")
        check(model.connectionError == nil, "a cancelled refresh showed an error: \(model.connectionError ?? "")")
        check(hours() == 6, "a cancelled refresh changed the snapshot")
        print("ok   refresh_order_test")
    }
}
