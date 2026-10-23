@testable import AgenthailHelpers
import Foundation

@main
struct ClaudeSubagentListTest {
    static func main() async {
        let links = (0..<12).map { index in
            link("agent-\(index)", working: index >= 10, minutesAgo: index, description: index == 0 ? "Review the parser" : nil, type: index == 1 ? "Explore" : nil)
        }
        let paged = ClaudeSubagentList(links, total: 30, expanded: false)
        check(paged.shown.map(\.agentId) == ["agent-10", "agent-11"] + (0..<8).map { "agent-\($0)" }, "working subagents come first, then the most recent: \(paged.shown.map(\.agentId))")
        check(paged.working == 2 && paged.total == 30 && paged.earlier == 20, "the server total counts what is not shown: \(paged.earlier)")
        check(paged.summary == "2 working, 30 subagents in all" && paged.earlierLabel == "Show 20 earlier", "summary: \(paged.summary)")

        let older = ClaudeSubagentList(links, total: nil, expanded: false)
        check(older.total == 12 && older.shown.count == 10 && older.earlier == 2, "a host without a total has sent every subagent, and the client collapses them itself")
        let expanded = ClaudeSubagentList(links, total: 12, expanded: true)
        check(expanded.shown.count == 12 && expanded.earlier == 0, "an expanded list shows every subagent")
        check(ClaudeSubagentList([], total: 0, expanded: false).summary == "0 subagents in all", "an empty list reads as none")

        check(links[0].title == "Review the parser" && links[0].kindLabel == nil, "the description is the title")
        check(links[1].title == "Explore" && links[1].kindLabel == nil, "without a description the type is the title")
        check(links[2].title == "Subagent agent-2", "without either the title is the short ID: \(links[2].title)")
        let described = link("x", working: false, minutesAgo: 0, description: "Fix it", type: "general-purpose")
        check(described.kindLabel == "general-purpose", "a described subagent shows its type beside the title")

        let legacy = #"{"sessionId":"s","claudeSubagents":[{"parentSessionId":"s","agentId":"a","depth":0,"working":false,"transcriptPath":"/tmp/a.jsonl"}]}"#
        let decoded = try? JSONDecoder().decode(SessionMetadata.self, from: Data(legacy.utf8))
        check(decoded?.claudeSubagents?.count == 1 && decoded?.claudeSubagentsTotal == nil, "metadata from an older host decodes without a total")
        let current = #"{"sessionId":"s","claudeSubagents":[],"claudeSubagentsTotal":7}"#
        check((try? JSONDecoder().decode(SessionMetadata.self, from: Data(current.utf8)))?.claudeSubagentsTotal == 7, "the total decodes")

        var overlay = MetadataOverlay()
        check(!overlay.allClaudeSubagents, "a session opens with its subagent history collapsed")
        overlay.showAllClaudeSubagents()
        overlay.absorb(try! JSONDecoder().decode(SessionMetadata.self, from: Data(current.utf8)))
        check(overlay.allClaudeSubagents && overlay.claudeSubagentsTotal == 7, "showing all survives a metadata reload")

        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [QueryProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        _ = try? await api.sessionMetadata(id: "s")
        _ = try? await api.sessionMetadata(id: "s", allSubagents: true)
        check(QueryProtocol.queries == ["id=s", "id=s&subagents=all"], "only an expanded list asks for every subagent: \(QueryProtocol.queries)")
        print("claude subagent list tests passed")
    }

    static func link(_ id: String, working: Bool, minutesAgo: Int, description: String? = nil, type: String? = nil) -> ClaudeSubagentLink {
        let lastActive = ISO8601DateFormatter().string(from: Date(timeIntervalSince1970: 1_800_000_000 - Double(minutesAgo * 60)))
        return ClaudeSubagentLink(parentSessionId: "s", agentId: id, agentType: type, description: description, working: working, lastActive: lastActive, transcriptPath: "/tmp/\(id).jsonl")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}

final class QueryProtocol: URLProtocol {
    nonisolated(unsafe) static var queries: [String] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        Self.queries.append(request.url?.query ?? "")
        let body = Data(#"{"sessionId":"s"}"#.utf8)
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: body)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
