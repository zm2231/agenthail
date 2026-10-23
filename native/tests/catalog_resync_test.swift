@testable import AgenthailHelpers
import Foundation

final class ResyncProtocol: URLProtocol, @unchecked Sendable {
    struct Host {
        var failures: Int
        var seq: Int
        var streams: [(status: Int, body: String)]
        var log: [String] = []
    }

    private static let lock = NSLock()
    nonisolated(unsafe) private static var hosts: [String: Host] = [:]

    static func log(_ host: String) -> [String] { lock.withLock { hosts[host]?.log ?? [] } }

    static func serve(_ host: String, failures: Int, seq: Int, streams: [(status: Int, body: String)]) {
        lock.withLock { hosts[host] = Host(failures: failures, seq: seq, streams: streams) }
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let url = request.url!
        let name = url.host ?? ""
        let after = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "after" }?.value ?? ""
        let snapshot = url.path == "/api/v1/snapshot"
        let (status, body): (Int, String) = Self.lock.withLock {
            var host = Self.hosts[name] ?? Host(failures: 0, seq: 0, streams: [])
            defer { Self.hosts[name] = host }
            if snapshot {
                if host.failures > 0 {
                    host.failures -= 1
                    host.log.append("snapshot failed")
                    return (500, #"{"error":{"message":"down"}}"#)
                }
                host.log.append("snapshot \(host.seq)")
                return (200, #"{"updatedAt":"2026-10-23T21:00:00Z","hostEpoch":"h","catalogSeq":\#(host.seq),"daemon":{"running":true,"pid":1,"stale":false},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5,"busyDelivery":"queue"}"#)
            }
            host.log.append("stream after \(after)")
            return host.streams.isEmpty ? (200, "") : host.streams.removeFirst()
        }
        let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": status == 200 && !snapshot ? "text/event-stream" : "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}

@main
struct CatalogResyncTest {
    static func listing(seq: UInt64) -> DashboardSnapshot {
        try! JSONDecoder().decode(DashboardSnapshot.self, from: Data(#"{"updatedAt":"2026-10-23T21:00:00Z","hostEpoch":"h","catalogSeq":\#(seq),"daemon":{"running":true,"pid":1,"stale":false},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5,"busyDelivery":"queue"}"#.utf8))
    }

    @MainActor
    static func model(_ host: String, bound: Int = 2000) -> AgenthailModel {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ResyncProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://\(host)")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        model.catalogInboxBound = bound
        model.adoptRefreshed(listing(seq: 10))
        return model
    }

    @MainActor
    static func waitForLog(_ host: String, _ expected: [String], within seconds: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(seconds)
        while Array(ResyncProtocol.log(host).prefix(expected.count)) != expected {
            if Date() > deadline { return false }
            RunLoop.main.run(until: Date().addingTimeInterval(0.01))
        }
        return true
    }

    @MainActor
    static func main() {
        let gap = #"{"error":{"code":"stream_gap","message":"cursor expired"}}"#
        ResyncProtocol.serve("gap.test", failures: 2, seq: 40, streams: [(409, gap)])
        let afterGap = model("gap.test")
        afterGap.startCatalogStream()
        let gapOrder = ["stream after 10", "snapshot failed", "snapshot failed", "snapshot 40", "stream after 40"]
        check(waitForLog("gap.test", gapOrder, within: 15), "after a stream gap the stream waits for a fresh snapshot, through failures: \(ResyncProtocol.log("gap.test"))")

        let session = #"{"id":"a","surface":"claude","name":"a","status":"busy","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":true,"steer":true}}"#
        let events = (11...60).map { #"data: {"stream":"catalog","seq":\#($0),"type":"session.upserted","data":{"session":\#(session)}}"# + "\n\n" }.joined()
        ResyncProtocol.serve("overflow.test", failures: 1, seq: 60, streams: [(200, events)])
        _ = afterGap
        let overflowed = model("overflow.test", bound: 3)
        overflowed.startCatalogStream()
        let overflowOrder = ["stream after 10", "snapshot failed", "snapshot 60", "stream after 60"]
        check(waitForLog("overflow.test", overflowOrder, within: 15), "an overflowed inbox resumes only after a fresh snapshot: \(ResyncProtocol.log("overflow.test"))")
        print("catalog resync tests passed")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
