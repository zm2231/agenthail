@testable import AgenthailHelpers
import Foundation
import UserNotifications

final class RespondStub: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var bodies: [[String: Any]] = []
    nonisolated(unsafe) static var replies: [(Int, String)] = []
    nonisolated(unsafe) static var requests: [URLRequest] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        Self.requests.append(request)
        var data = request.httpBody ?? Data()
        if data.isEmpty, let stream = request.httpBodyStream {
            stream.open()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let count = stream.read(&buffer, maxLength: buffer.count)
                if count <= 0 { break }
                data.append(buffer, count: count)
            }
            stream.close()
        }
        Self.bodies.append((try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:])
        let (status, body) = Self.replies.isEmpty ? (200, "{}") : Self.replies.removeFirst()
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@main
struct RequestNotificationsTest {
    static func main() async {
        checkPayload()
        checkMerge()
        await checkFetch()
        await checkRespond()
        checkDecoding()
        print("request notifications tests passed")
    }

    static func checkPayload() {
        let body = #"{"sessionId":"s1","requests":[{"id":"other","prompt":"Not this","actions":[{"id":"allow","title":"Allow"}]},{"id":"req-1","kind":"question","prompt":"Which color?","actions":[{"id":"o0","title":"Red"},{"id":"o1","title":"Blue"}],"questions":[]}]}"#
        let notice = RequestNotifications.notice(from: Data(body.utf8), requestID: "req-1")
        check(notice == RequestNotice(prompt: "Which color?", actions: [RequestNotificationAction(id: "o0", title: "Red"), RequestNotificationAction(id: "o1", title: "Blue")]), "notice \(String(describing: notice))")
        check(RequestNotifications.notice(from: Data(body.utf8), requestID: "gone") == nil, "a request no longer open offers nothing")
        let four = #"{"requests":[{"id":"r","prompt":"Ask","actions":[{"id":"o0","title":"A"},{"id":"o1","title":"B"},{"id":"o2","title":"C"},{"id":"o3","title":"D"},{"id":"reply","title":"Reply…"}]}]}"#
        check(RequestNotifications.notice(from: Data(four.utf8), requestID: "r")?.actions.map(\.id) == ["o0", "o1", "o2", "o3", "reply"], "four options and a reply are all offered")
        for appOnly in [#"{"requests":[{"id":"r","prompt":"Which color?","actions":[]}]}"#, #"{"requests":[{"id":"r","prompt":"Which color?"}]}"#] {
            check(RequestNotifications.notice(from: Data(appOnly.utf8), requestID: "r") == RequestNotice(prompt: "Which color?", actions: []), "a request answered in the app still shows its prompt: \(appOnly)")
        }
        for bad in [
            #"{"requests":[{"id":"r","prompt":"Ask","actions":[{"id":"o9","title":"Nine"}]}]}"#,
            #"{"requests":[{"id":"r","prompt":"Ask","actions":[{"id":"o0","title":"A"},{"id":"o0","title":"B"}]}]}"#,
            #"{"requests":[{"id":"r","prompt":"Ask","actions":[{"id":"o0","title":"A"},{"id":"o1","title":"B"},{"id":"o2","title":"C"},{"id":"o3","title":"D"},{"id":"reply","title":"Reply…"},{"id":"deny","title":"Deny"}]}]}"#,
            #"{"requests":[{"id":"r","prompt":" ","actions":[{"id":"allow","title":"Allow"}]}]}"#,
            "not json"
        ] {
            check(RequestNotifications.notice(from: Data(bad.utf8), requestID: "r") == nil, "no notice from \(bad)")
        }
        check(RequestNotifications.choice(forAction: "always") == "always", "always is a choice")
        check(RequestNotifications.choice(forAction: UNNotificationDefaultActionIdentifier) == nil, "opening is not a choice")
        let category = RequestNotifications.category(requestID: "req-1", actions: [RequestNotificationAction(id: "allow", title: "Allow"), RequestNotificationAction(id: "deny", title: "Deny")])
        check(category.identifier == "AGENTHAIL_REQ_req-1" && category.actions.map(\.identifier) == ["allow", "deny"], "category \(category.identifier)")
        check(category.actions.allSatisfy { $0.options.contains(.authenticationRequired) }, "every answer needs the device unlocked")
        check(category.actions[1].options.contains(.destructive) && !category.actions[0].options.contains(.destructive), "only deny is destructive")
        let replying = RequestNotifications.category(requestID: "req-2", actions: [RequestNotificationAction(id: "o0", title: "Red"), RequestNotificationAction(id: "reply", title: "Reply…")])
        check(!(replying.actions[0] is UNTextInputNotificationAction) && (replying.actions[1] as? UNTextInputNotificationAction)?.textInputButtonTitle == "Send", "reply takes text")
        check(replying.actions[1].options.contains(.authenticationRequired), "a reply needs the device unlocked")
        check(RequestNotifications.choice(forAction: "reply") == "reply", "reply is a choice")
    }

    static func checkMerge() {
        let base = [UNNotificationCategory(identifier: "AGENTHAIL_SESSION", actions: [], intentIdentifiers: [])]
        let other = UNNotificationCategory(identifier: "OTHER", actions: [], intentIdentifiers: [])
        let staleBase = UNNotificationCategory(identifier: "AGENTHAIL_SESSION", actions: [UNNotificationAction(identifier: "old", title: "Old")], intentIdentifiers: [])
        let shown = RequestNotifications.category(requestID: "shown", actions: [RequestNotificationAction(id: "allow", title: "Allow")])
        let answered = RequestNotifications.category(requestID: "answered", actions: [RequestNotificationAction(id: "allow", title: "Allow")])
        let adding = RequestNotifications.category(requestID: "new", actions: [RequestNotificationAction(id: "deny", title: "Deny")])
        let merged = RequestNotifications.merged(existing: [other, staleBase, shown, answered], base: base, adding: adding, shown: ["shown"])
        let ids = Set(merged.map(\.identifier))
        check(ids == ["AGENTHAIL_SESSION", "OTHER", "AGENTHAIL_REQ_shown", "AGENTHAIL_REQ_new"], "merged ids \(ids)")
        check(merged.first { $0.identifier == "AGENTHAIL_SESSION" }?.actions.isEmpty == true, "the base category replaces an older copy of itself")

        let many = (0..<40).map { RequestNotifications.category(requestID: "r\($0)", actions: [RequestNotificationAction(id: "allow", title: "Allow")]) }
        let capped = RequestNotifications.merged(existing: Set(many), base: [], adding: adding, shown: (0..<40).map { "r\($0)" })
        check(capped.count == RequestNotifications.retainedRequests && capped.contains { $0.identifier == "AGENTHAIL_REQ_new" } && capped.contains { $0.identifier == "AGENTHAIL_REQ_r0" } && !capped.contains { $0.identifier == "AGENTHAIL_REQ_r39" }, "the newest requests are kept: \(capped.count)")

        let cleared = RequestNotifications.merged(existing: [shown, other], base: base, adding: nil, shown: [])
        check(Set(cleared.map(\.identifier)) == ["AGENTHAIL_SESSION", "OTHER"], "no request shown keeps no request category")
    }

    static func checkFetch() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [RespondStub.self]
        let session = URLSession(configuration: configuration)
        RespondStub.requests = []
        RespondStub.replies = [(200, #"{"requests":[{"id":"req-1","prompt":"Claude wants to run true","actions":[{"id":"allow","title":"Allow"},{"id":"deny","title":"Deny"}]}]}"#), (401, #"{"ok":false}"#)]
        let notice = await RequestNotifications.fetchNotice(endpoint: URL(string: "https://mac.test:7412")!, token: "token", sessionID: "s 1", requestID: "req-1", session: session)
        check(notice?.prompt == "Claude wants to run true" && notice?.actions.map(\.id) == ["allow", "deny"], "fetched \(String(describing: notice))")
        let sent = RespondStub.requests.last
        check(sent?.url?.absoluteString == "https://mac.test:7412/api/v1/requests?sessionId=s%201" && sent?.httpMethod == "GET" && sent?.value(forHTTPHeaderField: "Authorization") == "Bearer token", "fetch request \(String(describing: sent))")
        check(sent?.timeoutInterval == RequestNotifications.fetchTimeout && RequestNotifications.fetchTimeout <= 5, "fetch timeout \(String(describing: sent?.timeoutInterval))")
        check(RequestNotifications.fetchSession.configuration.timeoutIntervalForResource == RequestNotifications.fetchTimeout, "the whole fetch is bounded")
        let refused = await RequestNotifications.fetchNotice(endpoint: URL(string: "https://mac.test:7412")!, token: "old", sessionID: "s1", requestID: "req-1", session: session)
        check(refused == nil, "a refused fetch offers nothing")
    }

    static func checkRespond() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [RespondStub.self]
        let api = AgenthailAPI(baseURL: URL(string: "https://mac.test")!, token: "token", session: URLSession(configuration: configuration))
        RespondStub.replies = [(200, #"{"ok":true,"result":{"requestId":"req-1","outcome":"answered"}}"#), (409, #"{"ok":false,"error":{"code":"stale","message":"this request was already answered or has expired"}}"#)]
        let outcome = try? await api.respond(sessionID: "s1", requestID: "req-1", decision: .choice("o1"))
        check(outcome == "answered", "outcome \(String(describing: outcome))")
        let sent = RespondStub.bodies.last ?? [:]
        check(sent["action"] as? String == "respond" && sent["sessionId"] as? String == "s1" && sent["requestId"] as? String == "req-1" && (sent["decision"] as? [String: String]) == ["choice": "o1"], "respond body \(sent)")
        do {
            _ = try await api.respond(sessionID: "s1", requestID: "req-1", decision: .choice("o1"))
            check(false, "a stale answer succeeded")
        } catch AgenthailAPIError.requestStale(let message) {
            check(message.contains("already answered"), "stale message \(message)")
        } catch {
            check(false, "stale error \(error)")
        }
        RespondStub.replies = [(200, #"{"ok":true,"result":{"requestId":"req-2","outcome":"denied"}}"#)]
        _ = try? await api.respond(sessionID: "s1", requestID: "req-2", decision: RequestDecision(choice: "reply", message: "use staging"))
        check((RespondStub.bodies.last?["decision"] as? [String: String]) == ["choice": "reply", "message": "use staging"], "reply body \(RespondStub.bodies.last ?? [:])")
        RespondStub.replies = [(200, "{}")]
        try? await api.setPermissionMode(sessionID: "s1", mode: "plan")
        let mode = RespondStub.bodies.last ?? [:]
        check(mode["action"] as? String == "permission-mode" && mode["permissionMode"] as? String == "plan", "permission mode body \(mode)")
    }

    static func checkDecoding() {
        let json = #"{"permissionMode":"","requests":[{"id":"r1","kind":"approval","tool":"Bash","title":"Claude wants to run true","input":{"command":"true","n":1},"alwaysAllow":true,"createdAt":"2026-10-19T00:00:00Z"},{"id":"r2","kind":"question","tool":"AskUserQuestion","questions":[{"question":"Which color?","header":"Color","multiSelect":false,"options":[{"label":"Red","description":"Warm"}]}],"alwaysAllow":false,"createdAt":"2026-10-19T00:00:00Z"}]}"#
        guard let state = try? JSONDecoder().decode(SessionRequests.self, from: Data(json.utf8)) else { return check(false, "requests did not decode") }
        check(state.requests.count == 2 && state.requests[0].summary == "Claude wants to run true" && state.requests[0].alwaysAllow, "approval \(state.requests[0])")
        check(state.requests[0].input?.contains(#""command":"true""#) == true, "input \(String(describing: state.requests[0].input))")
        check(state.requests[1].isQuestion && state.requests[1].questions?.first?.options.first?.label == "Red", "question \(state.requests[1])")
        check(SessionRequests.permissionModeLabel(state.permissionMode) == "Default (Claude settings)", "default label")
        let capabilities = try? JSONDecoder().decode(Capabilities.self, from: Data(#"{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":true,"interrupt":true,"steer":true,"respond":true,"permissionMode":true}"#.utf8))
        check(capabilities?.respond == true && capabilities?.permissionMode == true && capabilities?.commands == false, "capabilities \(String(describing: capabilities))")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
