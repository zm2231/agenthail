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
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        do {
            _ = try await api.history()
            fail("a route the host does not have must fail")
        } catch AgenthailAPIError.olderHost {
            check(AgenthailAPIError.olderHost.localizedDescription == "This computer runs an older Agenthail. Update it to use this.", "the older-host message tells the person to update that computer")
        } catch {
            fail("a route the host does not have says the host is older, not \(error)")
        }
        OlderHostStub.body = #"{"error":{"code":"not_found","message":"No such session."}}"#
        do {
            _ = try await api.history()
            fail("a missing resource must fail")
        } catch AgenthailAPIError.request(404, let message) {
            check(message == "No such session.", "a 404 for a missing resource keeps its own message: \(message)")
        } catch {
            fail("a missing resource is a plain request error, not \(error)")
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
