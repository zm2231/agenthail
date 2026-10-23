@testable import AgenthailHelpers
import Foundation

final class SnapshotTagProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var sentTags: [String?] = []
    nonisolated(unsafe) static var paths: [String] = []
    nonisolated(unsafe) static var currentTag = "\"one\""
    static let body = Data(#"{"updatedAt":"2026-10-23T00:00:00Z","eventCursor":1,"hostEpoch":"h","catalogSeq":4,"daemon":{"running":true,"pid":1,"stale":false},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5,"busyDelivery":"queue"}"#.utf8)

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let tag = request.value(forHTTPHeaderField: "If-None-Match")
        Self.sentTags.append(tag)
        Self.paths.append(request.url?.query.map { (request.url?.path ?? "") + "?" + $0 } ?? request.url?.path ?? "")
        let fresh = request.url?.query != nil
        let held = !fresh && tag == Self.currentTag
        let headers = fresh ? [:] : ["ETag": Self.currentTag]
        let response = HTTPURLResponse(url: request.url!, statusCode: held ? 304 : 200, httpVersion: "HTTP/1.1", headerFields: headers)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        if !held { client?.urlProtocol(self, didLoad: Self.body) }
        client?.urlProtocolDidFinishLoading(self)
    }
}

@main
struct SnapshotEtagTest {
    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }

    static func main() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [SnapshotTagProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let first = try? await api.snapshot()
        check(first?.catalogSeq == 4 && SnapshotTagProtocol.sentTags == [nil], "the first snapshot is fetched without a tag: \(SnapshotTagProtocol.sentTags)")
        let held = try? await api.snapshot()
        check(held?.catalogSeq == 4 && SnapshotTagProtocol.sentTags.last == "\"one\"", "a later snapshot names the one it holds and keeps it on 304: \(SnapshotTagProtocol.sentTags)")
        _ = try? await api.snapshot(fresh: true)
        check(SnapshotTagProtocol.paths.last == "/api/v1/snapshot?fresh=1" && SnapshotTagProtocol.sentTags.last == .some(nil), "a fresh snapshot names none: \(SnapshotTagProtocol.paths)")
        SnapshotTagProtocol.currentTag = "\"two\""
        let changed = try? await api.snapshot()
        check(changed?.catalogSeq == 4 && SnapshotTagProtocol.sentTags.last == "\"one\"", "a changed snapshot is fetched in full: \(SnapshotTagProtocol.sentTags)")
        _ = try? await api.snapshot()
        check(SnapshotTagProtocol.sentTags.last == "\"two\"", "the next snapshot names the newest one: \(SnapshotTagProtocol.sentTags)")
        print("ok")
    }
}
