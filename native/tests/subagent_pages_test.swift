@testable import AgenthailHelpers
import Foundation

@main
struct SubagentPagesTest {
    static func main() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [PageProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))

        let loaded = try! await api.subagents(of: "root")
        check(loaded.map(\.id) == ["a", "b", "c"], "every page is read: \(loaded.map(\.id))")
        let first = "parentId=root&limit=200", next = "parentId=root&limit=200&cursor=next-1"
        check(PageProtocol.queries == [first, next, first, next], "a changed catalog restarts from the first page and follows the cursor: \(PageProtocol.queries)")

        var cache = SubagentPageCache()
        let parent = session("root", hidden: 1)
        check(cache.loading(parent), "a parent with hidden subagents and no page is loading")
        check(!cache.loading(session("root")), "a parent without hidden subagents never loads")
        cache.store([session("A", parent: "root")], for: "root")
        check(!cache.loading(parent) && cache.session("A") != nil, "a stored page can be shown and opened")
        let before = SubagentFetchKey(parent, revision: cache.revision("root"))
        cache.sessionRemoved("B", unlisted: true)
        check(SubagentFetchKey(parent, revision: cache.revision("root")) != before, "a hidden subagent appearing under an unknown parent refetches the cached pages")
        cache.sessionRemoved("A", unlisted: false)
        check(cache.children(of: "root").isEmpty && cache.session("A") == nil, "a removed subagent leaves the page at once")
        cache.store([session("B", parent: "root")], for: "root")
        check(cache.children(of: "root").map(\.id) == ["B"] && cache.session("A") == nil, "swapping A for B at the same count shows B only")
        let served = SubagentFetchKey(parent, revision: cache.revision("root"))
        cache.refetch("root")
        check(!cache.holds("root") && cache.loading(parent) && SubagentFetchKey(parent, revision: cache.revision("root")) != served, "a page whose serving computer changed is fetched again")
        cache.store([session("B", parent: "root")], for: "root")

        let listedB = session("B", parent: "root", status: "busy")
        let merged = SessionFamilies.children(of: "root", listed: [listedB], fetched: cache.children(of: "root"))
        check(merged.map(\.status) == ["busy"], "the live listed row replaces its fetched copy")

        for id in ["p1", "p2", "p3", "p4"] { cache.store([session(id + "-child", parent: id)], for: id) }
        check(cache.session("B") == nil && cache.session("p4-child") != nil, "only the most recent \(SubagentPageCache.capacity) parents keep their pages")
        cache.fail("unreachable", for: "p5")
        check(cache.errors["p5"] == "unreachable" && cache.session("p1-child") == nil, "a failed parent also counts toward the bound")
        cache.sessionRemoved("p4", unlisted: false)
        check(cache.session("p4-child") == nil, "a removed parent drops its page")

        var pinned = session("child", parent: "root", status: "busy")
        pinned.listed = false
        let family = SessionFamilies.build([session("root", hidden: 2, hiddenWorking: 1), pinned])[0]
        check(family.members.map(\.id) == ["child"] && family.workingSubagents == 1, "a pinned hidden subagent is shown but already counted by its parent: \(family.workingSubagents)")
        print("subagent pages tests passed")
    }

    static func session(_ id: String, parent: String? = nil, hidden: Int = 0, hiddenWorking: Int = 0, status: String = "idle") -> SessionState {
        var json: [String: Any] = ["id": id, "surface": "codex", "name": id, "status": status, "queueCount": 0, "open": false, "current": false,
                                   "capabilities": ["send": true, "stream": true, "reply": true, "goal": true, "compact": true, "model": true, "interrupt": true, "steer": true]]
        if let parent { json["subagent"] = ["parentId": parent, "rootId": parent, "depth": 1] }
        if hidden > 0 {
            json["hiddenSubagents"] = ["count": hidden, "working": hiddenWorking]
            json["subagents"] = ["count": hidden, "working": hiddenWorking]
        }
        return try! JSONDecoder().decode(SessionState.self, from: JSONSerialization.data(withJSONObject: json))
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}

final class PageProtocol: URLProtocol {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var recorded: [String] = []
    nonisolated(unsafe) private static var conflicted = false

    static var queries: [String] { lock.withLock { recorded } }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let query = request.url?.query ?? ""
        let (status, body): (Int, String) = Self.lock.withLock {
            Self.recorded.append(query)
            if query.contains("cursor=next-1") {
                if !Self.conflicted {
                    Self.conflicted = true
                    return (409, #"{"error":{"code":"catalog_changed","message":"Reload the catalog before continuing."}}"#)
                }
                return (200, Self.page(["c"], next: nil))
            }
            return (200, Self.page(["a", "b"], next: "next-1"))
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}

    static func page(_ ids: [String], next: String?) -> String {
        let sessions = ids.map { #"{"id":"\#($0)","surface":"codex","name":"\#($0)","status":"idle","queueCount":0,"open":false,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":true,"compact":true,"model":true,"interrupt":true,"steer":true},"subagent":{"parentId":"root","rootId":"root","depth":1}}"# }
        let cursor = next.map { #","nextCursor":"\#($0)""# } ?? ""
        return #"{"updatedAt":"now","daemon":{"running":true},"surfaces":[],"sessions":[\#(sessions.joined(separator: ","))],"totalSessions":3\#(cursor),"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":24}"#
    }
}