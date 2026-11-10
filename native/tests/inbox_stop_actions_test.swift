@testable import AgenthailHelpers
import Foundation

final class StopDaemon: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var posts: [[String: Any]] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let (status, body): (Int, String)
        if request.url?.path == "/api/v1/actions" {
            let data = request.httpBody ?? request.httpBodyStream.map(StopStream.read) ?? Data()
            Self.posts.append((try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:])
            (status, body) = (200, #"{"ok":true}"#)
        } else {
            (status, body) = (404, #"{"error":{"code":"unknown_route","message":"not in this test"}}"#)
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

enum StopStream {
    static func read(_ stream: InputStream) -> Data {
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            if count <= 0 { break }
            data.append(buffer, count: count)
        }
        return data
    }
}

@main
struct InboxStopActionsTest {
    static let snapshot = #"""
    {"updatedAt":"now","daemon":{"running":true},"surfaces":[],"sessions":[],"totalSessions":0,
     "queue":[{"id":7,"sessionId":"s-web","target":"fix the build","message":"Run the migration after the backup","status":"dead","evidence":"unknown","attempts":1,"queuedAt":"2026-11-08T09:59:00Z"}],"channels":[],"relays":[],"history":[],"codexRecentHours":24,
     "leadReplacements":[{"id":5,"projectId":"api","name":"lead","sessionId":"s-api-2","previousSessionId":"s-api","changedBy":"you","reason":"context full","at":"2026-11-08T10:10:00Z"},
                         {"id":6,"projectId":"web","name":"lead","sessionId":"s-web-2","previousSessionId":"s-web","changedBy":"you","reason":"context full","at":"2026-11-08T10:11:00Z"}]}
    """#

    @MainActor
    static func main() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StopDaemon.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        model.snapshot = try JSONDecoder().decode(DashboardSnapshot.self, from: Data(snapshot.utf8))

        let stops = model.inboxStops
        check(stops.stopped.map(\.id) == ["replacement:/5", "replacement:/6"], "a dead queue row is not a stop of its own; its ask is a decision: \(stops.stopped.map(\.id))")
        check(stops.count == 2 && model.needsYouCount == 2, "the window chrome and status bar give the Inbox's answer: \(model.needsYouCount)")
        check(stops.entries.map(\.paneID) == stops.stopped.map(\.id), "the pane walks the same rows")
        check(stops.following(stops.entries[0]) == "replacement:/6", "after a row, the next one is picked")

        guard case .replacement(let first) = stops.stopped[0], case .replacement(let second) = stops.stopped[1] else {
            check(false, "the rows come in their kinds")
            return
        }
        model.acknowledgeLeadReplacement(first)
        model.acknowledgeLeadReplacement(second)
        check(model.inboxStops.count == 0, "every handled stop leaves the Inbox")
        await until { StopDaemon.posts.count >= 2 }
        let sent = StopDaemon.posts.map { post in "\(post["action"] ?? "")#\(post["replacementId"] ?? "")" }.sorted()
        check(sent == ["lead-replacement-ack#5", "lead-replacement-ack#6"], "each card button asks the daemon: \(sent)")
        print("inbox stop actions ok")
    }

    @MainActor
    static func until(_ condition: () -> Bool) async {
        for _ in 0..<200 where !condition() { try? await Task.sleep(for: .milliseconds(10)) }
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
