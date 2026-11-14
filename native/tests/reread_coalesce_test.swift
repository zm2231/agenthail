@testable import AgenthailHelpers
import Foundation

final class RereadDaemon: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var requests: [String] = []
    nonisolated(unsafe) static var held: DispatchSemaphore?
    nonisolated(unsafe) static var holding = false
    static let lock = NSLock()

    static func row(_ id: String) -> String {
        #"{"id":"\#(id)","surface":"claude","name":"\#(id)","status":"idle","lastActive":"2026-11-03T11:00:00Z","queueCount":0,"open":true,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false}}"#
    }

    static func page(_ ids: [String]) -> String {
        let families = ids.map { #"{"id":"\#($0)","rootId":"\#($0)","projectId":"","checkoutId":"","members":[{"id":"\#($0)","depth":0,"contextOnly":false,"pinned":false}],"pinned":false,"workingSubagents":0}"# }
        let collapsed = ids.map { #""\#($0)""# }.joined(separator: ",")
        let view = #"{"counts":{"all":{"families":\#(ids.count),"sessions":\#(ids.count)}},"surfaces":[],"projects":[{"id":"","name":"No project","counts":{"families":\#(ids.count),"sessions":\#(ids.count)},"collapsedFamilyIds":[\#(collapsed)]}],"families":[\#(families.joined(separator: ","))]}"#
        return #"{"sessions":[\#(ids.map(row).joined(separator: ","))],"hostEpoch":"e1","catalogSeq":5,"view":\#(view)}"#
    }

    static func rereads() -> Int {
        lock.withLock { requests.filter { $0.contains("scope=all") && $0.contains("limit=200") && !$0.contains("cursor") && !$0.contains("q=") }.count }
    }

    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "reread.test" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let url = request.url!
        let query = Dictionary(uniqueKeysWithValues: (URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []).map { ($0.name, $0.value ?? "") })
        Self.lock.withLock { Self.requests.append(url.path + "?" + (url.query ?? "")) }
        let body: String
        switch (url.path, query["scope"]) {
        case ("/api/v1/projects/activity", _): body = #"{"projects":[]}"#
        case ("/api/v1/history", _): body = #"{"items":[],"hasMore":false,"nextBefore":0,"kinds":[]}"#
        case ("/api/v1/snapshot", nil):
            body = #"{"updatedAt":"2026-11-03T12:00:00Z","hostEpoch":"e1","catalogSeq":5,"daemon":{"running":true,"pid":1,"stale":false},"surfaces":[],"projects":[],"queue":[],"channels":[],"relays":[],"codexRecentHours":5}"#
        case ("/api/v1/snapshot", _): body = Self.page(["live", "older"])
        default: body = "{}"
        }
        if query["scope"] == "all", query["limit"] == "200", let held = Self.lock.withLock({ Self.held }) {
            Self.lock.withLock { Self.holding = true }
            held.wait()
            Self.lock.withLock { Self.holding = false }
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}

@main
struct RereadCoalesceTest {
    static func check(_ condition: Bool, _ message: String) {
        guard !condition else { return }
        print("FAIL: \(message)")
        exit(1)
    }

    @MainActor
    static func settle(_ seconds: Double = 3, _ condition: () -> Bool) async -> Bool {
        let deadline = Date().addingTimeInterval(seconds)
        while !condition() && Date() < deadline { try? await Task.sleep(for: .milliseconds(10)) }
        return condition()
    }

    static func queued(_ seq: Int) -> CatalogStreamEvent {
        try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":\#(seq),"type":"session.queue","data":{"sessionId":"live"}}"#.utf8))
    }

    @MainActor
    final class Runs {
        var started = 0
        var finished = 0
        var held = false
        var most = 0
    }

    @MainActor
    static func cancelling() async {
        let runs = Runs()
        let coalescer = Coalescer(delay: .milliseconds(100)) {
            runs.started += 1
            runs.most = max(runs.most, runs.started - runs.finished)
            while runs.held, !Task.isCancelled { try? await Task.sleep(for: .milliseconds(5)) }
            runs.finished += 1
        }

        coalescer.request()
        let waiting = coalescer.task
        coalescer.cancel()
        check(await finished(waiting), "a run cancelled while waiting never stopped")
        check(runs.started == 0 && !coalescer.pending, "a cancel while waiting ran the work: \(runs.started)")
        coalescer.request()
        check(await settle { runs.finished == 1 }, "a request after a cancel while waiting starts fresh")
        check(!coalescer.pending, "the coalescer is idle after its run")

        runs.held = true
        coalescer.request()
        check(await settle { runs.started == 2 }, "the held run started")
        coalescer.cancel()
        coalescer.request()
        check(await settle { runs.started == 3 }, "a request after a cancel mid-work starts fresh: \(runs.started)")
        check(coalescer.pending, "the cancelled run cleared the fresh run's state")
        coalescer.request()
        runs.held = false
        check(await settle { runs.finished >= 3 }, "the cancelled and the fresh run both finish")
        check(await settle { runs.finished == runs.started && !coalescer.pending }, "the coalescer settles after the read news asked for")
        check(runs.started == 4 && runs.finished == 4 && !coalescer.pending, "news during the fresh run reads exactly once more: started \(runs.started), finished \(runs.finished), pending \(coalescer.pending)")
        check(runs.most == 1, "the cancelled run cleared the fresh run's state, so \(runs.most) runs overlapped")
    }

    @MainActor
    static func main() async {
        await cancelling()
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [RereadDaemon.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://reread.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        check(await model.refresh(), "the Mac reads its first snapshot")
        model.sessionFilter = .all
        check(await settle { model.knownSessions.contains { $0.id == "older" } }, "All reads its first page")

        let before = RereadDaemon.rereads()
        model.applyCatalogEvents([queued(6)])
        model.applyCatalogEvents([queued(7)])
        model.applyCatalogEvents([queued(8)])
        check(await settle { RereadDaemon.rereads() > before && !model.reader.rereadsPending }, "news rereads the listing")
        check(RereadDaemon.rereads() == before + 1, "one burst of news reread All \(RereadDaemon.rereads() - before) times")

        let gate = DispatchSemaphore(value: 0)
        RereadDaemon.lock.withLock { RereadDaemon.held = gate }
        let started = RereadDaemon.rereads()
        model.applyCatalogEvents([queued(9)])
        check(await settle { RereadDaemon.lock.withLock { RereadDaemon.holding } }, "the reread started")
        model.applyCatalogEvents([queued(10)])
        RereadDaemon.lock.withLock { RereadDaemon.held = nil }
        gate.signal()
        check(await settle { RereadDaemon.rereads() >= started + 2 && !model.reader.rereadsPending }, "news after a reread started reads again")
        check(RereadDaemon.rereads() == started + 2, "news after a reread started reread All \(RereadDaemon.rereads() - started) times, not twice")
        print("ok")
    }
}
