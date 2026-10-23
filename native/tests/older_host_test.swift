@testable import AgenthailHelpers
import Foundation

final class OlderHostStub: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var status = 404
    nonisolated(unsafe) static var body = #"{"error":{"code":"unknown_route","message":"This Agenthail has no /api/v1/history; it runs an older version."}}"#

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let response = HTTPURLResponse(url: request.url!, statusCode: Self.status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(Self.body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@main
struct OlderHostTest {
    static func main() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [OlderHostStub.self]
        let session = URLSession(configuration: configuration)
        let base = URL(string: "http://127.0.0.1:7499")!
        let api = AgenthailAPI(baseURL: base, token: "t", session: session)
        let voice = try! VoiceAPI(endpoint: base, token: "t", session: session)
        let calls: [(String, () async throws -> Void)] = [
            ("a request", { _ = try await api.history() }),
            ("an attachment", { _ = try await api.sessionAttachment(sessionID: "s", id: "a") }),
            ("the event stream", { try await api.streamEvents(after: 0, onConnected: {}, onEvent: { _ in }) }),
            ("a session stream", { try await api.streamSession(id: "s", after: 0, onConnected: {}, onEvent: { _ in }) }),
            ("the catalog stream", { try await api.streamCatalog(after: 0, onConnected: {}, onEvent: { _ in }) }),
            ("voice", { _ = try await voice.state() }),
            ("pairing", { _ = try await AgenthailAPI.completePairing(endpoint: base, secret: "s", name: "n", session: session) }),
        ]
        for (name, call) in calls {
            do {
                try await call()
                fail("\(name) to a route the host does not have must fail")
            } catch AgenthailAPIError.olderHost {
            } catch {
                fail("\(name) to a route the host does not have says the host is older, not \(error)")
            }
        }
        check(AgenthailAPIError.olderHost.localizedDescription == "This computer runs an older Agenthail. Update it to use this.", "the older-host message tells the person to update that computer")

        OlderHostStub.body = #"{"error":{"code":"not_found","message":"No such session."}}"#
        do {
            _ = try await api.history()
            fail("a missing resource must fail")
        } catch AgenthailAPIError.request(404, let message) {
            check(message == "No such session.", "a 404 for a missing resource keeps its own message: \(message)")
        } catch {
            fail("a missing resource is a plain request error, not \(error)")
        }
        do {
            try await api.streamSession(id: "s", after: 0, onConnected: {}, onEvent: { _ in })
            fail("a stream for a missing session must fail")
        } catch AgenthailAPIError.request(404, let message) {
            check(message == "No such session.", "a stream 404 keeps the daemon's message: \(message)")
        } catch {
            fail("a stream for a missing session is a plain request error, not \(error)")
        }
        OlderHostStub.status = 409
        OlderHostStub.body = #"{"error":{"code":"stream_gap","message":"gap"}}"#
        do {
            try await api.streamSession(id: "s", after: 0, onConnected: {}, onEvent: { _ in })
            fail("a stream gap must fail")
        } catch AgenthailAPIError.streamGap {
        } catch {
            fail("a stream gap stays a stream gap, not \(error)")
        }
        print("older host: ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition { fail(message) }
    }

    static func fail(_ message: String) -> Never {
        print("FAIL: \(message)")
        exit(1)
    }
}
