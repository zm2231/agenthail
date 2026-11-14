@testable import AgenthailHelpers
import Foundation

final class BriefStub: URLProtocol {
    nonisolated(unsafe) static var path: String?
    nonisolated(unsafe) static var method: String?
    nonisolated(unsafe) static var key: String?
    nonisolated(unsafe) static var hadBody = false
    nonisolated(unsafe) static var status = 200
    nonisolated(unsafe) static var answer = Data()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        BriefStub.path = request.url.map { $0.absoluteString.replacingOccurrences(of: "http://daemon.invalid", with: "") }
        BriefStub.method = request.httpMethod
        BriefStub.key = request.value(forHTTPHeaderField: "Idempotency-Key")
        BriefStub.hadBody = request.httpBody != nil || request.httpBodyStream != nil
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: BriefStub.status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: BriefStub.answer)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

func check(_ condition: Bool, _ message: String) {
    guard condition else {
        print("FAIL: \(message)")
        exit(1)
    }
}

func reply(_ json: String) -> ProjectBriefReply {
    try! JSONDecoder().decode(ProjectBriefReply.self, from: Data(json.utf8))
}

@main
struct ProjectBriefTest {
    @MainActor
    static func main() async {
        check(ProjectBrief.path(projectID: "repo:github.com/acme/web") == "/api/v1/projects/repo:github.com%2Facme%2Fweb/brief", "ids holding / are path-escaped: \(ProjectBrief.path(projectID: "repo:github.com/acme/web"))")
        check(ProjectBrief.path(projectID: "h-server::folder:a b?c#d%") == "/api/v1/projects/h-server::folder:a%20b%3Fc%23d%25/brief", "query, fragment and percent are escaped: \(ProjectBrief.path(projectID: "h-server::folder:a b?c#d%"))")

        let utc = TimeZone(identifier: "UTC")!
        let posix = Locale(identifier: "en_US_POSIX")
        let since = reply(#"{"ok":true,"ref":"@lead-web","result":{"status":"sent","evidence":"transport_accepted","sessionId":"s1","deliveryId":12},"started":false,"since":{"at":"2026-11-14T15:02:19.123Z","fallback":false}}"#)
        let sinceText = ProjectBrief.notice(since, lead: "lead", timeZone: utc, locale: posix).replacingOccurrences(of: "\u{202F}", with: " ")
        check(sinceText == "Asked @lead-web to catch you up since Nov 14, 2026 at 3:02 PM." || sinceText == "Asked @lead-web to catch you up since Nov 14, 2026, 3:02 PM.", "since names the window start: \(sinceText)")
        let fallback = ProjectBrief.notice(reply(#"{"ref":"vps::@gadget-lead","result":{"evidence":"queued"},"since":{"at":"2026-11-13T15:02:19Z","fallback":true}}"#), lead: "gadget-lead")
        check(fallback == "Asked vps::@gadget-lead to catch you up on the last 24 hours. It replies after its current turn.", "fallback and queued: \(fallback)")
        let held = ProjectBrief.notice(reply(#"{"ok":true,"ref":"vps::@gadget-lead","result":{"evidence":"held"}}"#), lead: "gadget-lead")
        check(held == "Asked vps::@gadget-lead to catch you up.", "held without since: \(held)")
        let started = ProjectBrief.notice(reply(#"{"ok":true,"started":true,"since":{"at":"2026-11-13T15:02:19Z","fallback":true}}"#), lead: "lead")
        check(started == "Asked @lead to catch you up on the last 24 hours. This starts its conversation.", "missing ref falls back to the lead name: \(started)")

        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [BriefStub.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://daemon.invalid")!, token: "t", session: URLSession(configuration: config))
        BriefStub.answer = Data(#"{"ok":true,"ref":"@lead-web","result":{"status":"sent","evidence":"transport_accepted"},"started":false,"since":{"at":"2026-11-14T15:02:19Z","fallback":false}}"#.utf8)
        let answered = try! await api.projectBrief(projectID: "repo:github.com/acme/web", idempotencyKey: "k1")
        check(BriefStub.method == "POST", "brief is a POST")
        check(BriefStub.path == "/api/v1/projects/repo:github.com%2Facme%2Fweb/brief", "escaped path reaches the server: \(BriefStub.path ?? "nil")")
        check(BriefStub.key == "k1", "the Idempotency-Key is sent")
        check(!BriefStub.hadBody, "a brief sends no body")
        check(answered.since?.fallback == false && answered.ref == "@lead-web", "reply decodes")

        BriefStub.status = 409
        BriefStub.answer = Data(#"{"ok":false,"error":{"code":"no_lead","message":"web has no lead"}}"#.utf8)
        let ask = ProjectBriefAsk()
        var keys: [String] = []
        await ask.ask { key in
            keys.append(key)
            return try await api.projectBrief(projectID: "web", idempotencyKey: key).ref ?? ""
        }
        check(ask.failure == "web has no lead" && ask.notice == nil && !ask.asking, "409 no_lead shows the server's message: \(ask.failure ?? "nil")")
        await ask.ask { key in
            keys.append(key)
            return "sent"
        }
        check(ask.notice == "sent" && ask.failure == nil, "a retry clears the failure")
        check(keys[0] == keys[1], "a retry after a failure reuses its key, so a request that did land is not asked twice")
        await ask.ask { key in
            keys.append(key)
            return "again"
        }
        check(keys[2] != keys[1], "a new ask after a success gets a new key")

        let gate = AsyncStream<Void>.makeStream()
        var runs = 0
        let first = Task { @MainActor in
            await ask.ask { _ in
                runs += 1
                for await _ in gate.stream { break }
                return "one"
            }
        }
        while !ask.asking { await Task.yield() }
        await ask.ask { _ in
            runs += 1
            return "two"
        }
        gate.continuation.yield()
        await first.value
        check(runs == 1 && ask.notice == "one", "a second tap while asking sends nothing: runs=\(runs)")
        print("project_brief_test: PASS")
    }
}
